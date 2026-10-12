package gui

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // decoders
	_ "image/png"
	"io"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"gioui.org/op/paint"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// DefaultMaxPx is the longest side images are downscaled to when maxPx is 0.
const DefaultMaxPx = 960

const (
	imgBudget    = 160 << 20        // decoded RGBA bytes kept (least recently used go first)
	imgCap       = 400              // entries kept (failed ones included)
	imgKeepFor   = time.Second      // entries drawn this recently are never evicted
	imgWorkers   = 3                // concurrent decodes
	imgRetryWait = 10 * time.Second // failed loads retry after this
	// A queued load nobody asked for during the last imgStaleTicks requests
	// and imgStaleAfter is dropped before it starts (the item scrolled away).
	imgStaleTicks = 300
	imgStaleAfter = time.Second
	imgMaxPixels  = 50_000_000 // larger pictures are refused (≈200 MB decoded)
	imgBigPixels  = 16_000_000 // decodes above this run one at a time
)

// ImgBucket rounds the longest side a picture is drawn at (px) up to the
// decode size, so nearby sizes share one cache entry: 240, 480, 720 or
// DefaultMaxPx (also for px ≤ 0).
func ImgBucket(px int) int {
	switch {
	case px <= 0:
		return DefaultMaxPx
	case px <= 240:
		return 240
	case px <= 480:
		return 480
	case px <= 720:
		return 720
	}
	return DefaultMaxPx
}

// ImageCache decodes images off the UI goroutine, downsizes them and keeps
// the GPU-ready ImageOps within a byte budget. Entries are keyed by (key,
// version, maxPx); pass Asset.UpdatedAt as the version so a regenerated file
// refreshes. While a new version or size decodes, the closest ready picture
// of the same key keeps showing (no flicker).
type ImageCache struct {
	invalidate func()
	now        func() time.Time // the clock (tests replace it)
	budget     int              // imgBudget (tests shrink it)
	cap        int              // imgCap

	mu      sync.Mutex
	m       map[imgKey]*imgEntry
	bytes   int // decoded bytes of the ready entries
	tick    uint64
	sync    bool
	sem     chan struct{}
	big     sync.Mutex // one very large decode at a time
	pending sync.WaitGroup
}

type imgKey struct {
	key   string
	ver   int64
	maxPx int
}

type imgEntry struct {
	op       paint.ImageOp
	img      *image.RGBA
	size     image.Point
	bytes    int
	ready    bool
	failed   bool
	failedAt time.Time
	used     uint64
	usedAt   time.Time
}

// NewImageCache returns a cache that calls invalidate (e.g. window.Invalidate;
// may be nil) from a background goroutine whenever an image becomes ready.
func NewImageCache(invalidate func()) *ImageCache {
	return &ImageCache{invalidate: invalidate, now: time.Now, budget: imgBudget, cap: imgCap,
		m: map[imgKey]*imgEntry{}, sem: make(chan struct{}, imgWorkers)}
}

// SetSync makes loads decode inline on the calling goroutine (headless
// screenshots, tests) so the first frame already has the pixels.
func (c *ImageCache) SetSync(v bool) {
	c.mu.Lock()
	c.sync = v
	c.mu.Unlock()
}

// Wait blocks until every in-flight decode has finished.
func (c *ImageCache) Wait() { c.pending.Wait() }

// Get returns the image file at path (jpeg/png/webp) scaled so its longest
// side is ≤ maxPx (0 = DefaultMaxPx). ok is false until it has been decoded
// (or if it cannot be); size is the size of the returned ImageOp.
func (c *ImageCache) Get(path string, ver int64, maxPx int) (paint.ImageOp, image.Point, bool) {
	if path == "" {
		return paint.ImageOp{}, image.Point{}, false
	}
	return c.Load(path, ver, maxPx, func() (image.Image, error) { return c.decodeFile(path) })
}

