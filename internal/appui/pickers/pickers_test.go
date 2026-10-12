package pickers

import (
	"image"
	"path/filepath"
	"strings"
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
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/tts"
)

func testGtx(now time.Time) C {
	return layout.Context{Ops: new(op.Ops), Now: now, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Exact(image.Pt(800, 600))}
}

func TestPickReportsOnceAndHolds(t *testing.T) {
	var p pick[string]
	now := time.Now()
	gtx := testGtx(now)
	if _, ok := p.Changed(); ok {
		t.Fatal("fresh pick reported a change")
	}
	p.set(gtx, "fast")
	if v, ok := p.Changed(); !ok || v != "fast" {
		t.Fatalf("Changed = %q, %v", v, ok)
	}
	if _, ok := p.Changed(); ok {
		t.Fatal("Changed reported twice")
	}
	// Until the model reports the pick, the field shows the pick.
	if got := p.shown(testGtx(now.Add(100*time.Millisecond)), "slow"); got != "fast" {
		t.Fatalf("shown before the model caught up = %q", got)
	}
	if got := p.shown(testGtx(now.Add(200*time.Millisecond)), "fast"); got != "fast" {
		t.Fatalf("shown = %q", got)
	}
	// Once caught up (or after holdFor) the model wins again.
	if got := p.shown(testGtx(now.Add(300*time.Millisecond)), "balanced"); got != "balanced" {
		t.Fatalf("shown after catching up = %q", got)
	}
	p.set(gtx, "x")
	if got := p.shown(testGtx(now.Add(holdFor+time.Millisecond)), "refused"); got != "refused" {
		t.Fatalf("a refused pick must fall back after holdFor, got %q", got)
	}
}

func TestLabels(t *testing.T) {
	if LanguageLabel("") != AutoLanguage || LanguageLabel("en") != "Англійська" || LanguageLabel("xx") != "xx" {
		t.Fatal("LanguageLabel")
	}
	if LanguageBadge("uk") != "UK" || LanguageBadge("") != "A" {
		t.Fatal("LanguageBadge")
	}
	for _, l := range studio.Languages {
		n := NativeName(l)
		if n == "" {
			t.Fatalf("%s: empty name", l.Code)
		}
	}
	am, _ := studio.LanguageByCode("am")
	if NativeName(am) != "Amharic" {
		t.Fatalf("Ethiopic script must fall back to English, got %q", NativeName(am))
	}
	uk, _ := studio.LanguageByCode("uk")
	if NativeName(uk) != "Українська" {
		t.Fatal("Cyrillic self-name expected")
	}
	if StyleLabel("cinematic") != "Кіно" || StyleLabel("nope") != "nope" {
		t.Fatal("StyleLabel")
	}
	if ImageModelLabel("GEM_PIX_2") != "Nano Banana Pro" || ImageModelLabel("x") != "" {
		t.Fatal("ImageModelLabel")
	}
	if AspectLabel("9:16") != "9:16 · Вертикальне" || AspectRatio("9:16") >= 1 || AspectRatio("??") < 1.7 {
		t.Fatal("aspects")
	}
	for _, a := range Aspects {
		if a.Value != "16:9" && a.Value != "9:16" {
			t.Fatalf("the engine keeps only 16:9 and 9:16, got %s", a.Value)
		}
	}
	list := []tts.Voice{{ID: "a", Name: "Rachel"}}
	if VoiceName(list, "a") != "Rachel" || VoiceName(list, "zz") != UnknownVoice || VoiceName(list, "") != "" {
		t.Fatal("VoiceName must never show a raw id")
	}
	meta := VoiceMeta(tts.Voice{Gender: "female", Accent: "british", Category: "premade", Usable: false})
	for _, want := range []string{"жіночий", "британський", "стандартний", "платний"} {
		if !strings.Contains(meta, want) {
			t.Fatalf("VoiceMeta %q lacks %q", meta, want)
		}
	}
}

