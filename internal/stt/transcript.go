package stt

// Ready transcripts ("Своя озвучка" with its text): when the user already has
// the text of the recording with timecodes, the studio reads it instead of
// recognising the speech again. ParseTranscript sniffs the content (the file
// name is only a hint):
//
//	SRT      1 ⏎ 00:00:01,000 --> 00:00:03,200 ⏎ Текст фрази
//	WebVTT   WEBVTT ⏎ 00:01.000 --> 00:03.200 align:start ⏎ Текст — YouTube's
//	         per-word <00:00:01.500><c> timings are kept, rolling captions
//	         (each cue repeating the previous line) are de-duplicated
//	JSON     ElevenLabs Scribe    {"language_code","text","words":[{"text","start","end","type"}]}
//	         OpenAI / Whisper     {"language","text","words":[{"word","start","end"}],"segments":[{"start","end","text"}]}
//	                              (openai-whisper, faster-whisper, WhisperX: segments[].words, word_segments)
//	         Deepgram             {"results":{"channels":[{"alternatives":[{"transcript","words":[{"punctuated_word","start","end"}]}]}]}}
//	         AssemblyAI           {"language_code","text","words":[{"text","start","end"}]} — milliseconds
//	         whisper.cpp -oj/-ojf {"transcription":[{"offsets":{"from","to"},"text","tokens":[…]}]}
//	         ElevenLabs text-to-speech with timestamps {"alignment":{"characters","character_start_times_seconds",…}}
//	         and AWS Transcribe, Google Speech-to-Text, Rev, Speechmatics, Vosk,
//	         Hugging Face "chunks", this app's own audio/transcript.json, JSON Lines
//	TXT      [00:12] Текст · 00:12 - Текст · 00:00:12,500 --> 00:00:15,000 Текст ·
//	         a timestamp alone on its line before the text (YouTube's transcript
//	         panel) · [00:00.000 --> 00:03.200] Текст (whisper's console) · SBV ·
//	         LRC · Audacity labels (start⇥end⇥text in seconds) · SMPTE 00:00:12:15
//
// Text is read as UTF-8 (a BOM is dropped), UTF-16, or — when it is neither —
// Windows-1251 (older Ukrainian and Russian subtitles). Markup, [sound notes]
// and music signs are dropped. Phrase-level formats get word timings spread
// over each phrase by the words' length; a phrase without an end lasts until
// the next one. The result is cleaned like a provider's reply (finish): words
// and segments both filled, Text, Language when the file names one (a name
// like "video.uk.srt" counts), Duration = the end of the speech (or the audio
// length a JSON reports).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"ytcartell/internal/tts"
)

// TranscriptExts lists the files ParseTranscript is made for (the file picker).
func TranscriptExts() []string { return []string{".srt", ".vtt", ".json", ".txt"} }

// ErrNoTimecodes: the file has no timings ParseTranscript understands.
var ErrNoTimecodes = errors.New("У файлі немає таймкодів. Потрібен текст із часом кожної фрази: субтитри SRT чи VTT, JSON від сервісу розпізнавання або TXT з мітками на кшталт [00:12]")

var (
	errEmptyTranscript = errors.New("Файл із текстом порожній")
	errNoSpeechText    = errors.New("У файлі є таймкоди, але немає тексту")
	errUnknownJSON     = errors.New("Не впізнаю цей JSON: у ньому немає слів чи фраз із таймкодами. Підходять файли ElevenLabs, OpenAI/Whisper, Deepgram, AssemblyAI або whisper.cpp")
)

// ParseTranscript reads a transcript with timecodes (the formats are listed
// above); name is the file's name, a hint only. The result always has words
// and segments with times in seconds; errors are plain Ukrainian.
func ParseTranscript(data []byte, name string) (Transcript, error) {
	text := decodeText(data)
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Transcript{}, errEmptyTranscript
	}
	var (
		tr  Transcript
		err error
	)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") && json.Valid([]byte(trimmed)) {
		tr, err = parseTranscriptJSON(trimmed)
	} else {
		tr, err = parseTimedText(text)
	}
	if err != nil {
		return Transcript{}, err
	}
	if tr.Language == "" {
		tr.Language = nameLanguage(name)
	}
	// An audio length far beyond the speech is another unit or another file:
	// the speech decides.
	if end := speechEnd(tr); !finite(tr.Duration) || tr.Duration > 10*end+60 {
		tr.Duration = 0
	}
	tr.finish()
	if len(tr.Words) == 0 {
		return Transcript{}, errNoSpeechText
	}
	if !(speechEnd(tr) > 0) {
		return Transcript{}, ErrNoTimecodes
	}
	// Word-by-word captions: phrases for the preview.
	if n := len(tr.Words); n >= 3 && float64(n) < 1.6*float64(len(tr.Segments)) {
		tr.Segments = segmentsFromWords(tr.Words)
	}
	return tr, nil
}

// speechEnd is the latest end of a word or phrase (0 without any).
func speechEnd(tr Transcript) float64 {
	end := 0.0
	for _, w := range tr.Words {
		if finite(w.End) {
			end = math.Max(end, w.End)
		}
	}
	for _, s := range tr.Segments {
		if finite(s.End) {
			end = math.Max(end, s.End)
		}
	}
	return end
}

