// Package montage is step 5 Монтаж of the project workspace (GUI.md →
// Screens → 2): the timeline editor (preview monitor, inspector, tracks with
// reorder, trims, per-scene transitions, voice and music lanes), the sound
// mix, the transition / motion / filter galleries, subtitles with a live
// preview, the cover studio and the final render.
//
// Layout of the main column (full width, card structure):
//
//	[ Таймлайн: monitor | inspector ; ruler + clips + voice + music ]
//	[ responsive masonry of cards: Фінальне відео, Звук, Переходи,
//	  Ефекти руху, Фільтри, Субтитри — 3 columns ≥ 1600 dp, 2 ≥ 1000, 1 ]
//	[ Обкладинка — the cover studio, full width ]
//
// One StepView serves every project; per-project UI state lives in proj.
// Every model change goes through c.Edit (sliders commit when the user
// pauses), blocking work through c.Do / c.Background / c.PickFile /
// c.PickSave, and pictures through c.Img.
package montage

import (
	"fmt"
	"hash/fnv"
	"image"
	"os"
	"strings"
	"sync"
	"time"

	"gioui.org/f32"
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

// New returns step 5.
func New() core.StepView { return &view{projects: map[string]*proj{}} }

// view is the step: per-project state by project id.
type view struct {
	mu       sync.Mutex
	projects map[string]*proj
}

func (v *view) state(id string) *proj {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := v.projects[id]
	if s == nil {
		s = newProj(id)
		v.projects[id] = s
	}
	return s
}

// proj is the UI state of the step for one project.
type proj struct {
	id   string
	page gui.Scroll

	film   film
	memo   renderMemo
	sigVer int64 // pv.Ver the signatures below belong to
	tlSig  int64 // timeline inputs
	outSig int64 // everything the render depends on

	// scrollTo asks the page to jump to an element once ("timeline", "cover").
	scrollTo string

	tl    timeline
	mon   monitor
	insp  inspector
	pop   transPopover
	sound soundCard
	trans transCard
	fx    effectsCard
	flt   filtersCard
	subs  subsCard
	cov   coverStudio
	fin   finalCard
	side  sideCards
	exp   exportSheet
	gaps  gapsDialog // «Не всі сцени готові» (gaps.go)

	// fake pins states for headless screenshots (tests only).
	fake fakeState
}

// fakeState lets the screenshot tests show states the demo data cannot
// produce on demand (a running montage). Zero in the app.
type fakeState struct {
	job *studio.Job
	// fresh shows the final card as if the film was never montaged.
	fresh bool
}

func newProj(id string) *proj {
	s := &proj{id: id}
	s.tl.init()
	return s
}

// job is the project's job, as the step shows it.
func (s *proj) job(pv *core.ProjectView) *studio.Job {
	if s.fake.job != nil {
		return s.fake.job
	}
	return pv.P.Job
}

// running reports the kind of the running job ("" when idle).
func (s *proj) running(pv *core.ProjectView) studio.JobKind {
	if j := s.job(pv); j != nil && j.Status == studio.JobRunning {
		return j.Kind
	}
	return ""
}

// ---- timeline data ----------------------------------------------------------------------------

// film is the project laid out on the final timeline (studio.Engine.Timeline),
// recomputed only when its inputs change.
type film struct {
	items  []studio.TimelineItem
	pos    map[string]int // scene id → index in items
	scene  map[string]int // scene id → index in p.Scenes
	total  float64
	hidden int // scenes the montage skips: no frame and no clip
	stills int // scenes on the timeline shown as a still with motion
	voices int // scenes with a voice line
	speech float64
}

// item returns the timeline item of a scene (nil when it is not on the timeline).
func (f *film) item(id string) *studio.TimelineItem {
	if k, ok := f.pos[id]; ok {
		return &f.items[k]
	}
	return nil
}

// prepare refreshes the per-frame caches (timeline, signatures, the render
// memo) for the current snapshot. Cheap when nothing changed.
func (s *proj) prepare(c *core.Ctx, pv *core.ProjectView) {
	if s.sigVer != pv.Ver || s.film.pos == nil {
		s.sigVer = pv.Ver
		tl := timelineSig(&pv.P)
		if tl != s.tlSig || s.film.pos == nil {
			s.tlSig = tl
			s.film = buildFilm(c, pv)
		}
		s.outSig = renderSig(&pv.P)
	}
	s.memo.observe(s, pv)
}

func buildFilm(c *core.Ctx, pv *core.ProjectView) film {
	p := &pv.P
	f := film{pos: map[string]int{}, scene: map[string]int{}}
	f.items = c.E.Timeline(*p)
	for i, sc := range p.Scenes {
		f.scene[sc.ID] = i
	}
	for k, it := range f.items {
		f.pos[it.SceneID] = k
		if !it.HasClip {
			f.stills++
		}
		if it.VoiceDuration > 0 {
			f.voices++
			f.speech += it.VoiceDuration
		}
	}
	f.hidden = len(p.Scenes) - len(f.items)
	f.total = studio.TimelineDuration(f.items)
	return f
}

// timelineSig hashes what the timeline depends on.
func timelineSig(p *studio.Project) int64 {
	h := fnv.New64a()
	m := p.Montage
	fmt.Fprintf(h, "%v|%s|%g|%s|", p.Input.Voiceover, m.Transition, m.TransitionDuration, m.Effect)
	for _, s := range p.Scenes {
		fmt.Fprintf(h, "%s|%s|%d|%s|%g|%d|%s|%g|%d|%g|%g|%s|%s|%s|%g|%g|%g;", s.ID,
			s.Frame.Path, s.Frame.UpdatedAt, s.Video.Path, s.Video.Duration, s.Video.UpdatedAt,
			s.Audio.Path, s.Audio.Duration, s.Audio.UpdatedAt, s.TrimIn, s.TrimOut, s.Transition, s.Effect,
			s.AudioSource, s.VoiceShift, s.VoiceTrimIn, s.VoiceTrimOut)
	}
	return int64(h.Sum64() & (1<<62 - 1))
}

// renderSig hashes everything the final video depends on.
func renderSig(p *studio.Project) int64 {
	h := fnv.New64a()
	m := p.Montage
	fmt.Fprintf(h, "%s|%v|%s|%g|%s|%s|%s|%+v|%s|%g|%v|%g;", p.Input.Aspect, p.Input.Voiceover,
		m.Transition, m.TransitionDuration, m.Filter, m.Effect, m.Subtitles, m.SubtitleStyle,
		m.MusicPath, m.MusicVolume, m.Ducking, m.AmbientVolume)
	for _, s := range p.Scenes {
		fmt.Fprintf(h, "%s|%s|%s|%d|%s|%d|%s|%d|%g|%g|%s|%s|%s|%g|%g|%g;", s.ID, s.Narration,
			s.Frame.Path, s.Frame.UpdatedAt, s.Video.Path, s.Video.UpdatedAt, s.Audio.Path, s.Audio.UpdatedAt,
			s.TrimIn, s.TrimOut, s.Transition, s.Effect, s.AudioSource, s.VoiceShift, s.VoiceTrimIn, s.VoiceTrimOut)
	}
	return int64(h.Sum64() & (1<<62 - 1))
}

// renderMemo remembers what the last render was made from, to tell the user
// when the film changed after it ("змонтуй заново").
type renderMemo struct {
	watching bool  // a montage job was seen running
	startSig int64 // renderSig when it started
	outAt    int64 // Output.UpdatedAt the memo describes
	outSig   int64 // renderSig of that output (0 = unknown)
}

func (m *renderMemo) observe(s *proj, pv *core.ProjectView) {
	out := pv.P.Montage.Output
	if s.running(pv) == studio.JobMontage {
		if !m.watching {
			m.watching, m.startSig = true, s.outSig
		}
		return
	}
	if out.UpdatedAt != m.outAt {
		m.outAt = out.UpdatedAt
		m.outSig = s.outSig // first sight (or not watched): assume it matches now
		if m.watching {
			m.outSig = m.startSig
		}
	}
	m.watching = false
}

// outdated reports that the finished video no longer matches the project.
func (s *proj) outdated(pv *core.ProjectView) bool {
	out := pv.P.Montage.Output
	if out.Status != studio.StatusDone || out.Path == "" {
		return false
	}
	if s.memo.outAt == out.UpdatedAt && s.memo.outSig != 0 && s.memo.outSig != s.outSig {
		return true
	}
	// Assets regenerated after the render (seen even after a restart).
	for _, sc := range pv.P.Scenes {
		for _, a := range []studio.Asset{sc.Frame, sc.Video, sc.Audio} {
			if a.Path != "" && a.UpdatedAt > out.UpdatedAt+1000 {
				return true
			}
		}
	}
	return false
}

// ---- StepView ----------------------------------------------------------------------------------

// bleed is how much wider than the main column the workspace lays the step
// out on each side (core.Bleeder): room for card shadows and the page's
// scrollbar, outside the cards.
const bleed unit.Dp = 12

// Bleed implements core.Bleeder.
func (v *view) Bleed() unit.Dp { return bleed }

// Page elements of the scrolling main column.
const (
	elTimeline = iota
	elCards
	elCover
	elEnd
)

// Layout draws the step's main column.
func (v *view) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	s := v.state(pv.ID)
	s.prepare(c, pv)
	th := c.Th
	s.takeScrollRequests(c, pv)
	switch s.scrollTo {
	case "cover":
		s.page.List.Position = layout.Position{First: elCover}
	case "timeline":
		s.page.List.Position = layout.Position{First: elTimeline}
	case "final":
		s.page.List.Position = layout.Position{First: elCards}
	}
	s.scrollTo = ""
	W := gtx.Constraints.Max.X
	b := gtx.Dp(bleed)
	inner := max(1, W-2*b) // the main column itself
	gap := gtx.Dp(gui.Sp24)
	if inner < gtx.Dp(700) {
		gap = gtx.Dp(gui.Sp16)
	}
	cols := columnsFor(gtx, inner)
	d := s.page.Layout(gtx, th, elEnd+1, func(gtx C, i int) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		top := gap
		if i == elTimeline {
			top = 0
		}
		if i == elEnd {
			return D{Size: pt(W, gtx.Dp(gui.Sp40))}
		}
		// Each element takes the column's width, inset by the bleed.
		defer op.Offset(pt(b, top)).Push(gtx.Ops).Pop()
		g := gtx
		g.Constraints = layout.Constraints{Min: pt(inner, 0), Max: pt(inner, gtx.Constraints.Max.Y)}
		var ed D
		switch i {
		case elTimeline:
			ed = s.timelineCard(g, c, pv)
		case elCards:
			ed = s.cards(g, c, pv, cols, gap)
		case elCover:
			ed = s.coverCard(g, c, pv)
		}
		return D{Size: pt(W, ed.Size.Y+top)}
	})
	topFade(gtx, th, &s.page)
	s.pop.layout(gtx, c, pv, s)
	s.exportLayout(gtx, c, pv)
	s.gapsLayout(gtx, c, pv)
	return d
}

