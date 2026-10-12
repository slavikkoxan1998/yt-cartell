package pickers

import (
	"image"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// VideoModeValue is the video mode of a project or profile:
// Input.StoryboardMode ("" or studio.StoryboardGrid) and Input.ChainFrames.
type VideoModeValue struct {
	Storyboard string
	Chain      bool
}

// OmniFlash reports whether Settings use the video model the storyboard and
// chaining modes need (Omni Flash on G-Labs or RoyalTechno).
func OmniFlash(c *core.Ctx) bool {
	if c == nil || c.E == nil {
		return false
	}
	return studio.OmniFlash(c.E.Config().Get())
}

// VideoMode is the "Режим відео" field: regular or a 3×3 storyboard per
// scene, and frame chaining (both for Omni Flash; the field warns
// when Settings use another video model). Changed reports the whole value.
type VideoMode struct {
	pick[VideoModeValue]
	Look

	btn      gui.Clickable
	chev     gui.Anim
	fx       flatFX
	pop      panel
	rows     [2]gui.Clickable
	chain    gui.Toggle
	settings gui.Clickable
}

// Layout draws the field.
func (s *VideoMode) Layout(gtx C, c *core.Ctx, label string, value VideoModeValue) D {
	th := c.Th
	value = s.shown(gtx, value)
	omni := OmniFlash(c)
	for i := range s.rows {
		if s.rows[i].Clicked(gtx) {
			v := value
			v.Storyboard = ""
			if i == 1 {
				v.Storyboard = studio.StoryboardGrid
			}
			s.set(gtx, v)
			value = v
		}
	}
	if on, ok := s.chain.Changed(gtx); ok {
		v := value
		v.Chain = on
		s.set(gtx, v)
		value = v
	}
	if s.settings.Clicked(gtx) {
		s.pop.hide(gtx, &s.btn)
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
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
	name := "Звичайний"
	icon := gui.IconFilm
	if value.Storyboard == studio.StoryboardGrid {
		name, icon = "Розкадровка 3×3", gui.IconGrid3x3
	}
	if value.Chain {
		name += " · ланцюжок"
	}
	caption, warn := "кожна сцена — з одного кадру", false
	switch {
	case (value.Storyboard != "" || value.Chain) && !omni:
		caption, warn = "потрібна Omni Flash — зараз інша модель", true
	case value.Storyboard != "" || value.Chain:
		caption = "для Omni Flash"
	}
	if s.Note != "" {
		caption, warn = s.Note, false
	}
	t := tile{Label: label, Value: name, Caption: caption, Warn: warn, Visual: visIcon(th, icon, s.Flat), Open: s.pop.open, Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	if s.pop.visible(gtx) {
		w := max(d.Size.X, gtx.Dp(380))
		body := func(gtx C) D { return s.body(gtx, th, value, omni) }
		mg := gtx
		mg.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Dp(700))}
		m := op.Record(gtx.Ops)
		bd := body(mg)
		m.Stop()
		if r, ok := s.pop.place(gtx, th, w, bd.Size.Y, bd.Size.Y); ok {
			s.pop.draw(gtx, th, r, body)
		}
	}
	return d
}

func (s *VideoMode) body(gtx C, th *gui.Theme, value VideoModeValue, omni bool) D {
	opts := []struct {
		icon          *gui.Icon
		title, detail string
		on            bool
	}{
		{gui.IconFilm, "Звичайний", "Кожна сцена оживає з одного першого кадру.", value.Storyboard == ""},
		{gui.IconGrid3x3, "Розкадровка 3×3", "Аркуш із 9 кадрів на сцену — модель бачить увесь рух.", value.Storyboard == studio.StoryboardGrid},
	}
	row := func(i int) layout.Widget {
		o := opts[i]
		return func(gtx C) D {
			w := gtx.Constraints.Max.X
			h := gtx.Dp(60)
			size := image.Pt(w, h)
			c := &s.rows[i]
			c.Clickable.Layout(gtx, func(gtx C) D {
				defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
				pointer.CursorPointer.Add(gtx.Ops)
				return D{Size: size}
			})
			if c.Hovered() || o.on {
				gui.FillRRect(gtx, image.Rect(0, 1, w, h-1), 12, th.Pal.Surface)
			}
			pad := gtx.Dp(10)
			ls := gtx.Dp(36)
			lr := image.Rect(pad, (h-ls)/2, pad+ls, (h-ls)/2+ls)
			leading(gtx, th, option{Icon: o.icon}, lr, true)
			x := pad + ls + gtx.Dp(12)
			right := w - pad
			if o.on {
				ic := gtx.Dp(18)
				t := op.Offset(image.Pt(right-ic, (h-ic)/2)).Push(gtx.Ops)
				gui.IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.ActionText)
				t.Pop()
				right -= ic + gtx.Dp(8)
			}
			lg := gtx
			lg.Constraints = layout.Constraints{Max: image.Pt(max(0, right-x), h)}
			m := op.Record(gtx.Ops)
			d := layout.Flex{Axis: layout.Vertical}.Layout(lg,
				layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium).Lined(1.3), MaxLines: 1}.Widget(th, o.title)),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, o.detail)),
			)
			call := m.Stop()
			t := op.Offset(image.Pt(x, (h-d.Size.Y)/2)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			t.Pop()
			return D{Size: size}
		}
	}
	ws := []layout.Widget{
		gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Як знімати сцени"),
		row(0), row(1),
		gui.Divider{}.Widget(th),
		func(gtx C) D { return s.chain.Layout(gtx, th, value.Chain, "Ланцюжок кадрів") },
		gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
			"Останній кадр сцени передається наступній: рух плавніший, але кліпи знімаються по черзі."),
	}
	if omni {
		ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Обидва режими працюють із відеомоделлю Omni Flash — вона вже вибрана."))
	} else {
		ws = append(ws, func(gtx C) D {
			return gui.Card{Tint: true, Radius: gui.RadiusControl, Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return layout.Flex{Alignment: layout.Start}.Layout(gtx,
							layout.Rigid(func(gtx C) D {
								return layout.Inset{Top: 1}.Layout(gtx, gui.IconAlertTriangle.Widget(16, th.Pal.WarningInk))
							}),
							layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
							layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text}.Widget(th,
								"Розкадровка й ланцюжок працюють лише з відеомоделлю Omni Flash (G-Labs, Google або RoyalTechno). Зараз у Налаштуваннях інша — з нею сцени знімуться звичайно.")),
						)
					},
					func(gtx C) D {
						return gui.Link{Text: "Відкрити налаштування відео", Trail: gui.IconChevronRight, Style: th.Label}.Layout(gtx, th, &s.settings)
					},
				)
			})
		})
	}
	return layout.UniformInset(gui.Sp16).Layout(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp12, ws...) })
}

// Open opens the panel (as if the field was pressed).
func (s *VideoMode) Open() { s.pop.openNow() }

// Close closes the panel.
func (s *VideoMode) Close() { s.pop.open = false }
