package queue

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Pictures a running job's card shows at most; the last place becomes «ще
// N» when there are more.
const (
	tileCap      = 24
	tileCapPhone = 12
)

// tile is one picture of a running job's card: a scene (or, for the
// references, a cast member or a location) and its state within the job.
type tile struct {
	id    string
	scene bool // a scene: the tile opens it in the project
	num   int  // scene number, 1-based (0 for references)
	name  string
	img   string // absolute picture path ("" = placeholder)
	ver   int64
	icon  *gui.Icon // placeholder
	state gui.AssetState
	pct   int    // running percent (-1 = not measured yet)
	note  string // pill text instead of the state's own ("Немає кадру")
	step  int    // the project step that shows it
}

// jobGrid is what a running job makes.
type jobGrid struct {
	tiles []tile
	ratio float32 // picture width/height
	title string  // "Сцени", "Персонажі й локації"
	done  int     // tiles that are ready
	info  string  // a line instead of the pictures (nothing to show yet)
}

// gridOf lists what job it makes in the project: frames → each scene's
// frame, videos → its clip, voices → its voice, render and the autopilot →
// the scene as a whole, references → the cast and the locations (also
// while a frames job or the autopilot draws those first). Other jobs
// (montage, cover, translation) show the scenes as they are.
func gridOf(pv *core.ProjectView, it studio.QueueItem) jobGrid {
	if pv == nil {
		return jobGrid{}
	}
	p := &pv.P
	refsBusy := false
	for _, ch := range p.Characters {
		refsBusy = refsBusy || busy(ch.Image)
	}
	for _, l := range p.Locations {
		refsBusy = refsBusy || busy(l.Image)
	}
	switch {
	case it.Kind == studio.JobRefs, refsBusy && (it.Kind == studio.JobFrames || it.Kind == studio.JobAutopilot):
		return refsGrid(pv)
	case it.Kind == studio.JobPlan:
		return jobGrid{info: "Студія пише план — персонажів, локації й сцени. Вони з'являться тут, щойно план буде готовий."}
	case len(p.Scenes) == 0:
		switch it.Kind {
		case studio.JobAutopilot:
			return jobGrid{info: "Автопілот пише план — сцени з'являться тут, щойно він буде готовий."}
		case "translate":
			return jobGrid{info: "Перекладаємо історію — сцени з'являться тут."}
		case "transcribe":
			return jobGrid{info: "Розпізнаємо твою озвучку й ділимо її на сцени — вони з'являться тут."}
		}
		return jobGrid{}
	}
	g := jobGrid{ratio: aspectRatio(p.Input.Aspect), title: "Сцени"}
	for i, s := range p.Scenes {
		t := tile{id: s.ID, scene: true, num: i + 1, name: strings.TrimSpace(s.Title), icon: gui.IconImage, step: 4}
		pic := frameOf(s)
		t.img, t.ver = pv.Abs(pic.Path), pic.UpdatedAt
		framed := s.Frame.Path != ""
		speaks := p.Input.Voiceover && spoken(s)
		switch it.Kind {
		case studio.JobFrames:
			t.state, t.pct = inJob(frameOf(s), true)
			t.step = 3
		case studio.JobVideos:
			t.state, t.pct = inJob(s.Video, framed)
			if t.state == gui.StateEmpty {
				t.note = "Немає кадру"
			}
		case studio.JobVoices:
			t.state, t.pct = inJob(s.Audio, speaks)
			if t.state == gui.StateEmpty {
				t.note = "Без озвучки"
			}
		case studio.JobRender:
			t.state, t.pct = pipeline(part{s.Video, framed}, part{s.Audio, speaks})
			if t.state == gui.StateEmpty {
				t.note = "Немає кадру"
			}
		case studio.JobAutopilot:
			t.state, t.pct = pipeline(part{frameOf(s), true}, part{s.Video, true}, part{s.Audio, speaks})
			if !set(s.Video) {
				t.step = 3
			}
		default:
			t.state, t.pct = core.AssetState(advanced(s))
			if !set(s.Video) {
				t.step = 3
			}
		}
		if t.state == gui.StateDone || t.state == gui.StateStale {
			g.done++
		}
		g.tiles = append(g.tiles, t)
	}
	return g
}

