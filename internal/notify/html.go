package notify

import (
	"strings"
	"unicode/utf8"
)

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// Escape makes plain text safe inside an HTML-mode message (the entities
// Telegram supports: &amp; &lt; &gt; &quot;).
func Escape(s string) string { return htmlEscaper.Replace(s) }

// ClipHTML shortens Telegram HTML to at most max visible characters: tags do
// not count, an entity counts as one character and characters outside the
// Basic Multilingual Plane (emoji) as two, like Telegram counts them. When
// something is cut the text ends with "…" and the tags left open are closed.
func ClipHTML(s string, max int) string {
	if max < 1 || visibleLen(s) <= max {
		return s
	}
	var b strings.Builder
	var open []string
	n := 0
	for i := 0; i < len(s); {
		if l := tagLen(s[i:]); l > 0 {
			tag := s[i : i+l]
			b.WriteString(tag)
			open = trackTag(open, tag)
			i += l
			continue
		}
		size, width := tokenAt(s[i:])
		if n+width > max-1 { // keep room for "…"
			break
		}
		b.WriteString(s[i : i+size])
		n += width
		i += size
	}
	b.WriteString("…")
	for k := len(open) - 1; k >= 0; k-- {
		b.WriteString("</" + open[k] + ">")
	}
	return b.String()
}

// visibleLen counts what Telegram shows (see ClipHTML).
func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if l := tagLen(s[i:]); l > 0 {
			i += l
			continue
		}
		size, width := tokenAt(s[i:])
		n += width
		i += size
	}
	return n
}

// tagLen is the length of the tag s starts with ("<b>", "</code>",
// `<a href="…">`), 0 when s does not start with one.
func tagLen(s string) int {
	if len(s) < 3 || s[0] != '<' {
		return 0
	}
	c := s[1]
	if c == '/' && len(s) > 2 {
		c = s[2]
	}
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
		return 0
	}
	if j := strings.IndexByte(s, '>'); j > 0 {
		return j + 1
	}
	return 0
}

// tokenAt measures the visible unit s starts with: an entity (&amp;) or one
// rune. width is in UTF-16 code units.
func tokenAt(s string) (size, width int) {
	if s[0] == '&' {
		if j := strings.IndexByte(s, ';'); j > 1 && j <= 10 && !strings.ContainsAny(s[1:j], " &<") {
			return j + 1, 1
		}
	}
	r, size := utf8.DecodeRuneInString(s)
	if r > 0xFFFF {
		return size, 2
	}
	return size, 1
}

// trackTag updates the stack of open tag names with tag.
func trackTag(open []string, tag string) []string {
	body := strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">")
	if strings.HasPrefix(body, "/") {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(body, "/")))
		for k := len(open) - 1; k >= 0; k-- {
			if open[k] == name {
				return open[:k]
			}
		}
		return open
	}
	if strings.HasSuffix(body, "/") {
		return open // self-closing
	}
	name := body
	if k := strings.IndexAny(name, " \t\n"); k >= 0 {
		name = name[:k]
	}
	return append(open, strings.ToLower(name))
}
