// Package settings is the Налаштування page (GUI.md → Screens → 4): cards in
// a responsive grid (two columns from 1280 dp) under a fixed header whose
// section tabs scroll smoothly to a card and follow the scroll position.
//
//	Підключення  Помічник підключення (assistant.go) · Текст · Кадри · Відео · G-Labs Studio · Озвучка · Google AI
//	             Studio · FFmpeg · Розпізнавання мови · Диктування · Сцен одночасно
//	Профілі      a card per profile + «Новий профіль» (sheets of package dialogs)
//	Автопілот    default profile, cover, retries, continue on error
//	Сповіщення   «Telegram-бот» (telegram.go): token, chat, test, events, the
//	             remote control (pairing code, allowed users, live progress),
//	             own Bot API server, what the bot can do
//	Вигляд       theme, reduced motion, tips, replay the tour
//	Дані         data folder, import from the web studio · Про студію: version,
//	             updates (the shell's sheet, «Перевіряти автоматично»), log
//
// Every change goes through config.Store.Update on a background goroutine
// (see store.go); text fields commit on Enter or when they lose focus.
package settings

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

// section is one tab of the header (Route.Section ids).
type section struct {
	id    string
	label string
	short string // label of the selected tab on phones
	icon  *gui.Icon
}

var sections = []section{
	{"connections", "Підключення", "Сервіси", gui.IconPlug},
	{"profiles", "Профілі", "Профілі", gui.IconLayers},
	{"autopilot", "Автопілот", "Автопілот", gui.IconRocket},
	{"telegram", "Сповіщення", "Telegram", gui.IconBell},
	{"appearance", "Вигляд", "Вигляд", gui.IconPalette},
	{"data", "Дані", "Дані", gui.IconHardDrive},
}

// tabOf maps a Route.Section (or a card mark) to its tab.
func tabOf(id string) int {
	switch id {
	case "assistant", "stt", "dictate", "text", "frames", "video", "stock", "glabs", "tts", "google", "ffmpeg", "speed":
		id = "connections"
	case "about":
		id = "data"
	}
	for i, s := range sections {
		if s.id == id {
			return i
		}
	}
	return -1
}

// Page is the settings screen state.
type Page struct {
	cfg mirror
	now config.Config // this frame's settings

	lastFrame int64
	applied   string // Route.Section of this visit
	want      string // section to jump to once measured

	tabs   gui.PillTabs
	scroll gui.Scroll
	blur   int // tag: a press anywhere takes focus from the fields

	// Section scrolling.
	marks    map[string]int // content y (px) of sections and cards
	insetTop int
	contentH int
	viewH    int
	anim     gui.Anim
	animOn   bool
	animSet  int
	spy      int // tab of the section in view
	pin      int // tab pinned by a click (-1 none)
	pinOff   int
	fade     gui.Anim

	assist     assistCard // «Помічник підключення» (assistant.go)
	text       textCard
	frames     framesCard // «Кадри» (media.go)
	video      videoCard  // «Відео»
	stock      stockCard  // «Стокові відео» (stock.go)
	glabs      glabsCard  // «G-Labs Studio»: the address and key both use
	tts        ttsCard
	google     googleCard
	ff         ffCard
	stt        sttCard
	dict       dictCard // «Диктування» (stt.go)
	speed      speedCard
	profiles   profilesSection
	autopilot  autopilotCard
	telegram   telegramCard
	appearance appearanceCard
	data       dataCard
	about      aboutCard

	// The click guard after a dropdown closes (guard.go).
	guardTag   int
	guardUntil time.Time
	popWas     bool

	// refreshed is when the settings file was last compared with the disk
	// (another studio window may have changed it).
	refreshed time.Time
}

// New creates the settings page.
func New() *Page {
	p := &Page{marks: map[string]int{}, pin: -1}
	p.cfg.view = &p.now // a change shows in the same frame (store.go)
	return p
}