// nameLanguage reads a language code from a name like "video.uk.srt" or
// "talk.en-US.vtt" (what yt-dlp and many editors write), "" otherwise.
func nameLanguage(name string) string {
	base := filepath.Base(name)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	if len(ext) < 3 || len(ext) > 12 {
		return ""
	}
	return tts.NormalizeLanguage(ext[1:])
}

// ---- text encodings --------------------------------------------------------------------------

// decodeText returns the file as UTF-8 text with "\n" line ends.
func decodeText(b []byte) string {
	var s string
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		s = strings.ToValidUTF8(string(b[3:]), "")
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		s = fromUTF16(b[2:], false)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		s = fromUTF16(b[2:], true)
	default:
		if be, ok := sniffUTF16(b); ok {
			s = fromUTF16(b, be)
		} else if mostlyUTF8(b) {
			s = strings.ToValidUTF8(string(b), "")
		} else {
			s = fromCP1251(b)
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// sniffUTF16 recognises UTF-16 without a byte order mark by its zero bytes
// (timecodes, digits and spaces are ASCII: one byte of each pair is zero).
func sniffUTF16(b []byte) (bigEndian, ok bool) {
	n := min(len(b), 1024) &^ 1
	if n < 8 {
		return false, false
	}
	even, odd := 0, 0
	for i := 0; i < n; i += 2 {
		if b[i] == 0 {
			even++
		}
		if b[i+1] == 0 {
			odd++
		}
	}
	pairs := n / 2
	switch {
	case odd*4 > pairs && even*20 < pairs:
		return false, true
	case even*4 > pairs && odd*20 < pairs:
		return true, true
	}
	return false, false
}

func fromUTF16(b []byte, bigEndian bool) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(u))
}

// mostlyUTF8: valid UTF-8, or UTF-8 with a few broken bytes (a cut file) —
// Windows-1251 text breaks on almost every letter instead.
func mostlyUTF8(b []byte) bool {
	valid, invalid := 0, 0
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n <= 1 {
			invalid++
			i++
			continue
		}
		valid++
		i += n
	}
	return invalid == 0 || valid >= 10*invalid
}

// cp1251High maps the bytes 0x80–0xBF of Windows-1251 (0xC0–0xFF are А…я).
var cp1251High = [64]rune{
	'Ђ', 'Ѓ', '‚', 'ѓ', '„', '…', '†', '‡', '€', '‰', 'Љ', '‹', 'Њ', 'Ќ', 'Ћ', 'Џ',
	'ђ', '‘', '’', '“', '”', '•', '–', '—', 0xFFFD, '™', 'љ', '›', 'њ', 'ќ', 'ћ', 'џ',
	0x00A0, 'Ў', 'ў', 'Ј', '¤', 'Ґ', '¦', '§', 'Ё', '©', 'Є', '«', '¬', 0x00AD, '®', 'Ї', // 0xA0 no-break space, 0xAD soft hyphen
	'°', '±', 'І', 'і', 'ґ', 'µ', '¶', '·', 'ё', '№', 'є', '»', 'ј', 'Ѕ', 'ѕ', 'ї',
}

func fromCP1251(b []byte) string {
	var sb strings.Builder
	sb.Grow(2 * len(b))
	for _, c := range b {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xC0:
			sb.WriteRune(cp1251High[c-0x80])
		default:
			sb.WriteRune('А' + rune(c-0xC0))
		}
	}
	return sb.String()
}

// ---- subtitles and text with timestamps -------------------------------------------------

// clockPat is a timestamp: [h:]m:ss with a decimal fraction (.5, ,500) or
// SMPTE frames (:12). Groups: hours, minutes, seconds, fraction, frames.
const clockPat = `(?:(\d{1,3}):)?(\d{1,3}):([0-5]\d)(?:[.,](\d{1,9})|[:;](\d\d))?`

var (
	// reRange: "00:00:01,000 --> 00:00:03,200 [settings or text]", SBV's
	// "0:00:01.000,0:00:03.200", whisper's "[00:01.000 --> 00:03.200] text".
	reRange = regexp.MustCompile(`^[\[(]?\s*` + clockPat + `\s*(?:-->|->|→|—|–|-|,)\s*` + clockPat + `\s*[\])]?\s*(.*)$`)
	// reStartB / reStart: a phrase's start only — "[00:12] text", "00:12 - text", "0:00".
	reStartB = regexp.MustCompile(`^[\[(]` + clockPat + `[\])]\s*(?:[-–—|:]\s*)?(.*)$`)
	reStart  = regexp.MustCompile(`^` + clockPat + `(?:\s*[-–—|:]\s*|\s+|$)(.*)$`)
	// reLabel: an Audacity label, seconds.
	reLabel  = regexp.MustCompile(`^(\d+(?:[.,]\d+)?)\t(\d+(?:[.,]\d+)?)(?:\t(.*))?$`)
	reClock  = regexp.MustCompile(`^` + clockPat + `$`)
	reInline = regexp.MustCompile(`<` + clockPat + `>`) // a word's start inside a cue (WebVTT, enhanced LRC)
	reSMPTE  = regexp.MustCompile(`\d:[0-5]\d:[0-5]\d[:;](\d\d)`)
	reTag    = regexp.MustCompile(`</?[A-Za-z][^<>]*>|\{\\[^{}]*\}`) // <i>, <v Name>, <c.color>, {\an8}
	reNote   = regexp.MustCompile(`\[[^\[\]]*\]`)                    // [Музика], [BLANK_AUDIO]
	reCueSet = regexp.MustCompile(`^(?:(?:align|position|line|size|vertical|region|[XYxy][12]):\S*\s*)+$`)
	reIndex  = regexp.MustCompile(`^\d+$`)
	// reSpeaker: a speaker line of its own in a cue ("Speaker 1" — Premiere, Descript).
	reSpeaker = regexp.MustCompile(`(?i)^(?:speaker|спікер|мовець|диктор|unknown|невідомий)(?:\s*\d+)?:?$`)
	reVTTLang = regexp.MustCompile(`(?im)^language:\s*([a-z]{2,3}(?:[-_][a-z0-9]+)*)\s*$`)
)

