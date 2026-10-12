package montage

// Субтитри: none / .srt file / burnt into the picture, the style presets
// with a live preview over a real frame (the look the montage burns in, as
// media.SubtitleStyle.Resolve describes it), and the user's overrides.

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type subsCard struct {
	mode    gui.Segmented
	tiles   []tile
	size    valSlider
	pos     gui.Segmented
	colors  swatchRow
	outline gui.Toggle
	box     gui.Toggle
	upper   gui.Toggle
	auto    gui.Clickable
	picture gui.ImageView
}

// subtitleColors are the text colours offered ("" = the preset's own).
var subtitleColors = []swatch{
	{Value: "", Tip: "Як у стилі"},
	{Value: "#FFFFFF", Tip: "Білий"},
	{Value: "#FFE000", Tip: "Жовтий"},
	{Value: "#F6E7B8", Tip: "Кремовий"},
	{Value: "#7DF9FF", Tip: "Блакитний"},
	{Value: "#8CFF6B", Tip: "Салатовий"},
	{Value: "#FF8FCB", Tip: "Рожевий"},
	{Value: "#FFA24C", Tip: "Помаранчевий"},
	{Value: "#141414", Tip: "Чорний"},
}

func (s *proj) subsCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.subs
	m := pv.P.Montage
	id := pv.ID
	style := m.SubtitleStyle
	mode := m.Subtitles
	if mode != "none" && mode != "burn" {
		mode = "srt"
	}
	edit := func(fn func(ss *studio.SubtitleStyle)) {
		c.Edit(id, func(p *studio.Project) { fn(&p.Montage.SubtitleStyle) })
	}
	if v, ok := st.mode.Changed(gtx); ok {
		c.Edit(id, func(p *studio.Project) { p.Montage.Subtitles = v })
		mode = v
	}
	resolved := mediaStyle(style).Resolve()
	if v, ok := st.size.Commit(gtx); ok {
		size := int(math.Round(float64(v)))
		edit(func(ss *studio.SubtitleStyle) { ss.Size = size })
	}
	if st.auto.Clicked(gtx) {
		edit(func(ss *studio.SubtitleStyle) { ss.Size = 0 })
	}
	if v, ok := st.pos.Changed(gtx); ok {
		edit(func(ss *studio.SubtitleStyle) { ss.Position = v })
	}
	if v, ok := st.colors.Changed(); ok {
		edit(func(ss *studio.SubtitleStyle) { ss.Color = v })
	}
	if v, ok := st.outline.Changed(gtx); ok {
		edit(func(ss *studio.SubtitleStyle) { ss.Outline = v })
	}
	if v, ok := st.box.Changed(gtx); ok {
		edit(func(ss *studio.SubtitleStyle) { ss.Box = v })
	}
	if v, ok := st.upper.Changed(gtx); ok {
		edit(func(ss *studio.SubtitleStyle) { ss.Uppercase = v })
	}
	burn := mode == "burn"
	curPreset := style.Preset
	if _, ok := media.SubtitlePresetByID(curPreset); !ok {
		curPreset = media.DefaultSubtitlePreset
	}
	ts := tiles(&st.tiles, len(media.SubtitlePresets))
	tilePic := s.pair(c, pv, 240)
	aspect := pv.P.Input.Aspect
	vertical := aspect == "9:16"
	sample := s.subtitleSample(c, pv, style.Uppercase || resolved.Uppercase)

	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		var hintText string
		switch mode {
		case "none":
			hintText = "Субтитрів не буде."
		case "srt":
			hintText = "Окремий файл .srt — завантаж його на YouTube разом із відео, глядач зможе вмикати й вимикати субтитри."
		default:
			hintText = "Текст буде вшито в картинку — зручно для Shorts і перегляду без звуку."
		}
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D {
				return cardHeader(gtx, th, gui.IconCaptions, "Субтитри", "Текст реплік на екрані: окремим файлом або прямо у відео.", nil)
			},
			func(gtx C) D {
				st.mode.Full = true
				return st.mode.Layout(gtx, th, mode, []gui.SegOption{
					{Value: "none", Label: "Без"},
					{Value: "srt", Label: "Файл .srt", Icon: gui.IconFileText},
					{Value: "burn", Label: "У відео", Icon: gui.IconSubtitles},
				})
			},
			hint(th, hintText),
			// Live preview over a real frame.
			func(gtx C) D {
				w := gtx.Constraints.Max.X
				ratio := ratioOf(aspect)
				if vertical {
					w = min(w, gtx.Dp(260))
				}
				h := int(float32(w) / ratio)
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(func(gtx C) D {
					size := pt(w, h)
					defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops).Pop()
					paint.Fill(gtx.Ops, th.Pal.Surface)
					pr := s.pair(c, pv, max(w, h))
					img := pr.a
					if pr.real && pr.sceneA != nil {
						if li, ok := lookImage(c, pv, pr.sceneA, m.Filter, max(w, h)); ok {
							img = li
						}
					}
					drawImage(gtx, img, size, true)
					switch mode {
					case "burn":
						drawSubtitle(gtx, th, resolved, sample, size, vertical)
					case "srt":
						drawYouTubeCaption(gtx, th, sample, size)
					}
					return D{Size: size}
				}))
			},
			// Presets.
			func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Label}.Widget(th, "Стиль")),
					layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Grid{MinWidth: galleryMin(gtx) - 8, Gap: gui.Sp12}.Layout(gtx, len(media.SubtitlePresets), func(gtx C, i int) D {
							pr := media.SubtitlePresets[i]
							return ts[i].layout(gtx, th, tileStyle{Label: pr.Label, Tip: pr.Hint, Selected: pr.ID == curPreset && burn,
								Disabled: !burn, Ratio: 16.0 / 9,
								Preview: func(gtx C, size image.Point, sec float64) {
									drawPresetSample(gtx, th, pr, tilePic.a, size)
								}}, func() {
								pid := pr.ID
								edit(func(ss *studio.SubtitleStyle) { *ss = studio.SubtitleStyle{Preset: pid, Uppercase: ss.Uppercase} })
							})
						})
					}),
				)
			},
			// Overrides.
			func(gtx C) D {
				if !burn {
					return hint(th, "Стиль, розмір і колір діють для субтитрів у відео. «Великі літери» — і для файлу .srt.")(gtx)
				}
				return D{}
			},
			func(gtx C) D {
				eff := resolved.Size
				if vertical && resolved.SizeVertical > 0 && style.Size == 0 {
					eff = resolved.SizeVertical
				}
				val := strconv.Itoa(eff) + " пікс."
				if style.Size == 0 {
					val = "авто · " + val
				}
				return fieldLabel(gtx, th, "Розмір літер", val, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D {
							return st.size.Layout(gtx, th, gui.SliderStyle{Value: float32(eff), Min: 32, Max: 120, Step: 2, Disabled: !burn,
								Format: func(v float32) string { return strconv.Itoa(int(v)) + " пікс." }})
						}),
						layout.Rigid(func(gtx C) D {
							if style.Size == 0 {
								return D{}
							}
							return layout.Inset{Left: gui.Sp8}.Layout(gtx, func(gtx C) D {
								return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Авто", Disabled: !burn, Tip: "Розмір зі стилю"}.Layout(gtx, th, &st.auto)
							})
						}),
					)
				})
			},
			func(gtx C) D {
				pos := resolved.Position
				gtx2 := gtx
				if !burn {
					gtx2 = gtx.Disabled()
				}
				return fieldLabel(gtx2, th, "Розташування", "", func(gtx C) D {
					st.pos.Full = true
					return st.pos.Layout(gtx, th, pos, []gui.SegOption{
						{Value: "bottom", Label: "Знизу"},
						{Value: "middle", Label: "Посередині"},
						{Value: "top", Label: "Вгорі"},
					})
				})
			},
			func(gtx C) D {
				gtx2 := gtx
				if !burn {
					gtx2 = gtx.Disabled()
				}
				items := make([]swatch, len(subtitleColors))
				copy(items, subtitleColors)
				items[0].Color = parseHex(mediaStyle(studio.SubtitleStyle{Preset: style.Preset}).Resolve().Color)
				items[0].Default = true
				return fieldLabel(gtx2, th, "Колір тексту", "", func(gtx C) D {
					return st.colors.Layout(gtx, th, items, style.Color)
				})
			},
			func(gtx C) D {
				st.outline.Disabled, st.box.Disabled = !burn, !burn
				return gui.Flow{Gap: gui.Sp24, RowGap: gui.Sp12}.Layout(gtx,
					func(gtx C) D {
						return st.outline.Layout(gtx, th, style.Outline || resolved.Outline >= 3, "Обводка")
					},
					func(gtx C) D { return st.box.Layout(gtx, th, style.Box || resolved.Box, "Плашка") },
					func(gtx C) D {
						return st.upper.Layout(gtx, th, style.Uppercase || resolved.Uppercase, "ВЕЛИКІ ЛІТЕРИ")
					},
				)
			},
		)
	})
}

