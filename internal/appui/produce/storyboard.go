package produce

// Step 3 — Розкадровка: a header card with the counts and the bulk actions
// (references, all frames, redraw changed); the references (cast +
// locations, why they matter); and the scene frames as a virtualised
// responsive grid (or a list). In storyboard mode every card can show its
// 3×3 sheet. Any picture opens the Lightbox. The running job itself (its
// progress, "Зупинити", a failure with "Повторити") is the workspace
// banner's: the step marks what is busy and disables what cannot start.

import (
	"fmt"
	"image/color"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type storyboard struct {
	projects map[string]*sbState
	lb       Lightbox
	lbRefs   bool   // the lightbox shows the references (else the frames)
	lbProj   string // project of the open lightbox
	desc     descEditor
	frame    int64     // Ctx frame of the last Layout
	code     *codeView // «Анімація кодом» films: this step writes the animations (codeanim.go)
}

func newStoryboard() *storyboard {
	return &storyboard{projects: map[string]*sbState{}, code: newCodeView()}
}

// sbState is the step's state for one project.
type sbState struct {
	list    pageList
	stagger gui.Stagger
	hl      highlight
	frames  map[string]*frameCard
	refs    map[string]*refCard
	head    sbHead
	batch   batch
	view    gui.Segmented
	toRefs  bool // scroll to the references on the next frame
	refsEl  int  // element index of the references head (last frame)
	toPlan  gui.Clickable
	side    sideState
}

type sbHead struct {
	refs, frames, stale gui.Clickable
	job                 jobBox // the "redraw changed" batch
	ring                gui.Anim
}

type frameCard struct {
	img       gui.ImageView
	open      pressArea
	veil      veil
	regen     gui.Clickable
	prompt    gui.Clickable
	upload    gui.Clickable
	reveal    gui.Clickable
	redraw    gui.Clickable
	collapse  gui.Clickable
	textClick widget.Clickable
	mode      gui.Segmented
	board     bool // show the 3×3 sheet (storyboard mode)
	boardSet  bool // board chosen by the user (else: sheet when it exists)
	editing   bool
	focus     bool
	field     gui.TextField
	exp       expander
	rw        rewriteNote
}

type refCard struct {
	img    gui.ImageView
	open   pressArea
	veil   veil
	regen  gui.Clickable
	edit   gui.Clickable
	upload gui.Clickable
	reveal gui.Clickable
	rw     rewriteNote
}

func (v *storyboard) state(id string) *sbState {
	st := v.projects[id]
	if st == nil {
		st = &sbState{frames: map[string]*frameCard{}, refs: map[string]*refCard{}}
		st.stagger.Max = 12
		v.projects[id] = st
	}
	return st
}

func (st *sbState) frame(id string) *frameCard {
	fc := st.frames[id]
	if fc == nil {
		fc = &frameCard{}
		st.frames[id] = fc
	}
	return fc
}

func (st *sbState) ref(id string) *refCard {
	rc := st.refs[id]
	if rc == nil {
		rc = &refCard{}
		st.refs[id] = rc
	}
	return rc
}

// showsBoard: the card shows the 3×3 sheet (storyboard mode, a sheet exists
// unless the user picked the frame).
func (fc *frameCard) showsBoard(p *studio.Project, s *studio.Scene) bool {
	if p.Input.StoryboardMode != studio.StoryboardGrid || s.Board.Path == "" {
		return false
	}
	if fc.boardSet {
		return fc.board
	}
	return true
}

// ---- counts ---------------------------------------------------------------------------

type frameStats struct {
	scenes, done, stale, busy, errs, empty int
	refs, refsDone, refsBusy, refsErr      int
	boards                                 int
	// skipRefs: «Без референсів» — no reference images are drawn (refs = 0)
	skipRefs bool
}

// left is what "generate frames" would draw (not done yet, failed included).
func (n frameStats) left() int { return n.scenes - n.done - n.busy }

func (n frameStats) refsLeft() int { return n.refs - n.refsDone - n.refsBusy }

func countFrames(p *studio.Project) frameStats {
	n := frameStats{scenes: len(p.Scenes), refs: len(p.Characters) + len(p.Locations)}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		a := s.Frame
		switch {
		case busy(a):
			n.busy++
		case a.Status == studio.StatusDone:
			n.done++
			if a.Stale {
				n.stale++
			}
		case a.Status == studio.StatusError:
			n.errs++
		default:
			n.empty++
		}
		if s.Board.Path != "" {
			n.boards++
		}
	}
	ref := func(a studio.Asset) {
		switch {
		case busy(a):
			n.refsBusy++
		case a.Status == studio.StatusDone:
			n.refsDone++
		case a.Status == studio.StatusError:
			n.refsErr++
		}
	}
	if p.Input.SkipRefs() {
		n.refs, n.skipRefs = 0, true
		return n
	}
	for _, c := range p.Characters {
		ref(c.Image)
	}
	for _, l := range p.Locations {
		ref(l.Image)
	}
	return n
}

// ---- layout ---------------------------------------------------------------------------

