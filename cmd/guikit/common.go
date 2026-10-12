package main

import (
	"image"
	"image/color"
	"math"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/gui"
)

type (
	C = layout.Context
	D = layout.Dimensions
)

// gallery holds the widget state of one rendered gallery image.
type gallery struct {
	th     *gui.Theme
	frame  int
	states map[string]any
	imgs   map[int]paint.ImageOp

	dialog gui.Dialog
	drawer gui.Drawer
	picker gui.BigPicker
	tour   gui.Tour
}

func newGallery(th *gui.Theme) *gallery {
	return &gallery{th: th, states: map[string]any{}, imgs: map[int]paint.ImageOp{}}
}

// st returns the state of type T stored under key (allocated on first use).
func st[T any](g *gallery, key string) *T {
	if v, ok := g.states[key]; ok {
		return v.(*T)
	}
	v := new(T)
	g.states[key] = v
	return v
}

func (g *gallery) click(key string) *gui.Clickable { return st[gui.Clickable](g, key) }

// pageFrame pads the page and draws its heading.
func (g *gallery) pageFrame(gtx C, title, sub string, body layout.Widget) D {
	th := g.th
	// Pages scroll: at 1440×900 the lower cards continue below the fold.
	sc := st[gui.Scroll](g, "page.scroll")
	return sc.LayoutWidget(gtx, th, func(gtx C) D { return g.pageBody(gtx, title, sub, body) })
}

func (g *gallery) pageBody(gtx C, title, sub string, body layout.Widget) D {
	th := g.th
	return layout.UniformInset(gui.Sp32).Layout(gtx, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp24,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.End}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D {
						return gui.VStack(gtx, gui.Sp4,
							gui.Text{Style: th.H1}.Widget(th, title),
							gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, sub),
						)
					}),
					layout.Rigid(func(gtx C) D {
						mode := "Світла тема"
						if th.Dark() {
							mode = "Темна тема · Вечір"
						}
						return gui.Chip{Text: mode, Icon: themeIcon(th), Selected: true}.Layout(gtx, th, nil)
					}),
				)
			},
			body,
		)
	})
}

func themeIcon(th *gui.Theme) *gui.Icon {
	if th.Dark() {
		return gui.IconMoon
	}
	return gui.IconSun
}

// section is a titled card.
func (g *gallery) section(title, note string, body layout.Widget) layout.Widget {
	th := g.th
	return func(gtx C) D {
		return gui.Card{}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return gui.VStack(gtx, gui.Sp16,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.H3}.Widget(th, title)),
						layout.Flexed(1, func(gtx C) D {
							if note == "" {
								return D{}
							}
							return layout.Inset{Left: gui.Sp12}.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, note))
						}),
					)
				},
				body,
			)
		})
	}
}

// labeled puts a small caption under w.
func (g *gallery) labeled(label string, w layout.Widget) layout.Widget {
	th := g.th
	return func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(w),
			layout.Rigid(layout.Spacer{Height: 6}.Layout),
			layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, label)),
		)
	}
}

// width caps a widget's width.
func width(dp unit.Dp, w layout.Widget) layout.Widget {
	return func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(dp))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return w(gtx)
	}
}

// image returns a synthetic warm "frame" picture (deterministic per seed).
func (g *gallery) image(seed int) paint.ImageOp {
	if op, ok := g.imgs[seed]; ok {
		return op
	}
	op := paint.NewImageOp(sceneImage(480, 300, seed))
	g.imgs[seed] = op
	return op
}

// sceneImage paints a soft illustrated landscape: a gradient sky, a sun and
// layered hills, in one of a few warm/cool palettes.
func sceneImage(w, h, seed int) *image.RGBA {
	type pal struct{ skyTop, skyBot, sun, hill1, hill2, hill3 color.NRGBA }
	pals := []pal{
		{hexc(0xFFB38A), hexc(0xFFE7C7), hexc(0xFFF6E0), hexc(0xE58A5C), hexc(0xC0613F), hexc(0x7E3B2A)},
		{hexc(0x8EC5E8), hexc(0xE3F1F7), hexc(0xFFFBEA), hexc(0x7BB38C), hexc(0x4F8A69), hexc(0x2F5E4B)},
		{hexc(0x3B2A5C), hexc(0xC56B7A), hexc(0xFFD6A0), hexc(0x6A3F66), hexc(0x42294A), hexc(0x21162B)},
		{hexc(0xF6D6A8), hexc(0xFFF2DD), hexc(0xFFFFFF), hexc(0xD9A66C), hexc(0xB07A45), hexc(0x6D4A2B)},
		{hexc(0x1F3B57), hexc(0x5C86A8), hexc(0xF2F0D8), hexc(0x2E5470), hexc(0x1D3A50), hexc(0x0F2233)},
	}
	p := pals[seed%len(pals)]
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	sunX := float64(w) * (0.25 + 0.5*frac(float64(seed)*0.37))
	sunY := float64(h) * 0.38
	sunR := float64(h) * 0.11
	hill := func(x float64, amp, base, freq, phase float64) float64 {
		return base + amp*math.Sin(x*freq+phase) + amp*0.4*math.Sin(x*freq*2.3+phase*1.7)
	}
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		sky := lerpc(p.skyTop, p.skyBot, float32(t))
		for x := 0; x < w; x++ {
			c := sky
			dx, dy := float64(x)-sunX, float64(y)-sunY
			if d := math.Hypot(dx, dy); d < sunR {
				c = p.sun
			} else if d < sunR*2.2 {
				c = lerpc(p.sun, c, float32((d-sunR)/(sunR*1.2)))
			}
			fx := float64(x) / float64(w)
			ph := float64(seed)
			if float64(y) > hill(fx, float64(h)*0.05, float64(h)*0.58, 5, ph) {
				c = p.hill1
			}
			if float64(y) > hill(fx, float64(h)*0.06, float64(h)*0.7, 3.3, ph+1) {
				c = p.hill2
			}
			if float64(y) > hill(fx, float64(h)*0.04, float64(h)*0.84, 7, ph+2) {
				c = p.hill3
			}
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	return img
}

func frac(v float64) float64 { return v - math.Floor(v) }

func hexc(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

func lerpc(a, b color.NRGBA, t float32) color.NRGBA { return gui.LerpColor(a, b, t) }

// paintZero is "no picture yet".
func paintZero() paint.ImageOp { return paint.ImageOp{} }