// mediaStyle converts the project's style to the media one (Resolve).
func mediaStyle(s studio.SubtitleStyle) media.SubtitleStyle {
	return media.SubtitleStyle{Preset: s.Preset, Size: s.Size, Position: s.Position, Color: s.Color,
		Outline: s.Outline, Box: s.Box, Uppercase: s.Uppercase}
}

// subtitleSample is the first cue of the selected scene's line.
func (s *proj) subtitleSample(c *core.Ctx, pv *core.ProjectView, upper bool) string {
	var line string
	pr := s.pair(c, pv, 240)
	if pr.sceneA != nil {
		line = strings.TrimSpace(pr.sceneA.Narration)
	}
	if line == "" {
		for _, sc := range pv.P.Scenes {
			if t := strings.TrimSpace(sc.Narration); t != "" {
				line = t
				break
			}
		}
	}
	if line == "" {
		line = "Так виглядатимуть субтитри у твоєму відео"
	}
	if upper {
		line = strings.ToUpper(line)
	}
	parts := cues(line, 84)
	if len(parts) == 0 {
		return line
	}
	return wrap2(parts[0], 42)
}

// ---- drawing ---------------------------------------------------------------------------------------

// parseHex reads #RRGGBB (opaque black when invalid).
func parseHex(s string) color.NRGBA {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if len(s) != 6 || err != nil {
		return color.NRGBA{A: 255}
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// drawSubtitle draws a cue (lines separated by "\n") over a frame of size in
// the resolved style, scaled like the montage scales it (sizes are px of a
// 1080p frame's short side).
func drawSubtitle(gtx C, th *gui.Theme, p media.SubtitlePreset, cue string, size image.Point, vertical bool) {
	W, H := size.X, size.Y
	k := float32(min(W, H)) / 1080
	fs := p.Size
	if vertical && p.SizeVertical > 0 {
		fs = p.SizeVertical
	}
	if fs <= 0 {
		fs = 64
	}
	px := max(7, float32(fs)*k)
	st := th.Body.Weighted(gui.FontMedium)
	if p.Bold {
		st = th.Body.Weighted(gui.FontBold)
	}
	st.Size = pxToSp(gtx, px)
	st.LineHeight = 1.15
	marginLR := int(0.06 * float32(W))
	maxW := W - 2*marginLR
	lines := wrapWords(gtx, th, st, cue, maxW)
	type ln struct {
		d                  D
		call, oCall, sCall op.CallOp
	}
	fg := parseHex(p.Color)
	oc := parseHex(p.OutlineColor)
	sc := color.NRGBA{A: uint8(255 * (1 - clamp32(float32(p.ShadowAlpha), 0, 1)))}
	var laid []ln
	total := 0
	for _, l := range lines {
		g := gtx
		g.Constraints = layout.Constraints{Max: pt(maxW, H)}
		d, call := measure(g, gui.Text{Style: st, Color: fg, MaxLines: 1}.Widget(th, l))
		_, oCall := measure(g, gui.Text{Style: st, Color: oc, MaxLines: 1}.Widget(th, l))
		_, sCall := measure(g, gui.Text{Style: st, Color: sc, MaxLines: 1}.Widget(th, l))
		laid = append(laid, ln{d, call, oCall, sCall})
		total += d.Size.Y
	}
	var y0 int
	switch p.Position {
	case "middle":
		y0 = (H - total) / 2
	case "top":
		mv := 0.06 * float32(H)
		if vertical {
			mv = 0.14 * float32(H)
		}
		y0 = int(mv)
	default:
		mv := 0.08 * float32(H)
		if vertical {
			mv = 0.3 * float32(H)
		}
		y0 = H - int(mv) - total
	}
	outline := float32(p.Outline) * k
	shadow := float32(p.Shadow) * k
	y := y0
	for _, l := range laid {
		x := (W - l.d.Size.X) / 2
		if p.Box {
			pad := max(1, float32(p.BoxPad)*k)
			bc := parseHex(p.BoxColor)
			bc.A = uint8(255 * (1 - clamp32(float32(p.BoxAlpha), 0, 1)))
			r := image.Rect(x-int(pad), y-int(pad*0.5), x+l.d.Size.X+int(pad), y+l.d.Size.Y+int(pad*0.5))
			fillRR(gtx, r, max(1, int(pad/2)), bc)
		} else {
			if shadow > 0.3 && sc.A > 0 {
				drawShifted(gtx, l.sCall, x, y, shadow, shadow)
			}
			if outline > 0.2 {
				n := 12
				if outline < 1.5 {
					n = 8
				}
				for i := 0; i < n; i++ {
					a := float64(i) / float64(n) * 2 * math.Pi
					drawShifted(gtx, l.oCall, x, y, outline*float32(math.Cos(a)), outline*float32(math.Sin(a)))
				}
			}
		}
		o := op.Offset(pt(x, y)).Push(gtx.Ops)
		l.call.Add(gtx.Ops)
		o.Pop()
		y += l.d.Size.Y
	}
}

// wrapWords breaks s (lines separated by "\n") into lines no wider than
// maxW px in style st, the way libass wraps a long subtitle line.
func wrapWords(gtx C, th *gui.Theme, st gui.TextStyle, s string, maxW int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		for _, w := range strings.Fields(para) {
			next := w
			if cur != "" {
				next = cur + " " + w
			}
			if cur != "" && th.TextWidth(gtx, st, next) > maxW {
				out = append(out, cur)
				cur = w
			} else {
				cur = next
			}
		}
		if cur != "" {
			out = append(out, cur)
		}
	}
	return out
}

// drawShifted replays a text macro (laid out in the outline or shadow
// colour) at (x+dx, y+dy).
func drawShifted(gtx C, call op.CallOp, x, y int, dx, dy float32) {
	st := op.Affine(f32.AffineId().Offset(f32.Pt(float32(x)+dx, float32(y)+dy))).Push(gtx.Ops)
	call.Add(gtx.Ops)
	st.Pop()
}

// drawYouTubeCaption draws a cue the way YouTube shows a .srt: white text
// on a translucent black plate near the bottom.
func drawYouTubeCaption(gtx C, th *gui.Theme, cue string, size image.Point) {
	W, H := size.X, size.Y
	px := max(8, float32(min(W, H))*0.052)
	st := th.Body
	st.Size = pxToSp(gtx, px)
	st.LineHeight = 1.25
	lines := wrapWords(gtx, th, st, strings.ReplaceAll(cue, "\n", " "), W*9/10)
	var ds []D
	var calls []op.CallOp
	total := 0
	for _, l := range lines {
		g := gtx
		g.Constraints = layout.Constraints{Max: pt(W*9/10, H)}
		d, call := measure(g, gui.Text{Style: st, Color: color.NRGBA{R: 255, G: 255, B: 255, A: 255}, MaxLines: 1}.Widget(th, strings.TrimSpace(l)))
		ds, calls = append(ds, d), append(calls, call)
		total += d.Size.Y
	}
	y := H - int(0.07*float32(H)) - total
	for i, d := range ds {
		x := (W - d.Size.X) / 2
		pad := int(px * 0.3)
		gui.FillRect(gtx, image.Rect(x-pad, y, x+d.Size.X+pad, y+d.Size.Y), color.NRGBA{A: 190})
		o := op.Offset(pt(x, y)).Push(gtx.Ops)
		calls[i].Add(gtx.Ops)
		o.Pop()
		y += d.Size.Y
	}
}

// drawPresetSample draws "Аа Бб" in a preset's look over a dim picture.
func drawPresetSample(gtx C, th *gui.Theme, p media.SubtitlePreset, img paint.ImageOp, size image.Point) {
	drawImage(gtx, img, size, true)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 20, G: 14, B: 10, A: 90}, clip.Rect(image.Rectangle{Max: size}).Op())
	q := p
	q.Position = "middle"
	// A fixed readable size for the sample; its look (colours, outline,
	// plate) is the point. Sizes are px of a 1080p short side.
	k := float32(min(size.X, size.Y)) / 1080
	f := float64(float32(size.Y) * 0.3 / k / max(1, float32(p.Size)))
	q.Size = int(float64(p.Size) * f)
	q.SizeVertical = 0
	q.Outline = p.Outline * f * 0.7
	q.Shadow = p.Shadow * f * 0.7
	q.BoxPad = p.BoxPad * f * 0.7
	sample := "Аа Бб"
	if p.Uppercase {
		sample = "АА ББ"
	}
	drawSubtitle(gtx, th, q, sample, size, false)
}

