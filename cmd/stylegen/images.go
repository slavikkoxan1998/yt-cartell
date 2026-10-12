package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // raw decoders
	"log"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"

	"ytcartell/internal/studio"
)

// The embedded preview: 640×480 (the gallery cards are 4:3), JPEG at
// quality 82, stepped down (not below minQuality) to stay near maxBytes.
const (
	outW, outH = 640, 480
	quality    = 82
	minQuality = 68
	maxBytes   = 60 << 10
)

// runProcess turns the raw pictures into the embedded previews and deletes
// previews of ids that are no longer built-in styles.
func runProcess(o opts) error {
	styles, err := selectStyles(o.ids)
	if err != nil {
		return err
	}
	if st, err := os.Stat(o.assets); err != nil || !st.IsDir() {
		return fmt.Errorf("previews folder %s not found (run from the module root)", o.assets)
	}
	rawDir := filepath.Join(o.work, "raw")
	var missing []string
	total, n := 0, 0
	for _, s := range styles {
		src := rawFile(rawDir, s.ID)
		if src == "" {
			missing = append(missing, s.ID)
			continue
		}
		img, err := decodeFile(src)
		if err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		out, q, err := previewJPEG(img)
		if err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		if err := writeAtomic(filepath.Join(o.assets, s.ID+".jpg"), out); err != nil {
			return err
		}
		b := img.Bounds()
		log.Printf("%-22s %4dx%-4d → %dx%d q%d %5.1f KB", s.ID, b.Dx(), b.Dy(), outW, outH, q, float64(len(out))/1024)
		total += len(out)
		n++
	}

	known := map[string]bool{}
	for _, s := range studio.Styles {
		known[s.ID] = true
	}
	entries, err := os.ReadDir(o.assets)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if id, ok := strings.CutSuffix(name, ".jpg"); ok && !e.IsDir() && !known[id] {
			if err := os.Remove(filepath.Join(o.assets, name)); err != nil {
				return err
			}
			log.Printf("removed stale preview %s", name)
		}
	}
	log.Printf("%d previews written, %.1f KB (avg %.1f KB)", n, float64(total)/1024, float64(total)/1024/float64(max(n, 1)))
	if len(missing) > 0 {
		return fmt.Errorf("no raw picture yet for %d styles: %s", len(missing), strings.Join(missing, ", "))
	}
	return nil
}

func decodeFile(path string) (image.Image, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

// crop43 is the largest centred 4:3 rectangle of b.
func crop43(b image.Rectangle) image.Rectangle {
	w, h := b.Dx(), b.Dy()
	if w*3 > h*4 { // wider than 4:3: trim the sides
		nw := (h*4 + 1) / 3
		x0 := b.Min.X + (w-nw)/2
		return image.Rect(x0, b.Min.Y, x0+nw, b.Max.Y)
	}
	nh := (w*3 + 2) / 4
	y0 := b.Min.Y + (h-nh)/2
	return image.Rect(b.Min.X, y0, b.Max.X, y0+nh)
}

// previewJPEG centre-crops src to 4:3, resizes it to outW×outH (Catmull-Rom)
// and encodes it, lowering the quality until it fits maxBytes.
func previewJPEG(src image.Image) ([]byte, int, error) {
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src) // under any transparency
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop43(src.Bounds()), xdraw.Over, nil)
	q := quality
	for {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: q}); err != nil {
			return nil, 0, err
		}
		if buf.Len() <= maxBytes || q <= minQuality {
			return buf.Bytes(), q, nil
		}
		q -= 2
	}
}

// ---- contact sheets ---------------------------------------------------------------------------------

type sheetTile struct {
	n   int
	s   studio.StyleDef
	img image.Image // nil = missing
}

// runSheet draws the previews with their number, id and label: one sheet of
// all styles and pages of 12 big enough to judge details.
func runSheet(o opts) error {
	styles, err := selectStyles(o.ids)
	if err != nil {
		return err
	}
	regular, err := face(goregular.TTF, 15)
	if err != nil {
		return err
	}
	bold, err := face(gobold.TTF, 15)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.work, 0o755); err != nil {
		return err
	}
	var tiles []sheetTile
	missing := 0
	for _, s := range styles {
		n := 0
		for i, all := range studio.Styles {
			if all.ID == s.ID {
				n = i + 1
			}
		}
		img, err := decodeFile(filepath.Join(o.assets, s.ID+".jpg"))
		if err != nil {
			missing++
			img = nil
		}
		tiles = append(tiles, sheetTile{n: n, s: s, img: img})
	}
	save := func(name string, img image.Image) error {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
			return err
		}
		p := filepath.Join(o.work, name)
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			return err
		}
		log.Printf("wrote %s (%d×%d, %d KB)", p, img.Bounds().Dx(), img.Bounds().Dy(), buf.Len()>>10)
		return nil
	}
	if err := save("contact-sheet.jpg", drawSheet(tiles, 8, 240, 180, regular, bold)); err != nil {
		return err
	}
	const per = 12
	for i := 0; i < len(tiles); i += per {
		page := tiles[i:min(i+per, len(tiles))]
		if err := save(fmt.Sprintf("sheet-%d.jpg", i/per+1), drawSheet(page, 4, 384, 288, regular, bold)); err != nil {
			return err
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d styles have no preview", missing)
	}
	return nil
}

func face(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// drawSheet lays tiles out in cols columns of tw×th pictures, each with a
// two-line caption ("NN id" and the gallery label).
func drawSheet(tiles []sheetTile, cols, tw, th int, regular, bold font.Face) *image.RGBA {
	const gap, labelH = 8, 42
	rows := (len(tiles) + cols - 1) / cols
	sheet := image.NewRGBA(image.Rect(0, 0, gap+cols*(tw+gap), gap+rows*(th+labelH+gap)))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.RGBA{0x1E, 0x1E, 0x22, 0xFF}), image.Point{}, draw.Src)
	text := func(f font.Face, col color.Color, x, y int, s string) {
		d := font.Drawer{Dst: sheet, Src: image.NewUniform(col), Face: f, Dot: fixed.P(x, y)}
		d.DrawString(s)
	}
	for i, t := range tiles {
		x := gap + (i%cols)*(tw+gap)
		y := gap + (i/cols)*(th+labelH+gap)
		r := image.Rect(x, y, x+tw, y+th)
		if t.img != nil {
			xdraw.CatmullRom.Scale(sheet, r, t.img, t.img.Bounds(), xdraw.Src, nil)
		} else {
			draw.Draw(sheet, r, image.NewUniform(color.RGBA{0x80, 0x10, 0x10, 0xFF}), image.Point{}, draw.Src)
			text(bold, color.White, x+10, y+th/2, "MISSING")
		}
		text(bold, color.RGBA{0xF2, 0xF2, 0xF2, 0xFF}, x+2, y+th+17, fmt.Sprintf("%02d  %s", t.n, t.s.ID))
		text(regular, color.RGBA{0xB8, 0xB8, 0xC0, 0xFF}, x+2, y+th+36, t.s.Label)
	}
	return sheet
}
