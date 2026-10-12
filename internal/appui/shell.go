// Package appui is the shell of the new "warm app" UI (GUI.md): the window
// and its event loop, the left rail (a bottom bar on phones), the routes with
// their page transitions, the global overlays (queue drawer, confirm dialog,
// sheets, first-run tour, toasts) and the headless renderer cmd/guishots
// uses. The screens live in the sub-packages (home, queue, project, settings,
// dialogs …) and talk to the shell through core.Ctx / core.Host.
//
// main.go calls Run (Fatal on a start-up error, LogPath for the crash
// message); cmd/guishots calls Headless.
package appui

import (
	"fmt"
	"image"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/dialogs"
	"ytcartell/internal/appui/home"
	"ytcartell/internal/appui/project"
	"ytcartell/internal/appui/queue"
	"ytcartell/internal/appui/settings"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type (
	// C and D shorten layout signatures.
	C = layout.Context
	D = layout.Dimensions
)

// Rail geometry (GUI.md "Layout").
const (
	railW     = 92  // dp
	narrowMax = 900 // dp: narrower windows get the bottom bar
)

// Shell is one window of the studio: it implements core.Host for the
// screens and draws the whole window every frame (Layout).
type Shell struct {
	e       *studio.Engine
	th      *gui.Theme
	c       *core.Ctx
	offline bool
	focused bool // the window has the keyboard focus (run.go; WindowFocused)

	mu         sync.Mutex // guards posted
	posted     []func()
	invalidate func()
	bg         sync.WaitGroup
	closed     atomic.Bool

	route   core.Route
	history []core.Route
	anim    gui.Route

	feed      *queue.Feed
	queue     *queue.View
	home      *home.Page
	settings  *settings.Page
	workspace *project.Workspace
	sheets    *dialogs.Sheets

	drawer  gui.Drawer
	confirm confirmState
	tour    gui.Tour
	tourReq bool // start the tour on the next frame (first run)

	rail    railState
	backTag int

	// window close protection (Run)
	closeOK bool
	closeFn func()

	// updates (update.go): the «Оновлення» sheet and its state; relaunch
	// starts the installed build once Run has closed the window and
	// flushed the engine.
	upd       *updater
	relaunch  func() error
	narrow    bool // this frame has the bottom bar
	updInRail bool // the rail shows «Оновлення» (else Settings carries the dot)
}

// routeKey is what a page transition keys on: switching a project's step or
// a settings section stays on the page (their own tabs animate).
type routeKey struct {
	page core.Page
	id   string
}

// newShell wires a shell to the engine. invalidate (may be nil) asks the
// window for a frame from any goroutine; offline shells never call network
// services or open OS dialogs (headless screenshots, tests).
func newShell(e *studio.Engine, th *gui.Theme, invalidate func(), offline bool) *Shell {
	s := &Shell{e: e, th: th, offline: offline, invalidate: invalidate}
	img := gui.NewImageCache(s.Invalidate)
	if offline {
		img.SetSync(true)
	}
	s.c = core.New(s, e, th, img, offline)
	s.feed = queue.NewFeed(e, s.Invalidate)
	s.feed.Snapshot() // starts the engine's single-asset tracker right away
	s.queue = queue.New(s.feed)
	s.home = home.New()
	s.home.Queue = s.feed.Snapshot
	s.settings = settings.New()
	s.workspace = project.NewWorkspace()
	s.sheets = dialogs.New()
	s.tour.Steps = tourSteps()
	s.upd = newUpdater(e.Config().Get())
	s.route = core.Route{Page: core.PageHome}
	s.applyUI(e.Config().Get().UI, true)
	e.Config().Subscribe(func(cfg config.Config) {
		if s.closed.Load() {
			return
		}
		ui := cfg.UI
		s.Post(func() { s.applyUI(ui, false) })
	})
	return s
}

// applyUI applies the appearance settings: theme (light / dark / system)
// and "Менше анімацій". The first call sets the palette without a crossfade.
func (s *Shell) applyUI(ui config.UI, first bool) {
	mode := gui.ModeLight
	switch ui.Theme {
	case "dark":
		mode = gui.ModeDark
	case "system":
		mode = gui.ModeSystem
	}
	if first {
		s.th.SetPaletteNow(mode == gui.ModeDark || (mode == gui.ModeSystem && gui.SystemDark()))
	}
	if mode != s.th.Mode() || mode == gui.ModeSystem {
		s.th.SetMode(mode)
	}
	gui.SetReducedMotion(ui.ReducedMotion)
}

// Close unsubscribes from the engine and stops every player.
func (s *Shell) Close() {
	if s.closed.Swap(true) {
		return
	}
	// Text still being typed in the workspace goes into the project before
	// the engine flushes its saves.
	s.workspace.FlushEdits()
	s.c.Close()
	s.feed.Close()
}

// ---- core.Host -----------------------------------------------------------------------------------

// Navigate changes the route (animated). Within one page (a project's step,
// a settings section) the route is replaced instead of stacked, so Back
// leaves the page. UI goroutine.
func (s *Shell) Navigate(r core.Route) {
	r = normalize(r)
	s.drawer.Close()
	s.upd.drawer.Close()
	cur := s.route
	if r == cur {
		return
	}
	if r.Page == cur.Page && r.ProjectID == cur.ProjectID {
		if r.Step != cur.Step {
			s.c.StopMedia() // another step: the sound of this one stops
		}
		s.route = r
		s.Invalidate()
		return
	}
	s.history = append(s.history, cur)
	if n := len(s.history); n > 40 {
		s.history = append([]core.Route(nil), s.history[n-40:]...)
	}
	s.setRoute(r)
}

func (s *Shell) setRoute(r core.Route) {
	s.route = r
	s.c.StopMedia()
	s.Invalidate()
}

// Current is the route being shown.
func (s *Shell) Current() core.Route { return s.route }

// WindowFocused reports whether the window is the active one (an optional
// Host method: dictation stops when the owner switches away). UI goroutine.
func (s *Shell) WindowFocused() bool { return s.focused }

// Back returns to the previous route (Home when there is none), skipping
// projects deleted since.
func (s *Shell) Back() {
	s.drawer.Close()
	for n := len(s.history); n > 0; n = len(s.history) {
		r := s.history[n-1]
		s.history = s.history[:n-1]
		if r.Page == core.PageProject && s.c.Project(r.ProjectID) == nil {
			continue
		}
		if r != s.route {
			s.setRoute(r)
			return
		}
	}
	if s.route.Page != core.PageHome {
		s.setRoute(core.Route{Page: core.PageHome})
	}
}

// Post runs fn on the UI goroutine at the start of the next frame. Any goroutine.
func (s *Shell) Post(fn func()) {
	s.mu.Lock()
	s.posted = append(s.posted, fn)
	s.mu.Unlock()
	s.Invalidate()
}

// Background runs fn on a new goroutine and redraws when it ends. A panic
// is logged and reported instead of killing the app. Any goroutine.
func (s *Shell) Background(fn func()) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer s.Invalidate()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("appui: background panic: %v\n%s", r, debug.Stack())
				s.th.Toast(gui.ToneError, "Внутрішня помилка — подробиці в журналі.")
			}
		}()
		fn()
	}()
}

