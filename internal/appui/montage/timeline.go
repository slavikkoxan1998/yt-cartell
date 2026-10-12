package montage

// The timeline editor: a ruler with a playhead, the clip track (frame
// thumbnail strips, width ∝ duration, drag to reorder with an animated
// reflow, trim handles on both edges, a transition chip between clips), a
// voice lane and a music lane. It scrolls horizontally (trackpad,
// Shift+wheel, the bar) and zooms (slider, buttons, Ctrl+wheel, wheel over
// the ruler). Only the clips in view are drawn, so long films stay at 60 fps.
//
// Geometry: every clip is drawn as a box from Start + in/2 to
// Start + Duration − out/2 (in/out = the overlaps of xfade transitions), so
// the boxes tile the film without gaps and a transition chip sits exactly
// on each cut. Box positions are animated in seconds (zooming never
// animates, reordering and trimming glide).

import (
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

// Track geometry (dp).
const (
	gutterDp  = 40
	rulerDp   = 26
	laneGapDp = 8
	clipDp    = 76
	voiceDp   = 30
	musicDp   = 30
	barDp     = 10
	padLDp    = 10 // content padding before 0:00
	padRDp    = 28
	handleDp  = 12 // trim handle hit width
	slopDp    = 6  // movement before a press becomes a drag
	edgeDp    = 56 // auto-scroll zone while dragging
	zoomHiDp  = 240
	chipDp    = 24
	chipGapDp = 14 // room above the clips for the transition chips
	vHandleDp = 9  // voice trim handle zone

	// minVoiceKeep is the least of a voice line trims may leave (seconds).
	minVoiceKeep = 0.3
)

type dragKind uint8

const (
	dragNone dragKind = iota
	dragPending
	dragReorder
	dragTrimL
	dragTrimR
	dragVoice      // pressed on a voice line: a click plays it, a drag moves it
	dragVoiceMove  // moving a voice line (Scene.VoiceShift)
	dragVoiceTrimL // cutting the head of a voice line (VoiceTrimIn, words stay in place)
	dragVoiceTrimR // cutting its tail (VoiceTrimOut)
	dragDead       // a gesture cancelled with Esc: ignored until the release
	dragScrub
	dragBar
)

type dragState struct {
	kind        dragKind
	id          string
	pid         pointer.ID
	x0, x       float32 // press / current x (viewport px)
	grab        float32 // pointer offset inside the lifted clip (px)
	tin0, tout0 float64 // trims at press (seconds)
	tin, tout   float64 // live trims
	vDur        float64 // source length of the trimmed clip
	drop        int     // reorder: target index in the display order
	scroll0     float32
	lastAuto    time.Time

	// A voice line at the press (effective values from the timeline) and live.
	vStart0, vDur0, vShift0 float64
	vTin0, vTout0, vRaw     float64
	vShift, vTin, vTout     float64
}

// voiceKinds are the voice-line drags.
func (d *dragState) voice() bool {
	return d.kind == dragVoiceMove || d.kind == dragVoiceTrimL || d.kind == dragVoiceTrimR
}

// liveVoice is where the dragged line is now (absolute seconds).
func (d *dragState) liveVoice() (start, dur float64) {
	switch d.kind {
	case dragVoiceMove:
		return d.vStart0 + (d.vShift - d.vShift0), d.vDur0
	case dragVoiceTrimL:
		return d.vStart0 + (d.vTin - d.vTin0), d.vRaw - d.vTin - d.vTout0
	case dragVoiceTrimR:
		return d.vStart0, d.vRaw - d.vTin0 - d.vTout
	}
	return d.vStart0, d.vDur0
}

// timeline is the editor's state for one project.
type timeline struct {
	tag, rulerTag, barTag int
	keyTag                int // focused during a drag: Esc cancels it

	zoom                 float32 // dp per second
	auto                 bool    // zoom follows the width ("fit") until the user zooms
	zoomS                gui.Slider
	zoomIn, zoomOut, fit gui.Clickable

	scroll  float32 // target horizontal scroll (px)
	scrollA gui.Anim

	clips map[string]*clipUI
	chips map[string]*gui.Clickable

	addMusic gui.Clickable
	toStep3  gui.Clickable

	drag  dragState
	hover hit
	ctrl  bool // Ctrl seen on the latest pointer event: the wheel zooms

	playhead    float64
	placed      bool // the playhead was put into the first shown scene
	force       bool // the next reveal moves the playhead even inside the scene
	revealScene string
	lastSel     string
	voice       string // scene whose voice was started from the lane

	geo tlGeom

	// pend keeps a just-committed reorder or trim on screen until the
	// engine's refreshed snapshot shows it (no one-frame jump back).
	pend pendingEdit
}

type pendingEdit struct {
	ver             int64    // pv.Ver when committed
	order           []string // reorder: scene ids in the new order
	trim            string   // trim: the scene
	tin, tout, vDur float64
	voice           string // a moved / trimmed voice line: its scene
	vStart, vLen    float64
	// …and its stored placement: a gesture started before the snapshot
	// caught up starts from these, not from the stale timeline.
	vShift, vTin, vTout float64
}

// applyPending lays base out as the pending edit will be.
func (t *timeline) applyPending(base []slot, f *film) []slot {
	p := &t.pend
	switch {
	case len(p.order) > 0:
		byID := make(map[string]slot, len(base))
		for _, s := range base {
			byID[s.id] = s
		}
		out := make([]slot, 0, len(base))
		at := 0.0
		for _, id := range p.order {
			if s, ok := byID[id]; ok {
				s.s0 = at
				out = append(out, s)
				at += s.w
			}
		}
		if len(out) == len(base) {
			return out
		}
	case p.trim != "":
		out := append([]slot(nil), base...)
		shift := 0.0
		for i := range out {
			out[i].s0 += shift
			if out[i].id != p.trim || out[i].k >= len(f.items) {
				continue
			}
			it := &f.items[out[i].k]
			prev := 0.0
			if out[i].k > 0 {
				prev = f.items[out[i].k-1].Overlap
			}
			delta := liveDuration(it, prev, p.vDur, p.tin, p.tout) - it.Duration
			out[i].w = math.Max(1.0/24, out[i].w+delta)
			out[i].holdFrom = clampf(p.vDur-p.tin-p.tout-prev/2, 0, out[i].w)
			shift += delta
		}
		return out
	}
	return base
}

type clipUI struct {
	start, width gui.Anim // seconds
	lift         gui.Anim
	hover        gui.Anim
	init         bool
}

// tlGeom is the geometry of the last frame (for hit tests of this frame's events).
type tlGeom struct {
	ok             bool
	viewW, viewH   int
	pxs            float32 // px per second
	scroll         float32
	contentW       float32
	padL           float32
	videoY, videoH int
	voiceY, voiceH int
	musicY, musicH int
	clips          []clipGeom
	total          float64
}

type clipGeom struct {
	id     string
	k      int     // index in film.items
	x0, x1 float32 // viewport px
	s0, w  float64 // seconds (display)
	voice  image.Rectangle
	// The whole voice file (with what the trims cut), viewport px.
	vFull0, vFull1 float32
}

type hitKind uint8

const (
	hitNone hitKind = iota
	hitClip
	hitTrimL
	hitTrimR
	hitVoice
	hitVoiceL
	hitVoiceR
	hitLane
)

type hit struct {
	kind hitKind
	id   string
}

func (t *timeline) init() {
	t.auto = true
	t.clips = map[string]*clipUI{}
	t.chips = map[string]*gui.Clickable{}
}

func (t *timeline) clipUI(id string) *clipUI {
	u := t.clips[id]
	if u == nil {
		u = &clipUI{}
		t.clips[id] = u
	}
	return u
}

func (t *timeline) chip(id string) *gui.Clickable {
	c := t.chips[id]
	if c == nil {
		c = new(gui.Clickable)
		t.chips[id] = c
	}
	return c
}

// ---- time ↔ x ---------------------------------------------------------------------------------

func (g *tlGeom) x(t float64) float32 { return g.padL + float32(t)*g.pxs - g.scroll }

func (g *tlGeom) t(x float32) float64 {
	if g.pxs <= 0 {
		return 0
	}
	return float64((x + g.scroll - g.padL) / g.pxs)
}

// zoomBounds is the zoom range (dp per second) for a viewport and film.
func zoomBounds(viewDp float32, total float64) (lo, hi, fit float32) {
	usable := max(40, viewDp-padLDp-padRDp)
	fit = zoomHiDp
	if total > 0 {
		fit = usable / float32(total)
	}
	lo = max(0.5, min(fit, 6))
	return lo, zoomHiDp, fit
}

// ---- display order ------------------------------------------------------------------------------

// slot is one clip box as displayed this frame (targets, seconds).
type slot struct {
	id       string
	k        int
	s0, w    float64 // box start and width
	vOff     float64 // voice start relative to the box start
	vDur     float64
	holdFrom float64 // the clip material ends here (relative); the rest holds the last frame
}

// slots returns the committed boxes in film order.
func (f *film) slots() []slot {
	out := make([]slot, len(f.items))
	for k, it := range f.items {
		in := 0.0
		if k > 0 {
			in = f.items[k-1].Overlap
		}
		s0 := it.Start + in/2
		w := it.Duration - in/2 - it.Overlap/2
		out[k] = slot{
			id: it.SceneID, k: k, s0: s0, w: max(w, 1.0/24),
			vOff: it.VoiceStart - s0, vDur: it.VoiceDuration,
			holdFrom: clampf(it.ClipDuration-in/2, 0, max(w, 0)),
		}
	}
	return out
}

// displaySlots applies a reorder drag: the dragged clip leaves the sequence
// and a gap of its width opens at the drop index.
func (t *timeline) displaySlots(base []slot) []slot {
	d := &t.drag
	if d.kind != dragReorder {
		return base
	}
	var moving slot
	rest := make([]slot, 0, len(base))
	for _, s := range base {
		if s.id == d.id {
			moving = s
			continue
		}
		rest = append(rest, s)
	}
	if moving.id == "" {
		return base
	}
	drop := max(0, min(d.drop, len(rest)))
	out := make([]slot, 0, len(base))
	at := 0.0
	for i := 0; i <= len(rest); i++ {
		if i == drop {
			m := moving
			m.s0 = at
			out = append(out, m)
			at += m.w
		}
		if i < len(rest) {
			s := rest[i]
			s.s0 = at
			out = append(out, s)
			at += s.w
		}
	}
	return out
}

// dropIndex is where the lifted clip would land: the number of other clips
// whose centre lies left of the lifted clip's centre.
func (t *timeline) dropIndex(base []slot, centre float64) int {
	n := 0
	at := 0.0
	for _, s := range base {
		if s.id == t.drag.id {
			continue
		}
		if at+s.w/2 < centre {
			n++
		}
		at += s.w
	}
	return n
}

// reorderScenes moves scene id so that the timeline follows order (scene ids
// in the new timeline order); scenes that are not on the timeline keep
// their places relative to their neighbours.
func reorderScenes(scenes []studio.Scene, id string, order []string) []studio.Scene {
	pos := -1
	for i, o := range order {
		if o == id {
			pos = i
		}
	}
	from := -1
	for i := range scenes {
		if scenes[i].ID == id {
			from = i
		}
	}
	if pos < 0 || from < 0 {
		return scenes
	}
	moving := scenes[from]
	out := make([]studio.Scene, 0, len(scenes))
	out = append(out, scenes[:from]...)
	out = append(out, scenes[from+1:]...)
	index := func(sid string) int {
		for i := range out {
			if out[i].ID == sid {
				return i
			}
		}
		return -1
	}
	at := 0
	switch {
	case pos+1 < len(order) && index(order[pos+1]) >= 0:
		at = index(order[pos+1])
	case pos > 0 && index(order[pos-1]) >= 0:
		at = index(order[pos-1]) + 1
	}
	out = append(out, studio.Scene{})
	copy(out[at+1:], out[at:])
	out[at] = moving
	return out
}

// trimLimits: the most that can be cut from one end, given the other.
func trimLimits(vDur, other float64) float64 {
	keep := math.Min(0.5, vDur)
	return math.Max(0, vDur-keep-other)
}

// clipSource is the source length of a scene's material: the clip, or the
// 8 s a still lasts.
func clipSource(it *studio.TimelineItem, sc *studio.Scene) float64 {
	if it.HasClip && sc.Video.Duration > 0 {
		return sc.Video.Duration
	}
	return 8
}

// liveDuration estimates a scene's length after new trims (the engine
// computes it exactly after the edit): clip frames vs. the voice line.
func liveDuration(it *studio.TimelineItem, prevOverlap, vDur, tin, tout float64) float64 {
	clipS := math.Max(1.0/24, vDur-tin-tout)
	length := math.Max(clipS, prevOverlap+it.Overlap+2.0/24)
	if it.VoiceDuration > 0 {
		length = math.Max(length, prevOverlap+0.25+it.Overlap+it.VoiceDuration+0.35)
	}
	return length
}

func snap(v float64) float64 { return math.Round(v*10) / 10 }

// ---- the card --------------------------------------------------------------------------------------

// timelineCard is the editor card: header with zoom, the preview monitor
// with the inspector, then the tracks.
func (s *proj) timelineCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	pad := gui.Sp24
	if gtx.Constraints.Max.X < gtx.Dp(600) {
		pad = gui.Sp16
	}
	return gui.Card{Padding: pad}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if len(s.film.items) == 0 {
			return s.emptyTimeline(gtx, c, pv)
		}
		inner := gtx.Constraints.Max.X
		wide := inner >= gtx.Dp(860)
		header := func(gtx C) D { return s.timelineHeader(gtx, c, pv) }
		tracks := func(gtx C) D { return s.tl.layout(gtx, c, pv, s) }
		mon := func(gtx C) D { return s.monitorBlock(gtx, c, pv, wide) }
		insp := func(gtx C) D { return s.inspectorBlock(gtx, c, pv) }
		if wide {
			return gui.VStack(gtx, gui.Sp20, header, func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(mon),
					layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
					layout.Flexed(1, insp),
				)
			}, tracks)
		}
		return gui.VStack(gtx, gui.Sp16, header, mon, tracks, insp)
	})
}