// topFade softens the page's top edge under the step tabs once it scrolls
// (the same touch as the workspace's own steps).
func topFade(gtx C, th *gui.Theme, sc *gui.Scroll) {
	if pos := sc.List.Position; pos.First == 0 && pos.Offset <= 0 {
		return
	}
	h := gtx.Dp(16)
	defer clip.Rect(image.Rect(0, 0, gtx.Constraints.Max.X, h)).Push(gtx.Ops).Pop()
	paint.LinearGradientOp{
		Stop1: f32.Pt(0, 0), Color1: th.Pal.Ground,
		Stop2: f32.Pt(0, float32(h)), Color2: gui.WithAlpha(th.Pal.Ground, 0),
	}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// takeScrollRequests handles the workspace's "show this scene" request
// and the project menu's «Експортувати в редактор…».
func (s *proj) takeScrollRequests(c *core.Ctx, pv *core.ProjectView) {
	u := c.UI(pv.ID)
	if u.Export {
		u.Export = false
		s.scrollTo = "final"
		s.openExport(c, pv, "")
	}
	if u.ScrollTo == "" {
		return
	}
	id := u.ScrollTo
	u.ScrollTo = ""
	if s.film.item(id) == nil {
		return
	}
	u.Scene = id
	s.scrollTo = "timeline"
	s.tl.revealScene = id
}

// columnsFor is the card grid's column count for a main column px wide.
func columnsFor(gtx C, px int) int {
	dp := float32(px) / gtx.Metric.PxPerDp
	switch {
	case dp >= 1600:
		return 3
	case dp >= 1000:
		return 2
	}
	return 1
}

// cards lays the settings cards out as a masonry of cols columns: each
// column stacks its cards, so no row leaves a hole under a short card.
func (s *proj) cards(gtx C, c *core.Ctx, pv *core.ProjectView, cols, gap int) D {
	all := map[string]layout.Widget{
		"final":   func(gtx C) D { return s.finalCard(gtx, c, pv) },
		"sound":   func(gtx C) D { return s.soundCard(gtx, c, pv) },
		"trans":   func(gtx C) D { return s.transCard(gtx, c, pv) },
		"fx":      func(gtx C) D { return s.effectsCard(gtx, c, pv) },
		"filters": func(gtx C) D { return s.filtersCard(gtx, c, pv) },
		"subs":    func(gtx C) D { return s.subsCard(gtx, c, pv) },
	}
	var plan [][]string
	switch cols {
	case 3:
		plan = [][]string{{"final", "filters"}, {"sound", "trans"}, {"subs", "fx"}}
	case 2:
		plan = [][]string{{"final", "trans", "filters"}, {"sound", "subs", "fx"}}
	default:
		plan = [][]string{{"final", "sound", "trans", "fx", "filters", "subs"}}
	}
	W := gtx.Constraints.Max.X
	cw := (W - gap*(len(plan)-1)) / len(plan)
	h := 0
	for k, col := range plan {
		x := k * (cw + gap)
		cg := gtx
		cg.Constraints = layout.Constraints{Min: pt(cw, 0), Max: pt(cw, 1<<20)}
		y := 0
		for i, name := range col {
			if i > 0 {
				y += gap
			}
			cd, call := measure(cg, all[name])
			stack := opOffset(gtx, x, y)
			call.Add(gtx.Ops)
			stack.Pop()
			y += cd.Size.Y
		}
		h = max(h, y)
	}
	return D{Size: pt(W, h)}
}

// Side draws the step's context cards: the render summary and the cover status.
func (v *view) Side(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	s := v.state(pv.ID)
	s.prepare(c, pv)
	return s.sideCards(gtx, c, pv)
}

// Next is the step's call to action.
func (v *view) Next(c *core.Ctx, pv *core.ProjectView) core.Next {
	s := v.state(pv.ID)
	s.prepare(c, pv)
	id := pv.ID
	p := &pv.P
	if j := s.job(pv); j != nil && j.Status == studio.JobRunning {
		if j.Kind == studio.JobMontage {
			return core.Next{Icon: gui.IconClapperboard, Title: "Монтуємо відео…", Text: jobLine(j, "Склеюємо сцени, звук і субтитри"), Busy: true}
		}
		return core.Next{Icon: gui.IconHourglass, Title: core.JobLabel(j.Kind) + " — триває", Text: jobLine(j, "Монтаж можна запустити, щойно етап завершиться"), Busy: true}
	}
	if len(s.film.items) == 0 {
		cfg := c.E.Config().Get()
		stockMode := cfg.Stock.Enabled || cfg.Video.Provider == "stock" || p.Input.Pipeline == "stock"
		title := "Спершу потрібні кадри"
		text := "Монтувати поки нема чого: згенеруй кадри сцен на кроці «Розкадровка»."
		step := 3
		if stockMode {
			title = "Спершу знайди відео"
			text = "Монтувати поки нема чого: знайди стокові відео на кроці «Відео й озвучка»."
			step = 4
		}
		return core.Next{Icon: gui.IconImages, Title: title,
			Text: text,
			Do:   func() { c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: step}) }}
	}
	out := p.Montage.Output
	if out.Status == studio.StatusDone && out.Path != "" && s.fin.size.get(pv.Abs(out.Path), out.UpdatedAt) > 0 {
		if s.outdated(pv) {
			return core.Next{Icon: gui.IconRefreshCw, Title: "Змонтувати заново",
				Text: "Після монтажу були зміни — збери відео ще раз, це не витрачає кредитів.",
				Do:   func() { s.runMontageNow(c, id) }}
		}
		abs := pv.Abs(out.Path)
		return core.Next{Icon: gui.IconPlay, Title: "Відео готове — відкрити",
			Text: core.Clock(out.Duration) + " · відкрий, збережи або зроби обкладинку",
			Do:   func() { c.OpenPath(abs) }}
	}
	text := fmt.Sprintf("%s · ≈ %s · локально через FFmpeg, без кредитів", count(len(s.film.items), "сцена", "сцени", "сцен"), core.Clock(s.film.total))
	return core.Next{Icon: gui.IconClapperboard, Title: "Змонтувати відео", Text: text, Do: func() { s.runMontageNow(c, id) }}
}

