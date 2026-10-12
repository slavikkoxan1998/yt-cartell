package pickers

import (
	"image"
	"strconv"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

// DurationPresets are the quick film lengths (seconds).
var DurationPresets = []int{15, 30, 60, 180, 300, 600, 1200, 1800}

// Duration limits (the engine clamps to the same range).
const (
	MinSeconds = 8
	MaxSeconds = 3600
)

// Duration picks a target length in seconds.
type Duration struct {
	pick[int]
	Look

	btn   gui.Clickable
	chev  gui.Anim
	fx    flatFX
	pop   panel
	chips [8]gui.Clickable
	mins  gui.TextField
	secs  gui.TextField
}

// Layout draws the field.
func (s *Duration) Layout(gtx core.C, c *core.Ctx, label string, seconds int) core.D {
	th := c.Th
	seconds = s.shown(gtx, seconds)
	for i, sec := range DurationPresets {
		if s.chips[i].Clicked(gtx) {
			s.set(gtx, sec)
			seconds = sec
			s.pop.hide(gtx, &s.btn)
		}
	}
	for _, k := range s.pop.keyPresses(gtx, &s.mins.Editor, &s.secs.Editor) {
		if k == key.NameEscape || k == key.NameReturn || k == key.NameEnter {
			s.pop.hide(gtx, &s.btn)
		}
	}
	if s.pop.takeOutside() {
		s.pop.hide(gtx, &s.btn)
	}
	s.mins.Editor.Filter, s.secs.Editor.Filter = "0123456789", "0123456789"
	s.mins.Editor.MaxLen, s.secs.Editor.MaxLen = 2, 3
	if v, ok := s.mins.Changed(gtx); ok && v != "" { // an empty field is being retyped
		m, _ := strconv.Atoi(v)
		t := clampSeconds(m*60 + seconds%60)
		s.set(gtx, t)
		seconds = t
	}
	if v, ok := s.secs.Changed(gtx); ok && v != "" {
		sec, _ := strconv.Atoi(v)
		t := clampSeconds((seconds/60)*60 + sec)
		s.set(gtx, t)
		seconds = t
	}
	s.mins.Sync(gtx, strconv.Itoa(seconds/60))
	s.secs.Sync(gtx, strconv.Itoa(seconds%60))
	if s.btn.Clicked(gtx) && !s.Disabled {
		s.pop.toggle(gtx, &s.btn)
	}
	caption := ""
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: core.Duration(seconds), Caption: caption, Visual: visIcon(th, gui.IconTimer, s.Flat), Flat: s.Flat,
		Open: s.pop.open, Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	if s.pop.visible(gtx) {
		w := max(d.Size.X, gtx.Dp(360))
		body := func(gtx C) D { return s.body(gtx, th, seconds) }
		// Measure the body at the popover width, then place it.
		mg := gtx
		mg.Constraints = layout.Constraints{Min: image.Pt(w, 0), Max: image.Pt(w, gtx.Dp(600))}
		m := op.Record(gtx.Ops)
		bd := body(mg)
		m.Stop()
		if r, ok := s.pop.place(gtx, th, w, bd.Size.Y, bd.Size.Y); ok {
			s.pop.draw(gtx, th, r, body)
		}
	}
	return d
}

func clampSeconds(t int) int { return max(MinSeconds, min(MaxSeconds, t)) }

// PresetLabel is the chip text of a preset length ("15 с", "1 хв").
func PresetLabel(sec int) string { return core.Duration(sec) }

func (s *Duration) body(gtx C, th *gui.Theme, seconds int) D {
	return layout.UniformInset(gui.Sp16).Layout(gtx, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Скільки триватиме відео"),
			func(gtx C) D {
				ws := make([]layout.Widget, len(DurationPresets))
				for i, sec := range DurationPresets {
					i, sec := i, sec
					ws[i] = func(gtx C) D {
						return gui.Chip{Text: PresetLabel(sec), Selected: sec == seconds}.Layout(gtx, th, &s.chips[i])
					}
				}
				return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
			},
			gui.Divider{}.Widget(th),
			func(gtx C) D {
				field := func(tf *gui.TextField) layout.Widget {
					return func(gtx C) D {
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(72), gtx.Dp(72)
						return tf.Layout(gtx, th, gui.FieldStyle{Submit: true})
					}
				}
				unit := func(s string) layout.Widget {
					return gui.Text{Style: th.Body, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, s)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Body, MaxLines: 1}.Widget(th, "Свій час")),
					layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
					layout.Rigid(field(&s.mins)),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(unit("хв")),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Rigid(field(&s.secs)),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(unit("с")),
				)
			},
			gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Від 8 секунд до 60 хвилин. Одна сцена — це один кліп."),
		)
	})
}

// Open opens the presets (as if the field was pressed).
func (s *Duration) Open() { s.pop.openNow() }

// Close closes the presets.
func (s *Duration) Close() { s.pop.open = false }