// clockOf converts the five clockPat groups to seconds.
func clockOf(g []string, fps float64) float64 {
	h, _ := strconv.Atoi(g[0])
	m, _ := strconv.Atoi(g[1])
	s, _ := strconv.Atoi(g[2])
	t := float64(h*3600 + m*60 + s)
	if g[3] != "" {
		f, _ := strconv.ParseFloat("0."+g[3], 64)
		t += f
	}
	if g[4] != "" {
		n, _ := strconv.Atoi(g[4])
		t += float64(n) / fps
	}
	return t
}

// frameRate guesses the frame rate of SMPTE timecodes: the first common rate
// above the largest frame number in the file.
func frameRate(text string) float64 {
	top := 0
	for _, m := range reSMPTE.FindAllStringSubmatch(text, -1) {
		if n, _ := strconv.Atoi(m[1]); n > top {
			top = n
		}
	}
	for _, fps := range []float64{25, 30, 50, 60} {
		if float64(top) < fps {
			return fps
		}
	}
	return 100
}

func startClock(t string, fps float64) (float64, string, bool) {
	m := reStartB.FindStringSubmatch(t)
	if m == nil {
		m = reStart.FindStringSubmatch(t)
	}
	if m == nil {
		return 0, "", false
	}
	return clockOf(m[1:6], fps), strings.TrimSpace(m[6]), true
}

// timedCue is one timed phrase of a subtitle or text file; end is NaN when the
// file gives a start only.
type timedCue struct {
	start, end float64
	lines      []string // as written (inline timings and markup kept)
}

// parseTimedText reads subtitles and text with timestamps. The kind of
// timing that most lines carry decides how the file is read: ranges (SRT,
// WebVTT, SBV, whisper's console — cues end at a blank line), Audacity labels,
// or starts only (a phrase lasts until the next timestamp).
func parseTimedText(text string) (Transcript, error) {
	lines := strings.Split(text, "\n")
	fps := frameRate(text)
	ranges, labels, starts := 0, 0, 0
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
		case reRange.MatchString(t):
			ranges++
		case reLabel.MatchString(t):
			labels++
		default:
			if _, _, ok := startClock(t, fps); ok {
				starts++
			}
		}
	}
	var cues []timedCue
	switch {
	case ranges > 0 && ranges >= starts && ranges >= labels:
		cues = rangeCues(lines, fps)
	case labels > 0 && labels >= starts:
		cues = labelCues(lines)
	case starts > 0:
		cues = startCues(lines, fps)
	default:
		return Transcript{}, ErrNoTimecodes
	}
	tr := cueTranscript(cues)
	if strings.HasPrefix(strings.TrimSpace(text), "WEBVTT") {
		head, _, _ := strings.Cut(text, "\n\n")
		if m := reVTTLang.FindStringSubmatch(head); m != nil {
			tr.Language = m[1]
		}
	}
	return tr, nil
}

// rangeCues reads SRT, WebVTT, SBV and alike: a timing line opens a cue, its
// text runs to the next empty line. Headers, notes, styles, cue ids and SRT
// numbers are skipped; a line of spaces does not end a cue (WebVTT).
func rangeCues(lines []string, fps float64) []timedCue {
	var out []timedCue
	open := false
	for i, raw := range lines {
		if raw == "" {
			open = false
			continue
		}
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if m := reRange.FindStringSubmatch(t); m != nil {
			c := timedCue{start: clockOf(m[1:6], fps), end: clockOf(m[6:11], fps)}
			if rest := strings.TrimSpace(m[11]); rest != "" && !reCueSet.MatchString(rest) {
				c.lines = append(c.lines, rest)
			}
			out = append(out, c)
			open = true
			continue
		}
		if !open || reIndex.MatchString(t) && nextIsRange(lines, i) {
			continue
		}
		last := &out[len(out)-1]
		last.lines = append(last.lines, t)
	}
	return out
}

func nextIsRange(lines []string, i int) bool {
	for _, l := range lines[i+1:] {
		if t := strings.TrimSpace(l); t != "" {
			return reRange.MatchString(t)
		}
	}
	return false
}

// startCues reads text where a timestamp starts each phrase: on the
// phrase's line ("[00:12] text") or alone before it (YouTube's transcript).
// Text before the first timestamp (a title, LRC tags) is skipped.
func startCues(lines []string, fps float64) []timedCue {
	var out []timedCue
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if sec, rest, ok := startClock(t, fps); ok {
			c := timedCue{start: sec, end: math.NaN()}
			if rest != "" {
				c.lines = []string{rest}
			}
			out = append(out, c)
			continue
		}
		if n := len(out); n > 0 {
			out[n-1].lines = append(out[n-1].lines, t)
		}
	}
	return out
}

