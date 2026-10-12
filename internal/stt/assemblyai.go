package stt

// AssemblyAI pre-recorded transcription (cfg.STT.Provider "assemblyai";
// verified 2026-10-02 against https://www.assemblyai.com/docs —
// pre-recorded-audio/api-reference/files/upload, …/transcripts/submit,
// …/transcripts/get, select-the-speech-model, supported-languages,
// select-the-region and guides/common_errors_and_solutions):
//
//	POST {base}/v2/upload        authorization: <key> (no "Bearer")
//	     raw bytes, Content-Type application/octet-stream → {"upload_url": …}
//	POST {base}/v2/transcript    {"audio_url", "punctuate", "format_text",
//	     "language_code": "uk" | "language_detection": true, "speech_models"}
//	     → {"id", "status"}
//	GET  {base}/v2/transcript/{id} until status completed | error (every 3 s):
//	     text, words[{text, start, end (ms), confidence}], audio_duration (s),
//	     language_code
//
// base: https://api.assemblyai.com, or https://api.eu.assemblyai.com for EU
// data residency (STT.AssemblyEU); the key is STT.AssemblyKey. Universal-3.5
// Pro is the default model, other languages fall back to Universal-2;
// Ukrainian is a Universal-2 language, so "uk" asks for it directly.
// Errors are {"error": "…"}: 401 a bad key, 400 "account balance is
// negative", 400 "Language code … is not supported", 429 (Retry-After) or
// 403 when rate-limited.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AssemblyAI endpoints (vars: tests point them at mocks).
var (
	AssemblyBase   = "https://api.assemblyai.com"
	AssemblyBaseEU = "https://api.eu.assemblyai.com"
	assemblyPoll   = 3 * time.Second
)

// ProviderAssembly is cfg.STT.Provider for AssemblyAI.
const ProviderAssembly = "assemblyai"

// AssemblyKeyURL is where the owner gets a key.
const AssemblyKeyURL = "https://www.assemblyai.com/dashboard"

func (c *Client) transcribeAssembly(ctx context.Context, s settings, path, lang string, progress func(int, string)) (Transcript, error) {
	rep := newReporter(progress)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Transcript{}, fmt.Errorf("Файл запису не знайдено: %s", filepath.Base(path))
	}
	if info.Size() == 0 {
		return Transcript{}, errors.New("Файл запису порожній")
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// 1. The file.
	rep.force(2, "Завантажуємо запис в AssemblyAI…")
	f, err := os.Open(path)
	if err != nil {
		return Transcript{}, fmt.Errorf("не вдалося прочитати файл запису: %w", err)
	}
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.base+"/v2/upload", f)
	if err != nil {
		f.Close()
		return Transcript{}, err
	}
	req.ContentLength = info.Size()
	req.Header.Set("authorization", s.key)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.do(req)
	f.Close()
	if err != nil {
		return Transcript{}, netErr(ctx, s, err)
	}
	if !res.ok() {
		return Transcript{}, assemblyErr(res)
	}
	var up struct {
		URL string `json:"upload_url"`
	}
	if json.Unmarshal(res.body, &up) != nil || up.URL == "" {
		return Transcript{}, fmt.Errorf("AssemblyAI: незрозуміла відповідь на завантаження: %s", clip(string(res.body), 160))
	}

	// 2. The job.
	job := map[string]any{"audio_url": up.URL, "punctuate": true, "format_text": true}
	switch l := strings.ToLower(strings.TrimSpace(lang)); l {
	case "":
		job["language_detection"] = true
	case "uk":
		job["language_code"] = l
		job["speech_models"] = []string{"universal-2"} // Ukrainian is a Universal-2 language
	default:
		job["language_code"] = l
	}
	body, _ := json.Marshal(job)
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if res, err = c.assemblyJSON(rctx, s, http.MethodPost, "/v2/transcript", body); err != nil {
		return Transcript{}, netErr(ctx, s, err)
	}
	if !res.ok() {
		return Transcript{}, assemblyErr(res)
	}
	if json.Unmarshal(res.body, &created) != nil || created.ID == "" {
		return Transcript{}, fmt.Errorf("AssemblyAI: незрозуміла відповідь: %s", clip(string(res.body), 160))
	}

	// 3. Wait for it.
	rep.force(10, "AssemblyAI розпізнає мову…")
	fails := 0
	for {
		if err := sleep(rctx, assemblyPoll); err != nil {
			if ctx.Err() != nil {
				return Transcript{}, ctx.Err()
			}
			return Transcript{}, errors.New("AssemblyAI не встиг розпізнати запис — спробуй ще раз пізніше")
		}
		res, err := c.assemblyJSON(rctx, s, http.MethodGet, "/v2/transcript/"+created.ID, nil)
		switch {
		case err != nil || res.status >= 500 || res.status == http.StatusTooManyRequests:
			if ctx.Err() != nil {
				return Transcript{}, ctx.Err()
			}
			if fails++; fails > 5 {
				if err != nil {
					return Transcript{}, netErr(ctx, s, err)
				}
				return Transcript{}, assemblyErr(res)
			}
			continue
		case !res.ok():
			return Transcript{}, assemblyErr(res)
		}
		fails = 0
		var r assemblyReply
		if err := json.Unmarshal(res.body, &r); err != nil {
			return Transcript{}, fmt.Errorf("AssemblyAI: незрозуміла відповідь: %s", clip(string(res.body), 160))
		}
		switch r.Status {
		case "completed":
			tr := r.transcript()
			tr.finish()
			rep.force(100, fmt.Sprintf("Мову розпізнано · %d %s", len(tr.Words), wordsWord(len(tr.Words))))
			return tr, nil
		case "error":
			return Transcript{}, assemblyErr(reply{status: http.StatusBadRequest, body: []byte(`{"error":` + strconv.Quote(r.Error) + `}`)})
		case "processing":
			rep.report(50, "AssemblyAI розпізнає мову…")
		}
	}
}

