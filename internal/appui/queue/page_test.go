package queue

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

func done(path string) studio.Asset { return studio.Asset{Status: studio.StatusDone, Path: path} }

func TestTileStates(t *testing.T) {
	cases := []struct {
		a     studio.Asset
		makes bool
		state gui.AssetState
		pct   int
	}{
		{studio.Asset{Status: studio.StatusRunning}, true, gui.StateRunning, -1},
		{studio.Asset{Status: studio.StatusRunning, Progress: 40}, true, gui.StateRunning, 40},
		{studio.Asset{Status: studio.StatusQueued}, true, gui.StateQueued, -1},
		{studio.Asset{Status: studio.StatusError}, true, gui.StateError, -1},
		{done("a.png"), true, gui.StateDone, -1},
		{studio.Asset{Status: studio.StatusDone, Path: "a.png", Stale: true}, true, gui.StateStale, -1},
		{studio.Asset{Status: studio.StatusEmpty}, true, gui.StateQueued, -1},
		{studio.Asset{}, false, gui.StateEmpty, -1},
	}
	for i, tc := range cases {
		if s, p := inJob(tc.a, tc.makes); s != tc.state || p != tc.pct {
			t.Errorf("inJob #%d = %v %d, want %v %d", i, s, p, tc.state, tc.pct)
		}
	}
	run := studio.Asset{Status: studio.StatusRunning, Progress: 30}
	empty := studio.Asset{Status: studio.StatusEmpty}
	fail := studio.Asset{Status: studio.StatusError}
	pcases := []struct {
		parts []part
		state gui.AssetState
		pct   int
	}{
		{[]part{{run, true}, {empty, true}}, gui.StateRunning, 30},
		{[]part{{studio.Asset{Status: studio.StatusRunning}, true}, {empty, true}}, gui.StateRunning, -1},
		{[]part{{done("v"), true}, {done("a"), true}}, gui.StateDone, -1},
		{[]part{{done("v"), true}, {fail, true}}, gui.StateError, -1},
		{[]part{{empty, true}, {done("a"), true}}, gui.StateQueued, -1},
		{[]part{{done("v"), true}, {empty, false}}, gui.StateDone, -1},
		{[]part{{empty, false}, {empty, false}}, gui.StateEmpty, -1},
	}
	for i, tc := range pcases {
		if s, p := pipeline(tc.parts...); s != tc.state || p != tc.pct {
			t.Errorf("pipeline #%d = %v %d, want %v %d", i, s, p, tc.state, tc.pct)
		}
	}

	// The window keeps the scene being made in view.
	ts := make([]tile, 30)
	for i := range ts {
		ts[i] = tile{num: i + 1, state: gui.StateDone}
	}
	if shown, hidden := window(ts[:10], 24); len(shown) != 10 || hidden != 0 {
		t.Fatalf("short list: %d shown, %d hidden", len(shown), hidden)
	}
	ts[20].state = gui.StateRunning
	shown, hidden := window(ts, 24)
	if len(shown) != 23 || hidden != 7 {
		t.Fatalf("window: %d shown, %d hidden", len(shown), hidden)
	}
	found := false
	for _, s := range shown {
		found = found || s.num == 21
	}
	if !found {
		t.Fatalf("the running scene is not in view: %d…%d", shown[0].num, shown[len(shown)-1].num)
	}
}

