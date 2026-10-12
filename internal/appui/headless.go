package appui

import (
	"image"
	"time"

	"gioui.org/io/input"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/home"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Headless builds an offline shell on e for screenshots and tests: the whole
// window (rail, page, overlays) exactly as Run draws it, but no network
// service is called, no OS dialog or player opens, pictures decode
// synchronously and the first-run tour does not start by itself. Close it
// when done.
//
//	s := appui.Headless(e)
//	defer s.Close()
//	s.SetDark(true)
//	s.Navigate(core.Route{Page: core.PageQueue})
//	img, err := s.Snapshot(1440, 900)
func Headless(e *studio.Engine) *Shell {
	return newShell(e, gui.MustTheme(), nil, true)
}

// Ctx is the screens' context of this shell.
func (s *Shell) Ctx() *core.Ctx { return s.c }

// Theme is the window's theme.
func (s *Shell) Theme() *gui.Theme { return s.th }

// Home is the state of Головна and Проєкти (screenshots open a card menu).
func (s *Shell) Home() *home.Page { return s.home }

// SetDark switches the palette at once (no crossfade).
func (s *Shell) SetDark(dark bool) { s.th.SetPaletteNow(dark) }

// ShowTour pins the tour at step (0-based) without animation.
func (s *Shell) ShowTour(step int) {
	s.Navigate(core.Route{Page: core.PageHome})
	s.tour.Start()
	s.tour.SetStep(step)
	s.tour.Force = true
}

// Flush waits (bounded) for background work and picture decodes, then runs
// what they posted.
func (s *Shell) Flush() {
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		s.c.Img.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
	s.runPosted()
}

// Snapshot renders the window at w×h px (1 px per dp) after its animations
// settle. opts replace the default frame options.
func (s *Shell) Snapshot(w, h int, opts ...gui.SnapOption) (*image.RGBA, error) {
	if len(opts) == 0 {
		opts = []gui.SnapOption{gui.Frames(4), gui.Settle(90)}
	}
	opts = append(opts, gui.BeforeFrame(func(int, *input.Router) { s.Flush() }))
	return gui.Snapshot(w, h, 1, s.Layout, opts...)
}
