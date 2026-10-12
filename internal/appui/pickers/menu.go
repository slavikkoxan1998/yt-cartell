package pickers

import (
	"image"

	"gioui.org/layout"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

// MenuItem is one entry of a Menu.
type MenuItem struct {
	Value    string // reported by Picked
	Label    string
	Caption  string // optional second line
	Icon     *gui.Icon
	Danger   bool // destructive (Видалити): the label in the error ink
	Disabled bool
	Checked  bool // a check on the right (the current profile …)
	Divider  bool // a hairline above the item
}

// Menu is a popover menu anchored to the widget that opens it (an icon
// button, a chip): the same card, motion, keys (↑ ↓ Enter Esc) and outside
// click as the picker lists. The menu owns the trigger's clicks.
//
//	if v, ok := s.more.Picked(); ok { … }
//	s.more.Layout(gtx, th, &s.moreBtn, items, func(gtx C) D {
//		return gui.Button{Kind: gui.Tinted, Icon: gui.IconMoreHorizontal, Tip: "Ще"}.Layout(gtx, th, &s.moreBtn)
//	})
type Menu struct {
	pick[string]
	// MinWidth of the popover (0 = 240 dp); it is never narrower than the trigger.
	MinWidth unit.Dp
	// Left aligns the popover's left edge with the trigger (default: right
	// edges aligned, for triggers at the right of a card).
	Left bool

	l    list
	opts []option
}

// Picked reports (and consumes) the value of the item the user chose.
func (m *Menu) Picked() (string, bool) { return m.Changed() }

// Open opens the menu (as if the trigger was pressed).
func (m *Menu) Open() { m.l.openNow() }

// IsOpen reports whether the menu shows.
func (m *Menu) IsOpen() bool { return m.l.open }

// Layout draws trigger and, while open, the menu under it.
func (m *Menu) Layout(gtx C, th *gui.Theme, btn *gui.Clickable, items []MenuItem, trigger layout.Widget) D {
	m.opts = m.opts[:0]
	two := false
	for _, it := range items {
		m.opts = append(m.opts, option{Value: it.Value, Label: it.Label, Caption: it.Caption, Icon: it.Icon,
			Disabled: it.Disabled, Danger: it.Danger, Divider: it.Divider, Check: it.Checked})
		two = two || it.Caption != ""
	}
	minW := m.MinWidth
	if minW == 0 {
		minW = 240
	}
	st := listStyle{Options: m.opts, Value: "\x00", Menu: true, NoCheck: true, TwoLine: two, MinWidth: minW}
	m.l.right = !m.Left
	if v, ok := m.l.update(gtx, btn, st); ok {
		m.set(gtx, v)
	}
	if btn.Clicked(gtx) {
		if m.l.open {
			m.l.hide(gtx, btn)
		} else {
			m.l.openAt(gtx, btn, st)
		}
	}
	d := gui.Anchor(gtx, th, m.l.key(), trigger)
	m.l.layout(gtx, th, d.Size, st)
	return d
}

// ---- Tile ---------------------------------------------------------------------------------

// TileStyle is the look of a custom settings tile that matches the picker
// fields (a toggle "Озвучка", a mode card): the same card, visual, label,
// value and caption. Trailing replaces the chevron (a gui.Toggle, a button).
type TileStyle struct {
	Label    string
	Value    string
	Caption  string
	Error    bool      // caption in the error ink
	Icon     *gui.Icon // visual (nil = none)
	Visual   layout.Widget
	Trailing layout.Widget
	Disabled bool
	Selected bool
	Tip      string
	// Flat draws a flat inset control for use inside a card or sheet (cards
	// never nest), like Look.Flat of the pickers.
	Flat bool
}

// tileFX keeps the hover/focus/press state of flat tiles by their button
// (UI goroutine only).
var tileFX = map[*gui.Clickable]*flatFX{}

// Tile draws a settings tile; btn (optional) makes the whole tile pressable.
func Tile(gtx C, th *gui.Theme, st TileStyle, btn *gui.Clickable) D {
	vis := st.Visual
	if vis == nil && st.Icon != nil {
		vis = visIcon(th, st.Icon, st.Flat)
	}
	t := tile{Label: st.Label, Value: st.Value, Caption: st.Caption, Error: st.Error, Visual: vis, Trail: st.Trailing}
	content := func(gtx C) D { return t.content(gtx, th, nil) }
	if st.Flat {
		fx := new(flatFX)
		if btn != nil {
			if tileFX[btn] == nil {
				tileFX[btn] = fx
			}
			fx = tileFX[btn]
		}
		return flatSurface(gtx, th, btn, fx, flatStyle{Open: st.Selected, Disabled: st.Disabled, Tip: st.Tip}, content)
	}
	return gui.Card{Click: btn, Padding: gui.Sp16, Radius: gui.RadiusInner, Selected: st.Selected, Disabled: st.Disabled, Tip: st.Tip}.Layout(gtx, th, content)
}

// IconVisual is the tile visual of an icon (a warm rounded square).
func IconVisual(th *gui.Theme, ic *gui.Icon) layout.Widget { return visIcon(th, ic, false) }

// FlatIconVisual is IconVisual for flat fields (a white square on the warm fill).
func FlatIconVisual(th *gui.Theme, ic *gui.Icon) layout.Widget { return visIcon(th, ic, true) }

// FrameVisual is the tile visual of a frame format of ratio w/h.
func FrameVisual(th *gui.Theme, ratio float32) layout.Widget { return visFrame(th, ratio, false) }

// FrameIcon strokes a frame of ratio w/h, its longest side box dp, centred
// in r (formats in chips and pills).
func FrameIcon(gtx C, th *gui.Theme, r image.Rectangle, ratio float32, box unit.Dp) {
	frameIcon(gtx, th, r, ratio, box, th.Pal.Text2)
}

// Close closes the menu (animated).
func (m *Menu) Close() { m.l.open = false }
