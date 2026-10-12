package gui

import "gioui.org/layout"

// Widget adapters: value widgets as layout.Widget, for flex children and
// stacks — layout.Rigid(gui.ProgressBar{Value: v}.Widget(th)).

// Widget adapts the tile to a layout.Widget.
func (s StatTile) Widget(th *Theme) layout.Widget { return func(gtx C) D { return s.Layout(gtx, th) } }

// Widget adapts the ring to a layout.Widget.
func (r ProgressRing) Widget(th *Theme) layout.Widget {
	return func(gtx C) D { return r.Layout(gtx, th) }
}

// Widget adapts the bar to a layout.Widget.
func (p ProgressBar) Widget(th *Theme) layout.Widget {
	return func(gtx C) D { return p.Layout(gtx, th) }
}

// Widget adapts the thumb to a layout.Widget.
func (t Thumb) Widget(th *Theme) layout.Widget { return func(gtx C) D { return t.Layout(gtx, th) } }

// Widget adapts the avatar to a layout.Widget.
func (a Avatar) Widget(th *Theme) layout.Widget { return func(gtx C) D { return a.Layout(gtx, th) } }

// Widget adapts the skeleton to a layout.Widget.
func (s Skeleton) Widget(th *Theme) layout.Widget {
	return func(gtx C) D { return s.Layout(gtx, th) }
}

// Widget adapts the divider to a layout.Widget.
func (d Divider) Widget(th *Theme) layout.Widget { return func(gtx C) D { return d.Layout(gtx, th) } }

// Widget adapts the spinner to a layout.Widget.
func (s Spinner) Widget(th *Theme) layout.Widget { return func(gtx C) D { return s.Layout(gtx, th) } }