func (v *storyboard) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	if pv == nil {
		return D{Size: gtx.Constraints.Max}
	}
	th := c.Th
	st := v.state(pv.ID)
	v.lb.Background, v.lb.Invalidate = c.Background, c.Invalidate
	p := &pv.P
	// Overlays do not outlive the step: closed when the step was not drawn
	// on the previous frame (another step, page or project).
	if f := c.Frame(); v.frame != 0 && f-v.frame > 1 {
		v.lb.Close()
		v.desc.dlg.Close()
	}
	v.frame = c.Frame()
	if v.lbProj != "" && v.lbProj != pv.ID {
		v.lb.Close()
	}
	if p.Input.CodeAnim() {
		return v.code.Layout(gtx, c, pv)
	}
	if len(p.Scenes) == 0 {
		d := v.emptyPlan(gtx, c, pv, st)
		v.desc.Layout(gtx, c, pv)
		return d
	}
	n := countFrames(p)
	u := c.UI(pv.ID)
	inset := gtx.Dp(bleed)
	W := max(0, gtx.Constraints.Max.X-2*inset)
	vertical := p.Input.Aspect == "9:16"
	listMode := u.List && W >= gtx.Dp(640)
	g := gui.Grid{MinWidth: 300, Gap: gui.Sp24, Stretch: true}
	if vertical {
		g.MinWidth = 210
	}
	if W < gtx.Dp(700) {
		g.Gap = gui.Sp16
	}
	cols := fillCols(g.Columns(gtx, W), len(p.Scenes))
	g.MaxCols = cols
	if listMode {
		cols = 1
	}
	rows := (len(p.Scenes) + cols - 1) / cols
	gapPx := gtx.Dp(g.Gap)

	els := make([]layout.Widget, 0, rows+6)
	els = append(els, func(gtx C) D {
		return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp32}.Layout(gtx, func(gtx C) D {
			return gui.FadeUp(gtx, st.stagger.Progress(gtx, 0), 8, func(gtx C) D { return v.header(gtx, c, pv, st, n) })
		})
	})
	st.refsEl = -1
	if n.refs > 0 {
		st.refsEl = len(els)
		els = append(els,
			func(gtx C) D {
				return layout.Inset{Bottom: gui.Sp16}.Layout(gtx, func(gtx C) D {
					return sectionHead(gtx, th, "Референси", fmt.Sprint(n.refs),
						"Героїв і місця студія малює один раз — з цих зображень будуються всі кадри, тому обличчя, одяг і місця однакові в кожній сцені.", nil)
				})
			},
			func(gtx C) D {
				return layout.Inset{Bottom: gui.Sp40}.Layout(gtx, func(gtx C) D { return v.refsGrid(gtx, c, pv, st) })
			},
		)
	}
	if n.skipRefs {
		els = append(els, func(gtx C) D {
			return layout.Inset{Bottom: gui.Sp32}.Layout(gtx, func(gtx C) D {
				return sectionHead(gtx, th, "Референси", "пропущено",
					"Режим «"+studio.PipelineLabel(p.Input.Pipeline)+"»: героїв і місця не малюють окремо — їхній вигляд з опису плану додається в кожен промт кадру. "+
						"Схожість між сценами трохи нижча; повний режим — на кроці «Сценарій» → «Режим виробництва».", nil)
			})
		})
	}
	els = append(els, func(gtx C) D {
		return layout.Inset{Bottom: gui.Sp16}.Layout(gtx, func(gtx C) D { return v.framesHead(gtx, c, pv, st, n) })
	})
	firstRow := len(els)
	for r := 0; r < rows; r++ {
		start, end := r*cols, min(len(p.Scenes), (r+1)*cols)
		last := r == rows-1
		els = append(els, func(gtx C) D {
			bottom := gapPx
			if last {
				bottom = gtx.Dp(gui.Sp32)
			}
			d := func() D {
				if listMode {
					return v.frameRow(gtx, c, pv, st, start)
				}
				return g.Layout(gtx, end-start, func(gtx C, k int) D { return v.frameCard(gtx, c, pv, st, start+k) })
			}()
			d.Size.Y += bottom
			return d
		})
	}

	// Jumps: the scene navigator (ScrollTo) and the tip's "to references".
	if id := u.ScrollTo; id != "" {
		u.ScrollTo = ""
		if _, i := pv.Scene(id); i >= 0 {
			st.list.ScrollTo(gtx, firstRow+i/cols, gtx.Dp(gui.Sp8))
			st.hl.Flash(id)
			u.Scene = id
		}
	}
	if st.toRefs {
		st.toRefs = false
		if st.refsEl >= 0 {
			st.list.ScrollTo(gtx, st.refsEl, gtx.Dp(gui.Sp8))
		}
	}

	d := st.list.Layout(gtx, th, len(els), inset, func(gtx C, i int) D { return els[i](gtx) })
	v.layoutLightbox(gtx, c, pv)
	v.desc.Layout(gtx, c, pv)
	return d
}

// emptyPlan fills the column before a plan exists.
func (v *storyboard) emptyPlan(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState) D {
	if st.toPlan.Clicked(gtx) {
		goStep(c, pv.ID, 2)
	}
	return emptyColumn(gtx, c, pv, &st.toPlan, gui.IconImages,
		"Кадри з'являться після плану",
		"Розкадровка малюється за планом: герої, місця й сцени. Спершу створи план на кроці «План» — це займе хвилину.",
		[]howStep{
			{gui.IconUsers, "Герої й місця", "Студія малює кожного героя й кожне місце один раз — це референси."},
			{gui.IconImages, "Кадри сцен", "Перший кадр кожної сцени малюється з цими референсами."},
			{gui.IconClapperboard, "Далі — відео", "На наступному кроці кадри оживають у кліпи."},
		})
}

// howStep is one tile of the "how this step works" row of an empty step.
type howStep struct {
	icon        *gui.Icon
	title, text string
}

// emptyColumn is the no-plan state shared by steps 3 and 4: a card over the
// whole column with the empty state in the middle (a spinner while the plan
// is being written).
func emptyColumn(gtx C, c *core.Ctx, pv *core.ProjectView, action *gui.Clickable, icon *gui.Icon, title, text string, how []howStep) D {
	th := c.Th
	planning := pv.RunningKind() == studio.JobPlan || pv.RunningKind() == studio.JobAutopilot || pv.RunningKind() == studio.JobTranscribe
	H := gtx.Constraints.Max.Y
	return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp24, Left: bleed, Right: bleed}.Layout(gtx, func(gtx C) D {
		h := max(gtx.Dp(360), H-gtx.Dp(gui.Sp8+gui.Sp24))
		gtx.Constraints.Min = pt(gtx.Constraints.Max.X, min(h, gtx.Constraints.Max.Y))
		return gui.Card{}.Layout(gtx, th, func(gtx C) D {
			return layout.Center.Layout(gtx, func(gtx C) D {
				if planning {
					msg := "Студія розкладає історію на сцени. Щойно план буде готовий, тут з'являться кадри."
					if j := pv.P.Job; j != nil && strings.TrimSpace(j.Message) != "" {
						msg = j.Message
					}
					return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(gui.ProgressRing{Value: -1, Size: 72, Label: "-", Tone: gui.ToneRunning}.Widget(th)),
						layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
						layout.Rigid(func(gtx C) D {
							return gui.EmptyState{Icon: gui.IconSparkles, Title: "План пишеться…", Text: msg}.Layout(gtx, th, nil)
						}),
					)
				}
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return gui.EmptyState{Icon: icon, Title: title, Text: text, Action: "До плану", ActionIcon: gui.IconArrowRight}.Layout(gtx, th, action)
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp40}.Layout),
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(820))
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return gui.Grid{MinWidth: 200, Gap: gui.Sp16, MaxCols: 3, Stretch: true}.Layout(gtx, len(how), func(gtx C, i int) D {
							return howTile(gtx, th, how[i])
						})
					}),
				)
			})
		})
	})
}

// howTile is an explainer tile of an empty step (flat Surface fill inside
// the empty-state card): the icon beside the title, a line under them. The
// tiles read left to right, so they need no numbers.
func howTile(gtx C, th *gui.Theme, h howStep) D {
	return gui.Card{Tint: true, Flat: true, Radius: gui.RadiusInner, Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return hrow(gtx, gui.Sp12,
					func(gtx C) D {
						s := gtx.Dp(40)
						gui.FillCircle(gtx, 40, th.Pal.Card)
						o := op.Offset(pt((s-gtx.Dp(20))/2, (s-gtx.Dp(20))/2)).Push(gtx.Ops)
						h.icon.Layout(gtx, 20, th.Pal.ActionText)
						o.Pop()
						return D{Size: pt(s, s)}
					},
					label(th, th.Label.Weighted(gui.FontSemiBold), th.Pal.Text, 1, h.title),
				)
			}),
			layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
			layout.Rigid(label(th, th.Small, th.Pal.Text2, 3, h.text)),
		)
	})
}