// labelCues reads an Audacity label track (start, end and text, tab-separated).
func labelCues(lines []string) []timedCue {
	var out []timedCue
	for _, raw := range lines {
		m := reLabel.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		a, _ := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
		b, _ := strconv.ParseFloat(strings.Replace(m[2], ",", ".", 1), 64)
		out = append(out, timedCue{start: a, end: b, lines: []string{m[3]}})
	}
	return out
}

// cueTranscript turns cues into phrases and words. Cues are ordered by time;
// one without a (valid) end lasts until the next one. Rolling captions —
// each cue repeating the previous cue's line above a new one, with tiny
// in-between cues (YouTube's automatic captions) — keep only the new lines.
// Words take the cue's inline timings when it has them, else they share its
// time by length.
func cueTranscript(cues []timedCue) Transcript {
	raw := make([]Segment, 0, len(cues))
	for _, c := range cues {
		lines := c.lines
		if len(lines) > 1 && reSpeaker.MatchString(cleanCue(lines[0])) {
			lines = lines[1:]
		}
		raw = append(raw, Segment{Text: strings.Join(lines, "\n"), Start: c.start, End: c.end})
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].Start < raw[j].Start })
	closeEnds(raw)

	var tr Transcript
	var recent []string // the last lines that went into the transcript
	prevEnd := math.Inf(-1)
	for _, c := range raw {
		lines := strings.Split(c.Text, "\n")
		if len(recent) > 0 && c.Start <= prevEnd+0.1 {
			k := 0
			for k < len(lines) && slices.Contains(recent, cleanCue(lines[k])) {
				k++
			}
			// A whole cue said again on purpose ("Так." "Так.") stays; only a
			// tiny in-between cue goes away whole.
			if k < len(lines) || c.End-c.Start < 0.1 {
				lines = lines[k:]
			}
		}
		prevEnd = math.Max(prevEnd, c.End)
		seg, words := cueWords(lines, c.Start, c.End)
		if seg.Text == "" {
			continue
		}
		tr.Segments = append(tr.Segments, seg)
		tr.Words = append(tr.Words, words...)
		for _, l := range lines {
			if p := cleanCue(l); p != "" {
				recent = append(recent, p)
			}
		}
		if len(recent) > 2 {
			recent = recent[len(recent)-2:]
		}
	}
	texts := make([]string, len(tr.Segments))
	for i, s := range tr.Segments {
		texts[i] = s.Text
	}
	tr.Text = joinTexts(texts)
	return tr
}

// cueWords is a cue's phrase and its words.
func cueWords(lines []string, start, end float64) (Segment, []Word) {
	raw := strings.Join(lines, " ")
	seg := Segment{Text: cleanCue(raw), Start: start, End: end}
	if seg.Text == "" {
		return seg, nil
	}
	locs := reInline.FindAllStringSubmatchIndex(raw, -1)
	if len(locs) == 0 {
		return seg, spreadWords(seg)
	}
	var words []Word
	at, from := start, 0
	for i := 0; i <= len(locs); i++ {
		to, next := len(raw), end
		if i < len(locs) {
			loc := locs[i]
			g := make([]string, 5)
			for k := range g {
				if a, b := loc[2+2*k], loc[3+2*k]; a >= 0 {
					g[k] = raw[a:b]
				}
			}
			to, next = loc[0], math.Min(end, math.Max(at, clockOf(g, 30)))
		}
		if piece := cleanCue(raw[from:to]); piece != "" {
			words = append(words, wordsFromSegments([]Segment{{Text: piece, Start: at, End: next}})...)
		}
		if i < len(locs) {
			at, from = next, locs[i][1]
		}
	}
	return seg, mergePunct(words)
}

// spreadWords is a phrase's words sharing its time by their length.
func spreadWords(seg Segment) []Word { return mergePunct(wordsFromSegments([]Segment{seg})) }

