// Package stt is the speech-to-text behind "Своя озвучка" (studio ModeVoice):
// the user's own voice-over is transcribed with word timings so the studio can
// split it into scenes timed to the speech. Settings come from cfg.STT and are
// read on every call:
//
//   - "elevenlabs" → POST https://api.elevenlabs.io/v1/speech-to-text (Scribe),
//     multipart, header xi-api-key. Docs:
//     https://elevenlabs.io/docs/api-reference/speech-to-text/convert
//     (model_id, file, language_code, tag_audio_events, timestamps_granularity;
//     reply: language_code, text, words[{text,start,end,type}], audio_duration_secs).
//     scribe_v1 was removed on 2026-07-09
//     (https://elevenlabs.io/docs/changelog/2026/6/8): the default is scribe_v2
//     and a configured scribe_v1 is upgraded to it.
//   - "openai" (or any Whisper-compatible server) → POST {base}/audio/transcriptions,
//     multipart, Bearer. Docs:
//     https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
//     and https://developers.openai.com/api/docs/guides/speech-to-text
//     (model, file ≤ 25 MB, language, response_format=verbose_json,
//     timestamp_granularities[]=word|segment — whisper-1 only; reply: language,
//     duration, text, words[{word,start,end}], segments[{start,end,text}]).
//     Whisper words carry no punctuation; the studio aligns them to the text.
//   - "assemblyai" → AssemblyAI: upload, a transcript job, polling (assemblyai.go,
//     https://www.assemblyai.com/docs); words in ms. Its own key (AssemblyKey);
//     AssemblyEU uses https://api.eu.assemblyai.com (the data stays in the EU).
//   - "local" → whisper.cpp's whisper-cli on this computer (no key, no upload):
//     local.go (finding the program and the model), localrun.go (running it:
//     -oj -ml 1 -sow word timings), localinstall.go (the one-time download the
//     user starts in Settings).
//
// Empty keys fall back to the ElevenLabs key stored for the voice-over (TTS keeps
// each provider's keys, so it works whichever voice-over is active) or the Text
// key (OpenAI); an empty OpenAI URL falls back to the Text URL.
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"ytcartell/internal/config"
	"ytcartell/internal/httpdiag"
	"ytcartell/internal/tts"
)

// Word is one spoken word with its time in the recording (seconds). Its text
// is as the provider wrote it (ElevenLabs includes punctuation, Whisper not).
type Word struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Segment is a phrase or sentence of the recording (seconds).
type Segment struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Transcript is the recognised speech of one recording.
type Transcript struct {
	Text string `json:"text"`
	// Language is the detected language as a catalogue code ("uk", "en", ISO
	// 639-3 for languages without a two-letter code), "" when unknown.
	Language string    `json:"language,omitempty"`
	Duration float64   `json:"duration"` // seconds of audio
	Words    []Word    `json:"words"`
	Segments []Segment `json:"segments"`
}

// Default models (Settings → Розпізнавання мови, model "").
//
// whisper-1 is the OpenAI model that returns word timings (verbose_json);
// OpenAI announced its shutdown for 2027-02-26 — then a Whisper-compatible
// server (or ElevenLabs Scribe, or whisper.cpp) has to take over.
const (
	DefaultElevenModel = "scribe_v2"
	DefaultOpenAIModel = "whisper-1"
)

// plainJSONOnly reports OpenAI models whose transcriptions come back as
// plain json/text only (gpt-4o-transcribe, gpt-4o-mini-transcribe and their
// snapshots, gpt-transcribe, gpt-live-transcribe, the diarize model): no
// verbose_json, so no word timings — fine for dictation, useless for cutting
// an own voice-over into scenes.
func plainJSONOnly(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gpt-4o-transcribe") || strings.HasPrefix(m, "gpt-4o-mini-transcribe") ||
		strings.HasPrefix(m, "gpt-transcribe") || strings.HasPrefix(m, "gpt-live-transcribe")
}

