package pickers

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // the embedded previews
	"math"
	"strconv"
	"strings"
	"sync"

	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/assets"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// StyleLabel is the gallery name of a style id (the id itself when unknown).
func StyleLabel(id string) string {
	if s, ok := studio.StyleByID(id); ok {
		return s.Label
	}
	return id
}

// StylePreview returns the picture of a style for a small thumbnail (≤ 240
// px): its embedded preview (every built-in style has one), else a palette
// poster painted from its three colours (decoded/painted off the UI
// goroutine; ok is false until ready).
func StylePreview(c *core.Ctx, id string) (paint.ImageOp, bool) {
	return StylePreviewAt(c, id, thumbPx)
}

// StylePreviewAt is StylePreview decoded for drawing px pixels wide (the
// longest side): the embedded 640×480 previews are scaled down to the
// display size (in 80 px steps, so nearby sizes share a cache entry) and
// never kept bigger than they are drawn.
func StylePreviewAt(c *core.Ctx, id string, px int) (paint.ImageOp, bool) {
	if c == nil || c.Img == nil || id == "" {
		return paint.ImageOp{}, false
	}
	if hasPreview(id) {
		img, _, ok := c.Img.Load("style:"+id, 0, previewPx(px), func() (image.Image, error) {
			b := assets.StylePreview(id)
			if len(b) == 0 {
				return nil, fmt.Errorf("немає прев'ю стилю «%s»", id)
			}
			img, _, err := image.Decode(bytes.NewReader(b))
			return img, err
		})
		return img, ok
	}
	def, ok := studio.StyleByID(id)
	if !ok {
		return paint.ImageOp{}, false
	}
	pal := def.Palette
	img, _, ok := c.Img.Load("stylepal:"+id, 0, -1, func() (image.Image, error) {
		return palettePoster(240, 180, pal, id), nil
	})
	return img, ok
}

// thumbPx is the decode size of small style thumbnails (fields, profiles).
const thumbPx = 240

// previewPx rounds a display width up to an 80 px step; the natural size
// (-1) from 640 px on.
func previewPx(px int) int {
	if px <= 0 {
		px = thumbPx
	}
	px = (px + 79) / 80 * 80
	if px >= 640 {
		return -1
	}
	return px
}

// galleryCardPx is the width (px) of a card in the style gallery (the kit's
// BigPicker: a dialog ≤ 1160 dp with 40 dp margins and 28 dp padding, cards
// ≥ 200 dp with 16 dp gaps).
func galleryCardPx(gtx core.C, win image.Point) int {
	w := min(gtx.Dp(1160), win.X-2*gtx.Dp(40))
	inner := w - 2*gtx.Dp(28)
	if inner <= 0 {
		return thumbPx
	}
	cols := gui.Grid{MinWidth: galleryMinCard, Gap: gui.Sp16}.Columns(gtx, inner)
	return (inner - gtx.Dp(gui.Sp16)*(cols-1)) / cols
}

// galleryMinCard is the gallery's minimum card width.
const galleryMinCard = 200

var (
	previewOnce sync.Once
	previewIDs  map[string]bool
)

// hasPreview reports an embedded preview picture for a style id.
func hasPreview(id string) bool {
	previewOnce.Do(func() {
		previewIDs = map[string]bool{}
		entries, err := assets.FS.ReadDir("styles")
		if err == nil {
			for _, e := range entries {
				if n := e.Name(); strings.HasSuffix(n, ".jpg") {
					previewIDs[strings.TrimSuffix(n, ".jpg")] = true
				}
			}
		}
	})
	return previewIDs[id]
}

// Palette returns the three palette colours of a style (zero colours for
// unknown ids or bad hex).
func Palette(id string) []color.NRGBA {
	def, ok := studio.StyleByID(id)
	if !ok {
		return nil
	}
	out := make([]color.NRGBA, 0, 3)
	for _, h := range def.Palette {
		if c, ok := parseHex(h); ok {
			out = append(out, c)
		}
	}
	return out
}

