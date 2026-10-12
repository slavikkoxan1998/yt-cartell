package montage

import (
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

// ---- pure logic ---------------------------------------------------------------------------------

func ids(scenes []studio.Scene) string {
	s := ""
	for _, sc := range scenes {
		s += sc.ID
	}
	return s
}

func TestReorderScenes(t *testing.T) {
	mk := func(s string) []studio.Scene {
		var out []studio.Scene
		for _, r := range s {
			out = append(out, studio.Scene{ID: string(r)})
		}
		return out
	}
	cases := []struct {
		scenes, id, order, want string
	}{
		{"ABCD", "A", "BCAD", "BCAD"},
		{"ABCD", "D", "DABC", "DABC"},
		{"ABCD", "B", "ACDB", "ACDB"},
		// x is not on the timeline: it stays between its neighbours.
		{"AxBC", "C", "CAB", "CAxB"},
		{"AxBC", "A", "BCA", "xBCA"},
		{"AxBC", "B", "BAC", "BAxC"},
	}
	for _, tc := range cases {
		var order []string
		for _, r := range tc.order {
			order = append(order, string(r))
		}
		got := ids(reorderScenes(mk(tc.scenes), tc.id, order))
		if got != tc.want {
			t.Errorf("reorder %s: move %s to %s = %s, want %s", tc.scenes, tc.id, tc.order, got, tc.want)
		}
	}
}

func TestTrimLimits(t *testing.T) {
	if got := trimLimits(8, 0); math.Abs(got-7.5) > 1e-9 {
		t.Fatalf("trimLimits(8,0) = %v", got)
	}
	if got := trimLimits(8, 7.8); got != 0 {
		t.Fatalf("trimLimits(8,7.8) = %v", got)
	}
	if got := trimLimits(0.3, 0); got != 0 {
		t.Fatalf("a clip shorter than the minimum cannot be trimmed: %v", got)
	}
}

func TestSlotsTile(t *testing.T) {
	// Boxes tile the film without gaps, also with overlapping transitions.
	f := film{items: []studio.TimelineItem{
		{SceneID: "a", Start: 0, Duration: 8.5, Overlap: 0.5, ClipDuration: 8},
		{SceneID: "b", Start: 8, Duration: 6, Overlap: 0, ClipDuration: 6},
		{SceneID: "c", Start: 14, Duration: 4, ClipDuration: 4},
	}}
	sl := f.slots()
	for k := 1; k < len(sl); k++ {
		if d := sl[k].s0 - (sl[k-1].s0 + sl[k-1].w); math.Abs(d) > 1e-9 {
			t.Fatalf("gap %v between %d and %d", d, k-1, k)
		}
	}
	end := sl[2].s0 + sl[2].w
	if math.Abs(end-18) > 1e-9 {
		t.Fatalf("film ends at %v, want 18", end)
	}
}

func TestCues(t *testing.T) {
	long := "Жила-була руда лисиця, яка щоночі виходила на пагорб і розмовляла з місяцем. Одного разу місяць не з'явився — і лисиця вирушила його шукати через ліс, річку й старе місто."
	parts := cues(long, 84)
	if len(parts) < 2 {
		t.Fatalf("cues: %q", parts)
	}
	for _, p := range parts {
		if n := len([]rune(p)); n > 110 {
			t.Fatalf("cue too long (%d): %q", n, p)
		}
	}
	if w := wrap2("коротко", 42); w != "коротко" {
		t.Fatalf("wrap2 short = %q", w)
	}
	if w := wrap2(parts[0], 42); len([]rune(parts[0])) > 42 && !containsRune(w, '\n') {
		t.Fatalf("wrap2 did not wrap %q", parts[0])
	}
}

func containsRune(s string, r rune) bool {
	for _, x := range s {
		if x == r {
			return true
		}
	}
	return false
}

func TestLooks(t *testing.T) {
	src := landscape(64, 36, 0)
	if applyLook(src, "none") != src || applyLook(src, "") != src {
		t.Fatal("no look must return the picture itself")
	}
	for _, f := range media.Filters {
		out := applyLook(src, f.ID)
		if out.Bounds() != src.Bounds() {
			t.Fatalf("%s: bounds %v", f.ID, out.Bounds())
		}
	}
	// Noir is grey.
	n := applyLook(src, "noir")
	c := n.RGBAAt(32, 30)
	if abs(int(c.R)-int(c.G)) > 3 || abs(int(c.G)-int(c.B)) > 3 {
		t.Fatalf("noir is not grey: %v", c)
	}
	// Warm is warmer than cool.
	w, k := applyLook(src, "warm").RGBAAt(20, 10), applyLook(src, "cool").RGBAAt(20, 10)
	if int(w.R)-int(w.B) <= int(k.R)-int(k.B) {
		t.Fatalf("warm %v not warmer than cool %v", w, k)
	}
	if s := shrink(landscape(400, 200, 1), 100); s.Bounds().Dx() != 100 || s.Bounds().Dy() != 50 {
		t.Fatalf("shrink: %v", s.Bounds())
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestMotionTransform(t *testing.T) {
	for _, e := range media.Effects {
		a, ok := motionTransform(e.ID, 320, 180, 0.5, 1, false)
		if e.ID == "none" {
			if ok {
				t.Fatal("none must not move")
			}
			continue
		}
		if !ok {
			t.Fatalf("%s: no motion", e.ID)
		}
		// The moved picture still covers the box: the box corners map back
		// inside the source frame.
		inv := a.Invert()
		for _, p := range []f32.Point{{X: 0, Y: 0}, {X: 320, Y: 0}, {X: 0, Y: 180}, {X: 320, Y: 180}} {
			q := inv.Transform(p)
			if q.X < -0.5 || q.Y < -0.5 || q.X > 320.5 || q.Y > 180.5 {
				t.Fatalf("%s: corner %v shows outside the frame (%v)", e.ID, p, q)
			}
		}
	}
}

func TestFormat(t *testing.T) {
	cases := map[string]string{secText(8): "8 с", secText(6.54): "6,5 с", pctText(0.18): "18%", bytesText(12_900_000): "12,3 МБ"}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	if fileName(`Лис/ця: "казка"?`) != "Лис_ця_ _казка__" {
		t.Errorf("fileName = %q", fileName(`Лис/ця: "казка"?`))
	}
	if parseHex("#FFE000") != (color.NRGBA{R: 255, G: 224, B: 0, A: 255}) {
		t.Error("parseHex")
	}
}

// ---- interaction (real pointer events through a headless router) ------------------------------------

// drive renders the step with a pointer script: script(frame, router)
// queues events before a frame (after the first layouts).
func drive(t *testing.T, c *core.Ctx, h *core.HeadlessHost, v *view, id string, frames int, script func(frame int, r *input.Router)) {
	t.Helper()
	draw := func(gtx C) D {
		c.Th.WantAnchors()
		pv := c.Project(id)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(1400, 1000))
		return v.Layout(g, c, pv)
	}
	if _, err := gui.Snapshot(1400, 1000, 1, core.Frame(c, h, draw), gui.Frames(frames), gui.BeforeFrame(script)); err != nil {
		t.Fatal(err)
	}
}

func demoCtx(t *testing.T) (*studio.Engine, *core.Ctx, *core.HeadlessHost, string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join("../../../..", "data", "studio")); err != nil {
		t.Skip("no web studio demo data")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "../../../..")
	if err != nil {
		t.Fatal(err)
	}
	c, h := core.NewHeadless(e)
	var id string
	for _, s := range e.List() {
		if p, ok := e.Get(s.ID); ok && len(p.Scenes) >= 3 && p.Input.Aspect == "16:9" {
			id = s.ID
		}
	}
	if id == "" {
		t.Skip("no demo project with three scenes")
	}
	return e, c, h, id
}

func press(r *input.Router, kind pointer.Kind, x, y float32) {
	r.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y), PointerID: 1})
}

