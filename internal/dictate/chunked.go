package dictate

// Phrase-at-a-time services (the chunker sends each phrase as a WAV):
//
// OpenAI-compatible (verified 2026-10-02 against
// https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
// and https://console.groq.com/docs/speech-to-text):
//
//	POST {base}/audio/transcriptions   Authorization: Bearer <key>   multipart:
//	  file (audio.wav) · model · response_format=json · temperature=0 · prompt
//	  · language (ISO 639-1) — gpt-transcribe takes languages[] instead
//	← {"text":"…"}
//
// gpt-4o-transcribe / gpt-4o-mini-transcribe answer json only (no
// verbose_json); whisper-1 and Whisper servers (Groq, local ones) take json too.
//
// Gemini (Google AI Studio; https://ai.google.dev/gemini-api/docs/audio and
// .../generate-content/audio):
//
//	POST {base}/models/{model}:generateContent   x-goog-api-key: <key>
//	{"contents":[{"role":"user","parts":[{"text":"<instructions>"},
//	  {"inlineData":{"mimeType":"audio/wav","data":"<base64>"}}]}],
//	 "generationConfig":{"temperature":0}}
//	← candidates[0].content.parts[].text
//
// Inline requests are capped at 20 MB (a 15 s phrase is 480 KB). With several
// Google keys the next one takes over after a quota error (429).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

// httpClient sends the phrase requests (a var: tests).
var httpClient = &http.Client{}

const phraseTimeout = 40 * time.Second

// ---- OpenAI-compatible ------------------------------------------------------------------------

type openaiHTTP struct{ p Plan }

func (t *openaiHTTP) start(context.Context) error { return nil }
func (t *openaiHTTP) close()                      {}

func (t *openaiHTTP) who() string {
	if h := hostOf(t.p.BaseURL); h != "" && h != openaiOfficialHost {
		return h
	}
	return "OpenAI"
}

func (t *openaiHTTP) transcribe(ctx context.Context, pcm []int16, lang, prompt string) (string, string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	model := firstNonEmpty(t.p.Model, WhisperCompatDef)
	_ = mw.WriteField("model", model)
	_ = mw.WriteField("response_format", "json")
	_ = mw.WriteField("temperature", "0")
	if lang != "" {
		if languagesArray(model) {
			_ = mw.WriteField("languages[]", lang)
		} else if len(lang) == 2 {
			_ = mw.WriteField("language", lang)
		}
	}
	if prompt != "" && !strings.Contains(model, "diarize") {
		_ = mw.WriteField("prompt", prompt)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	h.Set("Content-Type", "audio/wav")
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(wav(padTo(pcm, SampleRate/2)))
	_ = mw.Close()

	rctx, cancel := context.WithTimeout(ctx, phraseTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, strings.TrimRight(t.p.BaseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if t.p.Key != "" {
		req.Header.Set("Authorization", "Bearer "+t.p.Key)
	}
	status, b, err := do(req)
	if err != nil {
		return "", "", netErr(ctx, t.who(), err)
	}
	if status/100 != 2 {
		return "", "", httpErr(t.who(), status, b, true)
	}
	var r struct {
		Text      string `json:"text"`
		Language  string `json:"language"`
		Languages []struct {
			Code string `json:"code"`
		} `json:"languages"`
	}
	if json.Unmarshal(b, &r) != nil {
		if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
			return strings.TrimSpace(string(b)), "", nil // a server that answers text
		}
		return "", "", newErr(KindService, nil, "%s повернув незрозумілу відповідь: %s", t.who(), clip(string(b), 120))
	}
	det := ""
	if len(r.Languages) > 0 {
		det = r.Languages[0].Code
	} else if len(r.Language) == 2 {
		det = r.Language
	}
	return r.Text, det, nil
}

// padTo pads very short phrases with silence (some servers refuse < 0.1 s;
// a little air also helps the models).
func padTo(pcm []int16, n int) []int16 {
	if len(pcm) >= n {
		return pcm
	}
	out := make([]int16, n)
	copy(out[(n-len(pcm))/2:], pcm)
	return out
}

// ---- Gemini -----------------------------------------------------------------------------------

type geminiT struct {
	p    Plan
	mu   sync.Mutex
	next int // the key to use (rotates on quota errors)
}

func (t *geminiT) start(context.Context) error { return nil }
func (t *geminiT) close()                      {}

func (t *geminiT) keys() []string {
	if len(t.p.Keys) > 0 {
		return t.p.Keys
	}
	return []string{t.p.Key}
}

func geminiPrompt(lang, prompt string) string {
	var b strings.Builder
	b.WriteString("Transcribe the speech in this audio clip exactly as it is spoken, word for word")
	if lang != "" {
		fmt.Fprintf(&b, " (the language is ISO 639 %q)", lang)
	} else {
		b.WriteString(" in the language it is spoken in (never translate)")
	}
	b.WriteString(". Use normal punctuation and capitalisation. Reply with the transcript only: no quotes, labels, timestamps, translations or comments. If there is no intelligible speech, reply with nothing.")
	if prompt != "" {
		fmt.Fprintf(&b, "\nFor context only (do not repeat it), the text dictated just before this clip: %q", prompt)
	}
	return b.String()
}

func (t *geminiT) transcribe(ctx context.Context, pcm []int16, lang, prompt string) (string, string, error) {
	model := firstNonEmpty(t.p.Model, GeminiDef)
	payload, _ := json.Marshal(map[string]any{
		"contents": []any{map[string]any{
			"role": "user",
			"parts": []any{
				map[string]any{"text": geminiPrompt(lang, prompt)},
				map[string]any{"inlineData": map[string]any{"mimeType": "audio/wav", "data": base64.StdEncoding.EncodeToString(wav(padTo(pcm, SampleRate/2)))}},
			},
		}},
		"generationConfig": map[string]any{"temperature": 0},
	})
	keys := t.keys()
	var lastErr error
	for range keys {
		t.mu.Lock()
		i := t.next % len(keys)
		t.mu.Unlock()
		text, err := t.call(ctx, model, keys[i], payload)
		if err == nil {
			return text, "", nil
		}
		lastErr = err
		if KindOf(err) != KindQuota || len(keys) == 1 {
			return "", "", err
		}
		t.mu.Lock()
		t.next = i + 1
		t.mu.Unlock()
	}
	return "", "", lastErr
}

func (t *geminiT) call(ctx context.Context, model, key string, payload []byte) (string, error) {
	rctx, cancel := context.WithTimeout(ctx, phraseTimeout)
	defer cancel()
	u := strings.TrimRight(firstNonEmpty(t.p.BaseURL, geminiBase), "/") + "/models/" + model + ":generateContent"
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key)
	status, b, err := do(req)
	if err != nil {
		return "", netErr(ctx, "Google AI Studio", err)
	}
	if status/100 != 2 {
		return "", geminiErr(status, b, model)
	}
	var r struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", newErr(KindService, err, "Google AI Studio повернув незрозумілу відповідь")
	}
	if r.PromptFeedback.BlockReason != "" {
		return "", nil // blocked phrase: nothing to type
	}
	var parts []string
	if len(r.Candidates) > 0 {
		for _, p := range r.Candidates[0].Content.Parts {
			if !p.Thought && strings.TrimSpace(p.Text) != "" {
				parts = append(parts, strings.TrimSpace(p.Text))
			}
		}
	}
	return strings.Trim(strings.Join(parts, " "), "\"«»“”"), nil
}

