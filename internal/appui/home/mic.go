package home

// Voice typing in the story box (package dictate): the mic button in the card
// head starts a dictation; while the owner speaks, the recognised words go in
// at the caret — final text for good, the interim words faded with a dotted
// underline until the service settles them (gui.FieldStyle.Pending). Under
// the box a strip shows it is live: a pulsing ring, the level, the time,
// «Говори — текст з'являється тут» and «Стоп».
//
// It stops on «Стоп», the mic button, Esc, after the silence set in Settings
// → Диктування, when the story box leaves the window (the session misses the
// heartbeat of these frames), when another window becomes the active one
// (core.Host's optional WindowFocused) or when the window is blocked. Errors
// stay under the box with the way to fix them; with no service set up, a
// click opens a small dialog that leads to Settings → Диктування. The UI
// goroutine never waits: resolving the setup and the session run in the
// background and come back through Post / Invalidate.

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/dictate"
	"ytcartell/internal/gui"
)

// SectionDictate is the Settings card of dictation (Route.Section).
const SectionDictate = "dictate"

type mic struct {
	btn   gui.Clickable // the mic in the card head
	stop  gui.Clickable // «Стоп» in the strip
	fix   gui.Clickable // the Settings link of a notice
	close gui.Clickable // × of a notice

	sess      *dictate.Session
	splice    dictate.Splice
	st        dictate.State
	pending   [2]int // interim runes in the story box
	resolving bool   // working out the setup (background)
	sawFocus  bool   // the window was focused during this dictation
	blurred   bool   // it stopped because the window lost the focus
	err       error  // the last dictation's error (the notice)

	sheet       gui.Dialog // "not set up yet"
	sheetText   string
	sheetGo     gui.Clickable
	sheetCancel gui.Clickable

	show   gui.Anim // the strip appearing
	level  gui.Anim // smoothed microphone level
	levels [7]float32
	tick   time.Time

	// demo pins a recording look without a microphone (screenshots).
	demo *dictate.State
}

// recording: a dictation runs (or a demo pretends to).
func (m *mic) recording() bool { return m.sess != nil || m.demo != nil }

// state is this frame's session snapshot.
func (m *mic) state() dictate.State {
	if m.demo != nil {
		return *m.demo
	}
	return m.st
}

