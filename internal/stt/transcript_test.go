package stt

import (
	"errors"
	"math"
	"strings"
	"testing"
	"unicode/utf16"
)

// ---- helpers ------------------------------------------------------------------------------

func parse(t *testing.T, name, data string) Transcript {
	t.Helper()
	tr, err := ParseTranscript([]byte(data), name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return tr
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.0015 }

// wantWords checks the words' texts and, where given (not NaN), their times.
func wantWords(t *testing.T, what string, got []Word, want ...Word) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d words %+v, want %d %+v", what, len(got), got, len(want), want)
	}
	for i, w := range want {
		g := got[i]
		if g.Text != w.Text || !math.IsNaN(w.Start) && !near(g.Start, w.Start) || !math.IsNaN(w.End) && !near(g.End, w.End) {
			t.Fatalf("%s: word %d %+v, want %+v (all %+v)", what, i, g, w, got)
		}
	}
}

func w(text string, start, end float64) Word { return Word{Text: text, Start: start, End: end} }

// wt is a word whose times are not checked.
func wt(text string) Word { return Word{Text: text, Start: math.NaN(), End: math.NaN()} }

func wantSegments(t *testing.T, what string, got []Segment, want ...Segment) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d segments %+v, want %d %+v", what, len(got), got, len(want), want)
	}
	for i, s := range want {
		g := got[i]
		if g.Text != s.Text || !near(g.Start, s.Start) || !near(g.End, s.End) {
			t.Fatalf("%s: segment %d %+v, want %+v", what, i, g, s)
		}
	}
}

func sg(text string, start, end float64) Segment { return Segment{Text: text, Start: start, End: end} }

// checkInvariants: what every parsed transcript keeps — words and segments
// present, ordered, finite, End ≥ Start; Duration covers the speech.
func checkInvariants(t *testing.T, what string, tr Transcript) {
	t.Helper()
	if len(tr.Words) == 0 || len(tr.Segments) == 0 || tr.Text == "" {
		t.Fatalf("%s: empty parts %+v", what, tr)
	}
	for i, x := range tr.Words {
		if !finite(x.Start) || !finite(x.End) || x.End < x.Start || x.Start < 0 || i > 0 && x.Start < tr.Words[i-1].Start {
			t.Fatalf("%s: word %d %+v", what, i, x)
		}
	}
	for i, s := range tr.Segments {
		if !finite(s.Start) || !finite(s.End) || s.End < s.Start || i > 0 && s.Start < tr.Segments[i-1].Start {
			t.Fatalf("%s: segment %d %+v", what, i, s)
		}
	}
	if tr.Duration < speechEnd(tr)-1e-9 {
		t.Fatalf("%s: duration %.3f < speech %.3f", what, tr.Duration, speechEnd(tr))
	}
}

// ---- subtitles ---------------------------------------------------------------------------------

func TestParseSRT(t *testing.T) {
	srt := "\xef\xbb\xbf1\r\n00:00:00,500 --> 00:00:02,500\r\n<i>Жила-була</i> лисичка.\r\n\r\n" +
		"2\r\n00:00:03,000 --> 00:00:05,000\r\nВона мала\r\n<font color=\"#ff0\">теплу нору</font>.\r\n\r\n" +
		"3\r\n00:00:05,200 --> 00:00:06,000\r\n[Музика]\r\n\r\n" +
		"4\r\n00:00:06,500 --> 00:00:08,000 X1:10 X2:20 Y1:5 Y2:9\r\n{\\an8}Кінець — &amp; все!\r\n"
	tr := parse(t, "Лисичка.uk.srt", srt)
	checkInvariants(t, "srt", tr)
	wantSegments(t, "srt", tr.Segments,
		sg("Жила-була лисичка.", 0.5, 2.5), sg("Вона мала теплу нору.", 3, 5), sg("Кінець — & все!", 6.5, 8))
	// Words share their cue's time by length ("Жила-була" 9 + "лисичка." 8
	// letters); punctuation alone ("—", "&") joins the word before it.
	wantWords(t, "srt", tr.Words,
		w("Жила-була", 0.5, 0.5+2*9.0/17), w("лисичка.", 0.5+2*9.0/17, 2.5),
		wt("Вона"), wt("мала"), wt("теплу"), w("нору.", math.NaN(), 5),
		w("Кінець—&", 6.5, 6.5+1.5*6/12), w("все!", math.NaN(), 8))
	if tr.Text != "Жила-була лисичка. Вона мала теплу нору. Кінець — & все!" || tr.Duration != 8 || tr.Language != "uk" {
		t.Fatalf("srt text %q, duration %v, language %q", tr.Text, tr.Duration, tr.Language)
	}

	// No blank lines between cues (sloppy exports): numbers are not text.
	tight := "1\n00:00:01,000 --> 00:00:02,000\nРаз.\n2\n00:00:02,000 --> 00:00:03,000\nДва.\n"
	wantSegments(t, "tight", parse(t, "a.srt", tight).Segments, sg("Раз.", 1, 2), sg("Два.", 2, 3))

	// Word-by-word captions: phrases are grouped for the preview.
	words := "1\n00:00:01,000 --> 00:00:01,400\nПривіт,\n\n2\n00:00:01,400 --> 00:00:01,900\nсвіте!\n\n" +
		"3\n00:00:03,000 --> 00:00:03,400\nЯк\n\n4\n00:00:03,400 --> 00:00:04,000\nсправи?\n"
	tr = parse(t, "words.srt", words)
	wantWords(t, "word cues", tr.Words, w("Привіт,", 1, 1.4), w("світе!", 1.4, 1.9), w("Як", 3, 3.4), w("справи?", 3.4, 4))
	wantSegments(t, "word cues", tr.Segments, sg("Привіт, світе!", 1, 1.9), sg("Як справи?", 3, 4))
}