// ---- header -----------------------------------------------------------------------------

func (v *storyboard) header(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState, n frameStats) D {
	th := c.Th
	h := &st.head
	p := &pv.P
	running := pv.Running()
	kind := pv.RunningKind()
	grid := p.Input.StoryboardMode == studio.StoryboardGrid
	batchOn := st.batch.active()

	if h.refs.Clicked(gtx) {
		if n.refsLeft() > 0 {
			c.Run(pv.ID, studio.StageRefs, false, "")
		} else {
			c.Confirm(core.Confirm{
				Title: "Перемалювати всі референси?",
				Text:  "Герої й місця будуть намальовані заново з поточних описів. Це витрачає кредити, а кадри, намальовані зі старих референсів, не зміняться самі.",
				Icon:  gui.IconUsers, OK: "Перемалювати",
				OnOK: func() { c.Run(pv.ID, studio.StageRefs, true, "") },
			})
		}
	}
	if h.frames.Clicked(gtx) {
		if n.left() > 0 {
			c.Run(pv.ID, studio.StageFrames, false, "")
		} else {
			c.Confirm(core.Confirm{
				Title: "Перемалювати всі кадри?",
				Text:  "Кожен кадр буде намальовано заново з поточних промтів — це витрачає кредити. Кліпи, зняті зі старих кадрів, доведеться перезняти.",
				Icon:  gui.IconImages, OK: "Перемалювати все",
				OnOK: func() { c.Run(pv.ID, studio.StageFrames, true, "") },
			})
		}
	}
	if h.stale.Clicked(gtx) {
		v.redrawStale(c, pv, st)
	}
	summary := func(gtx C) D {
		left := func(gtx C) D {
			frac := float32(0)
			if n.scenes > 0 {
				frac = float32(n.done) / float32(n.scenes)
			}
			tone := gui.ToneNeutral
			if n.done == n.scenes && n.stale == 0 {
				tone = gui.ToneSuccess
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(gui.ProgressRing{Value: frac, Size: 64, Tone: tone, Anim: &h.ring}.Widget(th)),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(label(th, th.H3, th.Pal.Text, 2, fmt.Sprintf("Кадри: %d з %d готово", n.done, n.scenes))),
						layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
						layout.Rigid(func(gtx C) D {
							note := runningNote(pv, grid)
							if note == "" {
								return D{}
							}
							return label(th, th.Small.Weighted(gui.FontMedium), th.Pal.RunningInk, 1, note)(gtx)
						}),
						layout.Rigid(label(th, th.Small, th.Pal.Text2, 3, headLine(n, grid))),
					)
				}),
			)
		}
		right := func(gtx C) D {
			disabled := running || batchOn
			var ws []layout.Widget
			if n.refs > 0 {
				txt, bk := "Згенерувати референси", gui.Secondary
				if n.refsLeft() == 0 {
					txt, bk = "Референси заново", gui.Tinted
				}
				ws = append(ws, func(gtx C) D {
					return gui.Button{Kind: bk, Text: txt, Icon: gui.IconUsers, Busy: kind == studio.JobRefs,
						Disabled: disabled, Tip: "Персонажі й локації — основа схожості в усіх кадрах"}.Layout(gtx, th, &h.refs)
				})
			}
			if n.stale > 0 && !running {
				ws = append(ws, func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Text: fmt.Sprintf("Перегенерувати змінені · %d", n.stale), Icon: gui.IconRefreshCw,
						Busy: batchOn, Disabled: running, Tip: "Лише кадри, чиї промти змінились після малювання"}.Layout(gtx, th, &h.stale)
				})
			}
			ws = append(ws, func(gtx C) D {
				b := gui.Button{Kind: gui.Primary, Icon: gui.IconSparkles, Busy: kind == studio.JobFrames, Disabled: disabled}
				switch left := n.left(); {
				case left == 0:
					b.Kind, b.Icon, b.Text = gui.Tinted, gui.IconImages, "Кадри заново"
					b.Tip = "Перемалювати всі кадри з поточних промтів"
				case left == n.scenes:
					b.Text = "Згенерувати всі кадри"
					if grid {
						b.Text = "Згенерувати розкадровки"
					}
				default:
					b.Text = fmt.Sprintf("Згенерувати кадри · %d", left)
				}
				return b.Layout(gtx, th, &h.frames)
			})
			return gui.Flow{Gap: gui.Sp12}.Layout(gtx, ws...)
		}
		return spread(gtx, gui.Sp24, 300, left, right)
	}
	// The running job and a failed one are shown by the workspace banner
	// above the tabs; the header only adds its own "redraw changed" batch.
	ws := []layout.Widget{summary}
	if batchOn {
		ws = append(ws, func(gtx C) D {
			done, total := st.batch.progress()
			pct := 0
			if total > 0 {
				pct = done * 100 / total
			}
			d, stop := h.job.Layout(gtx, th, st.batch.label, fmt.Sprintf("Готово %d з %d", done, total), pct)
			if stop {
				st.batch.Stop()
				c.Toast(gui.ToneInfo, "Нові кадри не стартують; ті, що вже малюються, доробляться")
			}
			return d
		})
	}
	return gui.Card{Padding: headPad(gtx)}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return vstack(gtx, gui.Sp20, ws...)
	})
}

// headPad is the step header's padding (tighter on phones).
func headPad(gtx C) unit.Dp {
	if gtx.Constraints.Max.X < gtx.Dp(520) {
		return gui.Sp16
	}
	return gui.Sp24
}

// fillCols lets a short scene list spread over the column: fewer scenes
// than columns get wider cards (but a single scene still takes half).
func fillCols(cols, n int) int {
	if n >= cols {
		return cols
	}
	return max(n, min(cols, 2))
}

// runningNote names the job that runs right now ("Зараз: малюємо кадри.");
// its progress and "Зупинити" are in the workspace banner.
func runningNote(pv *core.ProjectView, grid bool) string {
	if !pv.Running() {
		return ""
	}
	t := []rune(jobTitle(pv.RunningKind(), grid))
	if len(t) > 0 {
		t[0] = []rune(strings.ToLower(string(t[0])))[0]
	}
	return "Зараз: " + string(t) + "."
}