// micUpdate runs every frame before the story box is laid out: clicks, Esc,
// the heartbeat, and the text the session recognised since the last frame.
func (p *Page) micUpdate(gtx C, c *core.Ctx) {
	h := &p.hero
	m := &h.mic
	if m.btn.Clicked(gtx) {
		if m.recording() {
			m.stopNow()
		} else {
			p.micStart(c)
		}
	}
	if m.stop.Clicked(gtx) {
		m.stopNow()
	}
	if m.close.Clicked(gtx) {
		m.err = nil
	}
	if m.fix.Clicked(gtx) {
		m.err = nil
		c.Navigate(core.Route{Page: core.PageSettings, Section: SectionDictate})
	}
	if m.sheetGo.Clicked(gtx) {
		m.sheet.Close()
		c.Navigate(core.Route{Page: core.PageSettings, Section: SectionDictate})
	}
	if m.sheetCancel.Clicked(gtx) || m.sheet.Dismissed() {
		m.sheet.Close()
	}
	if m.sess != nil {
		for {
			ev, ok := gtx.Event(
				key.Filter{Focus: &h.story.Editor, Name: key.NameEscape},
				key.Filter{Focus: &m.btn.Clickable, Name: key.NameEscape},
				key.Filter{Focus: &m.stop.Clickable, Name: key.NameEscape},
			)
			if !ok {
				break
			}
			if ke, ok := ev.(key.Event); ok && ke.State == key.Press {
				m.stopNow()
			}
		}
		if !gtx.Enabled() { // the page is disabled (e.g. covered by a dialog)
			m.sess.StopFor(dictate.StopGone)
		}
		// The owner switched to another window: the microphone goes off
		// (only after the window was seen focused during this dictation).
		if w, ok := c.Host.(interface{ WindowFocused() bool }); ok {
			if w.WindowFocused() {
				m.sawFocus = true
			} else if m.sawFocus && !m.blurred {
				m.blurred = true
				m.sess.StopFor(dictate.StopGone)
			}
		}
		m.sess.Heartbeat()
		m.st = m.sess.State()
		finals, interim, changed := m.sess.Take()
		if changed || len(finals) > 0 {
			m.apply(&h.story, m.splice.Update(h.story.Text(), caret(&h.story), finals, interim))
		}
		select {
		case <-m.sess.Done():
			p.micDone(c)
		default:
		}
		// The ring and the meter move while it records.
		next := 33 * time.Millisecond
		if gui.ReducedMotion() {
			next = 250 * time.Millisecond
		}
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(next)})
	}
	if m.splice.Active() && m.st.Phase != dictate.Done {
		a, b := m.splice.Interim()
		if rs := []rune(h.story.Text()); a < b && a < len(rs) && rs[a] == ' ' {
			a++ // the space before the words is not part of them
		}
		m.pending = [2]int{a, b}
	} else if m.demo == nil {
		m.pending = [2]int{}
	}
	m.sheet.Layout(gtx, c.Th, gui.DialogStyle{
		Title: "Диктування ще не налаштоване",
		Text:  m.sheetText,
		Icon:  gui.IconMic,
		Width: 460,
		Body:  func(gtx C) D { return micOptions(gtx, c.Th) },
		Actions: []layout.Widget{
			func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Text: "Не зараз"}.Layout(gtx, c.Th, &m.sheetCancel)
			},
			func(gtx C) D {
				return gui.Button{Text: "Відкрити налаштування", Trail: gui.IconArrowRight}.Layout(gtx, c.Th, &m.sheetGo)
			},
		},
	})
}

// micStart works out the setup in the background, then starts or explains.
func (p *Page) micStart(c *core.Ctx) {
	h := &p.hero
	m := &h.mic
	if m.resolving || m.sess != nil {
		return
	}
	if c.Offline {
		c.Toast(gui.ToneInfo, "Диктування недоступне в демо-режимі.")
		return
	}
	m.resolving, m.err = true, nil
	e := c.E
	c.Background(func() {
		plan := dictate.Resolve(e.Config().Get())
		ff := e.FF().FFmpeg
		c.Post(func() {
			m.resolving = false
			if !plan.Ready {
				m.sheetText = plan.Problem
				if plan.Provider == "" {
					m.sheetText = "Вибери, хто перетворюватиме мову на текст, — це хвилина в Налаштуваннях → Диктування."
				}
				m.sheet.Open()
				return
			}
			m.sess = dictate.Start(dictate.Options{Plan: plan, FFmpeg: ff, Notify: c.Invalidate})
			m.st = m.sess.State()
			start, end := h.story.Editor.Selection()
			m.apply(&h.story, m.splice.Begin(h.story.Text(), start, end))
			m.levels = [7]float32{}
			m.sawFocus, m.blurred = false, false
			h.focus = true // the caret shows where the words go
		})
	})
}

// stopNow ends the dictation: the microphone goes off at once, the words
// already spoken are still recognised (a second stop drops them).
func (m *mic) stopNow() {
	if m.demo != nil {
		m.demo = nil
		return
	}
	if m.sess != nil {
		m.sess.Stop()
	}
}