// fields lists every bound text field (flushed before checks).
func (p *Page) fields() []*field {
	return []*field{
		&p.text.base, &p.text.key.field, &p.text.model, &p.text.gmodel,
		&p.glabs.base, &p.glabs.key.field, &p.frames.model,
		&p.tts.base, &p.tts.model, &p.tts.voiceID,
		&p.frames.custom.base, &p.frames.custom.key.field, &p.frames.custom.model,
		&p.video.custom.base, &p.video.custom.key.field, &p.video.custom.model,
		&p.frames.custom.spec.editor, &p.frames.custom.spec.docs, // docs: never committed (no commit func)
		&p.video.custom.spec.editor, &p.video.custom.spec.docs,
		&p.tts.spec.editor, &p.tts.spec.docs, &p.stt.spec.editor, &p.stt.spec.docs, // audiospec.go
		&p.assist.input, // assistant.go: never committed
		&p.ff.ffmpeg, &p.ff.ffprobe,
		&p.stt.key.field, &p.stt.base, &p.stt.model, &p.stt.bin, &p.stt.mdl, &p.stt.aaiKey.field,
		&p.dict.key.field, &p.dict.base, &p.dict.model,
		&p.telegram.token.field, &p.telegram.chat, &p.telegram.addUser, &p.telegram.server, // telegram.go
	}
}

// overField reports the pointer over a text field (a press there is the
// field's: it keeps or takes the focus itself).
func (p *Page) overField() bool {
	for _, f := range append(p.fields(), &p.google.add, &p.tts.add) {
		if f.hovered {
			return true
		}
	}
	return false
}

// flushFields commits every edit still in a field (checks read the settings).
func (p *Page) flushFields() {
	for _, f := range p.fields() {
		f.flush()
	}
}

// metrics are the responsive decisions of a frame.
type metrics struct {
	pad    unit.Dp // page padding
	cols   int     // card columns
	narrow bool    // phone-sized content
	wide   bool    // header tabs beside the title
}

func (p *Page) measure(gtx C, c *core.Ctx) metrics {
	W := dp(gtx, gtx.Constraints.Max.X)
	win := W + 92 // the rail, when the shell does not report the window
	if ws := c.WindowSize(); ws.X > 0 {
		win = dp(gtx, ws.X)
	}
	m := metrics{pad: 32, cols: 1}
	switch {
	case win < 900:
		m.pad = 16
	case win < 1280:
		m.pad = 24
	}
	inner := W - 2*m.pad
	if win >= 1280 && inner >= 1000 {
		m.cols = 2
	}
	m.narrow = inner < 600
	return m
}

// Layout draws the page; section ("" = top) scrolls to a card once.
func (p *Page) Layout(gtx C, c *core.Ctx, section string) D {
	th := c.Th
	p.visit(gtx, c, section)
	p.now = p.cfg.get(c)
	m := p.measure(gtx, c)
	size := gtx.Constraints.Max

	// A press anywhere on the page ends typing in a field (so it commits);
	// the field that was pressed takes the focus back right after.
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &p.blur, Kinds: pointer.Press})
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok && pe.Kind == pointer.Press && !p.overField() {
			gtx.Execute(key.FocusCmd{})
		}
	}
	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	event.Op(gtx.Ops, &p.blur)

	if i, ok := p.tabs.Changed(gtx); ok && i >= 0 && i < len(sections) {
		p.goTo(gtx, sections[i].id)
	}

	pad := gtx.Dp(m.pad)
	topPad := gtx.Dp(28)
	if m.narrow {
		topPad = gtx.Dp(16)
	}
	hg := gtx
	hg.Constraints = layout.Constraints{Min: image.Pt(max(0, size.X-2*pad), 0), Max: image.Pt(max(0, size.X-2*pad), size.Y)}
	o := op.Offset(image.Pt(pad, topPad)).Push(gtx.Ops)
	hd := p.header(hg, c, m)
	o.Pop()
	top := topPad + hd.Size.Y + gtx.Dp(gui.Sp12)

	// The scrolling body.
	p.viewH = max(0, size.Y-top)
	p.stepScroll(gtx)
	sg := gtx
	sg.Constraints = layout.Exact(image.Pt(size.X, p.viewH))
	p.insetTop = gtx.Dp(gui.Sp12)
	o = op.Offset(image.Pt(0, top)).Push(gtx.Ops)
	cl := clip.Rect(image.Rectangle{Max: sg.Constraints.Max}).Push(gtx.Ops)
	p.scroll.LayoutWidget(sg, th, func(gtx C) D {
		d := layout.Inset{Left: m.pad, Right: m.pad, Top: dp(gtx, p.insetTop), Bottom: m.pad + gui.Sp24}.Layout(gtx, func(gtx C) D {
			return p.content(gtx, c, m)
		})
		p.contentH = d.Size.Y
		return d
	})
	p.fade.Duration = gui.DurHover
	topFade(gtx, th, size.X, gtx.Dp(20), p.fade.Animate(gtx, b2f(p.scroll.List.Position.Offset > 0)))
	p.guard(gtx, sg.Constraints.Max)
	cl.Pop()
	o.Pop()
	area.Pop()

	p.afterScroll(gtx)
	return D{Size: size}
}