// headLine is the second line of the step-3 header.
func headLine(n frameStats, grid bool) string {
	var parts []string
	if n.skipRefs {
		parts = append(parts, "референси пропущено")
	}
	if n.refs > 0 {
		parts = append(parts, fmt.Sprintf("Референси: %d з %d готово", n.refsDone, n.refs))
	}
	if n.busy > 0 {
		parts = append(parts, fmt.Sprintf("малюється %d", n.busy))
	}
	if n.stale > 0 {
		parts = append(parts, core.Count(n.stale, "кадр змінено", "кадри змінено", "кадрів змінено"))
	}
	if n.errs > 0 {
		parts = append(parts, core.Count(n.errs, "помилка", "помилки", "помилок"))
	}
	if n.refsErr > 0 {
		parts = append(parts, core.Count(n.refsErr, "референс не вдався", "референси не вдались", "референсів не вдались"))
	}
	if grid {
		parts = append(parts, "режим: розкадровка 3×3")
	}
	if len(parts) == 0 {
		return "Кадри малюються з референсами героїв і місць."
	}
	return strings.Join(parts, " · ")
}

// jobTitle names a running job in plain words.
func jobTitle(k studio.JobKind, grid bool) string {
	switch k {
	case studio.JobRefs:
		return "Малюємо референси"
	case studio.JobFrames:
		if grid {
			return "Малюємо розкадровки 3×3"
		}
		return "Малюємо кадри"
	case studio.JobVideos:
		return "Знімаємо відео"
	case studio.JobVoices:
		return "Озвучуємо"
	case studio.JobRender:
		return "Знімаємо відео й озвучуємо"
	case studio.JobAutopilot:
		return "Працює автопілот"
	case studio.JobPlan:
		return "Пишемо план"
	case studio.JobMontage:
		return "Монтуємо фільм"
	case studio.JobCover:
		return "Малюємо обкладинку"
	}
	return "Працює: " + core.JobLabel(k)
}

// redrawStale redraws the frames whose prompt changed after drawing.
func (v *storyboard) redrawStale(c *core.Ctx, pv *core.ProjectView, st *sbState) {
	st.batch.start(c, pv.ID, "Перемальовуємо змінені кадри", staleFrameItems(&pv.P))
}

// staleFrameItems are the frames (sheets) drawn before their prompt changed.
func staleFrameItems(p *studio.Project) []batchItem {
	var items []batchItem
	for i := range p.Scenes {
		if s := &p.Scenes[i]; frameStale(s) && !busy(s.Frame) {
			items = append(items, batchItem{kind: studio.RegenFrame, id: s.ID})
		}
	}
	return items
}

// ---- references ------------------------------------------------------------------------

func (v *storyboard) refsGrid(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState) D {
	th := c.Th
	p := &pv.P
	nc, nl := len(p.Characters), len(p.Locations)
	W := gtx.Constraints.Max.X
	gap := gui.Sp20
	charMin := unit.Dp(160)
	locMin := unit.Dp(250)
	if p.Input.Aspect == "9:16" {
		locMin = 146
	}
	need := func(n int, minW unit.Dp) int {
		if n == 0 {
			return 0
		}
		return n*gtx.Dp(minW) + (n-1)*gtx.Dp(gap)
	}
	group := func(title string, n int, minW unit.Dp, maxCols int, card func(gtx C, i int) D) layout.Widget {
		return func(gtx C) D {
			return vstack(gtx, gui.Sp12,
				label(th, th.Label, th.Pal.Text2, 1, fmt.Sprintf("%s · %d", title, n)),
				func(gtx C) D {
					return gui.Grid{MinWidth: minW, Gap: gap, MaxCols: maxCols, Stretch: true}.Layout(gtx, n, card)
				},
			)
		}
	}
	chars := func(maxCols int) layout.Widget {
		return group("Персонажі", nc, charMin, maxCols, func(gtx C, i int) D { return v.refCard(gtx, c, pv, st, studio.RegenCharacter, i) })
	}
	locs := func(maxCols int) layout.Widget {
		return group("Локації", nl, locMin, maxCols, func(gtx C, i int) D { return v.refCard(gtx, c, pv, st, studio.RegenLocation, i) })
	}
	colGap := gtx.Dp(gui.Sp32)
	if nc > 0 && nl > 0 && need(nc, charMin)+need(nl, locMin)+colGap <= W {
		return gui.Columns{Gap: gui.Sp32}.Layout(gtx,
			gui.Col{Span: float32(need(nc, charMin)), W: chars(nc)},
			gui.Col{Span: float32(need(nl, locMin)), W: locs(nl)})
	}
	// Stacked: each group as wide as its cards need (portraits stay a
	// sensible size instead of stretching over the column).
	capped := func(w layout.Widget, n int, maxW unit.Dp) layout.Widget {
		return func(gtx C) D {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, n*gtx.Dp(maxW)+(n-1)*gtx.Dp(gap))
			d := w(gtx)
			d.Size.X = W
			return d
		}
	}
	var ws []layout.Widget
	if nc > 0 {
		ws = append(ws, capped(chars(nc), nc, charMin*1.4))
	}
	if nl > 0 {
		ws = append(ws, capped(locs(nl), nl, locMin*1.5))
	}
	return vstack(gtx, gui.Sp24, ws...)
}

