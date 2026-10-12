package core

import (
	"os"
	"path/filepath"
	"sync"

	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// HeadlessHost is a Host for tests and headless screenshots: navigation and
// requests are recorded, Background work is tracked, Post runs on Flush.
type HeadlessHost struct {
	mu      sync.Mutex
	route   Route
	history []Route
	posted  []func()
	bg      sync.WaitGroup

	// Recorded requests (tests assert on them).
	Sheets   []Sheet
	Confirms []Confirm
	Queue    int // OpenQueue calls
	Tours    int // StartTour calls
}

func (h *HeadlessHost) Navigate(r Route) {
	h.mu.Lock()
	h.history = append(h.history, h.route)
	h.route = r
	h.mu.Unlock()
}

func (h *HeadlessHost) Current() Route {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.route
}

func (h *HeadlessHost) Back() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.history); n > 0 {
		h.route, h.history = h.history[n-1], h.history[:n-1]
	} else {
		h.route = Route{}
	}
}

func (h *HeadlessHost) Post(fn func()) {
	h.mu.Lock()
	h.posted = append(h.posted, fn)
	h.mu.Unlock()
}

func (h *HeadlessHost) Background(fn func()) {
	h.bg.Add(1)
	go func() {
		defer h.bg.Done()
		fn()
	}()
}

func (h *HeadlessHost) Invalidate() {}

func (h *HeadlessHost) OpenSheet(s Sheet) {
	h.mu.Lock()
	h.Sheets = append(h.Sheets, s)
	h.mu.Unlock()
}

func (h *HeadlessHost) Confirm(c Confirm) {
	h.mu.Lock()
	h.Confirms = append(h.Confirms, c)
	h.mu.Unlock()
}

func (h *HeadlessHost) OpenQueue() { h.mu.Lock(); h.Queue++; h.mu.Unlock() }
func (h *HeadlessHost) StartTour() { h.mu.Lock(); h.Tours++; h.mu.Unlock() }

// Flush waits for Background work, then runs everything posted (repeatedly,
// since posted functions may post more).
func (h *HeadlessHost) Flush() {
	for i := 0; i < 8; i++ {
		h.bg.Wait()
		h.mu.Lock()
		fns := h.posted
		h.posted = nil
		h.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			fn()
		}
	}
}

// NewHeadless builds an offline Ctx on e (no network, no OS dialogs, no
// players; images decode synchronously) for screenshots and tests.
func NewHeadless(e *studio.Engine) (*Ctx, *HeadlessHost) {
	th := gui.MustTheme()
	img := gui.NewImageCache(nil)
	img.SetSync(true)
	h := &HeadlessHost{}
	return New(h, e, th, img, true), h
}

// Frame wraps draw the way the shell does for one frame: flush posted work,
// Refresh, th.Root (overlays, toasts), EndFrame.
func Frame(c *Ctx, h *HeadlessHost, draw func(gtx C) D) func(gtx C) D {
	return func(gtx C) D {
		h.Flush()
		c.Refresh()
		d := c.Th.Root(gtx, draw)
		c.EndFrame()
		return d
	}
}

// Shot renders draw at w×h px (scale px per dp) after animations settle and
// writes a PNG to path.
func Shot(c *Ctx, h *HeadlessHost, w, hgt int, scale float32, path string, draw func(gtx C) D, opts ...gui.SnapOption) error {
	if len(opts) == 0 {
		opts = []gui.SnapOption{gui.Frames(4), gui.Settle(90)}
	}
	img, err := gui.Snapshot(w, hgt, scale, Frame(c, h, draw), opts...)
	if err != nil {
		return err
	}
	return gui.SavePNG(img, path)
}

// DemoEngine creates an engine on dataDir (a temp folder you own) with
// in-memory settings that never persist, seeded with the projects of the web
// studio at webRoot (the repo folder holding data/studio and uploads/) when
// it exists. Every secret is replaced by an obvious fake so screenshots never
// show real keys. It never touches the user's real data or settings.
func DemoEngine(dataDir, webRoot string) (*studio.Engine, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	cfg := config.Default()
	cfg.DataDir = dataDir
	store := config.NewStoreWith(cfg)
	e, err := studio.NewEngine(store)
	if e == nil {
		return nil, err
	}
	if webRoot != "" {
		if st, serr := os.Stat(filepath.Join(webRoot, "data", "studio")); serr == nil && st.IsDir() {
			_, _ = e.ImportFromWeb(webRoot)
		}
	}
	_ = store.Update(func(c *config.Config) {
		c.DataDir = dataDir
		c.Text.APIKey = "demo-text-key-0000"
		c.Glabs.APIKey = "demo-glabs-key-0000"
		c.TTS.APIKey = "demo-tts-key-0000"
		c.STT.APIKey = ""
		c.Telegram.BotToken = ""
		c.Telegram.ChatID = ""
		c.Google.Keys = nil
	})
	return e, nil
}