func TestPalettesAndPosters(t *testing.T) {
	for _, s := range studio.Styles {
		if len(Palette(s.ID)) != 3 {
			t.Fatalf("%s: palette %v", s.ID, s.Palette)
		}
	}
	img := palettePoster(120, 90, [3]string{"#0F1A24", "#D98E04", "#4A6B8A"}, "cinematic")
	if img.Bounds().Dx() != 120 || img.Bounds().Dy() != 90 {
		t.Fatal("poster size")
	}
	// The poster uses the palette: the top-left pixel is the sky colour.
	if c := img.RGBAAt(0, 0); c.R != 0x0F || c.B != 0x24 {
		t.Fatalf("sky = %v", c)
	}
	for _, s := range studio.Styles {
		if !hasPreview(s.ID) {
			t.Fatalf("%s: no embedded preview picture", s.ID)
		}
	}
	if hasPreview("no-such-style") {
		t.Fatal("preview for an unknown style id")
	}
}

func TestDurationClamp(t *testing.T) {
	if clampSeconds(3) != MinSeconds || clampSeconds(99999) != MaxSeconds || clampSeconds(90) != 90 {
		t.Fatal("clamp")
	}
	if PresetLabel(15) != "15 с" || PresetLabel(60) != "1 хв" {
		t.Fatal("preset labels")
	}
}

// TestFieldsRender lays every field out (closed and open) on a headless
// context: no panics, sane sizes, popovers anchored.
func TestFieldsRender(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	c.SetVoicesForTest(tts.VoiceList{Voices: []tts.Voice{{ID: "v1", Name: "Olena", Usable: true, PreviewURL: "https://example.invalid/a.mp3"}}})
	var (
		st    Style
		lang  Language
		voice Voice
		ch    Choice
		dur   Duration
		asp   Aspect
		model ImageModel
		vm    VideoMode
		menu  Menu
		btn   gui.Clickable
	)
	draw := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D { return st.Layout(gtx, c, "Стиль", "watercolor") },
			func(gtx C) D { return lang.Layout(gtx, c, "Мова", "uk") },
			func(gtx C) D { return voice.Layout(gtx, c, "Голос", "v1") },
			func(gtx C) D { return ch.Layout(gtx, c, "Жанр", studio.Genres, "drama") },
			func(gtx C) D { return dur.Layout(gtx, c, "Тривалість", 75) },
			func(gtx C) D { return asp.Layout(gtx, c, "Формат", "9:16") },
			func(gtx C) D { return model.Layout(gtx, c, "Модель", "GEM_PIX") },
			func(gtx C) D {
				return vm.Layout(gtx, c, "Режим", VideoModeValue{Storyboard: studio.StoryboardGrid, Chain: true})
			},
			func(gtx C) D {
				return menu.Layout(gtx, c.Th, &btn, []MenuItem{{Value: "a", Label: "A"}, {Value: "b", Label: "B", Danger: true, Divider: true}},
					func(gtx C) D { return gui.Button{Text: "Меню"}.Layout(gtx, c.Th, &btn) })
			},
		)
	}
	shot := func(name string) {
		t.Helper()
		img, err := gui.Snapshot(900, 1400, 1, core.Frame(c, h, draw), gui.Frames(3))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if img.Bounds().Dx() != 900 {
			t.Fatalf("%s: bad image", name)
		}
	}
	shot("closed")
	for _, open := range []func(){st.Open, lang.Open, voice.Open, ch.Open, dur.Open, asp.Open, model.Open, vm.Open, menu.Open} {
		open()
		shot("open")
	}
	if !menu.IsOpen() {
		t.Fatal("menu should stay open until an outside press")
	}
	menu.Close()
	if menu.IsOpen() {
		t.Fatal("Close")
	}
}

