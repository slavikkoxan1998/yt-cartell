package reference

// The models that watch the video.
//
//	Google  POST {base}/models/{model}:generateContent with the owner's Google
//	        AI Studio keys (gemini.Pool: a spent key rests 24 h, the next one
//	        is used). The video is a part {"fileData": {"fileUri": <YouTube
//	        URL>, "mimeType": "video/*"}, "videoMetadata": {"startOffset":
//	        "600s", "endOffset": "1200s", "fps": 0.5}} before the text part
//	        (https://ai.google.dev/gemini-api/docs/video-understanding);
//	        generationConfig asks for JSON and, for long videos,
//	        mediaResolution MEDIA_RESOLUTION_LOW (~100 instead of ~300 tokens a
//	        second). Public videos only; the free tier watches ≤ 8 h of
//	        YouTube a day.
//	Text    an OpenAI-compatible /chat/completions gateway (CarteLink …) whose
//	        model may be a Gemini that understands a YouTube URL in a content
//	        part. Shapes tried in order: image_url {url}, file {file_id,
//	        format} (LiteLLM), video_url {url}. The first answer must prove the
//	        model watched: its duration must match the watch page (else its
//	        title the oEmbed title); a refusal, a wrong length or an HTTP 4xx
//	        moves on to the next shape. The working shape (or "cannot") is
//	        remembered per endpoint + model for the session (a "cannot" for 6 h).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"ytcartell/internal/gemini"
)

// Request is one look at the video.
type Request struct {
	URL        string
	System     string
	Prompt     string
	Start, End float64 // clip in seconds (0 = from the start / to the end)
	FPS        float64 // frames a second (0 = the model's default, 1)
	LowRes     bool    // low media resolution (long videos)
}

// Watcher is a model that can watch a YouTube video.
type Watcher interface {
	Name() string  // "google" | "text"
	Label() string // for the UI: "Gemini · gemini-3.8-flash"
	Watch(ctx context.Context, r Request) (string, error)
}

// Tunables (package vars so tests shorten them).
var (
	googleBase   = "https://generativelanguage.googleapis.com/v1beta"
	watchTimeout = 10 * time.Minute
	retryPause   = 3 * time.Second
)

const maxReply = 32 << 20

// ---- Google AI Studio ----------------------------------------------------------------------------

// Google watches with the Gemini API.
type Google struct {
	Pool  *gemini.Pool
	Model string
	HTTP  *http.Client // nil = http.DefaultClient
}

func (g *Google) Name() string  { return "google" }
func (g *Google) Label() string { return "Gemini · " + g.Model }

type googlePart struct {
	Text          string         `json:"text,omitempty"`
	FileData      *googleFile    `json:"fileData,omitempty"`
	VideoMetadata map[string]any `json:"videoMetadata,omitempty"`
}

type googleFile struct {
	FileURI  string `json:"fileUri"`
	MimeType string `json:"mimeType,omitempty"`
}

// offset is a protobuf Duration ("75s", "75.5s").
func offset(sec float64) string {
	return strconv.FormatFloat(math.Round(sec*10)/10, 'f', -1, 64) + "s"
}

func (g *Google) body(r Request) ([]byte, error) {
	video := googlePart{FileData: &googleFile{FileURI: r.URL, MimeType: "video/*"}}
	vm := map[string]any{}
	if r.Start > 0 {
		vm["startOffset"] = offset(r.Start)
	}
	if r.End > 0 {
		vm["endOffset"] = offset(r.End)
	}
	if r.FPS > 0 {
		vm["fps"] = r.FPS
	}
	if len(vm) > 0 {
		video.VideoMetadata = vm
	}
	gc := map[string]any{"temperature": 0.2, "responseMimeType": "application/json"}
	if r.LowRes {
		gc["mediaResolution"] = "MEDIA_RESOLUTION_LOW"
	}
	req := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []googlePart{video, {Text: r.Prompt}}}},
		"generationConfig": gc,
	}
	if strings.TrimSpace(r.System) != "" {
		req["systemInstruction"] = map[string]any{"parts": []googlePart{{Text: r.System}}}
	}
	return json.Marshal(req)
}

// Watch implements Watcher. A request whose clip or frame rate the API
// refuses (an error that is not about the video) is made once more without
// them — the prompt still names the part to watch.
func (g *Google) Watch(ctx context.Context, r Request) (string, error) {
	if g.Pool == nil || g.Pool.Size() == 0 {
		return "", newError(KindNoService, "no Google keys")
	}
	out, err := g.do(ctx, r)
	if err != nil && ctx.Err() == nil && (r.Start > 0 || r.End > 0 || r.FPS > 0) && clipRefused(err) {
		log.Printf("[reference] %s refused the clip options (%v) — watching without them", g.Model, err)
		r.Start, r.End, r.FPS = 0, 0, 0
		out, err = g.do(ctx, r)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", googleErr(err)
	}
	return out, nil
}

