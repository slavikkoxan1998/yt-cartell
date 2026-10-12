package gui

import (
	"image"
	"testing"

	"gioui.org/io/key"
)

func TestPlacePopover(t *testing.T) {
	const gap, margin = 6, 12
	cases := []struct {
		name         string
		anchor       image.Rectangle
		winH, popH   int
		minH         int
		wantY, wantH int
		wantUp       bool
	}{
		{"fits below", image.Rect(0, 100, 300, 144), 900, 300, 120, 44 + gap, 300, false},
		{"exactly fits below", image.Rect(0, 100, 300, 144), 144 + gap + 300 + margin, 300, 120, 44 + gap, 300, false},
		// Near the bottom: 900-12-(844+6)=38 px below, 832 above → opens up.
		{"flips up near the bottom", image.Rect(0, 800, 300, 844), 900, 300, 120, -gap - 300, 300, true},
		// Not enough room either side, more below → stays below, shrunk.
		{"shrinks below", image.Rect(0, 150, 300, 194), 500, 400, 120, 44 + gap, 500 - margin - 194 - gap, false},
		// Not enough room either side, more above → up, shrunk to the room.
		{"shrinks above", image.Rect(0, 300, 300, 344), 500, 400, 120, -gap - (300 - gap - margin), 300 - gap - margin, true},
		// Tiny window: never below the minimum height.
		{"keeps min height", image.Rect(0, 40, 300, 84), 150, 400, 120, 44 + gap, 120, false},
	}
	for _, c := range cases {
		y, h, up := placePopover(c.anchor, c.winH, c.popH, c.minH, gap, margin)
		if y != c.wantY || h != c.wantH || up != c.wantUp {
			t.Errorf("%s: got y=%d h=%d up=%v, want y=%d h=%d up=%v", c.name, y, h, up, c.wantY, c.wantH, c.wantUp)
		}
	}
}

func TestFilterOptions(t *testing.T) {
	opts := []SelectOption{{Label: "Українська", Hint: "uk"}, {Label: "Англійська", Hint: "en"}, {Label: "Іспанська", Hint: "es"}}
	if got := filterOptions(opts, "  СЬКА ", nil); len(got) != 3 {
		t.Errorf("case-insensitive filter = %v", got)
	}
	if got := filterOptions(opts, "EN", nil); len(got) != 1 || got[0] != 1 {
		t.Errorf("hint filter = %v, want [1]", got)
	}
	if got := filterOptions(opts, "", nil); len(got) != 3 {
		t.Errorf("empty query = %v", got)
	}
}

var testOpts = []SelectOption{
	{Value: "a", Label: "Альфа"},
	{Value: "b", Label: "Бета"},
	{Value: "c", Label: "Гамма", Disabled: true},
	{Value: "d", Label: "Дельта"},
}

func TestSelectKeyboardPick(t *testing.T) {
	h := newHarness(t)
	var s Select
	value := "b"
	draw := func(gtx C) {
		if v, ok := s.Changed(gtx); ok {
			value = v
		}
		at(gtx, 100, 100, 300, 200, func(gtx C) D {
			return s.Layout(gtx, h.th, SelectStyle{Options: testOpts, Value: value})
		})
	}
	h.frame(draw)
	h.click(150, 120)
	h.frames(4, draw)
	if !s.IsOpen() {
		t.Fatal("select did not open on click")
	}
	if s.up {
		t.Error("select near the top opened upwards")
	}
	h.key(key.NameDownArrow) // Бета → (Гамма is disabled) → Дельта
	h.frame(draw)
	h.key(key.NameReturn)
	h.frames(2, draw)
	if value != "d" {
		t.Fatalf("picked %q, want d", value)
	}
	if s.IsOpen() {
		t.Error("select stayed open after Enter")
	}
	// Escape closes without picking.
	h.click(150, 120)
	h.frames(4, draw)
	h.key(key.NameUpArrow)
	h.frame(draw)
	h.key(key.NameEscape)
	h.frames(2, draw)
	if s.IsOpen() || value != "d" {
		t.Errorf("after Escape: open=%v value=%q", s.IsOpen(), value)
	}
	// An outside press closes it too.
	h.click(150, 120)
	h.frames(4, draw)
	h.click(700, 550)
	h.frames(2, draw)
	if s.IsOpen() {
		t.Error("outside click did not close the popover")
	}
}

func TestSelectFlipsNearBottom(t *testing.T) {
	h := newHarness(t)
	var s Select
	draw := func(gtx C) {
		at(gtx, 100, 530, 300, 60, func(gtx C) D {
			return s.Layout(gtx, h.th, SelectStyle{Options: testOpts, Value: "a"})
		})
	}
	h.frame(draw)
	h.click(150, 550)
	h.frames(4, draw)
	if !s.IsOpen() || !s.up {
		t.Fatalf("open=%v up=%v, want an upward popover near the window bottom", s.IsOpen(), s.up)
	}
}

func TestSelectSearch(t *testing.T) {
	h := newHarness(t)
	var s Select
	var opts []SelectOption
	for _, l := range []string{"Авто", "Українська", "Англійська", "Польська", "Німецька", "Французька", "Іспанська", "Італійська", "Японська", "Корейська"} {
		opts = append(opts, SelectOption{Value: l, Label: l})
	}
	value := "Авто"
	draw := func(gtx C) {
		if v, ok := s.Changed(gtx); ok {
			value = v
		}
		at(gtx, 100, 60, 300, 200, func(gtx C) D {
			return s.Layout(gtx, h.th, SelectStyle{Options: opts, Value: value})
		})
	}
	h.frame(draw)
	h.click(150, 80)
	h.frames(4, draw)
	if !h.r.Source().Focused(&s.search.Editor) {
		t.Fatal("search field is not focused with > 8 options")
	}
	h.typeText(0, 0, "япон")
	h.frames(2, draw)
	if len(s.shown) != 1 || opts[s.shown[0]].Value != "Японська" {
		t.Fatalf("filtered = %v", s.shown)
	}
	h.key(key.NameReturn)
	h.frames(2, draw)
	if value != "Японська" {
		t.Errorf("picked %q, want Японська", value)
	}
}
