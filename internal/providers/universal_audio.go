package providers

// The universal adapter for audio APIs: voice-over (TTS) and speech
// recognition (STT) described by the same JSON spec as images and video
// (universal.go), with their own placeholders:
//
//	TTS  {{text}} the line to speak · {{voice}} the voice id · {{model}}
//	     {{language}} ISO 639-1 ("uk") · {{language_name}} ("Ukrainian")
//	     {{speed}} 1 = normal · {{format}} "mp3"
//	     result: the reply bytes (audio/*), a URL, a data: URI or base64 —
//	     synchronous or an async job, exactly like an image
//	STT  {{audio}} the recording as a data: URI (a file part with
//	     "encoding": "multipart") · {{audio_b64}} raw base64 · {{audio_name}}
//	     {{audio_mime}} · {{language}} · {{model}}
//	     result: the transcript — the reply itself (JSON, SRT, VTT, text) or
//	     the value at "result" (an object/array is passed on as JSON, a URL is
//	     downloaded); package stt reads it (ParseTranscript knows the
//	     ElevenLabs, Whisper, Deepgram, AssemblyAI … shapes)
//
// The key, auth, polling, cancel, test and error rules are the image/video
// ones (the key only on the API's origin, one Idempotency-Key per job …).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"ytcartell/internal/gen"
	"ytcartell/internal/httpdiag"
)

// SpecKind is what a spec's API makes.
type SpecKind string

// Kinds of the universal adapter ("" in a Spec = image or video).
const (
	KindImage SpecKind = "image"
	KindVideo SpecKind = "video"
	KindTTS   SpecKind = "tts"
	KindSTT   SpecKind = "stt"
)

// Placeholders of the audio requests.
var (
	ttsRequestVars = []string{"text", "voice", "model", "language", "language_name", "speed", "format"}
	sttRequestVars = []string{"audio", "audio_b64", "audio_name", "audio_mime", "language", "model"}
)

// Size limits of the audio APIs.
const (
	maxAudioBytes      = 256 << 20 // a synthesised line (or a long voice-over)
	maxTranscriptBytes = 64 << 20
)

func (s *Spec) requestVars() []string {
	switch s.kind {
	case KindTTS:
		return ttsRequestVars
	case KindSTT:
		return sttRequestVars
	}
	return specRequestVars
}

// missingInput is the problem of a submit without the request's main input
// ("" = fine).
func (s *Spec) missingInput() string {
	switch s.kind {
	case KindTTS:
		if !s.uses["text"] {
			return "submit.body: немає {{text}} — куди студії передати текст для озвучки?"
		}
	case KindSTT:
		if !s.uses["audio"] && !s.uses["audio_b64"] {
			return `submit.body: немає {{audio}} (або {{audio_b64}}) — куди студії передати запис? Для завантаження файлу постав "encoding": "multipart"`
		}
	default:
		if !s.uses["prompt"] {
			return "submit.body: немає {{prompt}} — куди студії передати опис кадру?"
		}
	}
	return ""
}

// SpecPlaceholdersFor lists the request placeholders of a kind.
func SpecPlaceholdersFor(kind SpecKind) []string {
	vars := specRequestVars
	switch kind {
	case KindTTS:
		vars = ttsRequestVars
	case KindSTT:
		vars = sttRequestVars
	}
	out := make([]string, len(vars))
	for i, v := range vars {
		out[i] = "{{" + v + "}}"
	}
	return out
}

// ParseSpecFor reads and checks a spec of a kind.
func ParseSpecFor(text string, kind SpecKind) (*Spec, error) {
	if kind == KindImage || kind == KindVideo {
		kind = ""
	}
	return parseSpecKind(text, kind)
}

