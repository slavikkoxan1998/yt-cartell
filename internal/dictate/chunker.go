package dictate

// The phrase chunker for services that take a whole recording at a time
// (OpenAI-compatible /audio/transcriptions, Gemini, whisper.cpp):
//
//   - an energy voice detector (audio.go) finds phrases: speech starts a
//     phrase (with 300 ms before it), a pause of 0.7 s ends it (200 ms of the
//     pause stay in), and only phrases with ≥ 0.24 s of speech are sent —
//     silence is never uploaded (it costs and makes Whisper invent text);
//   - a phrase longer than maxPhrase is cut at the quietest 20 ms of its last
//     1.5 s; the next one starts 0.4 s earlier (overlap) and the words the
//     two share are dropped from the second;
//   - while a phrase goes on, the phrase so far is re-read every interimStep
//     for the interim text (only when nothing else waits: finals first);
//   - the finals go out in order, each with the previous text as the
//     service's prompt (continuity, punctuation).

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// transcriber recognises one phrase.
type transcriber interface {
	// transcribe returns the text of 16 kHz mono PCM; lang "" = detect;
	// prompt is the text before it. detected is the language heard when
	// the service says ("" = unknown).
	transcribe(ctx context.Context, pcm []int16, lang, prompt string) (text, detected string, err error)
	// start prepares (local: launches the server) — may block a while.
	start(ctx context.Context) error
	close()
}

type chunkMode struct {
	maxPhrase   int // frames
	interimStep int // frames between interim reads (0 = none)
	finish      time.Duration
}

var (
	chunkCloud    = chunkMode{maxPhrase: 750, interimStep: 125, finish: finishChunked}
	chunkLocal    = chunkMode{maxPhrase: 600, interimStep: 100, finish: 30 * time.Second}
	chunkLocalCLI = chunkMode{maxPhrase: 600, finish: 40 * time.Second}
)

const (
	preRoll    = 15 // frames kept before speech
	endPause   = 35 // frames of pause that end a phrase
	keepPause  = 10 // frames of the pause that stay in the phrase
	minSpeech  = 12 // frames of speech a phrase needs
	cutWindow  = 75 // frames searched for a quiet cut
	overlapFrm = 20 // frames repeated after a forced cut
)

type chunkRec struct {
	t    transcriber
	mode chunkMode
	lang string // the settings' language ("" = detect)
}

func newChunkRec(t transcriber, m chunkMode, lang string) *chunkRec {
	return &chunkRec{t: t, mode: m, lang: lang}
}

func (r *chunkRec) finishTime() time.Duration { return r.mode.finish }

type chunkJob struct {
	pcm     []int16
	final   bool
	phrase  int     // phrase number
	speech  float64 // seconds of speech in it
	overlap bool    // starts with words of the previous phrase
}