// runMontageNow starts the render from a callback (fresh snapshot).
func (s *proj) runMontageNow(c *core.Ctx, id string) {
	if pv := c.Project(id); pv != nil {
		s.runMontage(c, pv)
	}
}

// jobLine is a job's message with its percent.
func jobLine(j *studio.Job, fallback string) string {
	msg := strings.TrimSpace(j.Message)
	if msg == "" || msg == "Запуск…" {
		msg = fallback
	}
	if j.Progress > 0 {
		msg = fmt.Sprintf("%s · %d%%", msg, min(100, j.Progress))
	}
	return msg
}

// ---- shared bits ---------------------------------------------------------------------------------

// fileSize caches the size of a file by path and version.
type fileSize struct {
	path string
	ver  int64
	n    int64
}

func (f *fileSize) get(path string, ver int64) int64 {
	if path != f.path || ver != f.ver {
		f.path, f.ver, f.n = path, ver, 0
		if st, err := os.Stat(path); err == nil {
			f.n = st.Size()
		}
	}
	return f.n
}

// sceneIndex is the 1-based number of a scene in the project ("Сцена 3").
func sceneNumber(p *studio.Project, id string) int {
	for i, s := range p.Scenes {
		if s.ID == id {
			return i + 1
		}
	}
	return 0
}