// Invalidate requests a new frame. Any goroutine.
func (s *Shell) Invalidate() {
	if s.invalidate != nil {
		s.invalidate()
	}
}

// OpenSheet opens a global sheet (package dialogs) above everything.
func (s *Shell) OpenSheet(sh core.Sheet) {
	s.drawer.Close()
	s.upd.drawer.Close()
	s.sheets.Open(s.c, sh)
	s.Invalidate()
}

// Confirm opens the shared confirmation dialog.
func (s *Shell) Confirm(c core.Confirm) {
	s.confirm.open(c)
	s.Invalidate()
}

// OpenQueue slides the queue drawer in.
func (s *Shell) OpenQueue() {
	s.upd.drawer.Close()
	s.drawer.Open()
	s.Invalidate()
}

// StartTour replays the first-run tour on Головна.
func (s *Shell) StartTour() {
	s.Navigate(core.Route{Page: core.PageHome})
	s.tour.Force = false
	s.tour.Start()
	s.Invalidate()
}

// normalize fills defaults: a project route without an id goes Home, steps
// are 0 (the project's own) or 1–5.
func normalize(r core.Route) core.Route {
	if r.Page > core.PageProject {
		r.Page = core.PageHome
	}
	if r.Page == core.PageProject {
		if strings.TrimSpace(r.ProjectID) == "" {
			return core.Route{Page: core.PageHome}
		}
		r.Step = max(0, min(5, r.Step))
	} else {
		r.ProjectID, r.Step, r.Scene = "", 0, ""
	}
	if r.Page != core.PageSettings {
		r.Section = ""
	}
	return r
}