// refsGrid lists the cast and the locations of a project.
func refsGrid(pv *core.ProjectView) jobGrid {
	g := jobGrid{ratio: 1, title: "Персонажі й локації"}
	add := func(id, name string, a studio.Asset, icon *gui.Icon) {
		t := tile{id: id, name: strings.TrimSpace(name), img: pv.Abs(a.Path), ver: a.UpdatedAt, icon: icon, step: 3}
		t.state, t.pct = inJob(a, true)
		if t.state == gui.StateDone || t.state == gui.StateStale {
			g.done++
		}
		g.tiles = append(g.tiles, t)
	}
	for _, ch := range pv.P.Characters {
		add(ch.ID, ch.Name, ch.Image, gui.IconUserRound)
	}
	for _, l := range pv.P.Locations {
		add(l.ID, l.Name, l.Image, gui.IconMapPin)
	}
	return g
}

// inJob is an asset's state within a running job that makes it: one the
// job has not reached yet waits «У черзі» (when the job makes it at all).
func inJob(a studio.Asset, makes bool) (gui.AssetState, int) {
	switch a.Status {
	case studio.StatusRunning:
		return gui.StateRunning, measured(a.Progress)
	case studio.StatusQueued:
		return gui.StateQueued, -1
	case studio.StatusError:
		return gui.StateError, -1
	case studio.StatusDone:
		if a.Stale {
			return gui.StateStale, -1
		}
		return gui.StateDone, -1
	}
	if makes {
		return gui.StateQueued, -1
	}
	return gui.StateEmpty, -1
}

// part is one asset of a scene and whether the running job makes it.
type part struct {
	a     studio.Asset
	makes bool
}

// pipeline is a scene's state within a job that makes several of its
// assets: generating while any of them is (with the clip's percent),
// failed when one failed, ready when all are, else waiting its turn.
func pipeline(parts ...part) (gui.AssetState, int) {
	made, running, pct := false, false, -1
	for _, p := range parts {
		if !p.makes {
			continue
		}
		made = true
		if p.a.Status == studio.StatusRunning {
			running = true
			if m := measured(p.a.Progress); m >= 0 && pct < 0 {
				pct = m
			}
		}
	}
	switch {
	case !made:
		return gui.StateEmpty, -1
	case running:
		return gui.StateRunning, pct
	}
	queued, failed, done := false, false, true
	for _, p := range parts {
		if !p.makes {
			continue
		}
		queued = queued || p.a.Status == studio.StatusQueued
		failed = failed || p.a.Status == studio.StatusError
		done = done && p.a.Status == studio.StatusDone
	}
	switch {
	case queued:
		return gui.StateQueued, -1
	case failed:
		return gui.StateError, -1
	case done:
		return gui.StateDone, -1
	}
	return gui.StateQueued, -1
}

// measured is a progress worth showing (-1: images and voices report none
// until they are done).
func measured(pct int) int {
	if pct <= 0 {
		return -1
	}
	return min(100, pct)
}

func busy(a studio.Asset) bool {
	return a.Status == studio.StatusRunning || a.Status == studio.StatusQueued
}

// set reports an asset that exists or is on its way.
func set(a studio.Asset) bool { return a.Status != "" && a.Status != studio.StatusEmpty }

// frameOf is a scene's picture: its frame, or the 3×3 storyboard sheet.
func frameOf(s studio.Scene) studio.Asset {
	if !set(s.Frame) && set(s.Board) {
		return s.Board
	}
	return s.Frame
}

// advanced is a scene's most advanced asset: the clip, else the frame.
func advanced(s studio.Scene) studio.Asset {
	if set(s.Video) {
		return s.Video
	}
	return frameOf(s)
}

// spoken: the scene has words to voice (or a slice of the user's own
// recording).
func spoken(s studio.Scene) bool {
	return strings.TrimSpace(s.Narration) != "" || s.AudioSource == studio.AudioUpload
}

func aspectRatio(aspect string) float32 {
	if aspect == "9:16" {
		return 9.0 / 16
	}
	return 16.0 / 9
}

// window picks the tiles a card shows: all when they fit, else limit-1 of
// them starting a little before the first one being made (a «ще N» tile
// stands for the rest).
func window(ts []tile, limit int) ([]tile, int) {
	if len(ts) <= limit {
		return ts, 0
	}
	k := max(1, limit-1)
	first, queued := -1, -1
	for i, t := range ts {
		if t.state == gui.StateRunning && first < 0 {
			first = i
		}
		if t.state == gui.StateQueued && queued < 0 {
			queued = i
		}
	}
	if first < 0 {
		first = max(0, queued)
	}
	start := max(0, min(first-2, len(ts)-k))
	return ts[start : start+k], len(ts) - k
}