func TestParseWebVTT(t *testing.T) {
	vtt := "WEBVTT - Казка\nKind: captions\nLanguage: uk\n\nSTYLE\n::cue { color: yellow }\n\n" +
		"NOTE це коментар\nна два рядки\n\nintro\n00:01.000 --> 00:03.000 align:start position:10%\n<v Оповідач>Жила-була лисичка.</v>\n\n" +
		"00:03.500 --> 00:05.000\n<c.yellow>Вона</c> мала нору.\n\n" +
		"01:00:00.000 --> 01:00:01.500\n- Кінець.\n"
	tr := parse(t, "kazka.vtt", vtt)
	checkInvariants(t, "vtt", tr)
	wantSegments(t, "vtt", tr.Segments, sg("Жила-була лисичка.", 1, 3), sg("Вона мала нору.", 3.5, 5), sg("- Кінець.", 3600, 3601.5))
	if tr.Language != "uk" || tr.Duration != 3601.5 || tr.Words[len(tr.Words)-1].Text != "-Кінець." {
		t.Fatalf("vtt language %q duration %v words %+v", tr.Language, tr.Duration, tr.Words)
	}
}

// TestParseRollingCaptions: YouTube's automatic captions repeat the previous
// line in every cue (with tiny in-between cues) and time each word inline.
func TestParseRollingCaptions(t *testing.T) {
	vtt := "WEBVTT\nKind: captions\nLanguage: en\n\n" +
		"00:00:00.000 --> 00:00:02.869 align:start position:0%\n \nhello<00:00:00.420><c> everyone</c><00:00:00.840><c> welcome</c><00:00:01.290><c> back</c>\n\n" +
		"00:00:02.869 --> 00:00:02.879 align:start position:0%\nhello everyone welcome back\n \n\n" +
		"00:00:02.879 --> 00:00:05.150 align:start position:0%\nhello everyone welcome back\nto<00:00:03.240><c> my</c><00:00:03.570><c> channel</c>\n\n" +
		"00:00:05.150 --> 00:00:05.160 align:start position:0%\nto my channel\n \n\n" +
		"00:00:05.160 --> 00:00:07.000 align:start position:0%\nto my channel\n[Music]\n"
	tr := parse(t, "video.en.vtt", vtt)
	checkInvariants(t, "rolling vtt", tr)
	wantWords(t, "rolling vtt", tr.Words,
		w("hello", 0, 0.42), w("everyone", 0.42, 0.84), w("welcome", 0.84, 1.29), w("back", 1.29, 2.869),
		w("to", 2.879, 3.24), w("my", 3.24, 3.57), w("channel", 3.57, 5.15))
	if tr.Text != "hello everyone welcome back to my channel" || tr.Language != "en" || len(tr.Segments) != 2 {
		t.Fatalf("rolling vtt %q %q %+v", tr.Text, tr.Language, tr.Segments)
	}

	// The same converted to SRT (inline timings lost): no repeats either, but
	// a line said twice on purpose stays.
	srt := "1\n00:00:00,000 --> 00:00:02,869\nhello everyone welcome back\n\n" +
		"2\n00:00:02,869 --> 00:00:02,879\nhello everyone welcome back\n\n" +
		"3\n00:00:02,879 --> 00:00:05,150\nhello everyone welcome back\nto my channel\n\n" +
		"4\n00:00:05,150 --> 00:00:07,000\nТак.\n\n5\n00:00:07,000 --> 00:00:08,000\nТак.\n"
	tr = parse(t, "rolling.srt", srt)
	if tr.Text != "hello everyone welcome back to my channel Так. Так." {
		t.Fatalf("rolling srt %q", tr.Text)
	}
	wantSegments(t, "rolling srt", tr.Segments, sg("hello everyone welcome back", 0, 2.869), sg("to my channel", 2.879, 5.15),
		sg("Так.", 5.15, 7), sg("Так.", 7, 8))
}