func TestGridOf(t *testing.T) {
	p := studio.Project{Input: studio.Input{Aspect: "9:16", Voiceover: true}}
	p.Scenes = []studio.Scene{
		{ID: "s1", Title: "Ранок", Narration: "Раз", Frame: done("f1.png"), Video: done("v1.mp4"), Audio: done("a1.mp3")},
		{ID: "s2", Narration: "Два", Frame: done("f2.png"), Video: studio.Asset{Status: studio.StatusRunning, Progress: 40}},
		{ID: "s3", Narration: "Три", Frame: done("f3.png"), Video: studio.Asset{Status: studio.StatusEmpty}},
		{ID: "s4", Frame: studio.Asset{Status: studio.StatusEmpty}, Video: studio.Asset{Status: studio.StatusEmpty}},
	}
	dir := t.TempDir()
	pv := &core.ProjectView{ID: "p", P: p, OK: true, Dir: dir}
	states := func(g jobGrid) []gui.AssetState {
		var out []gui.AssetState
		for _, t := range g.tiles {
			out = append(out, t.state)
		}
		return out
	}
	eq := func(a, b []gui.AssetState) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	g := gridOf(pv, studio.QueueItem{Kind: studio.JobVideos, Step: 4})
	if want := []gui.AssetState{gui.StateDone, gui.StateRunning, gui.StateQueued, gui.StateEmpty}; !eq(states(g), want) {
		t.Fatalf("videos: %v, want %v", states(g), want)
	}
	if g.done != 1 || g.ratio != 9.0/16 || g.title != "Сцени" || g.tiles[1].pct != 40 || g.tiles[3].note != "Немає кадру" {
		t.Fatalf("videos grid = %+v", g)
	}
	if g.tiles[0].img != filepath.Join(dir, "f1.png") || g.tiles[0].step != 4 || !g.tiles[0].scene || g.tiles[0].num != 1 {
		t.Fatalf("tile = %+v", g.tiles[0])
	}

	g = gridOf(pv, studio.QueueItem{Kind: studio.JobFrames, Step: 3})
	if want := []gui.AssetState{gui.StateDone, gui.StateDone, gui.StateDone, gui.StateQueued}; !eq(states(g), want) || g.tiles[0].step != 3 {
		t.Fatalf("frames: %v (step %d)", states(g), g.tiles[0].step)
	}

	// Render: the clip and the voice together; a scene without words needs
	// only its clip.
	g = gridOf(pv, studio.QueueItem{Kind: studio.JobRender, Step: 4})
	if want := []gui.AssetState{gui.StateDone, gui.StateRunning, gui.StateQueued, gui.StateEmpty}; !eq(states(g), want) {
		t.Fatalf("render: %v, want %v", states(g), want)
	}

	// The autopilot: waiting scenes are queued, the running clip shows.
	g = gridOf(pv, studio.QueueItem{Kind: studio.JobAutopilot, Step: 4})
	if want := []gui.AssetState{gui.StateDone, gui.StateRunning, gui.StateQueued, gui.StateQueued}; !eq(states(g), want) {
		t.Fatalf("autopilot: %v, want %v", states(g), want)
	}
	if g.tiles[3].step != 3 || g.tiles[0].step != 4 {
		t.Fatalf("autopilot steps: %d %d", g.tiles[3].step, g.tiles[0].step)
	}

	// References: the cast and the places; a frames job that draws them
	// first shows them too.
	pv.P.Characters = []studio.Character{{ID: "c1", Name: "Тарас", Image: done("c1.png")}}
	pv.P.Locations = []studio.Location{{ID: "l1", Name: "Поле", Image: studio.Asset{Status: studio.StatusRunning}}}
	for _, k := range []studio.JobKind{studio.JobRefs, studio.JobFrames, studio.JobAutopilot} {
		g = gridOf(pv, studio.QueueItem{Kind: k, Step: 3})
		if want := []gui.AssetState{gui.StateDone, gui.StateRunning}; !eq(states(g), want) || g.ratio != 1 || g.tiles[0].scene || g.tiles[1].name != "Поле" {
			t.Fatalf("%s while the references draw: %+v", k, g)
		}
	}
	if g = gridOf(pv, studio.QueueItem{Kind: studio.JobVideos}); len(g.tiles) != 4 {
		t.Fatalf("videos ignore the references: %d tiles", len(g.tiles))
	}

	// Nothing to show yet: a line instead.
	if g = gridOf(pv, studio.QueueItem{Kind: studio.JobPlan}); len(g.tiles) != 0 || g.info == "" {
		t.Fatalf("plan grid = %+v", g)
	}
	empty := &core.ProjectView{ID: "e", OK: true, Dir: dir}
	if g = gridOf(empty, studio.QueueItem{Kind: studio.JobAutopilot}); len(g.tiles) != 0 || g.info == "" {
		t.Fatalf("autopilot without scenes = %+v", g)
	}
	if g = gridOf(nil, studio.QueueItem{Kind: studio.JobVideos}); len(g.tiles) != 0 || g.info != "" {
		t.Fatalf("missing project = %+v", g)
	}
}