// FromBytes decodes embedded image bytes (style previews) under key, at
// their natural size.
func (c *ImageCache) FromBytes(key string, data []byte) (paint.ImageOp, image.Point, bool) {
	if len(data) == 0 {
		return paint.ImageOp{}, image.Point{}, false
	}
	return c.FromBytesFunc(key, func() []byte { return data })
}

// FromBytesFunc is FromBytes for bytes that are costly to produce (embed.FS
// copies them on every read): data is only called on a cache miss.
func (c *ImageCache) FromBytesFunc(key string, data func() []byte) (paint.ImageOp, image.Point, bool) {
	return c.Load("bytes:"+key, 0, -1, func() (image.Image, error) {
		b := data()
		if len(b) == 0 {
			return nil, fmt.Errorf("немає даних зображення «%s»", key)
		}
		img, _, err := image.Decode(bytes.NewReader(b))
		return img, err
	})
}

// Load is the general form: load produces the image (a decoded file, a video
// thumbnail from ffmpeg, …) and is called at most once per (key, ver, maxPx)
// on a background goroutine. maxPx < 0 keeps the natural size.
func (c *ImageCache) Load(key string, ver int64, maxPx int, load func() (image.Image, error)) (paint.ImageOp, image.Point, bool) {
	if maxPx == 0 {
		maxPx = DefaultMaxPx
	}
	k := imgKey{key, ver, maxPx}
	now := c.now()
	c.mu.Lock()
	c.tick++
	e := c.m[k]
	if e != nil && e.failed && now.Sub(e.failedAt) > imgRetryWait {
		c.dropLocked(k)
		e = nil
	}
	if e != nil {
		e.used, e.usedAt = c.tick, now
		if e.ready {
			op, sz := e.op, e.size
			c.mu.Unlock()
			return op, sz, true
		}
		if !e.failed {
			op, sz, ok := c.previousLocked(k)
			c.mu.Unlock()
			return op, sz, ok
		}
		c.mu.Unlock()
		return paint.ImageOp{}, image.Point{}, false
	}
	// A larger decode of this version is already here: scale that down
	// instead of reading and decoding the file again.
	if src := c.largerLocked(k); src != nil {
		load = func() (image.Image, error) { return src, nil }
	}
	e = &imgEntry{used: c.tick, usedAt: now}
	c.m[k] = e
	c.evictLocked(now)
	if c.sync {
		c.mu.Unlock()
		c.run(k, e, load)
		c.mu.Lock()
		op, sz, ok := e.op, e.size, e.ready
		c.mu.Unlock()
		return op, sz, ok
	}
	op, sz, ok := c.previousLocked(k)
	c.pending.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.pending.Done()
		c.sem <- struct{}{}
		defer func() { <-c.sem }()
		if !c.wanted(k, e) {
			return
		}
		c.run(k, e, load)
		if c.invalidate != nil {
			c.invalidate()
		}
	}()
	return op, sz, ok
}

// Forget drops every cached version of key (e.g. a deleted file).
func (c *ImageCache) Forget(key string) {
	c.mu.Lock()
	for k := range c.m {
		if k.key == key {
			c.dropLocked(k)
		}
	}
	c.mu.Unlock()
}

// wanted reports whether a queued load should still run: false when its
// entry was evicted meanwhile or nobody has asked for it lately.
func (c *ImageCache) wanted(k imgKey, e *imgEntry) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m[k] != e {
		return false
	}
	if c.tick-e.used > imgStaleTicks && c.now().Sub(e.usedAt) > imgStaleAfter {
		c.dropLocked(k)
		return false
	}
	return true
}

func (c *ImageCache) run(k imgKey, e *imgEntry, load func() (image.Image, error)) {
	var rgba *image.RGBA
	func() {
		// A decoder bug on a malformed file must not take the app down.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("gui: image %q: decode panic: %v\n%s", k.key, r, debug.Stack())
				rgba = nil
			}
		}()
		img, err := load()
		if err == nil && img != nil {
			rgba = scaleImage(img, k.maxPx)
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m[k] != e { // forgotten or evicted meanwhile
		return
	}
	if rgba == nil {
		e.failed, e.failedAt = true, c.now()
		return
	}
	e.op = paint.NewImageOp(rgba)
	e.img = rgba
	e.size = rgba.Bounds().Size()
	e.bytes = len(rgba.Pix)
	e.ready = true
	c.bytes += e.bytes
	// The new version replaces older ones of the same key (every size).
	for ok, oe := range c.m {
		if ok.key == k.key && ok.ver < k.ver && oe.ready {
			c.dropLocked(ok)
		}
	}
	c.evictLocked(c.now())
}