// clipRefused: a 400 or 5xx that is not about the video itself.
func clipRefused(err error) bool {
	var ae *gemini.APIError
	if !errors.As(err, &ae) {
		return false
	}
	if ae.Status >= 500 {
		return true
	}
	if ae.Status != http.StatusBadRequest {
		return false
	}
	msg := googleMessage(ae.Body)
	return !reVideoPrivate.MatchString(msg) && !reVideoUnlisted.MatchString(msg) && !reVideoAge.MatchString(msg) &&
		!reVideoUnavailable.MatchString(msg) && !reVideoTooLong.MatchString(msg) && !strings.Contains(strings.ToUpper(ae.Body), "API_KEY")
}

// do makes the request (5xx and dropped connections retried twice) and
// returns the raw error.
func (g *Google) do(ctx context.Context, r Request) (string, error) {
	body, err := g.body(r)
	if err != nil {
		return "", err
	}
	model := strings.TrimPrefix(strings.TrimSpace(g.Model), "models/")
	u := googleBase + "/models/" + model + ":generateContent"
	label := "gemini:" + model
	hc := g.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	var out string
	for attempt := 0; ; attempt++ {
		err = g.Pool.Do(ctx, func(key string) error {
			actx, cancel := context.WithTimeout(ctx, watchTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(actx, http.MethodPost, u, bytes.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-goog-api-key", key)
			res, err := hc.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			b, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
			if err != nil {
				return err
			}
			if res.StatusCode < 200 || res.StatusCode > 299 {
				return &gemini.APIError{Label: label, Status: res.StatusCode, Body: string(b)}
			}
			text, why, err := googleText(b)
			if err != nil {
				return err
			}
			if strings.TrimSpace(text) == "" {
				return &Error{Kind: KindBadReply, Msg: "Gemini не відповів" + why, Detail: clip(string(b), 400)}
			}
			out = text
			return nil
		})
		if err == nil {
			return out, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if attempt < 2 && retryable(err) {
			if serr := sleep(ctx, retryPause*time.Duration(attempt+1)); serr != nil {
				return "", serr
			}
			continue
		}
		return "", err
	}
}

// googleText is the answer's text (thoughts skipped) and, when empty, why.
func googleText(b []byte) (string, string, error) {
	var r struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback *struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", "", &Error{Kind: KindBadReply, Msg: "Незрозуміла відповідь Google", Detail: err.Error()}
	}
	var sb strings.Builder
	why := ""
	if len(r.Candidates) > 0 {
		for _, p := range r.Candidates[0].Content.Parts {
			if !p.Thought {
				sb.WriteString(p.Text)
			}
		}
		if f := r.Candidates[0].FinishReason; f != "" && f != "STOP" && f != "MAX_TOKENS" {
			why = " (" + f + ")"
		}
	}
	if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
		why = " (заблоковано: " + r.PromptFeedback.BlockReason + ")"
	}
	return sb.String(), why, nil
}

// retryable: a 5xx, a timeout or a dropped connection (not a quota, not a
// refusal of the video).
func retryable(err error) bool {
	var ae *gemini.APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500 || ae.Status == http.StatusRequestTimeout
	}
	var re *Error
	if errors.As(err, &re) || errors.Is(err, gemini.ErrNoKeys) || errors.Is(err, gemini.ErrExhausted) {
		return false
	}
	return true // network
}

var (
	reVideoPrivate     = regexp.MustCompile(`(?i)private`)
	reVideoUnlisted    = regexp.MustCompile(`(?i)unlisted`)
	reVideoAge         = regexp.MustCompile(`(?i)age[- ]?restrict|confirm your age`)
	reVideoUnavailable = regexp.MustCompile(`(?i)not (be )?(found|available)|unavailable|cannot (fetch|access|be accessed)|could not (fetch|access|be retrieved|process)|failed to (fetch|download|retrieve)|invalid (youtube|video)|unsupported (video|url)|removed`)
	reVideoTooLong     = regexp.MustCompile(`(?i)too long|exceeds? the maximum|input token count|token count .* exceeds|context (length|window)|too large`)
)

