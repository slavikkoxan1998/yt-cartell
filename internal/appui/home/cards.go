package home

import (
	"image"
	"strings"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// cardState is the widget state of one project card.
type cardState struct {
	open gui.Clickable
	more gui.Clickable
	img  gui.ImageView
	ring gui.Anim
	hov  gui.Anim
	seen int64
}

func (p *Page) card(id string, frame int64) *cardState {
	st := p.cards[id]
	if st == nil {
		st = &cardState{}
		p.cards[id] = st
	}
	st.seen = frame
	return st
}

// status is where a project stands, for the pill, the ring and the filters.
type status uint8

const (
	stDraft   status = iota // step 1, nothing planned yet
	stWork                  // in progress, idle
	stRunning               // a job runs
	stDone                  // the final video exists
	stError                 // the last job failed
)

// cardInfo is everything a project card shows.
type cardInfo struct {
	title   string
	meta    string
	caption string
	status  status
	pill    gui.StatusPill
	ring    float32
	tone    gui.Tone
	step    int
}

// statusOf is where a project stands (cheap: no strings; the filters call
// it for every project).
func statusOf(c *core.Ctx, s studio.Summary) status {
	st := stWork
	if max(1, s.Step) <= 1 && s.Scenes == 0 {
		st = stDraft
	}
	if pv := c.Project(s.ID); pv != nil {
		if o := pv.P.Montage.Output; o.Status == studio.StatusDone && o.Path != "" {
			st = stDone
		}
		if j := pv.P.Job; j != nil && j.Status == studio.JobError {
			st = stError
		}
	}
	if s.Running != "" {
		st = stRunning
	}
	return st
}

// info reads a project for its card (the snapshot is cached by core until
// the engine reports a change).
func info(c *core.Ctx, s studio.Summary) cardInfo {
	in := cardInfo{title: strings.TrimSpace(s.Title), step: max(1, min(5, s.Step))}
	if in.title == "" {
		in.title = "Без назви"
	}
	var p *studio.Project
	if pv := c.Project(s.ID); pv != nil {
		p = &pv.P
	}
	// meta: scenes · length · format
	var parts []string
	if s.Scenes > 0 {
		parts = append(parts, core.Count(s.Scenes, "сцена", "сцени", "сцен"))
	} else {
		parts = append(parts, "без сцен")
	}
	if p != nil {
		if o := p.Montage.Output; o.Status == studio.StatusDone && o.Duration > 0 {
			parts = append(parts, core.Clock(o.Duration))
		} else if p.Input.TargetSeconds > 0 {
			parts = append(parts, "≈ "+core.Duration(p.Input.TargetSeconds))
		}
	}
	if s.Aspect != "" {
		parts = append(parts, s.Aspect)
	}
	in.meta = strings.Join(parts, " · ")

	in.status = statusOf(c, s)
	var job *studio.Job
	if p != nil {
		job = p.Job
	}
	stepText := "Крок " + itoa(in.step) + " з 5"
	updated := "Оновлено " + core.Updated(s.UpdatedAt)
	in.caption = updated
	in.ring, in.tone = (float32(in.step)-0.5)/5, gui.ToneNeutral
	switch in.status {
	case stRunning:
		pct := 0
		if job != nil {
			pct = max(0, min(100, job.Progress))
		}
		shown := pct
		if shown <= 0 {
			shown = -1 // nothing measured yet: "Генерується", not a stuck 0 %
		}
		in.pill = gui.StatusPill{State: gui.StateRunning, Percent: shown, Small: true}
		in.ring, in.tone = float32(pct)/100, gui.ToneRunning
		in.caption = "Зараз: " + core.JobLabel(s.Running)
	case stDone:
		in.pill = gui.StatusPill{State: gui.StateDone, Small: true}
		if p != nil && p.Montage.Output.Stale {
			in.pill = gui.StatusPill{State: gui.StateStale, Text: "Змінено", Small: true}
		}
		in.ring, in.tone = 1, gui.ToneSuccess
	case stError:
		in.pill = gui.StatusPill{State: gui.StateError, Small: true}
		if job != nil {
			in.caption = "Не вдалося: " + core.JobLabel(job.Kind)
		}
	case stDraft:
		in.pill = gui.StatusPill{State: gui.StateEmpty, Text: "Чернетка", Small: true}
	default:
		in.pill = gui.StatusPill{State: gui.StateEmpty, Text: stepText, Small: true}
	}
	return in
}

func moreAnchor(id string) string { return "home.more." + id }

// cardMenu is the ⋯ menu of a project card.
func cardMenu() []popItem {
	return []popItem{
		{Value: "open", Label: "Відкрити", Icon: gui.IconArrowUpRight},
		{Value: "translate", Label: "Перекласти", Icon: gui.IconLanguages},
		{Value: "duplicate", Label: "Дублювати", Icon: gui.IconCopy},
		{Value: "reveal", Label: "Показати в папці", Icon: gui.IconFolderOpen},
		{Value: "delete", Label: "Видалити", Icon: gui.IconTrash2, Danger: true, Rule: true},
	}
}

// cardAction runs a ⋯ menu action.
func (p *Page) cardAction(c *core.Ctx, id, action string) {
	title := "Без назви"
	if pv := c.Project(id); pv != nil && strings.TrimSpace(pv.P.Title) != "" {
		title = strings.TrimSpace(pv.P.Title)
	}
	switch action {
	case "open":
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: id})
	case "translate":
		c.OpenSheet(core.Sheet{Kind: core.SheetTranslate, ProjectID: id})
	case "duplicate":
		var copyTitle string
		c.Do(func() error {
			cp, err := c.E.Duplicate(id)
			copyTitle = cp.Title
			return err
		}, func() { c.Toast(gui.ToneSuccess, "Створено копію «"+copyTitle+"»") })
	case "reveal":
		c.Reveal(c.E.Dir(id))
	case "delete":
		c.Confirm(core.Confirm{
			Title:  "Видалити проєкт?",
			Text:   "«" + title + "» і всі його файли — кадри, відео, озвучку й монтаж — буде видалено з папки даних. Скасувати це не можна.",
			Icon:   gui.IconTrash2,
			OK:     "Видалити",
			Danger: true,
			OnOK: func() {
				c.Do(func() error { return c.E.Delete(id) }, func() { c.Toast(gui.ToneSuccess, "Проєкт «"+title+"» видалено") })
			},
		})
	}
}