// errNoTimings explains why such a model cannot transcribe a voice-over.
func errNoTimings(model string) error {
	return fmt.Errorf("Модель «%s» не повертає таймкоди слів, а «Своїй озвучці» вони потрібні — вкажи whisper-1 (або інший Whisper-сумісний сервер) чи вибери ElevenLabs Scribe у Налаштуваннях → Розпізнавання мови", model)
}

// DefaultModel is the model used for provider when Settings leave it empty.
func DefaultModel(provider string) string {
	if strings.EqualFold(strings.TrimSpace(provider), "openai") {
		return DefaultOpenAIModel
	}
	return DefaultElevenModel
}

// Endpoints and tunables (package vars so tests can point them at mocks).
var (
	elevenBase     = "https://api.elevenlabs.io"
	requestTimeout = 20 * time.Minute // one upload + transcription
	testTimeout    = 20 * time.Second
	retries        = 2 // extra attempts on 429 / 5xx / dropped connections
	retryBase      = 2 * time.Second
)

const (
	maxReply = 64 << 20
	// OpenAILimit is the documented upload limit of /audio/transcriptions.
	OpenAILimit = 25 << 20
)

// Client transcribes with the provider chosen in Settings.
type Client struct {
	get  func() config.Config
	http *http.Client
}

// New creates a client reading its settings from get.
func New(get func() config.Config) *Client {
	return &Client{get: get, http: &http.Client{}}
}

type settings struct {
	provider, base, key, model string
}

func (c *Client) settings() settings {
	cfg := c.get()
	s := cfg.STT
	out := settings{
		provider: strings.ToLower(strings.TrimSpace(s.Provider)),
		key:      strings.TrimSpace(s.APIKey),
		model:    strings.TrimSpace(s.Model),
	}
	if out.provider == ProviderLocal { // no endpoint, no key: see local.go
		out.key, out.model = "", ""
		return out
	}
	if out.provider == ProviderCustom { // custom.go: the spec decides
		out.base = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
		return out
	}
	if out.provider == ProviderAssembly { // assemblyai.go: its own key and region
		out.key, out.base = strings.TrimSpace(s.AssemblyKey), AssemblyBase
		if s.AssemblyEU {
			out.base = AssemblyBaseEU
		}
		return out
	}
	if out.provider == "openai" {
		out.base = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
		reuse := cfg.Text.OpenAICompatible() // an Anthropic or Ollama text endpoint has no Whisper
		if out.base == "" && reuse {
			out.base = strings.TrimRight(strings.TrimSpace(cfg.Text.BaseURL), "/")
		}
		if out.key == "" && reuse {
			out.key = strings.TrimSpace(cfg.Text.APIKey)
		}
		if out.model == "" {
			out.model = DefaultOpenAIModel
		}
		return out
	}
	out.provider, out.base = "elevenlabs", elevenBase
	// The voice-over's stored ElevenLabs key(s) — kept per provider, so they
	// are there even while another TTS provider is active; another
	// provider's key is never sent to ElevenLabs.
	if out.key == "" {
		if keys := cfg.TTS.Of("elevenlabs").AllKeys(); len(keys) > 0 {
			out.key = keys[0]
		}
	}
	out.model = elevenModel(out.model)
	return out
}

// Model reports the provider ("elevenlabs" | "openai" | "local") and the
// model the next transcription will use (local: the catalogue id such as
// "small", or the model's file name).
func (c *Client) Model() (provider, model string) {
	s := c.settings()
	if s.provider == ProviderLocal {
		if _, m, _, _ := LocalStatus(c.get()); m != "" {
			return s.provider, modelLabel(m)
		}
	}
	return s.provider, s.model
}

// elevenModel maps "" and the removed Scribe v1 ids to the current batch model.
func elevenModel(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "", "scribe_v1", "scribe_v1_experimental":
		return DefaultElevenModel
	}
	return strings.TrimSpace(m)
}

