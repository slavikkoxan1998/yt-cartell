package media

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// ---- subtitles ----------------------------------------------------------------------

// srtTime formats seconds as HH:MM:SS,mmm.
func srtTime(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int64(math.Round(sec * 1000))
	h := ms / 3600000
	m := ms / 60000 % 60
	s := ms / 1000 % 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms%1000)
}

func runeLen(s string) int { return len([]rune(s)) }

// splitAfterPunct splits on whitespace that follows . ! ? … ; : , (the TS
// regexp /(?<=[.!?…;:,])\s+/).
func splitAfterPunct(text string) []string {
	var out []string
	r := []rune(text)
	start := 0
	for i := 0; i < len(r); i++ {
		if !unicode.IsSpace(r[i]) || i == 0 || !strings.ContainsRune(".!?…;:,", r[i-1]) {
			continue
		}
		j := i
		for j < len(r) && unicode.IsSpace(r[j]) {
			j++
		}
		out = append(out, string(r[start:i]))
		start = j
		i = j - 1
	}
	return append(out, string(r[start:]))
}

// cues breaks a line into short readable cues (≤ ~2 lines of ~42 chars).
func cues(text string, maxChars int) []string {
	var merged []string
	cur := ""
	for _, part := range splitAfterPunct(text) {
		next := part
		if cur != "" {
			next = cur + " " + part
		}
		if cur != "" && runeLen(next) > maxChars {
			merged = append(merged, cur)
			cur = part
		} else {
			cur = next
		}
	}
	if cur != "" {
		merged = append(merged, cur)
	}
	var out []string
	for _, c := range merged {
		if float64(runeLen(c)) <= float64(maxChars)*1.3 {
			out = append(out, c)
			continue
		}
		buf := ""
		for _, word := range strings.Fields(c) {
			if buf != "" && runeLen(strings.TrimSpace(buf+" "+word)) > maxChars {
				out = append(out, buf)
				buf = word
			} else {
				buf = strings.TrimSpace(buf + " " + word)
			}
		}
		if buf != "" {
			out = append(out, buf)
		}
	}
	return out
}

// wrap2 splits a cue into two lines near the middle when it is wider than width.
func wrap2(text string, width int) string {
	r := []rune(text)
	n := len(r)
	if n <= width {
		return text
	}
	from := min(n-1, (n+1)/2+6)
	mid := -1
	for i := from; i >= 0; i-- {
		if r[i] == ' ' {
			mid = i
			break
		}
	}
	if mid <= 0 {
		return text
	}
	return string(r[:mid]) + "\n" + string(r[mid+1:])
}

// srtSeg is one scene's subtitles on the final timeline: the narration is
// spread over [start, start+span) (span = the voice length, or the time the
// scene is on screen when it has no voice).
type srtSeg struct {
	narration   string
	start, span float64
	color       string // burnt-in text colour #RRGGBB ("" = the style's): a multi-voice speaker
}

// cue is one subtitle on the final timeline; text lines are separated by "\n".
type cue struct {
	start, end float64
	text       string
	color      string // #RRGGBB of a speaker ("" = the style's colour)
}

// timedCues spreads each narration over its span, proportionally to the
// length of its cues (≤ 84 characters, wrapped on two lines); upper writes
// the text in capitals.
func timedCues(segs []srtSeg, upper bool) []cue { return timedCuesWith(segs, upper, 0) }

// timedCuesWith is timedCues with phrase cues of at most maxWords words
// (0 = the usual ≤ 84-character cues): a phrase preset changes word group by
// word group in step with the speech.
func timedCuesWith(segs []srtSeg, upper bool, maxWords int) []cue {
	var out []cue
	for _, s := range segs {
		text := strings.TrimSpace(s.narration)
		if text == "" {
			continue
		}
		if upper {
			text = strings.ToUpper(text)
		}
		parts := cues(text, 84)
		if maxWords > 0 {
			parts = phraseCues(text, maxWords)
		}
		total := 0
		for _, c := range parts {
			total += runeLen(c)
		}
		if total == 0 {
			total = 1
		}
		at := s.start
		for _, c := range parts {
			d := s.span * float64(runeLen(c)) / float64(total)
			out = append(out, cue{start: at, end: at + d, text: wrap2(c, 42), color: s.color})
			at += d
		}
	}
	// Speech inside the clips and moved voice lines may come out of order.
	sort.SliceStable(out, func(a, b int) bool { return out[a].start < out[b].start })
	return out
}