// transitionLabel is the Ukrainian name of a transition id.
func transitionLabel(id string) string {
	if t, ok := media.TransitionByID(id); ok {
		return t.Label
	}
	if id == "" {
		t, _ := media.TransitionByID(media.DefaultTransition)
		return t.Label
	}
	return "Без переходу"
}

// effectLabel is the Ukrainian name of a motion effect id ("" = the default).
func effectLabel(id string) string {
	if e, ok := media.EffectByID(id); ok {
		return e.Label
	}
	e, _ := media.EffectByID(media.DefaultEffect)
	return e.Label
}

// filterLabel is the Ukrainian name of a colour look.
func filterLabel(id string) string {
	if f, ok := media.FilterByID(id); ok {
		return f.Label
	}
	return "Без фільтра"
}

// projectTransition is the project's default transition id ("" = fade, like new projects).
func projectTransition(m studio.Montage) string {
	if _, ok := media.TransitionByID(m.Transition); ok {
		return m.Transition
	}
	if m.Transition == "" {
		return media.DefaultTransition
	}
	return "none"
}

// transDuration is the effective transition duration in seconds.
func transDuration(m studio.Montage) float64 {
	d := m.TransitionDuration
	if d <= 0 {
		d = media.DefaultTransitionDuration
	}
	return clampf(d, media.MinTransitionDuration, media.MaxTransitionDuration)
}

// now is the frame clock (tests pin it).
func now(gtx C) time.Time { return gtx.Now }

// dpf converts a float dp to px.
func dpf(gtx C, v float32) float32 { return v * gtx.Metric.PxPerDp }
