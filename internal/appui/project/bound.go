package project

import (
	"image"
	"image/color"
	"time"

	"gioui.org/gesture"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

// debounce is how long typing pauses before an edit is saved.
const debounce = 600 * time.Millisecond

// bound ties a gui.TextField to one model string. Edits are committed
// (c.Edit) after a 600 ms pause or when the field loses focus; the model
// never overwrites what the user is typing; a fresh commit is held until
// the snapshot reports it, so the text never flickers back. A field that
// stops being drawn with an edit pending (step or project switched) is
// flushed by its binder at the end of the frame, or by its timer when the
// workspace itself is no longer drawn.
type bound struct {
	gui.TextField

	dirty   bool
	val     string
	at      time.Time // frame clock of the last edit
	atWall  time.Time // wall clock of the last edit (the binder's timer)
	commit  func(string)
	hold    string
	holding bool
	holdAt  time.Time
	drawn   int64 // frame the field was last updated in
}

// binder flushes the pending edits of fields that were not drawn, and
// commits paused edits on a timer even when the workspace is no longer drawn
// (the user typed and went straight to Головна).
type binder struct {
	pending   map[*bound]bool
	c         *core.Ctx // set by the workspace every frame
	scheduled bool
}

func (b *binder) add(f *bound) {
	if b.pending == nil {
		b.pending = map[*bound]bool{}
	}
	b.pending[f] = true
	b.schedule()
}

// schedule arms the timer that commits paused edits (UI goroutine).
func (b *binder) schedule() {
	if b.scheduled || b.c == nil {
		return
	}
	b.scheduled = true
	c := b.c
	c.Background(func() {
		time.Sleep(debounce + 100*time.Millisecond)
		c.Post(func() {
			b.scheduled = false
			b.flushDue(time.Now())
		})
	})
}

// flushDue commits the edits that paused for the debounce, drawn or not,
// and re-arms the timer for the ones still being typed.
func (b *binder) flushDue(now time.Time) {
	again := false
	for f := range b.pending {
		switch {
		case !f.dirty:
			delete(b.pending, f)
		case now.Sub(f.atWall) >= debounce:
			f.flush()
			delete(b.pending, f)
		default:
			again = true
		}
	}
	if again {
		b.schedule()
	}
}

// flushStale commits the pending edits of fields not drawn in frame.
func (b *binder) flushStale(frame int64) {
	for f := range b.pending {
		if !f.dirty {
			delete(b.pending, f)
			continue
		}
		if f.drawn < frame {
			f.flush()
			delete(b.pending, f)
		}
	}
}

// flushAll commits every pending edit (leaving the project).
func (b *binder) flushAll() {
	for f := range b.pending {
		f.flush()
		delete(b.pending, f)
	}
}

func (f *bound) flush() {
	if !f.dirty {
		return
	}
	f.dirty = false
	f.hold, f.holding, f.holdAt = f.val, true, time.Now()
	if f.commit != nil {
		f.commit(f.val)
	}
}

// update runs the binding for this frame (call it before the field's
// Layout): model is the snapshot value, commit saves a new value.
func (f *bound) update(gtx C, b *binder, frame int64, model string, commit func(string)) {
	f.drawn, f.commit = frame, commit
	if v, ok := f.Changed(gtx); ok {
		f.dirty, f.val, f.at, f.atWall = true, v, gtx.Now, time.Now()
		b.add(f)
	}
	if f.dirty {
		if !f.Focused(gtx) || gtx.Now.Sub(f.at) >= debounce {
			f.dirty = false
			f.hold, f.holding, f.holdAt = f.val, true, gtx.Now
			commit(f.val)
		} else {
			gtx.Execute(op.InvalidateCmd{At: f.at.Add(debounce)})
		}
	}
	if f.holding && (model == f.hold || gtx.Now.Sub(f.holdAt) > time.Second) {
		f.holding = false
	}
	if !f.dirty && !f.holding {
		f.Sync(gtx, model)
	}
}

// text is the field's current text (what the user sees).
func (f *bound) text() string { return f.Editor.Text() }

// ---- inline heading editor ---------------------------------------------------------------

// inline draws a bound field as plain heading text that turns into a soft
// well while hovered and a focused field while edited (the project title).
type inline struct {
	bound
	hover gesture.Hover
	focA  gui.Anim
	hovA  gui.Anim
}

// lines is how many lines the idle label may wrap to (it ends with "…" after).
func (t *inline) layout(gtx C, th *gui.Theme, st gui.TextStyle, placeholder string, lines int) D {
	ed := &t.Editor
	ed.SingleLine, ed.Submit = true, true
	if t.Submitted(gtx) {
		gtx.Execute(key.FocusCmd{Tag: nil}) // Enter finishes editing
	}
	focused := t.Focused(gtx)
	hovered := t.hover.Update(gtx.Source)
	t.focA.Duration, t.hovA.Duration = gui.DurHover, gui.DurHover
	fa := t.focA.Animate(gtx, b2f(focused))
	ha := t.hovA.Animate(gtx, b2f(hovered && !focused))
	padX, padY := gtx.Dp(10), gtx.Dp(4)
	w := gtx.Constraints.Max.X
	eg := gtx
	eg.Constraints = layout.Constraints{Min: image.Pt(w-2*padX, 0), Max: image.Pt(w-2*padX, gtx.Dp(80))}
	m := op.Record(gtx.Ops)
	ed.LineHeightScale = st.LineHeight
	// While not edited the text shows as a label that ends with "…" when
	// too long; the editor stays underneath (transparent) to take the click.
	idle := !focused && ed.Len() > 0
	ts := th.Pal.Text
	if idle {
		ts = color.NRGBA{}
	}
	d := ed.Layout(eg, th.Shaper, st.Font, st.Size, colorMacro(gtx.Ops, ts), colorMacro(gtx.Ops, th.Pal.Selection))
	// The label (wrapping to lines) sets the height while editing too, so
	// focusing the title never makes the header jump.
	lg := eg
	lg.Constraints.Min = image.Point{}
	lm := op.Record(gtx.Ops)
	ld := gui.Text{Style: st, MaxLines: max(1, lines)}.Layout(lg, th, ed.Text())
	label := lm.Stop()
	if idle {
		label.Add(gtx.Ops)
	}
	call := m.Stop()
	var hint op.CallOp
	if ed.Len() == 0 && placeholder != "" {
		m := op.Record(gtx.Ops)
		gui.Text{Style: st, Color: th.Pal.Text3, MaxLines: 1}.Layout(eg, th, placeholder)
		hint = m.Stop()
	}
	h := max(d.Size.Y, ld.Size.Y) + 2*padY
	r := image.Rect(-padX, 0, w-padX, h)
	// The well: hover tint, then a card with an action border while editing.
	if ha > 0.01 || fa > 0.01 {
		bg := gui.LerpColor(gui.WithAlpha(th.Pal.Surface, ha), th.Pal.Card, fa)
		gui.FillRRect(gtx, r, gui.RadiusControl, bg)
		if fa > 0.01 {
			gui.Border(gtx, r, gui.RadiusControl, 1.5, gui.WithAlpha(th.Pal.Action, fa))
		}
	}
	area := clip.Rect(r).Push(gtx.Ops)
	t.hover.Add(gtx.Ops)
	area.Pop()
	o := op.Offset(image.Pt(0, padY)).Push(gtx.Ops)
	hint.Add(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// colorMacro records a ColorOp for widget.Editor materials.
func colorMacro(ops *op.Ops, c color.NRGBA) op.CallOp {
	m := op.Record(ops)
	paint.ColorOp{Color: c}.Add(ops)
	return m.Stop()
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