// cleanCue is the spoken text of a cue: markup, inline timings, [sound
// notes], music signs, invisible marks and HTML entities removed, spaces
// collapsed.
func cleanCue(s string) string {
	s = reInline.ReplaceAllString(s, "")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reNote.ReplaceAllString(s, " ")
	s = strings.Map(func(r rune) rune {
		switch r {
		case '♪', '♫', '♬', '♩', 0x200B, 0x200E, 0x200F, 0xFEFF: // + zero-width space, direction marks, BOM
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// closeEnds gives phrases without a (valid) end the start of the next later
// phrase; the last one gets the time it takes to say it.
// segs must be ordered by start.
func closeEnds(segs []Segment) {
	later := math.NaN() // the first start after segs[i] that is later than its own
	for i := len(segs) - 1; i >= 0; i-- {
		s := &segs[i]
		if i+1 < len(segs) && segs[i+1].Start > s.Start {
			later = segs[i+1].Start
		}
		if finite(s.End) && s.End > s.Start {
			continue
		}
		if s.End = later; !finite(s.End) {
			s.End = s.Start + sayTime(s.Text)
		}
	}
}

// sayTime is a generous guess of how long text takes to say (seconds).
func sayTime(text string) float64 {
	return math.Max(1, float64(utf8.RuneCountInString(cleanCue(text)))/12+0.5)
}

// ---- JSON --------------------------------------------------------------------------------

func parseTranscriptJSON(text string) (Transcript, error) {
	var root any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		if tr, ok := parseJSONLines(text); ok {
			return tr, nil
		}
		return Transcript{}, fmt.Errorf("Файл JSON пошкоджений — перевір, що він збережений повністю (%s)", clip(err.Error(), 120))
	}
	return parseJSONValue(root)
}

func parseJSONValue(root any) (Transcript, error) {
	if m := jobj(root); m != nil {
		if _, ok := m["transcription"]; ok {
			return parseWhisperCpp(m)
		}
		if jobj(m["alignment"]) != nil || jobj(m["normalized_alignment"]) != nil {
			return parseAlignment(m)
		}
	}
	return parseJSONItems(root)
}

// parseJSONLines reads JSON Lines (one result object per line: streaming
// replies, Vosk): every line must be an object; their words are joined.
func parseJSONLines(text string) (Transcript, bool) {
	var out Transcript
	var texts []string
	n := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v any
		if json.Unmarshal([]byte(line), &v) != nil || jobj(v) == nil {
			return Transcript{}, false
		}
		n++
		tr, err := parseJSONValue(v)
		if err != nil {
			continue // a line without speech (silence, a final empty result)
		}
		out.Words = append(out.Words, tr.Words...)
		out.Segments = append(out.Segments, tr.Segments...)
		if t := strings.TrimSpace(tr.Text); t != "" {
			texts = append(texts, t)
		}
		if out.Language == "" {
			out.Language = tr.Language
		}
	}
	if n < 2 || len(out.Words)+len(out.Segments) == 0 {
		return Transcript{}, false
	}
	out.Text = joinTexts(texts)
	return out, true
}

func jobj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func jarr(v any) []any          { a, _ := v.([]any); return a }
func jstr(v any) string         { s, _ := v.(string); return s }

// jnum reads a time or a number: a JSON number, a numeric string ("0.5",
// Google's "1.500s") or a clock ("00:00:01,500").
func jnum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, finite(x)
	case string:
		s := strings.TrimSpace(x)
		if f, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64); err == nil && finite(f) {
			return f, true
		}
		if m := reClock.FindStringSubmatch(s); m != nil {
			return clockOf(m[1:6], 30), true
		}
	}
	return 0, false
}

// jsonAt collects the values at a dotted path: "*" is every element of an
// array, a number one element.
func jsonAt(v any, path string) []any {
	cur := []any{v}
	for _, key := range strings.Split(path, ".") {
		var next []any
		for _, c := range cur {
			switch x := c.(type) {
			case map[string]any:
				if y, ok := x[key]; ok {
					next = append(next, y)
				}
			case []any:
				if key == "*" {
					next = append(next, x...)
				} else if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(x) {
					next = append(next, x[i])
				}
			}
		}
		cur = next
	}
	return cur
}

// jsonElements are the members of every array found at path.
func jsonElements(v any, path string) []any {
	var out []any
	for _, x := range jsonAt(v, path) {
		out = append(out, jarr(x)...)
	}
	return out
}