// projectCard draws one card of «Мої проєкти»: thumbnail with the status
// pill and the ⋯ button, title, meta and the progress ring. The whole card
// opens the project.
func (p *Page) projectCard(gtx C, c *core.Ctx, s studio.Summary) D {
	th := c.Th
	st := p.card(s.ID, c.Frame())
	if st.open.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: s.ID})
	}
	if st.more.Clicked(gtx) {
		p.OpenMenu(s.ID)
	}
	in := info(c, s)
	return gui.Card{Click: &st.open, Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				return st.img.Layout(gtx, th, c.Img, s.ThumbPath, 0, gui.Thumb{
					Ratio: 16.0 / 10, Placeholder: gui.IconClapperboard,
					Overlay: func(gtx C) D { return p.cardOverlay(gtx, th, s, st, in) },
				})
			},
			func(gtx C) D {
				return layout.Inset{Left: gui.Sp4, Right: gui.Sp4, Bottom: gui.Sp4}.Layout(gtx, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D {
							return gui.VStack(gtx, 2,
								gui.Text{Style: th.BodyStrong.Sized(16), MaxLines: 1}.Widget(th, in.title),
								gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, in.meta),
								gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, in.caption),
							)
						}),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Rigid(func(gtx C) D {
							return gui.ProgressRing{Value: in.ring, Size: 48, Stroke: 5, Tone: in.tone, Anim: &st.ring}.Layout(gtx, th)
						}),
					)
				})
			},
		)
	})
}

