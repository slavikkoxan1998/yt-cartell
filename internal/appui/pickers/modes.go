package pickers

import (
	"image"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// ModeInfo is one option of the «Звук» and «Що в кадрі» choosers.
type ModeInfo struct {
	Value   string
	Label   string
	Caption string // one line: what the option means
	Icon    *gui.Icon
}

// SoundModes are the sound modes (studio.Input.AudioMode) in display order.
var SoundModes = []ModeInfo{
	{studio.AudioVoiceover, "Озвучка диктора", "диктор читає текст поверх кадрів", gui.IconMic},
	{studio.AudioInVideo, "Голос у відео", "герої говорять прямо в кадрі", gui.IconMessageCircle},
	{studio.AudioSilent, "Без голосу", "лише музика й звуки сцен", gui.IconMusic},
}

// ContentModes are the content modes (studio.Input.Content) in display order.
var ContentModes = []ModeInfo{
	{studio.ContentCharacters, "Персонажі", "герої, що повторюються в сценах", gui.IconUsers},
	{studio.ContentNoPeople, "Без людей", "місця, предмети, природа", gui.IconMountain},
	{studio.ContentMotion, "Моушн-графіка", "інфографіка й анімований текст", gui.IconShapes},
	{studio.ContentMixed, "Змішано", "герої, b-roll і графіка", gui.IconLayers},
}

// ModeByValue finds an option of list ("" or unknown → the first).
func ModeByValue(list []ModeInfo, v string) ModeInfo {
	for _, m := range list {
		if m.Value == v {
			return m
		}
	}
	return list[0]
}

// SpeechNote is the warning for «Голос у відео» when the video model in
// Settings makes no sound ("" = it speaks).
func SpeechNote(c *core.Ctx) (note string, hard bool) {
	if c == nil || c.E == nil {
		return "", false
	}
	switch studio.VideoSpeech(c.E.Config().Get()) {
	case studio.SpeechNo:
		return "відеомодель у налаштуваннях без звуку", true
	case studio.SpeechMaybe:
		return "звук залежить від відеомоделі — спробуй одну сцену", false
	}
	return "", false
}

// ---- Sound ----------------------------------------------------------------------------------

// Sound is the «Звук» field: a narrator's voice-over, the characters'
// own voices inside the clips, or no voice. The value is the effective mode
// (studio.Input.SoundMode); it warns when the video model makes no sound.
type Sound struct {
	pick[string]
	Look

	btn      gui.Clickable
	chev     gui.Anim
	fx       flatFX
	pop      panel
	rows     [3]gui.Clickable
	settings gui.Clickable
}

// Layout draws the field.
func (s *Sound) Layout(gtx C, c *core.Ctx, label, value string) D {
	th := c.Th
	value = s.shown(gtx, value)
	for i := range s.rows {
		if s.rows[i].Clicked(gtx) {
			s.set(gtx, SoundModes[i].Value)
			value = SoundModes[i].Value
			s.pop.hide(gtx, &s.btn)
		}
	}
	if s.settings.Clicked(gtx) {
		s.pop.hide(gtx, &s.btn)
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}
	s.keys(gtx)
	if s.btn.Clicked(gtx) && !s.Disabled {
		s.pop.toggle(gtx, &s.btn)
	}
	cur := ModeByValue(SoundModes, value)
	caption, warn := cur.Caption, false
	note, _ := SpeechNote(c)
	if cur.Value == studio.AudioInVideo && note != "" {
		caption, warn = note, true
	}
	if s.Note != "" {
		caption, warn = s.Note, false
	}
	t := tile{Label: label, Value: cur.Label, Caption: caption, Warn: warn, Visual: visIcon(th, cur.Icon, s.Flat), Open: s.pop.open, Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	s.popover(gtx, th, d, 400, func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Як звучить фільм")}
		for i, m := range SoundModes {
			ws = append(ws, modeRow(th, &s.rows[i], m, m.Value == cur.Value))
		}
		if note, hard := SpeechNote(c); note != "" {
			text := "«Голос у відео» потребує відеомоделі зі звуком: Veo 3.1 або Omni Flash (G-Labs, Google, RoyalTechno). Інші моделі можуть зняти кліпи без звуку."
			if hard {
				text = "Відеомодель у Налаштуваннях знімає без звуку — репліки героїв не прозвучать. Для «Голосу у відео» вибери Veo 3.1 або Omni Flash."
			}
			ws = append(ws, gui.Divider{}.Widget(th), warnCard(th, text, &s.settings))
		} else {
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th,
				"«Голос у відео»: студія пише репліки, а відеомодель сама озвучує героїв у кадрі. Субтитри — з реплік."))
		}
		return layout.UniformInset(gui.Sp16).Layout(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, ws...) })
	})
	return d
}

func (s *Sound) keys(gtx C) {
	for _, k := range s.pop.keyPresses(gtx) {
		if k == key.NameEscape || k == key.NameReturn || k == key.NameEnter {
			s.pop.hide(gtx, &s.btn)
		}
	}
	if s.pop.takeOutside() {
		s.pop.hide(gtx, &s.btn)
	}
}

func (s *Sound) popover(gtx C, th *gui.Theme, d D, minW int, body layout.Widget) {
	popover(gtx, th, &s.pop, d, minW, body)
}

// Open opens the panel (as if the field was pressed).
func (s *Sound) Open() { s.pop.openNow() }

// Close closes the panel.
func (s *Sound) Close() { s.pop.open = false }

// ---- Content ---------------------------------------------------------------------------------

// ContentValue is what the frames show and whether animated on-screen text
// is drawn (studio.Input.Content, Input.Overlays "" | "on" | "off").
type ContentValue struct {
	Content  string
	Overlays string
}