func (v *storyboard) refCard(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState, kind studio.RegenKind, i int) D {
	th := c.Th
	p := &pv.P
	var id, name, tag, desc string
	var a studio.Asset
	ratio := float32(3.0 / 4)
	what := "персонажа"
	if kind == studio.RegenCharacter {
		ch := &p.Characters[i]
		id, name, tag, desc, a = ch.ID, ch.Name, ch.Tag, ch.Description, ch.Image
	} else {
		l := &p.Locations[i]
		id, name, tag, desc, a = l.ID, l.Name, l.Tag, l.Description, l.Image
		ratio = ratioOf(p.Input.Aspect)
		what = "локації"
	}
	name = firstText(name, "Без імені")
	rc := st.ref(id)
	running := pv.Running()
	rc.rw.update(gtx, c, pv.ID, kind, id)
	if rc.open.Clicked(gtx) && a.Path != "" {
		v.openRefs(pv, id)
	}
	if rc.regen.Clicked(gtx) {
		c.Regen(pv.ID, kind, id, "")
	}
	if rc.edit.Clicked(gtx) {
		v.desc.Open(pv.ID, kind, id, name, desc)
	}
	if rc.upload.Clicked(gtx) {
		pickImage(c, pv.ID, kind, id)
	}
	if rc.reveal.Clicked(gtx) && a.Path != "" {
		c.Reveal(pv.Abs(a.Path))
	}
	idx := i
	if kind == studio.RegenLocation {
		idx += len(p.Characters)
	}
	return gui.FadeUp(gtx, st.stagger.Progress(gtx, 1+min(idx, 3)), 10, func(gtx C) D {
		return gui.Card{Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
			media := func(gtx C) D {
				ph := gui.IconUser
				if kind == studio.RegenLocation {
					ph = gui.IconMountain
				}
				return rc.img.Layout(gtx, th, c.Img, pv.Abs(a.Path), a.UpdatedAt, gui.Thumb{Ratio: ratio, Placeholder: ph, Overlay: func(gtx C) D {
					size := gtx.Constraints.Max
					rc.open.Layout(gtx, th, size, a.Path != "" && !busy(a))
					m := gtx.Dp(8)
					rc.veil.Layout(gtx, th, size, busy(a), busyPct(a))
					switch {
					case busy(a):
					case a.Path == "" && a.Status == studio.StatusError:
						emptyCaption(gtx, th, size, "Не вдалося намалювати")
					case a.Path == "":
						emptyCaption(gtx, th, size, "Ще не намальовано")
					}
					// The ring already says "running"; small cards show the
					// other states as a pill.
					if a.Status != studio.StatusRunning && (a.Status != studio.StatusDone || a.Stale) {
						pill := assetPill(a)
						pill.Small = true
						corner(gtx, size, m, true, false, pill.Widget(th))
					}
					return D{Size: size}
				}})
			}
			info := func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(label(th, th.Label.Weighted(gui.FontSemiBold), th.Pal.Text, 1, name)),
					layout.Rigid(func(gtx C) D {
						if tag == "" {
							return D{}
						}
						return label(th, th.Caption, th.Pal.Text3, 1, "@"+tag)(gtx)
					}),
				)
			}
			rwNote := func(gtx C) D { return rc.rw.Layout(gtx, th, a.Rewrite) }
			errLine := func(gtx C) D {
				if a.Status != studio.StatusError {
					return D{}
				}
				return label(th, th.Caption, th.Pal.ErrorInk, 6, assetErr(a))(gtx)
			}
			actions := func(gtx C) D {
				regenTip := "Перемалювати"
				switch {
				case a.Status == studio.StatusError:
					regenTip = "Спробувати ще раз"
				case a.Path == "":
					regenTip = "Намалювати"
				}
				dis := running || busy(a)
				bs := unit.Dp(34)
				if gtx.Constraints.Max.X < gtx.Dp(146) {
					bs = 30
				}
				return layout.Flex{Spacing: layout.SpaceBetween, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconRefreshCw, Size: bs, Tip: regenTip, Disabled: dis}.Layout(gtx, th, &rc.regen)
					}),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconPenLine, Size: bs, Tip: "Змінити опис " + what + " і перемалювати", Disabled: dis}.Layout(gtx, th, &rc.edit)
					}),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconUpload, Size: bs, Tip: "Завантажити своє зображення", Disabled: busy(a)}.Layout(gtx, th, &rc.upload)
					}),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconFolderOpen, Size: bs, Tip: "Показати у папці", Disabled: a.Path == "" || c.Offline}.Layout(gtx, th, &rc.reveal)
					}),
				)
			}
			return pinned(gtx, gui.Sp8, []layout.Widget{media, func(gtx C) D {
				return layout.Inset{Top: 2, Left: 2, Right: 2}.Layout(gtx, info)
			}, errLine, rwNote}, actions)
		})
	})
}

// ---- frames -----------------------------------------------------------------------------

func (v *storyboard) framesHead(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState, n frameStats) D {
	th := c.Th
	p := &pv.P
	u := c.UI(pv.ID)
	if val, ok := st.view.Changed(gtx); ok {
		u.List = val == "list"
	}
	title, desc := "Кадри сцен", "Перший кадр кожної сцени — з нього модель зніме відео, тому саме тут варто довести картинку до ладу. Клацни кадр, щоб роздивитися."
	if p.Input.StoryboardMode == studio.StoryboardGrid {
		title = "Розкадровка сцен"
		desc = "Для кожної сцени — аркуш 3×3: дев'ять моментів дії, за якими Omni Flash зніме кліп. Панель 1 стає кадром сцени."
	}
	var right layout.Widget
	if gtx.Constraints.Max.X >= gtx.Dp(640) {
		right = func(gtx C) D {
			val := "grid"
			if u.List {
				val = "list"
			}
			return st.view.Layout(gtx, th, val, []gui.SegOption{
				{Value: "grid", Label: "Сітка", Icon: gui.IconLayoutGrid},
				{Value: "list", Label: "Список", Icon: gui.IconList},
			})
		}
	}
	return sectionHead(gtx, th, title, fmt.Sprint(n.scenes), desc, right)
}

// frameEvents handles a frame card's actions (before it is laid out).
func (v *storyboard) frameEvents(gtx C, c *core.Ctx, pv *core.ProjectView, fc *frameCard, s *studio.Scene) {
	id := s.ID
	fc.rw.update(gtx, c, pv.ID, studio.RegenFrame, id)
	if fc.open.Clicked(gtx) {
		v.openFrames(pv, id)
		c.UI(pv.ID).Scene = id
	}
	if fc.regen.Clicked(gtx) || fc.redraw.Clicked(gtx) {
		if c.Regen(pv.ID, studio.RegenFrame, id, "") {
			fc.editing = false
			c.UI(pv.ID).Scene = id
		}
	}
	if fc.prompt.Clicked(gtx) || fc.textClick.Clicked(gtx) {
		fc.editing = !fc.editing
		if fc.editing {
			fc.field.SetText(s.ImagePrompt)
			fc.focus = true
		}
	}
	if fc.collapse.Clicked(gtx) {
		fc.editing = false
	}
	if fc.upload.Clicked(gtx) {
		if s.Board.Path != "" {
			c.Confirm(core.Confirm{
				Title: "Замінити розкадровку своїм кадром?",
				Text:  "Свій кадр замінить аркуш 3×3 цієї сцени: кліп зніматиметься з твого кадру.",
				Icon:  gui.IconUpload, OK: "Вибрати файл",
				OnOK: func() { pickImage(c, pv.ID, studio.RegenFrame, id) },
			})
		} else {
			pickImage(c, pv.ID, studio.RegenFrame, id)
		}
	}
	if fc.reveal.Clicked(gtx) {
		path := s.Frame.Path
		if fc.showsBoard(&pv.P, s) {
			path = s.Board.Path
		}
		if path != "" {
			c.Reveal(pv.Abs(path))
		}
	}
	if val, ok := fc.field.Changed(gtx); ok {
		c.Edit(pv.ID, func(p *studio.Project) {
			for k := range p.Scenes {
				if p.Scenes[k].ID == id {
					p.Scenes[k].ImagePrompt = val
				}
			}
		})
	}
	if m, ok := fc.mode.Changed(gtx); ok {
		fc.board, fc.boardSet = m == "board", true
	}
}