// ---- text with timestamps ------------------------------------------------------------------------

func TestParseTimedText(t *testing.T) {
	say := func(s string) float64 { return math.Max(1, float64(len([]rune(s)))/12+0.5) }
	for _, tc := range []struct {
		name, data string
		want       []Segment
	}{
		{"brackets.txt", "[00:00] Жила-була лисичка.\n\n[00:03] Вона мала нору.\n[00:06.5] Кінець.",
			[]Segment{sg("Жила-була лисичка.", 0, 3), sg("Вона мала нору.", 3, 6.5), sg("Кінець.", 6.5, 6.5+say("Кінець."))}},
		{"youtube.txt", "Транскрипт\n0:00\nЖила-була лисичка.\n0:03\nВона мала\nнору.\n1:02:03\nКінець.",
			[]Segment{sg("Жила-була лисичка.", 0, 3), sg("Вона мала нору.", 3, 3723), sg("Кінець.", 3723, 3723+say("Кінець."))}},
		{"dashes.txt", "00:00 - Жила-була лисичка.\n00:03 — Вона мала нору.\n00:05: Кінець.",
			[]Segment{sg("Жила-була лисичка.", 0, 3), sg("Вона мала нору.", 3, 5), sg("Кінець.", 5, 5+say("Кінець."))}},
		{"ranges.txt", "00:00:00,500 --> 00:00:02,500 Жила-була лисичка.\n00:00:03,000 --> 00:00:05,000 Кінець.",
			[]Segment{sg("Жила-була лисичка.", 0.5, 2.5), sg("Кінець.", 3, 5)}},
		{"whisper.txt", "[00:00.000 --> 00:02.000]  Жила-була лисичка.\n[00:02.000 --> 00:04.500]  Кінець.\n",
			[]Segment{sg("Жила-була лисичка.", 0, 2), sg("Кінець.", 2, 4.5)}},
		{"labels.txt", "0.500000\t2.500000\tЖила-була лисичка.\n3,000000\t5,000000\tКінець.\n",
			[]Segment{sg("Жила-була лисичка.", 0.5, 2.5), sg("Кінець.", 3, 5)}},
		{"youtube.sbv", "0:00:00.500,0:00:02.500\nЖила-була лисичка.\n\n0:00:03.000,0:00:05.000\nКінець.\n",
			[]Segment{sg("Жила-була лисичка.", 0.5, 2.5), sg("Кінець.", 3, 5)}},
		{"song.lrc", "[ar:Автор]\n[ti:Казка]\n[00:00.50]Жила-була лисичка.\n[00:03.00][00:09.00]Кінець.\n",
			[]Segment{sg("Жила-була лисичка.", 0.5, 3), sg("Кінець.", 3, 3+say("Кінець."))}},
		{"premiere.txt", "00:00:00:00 - 00:00:02:12\nSpeaker 1\nЖила-була лисичка.\n\n00:00:02:12 - 00:00:05:00\nSpeaker 1\nКінець.\n",
			[]Segment{sg("Жила-була лисичка.", 0, 2.48), sg("Кінець.", 2.48, 5)}}, // 25 fps
	} {
		tr := parse(t, tc.name, tc.data)
		checkInvariants(t, tc.name, tr)
		wantSegments(t, tc.name, tr.Segments, tc.want...)
	}
}

// ---- encodings -----------------------------------------------------------------------------------

