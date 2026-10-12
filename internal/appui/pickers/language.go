package pickers

import (
	"image"
	"strings"
	"unicode"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// AutoLanguage is the label of the "" language: the film speaks the language
// the story is written in.
const AutoLanguage = "Авто — як у тексті"

// LanguageLabel is the Ukrainian name of an audience language ("" = Авто;
// an unknown code stays as it is).
func LanguageLabel(code string) string {
	if strings.TrimSpace(code) == "" {
		return AutoLanguage
	}
	if l, ok := studio.LanguageByCode(code); ok {
		return l.Label
	}
	return code
}

// LanguageBadge is the short code shown on language squares ("UK", "EN").
func LanguageBadge(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return "A"
	}
	return strings.ToUpper(code)
}

// Language is the language field (value = studio.Language.Code, "" = Авто).
type Language struct {
	pick[string]
	Look
	// NoAuto hides the "Авто" option (a translation needs a real language).
	NoAuto bool

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  list
	opts []option
}

func (s *Language) options() []option {
	if len(s.opts) > 0 {
		return s.opts
	}
	if !s.NoAuto {
		s.opts = append(s.opts, option{Value: "", Label: AutoLanguage, Icon: gui.IconSparkles, Search: "auto авто"})
	}
	for _, l := range studio.Languages {
		s.opts = append(s.opts, option{Value: l.Code, Label: l.Label, Hint: NativeName(l), Badge: LanguageBadge(l.Code),
			Search: l.Native + " " + l.English + " " + l.Code})
	}
	return s.opts
}

// Layout draws the field.
func (s *Language) Layout(gtx core.C, c *core.Ctx, label, value string) core.D {
	th := c.Th
	value = s.shown(gtx, value)
	st := listStyle{Options: s.options(), Value: value, Search: true, Hint: "Пошук мови", Lead: true, MinWidth: 320}
	if v, ok := s.pop.update(gtx, &s.btn, st); ok {
		s.set(gtx, v)
		value = v
		st.Value = v
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		if s.pop.open {
			s.pop.hide(gtx, &s.btn)
		} else {
			s.pop.openAt(gtx, &s.btn, st)
		}
	}
	caption := "мова історії, яку ти напишеш"
	vis := visIcon(th, gui.IconLanguages, s.Flat)
	if l, ok := studio.LanguageByCode(value); ok && value != "" {
		caption = NativeName(l)
		vis = visText(th, LanguageBadge(l.Code), 15, s.Flat)
	} else if value != "" {
		caption = ""
	}
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: LanguageLabel(value), Caption: caption, Visual: vis, Flat: s.Flat,
		Open: s.pop.open, Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	return fieldWithPopover(gtx, th, t, &s.btn, &s.chev, &s.fx, func(gtx C, field image.Point) {
		s.pop.layout(gtx, th, field, st)
	})
}

// Open opens the list (as if the field was pressed).
func (s *Language) Open() { s.pop.openNow() }

// NativeName is a language's self-name when the app fonts can draw it
// (Latin, Cyrillic, Greek), else its English name — never boxes.
func NativeName(l studio.Language) string {
	for _, r := range l.Native {
		if !unicode.In(r, unicode.Latin, unicode.Cyrillic, unicode.Greek, unicode.Common, unicode.Inherited) {
			return l.English
		}
	}
	return l.Native
}

// Close closes the list.
func (s *Language) Close() { s.pop.open = false }
