package settings

import (
	"image"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
)

// driver runs frames of the page through a real input router.
type driver struct {
	t   *testing.T
	c   *core.Ctx
	h   *core.HeadlessHost
	p   *Page
	r   input.Router
	ops op.Ops
	n   int
	// section is the Route.Section passed to Layout.
	section string
}

func (d *driver) frame(before func(gtx C)) {
	d.ops.Reset()
	gtx := layout.Context{
		Ops:         &d.ops,
		Now:         time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(d.n) * 16 * time.Millisecond),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(1440, 900)),
		Source:      d.r.Source(),
	}
	d.n++
	core.Frame(d.c, d.h, func(gtx C) D {
		if before != nil {
			before(gtx)
		}
		return d.p.Layout(gtx, d.c, d.section)
	})(gtx)
	d.r.Frame(&d.ops)
}

func (d *driver) click(x, y float32) {
	pos := f32.Pt(x, y)
	d.r.Queue(
		pointer.Event{Kind: pointer.Move, Position: pos, Source: pointer.Mouse},
		pointer.Event{Kind: pointer.Press, Position: pos, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary},
		pointer.Event{Kind: pointer.Release, Position: pos, Source: pointer.Mouse},
	)
}

// labels are the window rectangles of the text drawn as label (Gio's semantic
// labels; text painted twice for its weight counts once), top to bottom.
func (d *driver) labels(label string) []image.Rectangle {
	var out []image.Rectangle
	seen := map[image.Rectangle]bool{}
	for _, n := range d.r.AppendSemantics(nil) {
		if n.Desc.Label == label && !seen[n.Desc.Bounds] {
			seen[n.Desc.Bounds] = true
			out = append(out, n.Desc.Bounds)
		}
	}
	return out
}

// near is the first label rectangle within dy px below (or on the line of)
// anchor and starting at or right of its left edge minus 40 px.
func near(rs []image.Rectangle, anchor image.Rectangle, dy int) (image.Rectangle, bool) {
	for _, r := range rs {
		if r.Min.Y >= anchor.Min.Y-12 && r.Min.Y <= anchor.Max.Y+dy && r.Min.X >= anchor.Min.X-40 {
			return r, true
		}
	}
	return image.Rectangle{}, false
}

// clickRect presses the text at the start of r (labels can be wider than their text).
func (d *driver) clickRect(r image.Rectangle) { d.click(float32(r.Min.X+6), float32(r.Min.Y+6)) }

// settle runs n frames, letting background saves finish in between.
func (d *driver) settle(n int) {
	for i := 0; i < n; i++ {
		d.frame(nil)
		d.h.Flush()
	}
}

// mustLabel is the first rectangle of label (the test fails without one).
func (d *driver) mustLabel(label string) image.Rectangle {
	d.t.Helper()
	rs := d.labels(label)
	if len(rs) == 0 {
		d.t.Fatalf("no %q on the page", label)
	}
	return rs[0]
}

// TestFieldCommitsOnBlur types into a field and presses elsewhere on the
// page: the value is saved once, when the field loses focus.
func TestFieldCommitsOnBlur(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New()}
	d.frame(nil)
	ed := &d.p.text.base.Editor
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: ed.Len()}, Text: "http://example.test/v1/"})
	d.frame(nil)
	h.Flush()
	if got := c.E.Config().Get().Text.BaseURL; got == "http://example.test/v1" {
		t.Fatal("saved while typing")
	}
	if !d.p.text.base.edited {
		t.Fatalf("edit not noticed; field text %q", ed.Text())
	}
	d.click(600, 20) // the page header: not a field
	d.frame(nil)
	d.frame(nil)
	h.Flush()
	if got := c.E.Config().Get().Text.BaseURL; got != "http://example.test/v1" {
		t.Fatalf("BaseURL = %q after blur", got)
	}
	if d.p.text.base.edited {
		t.Fatal("still marked as edited")
	}
}