func firstString(v any, paths ...string) string {
	for _, p := range paths {
		for _, x := range jsonAt(v, p) {
			if s := strings.TrimSpace(jstr(x)); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNumber(v any, paths ...string) float64 {
	for _, p := range paths {
		for _, x := range jsonAt(v, p) {
			if f, ok := jnum(x); ok && f > 0 {
				return f
			}
		}
	}
	return 0
}

// Where the services keep words, phrases, text, language and length; the
// first path that holds timed items wins. Words inside segments
// (openai-whisper, faster-whisper, WhisperX) are read segment by segment
// (nestedWords).
var (
	jsonWordPaths = []string{
		"words",         // ElevenLabs Scribe and forced alignment, OpenAI, AssemblyAI
		"word_segments", // WhisperX
		"results.channels.0.alternatives.0.words", // Deepgram
		"transcript.words",                        // this app's audio/transcript.json
		"transcripts.*.words",                     // ElevenLabs, several channels
		"results.items",                           // AWS Transcribe
		"results.*.alternatives.0.words",          // Google Speech-to-Text
		"monologues.*.elements",                   // Rev
		"utterances.*.words",                      // AssemblyAI utterances
		"result",                                  // Vosk
		"results",                                 // Speechmatics
	}
	jsonSegmentPaths = []string{
		"segments", "utterances", "results.utterances", "sentences", "paragraphs",
		"results.channels.0.alternatives.0.paragraphs.paragraphs.*.sentences", // Deepgram
		"transcript.segments", // this app's audio/transcript.json
		"chunks",              // Hugging Face pipelines
	}
	jsonTextPaths = []string{"text", "transcript", "transcript.text", "results.channels.0.alternatives.0.transcript", "results.transcripts.0.transcript"}
	jsonLangPaths = []string{"language_code", "language", "detected_language", "languageCode", "transcript.language",
		"results.channels.0.detected_language", "results.language_code", "results.0.languageCode", "metadata.language"}
	jsonDurPaths = []string{"audio_duration_secs", "duration", "audio_duration", "transcript.duration", "metadata.duration"}
)

// jsonItem is one timed element of a JSON transcript: a word or a phrase.
type jsonItem struct {
	text       string
	start, end float64 // NaN when missing
	raw        bool    // seconds or milliseconds — unitScale decides
	skip       bool    // spacing, sound events
}

var (
	jsonStartKeys = []string{"start", "start_time", "startTime", "startOffset", "start_ts", "begin", "ts", "from"}
	jsonEndKeys   = []string{"end", "end_time", "endTime", "endOffset", "end_ts", "stop", "to"}
	jsonTextKeys  = []string{"punctuated_word", "text", "word", "value", "content", "transcript", "sentence"}
)

func readItem(v any) (jsonItem, bool) {
	m := jobj(v)
	if m == nil {
		return jsonItem{}, false
	}
	it := jsonItem{start: math.NaN(), end: math.NaN(), raw: true}
	switch strings.ToLower(jstr(m["type"])) {
	case "spacing", "audio_event", "event", "silence", "noise", "music", "speaker_change":
		it.skip = true
	}
	for _, k := range jsonTextKeys {
		if s := jstr(m[k]); strings.TrimSpace(s) != "" {
			it.text = s
			break
		}
	}
	if it.text == "" { // AWS, Speechmatics: the best alternative
		if alts := jarr(m["alternatives"]); len(alts) > 0 {
			it.text = jstr(jobj(alts[0])["content"])
		}
	}
	for _, k := range jsonStartKeys {
		if x, ok := jnum(m[k]); ok {
			it.start = x
			break
		}
	}
	for _, k := range jsonEndKeys {
		if x, ok := jnum(m[k]); ok {
			it.end = x
			break
		}
	}
	if ts := jarr(m["timestamp"]); len(ts) == 2 { // Hugging Face: [start, end]
		if a, ok := jnum(ts[0]); ok {
			it.start = a
		}
		if b, ok := jnum(ts[1]); ok {
			it.end = b
		}
	}
	if a, b, ok := cppTimes(m); ok && !finite(it.start) {
		it.start, it.end, it.raw = a, b, false
	}
	return it, true
}

func readItems(list []any) []jsonItem {
	out := make([]jsonItem, 0, len(list))
	for _, v := range list {
		if it, ok := readItem(v); ok {
			out = append(out, it)
		}
	}
	return out
}

// anyTimed: at least one item has text and a start.
func anyTimed(items []jsonItem) bool {
	for _, it := range items {
		if !it.skip && finite(it.start) && strings.TrimSpace(it.text) != "" {
			return true
		}
	}
	return false
}

// wordLevel: the items hold single words, not phrases (nine in ten of them
// are one word).
func wordLevel(items []jsonItem) bool {
	n, single := 0, 0
	for _, it := range items {
		if it.skip || strings.TrimSpace(it.text) == "" {
			continue
		}
		n++
		if len(strings.Fields(it.text)) == 1 {
			single++
		}
	}
	return n > 0 && single*10 >= n*9
}

// unitScale is 0.001 when a list's times are milliseconds (AssemblyAI): its
// median duration is longer than limit seconds, which no word (20) or
// phrase (300) lasts.
func unitScale(items []jsonItem, limit float64) float64 {
	var d []float64
	for _, it := range items {
		if it.raw && finite(it.start) && finite(it.end) && it.end > it.start {
			d = append(d, it.end-it.start)
		}
	}
	if len(d) == 0 {
		return 1
	}
	sort.Float64s(d)
	if d[len(d)/2] > limit {
		return 0.001
	}
	return 1
}

// itemsToWords turns word items into words in seconds (times may stay NaN:
// fillWithin), sound notes dropped, lone punctuation merged.
func itemsToWords(items []jsonItem) []Word {
	scale := unitScale(items, 20)
	var out []Word
	for _, it := range items {
		text := strings.TrimSpace(it.text)
		if it.skip || text == "" || strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
			continue
		}
		s, e := it.start, it.end
		if it.raw {
			s, e = s*scale, e*scale
		}
		out = append(out, Word{Text: text, Start: s, End: e})
	}
	return mergePunct(out)
}

// fillWithin gives words without times the gap between their timed
// neighbours inside [from, to] (to NaN: open) — WhisperX leaves numbers
// untimed.
func fillWithin(words []Word, from, to float64) {
	// next[i]: the first timed start at or after word i (to when none).
	next := make([]float64, len(words)+1)
	next[len(words)] = to
	for i := len(words) - 1; i >= 0; i-- {
		if next[i] = next[i+1]; finite(words[i].Start) {
			next[i] = words[i].Start
		}
	}
	for i := range words {
		w := &words[i]
		if !finite(w.Start) {
			w.Start = from
			if i > 0 {
				w.Start = words[i-1].End
			}
		}
		if !finite(w.End) {
			if w.End = next[i+1]; !finite(w.End) {
				w.End = w.Start
			}
		}
		w.End = math.Max(w.Start, w.End)
	}
}

func hasTimes(words []Word) bool {
	for _, w := range words {
		if finite(w.Start) {
			return true
		}
	}
	return false
}

// itemsToSegments turns phrase items into ordered phrases in seconds, each
// with an end.
func itemsToSegments(items []jsonItem) []Segment {
	scale := unitScale(items, 300)
	var out []Segment
	for _, it := range items {
		if seg, ok := itemSegment(it, scale); ok {
			out = append(out, seg)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	closeEnds(out)
	return out
}

func itemSegment(it jsonItem, scale float64) (Segment, bool) {
	text := cleanCue(it.text)
	s, e := it.start, it.end
	if it.raw {
		s, e = s*scale, e*scale
	}
	if it.skip || text == "" || !finite(s) {
		return Segment{}, false
	}
	return Segment{Text: text, Start: s, End: e}, true
}

// nestedTimed: some segment carries timed words of its own.
func nestedTimed(root any) bool {
	for _, v := range jsonElements(root, "segments") {
		if anyTimed(readItems(jarr(jobj(v)["words"]))) {
			return true
		}
	}
	return false
}

// nestedWords reads segments that carry their own words (openai-whisper,
// faster-whisper, WhisperX): untimed words fall between their neighbours
// inside the segment, a segment without words shares its time by length.
func nestedWords(root any) ([]Segment, []Word) {
	var its []jsonItem
	var lists [][]any
	for _, v := range jsonElements(root, "segments") {
		if it, ok := readItem(v); ok {
			its = append(its, it)
			lists = append(lists, jarr(jobj(v)["words"]))
		}
	}
	type part struct {
		seg   Segment
		words []any
	}
	scale := unitScale(its, 300)
	var parts []part
	for i, it := range its {
		if seg, ok := itemSegment(it, scale); ok {
			parts = append(parts, part{seg, lists[i]})
		}
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].seg.Start < parts[j].seg.Start })
	segs := make([]Segment, len(parts))
	for i, p := range parts {
		segs[i] = p.seg
	}
	closeEnds(segs)
	var words []Word
	for i, s := range segs {
		ws := itemsToWords(readItems(parts[i].words))
		if !hasTimes(ws) {
			words = append(words, spreadWords(s)...)
			continue
		}
		fillWithin(ws, s.Start, s.End)
		words = append(words, ws...)
	}
	return segs, words
}

func hasWordRune(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// openingMark: punctuation that belongs to the word after it («, (, ¿ …).
func openingMark(s string) bool {
	for _, r := range s {
		if !unicode.In(r, unicode.Ps, unicode.Pi) && r != '¿' && r != '¡' {
			return false
		}
	}
	return s != ""
}

// mergePunct attaches punctuation that came as a word of its own (AWS, Rev,
// character alignments) to its neighbour: closing marks to the word before,
// opening ones to the word after.
func mergePunct(words []Word) []Word {
	out := words[:0]
	var pending, tail strings.Builder // marks for the next word / for the last one
	flush := func() {
		if tail.Len() > 0 {
			out[len(out)-1].Text += tail.String()
			tail.Reset()
		}
	}
	for _, w := range words {
		if hasWordRune(w.Text) {
			flush()
			if pending.Len() > 0 {
				w.Text = pending.String() + w.Text
				pending.Reset()
			}
			out = append(out, w)
			continue
		}
		if len(out) > 0 && !openingMark(w.Text) {
			tail.WriteString(w.Text)
		} else {
			pending.WriteString(w.Text)
		}
	}
	if len(out) > 0 {
		tail.WriteString(pending.String())
		flush()
	}
	return out
}

// coverSegments adds words spread over every phrase that no timed word falls
// in (servers that time only some phrases word by word).
func coverSegments(words []Word, segs []Segment) []Word {
	if len(segs) == 0 {
		return words
	}
	var starts []float64
	for _, w := range words {
		if finite(w.Start) {
			starts = append(starts, w.Start)
		}
	}
	sort.Float64s(starts)
	for _, s := range segs {
		if i := sort.SearchFloat64s(starts, s.Start-0.05); i < len(starts) && starts[i] <= s.End+0.05 {
			continue
		}
		words = append(words, spreadWords(s)...)
	}
	return words
}

// parseJSONItems reads the JSON of the speech services by where they keep
// their words and phrases (jsonWordPaths, jsonSegmentPaths, nestedWords); a
// bare array is a list of words or of phrases.
func parseJSONItems(root any) (Transcript, error) {
	var tr Transcript
	if !anyTimed(readItems(jsonElements(root, "words"))) && nestedTimed(root) {
		tr.Segments, tr.Words = nestedWords(root)
	} else {
		var words, segs []jsonItem
		if arr := jarr(root); arr != nil {
			if items := readItems(arr); wordLevel(items) {
				words = items
			} else {
				segs = items
			}
		} else {
			for _, p := range jsonWordPaths {
				if items := readItems(jsonElements(root, p)); anyTimed(items) {
					words = items
					break
				}
			}
			for _, p := range jsonSegmentPaths {
				if items := readItems(jsonElements(root, p)); anyTimed(items) {
					segs = items
					break
				}
			}
			if !anyTimed(words) && wordLevel(segs) { // phrases of one word each
				words, segs = segs, nil
			}
		}
		if !anyTimed(words) && !anyTimed(segs) {
			if firstString(root, jsonTextPaths...) != "" {
				return Transcript{}, ErrNoTimecodes
			}
			return Transcript{}, errUnknownJSON
		}
		tr.Words, tr.Segments = itemsToWords(words), itemsToSegments(segs)
		fillWithin(tr.Words, 0, math.NaN())
		tr.Words = coverSegments(tr.Words, tr.Segments)
	}
	tr.Text = firstString(root, jsonTextPaths...)
	if tr.Text == "" { // Google: one transcript per result
		var parts []string
		for _, x := range jsonAt(root, "results.*.alternatives.0.transcript") {
			if s := strings.TrimSpace(jstr(x)); s != "" {
				parts = append(parts, s)
			}
		}
		tr.Text = joinTexts(parts)
	}
	tr.Language = firstString(root, jsonLangPaths...)
	tr.Duration = firstNumber(root, jsonDurPaths...)
	return tr, nil
}

// ---- whisper.cpp and ElevenLabs alignments --------------------------------------------------------

// cppTimes reads whisper.cpp's "offsets" (milliseconds) or "timestamps"
// ("00:00:01,500") of a segment or token.
func cppTimes(m map[string]any) (start, end float64, ok bool) {
	if off := jobj(m["offsets"]); off != nil {
		if a, ok := jnum(off["from"]); ok {
			b, ok := jnum(off["to"])
			if !ok {
				b = a
			}
			return a / 1000, b / 1000, true
		}
	}
	if ts := jobj(m["timestamps"]); ts != nil {
		if a, ok := jnum(ts["from"]); ok {
			b, ok := jnum(ts["to"])
			if !ok {
				b = a
			}
			return a, b, true
		}
	}
	return 0, 0, false
}

// reSpecial: whisper's control tokens ([_BEG_], [_TT_150], <|endoftext|>).
var reSpecial = regexp.MustCompile(`^\s*(?:\[_[^\]]*\]|<\|[^|]*\|>)\s*$`)

// parseWhisperCpp reads whisper.cpp's -oj / -ojf output: segments with
// offsets in milliseconds; -ojf adds timed tokens, from which the words get
// their times.
func parseWhisperCpp(root map[string]any) (Transcript, error) {
	type part struct {
		seg    Segment
		tokens []any
	}
	var parts []part
	for _, v := range jarr(root["transcription"]) {
		s := jobj(v)
		if s == nil {
			continue
		}
		start, end, ok := cppTimes(s)
		text := cleanCue(jstr(s["text"]))
		if !ok || text == "" {
			continue
		}
		parts = append(parts, part{Segment{Text: text, Start: start, End: end}, jarr(s["tokens"])})
	}
	if len(parts) == 0 {
		return Transcript{}, ErrNoTimecodes
	}
	sort.SliceStable(parts, func(i, j int) bool { return parts[i].seg.Start < parts[j].seg.Start })
	var tr Transcript
	tr.Segments = make([]Segment, len(parts))
	for i, p := range parts {
		tr.Segments[i] = p.seg
	}
	closeEnds(tr.Segments)
	for i, seg := range tr.Segments {
		tr.Words = append(tr.Words, cppWords(parts[i].tokens, seg)...)
	}
	tr.Language = firstString(root, "result.language")
	if l := firstString(root, "params.language"); tr.Language == "" && !strings.EqualFold(l, "auto") {
		tr.Language = l
	}
	return tr, nil
}

// cppWords are a segment's words timed by its tokens: a token starting with
// a space starts a word. The words' text is the segment's own (a token may
// hold half of a multi-byte letter); when the counts disagree the segment's
// time is shared by length instead.
func cppWords(tokens []any, seg Segment) []Word {
	type group struct{ start, end float64 } // a word's tokens
	var gs []group
	for _, v := range tokens {
		t := jobj(v)
		text := jstr(t["text"])
		if strings.TrimSpace(text) == "" || reSpecial.MatchString(text) {
			continue
		}
		a, b, ok := cppTimes(t)
		if !ok {
			continue
		}
		if strings.HasPrefix(text, " ") || len(gs) == 0 {
			gs = append(gs, group{a, math.Max(a, b)})
			continue
		}
		g := &gs[len(gs)-1]
		g.end = math.Max(g.end, b)
	}
	fields := strings.Fields(seg.Text)
	if len(gs) > 0 && len(gs) == len(fields) {
		out := make([]Word, len(fields))
		for i, f := range fields {
			out[i] = Word{Text: f, Start: gs[i].start, End: gs[i].end}
		}
		return out
	}
	return spreadWords(seg)
}

// parseAlignment reads ElevenLabs text-to-speech "with timestamps": a time
// for every character; words are the runs between spaces.
func parseAlignment(root map[string]any) (Transcript, error) {
	a := jobj(root["alignment"])
	if a == nil {
		a = jobj(root["normalized_alignment"])
	}
	chars := jarr(a["characters"])
	starts := jarr(a["character_start_times_seconds"])
	ends := jarr(a["character_end_times_seconds"])
	n := min(len(chars), len(starts), len(ends))
	var tr Transcript
	var text, word strings.Builder
	closeWord := func() {
		if word.Len() > 0 {
			tr.Words[len(tr.Words)-1].Text = word.String()
			word.Reset()
		}
	}
	for i := 0; i < n; i++ {
		c := jstr(chars[i])
		text.WriteString(c)
		if strings.TrimSpace(c) == "" {
			closeWord()
			continue
		}
		s, ok := jnum(starts[i])
		if !ok {
			s = math.NaN()
		}
		e, ok := jnum(ends[i])
		if !ok {
			e = s
		}
		if word.Len() == 0 {
			tr.Words = append(tr.Words, Word{Start: s, End: e})
		} else if finite(e) {
			tr.Words[len(tr.Words)-1].End = e
		}
		word.WriteString(c)
	}
	closeWord()
	tr.Words = mergePunct(tr.Words)
	if !hasTimes(tr.Words) {
		return Transcript{}, ErrNoTimecodes
	}
	fillWithin(tr.Words, 0, math.NaN())
	tr.Text = text.String()
	return tr, nil
}
