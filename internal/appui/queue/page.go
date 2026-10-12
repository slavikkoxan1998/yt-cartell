package queue

import (
	"fmt"
	"image"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// History on the page: the last historyDays days, historyStep rows at a
// time («Показати ще» adds more, older ones included).
const (
	historyDays = 7
	historyStep = 50
)

// minQuick is the least spare height (dp) worth filling with «Що можна
// запустити» and its quick start while something runs.
const minQuick = 360

// history is the part of the recent results the page shows, and whether
// there is more to reveal.
func (v *View) history(recent []studio.QueueItem, now time.Time) (items []studio.QueueItem, more bool) {
	limit := v.limit
	if limit <= 0 {
		limit = historyStep
	}
	cutoff := now.AddDate(0, 0, -historyDays).UnixMilli()
	for _, it := range recent { // newest first
		if !v.older && it.FinishedAt > 0 && it.FinishedAt < cutoff {
			return items, true
		}
		if len(items) == limit {
			return items, true
		}
		items = append(items, it)
	}
	return items, false
}

// pageData is what one frame of the page shows.
type pageData struct {
	jobs    []studio.QueueItem // running project jobs: a rich card each
	singles []studio.QueueItem // single pictures, clips and voices generating
	hist    []studio.QueueItem // «Нещодавно»
	more    bool               // older history to reveal
	next    []nextStep         // projects waiting for their next step
	now     time.Time
	welcome bool // no project and nothing ever ran: one welcome card
}

func (d pageData) running() bool { return len(d.jobs)+len(d.singles) > 0 }

func (v *View) data(c *core.Ctx) pageData {
	snap := v.Feed.Snapshot()
	d := pageData{now: time.Now()}
	for _, it := range snap.Running {
		if it.Kind != "" {
			d.jobs = append(d.jobs, it)
		} else {
			d.singles = append(d.singles, it)
		}
	}
	d.hist, d.more = v.history(snap.Recent, d.now)
	list := c.List()
	d.next = nextSteps(c, list)
	d.welcome = len(list) == 0 && len(snap.Running) == 0 && len(snap.Recent) == 0
	return d
}

// Page is the full «Черга» page. Wide windows put the work on the left —
// every running job as a card with its scenes, or (nothing running) «Що
// можна запустити» down to the bottom of the window — and «Нещодавно», the
// last week by day, on the right. Narrow windows stack them.
func (v *View) Page(gtx C, c *core.Ctx) D {
	v.sweep(c)
	th := c.Th
	if v.pageStop.Clicked(gtx) {
		confirmCancelAll(c)
	}
	if v.newStory.Clicked(gtx) {
		c.OpenSheet(core.Sheet{Kind: core.SheetNewProject})
	}
	if v.more.Clicked(gtx) {
		v.limit, v.older = max(v.limit, historyStep)+historyStep, true
	}
	if v.allProjects.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProjects})
	}
	d := v.data(c)
	pad := PagePad(gtx, th)
	// Relative times ("іде 3 хв", "Вчора") move on by themselves.
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(30 * time.Second)})
	viewH := gtx.Constraints.Max.Y
	return v.scroll.LayoutWidget(gtx, th, func(gtx C) D {
		return layout.Inset{Top: pad, Bottom: pad, Left: pad, Right: pad}.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			hd, hc := record(gtx, func(gtx C) D { return v.pageHeader(gtx, c) })
			gap := gtx.Dp(gui.Sp24)
			hc.Add(gtx.Ops)
			// The height left under the header: the idle column, the quick
			// start and the welcome card reach the bottom of the window.
			fill := viewH - 2*gtx.Dp(pad) - hd.Size.Y - gap
			defer op.Offset(image.Pt(0, hd.Size.Y+gap)).Push(gtx.Ops).Pop()
			bd := v.body(gtx, c, d, fill)
			return D{Size: image.Pt(gtx.Constraints.Max.X, hd.Size.Y+gap+bd.Size.Y)}
		})
	})
}

func (v *View) pageHeader(gtx C, c *core.Ctx) D {
	th := c.Th
	title := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp4,
			gui.Text{Style: th.H1}.Widget(th, "Черга"),
			gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, "Усе, що генерується зараз, у всіх проєктах. Проєкт можна закрити — робота триває."),
		)
	}
	btn := func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Icon: gui.IconCircleStop, Text: "Скасувати все",
			Disabled: v.Feed.Jobs() == 0}.Layout(gtx, th, &v.pageStop)
	}
	if gtx.Constraints.Max.X < gtx.Dp(640) {
		return gui.VStack(gtx, gui.Sp16, title, func(gtx C) D {
			return layout.W.Layout(gtx, btn)
		})
	}
	return layout.Flex{Alignment: layout.End}.Layout(gtx,
		layout.Flexed(1, title),
		layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
		layout.Rigid(btn),
	)
}