// CheckSpecFor is CheckSpec for any kind: a one-line summary of how the API
// works plus warnings.
func CheckSpecFor(text string, kind SpecKind) (summary string, warnings []string, err error) {
	switch kind {
	case KindTTS, KindSTT:
	default:
		return CheckSpec(text, kind == KindVideo)
	}
	s, err := ParseSpecFor(text, kind)
	if err != nil {
		return "", nil, err
	}
	method := func(c *SpecCall, def string) string { return firstNonEmpty(c.Method, def) }
	var parts []string
	if s.ID == "" {
		parts = append(parts, fmt.Sprintf("Синхронне API: %s %s", method(s.Submit, "POST"), clip(s.Submit.Path, 60)))
	} else {
		parts = append(parts, fmt.Sprintf("Задачі: %s %s → статус %s", method(s.Submit, "POST"), clip(s.Submit.Path, 50), clip(s.Poll.Path, 50)))
	}
	what := "аудіо"
	if kind == KindSTT {
		what = "текст"
	}
	switch {
	case s.Fetch != nil && len(s.Result) == 0:
		parts = append(parts, what+" — відповідь "+clip(s.Fetch.Path, 50))
	case len(s.Result) > 0:
		parts = append(parts, what+": "+strings.Join(s.Result, " або "))
	default:
		parts = append(parts, what+" — сама відповідь")
	}
	if kind == KindTTS {
		if !s.uses["voice"] {
			warnings = append(warnings, "голос не передається ({{voice}}) — сервіс озвучить своїм типовим")
		}
		if !s.uses["language"] && !s.uses["language_name"] {
			warnings = append(warnings, "мова не передається ({{language}}) — сервіс визначатиме її сам")
		}
	} else if s.Submit.Encoding != "multipart" && s.uses["audio"] {
		warnings = append(warnings, `{{audio}} іде як data: URI у JSON — якщо сервіс чекає файл, постав "encoding": "multipart"`)
	}
	if s.ID != "" && s.Cancel == nil {
		warnings = append(warnings, "немає cancel — задачу, яку студія перестала чекати, сервіс доробить")
	}
	return strings.Join(parts, " · "), warnings, nil
}

// ---- running ----------------------------------------------------------------------------

// AudioAPI is one configured audio API (the TTS or STT section's address,
// key and model plus the spec).
type AudioAPI struct {
	Kind    SpecKind // KindTTS | KindSTT
	BaseURL string   // "" = the origin of an absolute submit path
	Key     string
	Model   string
	Spec    string
	HTTP    *http.Client // nil = a default client
}

// SpeechRequest is one line to voice.
type SpeechRequest struct {
	Text, Voice string
	Language    string  // ISO 639-1 ("" = none)
	Speed       float64 // 0 = none
	Format      string  // "" = mp3
}

// TranscribeRequest is one recording to transcribe.
type TranscribeRequest struct {
	Audio    []byte
	Name     string // file name (its extension names the format)
	MIME     string // "" = from the name / the bytes
	Language string // ISO 639-1 ("" = detect)
}

func (a AudioAPI) what() string {
	if a.Kind == KindSTT {
		return "розпізнавання мови"
	}
	return "озвучки"
}

// endpoint resolves the API (spec parsed, base URL checked).
func (a AudioAPI) endpoint() (*endpoint, *Spec, error) {
	kind := a.Kind
	if kind != KindSTT {
		kind = KindTTS
	}
	sp, err := ParseSpecFor(a.Spec, kind)
	if err != nil {
		if strings.TrimSpace(a.Spec) == "" {
			return nil, nil, &Error{Msg: fmt.Sprintf("Своє API %s: опис API порожній — обери шаблон або «Налаштувати з документації»", a.what())}
		}
		return nil, nil, &Error{Msg: fmt.Sprintf("Своє API %s: %s", a.what(), err.Error())}
	}
	c := &Client{http: a.HTTP}
	ep, _, err := c.universalEndpointKind(a.BaseURL, a.Key, a.Model, a.Spec, a.what(), kind)
	if err != nil {
		return nil, nil, err
	}
	return ep, sp, nil
}

