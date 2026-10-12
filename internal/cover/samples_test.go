package cover

// Visual review helpers: with COVER_SAMPLES=<dir> these write contact
// material (glyph sheets per font, every position, typical short titles) to
// look at; without it they are skipped.

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

func TestSampleGlyphSheet(t *testing.T) {
	dir := os.Getenv("COVER_SAMPLES")
	if dir == "" {
		t.Skip()
	}
	for _, f := range Fonts {
		spec := ResolveSpec(Text{Template: "minimal", Font: f.ID, Title: "Ґґ Гг Єє Її Іі Йй ʼ’ «Щ» — №5 ₴", Stroke: "none"})
		spec.Look.Uppercase = false
		img, err := Render(nil, spec, image.Pt(1280, 360))
		if err != nil {
			t.Fatal(err)
		}
		writeJPEG(t, filepath.Join(dir, "glyphs-"+f.ID+".jpg"), img)
	}
}

func TestSamplePositions(t *testing.T) {
	dir := os.Getenv("COVER_SAMPLES")
	if dir == "" {
		t.Skip()
	}
	base := CoverFit(baseFor(t, false), Landscape)
	for _, tpl := range []string{"bottom-bold", "white-plate", "news"} {
		for _, pos := range []string{"top", "center", "bottom", "left", "right"} {
			spec := ResolveSpec(Text{Template: tpl, Position: pos, Title: "Він *врятував* хлопчика в нічному лісі", Subtitle: "Правдива історія з Карпат", Badge: "Нове"})
			c := image.NewRGBA(base.Rect)
			copy(c.Pix, base.Pix)
			if err := Draw(c, spec); err != nil {
				t.Fatal(err)
			}
			// Mark the avoided corner for review.
			_, avoid := SafeArea(Landscape)
			for _, a := range avoid {
				for x := a.Min.X; x < a.Max.X; x++ {
					c.Set(x, a.Min.Y, image.White.C)
				}
				for y := a.Min.Y; y < a.Max.Y; y++ {
					c.Set(a.Min.X, y, image.White.C)
				}
			}
			writeJPEG(t, filepath.Join(dir, "pos-"+tpl+"-"+pos+".jpg"), c)
		}
	}
}

func TestSampleTypicalTitles(t *testing.T) {
	dir := os.Getenv("COVER_SAMPLES")
	if dir == "" {
		t.Skip()
	}
	titles := map[string]string{
		"bottom-bold": "Він *врятував* хлопчика!", "yellow-shock": "*Ніхто* не вірив", "white-plate": "Що сталося в *лісі*?",
		"red-ribbon": "Остання *ніч* у лісі", "poster": "Світлячок", "left-gradient": "Він *світився* до кінця",
		"minimal": "Історія, яку *варто* почути", "shorts-top": "Він *врятував* хлопчика!", "neon": "*Світло* в темряві",
		"fairy": "Казка про *світлячка*", "news": "*Шок*: зник хлопчик", "horror": "Не йди в *ліс*",
	}
	for _, vertical := range []bool{false, true} {
		size, tag := Landscape, "169"
		if vertical {
			size, tag = Portrait, "916"
		}
		base := CoverFit(baseFor(t, vertical), size)
		for _, tpl := range Templates {
			spec := ResolveSpec(Text{Template: tpl.ID, Title: titles[tpl.ID], Badge: "Нове", Subtitle: "Правдива історія"})
			c := image.NewRGBA(base.Rect)
			copy(c.Pix, base.Pix)
			if err := Draw(c, spec); err != nil {
				t.Fatal(err)
			}
			writeJPEG(t, filepath.Join(dir, "typ-"+tag+"-"+tpl.ID+".jpg"), c)
		}
	}
}