func TestParseEncodings(t *testing.T) {
	const srt = "1\r\n00:00:01,000 --> 00:00:03,000\r\nЇжак і ґава — «є»!\r\n"
	utf16le := func(s string, bom bool) []byte {
		var b []byte
		if bom {
			b = append(b, 0xFF, 0xFE)
		}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	}
	utf16be := func(s string) []byte {
		b := []byte{0xFE, 0xFF}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u>>8), byte(u))
		}
		return b
	}
	cp1251 := func(s string) []byte {
		rev := map[rune]byte{}
		for i, r := range cp1251High {
			rev[r] = byte(0x80 + i)
		}
		var b []byte
		for _, r := range s {
			switch {
			case r < 0x80:
				b = append(b, byte(r))
			case r >= 'А' && r <= 'я':
				b = append(b, byte(0xC0+r-'А'))
			default:
				c, ok := rev[r]
				if !ok {
					t.Fatalf("no cp1251 byte for %q", r)
				}
				b = append(b, c)
			}
		}
		return b
	}
	for name, data := range map[string][]byte{
		"utf-16le":       utf16le(srt, true),
		"utf-16le-nobom": utf16le(srt, false),
		"utf-16be":       utf16be(srt),
		"windows-1251":   cp1251(srt),
		"utf-8-cut":      append([]byte(srt), 0xD0), // a file cut inside a letter stays UTF-8
	} {
		tr, err := ParseTranscript(data, "x.srt")
		if err != nil || tr.Text != "Їжак і ґава — «є»!" {
			t.Errorf("%s: %q %v", name, tr.Text, err)
		}
	}
}

// ---- JSON of the speech services ----------------------------------------------------------------

func TestParseScribeJSON(t *testing.T) {
	js := `{"language_code":"ukr","language_probability":0.98,"text":"Привіт, світе! (сміх) Як справи?","audio_duration_secs":3.5,
	 "words":[
	   {"text":"Привіт,","start":0.1,"end":0.5,"type":"word","speaker_id":"speaker_0"},
	   {"text":" ","start":0.5,"end":0.6,"type":"spacing"},
	   {"text":"світе!","start":0.6,"end":1.0,"type":"word"},
	   {"text":" ","start":1.0,"end":1.2,"type":"spacing"},
	   {"text":"(сміх)","start":1.2,"end":1.8,"type":"audio_event"},
	   {"text":"Як","start":2.0,"end":2.2,"type":"word"},
	   {"text":"справи?","start":2.2,"end":2.7,"type":"word"}]}`
	tr := parse(t, "scribe.json", js)
	checkInvariants(t, "scribe", tr)
	wantWords(t, "scribe", tr.Words, w("Привіт,", 0.1, 0.5), w("світе!", 0.6, 1), w("Як", 2, 2.2), w("справи?", 2.2, 2.7))
	wantSegments(t, "scribe", tr.Segments, sg("Привіт, світе!", 0.1, 1), sg("Як справи?", 2, 2.7))
	if tr.Language != "uk" || tr.Duration != 3.5 || tr.Text != "Привіт, світе! (сміх) Як справи?" {
		t.Fatalf("scribe %q %v %q", tr.Language, tr.Duration, tr.Text)
	}

	// Several channels: the words of all of them, in time order.
	multi := `{"transcripts":[{"language_code":"en","text":"Hi there.","words":[{"text":"Hi","start":0,"end":0.3,"type":"word"},{"text":"there.","start":0.4,"end":0.8,"type":"word"}]},
	  {"text":"Hello.","words":[{"text":"Hello.","start":0.5,"end":0.9,"type":"word"}]}]}`
	tr = parse(t, "multi.json", multi)
	wantWords(t, "multichannel", tr.Words, w("Hi", 0, 0.3), w("there.", 0.4, 0.8), w("Hello.", 0.5, 0.9))
}

