// Package project is the project workspace: header card, step tabs, the
// right context column, and steps 1 Сценарій + 2 План (GUI.md → Screens → 2).
//
// Layout of the workspace (full window width, no dead half):
//
//	header card   progress ring "Крок x/5", inline title, logline, fact
//	              pills, profile chip (menu), Автопілот / Зупинити, ⋯ menu
//	job banner    while a job runs (label, message, animated bar, Зупинити);
//	              a failed job (Повторити) and a finished one (fades out)
//	step tabs     gui.PillTabs with numbers and checks (+ "Огляд" under
//	              1340 dp of content, which opens the context drawer)
//	body          the step's main column (StepView.Layout scrolls itself)
//	              + the context column (360 dp): next-step card, this
//	              project's job, the step's Side cards, scene navigator, tip
//
// Steps 3–5 come from packages produce and montage. Every step change
// fades/rises the main column (gui.Route).
package project

import (
	"fmt"
	"image"
	"strconv"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/montage"
	"ytcartell/internal/appui/produce"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Layout constants (dp).
const (
	sideW      unit.Dp = 360  // context column
	sideGap    unit.Dp = 24   // between the main and the context column
	wideMin    unit.Dp = 1340 // content width from which the context column shows
	bleed      unit.Dp = 12   // scroll lists reach this far past their cards (shadows, scrollbar)
	headerGap  unit.Dp = 16
	compactMax unit.Dp = 760 // narrower content: compact header
)

// Workspace is the state of the project screen (one per window; it switches
// projects by id).
type Workspace struct {
	cur        string
	projs      map[string]*projState
	routeScene string // Route.Scene already handled

	drawer sideSheet
	side   gui.Scroll

	drawerBtn gui.Clickable
	nextBtn   gui.Clickable
	homeBtn   gui.Clickable
	tipBtn    gui.Clickable
	dnext     gui.Clickable // the next-step card inside the drawer

	h header
}

// projState is what the workspace keeps per project.
type projState struct {
	id    string
	steps [5]core.StepView
	s1    *step1
	s2    *step2
	bind  binder
	f     facts
	nav   map[string]*navItem

	title inline
	ring  gui.Anim
	// route animates step changes; tabs are the step tabs (per project, so
	// opening another project never replays this one's step view).
	route gui.Route
	tabs  gui.PillTabs

	banner bannerState
}

// NewWorkspace creates the workspace.
func NewWorkspace() *Workspace { return &Workspace{projs: map[string]*projState{}} }

func (w *Workspace) state(id string) *projState {
	ps := w.projs[id]
	if ps == nil {
		ps = &projState{id: id, nav: map[string]*navItem{}}
		ps.s1 = newStep1(ps)
		ps.s2 = newStep2(ps)
		ps.steps = [5]core.StepView{ps.s1, ps.s2, produce.NewStoryboard(), produce.NewRender(), montage.New()}
		w.projs[id] = ps
	}
	return ps
}

// pagePad is the page padding for a content width (GUI.md: 32, 24 under
// 1280, 16 under 900).
func pagePad(gtx C, width int) int {
	switch {
	case width >= gtx.Dp(1280-92):
		return gtx.Dp(32)
	case width >= gtx.Dp(900-92):
		return gtx.Dp(24)
	}
	return gtx.Dp(16)
}

// Layout draws project id at step (1–5; 0 = the project's current step).
func (w *Workspace) Layout(gtx core.C, c *core.Ctx, id string, step int) core.D {
	if w.projs == nil {
		w.projs = map[string]*projState{}
	}
	if w.cur != id {
		if old := w.projs[w.cur]; old != nil {
			old.bind.flushAll()
		}
		w.cur = id
		w.side.List.Position = layout.Position{}
		w.h.more.Close()
		w.h.prof.Close()
		w.drawer.Close()
	}
	pv := c.Project(id)
	if pv == nil {
		return w.missing(gtx, c)
	}
	ps := w.state(id)
	ps.bind.c = c
	ps.f = factsOf(c, pv)
	// A route to a scene (search, queue) scrolls the step to it once.
	if r := c.Current(); r.Page == core.PageProject && r.ProjectID == id && r.Scene != w.routeScene {
		w.routeScene = r.Scene
		if r.Scene != "" {
			ui := c.UI(id)
			ui.ScrollTo, ui.Scene = r.Scene, r.Scene
		}
	}
	if step < 1 || step > 5 {
		step = max(1, min(5, pv.P.Step))
	}
	d := w.layout(gtx, c, pv, ps, step)
	ps.bind.flushStale(c.Frame())
	return d
}

func (w *Workspace) layout(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, step int) D {
	th := c.Th
	W, H := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	pad := pagePad(gtx, W)
	inner := max(0, W-2*pad)
	wide := W >= gtx.Dp(wideMin)
	compact := inner < gtx.Dp(compactMax)
	sv := ps.steps[step-1]
	next := sv.Next(c, pv)

	free := func(width int) C {
		g := gtx
		g.Constraints = layout.Constraints{Min: image.Pt(width, 0), Max: image.Pt(width, H)}
		return g
	}
	y := pad
	if compact {
		y = gtx.Dp(16)
	}
	// Header card.
	hm := op.Record(gtx.Ops)
	hd := w.h.layout(free(inner), c, pv, ps, compact, &w.drawerBtn)
	hc := hm.Stop()
	o := op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
	hc.Add(gtx.Ops)
	o.Pop()
	y += hd.Size.Y + gtx.Dp(headerGap)
	// Job banner (grows in and out).
	bm := op.Record(gtx.Ops)
	bd := ps.banner.layout(free(inner), c, pv)
	bc := bm.Stop()
	o = op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
	bc.Add(gtx.Ops)
	o.Pop()
	// The gap under the banner grows and shrinks with it (no jump).
	y += bd.Size.Y + min(gtx.Dp(12), bd.Size.Y)
	// Step tabs (+ the context drawer toggle when there is no column).
	tm := op.Record(gtx.Ops)
	td := w.tabsRow(free(inner), c, pv, ps, step, wide || compact)
	tc := tm.Stop()
	o = op.Offset(image.Pt(pad, y)).Push(gtx.Ops)
	tc.Add(gtx.Ops)
	o.Pop()
	y += td.Size.Y + gtx.Dp(headerGap)

	bodyH := max(0, H-y)
	mainW := inner
	if wide {
		mainW = inner - gtx.Dp(sideW) - gtx.Dp(sideGap)
	}
	// Main column: my steps get a bleed (shadows, scrollbar), others the exact rect.
	b := 0
	if bl, ok := sv.(core.Bleeder); ok {
		b = gtx.Dp(bl.Bleed())
	}
	mg := gtx
	mg.Constraints = layout.Exact(image.Pt(mainW+2*b, bodyH))
	o = op.Offset(image.Pt(pad-b, y)).Push(gtx.Ops)
	cl := clip.Rect(image.Rect(0, 0, mainW+2*b, bodyH)).Push(gtx.Ops)
	key := pv.ID + "#" + strconv.Itoa(step)
	ps.route.Layout(mg, key, func(gtx C) D { return sv.Layout(gtx, c, pv) })
	cl.Pop()
	o.Pop()

	if w.nextBtn.Clicked(gtx) && next.Do != nil && !next.Busy {
		next.Do()
	}
	if w.dnext.Clicked(gtx) && next.Do != nil && !next.Busy {
		w.drawer.Close()
		next.Do()
	}
	if wide {
		sb := gtx.Dp(bleed)
		sg := gtx
		sg.Constraints = layout.Exact(image.Pt(gtx.Dp(sideW)+2*sb, bodyH))
		o = op.Offset(image.Pt(pad+mainW+gtx.Dp(sideGap)-sb, y)).Push(gtx.Ops)
		w.sideColumn(sg, c, pv, ps, step, sv, next)
		o.Pop()
		if w.drawer.IsOpen() {
			w.drawer.Close()
		}
	} else {
		w.drawer.Layout(gtx, th, "Огляд проєкту", 420, func(gtx C) D {
			return w.sideStack(gtx, c, pv, ps, step, sv, next)
		})
	}
	return D{Size: image.Pt(W, H)}
}

// tabsRow draws the step tabs and, without a context column, the "Огляд"
// button that opens the drawer.
func (w *Workspace) tabsRow(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, step int, plain bool) D {
	th := c.Th
	if i, ok := ps.tabs.Changed(gtx); ok && i+1 != step {
		ps.bind.flushAll()
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: pv.ID, Step: i + 1})
	}
	if w.drawerBtn.Clicked(gtx) {
		w.drawer.Toggle()
	}
	tabs := make([]gui.Tab, 5)
	for n := 1; n <= 5; n++ {
		tabs[n-1] = gui.Tab{Label: core.StepNames[n-1], Number: n, Done: ps.f.done[n] && n != step, Disabled: !ps.f.enabled[n] && n != step}
		tabs[n-1].Label, tabs[n-1].Badge = core.StepLabel(pv.P.Input, n)
	}
	tabsW := func(gtx C) D { return ps.tabs.Layout(gtx, th, step-1, tabs) }
	if plain {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(tabsW))
	}
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.W.Layout(gtx, tabsW)
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Rigid(func(gtx C) D {
			txt := "Огляд"
			if gtx.Constraints.Max.X < gtx.Dp(200) {
				txt = ""
			}
			return gui.Button{Kind: gui.Tinted, Icon: gui.IconPanelRight, Text: txt, Tip: "Наступний крок, сцени й підказки"}.Layout(gtx, th, &w.drawerBtn)
		}),
	)
}

