package media

// Subtitle looks: a preset from SubtitlePresets plus the user's overrides,
// written as the style of an ASS script that is burnt in (buildASS). The
// .srt file stays plain text (only Uppercase applies to it as well).

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// SubtitleStyle is the user's subtitle look (mirrors studio.SubtitleStyle).
type SubtitleStyle struct {
	Preset    string // SubtitlePresets id ("" = classic white with outline)
	Size      int    // font size in px on a 1080p frame (0 = the preset's)
	Position  string // "bottom" | "middle" | "top" ("" = the preset's)
	Color     string // text colour #RRGGBB ("" = the preset's)
	Outline   bool   // add an outline around the letters (presets without one)
	Box       bool   // add a semi-transparent plate behind the text
	Uppercase bool   // capital letters — in the burnt subtitles AND the .srt
}

// Resolve merges the overrides into the preset: Size/Position/Color replace
// the preset's values when set; Outline, Box and Uppercase switch the feature
// on (a preset that already has it keeps its own look).
func (s SubtitleStyle) Resolve() SubtitlePreset {
	p, ok := SubtitlePresetByID(s.Preset)
	if !ok {
		p, _ = SubtitlePresetByID(DefaultSubtitlePreset)
	}
	if s.Size > 0 {
		p.Size, p.SizeVertical = s.Size, s.Size
	}
	if pos := strings.ToLower(strings.TrimSpace(s.Position)); pos == "bottom" || pos == "middle" || pos == "top" {
		p.Position = pos
	}
	if _, ok := parseHexColor(s.Color); ok {
		p.Color = s.Color
	}
	if s.Outline && p.Outline < 3 {
		p.Outline = 3
		if _, ok := parseHexColor(p.OutlineColor); !ok || strings.EqualFold(p.OutlineColor, p.Color) {
			p.OutlineColor = "#000000"
		}
	}
	if s.Box && !p.Box {
		p.Box, p.BoxColor, p.BoxAlpha, p.BoxPad = true, "#000000", 0.4, 12
	}
	if s.Uppercase {
		p.Uppercase = true
	}
	return p
}

// parseHexColor reads #RRGGBB (or RRGGBB, #RGB) into r, g, b.
func parseHexColor(s string) ([3]uint8, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return [3]uint8{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return [3]uint8{}, false
	}
	return [3]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

// assColor is an ASS colour &HAABBGGRR; alpha 0 = opaque … 1 = invisible.
func assColor(hex, fallback string, alpha float64) string {
	c, ok := parseHexColor(hex)
	if !ok {
		c, _ = parseHexColor(fallback)
	}
	a := uint8(math.Round(math.Max(0, math.Min(1, alpha)) * 255))
	return fmt.Sprintf("&H%02X%02X%02X%02X", a, c[2], c[1], c[0])
}

// assStyle is the "Default" style of the burnt-in subtitle script for a W×H
// video, as the V4+ Styles format names and the matching values. PlayRes is
// the video size, so every value is in pixels; the 1080p sizes of the preset
// scale with the short side of the frame. (A written script instead of the
// subtitles filter's force_style: libass applies a forced Alignment in the
// legacy SSA numbering, which differs from the numpad one for middle/top.)
func (p SubtitlePreset) assStyle(W, H int) (names, values []string) {
	vertical := H > W
	k := float64(min(W, H)) / 1080
	px := func(v float64) string { return strconv.FormatFloat(math.Round(v*k*10)/10, 'f', -1, 64) }
	size := p.Size
	if vertical && p.SizeVertical > 0 {
		size = p.SizeVertical
	}
	if size <= 0 {
		size = 64
	}
	fontSize := max(8, int(math.Round(float64(size)*k)))

	// Position (numpad alignment): bottom sits above the player controls
	// (and, on vertical video, above the Shorts/Reels caption area); top
	// stays below the title bar.
	align, marginV := 2, 0.08*float64(H)
	switch p.Position {
	case "middle":
		align, marginV = 5, 0
	case "top":
		align, marginV = 8, 0.06*float64(H)
		if vertical {
			marginV = 0.14 * float64(H)
		}
	default:
		if vertical {
			marginV = 0.3 * float64(H)
		}
	}
	marginLR := strconv.Itoa(int(math.Round(0.06 * float64(W))))

	bold := "0"
	if p.Bold {
		bold = "-1"
	}
	border, outlineCol, backCol, outline, shadow := "1",
		assColor(p.OutlineColor, "#000000", 0), assColor("#000000", "#000000", p.ShadowAlpha), px(p.Outline), px(p.Shadow)
	if p.Box {
		// BorderStyle 3: an opaque box per line in OutlineColour, Outline = padding.
		box := assColor(p.BoxColor, "#000000", p.BoxAlpha)
		border, outlineCol, backCol, outline, shadow = "3", box, box, px(math.Max(1, p.BoxPad)), "0"
	}
	fields := [][2]string{
		{"Name", "Default"}, {"Fontname", "Arial"}, {"Fontsize", strconv.Itoa(fontSize)},
		{"PrimaryColour", assColor(p.Color, "#FFFFFF", 0)}, {"SecondaryColour", "&H000000FF"},
		{"OutlineColour", outlineCol}, {"BackColour", backCol},
		{"Bold", bold}, {"Italic", "0"}, {"Underline", "0"}, {"StrikeOut", "0"},
		{"ScaleX", "100"}, {"ScaleY", "100"}, {"Spacing", "0"}, {"Angle", "0"},
		{"BorderStyle", border}, {"Outline", outline}, {"Shadow", shadow},
		{"Alignment", strconv.Itoa(align)},
		{"MarginL", marginLR}, {"MarginR", marginLR}, {"MarginV", strconv.Itoa(int(math.Round(marginV)))},
		{"Encoding", "1"},
	}
	for _, f := range fields {
		names, values = append(names, f[0]), append(values, f[1])
	}
	return names, values
}