// ---- cue splitting (a port of media's unexported srt cues) -------------------------------------------

// splitAfterPunct splits on whitespace that follows . ! ? … ; : ,
func splitAfterPunct(s string) []string {
	var out []string
	r := []rune(s)
	start := 0
	for i := 0; i < len(r); i++ {
		if !unicode.IsSpace(r[i]) || i == 0 || !strings.ContainsRune(".!?…;:,", r[i-1]) {
			continue
		}
		j := i
		for j < len(r) && unicode.IsSpace(r[j]) {
			j++
		}
		out = append(out, string(r[start:i]))
		start = j
		i = j - 1
	}
	return append(out, string(r[start:]))
}

// cues breaks a line into short readable cues (≤ ~2 lines of ~42 chars).
func cues(s string, maxChars int) []string {
	rl := func(s string) int { return len([]rune(s)) }
	var merged []string
	cur := ""
	for _, part := range splitAfterPunct(s) {
		next := part
		if cur != "" {
			next = cur + " " + part
		}
		if cur != "" && rl(next) > maxChars {
			merged = append(merged, cur)
			cur = part
		} else {
			cur = next
		}
	}
	if cur != "" {
		merged = append(merged, cur)
	}
	var out []string
	for _, c := range merged {
		if float64(rl(c)) <= float64(maxChars)*1.3 {
			out = append(out, c)
			continue
		}
		buf := ""
		for _, word := range strings.Fields(c) {
			if buf != "" && rl(strings.TrimSpace(buf+" "+word)) > maxChars {
				out = append(out, buf)
				buf = word
			} else {
				buf = strings.TrimSpace(buf + " " + word)
			}
		}
		if buf != "" {
			out = append(out, buf)
		}
	}
	return out
}