// ---- frame ---------------------------------------------------------------------------------------

func (s *Shell) runPosted() {
	for i := 0; i < 8; i++ { // posted funcs may post again; bounded per frame
		s.mu.Lock()
		fns := s.posted
		s.posted = nil
		s.mu.Unlock()
		if len(fns) == 0 {
			return
		}
		for _, fn := range fns {
			fn()
		}
	}
}

// Layout draws one frame of the whole window: posted work, the engine's
// changes (Refresh), the rail and the page inside th.Root (with every
// overlay), the global shortcuts, then EndFrame.
func (s *Shell) Layout(gtx C) D {
	s.runPosted()
	s.c.Refresh()
	if s.tourReq {
		s.tourReq = false
		s.StartTour()
	}
	s.checkRoute()
	d := s.th.Root(gtx, s.frame)
	s.shortcuts(gtx)
	s.c.EndFrame()
	return d
}

// checkRoute leaves a project that no longer exists (deleted meanwhile).
func (s *Shell) checkRoute() {
	r := s.route
	if r.Page != core.PageProject || s.c.Project(r.ProjectID) != nil {
		return
	}
	s.c.Toast(gui.ToneInfo, "Проєкт не знайдено — можливо, його видалили.")
	kept := s.history[:0]
	for _, h := range s.history {
		if h.ProjectID != r.ProjectID {
			kept = append(kept, h)
		}
	}
	s.history = kept
	s.setRoute(core.Route{Page: core.PageHome})
}

func (s *Shell) frame(gtx C) D {
	th := s.th
	W, H := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	narrow := W < gtx.Dp(narrowMax)
	s.narrow = narrow
	s.railActions(gtx)
	var content image.Rectangle
	var bar op.CallOp
	barH := 0
	if narrow {
		bg := gtx
		bg.Constraints = layout.Constraints{Min: image.Pt(W, 0), Max: image.Pt(W, H)}
		var bd D
		bd, bar = record(bg, s.layoutBottomBar)
		barH = bd.Size.Y
		content = image.Rect(0, 0, W, max(0, H-barH))
	} else {
		rw := gtx.Dp(railW)
		rg := gtx
		rg.Constraints = layout.Exact(image.Pt(rw, H))
		s.layoutRail(rg)
		content = image.Rect(rw, 0, W, H)
	}
	func() {
		cg := gtx
		cg.Constraints = layout.Exact(content.Size())
		defer op.Offset(content.Min).Push(gtx.Ops).Pop()
		defer clip.Rect(image.Rectangle{Max: content.Size()}).Push(gtx.Ops).Pop()
		r := s.route
		s.anim.Layout(cg, routeKey{r.Page, r.ProjectID}, func(gtx C) D { return s.page(gtx, r) })
	}()
	if narrow {
		o := op.Offset(image.Pt(0, H-barH)).Push(gtx.Ops)
		bar.Add(gtx.Ops)
		o.Pop()
	}

	// Overlays: each queues a layer above the page.
	snap := s.feed.Snapshot()
	s.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Черга",
		Subtitle: queue.Subtitle(snap),
		Width:    460,
		Body:     func(gtx C) D { return s.queue.DrawerBody(gtx, s.c) },
		Footer:   func(gtx C) D { return s.queue.DrawerFooter(gtx, s.c) },
	})
	s.sheets.Layout(gtx, s.c)
	s.layoutUpdate(gtx)
	s.confirm.layout(gtx, th)
	s.tour.Layout(gtx, th)
	if s.tour.Finished() {
		s.tourDone()
	}
	s.backButton(gtx)
	return D{Size: image.Pt(W, H)}
}

// page draws the screen of route r.
func (s *Shell) page(gtx C, r core.Route) D {
	switch r.Page {
	case core.PageProjects:
		return s.home.Projects(gtx, s.c)
	case core.PageQueue:
		return s.queue.Page(gtx, s.c)
	case core.PageSettings:
		return s.settings.Layout(gtx, s.c, r.Section)
	case core.PageProject:
		return s.workspace.Layout(gtx, s.c, r.ProjectID, r.Step)
	}
	return s.home.Home(gtx, s.c)
}