func (v *storyboard) frameCard(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState, i int) D {
	th := c.Th
	p := &pv.P
	s := &p.Scenes[i]
	fc := st.frame(s.ID)
	v.frameEvents(gtx, c, pv, fc, s)
	return v.cardWrap(gtx, th, st, i, s.ID, func(gtx C) D {
		return gui.Card{Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
			wide := gtx.Constraints.Max.X >= gtx.Dp(250)
			top := []layout.Widget{
				func(gtx C) D { return v.frameMedia(gtx, c, pv, fc, s, i) },
				v.boardToggle(th, p, fc, s),
				func(gtx C) D {
					return layout.Inset{Left: 2, Right: 2}.Layout(gtx, func(gtx C) D { return v.frameTitle(gtx, th, pv, s, i) })
				},
				func(gtx C) D {
					return layout.Inset{Left: 2, Right: 2}.Layout(gtx, func(gtx C) D { return v.framePrompt(gtx, th, fc, s) })
				},
				func(gtx C) D { return frameNotice(gtx, th, s) },
				func(gtx C) D { return fc.rw.Layout(gtx, th, s.Frame.Rewrite) },
			}
			return pinned(gtx, gui.Sp12, top, func(gtx C) D { return v.frameActions(gtx, c, pv, fc, s, wide) })
		})
	})
}

// frameRow is the list-mode card: the picture on the left, the rest beside it.
func (v *storyboard) frameRow(gtx C, c *core.Ctx, pv *core.ProjectView, st *sbState, i int) D {
	th := c.Th
	p := &pv.P
	s := &p.Scenes[i]
	fc := st.frame(s.ID)
	v.frameEvents(gtx, c, pv, fc, s)
	return v.cardWrap(gtx, th, st, i, s.ID, func(gtx C) D {
		return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
			W := gtx.Constraints.Max.X
			mw := min(W*38/100, gtx.Dp(440))
			if p.Input.Aspect == "9:16" {
				mw = min(W*22/100, gtx.Dp(220))
			}
			gap := gtx.Dp(gui.Sp20)
			mg := gtx
			mg.Constraints = layout.Constraints{Max: pt(mw, gtx.Constraints.Max.Y)}
			md, mc := record(mg, func(gtx C) D {
				return vstack(gtx, gui.Sp12,
					func(gtx C) D { return v.frameMedia(gtx, c, pv, fc, s, i) },
					v.boardToggle(th, p, fc, s))
			})
			dg := gtx
			dg.Constraints = layout.Constraints{Min: pt(0, md.Size.Y), Max: pt(max(0, W-mw-gap), gtx.Constraints.Max.Y)}
			dd, dc := record(dg, func(gtx C) D {
				return pinned(gtx, gui.Sp12, []layout.Widget{
					func(gtx C) D { return v.frameTitle(gtx, th, pv, s, i) },
					func(gtx C) D { return v.framePrompt(gtx, th, fc, s) },
					func(gtx C) D { return frameNotice(gtx, th, s) },
					func(gtx C) D { return fc.rw.Layout(gtx, th, s.Frame.Rewrite) },
				}, func(gtx C) D {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(340))
					return v.frameActions(gtx, c, pv, fc, s, true)
				})
			})
			mc.Add(gtx.Ops)
			drawAt(gtx, mw+gap, 0, dc)
			return D{Size: pt(W, max(md.Size.Y, dd.Size.Y))}
		})
	})
}

// cardWrap adds the first-load entry motion and the jump highlight.
func (v *storyboard) cardWrap(gtx C, th *gui.Theme, st *sbState, i int, id string, w layout.Widget) D {
	d := gui.FadeUp(gtx, st.stagger.Progress(gtx, 2+i), 12, w)
	hlRing(gtx, th, d.Size, st.hl.Alpha(gtx, id))
	return d
}

func (v *storyboard) frameMedia(gtx C, c *core.Ctx, pv *core.ProjectView, fc *frameCard, s *studio.Scene, i int) D {
	th := c.Th
	p := &pv.P
	a := s.Frame
	pic := s.Frame
	board := fc.showsBoard(p, s)
	if board {
		pic = s.Board
	}
	empty := "Кадру ще немає"
	if p.Input.StoryboardMode == studio.StoryboardGrid {
		empty = "Розкадровки ще немає"
	}
	return fc.img.Layout(gtx, th, c.Img, pv.Abs(pic.Path), pic.UpdatedAt, gui.Thumb{Ratio: ratioOf(p.Input.Aspect), Placeholder: gui.IconImage, Overlay: func(gtx C) D {
		size := gtx.Constraints.Max
		fc.open.Layout(gtx, th, size, pic.Path != "" && !busy(a))
		m := gtx.Dp(10)
		fc.veil.Layout(gtx, th, size, busy(a), busyPct(a))
		if pic.Path == "" && !busy(a) {
			emptyCaption(gtx, th, size, empty)
		}
		corner(gtx, size, m, false, false, func(gtx C) D { return numBadge(gtx, th, i+1) })
		pill := assetPill(a)
		pill.Small = true
		corner(gtx, size, m, true, false, pill.Widget(th))
		return D{Size: size}
	}})
}

// boardToggle is "Кадр | Розкадровка 3×3" in storyboard mode (nil otherwise).
func (v *storyboard) boardToggle(th *gui.Theme, p *studio.Project, fc *frameCard, s *studio.Scene) layout.Widget {
	if p.Input.StoryboardMode != studio.StoryboardGrid {
		return nil
	}
	return func(gtx C) D {
		val := "frame"
		if fc.showsBoard(p, s) {
			val = "board"
		}
		boardLbl := "Розкадровка 3×3"
		if gtx.Constraints.Max.X < gtx.Dp(300) {
			boardLbl = "3×3"
		}
		fc.mode.Full = true
		return fc.mode.Layout(gtx, th, val, []gui.SegOption{
			{Value: "frame", Label: "Кадр", Icon: gui.IconImage},
			{Value: "board", Label: boardLbl, Icon: gui.IconGrid3x3, Disabled: s.Board.Path == ""},
		})
	}
}

func (v *storyboard) frameTitle(gtx C, th *gui.Theme, pv *core.ProjectView, s *studio.Scene, i int) D {
	cast := castLine(pv, s)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(label(th, th.BodyStrong.Lined(1.35), th.Pal.Text, 1, sceneTitle(s, i+1))),
		layout.Rigid(func(gtx C) D {
			if cast == "" {
				return D{}
			}
			return label(th, th.Caption, th.Pal.Text3, 1, cast)(gtx)
		}),
	)
}