// visit notices a new visit (the page was not drawn last frame) and the
// section asked by the route.
func (p *Page) visit(gtx C, c *core.Ctx, section string) {
	f := c.Frame()
	if p.lastFrame == 0 || f > p.lastFrame+1 {
		p.applied = ""
		p.animOn = false
		p.pin, p.spy = -1, 0
		p.scroll.List.Position = layout.Position{}
		for _, k := range []*check{&p.text.check, &p.frames.check, &p.video.check, &p.glabs.check, &p.google.check, &p.ff.check, &p.stt.check, &p.telegram.check,
			&p.tts.check, &p.frames.custom.check, &p.video.custom.check, &p.dict.check} {
			k.reset()
		}
		p.profiles.stagger.Restart()
		p.refreshed = time.Time{}
	}
	p.lastFrame = f
	// What another studio window saved shows here too (config.Store.Refresh
	// reads the file and adopts it only when it changed): on every visit, then
	// every few seconds while the page is in use.
	if !c.Offline && c.E != nil && gtx.Now.Sub(p.refreshed) >= 4*time.Second {
		p.refreshed = gtx.Now
		c.Background(func() {
			if c.E.Config().Refresh() {
				c.Invalidate()
			}
		})
	}
	if section != "" && section != p.applied {
		p.want = section
	}
	p.applied = section
}

func (p *Page) header(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	tabs := make([]gui.Tab, len(sections))
	for i, s := range sections {
		tabs[i] = gui.Tab{Label: s.label, Icon: s.icon}
		if m.narrow {
			tabs[i].Label = s.short // the others collapse to their icons
		}
	}
	sel := p.spy
	if p.pin >= 0 {
		sel = p.pin
	}
	title := func(gtx C) D {
		if m.narrow {
			return gui.Text{Style: th.H2}.Layout(gtx, th, "Налаштування")
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.H1}.Widget(th, "Налаштування")),
			layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
			layout.Rigid(gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th,
				"Звідки студія бере текст, кадри, відео й голос. Ключі зберігаються лише на цьому комп'ютері.")),
		)
	}
	tabsW := func(gtx C) D { return p.tabs.Layout(gtx, th, sel, tabs) }
	// Tabs beside the title when both fit.
	need := gtx.Dp(8)
	for _, s := range sections {
		need += gtx.Dp(16+18+8+16) + th.TextWidth(gtx, th.Label.Weighted(gui.FontSemiBold), s.label)
	}
	W := gtx.Constraints.Max.X
	if W-need >= gtx.Dp(460) {
		return layout.Flex{Alignment: layout.End}.Layout(gtx,
			layout.Flexed(1, title),
			layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
			layout.Rigid(tabsW),
		)
	}
	return gui.VStack(gtx, gui.Sp16, title, func(gtx C) D {
		gtx.Constraints.Min.X = 0
		return tabsW(gtx)
	})
}