// Speech voices one line through the spec: the audio bytes and their
// Content-Type.
func (a AudioAPI) Speech(ctx context.Context, r SpeechRequest) ([]byte, string, error) {
	a.Kind = KindTTS
	ep, sp, err := a.endpoint()
	if err != nil {
		return nil, "", err
	}
	u := newUniJob(ep, sp, false)
	u.vars["text"] = r.Text
	if v := strings.TrimSpace(r.Voice); v != "" {
		u.vars["voice"] = v
	}
	if l := strings.TrimSpace(r.Language); l != "" {
		u.vars["language"] = l
		if n := languageNames[strings.ToLower(l)]; n != "" {
			u.vars["language_name"] = n
		}
	}
	if r.Speed > 0 {
		u.vars["speed"] = json.Number(strconv.FormatFloat(r.Speed, 'f', -1, 64))
	}
	u.vars["format"] = firstNonEmpty(r.Format, "mp3")
	data, ctype, err := u.run(ctx, maxAudioBytes, gen.NoProgress)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", ep.errf(0, false, "%s: порожня відповідь замість аудіо", ep.name)
	}
	if mt, _, _ := mime.ParseMediaType(ctype); strings.Contains(mt, "json") || strings.HasPrefix(mt, "text/") {
		return nil, "", ep.errf(0, false, "%s: замість аудіо прийшов %s: %s", ep.name, mt, httpdiag.Body(data, 200, ep.key))
	}
	return data, ctype, nil
}

// Transcribe sends a recording through the spec and returns the transcript
// as the API gave it (JSON, SRT, VTT or text) for stt.ParseTranscript.
func (a AudioAPI) Transcribe(ctx context.Context, r TranscribeRequest) ([]byte, error) {
	a.Kind = KindSTT
	ep, sp, err := a.endpoint()
	if err != nil {
		return nil, err
	}
	if len(r.Audio) == 0 {
		return nil, ep.errf(0, false, "%s: порожній запис", ep.name)
	}
	mt := strings.TrimSpace(r.MIME)
	if mt == "" {
		mt = audioMIME(r.Name, r.Audio)
	}
	u := newUniJob(ep, sp, true) // video timeouts: long recordings take minutes
	u.raw = true
	b64 := base64.StdEncoding.EncodeToString(r.Audio)
	u.vars["audio"] = "data:" + mt + ";base64," + b64
	u.vars["audio_b64"] = b64
	u.vars["audio_name"] = firstNonEmpty(r.Name, "audio."+audioExt(mt))
	u.vars["audio_mime"] = mt
	if l := strings.TrimSpace(r.Language); l != "" {
		u.vars["language"] = l
	}
	data, _, err := u.run(ctx, maxTranscriptBytes, gen.NoProgress)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, ep.errf(0, false, "%s: порожня відповідь замість тексту", ep.name)
	}
	return data, nil
}

// Test makes the spec's "test" call (nothing is generated); untested says
// the spec has none (only its syntax was checked).
func (a AudioAPI) Test(ctx context.Context) (ok bool, msg string) {
	ep, sp, err := a.endpoint()
	if err != nil {
		return false, err.Error()
	}
	if sp.Test == nil {
		return true, ep.name + `: опис API коректний · ключ і адресу перевірить перша озвучка (додай "test" в опис, щоб перевіряти без генерації)`
	}
	p := &protocol{name: ep.name, test: func(ctx context.Context, ep *endpoint) (string, error) { return uniTest(ctx, ep, sp) }}
	return runTest(ctx, ep, p)
}