// framePrompt is the collapsible prompt: a few lines (click to edit) or the
// editor that commits every change through c.Edit (the frame turns
// "Змінено" at once) with "redraw with this prompt".
func (v *storyboard) framePrompt(gtx C, th *gui.Theme, fc *frameCard, s *studio.Scene) D {
	return fc.exp.Layout(gtx, func(gtx C) D {
		if !fc.editing {
			txt := strings.TrimSpace(s.ImagePrompt)
			col := th.Pal.Text2
			if txt == "" {
				txt, col = "Промт порожній — кадр малюється з назви сцени. Клацни, щоб написати.", th.Pal.Text3
			}
			d, call := record(gtx, label(th, th.Small, col, 3, txt))
			fc.textClick.Layout(gtx, func(gtx C) D {
				return D{Size: d.Size}
			})
			call.Add(gtx.Ops)
			return d
		}
		fc.field.Sync(gtx, s.ImagePrompt)
		if fc.focus {
			fc.focus = false
			fc.field.Focus(gtx)
		}
		return vstack(gtx, gui.Sp8,
			func(gtx C) D {
				return fc.field.Layout(gtx, th, gui.FieldStyle{Label: "Промт кадру", MinLines: 4, MaxLines: 10, MaxLen: 4000,
					Style:       th.Small.Sized(14).Lined(1.5),
					Placeholder: "Англійською: хто, де, що відбувається, світло",
					Hint:        "@тег — герой чи місце з референсів"})
			},
			func(gtx C) D {
				return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
					func(gtx C) D {
						return gui.Button{Kind: gui.Primary, Size: gui.Sm, Text: "Перемалювати з цим промтом", Icon: gui.IconRefreshCw,
							Busy: busy(s.Frame)}.Layout(gtx, th, &fc.redraw)
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Згорнути"}.Layout(gtx, th, &fc.collapse)
					},
				)
			},
		)
	})
}

// frameNotice explains a failed frame (the retry is the card's button), or
// a storyboard sheet the service refused while the single frame was drawn
// instead (studio.genBoard's fallback).
func frameNotice(gtx C, th *gui.Theme, s *studio.Scene) D {
	switch {
	case s.Frame.Status == studio.StatusError:
		return notice{Tone: gui.ToneError, Icon: gui.IconAlertTriangle, Title: "Кадр не вдався",
			Text: assetErr(s.Frame), Lines: errLines, Small: true}.Layout(gtx, th)
	case s.Board.Status == studio.StatusError && s.Board.Path == "" && s.Frame.Status == studio.StatusDone:
		return notice{Tone: gui.ToneWarning, Icon: gui.IconAlertTriangle, Title: "Без розкадровки 3×3",
			Text: assetErr(s.Board), Lines: errLines, Small: true}.Layout(gtx, th)
	}
	return D{}
}

func (v *storyboard) frameActions(gtx C, c *core.Ctx, pv *core.ProjectView, fc *frameCard, s *studio.Scene, wide bool) D {
	th := c.Th
	a := s.Frame
	running := pv.Running()
	b := gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Перемалювати", Busy: busy(a), Disabled: running, Full: true}
	switch {
	case a.Status == studio.StatusError:
		b.Text, b.Icon = "Повторити", gui.IconRotateCcw
	case a.Path == "" && !busy(a):
		b.Text, b.Icon = "Намалювати", gui.IconSparkles
	case frameStale(s):
		b.Kind = gui.Primary
	}
	if busy(a) {
		b.Text = "Малюється…"
	}
	if !wide {
		b.Tip, b.Text, b.Full = b.Text, "", false
	}
	promptC := color.NRGBA{}
	if fc.editing {
		promptC = th.Pal.ActionText
	}
	path := a.Path
	if fc.showsBoard(&pv.P, s) {
		path = s.Board.Path
	}
	icons := []layout.FlexChild{
		layout.Rigid(func(gtx C) D {
			tip := "Змінити промт кадру"
			if fc.editing {
				tip = "Сховати промт"
			}
			return gui.IconButton{Icon: gui.IconPenLine, Size: 36, Color: promptC, Tip: tip}.Layout(gtx, th, &fc.prompt)
		}),
		layout.Rigid(func(gtx C) D {
			return gui.IconButton{Icon: gui.IconUpload, Size: 36, Tip: "Завантажити свій кадр", Disabled: busy(a)}.Layout(gtx, th, &fc.upload)
		}),
		layout.Rigid(func(gtx C) D {
			return gui.IconButton{Icon: gui.IconFolderOpen, Size: 36, Tip: "Показати у папці", Disabled: path == "" || c.Offline}.Layout(gtx, th, &fc.reveal)
		}),
	}
	ch := make([]layout.FlexChild, 0, 6)
	switch {
	case fc.editing:
		// The editor has its own "redraw with this prompt".
		ch = append(ch, layout.Flexed(1, func(gtx C) D { return D{Size: pt(gtx.Constraints.Min.X, 0)} }))
	case wide:
		ch = append(ch, layout.Flexed(1, func(gtx C) D { return b.Layout(gtx, th, &fc.regen) }), layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout))
	default:
		ch = append(ch, layout.Rigid(func(gtx C) D { return b.Layout(gtx, th, &fc.regen) }), layout.Flexed(1, func(gtx C) D { return D{Size: pt(gtx.Constraints.Min.X, 0)} }))
	}
	ch = append(ch, icons...)
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

// ---- lightbox --------------------------------------------------------------------------

func (v *storyboard) frameItems(pv *core.ProjectView, st *sbState) []LightboxItem {
	p := &pv.P
	items := make([]LightboxItem, 0, len(p.Scenes))
	for i := range p.Scenes {
		s := &p.Scenes[i]
		pic, grid := s.Frame, 0
		caption := castLine(pv, s)
		if fc := st.frames[s.ID]; (fc != nil && fc.showsBoard(p, s)) || (fc == nil && p.Input.StoryboardMode == studio.StoryboardGrid && s.Board.Path != "") {
			pic, grid = s.Board, 3
			caption = "Розкадровка 3×3 · клацни панель, щоб наблизити"
		}
		if pic.Path == "" {
			continue
		}
		items = append(items, LightboxItem{Key: s.ID, Path: pv.Abs(pic.Path), Ver: pic.UpdatedAt,
			Title: fmt.Sprintf("Сцена %d · %s", i+1, sceneTitle(s, i+1)), Caption: caption, Grid: grid})
	}
	return items
}

func (v *storyboard) refItems(pv *core.ProjectView) []LightboxItem {
	p := &pv.P
	var items []LightboxItem
	for _, ch := range p.Characters {
		if ch.Image.Path != "" {
			items = append(items, LightboxItem{Key: ch.ID, Path: pv.Abs(ch.Image.Path), Ver: ch.Image.UpdatedAt,
				Title: firstText(ch.Name, "Персонаж"), Caption: "Персонаж · @" + ch.Tag})
		}
	}
	for _, l := range p.Locations {
		if l.Image.Path != "" {
			items = append(items, LightboxItem{Key: l.ID, Path: pv.Abs(l.Image.Path), Ver: l.Image.UpdatedAt,
				Title: firstText(l.Name, "Локація"), Caption: "Локація · @" + l.Tag})
		}
	}
	return items
}