// micDone takes the last text of a finished session.
func (p *Page) micDone(c *core.Ctx) {
	h := &p.hero
	m := &h.mic
	st := m.sess.State()
	finals, interim, _ := m.sess.Take()
	edits := m.splice.Update(h.story.Text(), caret(&h.story), finals, interim)
	m.apply(&h.story, edits)
	m.apply(&h.story, m.splice.End(h.story.Text(), true))
	m.sess, m.st, m.pending = nil, st, [2]int{}
	switch st.Reason {
	case dictate.StopError:
		m.err = st.Err
	case dictate.StopSilence:
		if st.Heard {
			c.Toast(gui.ToneInfo, "Диктування зупинилось після паузи. Натисни мікрофон, щоб продовжити.")
		} else {
			c.Toast(gui.ToneWarning, "Мікрофон нічого не почув — перевір, чи він увімкнений і вибраний у Налаштуваннях → Диктування.")
		}
	case dictate.StopGone:
		if m.blurred {
			c.Toast(gui.ToneInfo, "Диктування зупинено: вікно студії більше не активне. Сказане вже в полі.")
		}
	case dictate.StopLimit:
		c.Toast(gui.ToneInfo, "Диктування зупинилось: найдовший запис — 15 хвилин. Натисни мікрофон, щоб продовжити.")
	}
}

// apply runs splice edits on the story box (each one undoable).
func (m *mic) apply(f *gui.TextField, edits []dictate.Edit) {
	for _, e := range edits {
		f.Editor.SetCaret(e.Start, e.End)
		f.Editor.Insert(e.Text)
		f.Editor.SetCaret(e.Caret, e.Caret)
	}
}

// caret is the story box's caret (-1 with a selection).
func caret(f *gui.TextField) int {
	a, b := f.Editor.Selection()
	if a != b {
		return -1
	}
	return a
}

// micButton is the mic in the card head: a round ghost button, tinted while
// it records.
func (h *hero) micButton(gtx C, th *gui.Theme) D {
	m := &h.mic
	b := gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconMic, Round: true, Tip: "Надиктувати", Busy: m.resolving}
	if m.recording() {
		b.Kind, b.Tip = gui.Tinted, "Зупинити диктування (Esc)"
	}
	return b.Layout(gtx, th, &m.btn)
}

// micAround lays the story box out with the dictation strip (or a notice)
// under it.
func (p *Page) micAround(gtx C, c *core.Ctx, box layout.Widget) D {
	m := &p.hero.mic
	on := m.recording() || m.err != nil
	m.show.Duration = gui.DurDialog
	a := m.show.Animate(gtx, b2f(on))
	if a <= 0.001 {
		return box(gtx)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(box),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			d, call := recordW(gtx, func(gtx C) D {
				return layout.Inset{Top: gui.Sp12}.Layout(gtx, func(gtx C) D {
					if m.err != nil && !m.recording() {
						return p.micNotice(gtx, c)
					}
					return p.micStrip(gtx, c)
				})
			})
			// Slide open: the height follows the animation.
			h := int(float32(d.Size.Y) * a)
			defer clip.Rect(image.Rect(0, 0, d.Size.X, h)).Push(gtx.Ops).Pop()
			gui.FadeUp(gtx, a, 6, func(gtx C) D { call.Add(gtx.Ops); return d })
			return D{Size: image.Pt(d.Size.X, h)}
		}),
	)
}

// micStrip is the live bar: ring, level, what to do, time, «Стоп».
func (p *Page) micStrip(gtx C, c *core.Ctx) D {
	th := c.Th
	m := &p.hero.mic
	st := m.state()
	m.levelTick(gtx, st)
	narrow := gtx.Constraints.Max.X < gtx.Dp(520)
	title, tone := "Говори — текст з'являється тут", th.Pal.Text
	switch {
	case m.sess == nil && m.demo == nil:
		title = "Готово"
	case st.Phase == dictate.Starting:
		title = "Вмикаємо мікрофон…"
	case st.Phase == dictate.Finishing:
		title = "Дописуємо останні слова…"
	case st.Mute:
		title, tone = "Мікрофон мовчить — перевір, чи він увімкнений", th.Pal.WarningInk
	}
	sub := strings.TrimSpace(st.Label)
	if st.Device != "" {
		sub = "Мікрофон: " + st.Device + " · " + sub
	}
	well := func(gtx C) D {
		ring := func(gtx C) D { return m.ring(gtx, th, st) }
		meter := func(gtx C) D { return m.meter(gtx, th) }
		texts := func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium), Color: tone, MaxLines: 2}.Widget(th, title)),
				layout.Rigid(func(gtx C) D {
					if sub == "" {
						return D{}
					}
					return gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, sub)
				}),
			)
		}
		clock := gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, core.Clock(st.Elapsed.Seconds()))
		stop := func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconSquare, Text: "Стоп",
				Tip: "Зупинити диктування (Esc)"}.Layout(gtx, th, &m.stop)
		}
		if narrow {
			// Phones: the controls on one row, the words under them.
			return gui.VStack(gtx, gui.Sp8,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(ring),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Rigid(meter),
						layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Max.X, 0)} }),
						layout.Rigid(clock),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Rigid(stop),
					)
				},
				texts,
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(ring),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(meter),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, texts),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(clock),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(stop),
		)
	}
	return wellBox(gtx, th, th.Pal.Surface, well)
}

