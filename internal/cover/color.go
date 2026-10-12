package cover

import (
	"image/color"
	"strings"
)

// normHex validates "#RRGGBB" / "RRGGBB" / "#RGB" and returns "#RRGGBB"
// (upper case).
func normHex(s string) (string, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return "", false
	}
	for i := 0; i < 6; i++ {
		if hexVal(s[i]) < 0 {
			return "", false
		}
	}
	return "#" + strings.ToUpper(s), true
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// parseHex turns "#RRGGBB" into an opaque colour (black when invalid).
func parseHex(s string) color.NRGBA {
	h, ok := normHex(s)
	if !ok {
		return color.NRGBA{A: 255}
	}
	b := func(i int) uint8 { return uint8(hexVal(h[i])<<4 | hexVal(h[i+1])) }
	return color.NRGBA{b(1), b(3), b(5), 255}
}

// luma is the relative luminance (0–1, sRGB weights, no linearisation).
func luma(c color.NRGBA) float32 {
	return (0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B)) / 255
}

// contrastText is black or white, whichever reads better on bg.
func contrastText(bg color.NRGBA) color.NRGBA {
	if luma(bg) > 0.58 {
		return color.NRGBA{0x11, 0x11, 0x11, 255}
	}
	return color.NRGBA{255, 255, 255, 255}
}

// pick resolves a Look colour reference: "accent", "text", "#RRGGBB"; def
// when ref is empty or invalid.
func (s *Spec) pick(ref string, def color.NRGBA) color.NRGBA {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "":
		return def
	case "accent":
		return parseHex(s.Accent)
	case "text":
		return parseHex(s.Color)
	}
	if _, ok := normHex(ref); ok {
		return parseHex(ref)
	}
	return def
}
