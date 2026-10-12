package update

import (
	"regexp"
	"strings"
)

// BlockKind is the kind of one block of release notes.
type BlockKind uint8

const (
	Para    BlockKind = iota // a paragraph
	Bullet                   // a list item (Marker "" = a dot, "1." = numbered)
	Heading                  // a Markdown heading
)

// Block is one block of release notes as plain text.
type Block struct {
	Kind   BlockKind
	Text   string
	Marker string // Bullet: "" or "1."
	Level  int    // Bullet: 0, or 1 when nested
}

var (
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	reBullet   = regexp.MustCompile(`^(\s*)(?:[-*+•]|(\d{1,3})[.)])\s+(.*)$`)
	reRule     = regexp.MustCompile(`^\s*(?:-\s*){3,}$|^\s*(?:\*\s*){3,}$|^\s*(?:_\s*){3,}$`)
	reImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reLink     = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reAutoLink = regexp.MustCompile(`<(https?://[^>\s]+)>`)
	reTag      = regexp.MustCompile(`</?[A-Za-z][^<>]*>`)
	reStrong   = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	reEm       = regexp.MustCompile(`(^|[\s(])[*_]([^*_\s][^*_]*?)[*_]([\s).,!?:;]|$)`)
	reTask     = regexp.MustCompile(`^\[[ xX]\]\s+`)
	reSpaces   = regexp.MustCompile(`[ \t]+`)
)

// ParseNotes turns a release body (GitHub Markdown) into plain blocks:
// headings, paragraphs (wrapped lines joined) and bullets (numbered ones
// keep their number); links keep their text, images, HTML and emphasis
// marks go, code fences become plain paragraphs.
func ParseNotes(md string) []Block {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = reComment.ReplaceAllString(md, "")
	var out []Block
	var para []string
	flush := func() {
		if len(para) > 0 {
			if t := inline(strings.Join(para, " ")); t != "" {
				out = append(out, Block{Kind: Para, Text: t})
			}
			para = nil
		}
	}
	inFence := false
	lastBullet := -1 // index in out of the bullet a wrapped line continues
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			inFence = !inFence
			lastBullet = -1
			continue
		}
		if inFence {
			if trimmed != "" {
				out = append(out, Block{Kind: Para, Text: trimmed})
			}
			continue
		}
		if trimmed == "" {
			flush()
			lastBullet = -1
			continue
		}
		if reRule.MatchString(trimmed) {
			flush()
			lastBullet = -1
			continue
		}
		if m := reHeading.FindStringSubmatch(trimmed); m != nil {
			flush()
			lastBullet = -1
			if t := inline(m[2]); t != "" {
				out = append(out, Block{Kind: Heading, Text: t})
			}
			continue
		}
		if m := reBullet.FindStringSubmatch(line); m != nil {
			flush()
			b := Block{Kind: Bullet, Text: inline(reTask.ReplaceAllString(m[3], ""))}
			if len(strings.ReplaceAll(m[1], "\t", "    ")) >= 2 {
				b.Level = 1
			}
			if m[2] != "" {
				b.Marker = m[2] + "."
			}
			if b.Text != "" {
				out = append(out, b)
				lastBullet = len(out) - 1
			}
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, ">"))
		if lastBullet >= 0 && (line[0] == ' ' || line[0] == '\t') {
			out[lastBullet].Text = strings.TrimSpace(out[lastBullet].Text + " " + inline(trimmed))
			continue
		}
		lastBullet = -1
		para = append(para, trimmed)
	}
	flush()
	return out
}

// inline strips Markdown inline syntax and HTML from s.
func inline(s string) string {
	s = reImage.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$1")
	s = reAutoLink.ReplaceAllString(s, "$1")
	s = reTag.ReplaceAllString(s, "")
	s = reStrong.ReplaceAllString(s, "$1$2")
	s = reEm.ReplaceAllString(s, "$1$2$3")
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "~~", "")
	r := strings.NewReplacer(`\*`, "*", `\_`, "_", `\#`, "#", `\[`, "[", `\]`, "]", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&nbsp;", " ")
	s = r.Replace(s)
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}