// emptyTimeline fills the card when there is nothing to montage yet.
func (s *proj) emptyTimeline(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	step, action, icon := 3, "До розкадровки", gui.IconImages
	text := "Спершу згенеруй кадри сцен на кроці «Розкадровка» — або кліпи на кроці «Відео й озвучка». Тоді тут з'явиться таймлайн твого фільму."
	if len(pv.P.Scenes) == 0 {
		step, action, icon = 2, "До плану", gui.IconListOrdered
		text = "У проєкті ще немає сцен. Створи план на кроці «План», а потім кадри — і тут з'явиться таймлайн твого фільму."
	}
	if s.tl.toStep3.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: pv.ID, Step: step})
	}
	return layout.Inset{Top: gui.Sp24, Bottom: gui.Sp24}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return gui.EmptyState{Icon: gui.IconFilm, Title: "Поки що нема чого монтувати", Text: text,
					Action: action, ActionIcon: icon}.Layout(gtx, th, &s.tl.toStep3)
			}),
		)
	})
}

// timelineHeader: title + summary, warnings, zoom controls.
func (s *proj) timelineHeader(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	t := &s.tl
	f := &s.film
	meta := count(len(f.items), "сцена", "сцени", "сцен") + " · " + core.Clock(f.total) + " · " + pv.P.Input.Aspect
	if f.stills > 0 {
		meta += " · " + count(f.stills, "кадр", "кадри", "кадрів") + " з рухом"
	}
	narrow := gtx.Constraints.Max.X < gtx.Dp(560)
	title := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Таймлайн")),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, meta)),
		)
	}
	zoom := func(gtx C) D { return t.zoomControls(gtx, th) }
	warn := func(gtx C) D {
		if f.hidden == 0 {
			return D{}
		}
		return gui.Chip{Text: count(f.hidden, "сцена", "сцени", "сцен") + " без кадру — не у фільмі", Dot: th.Pal.Warning, Small: true,
			Tip: "У таких сцен немає ні кадру, ні кліпу — монтаж їх пропустить. Згенеруй кадри на кроці «Розкадровка»."}.Layout(gtx, th, nil)
	}
	if narrow {
		return gui.VStack(gtx, gui.Sp12, title, func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, warn, zoom) })
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, title),
		layout.Rigid(warn),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Rigid(zoom),
	)
}

// zoomControls: − slider + and "fit".
func (t *timeline) zoomControls(gtx C, th *gui.Theme) D {
	g := &t.geo
	lo, hi, fit := zoomBounds(float32(g.viewW)/gtx.Metric.PxPerDp, g.total)
	cur := t.zoom
	if cur <= 0 {
		cur = clamp32(fit, 8, 60)
	}
	setZoom := func(z float32) {
		t.auto = false
		t.applyZoom(gtx, clamp32(z, lo, hi), -1)
	}
	if t.zoomIn.Clicked(gtx) {
		setZoom(cur * 1.5)
	}
	if t.zoomOut.Clicked(gtx) {
		setZoom(cur / 1.5)
	}
	if t.fit.Clicked(gtx) {
		t.auto = false
		t.applyZoom(gtx, clamp32(fit, lo, hi), -1)
		t.scroll = 0
	}
	if v, ok := t.zoomS.Changed(gtx); ok {
		setZoom(lo * float32(math.Pow(float64(hi/lo), float64(v))))
	}
	sv := float32(0)
	if hi > lo {
		sv = float32(math.Log(float64(cur/lo)) / math.Log(float64(hi/lo)))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return gui.IconButton{Icon: gui.IconMinus, Size: 32, Tip: "Віддалити"}.Layout(gtx, th, &t.zoomOut)
		}),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(120), gtx.Dp(120)
			return layout.Inset{Left: gui.Sp4, Right: gui.Sp4}.Layout(gtx, func(gtx C) D {
				return t.zoomS.Layout(gtx, th, gui.SliderStyle{Value: clamp32(sv, 0, 1), Min: 0, Max: 1,
					Format: func(v float32) string {
						z := lo * float32(math.Pow(float64(hi/lo), float64(v)))
						return zoomText(float32(g.viewW)/gtx.Metric.PxPerDp, z)
					}})
			})
		}),
		layout.Rigid(func(gtx C) D {
			return gui.IconButton{Icon: gui.IconPlus, Size: 32, Tip: "Наблизити"}.Layout(gtx, th, &t.zoomIn)
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.IconButton{Icon: gui.IconMaximize2, Size: 32, Tip: "Показати весь фільм"}.Layout(gtx, th, &t.fit)
		}),
	)
}

// zoomText describes a zoom level for the slider bubble: how much of the
// film fits the view.
func zoomText(viewDp, dpPerSec float32) string {
	sec := float64(viewDp / max(dpPerSec, 0.01))
	if sec < 60 {
		return "у вікні ≈ " + secText(sec)
	}
	return "у вікні ≈ " + core.Clock(sec)
}

// applyZoom sets the zoom keeping the time under anchor (viewport px; < 0 =
// the playhead when visible, else the centre) in place.
func (t *timeline) applyZoom(gtx C, z float32, anchor float32) {
	g := &t.geo
	if !g.ok {
		t.zoom = z
		return
	}
	if anchor < 0 {
		anchor = float32(g.viewW) / 2
		if x := g.x(t.playhead); x >= 0 && x <= float32(g.viewW) {
			anchor = x
		}
	}
	at := g.t(anchor)
	t.zoom = z
	pxs := z * gtx.Metric.PxPerDp
	t.scroll = g.padL + float32(at)*pxs - anchor
	t.scrollA.Set(t.scroll)
}

// ---- tracks ----------------------------------------------------------------------------------------