// wrap2 splits a cue into two lines near the middle when it is wider than width.
func wrap2(s string, width int) string {
	r := []rune(s)
	n := len(r)
	if n <= width {
		return s
	}
	from := min(n-1, (n+1)/2+6)
	mid := -1
	for i := from; i >= 0; i-- {
		if r[i] == ' ' {
			mid = i
			break
		}
	}
	if mid <= 0 {
		return s
	}
	return string(r[:mid]) + "\n" + string(r[mid+1:])
}

// cueAt returns the subtitle on screen at film time sec (spread over each
// scene's line like the montage does), "" when none.
func (s *proj) cueAt(pv *core.ProjectView, sec float64, upper bool) string {
	for _, it := range s.film.items {
		sc := sceneOf(pv, it.SceneID)
		if sc == nil {
			continue
		}
		text := strings.TrimSpace(sc.Narration)
		if text == "" {
			continue
		}
		span := it.VoiceDuration
		if span <= 0 {
			span = math.Max(1.5, it.Duration-(it.VoiceStart-it.Start)-it.Overlap-0.35)
		}
		if sec < it.VoiceStart || sec >= it.VoiceStart+span {
			continue
		}
		if upper {
			text = strings.ToUpper(text)
		}
		parts := cues(text, 84)
		total := 0
		for _, c := range parts {
			total += len([]rune(c))
		}
		at := it.VoiceStart
		for _, c := range parts {
			d := span * float64(len([]rune(c))) / float64(max(1, total))
			if sec < at+d {
				return wrap2(c, 42)
			}
			at += d
		}
	}
	return ""
}

var _ = text.Middle
