package gui

import (
	"image"
	"math"
	"testing"
)

func TestShadowTextureProfile(t *testing.T) {
	white, black := rgb(0xFFFFFF), rgb(0x000000)
	key := shadowKey{rad: 8, sigma: 16, alpha: 200, col: black, ground: white}
	const sigma = 4
	margin := int(math.Ceil(2.5*sigma)) + 1
	tex := shadowTexture(key, sigma, margin)
	img := tex.img
	W, H := tex.size.X, tex.size.Y
	alphaAt := func(x, y int) float32 { return float32(img.Pix[img.PixOffset(x, y)+3]) / 255 }
	want := func(cov float32) float32 { return linearAlpha(white, black, cov) }
	cy := H / 2
	// Deep inside: the full alpha; on the shape's edge: half of it (Φ(0)).
	if got := alphaAt(W/2, cy); math.Abs(float64(got-want(0.2))) > 0.01 {
		t.Errorf("centre alpha = %.3f, want %.3f", got, want(0.2))
	}
	if got := alphaAt(margin, cy); math.Abs(float64(got-want(0.1))) > 0.03 {
		t.Errorf("edge alpha = %.3f, want ≈ %.3f", got, want(0.1))
	}
	if got := alphaAt(0, 0); got > 0.004 {
		t.Errorf("outer corner alpha = %.3f, want ≈ 0", got)
	}
	// Smoothly falling off from the centre outwards along the middle row.
	prev := float32(2)
	for x := W / 2; x >= 0; x-- {
		a := alphaAt(x, cy)
		if a > prev+1.5/255 {
			t.Fatalf("profile rises at x=%d (%.3f > %.3f)", x, a, prev)
		}
		prev = a
	}
}

func TestShadowCacheStableDuringCrossfade(t *testing.T) {
	h := newHarness(t)
	var c Clickable
	draw := func(gtx C) {
		Shadow(gtx, h.th, image.Rect(40, 40, 400, 240), RadiusCard, ElevCard)
		at(gtx, 450, 40, 300, 200, func(gtx C) D {
			return Card{Click: &c}.Layout(gtx, h.th, func(gtx C) D { return D{Size: image.Pt(200, 100)} })
		})
	}
	h.frames(2, draw)
	shadowMu.Lock()
	before := len(shadowCache)
	shadowMu.Unlock()
	h.th.SetMode(ModeDark)
	h.frames(20, draw)
	shadowMu.Lock()
	added := len(shadowCache) - before
	shadowMu.Unlock()
	// Two layers × two shape classes, rebuilt once for the dark palette.
	if added > 8 {
		t.Errorf("crossfade built %d shadow textures, want ≤ 8", added)
	}
}