func (v *storyboard) openFrames(pv *core.ProjectView, id string) {
	st := v.state(pv.ID)
	items := v.frameItems(pv, st)
	for i, it := range items {
		if it.Key == id {
			v.lbRefs, v.lbProj = false, pv.ID
			v.lb.Open(items, i)
			return
		}
	}
}

func (v *storyboard) openRefs(pv *core.ProjectView, id string) {
	items := v.refItems(pv)
	for i, it := range items {
		if it.Key == id {
			v.lbRefs, v.lbProj = true, pv.ID
			v.lb.Open(items, i)
			return
		}
	}
}

func (v *storyboard) layoutLightbox(gtx C, c *core.Ctx, pv *core.ProjectView) {
	if v.lb.IsOpen() && v.lbProj == pv.ID {
		if v.lbRefs {
			v.lb.Update(v.refItems(pv))
		} else {
			v.lb.Update(v.frameItems(pv, v.state(pv.ID)))
		}
	}
	v.lb.Layout(gtx, c.Th, c.Img)
}

// ---- side column -------------------------------------------------------------------------

func (v *storyboard) Side(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	if pv == nil {
		return D{}
	}
	th := c.Th
	st := v.state(pv.ID)
	p := &pv.P
	if p.Input.CodeAnim() && len(p.Scenes) > 0 {
		return v.code.Side(gtx, c, pv)
	}
	if len(p.Scenes) == 0 {
		return gui.TipCard{Icon: gui.IconLightbulb, Title: "Що буде на цьому кроці",
			Text: "Спершу студія намалює героїв і місця, а тоді — перший кадр кожної сцени з цими референсами."}.Layout(gtx, th, nil)
	}
	n := countFrames(p)
	grid := p.Input.StoryboardMode == studio.StoryboardGrid
	u := c.UI(pv.ID)
	first := func(pred func(s *studio.Scene) bool) string {
		for i := range p.Scenes {
			if pred(&p.Scenes[i]) {
				return p.Scenes[i].ID
			}
		}
		return ""
	}
	rows := []sideRow{
		{Tone: gui.ToneSuccess, Label: "Готово", Count: n.done - n.stale, Always: true},
		{Tone: gui.ToneRunning, Label: "Малюються", Count: n.busy, Scene: first(func(s *studio.Scene) bool { return busy(s.Frame) })},
		{Tone: gui.ToneWarning, Label: "Змінено — варто перемалювати", Count: n.stale, Scene: first(frameStale)},
		{Tone: gui.ToneError, Label: "Помилки", Count: n.errs, Scene: first(func(s *studio.Scene) bool { return s.Frame.Status == studio.StatusError })},
		{Tone: gui.ToneNeutral, Label: "Ще не намальовано", Count: n.empty, Scene: first(func(s *studio.Scene) bool {
			return !busy(s.Frame) && s.Frame.Status != studio.StatusDone && s.Frame.Status != studio.StatusError
		})},
	}
	if id := st.side.clicked(gtx, rows); id != "" {
		u.ScrollTo = id
	}
	if st.side.tip.Clicked(gtx) {
		st.toRefs = true
	}
	ws := []layout.Widget{
		func(gtx C) D {
			return st.side.card(gtx, th, "Стан кадрів", fmt.Sprintf("%d з %d", n.done, n.scenes), rows)
		},
	}
	if n.refs > 0 {
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLightbulb, Title: "Чому спершу референси",
				Text: "Кожен кадр малюється з референсами героїв і місць, тож обличчя, одяг і місця однакові в усіх сценах. " +
					"Не подобається герой — перемалюй його або завантаж своє зображення до того, як малювати кадри.",
				Action: "До референсів"}.Layout(gtx, th, &st.side.tip)
		})
	}
	if grid {
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconGrid3x3, Title: "Розкадровка 3×3",
				Text: "Для кожної сцени модель малює дев'ять моментів дії. Omni Flash знімає кліп за цим планом, а панель 1 стає кадром сцени."}.Layout(gtx, th, nil)
		})
	}
	return sideStack(gtx, ws...)
}

// ---- next step ---------------------------------------------------------------------------

func (v *storyboard) Next(c *core.Ctx, pv *core.ProjectView) core.Next {
	if pv == nil {
		return core.Next{}
	}
	p := &pv.P
	id := pv.ID
	if p.Input.CodeAnim() && len(p.Scenes) > 0 {
		return v.code.Next(c, pv)
	}
	if len(p.Scenes) == 0 {
		if k := pv.RunningKind(); k == studio.JobPlan || k == studio.JobAutopilot {
			return core.Next{Icon: gui.IconListChecks, Title: "План пишеться…", Text: firstText(p.Job.Message, "Щойно будуть сцени, можна малювати кадри."), Busy: true}
		}
		return core.Next{Icon: gui.IconListChecks, Title: "Спершу створи план",
			Text: "Кадри малюються за планом: герої, місця й сцени.", Do: func() { goStep(c, id, 2) }}
	}
	if pv.Running() {
		k := pv.RunningKind()
		return core.Next{Icon: gui.IconSparkles, Title: jobTitle(k, p.Input.StoryboardMode == studio.StoryboardGrid) + "…",
			Text: firstText(p.Job.Message, "Студія працює — можна дивитися, як з'являються кадри."), Busy: true}
	}
	n := countFrames(p)
	st := v.state(id)
	switch {
	case st.batch.active():
		done, total := st.batch.progress()
		return core.Next{Icon: gui.IconRefreshCw, Title: st.batch.label + "…", Text: fmt.Sprintf("Готово %d з %d.", done, total), Busy: true}
	case n.left() > 0:
		text := fmt.Sprintf("%s ще немає — студія намалює їх з референсами героїв і місць.", core.Count(n.left(), "кадру", "кадрів", "кадрів"))
		if n.left() == n.scenes {
			text = "Студія намалює перший кадр кожної сцени з референсами героїв і місць."
		}
		return core.Next{Icon: gui.IconImages, Title: "Згенерувати кадри", Text: text, Do: func() { c.Run(id, studio.StageFrames, false, "") }}
	case n.stale > 0:
		return core.Next{Icon: gui.IconRefreshCw, Title: fmt.Sprintf("Перемалювати змінені · %d", n.stale),
			Text: "Промти цих кадрів змінились після малювання.", Do: func() { v.redrawStale(c, pv, st) }}
	}
	return core.Next{Icon: gui.IconClapperboard, Title: "Далі: відео й озвучка",
		Text: fmt.Sprintf("Усі %s готові — час їх оживити.", core.Count(n.scenes, "кадр", "кадри", "кадрів")), Do: func() { goStep(c, id, 4) }}
}