func TestParseWhisperJSON(t *testing.T) {
	// OpenAI verbose_json: words and segments side by side.
	js := `{"task":"transcribe","language":"ukrainian","duration":5.0,"text":"Привіт світе. Як справи?",
	 "words":[{"word":"Привіт","start":0.1,"end":0.5},{"word":"світе","start":0.6,"end":1.0},{"word":"Як","start":2.0,"end":2.2},{"word":"справи","start":2.2,"end":2.7}],
	 "segments":[{"id":0,"seek":0,"start":0.0,"end":1.2,"text":" Привіт світе.","tokens":[1,2]},{"id":1,"start":1.9,"end":2.9,"text":" Як справи?"}]}`
	tr := parse(t, "verbose.json", js)
	checkInvariants(t, "verbose_json", tr)
	wantWords(t, "verbose_json", tr.Words, w("Привіт", 0.1, 0.5), w("світе", 0.6, 1), w("Як", 2, 2.2), w("справи", 2.2, 2.7))
	wantSegments(t, "verbose_json", tr.Segments, sg("Привіт світе.", 0, 1.2), sg("Як справи?", 1.9, 2.9))
	if tr.Language != "uk" || tr.Duration != 5 || tr.Text != "Привіт світе. Як справи?" {
		t.Fatalf("verbose_json %q %v %q", tr.Language, tr.Duration, tr.Text)
	}

	// openai-whisper / WhisperX: words inside segments; a segment without
	// words is spread by length, an untimed word (a number) sits between its
	// neighbours inside its segment.
	nested := `{"language":"uk","text":" Раз два. Три чотири п'ять. 2024 рік.","segments":[
	  {"start":0.0,"end":1.0,"text":" Раз два.","words":[{"word":" Раз","start":0.0,"end":0.4,"probability":0.9},{"word":" два.","start":0.5,"end":1.0}]},
	  {"start":1.5,"end":3.0,"text":" Три чотири п'ять."},
	  {"start":3.5,"end":4.5,"text":" 2024 рік.","words":[{"word":"2024","score":0.5},{"word":"рік.","start":4.0,"end":4.5,"score":0.9}]}],
	 "word_segments":[{"word":"Раз","start":0.0,"end":0.4}]}`
	tr = parse(t, "whisperx.json", nested)
	checkInvariants(t, "nested", tr)
	wantWords(t, "nested", tr.Words, w("Раз", 0, 0.4), w("два.", 0.5, 1), w("Три", 1.5, 1.8), w("чотири", 1.8, 2.4), w("п'ять.", 2.4, 3),
		w("2024", 3.5, 4), w("рік.", 4, 4.5))
	wantSegments(t, "nested", tr.Segments, sg("Раз два.", 0, 1), sg("Три чотири п'ять.", 1.5, 3), sg("2024 рік.", 3.5, 4.5))

	// Segments only: words spread over each phrase.
	segs := `{"text":"Раз два. Три.","segments":[{"start":1,"end":2,"text":"Раз два."},{"start":3,"end":3.5,"text":"Три."}]}`
	wantWords(t, "segments only", parse(t, "segments.json", segs).Words, w("Раз", 1, 1+3.0/7), w("два.", 1+3.0/7, 2), w("Три.", 3, 3.5))

	// A bare array of phrases (Hugging Face chunks style too).
	arr := `[{"timestamp":[0.5,2.0],"text":" Жила-була лисичка."},{"timestamp":[2.5,4.0],"text":" Кінець."}]`
	wantSegments(t, "array", parse(t, "chunks.json", arr).Segments, sg("Жила-була лисичка.", 0.5, 2), sg("Кінець.", 2.5, 4))
}

func TestParseDeepgramAndAssemblyAI(t *testing.T) {
	dg := `{"metadata":{"request_id":"x","duration":4.2,"channels":1},
	 "results":{"channels":[{"detected_language":"uk","alternatives":[{"transcript":"Привіт, світе. Як справи?","confidence":0.98,
	   "words":[{"word":"привіт","start":0.1,"end":0.5,"confidence":0.9,"punctuated_word":"Привіт,"},
	            {"word":"світе","start":0.6,"end":1.0,"punctuated_word":"світе."},
	            {"word":"як","start":2.0,"end":2.2,"punctuated_word":"Як"},
	            {"word":"справи","start":2.2,"end":2.7,"punctuated_word":"справи?"}]}]}],
	  "utterances":[{"start":0.1,"end":1.0,"transcript":"Привіт, світе."},{"start":2.0,"end":2.7,"transcript":"Як справи?"}]}}`
	tr := parse(t, "deepgram.json", dg)
	checkInvariants(t, "deepgram", tr)
	wantWords(t, "deepgram", tr.Words, w("Привіт,", 0.1, 0.5), w("світе.", 0.6, 1), w("Як", 2, 2.2), w("справи?", 2.2, 2.7))
	wantSegments(t, "deepgram", tr.Segments, sg("Привіт, світе.", 0.1, 1), sg("Як справи?", 2, 2.7))
	if tr.Language != "uk" || tr.Duration != 4.2 || tr.Text != "Привіт, світе. Як справи?" {
		t.Fatalf("deepgram %q %v %q", tr.Language, tr.Duration, tr.Text)
	}

	// AssemblyAI: milliseconds.
	aai := `{"id":"abc","status":"completed","language_code":"en_us","audio_duration":4,"text":"Hello world. How are you?",
	 "words":[{"text":"Hello","start":100,"end":500,"confidence":0.99,"speaker":null},{"text":"world.","start":600,"end":1000},
	          {"text":"How","start":2000,"end":2200},{"text":"are","start":2200,"end":2400},{"text":"you?","start":2400,"end":2900}],
	 "utterances":[{"start":100,"end":1000,"text":"Hello world.","speaker":"A"},{"start":2000,"end":2900,"text":"How are you?","speaker":"A"}]}`
	tr = parse(t, "assemblyai.json", aai)
	checkInvariants(t, "assemblyai", tr)
	wantWords(t, "assemblyai", tr.Words, w("Hello", 0.1, 0.5), w("world.", 0.6, 1), w("How", 2, 2.2), w("are", 2.2, 2.4), w("you?", 2.4, 2.9))
	wantSegments(t, "assemblyai", tr.Segments, sg("Hello world.", 0.1, 1), sg("How are you?", 2, 2.9))
	if tr.Language != "en" || tr.Duration != 4 {
		t.Fatalf("assemblyai %q %v", tr.Language, tr.Duration)
	}
}