// missing is the friendly screen of a deleted or unknown project.
func (w *Workspace) missing(gtx C, c *core.Ctx) D {
	if w.homeBtn.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageHome})
	}
	return layout.Center.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
		return gui.EmptyState{Icon: gui.IconFolderOpen, Title: "Проєкт не знайдено",
			Text:   "Можливо, його видалили або перенесли в іншу папку даних. Усі інші проєкти — на головній.",
			Action: "На головну", ActionIcon: gui.IconHome}.Layout(gtx, c.Th, &w.homeBtn)
	})
}

// ---- job banner ------------------------------------------------------------------------------

// bannerState animates the job banner under the header: a running job, a
// failed one (until dismissed or an hour passed) and a finished one (a few
// seconds).
type bannerState struct {
	open    gui.Anim
	bar     gui.Anim
	stop    gui.Clickable
	retry   gui.Clickable
	choose  gui.Clickable // a job blocked by scenes that are not ready: the montage choice
	dismiss gui.Clickable
	hidden  int64 // FinishedAt of the job the user dismissed
	last    layout.Widget
}

// doneFor is how long a finished job's banner stays.
const doneFor = 7 * time.Second

func (b *bannerState) layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	j := pv.P.Job
	if b.stop.Clicked(gtx) {
		c.E.Cancel(pv.ID)
	}
	if b.retry.Clicked(gtx) {
		switch {
		case c.Offline:
			c.Toast(gui.ToneError, offlineText)
		default:
			c.Error(c.E.Retry(pv.ID))
		}
	}
	if b.dismiss.Clicked(gtx) && j != nil {
		b.hidden = j.FinishedAt
	}
	if b.choose.Clicked(gtx) {
		// Step 5 opens «Не всі сцени готові» with the scenes and their errors.
		c.UI(pv.ID).Gaps = true
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: pv.ID, Step: 5})
	}
	var body layout.Widget
	now := time.Now()
	switch {
	case j == nil:
	case j.Status == studio.JobRunning:
		body = func(gtx C) D { return b.running(gtx, c, j) }
	case j.Status == studio.JobError && j.FinishedAt != b.hidden && now.Sub(time.UnixMilli(j.FinishedAt)) < time.Hour:
		body = func(gtx C) D { return b.failed(gtx, c, pv, j) }
	case j.Status == studio.JobDone && j.FinishedAt != b.hidden && now.Sub(time.UnixMilli(j.FinishedAt)) < doneFor:
		left := doneFor - now.Sub(time.UnixMilli(j.FinishedAt))
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(left)})
		body = func(gtx C) D { return b.finished(gtx, c, j) }
	}
	b.open.Duration, b.open.Ease = gui.DurSheet, gui.EaseInOutCubic
	a := b.open.Animate(gtx, b2f(body != nil))
	if body != nil {
		b.last = body
	}
	if a <= 0.001 || b.last == nil {
		return D{}
	}
	return collapse(gtx, a, b.last)
}