// finishRaw is finish for a transcript: the optional fetch, then the value
// at "result" (objects and arrays as JSON, a URL downloaded, text as is) or
// the whole reply.
func (u *uniJob) finishRaw(ctx context.Context, limit int64) ([]byte, string, error) {
	sp, ep := u.sp, u.ep
	v := u.status
	if sp.Fetch != nil {
		rq, err := u.request("fetch", sp.Fetch, http.MethodGet, downloadTimeout, max(limit, maxReply))
		if err != nil {
			return nil, "", err
		}
		res, err := ep.call(ctx, rq)
		if err != nil {
			return nil, "", err
		}
		if !looksJSON(res.body) {
			return res.body, res.header.Get("Content-Type"), nil
		}
		if v, err = u.decode(res, "результату"); err != nil {
			return nil, "", err
		}
	}
	for _, p := range sp.Result {
		x, ok := lookupPath(v, p)
		if !ok || emptyValue(x) {
			continue
		}
		if s, isStr := x.(string); isStr {
			t := strings.TrimSpace(s)
			if strings.HasPrefix(t, "http://") || strings.HasPrefix(t, "https://") {
				data, ctype, err := ep.download(ctx, u.withKey(ep.abs(t)), limit)
				return data, ctype, err
			}
			return []byte(t), "text/plain", nil
		}
		b, err := json.Marshal(x)
		if err != nil {
			return nil, "", err
		}
		return b, "application/json", nil
	}
	if len(sp.Result) > 0 {
		b, _ := json.Marshal(v)
		return nil, "", ep.errf(0, false, "%s: у відповіді немає тексту (result: %s): %s", ep.name, strings.Join(sp.Result, ", "), httpdiag.Body(b, 240, ep.key))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, "", err
	}
	return b, "application/json", nil
}

// looksJSON: the body is a JSON object or array.
func looksJSON(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[') && json.Valid(t)
}

// partName is the file name of a multipart file part of type mt.
func partName(mt string, n int) string {
	if strings.HasPrefix(strings.ToLower(mt), "audio/") {
		return fmt.Sprintf("audio-%d.%s", n, audioExt(mt))
	}
	return fmt.Sprintf("image-%d.%s", n, imageExt(mt))
}

func audioExt(mt string) string {
	switch strings.ToLower(mt) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/ogg", "audio/opus":
		return "ogg"
	case "audio/flac", "audio/x-flac":
		return "flac"
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac":
		return "m4a"
	case "audio/webm":
		return "webm"
	}
	return "mp3"
}

// audioMIME guesses the type of a recording from its name, then its bytes.
func audioMIME(name string, data []byte) string {
	low := strings.ToLower(name)
	for ext, mt := range map[string]string{".wav": "audio/wav", ".ogg": "audio/ogg", ".opus": "audio/ogg", ".flac": "audio/flac",
		".m4a": "audio/mp4", ".mp4": "audio/mp4", ".aac": "audio/aac", ".webm": "audio/webm", ".mp3": "audio/mpeg"} {
		if strings.HasSuffix(low, ext) {
			return mt
		}
	}
	if s := http.DetectContentType(data); strings.HasPrefix(s, "audio/") {
		return s
	}
	return "audio/mpeg"
}

// languageNames are English names for {{language_name}}.
var languageNames = map[string]string{
	"uk": "Ukrainian", "en": "English", "ru": "Russian", "pl": "Polish", "de": "German", "fr": "French", "es": "Spanish",
	"it": "Italian", "pt": "Portuguese", "tr": "Turkish", "nl": "Dutch", "cs": "Czech", "sk": "Slovak", "ro": "Romanian",
	"bg": "Bulgarian", "hr": "Croatian", "sv": "Swedish", "da": "Danish", "fi": "Finnish", "no": "Norwegian", "el": "Greek",
	"ja": "Japanese", "ko": "Korean", "zh": "Chinese", "ar": "Arabic", "he": "Hebrew", "hi": "Hindi", "id": "Indonesian",
	"vi": "Vietnamese", "th": "Thai", "ka": "Georgian", "kk": "Kazakh", "be": "Belarusian", "lt": "Lithuanian", "lv": "Latvian",
	"et": "Estonian", "hu": "Hungarian", "sr": "Serbian", "sl": "Slovenian",
}
