package notify

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoders for image.Decode
	"image/jpeg"
	_ "image/png"
	"net/http"
	"os"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// photoKeep: JPEG/PNG files up to this size are uploaded as they are.
	photoKeep = 1 << 20
	// photoMaxSide bounds a re-encoded photo (Telegram shows ≤ 1280 px anyway).
	photoMaxSide = 1280
	// photoLimit is Telegram's limit for sendPhoto.
	photoLimit = 10 << 20
	// photoReadLimit refuses to load absurdly big files into memory.
	photoReadLimit = 64 << 20
	photoQuality   = 85
)

// ErrPhoto: the picture could not be read or prepared for upload (SendPhoto
// errors match it with errors.Is; the text alone can still be sent).
var ErrPhoto = errors.New("зображення не вдалося підготувати")

type photoFile struct {
	data       []byte
	name, mime string
}

// preparePhoto loads the image at path for sendPhoto: small JPEG/PNG files
// as they are, anything else re-encoded to a JPEG of at most 1280 px.
func preparePhoto(path string) (photoFile, error) {
	fail := func(msg string, err error) (photoFile, error) {
		return photoFile{}, &Error{msg: msg, err: fmt.Errorf("%w: %w", ErrPhoto, err)}
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fail("Не вдалося прочитати зображення для Telegram", err)
	case info.IsDir():
		return fail("Не вдалося прочитати зображення для Telegram", errors.New("це папка"))
	case info.Size() > photoReadLimit:
		return fail("Зображення завелике для Telegram", fmt.Errorf("%d bytes", info.Size()))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fail("Не вдалося прочитати зображення для Telegram", err)
	}
	mime := http.DetectContentType(data)
	plain := mime == "image/jpeg" || mime == "image/png"
	name := "photo.jpg"
	if mime == "image/png" {
		name = "photo.png"
	}
	if plain && len(data) <= photoKeep {
		return photoFile{data: data, name: name, mime: mime}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		if plain && len(data) <= photoLimit { // let Telegram try
			return photoFile{data: data, name: name, mime: mime}, nil
		}
		return fail("Зображення не вдалося підготувати для Telegram", err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, fitPhoto(img, photoMaxSide), &jpeg.Options{Quality: photoQuality}); err != nil {
		return fail("Зображення не вдалося підготувати для Telegram", err)
	}
	return photoFile{data: buf.Bytes(), name: "photo.jpg", mime: "image/jpeg"}, nil
}

// fitPhoto scales img down so its longer side is at most maxSide and puts
// it on an opaque white background (JPEG has no transparency).
func fitPhoto(img image.Image, maxSide int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if long := max(w, h); long > maxSide {
		w = max(1, w*maxSide/long)
		h = max(1, h*maxSide/long)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, xdraw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}