// buildSRT is the .srt file for the cues. Returns "" when there is nothing
// to show.
func buildSRT(segs []srtSeg, upper bool) string { return srtOf(timedCues(segs, upper)) }

// buildSRTFor is the .srt of the cues as preset p cuts them (Uppercase,
// phrase cues).
func buildSRTFor(segs []srtSeg, p SubtitlePreset) string {
	return srtOf(timedCuesWith(segs, p.Uppercase, p.MaxWords))
}

func srtOf(cs []cue) string {
	var lines []string
	for i, c := range cs {
		lines = append(lines, fmt.Sprintf("%d\n%s --> %s\n%s\n", i+1, srtTime(c.start), srtTime(c.end), c.text))
	}
	return strings.Join(lines, "\n")
}

// assTime formats seconds as H:MM:SS.cc (ASS centiseconds).
func assTime(sec float64) string {
	cs := int64(math.Round(math.Max(0, sec) * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

// assText makes plain text safe for an ASS dialogue line: override braces and
// backslash escapes would be interpreted; line breaks become \N.
func assText(s string) string {
	s = strings.NewReplacer("{", "(", "}", ")", `\`, "/", "\r", "").Replace(s)
	return strings.ReplaceAll(s, "\n", `\N`)
}

// buildASS is the styled subtitle script burnt into a W×H video: the same
// cues as the .srt, drawn with style p (PlayRes = the video size, so every
// style value is in pixels). Returns "" when there is nothing to show.
func buildASS(segs []srtSeg, p SubtitlePreset, W, H int) string {
	cs := timedCuesWith(segs, p.Uppercase, p.MaxWords)
	if len(cs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Script Info]\nScriptType: v4.00+\nPlayResX: %d\nPlayResY: %d\nWrapStyle: 0\nScaledBorderAndShadow: yes\nYCbCr Matrix: None\n\n", W, H)
	names, values := p.assStyle(W, H)
	b.WriteString("[V4+ Styles]\nFormat: " + strings.Join(names, ", ") + "\nStyle: " + strings.Join(values, ",") + "\n\n")
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s%s\n", assTime(c.start), assTime(c.end), assSpeakerColor(c.color), assText(c.text))
	}
	return b.String()
}

// assSpeakerColor is the override tag that colours one cue in its speaker's
// colour ("" for the style's own colour).
func assSpeakerColor(hex string) string {
	c, ok := parseHexColor(hex)
	if !ok {
		return ""
	}
	return fmt.Sprintf(`{\1c&H%02X%02X%02X&}`, c[2], c[1], c[0])
}

// phraseCues cuts a line into groups of 1…maxWords words for phrase
// subtitles: a group ends at a punctuation mark, and a run between marks is
// split into groups as even as possible (5 words → 3 + 2, never 4 + 1).
func phraseCues(text string, maxWords int) []string {
	maxWords = max(1, maxWords)
	var out []string
	var run []string
	flush := func() {
		n := len(run)
		if n == 0 {
			return
		}
		groups := (n + maxWords - 1) / maxWords
		at := 0
		for g := 0; g < groups; g++ {
			size := n / groups
			if g < n%groups {
				size++
			}
			out = append(out, strings.Join(run[at:at+size], " "))
			at += size
		}
		run = run[:0]
	}
	for _, w := range strings.Fields(text) {
		run = append(run, w)
		if r := []rune(strings.TrimRight(w, "»\"')”")); len(r) > 0 && strings.ContainsRune(".!?…;:,—", r[len(r)-1]) {
			flush()
		}
	}
	flush()
	return out
}