// TestKeyFieldNeverShowsKey pastes a key: it is saved on Enter and the field
// empties (the stored key is only ever shown masked).
func TestKeyFieldNeverShowsKey(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New()}
	d.frame(nil)
	ed := &d.p.text.key.Editor
	if ed.Len() != 0 {
		t.Fatal("the key field must start empty")
	}
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.r.Queue(key.EditEvent{Range: key.Range{}, Text: "cg-new-demo-key-1234"})
	d.frame(nil)
	d.r.Queue(key.Event{Name: key.NameReturn, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Release})
	d.frame(nil)
	d.frame(nil)
	h.Flush()
	if got := c.E.Config().Get().Text.APIKey; got != "cg-new-demo-key-1234" {
		t.Fatalf("APIKey = %q", got)
	}
	if ed.Len() != 0 {
		t.Fatalf("the key stays in the field: %q", ed.Text())
	}
	_ = config.Mask
}

// TestSectionNavigation: a route section jumps there on entry; a tab click
// scrolls smoothly and the tabs follow the scroll position.
func TestSectionNavigation(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New(), section: "telegram"}
	for i := 0; i < 4; i++ {
		d.frame(nil)
	}
	pos := d.p.scroll.List.Position
	want, ok := d.p.target(layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}, "telegram")
	if !ok || pos.Offset != want || want == 0 {
		t.Fatalf("entry jump: offset %d, want %d (ok %v)", pos.Offset, want, ok)
	}
	if d.p.pin != tabOf("telegram") {
		t.Fatalf("pinned tab %d, want %d", d.p.pin, tabOf("telegram"))
	}
	// Back to the top with the first tab: an animation, not a jump.
	d.frame(func(gtx C) { d.p.goTo(gtx, "connections") })
	for i := 0; i < 12; i++ { // ~200 ms into the animation
		d.frame(nil)
	}
	if mid := d.p.scroll.List.Position.Offset; !(mid < want && mid > 0) {
		t.Fatalf("no smooth scroll: offset %d (from %d to 0)", mid, want)
	}
	if d.p.pin != 0 {
		t.Fatalf("the clicked tab must stay selected while scrolling (pin %d)", d.p.pin)
	}
	for i := 0; i < 60; i++ {
		d.frame(nil)
	}
	if off := d.p.scroll.List.Position.Offset; off != 0 {
		t.Fatalf("scroll ended at %d, want 0", off)
	}
	if d.p.spy != 0 {
		t.Fatalf("spy tab %d at the top", d.p.spy)
	}
}

// TestPressOnFieldKeepsTyping: a press over a text field (its box, its eye
// button) does not end the typing; a press elsewhere does.
func TestPressOnFieldKeepsTyping(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New()}
	d.frame(nil)
	ed := &d.p.text.base.Editor
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: ed.Len()}, Text: "http://keep.test/v1"})
	d.frame(nil)
	d.p.text.key.hovered = true // the pointer rests on the key field (its eye button)
	d.click(600, 20)
	d.frame(nil)
	d.frame(nil)
	h.Flush()
	if !d.p.text.base.edited || c.E.Config().Get().Text.BaseURL == "http://keep.test/v1" {
		t.Fatal("a press over a field ended the typing")
	}
	d.p.text.key.hovered = false
	d.click(600, 20)
	d.frame(nil)
	d.frame(nil)
	h.Flush()
	if got := c.E.Config().Get().Text.BaseURL; got != "http://keep.test/v1" {
		t.Fatalf("BaseURL = %q after a press elsewhere", got)
	}
}

// TestLeavingCommits: when the page is left while typing, the route
// transition draws it once more with a disabled context (gui.Route's fade
// out) — that counts as leaving the field, so the edit is saved.
func TestLeavingCommits(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New()}
	d.frame(nil)
	ed := &d.p.stt.model.Editor
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: ed.Len()}, Text: "scribe_v2_test"})
	d.frame(nil)
	// The fade-out frame of a route change.
	d.ops.Reset()
	gtx := layout.Context{
		Ops: &d.ops, Now: time.Date(2026, 1, 1, 12, 0, 1, 0, time.UTC),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(1440, 900)),
		Source:      d.r.Source(),
	}
	core.Frame(c, h, func(gtx C) D { return d.p.Layout(gtx.Disabled(), c, "") })(gtx)
	h.Flush()
	if got := c.E.Config().Get().STT.Model; got != "scribe_v2_test" {
		t.Fatalf("STT model = %q after leaving the page", got)
	}
}