// TestLanguageClickAndKeys drives the real input router: a click opens the
// list, ↓ moves the highlight, Enter picks and closes it.
func TestLanguageClickAndKeys(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var lang Language
	draw := func(gtx C) D {
		gtx.Constraints.Max.X = 400
		return lang.Layout(gtx, c, "Мова", "uk")
	}
	press := func(r *input.Router, x, y float32) {
		r.Queue(
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(x, y)},
		)
	}
	keyPress := func(r *input.Router, name key.Name) {
		r.Queue(key.Event{Name: name, State: key.Press}, key.Event{Name: name, State: key.Release})
	}
	var picked string
	var ok bool
	_, err = gui.Snapshot(800, 900, 1, core.Frame(c, h, func(gtx C) D {
		d := draw(gtx)
		if v, k := lang.Changed(); k {
			picked, ok = v, true
		}
		return d
	}), gui.Frames(14), gui.BeforeFrame(func(frame int, r *input.Router) {
		switch frame {
		case 2:
			press(r, 150, 40) // the tile
		case 8:
			keyPress(r, key.NameDownArrow) // Українська → Англійська
		case 10:
			keyPress(r, key.NameReturn)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || picked != "en" {
		t.Fatalf("picked %q (%v); open=%v", picked, ok, lang.pop.open)
	}
	if lang.pop.open {
		t.Fatal("Enter must close the list")
	}
}

// TestChoiceRowClickAndOutsidePress: a click on a row picks it; a press
// outside closes the list without picking.
func TestChoiceRowClickAndOutsidePress(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var tempo Choice
	tileH := 0
	var picks []string
	click := func(r *input.Router, x, y float32) {
		r.Queue(
			pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)},
			pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(x, y)},
		)
	}
	_, err = gui.Snapshot(800, 900, 1, core.Frame(c, h, func(gtx C) D {
		gtx.Constraints.Max.X = 400
		d := tempo.Layout(gtx, c, "Темп", studio.Tempos, "balanced")
		tileH = d.Size.Y
		if v, ok := tempo.Changed(); ok {
			picks = append(picks, v)
		}
		return d
	}), gui.Frames(26), gui.BeforeFrame(func(frame int, r *input.Router) {
		switch frame {
		case 2:
			click(r, 150, float32(tileH)/2) // open
		case 8:
			// Rows start under the tile: gap 6 + padding 6; the third row is "Швидкий".
			click(r, 150, float32(tileH+popGap+popPad+2*rowH+rowH/2))
		case 14:
			click(r, 150, float32(tileH)/2) // open again
		case 20:
			click(r, 700, 850) // outside
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(picks) != 1 || picks[0] != "fast" {
		t.Fatalf("picks = %v", picks)
	}
	if tempo.pop.open {
		t.Fatal("an outside press must close the list")
	}
}

// TestStylePreviewSizes: previews decode at the size they are drawn (80 px
// steps, the natural 640 px at most), the gallery cards are measured like
// the kit lays them out.
func TestStylePreviewSizes(t *testing.T) {
	for in, want := range map[int]int{0: 240, 100: 160, 208: 240, 241: 320, 312: 320, 560: 560, 600: -1, 900: -1} {
		if got := previewPx(in); got != want {
			t.Fatalf("previewPx(%d) = %d, want %d", in, got, want)
		}
	}
	gtx := layout.Context{Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	// 1920 px window at 100 %: a 1160 dp dialog, 5 cards of 208 px.
	if got := galleryCardPx(gtx, image.Pt(1920, 1080)); got != 208 {
		t.Fatalf("card at 1920 = %d", got)
	}
	gtx.Metric = unit.Metric{PxPerDp: 1.5, PxPerSp: 1.5}
	if got := galleryCardPx(gtx, image.Pt(1920, 1080)); got <= 208 || got > 400 {
		t.Fatalf("card at 150 %% = %d", got)
	}

	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, _ := core.NewHeadless(e)
	defer c.Close()
	c.Img.SetSync(true)
	for _, px := range []int{96, 208, 312} {
		img, ok := StylePreviewAt(c, "cinematic", px)
		if !ok {
			t.Fatalf("no preview at %d px", px)
		}
		if w := img.Size().X; w > previewPx(px) || w < px {
			t.Fatalf("decoded %d px wide for %d px (want ≤ %d)", w, px, previewPx(px))
		}
	}
	if img, ok := StylePreview(c, "cinematic"); !ok || img.Size().X > thumbPx {
		t.Fatalf("thumbnail decoded %d px wide", img.Size().X)
	}
}
