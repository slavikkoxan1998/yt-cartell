package montage

// Headless screenshots of the step (skipped unless GUISHOTS=<dir>):
//
//	GUISHOTS=../../../screenshots/new/montage CGO_ENABLED=0 go test ./internal/appui/montage -run TestShots -v
//
// The demo engine imports the web studio's projects into a temp folder
// (fake keys, in-memory settings) — the user's real data is never touched.

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// demo builds the demo engine and gives the 16:9 project a cover picture
// (a scene frame, through SetCoverImage — the cover stage needs the network).
func demo(t *testing.T) *studio.Engine {
	t.Helper()
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "../../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range e.List() {
		p, ok := e.Get(s.ID)
		if !ok || len(p.Scenes) < 2 || p.Input.Aspect == "9:16" || p.Scenes[1].Frame.Path == "" {
			continue
		}
		if err := e.SetCoverImage(s.ID, e.Abs(s.ID, p.Scenes[1].Frame.Path)); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// harness draws the step like the workspace does: main column (+ the
// context column when there is room) on the warm ground.
func harness(c *core.Ctx, v *view, pv func() *core.ProjectView, mainW int, side bool) func(gtx C) D {
	return func(gtx C) D {
		p := pv()
		pad := gtx.Dp(32)
		if mainW < 600 {
			pad = gtx.Dp(16)
		}
		H := gtx.Constraints.Max.Y
		// Main column, laid out Bleed() wider on each side like the workspace does.
		func() {
			b := gtx.Dp(v.Bleed())
			defer op.Offset(image.Pt(pad-b, pad)).Push(gtx.Ops).Pop()
			g := gtx
			g.Constraints = layout.Exact(image.Pt(mainW+2*b, H-pad))
			v.Layout(g, c, p)
		}()
		if side {
			x := pad + mainW + gtx.Dp(24)
			defer op.Offset(image.Pt(x, pad)).Push(gtx.Ops).Pop()
			g := gtx
			g.Constraints = layout.Constraints{Min: image.Pt(gtx.Dp(360), 0), Max: image.Pt(gtx.Dp(360), H-pad)}
			layout.Flex{Axis: layout.Vertical}.Layout(g,
				layout.Rigid(func(gtx C) D {
					n := v.Next(c, p)
					var cl gui.Clickable
					return gui.NextStepCard{Icon: n.Icon, Title: n.Title, Text: n.Text, Busy: n.Busy}.Layout(gtx, c.Th, &cl)
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp24}.Layout),
				layout.Rigid(func(gtx C) D { return v.Side(gtx, c, p) }),
			)
		}
		return D{Size: gtx.Constraints.Max}
	}
}

func TestShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	e := demo(t)
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()

	var empty, wide, tall string
	for _, s := range e.List() {
		p, _ := e.Get(s.ID)
		switch {
		case len(p.Scenes) == 0:
			empty = s.ID
		case p.Input.Aspect == "9:16":
			tall = s.ID
		default:
			wide = s.ID
		}
	}
	if wide == "" || tall == "" {
		t.Fatalf("demo projects missing: wide=%q tall=%q", wide, tall)
	}
	// Some cover text and a subtitle style to show.
	_ = e.Edit(wide, func(p *studio.Project) {
		p.Montage.CoverText = studio.CoverText{Title: "Він *повернувся* додому", Subtitle: "Правдива історія з Карпат", Badge: "НОВЕ"}
		p.Montage.SubtitleStyle = studio.SubtitleStyle{Preset: "youtube"}
		p.Montage.Filter = "cinematic"
	})
	_ = e.Edit(tall, func(p *studio.Project) {
		p.Montage.CoverText = studio.CoverText{Title: "Зерно *пам'яті*"}
		p.Montage.Subtitles = "burn"
	})
	c.Refresh()

	type shot struct {
		name   string
		id     string
		mainW  int
		height int
		side   bool
		prep   func(v *view)
	}
	selectSecond := func(id string) func(v *view) {
		return func(v *view) {
			if pv := c.Project(id); pv != nil && len(pv.P.Scenes) > 1 {
				c.UI(id).Scene = pv.P.Scenes[1].ID
			}
		}
	}
	scrollCover := func(id string) func(v *view) {
		return func(v *view) { v.state(id).scrollTo = "cover" }
	}
	running := func(id string) func(v *view) {
		return func(v *view) {
			v.state(id).fake.job = &studio.Job{Kind: studio.JobMontage, Status: studio.JobRunning, Progress: 42, Message: "Монтуємо сцену 2 з 3"}
		}
	}
	coverTab := func(id string, tab int) func(v *view) {
		return func(v *view) {
			st := v.state(id)
			st.scrollTo = "cover"
			st.cov.tab = tab
		}
	}
	popover := func(id string) func(v *view) {
		return func(v *view) {
			if pv := c.Project(id); pv != nil && len(pv.P.Scenes) > 1 {
				v.state(id).pop.open(pv.P.Scenes[0].ID)
			}
		}
	}
	trimming := func(id string) func(v *view) {
		return func(v *view) {
			if pv := c.Project(id); pv != nil && len(pv.P.Scenes) > 1 {
				v.state(id).tl.drag = dragState{kind: dragTrimR, id: pv.P.Scenes[0].ID, vDur: 8, tout: 1.6, x0: 400, x: 400}
			}
		}
	}
	reordering := func(id string) func(v *view) {
		return func(v *view) {
			if pv := c.Project(id); pv != nil && len(pv.P.Scenes) > 2 {
				v.state(id).tl.drag = dragState{kind: dragReorder, id: pv.P.Scenes[0].ID, x0: 690, x: 700, grab: 120, drop: 1}
			}
		}
	}
	// A voice line mid-gesture: moved, or trimmed at the head or the tail.
	voiceDrag := func(id string, kind dragKind, amount float64) func(v *view) {
		return func(v *view) {
			pv := c.Project(id)
			if pv == nil || len(pv.P.Scenes) < 2 {
				return
			}
			for _, it := range e.Timeline(pv.P) {
				if it.SceneID != pv.P.Scenes[1].ID || it.VoiceDuration <= 0 {
					continue
				}
				d := dragState{kind: kind, id: it.SceneID, x0: 400, x: 400,
					vStart0: it.VoiceStart, vDur0: it.VoiceDuration, vShift0: it.VoiceShift,
					vTin0: it.VoiceTrimIn, vTout0: it.VoiceTrimOut, vRaw: it.VoiceRawDuration}
				d.vShift, d.vTin, d.vTout = d.vShift0, d.vTin0, d.vTout0
				switch kind {
				case dragVoiceMove:
					d.vShift += amount
				case dragVoiceTrimL:
					d.vTin += amount
					d.vShift += amount
				case dragVoiceTrimR:
					d.vTout += amount
				}
				v.state(id).tl.drag = d
				c.UI(id).Scene = it.SceneID
			}
		}
	}
	// The line being edited in the inspector (typed, not stored yet).
	editingLine := func(id string) func(v *view) {
		return func(v *view) {
			pv := c.Project(id)
			if pv == nil || len(pv.P.Scenes) < 2 {
				return
			}
			sc := pv.P.Scenes[1]
			c.UI(id).Scene = sc.ID
			st := &v.state(id).insp
			st.textScene, st.textDirty = sc.ID, true
			st.text.SetText(sc.Narration + " І тут він зрозумів, що дорога додому коротша, ніж здавалося.")
		}
	}
	shots := []shot{
		{"empty", empty, 1400, 1000, true, nil},
		{"timeline", wide, 1400, 3400, true, nil},
		{"selected", wide, 1400, 1000, true, selectSecond(wide)},
		{"popover", wide, 1400, 1000, true, popover(wide)},
		{"trim", wide, 1400, 800, true, trimming(wide)},
		{"reorder", wide, 1400, 800, true, reordering(wide)},
		{"voice-move", wide, 1400, 800, true, voiceDrag(wide, dragVoiceMove, 2.2)},
		{"voice-trim-head", wide, 1400, 800, true, voiceDrag(wide, dragVoiceTrimL, 0.8)},
		{"voice-trim-tail", wide, 1400, 800, true, voiceDrag(wide, dragVoiceTrimR, 1.5)},
		{"line", wide, 1400, 1000, true, editingLine(wide)},
		{"line", wide, 1000, 1500, true, editingLine(wide)},
		{"cover", wide, 1400, 1300, true, scrollCover(wide)},
		{"cover-templates", wide, 1400, 1300, true, coverTab(wide, tabTemplates)},
		{"cover-style", wide, 1400, 1500, true, coverTab(wide, tabStyle)},
		{"cover-picture", wide, 1400, 1300, true, coverTab(wide, tabPicture)},
		{"cover-picture", tall, 1000, 1700, true, coverTab(tall, tabPicture)},
		{"running", wide, 1400, 1300, true, running(wide)},
		{"vertical", tall, 1400, 3400, true, nil},
		{"timeline", wide, 1000, 3800, true, nil},
		{"cover", tall, 1000, 1500, true, scrollCover(tall)},
		{"timeline", wide, 360, 5200, false, nil},
		{"vertical", tall, 360, 5600, false, nil},
	}
	for _, dark := range []bool{false, true} {
		for _, s := range shots {
			if s.id == "" {
				continue
			}
			v := New().(*view)
			if s.prep != nil {
				s.prep(v)
			}
			c.Th.SetPaletteNow(dark)
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
			name := fmt.Sprintf("%s-%d-%s.png", s.name, s.mainW, mode)
			id := s.id
			draw := harness(c, v, func() *core.ProjectView { return c.Project(id) }, s.mainW, s.side)
			if err := core.Shot(c, h, W, s.height, 1, filepath.Join(out, name), draw); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			c.UI(id).Scene = ""
			t.Logf("wrote %s", name)
		}
	}
}

