package dictate

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"ytcartell/internal/stt"
)

// Splice places dictated text into an editable text at the caret. Final
// pieces go in for good; the interim (still changing) text sits right after
// them and is replaced on every update. All offsets are in runes, like
// Gio's widget.Editor.
//
//	edits := sp.Begin(ed.Text(), ed.Selection())          // once
//	edits = sp.Update(ed.Text(), caret, finals, interim)  // every frame with news
//	edits = sp.End(ed.Text(), true)                       // keep what is left
//
// The caller applies each Edit in order (select [Start, End), insert Text,
// put the caret at Caret) and may show [IStart, IEnd) as interim text. When
// the owner moves the caret or edits the text meanwhile, the interim text is
// taken out and dictation goes on at the new caret.
type Splice struct {
	anchor  int    // where the next final piece goes
	interim []rune // interim text in the buffer at anchor (its leading space included)
	caret   int    // the caret the last edit left
	on      bool
}

// Edit is one change of the text.
type Edit struct {
	Start, End int    // runes replaced
	Text       string // inserted text
	Caret      int    // caret afterwards
	// IStart, IEnd: the interim text after this edit (empty range = none).
	IStart, IEnd int
}

// Active reports a splice between Begin and End.
func (s *Splice) Active() bool { return s.on }

// Interim is the rune range of the interim text in the buffer.
func (s *Splice) Interim() (start, end int) {
	return s.anchor, s.anchor + len(s.interim)
}

// Begin anchors at the selection of text (a selection is replaced by what is
// dictated: it is removed now).
func (s *Splice) Begin(text string, selStart, selEnd int) []Edit {
	n := utf8.RuneCountInString(text)
	lo, hi := clampRange(selStart, selEnd, n)
	*s = Splice{anchor: lo, caret: lo, on: true}
	if lo == hi {
		return nil
	}
	return []Edit{{Start: lo, End: hi, Caret: lo, IStart: lo, IEnd: lo}}
}

// Update inserts the finals and shows interim at the anchor. caret is the
// editor's caret now (a collapsed selection; -1 = unknown, keep going).
func (s *Splice) Update(text string, caret int, finals []string, interim string) []Edit {
	if !s.on {
		return nil
	}
	rs := []rune(text)
	var edits []Edit
	if !s.intact(rs) || (caret >= 0 && caret != s.caret) {
		// The owner edited or moved: drop the interim text where it still
		// is, then continue at the caret.
		if s.intact(rs) && len(s.interim) > 0 {
			end := s.anchor + len(s.interim)
			edits = append(edits, Edit{Start: s.anchor, End: end, Caret: s.anchor, IStart: s.anchor, IEnd: s.anchor})
			rs = append(rs[:s.anchor:s.anchor], rs[end:]...)
			if caret >= end {
				caret -= len(s.interim)
			} else if caret > s.anchor {
				caret = s.anchor
			}
		}
		if caret < 0 || caret > len(rs) {
			caret = min(max(s.caret, 0), len(rs))
		}
		s.anchor, s.interim, s.caret = caret, nil, caret
	}
	if len(finals) == 0 && string(s.interim) == joinPiece(prevRune(rs, s.anchor), sentenceStart(rs, s.anchor), interim) {
		return edits
	}
	// Build: the finals one after another, then the interim text.
	var b strings.Builder
	prev, start := prevRune(rs, s.anchor), sentenceStart(rs, s.anchor)
	for _, f := range finals {
		p := joinPiece(prev, start, f)
		if p == "" {
			continue
		}
		b.WriteString(p)
		r, _ := utf8.DecodeLastRuneInString(p)
		prev, start = r, endsSentence(p)
	}
	fin := b.String()
	ip := joinPiece(prev, start, interim)
	ins := fin + ip
	end := s.anchor + len(s.interim)
	// A space before the text that follows, when it starts with a word.
	if next := runeAt(rs, end); ins != "" && next != 0 && needsSpaceBefore(next) && !unicode.IsSpace(lastRune(ins)) {
		ins += " "
	}
	nf, ni := utf8.RuneCountInString(fin), utf8.RuneCountInString(ip)
	e := Edit{Start: s.anchor, End: end, Text: ins}
	s.anchor += nf
	s.interim = []rune(ip)
	s.caret = s.anchor + ni
	e.Caret, e.IStart, e.IEnd = s.caret, s.anchor, s.anchor+ni
	return append(edits, e)
}

// End finishes the splice: the interim text stays as typed text (keep) or is
// taken out.
func (s *Splice) End(text string, keep bool) []Edit {
	if !s.on {
		return nil
	}
	rs := []rune(text)
	var edits []Edit
	if !keep && len(s.interim) > 0 && s.intact(rs) {
		end := s.anchor + len(s.interim)
		edits = append(edits, Edit{Start: s.anchor, End: end, Caret: s.anchor, IStart: s.anchor, IEnd: s.anchor})
	}
	*s = Splice{}
	return edits
}

// intact: the interim text is still where it was put.
func (s *Splice) intact(rs []rune) bool {
	end := s.anchor + len(s.interim)
	if s.anchor < 0 || end > len(rs) {
		return false
	}
	for i, r := range s.interim {
		if rs[s.anchor+i] != r {
			return false
		}
	}
	return true
}

// joinPiece prepares a recognised piece for insertion after prev: trimmed,
// a space in front where words need one, a capital letter at the start of a
// sentence.
func joinPiece(prev rune, sentence bool, piece string) string {
	piece = strings.Join(strings.Fields(piece), " ")
	if piece == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(piece)
	if sentence && unicode.IsLower(first) {
		piece = string(unicode.ToUpper(first)) + piece[size:]
	}
	if prev != 0 && !unicode.IsSpace(prev) && !opener(prev) && !closer(first) && !(stt.Unspaced(prev) && stt.Unspaced(first)) {
		piece = " " + piece
	}
	return piece
}

// sentenceStart: text at rune i starts a sentence (start of the text, a new
// line, or after . ! ? …).
func sentenceStart(rs []rune, i int) bool {
	for j := min(i, len(rs)) - 1; j >= 0; j-- {
		r := rs[j]
		switch {
		case r == '\n':
			return true
		case unicode.IsSpace(r), strings.ContainsRune(`"'»”’)]`, r):
			continue
		}
		return strings.ContainsRune(".!?…", r)
	}
	return true
}

func endsSentence(s string) bool {
	s = strings.TrimRight(s, ` "'»”’)]`)
	r, _ := utf8.DecodeLastRuneInString(s)
	return strings.ContainsRune(".!?…", r)
}

func opener(r rune) bool { return strings.ContainsRune(`([{«„“‘`, r) }

func closer(r rune) bool { return strings.ContainsRune(`,.!?;:…)]}»”’%`, r) }

// needsSpaceBefore: a word (or an opening bracket) follows.
func needsSpaceBefore(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || opener(r)
}

func prevRune(rs []rune, i int) rune {
	if i <= 0 || i > len(rs) {
		return 0
	}
	return rs[i-1]
}

func runeAt(rs []rune, i int) rune {
	if i < 0 || i >= len(rs) {
		return 0
	}
	return rs[i]
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

func clampRange(a, b, n int) (int, int) {
	if a > b {
		a, b = b, a
	}
	a, b = min(max(a, 0), n), min(max(b, 0), n)
	return a, b
}
