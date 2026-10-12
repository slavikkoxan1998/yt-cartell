package queue

import (
	"fmt"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type (
	// C and D shorten layout signatures.
	C = layout.Context
	D = layout.Dimensions
)

const (
	drawerRecent = 12 // recent rows in the drawer (the page shows more)
)

// View is the queue's widget state: one per window, shared by the drawer and
// the page (both read the same Feed).
type View struct {
	Feed *Feed

	rows   map[string]*rowState
	projs  map[string]*projState
	frame  int64
	scroll gui.Scroll
	// history paging on the page: rows shown and whether older weeks are in
	limit int
	older bool

	cancelAll   gui.Clickable
	openPage    gui.Clickable
	pageStop    gui.Clickable
	newStory    gui.Clickable
	more        gui.Clickable
	allProjects gui.Clickable
	notify      gui.Clickable
}

// rowState is the interaction and animation state of one queue row (keyed
// by QueueItem.Key, which identifies one job run or one regeneration). The
// drawer row, the page's history row and a running job's card share it.
type rowState struct {
	open, retry, stop gui.Clickable
	bar               gui.Anim
	in                gui.Anim
	img               gui.ImageView
	seen              int64
	// a running job's pictures (scene, cast member or location id) and its
	// «ще N» tile
	tiles map[string]*tileState
	rest  gui.Clickable
}

// tileState is one picture of a running job's card.
type tileState struct {
	click gui.Clickable
	img   gui.ImageView
	bar   gui.Anim
	hov   gui.Anim
}

// projState is one project row of «Що можна запустити».
type projState struct {
	open gui.Clickable
	img  gui.ImageView
	seen int64
}

// New creates the queue view on feed.
func New(feed *Feed) *View {
	return &View{Feed: feed, rows: map[string]*rowState{}, projs: map[string]*projState{}}
}

func (v *View) state(key string) *rowState {
	st := v.rows[key]
	if st == nil {
		st = &rowState{}
		st.in.Duration = gui.DurRoute
		st.in.Set(0)
		v.rows[key] = st
	}
	st.seen = v.frame
	return st
}

func (st *rowState) tile(id string) *tileState {
	if st.tiles == nil {
		st.tiles = map[string]*tileState{}
	}
	ts := st.tiles[id]
	if ts == nil {
		ts = &tileState{}
		st.tiles[id] = ts
	}
	return ts
}

func (v *View) proj(id string) *projState {
	ps := v.projs[id]
	if ps == nil {
		ps = &projState{}
		v.projs[id] = ps
	}
	ps.seen = v.frame
	return ps
}

// sweep forgets rows that were not drawn for a while (finished long ago).
func (v *View) sweep(c *core.Ctx) {
	f := c.Frame()
	if f == v.frame {
		return
	}
	v.frame = f
	if f%600 != 0 {
		return
	}
	for k, st := range v.rows {
		if f-st.seen > 600 {
			delete(v.rows, k)
		}
	}
	for k, ps := range v.projs {
		if f-ps.seen > 600 {
			delete(v.projs, k)
		}
	}
}

// Subtitle is the one-line summary under the drawer title.
func Subtitle(snap studio.QueueSnapshot) string {
	n := snap.Counts.Running
	var parts []string
	if n > 0 {
		parts = append(parts, "Генерується: "+fmt.Sprint(n))
	} else {
		parts = append(parts, "Зараз нічого не генерується")
	}
	if e := snap.Counts.Errors; e > 0 {
		parts = append(parts, core.Count(e, "помилка", "помилки", "помилок"))
	}
	return strings.Join(parts, " · ")
}

// ---- actions -----------------------------------------------------------------------------------

func (v *View) handle(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState) {
	if st.open.Clicked(gtx) {
		r := core.Route{Page: core.PageProject, ProjectID: it.ProjectID, Step: it.Step}
		switch it.Regen {
		case studio.RegenFrame, studio.RegenVideo, studio.RegenVoice:
			r.Scene = it.ItemID // straight to the scene
		}
		c.Navigate(r)
	}
	if st.retry.Clicked(gtx) {
		started := false
		switch stage, partial := retryStage(it); {
		case partial:
			started = c.Run(it.ProjectID, stage, false, "") // finished items are skipped
		case c.Offline:
			c.Run(it.ProjectID, "", false, "") // refuses: no generation in demo renders
		default:
			if err := c.E.RetryItem(it); err != nil {
				c.Error(err)
			} else {
				started = true
			}
		}
		if started {
			c.Toast(gui.ToneRunning, "Запущено знову: "+it.Label)
		}
	}
	if st.stop.Clicked(gtx) && it.Kind != "" {
		c.E.Cancel(it.ProjectID)
		c.Toast(gui.ToneInfo, "Зупиняємо «"+it.Label+"» — готові частини збережуться.")
	}
}

// confirmCancelAll asks before stopping every running job.
func confirmCancelAll(c *core.Ctx) {
	c.Confirm(core.Confirm{
		Title:  "Зупинити все, що генерується?",
		Text:   "Усі етапи, що зараз ідуть, зупиняться. Готові кадри, відео й озвучка залишаться — продовжити можна будь-коли кнопкою «Повторити».",
		Icon:   gui.IconCircleStop,
		OK:     "Зупинити все",
		Danger: true,
		OnOK: func() {
			n := c.E.CancelAll()
			if n == 0 {
				c.Toast(gui.ToneInfo, "Зупиняти нічого: окремі кадри й озвучки завершаться самі.")
				return
			}
			c.Toast(gui.ToneInfo, "Зупиняємо "+core.Count(n, "етап", "етапи", "етапів")+" — готове збережеться.")
		},
	})
}

// ---- lists -------------------------------------------------------------------------------------

// entry is one line of a queue list: a group heading or a row.
type entry struct {
	head    string // a heading ("Сьогодні") instead of a row
	count   int    // shown after the heading when > 0
	it      studio.QueueItem
	divider bool // hairline under the row (the next entry is a row too)
	grouped bool // the row sits under a day heading: its time only
}

// rowsOf lists items as rows.
func rowsOf(items []studio.QueueItem) []entry {
	out := make([]entry, len(items))
	for i, it := range items {
		out[i] = entry{it: it, divider: i < len(items)-1}
	}
	return out
}

// byDay lists finished items under day headings: «Сьогодні», «Вчора»,
// «12 вересня» (with the year when it is another one).
func byDay(items []studio.QueueItem, now time.Time) []entry {
	var out []entry
	last := ""
	for i, it := range items {
		day := dayLabel(it.FinishedAt, now)
		if day != last {
			out = append(out, entry{head: day})
			last = day
		}
		next := i+1 < len(items) && dayLabel(items[i+1].FinishedAt, now) == day
		out = append(out, entry{it: it, divider: next, grouped: true})
	}
	return out
}

var monthsGen = [...]string{"січня", "лютого", "березня", "квітня", "травня", "червня",
	"липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}

// dayLabel names the day of a unix-ms time relative to now.
func dayLabel(ms int64, now time.Time) string {
	if ms <= 0 {
		return "Раніше"
	}
	t := time.UnixMilli(ms).In(now.Location())
	y, m, d := t.Date()
	ny, nm, nd := now.Date()
	if y == ny && m == nm && d == nd {
		return "Сьогодні"
	}
	if yy, ym, yd := now.AddDate(0, 0, -1).Date(); y == yy && m == ym && d == yd {
		return "Вчора"
	}
	s := fmt.Sprintf("%d %s", d, monthsGen[m-1])
	if y != ny {
		s += fmt.Sprintf(" %d", y)
	}
	return s
}

// heading is a group title: sentence case, Label 14 Medium.
func heading(gtx C, th *gui.Theme, title string, n int, first bool) D {
	if n > 0 {
		title += fmt.Sprintf(" · %d", n)
	}
	top := gui.Sp16
	if first {
		top = gui.Sp4
	}
	return layout.Inset{Top: top, Bottom: gui.Sp4}.Layout(gtx,
		gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, title))
}

func (v *View) entry(gtx C, c *core.Ctx, e entry, first, wide bool) D {
	if e.head != "" {
		return heading(gtx, c.Th, e.head, e.count, first)
	}
	return v.row(gtx, c, e.it, e.divider, wide, e.grouped)
}

// ---- drawer ------------------------------------------------------------------------------------

// DrawerBody is the content of the queue drawer: what runs, then the recent
// results by day (it scrolls inside the drawer).
func (v *View) DrawerBody(gtx C, c *core.Ctx) D {
	v.sweep(c)
	th := c.Th
	snap := v.Feed.Snapshot()
	if len(snap.Running) == 0 && len(snap.Recent) == 0 {
		return layout.Inset{Top: gui.Sp40}.Layout(gtx, func(gtx C) D {
			return gui.EmptyState{Icon: gui.IconListVideo, Title: "Черга порожня",
				Text: "Тут з'являться кадри, відео й озвучка, що генеруються — з усіх проєктів одразу."}.Layout(gtx, th, nil)
		})
	}
	var entries []entry
	if len(snap.Running) > 0 {
		entries = append(entries, entry{head: "Зараз генерується", count: len(snap.Running)})
		entries = append(entries, rowsOf(snap.Running)...)
	}
	entries = append(entries, byDay(snap.Recent[:min(len(snap.Recent), drawerRecent)], time.Now())...)
	ws := make([]layout.Widget, len(entries))
	for i, e := range entries {
		i, e := i, e
		ws[i] = func(gtx C) D { return v.entry(gtx, c, e, i == 0, false) }
	}
	return gui.VStack(gtx, 0, ws...)
}

// DrawerFooter holds «Скасувати все» and the way to the full page.
func (v *View) DrawerFooter(gtx C, c *core.Ctx) D {
	th := c.Th
	if v.cancelAll.Clicked(gtx) {
		confirmCancelAll(c)
	}
	if v.openPage.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageQueue})
	}
	jobs := v.Feed.Jobs()
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconCircleStop, Text: "Скасувати все",
				Disabled: jobs == 0, Tip: "Зупинити всі етапи, що генеруються"}.Layout(gtx, th, &v.cancelAll)
		}),
		layout.Flexed(1, func(gtx C) D { return D{Size: gtx.Constraints.Min} }),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Уся черга", Trail: gui.IconChevronRight}.Layout(gtx, th, &v.openPage)
		}),
	)
}

// ---- helpers -----------------------------------------------------------------------------------

// PagePad is the page padding for the current window width (GUI.md: 32 dp,
// 24 under 1280, 16 under 900).
func PagePad(gtx C, th *gui.Theme) unit.Dp {
	w := th.WindowSize().X
	if w == 0 {
		w = gtx.Constraints.Max.X
	}
	dp := float32(w) / gtx.Metric.PxPerDp
	switch {
	case dp < 900:
		return gui.Sp16
	case dp < 1280:
		return gui.Sp24
	}
	return gui.Sp32
}

// record lays w out into a macro: its size first, the drawing later.
func record(gtx C, w layout.Widget) (D, op.CallOp) {
	m := op.Record(gtx.Ops)
	d := w(gtx)
	return d, m.Stop()
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