// tileMin is the least width of a picture in the grid.
func tileMin(ratio float32, compact bool) unit.Dp {
	switch {
	case ratio < 1 && compact:
		return 92
	case ratio < 1:
		return 140
	case ratio == 1 && compact:
		return 92
	case ratio == 1:
		return 128
	case compact:
		return 132
	}
	return 180
}

// ---- card ----------------------------------------------------------------------------------------

// jobCard is a running project job, expanded: the project's picture, what
// runs, for which project and how far, «Відкрити проєкт» and «Зупинити»,
// then the scenes (or the references) it makes, each with its own state —
// a click opens that scene in the project.
func (v *View) jobCard(gtx C, c *core.Ctx, it studio.QueueItem) D {
	th := c.Th
	st := v.state(it.Key)
	v.handle(gtx, c, it, st)
	in := st.in.Animate(gtx, 1)
	g := gridOf(c.Project(it.ProjectID), it)
	return gui.FadeUp(gtx, in, 8, func(gtx C) D {
		return gui.Card{Padding: cardPad(gtx)}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			ws := []layout.Widget{func(gtx C) D { return v.jobHead(gtx, c, it, st) }}
			switch {
			case len(g.tiles) > 0:
				ws = append(ws, func(gtx C) D { return v.jobTiles(gtx, c, it, st, g) })
			case g.info != "":
				ws = append(ws, func(gtx C) D { return infoLine(gtx, th, g.info) })
			}
			return gui.VStack(gtx, gui.Sp20, ws...)
		})
	})
}

// jobHead: the picture, the work and the project, the live progress and
// the message, the actions on the right (under it all on phones).
func (v *View) jobHead(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState) D {
	th := c.Th
	narrow := gtx.Constraints.Max.X < gtx.Dp(560)
	thumb := func(gtx C) D { return thumbBox(gtx, c, &st.img, it.Thumb, kindIcon(it), narrow) }
	text := func(gtx C) D {
		ch := []layout.FlexChild{
			layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, it.Label)),
			layout.Rigid(layout.Spacer{Height: 2}.Layout),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, meta(it, false))),
			layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
			layout.Rigid(func(gtx C) D { return jobBar(gtx, th, it, st) }),
		}
		if msg := message(it); msg != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
				layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, msg)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
	acts := func(gtx C) D { return v.jobActions(gtx, c, it, st) }
	if narrow {
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(thumb),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Flexed(1, text),
				)
			},
			func(gtx C) D { return layout.W.Layout(gtx, acts) },
		)
	}
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(thumb),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Flexed(1, text),
		layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
		layout.Rigid(acts),
	)
}

// jobBar is the job's progress: an animated bar and the percentage, or a
// sweeping bar while nothing is measured yet.
func jobBar(gtx C, th *gui.Theme, it studio.QueueItem, st *rowState) D {
	if indeterminate(it) {
		return gui.ProgressBar{Value: -1, Height: 8, Tone: gui.ToneRunning}.Layout(gtx, th)
	}
	pct := max(0, min(100, it.Progress))
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			st.bar.Duration, st.bar.Ease = gui.DurValue, gui.EaseOutCubic
			return gui.ProgressBar{Value: st.bar.Animate(gtx, float32(pct)/100), Height: 8, Tone: gui.ToneRunning}.Layout(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), MaxLines: 1}.Widget(th, fmt.Sprintf("%d%%", pct))),
	)
}

func (v *View) jobActions(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState) D {
	th := c.Th
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
		func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconArrowUpRight, Text: "Відкрити проєкт"}.Layout(gtx, th, &st.open)
		},
		func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconCircleStop, Text: "Зупинити",
				Tip: "Зупинити цей етап — готові частини збережуться"}.Layout(gtx, th, &st.stop)
		},
	)
}