// cardOverlay puts the status pill (top left) and the ⋯ button (top right)
// on the thumbnail.
func (p *Page) cardOverlay(gtx C, th *gui.Theme, s studio.Summary, st *cardState, in cardInfo) D {
	size := gtx.Constraints.Max
	m := gtx.Dp(gui.Sp8)
	func() {
		defer op.Offset(image.Pt(m, m)).Push(gtx.Ops).Pop()
		pg := gtx
		pg.Constraints = layout.Constraints{Max: image.Pt(max(0, size.X-2*m-gtx.Dp(40)), size.Y)}
		in.pill.Layout(pg, th)
	}()
	b := gtx.Dp(32)
	func() {
		defer op.Offset(image.Pt(size.X-m-b, m)).Push(gtx.Ops).Pop()
		gui.Anchor(gtx, th, moreAnchor(s.ID), func(gtx C) D {
			return overlayButton(gtx, th, &st.more, &st.hov, gui.IconMoreHorizontal, "Дії з проєктом")
		})
	}()
	return D{Size: size}
}

// overlayButton is a round button on a picture: a translucent disc with an
// icon (the kit's IconButton is transparent until hovered).
func overlayButton(gtx C, th *gui.Theme, c *gui.Clickable, hov *gui.Anim, ic *gui.Icon, tip string) D {
	px := gtx.Dp(32)
	hov.Duration = gui.DurHover
	h := hov.Animate(gtx, b2f(c.Hovered()))
	if c.Hovered() {
		th.Tooltip(gtx, c, c.Pressed(), tip)
	}
	return c.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		s := float32(1)
		if c.Pressed() {
			s = 0.94
		}
		cx := float32(px) / 2
		defer op.Affine(f32.AffineId().Scale(f32.Pt(cx, cx), f32.Pt(s, s))).Push(gtx.Ops).Pop()
		bg := gui.LerpColor(th.Pal.Overlay, th.Pal.Card, h)
		gui.Shadow(gtx, th, image.Rect(0, 0, px, px), unit.Dp(16), gui.ElevControl)
		gui.FillCircle(gtx, 32, bg)
		if gtx.Focused(&c.Clickable) {
			gui.FocusRing(gtx, th, image.Rect(0, 0, px, px), 16, 1)
		}
		is := gtx.Dp(18)
		o := op.Offset(image.Pt((px-is)/2, (px-is)/2)).Push(gtx.Ops)
		ic.Layout(gtx, 18, th.Pal.OverlayInk)
		o.Pop()
		return D{Size: image.Pt(px, px)}
	})
}

// gridRows lays n cells out as virtualisable rows of cols cells each (one
// widget per row, cells stretched to the row's height); stagger (optional)
// fades them in on first load.
func gridRows(n, cols int, stagger *gui.Stagger, cell func(gtx C, i int) D) []layout.Widget {
	var rows []layout.Widget
	for r := 0; r*cols < n; r++ {
		start, end := r*cols, min(n, (r+1)*cols)
		rows = append(rows, func(gtx C) D {
			// MinWidth 1 dp: the column count is cols, decided by fluidCols.
			return gui.Grid{MinWidth: 1, Gap: gui.Sp24, MaxCols: cols, Stretch: true}.Layout(gtx, end-start, func(gtx C, i int) D {
				k := start + i
				pr := float32(1)
				if stagger != nil {
					pr = stagger.Progress(gtx, k)
				}
				return gui.FadeUp(gtx, pr, 12, func(gtx C) D { return cell(gtx, k) })
			})
		})
	}
	return rows
}

// Card widths (GUI.md: cards ≈ 300 dp; fluid, never a dead half).
const (
	cardMinWidth unit.Dp = 280
	cardMaxWidth unit.Dp = 600
)

// fluidCols is the column count for n cards in a row w px wide: every card
// between minW and maxW wide, and as few empty slots in the last row as
// those widths allow — few projects get wide cards that fill the row
// instead of a narrow card and a dead half. Ties go to more columns.
func fluidCols(gtx C, w, n int, minW, maxW unit.Dp) int {
	gap := gtx.Dp(gui.Sp24)
	hi := max(1, (w+gap)/(gtx.Dp(minW)+gap))
	step := gtx.Dp(maxW) + gap
	lo := min(hi, max(1, (w+gap+step-1)/step))
	switch {
	case n <= 0:
		return hi
	case n <= hi:
		return max(n, lo)
	}
	best, bestEmpty := hi, 2.0
	for cols := hi; cols >= lo; cols-- {
		if empty := float64((cols-n%cols)%cols) / float64(cols); empty < bestEmpty-1e-9 {
			best, bestEmpty = cols, empty
		}
	}
	return best
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
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