// layout draws the ruler, the lanes and the scrollbar and handles their input.
func (t *timeline) layout(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj) D {
	th := c.Th
	f := &s.film
	W := gtx.Constraints.Max.X
	gut := gtx.Dp(gutterDp)
	viewW := max(1, W-gut)
	rulerH, gapH := gtx.Dp(rulerDp), gtx.Dp(laneGapDp)
	videoH, voiceH, musicH := gtx.Dp(clipDp), gtx.Dp(voiceDp), gtx.Dp(musicDp)
	barH := gtx.Dp(barDp)
	videoY := rulerH + gtx.Dp(chipGapDp)
	voiceY := videoY + videoH + gapH
	musicY := voiceY + voiceH + gtx.Dp(6)
	viewH := musicY + musicH + gapH
	H := viewH + barH

	// Zoom and scroll bounds for this width.
	lo, hi, fit := zoomBounds(float32(viewW)/gtx.Metric.PxPerDp, f.total)
	if t.auto || t.zoom <= 0 {
		t.zoom = clamp32(fit, max(lo, 8), 60)
		if fit < 8 {
			t.zoom = max(lo, 8)
		}
	}
	t.zoom = clamp32(t.zoom, lo, hi)
	pxs := t.zoom * gtx.Metric.PxPerDp
	padL := float32(gtx.Dp(padLDp))
	contentW := padL + float32(f.total)*pxs + float32(gtx.Dp(padRDp))
	maxScroll := max(0, contentW-float32(viewW))

	// Selection coming from elsewhere (scene navigator) or an explicit reveal.
	sel := c.UI(pv.ID).Scene
	if f.item(sel) == nil && len(f.items) > 0 {
		sel = f.items[0].SceneID
	}
	if sel != t.lastSel && t.lastSel != "" && t.drag.kind == dragNone {
		t.revealScene = sel
	}
	t.lastSel = sel

	base := f.slots()
	if !t.placed && len(base) > 0 {
		t.placed, t.force = true, true
		t.revealScene = sel
	}
	if t.revealScene != "" {
		for _, sl := range base {
			if sl.id == t.revealScene {
				x0 := padL + float32(sl.s0)*pxs
				x1 := x0 + float32(sl.w)*pxs
				if x0 < t.scroll || x1 > t.scroll+float32(viewW) {
					t.scroll = x0 - float32(viewW)/3
				}
				if t.force || t.playhead < sl.s0 || t.playhead >= sl.s0+sl.w {
					t.playhead = intoScene(sl.s0, sl.w)
				}
			}
		}
		t.revealScene, t.force = "", false
	}

	// Input first (against last frame's geometry), then the new geometry.
	t.events(gtx, c, pv, s, base)
	if t.pend.ver != 0 {
		if pv.Ver != t.pend.ver {
			t.pend = pendingEdit{} // the snapshot shows the edit now
		} else {
			base = t.applyPending(base, f)
		}
	}
	if s.mon.playing {
		// Follow the playhead while the draft plays.
		if x := padL + float32(t.playhead)*pxs - t.scroll; x > float32(viewW)*0.9 || x < 0 {
			t.scroll = padL + float32(t.playhead)*pxs - float32(viewW)*0.2
		}
	}
	t.scroll = clamp32(t.scroll, 0, maxScroll)
	t.scrollA.Duration, t.scrollA.Ease = 140*time.Millisecond, gui.EaseOutCubic
	scroll := clamp32(t.scrollA.Animate(gtx, t.scroll), 0, maxScroll)
	if t.drag.kind == dragBar || t.drag.kind == dragReorder || t.drag.kind == dragTrimL || t.drag.kind == dragTrimR || t.drag.voice() {
		t.scrollA.Set(t.scroll)
		scroll = t.scroll
	}
	t.playhead = clampf(t.playhead, 0, f.total)

	g := &t.geo
	*g = tlGeom{ok: true, viewW: viewW, viewH: viewH, pxs: pxs, scroll: scroll, contentW: contentW, padL: padL,
		videoY: videoY, videoH: videoH, voiceY: voiceY, voiceH: voiceH, musicY: musicY, musicH: musicH, total: f.total, clips: g.clips[:0]}

	// Animate the boxes towards this frame's targets (seconds).
	disp := t.displaySlots(base)
	seen := map[string]bool{}
	for _, sl := range disp {
		u := t.clipUI(sl.id)
		seen[sl.id] = true
		u.start.Duration, u.start.Ease = 220*time.Millisecond, gui.EaseInOutCubic
		u.width.Duration, u.width.Ease = 220*time.Millisecond, gui.EaseInOutCubic
		if !u.init {
			u.init = true
			u.start.Set(float32(sl.s0))
			u.width.Set(float32(sl.w))
		}
		s0 := float64(u.start.Animate(gtx, float32(sl.s0)))
		w := float64(u.width.Animate(gtx, float32(sl.w)))
		if t.drag.kind == dragReorder && sl.id == t.drag.id {
			s0 = float64(t.drag.x-t.drag.grab+scroll-padL) / float64(pxs)
			u.start.Set(float32(s0))
		}
		cg := clipGeom{id: sl.id, k: sl.k, s0: s0, w: w, x0: g.x(s0), x1: g.x(s0 + w)}
		if sl.vDur > 0 {
			vs, vd := s0+sl.vOff, sl.vDur
			it := &f.items[sl.k]
			tin, raw := it.VoiceTrimIn, it.VoiceRawDuration
			switch {
			case t.drag.voice() && t.drag.id == sl.id:
				vs, vd = t.drag.liveVoice()
				tin, raw = t.drag.vTin0, t.drag.vRaw
				if t.drag.kind == dragVoiceTrimL {
					tin = t.drag.vTin
				}
			case t.pend.voice == sl.id:
				vs, vd = t.pend.vStart, t.pend.vLen
			}
			vx0 := g.x(vs)
			vx1 := vx0 + float32(vd)*pxs
			cg.voice = image.Rect(int(vx0), voiceY, int(vx1), voiceY+voiceH)
			if raw <= 0 {
				raw, tin = vd, 0
			}
			cg.vFull0 = g.x(vs - tin)
			cg.vFull1 = cg.vFull0 + float32(raw)*pxs
		}
		g.clips = append(g.clips, cg)
	}
	for id := range t.clips {
		if !seen[id] {
			delete(t.clips, id)
		}
	}

	// ---- drawing ----
	// Gutter: lane icons and the playhead time.
	func() {
		lab := core.Clock(t.playhead)
		textAt(gtx, th, 0, (rulerH-gtx.Sp(12)*4/3)/2, gut-gtx.Dp(4), th.Caption.Weighted(gui.FontMedium), th.Pal.Text2, lab)
		ic := func(y, h int, icon *gui.Icon) {
			is := gtx.Dp(18)
			o := op.Offset(pt((gut-gtx.Dp(8)-is)/2, y+(h-is)/2)).Push(gtx.Ops)
			icon.Layout(gtx, 18, th.Pal.Text3)
			o.Pop()
		}
		ic(videoY, videoH, gui.IconFilm)
		ic(voiceY, voiceH, gui.IconMic)
		ic(musicY, musicH, gui.IconMusic)
	}()

	// Viewport (clipped): lanes, ruler, clips, playhead.
	func() {
		defer op.Offset(pt(gut, 0)).Push(gtx.Ops).Pop()
		// Where the tracks are in the window (popovers, tests).
		gui.Anchor(gtx, th, "mont.tracks", func(gtx C) D { return D{Size: pt(viewW, viewH)} })
		defer clip.Rect(image.Rectangle{Max: pt(viewW, viewH)}).Push(gtx.Ops).Pop()
		rad := gtx.Dp(10)
		// Lane wells.
		well := th.Pal.Surface
		fillRR(gtx, image.Rect(0, videoY-gtx.Dp(4), viewW, videoY+videoH+gtx.Dp(4)), rad+gtx.Dp(2), well)
		fillRR(gtx, image.Rect(0, voiceY, viewW, voiceY+voiceH), rad, well)
		fillRR(gtx, image.Rect(0, musicY, viewW, musicY+musicH), rad, well)
		t.drawRuler(gtx, th, rulerH)
		t.drawVoices(gtx, c, pv, s, disp)
		t.drawMusic(gtx, c, pv, s)
		t.drawClips(gtx, c, pv, s, sel)
		t.drawPlayhead(gtx, th, viewH)
		// Input areas: the whole viewport (clips, lanes) and the ruler.
		t.inputAreas(gtx, rulerH)
	}()
	// Transition chips sit on top of the track handler (their own clicks).
	func() {
		defer op.Offset(pt(gut, 0)).Push(gtx.Ops).Pop()
		cl := clip.Rect(image.Rectangle{Min: pt(0, videoY-gtx.Dp(chipGapDp)), Max: pt(viewW, videoY+videoH)}).Push(gtx.Ops)
		t.drawChips(gtx, c, pv, s)
		cl.Pop()
		if pv.P.Montage.MusicPath == "" {
			t.drawAddMusic(gtx, c, pv, s)
		}
	}()
	// Scrollbar.
	func() {
		defer op.Offset(pt(gut, viewH)).Push(gtx.Ops).Pop()
		t.drawBar(gtx, th, viewW, barH, maxScroll)
	}()
	return D{Size: pt(W, H)}
}

// ---- input -------------------------------------------------------------------------------------------

func (t *timeline) scrollYRange() pointer.ScrollRange {
	if t.ctrl {
		return pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}
	}
	return pointer.ScrollRange{}
}

// hitTest finds what is under a viewport point (last frame's geometry).
func (t *timeline) hitTest(gtx C, p f32.Point) hit {
	g := &t.geo
	x, y := p.X, int(p.Y)
	hw := float32(gtx.Dp(handleDp))
	switch {
	case y >= g.videoY-gtx.Dp(4) && y < g.videoY+g.videoH+gtx.Dp(4):
		// The clip under x (the one whose box contains it wins at a cut),
		// else one a few pixels away (the film's first and last edges).
		find := func(slack float32) (clipGeom, bool) {
			for _, cg := range g.clips {
				if x >= cg.x0-slack && x < cg.x1+slack {
					return cg, true
				}
			}
			return clipGeom{}, false
		}
		cg, ok := find(0)
		if !ok {
			if cg, ok = find(hw / 3); !ok {
				return hit{kind: hitLane}
			}
		}
		if cg.x1-cg.x0 >= 3*hw {
			if x >= cg.x1-hw*0.7 {
				return hit{hitTrimR, cg.id}
			}
			if x <= cg.x0+hw*0.7 {
				return hit{hitTrimL, cg.id}
			}
		}
		return hit{hitClip, cg.id}
	case y >= g.voiceY && y < g.voiceY+g.voiceH:
		// The top-most line wins (moved lines may overlap); its edges trim.
		vh := float32(gtx.Dp(vHandleDp))
		for i := len(g.clips) - 1; i >= 0; i-- {
			r := g.clips[i].voice
			x0, x1 := float32(r.Min.X), float32(r.Max.X)
			if r.Empty() || x < x0-vh/3 || x > x1+vh/3 {
				continue
			}
			id := g.clips[i].id
			if x1-x0 >= 3*vh {
				if x <= x0+vh*0.8 {
					return hit{hitVoiceL, id}
				}
				if x >= x1-vh*0.8 {
					return hit{hitVoiceR, id}
				}
			}
			return hit{hitVoice, id}
		}
		return hit{kind: hitLane}
	}
	return hit{kind: hitLane}
}