// body lays the sections out under the header; fill (px) is the height
// that reaches the bottom of the window.
func (v *View) body(gtx C, c *core.Ctx, d pageData, fill int) D {
	if d.welcome {
		return v.welcome(gtx, c, fill)
	}
	// The notifications tip joins an idle page (a running one fills up
	// with the jobs and «Що можна запустити»).
	tip := !d.running() && v.wantNotify(c)
	if gtx.Constraints.Max.X < gtx.Dp(900) {
		var ws []layout.Widget
		if d.running() {
			ws = append(ws, func(gtx C) D { return v.runningSection(gtx, c, d) })
		} else {
			ws = append(ws, func(gtx C) D { return v.nextSection(gtx, c, d, 0, true) })
		}
		ws = append(ws, func(gtx C) D { return v.historySection(gtx, c, d) })
		if d.running() && len(d.next) > 0 {
			ws = append(ws, func(gtx C) D { return v.nextSection(gtx, c, d, 0, false) })
		}
		if tip {
			ws = append(ws, func(gtx C) D { return v.notifyCard(gtx, c) })
		}
		return gui.VStack(gtx, gui.Sp32, ws...)
	}
	// While something runs, «Що можна запустити» with its quick start fills
	// the column that has room for it down to the bottom of the window (the
	// left one under the jobs, else the right one under the history); with
	// no room it closes the right column without the quick start. Columns
	// lays the left column out first.
	gap := gtx.Dp(gui.Sp32)
	nextLeft := false
	left := func(gtx C) D {
		if !d.running() {
			return v.nextSection(gtx, c, d, fill, true)
		}
		rd, rc := record(gtx, func(gtx C) D { return v.runningSection(gtx, c, d) })
		rc.Add(gtx.Ops)
		rest := fill - rd.Size.Y - gap
		if !v.fits(gtx, c, d, rest) {
			return rd
		}
		nextLeft = true
		defer op.Offset(image.Pt(0, rd.Size.Y+gap)).Push(gtx.Ops).Pop()
		nd := v.nextSection(gtx, c, d, rest, true)
		return D{Size: image.Pt(gtx.Constraints.Max.X, rd.Size.Y+gap+nd.Size.Y)}
	}
	right := func(gtx C) D {
		ws := []layout.Widget{func(gtx C) D { return v.historySection(gtx, c, d) }}
		if tip {
			ws = append(ws, func(gtx C) D { return v.notifyCard(gtx, c) })
		}
		hd, hc := record(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp32, ws...) })
		hc.Add(gtx.Ops)
		if !d.running() || nextLeft {
			return hd
		}
		rest := fill - hd.Size.Y - gap
		full := v.fits(gtx, c, d, rest)
		if !full && len(d.next) == 0 {
			return hd
		}
		minH := 0
		if full {
			minH = rest
		}
		defer op.Offset(image.Pt(0, hd.Size.Y+gap)).Push(gtx.Ops).Pop()
		nd := v.nextSection(gtx, c, d, minH, full)
		return D{Size: image.Pt(gtx.Constraints.Max.X, hd.Size.Y+gap+nd.Size.Y)}
	}
	return gui.Columns{Gap: gui.Sp24}.Layout(gtx, gui.Col{Span: 7, W: left}, gui.Col{Span: 5, W: right})
}

// fits reports whether «Що можна запустити» with the quick start fits in
// h px (it then stretches to fill them).
func (v *View) fits(gtx C, c *core.Ctx, d pageData, h int) bool {
	if h < gtx.Dp(minQuick) {
		return false
	}
	nd, _ := record(gtx, func(gtx C) D { return v.nextSection(gtx, c, d, 0, true) })
	return nd.Size.Y <= h
}

// wantNotify: Telegram notifications are not set up (and tips are on) — the
// page suggests them, since the queue's work often ends while you are away.
func (v *View) wantNotify(c *core.Ctx) bool {
	cfg := c.E.Config().Get()
	t := cfg.Telegram
	return !cfg.UI.TipsHidden && !(t.Enabled && t.BotToken != "" && t.ChatID != "")
}