func (b *bannerState) running(gtx C, c *core.Ctx, j *studio.Job) D {
	th := c.Th
	pct := max(0, min(100, j.Progress))
	narrow := gtx.Constraints.Max.X < gtx.Dp(560)
	icon := func(gtx C) D {
		s := gtx.Dp(40)
		gui.FillCircle(gtx, 40, th.Pal.RunningSoft)
		is := gtx.Dp(20)
		o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
		gui.Spinner{Size: 20, Color: th.Pal.RunningInk}.Layout(gtx, th)
		o.Pop()
		return D{Size: image.Pt(s, s)}
	}
	msg := gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, firstText(j.Message, "Працюємо…"))
	bar := gui.ProgressBar{Value: float32(pct) / 100, Height: 6, Tone: gui.ToneRunning, Anim: &b.bar}.Widget(th)
	head := func(gtx C) D {
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.BodyStrong, MaxLines: 1}.Widget(th, core.JobLabel(j.Kind))),
			layout.Flexed(1, func(gtx C) D {
				if narrow {
					return D{Size: image.Pt(gtx.Constraints.Min.X, 0)}
				}
				return layout.Inset{Left: gui.Sp12}.Layout(gtx, msg)
			}),
			layout.Rigid(gui.Text{Style: th.BodyStrong, MaxLines: 1}.Widget(th, strconv.Itoa(pct)+"%")),
		)
	}
	return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		if narrow {
			// The compact header has the stop button; the banner reads top to bottom.
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(icon),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return gui.VStack(gtx, 4, head, msg, func(gtx C) D { return layout.Inset{Top: 4}.Layout(gtx, bar) })
				}),
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(icon),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, head, bar) }),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconSquare, Text: "Зупинити",
					Tip: "Нові задачі не стартують; уже запущені доробляться"}.Layout(gtx, th, &b.stop)
			}),
		)
	})
}

