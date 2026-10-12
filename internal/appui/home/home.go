// Package home is Головна — the first viewport of the new UI (GUI.md "FIRST
// VIEWPORT") — and the Проєкти page.
//
// Головна: a greeting with the project search; the story card ("Нова
// історія": a large story box with .txt import, profile chips with
// «Керувати», language and duration chips, the estimate line and the two
// ways to start — «Автопілот» and «Покроково»); the quick-action tiles; a
// right column with the "continue" card, the live queue and a rotating tip
// (below the main content on narrower windows); and «Мої проєкти» — a card
// grid with thumbnails, progress rings, status pills and a ⋯ menu.
//
// Проєкти: every project with search, status filters, sorting and the
// service status chips of the old projects board.
package home

import (
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type (
	// C and D shorten layout signatures.
	C = layout.Context
	D = layout.Dimensions
)

// Tour anchors on Головна (gui.Anchor keys the shell's first-run tour points at).
const (
	AnchorStory     = "home.story"     // the story box
	AnchorProfiles  = "home.profiles"  // the profile chips
	AnchorAutopilot = "home.autopilot" // the «Автопілот» button
)

// Page is the state of Головна and Проєкти (one per window; they share the
// project cards, the ⋯ menu and the service checks).
type Page struct {
	// Queue returns the live queue snapshot; the shell passes its shared
	// feed. nil asks the engine every frame (tests).
	Queue func() studio.QueueSnapshot

	hero  hero
	pop   popList
	cards map[string]*cardState
	svc   services
	tip   tipState
	ff    ffHint // the «Не знайдено FFmpeg» call-out's install buttons (side.go)

	home  homeState
	proj  projState
	frame int64 // last frame either page was drawn
	sweep int64
}

type homeState struct {
	scroll    gui.Scroll
	search    gui.SearchField
	all       gui.Clickable
	next      gui.Clickable
	queue     gui.Clickable
	setup     gui.Clickable
	assist    gui.Clickable // «Помічник підключення» (Settings → Підключення)
	tour      gui.Clickable
	theme     gui.Clickable
	social    gui.Clickable
	tiles     [4]gui.Clickable // quick actions (quickDefs)
	stagger   gui.Stagger
	qrows     map[string]*gui.Anim
	nextHover gui.Anim
	nextRing  gui.Anim
}

// New creates the page state.
func New() *Page {
	p := &Page{cards: map[string]*cardState{}}
	p.home.qrows = map[string]*gui.Anim{}
	return p
}

// OpenMenu opens the ⋯ menu of a project card (headless screenshots use it
// to show the menu; the card button does the same).
func (p *Page) OpenMenu(projectID string) {
	p.pop.Open("menu:"+projectID, moreAnchor(projectID), cardMenu(), "", false, 240, true)
}

// OpenChooser opens a chooser of the story card — "lang", "dur" or
// "profile" (the phone layout's list) — as its chip does (screenshots).
func (p *Page) OpenChooser(c *core.Ctx, name string) {
	pr, has := p.hero.profile(c)
	lang, secs := p.hero.values(pr)
	switch name {
	case "lang":
		if lang == "" {
			lang = autoLang
		}
		p.pop.Open("lang", "home.lang", languageItems(), lang, true, 320, false)
	case "dur":
		p.pop.Open("dur", "home.dur", durationItems(c, secs), itoa(secs), false, 260, false)
	case "profile":
		p.pop.Open("profile", "home.profile", profileItems(c), profileValue(pr, has), false, 300, false)
	case "sound", "content":
		p.hero.modes.update(pr, has)
		list, v := pickers.SoundModes, p.hero.modes.sound
		if name == "content" {
			list, v = pickers.ContentModes, p.hero.modes.content
		}
		p.pop.Open(name, "home."+name, modeItems(list), v, false, 340, false)
	}
}

// entered reports whether this is the first frame the page is drawn after
// it was not (a route change), and forgets card states long unseen.
func (p *Page) entered(c *core.Ctx) bool {
	f := c.Frame()
	e := f != p.frame+1 && f != p.frame
	p.frame = f
	if e {
		p.pop.Close() // a menu left open on another visit does not come back
		p.hero.edit.close()
	}
	if f-p.sweep > 900 {
		p.sweep = f
		for id, st := range p.cards {
			if f-st.seen > 900 {
				delete(p.cards, id)
			}
		}
	}
	return e
}

func (p *Page) queueSnap(c *core.Ctx) studio.QueueSnapshot {
	if p.Queue != nil {
		return p.Queue()
	}
	return c.E.Queue()
}

// dispatch applies a pick from the shared popover: a language or length on
// the story card (or the phone profile list), or a ⋯ menu action of a card.
func (p *Page) dispatch(gtx C, c *core.Ctx) {
	id, val, ok := p.pop.Take()
	if !ok {
		return
	}
	switch {
	case id == "lang":
		if val == autoLang {
			val = ""
		}
		p.hero.langCode, p.hero.langSet = val, true
	case id == "dur":
		if n := atoi(val); n > 0 {
			p.hero.secs, p.hero.secsSet = n, true
		}
	case id == "sound" || id == "content":
		p.hero.modes.pick(id, val)
	case id == "profile":
		switch val {
		case profManage:
			c.OpenSheet(core.Sheet{Kind: core.SheetProfiles})
		case profDefault:
			p.hero.profileID, p.hero.picked = "", true
			p.hero.langSet, p.hero.secsSet = false, false
		default:
			p.hero.profileID, p.hero.picked = val, true
			p.hero.langSet, p.hero.secsSet = false, false
		}
	case strings.HasPrefix(id, "menu:"):
		p.cardAction(c, strings.TrimPrefix(id, "menu:"), val)
	}
	gtx.Execute(op.InvalidateCmd{})
}

// ---- shared helpers ----------------------------------------------------------------------------

// PagePad is the page padding for the current window width (GUI.md: 32 dp,
// 24 under 1280, 16 under 900).
func PagePad(gtx C, th *gui.Theme) unit.Dp {
	dp := windowDp(gtx, th)
	switch {
	case dp < 900:
		return gui.Sp16
	case dp < 1280:
		return gui.Sp24
	}
	return gui.Sp32
}

// windowDp is the window width in dp (the constraints outside Root).
func windowDp(gtx C, th *gui.Theme) float32 {
	w := th.WindowSize().X
	if w == 0 {
		w = gtx.Constraints.Max.X
	}
	return float32(w) / gtx.Metric.PxPerDp
}

// narrowWindow reports the phone layout (the rail is a bottom bar).
func narrowWindow(gtx C, th *gui.Theme) bool { return windowDp(gtx, th) < 900 }

// pageList lays out a page as a virtualised list of full-width rows with the
// page padding around and gap between them.
func pageList(gtx C, th *gui.Theme, sc *gui.Scroll, pad unit.Dp, rows []layout.Widget) D {
	n := len(rows)
	return sc.Layout(gtx, th, n, func(gtx C, i int) D {
		in := layout.Inset{Left: pad, Right: pad, Top: gui.Sp24}
		if i == 0 {
			in.Top = pad
		}
		if i == n-1 {
			in.Bottom = gui.Sp40
		}
		return in.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return rows[i](gtx)
		})
	})
}

// sectionHead is a section title with an optional count and a link on the right.
func sectionHead(gtx C, th *gui.Theme, title, count string, link string, click *gui.Clickable) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(gui.Text{Style: th.H2, MaxLines: 1}.Widget(th, title)),
		layout.Rigid(func(gtx C) D {
			if count == "" {
				return D{}
			}
			return layout.Inset{Left: gui.Sp12}.Layout(gtx, gui.Badge{Text: count}.Widget(th))
		}),
		layout.Flexed(1, func(gtx C) D {
			if link == "" || click == nil {
				return D{Size: image.Pt(gtx.Constraints.Min.X, 0)}
			}
			return layout.E.Layout(gtx, func(gtx C) D {
				return gui.Link{Text: link, Trail: gui.IconChevronRight}.Layout(gtx, th, click)
			})
		}),
	)
}

// minuteTick asks for a frame when relative times ("5 хв тому") change.
func minuteTick(gtx C) { gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(30 * time.Second)}) }

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
