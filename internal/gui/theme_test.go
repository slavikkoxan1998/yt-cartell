package gui

import (
	"image/color"
	"math"
	"reflect"
	"testing"

	"gioui.org/layout"
)

func relLum(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrast(a, b color.NRGBA) float64 {
	la, lb := relLum(a), relLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestPaletteContrast(t *testing.T) {
	for name, p := range map[string]Palette{"light": LightPalette, "dark": DarkPalette} {
		checks := []struct {
			what   string
			fg, bg color.NRGBA
			min    float64
		}{
			{"Text on Card", p.Text, p.Card, 7},
			{"Text2 on Card", p.Text2, p.Card, 4.5},
			{"Text2 on Surface", p.Text2, p.Surface, 4.5},
			{"Text3 on Card", p.Text3, p.Card, 4.5},
			{"Text3 on Surface", p.Text3, p.Surface, 4.5},
			{"Text3 on Ground", p.Text3, p.Ground, 4.5},
			{"ActionText on Card", p.ActionText, p.Card, 4.5},
			{"ActionText on ActionSoft", p.ActionText, p.ActionSoft, 4.5},
			{"SuccessInk on SuccessSoft", p.SuccessInk, p.SuccessSoft, 4.5},
			{"RunningInk on RunningSoft", p.RunningInk, p.RunningSoft, 4.5},
			{"WarningInk on WarningSoft", p.WarningInk, p.WarningSoft, 4.5},
			{"ErrorInk on ErrorSoft", p.ErrorInk, p.ErrorSoft, 4.5},
			{"white on ErrorFill", rgb(0xFFFFFF), p.ErrorFill, 4.5},
			{"TooltipFg on TooltipBg", p.TooltipFg, p.TooltipBg, 7},
		}
		for _, c := range checks {
			if got := contrast(c.fg, c.bg); got < c.min {
				t.Errorf("%s: %s = %.2f:1, want ≥ %.1f:1", name, c.what, got, c.min)
			}
		}
	}
}

func TestPaletteFieldsComplete(t *testing.T) {
	var p Palette
	n := 0
	rt := reflect.TypeOf(p)
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Type == reflect.TypeOf(color.NRGBA{}) {
			n++
		}
	}
	if got := len(p.fields()); got != n {
		t.Fatalf("Palette.fields lists %d colours, the struct has %d", got, n)
	}
	// Every field pointer is distinct (no token listed twice).
	seen := map[*color.NRGBA]bool{}
	for _, f := range p.fields() {
		if seen[f] {
			t.Fatal("a palette field is listed twice")
		}
		seen[f] = true
	}
	if LerpPalette(LightPalette, DarkPalette, 0) != LightPalette || LerpPalette(LightPalette, DarkPalette, 1) != DarkPalette {
		t.Error("LerpPalette endpoints are wrong")
	}
}

func TestClickableClickedBeforeAndAfterLayout(t *testing.T) {
	h := newHarness(t)
	var c Clickable
	before, after := 0, 0
	readAfter := false
	draw := func(gtx C) {
		if !readAfter && c.Clicked(gtx) {
			before++
		}
		// Laid out twice in one frame (row equalising) — clicks must survive.
		for i := 0; i < 2; i++ {
			at(gtx, 10, 10, 300, 100, func(gtx C) D {
				return Button{Text: "Далі"}.Layout(gtx, h.th, &c)
			})
		}
		if readAfter && c.Clicked(gtx) {
			after++
		}
	}
	h.frame(draw)
	h.click(30, 30)
	h.frames(2, draw)
	if before != 1 {
		t.Fatalf("clicks read before Layout = %d, want 1", before)
	}
	readAfter = true
	h.click(30, 30)
	h.frames(2, draw)
	if after != 1 {
		t.Fatalf("clicks read after Layout = %d, want 1", after)
	}
}

func TestDisabledButtonIgnoresClicks(t *testing.T) {
	h := newHarness(t)
	var c Clickable
	clicks := 0
	draw := func(gtx C) {
		if c.Clicked(gtx) {
			clicks++
		}
		at(gtx, 10, 10, 300, 100, func(gtx C) D {
			return Button{Text: "Далі", Disabled: true}.Layout(gtx, h.th, &c)
		})
	}
	h.frame(draw)
	h.click(30, 30)
	h.frames(2, draw)
	if clicks != 0 {
		t.Errorf("disabled button clicked %d times", clicks)
	}
}

func TestToastsStackInOrder(t *testing.T) {
	h := newHarness(t)
	h.th.Toast(ToneSuccess, "перший")
	h.th.Toast(ToneError, "другий")
	h.th.Toast(ToneInfo, "третій")
	h.frames(3, func(C) {})
	items := h.th.svc.toasts.items
	if len(items) != 3 || items[0].text != "перший" || items[1].text != "другий" || items[2].text != "третій" {
		var got []string
		for _, t := range items {
			got = append(got, t.text)
		}
		t.Fatalf("toasts = %v", got)
	}
}

func TestSnapshotSettles(t *testing.T) {
	th := MustTheme()
	var a Anim
	frames := 0
	img, err := Snapshot(64, 64, 1, func(gtx layout.Context) layout.Dimensions {
		frames++
		a.Duration = DurDialog
		target := float32(0)
		if frames > 1 {
			target = 1 // starts animating on the second frame
		}
		a.Animate(gtx, target)
		FillRect(gtx, rectWH(64, 64), th.Pal.Ground)
		return layout.Dimensions{}
	}, Frames(2), Settle(0))
	if err != nil {
		t.Skipf("no headless GPU: %v", err)
	}
	if img.Bounds().Dx() != 64 || a.Running() || a.Target() != 1 {
		t.Errorf("settled after %d frames: running=%v", frames, a.Running())
	}
	if frames < 10 || frames > 40 {
		t.Errorf("frames = %d, want the ~180 ms animation to run to its end and stop", frames)
	}
}

func TestOnColor(t *testing.T) {
	if OnColor(LightPalette.ErrorFill) != rgb(0xFFFFFF) {
		t.Error("white expected on the deep error fill")
	}
	for _, c := range []color.NRGBA{DarkPalette.Success, DarkPalette.Warning, DarkPalette.Running} {
		if got := OnColor(c); contrast(got, c) < 4.5 {
			t.Errorf("OnColor(%v) = %v: %.2f:1", c, got, contrast(got, c))
		}
	}
}

func palettes() map[string]Palette {
	return map[string]Palette{"light": LightPalette, "dark": DarkPalette}
}

// TestActionInkContrast: every label and icon on an Action fill uses
// OnAction, which must read ≥ 4.5:1 on the rest, hover and pressed fills —
// and on every blend between them (hover/press animate).
func TestActionInkContrast(t *testing.T) {
	for name, p := range palettes() {
		for _, f := range []struct {
			what string
			c    color.NRGBA
		}{{"Action", p.Action}, {"ActionHover", p.ActionHover}, {"ActionPressed", p.ActionPressed}} {
			if got := contrast(p.OnAction, f.c); got < 4.5 {
				t.Errorf("%s: OnAction on %s = %.2f:1, want ≥ 4.5:1", name, f.what, got)
			}
		}
		for i := 0; i <= 10; i++ {
			k := float32(i) / 10
			for _, c := range []color.NRGBA{LerpColor(p.Action, p.ActionHover, k), LerpColor(p.ActionHover, p.ActionPressed, k), LerpColor(p.Action, p.ActionPressed, k)} {
				if got := contrast(p.OnAction, c); got < 4.5 {
					t.Errorf("%s: OnAction on blended %v = %.2f:1", name, c, got)
				}
			}
		}
	}
}

// TestButtonStatesContrast walks every button kind through rest, hover,
// pressed and the blends between them in both palettes.
func TestButtonStatesContrast(t *testing.T) {
	th := MustTheme()
	kinds := map[string]ButtonKind{"Primary": Primary, "Secondary": Secondary, "Tinted": Tinted, "Ghost": Ghost, "Danger": Danger}
	for pname, p := range palettes() {
		th.Pal = p
		for kname, k := range kinds {
			for _, h := range []float32{0, 0.5, 1} {
				for _, pr := range []float32{0, 0.5, 1} {
					bg, fg, _ := Button{Kind: k}.colors(th, interaction{hover: h, press: pr})
					solid := Over(p.Card, bg) // translucent fills sit on a card
					if got := contrast(fg, solid); got < 4.5 {
						t.Errorf("%s %s hover=%.1f press=%.1f: %v on %v = %.2f:1", pname, kname, h, pr, fg, solid, got)
					}
				}
			}
		}
	}
}

// TestSolidFillTextContrast covers the other solid fill + text pairs.
func TestSolidFillTextContrast(t *testing.T) {
	for name, p := range palettes() {
		fills := map[string]color.NRGBA{
			"Success": p.Success, "Running": p.Running, "Warning": p.Warning, "Error": p.Error,
			"ErrorFill": p.ErrorFill, "Text2 (neutral solid badge)": p.Text2, "TooltipBg": p.TooltipBg,
		}
		for what, c := range fills {
			if got := contrast(OnColor(c), c); got < 4.5 {
				t.Errorf("%s: OnColor on %s = %.2f:1", name, what, got)
			}
		}
		// Danger keeps white; its hover/press only deepen.
		for _, k := range []float32{0, 0.12, 0.22} {
			c := LerpColor(p.ErrorFill, rgb(0x000000), k)
			if got := contrast(rgb(0xFFFFFF), c); got < 4.5 {
				t.Errorf("%s: white on ErrorFill deepened %.2f = %.2f:1", name, k, got)
			}
		}
		pairs := []struct {
			what   string
			fg, bg color.NRGBA
		}{
			{"chip label on SurfaceHover (pressed)", p.Text2, p.SurfaceHover},
			{"selected chip label", p.Text, p.ActionSoft},
			{"selected chip icon (rest)", p.ActionText, p.ActionSoft},
			{"selected chip icon (hover)", LerpColor(p.ActionText, p.Text, 0.35), p.ActionSoft},
			{"active nav label", p.ActionText, p.Card},
			{"tile hover icon", p.ActionText, p.ActionSoft},
			{"link hover", LerpColor(p.ActionText, p.Text, 0.3), p.Card},
			{"tab number on Action", p.OnAction, p.Action},
			{"unselected tab number", p.Text2, p.SurfaceHover},
			{"step done check", OnColor(p.Success), p.Success},
		}
		for _, c := range pairs {
			if got := contrast(c.fg, c.bg); got < 4.5 {
				t.Errorf("%s: %s = %.2f:1", name, c.what, got)
			}
		}
	}
}