// events handles the tracks, the ruler and the scrollbar.
func (t *timeline) events(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj, base []slot) {
	g := &t.geo
	d := &t.drag
	slop := float32(gtx.Dp(slopDp))
	id := pv.ID
	// Esc cancels a drag in progress (the focus moves to keyTag when it starts).
	for {
		ev, ok := gtx.Event(key.FocusFilter{Target: &t.keyTag}, key.Filter{Focus: &t.keyTag, Name: key.NameEscape})
		if !ok {
			break
		}
		if e, ok := ev.(key.Event); ok && e.State == key.Press {
			switch d.kind {
			case dragReorder, dragTrimL, dragTrimR, dragVoiceMove, dragVoiceTrimL, dragVoiceTrimR:
				*d = dragState{kind: dragDead, pid: d.pid}
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &t.tag,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Enter | pointer.Leave | pointer.Scroll,
			ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
			ScrollY: t.scrollYRange()})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || !g.ok {
			continue
		}
		t.ctrl = e.Modifiers.Contain(key.ModCtrl)
		switch e.Kind {
		case pointer.Scroll:
			if t.ctrl && e.Scroll.Y != 0 {
				t.wheelZoom(gtx, e.Scroll.Y, e.Position.X)
			} else if e.Scroll.X != 0 {
				t.scroll += e.Scroll.X
			}
		case pointer.Move, pointer.Enter:
			if d.kind == dragNone {
				t.hover = t.hitTest(gtx, e.Position)
			}
		case pointer.Leave:
			if d.kind == dragNone {
				t.hover = hit{}
			}
		case pointer.Press:
			if e.Buttons == pointer.ButtonSecondary {
				// A right click on a voice line puts it back in place.
				if h := t.hitTest(gtx, e.Position); h.kind == hitVoice || h.kind == hitVoiceL || h.kind == hitVoiceR {
					t.resetVoice(c, pv, h.id)
				}
				continue
			}
			if e.Buttons != pointer.ButtonPrimary {
				continue
			}
			h := t.hitTest(gtx, e.Position)
			*d = dragState{pid: e.PointerID, x0: e.Position.X, x: e.Position.X, id: h.id, scroll0: t.scroll}
			s.mon.stop(c)
			switch h.kind {
			case hitTrimL, hitTrimR:
				it := s.film.item(h.id)
				sc := sceneOf(pv, h.id)
				if it == nil || sc == nil {
					d.kind = dragNone
					continue
				}
				d.kind = dragTrimL
				if h.kind == hitTrimR {
					d.kind = dragTrimR
				}
				d.vDur = clipSource(it, sc)
				d.tin0, d.tout0 = it.TrimIn, it.TrimOut
				if pe := &t.pend; pe.trim == h.id && pe.ver == pv.Ver {
					d.tin0, d.tout0 = pe.tin, pe.tout // the last trim is not in the snapshot yet
				}
				d.tin, d.tout = d.tin0, d.tout0
				t.selectScene(c, id, h.id, false)
				gtx.Execute(pointer.GrabCmd{Tag: &t.tag, ID: e.PointerID})
				gtx.Execute(key.FocusCmd{Tag: &t.keyTag})
			case hitClip:
				d.kind = dragPending
				for _, cg := range g.clips {
					if cg.id == h.id {
						d.grab = e.Position.X - cg.x0
					}
				}
			case hitVoice, hitVoiceL, hitVoiceR:
				it := s.film.item(h.id)
				if it == nil || it.VoiceDuration <= 0 {
					d.kind = dragNone
					continue
				}
				d.vStart0, d.vDur0, d.vShift0 = it.VoiceStart, it.VoiceDuration, it.VoiceShift
				d.vTin0, d.vTout0, d.vRaw = it.VoiceTrimIn, it.VoiceTrimOut, it.VoiceRawDuration
				if pe := &t.pend; pe.voice == h.id && pe.ver == pv.Ver {
					// The last gesture on this line is not in the snapshot yet.
					d.vStart0, d.vDur0 = pe.vStart, pe.vLen
					d.vShift0, d.vTin0, d.vTout0 = pe.vShift, pe.vTin, pe.vTout
				}
				if d.vRaw <= 0 {
					d.vRaw = it.VoiceDuration + d.vTin0 + d.vTout0
				}
				d.vShift, d.vTin, d.vTout = d.vShift0, d.vTin0, d.vTout0
				switch h.kind {
				case hitVoiceL, hitVoiceR:
					d.kind = dragVoiceTrimL
					if h.kind == hitVoiceR {
						d.kind = dragVoiceTrimR
					}
					t.selectScene(c, id, h.id, false)
					gtx.Execute(pointer.GrabCmd{Tag: &t.tag, ID: e.PointerID})
					gtx.Execute(key.FocusCmd{Tag: &t.keyTag})
				default:
					d.kind = dragVoice
				}
			default:
				d.kind = dragScrub
				t.playhead = clampf(g.t(e.Position.X), 0, g.total)
			}
		case pointer.Drag:
			if e.PointerID != d.pid {
				continue
			}
			d.x = e.Position.X
			switch d.kind {
			case dragPending:
				if abs32(d.x-d.x0) > slop {
					d.kind = dragReorder
					d.drop = t.indexOf(base, d.id)
					t.selectScene(c, id, d.id, false)
					gtx.Execute(pointer.GrabCmd{Tag: &t.tag, ID: e.PointerID})
					gtx.Execute(key.FocusCmd{Tag: &t.keyTag})
				}
			case dragVoice:
				if abs32(d.x-d.x0) > slop {
					d.kind = dragVoiceMove
					t.selectScene(c, id, d.id, false)
					gtx.Execute(pointer.GrabCmd{Tag: &t.tag, ID: e.PointerID})
					gtx.Execute(key.FocusCmd{Tag: &t.keyTag})
					t.updateVoice()
				}
			case dragVoiceMove, dragVoiceTrimL, dragVoiceTrimR:
				t.updateVoice()
			case dragScrub:
				t.playhead = clampf(g.t(d.x), 0, g.total)
			case dragTrimL, dragTrimR:
				t.updateTrim(gtx)
			}
			if d.kind == dragReorder {
				centre := g.t(d.x-d.grab) + t.widthOf(base, d.id)/2
				d.drop = t.dropIndex(base, centre)
			}
		case pointer.Release, pointer.Cancel:
			if e.PointerID != d.pid {
				continue
			}
			cancel := e.Kind == pointer.Cancel
			switch d.kind {
			case dragPending:
				if !cancel {
					t.selectScene(c, id, d.id, true)
				}
			case dragVoice:
				if !cancel {
					t.selectScene(c, id, d.id, false)
					t.toggleVoice(c, pv, s, d.id)
				}
			case dragVoiceMove, dragVoiceTrimL, dragVoiceTrimR:
				if !cancel {
					t.commitVoice(c, pv)
				}
			case dragReorder:
				if !cancel {
					t.commitReorder(c, pv, base)
				}
			case dragTrimL, dragTrimR:
				if !cancel {
					// Start the glide from what the drag showed: the trimmed
					// material, which then ripples into its new place.
					for _, cg := range g.clips {
						if cg.id != d.id {
							continue
						}
						u := t.clipUI(d.id)
						if d.kind == dragTrimL {
							dt := d.tin - d.tin0
							u.start.Set(float32(cg.s0 + dt))
							u.width.Set(float32(math.Max(cg.w-dt, 1.0/24)))
						} else {
							clip0 := d.vDur - d.tin0 - d.tout0
							u.width.Set(float32(math.Max(clip0-(d.tout-d.tout0), 1.0/24)))
						}
					}
					t.commitTrim(c, pv)
				}
			}
			*d = dragState{}
			t.hover = t.hitTest(gtx, e.Position)
			if gtx.Focused(&t.keyTag) {
				// Esc belongs to the rest of the app again.
				gtx.Execute(key.FocusCmd{})
			}
		}
	}
	// Auto-scroll while dragging a clip or a trim handle near the edges
	// (once the pointer has really moved: a press at the edge does not scroll).
	if g.ok && (d.kind == dragReorder || d.kind == dragTrimL || d.kind == dragTrimR || d.voice()) && abs32(d.x-d.x0) > slop {
		edge := float32(gtx.Dp(edgeDp))
		var v float32
		switch {
		case d.x < edge:
			v = -(edge - d.x) / edge
		case d.x > float32(g.viewW)-edge:
			v = (d.x - (float32(g.viewW) - edge)) / edge
		}
		if v != 0 {
			dt := float32(1.0 / 60)
			if !d.lastAuto.IsZero() {
				dt = float32(min(0.05, gtx.Now.Sub(d.lastAuto).Seconds()))
			}
			d.lastAuto = gtx.Now
			// Clamped here: trims and voice drags count the scrolled distance.
			t.scroll = clamp32(t.scroll+clamp32(v, -1, 1)*float32(gtx.Dp(900))*dt, 0, max(0, g.contentW-float32(g.viewW)))
			switch {
			case d.kind == dragReorder:
				centre := g.t(d.x-d.grab) + t.widthOf(base, d.id)/2
				d.drop = t.dropIndex(base, centre)
			case d.voice():
				t.updateVoice()
			default:
				t.updateTrim(gtx)
			}
			gtx.Execute(op.InvalidateCmd{})
		} else {
			d.lastAuto = time.Time{}
		}
	}
	// Ruler: scrub the playhead; Ctrl+wheel zooms like over the tracks.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &t.rulerTag,
			Kinds:   pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Scroll | pointer.Move,
			ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
			ScrollY: t.scrollYRange()})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || !g.ok {
			continue
		}
		t.ctrl = e.Modifiers.Contain(key.ModCtrl)
		switch e.Kind {
		case pointer.Scroll:
			if t.ctrl && e.Scroll.Y != 0 {
				t.wheelZoom(gtx, e.Scroll.Y, e.Position.X)
			}
			if e.Scroll.X != 0 {
				t.scroll += e.Scroll.X
			}
		case pointer.Press:
			s.mon.stop(c)
			t.playhead = clampf(g.t(e.Position.X), 0, g.total)
		case pointer.Drag:
			t.playhead = clampf(g.t(e.Position.X), 0, g.total)
		}
	}
	// Scrollbar: drag the thumb, press the track to jump.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &t.barTag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || !g.ok {
			continue
		}
		vis := float32(g.viewW) / max(g.contentW, 1)
		thumbW := max(float32(gtx.Dp(40)), float32(g.viewW)*vis)
		travel := max(1, float32(g.viewW)-thumbW)
		maxScroll := max(0, g.contentW-float32(g.viewW))
		thumbX := 0 + travel*(g.scroll/max(1, maxScroll))
		switch e.Kind {
		case pointer.Press:
			if e.Position.X < thumbX || e.Position.X > thumbX+thumbW {
				// Jump so the thumb centres on the press.
				t.scroll = (e.Position.X - thumbW/2) / travel * maxScroll
			}
			t.drag = dragState{kind: dragBar, pid: e.PointerID, x0: e.Position.X, scroll0: t.scroll}
		case pointer.Drag:
			if t.drag.kind == dragBar {
				t.scroll = t.drag.scroll0 + (e.Position.X-t.drag.x0)/travel*maxScroll
			}
		case pointer.Release, pointer.Cancel:
			if t.drag.kind == dragBar {
				t.drag = dragState{}
			}
		}
	}
}

// wheelZoom zooms by a wheel delta around the pointer.
func (t *timeline) wheelZoom(gtx C, dy, x float32) {
	lo, hi, _ := zoomBounds(float32(t.geo.viewW)/gtx.Metric.PxPerDp, t.geo.total)
	f := float32(math.Pow(1.0018, float64(-dy)))
	t.auto = false
	t.applyZoom(gtx, clamp32(t.zoom*f, lo, hi), x)
}

func (t *timeline) indexOf(base []slot, id string) int {
	for i, s := range base {
		if s.id == id {
			return i
		}
	}
	return 0
}

func (t *timeline) widthOf(base []slot, id string) float64 {
	for _, s := range base {
		if s.id == id {
			return s.w
		}
	}
	return 0
}

// updateTrim turns the drag distance into new trims (snapped, clamped).
func (t *timeline) updateTrim(gtx C) {
	d := &t.drag
	g := &t.geo
	dt := float64((d.x - d.x0 + t.scroll - d.scroll0) / max(g.pxs, 0.001))
	switch d.kind {
	case dragTrimL:
		d.tin = snap(clampf(d.tin0+dt, 0, trimLimits(d.vDur, d.tout0)))
	case dragTrimR:
		d.tout = snap(clampf(d.tout0-dt, 0, trimLimits(d.vDur, d.tin0)))
	}
}

// updateVoice turns the drag distance into a move or trims of the voice
// line (snapped to 0.1 s; the line stays inside the film and keeps at least
// minVoiceKeep of audio; cutting the head keeps the words in place).
func (t *timeline) updateVoice() {
	d := &t.drag
	g := &t.geo
	// The pointer's way plus what the view scrolled under it meanwhile.
	dt := float64((d.x - d.x0 + t.scroll - d.scroll0) / max(g.pxs, 0.001))
	keep := math.Min(minVoiceKeep, d.vRaw)
	switch d.kind {
	case dragVoiceMove:
		// Snap the move, then keep the line inside the film.
		lo, hi := -d.vStart0, math.Max(0, g.total-d.vDur0)-d.vStart0
		d.vShift = d.vShift0 + clampf(snap(d.vShift0+dt)-d.vShift0, lo, hi)
	case dragVoiceTrimL:
		d.vTin = snapIn(d.vTin0+dt, 0, math.Max(0, d.vRaw-d.vTout0-keep))
		d.vShift = d.vShift0 + (d.vTin - d.vTin0)
	case dragVoiceTrimR:
		d.vTout = snapIn(d.vTout0-dt, 0, math.Max(0, d.vRaw-d.vTin0-keep))
	}
}