func TestParseWhisperCppJSON(t *testing.T) {
	oj := `{"systeminfo":"AVX = 1","model":{"type":"base"},"params":{"model":"models/ggml-base.bin","language":"auto","translate":false},"result":{"language":"uk"},
	 "transcription":[
	  {"timestamps":{"from":"00:00:00,000","to":"00:00:02,000"},"offsets":{"from":0,"to":2000},"text":" Жила-була лисичка."},
	  {"timestamps":{"from":"00:00:02,500","to":"00:00:04,000"},"offsets":{"from":2500,"to":4000},"text":" [BLANK_AUDIO]"},
	  {"timestamps":{"from":"00:00:04,000","to":"00:00:06,000"},"text":" Кінець."}]}`
	tr := parse(t, "whisper.cpp.json", oj)
	checkInvariants(t, "-oj", tr)
	wantSegments(t, "-oj", tr.Segments, sg("Жила-була лисичка.", 0, 2), sg("Кінець.", 4, 6))
	wantWords(t, "-oj", tr.Words, w("Жила-була", 0, 2*9.0/17), w("лисичка.", 2*9.0/17, 2), w("Кінець.", 4, 6))
	if tr.Language != "uk" {
		t.Fatalf("-oj language %q", tr.Language)
	}

	// -ojf: tokens time the words (a space starts one; control tokens skipped).
	ojf := `{"result":{"language":"en"},"transcription":[{"offsets":{"from":0,"to":3000},"text":" Hello, world.","tokens":[
	  {"text":"[_BEG_]","offsets":{"from":0,"to":0},"id":50364,"p":0.9},
	  {"text":" Hello","offsets":{"from":0,"to":800},"id":1,"p":0.9},
	  {"text":",","offsets":{"from":800,"to":900},"id":2,"p":0.9},
	  {"text":" wor","offsets":{"from":1000,"to":1500},"id":3,"p":0.9},
	  {"text":"ld","offsets":{"from":1500,"to":2000},"id":4,"p":0.9},
	  {"text":".","offsets":{"from":2000,"to":2100},"id":5,"p":0.9},
	  {"text":"[_TT_150]","offsets":{"from":3000,"to":3000},"id":6,"p":0.9}]}]}`
	tr = parse(t, "full.json", ojf)
	wantWords(t, "-ojf", tr.Words, w("Hello,", 0, 0.9), w("world.", 1, 2.1))
	if tr.Language != "en" {
		t.Fatalf("-ojf language %q", tr.Language)
	}
}