// micNotice is the error of the last dictation with the way to fix it.
func (p *Page) micNotice(gtx C, c *core.Ctx) D {
	th := c.Th
	m := &p.hero.mic
	msg := core.ErrorText(m.err)
	settings := dictate.NeedsSettings(m.err) || dictate.KindOf(m.err) == dictate.KindPermission
	body := func(gtx C) D {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 2, Right: gui.Sp12}.Layout(gtx, gui.IconCircleAlert.Widget(20, th.Pal.ErrorInk))
			}),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Body, Color: th.Pal.Text}.Widget(th, msg)),
					layout.Rigid(func(gtx C) D {
						if !settings {
							return D{}
						}
						return layout.Inset{Top: gui.Sp8}.Layout(gtx, func(gtx C) D {
							return gui.Link{Text: "Налаштування диктування", Trail: gui.IconChevronRight}.Layout(gtx, th, &m.fix)
						})
					}),
				)
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: gui.Sp8}.Layout(gtx, func(gtx C) D {
					return gui.IconButton{Icon: gui.IconX, Size: 28, IconSize: 16, Tip: "Сховати"}.Layout(gtx, th, &m.close)
				})
			}),
		)
	}
	return wellBox(gtx, th, th.Pal.ErrorSoft, body)
}

// micOptions lists the ways to set dictation up (the dialog's body).
func micOptions(gtx C, th *gui.Theme) D {
	row := func(icon *gui.Icon, title, text string) layout.Widget {
		return func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return layout.Inset{Top: 2, Right: gui.Sp12}.Layout(gtx, icon.Widget(20, th.Pal.Text2))
				}),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium)}.Widget(th, title)),
						layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, text)),
					)
				}),
			)
		}
	}
	return wellBox(gtx, th, th.Pal.Surface, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			row(gui.IconZap, "Найшвидше — ElevenLabs", "Ключ той самий, що для озвучки ElevenLabs: текст з'являється, поки говориш."),
			row(gui.IconCpu, "Без інтернету — whisper.cpp", "Безкоштовно на цьому комп'ютері, якщо він встановлений (Налаштування → Розпізнавання мови)."),
			row(gui.IconPlug, "Або інший сервіс", "OpenAI, Deepgram, AssemblyAI, Google AI Studio чи будь-який OpenAI-сумісний."),
		)
	})
}

// ---- the live look ----------------------------------------------------------------------------

// levelTick shifts the meter's history about 15 times a second.
func (m *mic) levelTick(gtx C, st dictate.State) {
	m.level.Duration = 90 * time.Millisecond
	lv := m.level.Animate(gtx, st.Level)
	if m.demo != nil {
		m.levels = [7]float32{0.35, 0.7, 0.9, 0.6, 0.8, 0.45, 0.25}
		return
	}
	if gtx.Now.Sub(m.tick) >= 66*time.Millisecond {
		m.tick = gtx.Now
		copy(m.levels[:], m.levels[1:])
		m.levels[len(m.levels)-1] = lv
	}
}