func TestReorderByDrag(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	before, _ := e.Get(id)
	v := New().(*view)
	s := v.state(id)
	var ok bool
	drive(t, c, h, v, id, 16, func(frame int, r *input.Router) {
		g := &s.tl.geo
		area, found := c.Th.AnchorRect("mont.tracks")
		if !found || len(g.clips) < 3 {
			return
		}
		y := float32(area.Min.Y + g.videoY + g.videoH/2)
		first, last := g.clips[0], g.clips[len(g.clips)-1]
		x0 := float32(area.Min.X) + (first.x0+first.x1)/2
		x1 := float32(area.Min.X) + last.x1 - 4
		switch frame {
		case 5:
			press(r, pointer.Move, x0, y)
			press(r, pointer.Press, x0, y)
		case 6:
			press(r, pointer.Move, x0+30, y)
		case 7:
			press(r, pointer.Move, (x0+x1)/2, y)
		case 8:
			press(r, pointer.Move, x1, y)
			ok = s.tl.drag.kind == dragReorder
		case 9:
			press(r, pointer.Release, x1, y)
		}
	})
	if !ok {
		t.Fatal("the drag did not lift the clip")
	}
	after, _ := e.Get(id)
	if after.Scenes[len(after.Scenes)-1].ID != before.Scenes[0].ID {
		t.Fatalf("first scene not moved to the end: %s → %s", ids(before.Scenes), ids(after.Scenes))
	}
}

