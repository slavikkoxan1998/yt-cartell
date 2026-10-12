package providers

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"math"

	_ "image/gif" // decoders for image.Decode
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"ytcartell/internal/gen"
)

// fitImage keeps an input image within limit bytes (an API's upload cap):
// images already small enough are returned untouched, others are re-encoded
// as JPEG and scaled down step by step until they fit.
func fitImage(r gen.RefImage, limit int) (gen.RefImage, error) {
	if limit <= 0 || len(r.Data) <= limit {
		return r, nil
	}
	img, _, err := image.Decode(bytes.NewReader(r.Data))
	if err != nil {
		return r, fmt.Errorf("зображення завелике (%d МБ) і не читається: %w", len(r.Data)>>20, err)
	}
	for _, q := range []int{90, 82} {
		if b, err := encodeJPEG(img, q); err == nil && len(b) <= limit {
			return gen.RefImage{Data: b, MIME: "image/jpeg", Name: jpegName(r.Name)}, nil
		}
	}
	for scale := 0.8; scale > 0.1; scale *= 0.8 {
		b := img.Bounds()
		w, h := int(math.Round(float64(b.Dx())*scale)), int(math.Round(float64(b.Dy())*scale))
		if w < 64 || h < 64 {
			break
		}
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		if out, err := encodeJPEG(dst, 85); err == nil && len(out) <= limit {
			return gen.RefImage{Data: out, MIME: "image/jpeg", Name: jpegName(r.Name)}, nil
		}
	}
	return r, fmt.Errorf("зображення завелике (%d МБ) для цього сервісу", len(r.Data)>>20)
}

func encodeJPEG(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
	return buf.Bytes(), err
}

// jpegName swaps the extension of a ref name to .jpg (keeps @name binding).
func jpegName(name string) string {
	if name == "" {
		return ""
	}
	for i := len(name) - 1; i >= 0 && name[i] != '/'; i-- {
		if name[i] == '.' {
			return name[:i] + ".jpg"
		}
	}
	return name + ".jpg"
}