// jobTiles: the caption ("Сцени · готово 1 з 8") over the pictures, in as
// many rows as they need, up to tileCap.
func (v *View) jobTiles(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState, g jobGrid) D {
	th := c.Th
	compact := gtx.Constraints.Max.X < gtx.Dp(560)
	limit, gap := tileCap, gui.Sp12
	if compact {
		limit, gap = tileCapPhone, gui.Sp8
	}
	shown, hidden := window(g.tiles, limit)
	n := len(shown)
	if hidden > 0 {
		n++
	}
	caption := fmt.Sprintf("%s · готово %d з %d", g.title, g.done, len(g.tiles))
	return gui.VStack(gtx, gui.Sp12,
		gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, caption),
		func(gtx C) D {
			return gui.Grid{MinWidth: tileMin(g.ratio, compact), Gap: gap}.Layout(gtx, n, func(gtx C, i int) D {
				if i == len(shown) {
					return v.restTile(gtx, c, it, st, hidden, g.ratio)
				}
				t := shown[i]
				return v.tile(gtx, c, it, t, st.tile(t.id), g.ratio)
			})
		},
	)
}

// tile is one picture of a job's card: the frame (or the cast member, the
// location) with its state as a pill and a thin bar while it renders, its
// number and name under it. A click opens it in the project.
func (v *View) tile(gtx C, c *core.Ctx, it studio.QueueItem, t tile, ts *tileState, ratio float32) D {
	th := c.Th
	if ts.click.Clicked(gtx) {
		r := core.Route{Page: core.PageProject, ProjectID: it.ProjectID, Step: t.step}
		if t.scene {
			r.Scene = t.id
		}
		c.Navigate(r)
	}
	ts.hov.Duration = gui.DurHover
	hov := ts.hov.Animate(gtx, b2f(ts.click.Hovered()))
	if ts.click.Hovered() {
		th.Tooltip(gtx, &ts.click, ts.click.Pressed(), tileTip(t))
	}
	w := gtx.Constraints.Max.X
	return ts.click.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		pg := gtx
		pg.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Constraints.Max.Y)}
		pd := ts.img.Layout(pg, th, c.Img, t.img, t.ver, gui.Thumb{Ratio: ratio, Radius: 12, Placeholder: t.icon,
			Loading: t.img == "" && t.state == gui.StateRunning,
			Overlay: func(gtx C) D { return tileOverlay(gtx, th, t, ts) }})
		r := image.Rectangle{Max: pd.Size}
		if hov > 0.01 {
			gui.Border(gtx, r, 12, 2, gui.WithAlpha(th.Pal.Action, hov))
		}
		if gtx.Focused(&ts.click.Clickable) {
			gui.FocusRing(gtx, th, r, 12, 1)
		}
		top := pd.Size.Y + gtx.Dp(8)
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Dp(48))}
		off := op.Offset(image.Pt(0, top)).Push(gtx.Ops)
		cd := tileCaption(cg, th, t, hov)
		off.Pop()
		return D{Size: image.Pt(w, top+cd.Size.Y)}
	})
}

func tileTip(t tile) string {
	if t.scene {
		return fmt.Sprintf("Відкрити сцену %d", t.num)
	}
	return "Відкрити в проєкті"
}

// tileOverlay puts the state pill in the picture's corner and, while the
// clip renders, a thin progress bar along its bottom.
func tileOverlay(gtx C, th *gui.Theme, t tile, ts *tileState) D {
	size := gtx.Constraints.Max
	m := gtx.Dp(8)
	if size.X < gtx.Dp(120) {
		m = gtx.Dp(6)
	}
	room := max(0, size.X-2*m)
	pill := gui.StatusPill{State: t.state, Percent: t.pct, Text: t.note, Small: true}
	switch {
	case t.state == gui.StateStale:
		pill.Text = "Змінено"
	case t.state == gui.StateRunning && room < gtx.Dp(140):
		// Narrow pictures: the spinner says "generating", the number how far.
		pill.Text = " "
		if t.pct >= 0 {
			pill.Text = fmt.Sprintf("%d%%", t.pct)
		}
	}
	func() {
		defer op.Offset(image.Pt(m, m)).Push(gtx.Ops).Pop()
		pg := gtx
		pg.Constraints = layout.Constraints{Max: image.Pt(room, size.Y)}
		pill.Layout(pg, th)
	}()
	if t.state == gui.StateRunning && t.pct >= 0 {
		ts.bar.Duration, ts.bar.Ease = gui.DurValue, gui.EaseOutCubic
		val := ts.bar.Animate(gtx, float32(t.pct)/100)
		bh := gtx.Dp(4)
		defer op.Offset(image.Pt(m, size.Y-m-bh)).Push(gtx.Ops).Pop()
		bg := gtx
		bg.Constraints = layout.Exact(image.Pt(room, bh))
		gui.ProgressBar{Value: val, Height: 4, Tone: gui.ToneRunning, Track: gui.WithAlpha(th.Pal.Card, 0.75)}.Layout(bg, th)
	}
	return D{Size: size}
}