func (b *bannerState) failed(gtx C, c *core.Ctx, pv *core.ProjectView, j *studio.Job) D {
	th := c.Th
	blocked := j.Blocked == studio.BlockedGaps
	narrow := gtx.Constraints.Max.X < gtx.Dp(560)
	icon := func(gtx C) D {
		s := gtx.Dp(40)
		gui.FillCircle(gtx, 40, th.Pal.ErrorSoft)
		is := gtx.Dp(20)
		o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
		gui.IconAlertTriangle.Layout(gtx, 20, th.Pal.ErrorInk)
		o.Pop()
		return D{Size: image.Pt(s, s)}
	}
	text := func(gtx C) D {
		title := "Етап «" + core.JobLabel(j.Kind) + "» зупинився з помилкою"
		msg := core.ErrorText(errString(firstText(j.Error, j.Message)))
		lines := 3
		if blocked {
			// The autopilot stopped before the montage: some scenes failed.
			// Nothing was filled in; the montage step lists them and asks.
			title = core.JobLabel(j.Kind) + " чекає на твоє рішення перед монтажем"
			if n := len(studio.MontageGaps(&pv.P, "")); n > 0 {
				msg = fmt.Sprintf("Не готові %d з %d сцен — студія нічого не підставила сама. Обери: змонтувати без них, з кадром замість відео або перезняти невдалі.", n, len(pv.P.Scenes))
			}
			lines = 4
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.BodyStrong, MaxLines: 2}.Widget(th, title)),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: lines}.Widget(th, msg)),
		)
	}
	retry := func(gtx C) D {
		if blocked {
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
				func(gtx C) D {
					return gui.Button{Kind: gui.Primary, Size: gui.Sm, Icon: gui.IconScissors, Text: "Обрати, як змонтувати"}.Layout(gtx, th, &b.choose)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Перезняти невдалі",
						Tip: "Автопілот ще раз спробує сцени, що не вдалися (витрачає кредити)"}.Layout(gtx, th, &b.retry)
				})
		}
		return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Повторити"}.Layout(gtx, th, &b.retry)
	}
	dismiss := func(gtx C) D {
		return gui.IconButton{Icon: gui.IconX, Size: 36, Tip: "Сховати"}.Layout(gtx, th, &b.dismiss)
	}
	return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		if narrow {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(icon),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return gui.VStack(gtx, gui.Sp12, text, func(gtx C) D { return layout.W.Layout(gtx, retry) })
				}),
				layout.Rigid(dismiss),
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(icon),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, text),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(retry),
			layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
			layout.Rigid(dismiss),
		)
	})
}

