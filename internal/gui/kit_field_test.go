package gui

import (
	"testing"
	"unicode/utf8"

	"gioui.org/io/key"
)

func TestTextFieldEditSyncAndMaxLen(t *testing.T) {
	h := newHarness(t)
	var f TextField
	model := "Лисиця"
	changes := 0
	blur := false
	draw := func(gtx C) {
		if v, ok := f.Changed(gtx); ok {
			model = v
			changes++
		}
		f.Sync(gtx, model)
		if blur {
			gtx.Execute(key.FocusCmd{})
			blur = false
		}
		at(gtx, 10, 10, 400, 200, func(gtx C) D {
			return f.Layout(gtx, h.th, FieldStyle{Label: "Назва", MaxLen: 10})
		})
	}
	h.frame(draw)
	if f.Text() != "Лисиця" {
		t.Fatalf("Sync did not fill the field: %q", f.Text())
	}
	h.frame(draw)
	if changes != 0 {
		t.Fatalf("Sync was reported as a user edit (%d changes)", changes)
	}
	// Click the box (label ≈ 18 px + 8 px gap above it) to focus, then type.
	h.click(100, 55)
	h.frames(2, draw)
	if !h.r.Source().Focused(&f.Editor) {
		t.Fatal("click did not focus the field")
	}
	n := utf8.RuneCountInString(f.Text())
	h.typeText(n, n, " і місяць")
	h.frames(2, draw)
	if model != "Лисиця і м" || utf8.RuneCountInString(model) != 10 {
		t.Fatalf("model = %q, want the first 10 runes (MaxLen)", model)
	}
	// While focused, the model never overwrites what the user is typing.
	model = "Інше"
	h.frame(draw)
	if f.Text() != "Лисиця і м" {
		t.Errorf("Sync overwrote a focused field: %q", f.Text())
	}
	// Once blurred, the model wins again.
	blur = true
	h.frames(3, draw)
	if f.Text() != "Інше" {
		t.Errorf("after blur text = %q, want the model", f.Text())
	}
}

func TestTextFieldSubmitAndPassword(t *testing.T) {
	h := newHarness(t)
	var f TextField
	submits := 0
	draw := func(gtx C) {
		if f.Submitted(gtx) {
			submits++
		}
		at(gtx, 10, 10, 400, 100, func(gtx C) D {
			return f.Layout(gtx, h.th, FieldStyle{Password: true, Submit: true})
		})
	}
	h.frame(draw)
	if f.Editor.Mask != '•' {
		t.Fatalf("password field mask = %q", f.Editor.Mask)
	}
	f.reveal.Click()
	h.frames(2, draw)
	if f.Editor.Mask != 0 {
		t.Errorf("show button did not reveal the text (mask %q)", f.Editor.Mask)
	}
	h.click(50, 30)
	h.frame(draw)
	h.typeText(0, 0, "ключ")
	h.frame(draw)
	h.key(key.NameReturn)
	h.frames(2, draw)
	if submits != 1 {
		t.Errorf("submits = %d, want 1", submits)
	}
	if f.Text() != "ключ" {
		t.Errorf("Enter changed the text of a single-line field: %q", f.Text())
	}
}

func TestTextAreaGrows(t *testing.T) {
	h := newHarness(t)
	var f TextField
	var height int
	draw := func(gtx C) {
		height = at(gtx, 10, 10, 300, 500, func(gtx C) D {
			return f.Layout(gtx, h.th, FieldStyle{MinLines: 2, MaxLines: 4})
		}).Size.Y
	}
	h.frame(draw)
	h0 := height
	f.SetText("рядок 1\nрядок 2\nрядок 3")
	h.frame(draw)
	h1 := height
	f.SetText("1\n2\n3\n4\n5\n6\n7\n8")
	h.frame(draw)
	h2 := height
	if !(h0 < h1 && h1 < h2) {
		t.Errorf("heights %d → %d → %d, want growth", h0, h1, h2)
	}
	f.SetText("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12")
	h.frame(draw)
	if height != h2 {
		t.Errorf("grew past MaxLines: %d, want %d", height, h2)
	}
}

func TestSearchFieldEscapeClears(t *testing.T) {
	h := newHarness(t)
	var s SearchField
	query := ""
	draw := func(gtx C) {
		if q, ok := s.Changed(gtx); ok {
			query = q
		}
		at(gtx, 10, 10, 300, 60, func(gtx C) D { return s.Layout(gtx, h.th, "Пошук") })
	}
	h.frame(draw)
	h.click(100, 30)
	h.frame(draw)
	h.typeText(0, 0, "кіно")
	h.frames(2, draw)
	if query != "кіно" {
		t.Fatalf("query = %q", query)
	}
	h.key(key.NameEscape)
	h.frames(2, draw)
	if query != "" || s.Query() != "" {
		t.Errorf("after Escape query=%q text=%q, want empty", query, s.Query())
	}
}

func TestPluralUK(t *testing.T) {
	for n, want := range map[int]string{0: "сцен", 1: "сцена", 2: "сцени", 4: "сцени", 5: "сцен", 11: "сцен", 12: "сцен", 14: "сцен",
		21: "сцена", 22: "сцени", 25: "сцен", 101: "сцена", 111: "сцен", 112: "сцен", 122: "сцени"} {
		if got := PluralUK(n, "сцена", "сцени", "сцен"); got != want {
			t.Errorf("PluralUK(%d) = %q, want %q", n, got, want)
		}
	}
}