// googleErr explains a failed Gemini call.
func googleErr(err error) error {
	switch {
	case errors.Is(err, gemini.ErrNoKeys):
		return newError(KindNoService, err.Error())
	case errors.Is(err, gemini.ErrExhausted):
		return newError(KindQuota, err.Error())
	}
	var re *Error
	if errors.As(err, &re) {
		return re
	}
	var ae *gemini.APIError
	if !errors.As(err, &ae) {
		return newError(KindNetwork, err.Error())
	}
	msg := googleMessage(ae.Body)
	switch {
	case ae.Status == http.StatusTooManyRequests || strings.Contains(strings.ToUpper(ae.Body), "RESOURCE_EXHAUSTED"):
		return newError(KindQuota, msg)
	case ae.Status == http.StatusBadRequest || ae.Status == http.StatusForbidden || ae.Status == http.StatusNotFound:
		switch {
		case strings.Contains(strings.ToUpper(ae.Body), "API_KEY") || strings.Contains(strings.ToLower(msg), "api key"):
			return &Error{Kind: KindOther, Msg: "Ключ Google AI Studio не підходить — перевір його в Налаштуваннях → Підключення.", Detail: msg}
		case strings.Contains(strings.ToLower(msg), "location is not supported"):
			return &Error{Kind: KindOther, Msg: "Gemini API недоступний у твоєму регіоні — спробуй через текстовий сервіс (у вікні «Перезняти за прикладом» → «Хто дивиться відео» → «Текстовий сервіс»).", Detail: msg}
		case reVideoPrivate.MatchString(msg):
			return newError(KindPrivate, msg)
		case reVideoUnlisted.MatchString(msg):
			return newError(KindUnlisted, msg)
		case reVideoAge.MatchString(msg):
			return newError(KindAge, msg)
		case reVideoTooLong.MatchString(msg):
			return &Error{Kind: KindTooLong, Msg: "Відео задовге для одного перегляду моделі — візьми коротше відео.", Detail: msg}
		case reVideoUnavailable.MatchString(msg):
			return newError(KindUnavailable, msg)
		case ae.Status == http.StatusNotFound:
			return &Error{Kind: KindOther, Msg: "Модель Gemini не знайдена — перевір назву моделі Gemini у Налаштуваннях → Google AI Studio.", Detail: msg}
		}
	}
	return &Error{Kind: KindOther, Msg: "Gemini не зміг розібрати відео: " + clip(ae.Error(), 220), Detail: msg}
}

// googleMessage is error.message of a Google error body (else the body).
func googleMessage(body string) string {
	var one struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &one) == nil && one.Error.Message != "" {
		return one.Error.Message
	}
	var many []struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &many) == nil && len(many) > 0 && many[0].Error.Message != "" {
		return many[0].Error.Message
	}
	return clip(strings.TrimSpace(body), 300)
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

// ---- an OpenAI-compatible gateway ------------------------------------------------------------------

// Text watches through an OpenAI-compatible chat endpoint.
type Text struct {
	BaseURL, Key, Model string
	HTTP                *http.Client // nil = http.DefaultClient
	// Truth is what the watch page said: the first answer is checked by it.
	Truth Meta
}

func (t *Text) Name() string { return "text" }
func (t *Text) Label() string {
	return "текстовий сервіс · " + t.Model
}

// videoShapes are the content parts a gateway may turn into a video for its model.
var videoShapes = []struct {
	name string
	part func(url string) map[string]any
}{
	{"image_url", func(u string) map[string]any {
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}}
	}},
	{"file", func(u string) map[string]any {
		return map[string]any{"type": "file", "file": map[string]any{"file_id": u, "format": "video/mp4"}}
	}},
	{"video_url", func(u string) map[string]any {
		return map[string]any{"type": "video_url", "video_url": map[string]any{"url": u}}
	}},
}

type textCap struct {
	shape int // index into videoShapes, -1 = this endpoint cannot watch
	at    time.Time
}

var (
	textCaps  sync.Map // base|model → textCap
	capForget = 6 * time.Hour
	errCannot = &Error{Kind: KindNotSeen, Msg: "Текстовий сервіс не вміє дивитися відео з YouTube — додай ключ Google AI Studio в Налаштуваннях → Підключення."}
)

func (t *Text) capKey() string { return strings.TrimRight(t.BaseURL, "/") + "|" + t.Model }

// TextCan reports what is known about an endpoint: known=false = never
// tried in this session.
func TextCan(baseURL, model string) (can, known bool) {
	v, ok := textCaps.Load(strings.TrimRight(baseURL, "/") + "|" + model)
	if !ok {
		return false, false
	}
	c := v.(textCap)
	if c.shape < 0 && time.Since(c.at) > capForget {
		return false, false
	}
	return c.shape >= 0, true
}