// notifyCard suggests the Telegram notifications.
func (v *View) notifyCard(gtx C, c *core.Ctx) D {
	if v.notify.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "telegram"})
	}
	return gui.TipCard{Icon: gui.IconBellRing, Title: "Дізнавайся, коли відео готове",
		Text:   "Бот у Telegram напише, щойно етап завершиться або щось не вдасться, — навіть коли ти не за комп'ютером.",
		Action: "Налаштувати сповіщення"}.Layout(gtx, c.Th, &v.notify)
}

// runningSection: «Зараз генерується» — a card per running job with its
// scenes, then the single pictures, clips and voices in one card.
func (v *View) runningSection(gtx C, c *core.Ctx, d pageData) D {
	return section(gtx, c.Th, "Зараз генерується", len(d.jobs)+len(d.singles), gui.ToneRunning, 0, func(gtx C) D {
		var ws []layout.Widget
		for _, it := range d.jobs {
			it := it
			ws = append(ws, func(gtx C) D { return v.jobCard(gtx, c, it) })
		}
		if len(d.singles) > 0 {
			ws = append(ws, func(gtx C) D { return v.singlesCard(gtx, c, d.singles) })
		}
		return gui.VStack(gtx, gui.Sp24, ws...)
	})
}

// historySection: «Нещодавно» — the results of the last week by day, each
// with its picture.
func (v *View) historySection(gtx C, c *core.Ctx, d pageData) D {
	th := c.Th
	return section(gtx, th, "Нещодавно", 0, gui.ToneNeutral, 0, func(gtx C) D {
		return gui.Card{Padding: cardPad(gtx)}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			if len(d.hist) == 0 {
				return gui.ActivityRow{Icon: gui.IconHistory, Title: "Історія порожня",
					Meta: "Готові й невдалі етапи за тиждень з'являться тут.", Progress: -1}.Layout(gtx, th, nil)
			}
			entries := byDay(d.hist, d.now)
			ws := make([]layout.Widget, 0, len(entries)+1)
			for i, e := range entries {
				i, e := i, e
				ws = append(ws, func(gtx C) D {
					if e.head != "" {
						return heading(gtx, th, e.head, 0, i == 0)
					}
					return v.histRow(gtx, c, e.it, e.divider)
				})
			}
			if d.more {
				ws = append(ws, func(gtx C) D {
					return layout.Inset{Top: gui.Sp12}.Layout(gtx, func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Text: "Показати ще", Trail: gui.IconChevronDown}.Layout(gtx, th, &v.more)
					})
				})
			}
			return gui.VStack(gtx, 0, ws...)
		})
	})
}

// nextSection: «Що можна запустити». full adds the quick start for a new
// story; minH (px, 0 = natural) is the height the section fills.
func (v *View) nextSection(gtx C, c *core.Ctx, d pageData, minH int, full bool) D {
	return section(gtx, c.Th, "Що можна запустити", 0, gui.ToneNeutral, minH, func(gtx C) D {
		return v.nextCard(gtx, c, d, full)
	})
}

// section is a column block: a heading (H3, an optional count) over its
// content. minH (px) is the height the whole block reaches (0 = natural):
// the content gets the rest as its minimum height.
func section(gtx C, th *gui.Theme, title string, n int, tone gui.Tone, minH int, w layout.Widget) D {
	hd, hc := record(gtx, func(gtx C) D { return sectionHead(gtx, th, title, n, tone) })
	gap := gtx.Dp(gui.Sp12)
	hc.Add(gtx.Ops)
	cg := gtx
	cg.Constraints.Min.X = gtx.Constraints.Max.X
	cg.Constraints.Min.Y = min(max(0, minH-hd.Size.Y-gap), cg.Constraints.Max.Y)
	off := op.Offset(image.Pt(0, hd.Size.Y+gap)).Push(gtx.Ops)
	bd := w(cg)
	off.Pop()
	return D{Size: image.Pt(gtx.Constraints.Max.X, hd.Size.Y+gap+bd.Size.Y)}
}

func sectionHead(gtx C, th *gui.Theme, title string, n int, tone gui.Tone) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, title)),
		layout.Rigid(func(gtx C) D {
			if n <= 0 {
				return D{}
			}
			return layout.Inset{Left: gui.Sp8}.Layout(gtx, gui.Badge{Text: fmt.Sprint(n), Tone: tone}.Widget(th))
		}),
	)
}

// cardPad is the padding of the page's cards: roomy, tighter on phones.
func cardPad(gtx C) unit.Dp {
	if gtx.Constraints.Max.X < gtx.Dp(480) {
		return gui.Sp16
	}
	return gui.Sp24
}
