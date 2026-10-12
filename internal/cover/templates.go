package cover

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text is the user's cover design (mirrors studio.CoverText; the cover
// package does not import studio). Empty fields take the template default;
// Stroke "none" switches the outline off.
type Text struct {
	Template string
	Title    string // "*word*" marks words drawn in the highlight colour; "\n" forces a line break
	Subtitle string
	Badge    string
	Font     string
	Color    string // #RRGGBB
	Stroke   string // #RRGGBB, "none" = no outline, "" = template default
	Accent   string // #RRGGBB
	Position string // top | center | bottom | left | right
	Size     int    // title font size in px of the full-size cover (0 = auto)
}

// Spec is a fully resolved design: Text with the template defaults applied.
type Spec struct {
	Template string
	Title    string
	Subtitle string
	Badge    string
	Font     string // catalog id
	Color    string // #RRGGBB
	Stroke   string // #RRGGBB, "" = no outline
	Accent   string // #RRGGBB
	Position string // top | center | bottom | left | right
	Size     int    // title font size cap in px of the full-size cover (0 = auto)
	Look     Look   // the template's visual recipe
}

// Look is the visual recipe of a template. Lengths are in em of the title
// font unless noted; colours are "#RRGGBB", "accent" (Spec.Accent), "text"
// (Spec.Color) or "" (the documented default).
type Look struct {
	Uppercase  bool
	Align      string  // left | center | right; "" = follows the position
	LineHeight float32 // baseline distance (em); lines spread when accents would touch
	Tracking   float32 // extra letter spacing (em)
	MaxSize    float32 // largest title size as a fraction of the canvas height

	StrokeWidth float32 // outline width in the Spec.Stroke colour
	Outer       string  // second, outer outline colour ("" = none)
	OuterWidth  float32

	ShadowX, ShadowY float32 // drop shadow offset
	ShadowBlur       float32
	ShadowOpacity    float32 // 0 = no shadow
	ShadowColor      string  // "" = black

	Glow         string  // glow colour ("" = none)
	GlowRadius   float32 // blur radius
	GlowStrength float32 // 0–1.5

	// Scrim behind the text for readability:
	//	none     — nothing
	//	gradient — dark gradient from the edge nearest the text
	//	band     — full-width band behind the title
	//	plates   — rounded plate behind every title line
	//	box      — tight box behind the title block (news lower third)
	Scrim        string
	ScrimColor   string // "" = black
	ScrimOpacity float32
	Vignette     float32 // 0–1 darkening of the corners

	Highlight string // colour of *marked* words ("" = accent)

	BadgeFill string  // "" = accent
	BadgeText string  // "" = black or white, whichever reads better on the fill
	BadgeTilt float32 // degrees, negative = counter-clockwise
	BadgeFont string  // "" = the title font (script faces use montserrat)

	SubtitleColor  string  // "" = text colour
	SubtitleBox    string  // box colour behind the subtitle ("" = none)
	SubtitleScale  float32 // subtitle size relative to the title (max)
	SubtitleUpper  bool
	SubtitleTrack  float32 // subtitle letter spacing (em of the subtitle)
	SubtitleFont   string  // "" = the title font (script faces use rubik)
	SubtitleStroke bool    // outline the subtitle like the title
}

// TemplateDef is one ready-made design of the catalog.
type TemplateDef struct {
	ID          string
	Label       string // Ukrainian
	Description string // Ukrainian, one sentence
	Shorts      bool   // made for vertical 9:16 covers
	Font        string
	Color       string
	Stroke      string // "" = no outline
	Accent      string
	Position    string
	Look        Look
}