func (b *bannerState) finished(gtx C, c *core.Ctx, j *studio.Job) D {
	th := c.Th
	return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				s := gtx.Dp(40)
				gui.FillCircle(gtx, 40, th.Pal.SuccessSoft)
				is := gtx.Dp(20)
				o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
				gui.IconCheck.LayoutStroke(gtx, 20, 2.25, th.Pal.SuccessInk)
				o.Pop()
				return D{Size: image.Pt(s, s)}
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.BodyStrong, MaxLines: 1}.Widget(th, core.JobLabel(j.Kind)+" · готово")),
					layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, firstText(j.Message, "Готово"))),
				)
			}),
			layout.Rigid(func(gtx C) D {
				return gui.IconButton{Icon: gui.IconX, Size: 36, Tip: "Сховати"}.Layout(gtx, th, &b.dismiss)
			}),
		)
	})
}

// errString wraps a stored error message for core.ErrorText.
type errString string

func (e errString) Error() string { return string(e) }

// ---- context column --------------------------------------------------------------------------

// sideColumn is the 360 dp context column (virtualised: the scene
// navigator can hold 150 rows).
func (w *Workspace) sideColumn(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, step int, sv core.StepView, next core.Next) D {
	th := c.Th
	els := w.sideElements(c, pv, ps, step, sv, next, &w.nextBtn)
	b := gtx.Dp(bleed)
	return w.side.Layout(gtx, th, len(els), func(gtx C, i int) D {
		gtx.Constraints.Min.X = 0
		cw := gtx.Constraints.Max.X - 2*b
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(cw, 0), Max: image.Pt(cw, gtx.Constraints.Max.Y)}
		m := op.Record(gtx.Ops)
		d := els[i](cg)
		call := m.Stop()
		if d.Size.Y == 0 {
			return D{Size: image.Pt(gtx.Constraints.Max.X, 0)}
		}
		o := op.Offset(image.Pt(b, 0)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(gtx.Constraints.Max.X, d.Size.Y)}
	})
}

// sideStack is the drawer body: the same cards, stacked.
func (w *Workspace) sideStack(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, step int, sv core.StepView, next core.Next) D {
	els := w.sideElements(c, pv, ps, step, sv, next, &w.dnext)
	var ch []layout.FlexChild
	for _, el := range els {
		el := el
		ch = append(ch, layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return el(gtx)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// sideElements lists the context cards; each carries its bottom gap.
func (w *Workspace) sideElements(c *core.Ctx, pv *core.ProjectView, ps *projState, step int, sv core.StepView, next core.Next, nextBtn *gui.Clickable) []layout.Widget {
	th := c.Th
	gap := func(wd layout.Widget) layout.Widget {
		return func(gtx C) D {
			m := op.Record(gtx.Ops)
			d := wd(gtx)
			call := m.Stop()
			if d.Size.Y == 0 {
				return d
			}
			call.Add(gtx.Ops)
			return D{Size: image.Pt(d.Size.X, d.Size.Y+gtx.Dp(gui.Sp16))}
		}
	}
	var els []layout.Widget
	els = append(els, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, gtx.Dp(2))} })
	if next.Title != "" {
		els = append(els, gap(func(gtx C) D {
			return gui.NextStepCard{Icon: next.Icon, Title: next.Title, Text: next.Text, Busy: next.Busy}.Layout(gtx, th, nextBtn)
		}))
	}
	els = append(els, gap(func(gtx C) D { return w.jobCard(gtx, c, pv) }))
	els = append(els, gap(func(gtx C) D { return sv.Side(gtx, c, pv) }))
	if n := len(pv.P.Scenes); n > 0 {
		els = append(els, func(gtx C) D {
			return layout.Inset{Top: gui.Sp4, Bottom: gui.Sp8, Left: gui.Sp4}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.H3}.Widget(th, "Сцени")),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, scenesN(n)+" · ≈ "+core.Clock(ps.f.total))),
				)
			})
		})
		locName := map[string]string{}
		for _, l := range pv.P.Locations {
			locName[l.ID] = l.Name
		}
		for i := range pv.P.Scenes {
			i := i
			// A location break (the old stripboard): the place where the next scenes happen.
			if lid := pv.P.Scenes[i].LocationID; lid != "" && (i == 0 || pv.P.Scenes[i-1].LocationID != lid) && locName[lid] != "" {
				name := locName[lid]
				first := i == 0
				els = append(els, func(gtx C) D { return locBreak(gtx, th, name, first) })
			}
			els = append(els, func(gtx C) D { return w.navRow(gtx, c, pv, ps, i, step) })
		}
		els = append(els, func(gtx C) D { return D{Size: image.Pt(0, gtx.Dp(gui.Sp16))} })
	}
	els = append(els, gap(func(gtx C) D { return w.tip(gtx, c, pv, step) }))
	return els
}

