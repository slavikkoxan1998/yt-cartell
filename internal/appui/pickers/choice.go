package pickers

import (
	"image"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// ---- Choice -----------------------------------------------------------------------------

// Choice picks one of items (value = studio.Choice.ID).
type Choice struct {
	pick[string]
	Look
	// None, when set, offers "" first with this label ("Авто — за історією",
	// "Нейтральний оповідач").
	None string
	// Icon is the field's visual (nil = a sliders icon).
	Icon *gui.Icon

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  list
	opts []option
}

// Layout draws the field.
func (s *Choice) Layout(gtx core.C, c *core.Ctx, label string, items []studio.Choice, value string) core.D {
	th := c.Th
	value = s.shown(gtx, value)
	s.opts = s.opts[:0]
	if s.None != "" {
		s.opts = append(s.opts, option{Value: "", Label: s.None})
	}
	for _, it := range items {
		s.opts = append(s.opts, option{Value: it.ID, Label: it.Label, Search: it.ID})
	}
	shown := value
	known := false
	for _, o := range s.opts {
		if o.Value == value {
			shown, known = o.Label, true
		}
	}
	if !known && value == "" {
		shown = "Не вибрано"
	}
	if !known && value != "" && len(s.opts) > 0 {
		// A free-text value (an older project): offer it as it is.
		s.opts = append(s.opts, option{Value: value, Label: value})
	}
	st := listStyle{Options: s.opts, Value: value, Hint: "Пошук", MinWidth: 280}
	if v, ok := s.pop.update(gtx, &s.btn, st); ok {
		s.set(gtx, v)
		value, shown = v, labelOf(s.opts, v)
		st.Value = v
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		if s.pop.open {
			s.pop.hide(gtx, &s.btn)
		} else {
			s.pop.openAt(gtx, &s.btn, st)
		}
	}
	icon := s.Icon
	if icon == nil {
		icon = gui.IconSlidersHorizontal
	}
	t := tile{Label: label, Value: shown, Caption: s.Note, Visual: visIcon(th, icon, s.Flat), Open: s.pop.open, Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	return fieldWithPopover(gtx, th, t, &s.btn, &s.chev, &s.fx, func(gtx C, field image.Point) {
		s.pop.layout(gtx, th, field, st)
	})
}

func labelOf(opts []option, v string) string {
	for _, o := range opts {
		if o.Value == v {
			return o.Label
		}
	}
	return v
}

// fieldWithPopover draws the tile and, anchored to it, its popover.
func fieldWithPopover(gtx C, th *gui.Theme, t tile, btn *gui.Clickable, chev *gui.Anim, fx *flatFX, pop func(gtx C, field image.Point)) D {
	d := t.layout(gtx, th, btn, chev, fx)
	pop(gtx, d.Size)
	return d
}

// ---- Aspect -----------------------------------------------------------------------------

// AspectInfo describes a frame format.
type AspectInfo struct {
	Value   string  // "16:9"
	Ratio   float32 // width / height
	Label   string  // "Горизонтальне"
	Caption string  // "YouTube"
}

// Aspects are the frame formats the video models render (the engine keeps
// only these on a project).
var Aspects = []AspectInfo{
	{"16:9", 16.0 / 9, "Горизонтальне", "YouTube"},
	{"9:16", 9.0 / 16, "Вертикальне", "Shorts / Reels / TikTok"},
}

// AspectRatio is the width/height of a format ("16:9" → 1.78; unknown → 16/9).
func AspectRatio(v string) float32 {
	for _, a := range Aspects {
		if a.Value == v {
			return a.Ratio
		}
	}
	return 16.0 / 9
}

// AspectLabel is "16:9 · Горизонтальне" (the value itself when unknown).
func AspectLabel(v string) string {
	for _, a := range Aspects {
		if a.Value == v {
			return a.Value + " · " + a.Label
		}
	}
	return v
}

// Aspect picks the frame format ("16:9" | "9:16").
type Aspect struct {
	pick[string]
	Look

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  list
	opts []option
}

// Layout draws the field.
func (s *Aspect) Layout(gtx core.C, c *core.Ctx, label, value string) core.D {
	th := c.Th
	value = s.shown(gtx, value)
	s.opts = s.opts[:0]
	cur := AspectInfo{Value: value, Ratio: AspectRatio(value), Label: value}
	for _, a := range Aspects {
		s.opts = append(s.opts, option{Value: a.Value, Label: a.Value + " · " + a.Label, Caption: a.Caption, Frame: a.Ratio})
		if a.Value == value {
			cur = a
		}
	}
	st := listStyle{Options: s.opts, Value: value, TwoLine: true, Lead: true, MinWidth: 300}
	if v, ok := s.pop.update(gtx, &s.btn, st); ok {
		s.set(gtx, v)
		value = v
		st.Value = v
		for _, a := range Aspects {
			if a.Value == v {
				cur = a
			}
		}
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		if s.pop.open {
			s.pop.hide(gtx, &s.btn)
		} else {
			s.pop.openAt(gtx, &s.btn, st)
		}
	}
	caption := cur.Caption
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: AspectLabel(cur.Value), Caption: caption, Visual: visFrame(th, cur.Ratio, s.Flat), Flat: s.Flat,
		Open: s.pop.open, Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	return fieldWithPopover(gtx, th, t, &s.btn, &s.chev, &s.fx, func(gtx C, field image.Point) {
		s.pop.layout(gtx, th, field, st)
	})
}

// ---- ImageModel ---------------------------------------------------------------------------

// ImageModelInfo describes a frame model.
type ImageModelInfo struct {
	Value   string // NARWHAL | GEM_PIX_2 | GEM_PIX
	Label   string
	Caption string
	Icon    *gui.Icon
}

// ImageModels are the frame models in display order.
var ImageModels = []ImageModelInfo{
	{"NARWHAL", "Nano Banana 2", "Рекомендовано: гарна якість і швидкість", gui.IconSparkles},
	{"GEM_PIX_2", "Nano Banana Pro", "Найкраща якість і схожість героїв, повільніша", gui.IconCrown},
	{"GEM_PIX", "Nano Banana 2 Lite", "Найшвидша й найдешевша, простіші кадри", gui.IconZap},
}

// ImageModelLabel is the name of a frame model ("" for unknown).
func ImageModelLabel(v string) string {
	for _, m := range ImageModels {
		if m.Value == v {
			return m.Label
		}
	}
	return ""
}

// ImageModel picks the frame model (NARWHAL | GEM_PIX_2 | GEM_PIX).
type ImageModel struct {
	pick[string]
	Look

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  list
	opts []option
}

// Layout draws the field.
func (s *ImageModel) Layout(gtx core.C, c *core.Ctx, label, value string) core.D {
	th := c.Th
	value = s.shown(gtx, value)
	s.opts = s.opts[:0]
	cur := ImageModelInfo{Value: value, Label: value, Icon: gui.IconImage}
	for _, m := range ImageModels {
		s.opts = append(s.opts, option{Value: m.Value, Label: m.Label, Caption: m.Caption, Icon: m.Icon})
		if m.Value == value {
			cur = m
		}
	}
	st := listStyle{Options: s.opts, Value: value, TwoLine: true, Lead: true, MinWidth: 340}
	if v, ok := s.pop.update(gtx, &s.btn, st); ok {
		s.set(gtx, v)
		value = v
		st.Value = v
		for _, m := range ImageModels {
			if m.Value == v {
				cur = m
			}
		}
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		if s.pop.open {
			s.pop.hide(gtx, &s.btn)
		} else {
			s.pop.openAt(gtx, &s.btn, st)
		}
	}
	caption := cur.Caption
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: cur.Label, Caption: caption, Visual: visIcon(th, cur.Icon, s.Flat), Flat: s.Flat,
		Open: s.pop.open, Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	return fieldWithPopover(gtx, th, t, &s.btn, &s.chev, &s.fx, func(gtx C, field image.Point) {
		s.pop.layout(gtx, th, field, st)
	})
}

// Open opens the list (as if the field was pressed).
func (s *Choice) Open() { s.pop.openNow() }

// Open opens the list (as if the field was pressed).
func (s *Aspect) Open() { s.pop.openNow() }

// Open opens the list (as if the field was pressed).
func (s *ImageModel) Open() { s.pop.openNow() }

// Close closes the list.
func (s *Choice) Close() { s.pop.open = false }

// Close closes the list.
func (s *Aspect) Close() { s.pop.open = false }

// Close closes the list.
func (s *ImageModel) Close() { s.pop.open = false }