// Templates is the catalog, in display order (the first one is the default
// for horizontal covers, "shorts-top" for vertical ones).
var Templates = []TemplateDef{
	{
		ID: "bottom-bold", Label: "Великий заголовок знизу",
		Description: "Білий жирний текст із чорним контуром унизу кадру, темний градієнт для читабельності",
		Font:        "montserrat", Color: "#FFFFFF", Stroke: "#000000", Accent: "#FFD400", Position: "bottom",
		Look: Look{
			Uppercase: true, LineHeight: 1.0, MaxSize: 0.21,
			StrokeWidth: 0.085,
			ShadowX:     0.02, ShadowY: 0.05, ShadowBlur: 0.07, ShadowOpacity: 0.6,
			Scrim: "gradient", ScrimOpacity: 0.72,
			SubtitleScale: 0.42, SubtitleStroke: true,
		},
	},
	{
		ID: "yellow-shock", Label: "Жовтий шок-бейдж",
		Description: "Жовтий заголовок із товстим контуром і червоний бейдж навскіс — для емоційних історій",
		Font:        "rubik", Color: "#FFE417", Stroke: "#0B0B0B", Accent: "#E5162B", Position: "top",
		Look: Look{
			Uppercase: true, LineHeight: 1.0, MaxSize: 0.22,
			StrokeWidth: 0.11,
			ShadowX:     0.035, ShadowY: 0.06, ShadowBlur: 0.02, ShadowOpacity: 0.75,
			Scrim: "gradient", ScrimOpacity: 0.5,
			Highlight:     "#FFFFFF",
			BadgeTilt:     -5,
			SubtitleColor: "#FFFFFF", SubtitleScale: 0.4, SubtitleUpper: true, SubtitleStroke: true,
		},
	},
	{
		ID: "white-plate", Label: "Біла плашка",
		Description: "Чорний текст на білих плашках, як підписи в соцмережах — читається на будь-якому фоні",
		Font:        "montserrat", Color: "#111111", Accent: "#E4002B", Position: "bottom",
		Look: Look{
			Align: "left", LineHeight: 1.08, MaxSize: 0.15,
			ShadowY: 0.04, ShadowBlur: 0.16, ShadowOpacity: 0.35,
			Scrim: "plates", ScrimColor: "#FFFFFF", ScrimOpacity: 1,
			BadgeText:     "#FFFFFF",
			SubtitleColor: "#FFFFFF", SubtitleBox: "#111111", SubtitleScale: 0.46,
		},
	},
	{
		ID: "red-ribbon", Label: "Червона стрічка",
		Description: "Білий вузький текст на червоній стрічці через увесь кадр",
		Font:        "oswald", Color: "#FFFFFF", Accent: "#E1141E", Position: "bottom",
		Look: Look{
			Uppercase: true, LineHeight: 1.04, Tracking: 0.01, MaxSize: 0.2,
			ShadowX: 0.015, ShadowY: 0.03, ShadowBlur: 0.03, ShadowOpacity: 0.3,
			Scrim: "band", ScrimColor: "accent", ScrimOpacity: 1,
			Highlight: "#FFE417",
			BadgeFill: "#FFFFFF", BadgeText: "accent",
			SubtitleColor: "#FFFFFF", SubtitleBox: "#0E0E0E", SubtitleScale: 0.42, SubtitleUpper: true, SubtitleTrack: 0.04,
		},
	},
	{
		ID: "poster", Label: "Кіно-постер (по центру)",
		Description: "Заголовок із засічками по центру, м'яке світло й віньєтка — як афіша фільму",
		Font:        "alegreya", Color: "#FFF8EC", Accent: "#E9C46A", Position: "center",
		Look: Look{
			Uppercase: true, LineHeight: 1.02, Tracking: 0.035, MaxSize: 0.2,
			ShadowY: 0.03, ShadowBlur: 0.16, ShadowOpacity: 0.85,
			Glow: "#FFE7B8", GlowRadius: 0.35, GlowStrength: 0.35,
			Scrim: "gradient", ScrimOpacity: 0.5, Vignette: 0.7,
			BadgeText:     "#111111",
			SubtitleColor: "accent", SubtitleScale: 0.34, SubtitleUpper: true, SubtitleTrack: 0.2, SubtitleFont: "montserrat",
		},
	},
	{
		ID: "left-gradient", Label: "Ліва колонка з градієнтом",
		Description: "Текст ліворуч на темному градієнті, герой обкладинки лишається праворуч",
		Font:        "montserrat", Color: "#FFFFFF", Accent: "#FFC400", Position: "left",
		Look: Look{
			Uppercase: true, Align: "left", LineHeight: 1.0, MaxSize: 0.2,
			ShadowX: 0.02, ShadowY: 0.04, ShadowBlur: 0.08, ShadowOpacity: 0.55,
			Scrim: "gradient", ScrimOpacity: 0.86,
			SubtitleColor: "#E8E8E8", SubtitleScale: 0.36,
		},
	},
	{
		ID: "minimal", Label: "Мінімал",
		Description: "Чистий білий текст із м'якою тінню — без плашок і контурів",
		Font:        "unbounded", Color: "#FFFFFF", Accent: "#FFFFFF", Position: "bottom",
		Look: Look{
			Align: "left", LineHeight: 1.12, MaxSize: 0.12,
			ShadowY: 0.04, ShadowBlur: 0.18, ShadowOpacity: 0.75,
			Scrim: "gradient", ScrimOpacity: 0.45,
			BadgeText:     "#111111",
			SubtitleColor: "#DDDDDD", SubtitleScale: 0.5, SubtitleFont: "montserrat",
		},
	},
	{
		ID: "shorts-top", Label: "Shorts: заголовок угорі", Shorts: true,
		Description: "Для вертикальних Shorts: великий текст угорі з контуром і жовтими акцентами",
		Font:        "montserrat", Color: "#FFFFFF", Stroke: "#000000", Accent: "#FFE000", Position: "top",
		Look: Look{
			Uppercase: true, LineHeight: 1.0, MaxSize: 0.17,
			StrokeWidth: 0.1,
			ShadowX:     0.02, ShadowY: 0.05, ShadowBlur: 0.06, ShadowOpacity: 0.6,
			Scrim: "gradient", ScrimOpacity: 0.55,
			SubtitleScale: 0.45, SubtitleStroke: true,
		},
	},
	{
		ID: "neon", Label: "Неон",
		Description: "Світний неоновий текст для фантастики, космосу, ігор і технологій",
		Font:        "unbounded", Color: "#F4FDFF", Stroke: "#18E4FF", Accent: "#18E4FF", Position: "center",
		Look: Look{
			Uppercase: true, LineHeight: 1.06, Tracking: 0.02, MaxSize: 0.17,
			StrokeWidth: 0.03,
			Glow:        "accent", GlowRadius: 0.2, GlowStrength: 0.9,
			Scrim: "gradient", ScrimOpacity: 0.55, Vignette: 0.55,
			Highlight:     "#FF3DE5",
			BadgeText:     "#061018",
			SubtitleColor: "accent", SubtitleScale: 0.36, SubtitleUpper: true, SubtitleTrack: 0.12,
		},
	},
	{
		ID: "fairy", Label: "Казка",
		Description: "Рукописний золотий текст із подвійним контуром — для казок і дитячих історій",
		Font:        "pacifico", Color: "#FFE27A", Stroke: "#4A1D6E", Accent: "#FF7EB9", Position: "top",
		Look: Look{
			LineHeight: 1.12, MaxSize: 0.2,
			StrokeWidth: 0.075, Outer: "#FFFFFF", OuterWidth: 0.045,
			ShadowX: 0.02, ShadowY: 0.06, ShadowBlur: 0.08, ShadowOpacity: 0.45,
			Scrim: "gradient", ScrimOpacity: 0.3,
			BadgeText: "#FFFFFF", BadgeTilt: -4, BadgeFont: "rubik",
			SubtitleColor: "#FFFFFF", SubtitleScale: 0.36, SubtitleFont: "rubik", SubtitleStroke: true,
		},
	},
	{
		ID: "news", Label: "Новини / факт",
		Description: "Чорний текст на жовтій плашці й підзаголовок на чорній — як титр у новинах",
		Font:        "oswald", Color: "#0E0E0E", Accent: "#FFD60A", Position: "bottom",
		Look: Look{
			Uppercase: true, Align: "left", LineHeight: 1.0, Tracking: 0.01, MaxSize: 0.17,
			ShadowY: 0.03, ShadowBlur: 0.14, ShadowOpacity: 0.4,
			Scrim: "box", ScrimColor: "accent", ScrimOpacity: 1,
			Highlight: "#C8102E",
			BadgeFill: "#C8102E", BadgeText: "#FFFFFF",
			SubtitleColor: "#FFFFFF", SubtitleBox: "#0E0E0E", SubtitleScale: 0.4, SubtitleUpper: true, SubtitleTrack: 0.03,
		},
	},
	{
		ID: "horror", Label: "Моторошний",
		Description: "Криваво-червоний текст із темним світінням і віньєткою — для хорору й таємниць",
		Font:        "alegreya", Color: "#E3141C", Stroke: "#050505", Accent: "#E3141C", Position: "center",
		Look: Look{
			Uppercase: true, LineHeight: 1.02, Tracking: 0.02, MaxSize: 0.2,
			StrokeWidth: 0.055,
			ShadowY:     0.04, ShadowBlur: 0.1, ShadowOpacity: 0.8,
			Glow: "#7A0006", GlowRadius: 0.3, GlowStrength: 0.9,
			Scrim: "gradient", ScrimOpacity: 0.55, Vignette: 0.85,
			Highlight: "#F2F2F2",
			BadgeFill: "#050505", BadgeText: "accent",
			SubtitleColor: "#EDEDED", SubtitleScale: 0.34, SubtitleUpper: true, SubtitleTrack: 0.2, SubtitleFont: "oswald",
		},
	},
}

