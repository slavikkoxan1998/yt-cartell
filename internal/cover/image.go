package cover

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	_ "image/png" // LoadImage
	"io"
	"os"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // LoadImage
)

// MaxBytes is YouTube's size limit for a custom thumbnail.
const MaxBytes = 2 << 20

// maxPixels guards against absurdly large pictures (decompression bombs).
const maxPixels = 60_000_000

// LoadImage decodes a JPEG, PNG or WebP file.
func LoadImage(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("не вдалося прочитати зображення: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("зображення завелике (%d×%d)", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("не вдалося прочитати зображення: %w", err)
	}
	return img, nil
}

// Render draws spec over base (scaled to cover size, centre crop) and returns
// the finished cover. A nil base gets a dark neutral background. size is
// usually SizeFor(aspect) or a smaller preview of the same shape.
func Render(base image.Image, spec Spec, size image.Point) (image.Image, error) {
	if size.X < 32 || size.Y < 32 || size.X > 8192 || size.Y > 8192 {
		return nil, errors.New("cover: unsupported size")
	}
	canvas := CoverFit(base, size)
	if err := Draw(canvas, spec); err != nil {
		return nil, err
	}
	return canvas, nil
}

// CoverFit scales src to fill size (centre crop, Catmull-Rom) on an opaque
// canvas. nil src gives a dark gradient.
func CoverFit(src image.Image, size image.Point) *image.RGBA {
	dst := image.NewRGBA(image.Rectangle{Max: size})
	if src == nil || src.Bounds().Empty() {
		background(dst)
		return dst
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	// Crop of src with the target aspect ratio.
	cw, ch := sw, sw*size.Y/size.X
	if ch > sh {
		ch, cw = sh, sh*size.X/size.Y
	}
	cw, ch = max(1, min(cw, sw)), max(1, min(ch, sh))
	x0 := sb.Min.X + (sw-cw)/2
	y0 := sb.Min.Y + (sh-ch)/2
	crop := image.Rect(x0, y0, x0+cw, y0+ch)
	// Transparent pictures sit on black.
	stddraw.Draw(dst, dst.Rect, image.Black, image.Point{}, stddraw.Src)
	if crop.Size() == size {
		stddraw.Draw(dst, dst.Rect, src, crop.Min, stddraw.Over)
		return dst
	}
	if f := min(cw/size.X, ch/size.Y); f >= 2 {
		// Large reductions: box-average by the integer factor first (fast
		// and alias-free), then the kernel pass for the rest.
		mid := boxReduce(src, crop, f)
		xdraw.CatmullRom.Scale(dst, dst.Rect, mid, mid.Rect, xdraw.Over, nil)
		return dst
	}
	xdraw.CatmullRom.Scale(dst, dst.Rect, src, crop, xdraw.Over, nil)
	return dst
}

// boxReduce averages f×f blocks of the crop of src.
func boxReduce(src image.Image, crop image.Rectangle, f int) *image.RGBA {
	w, h := crop.Dx()/f, crop.Dy()/f
	in := toRGBA(src, crop)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	n := uint32(f * f)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a uint32
			for yy := 0; yy < f; yy++ {
				row := in.Pix[in.PixOffset(x*f, y*f+yy):]
				for xx := 0; xx < f; xx++ {
					p := row[xx*4 : xx*4+4 : xx*4+4]
					r += uint32(p[0])
					g += uint32(p[1])
					b += uint32(p[2])
					a += uint32(p[3])
				}
			}
			o := out.Pix[out.PixOffset(x, y):]
			o[0], o[1], o[2], o[3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
	return out
}

// toRGBA returns the crop of src as an RGBA image starting at 0,0.
func toRGBA(src image.Image, crop image.Rectangle) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok && crop == r.Rect {
		out := *r
		out.Rect = image.Rect(0, 0, crop.Dx(), crop.Dy())
		out.Pix = r.Pix[r.PixOffset(crop.Min.X, crop.Min.Y):]
		return &out
	}
	out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	stddraw.Draw(out, out.Rect, src, crop.Min, stddraw.Src)
	return out
}

// background is the neutral dark gradient used without a picture.
func background(dst *image.RGBA) {
	b := dst.Rect
	top := color.NRGBA{0x2A, 0x2F, 0x3A, 255}
	bot := color.NRGBA{0x0C, 0x0D, 0x11, 255}
	for y := 0; y < b.Dy(); y++ {
		t := float32(y) / float32(max(1, b.Dy()-1))
		r := uint8(float32(top.R)*(1-t) + float32(bot.R)*t)
		g := uint8(float32(top.G)*(1-t) + float32(bot.G)*t)
		bl := uint8(float32(top.B)*(1-t) + float32(bot.B)*t)
		row := dst.Pix[dst.PixOffset(0, y):]
		for x := 0; x < b.Dx(); x++ {
			p := row[x*4 : x*4+4 : x*4+4]
			p[0], p[1], p[2], p[3] = r, g, bl, 255
		}
	}
}

// EncodeJPEG writes img as a JPEG that stays under YouTube's 2 MB limit
// (quality 92, lowered only when needed).
func EncodeJPEG(w io.Writer, img image.Image) error {
	var buf bytes.Buffer
	for _, q := range []int{92, 88, 84, 78, 70, 60} {
		buf.Reset()
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return err
		}
		if buf.Len() <= MaxBytes-4096 {
			break
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}