// content lays out every section and records where each starts.
func (p *Page) content(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	W := gtx.Constraints.Max.X
	y := 0
	place := func(w layout.Widget, id string) {
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(W, 0), Max: image.Pt(W, gtx.Constraints.Max.Y)}
		d, call := record(cg, w)
		if id != "" {
			p.marks[id] = y
		}
		o := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		y += d.Size.Y
	}
	gap := gtx.Dp(gui.Sp40)
	if m.narrow {
		gap = gtx.Dp(gui.Sp32)
	}

	// Підключення.
	base := y
	place(func(gtx C) D {
		return sectionHead(gtx, th, "Підключення",
			"Сервіси, що пишуть текст, малюють кадри, знімають відео й озвучують. Після змін натисни «Перевірити».", nil)
	}, "connections")
	y += gtx.Dp(gui.Sp20)
	place(func(gtx C) D { return p.assistCard(gtx, c, m) }, "assistant")
	y += gtx.Dp(gui.Sp24)
	card := func(id string, f func(gtx C, c *core.Ctx, m metrics) D) block {
		return block{id: id, w: func(gtx C) D { return f(gtx, c, m) }}
	}
	left := []block{card("text", p.textCard), card("frames", p.framesCard), card("video", p.videoCard), card("glabs", p.glabsCard),
		card("speed", p.speedCard), card("ffmpeg", p.ffCard)}
	right := []block{card("stock", p.stockCard), card("tts", p.ttsCard), card("google", p.googleCard), card("stt", p.sttCard), card("dictate", p.dictCard)}
	if m.cols < 2 {
		// One column: the services in the order a film needs them.
		left = []block{left[0], left[1], left[2], left[3], left[4], right[1], right[0], right[2], right[3], left[5], left[6]}
		right = nil
	}
	_ = base
	start := y
	place(func(gtx C) D { return p.columns(gtx, start, m, left, right) }, "")

	// Профілі.
	y += gap
	place(func(gtx C) D {
		return sectionHead(gtx, th, "Профілі",
			"Готові набори налаштувань для швидкого старту: стиль, формат, тривалість, голос і монтаж.", p.newProfileButton(th))
	}, "profiles")
	y += gtx.Dp(gui.Sp20)
	place(func(gtx C) D { return p.profilesGrid(gtx, c, m) }, "")

	// Автопілот · Сповіщення · Вигляд · Дані · Про студію.
	y += gap
	start = y
	left = []block{card("autopilot", p.autopilotCard), card("appearance", p.appearanceCard),
		card("about", p.aboutCard)}
	right = []block{card("telegram", p.telegramCard), card("data", p.dataCard)}
	place(func(gtx C) D { return p.columns(gtx, start, m, left, right) }, "")
	return D{Size: image.Pt(W, y)}
}

// block is one card of a column, with the mark it records.
type block struct {
	id string
	w  layout.Widget
}

// columns lays two stacks of cards side by side (one column when narrow,
// left and right interleaved) and records each card's y from base.
func (p *Page) columns(gtx C, base int, m metrics, left, right []block) D {
	W := gtx.Constraints.Max.X
	gap := gtx.Dp(gui.Sp24)
	if m.cols < 2 {
		var all []block
		for i := 0; i < max(len(left), len(right)); i++ {
			if i < len(left) {
				all = append(all, left[i])
			}
			if i < len(right) {
				all = append(all, right[i])
			}
		}
		// Keep the settings order of the old screen: the column order reads
		// naturally when interleaved.
		return p.stack(gtx, base, W, all)
	}
	cw := (W - gap) / 2
	ld := p.stack(gtx, base, cw, left)
	o := op.Offset(image.Pt(cw+gap, 0)).Push(gtx.Ops)
	rd := p.stack(gtx, base, cw, right)
	o.Pop()
	return D{Size: image.Pt(W, max(ld.Size.Y, rd.Size.Y))}
}