// DefaultTemplate is the template used when none is chosen.
func DefaultTemplate(vertical bool) string {
	if vertical {
		return "shorts-top"
	}
	return Templates[0].ID
}

// TemplateByID finds a template of the catalog.
func TemplateByID(id string) (TemplateDef, bool) {
	for _, t := range Templates {
		if t.ID == id {
			return t, true
		}
	}
	return TemplateDef{}, false
}

// Positions of the text block, in UI order.
var Positions = []struct{ ID, Label string }{
	{"top", "Угорі"},
	{"center", "По центру"},
	{"bottom", "Унизу"},
	{"left", "Ліворуч"},
	{"right", "Праворуч"},
}

func knownPosition(p string) bool {
	for _, x := range Positions {
		if x.ID == p {
			return true
		}
	}
	return false
}

// Limits of the user's text (runes).
const (
	MaxTitle    = 120
	MaxSubtitle = 120
	MaxBadge    = 24
	minSize     = 12
	maxSize     = 400
)

// ResolveSpec applies the template defaults to t and cleans it up. An unknown
// template falls back to the default one; invalid colours, fonts and
// positions take the template's values.
func ResolveSpec(t Text) Spec {
	tpl, ok := TemplateByID(strings.TrimSpace(t.Template))
	if !ok {
		tpl = Templates[0]
	}
	s := Spec{
		Template: tpl.ID,
		Title:    cleanText(t.Title, MaxTitle, true),
		Subtitle: cleanText(t.Subtitle, MaxSubtitle, false),
		Badge:    cleanText(t.Badge, MaxBadge, false),
		Font:     tpl.Font,
		Color:    tpl.Color,
		Stroke:   tpl.Stroke,
		Accent:   tpl.Accent,
		Position: tpl.Position,
		Look:     tpl.Look,
	}
	if f := strings.TrimSpace(t.Font); f != "" {
		if _, ok := FontByID(f); ok {
			s.Font = f
		}
	}
	if c, ok := normHex(t.Color); ok {
		s.Color = c
	}
	switch st := strings.ToLower(strings.TrimSpace(t.Stroke)); st {
	case "":
	case "none", "transparent", "-":
		s.Stroke = ""
	default:
		if c, ok := normHex(st); ok {
			s.Stroke = c
		}
	}
	if c, ok := normHex(t.Accent); ok {
		s.Accent = c
	}
	if p := strings.ToLower(strings.TrimSpace(t.Position)); knownPosition(p) {
		s.Position = p
	}
	if t.Size > 0 {
		s.Size = max(minSize, min(maxSize, t.Size))
	}
	if s.Stroke != "" && s.Look.StrokeWidth <= 0 {
		// A user-chosen outline on a template without one.
		s.Look.StrokeWidth = 0.07
	}
	return s
}

// cleanText trims, collapses runs of spaces and clips to max runes. Line
// breaks survive only in the title (at most MaxLinesShorts lines; the
// renderer joins the extra ones on horizontal covers).
func cleanText(s string, limit int, keepBreaks bool) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		// No-break spaces survive: they glue words on one line.
		words := strings.FieldsFunc(l, func(r rune) bool { return unicode.IsSpace(r) && r != 0xa0 && r != 0x202f })
		if l = strings.Join(words, " "); l != "" {
			out = append(out, l)
		}
	}
	sep := " "
	if keepBreaks {
		sep = "\n"
		if len(out) > MaxLinesShorts {
			out = append(out[:MaxLinesShorts-1], strings.Join(out[MaxLinesShorts-1:], " "))
		}
	}
	s = strings.Join(out, sep)
	if utf8.RuneCountInString(s) > limit {
		s = strings.TrimSpace(string([]rune(s)[:limit]))
	}
	return s
}
