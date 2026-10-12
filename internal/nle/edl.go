package nle

// CMX 3600 EDL — the oldest common denominator (Resolve: File → Import →
// Timeline; Premiere: File → Import; Avid, Vegas …). One video track: the
// scenes of V1 with their cuts and dissolves (a dip is written as a
// dissolve of the same length). Reel "AX" with the clip and file names as
// comments ("* FROM CLIP NAME:" / "* SOURCE FILE:") — editors find the
// media by name. Sound, texts, levels and motion do not exist in an EDL.

import (
	"fmt"
	"strings"
	"unicode"
)

// timecode formats frame n as HH:MM:SS:FF at the rate's timebase (non-drop).
func timecode(n int64, r Rate) string {
	tb := r.Timebase()
	if n < 0 {
		n = 0
	}
	ff := n % tb
	s := n / tb
	return fmt.Sprintf("%02d:%02d:%02d:%02d", s/3600, s/60%60, s%60, ff)
}

// translit spells Cyrillic letters in Latin (Ukrainian national system,
// simplified): EDL titles and names are 7-bit ASCII.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "h", 'ґ': "g", 'д': "d", 'е': "e", 'є': "ie", 'ж': "zh", 'з': "z", 'и': "y",
	'і': "i", 'ї': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s",
	'т': "t", 'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ь': "", 'ю': "iu", 'я': "ia",
	'ё': "io", 'ы': "y", 'э': "e",
}

// asciiName keeps an EDL title or comment printable ASCII.
func asciiName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 128 && unicode.IsPrint(r):
			b.WriteRune(r)
		case r == 'ь' || r == 'Ь' || r == 'ъ' || r == 'Ъ' || r == '’' || r == 'ʼ':
			// soft sign and apostrophes are not spelled
		case translit[unicode.ToLower(r)] != "":
			t := translit[unicode.ToLower(r)]
			if unicode.IsUpper(r) && t != "" {
				t = strings.ToUpper(t[:1]) + t[1:]
			}
			b.WriteString(t)
		case r == '·' || r == '—' || r == '–':
			b.WriteByte('-')
		case r == '«' || r == '»' || r == '“' || r == '”':
			b.WriteByte('"')
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// EDL renders V1 of t as a CMX 3600 edit decision list.
func EDL(t *Timeline) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TITLE: %s\n", asciiName(t.Name))
	b.WriteString("FCM: NON-DROP FRAME\n\n")
	if len(t.Video) == 0 {
		return []byte(b.String()), nil
	}
	v1 := t.Video[0]
	trans := map[int64]Transition{}
	for _, x := range v1.Transitions {
		trans[x.At] = x
	}
	r := t.Rate
	ev := 0
	line := func(c Clip, kind string, dur int64, srcIn, srcOut, recIn, recOut int64) {
		d := "   "
		if kind == "D" {
			d = fmt.Sprintf("%03d", dur)
		}
		fmt.Fprintf(&b, "%03d  AX       V     %-4s %s %s %s %s %s\n", ev, kind, d,
			timecode(srcIn, r), timecode(srcOut, r), timecode(recIn, r), timecode(recOut, r))
	}
	comment := func(label string, c Clip) {
		fmt.Fprintf(&b, "* %s CLIP NAME: %s\n", label, asciiName(c.Media.Name))
	}
	for i, c := range v1.Clips {
		start, in := c.Start, c.In
		end := c.End()
		// A dissolve out of this clip starts at the transition's first frame.
		if x, ok := trans[c.End()]; ok && i+1 < len(v1.Clips) && v1.Clips[i+1].Start == c.End() {
			end = x.From()
		}
		if x, ok := trans[c.Start]; ok && i > 0 && v1.Clips[i-1].End() == c.Start {
			prev := v1.Clips[i-1]
			from := x.From()
			ev++
			// The outgoing clip, zero length at the transition's start …
			pIn := prev.In + (from - prev.Start)
			line(prev, "C", 0, pIn, pIn, from, from)
			// … dissolving into this clip from there.
			in -= c.Start - from
			start = from
			line(c, "D", x.Len, in, in+(end-start), start, end)
			comment("FROM", prev)
			comment("TO", c)
			fmt.Fprintf(&b, "* SOURCE FILE: %s\n", asciiName(c.Media.Rel))
			if x.Source != "" && x.Source != "dissolve" {
				fmt.Fprintf(&b, "* EFFECT: %s\n", asciiName(x.Source))
			}
			b.WriteByte('\n')
			continue
		}
		ev++
		line(c, "C", 0, in, in+(end-start), start, end)
		comment("FROM", c)
		fmt.Fprintf(&b, "* SOURCE FILE: %s\n\n", asciiName(c.Media.Rel))
	}
	return []byte(b.String()), nil
}