// decodeFile decodes an image file, refusing absurdly large pictures before
// allocating their pixels and running very large decodes one at a time.
func (c *ImageCache) decodeFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	px := cfg.Width * cfg.Height
	if cfg.Width <= 0 || cfg.Height <= 0 || px > imgMaxPixels {
		return nil, fmt.Errorf("зображення %d×%d завелике для перегляду", cfg.Width, cfg.Height)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	if px > imgBigPixels {
		c.big.Lock()
		defer c.big.Unlock()
	}
	img, _, err := image.Decode(bufio.NewReader(f))
	return img, err
}

// dropLocked removes entry k and its bytes.
func (c *ImageCache) dropLocked(k imgKey) {
	if e := c.m[k]; e != nil {
		if e.ready {
			c.bytes -= e.bytes
		}
		delete(c.m, k)
	}
}

// largerLocked returns the pixels of a ready entry of the same key and
// version decoded at a larger size (or natural size), if any.
func (c *ImageCache) largerLocked(k imgKey) *image.RGBA {
	if k.maxPx < 0 {
		return nil
	}
	var best *image.RGBA
	for ok, e := range c.m {
		if ok.key != k.key || ok.ver != k.ver || !e.ready || e.img == nil {
			continue
		}
		if ok.maxPx < 0 || ok.maxPx > k.maxPx {
			if best == nil || len(e.img.Pix) < len(best.Pix) {
				best = e.img // the smallest one that is still larger
			}
		}
	}
	return best
}

// previousLocked finds a ready picture of the same key to show while k
// decodes: the same size at another version first, then this version at
// another size, then anything.
func (c *ImageCache) previousLocked(k imgKey) (paint.ImageOp, image.Point, bool) {
	var best *imgEntry
	rank := 0
	for ok, e := range c.m {
		if ok.key != k.key || ok == k || !e.ready {
			continue
		}
		r := 1
		switch {
		case ok.maxPx == k.maxPx:
			r = 3
		case ok.ver == k.ver:
			r = 2
		}
		if r > rank {
			best, rank = e, r
		}
	}
	if best == nil {
		return paint.ImageOp{}, image.Point{}, false
	}
	return best.op, best.size, true
}

// evictLocked removes least-recently-used settled entries while the cache is
// over its byte budget or entry cap. Entries drawn within imgKeepFor (the
// visible set) stay even over budget, so nothing on screen thrashes.
func (c *ImageCache) evictLocked(now time.Time) {
	for c.bytes > c.budget || len(c.m) > c.cap {
		var victim imgKey
		oldest := ^uint64(0)
		for k, e := range c.m {
			if (e.ready || e.failed) && e.used < oldest && now.Sub(e.usedAt) > imgKeepFor {
				victim, oldest = k, e.used
			}
		}
		if oldest == ^uint64(0) {
			return // everything left is in flight or on screen
		}
		c.dropLocked(victim)
	}
}

// scaleImage converts img to RGBA, downsizing (ApproxBiLinear) so the longest
// side is ≤ maxPx (maxPx < 0 = natural size).
func scaleImage(img image.Image, maxPx int) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if maxPx > 0 && (w > maxPx || h > maxPx) {
		if w >= h {
			h = max(1, h*maxPx/w)
			w = maxPx
		} else {
			w = max(1, w*maxPx/h)
			h = maxPx
		}
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		return dst
	}
	if rgba, ok := img.(*image.RGBA); ok && b.Min == (image.Point{}) {
		return rgba
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}