// snapIn snaps v to 0.1 s without leaving [lo, hi].
func snapIn(v, lo, hi float64) float64 {
	v = snap(clampf(v, lo, hi))
	if v > hi {
		v = math.Max(lo, math.Floor(hi*10+1e-9)/10)
	}
	if v < lo {
		v = math.Min(hi, math.Ceil(lo*10-1e-9)/10)
	}
	return clampf(v, lo, hi)
}

// commitVoice stores the moved / trimmed line (one edit per gesture).
func (t *timeline) commitVoice(c *core.Ctx, pv *core.ProjectView) {
	d := &t.drag
	if d.vShift == d.vShift0 && d.vTin == d.vTin0 && d.vTout == d.vTout0 {
		return
	}
	id, kind := d.id, d.kind
	shift, tin, tout := d.vShift, d.vTin, d.vTout // what the gesture shows
	start, dur := d.liveVoice()
	if kind == dragVoiceTrimL {
		// Cutting the head keeps the words where they were. The engine lays
		// the trimmed line out anew (an own-voice slice also moves its default
		// start), so the shift that does it comes from that layout.
		cp, ok := c.E.Get(pv.ID) // the engine's latest (the snapshot may lag)
		if !ok {
			cp = pv.P
		}
		cp.Scenes = slices.Clone(cp.Scenes)
		for i := range cp.Scenes {
			if cp.Scenes[i].ID == id {
				cp.Scenes[i].VoiceTrimIn = tin
			}
		}
		items := c.E.Timeline(cp)
		for _, it := range items {
			if it.SceneID == id && it.VoiceDuration > 0 {
				shift = start - it.VoiceDefaultStart
				start = clampf(start, 0, math.Max(0, studio.TimelineDuration(items)-it.VoiceDuration))
				dur = it.VoiceDuration
			}
		}
	}
	ok := c.Edit(pv.ID, func(p *studio.Project) {
		for i := range p.Scenes {
			s := &p.Scenes[i]
			if s.ID != id {
				continue
			}
			switch kind {
			case dragVoiceMove:
				s.VoiceShift = shift
			case dragVoiceTrimL:
				s.VoiceTrimIn, s.VoiceShift = tin, shift
			case dragVoiceTrimR:
				s.VoiceTrimOut = tout
			}
		}
	})
	if ok {
		t.pend = pendingEdit{ver: pv.Ver, voice: id, vStart: start, vLen: dur, vShift: shift, vTin: tin, vTout: tout}
	}
}

// resetVoice puts a scene's line back where it was, untrimmed.
func (t *timeline) resetVoice(c *core.Ctx, pv *core.ProjectView, id string) {
	sc := sceneOf(pv, id)
	if sc == nil || (sc.VoiceShift == 0 && sc.VoiceTrimIn == 0 && sc.VoiceTrimOut == 0) {
		return
	}
	if c.Edit(pv.ID, func(p *studio.Project) {
		for i := range p.Scenes {
			if p.Scenes[i].ID == id {
				p.Scenes[i].VoiceShift, p.Scenes[i].VoiceTrimIn, p.Scenes[i].VoiceTrimOut = 0, 0, 0
			}
		}
	}) {
		c.Toast(gui.ToneInfo, "Голос повернуто на місце")
	}
}

// selectScene makes id the selected scene (shared with the scene navigator).
func (t *timeline) selectScene(c *core.Ctx, projectID, id string, movePlayhead bool) {
	c.UI(projectID).Scene = id
	t.lastSel = id
	if !movePlayhead {
		return
	}
	for _, cg := range t.geo.clips {
		if cg.id == id && (t.playhead < cg.s0 || t.playhead >= cg.s0+cg.w) {
			t.playhead = intoScene(cg.s0, cg.w)
		}
	}
}

// intoScene is where the playhead goes to show a scene: a little into it,
// past the fade from the previous one.
func intoScene(s0, w float64) float64 { return s0 + math.Min(w*0.35, 1.2) }

// toggleVoice plays (or stops) a scene's voice line from the voice lane:
// exactly the part the trims keep.
func (t *timeline) toggleVoice(c *core.Ctx, pv *core.ProjectView, s *proj, id string) {
	sc := sceneOf(pv, id)
	if sc == nil || sc.Audio.Path == "" {
		return
	}
	src := pv.Abs(sc.Audio.Path)
	if c.Audio.Playing(src) {
		c.Audio.Stop()
		t.voice = ""
		return
	}
	at, dur := 0.0, 0.0 // the whole file when the timeline does not know the line
	if it := s.film.item(id); it != nil && it.VoiceDuration > 0 {
		at, dur = it.VoiceTrimIn, it.VoiceDuration
	}
	if pe := &t.pend; pe.voice == id && pe.ver == pv.Ver {
		at, dur = pe.vTin, pe.vLen // just trimmed: the snapshot is catching up
	}
	t.voice = id
	if err := c.PlayRange(src, at, dur, nil); err != nil && !c.Offline {
		c.Error(err)
	}
}

// commitReorder stores the new scene order.
func (t *timeline) commitReorder(c *core.Ctx, pv *core.ProjectView, base []slot) {
	d := &t.drag
	from := t.indexOf(base, d.id)
	if d.drop == from {
		return
	}
	order := make([]string, 0, len(base))
	for _, s := range t.displaySlots(base) {
		order = append(order, s.id)
	}
	id := d.id
	if c.Edit(pv.ID, func(p *studio.Project) { p.Scenes = reorderScenes(p.Scenes, id, order) }) {
		t.pend = pendingEdit{ver: pv.Ver, order: order}
	}
}

// commitTrim stores the trims of the dragged clip.
func (t *timeline) commitTrim(c *core.Ctx, pv *core.ProjectView) {
	d := &t.drag
	if d.tin == d.tin0 && d.tout == d.tout0 {
		return
	}
	id, tin, tout, kind := d.id, d.tin, d.tout, d.kind
	if kind == dragTrimL {
		tout = d.tout0
	} else {
		tin = d.tin0
	}
	ok := c.Edit(pv.ID, func(p *studio.Project) {
		for i := range p.Scenes {
			if p.Scenes[i].ID == id {
				if kind == dragTrimL {
					p.Scenes[i].TrimIn = tin
				} else {
					p.Scenes[i].TrimOut = tout
				}
			}
		}
	})
	if ok {
		t.pend = pendingEdit{ver: pv.Ver, trim: id, tin: tin, tout: tout, vDur: d.vDur}
	}
}

// inputAreas registers the viewport and ruler handlers, with cursors for
// what the pointer is over.
func (t *timeline) inputAreas(gtx C, rulerH int) {
	g := &t.geo
	area := clip.Rect(image.Rect(0, rulerH, g.viewW, g.viewH)).Push(gtx.Ops)
	event.Op(gtx.Ops, &t.tag)
	event.Op(gtx.Ops, &t.keyTag)
	switch t.drag.kind {
	case dragReorder, dragVoiceMove:
		pointer.CursorGrabbing.Add(gtx.Ops)
	case dragTrimL, dragTrimR, dragVoiceTrimL, dragVoiceTrimR:
		pointer.CursorColResize.Add(gtx.Ops)
	default:
		// Per-element cursors (children of the handler's area).
		hw := float32(gtx.Dp(handleDp))
		for _, cg := range g.clips {
			if cg.x1 < 0 || cg.x0 > float32(g.viewW) {
				continue
			}
			body := image.Rect(int(cg.x0), g.videoY, int(cg.x1), g.videoY+g.videoH)
			st := clip.Rect(body).Push(gtx.Ops)
			pointer.CursorGrab.Add(gtx.Ops)
			st.Pop()
			if cg.x1-cg.x0 >= 3*hw {
				for _, r := range []image.Rectangle{
					image.Rect(int(cg.x0-hw/3), g.videoY, int(cg.x0+hw*0.7), g.videoY+g.videoH),
					image.Rect(int(cg.x1-hw*0.7), g.videoY, int(cg.x1+hw/3), g.videoY+g.videoH),
				} {
					st := clip.Rect(r).Push(gtx.Ops)
					pointer.CursorColResize.Add(gtx.Ops)
					st.Pop()
				}
			}
			if r := cg.voice; !r.Empty() {
				st := clip.Rect(r).Push(gtx.Ops)
				pointer.CursorGrab.Add(gtx.Ops)
				st.Pop()
				vh := gtx.Dp(vHandleDp)
				if r.Dx() >= 3*vh {
					for _, e := range []image.Rectangle{
						image.Rect(r.Min.X-vh/3, r.Min.Y, r.Min.X+vh*4/5, r.Max.Y),
						image.Rect(r.Max.X-vh*4/5, r.Min.Y, r.Max.X+vh/3, r.Max.Y),
					} {
						st := clip.Rect(e).Push(gtx.Ops)
						pointer.CursorColResize.Add(gtx.Ops)
						st.Pop()
					}
				}
			}
		}
	}
	area.Pop()
	ra := clip.Rect(image.Rect(0, 0, g.viewW, rulerH)).Push(gtx.Ops)
	event.Op(gtx.Ops, &t.rulerTag)
	pointer.CursorPointer.Add(gtx.Ops)
	ra.Pop()
}

// ---- drawing ------------------------------------------------------------------------------------------

// rulerStep picks the labelled tick spacing (seconds) for a zoom.
func rulerStep(pxs, minPx float32) (major, minor float64) {
	steps := []float64{0.5, 1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 1800}
	for _, s := range steps {
		if float32(s)*pxs >= minPx {
			major = s
			break
		}
	}
	if major == 0 {
		major = 3600
	}
	switch major {
	case 0.5, 1:
		minor = major / 5
	case 2, 10, 60, 120, 600:
		minor = major / 4
	case 5, 15, 30, 300, 1800:
		minor = major / 5
	default:
		minor = major / 6
	}
	if minor*float64(pxs) < 5 {
		minor = major
	}
	return major, minor
}

func (t *timeline) drawRuler(gtx C, th *gui.Theme, h int) {
	g := &t.geo
	major, minor := rulerStep(g.pxs, float32(gtx.Dp(76)))
	t0 := math.Max(0, g.t(0)-major)
	t1 := math.Min(g.total, g.t(float32(g.viewW))+major)
	tick := th.Pal.BorderStrong
	for v := math.Floor(t0/minor) * minor; v <= t1+1e-9; v += minor {
		if v < -1e-9 {
			continue
		}
		x := int(g.x(v))
		isMajor := math.Abs(math.Remainder(v, major)) < minor/10
		th2 := gtx.Dp(5)
		if isMajor {
			th2 = gtx.Dp(9)
		}
		gui.FillRect(gtx, image.Rect(x, h-th2, x+max(1, gtx.Dp(1)), h), tick)
		if isMajor {
			lab := core.Clock(v)
			if major < 1 {
				lab = decimal(v, 1)
			}
			textAt(gtx, th, x+gtx.Dp(4), h-th2-gtx.Dp(14), gtx.Dp(60), th.Caption, th.Pal.Text3, lab)
		}
	}
	// End of the film.
	xe := int(g.x(g.total))
	if xe > 0 && xe < g.viewW {
		gui.FillRect(gtx, image.Rect(xe, gtx.Dp(4), xe+max(1, gtx.Dp(1)), h), th.Pal.Text3)
	}
}