// Watch implements Watcher.
func (t *Text) Watch(ctx context.Context, r Request) (string, error) {
	if strings.TrimSpace(t.BaseURL) == "" || strings.TrimSpace(t.Model) == "" {
		return "", newError(KindNoService, "no text endpoint")
	}
	if v, ok := textCaps.Load(t.capKey()); ok {
		c := v.(textCap)
		switch {
		case c.shape >= 0:
			return t.call(ctx, r, c.shape)
		case time.Since(c.at) < capForget:
			return "", errCannot
		}
	}
	for i := range videoShapes {
		reply, err := t.call(ctx, r, i)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var he *httpError
			if errors.As(err, &he) && he.status >= 400 && he.status < 500 && he.status != 401 && he.status != 403 && he.status != 429 {
				log.Printf("[reference] %s: %s parts refused (%d)", t.Model, videoShapes[i].name, he.status)
				continue // this shape is not understood
			}
			return "", err
		}
		if ok, why := verifyReply(reply, t.Truth); ok {
			textCaps.Store(t.capKey(), textCap{shape: i, at: time.Now()})
			log.Printf("[reference] %s watches YouTube through %s parts", t.Model, videoShapes[i].name)
			return reply, nil
		} else {
			log.Printf("[reference] %s with %s parts did not watch the video: %s", t.Model, videoShapes[i].name, why)
		}
	}
	textCaps.Store(t.capKey(), textCap{shape: -1, at: time.Now()})
	return "", errCannot
}

type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, clip(e.body, 200)) }

func (t *Text) call(ctx context.Context, r Request, shape int) (string, error) {
	instr := "### INSTRUCTIONS\n" + r.System + "\n\n### INPUT\n" + r.Prompt
	if r.Start > 0 || r.End > 0 {
		instr += fmt.Sprintf("\n\n(Watch only %s–%s of the video.)", Clock(r.Start), Clock(r.End))
	}
	body, err := json.Marshal(map[string]any{
		"model":       t.Model,
		"temperature": 0.2,
		"stream":      false,
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": instr},
			videoShapes[shape].part(r.URL),
		}}},
	})
	if err != nil {
		return "", err
	}
	hc := t.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, retryPause*time.Duration(attempt)); err != nil {
				return "", err
			}
		}
		actx, cancel := context.WithTimeout(ctx, watchTimeout)
		req, err := http.NewRequestWithContext(actx, http.MethodPost, strings.TrimRight(t.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			cancel()
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		if t.Key != "" {
			req.Header.Set("Authorization", "Bearer "+t.Key)
		}
		res, err := hc.Do(req)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastErr = newError(KindNetwork, err.Error())
			continue
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
		res.Body.Close()
		cancel()
		if err != nil {
			lastErr = newError(KindNetwork, err.Error())
			continue
		}
		if res.StatusCode >= 500 || res.StatusCode == 429 {
			lastErr = &httpError{res.StatusCode, string(b)}
			continue
		}
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return "", &httpError{res.StatusCode, string(b)}
		}
		var cr struct {
			Choices []struct {
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(b, &cr); err != nil || len(cr.Choices) == 0 {
			return "", &Error{Kind: KindBadReply, Msg: "Текстовий сервіс повернув незрозумілу відповідь", Detail: clip(string(b), 300)}
		}
		return contentText(cr.Choices[0].Message.Content), nil
	}
	var he *httpError
	if errors.As(lastErr, &he) && he.status == 429 {
		return "", newError(KindQuota, he.body)
	}
	return "", lastErr
}

// contentText is a message content: a string or a list of text parts.
func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var sb strings.Builder
		for _, p := range c {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					sb.WriteString(s)
				}
			}
		}
		return sb.String()
	}
	return ""
}

var reRefusal = regexp.MustCompile(`(?i)(can(not|'t)|unable to|don't have|do not have|no) (access|watch|view|see|open|play)|не можу (переглянути|подивитися|відкрити)|немає доступу`)

// verifyReply checks that an answer comes from a model that watched the
// video: its length matches the watch page (±max(6 s, 12 %)), else its
// title matches oEmbed's; without any ground truth "seen" and a length are
// required.
func verifyReply(reply string, truth Meta) (bool, string) {
	obj, err := parseObject(reply)
	if err != nil {
		if reRefusal.MatchString(reply) {
			return false, "refused: " + clip(reply, 120)
		}
		return false, "no JSON"
	}
	if seen, ok := obj["seen"].(bool); ok && !seen {
		return false, "seen=false"
	}
	dur := seconds(obj["durationSeconds"])
	title, _ := obj["title"].(string)
	switch {
	case truth.Duration > 0:
		if dur > 0 && math.Abs(dur-truth.Duration) <= math.Max(6, truth.Duration*0.12) {
			return true, ""
		}
		return false, fmt.Sprintf("length %.0f s, the video has %.0f s", dur, truth.Duration)
	case strings.TrimSpace(truth.Title) != "":
		if titleMatch(title, truth.Title) {
			return true, ""
		}
		return false, fmt.Sprintf("title %q is not %q", title, truth.Title)
	}
	if dur > 0 {
		return true, ""
	}
	return false, "no length"
}

// titleMatch: at least half of the real title's words are in the answer's.
func titleMatch(got, want string) bool {
	g, w := words(got), words(want)
	if len(w) == 0 || len(g) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, x := range g {
		have[x] = true
	}
	n := 0
	for _, x := range w {
		if have[x] {
			n++
		}
	}
	return n*2 >= len(w)
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}