func (s settings) who() string {
	if s.provider == ProviderLocal {
		return "whisper.cpp"
	}
	if s.provider == "openai" {
		if u, err := url.Parse(s.base); err == nil && u.Host != "" {
			return u.Host
		}
		return "OpenAI"
	}
	if s.provider == ProviderAssembly {
		return "AssemblyAI"
	}
	if s.provider == ProviderCustom {
		if u, err := url.Parse(s.base); err == nil && u.Host != "" {
			return u.Host
		}
		return "Своє API"
	}
	return "ElevenLabs"
}

func (s settings) noKey() error {
	if s.provider == ProviderAssembly {
		return errors.New("Не задано ключ AssemblyAI (Налаштування → Розпізнавання мови)")
	}
	if s.provider == "openai" {
		return errors.New("Не задано ключ для розпізнавання мови (Налаштування → Розпізнавання мови або ключ текстової моделі)")
	}
	return errors.New("Не задано ключ ElevenLabs для розпізнавання мови (Налаштування → Розпізнавання мови)")
}

// ---- transcription -----------------------------------------------------------------

// TranscribeProgress is Transcribe with progress reports: pct 0–100 and a
// short Ukrainian message. Only the local provider (whisper.cpp) reports as it
// goes; the online services say nothing until their reply arrives. progress
// may be nil and is called at most ~8 times a second.
func (c *Client) TranscribeProgress(ctx context.Context, path, lang string, progress func(pct int, msg string)) (Transcript, error) {
	switch s := c.settings(); {
	case s.provider == ProviderLocal:
		return c.transcribeLocal(ctx, path, lang, progress)
	case s.provider == ProviderAssembly && s.key != "":
		return c.transcribeAssembly(ctx, s, path, lang, progress)
	case s.provider == ProviderCustom:
		return c.transcribeCustom(ctx, path, lang)
	}
	return c.Transcribe(ctx, path, lang)
}

// Transcribe uploads the recording at path (or, with the local provider,
// runs whisper.cpp on it) and returns its transcript with word timings. lang
// is the spoken language as a catalogue code ("" = detect). Cancelling ctx
// aborts the upload or stops whisper.cpp at once.
func (c *Client) Transcribe(ctx context.Context, path, lang string) (Transcript, error) {
	s := c.settings()
	if s.provider == ProviderLocal {
		return c.transcribeLocal(ctx, path, lang, nil)
	}
	if s.provider == ProviderCustom {
		return c.transcribeCustom(ctx, path, lang)
	}
	if s.key == "" {
		return Transcript{}, s.noKey()
	}
	if s.provider == ProviderAssembly {
		return c.transcribeAssembly(ctx, s, path, lang, nil)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Transcript{}, fmt.Errorf("Файл запису не знайдено: %s", filepath.Base(path))
	}
	if info.Size() == 0 {
		return Transcript{}, errors.New("Файл запису порожній")
	}
	if s.provider == "openai" && plainJSONOnly(s.model) {
		return Transcript{}, errNoTimings(s.model)
	}
	if s.provider == "openai" && info.Size() > OpenAILimit {
		return Transcript{}, errTooBig(s)
	}
	var res reply
	for attempt := 0; ; attempt++ {
		res, err = c.upload(ctx, s, path, info.Size(), lang)
		if err == nil && res.ok() {
			break
		}
		if ctx.Err() != nil {
			return Transcript{}, ctx.Err()
		}
		// A dropped connection, 429 or 5xx is retried; a timed-out upload is not
		// (it already took requestTimeout), nor an exhausted quota.
		again := err != nil && !isTimeout(err) || err == nil && (res.status == http.StatusTooManyRequests || res.status >= 500) && !quota(res)
		if again && attempt < retries {
			if serr := sleep(ctx, retryBase*time.Duration(attempt+1)); serr != nil {
				return Transcript{}, serr
			}
			continue
		}
		if err != nil {
			return Transcript{}, netErr(ctx, s, err)
		}
		return Transcript{}, httpErr(s, res)
	}
	var tr Transcript
	if s.provider == "openai" {
		tr, err = parseOpenAI(res.body)
	} else {
		tr, err = parseEleven(res.body)
	}
	if err != nil {
		return Transcript{}, err
	}
	tr.finish()
	return tr, nil
}

