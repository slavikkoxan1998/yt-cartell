package produce

import (
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

func done(path string) studio.Asset {
	return studio.Asset{Path: path, Status: studio.StatusDone, UpdatedAt: 1}
}

func TestCountsAndStaleRules(t *testing.T) {
	p := &studio.Project{Input: studio.Input{Voiceover: true}}
	p.Characters = []studio.Character{{ID: "c1", Image: done("a.png")}, {ID: "c2", Image: studio.Asset{Status: studio.StatusRunning, Progress: 40}}}
	p.Locations = []studio.Location{{ID: "l1", Image: studio.Asset{Status: studio.StatusError, Error: "x"}}}
	s := func(id string) studio.Scene { return studio.Scene{ID: id, Narration: "Текст сцени"} }
	a, b, c, d, e := s("a"), s("b"), s("c"), s("d"), s("e")
	a.Frame, a.Video, a.Audio = done("f.png"), done("v.mp4"), done("a.mp3")
	b.Frame, b.Video, b.Audio = done("f.png"), done("v.mp4"), done("a.mp3")
	b.Frame.Stale = true // prompt edited after drawing: frame and clip are outdated
	c.Frame = studio.Asset{Status: studio.StatusQueued, Progress: 100}
	d.Frame = studio.Asset{Status: studio.StatusError, Error: "nope"}
	e.Narration, e.AudioSource, e.VoiceStart, e.VoiceEnd = "", studio.AudioUpload, 1, 4
	e.Frame, e.Audio = done("f.png"), done("a.mp3")
	e.Audio.Stale = true
	p.Scenes = []studio.Scene{a, b, c, d, e}

	n := countFrames(p)
	if n.scenes != 5 || n.done != 3 || n.stale != 1 || n.busy != 1 || n.errs != 1 || n.empty != 0 {
		t.Fatalf("frames: %+v", n)
	}
	if n.left() != 1 || n.refsDone != 1 || n.refsBusy != 1 || n.refsErr != 1 || n.refsLeft() != 1 {
		t.Fatalf("frames left/refs: %+v", n)
	}
	m := countClips(p)
	if m.withFrame != 3 || m.noFrames != 2 || m.vidDone != 2 || m.vidLeft != 1 || m.vidStale != 1 {
		t.Fatalf("clips: %+v", m)
	}
	if m.voiceable != 5 || m.voiceDone != 3 || m.voiceLeft != 2 || m.voiceStale != 1 || m.redo != 2 {
		t.Fatalf("voices: %+v", m)
	}
	fi := staleFrameItems(p)
	if len(fi) != 1 || fi[0].id != "b" || fi[0].kind != studio.RegenFrame {
		t.Fatalf("stale frames: %+v", fi)
	}
	ri := staleRenderItems(p)
	if len(ri) != 2 {
		t.Fatalf("stale render items: %+v", ri)
	}
	if ri[0].kind != studio.RegenFrame || ri[0].then == nil || ri[0].then.kind != studio.RegenVideo || ri[0].size() != 2 {
		t.Fatalf("a changed frame must be redrawn before its clip: %+v", ri[0])
	}
	if ri[1].kind != studio.RegenVoice || ri[1].id != "e" {
		t.Fatalf("own-voice slice must be re-cut: %+v", ri[1])
	}
}

func TestLengthHint(t *testing.T) {
	p := &studio.Project{Input: studio.Input{Voiceover: true}}
	env := clipEnv{clipSec: 8}
	s := &studio.Scene{ID: "s", Narration: "раз два три"}
	s.Video = studio.Asset{Status: studio.StatusDone, Duration: 8}
	s.Audio = studio.Asset{Status: studio.StatusDone, Duration: 5.4}
	if txt, warn := lengthHint(p, s, env); warn || txt != "Кліп 8 с · голос 5,4 с" {
		t.Fatalf("fits: %q %v", txt, warn)
	}
	s.Audio.Duration = 9.2
	if txt, warn := lengthHint(p, s, env); !warn || txt == "" {
		t.Fatalf("voice longer than the clip must warn: %q", txt)
	}
	// Exact numbers from the timeline win.
	env.tl = map[string]studio.TimelineItem{"s": {SceneID: "s", HasClip: true, ClipDuration: 6, VoiceDuration: 7, Duration: 8.2}}
	if txt, warn := lengthHint(p, s, env); !warn || txt != "Голос 7 с довший за кліп 6 с — останній кадр потримається ще 2,2 с" {
		t.Fatalf("timeline hint: %q", txt)
	}
	// Not rendered yet: estimates are marked.
	env.tl = nil
	s.Video, s.Audio = studio.Asset{}, studio.Asset{}
	if txt, _ := lengthHint(p, s, env); txt != "Кліп ≈8 с · голос ≈1,3 с" {
		t.Fatalf("estimate: %q", txt)
	}
}

func TestLightboxMath(t *testing.T) {
	var l Lightbox
	l.Open([]LightboxItem{{Key: "a", Grid: 3}, {Key: "b"}, {Key: "c"}}, 1)
	l.view = image.Rect(100, 100, 900, 600) // 800×500
	l.fit = f32.Pt(800, 450)
	// Zooming keeps the picture point under the pointer in place.
	p := f32.Pt(700, 300)
	before := l.picPoint(p)
	l.zoomAt(p, 2)
	l.zoom, l.pan = l.zoomT, l.panT
	if got := l.picPoint(p); math.Abs(float64(got.X-before.X)) > 0.5 || math.Abs(float64(got.Y-before.Y)) > 0.5 {
		t.Fatalf("zoom moved the point under the pointer: %v → %v", before, got)
	}
	// Pan is clamped so the picture keeps covering the view.
	pp := l.clampPan(f32.Pt(10000, -10000), 2)
	if pp.X != 400 || pp.Y != -200 {
		t.Fatalf("clamp = %v, want (400,-200)", pp)
	}
	if pp := l.clampPan(f32.Pt(50, 50), 1); pp != (f32.Point{}) {
		t.Fatalf("a fitted picture must stay centred, got %v", pp)
	}
	// A click on panel 5 of a sheet fills the view with it.
	l.Go(0)
	l.fit = f32.Pt(800, 450)
	l.clickAt(l.items[0], f32.Pt(500, 350)) // centre of the view = panel 5
	if l.zoomT < 2.5 || math.Abs(float64(l.panT.X)) > 1 || math.Abs(float64(l.panT.Y)) > 1 {
		t.Fatalf("panel zoom: zoom %.2f pan %v", l.zoomT, l.panT)
	}
	// Refreshing the items keeps the current one by key.
	l.Go(2)
	l.Update([]LightboxItem{{Key: "x"}, {Key: "c"}, {Key: "a"}})
	if l.Current() != "c" || l.Index() != 1 {
		t.Fatalf("update lost the current item: %q at %d", l.Current(), l.Index())
	}
	l.Update(nil)
	if l.IsOpen() {
		t.Fatal("no items left: the lightbox must close")
	}
}

// picPoint maps a window point to picture coordinates (fractions of the
// fitted size) for the current view.
func (l *Lightbox) picPoint(p f32.Point) f32.Point {
	r := l.picRect()
	return f32.Pt((p.X-r.Min.X)/r.Dx(), (p.Y-r.Min.Y)/r.Dy())
}

// frames runs n frames of draw through a real input router (no GPU).
type frames struct {
	r   input.Router
	ops op.Ops
	now time.Time
}

func (f *frames) run(n, w, h int, draw func(gtx C) D) time.Duration {
	if f.now.IsZero() {
		f.now = gui.SnapshotEpoch
	}
	var spent time.Duration
	for i := 0; i < n; i++ {
		f.ops.Reset()
		gtx := layout.Context{Ops: &f.ops, Now: f.now, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(w, h)), Source: f.r.Source()}
		t0 := time.Now()
		draw(gtx)
		spent += time.Since(t0)
		f.r.Frame(&f.ops)
		f.now = f.now.Add(16 * time.Millisecond)
	}
	return spent
}