func parseHex(s string) (color.NRGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.NRGBA{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, true
}

// palettePoster paints a small illustrated poster from a style palette: a
// soft gradient sky in the first colour, a sun in the second and layered
// hills from the third — the same visual language as the real previews, so
// the gallery reads as one family.
func palettePoster(w, h int, pal [3]string, seedKey string) *image.RGBA {
	c0, _ := parseHex(pal[0])
	c1, _ := parseHex(pal[1])
	c2, _ := parseHex(pal[2])
	seed := 0
	for _, r := range seedKey {
		seed = seed*31 + int(r)
	}
	if seed < 0 {
		seed = -seed
	}
	sky0 := c0
	sky1 := gui.LerpColor(c0, c1, 0.35)
	hill1 := gui.LerpColor(c2, c0, 0.25)
	hill2 := c2
	hill3 := gui.LerpColor(c2, color.NRGBA{A: 255}, 0.35)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fr := float64(seed%97) / 97
	sunX := float64(w) * (0.28 + 0.44*fr)
	sunY := float64(h) * 0.36
	sunR := float64(h) * 0.13
	hill := func(x, amp, base, freq, phase float64) float64 {
		return base + amp*math.Sin(x*freq+phase) + amp*0.4*math.Sin(x*freq*2.3+phase*1.7)
	}
	ph := float64(seed % 13)
	for y := 0; y < h; y++ {
		t := float32(y) / float32(h)
		sky := gui.LerpColor(sky0, sky1, t)
		for x := 0; x < w; x++ {
			c := sky
			dx, dy := float64(x)-sunX, float64(y)-sunY
			if d := math.Hypot(dx, dy); d < sunR {
				c = c1
			} else if d < sunR*2.1 {
				c = gui.LerpColor(c1, c, float32((d-sunR)/(sunR*1.1)))
			}
			fx := float64(x) / float64(w)
			if float64(y) > hill(fx, float64(h)*0.05, float64(h)*0.6, 5, ph) {
				c = hill1
			}
			if float64(y) > hill(fx, float64(h)*0.06, float64(h)*0.72, 3.3, ph+1) {
				c = hill2
			}
			if float64(y) > hill(fx, float64(h)*0.04, float64(h)*0.86, 7, ph+2) {
				c = hill3
			}
			img.SetRGBA(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	return img
}

// Style is the style gallery field (value = studio.StyleDef.ID).
type Style struct {
	pick[string]
	Look

	btn     gui.Clickable
	chev    gui.Anim
	fx      flatFX
	gal     gui.BigPicker
	items   []gui.PickerItem
	itemsPx int // the decode size of the items' pictures
	cats    []string
}

// Layout draws the field.
func (s *Style) Layout(gtx core.C, c *core.Ctx, label, value string) core.D {
	th := c.Th
	value = s.shown(gtx, value)
	if v, ok := s.gal.Changed(gtx); ok {
		s.set(gtx, v)
		value = v
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		s.gal.Open()
	}
	def, known := studio.StyleByID(value)
	name, caption := value, ""
	if known {
		name, caption = def.Label, def.Suits
	} else if value == "" {
		name = "Не вибрано"
	}
	if s.Note != "" {
		caption = s.Note
	}
	thumb := gui.Thumb{Placeholder: gui.IconPalette}
	if img, ok := StylePreview(c, value); ok {
		thumb.Image = img
	} else if known {
		thumb.Loading = true
	}
	t := tile{Label: label, Value: name, Caption: caption, Visual: visThumb(th, thumb), Open: s.gal.IsOpen(), Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	cats := s.categories()
	if win := c.WindowSize(); win.X > 0 && float32(win.X)/gtx.Metric.PxPerDp < 640 {
		// The kit squeezes many category tabs into nothing on a phone: search only.
		cats = []string{}
	}
	s.gal.Layout(gtx, th, gui.PickerStyle{
		Title:             "Стиль кадрів",
		Subtitle:          "Обери, як виглядатимуть усі кадри фільму. Стиль можна змінити й перегенерувати кадри пізніше.",
		Items:             s.pickerItems(gtx, c),
		Categories:        cats,
		Value:             value,
		SearchPlaceholder: "Пошук стилю",
		MinCardWidth:      galleryMinCard,
		Ratio:             4.0 / 3,
	})
	return d
}

func (s *Style) categories() []string {
	if s.cats == nil {
		for _, cat := range studio.StyleCategories {
			s.cats = append(s.cats, cat.Label)
		}
	}
	return s.cats
}

// pickerItems builds the gallery items once and refreshes their pictures
// while the gallery shows (previews decode in the background).
func (s *Style) pickerItems(gtx core.C, c *core.Ctx) []gui.PickerItem {
	if s.items == nil {
		cat := map[string]string{}
		for _, k := range studio.StyleCategories {
			cat[k.ID] = k.Label
		}
		for _, st := range studio.Styles {
			s.items = append(s.items, gui.PickerItem{
				Value: st.ID, Label: st.Label, Caption: st.Suits, Category: cat[st.Category],
				Swatches: Palette(st.ID), Icon: gui.IconPalette,
			})
		}
	}
	if s.gal.IsOpen() {
		px := previewPx(galleryCardPx(gtx, c.WindowSize()))
		if px != s.itemsPx {
			// Another card size: decode again (the cache shows the old
			// pictures meanwhile).
			s.itemsPx = px
			for i := range s.items {
				s.items[i].Image = paint.ImageOp{}
			}
		}
		for i := range s.items {
			if s.items[i].Image.Size().X > 0 {
				continue
			}
			if img, ok := StylePreviewAt(c, s.items[i].Value, px); ok {
				s.items[i].Image = img
			}
		}
	}
	return s.items
}

// Open opens the gallery (as if the field was pressed).
func (s *Style) Open() { s.gal.Open() }

// OpenSearch opens the gallery filtered by query.
func (s *Style) OpenSearch(query string) { s.gal.OpenWith(query) }

// Close closes the gallery.
func (s *Style) Close() { s.gal.Close() }
