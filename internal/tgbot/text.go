package tgbot

// Small text helpers: project keys for callback data, progress bars, times,
// sizes and HTML pieces. Every dynamic text goes through esc.

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

// esc escapes text for an HTML message.
func esc(s string) string { return notify.Escape(s) }

// key is a project's short id for callback data: 8 characters of its
// SHA-256 (project ids may be up to 64 characters; callback data is ≤ 64
// bytes). The same project always has the same key, so buttons of old
// messages keep working after a restart.
func key(id string) string {
	sum := sha256.Sum256([]byte(id))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:8]
}

// projectByKey finds the project behind a key.
func (b *Bot) projectByKey(k string) (studio.Project, bool) {
	if k == "" {
		return studio.Project{}, false
	}
	for _, s := range b.e.List() {
		if key(s.ID) == k {
			return b.e.Get(s.ID)
		}
	}
	return studio.Project{}, false
}

// bar is a 10-cell progress bar with the percentage: "▰▰▰▰▱▱▱▱▱▱ 42 %".
func bar(pct int) string {
	pct = max(0, min(100, pct))
	n := (pct + 5) / 10
	return strings.Repeat("▰", n) + strings.Repeat("▱", 10-n) + " " + strconv.Itoa(pct) + " %"
}

// clock is a video length: "0:45", "1:02", "1:02:03".
func clock(sec float64) string {
	s := int(math.Round(sec))
	if s < 0 {
		s = 0
	}
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// span is a duration in words: "45 с", "3 хв", "1 год 20 хв".
func span(sec int) string {
	switch {
	case sec < 60:
		return strconv.Itoa(max(0, sec)) + " с"
	case sec < 3600:
		if sec%60 >= 30 && sec < 600 {
			return fmt.Sprintf("%d хв %d с", sec/60, sec%60)
		}
		return strconv.Itoa(sec/60) + " хв"
	}
	if m := sec / 60 % 60; m > 0 {
		return fmt.Sprintf("%d год %d хв", sec/3600, m)
	}
	return strconv.Itoa(sec/3600) + " год"
}

// length is a film length choice: "30 с", "1 хв", "1 хв 30 с".
func length(sec int) string {
	switch {
	case sec <= 0:
		return "як у профілі"
	case sec < 60:
		return strconv.Itoa(sec) + " с"
	case sec%60 == 0:
		return strconv.Itoa(sec/60) + " хв"
	}
	return fmt.Sprintf("%d хв %d с", sec/60, sec%60)
}

// plural picks the Ukrainian form for n: one, few (2–4), many.
func plural(n int, one, few, many string) string {
	m10, m100 := n%10, n%100
	switch {
	case m10 == 1 && m100 != 11:
		return one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		return few
	}
	return many
}

func count(n int, one, few, many string) string {
	return strconv.Itoa(n) + " " + plural(n, one, few, many)
}

// excerpt is the start of a text on one line (≤ n characters).
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:—-") + "…"
}

// titleFrom makes a working title from a story: its first sentence, ≤ 6
// words and 48 characters (like Home does); "" lets the plan name it.
func titleFrom(story string) string {
	s := strings.TrimSpace(story)
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
	return strings.Trim(out, " ,;:—-«»\"")
}

// title is a project title in bold quotes.
func title(t string) string {
	if strings.TrimSpace(t) == "" {
		t = "Без назви"
	}
	return "<b>«" + esc(clipName(t, 80)) + "»</b>"
}

// stepNames are the five steps of a project.
var stepNames = [...]string{"", "Сценарій", "План", "Розкадровка", "Відео й озвучка", "Монтаж"}

func stepName(n int) string {
	if n >= 1 && n < len(stepNames) {
		return stepNames[n]
	}
	return "Сценарій"
}