// TextOn is the effective on-screen text switch of v.
func (v ContentValue) TextOn() bool {
	return studio.Input{Content: v.Content, Overlays: v.Overlays}.TextOverlays()
}

// Content is the «Що в кадрі» field: characters, no people, motion
// graphics or mixed — plus the «Анімований текст у кадрі» switch. A new
// content resets the text switch to its default (on for motion graphics).
type Content struct {
	pick[ContentValue]
	Look

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  panel
	rows [4]gui.Clickable
	text gui.Toggle
}

// Layout draws the field.
func (s *Content) Layout(gtx C, c *core.Ctx, label string, value ContentValue) D {
	th := c.Th
	value = s.shown(gtx, value)
	for i := range s.rows {
		if s.rows[i].Clicked(gtx) {
			v := ContentValue{Content: ContentModes[i].Value}
			if v.Content == value.Content || (value.Content == "" && i == 0) {
				v.Overlays = value.Overlays // the same choice keeps the switch
			}
			s.set(gtx, v)
			value = v
		}
	}
	if on, ok := s.text.Changed(gtx); ok {
		v := value
		v.Overlays = "off"
		if on {
			v.Overlays = "on"
		}
		s.set(gtx, v)
		value = v
	}
	for _, k := range s.pop.keyPresses(gtx) {
		if k == key.NameEscape || k == key.NameReturn || k == key.NameEnter {
			s.pop.hide(gtx, &s.btn)
		}
	}
	if s.pop.takeOutside() {
		s.pop.hide(gtx, &s.btn)
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		s.pop.toggle(gtx, &s.btn)
	}
	cur := ModeByValue(ContentModes, value.Content)
	name := cur.Label
	if value.TextOn() && cur.Value != studio.ContentMotion {
		name += " · текст"
	}
	caption := cur.Caption
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: name, Caption: caption, Visual: visIcon(th, cur.Icon, s.Flat), Open: s.pop.open, Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	popover(gtx, th, &s.pop, d, 400, func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Що показуємо в кадрі")}
		for i, m := range ContentModes {
			ws = append(ws, modeRow(th, &s.rows[i], m, m.Value == cur.Value))
		}
		ws = append(ws,
			gui.Divider{}.Widget(th),
			func(gtx C) D {
				return s.text.Layout(gtx, th, value.TextOn(), "Анімований текст у кадрі")
			},
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
				"Заголовки, числа й підписи студія сама анімує поверх сцен під час монтажу. Для моушн-графіки — увімкнено."),
		)
		return layout.UniformInset(gui.Sp16).Layout(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, ws...) })
	})
	return d
}

// Open opens the panel (as if the field was pressed).
func (s *Content) Open() { s.pop.openNow() }

// Close closes the panel.
func (s *Content) Close() { s.pop.open = false }

// ---- shared -----------------------------------------------------------------------------------

// popover draws a panel's body under (or above) the field of size d.
func popover(gtx C, th *gui.Theme, p *panel, d D, minW int, body layout.Widget) {
	if !p.visible(gtx) {
		return
	}
	w := max(d.Size.X, gtx.Dp(unit.Dp(minW)))
	mg := gtx
	mg.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Dp(700))}
	m := op.Record(gtx.Ops)
	bd := body(mg)
	m.Stop()
	if r, ok := p.place(gtx, th, w, bd.Size.Y, bd.Size.Y); ok {
		p.draw(gtx, th, r, body)
	}
}

// modeRow is one option of a mode panel: an icon square, the name and its
// line, a check on the chosen one.
func modeRow(th *gui.Theme, click *gui.Clickable, m ModeInfo, on bool) layout.Widget {
	return func(gtx C) D {
		w := gtx.Constraints.Max.X
		h := gtx.Dp(58)
		size := image.Pt(w, h)
		click.Clickable.Layout(gtx, func(gtx C) D {
			defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			return D{Size: size}
		})
		if click.Hovered() || on {
			gui.FillRRect(gtx, image.Rect(0, 1, w, h-1), 12, th.Pal.Surface)
		}
		pad := gtx.Dp(10)
		ls := gtx.Dp(36)
		leading(gtx, th, option{Icon: m.Icon}, image.Rect(pad, (h-ls)/2, pad+ls, (h-ls)/2+ls), true)
		x := pad + ls + gtx.Dp(12)
		right := w - pad
		if on {
			ic := gtx.Dp(18)
			t := op.Offset(image.Pt(right-ic, (h-ic)/2)).Push(gtx.Ops)
			gui.IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.ActionText)
			t.Pop()
			right -= ic + gtx.Dp(8)
		}
		lg := gtx
		lg.Constraints = layout.Constraints{Max: image.Pt(max(0, right-x), h)}
		rec := op.Record(gtx.Ops)
		d := layout.Flex{Axis: layout.Vertical}.Layout(lg,
			layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium).Lined(1.3), MaxLines: 1}.Widget(th, m.Label)),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, m.Caption)),
		)
		call := rec.Stop()
		t := op.Offset(image.Pt(x, (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		t.Pop()
		return D{Size: size}
	}
}

// warnCard is a tinted warning with a link to the video settings.
func warnCard(th *gui.Theme, text string, link *gui.Clickable) layout.Widget {
	return func(gtx C) D {
		return gui.Card{Tint: true, Radius: gui.RadiusControl, Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return gui.VStack(gtx, gui.Sp8,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Top: 1}.Layout(gtx, gui.IconAlertTriangle.Widget(16, th.Pal.WarningInk))
						}),
						layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
						layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text}.Widget(th, text)),
					)
				},
				func(gtx C) D {
					return gui.Link{Text: "Відкрити налаштування відео", Trail: gui.IconChevronRight, Style: th.Label}.Layout(gtx, th, link)
				},
			)
		})
	}
}