// jobCard is this project's job in the context column: the running stage
// with a ring, or the last finished one.
func (w *Workspace) jobCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	j := pv.P.Job
	if j == nil {
		return D{}
	}
	var row gui.ActivityRow
	switch j.Status {
	case studio.JobRunning:
		return D{} // the banner under the header shows the running job
	case studio.JobDone:
		row = gui.ActivityRow{Icon: gui.IconCheck, Tone: gui.ToneSuccess, Title: core.JobLabel(j.Kind) + " · готово",
			Meta: core.Updated(j.FinishedAt), Progress: -1}
	case studio.JobError:
		row = gui.ActivityRow{Icon: gui.IconAlertTriangle, Tone: gui.ToneError, Title: core.JobLabel(j.Kind) + " · помилка",
			Meta: core.Updated(j.FinishedAt), Progress: -1}
	case studio.JobCancelled:
		row = gui.ActivityRow{Icon: gui.IconCircleStop, Title: core.JobLabel(j.Kind) + " · зупинено",
			Meta: core.Updated(j.FinishedAt), Progress: -1}
	default:
		return D{}
	}
	return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp4,
			gui.Text{Style: th.Label, Color: th.Pal.Text2}.Widget(th, "Останній етап"),
			func(gtx C) D { return row.Layout(gtx, th, nil) },
		)
	})
}

// jobIcon is the icon of a job kind.
func jobIcon(k studio.JobKind) *gui.Icon {
	switch k {
	case studio.JobPlan:
		return gui.IconScrollText
	case studio.JobRefs:
		return gui.IconUsers
	case studio.JobFrames:
		return gui.IconImages
	case studio.JobVideos, studio.JobRender:
		return gui.IconFilm
	case studio.JobVoices:
		return gui.IconMic
	case studio.JobMontage:
		return gui.IconScissors
	case studio.JobCover:
		return gui.IconImage
	case studio.JobAutopilot:
		return gui.IconRocket
	case "translate":
		return gui.IconLanguages
	case "transcribe":
		return gui.IconAudioLines
	}
	return gui.IconActivity
}

// navItem is the state of one navigator row.
type navItem struct {
	btn gui.Clickable
	hov gui.Anim
}