// ring is the mic in a running-violet circle with a ring pulsing outwards
// (stronger while the owner speaks); a spinner while it starts or finishes.
func (m *mic) ring(gtx C, th *gui.Theme, st dictate.State) D {
	size := gtx.Dp(44)
	inner := float32(gtx.Dp(32))
	c := f32.Pt(float32(size)/2, float32(size)/2)
	p := &th.Pal
	if st.Phase == dictate.Starting || st.Phase == dictate.Finishing {
		o := op.Offset(image.Pt((size-gtx.Dp(28))/2, (size-gtx.Dp(28))/2)).Push(gtx.Ops)
		gui.Spinner{Size: 28, Color: p.Running}.Layout(gtx, th)
		o.Pop()
		return D{Size: image.Pt(size, size)}
	}
	if !gui.ReducedMotion() {
		const period = 1400 * time.Millisecond
		t := float32(gtx.Now.UnixNano()%int64(period)) / float32(period)
		strength := 0.35 + 0.65*min(1, st.Level*1.6)
		r := inner/2 + (float32(size)/2-inner/2)*t
		ring(gtx, c, r, float32(gtx.Dp(2)), gui.WithAlpha(p.Running, (1-t)*0.6*strength))
	} else {
		ring(gtx, c, float32(size)/2-float32(gtx.Dp(2)), float32(gtx.Dp(2)), gui.WithAlpha(p.Running, 0.35))
	}
	disc(gtx, c, inner/2, p.RunningSoft)
	o := op.Offset(image.Pt((size-gtx.Dp(18))/2, (size-gtx.Dp(18))/2)).Push(gtx.Ops)
	gui.IconMic.Layout(gtx, 18, p.RunningInk)
	o.Pop()
	return D{Size: image.Pt(size, size)}
}

// meter is seven round bars of the recent level.
func (m *mic) meter(gtx C, th *gui.Theme) D {
	bw, gap, hmax := gtx.Dp(3), gtx.Dp(3), gtx.Dp(24)
	w := len(m.levels)*bw + (len(m.levels)-1)*gap
	for i, l := range m.levels {
		h := max(bw, int(float32(hmax)*float32(math.Sqrt(float64(l)))))
		x := i * (bw + gap)
		y := (hmax - h) / 2
		col := th.Pal.Running
		if l < 0.05 {
			col = gui.WithAlpha(th.Pal.Running, 0.35)
		}
		gui.FillRRect(gtx, image.Rect(x, y, x+bw, y+h), 2, col)
	}
	return D{Size: image.Pt(w, hmax)}
}

// ---- small drawing helpers --------------------------------------------------------------------

// wellBox is a tinted well inside the card (16 dp corners, no border).
func wellBox(gtx C, th *gui.Theme, fill color.NRGBA, w layout.Widget) D {
	pad := gtx.Dp(gui.Sp12)
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(max(0, W-2*pad-gtx.Dp(4)), 0), Max: image.Pt(max(0, W-2*pad-gtx.Dp(4)), gtx.Constraints.Max.Y)}
	d, call := recordW(cg, w)
	size := image.Pt(W, d.Size.Y+2*pad)
	gui.FillRRect(gtx, image.Rectangle{Max: size}, gui.RadiusInner, fill)
	o := op.Offset(image.Pt(pad+gtx.Dp(4), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

func recordW(gtx C, w layout.Widget) (D, op.CallOp) {
	r := op.Record(gtx.Ops)
	d := w(gtx)
	return d, r.Stop()
}

func disc(gtx C, c f32.Point, r float32, col color.NRGBA) {
	b := image.Rect(int(c.X-r+0.5), int(c.Y-r+0.5), int(c.X+r+0.5), int(c.Y+r+0.5))
	defer clip.Ellipse(b).Push(gtx.Ops).Pop()
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

func ring(gtx C, c f32.Point, r, width float32, col color.NRGBA) {
	if col.A == 0 || r <= 0 {
		return
	}
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(c.X+r, c.Y))
	path.ArcTo(c, c, 2*math.Pi)
	path.Close()
	defer clip.Stroke{Path: path.End(), Width: width}.Op().Push(gtx.Ops).Pop()
	paint.ColorOp{Color: col}.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}