// upload sends one multipart request; the body streams the file with a known
// length (the fields come first, then the file, then the closing boundary).
func (c *Client) upload(ctx context.Context, s settings, path string, size int64, lang string) (reply, error) {
	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	var endpoint string
	hdr := http.Header{}
	lang = strings.ToLower(strings.TrimSpace(lang))
	if s.provider == "openai" {
		endpoint = s.base + "/audio/transcriptions"
		hdr.Set("Authorization", "Bearer "+s.key)
		_ = mw.WriteField("model", s.model)
		_ = mw.WriteField("response_format", "verbose_json")
		_ = mw.WriteField("timestamp_granularities[]", "word")
		_ = mw.WriteField("timestamp_granularities[]", "segment")
		if len(lang) == 2 { // the API takes ISO 639-1 only
			_ = mw.WriteField("language", lang)
		}
	} else {
		endpoint = s.base + "/v1/speech-to-text"
		hdr.Set("xi-api-key", s.key)
		_ = mw.WriteField("model_id", s.model)
		_ = mw.WriteField("timestamps_granularity", "word")
		_ = mw.WriteField("tag_audio_events", "false")
		if lang != "" { // ISO 639-1 or 639-3
			_ = mw.WriteField("language_code", lang)
		}
	}
	ext := strings.ToLower(filepath.Ext(path))
	ctype := mime.TypeByExtension(ext)
	if ctype == "" || !(strings.HasPrefix(ctype, "audio/") || strings.HasPrefix(ctype, "video/")) {
		ctype = audioTypes[ext]
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	part := textproto.MIMEHeader{}
	// An ASCII name keeps every server happy; the extension tells the format.
	part.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="audio%s"`, ext))
	part.Set("Content-Type", ctype)
	if _, err := mw.CreatePart(part); err != nil {
		return reply{}, err
	}
	tail := "\r\n--" + mw.Boundary() + "--\r\n"

	f, err := os.Open(path)
	if err != nil {
		return reply{}, fmt.Errorf("не вдалося прочитати файл запису: %w", err)
	}
	defer f.Close()
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	body := io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(f, size), strings.NewReader(tail))
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, endpoint, body)
	if err != nil {
		return reply{}, err
	}
	req.ContentLength = int64(head.Len()) + size + int64(len(tail))
	req.Header = hdr
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	return c.do(req)
}

// audioTypes fills in MIME types the OS registry may not know.
var audioTypes = map[string]string{
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4", ".aac": "audio/aac", ".ogg": "audio/ogg",
	".oga": "audio/ogg", ".opus": "audio/ogg", ".flac": "audio/flac", ".wma": "audio/x-ms-wma", ".webm": "audio/webm",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".mkv": "video/x-matroska",
}

type reply struct {
	status int
	ctype  string
	body   []byte
}

func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

func (c *Client) do(req *http.Request) (reply, error) {
	res, err := c.http.Do(req)
	if err != nil {
		httpdiag.Record(req.Context(), req.Method, req.URL.String(), 0, nil, err)
		return reply{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxReply+1))
	if err != nil {
		httpdiag.Record(req.Context(), req.Method, req.URL.String(), 0, nil, err)
		return reply{}, err
	}
	httpdiag.Record(req.Context(), req.Method, req.URL.String(), res.StatusCode, b, nil)
	if len(b) > maxReply {
		return reply{}, fmt.Errorf("відповідь більша за %d МБ", maxReply>>20)
	}
	return reply{status: res.StatusCode, ctype: res.Header.Get("Content-Type"), body: b}, nil
}

// ---- replies ----------------------------------------------------------------------------

// ElevenLabs Scribe reply (SpeechToTextChunkResponseModel).
type elevenReply struct {
	LanguageCode  string  `json:"language_code"`
	Text          string  `json:"text"`
	AudioDuration float64 `json:"audio_duration_secs"`
	Words         []struct {
		Text  string   `json:"text"`
		Start *float64 `json:"start"`
		End   *float64 `json:"end"`
		Type  string   `json:"type"` // word | spacing | audio_event
	} `json:"words"`
}

func parseEleven(b []byte) (Transcript, error) {
	var r elevenReply
	if err := json.Unmarshal(b, &r); err != nil {
		return Transcript{}, fmt.Errorf("ElevenLabs: незрозуміла відповідь розпізнавання: %s", clip(string(b), 200))
	}
	tr := Transcript{Text: r.Text, Language: r.LanguageCode, Duration: r.AudioDuration}
	for _, w := range r.Words {
		if w.Type != "" && w.Type != "word" {
			continue // spacing and (untagged) audio events
		}
		tr.Words = append(tr.Words, Word{Text: w.Text, Start: timeOr(w.Start), End: timeOr(w.End)})
	}
	return tr, nil
}

type openaiWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Whisper verbose_json (some compatible servers nest words in segments).
type openaiReply struct {
	Language string       `json:"language"`
	Duration float64      `json:"duration"`
	Text     string       `json:"text"`
	Words    []openaiWord `json:"words"`
	Segments []struct {
		Start float64      `json:"start"`
		End   float64      `json:"end"`
		Text  string       `json:"text"`
		Words []openaiWord `json:"words"`
	} `json:"segments"`
}

func parseOpenAI(b []byte) (Transcript, error) {
	var r openaiReply
	if err := json.Unmarshal(b, &r); err != nil {
		return Transcript{}, fmt.Errorf("Сервіс розпізнавання повернув незрозумілу відповідь (потрібна модель з verbose_json, напр. whisper-1): %s", clip(string(b), 200))
	}
	tr := Transcript{Text: r.Text, Language: r.Language, Duration: r.Duration}
	for _, w := range r.Words {
		tr.Words = append(tr.Words, Word{Text: w.Word, Start: w.Start, End: w.End})
	}
	nested := len(tr.Words) == 0
	for _, s := range r.Segments {
		tr.Segments = append(tr.Segments, Segment{Text: s.Text, Start: s.Start, End: s.End})
		if nested {
			for _, w := range s.Words {
				tr.Words = append(tr.Words, Word{Text: w.Word, Start: w.Start, End: w.End})
			}
		}
	}
	return tr, nil
}

func timeOr(v *float64) float64 {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return math.NaN() // filled in by finish
	}
	return *v
}

// finish cleans a provider transcript: trimmed texts, times fixed and
// ordered, words from segments (or segments from words) when only one kind
// came back, text, duration and a catalogue language code.
func (t *Transcript) finish() {
	t.Text = strings.TrimSpace(t.Text)
	words := t.Words[:0]
	for _, w := range t.Words {
		if w.Text = strings.TrimSpace(w.Text); w.Text != "" {
			words = append(words, w)
		}
	}
	fillTimes(words)
	sort.SliceStable(words, func(i, j int) bool { return words[i].Start < words[j].Start })
	t.Words = words

	segs := t.Segments[:0]
	for _, s := range t.Segments {
		s.Text = strings.TrimSpace(s.Text)
		if s.Text == "" || !finite(s.Start) || !finite(s.End) {
			continue
		}
		s.Start = math.Max(0, s.Start)
		s.End = math.Max(s.Start, s.End)
		segs = append(segs, s)
	}
	sort.SliceStable(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	t.Segments = segs

	switch {
	case len(t.Words) == 0 && len(t.Segments) > 0:
		t.Words = wordsFromSegments(t.Segments)
	case len(t.Segments) == 0 && len(t.Words) > 0:
		t.Segments = segmentsFromWords(t.Words)
	}
	if t.Words == nil {
		t.Words = []Word{}
	}
	if t.Segments == nil {
		t.Segments = []Segment{}
	}
	if t.Text == "" {
		parts := make([]string, len(t.Segments))
		for i, s := range t.Segments {
			parts[i] = s.Text
		}
		t.Text = joinTexts(parts)
	}
	end := 0.0
	if n := len(t.Words); n > 0 {
		end = t.Words[n-1].End
	}
	if n := len(t.Segments); n > 0 {
		end = math.Max(end, t.Segments[n-1].End)
	}
	if !finite(t.Duration) || t.Duration < end {
		t.Duration = end
	}
	raw := strings.TrimSpace(t.Language)
	if t.Language = tts.NormalizeLanguage(raw); t.Language == "" {
		t.Language = strings.ToLower(raw)
	}
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// fillTimes gives words without times (NaN) the end of the previous word,
// clamps negatives and keeps End ≥ Start.
func fillTimes(words []Word) {
	last := 0.0
	for i := range words {
		w := &words[i]
		if !finite(w.Start) {
			w.Start = last
		}
		if !finite(w.End) {
			w.End = w.Start
		}
		w.Start = math.Max(0, w.Start)
		w.End = math.Max(w.Start, w.End)
		last = w.End
	}
}

// wordsFromSegments spreads each segment's time over its words by length
// (servers that return phrase timings only).
func wordsFromSegments(segs []Segment) []Word {
	var out []Word
	for _, s := range segs {
		toks := strings.Fields(s.Text)
		total := 0
		for _, t := range toks {
			total += len([]rune(t))
		}
		if total == 0 {
			continue
		}
		at, span := s.Start, s.End-s.Start
		for _, t := range toks {
			d := span * float64(len([]rune(t))) / float64(total)
			out = append(out, Word{Text: t, Start: at, End: at + d})
			at += d
		}
	}
	return out
}

// segmentsFromWords groups words into sentences: a segment ends after a word
// with a sentence mark, before a pause of a second or more, or at 40 words.
func segmentsFromWords(words []Word) []Segment {
	var out []Segment
	var cur []string
	start := 0.0
	for i, w := range words {
		if len(cur) == 0 {
			start = w.Start
		}
		cur = append(cur, w.Text)
		last := i == len(words)-1
		pause := !last && words[i+1].Start-w.End >= 1.0
		if last || pause || sentenceEnd(w.Text) || len(cur) >= 40 {
			out = append(out, Segment{Text: joinTexts(cur), Start: start, End: w.End})
			cur = nil
		}
	}
	return out
}

func sentenceEnd(word string) bool {
	w := strings.TrimRight(word, `"'»”’)]`)
	return strings.HasSuffix(w, ".") || strings.HasSuffix(w, "!") || strings.HasSuffix(w, "?") ||
		strings.HasSuffix(w, "…") || strings.HasSuffix(w, "。") || strings.HasSuffix(w, "！") || strings.HasSuffix(w, "？")
}

// joinTexts joins words or phrases with spaces, except between two
// characters of scripts written without spaces (Chinese, Japanese, Thai…).
func joinTexts(parts []string) string {
	var b strings.Builder
	prev := rune(0)
	for _, p := range parts {
		if p == "" {
			continue
		}
		first := []rune(p)[0]
		if b.Len() > 0 && !(Unspaced(prev) && Unspaced(first)) {
			b.WriteByte(' ')
		}
		b.WriteString(p)
		r := []rune(p)
		prev = r[len(r)-1]
	}
	return b.String()
}

// Unspaced reports a character of a script written without spaces between
// words (Han, kana, Thai, Lao, Myanmar, Khmer) or CJK punctuation.
func Unspaced(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Myanmar, unicode.Khmer) ||
		r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF
}