// navRow is one scene of the navigator: thumbnail, number and title, stage.
func (w *Workspace) navRow(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, i, step int) D {
	th := c.Th
	s := &pv.P.Scenes[i]
	it := ps.nav[s.ID]
	if it == nil {
		it = new(navItem)
		ps.nav[s.ID] = it
	}
	ui := c.UI(pv.ID)
	if it.btn.Clicked(gtx) {
		ui.Scene, ui.ScrollTo = s.ID, s.ID
		if step == 1 {
			ps.bind.flushAll()
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: pv.ID, Step: 2, Scene: s.ID})
		}
		w.drawer.Close()
	}
	W := gtx.Constraints.Max.X
	h := gtx.Dp(56)
	size := image.Pt(W, h)
	it.btn.Clickable.Layout(gtx, func(gtx C) D {
		defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
		pointerHand(gtx)
		return D{Size: size}
	})
	it.hov.Duration = gui.DurHover
	hv := it.hov.Animate(gtx, b2f(it.btn.Hovered()))
	sel := ui.Scene == s.ID
	switch {
	case sel:
		gui.FillRRect(gtx, image.Rect(0, 2, W, h-2), 12, th.Pal.Surface)
	case hv > 0.01:
		gui.FillRRect(gtx, image.Rect(0, 2, W, h-2), 12, gui.WithAlpha(th.Pal.Surface, hv))
	}
	pad := gtx.Dp(8)
	tw, tht := gtx.Dp(64), gtx.Dp(40)
	o := op.Offset(image.Pt(pad, (h-tht)/2)).Push(gtx.Ops)
	tg := gtx
	tg.Constraints = layout.Exact(image.Pt(tw, tht))
	t := gui.Thumb{Radius: 8, Placeholder: gui.IconImage}
	if s.Frame.Path != "" && c.Img != nil {
		if img, _, ok := c.Img.Get(pv.Abs(s.Frame.Path), s.Frame.UpdatedAt, gui.ImgBucket(tw)); ok {
			t.Image = img
		} else {
			t.Loading = true
		}
	}
	t.Layout(tg, th)
	o.Pop()
	x := pad + tw + gtx.Dp(12)
	label, tone := sceneStage(*s)
	_, _, ink := th.Pal.ToneColors(tone)
	if tone == gui.ToneNeutral {
		ink = th.Pal.Text3
	}
	dur := core.Clock(sceneSeconds(*s, float64(ps.f.clip)))
	dg := gtx
	dg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(60), h)}
	m := op.Record(gtx.Ops)
	dd := gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Layout(dg, th, dur)
	dc := m.Stop()
	o = op.Offset(image.Pt(W-pad-dd.Size.X, (h-dd.Size.Y)/2)).Push(gtx.Ops)
	dc.Add(gtx.Ops)
	o.Pop()
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(max(0, W-x-pad-dd.Size.X-gtx.Dp(8)), h)}
	m = op.Record(gtx.Ops)
	title := firstText(s.Title, clipText(s.Narration, 60), "Сцена")
	ld := layout.Flex{Axis: layout.Vertical}.Layout(lg,
		layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, strconv.Itoa(i+1))),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, title)),
			)
		}),
		layout.Rigid(gui.Text{Style: th.Caption, Color: ink, MaxLines: 1}.Widget(th, label)),
	)
	lc := m.Stop()
	o = op.Offset(image.Pt(x, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// sceneStage is a scene's stage in a word and its tone.
func sceneStage(s studio.Scene) (string, gui.Tone) {
	switch studio.SceneState(s) {
	case "running":
		return "генерується", gui.ToneRunning
	case "error":
		return "помилка", gui.ToneError
	case "stale":
		return "змінено — перегенеруй", gui.ToneWarning
	case "shot":
		return "відео готове", gui.ToneSuccess
	case "framed":
		return "кадр готовий", gui.ToneSuccess
	}
	return "у плані", gui.ToneNeutral
}

// tip is the contextual hint card of a step.
func (w *Workspace) tip(gtx C, c *core.Ctx, pv *core.ProjectView, step int) D {
	th := c.Th
	var title, text string
	switch step {
	case 1:
		if pv.P.Input.Mode == studio.ModeVoice {
			title, text = "Своя озвучка", "Запис ріжемо на сцени по паузах у мовленні. Чистий звук без музики розпізнається найкраще."
			if pv.P.Input.TranscriptFile != "" {
				text = "Сцени наріжемо за таймкодами з твого файлу — розпізнавати мову не доведеться. Текст можна підправити в Плані."
			}
		} else {
			title, text = "Порада", "Маєш готовий текст для озвучки? Вибери «Готовий сценарій» — тоді кожне речення прозвучить дослівно."
		}
	case 2:
		title, text = "Порада", "Згадуй героїв у промтах як @тег — тоді в кадрі вони виглядатимуть точно як на референсі."
	case 3:
		title, text = "Порада", "Спершу перевір референси героїв: від них залежить схожість у всіх кадрах."
	case 4:
		title, text = "Порада", "Сцену з невдалим рухом можна перегенерувати окремо — інші кліпи залишаться."
	default:
		title, text = "Порада", "Музику, переходи й субтитри можна змінювати й перемонтовувати скільки завгодно — це безкоштовно."
	}
	return gui.TipCard{Icon: gui.IconLightbulb, Title: title, Text: text}.Layout(gtx, th, &w.tipBtn)
}

// locBreak names the location of the scenes below it in the navigator.
func locBreak(gtx C, th *gui.Theme, name string, first bool) D {
	top := gui.Sp12
	if first {
		top = gui.Sp4
	}
	return layout.Inset{Top: top, Bottom: gui.Sp4, Left: gui.Sp8}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.IconMapPin.Widget(13, th.Pal.Text3)),
			layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, name)),
		)
	})
}

// FlushEdits commits every edit still waiting for its typing pause (all
// projects). The shell should call it when the window closes, before
// Engine.Flush, so the last keystrokes are saved.
func (w *Workspace) FlushEdits() {
	for _, ps := range w.projs {
		ps.bind.flushAll()
	}
}