func (t *timeline) drawPlayhead(gtx C, th *gui.Theme, h int) {
	g := &t.geo
	x := g.x(t.playhead)
	if x < -4 || x > float32(g.viewW)+4 {
		return
	}
	col := th.Pal.Action
	lw := gtx.Dp(2)
	xi := int(x)
	gui.FillRect(gtx, image.Rect(xi-lw/2, gtx.Dp(6), xi-lw/2+lw, h), col)
	// A small flag on the ruler.
	fw, fh := gtx.Dp(12), gtx.Dp(14)
	var p clip.Path
	p.Begin(gtx.Ops)
	fx := float32(xi) - float32(fw)/2
	p.MoveTo(f32.Pt(fx, 0))
	p.LineTo(f32.Pt(fx+float32(fw), 0))
	p.LineTo(f32.Pt(fx+float32(fw), float32(fh)*0.62))
	p.LineTo(f32.Pt(float32(xi), float32(fh)))
	p.LineTo(f32.Pt(fx, float32(fh)*0.62))
	p.Close()
	paint.FillShape(gtx.Ops, col, clip.Outline{Path: p.End()}.Op())
}

// drawClips draws the visible clip boxes (the lifted one last, on top).
func (t *timeline) drawClips(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj, sel string) {
	g := &t.geo
	var lifted *clipGeom
	for i := range g.clips {
		cg := &g.clips[i]
		if t.drag.kind == dragReorder && cg.id == t.drag.id {
			lifted = cg
			continue
		}
		if cg.x1 < -8 || cg.x0 > float32(g.viewW)+8 {
			continue
		}
		t.drawClip(gtx, c, pv, s, cg, cg.id == sel, false)
	}
	if lifted != nil {
		// Drop marker where the clip will land.
		disp := t.displaySlots(s.film.slots())
		for _, sl := range disp {
			if sl.id == t.drag.id {
				x := int(g.x(sl.s0))
				w := int(float32(sl.w) * g.pxs)
				r := image.Rect(x+gtx.Dp(2), g.videoY, x+w-gtx.Dp(2), g.videoY+g.videoH)
				strokeRR(gtx, r, gtx.Dp(10), float32(gtx.Dp(2)), gui.WithAlpha(c.Th.Pal.Action, 0.8))
				fillRR(gtx, r, gtx.Dp(10), gui.WithAlpha(c.Th.Pal.ActionSoft, 0.55))
			}
		}
		t.drawClip(gtx, c, pv, s, lifted, true, true)
	}
}

// drawClip draws one clip box: the frame thumbnail strip, the held tail,
// labels, status and trim handles.
func (t *timeline) drawClip(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj, cg *clipGeom, selected, lifted bool) {
	th := c.Th
	g := &t.geo
	sc := sceneOf(pv, cg.id)
	it := s.film.item(cg.id)
	if sc == nil || it == nil {
		return
	}
	u := t.clipUI(cg.id)
	gap := float32(gtx.Dp(2))
	x0, x1 := cg.x0+gap, cg.x1-gap
	y0, y1 := g.videoY, g.videoY+g.videoH
	hovered := t.hover.id == cg.id && (t.hover.kind == hitClip || t.hover.kind == hitTrimL || t.hover.kind == hitTrimR)
	u.hover.Duration = gui.DurHover
	hv := u.hover.Animate(gtx, b2f(hovered || lifted))
	u.lift.Duration = 160 * time.Millisecond
	lv := u.lift.Animate(gtx, b2f(lifted))

	// Live trim: the material between the handles; the ghost shows what is cut.
	trim := (t.drag.kind == dragTrimL || t.drag.kind == dragTrimR) && t.drag.id == cg.id
	matX0, matX1 := x0, x1
	s0 := cg.s0
	holdX := g.x(s0 + clampf(slotHold(s, cg.k), 0, cg.w))
	if trim {
		d := &t.drag
		clip0 := d.vDur - d.tin0 - d.tout0
		ghost0 := g.x(s0 - d.tin0)
		ghost1 := g.x(s0 + clip0 + d.tout0)
		gr := image.Rect(int(ghost0), y0, int(ghost1), y1)
		strokeRR(gtx, gr, gtx.Dp(10), float32(max(1, gtx.Dp(1))), gui.WithAlpha(th.Pal.Text3, 0.8))
		fillRR(gtx, gr, gtx.Dp(10), gui.WithAlpha(th.Pal.Surface, 0.6))
		matX0 = g.x(s0 + (d.tin - d.tin0))
		matX1 = g.x(s0 + clip0 - (d.tout - d.tout0))
		holdX = matX1
		x0 = matX0
		x1 = max(x1, matX1)
		if d.kind == dragTrimR {
			x1 = matX1
		}
	}
	r := image.Rect(int(x0), y0, int(x1), y1)
	if r.Dx() < 2 {
		return
	}
	rad := gtx.Dp(10)
	dy := -int(float32(gtx.Dp(3)) * lv)
	r = r.Add(pt(0, dy))
	if lv > 0.01 {
		gui.Shadow(gtx, th, r, 10, gui.LerpElevation(gui.ElevCard, gui.ElevSheet, lv))
	}
	func() {
		defer clip.UniformRRect(r, rad).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.Pal.SurfaceHover)
		ratio := ratioOf(pv.P.Input.Aspect)
		tw := max(gtx.Dp(24), int(float32(g.videoH)*ratio))
		img, ok := sceneImage(c, pv, sc, max(tw, g.videoH))
		if ok {
			start := r.Min.X
			// Keep tiles stable while scrolling: align them to the clip start.
			for x := start; x < r.Max.X; x += tw {
				if x+tw < 0 || x > g.viewW {
					continue
				}
				o := op.Offset(pt(x, r.Min.Y)).Push(gtx.Ops)
				drawImage(gtx, img, pt(tw, r.Dy()), true)
				o.Pop()
			}
		} else {
			ic := gtx.Dp(22)
			o := op.Offset(pt(r.Min.X+(r.Dx()-ic)/2, r.Min.Y+(r.Dy()-ic)/2)).Push(gtx.Ops)
			gui.IconImage.Layout(gtx, 22, th.Pal.Text3)
			o.Pop()
		}
		// Held tail: the last frame stays while the voice finishes.
		if hx := int(holdX); hx < r.Max.X-gtx.Dp(2) {
			hr := image.Rect(max(hx, r.Min.X), r.Min.Y, r.Max.X, r.Max.Y)
			paint.FillShape(gtx.Ops, gui.WithAlpha(th.Pal.Card, 0.62), clip.Rect(hr).Op())
			for yy := hr.Min.Y + gtx.Dp(3); yy < hr.Max.Y; yy += gtx.Dp(7) {
				gui.FillRect(gtx, image.Rect(hr.Min.X, yy, hr.Min.X+max(1, gtx.Dp(1)), yy+gtx.Dp(3)), gui.WithAlpha(th.Pal.Text2, 0.7))
			}
		}
		// Bottom shade for the labels.
		shade := r
		shade.Min.Y = r.Max.Y - gtx.Dp(26)
		paint.LinearGradientOp{Stop1: f32.Pt(0, float32(shade.Min.Y)), Color1: color.NRGBA{A: 0},
			Stop2: f32.Pt(0, float32(shade.Max.Y)), Color2: color.NRGBA{R: 16, G: 12, B: 10, A: 150}}.Add(gtx.Ops)
		st := clip.Rect(shade).Push(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		st.Pop()
		w := r.Dx()
		white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
		num := sceneNumber(&pv.P, cg.id)
		// Duration (live while trimming) at the bottom right, the title at the left.
		durS := it.Duration
		if trim {
			k := s.film.pos[cg.id]
			prev := 0.0
			if k > 0 {
				prev = s.film.items[k-1].Overlap
			}
			durS = liveDuration(it, prev, t.drag.vDur, t.drag.tin, t.drag.tout)
		}
		dur := secText(durS)
		dw := th.TextWidth(gtx, th.Caption.Weighted(gui.FontSemiBold), dur)
		in := gtx.Dp(8)
		if selected || hovered || trim {
			in = gtx.Dp(16) // clear of the trim grips
		}
		if w >= dw+2*in {
			textAt(gtx, th, r.Max.X-dw-in, r.Max.Y-gtx.Dp(20), dw+gtx.Dp(2), th.Caption.Weighted(gui.FontSemiBold), white, dur)
		}
		label := itoa(num)
		if t := strings.TrimSpace(sc.Title); t != "" {
			label += " · " + t
		}
		if tw := w - dw - 2*in - gtx.Dp(8); tw > gtx.Dp(14) {
			textAt(gtx, th, r.Min.X+in, r.Max.Y-gtx.Dp(20), tw, th.Caption.Weighted(gui.FontMedium), gui.WithAlpha(white, 0.95), label)
		}
		// Status: a still with motion, running generation, stale or failed assets.
		t.drawClipStatus(gtx, th, sc, it, r)
	}()
	// Outline: selected (action), hovered, else a hairline.
	switch {
	case selected:
		strokeRR(gtx, r, rad, float32(gtx.Dp(2)), th.Pal.Action)
	case hv > 0.01:
		strokeRR(gtx, r, rad, float32(gtx.Dp(1.5)), gui.WithAlpha(th.Pal.Text, 0.35*hv))
	default:
		strokeRR(gtx, r, rad, float32(max(1, gtx.Dp(1))), gui.WithAlpha(th.Pal.Text, 0.12))
	}
	// Trim handles.
	if (selected || hv > 0.01 || trim) && r.Dx() >= gtx.Dp(3*handleDp) && !lifted {
		a := max(hv, b2f(selected))
		for side := 0; side < 2; side++ {
			active := trim && ((side == 0 && t.drag.kind == dragTrimL) || (side == 1 && t.drag.kind == dragTrimR))
			hot := active || (t.hover.id == cg.id && ((side == 0 && t.hover.kind == hitTrimL) || (side == 1 && t.hover.kind == hitTrimR)))
			hw, hh := gtx.Dp(8), r.Dy()-gtx.Dp(18)
			x := r.Min.X + gtx.Dp(3)
			if side == 1 {
				x = r.Max.X - gtx.Dp(3) - hw
			}
			hr := image.Rect(x, r.Min.Y+(r.Dy()-hh)/2, x+hw, r.Min.Y+(r.Dy()+hh)/2)
			fill := gui.WithAlpha(th.Pal.Card, a)
			grip := gui.WithAlpha(th.Pal.Text2, a)
			if hot {
				fill, grip = th.Pal.Action, th.Pal.OnAction
			}
			gui.Shadow(gtx, th, hr, 4, gui.ElevControl)
			fillRR(gtx, hr, gtx.Dp(4), fill)
			gx := hr.Min.X + hw/2
			gh := min(gtx.Dp(22), hr.Dy()-gtx.Dp(8))
			gy := hr.Min.Y + (hr.Dy()-gh)/2
			gui.FillRect(gtx, image.Rect(gx-max(1, gtx.Dp(1))/2, gy, gx-max(1, gtx.Dp(1))/2+max(1, gtx.Dp(1)), gy+gh), grip)
		}
	}
	// Live trim bubble.
	if trim {
		d := &t.drag
		k := s.film.pos[cg.id]
		prev := 0.0
		if k > 0 {
			prev = s.film.items[k-1].Overlap
		}
		clipLen := d.vDur - d.tin - d.tout
		txt := "Кліп " + secText(clipLen)
		if d.kind == dragTrimL {
			txt = "Початок −" + secText(d.tin) + " · " + txt
		} else {
			txt = "Кінець −" + secText(d.tout) + " · " + txt
		}
		if full := liveDuration(it, prev, d.vDur, d.tin, d.tout); full > clipLen+0.05 {
			txt += " · сцена " + secText(full)
		}
		bx := int(matX0)
		if d.kind == dragTrimR {
			bx = int(matX1)
		}
		t.bubble(gtx, th, bx, y0-gtx.Dp(6), txt)
	}
}