func TestParseOtherJSON(t *testing.T) {
	// ElevenLabs text-to-speech with timestamps: a time per character.
	al := `{"audio_base64":"AAAA","alignment":{"characters":["H","i","!"," ","Y","o","u"," ","—"," ","o","k","."],
	  "character_start_times_seconds":[0,0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0,1.1,1.2],
	  "character_end_times_seconds":[0.1,0.2,0.3,0.4,0.5,0.6,0.7,0.8,0.9,1.0,1.1,1.2,1.3]},"normalized_alignment":{}}`
	tr := parse(t, "tts.json", al)
	checkInvariants(t, "alignment", tr)
	wantWords(t, "alignment", tr.Words, w("Hi!", 0, 0.3), w("You—", 0.4, 0.7), w("ok.", 1, 1.3))
	if tr.Text != "Hi! You — ok." {
		t.Fatalf("alignment text %q", tr.Text)
	}

	// AWS Transcribe: string times, punctuation as items of its own.
	aws := `{"jobName":"j","accountId":"1","status":"COMPLETED","results":{"transcripts":[{"transcript":"Hello, world."}],
	 "items":[{"start_time":"0.04","end_time":"0.5","alternatives":[{"confidence":"0.99","content":"Hello"}],"type":"pronunciation"},
	          {"alternatives":[{"confidence":"0.0","content":","}],"type":"punctuation"},
	          {"start_time":"0.6","end_time":"1.1","alternatives":[{"confidence":"0.99","content":"world"}],"type":"pronunciation"},
	          {"alternatives":[{"confidence":"0.0","content":"."}],"type":"punctuation"}]}}`
	tr = parse(t, "aws.json", aws)
	wantWords(t, "aws", tr.Words, w("Hello,", 0.04, 0.5), w("world.", 0.6, 1.1))
	if tr.Text != "Hello, world." {
		t.Fatalf("aws text %q", tr.Text)
	}

	// Google Speech-to-Text: "1.500s" times, one transcript per result.
	google := `{"results":[{"alternatives":[{"transcript":"hello world","confidence":0.9,"words":[{"startTime":"0.100s","endTime":"0.500s","word":"hello"},{"startTime":"0.600s","endTime":"1s","word":"world"}]}],"languageCode":"en-us"},
	  {"alternatives":[{"transcript":" how are you","words":[{"startTime":"2s","endTime":"2.200s","word":"how"},{"startTime":"2.200s","endTime":"2.400s","word":"are"},{"startTime":"2.400s","endTime":"2.900s","word":"you"}]}],"languageCode":"en-us"}]}`
	tr = parse(t, "google.json", google)
	wantWords(t, "google", tr.Words, w("hello", 0.1, 0.5), w("world", 0.6, 1), w("how", 2, 2.2), w("are", 2.2, 2.4), w("you", 2.4, 2.9))
	if tr.Text != "hello world how are you" || tr.Language != "en" {
		t.Fatalf("google %q %q", tr.Text, tr.Language)
	}

	// Rev: elements with ts/end_ts, punctuation and spaces in between.
	rev := `{"monologues":[{"speaker":0,"elements":[{"type":"text","value":"Hello","ts":0.5,"end_ts":0.9},{"type":"punct","value":","},
	  {"type":"punct","value":" "},{"type":"text","value":"world","ts":1.0,"end_ts":1.4},{"type":"punct","value":"."}]}]}`
	wantWords(t, "rev", parse(t, "rev.json", rev).Words, w("Hello,", 0.5, 0.9), w("world.", 1, 1.4))

	// JSON Lines (Vosk: one result per line).
	vosk := "{\"result\":[{\"conf\":1.0,\"end\":1.02,\"start\":0.63,\"word\":\"привіт\"}],\"text\":\"привіт\"}\n" +
		"{\"text\":\"\"}\n{\"result\":[{\"conf\":1.0,\"end\":2.5,\"start\":2.0,\"word\":\"світе\"}],\"text\":\"світе\"}\n"
	tr = parse(t, "vosk.json", vosk)
	wantWords(t, "vosk", tr.Words, w("привіт", 0.63, 1.02), w("світе", 2, 2.5))
	if tr.Text != "привіт світе" {
		t.Fatalf("vosk text %q", tr.Text)
	}

	// This app's own sidecar (audio/transcript.json of another project).
	own := `{"voiceFile":"audio/v.mp3","provider":"openai","createdAt":1,"transcript":{"text":"Раз два.","language":"uk","duration":3,
	  "words":[{"text":"Раз","start":0.5,"end":0.9},{"text":"два.","start":1,"end":1.5}],"segments":[{"text":"Раз два.","start":0.5,"end":1.5}]}}`
	tr = parse(t, "transcript.json", own)
	wantWords(t, "sidecar", tr.Words, w("Раз", 0.5, 0.9), w("два.", 1, 1.5))
	if tr.Text != "Раз два." || tr.Language != "uk" || tr.Duration != 3 {
		t.Fatalf("sidecar %+v", tr)
	}
}

// ---- errors and helpers --------------------------------------------------------------------