func TestLightboxKeys(t *testing.T) {
	th := gui.MustTheme()
	cache := gui.NewImageCache(nil)
	var l Lightbox
	l.Open([]LightboxItem{{Key: "a"}, {Key: "b"}, {Key: "c"}}, 0)
	var f frames
	draw := func(gtx C) D {
		th.Root(gtx, func(gtx C) D {
			l.Layout(gtx, th, cache)
			return D{Size: gtx.Constraints.Max}
		})
		return D{}
	}
	f.run(3, 1200, 800, draw)
	f.r.Queue(key.Event{Name: key.NameRightArrow, State: key.Press})
	f.run(2, 1200, 800, draw)
	if l.Index() != 1 {
		t.Fatalf("→ must move to the next picture, at %d", l.Index())
	}
	f.r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	f.run(2, 1200, 800, draw)
	if l.IsOpen() {
		t.Fatal("Esc must close the lightbox")
	}
}

// demo returns a headless context on the web studio's demo projects.
func demo(t *testing.T) (*core.Ctx, *core.HeadlessHost, map[string]string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(webRoot, "data", "studio")); err != nil {
		t.Skip("the web studio demo data is not there")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(0) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	ids := map[string]string{}
	for _, s := range c.List() {
		switch {
		case s.Scenes == 0:
			ids["empty"] = s.ID
		case s.Aspect == "9:16":
			ids["vertical"] = s.ID
		default:
			ids["wide"] = s.ID
		}
	}
	if len(ids) < 3 {
		t.Skip("the demo projects are not all there")
	}
	return c, h, ids
}