// overlayOpen reports a modal layer the keyboard belongs to.
func (s *Shell) overlayOpen() bool {
	return s.drawer.IsOpen() || s.confirm.dlg.IsOpen() || s.tour.Active() || s.sheets.IsOpen() || s.upd.drawer.IsOpen()
}

// shortcuts handles the window's keys after every widget had its chance:
// Esc (and the Back key) leaves a focused field first, then goes back;
// Ctrl+N opens "Новий проєкт".
func (s *Shell) shortcuts(gtx C) {
	for {
		ev, ok := gtx.Event(
			key.Filter{Name: key.NameEscape},
			key.Filter{Name: key.NameBack},
			key.Filter{Name: "N", Required: key.ModShortcut},
		)
		if !ok {
			return
		}
		ke, ok := ev.(key.Event)
		if !ok || ke.State != key.Press {
			continue
		}
		switch ke.Name {
		case key.NameEscape, key.NameBack:
			if s.overlayOpen() {
				continue
			}
			if !gtx.Focused(nil) {
				gtx.Execute(key.FocusCmd{Tag: nil})
				continue
			}
			s.Back()
		case "N":
			if s.tour.Active() {
				continue
			}
			s.OpenSheet(core.Sheet{Kind: core.SheetNewProject})
		}
		gtx.Execute(op.InvalidateCmd{})
	}
}

// backButton makes the mouse's Back button go back: a pass-through area
// over the page (deferred, so it sits above the content and below the
// overlays).
func (s *Shell) backButton(gtx C) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &s.backTag, Kinds: pointer.Press})
		if !ok {
			break
		}
		if pe, ok := ev.(pointer.Event); ok && pe.Buttons.Contain(pointer.ButtonQuaternary) && !s.overlayOpen() {
			s.Back()
		}
	}
	m := op.Record(gtx.Ops)
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect(image.Rectangle{Max: gtx.Constraints.Max}).Push(gtx.Ops)
	event.Op(gtx.Ops, &s.backTag)
	area.Pop()
	pass.Pop()
	op.Defer(gtx.Ops, m.Stop())
}

// tourDone remembers that the first-run tour was finished or skipped.
func (s *Shell) tourDone() {
	e := s.e
	s.Background(func() {
		if err := e.Config().Update(func(cfg *config.Config) { cfg.UI.TourDone = true }); err != nil {
			s.c.Error(fmt.Errorf("не вдалося зберегти налаштування: %w", err))
		}
	})
}

// ---- confirm dialog ------------------------------------------------------------------------------

// confirmState is the shared confirmation dialog (core.Confirm).
type confirmState struct {
	dlg        gui.Dialog
	cur        core.Confirm
	ok, cancel gui.Clickable
}

func (cs *confirmState) open(c core.Confirm) {
	cs.cur = c
	cs.dlg.Open()
}

func (cs *confirmState) layout(gtx C, th *gui.Theme) {
	if cs.cancel.Clicked(gtx) {
		cs.dlg.Close()
	}
	if cs.ok.Clicked(gtx) && cs.dlg.IsOpen() {
		cs.dlg.Close()
		if fn := cs.cur.OnOK; fn != nil {
			cs.cur.OnOK = nil
			fn()
		}
	}
	cs.dlg.Dismissed()
	cur := cs.cur
	okText, cancelText := cur.OK, cur.Cancel
	if okText == "" {
		okText = "Так"
	}
	if cancelText == "" {
		cancelText = "Скасувати"
	}
	kind, tone := gui.Primary, gui.ToneAction
	var okIcon *gui.Icon
	if cur.Danger {
		kind, tone = gui.Danger, gui.ToneError
		for _, verb := range []string{"Видалити", "Прибрати", "Очистити"} {
			if strings.HasPrefix(okText, verb) {
				okIcon = gui.IconTrash2
			}
		}
	}
	cs.dlg.Layout(gtx, th, gui.DialogStyle{
		Title: cur.Title,
		Text:  cur.Text,
		Icon:  cur.Icon,
		Tone:  tone,
		Actions: []layout.Widget{
			func(gtx C) D { return gui.Button{Kind: gui.Tinted, Text: cancelText}.Layout(gtx, th, &cs.cancel) },
			func(gtx C) D { return gui.Button{Kind: kind, Icon: okIcon, Text: okText}.Layout(gtx, th, &cs.ok) },
		},
	})
}

// record lays w out into a macro and returns its size and call.
func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}
