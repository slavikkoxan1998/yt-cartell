package gui

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
)

// Force pins interaction looks for previews and screenshots (cmd/guikit).
// Leave it zero in the app.
type Force uint8

const (
	ForceHover Force = 1 << iota
	ForcePress
	ForceFocus
)

// Clickable is the state of anything pressable (buttons, cards, tiles, chips,
// tabs …). It embeds widget.Clickable and adds the animation state of
// hover, press and focus. Declare one per widget in your screen state:
//
//	var s struct{ next gui.Clickable }
//	if s.next.Clicked(gtx) { … }
//	gui.Button{Text: "Далі"}.Layout(gtx, th, &s.next)
//
// Clicked works before or after Layout in the same frame.
type Clickable struct {
	widget.Clickable

	hover, focus Anim
	press        Spring
	pending      int       // clicks drained by Layout, not yet read
	frame        time.Time // gtx.Now of the frame pending belongs to
}

// Clicked reports (and consumes) one click.
func (c *Clickable) Clicked(gtx C) bool {
	if c.pending > 0 {
		c.pending--
		return true
	}
	return c.Clickable.Clicked(gtx)
}

// interaction is a widget's resolved interaction state for one frame.
type interaction struct {
	hovered, pressed, focused bool
	hover, focus              float32 // animated 0…1
	press                     float32 // animated press amount 0…1
	scale                     float32 // press scale (1 at rest)
}

// update drains pending input (keeping clicks for Clicked), then resolves the
// hover/press/focus flags and their animations. pressScale is the scale at
// full press (0.97 for buttons, 1 for no scaling).
func (c *Clickable) update(gtx C, f Force, pressScale float32) interaction {
	// Unread clicks live for one frame. A widget laid out twice in a frame
	// (row equalising, Stretch) must not drop the clicks of the first pass.
	if !gtx.Now.Equal(c.frame) {
		c.pending, c.frame = 0, gtx.Now
	}
	for {
		_, ok := c.Clickable.Update(gtx)
		if !ok {
			break
		}
		c.pending++
	}
	if c.pending > 0 {
		gtx.Execute(op.InvalidateCmd{}) // let a caller that reads after Layout see it
	}
	en := gtx.Enabled()
	st := interaction{
		hovered: f&ForceHover != 0 || (en && c.Clickable.Hovered()),
		pressed: f&ForcePress != 0 || (en && c.Clickable.Pressed()),
		focused: f&ForceFocus != 0 || (en && gtx.Focused(&c.Clickable)),
	}
	c.hover.Duration, c.focus.Duration = DurHover, DurHover
	st.hover = c.hover.Animate(gtx, b2f(st.hovered || st.pressed))
	st.focus = c.focus.Animate(gtx, b2f(st.focused))
	target := float32(1)
	if st.pressed {
		target = pressScale
	}
	st.scale = c.press.Animate(gtx, target)
	if pressScale < 1 {
		st.press = clamp01((1 - st.scale) / (1 - pressScale))
	} else if st.pressed {
		st.press = 1
	}
	return st
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// hitArea registers c's input over a size-sized rectangle with the hand
// cursor. Visuals are painted separately (after it), so focus rings and press
// scaling are never clipped by the input area.
func (c *Clickable) hitArea(gtx C, size image.Point) {
	if !gtx.Enabled() {
		cursorArea(gtx, size, pointer.CursorDefault)
		return
	}
	c.Clickable.Layout(gtx, func(gtx C) D {
		cursorArea(gtx, size, pointer.CursorPointer)
		return D{Size: size}
	})
}

func cursorArea(gtx C, size image.Point, cur pointer.Cursor) {
	defer clip.Rect(rectWH(size.X, size.Y)).Push(gtx.Ops).Pop()
	cur.Add(gtx.Ops)
}

// opacity pushes an opacity layer when a < 1 and returns its Pop.
func opacity(gtx C, a float32) func() {
	if a >= 0.999 {
		return func() {}
	}
	return paint.PushOpacity(gtx.Ops, max(0, a)).Pop
}

// disabledAlpha is the opacity of disabled controls.
const disabledAlpha = 0.45

// clipRect pushes a rectangular clip of size sz at the origin.
func clipRect(gtx C, sz image.Point) clip.Stack {
	return clip.Rect(rectWH(sz.X, sz.Y)).Push(gtx.Ops)
}

// evtag registers tag for pointer/key events over the current clip area.
func evtag(gtx C, tag event.Tag) { event.Op(gtx.Ops, tag) }
