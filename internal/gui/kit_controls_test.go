package gui

import (
	"testing"

	"gioui.org/io/key"
)

func TestToggleClick(t *testing.T) {
	h := newHarness(t)
	var tg Toggle
	on := false
	draw := func(gtx C) {
		if v, ok := tg.Changed(gtx); ok {
			on = v
		}
		at(gtx, 10, 10, 300, 60, func(gtx C) D { return tg.Layout(gtx, h.th, on, "Озвучка") })
	}
	h.frame(draw)
	h.click(20, 20)
	h.frames(2, draw)
	if !on {
		t.Fatal("click did not switch the toggle on")
	}
	h.click(120, 20) // the label is part of the target
	h.frames(2, draw)
	if on {
		t.Fatal("click on the label did not switch it off")
	}
	tg.Disabled = true
	h.click(20, 20)
	h.frames(2, draw)
	if on {
		t.Error("disabled toggle changed")
	}
}

func TestSliderKeysAndSnap(t *testing.T) {
	h := newHarness(t)
	var s Slider
	value := float32(4)
	draw := func(gtx C) {
		if v, ok := s.Changed(gtx); ok {
			value = v
		}
		at(gtx, 10, 10, 300, 60, func(gtx C) D {
			return s.Layout(gtx, h.th, SliderStyle{Value: value, Min: 4, Max: 8, Step: 2})
		})
	}
	h.frame(draw)
	h.click(12, 24) // press focuses the slider (and lands on the minimum)
	h.frames(2, draw)
	h.key(key.NameRightArrow)
	h.frames(2, draw)
	if value != 6 {
		t.Fatalf("after → value = %v, want 6", value)
	}
	h.key(key.NameEnd)
	h.frames(2, draw)
	if value != 8 {
		t.Fatalf("after End value = %v, want 8", value)
	}
	h.key(key.NameRightArrow)
	h.frames(2, draw)
	if value != 8 {
		t.Errorf("stepped past Max: %v", value)
	}
	// A press in the middle snaps to the nearest step.
	h.click(160, 24)
	h.frames(2, draw)
	if value != 6 {
		t.Errorf("press in the middle = %v, want 6", value)
	}
}

func TestPillTabsClickAndArrows(t *testing.T) {
	h := newHarness(t)
	var p PillTabs
	sel := 0
	tabs := []Tab{{Label: "Сценарій", Number: 1}, {Label: "План", Number: 2}, {Label: "Кадри", Number: 3}}
	var width int
	draw := func(gtx C) {
		if i, ok := p.Changed(gtx); ok {
			sel = i
		}
		width = at(gtx, 10, 10, 600, 60, func(gtx C) D { return p.Layout(gtx, h.th, sel, tabs) }).Size.X
	}
	h.frame(draw)
	// Click roughly in the middle of the tab strip → the second tab.
	h.click(float32(10+width/2), 30)
	h.frames(2, draw)
	if sel != 1 {
		t.Fatalf("clicked tab = %d, want 1", sel)
	}
	h.frame(func(gtx C) {
		gtx.Execute(key.FocusCmd{Tag: &p.clicks[1].Clickable})
		draw(gtx)
	})
	h.key(key.NameRightArrow)
	h.frames(2, draw)
	if sel != 2 {
		t.Errorf("→ selected %d, want 2", sel)
	}
	if !h.r.Source().Focused(&p.clicks[2].Clickable) {
		t.Error("focus did not follow the arrow key")
	}
}
