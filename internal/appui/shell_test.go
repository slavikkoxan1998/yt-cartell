package appui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// webRoot is the web studio folder (demo projects), relative to this package.
const webRoot = "../../.."

// testEngine is a demo engine on a temp folder whose services all point at
// a closed local port: nothing can reach a real (or the user's local) service.
func testEngine(t *testing.T, demo bool) *studio.Engine {
	t.Helper()
	root := ""
	if demo {
		root = webRoot
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), root)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Config().Update(func(c *config.Config) {
		c.Text.BaseURL = "http://127.0.0.1:1/v1"
		c.Glabs.BaseURL = "http://127.0.0.1:1"
		c.TTS.APIKey = ""
		c.Google.Keys = nil
	})
	t.Cleanup(func() { _ = e.Shutdown(2 * time.Second) })
	return e
}

// frames lays the shell out n times on a fixed clock without a GPU.
func frames(s *Shell, w, h, n int) {
	var router input.Router
	var ops op.Ops
	for i := 0; i < n; i++ {
		ops.Reset()
		gtx := layout.Context{
			Ops:         &ops,
			Now:         gui.SnapshotEpoch.Add(time.Duration(i) * 16 * time.Millisecond),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(w, h)),
			Source:      router.Source(),
		}
		s.Layout(gtx)
		router.Frame(&ops)
		s.Flush()
	}
}

func TestNavigateAndBack(t *testing.T) {
	e := testEngine(t, false)
	p, err := e.Create("Тест")
	if err != nil {
		t.Fatal(err)
	}
	s := Headless(e)
	defer s.Close()
	s.Navigate(core.Route{Page: core.PageProjects})
	s.Navigate(core.Route{Page: core.PageProject, ProjectID: p.ID, Step: 2})
	s.Navigate(core.Route{Page: core.PageProject, ProjectID: p.ID, Step: 3}) // same page: replaced
	if got := s.Current(); got.Step != 3 || got.ProjectID != p.ID {
		t.Fatalf("route = %+v", got)
	}
	if len(s.history) != 2 {
		t.Fatalf("history = %+v, want Home, Projects", s.history)
	}
	s.Back()
	if s.Current().Page != core.PageProjects {
		t.Fatalf("back: %+v, want Projects", s.Current())
	}
	s.Back()
	s.Back() // nothing left: stays Home
	if s.Current().Page != core.PageHome {
		t.Fatalf("back: %+v, want Home", s.Current())
	}
	// A route without a project goes Home; settings keep their section only.
	s.Navigate(core.Route{Page: core.PageProject})
	if s.Current().Page != core.PageHome {
		t.Fatalf("empty project route: %+v", s.Current())
	}
	s.Navigate(core.Route{Page: core.PageSettings, Section: "telegram", ProjectID: "x"})
	if r := s.Current(); r.Section != "telegram" || r.ProjectID != "" {
		t.Fatalf("settings route: %+v", r)
	}
}

func TestDeletedProjectLeaves(t *testing.T) {
	e := testEngine(t, false)
	p, err := e.Create("Буде видалено")
	if err != nil {
		t.Fatal(err)
	}
	s := Headless(e)
	defer s.Close()
	s.Navigate(core.Route{Page: core.PageProject, ProjectID: p.ID})
	frames(s, 1200, 800, 2)
	if s.Current().Page != core.PageProject {
		t.Fatalf("route = %+v", s.Current())
	}
	if err := e.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.Current().Page == core.PageProject && time.Now().Before(deadline) {
		frames(s, 1200, 800, 1)
		time.Sleep(20 * time.Millisecond)
	}
	if s.Current().Page != core.PageHome {
		t.Fatalf("deleted project still open: %+v", s.Current())
	}
}

func TestConfirmRunsOnOK(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	ran := 0
	s.Confirm(core.Confirm{Title: "Точно?", OK: "Видалити", Danger: true, OnOK: func() { ran++ }})
	frames(s, 1000, 700, 3)
	if !s.confirm.dlg.IsOpen() {
		t.Fatal("confirm dialog is not open")
	}
	s.confirm.ok.Click()
	frames(s, 1000, 700, 2)
	if ran != 1 || s.confirm.dlg.IsOpen() {
		t.Fatalf("OnOK ran %d times, open=%v", ran, s.confirm.dlg.IsOpen())
	}
	// Cancel closes without running it.
	s.Confirm(core.Confirm{Title: "Ще раз?", OnOK: func() { ran++ }})
	frames(s, 1000, 700, 2)
	s.confirm.cancel.Click()
	frames(s, 1000, 700, 2)
	if ran != 1 || s.confirm.dlg.IsOpen() {
		t.Fatalf("cancel: ran=%d open=%v", ran, s.confirm.dlg.IsOpen())
	}
}