func TestNextOf(t *testing.T) {
	check := func(name string, p studio.Project, step int, text, warn string) {
		t.Helper()
		n, ok := nextOf(&p)
		if step == 0 {
			if ok {
				t.Errorf("%s: finished film listed: %+v", name, n)
			}
			return
		}
		if !ok || n.step != step || !strings.Contains(n.text, text) || !strings.Contains(n.warn, warn) || (warn == "" && n.warn != "") {
			t.Errorf("%s: %+v %v; want step %d %q %q", name, n, ok, step, text, warn)
		}
	}
	check("draft", studio.Project{}, 1, "історія чекає", "")
	p := studio.Project{Input: studio.Input{Text: "Історія", Voiceover: true}}
	check("no plan", p, 2, "план ще не створено", "")
	p.Job = &studio.Job{Kind: studio.JobPlan, Status: studio.JobError}
	check("plan failed", p, 2, "план", "Не вдалося: план")
	p.Job = nil

	p.Scenes = []studio.Scene{
		{ID: "a", Narration: "Раз", Frame: done("a.png")},
		{ID: "b", Narration: "Два", Frame: studio.Asset{Status: studio.StatusError}},
		{ID: "c", Narration: "Три", Frame: done("c.png")},
	}
	check("frames", p, 3, "кадри чекають · готово 2 з 3", "1 кадр з помилкою")

	p.Scenes[1].Frame = done("b.png")
	p.Scenes[0].Video = done("a.mp4")
	p.Scenes[1].Video = studio.Asset{Status: studio.StatusError}
	p.Scenes[2].Video = studio.Asset{Status: studio.StatusError}
	check("videos", p, 4, "відео чекають · готово 1 з 3", "2 відео з помилкою")

	for i := range p.Scenes {
		p.Scenes[i].Video = done("v.mp4")
		p.Scenes[i].Audio = done("a.mp3")
	}
	p.Scenes[2].Audio = studio.Asset{Status: studio.StatusError}
	check("voices", p, 4, "озвучка чекає · готово 2 з 3", "1 озвучка з помилкою")

	p.Scenes[2].Audio = done("c.mp3")
	check("montage", p, 5, "лишився монтаж", "")
	p.Montage.Output = done("film.mp4")
	check("finished", p, 0, "", "")
	p.Montage.Output.Stale = true
	check("changed", p, 5, "перезбери", "")
}

func TestPageClicks(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(2 * time.Second)
	c, h := core.NewHeadless(e)
	defer c.Close()
	f := NewFeed(e, nil)
	defer f.Close()
	v := New(f)

	var router input.Router
	var ops op.Ops
	frame := 0
	draw := func(w, ht int, fn func(gtx C) D) {
		frame++
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Now: gui.SnapshotEpoch.Add(time.Duration(frame) * time.Second),
			Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(w, ht)), Source: router.Source()}
		core.Frame(c, h, fn)(gtx)
		router.Frame(&ops)
	}
	page := func(gtx C) D { return v.Page(gtx, c) }

	// Nothing ever ran: the welcome card with «Нова історія».
	if d := v.data(c); !d.welcome {
		t.Fatal("an empty studio does not welcome")
	}
	draw(1920, 1080, page)
	v.newStory.Click()
	draw(1920, 1080, page)
	if len(h.Sheets) != 1 || h.Sheets[0].Kind != core.SheetNewProject {
		t.Fatalf("sheets = %+v", h.Sheets)
	}

	// A project waiting for its plan: «Продовжити» opens step 2.
	p, err := e.Create("Чекає на план")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Edit(p.ID, func(q *studio.Project) { q.Input.Text = "Історія про лиса." })
	// The engine reports changes asynchronously: wait until the page sees it.
	var d pageData
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		c.Refresh()
		if d = v.data(c); (!d.welcome && len(d.next) == 1 && d.next[0].step == 2) || time.Now().After(deadline) {
			break
		}
	}
	if d.welcome || len(d.next) != 1 || d.next[0].step != 2 {
		t.Fatalf("page data = %+v", d)
	}
	for _, sz := range [][2]int{{1920, 1080}, {1440, 900}, {390, 844}} {
		draw(sz[0], sz[1], page)
	}
	ps := v.projs[p.ID]
	if ps == nil {
		t.Fatal("no project row")
	}
	ps.open.Click()
	draw(1920, 1080, page)
	if r := h.Current(); r.Page != core.PageProject || r.ProjectID != p.ID || r.Step != 2 {
		t.Fatalf("route = %+v", r)
	}

	// A running job's picture opens its scene; «ще N» the project.
	it := studio.QueueItem{Key: "job:test", ProjectID: p.ID, Kind: studio.JobVideos, Step: 4, Status: studio.JobRunning}
	tl := tile{id: "s7", scene: true, num: 7, step: 4, state: gui.StateRunning, pct: 30}
	st := v.state(it.Key)
	ts := st.tile(tl.id)
	pictures := func(gtx C) D {
		return layout.Flex{}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				gtx.Constraints = layout.Exact(image.Pt(160, 320))
				return v.tile(gtx, c, it, tl, ts, 9.0/16)
			}),
			layout.Rigid(func(gtx C) D {
				gtx.Constraints = layout.Exact(image.Pt(160, 320))
				return v.restTile(gtx, c, it, st, 12, 9.0/16)
			}),
		)
	}
	draw(800, 600, pictures)
	ts.click.Click()
	draw(800, 600, pictures)
	if r := h.Current(); r.Page != core.PageProject || r.ProjectID != p.ID || r.Step != 4 || r.Scene != "s7" {
		t.Fatalf("tile route = %+v", r)
	}
	st.rest.Click()
	draw(800, 600, pictures)
	if r := h.Current(); r.Scene != "" || r.Step != 4 || r.ProjectID != p.ID {
		t.Fatalf("«ще N» route = %+v", r)
	}
}
