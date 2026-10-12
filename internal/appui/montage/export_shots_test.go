package montage

// Headless screenshots of «Експортувати в…» (skipped unless GUISHOTS=<dir>):
// the menu next to «Змонтувати відео», the drawer before, during and after
// an export, at ~1440 and 390 px, light and dark. The demo engine works in
// a temp folder; nothing is exported (an offline Ctx never starts one).

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/studio"
)

func TestExportShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	e := demo(t)
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var wide string
	for _, s := range e.List() {
		if p, _ := e.Get(s.ID); len(p.Scenes) > 0 && p.Input.Aspect != "9:16" {
			wide = s.ID
		}
	}
	// Not montaged yet: «Змонтувати відео» + «Експортувати в…» side by side.
	fresh := func(v *view) {
		st := v.state(wide)
		st.scrollTo = "final"
		st.fake.fresh = true
	}
	if wide == "" {
		t.Fatal("no demo project")
	}
	c.Refresh()

	open := func(target string) func(v *view) {
		return func(v *view) {
			st := v.state(wide)
			st.scrollTo = "final"
			st.openExport(c, c.Project(wide), target)
		}
	}
	incomplete := func(v *view) {
		st := v.state(wide)
		st.exp.fakeReady = &studio.ExportReadiness{Scenes: 12, OnTimeline: 12, Clips: 10, Stills: 2, Voices: 11, MissingVoices: 1,
			Music: true, Subtitles: true, Overlays: false, Cover: true}
		open(studio.ExportCapCut)(v)
	}
	running := func(v *view) {
		open(studio.ExportResolve)(v)
		st := &v.state(wide).exp
		st.phase, st.pct, st.msg = exportRunning, 46, "Сцена 6 з 12"
	}
	done := func(v *view) {
		open(studio.ExportResolve)(v)
		st := &v.state(wide).exp
		tgt, _ := studio.ExportTargetByID(studio.ExportResolve)
		st.phase = exportDone
		st.res = studio.ExportResult{Dir: filepath.Join(os.TempDir(), "export", "resolve-2026-10-02"), Zip: "x.zip", Target: tgt,
			Timeline: "timeline.xml", Files: 41, Notes: []string{
				"Перехід «Шторка вліво» (2) перенесено як розчинення тієї ж тривалості — за бажання заміни його в редакторі.",
				"Кольоровий фільтр «Кіно» не переноситься — зроби корекцію кольору в редакторі.",
				"Приглушення музики під голосом перенесено ключовими кадрами гучності (-12 дБ); точна копія фільму — stems/music.wav, якщо її увімкнено.",
			}}
	}
	menu := func(v *view) {
		st := v.state(wide)
		st.scrollTo = "final"
		st.exp.menu.Open()
	}
	type shot struct {
		name   string
		id     string
		mainW  int
		height int
		side   bool
		prep   func(v *view)
	}
	shots := []shot{
		{"export-menu", wide, 1000, 1100, true, menu},
		{"export-sheet", wide, 1000, 1100, true, open(studio.ExportResolve)},
		{"export-capcut", wide, 1000, 1100, true, incomplete},
		{"export-running", wide, 1000, 900, true, running},
		{"export-done", wide, 1000, 1100, true, done},
		{"export-fresh", wide, 1000, 1100, true, fresh},
		{"export-menu", wide, 358, 900, false, menu},
		{"export-sheet", wide, 358, 1200, false, open(studio.ExportResolve)},
		{"export-done", wide, 358, 1200, false, done},
		{"export-fresh", wide, 358, 1100, false, fresh},
	}
	for _, dark := range []bool{false, true} {
		for _, s := range shots {
			if s.id == "" {
				continue
			}
			v := New().(*view)
			c.Th.SetPaletteNow(dark)
			if s.prep != nil {
				s.prep(v)
			}
			mode := "light"
			if dark {
				mode = "dark"
			}
			pad := 32
			if s.mainW < 600 {
				pad = 16
			}
			W := pad*2 + s.mainW
			if s.side {
				W += 24 + 360
			}
			name := fmt.Sprintf("%s-%d-%s.png", s.name, W, mode)
			id := s.id
			draw := harness(c, v, func() *core.ProjectView { return c.Project(id) }, s.mainW, s.side)
			if err := core.Shot(c, h, W, s.height, 1, filepath.Join(out, name), draw); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			t.Logf("wrote %s", name)
		}
	}
}