func (r *chunkRec) run(ctx context.Context, audio <-chan []int16, out sink) error {
	defer r.t.close()
	startErr := make(chan error, 1)
	go func() { startErr <- r.t.start(ctx) }()

	jobs := make(chan chunkJob, 256)
	errc := make(chan error, 1)
	var finalsQueued atomic.Int32
	var phraseNo atomic.Int64
	var busy atomic.Bool
	lang := newLangLock()
	lang.set(r.lang)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := <-startErr; err != nil {
			errc <- err
			for range jobs { // drain so the reader never blocks
			}
			return
		}
		var prev string // the text so far (prompt, overlap)
		for j := range jobs {
			if !j.final && (finalsQueued.Load() > 0 || int64(j.phrase) != phraseNo.Load()) {
				busy.Store(false)
				continue // stale interim
			}
			text, det, err := r.t.transcribe(ctx, j.pcm, lang.get(), tail(prev, 200))
			if err != nil && j.final && retryable(err) && ctx.Err() == nil {
				time.Sleep(600 * time.Millisecond)
				text, det, err = r.t.transcribe(ctx, j.pcm, lang.get(), tail(prev, 200))
			}
			if err != nil {
				if ctx.Err() != nil {
					if j.final {
						finalsQueued.Add(-1)
					}
					continue
				}
				if j.final || KindOf(err) == KindAuth || KindOf(err) == KindQuota {
					select {
					case errc <- err:
					default:
					}
					for range jobs {
					}
					return
				}
				log.Printf("dictate: interim: %v", err)
				busy.Store(false)
				continue
			}
			text = cleanText(text, j.speech)
			if j.final {
				if j.overlap {
					text = dropOverlap(prev, text)
				}
				if det != "" && j.speech >= 1.5 {
					lang.lock(det)
				}
				if text != "" {
					out.final(text)
					prev = strings.TrimSpace(prev + " " + text)
				}
				if finalsQueued.Add(-1) == 0 {
					out.interim("")
				}
			} else {
				if finalsQueued.Load() == 0 && int64(j.phrase) == phraseNo.Load() {
					if j.overlap {
						text = dropOverlap(prev, text)
					}
					out.interim(text)
				}
				busy.Store(false)
			}
		}
	}()

	var v vad
	var ring [][]int16
	var phrase []int16
	var flags []bool     // speech per frame of phrase
	var levels []float64 // dB per frame of phrase
	in, overlapped := false, false
	speech, quiet, since := 0, 0, 0
	queue := func(j chunkJob) {
		if j.final {
			finalsQueued.Add(1)
		}
		jobs <- j
	}
	endPhrase := func(cut int, forced bool) {
		// cut: frames of the phrase that go into this final.
		sp := 0
		for _, f := range flags[:cut] {
			if f {
				sp++
			}
		}
		pcm := append([]int16(nil), phrase[:cut*FrameSize]...)
		no := int(phraseNo.Load())
		if sp >= minSpeech {
			queue(chunkJob{pcm: pcm, final: true, phrase: no, speech: float64(sp) * FrameMs / 1000, overlap: overlapped})
		}
		phraseNo.Add(1)
		if forced {
			from := max(0, cut-overlapFrm)
			phrase = append([]int16(nil), phrase[from*FrameSize:]...)
			flags = append([]bool(nil), flags[from:]...)
			levels = append([]float64(nil), levels[from:]...)
			speech = 0
			for _, f := range flags {
				if f {
					speech++
				}
			}
			overlapped, since = true, 0
			return
		}
		in, overlapped = false, false
		ring = ring[:0]
		phrase, flags, levels = nil, nil, nil
		speech, quiet, since = 0, 0, 0
	}
	for {
		var f []int16
		var ok bool
		select {
		case f, ok = <-audio:
		case err := <-errc:
			close(jobs)
			wg.Wait()
			return err
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return ctx.Err()
		}
		if !ok {
			break
		}
		db := frameDB(f)
		sp := v.push(db)
		if !in {
			ring = append(ring, f)
			if len(ring) > preRoll {
				ring = ring[1:]
			}
			if !sp {
				continue
			}
			in = true
			for _, rf := range ring {
				phrase = append(phrase, rf...)
				flags = append(flags, false)
				levels = append(levels, frameDB(rf))
			}
			flags[len(flags)-1] = true
			speech, quiet, since = 1, 0, 0
			continue
		}
		phrase = append(phrase, f...)
		flags = append(flags, sp)
		levels = append(levels, db)
		since++
		if sp {
			speech++
			quiet = 0
		} else {
			quiet++
		}
		n := len(flags)
		switch {
		case quiet >= endPause:
			endPhrase(n-(quiet-keepPause), false)
		case n >= r.mode.maxPhrase:
			// The quietest frame near the end is the least harmful cut.
			best, bestDB := n, 1e9
			for i := n - 1; i >= max(0, n-cutWindow); i-- {
				if levels[i] < bestDB {
					best, bestDB = i+1, levels[i]
				}
			}
			endPhrase(best, true)
		case r.mode.interimStep > 0 && since >= r.mode.interimStep && speech >= 25 && finalsQueued.Load() == 0 && !busy.Load():
			since = 0
			busy.Store(true)
			select {
			case jobs <- chunkJob{pcm: append([]int16(nil), phrase...), phrase: int(phraseNo.Load()), speech: float64(speech) * FrameMs / 1000, overlap: overlapped}:
			default:
				busy.Store(false)
			}
		}
	}
	// The end of the recording: the phrase in progress becomes final.
	if in && len(flags) > 0 {
		cut := len(flags)
		if quiet > keepPause {
			cut -= quiet - keepPause
		}
		endPhrase(cut, false)
	}
	close(jobs)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		<-done
		return nil
	}
	select {
	case err := <-errc:
		return err
	default:
	}
	return nil
}