// TestSixtyScenes lays the step out with a 60-scene film and reports the
// cost of a frame (the timeline draws only the clips in view).
func TestSixtyScenes(t *testing.T) {
	if os.Getenv("GUISHOTS") == "" && os.Getenv("G4PERF") == "" {
		t.Skip("set GUISHOTS or G4PERF")
	}
	data := filepath.Join(t.TempDir(), "data")
	e, err := core.DemoEngine(data, "../../../..")
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, s := range e.List() {
		if p, ok := e.Get(s.ID); ok && len(p.Scenes) >= 3 && p.Input.Aspect == "16:9" {
			id = s.ID
		}
	}
	_ = e.Shutdown(5 * time.Second)
	if id == "" {
		t.Skip("no demo project")
	}
	path := filepath.Join(data, "projects", id, "project.json")
	raw, _ := os.ReadFile(path)
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	scenes, _ := p["scenes"].([]any)
	var many []any
	for i := 0; i < 60; i++ {
		src, _ := json.Marshal(scenes[i%len(scenes)])
		var sc map[string]any
		_ = json.Unmarshal(src, &sc)
		sc["id"] = fmt.Sprintf("s%02dxxxx", i)
		sc["title"] = fmt.Sprintf("Сцена номер %d", i+1)
		many = append(many, sc)
	}
	p["scenes"] = many
	out, _ := json.Marshal(p)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	e2, err := core.DemoEngine(data, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Shutdown(0)
	c, h := core.NewHeadless(e2)
	defer c.Close()
	v := New().(*view)
	draw := harness(c, v, func() *core.ProjectView { return c.Project(id) }, 1400, true)
	// Warm up (image decodes), then time frames.
	if _, err := gui.Snapshot(1848, 1000, 1, core.Frame(c, h, draw), gui.Frames(3)); err != nil {
		t.Fatal(err)
	}
	const n = 30
	start := time.Now()
	if _, err := gui.Snapshot(1848, 1000, 1, core.Frame(c, h, draw), gui.Frames(n)); err != nil {
		t.Fatal(err)
	}
	per := time.Since(start) / n
	t.Logf("60 scenes: %v per frame (layout + ops, incl. one raster at the end)", per)
	if len(v.state(id).film.items) != 60 {
		t.Fatalf("timeline has %d items", len(v.state(id).film.items))
	}
	if out := os.Getenv("GUISHOTS"); out != "" {
		_ = core.Shot(c, h, 1848, 1000, 1, filepath.Join(out, "sixty-1400-light.png"), draw)
	}
}