// assemblyReply is GET /v2/transcript/{id}.
type assemblyReply struct {
	Status   string  `json:"status"`
	Error    string  `json:"error"`
	Text     string  `json:"text"`
	Duration float64 `json:"audio_duration"` // seconds
	Language string  `json:"language_code"`
	Words    []struct {
		Text  string   `json:"text"`
		Start *float64 `json:"start"` // ms
		End   *float64 `json:"end"`
	} `json:"words"`
}

func (r assemblyReply) transcript() Transcript {
	tr := Transcript{Text: r.Text, Language: r.Language, Duration: r.Duration}
	for _, w := range r.Words {
		start, end := timeOr(w.Start), timeOr(w.End)
		tr.Words = append(tr.Words, Word{Text: w.Text, Start: start / 1000, End: end / 1000})
	}
	return tr
}

func (c *Client) assemblyJSON(ctx context.Context, s settings, method, path string, body []byte) (reply, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, rd)
	if err != nil {
		return reply{}, err
	}
	req.Header.Set("authorization", s.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req)
}

// testAssembly: a cheap authenticated read (the newest transcript, if any).
func (c *Client) testAssembly(ctx context.Context, s settings) (bool, string) {
	res, err := c.assemblyJSON(ctx, s, http.MethodGet, "/v2/transcript?limit=1", nil)
	if err != nil {
		return false, netErr(ctx, s, err).Error()
	}
	if !res.ok() {
		return false, assemblyErr(res).Error()
	}
	where := ""
	if strings.Contains(s.base, ".eu.") {
		where = " · дані в ЄС"
	}
	return true, "AssemblyAI: ключ працює" + where
}

// assemblyErr turns an AssemblyAI error into plain Ukrainian.
func assemblyErr(r reply) error {
	msg := errDetail(r.body)
	if msg == "" {
		msg = strings.TrimSpace(string(r.body))
	}
	low := strings.ToLower(msg)
	switch {
	case r.status == http.StatusUnauthorized || strings.Contains(low, "authentication error"):
		return errors.New("AssemblyAI не прийняв ключ (401) — перевір ключ у Налаштуваннях → Розпізнавання мови")
	case strings.Contains(low, "balance") || strings.Contains(low, "top up") || r.status == http.StatusPaymentRequired:
		return errors.New("AssemblyAI: на рахунку закінчились кошти — поповни баланс на assemblyai.com або вибери інший сервіс")
	case strings.Contains(low, "language") && strings.Contains(low, "not supported"):
		return errors.New("AssemblyAI не розпізнає цю мову — вибери іншу мову проєкту або інший сервіс розпізнавання")
	case r.status == http.StatusTooManyRequests || (r.status == http.StatusForbidden && strings.Contains(low, "rate")):
		return errors.New("AssemblyAI перевантажений або вичерпано ліміт запитів — спробуй за хвилину")
	case r.status >= 500:
		return fmt.Errorf("AssemblyAI тимчасово недоступний (%d) — спробуй ще раз", r.status)
	case strings.Contains(low, "transcoding failed") || strings.Contains(low, "file does not appear to contain audio"):
		return errors.New("AssemblyAI не зміг прочитати запис — перевір, що це звичайний аудіо- чи відеофайл")
	}
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", r.status)
	}
	return fmt.Errorf("AssemblyAI відхилив запит на розпізнавання: %s", clip(msg, 200))
}
