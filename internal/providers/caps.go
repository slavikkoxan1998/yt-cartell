package providers

import (
	"slices"
	"strings"
)

// VideoCaps is what a video model accepts. The engine picks clip lengths,
// aspect ratios and the request mode from it.
type VideoCaps struct {
	// Lengths are the clip lengths in seconds the model renders, ascending.
	Lengths []int
	// MaxRefs is how many reference images components mode takes (0 = none).
	MaxRefs int
	// StartFrame: image-to-video from a start frame.
	StartFrame bool
	// Components: reference-images mode (gen.ModeComponents).
	Components bool
	// Aspects are the accepted aspect ratios ("16:9", "9:16", …).
	Aspects []string
}

// ImageCaps is what an image model accepts.
type ImageCaps struct {
	// MaxRefs is how many reference images one request takes (0 = text only).
	MaxRefs int
	// Aspects are the accepted aspect ratios ("16:9", "9:16", "1:1", …).
	Aspects []string
}

// Caps holds the capabilities of a preset's default model: Image for image
// presets, Video for video presets (the other one stays zero).
type Caps struct {
	Image ImageCaps
	Video VideoCaps
}

// Snap returns the allowed length closest to sec (ties → the longer one, so a
// voice line is never cut). No known lengths (an unknown model) → any whole
// second: sec rounded. sec <= 0 → fallback.
func (c VideoCaps) Snap(sec float64, fallback int) int {
	if sec <= 0 {
		return fallback
	}
	if len(c.Lengths) == 0 {
		return max(1, int(sec+0.5))
	}
	best := c.Lengths[0]
	for _, l := range c.Lengths[1:] {
		if d, bd := absf(float64(l)-sec), absf(float64(best)-sec); d < bd || d == bd && l > best {
			best = l
		}
	}
	return best
}

// Cover returns the shortest allowed length that is at least sec, or the
// longest one when none is (dynamic per-scene clip lengths). No lengths →
// fallback.
func (c VideoCaps) Cover(sec float64, fallback int) int {
	if len(c.Lengths) == 0 {
		return fallback
	}
	for _, l := range c.Lengths {
		if float64(l) >= sec {
			return l
		}
	}
	return c.Lengths[len(c.Lengths)-1]
}

// Aspect returns a as the model accepts it: a itself when allowed, else the
// allowed ratio with the same orientation, else the first allowed one.
func (c VideoCaps) Aspect(a string) string { return pickAspect(a, c.Aspects) }

// Aspect returns a as the model accepts it (see VideoCaps.Aspect).
func (c ImageCaps) Aspect(a string) string { return pickAspect(a, c.Aspects) }

func pickAspect(a string, allowed []string) string {
	a = strings.TrimSpace(a)
	if len(allowed) == 0 {
		return a
	}
	if slices.Contains(allowed, a) {
		return a
	}
	portrait := a == "9:16" || a == "3:4" || a == "2:3" || a == "4:5"
	for _, cand := range []string{"9:16", "2:3", "3:4", "4:5"} {
		if portrait && slices.Contains(allowed, cand) {
			return cand
		}
	}
	for _, cand := range []string{"16:9", "3:2", "4:3", "1:1"} {
		if !portrait && slices.Contains(allowed, cand) {
			return cand
		}
	}
	return allowed[0]
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