// stack lays cards one under another. A card that draws nothing takes no
// room and no gap.
func (p *Page) stack(gtx C, base, w int, bs []block) D {
	y := 0
	gap := gtx.Dp(gui.Sp24)
	first := true
	for _, b := range bs {
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Constraints.Max.Y)}
		d, call := record(cg, b.w)
		if d.Size.Y == 0 {
			delete(p.marks, b.id)
			continue
		}
		if !first {
			y += gap
		}
		first = false
		if b.id != "" {
			p.marks[b.id] = base + y
		}
		o := op.Offset(image.Pt(0, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		y += d.Size.Y
	}
	return D{Size: image.Pt(w, y)}
}

// ---- section scrolling -------------------------------------------------------------------

// target is the scroll offset that shows mark id at the top.
func (p *Page) target(gtx C, id string) (int, bool) {
	y, ok := p.marks[id]
	if !ok {
		return 0, false
	}
	t := p.insetTop + y - gtx.Dp(gui.Sp24) // clear of the top fade
	if y == 0 {
		t = 0 // the first section: the very top
	}
	return max(0, min(t, p.maxScroll())), true
}

func (p *Page) maxScroll() int { return max(0, p.contentH-p.viewH) }

// goTo scrolls smoothly to a section or card.
func (p *Page) goTo(gtx C, id string) {
	t, ok := p.target(gtx, id)
	if !ok {
		return
	}
	p.pin = tabOf(id)
	cur := p.scroll.List.Position.Offset
	if cur == t {
		p.pinOff = t
		return
	}
	d := time.Duration(280+min(320, abs(t-cur)*160/max(1, p.viewH))) * time.Millisecond
	p.anim.Duration, p.anim.Ease = d, gui.EaseInOutCubic
	p.anim.Set(float32(cur))
	p.anim.To(gtx, float32(t))
	p.animOn = true
	p.animSet = cur
	gtx.Execute(op.InvalidateCmd{})
}

// stepScroll moves the list along the running animation (a wheel scroll by
// the user cancels it).
func (p *Page) stepScroll(gtx C) {
	pos := &p.scroll.List.Position
	if !p.animOn {
		return
	}
	if pos.Offset != p.animSet || pos.First != 0 {
		p.animOn = false
		return
	}
	v := int(p.anim.Value(gtx) + 0.5)
	pos.First, pos.Offset = 0, v
	p.animSet = v
	if !p.anim.Running() {
		p.animOn = false
		p.pinOff = v
	}
}

// afterScroll jumps to the section the route asked for (once its position
// is known) and works out which tab is in view.
func (p *Page) afterScroll(gtx C) {
	pos := &p.scroll.List.Position
	if p.want != "" {
		if t, ok := p.target(gtx, p.want); ok {
			pos.First, pos.Offset = 0, t
			p.pin, p.pinOff = tabOf(p.want), t
			p.want = ""
			p.animOn = false
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	off := pos.Offset
	if p.pin >= 0 && !p.animOn && off != p.pinOff {
		p.pin = -1 // the user scrolled on
	}
	// The section whose top passed the upper third of the view.
	line := off - p.insetTop + min(p.viewH/3, gtx.Dp(160))
	spy, best := 0, -1
	for i, s := range sections {
		y, ok := p.marks[s.id]
		if !ok {
			continue
		}
		if y <= line && y > best {
			spy, best = i, y
		}
	}
	if off >= p.maxScroll()-2 && p.maxScroll() > 0 {
		// At the bottom: the lowest section that starts in view.
		for i, s := range sections {
			if y, ok := p.marks[s.id]; ok && y > best && y < off-p.insetTop+p.viewH-gtx.Dp(48) {
				spy, best = i, y
			}
		}
	}
	p.spy = spy
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