// slotHold is where a scene's clip material ends inside its box (seconds).
func slotHold(s *proj, k int) float64 {
	f := &s.film
	if k < 0 || k >= len(f.items) {
		return 0
	}
	in := 0.0
	if k > 0 {
		in = f.items[k-1].Overlap
	}
	return f.items[k].ClipDuration - in/2
}

func (t *timeline) drawClipStatus(gtx C, th *gui.Theme, sc *studio.Scene, it *studio.TimelineItem, r image.Rectangle) {
	if r.Dx() < gtx.Dp(58) {
		return
	}
	var icon *gui.Icon
	var col color.NRGBA
	spin := false
	switch {
	case sc.Video.Status == studio.StatusRunning || sc.Frame.Status == studio.StatusRunning:
		spin = true
	case sc.Video.Status == studio.StatusError || sc.Frame.Status == studio.StatusError:
		icon, col = gui.IconAlertTriangle, th.Pal.ErrorInk
	case sc.Video.Stale || sc.Frame.Stale || sc.Audio.Stale:
		icon, col = gui.IconRefreshCw, th.Pal.WarningInk
	case !it.HasClip:
		icon, col = gui.IconImage, th.Pal.OverlayInk
	default:
		return
	}
	s := gtx.Dp(22)
	x, y := r.Max.X-s-gtx.Dp(16), r.Min.Y+gtx.Dp(6)
	fillCircle(gtx, float32(x)+float32(s)/2, float32(y)+float32(s)/2, float32(s)/2, th.Pal.Overlay)
	is := gtx.Dp(13)
	o := op.Offset(pt(x+(s-is)/2, y+(s-is)/2)).Push(gtx.Ops)
	if spin {
		gui.Spinner{Size: 13, Color: th.Pal.RunningInk}.Layout(gtx, th)
	} else {
		icon.Layout(gtx, 13, col)
	}
	o.Pop()
}