func geminiErr(status int, b []byte, model string) error {
	low := strings.ToLower(string(b))
	switch {
	case status == http.StatusBadRequest && (strings.Contains(low, "api_key_invalid") || strings.Contains(low, "api key not valid")),
		status == http.StatusUnauthorized:
		return newErr(KindAuth, nil, "Google AI Studio не прийняв ключ — перевір його в Налаштуваннях")
	case status == http.StatusForbidden:
		return newErr(KindAuth, nil, "Google AI Studio відмовив у доступі (403) — перевір ключ і чи увімкнено Gemini API для проєкту")
	case status == http.StatusTooManyRequests || strings.Contains(low, "resource_exhausted"):
		return newErr(KindQuota, nil, "Google AI Studio: вичерпано ліміт запитів — спробуй за хвилину або додай ще ключ")
	case status == http.StatusNotFound:
		return newErr(KindService, nil, "Модель «%s» недоступна в Google AI Studio — вкажи іншу в Налаштуваннях → Диктування", model)
	case status >= 500:
		return newErr(KindService, nil, "Google AI Studio тимчасово недоступний (%d) — спробуй ще раз", status)
	}
	return newErr(KindService, nil, "Google AI Studio відхилив запит (%d): %s", status, clip(firstNonEmpty(errDetail(b), string(b)), 160))
}

// ---- shared HTTP helpers ----------------------------------------------------------------------

func do(req *http.Request) (int, []byte, error) {
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, b, err
}

// netErr maps a transport failure.
func netErr(ctx context.Context, who string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if strings.Contains(err.Error(), "deadline exceeded") || strings.Contains(err.Error(), "Timeout") {
		return newErr(KindNetwork, err, "%s не відповів вчасно — перевір інтернет і спробуй ще раз", who)
	}
	return newErr(KindNetwork, err, "Немає з'єднання з %s — перевір інтернет або адресу в Налаштуваннях → Диктування", who)
}

// httpErr maps a non-2xx reply of a transcription endpoint.
func httpErr(who string, status int, b []byte, openai bool) error {
	low := strings.ToLower(string(b))
	switch {
	case status == http.StatusUnauthorized:
		return newErr(KindAuth, nil, "%s не прийняв ключ (401) — перевір його в Налаштуваннях → Диктування", who)
	case status == http.StatusPaymentRequired || strings.Contains(low, "insufficient_quota") || strings.Contains(low, "quota_exceeded"):
		return newErr(KindQuota, nil, "%s: закінчились кошти або кредити — поповни баланс або вибери інший сервіс диктування", who)
	case status == http.StatusNotFound && openai:
		return newErr(KindService, nil, "%s не вміє розпізнавати мову (немає /audio/transcriptions) або не знає модель — перевір адресу й модель у Налаштуваннях → Диктування", who)
	case status == http.StatusTooManyRequests:
		return newErr(KindQuota, nil, "%s перевантажений (429) — спробуй за хвилину", who)
	case status >= 500:
		return newErr(KindService, nil, "%s тимчасово недоступний (%d) — спробуй ще раз", who, status)
	}
	return newErr(KindService, nil, "%s відхилив запит (%d): %s", who, status, clip(firstNonEmpty(errDetail(b), string(b)), 160))
}
