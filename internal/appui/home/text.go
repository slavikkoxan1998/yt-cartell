package home

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// TitleFrom names a project after the first words of its story: the first
// sentence, at most six words and 48 characters, capitalised, without the
// trailing punctuation. "" for an empty story.
func TitleFrom(story string) string {
	s := strings.TrimSpace(story)
	if s == "" {
		return ""
	}
	// First line, then the first sentence of it.
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	for i, r := range s {
		if (r == '.' || r == '!' || r == '?' || r == '…') && i > 0 {
			s = s[:i]
			break
		}
	}
	words := strings.Fields(s)
	if len(words) > 6 {
		words = words[:6]
	}
	out := ""
	for _, w := range words {
		next := strings.TrimSpace(out + " " + w)
		if utf8.RuneCountInString(next) > 48 {
			break
		}
		out = next
	}
	if out == "" && len(words) > 0 {
		r := []rune(words[0])
		out = string(r[:min(len(r), 48)])
	}
	out = strings.TrimRightFunc(out, func(r rune) bool {
		return unicode.IsPunct(r) && r != '»' && r != ')' && r != '"' || unicode.IsSpace(r)
	})
	out = strings.TrimLeftFunc(out, func(r rune) bool { return unicode.IsPunct(r) && r != '«' && r != '(' && r != '"' })
	r := []rune(out)
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// decodeText turns a text file into a string: UTF-8 (with or without a BOM),
// UTF-16 with a BOM (Notepad's "Unicode"), else Windows-1251 — the usual
// encoding of older Ukrainian .txt files. Line ends become "\n".
func decodeText(b []byte) string {
	var s string
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s = string(b[3:])
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		s = string(utf16.Decode(u))
	case utf8.Valid(b):
		s = string(b)
	default:
		if d, err := charmap.Windows1251.NewDecoder().Bytes(b); err == nil {
			s = string(d)
		} else {
			s = strings.ToValidUTF8(string(b), "�")
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// languageLabel is the Ukrainian name of an audience language ("" = Авто).
func languageLabel(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Мова: авто"
	}
	if l, ok := studio.LanguageByCode(code); ok {
		return l.Label
	}
	return code
}

// languageItems are the rows of the language chooser: "Авто" first, then
// every audience language with its self-name.
func languageItems() []popItem {
	items := make([]popItem, 0, len(studio.Languages)+1)
	items = append(items, popItem{Value: autoLang, Label: pickers.AutoLanguage, Icon: gui.IconSparkles})
	for _, l := range studio.Languages {
		hint := pickers.NativeName(l) // a self-name the app fonts can draw
		if hint == l.Label {
			hint = ""
		}
		items = append(items, popItem{Value: l.Code, Label: l.Label, Hint: hint})
	}
	return items
}

// durationLabel is a length in words ("1 хв 30 с").
func durationLabel(sec int) string { return core.Duration(sec) }

// words counts the words of a text.
func words(s string) int { return len(strings.Fields(s)) }

// autoLang is the chooser's value for "Авто" (the model keeps the story's
// language; stored as "").
const autoLang = "auto"