// ---- service check ------------------------------------------------------------------------

// Test checks the configured provider and key without transcribing anything
// (ElevenLabs: GET /v1/user; OpenAI-compatible: GET {base}/models; local:
// `whisper-cli --help` / `--version` and the model file).
func (c *Client) Test(ctx context.Context) (bool, string) {
	s := c.settings()
	if s.provider == ProviderLocal {
		return c.testLocal(ctx)
	}
	if s.provider == ProviderCustom {
		return c.testCustom(ctx)
	}
	if s.key == "" {
		return false, s.noKey().Error()
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	ctx, tr := httpdiag.WithTrace(ctx)
	var ok bool
	var msg string
	switch s.provider {
	case "openai":
		ok, msg = c.testOpenAI(ctx, s)
	case ProviderAssembly:
		ok, msg = c.testAssembly(ctx, s)
	default:
		ok, msg = c.testEleven(ctx, s)
	}
	if !ok {
		msg = tr.Append(msg, s.key) // the exact request under the message
	}
	return ok, msg
}

func (c *Client) fetch(ctx context.Context, u string, hdr map[string]string) (reply, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return reply{}, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.do(req)
}

func (c *Client) testEleven(ctx context.Context, s settings) (bool, string) {
	res, err := c.fetch(ctx, s.base+"/v1/user", map[string]string{"xi-api-key": s.key})
	if err != nil {
		return false, netErr(ctx, s, err).Error()
	}
	if res.ok() {
		var u struct {
			Subscription struct {
				Tier string `json:"tier"`
			} `json:"subscription"`
		}
		_ = json.Unmarshal(res.body, &u)
		msg := "ElevenLabs Scribe (" + s.model + "): ключ працює"
		if t := strings.TrimSpace(u.Subscription.Tier); t != "" {
			msg += " · тариф " + t
		}
		return true, msg
	}
	if strings.Contains(string(res.body), "missing_permissions") {
		// A restricted key: valid, but not allowed to read the account.
		return true, "ElevenLabs: ключ дійсний (без доступу до даних акаунта) — дозвіл на розпізнавання мови перевіриться під час першого запуску"
	}
	return false, httpErr(s, res).Error()
}

func (c *Client) testOpenAI(ctx context.Context, s settings) (bool, string) {
	if plainJSONOnly(s.model) {
		return false, errNoTimings(s.model).Error()
	}
	res, err := c.fetch(ctx, s.base+"/models", map[string]string{"Authorization": "Bearer " + s.key})
	if err != nil {
		return false, netErr(ctx, s, err).Error()
	}
	if res.status == http.StatusNotFound {
		return false, fmt.Sprintf("Сервер %s не відповідає на /models — перевір адресу розпізнавання мови в Налаштуваннях", s.who())
	}
	if !res.ok() {
		return false, httpErr(s, res).Error()
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(res.body, &list) != nil || len(list.Data) == 0 {
		return true, fmt.Sprintf("%s відповідає (список моделей недоступний) · модель %s", s.who(), s.model)
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if m.ID == s.model {
			return true, fmt.Sprintf("%s: ключ працює · модель %s доступна", s.who(), s.model)
		}
		ids = append(ids, m.ID)
	}
	if len(ids) > 6 {
		ids = append(ids[:6], "…")
	}
	return false, fmt.Sprintf("%s відповідає, але моделі «%s» немає (є: %s) — вкажи модель розпізнавання, напр. whisper-1", s.who(), s.model, strings.Join(ids, ", "))
}

// ---- errors ---------------------------------------------------------------------------------

func errTooBig(s settings) error {
	if s.provider == "openai" {
		return fmt.Errorf("Запис завеликий для %s (приймає до 25 МБ) — скороти запис або вибери ElevenLabs у Налаштуваннях", s.who())
	}
	return errors.New("Запис завеликий для сервісу розпізнавання — скороти його")
}

// quota: the account ran out of credits (retrying does not help).
func quota(r reply) bool {
	b := strings.ToLower(string(r.body))
	return strings.Contains(b, "quota_exceeded") || strings.Contains(b, "insufficient_quota")
}

// httpErr turns a non-2xx reply into a plain Ukrainian message.
func httpErr(s settings, r reply) error {
	detail := errDetail(r.body)
	body := strings.ToLower(string(r.body))
	switch {
	case strings.Contains(body, "missing_permissions"):
		return errors.New("Ключ ElevenLabs не має дозволу на розпізнавання мови (Speech to Text) — увімкни його в налаштуваннях ключа на elevenlabs.io")
	case r.status == http.StatusUnauthorized:
		return fmt.Errorf("Ключ не підходить — %s відхилив його (401). Перевір ключ розпізнавання мови в Налаштуваннях", s.who())
	case quota(r) || r.status == http.StatusPaymentRequired:
		return fmt.Errorf("%s: закінчились кредити на тарифі — поповни баланс або спробуй пізніше", s.who())
	case r.status == http.StatusRequestEntityTooLarge:
		return errTooBig(s)
	case r.status == http.StatusNotFound && s.provider == "openai":
		return fmt.Errorf("Сервер %s не вміє розпізнавати мову (немає /audio/transcriptions) — вкажи інший URL у Налаштуваннях або вибери ElevenLabs", s.who())
	case (r.status == http.StatusBadRequest || r.status == http.StatusUnprocessableEntity) && tooLong(body):
		return errors.New("Запис задовгий для сервісу розпізнавання — скороти його")
	case r.status == http.StatusTooManyRequests:
		return fmt.Errorf("%s перевантажений (429) — спробуй за хвилину", s.who())
	case r.status >= 500:
		return fmt.Errorf("%s тимчасово недоступний (%d) — спробуй ще раз", s.who(), r.status)
	}
	if detail == "" {
		detail = clip(string(r.body), 200)
	}
	return fmt.Errorf("%s відхилив запит на розпізнавання (%d): %s", s.who(), r.status, detail)
}

func tooLong(body string) bool {
	for _, k := range []string{"too long", "duration", "too large", "maximum"} {
		if strings.Contains(body, k) {
			return true
		}
	}
	return false
}

// errDetail digs the message out of {"detail":{"message"}}, {"detail":[{"msg"}]},
// {"detail":"…"} (ElevenLabs) or {"error":{"message"}} (OpenAI).
func errDetail(b []byte) string {
	var v struct {
		Detail json.RawMessage `json:"detail"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	for _, raw := range []json.RawMessage{v.Detail, v.Error} {
		if len(raw) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return clip(s, 240)
		}
		var o struct {
			Message string `json:"message"`
			Msg     string `json:"msg"`
		}
		if json.Unmarshal(raw, &o) == nil && (o.Message != "" || o.Msg != "") {
			return clip(o.Message+o.Msg, 240)
		}
		var list []struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal(raw, &list) == nil && len(list) > 0 && list[0].Msg != "" {
			return clip(list[0].Msg, 240)
		}
	}
	return ""
}

// netErr turns a transport failure into a friendly message (cancellation passes through).
func netErr(ctx context.Context, s settings, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s не відповів вчасно — спробуй ще раз", s.who())
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
		if ue.Timeout() {
			return fmt.Errorf("%s не відповів вчасно — спробуй ще раз", s.who())
		}
	}
	return fmt.Errorf("Немає з'єднання з %s — перевір інтернет або адресу в Налаштуваннях (%s)", s.who(), clip(err.Error(), 160))
}

func isTimeout(err error) bool {
	var ue *url.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ue) && ue.Timeout()
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