// bubble draws a small dark label centred at x above y.
func (t *timeline) bubble(gtx C, th *gui.Theme, x, y int, s string) {
	m := op.Record(gtx.Ops)
	st := th.Caption.Weighted(gui.FontSemiBold)
	g := gtx
	g.Constraints = layout.Constraints{Max: pt(gtx.Dp(360), gtx.Dp(40))}
	d, call := measure(g, gui.Text{Style: st, Color: th.Pal.TooltipFg, MaxLines: 1}.Widget(th, s))
	padX, padY := gtx.Dp(8), gtx.Dp(4)
	w, h := d.Size.X+2*padX, d.Size.Y+2*padY
	bx := max(0, min(x-w/2, t.geo.viewW-w))
	by := max(0, y-h)
	r := image.Rect(bx, by, bx+w, by+h)
	gui.Shadow(gtx, th, r, gui.RadiusSmall, gui.ElevPop)
	fillRR(gtx, r, gtx.Dp(gui.RadiusSmall), th.Pal.TooltipBg)
	o := op.Offset(pt(bx+padX, by+padY)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	op.Defer(gtx.Ops, m.Stop())
}

// drawVoices draws the voice lane: one block per line with a mini waveform.
func (t *timeline) drawVoices(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj, disp []slot) {
	th := c.Th
	g := &t.geo
	if !pv.P.Input.Voiceover {
		textAt(gtx, th, gtx.Dp(12), g.voiceY+(g.voiceH-gtx.Sp(12)*4/3)/2, g.viewW, th.Caption, th.Pal.Text3, "Озвучку вимкнено на кроці «Сценарій»")
		return
	}
	if s.film.voices == 0 {
		textAt(gtx, th, gtx.Dp(12), g.voiceY+(g.voiceH-gtx.Sp(12)*4/3)/2, g.viewW, th.Caption, th.Pal.Text3, "Озвучки ще немає — вона з'явиться після кроку «Відео й озвучка»")
		return
	}
	sel := c.UI(pv.ID).Scene
	d := &t.drag
	// The dragged line is drawn last, over its neighbours.
	order := make([]int, 0, len(g.clips))
	top := -1
	for i, cg := range g.clips {
		if d.voice() && cg.id == d.id {
			top = i
			continue
		}
		order = append(order, i)
	}
	if top >= 0 {
		order = append(order, top)
	}
	for _, i := range order {
		t.drawVoice(gtx, c, pv, &g.clips[i], sel)
	}
	// Lines may overlap (both sound, the mix is limited): mark where.
	inset := gtx.Dp(3)
	for i := range g.clips {
		a := g.clips[i].voice
		if a.Empty() || a.Max.X < 0 || a.Min.X > g.viewW {
			continue
		}
		for j := i + 1; j < len(g.clips); j++ {
			b := g.clips[j].voice
			x0, x1 := max(a.Min.X, b.Min.X), min(a.Max.X, b.Max.X)
			if b.Empty() || x1-x0 < 2 {
				continue
			}
			r := image.Rect(x0, a.Min.Y+inset, x1, a.Max.Y-inset)
			fillRR(gtx, r, gtx.Dp(3), gui.WithAlpha(th.Pal.Warning, 0.32))
			line := max(1, gtx.Dp(2))
			paint.FillShape(gtx.Ops, th.Pal.Warning, clip.Rect(image.Rect(x0, r.Min.Y, x1, r.Min.Y+line)).Op())
			paint.FillShape(gtx.Ops, th.Pal.Warning, clip.Rect(image.Rect(x0, r.Max.Y-line, x1, r.Max.Y)).Op())
		}
	}
}

// drawVoice draws one voice line: its block with a waveform (bars stay where
// they are in the recording, so trims cut them and moves carry them), the
// trim grips, and while trimming the whole recording as a ghost.
func (t *timeline) drawVoice(gtx C, c *core.Ctx, pv *core.ProjectView, cg *clipGeom, sel string) {
	th := c.Th
	g := &t.geo
	d := &t.drag
	r := cg.voice
	if r.Empty() {
		return
	}
	trimming := d.id == cg.id && (d.kind == dragVoiceTrimL || d.kind == dragVoiceTrimR)
	lo, hi := float32(r.Min.X), float32(r.Max.X)
	if trimming {
		lo, hi = min(lo, cg.vFull0), max(hi, cg.vFull1)
	}
	if hi < 0 || lo > float32(g.viewW) {
		return
	}
	sc := sceneOf(pv, cg.id)
	if sc == nil {
		return
	}
	r.Min.Y += gtx.Dp(3)
	r.Max.Y -= gtx.Dp(3)
	view := image.Rect(-gtx.Dp(8), r.Min.Y, g.viewW+gtx.Dp(8), r.Max.Y)
	seed := cg.id + "|" + sc.Audio.Path // a new recording gets a new waveform
	dragging := d.voice() && d.id == cg.id
	playing := sc.Audio.Path != "" && c.Audio.Playing(pv.Abs(sc.Audio.Path))
	hovered := d.kind == dragNone && t.hover.id == cg.id &&
		(t.hover.kind == hitVoice || t.hover.kind == hitVoiceL || t.hover.kind == hitVoiceR)
	bars := th.Pal.Text3
	if hovered || dragging || cg.id == sel {
		bars = th.Pal.Text2
	}
	if playing {
		bars = th.Pal.Action
	}
	rad := gtx.Dp(6)
	full := image.Rect(int(cg.vFull0), r.Min.Y+gtx.Dp(3), int(cg.vFull1), r.Max.Y-gtx.Dp(3))
	if trimming {
		// The recording the trims cut from: a dashed ghost with faint bars.
		lane := image.Rect(int(cg.vFull0), r.Min.Y, int(cg.vFull1), r.Max.Y)
		fillRR(gtx, lane, rad, gui.WithAlpha(th.Pal.SurfaceHover, 0.55))
		dashRR(gtx, lane, rad, gui.WithAlpha(th.Pal.Text3, 0.8))
		func() {
			defer clip.UniformRRect(lane, rad).Push(gtx.Ops).Pop()
			waveSpan(gtx, full, view, seed, gui.WithAlpha(th.Pal.Text3, 0.3))
		}()
	}
	if r.Dx() < 2 {
		return
	}
	fill := gui.LerpColor(th.Pal.Card, th.Pal.SurfaceHover, 0.35)
	if dragging {
		gui.Shadow(gtx, th, r, gui.RadiusSmall, gui.ElevPop)
		fill = th.Pal.Card
	}
	fillRR(gtx, r, rad, fill)
	border := th.Pal.Border
	if cg.id == sel || dragging {
		border = gui.WithAlpha(th.Pal.Text, 0.35)
	}
	strokeRR(gtx, r, rad, float32(max(1, gtx.Dp(1))), border)
	func() {
		defer clip.UniformRRect(r, rad).Push(gtx.Ops).Pop()
		x := r.Min.X + gtx.Dp(6)
		if r.Dx() > gtx.Dp(54) {
			is := gtx.Dp(14)
			ic := gui.IconPlay
			if playing {
				ic = gui.IconSquare
			}
			o := op.Offset(pt(x, r.Min.Y+(r.Dy()-is)/2)).Push(gtx.Ops)
			ic.Layout(gtx, 14, bars)
			o.Pop()
			x += is + gtx.Dp(6)
		}
		inner := image.Rect(x, full.Min.Y, r.Max.X-gtx.Dp(4), full.Max.Y)
		waveSpan(gtx, full, inner.Intersect(view), seed, bars)
	}()
	// Trim grips on the selected, hovered or dragged line.
	if (cg.id == sel || hovered || dragging) && r.Dx() >= 3*gtx.Dp(vHandleDp) {
		for side := 0; side < 2; side++ {
			want := dragVoiceTrimL
			wantHit := hitVoiceL
			if side == 1 {
				want, wantHit = dragVoiceTrimR, hitVoiceR
			}
			hot := (dragging && d.kind == want) || (hovered && t.hover.kind == wantHit)
			gw := gtx.Dp(3)
			col := gui.WithAlpha(th.Pal.Text2, 0.8)
			if hot {
				col, gw = th.Pal.Action, gtx.Dp(4)
			}
			x := r.Min.X + gtx.Dp(3)
			if side == 1 {
				x = r.Max.X - gtx.Dp(3) - gw
			}
			fillRR(gtx, image.Rect(x, r.Min.Y+gtx.Dp(4), x+gw, r.Max.Y-gtx.Dp(4)), gw/2, col)
		}
	}
	if hovered {
		tip := "Клік — послухати, тягни — зсунути. Права кнопка — повернути на місце"
		switch t.hover.kind {
		case hitVoiceL:
			tip = "Тягни, щоб обрізати початок голосу"
		case hitVoiceR:
			tip = "Тягни, щоб обрізати кінець голосу"
		}
		th.Tooltip(gtx, t.clipUI(cg.id), false, tip)
	}
	if dragging {
		start, dur := d.liveVoice()
		var txt string
		bx := (r.Min.X + r.Max.X) / 2
		switch d.kind {
		case dragVoiceMove:
			switch v := math.Round(d.vShift*10) / 10; {
			case v > 0:
				txt = "Пізніше на " + secText(v)
			case v < 0:
				txt = "Раніше на " + secText(-v)
			default:
				txt = "На своєму місці"
			}
			txt += " · з " + clockTenths(start)
		case dragVoiceTrimL:
			bx = r.Min.X
			txt = "Зрізано початок " + secText(d.vTin) + " · лишається " + secText(dur)
			if d.vTin == 0 {
				txt = "Початок цілий · " + secText(dur)
			}
		case dragVoiceTrimR:
			bx = r.Max.X
			txt = "Зрізано кінець " + secText(d.vTout) + " · лишається " + secText(dur)
			if d.vTout == 0 {
				txt = "Кінець цілий · " + secText(dur)
			}
		}
		t.bubble(gtx, th, bx, g.voiceY-gtx.Dp(4), txt)
	}
}

// clockTenths is m:ss,t (a voice start to a tenth of a second).
func clockTenths(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	t := int(math.Round(sec * 10))
	s := t / 10 % 60
	ss := itoa(s)
	if s < 10 {
		ss = "0" + ss
	}
	return itoa(t/600) + ":" + ss + "," + itoa(t%10)
}

// waveSpan draws a speech-like waveform for a recording laid over span
// (hashed from seed), only the bars inside vis. Bar i always sits at
// span.Min.X + i·step, so a trim or a move never reshuffles the bars.
func waveSpan(gtx C, span, vis image.Rectangle, seed string, col color.NRGBA) {
	if span.Dx() <= 0 || span.Dy() <= 0 || vis.Empty() {
		return
	}
	h := fnv.New32a()
	h.Write([]byte(seed))
	base := h.Sum32()
	step := gtx.Dp(4)
	bw := max(1, gtx.Dp(2))
	n := span.Dx() / step
	i0 := max(0, (vis.Min.X-span.Min.X)/step)
	i1 := min(n, (vis.Max.X-span.Min.X)/step+1)
	for i := i0; i < i1; i++ {
		x := span.Min.X + i*step
		if x < vis.Min.X || x+bw > vis.Max.X {
			continue
		}
		v := base ^ uint32(i)*2654435761
		v = v*1664525 + 1013904223
		v ^= v >> 13
		a := 0.25 + 0.75*float32(v>>24)/255
		env := 0.55 + 0.45*float32(math.Sin(float64(i)/float64(max(1, n))*math.Pi))
		bh := max(float32(bw), float32(span.Dy())*a*env)
		y := span.Min.Y + int((float32(span.Dy())-bh)/2)
		fillRR(gtx, image.Rect(x, y, x+bw, y+int(bh)), bw/2, col)
	}
}

// drawMusic draws the music lane: the looped track across the film with
// its level, the ducking under every voice line and the fade-out.
func (t *timeline) drawMusic(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj) {
	th := c.Th
	g := &t.geo
	m := pv.P.Montage
	if m.MusicPath == "" {
		return
	}
	x0, x1 := g.x(0), g.x(g.total)
	r := image.Rect(int(max(x0, -8)), g.musicY+gtx.Dp(3), int(min(x1, float32(g.viewW)+8)), g.musicY+g.musicH-gtx.Dp(3))
	if r.Dx() <= 0 {
		return
	}
	fill := gui.LerpColor(th.Pal.Card, th.Pal.SurfaceHover, 0.35)
	fillRR(gtx, r, gtx.Dp(6), fill)
	strokeRR(gtx, r, gtx.Dp(6), float32(max(1, gtx.Dp(1))), th.Pal.Border)
	defer clip.UniformRRect(r, gtx.Dp(6)).Push(gtx.Ops).Pop()
	vol := float32(clampf(m.MusicVolume, 0, 1))
	// Envelope bars: level × ducking × fade-out.
	step := gtx.Dp(4)
	bw := max(1, gtx.Dp(2))
	h := fnv.New32a()
	h.Write([]byte(m.MusicPath))
	seed := h.Sum32()
	inner := r.Dy() - gtx.Dp(6)
	startX := int(x0) + gtx.Dp(28)
	if startX < r.Min.X {
		startX += (r.Min.X - startX) / step * step // skip what is scrolled away, keeping the bars in phase
	}
	for x := startX; x < r.Max.X; x += step {
		i := uint32((x - int(x0)) / step)
		v := seed ^ (i * 2654435761)
		v = v*1664525 + 1013904223
		a := 0.45 + 0.55*float32(v>>24)/255
		sec := g.t(float32(x))
		lv := 0.25 + 0.75*vol
		if m.Ducking && s.voiceAt(sec) {
			lv *= 0.45
		}
		if rem := g.total - sec; rem < 2.5 {
			lv *= float32(math.Max(0, rem/2.5))
		}
		if x < r.Min.X {
			continue
		}
		bh := max(float32(bw), float32(inner)*a*lv)
		y := r.Min.Y + (r.Dy()-int(bh))/2
		fillRR(gtx, image.Rect(x, y, x+bw, y+int(bh)), bw/2, th.Pal.Text3)
	}
	// Name tag at the visible start of the lane.
	lx := max(r.Min.X, 0) + gtx.Dp(6)
	name := strings.TrimSpace(m.MusicName)
	if name == "" {
		name = "Музика"
	}
	label := name + " · " + pctText(m.MusicVolume)
	st := th.Caption.Weighted(gui.FontMedium)
	lw := min(th.TextWidth(gtx, st, label), gtx.Dp(320))
	tag := image.Rect(lx, r.Min.Y+gtx.Dp(3), lx+lw+gtx.Dp(28), r.Max.Y-gtx.Dp(3))
	fillRR(gtx, tag, tag.Dy()/2, th.Pal.Card)
	is := gtx.Dp(12)
	o := op.Offset(pt(tag.Min.X+gtx.Dp(7), tag.Min.Y+(tag.Dy()-is)/2)).Push(gtx.Ops)
	gui.IconMusic.Layout(gtx, 12, th.Pal.Text2)
	o.Pop()
	textAt(gtx, th, tag.Min.X+gtx.Dp(22), tag.Min.Y+(tag.Dy()-gtx.Sp(12)*4/3)/2, lw+gtx.Dp(2), st, th.Pal.Text2, label)
}

// voiceAt reports whether a voice line plays at film time sec.
func (s *proj) voiceAt(sec float64) bool {
	for _, it := range s.film.items {
		if it.VoiceDuration > 0 && sec >= it.VoiceStart && sec < it.VoiceStart+it.VoiceDuration {
			return true
		}
	}
	return false
}

// drawAddMusic offers music in the empty music lane.
func (t *timeline) drawAddMusic(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj) {
	g := &t.geo
	if t.addMusic.Clicked(gtx) {
		s.pickMusic(c, pv)
	}
	o := op.Offset(pt(gtx.Dp(6), g.musicY+(g.musicH-gtx.Dp(26))/2)).Push(gtx.Ops)
	gg := gtx
	gg.Constraints = layout.Constraints{Max: pt(g.viewW, gtx.Dp(26))}
	gui.Button{Kind: gui.Ghost, Size: gui.Sm, Height: 26, Icon: gui.IconPlus, Text: "Додати фонову музику", Busy: s.sound.busy}.Layout(gg, c.Th, &t.addMusic)
	o.Pop()
}

// drawChips draws the transition chip on every cut.
func (t *timeline) drawChips(gtx C, c *core.Ctx, pv *core.ProjectView, s *proj) {
	if t.drag.kind == dragReorder || t.drag.kind == dragTrimL || t.drag.kind == dragTrimR {
		return
	}
	th := c.Th
	g := &t.geo
	m := pv.P.Montage
	def := projectTransition(m)
	for i := 0; i+1 < len(g.clips); i++ {
		cg := g.clips[i]
		x := cg.x1
		if x < -40 || x > float32(g.viewW)+40 {
			continue
		}
		sc := sceneOf(pv, cg.id)
		it := s.film.item(cg.id)
		if sc == nil || it == nil {
			continue
		}
		cl := t.chip(cg.id)
		if cl.Clicked(gtx) {
			s.pop.open(cg.id)
		}
		id := it.Transition
		tr, _ := media.TransitionByID(id)
		own := sc.Transition != "" && sc.Transition != def
		size := gtx.Dp(chipDp)
		w := size
		if tr.Overlaps() {
			w = max(size, int(float32(it.Overlap)*g.pxs)+gtx.Dp(12))
		}
		cy := g.videoY
		bx := int(x) - w/2
		tip := "Перехід: " + tr.Label
		if !own {
			tip += " (як у проєкті)"
		}
		o := op.Offset(pt(bx, cy-size/2)).Push(gtx.Ops)
		key := "mont.chip:" + cg.id
		gui.Anchor(gtx, th, key, func(gtx C) D {
			hovered := cl.Hovered()
			open := s.pop.sceneID == cg.id
			r := image.Rectangle{Max: pt(w, size)}
			gui.Shadow(gtx, th, r, gui.RadiusPill, gui.ElevControl)
			fill := th.Pal.Card
			ink := th.Pal.Text2
			if own || open {
				fill, ink = th.Pal.ActionSoft, th.Pal.ActionText
			}
			if hovered {
				fill = gui.LerpColor(fill, th.Pal.SurfaceHover, 0.5)
				ink = th.Pal.Text
			}
			fillRR(gtx, r, size/2, fill)
			strokeRR(gtx, r, size/2, float32(max(1, gtx.Dp(1))), gui.WithAlpha(th.Pal.Text, 0.14))
			is := gtx.Dp(15)
			io := op.Offset(pt((w-is)/2, (size-is)/2)).Push(gtx.Ops)
			transitionIcon(tr).Layout(gtx, 15, ink)
			io.Pop()
			if hovered {
				th.Tooltip(gtx, cl, cl.Pressed(), tip)
			}
			pressArea(gtx, cl, r.Size())
			return D{Size: r.Size()}
		})
		o.Pop()
	}
}

// drawBar draws the horizontal scrollbar (a slim thumb on a faint track).
func (t *timeline) drawBar(gtx C, th *gui.Theme, w, h int, maxScroll float32) {
	g := &t.geo
	area := clip.Rect(image.Rectangle{Max: pt(w, h)}).Push(gtx.Ops)
	event.Op(gtx.Ops, &t.barTag)
	area.Pop()
	if maxScroll <= 0 {
		return
	}
	vis := float32(w) / max(g.contentW, 1)
	thumbW := max(float32(gtx.Dp(40)), float32(w)*vis)
	travel := max(1, float32(w)-thumbW)
	x := travel * (g.scroll / maxScroll)
	bh := gtx.Dp(5)
	y := (h - bh) / 2
	fillRR(gtx, image.Rect(0, y, w, y+bh), bh/2, gui.WithAlpha(th.Pal.Track, 0.9))
	col := gui.WithAlpha(th.Pal.Text3, 0.6)
	if t.drag.kind == dragBar {
		col = th.Pal.Text2
	}
	tr := image.Rect(int(x), y, int(x+thumbW), y+bh)
	fillRR(gtx, tr, bh/2, col)
	st := clip.Rect(image.Rect(int(x), 0, int(x+thumbW), h)).Push(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	st.Pop()
}

// ---- small helpers -----------------------------------------------------------------------------------

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// sceneOf finds a scene of the snapshot by id.
func sceneOf(pv *core.ProjectView, id string) *studio.Scene {
	sc, _ := pv.Scene(id)
	return sc
}

var _ = unit.Dp(0)