func TestParseTranscriptErrors(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		want       string
	}{
		{"empty.srt", " \n\t ", "порожній"},
		{"plain.txt", "Жила-була лисичка.\nВона мала нору.", "немає таймкодів"},
		{"broken.json", `{"words":[{"text":"a","start":0.1`, "JSON пошкоджений"},
		{"unknown.json", `{"foo":1,"bar":[1,2]}`, "Не впізнаю цей JSON"},
		{"text-only.json", `{"text":"Привіт"}`, "немає таймкодів"},
		{"zeros.json", `{"words":[{"text":"a","start":0,"end":0},{"text":"b","start":0,"end":0}]}`, "немає таймкодів"},
		{"music.srt", "1\n00:00:01,000 --> 00:00:03,000\n[Музика]\n\n2\n00:00:03,000 --> 00:00:04,000\n♪ ♪\n", "немає тексту"},
		{"binary.docx", "PK\x03\x04\x14\x00\x06\x00\x08\x00\x00\x00!\x00\xb5U\x8a\x97", "немає таймкодів"},
	} {
		_, err := ParseTranscript([]byte(tc.data), tc.name)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want …%s…", tc.name, err, tc.want)
		}
	}
	if _, err := ParseTranscript([]byte("Привіт"), "a.txt"); !errors.Is(err, ErrNoTimecodes) {
		t.Errorf("ErrNoTimecodes: %v", err)
	}
	if ext := TranscriptExts(); strings.Join(ext, " ") != ".srt .vtt .json .txt" {
		t.Errorf("exts %v", ext)
	}
	for name, want := range map[string]string{"video.uk.srt": "uk", "talk.en-US.vtt": "en", "my.voice.srt": "", "plain.srt": "", "Казка.ru.txt": "ru"} {
		if got := nameLanguage(name); got != want {
			t.Errorf("nameLanguage(%q) = %q, want %q", name, got, want)
		}
	}
}

// FuzzParseTranscript: no input panics, and every transcript that parses
// keeps the invariants (go test runs the seeds; -fuzz explores).
func FuzzParseTranscript(f *testing.F) {
	for _, seed := range []string{
		"1\n00:00:01,000 --> 00:00:03,000\n<i>Раз</i> два.\n\n2\n00:00:03,000 --> 00:00:04,000\nТри.\n",
		"WEBVTT\n\n00:00.000 --> 00:02.869 align:start\nhello<00:00:00.420><c> world</c>\n\n00:02.869 --> 00:02.879\nhello world\n",
		"[00:00] Раз.\n[00:03] Два.\n0:05\nТри\n",
		"0.5\t2.5\tРаз\n3\t5\tДва\n",
		`{"words":[{"text":"a","start":0.1,"end":0.5,"type":"word"},{"text":",","start":null}],"segments":[{"start":0,"end":1,"text":"a,"}]}`,
		`{"transcription":[{"offsets":{"from":0,"to":900},"text":" Hi.","tokens":[{"text":" Hi","offsets":{"from":0,"to":500}}]}]}`,
		`{"alignment":{"characters":["a"," ","b"],"character_start_times_seconds":[0,0.1,0.2],"character_end_times_seconds":[0.1,0.2,0.3]}}`,
		`[{"timestamp":[0,1],"text":"x"}]`,
		"{\"result\":[{\"word\":\"a\",\"start\":1,\"end\":2}]}\n{\"result\":[{\"word\":\"b\",\"start\":3,\"end\":4}]}",
	} {
		f.Add([]byte(seed), "x.srt")
	}
	f.Fuzz(func(t *testing.T, data []byte, name string) {
		tr, err := ParseTranscript(data, name)
		if err != nil {
			return
		}
		if len(tr.Words) == 0 || len(tr.Segments) == 0 {
			t.Fatalf("parsed without words or phrases: %+v", tr)
		}
		for i, x := range tr.Words {
			if !finite(x.Start) || !finite(x.End) || x.Start < 0 || x.End < x.Start || i > 0 && x.Start < tr.Words[i-1].Start {
				t.Fatalf("word %d %+v", i, x)
			}
		}
		for i, s := range tr.Segments {
			if !finite(s.Start) || !finite(s.End) || s.Start < 0 || s.End < s.Start {
				t.Fatalf("segment %d %+v", i, s)
			}
		}
		if !finite(tr.Duration) || tr.Duration < speechEnd(tr)-1e-9 {
			t.Fatalf("duration %v, speech %v", tr.Duration, speechEnd(tr))
		}
	})
}