func TestOfflineNeverGenerates(t *testing.T) {
	c, h, ids := demo(t)
	pv := c.Project(ids["wide"])
	sid := pv.P.Scenes[0].ID
	before := c.Th.Toasts()
	if c.Regen(pv.ID, studio.RegenFrame, sid, "") {
		t.Fatal("offline: regen must refuse")
	}
	c.Run(pv.ID, studio.StageFrames, true, "")
	var b batch
	b.start(c, pv.ID, "test", []batchItem{{kind: studio.RegenFrame, id: sid}})
	if b.active() {
		t.Fatal("offline: a batch started")
	}
	p, _ := c.E.Get(pv.ID)
	if p.Job != nil && p.Job.Status == studio.JobRunning {
		t.Fatal("offline: a stage started")
	}
	if p.Scenes[0].Frame.Status != studio.StatusDone {
		t.Fatalf("offline: the frame changed state: %s", p.Scenes[0].Frame.Status)
	}
	var f frames
	f.run(1, 800, 600, core.Frame(c, h, func(gtx C) D { return D{Size: gtx.Constraints.Max} }))
	if c.Th.Toasts() <= before {
		t.Fatal("offline refusals must tell the user")
	}
}

// sixty is a view of the vertical demo project stretched to 60 scenes.
func sixty(pv *core.ProjectView) *core.ProjectView {
	return fakeView(pv, func(p *studio.Project) {
		base := append([]studio.Scene(nil), p.Scenes...)
		for len(p.Scenes) < 60 {
			s := base[len(p.Scenes)%len(base)]
			s.ID = fmt.Sprintf("x%02d", len(p.Scenes))
			p.Scenes = append(p.Scenes, s)
		}
	})
}

func TestSixtyScenesVirtualisedAndSmooth(t *testing.T) {
	c, h, ids := demo(t)
	pv := sixty(c.Project(ids["vertical"]))
	for _, tc := range []struct {
		name string
		v    core.StepView
		list func() *pageList
	}{
		{"storyboard", nil, nil},
		{"render", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var v core.StepView
			var list *pageList
			if tc.name == "storyboard" {
				sv := newStoryboard()
				v, list = sv, &sv.state(pv.ID).list
			} else {
				rv := newRender()
				v, list = rv, &rv.state(pv.ID).list
			}
			draw := core.Frame(c, h, func(gtx C) D { return v.Layout(gtx, c, pv) })
			var f frames
			f.run(8, 1400, 1000, draw) // decode the visible pictures, settle the entry
			spent := f.run(30, 1400, 1000, draw)
			avg := spent / 30
			t.Logf("%s: %v per frame, %d of the page's elements built", tc.name, avg, len(list.heights))
			if len(list.heights) > 8 {
				t.Fatalf("not virtualised: %d elements laid out", len(list.heights))
			}
			if avg > 16*time.Millisecond {
				t.Fatalf("too slow for 60 fps: %v per frame", avg)
			}
			// Jump to scene 50: the page glides there and the row lands at the top.
			c.UI(pv.ID).ScrollTo = pv.P.Scenes[50].ID
			f.run(45, 1400, 1000, draw)
			if c.UI(pv.ID).ScrollTo != "" {
				t.Fatal("ScrollTo must be cleared")
			}
			found := false
			for el, y := range list.ys {
				if y >= 0 && y <= 16 && el > 3 {
					found = true
				}
			}
			if !found {
				t.Fatalf("no row settled at the top after the jump: %v", list.ys)
			}
		})
	}
}