// langLock remembers the language of the first long phrase when the
// settings say "detect": short phrases are easily misheard as a neighbour
// language (Whisper confuses Ukrainian and Russian on 2-second clips).
type langLock struct {
	mu   sync.Mutex
	v    string
	auto bool
}

func newLangLock() *langLock { return &langLock{} }

func (l *langLock) set(v string) {
	l.mu.Lock()
	l.v, l.auto = v, v == ""
	l.mu.Unlock()
}

func (l *langLock) lock(v string) {
	l.mu.Lock()
	if l.auto && l.v == "" {
		l.v = v
	}
	l.mu.Unlock()
}

func (l *langLock) get() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.v
}

// retryable: a dropped connection or a 5xx deserves one more try.
func retryable(err error) bool {
	k := KindOf(err)
	return k == KindNetwork || k == KindService
}

// hallucinations Whisper-like models write on near-silence (lowercase, no
// punctuation).
var hallucinations = []string{
	"дякую за перегляд", "дякую за увагу", "субтитри", "редактор субтитрів", "продовження буде",
	"спасибо за просмотр", "продолжение следует", "субтитры", "редактор субтитров",
	"thank you for watching", "thanks for watching", "thank you", "subtitles by",
	"music", "музика", "blank_audio",
}

// cleanText tidies a phrase: no [annotations], no lone punctuation, no
// stock phrases a model invents on short noise.
func cleanText(s string, speech float64) string {
	s = dropBracketed(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	if !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
		return ""
	}
	if speech < 2.5 {
		n := normWords(s)
		for _, h := range hallucinations {
			if n == h || (strings.HasPrefix(n, h) && len(n) < len(h)+12) {
				return ""
			}
		}
	}
	return s
}

// dropBracketed removes [..], (..) and *..* annotations and ♪ notes.
func dropBracketed(s string) string {
	var b strings.Builder
	depth := 0
	star := false
	for _, r := range s {
		switch {
		case r == '[' || r == '(':
			depth++
		case (r == ']' || r == ')') && depth > 0:
			depth--
		case r == '*':
			star = !star
		case r == '♪' || r == '♫':
		case depth == 0 && !star:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normWords lowercases and keeps letters, digits and single spaces.
func normWords(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

// dropOverlap removes the words at the start of next that repeat the end of
// prev (the overlap of a forced cut), up to 8 words.
func dropOverlap(prev, next string) string {
	pw := strings.Fields(normWords(prev))
	nw := strings.Fields(next)
	nn := strings.Fields(normWords(next))
	if len(nn) != len(nw) { // punctuation-only tokens: compare token by token
		nn = make([]string, len(nw))
		for i, w := range nw {
			nn[i] = normWords(w)
		}
	}
	for k := min(8, len(pw), len(nn)); k > 0; k-- {
		match := true
		for i := 0; i < k; i++ {
			if pw[len(pw)-k+i] != nn[i] {
				match = false
				break
			}
		}
		if match {
			return strings.Join(nw[k:], " ")
		}
	}
	return next
}

// tail is the end of s, at most n runes, cut at a word.
func tail(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	t := string(r[len(r)-n:])
	if i := strings.IndexByte(t, ' '); i >= 0 && i < len(t)-1 {
		t = t[i+1:]
	}
	return t
}
