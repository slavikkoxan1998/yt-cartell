package montage

// Pictures: scene frames (or a still of the clip) from the shared image
// cache at the size they are drawn, the A/B pair the galleries preview
// with, and warm synthetic pictures when a project has no frames yet.

import (
	"context"
	"image"
	"image/color"
	"math"
	"sync"
	"time"

	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// sceneImage returns a scene's picture decoded so its longest side is about
// px: the frame, else a still of the clip (ffmpeg, cached).
func sceneImage(c *core.Ctx, pv *core.ProjectView, sc *studio.Scene, px int) (paint.ImageOp, bool) {
	if c.Img == nil || sc == nil {
		return paint.ImageOp{}, false
	}
	if sc.Frame.Path != "" {
		img, _, ok := c.Img.Get(pv.Abs(sc.Frame.Path), sc.Frame.UpdatedAt, gui.ImgBucket(px))
		return img, ok
	}
	if sc.Video.Path != "" && sc.Video.Status == studio.StatusDone {
		return videoStill(c, pv.Abs(sc.Video.Path), sc.Video.UpdatedAt, 0.5, px)
	}
	return paint.ImageOp{}, false
}

// videoStill is a frame of a video at sec (ffmpeg, off the UI goroutine, cached).
func videoStill(c *core.Ctx, abs string, ver int64, sec float64, px int) (paint.ImageOp, bool) {
	maxPx := gui.ImgBucket(px)
	ff := c.E.FF()
	img, _, ok := c.Img.Load(abs+"#still", ver, maxPx, func() (image.Image, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return ff.Thumbnail(ctx, abs, sec, maxPx)
	})
	return img, ok
}

// tinyImage is a scene's picture shrunk to a few pixels (pixelize previews).
func tinyImage(c *core.Ctx, pv *core.ProjectView, sc *studio.Scene) (paint.ImageOp, bool) {
	if c.Img == nil || sc == nil || sc.Frame.Path == "" {
		return paint.ImageOp{}, false
	}
	img, _, ok := c.Img.Get(pv.Abs(sc.Frame.Path), sc.Frame.UpdatedAt, 18)
	return img, ok
}

// pair is the A → B pair of pictures the transition previews use: the
// selected scene and the one after it (synthetic pictures without frames).
type pair struct {
	a, b       paint.ImageOp
	tinyA      paint.ImageOp
	tinyB      paint.ImageOp
	sceneA     *studio.Scene
	real       bool
	aID, bID   string
	aVer, bVer int64
}

func (s *proj) pair(c *core.Ctx, pv *core.ProjectView, px int) pair {
	f := &s.film
	var ids []string
	sel := c.UI(pv.ID).Scene
	if k, ok := f.pos[sel]; ok {
		ids = append(ids, f.items[k].SceneID)
		if k+1 < len(f.items) {
			ids = append(ids, f.items[k+1].SceneID)
		} else if k > 0 {
			ids = append(ids, f.items[k-1].SceneID)
		}
	} else {
		for k := 0; k < len(f.items) && k < 2; k++ {
			ids = append(ids, f.items[k].SceneID)
		}
	}
	syn := synthetic()
	out := pair{a: syn.a, b: syn.b, tinyA: syn.tinyA, tinyB: syn.tinyB}
	if len(ids) > 0 {
		sa := sceneOf(pv, ids[0])
		if img, ok := sceneImage(c, pv, sa, px); ok {
			out.a, out.real, out.sceneA = img, true, sa
			if t, ok := tinyImage(c, pv, sa); ok {
				out.tinyA = t
			}
		}
	}
	if len(ids) > 1 {
		sb := sceneOf(pv, ids[1])
		if img, ok := sceneImage(c, pv, sb, px); ok {
			out.b = img
			if t, ok := tinyImage(c, pv, sb); ok {
				out.tinyB = t
			}
		}
	} else if out.real {
		out.b = syn.b
	}
	return out
}

// ---- synthetic pictures ------------------------------------------------------------------------

type synPair struct{ a, b, tinyA, tinyB paint.ImageOp }

var (
	synOnce sync.Once
	syn     synPair
)

// synthetic returns two warm illustrated landscapes (sunset and meadow).
func synthetic() synPair {
	synOnce.Do(func() {
		a := landscape(320, 180, 0)
		b := landscape(320, 180, 1)
		syn = synPair{
			a: paint.NewImageOp(a), b: paint.NewImageOp(b),
			tinyA: paint.NewImageOp(landscape(18, 10, 0)), tinyB: paint.NewImageOp(landscape(18, 10, 1)),
		}
	})
	return syn
}

// landscape paints a soft gradient sky, a sun and layered hills.
func landscape(w, h, seed int) *image.RGBA {
	type pal struct{ top, bot, sun, h1, h2, h3 color.NRGBA }
	pals := []pal{
		{hex(0xFFB38A), hex(0xFFE7C7), hex(0xFFF6E0), hex(0xE58A5C), hex(0xC0613F), hex(0x7E3B2A)},
		{hex(0x8EC5E8), hex(0xE3F1F7), hex(0xFFFBEA), hex(0x7BB38C), hex(0x4F8A69), hex(0x2F5E4B)},
		{hex(0x3B2A5C), hex(0xC56B7A), hex(0xFFD6A0), hex(0x6A3F66), hex(0x42294A), hex(0x21162B)},
	}
	p := pals[seed%len(pals)]
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sunX := float64(w) * (0.3 + 0.4*float64(seed%2))
	sunY := float64(h) * 0.38
	sunR := float64(h) * 0.12
	hill := func(x, amp, base, freq, ph float64) float64 {
		return base + amp*math.Sin(x*freq+ph) + amp*0.4*math.Sin(x*freq*2.3+ph*1.7)
	}
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		sky := gui.LerpColor(p.top, p.bot, float32(t))
		for x := 0; x < w; x++ {
			c := sky
			dx, dy := float64(x)-sunX, float64(y)-sunY
			if d := math.Hypot(dx, dy); d < sunR {
				c = p.sun
			} else if d < sunR*2.2 {
				c = gui.LerpColor(p.sun, c, float32((d-sunR)/(sunR*1.2)))
			}
			fx := float64(x) / float64(w)
			ph := float64(seed)
			if float64(y) > hill(fx, float64(h)*0.05, float64(h)*0.58, 5, ph) {
				c = p.h1
			}
			if float64(y) > hill(fx, float64(h)*0.06, float64(h)*0.7, 3.3, ph+1) {
				c = p.h2
			}
			if float64(y) > hill(fx, float64(h)*0.04, float64(h)*0.84, 7, ph+2) {
				c = p.h3
			}
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	return img
}

func hex(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}