func TestRailOpensQueueAndSettings(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	frames(s, 1200, 800, 2)
	s.rail.queue.Click()
	frames(s, 1200, 800, 2)
	if !s.drawer.IsOpen() {
		t.Fatal("the rail's Черга did not open the drawer")
	}
	s.rail.settings.Click()
	frames(s, 1200, 800, 2)
	if s.Current().Page != core.PageSettings || s.drawer.IsOpen() {
		t.Fatalf("route %+v, drawer open %v", s.Current(), s.drawer.IsOpen())
	}
	// The phone layout has the same destinations in the bottom bar.
	s.rail.home.Click()
	frames(s, 390, 844, 2)
	if s.Current().Page != core.PageHome {
		t.Fatalf("bottom bar: route %+v", s.Current())
	}
}

func TestTourFinishSavesTourDone(t *testing.T) {
	e := testEngine(t, false)
	_ = e.Config().Update(func(c *config.Config) { c.UI.TourDone = false })
	s := Headless(e)
	defer s.Close()
	s.StartTour()
	frames(s, 1200, 800, 3)
	if !s.tour.Active() {
		t.Fatal("tour did not start")
	}
	s.tour.Stop()
	s.tourDone()
	s.Flush()
	if !e.Config().Get().UI.TourDone {
		t.Fatal("TourDone not saved")
	}
}

func TestThemeFollowsConfig(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	_ = e.Config().Update(func(c *config.Config) { c.UI.Theme = "dark"; c.UI.ReducedMotion = true })
	frames(s, 800, 600, 2)
	if !s.th.Dark() || !gui.ReducedMotion() {
		t.Fatalf("dark=%v reduced=%v", s.th.Dark(), gui.ReducedMotion())
	}
	_ = e.Config().Update(func(c *config.Config) { c.UI.Theme = "light"; c.UI.ReducedMotion = false })
	frames(s, 800, 600, 2)
	if s.th.Dark() || gui.ReducedMotion() {
		t.Fatalf("dark=%v reduced=%v", s.th.Dark(), gui.ReducedMotion())
	}
}

// TestScreens renders every page of the shell (GPU headless) to catch
// panics; with GUISHOTS=<dir> it also writes the PNGs (light and dark at
// 1920×1080, 1440×900 and 390×844).
func TestScreens(t *testing.T) {
	e := testEngine(t, true)
	empty := testEngine(t, false)
	out := os.Getenv("GUISHOTS")
	type screen struct {
		name  string
		e     *studio.Engine
		setup func(s *Shell)
	}
	var first string
	if l := e.List(); len(l) > 0 {
		first = l[0].ID
	}
	screens := []screen{
		{"home", e, nil},
		{"home-empty", empty, nil},
		{"projects", e, func(s *Shell) { s.Navigate(core.Route{Page: core.PageProjects}) }},
		{"queue", e, func(s *Shell) { s.Navigate(core.Route{Page: core.PageQueue}) }},
		{"drawer", e, func(s *Shell) { s.OpenQueue() }},
		{"settings", e, func(s *Shell) { s.Navigate(core.Route{Page: core.PageSettings}) }},
		{"tour", e, func(s *Shell) { s.ShowTour(1) }},
	}
	if first != "" {
		screens = append(screens,
			screen{"menu", e, func(s *Shell) { s.Home().OpenMenu(first) }},
			screen{"project", e, func(s *Shell) { s.Navigate(core.Route{Page: core.PageProject, ProjectID: first}) }},
		)
	}
	sizes := [][2]int{{1440, 900}}
	themes := []bool{false}
	if out != "" {
		sizes = [][2]int{{1920, 1080}, {1440, 900}, {390, 844}}
		themes = []bool{false, true}
	}
	for _, sc := range screens {
		for _, dark := range themes {
			for _, sz := range sizes {
				s := Headless(sc.e)
				s.SetDark(dark)
				if sc.setup != nil {
					sc.setup(s)
				}
				img, err := s.Snapshot(sz[0], sz[1], gui.Frames(4), gui.Settle(60))
				s.Close()
				if err != nil {
					t.Fatalf("%s: %v", sc.name, err)
				}
				if out == "" {
					continue
				}
				theme := "light"
				if dark {
					theme = "dark"
				}
				path := filepath.Join(out, fmt.Sprintf("%s-%s-%dx%d.png", sc.name, theme, sz[0], sz[1]))
				if err := gui.SavePNG(img, path); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// The window opens straight on Home and takes input from the first frame:
// Ctrl+N opens the new-project sheet.
func TestStartsOnHome(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	if r := s.Current(); r.Page != core.PageHome {
		t.Fatalf("start route %+v", r)
	}
	var router input.Router
	var ops op.Ops
	n := 0
	frame := func() {
		n++
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Now: gui.SnapshotEpoch.Add(time.Duration(n) * 16 * time.Millisecond),
			Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(1200, 800)), Source: router.Source()}
		s.Layout(gtx)
		router.Frame(&ops)
		s.Flush()
	}
	ctrlN := func() { router.Queue(key.Event{Name: "N", Modifiers: key.ModShortcut, State: key.Press}) }
	frame()
	frame()
	ctrlN()
	frame()
	if !s.sheets.IsOpen() {
		t.Fatal("Ctrl+N did not open the new-project sheet")
	}
}