// tileCaption is the line under a picture: the scene's number and title,
// or the reference's name.
func tileCaption(gtx C, th *gui.Theme, t tile, hov float32) D {
	ink := gui.LerpColor(th.Pal.Text, th.Pal.ActionText, hov)
	switch {
	case !t.scene:
		name := t.name
		if name == "" {
			name = "Без назви"
		}
		return gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: ink, MaxLines: 1}.Layout(gtx, th, name)
	case t.name == "":
		return gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: ink, MaxLines: 1}.Layout(gtx, th, fmt.Sprintf("Сцена %d", t.num))
	}
	return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
		layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontSemiBold), Color: ink, MaxLines: 1}.Widget(th, fmt.Sprint(t.num))),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, t.name)),
	)
}

// restTile stands for the pictures the card leaves out: «ще N», opening
// the project.
func (v *View) restTile(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState, n int, ratio float32) D {
	th := c.Th
	if st.rest.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: it.ProjectID, Step: it.Step})
	}
	w := gtx.Constraints.Max.X
	h := int(float32(w)/ratio + 0.5)
	hov := b2f(st.rest.Hovered())
	return st.rest.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		r := image.Rect(0, 0, w, h)
		gui.FillRRect(gtx, r, 12, gui.LerpColor(th.Pal.Surface, th.Pal.SurfaceHover, hov))
		if gtx.Focused(&st.rest.Clickable) {
			gui.FocusRing(gtx, th, r, 12, 1)
		}
		lg := gtx
		lg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-gtx.Dp(16)), h)}
		ld, lc := record(lg, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, fmt.Sprintf("ще %d", n))),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "у проєкті")),
			)
		})
		off := op.Offset(image.Pt((w-ld.Size.X)/2, (h-ld.Size.Y)/2)).Push(gtx.Ops)
		lc.Add(gtx.Ops)
		off.Pop()
		top := h + gtx.Dp(8)
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(w, gtx.Dp(48))}
		off = op.Offset(image.Pt(0, top)).Push(gtx.Ops)
		cd := gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.ActionText, MaxLines: 1}.Layout(cg, th, "Відкрити всі")
		off.Pop()
		return D{Size: image.Pt(w, top+cd.Size.Y)}
	})
}

// infoLine stands in for the pictures while there are none yet.
func infoLine(gtx C, th *gui.Theme, text string) D {
	return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.Spinner{Size: 18, Color: th.Pal.RunningInk}.Widget(th)),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, text)),
		)
	})
}

// singlesCard holds the single pictures, clips and voices that generate
// outside a job: a row each, with the picture and the live progress.
func (v *View) singlesCard(gtx C, c *core.Ctx, items []studio.QueueItem) D {
	th := c.Th
	return gui.Card{Padding: cardPad(gtx)}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ws := []layout.Widget{func(gtx C) D {
			return gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th,
				"Окремі кадри, відео й озвучка · "+fmt.Sprint(len(items)))
		}}
		for i, it := range items {
			it, last := it, i == len(items)-1
			ws = append(ws, func(gtx C) D { return v.histRow(gtx, c, it, !last) })
		}
		return gui.VStack(gtx, gui.Sp4, ws...)
	})
}

// thumbBox is a row's 16:9 picture: 120×68 dp (96×54 when small).
func thumbBox(gtx C, c *core.Ctx, img *gui.ImageView, path string, icon *gui.Icon, small bool) D {
	w := gtx.Dp(120)
	if small {
		w = gtx.Dp(96)
	}
	sz := image.Pt(w, w*9/16)
	cg := gtx
	cg.Constraints = layout.Exact(sz)
	img.Layout(cg, c.Th, c.Img, path, 0, gui.Thumb{Ratio: 16.0 / 9, Radius: 12, Placeholder: icon})
	return D{Size: sz}
}