func TestTrimByDrag(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	var target string
	drive(t, c, h, v, id, 16, func(frame int, r *input.Router) {
		g := &s.tl.geo
		area, found := c.Th.AnchorRect("mont.tracks")
		if !found || len(g.clips) < 2 {
			return
		}
		cg := g.clips[0]
		target = cg.id
		y := float32(area.Min.Y + g.videoY + g.videoH/3)
		x := float32(area.Min.X) + cg.x1 - 3
		dx := 2 * g.pxs // two seconds
		switch frame {
		case 5:
			press(r, pointer.Move, x, y)
			press(r, pointer.Press, x, y)
		case 6:
			press(r, pointer.Move, x-dx/2, y)
		case 7:
			press(r, pointer.Move, x-dx, y)
		case 8:
			press(r, pointer.Release, x-dx, y)
		}
	})
	p, _ := e.Get(id)
	for _, sc := range p.Scenes {
		if sc.ID == target {
			if math.Abs(sc.TrimOut-2) > 0.15 {
				t.Fatalf("trim out = %v, want ≈ 2", sc.TrimOut)
			}
			return
		}
	}
	t.Fatal("scene not found")
}

func TestChipOpensPopover(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	var opened string
	drive(t, c, h, v, id, 14, func(frame int, r *input.Router) {
		g := &s.tl.geo
		area, found := c.Th.AnchorRect("mont.tracks")
		if !found || len(g.clips) < 2 {
			return
		}
		x := float32(area.Min.X) + g.clips[0].x1
		y := float32(area.Min.Y + g.videoY)
		switch frame {
		case 5:
			press(r, pointer.Move, x, y)
			press(r, pointer.Press, x, y)
		case 6:
			press(r, pointer.Release, x, y)
		case 9:
			opened = s.pop.sceneID
		}
	})
	if opened == "" {
		t.Fatal("clicking the transition chip did not open the popover")
	}
}

func TestSliderCommitsOnceAfterPause(t *testing.T) {
	th := gui.MustTheme()
	var vs valSlider
	model := float32(0.2)
	var commits []float32
	draw := func(gtx C) D {
		if v, ok := vs.Commit(gtx); ok {
			commits = append(commits, v)
			model = v
		}
		g := gtx
		g.Constraints = layout.Exact(image.Pt(400, 40))
		return vs.Layout(g, th, gui.SliderStyle{Value: model, Min: 0, Max: 1, Step: 0.01})
	}
	script := func(frame int, r *input.Router) {
		switch frame {
		case 2:
			press(r, pointer.Move, 90, 14)
			press(r, pointer.Press, 90, 14)
		case 3:
			press(r, pointer.Move, 200, 14)
		case 4:
			press(r, pointer.Move, 300, 14)
		case 5:
			press(r, pointer.Release, 300, 14)
		}
	}
	// 40 frames of 16 ms: the pause after the release is long enough to commit.
	if _, err := gui.Snapshot(400, 40, 1, func(gtx C) D { return th.Root(gtx, draw) }, gui.Frames(40), gui.BeforeFrame(script)); err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 {
		t.Fatalf("commits = %v, want exactly one", commits)
	}
	if commits[0] < 0.6 || commits[0] > 0.8 {
		t.Fatalf("committed %v, want ≈ 0.74", commits[0])
	}
}
