// Package tts is the provider-agnostic text-to-speech used by the render stage
// and the voice pickers. Config comes from the Settings screen (cfg.TTS); the
// provider presets are listed by Providers():
//
//   - "elevenlabs" → POST /v1/text-to-speech/{voice}          (xi-api-key)   eleven.go
//   - "cartesia"   → POST /tts/bytes (Sonic 3.6)               (Bearer)       cartesia.go
//   - "openai"     → POST {base}/audio/speech                  (Bearer)       openai.go
//   - "google"     → POST texttospeech.googleapis.com/v1/text:synthesize (x-goog-api-key) google.go
//   - "azure"      → POST {region}.tts.speech.microsoft.com/cognitiveservices/v1 (SSML) azure.go
//   - "minimax"    → POST /v1/t2a_v2                           (Bearer)       minimax.go
//   - "fish"       → POST /v1/tts                              (Bearer)       fish.go
//   - "custom"     → POST {url} {text,voice,model}             (Bearer, optional); accepts raw
//     audio, or JSON {audio|audioBase64|data|audioUrl|url}                    openai.go
//
// Every call goes through the key pool (keys.go): TTS.APIKey + TTS.Keys rotate
// per request; exhausted keys rest until the 1st of next month, failing keys
// are skipped. Every provider returns MP3. The settings are kept per provider
// (config.TTS.Providers); the client reads the active provider's view, so a
// switch never lends one provider's keys or voice to another — and a voice id
// that surely belongs to another provider is refused before any request.
package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ytcartell/internal/config"
	"ytcartell/internal/httpdiag"
)

// Voice is one voice of the configured account.
type Voice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Category   string `json:"category"`         // premade | professional | cloned | generated | library | …
	Gender     string `json:"gender,omitempty"` // male | female | neutral (lower case)
	Age        string `json:"age,omitempty"`
	Accent     string `json:"accent,omitempty"` // English word: "american", "ukrainian"…
	PreviewURL string `json:"previewUrl,omitempty"`
	// Language is the locale the voice speaks ("uk-UA", "en"); "" = any
	// language (multilingual models, Chirp 3 HD personas).
	Language string `json:"language,omitempty"`
	// Usable is false when the account tier cannot use this voice through the API.
	Usable bool `json:"usable"`
	// Locked says why the voice is not usable ("потрібен платний тариф
	// ElevenLabs"; "" = no reason known).
	Locked string `json:"locked,omitempty"`
	// Note is a short notice about a usable voice ("доступний до 31 грудня
	// 2026" — ElevenLabs retires its default voices).
	Note string `json:"note,omitempty"`
}

// VoiceList is the voice catalogue of the configured account.
type VoiceList struct {
	Provider  string  `json:"provider"`
	Tier      string  `json:"tier"`
	CharsLeft int     `json:"charsLeft"` // -1 = unknown
	Voices    []Voice `json:"voices"`
}

// Endpoints and tunables (unexported vars so tests can point them at mocks).
var (
	elevenBase   = "https://api.elevenlabs.io" // /v1/… and /v2/…
	openaiBase   = "https://api.openai.com/v1"
	cartesiaBase = "https://api.cartesia.ai"
	googleBase   = "https://texttospeech.googleapis.com"
	minimaxBase  = "https://api.minimax.io"
	fishBase     = "https://api.fish.audio"
	// azureEndpoint is the regional host of an Azure Speech resource.
	azureEndpoint = func(region string) string { return "https://" + region + ".tts.speech.microsoft.com" }

	// Rate limits (e.g. ElevenLabs free tier: 2 concurrent requests) → back off and retry.
	retryBase = 1500 * time.Millisecond

	synthTimeout = 3 * time.Minute
	listTimeout  = 30 * time.Second
	testTimeout  = 20 * time.Second
	voicesTTL    = 10 * time.Minute
	voicePages   = 5 // ×100 voices

	// Key checks: at most probeParallel at once, started probeGap apart.
	probeParallel = 3
	probeGap      = 120 * time.Millisecond
)

const maxAudio = 256 << 20

// Client synthesizes speech with the provider chosen in Settings. Settings
// are read on every call.
type Client struct {
	get  func() config.Config
	http *http.Client
	pool *pool

	mu     sync.Mutex
	cache  *voiceCache
	models *modelCache // ElevenLabs GET /v1/models (see models.go)
	// fetchMu serialises /v1/models requests (one fetch fills the cache for
	// every concurrent voice).
	fetchMu sync.Mutex
}

type voiceCache struct {
	at   time.Time
	key  string
	list VoiceList
}

// New creates a client reading its settings from get. Key states live in
// memory until SetStateFile.
func New(get func() config.Config) *Client {
	return &Client{get: get, http: &http.Client{}, pool: newPool()}
}

type settings struct {
	provider string
	info     ProviderInfo
	keys     []poolKey // rotation list (keys.go)
	key      string    // the first key ("" = none)
	base     string
	voice    string
	model    string
}

func (c *Client) settings() settings {
	cfg := c.get()
	t := cfg.TTS
	s := settings{
		provider: normProvider(t.Provider),
		base:     strings.TrimRight(strings.TrimSpace(t.BaseURL), "/"),
		voice:    strings.TrimSpace(t.Voice),
		model:    strings.TrimSpace(t.Model),
	}
	s.info, _ = ProviderByID(s.provider)
	s.keys = poolKeys(s.provider, t, cfg.Google.Keys)
	if len(s.keys) > 0 {
		s.key = s.keys[0].key
	}
	return s
}

// SynthOptions tune one synthesis; zero values keep the provider defaults.
type SynthOptions struct {
	// Model overrides the TTS model. "" = the model from Settings, else the
	// provider default ((ElevenLabs) the best model for LanguageCode, see ModelFor).
	Model string
	// LanguageCode is the language of the text: ISO 639-1 ("uk", "en"; ISO 639-3
	// when there is no two-letter code, e.g. "fil"). "" = detect: providers that
	// need a language get the one the writing system shows.
	LanguageCode string
	// Stability and Similarity (0–1; ElevenLabs) and Speed (1 = normal, clamped
	// to each provider's range); 0 = default.
	Stability, Similarity, Speed float64
}

// Synthesize voices text and returns mp3 bytes. voiceID "" = the default
// voice from Settings.
func (c *Client) Synthesize(ctx context.Context, text, voiceID string) ([]byte, error) {
	return c.SynthesizeWith(ctx, text, voiceID, SynthOptions{})
}

// SynthesizeWith is Synthesize with a model, language and voice settings.
func (c *Client) SynthesizeWith(ctx context.Context, text, voiceID string, opt SynthOptions) ([]byte, error) {
	s := c.settings()
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("Немає тексту для озвучки")
	}
	voice := firstNonEmpty(voiceID, s.voice)
	if err := foreignVoice(s, voice); err != nil {
		return nil, err
	}
	switch s.provider {
	case "piper":
		return c.piper(ctx, s, text, voice, opt)
	case "openai":
		return c.openAI(ctx, s, text, voice, opt)
	case "custom":
		return c.custom(ctx, s, text, voice, opt)
	case "cartesia":
		return c.cartesia(ctx, s, text, voice, opt)
	case "google":
		return c.google(ctx, s, text, voice, opt)
	case "azure":
		return c.azure(ctx, s, text, voice, opt)
	case "minimax":
		return c.minimax(ctx, s, text, voice, opt)
	case "fish":
		return c.fish(ctx, s, text, voice, opt)
	case "lumean":
		return c.lumean(ctx, s, text, voice, opt)
	default:
		return c.eleven(ctx, s, text, voice, opt)
	}
}

// Voices lists the voices of the configured account (cached 10 min per
// provider and key; force bypasses the cache). OpenAI's built-in voices need
// no request; an own server ("custom") has no list.
func (c *Client) Voices(ctx context.Context, force bool) (VoiceList, error) {
	s := c.settings()
	switch s.provider {
	case "piper":
		return c.piperVoices(ctx, s)
	case "custom":
		return VoiceList{Provider: s.provider, CharsLeft: -1, Voices: []Voice{}}, nil
	case "openai":
		if !officialOpenAI(s) {
			return VoiceList{Provider: s.provider, CharsLeft: -1, Voices: []Voice{}}, nil
		}
		return VoiceList{Provider: s.provider, CharsLeft: -1, Voices: openAIVoices(firstNonEmpty(s.model, openAIModel(s)))}, nil
	}
	if len(s.keys) == 0 {
		return VoiceList{}, errNoKey(s)
	}
	ck := strings.Join([]string{s.provider, c.pool.usable(s).id, s.base, s.model}, "|")
	c.mu.Lock()
	if vc := c.cache; !force && vc != nil && vc.key == ck && time.Since(vc.at) < voicesTTL {
		out := copyList(vc.list)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	var list VoiceList
	var err error
	switch s.provider {
	case "cartesia":
		list, err = c.cartesiaVoices(ctx, s)
	case "google":
		list, err = c.googleVoices(ctx, s)
	case "azure":
		list, err = c.azureVoices(ctx, s)
	case "minimax":
		list, err = c.minimaxVoices(ctx, s)
	case "fish":
		list, err = c.fishVoices(ctx, s)
	case "lumean":
		list, err = c.lumeanVoices(ctx, s)
	default:
		list, err = c.elevenVoices(ctx, s)
	}
	if err != nil {
		return VoiceList{}, err
	}
	list.Provider = s.provider
	if list.Voices == nil {
		list.Voices = []Voice{}
	}
	c.mu.Lock()
	c.cache = &voiceCache{at: time.Now(), key: ck, list: copyList(list)}
	c.mu.Unlock()
	return list, nil
}

// cachedVoices is the voice list when it is already cached (no request).
func (c *Client) cachedVoices(s settings) []Voice {
	ck := strings.Join([]string{s.provider, c.pool.usable(s).id, s.base, s.model}, "|")
	c.mu.Lock()
	defer c.mu.Unlock()
	if vc := c.cache; vc != nil && vc.key == ck {
		return append([]Voice(nil), vc.list.Voices...)
	}
	return nil
}

// ForeignVoice names the provider a voice id surely belongs to when it is
// not the current provider's ("" = it may be the current provider's own):
// a project keeps the ids it was planned with after the provider changes.
func (c *Client) ForeignVoice(voiceID string) string { return ForeignVoiceOf(c.get().TTS, voiceID) }

// ForeignVoiceOf is ForeignVoice for the settings t (no client needed: the
// voice pickers).
func ForeignVoiceOf(t config.TTS, voiceID string) string {
	s := settings{provider: normProvider(t.Provider), base: strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")}
	return config.ForeignTTSVoice(s.provider, voiceID, s.provider == "openai" && !officialOpenAI(s))
}

// foreignVoice refuses, before any request, a voice of another provider.
func foreignVoice(s settings, voice string) error {
	owner := config.ForeignTTSVoice(s.provider, voice, s.provider == "openai" && !officialOpenAI(s))
	if owner == "" {
		return nil
	}
	other, _ := ProviderByID(owner)
	return fatal(fmt.Sprintf("Голос сцени — з іншого сервісу (%s), а озвучка зараз іде через %s. Вибери голос %s у проєкті або в Налаштуваннях → Озвучка",
		firstNonEmpty(other.Label, owner), s.info.Label, s.info.Label))
}

// sortVoices: usable first, then own voices, then voices of lead (a language
// code, "" = none), then by name.
func sortVoices(voices []Voice, lead string) {
	own := func(v Voice) bool { return v.Category == "cloned" || v.Category == "generated" }
	leads := func(v Voice) bool { return lead != "" && langOfLocale(v.Language) == lead }
	sort.SliceStable(voices, func(i, j int) bool {
		a, b := voices[i], voices[j]
		if a.Usable != b.Usable {
			return a.Usable
		}
		if own(a) != own(b) {
			return own(a)
		}
		if leads(a) != leads(b) {
			return leads(a)
		}
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return la < lb
		}
		return a.ID < b.ID
	})
}

// ---- service check ----------------------------------------------------------------------

// Test checks every configured key with a cheap authenticated request (an
// account, balance or voice-list read — never a paid synthesis), updates the
// key states (an invalid key → error, a working one clears its error, a
// provider-reported spent quota → exhausted) and sums it up in one line.
func (c *Client) Test(ctx context.Context) (bool, string) {
	s := c.settings()
	label := s.info.Label
	if s.provider == "custom" && s.base == "" && c.ttsSpec() == "" {
		return false, "Для власного сервера вкажи адресу (Налаштування → Озвучка)"
	}
	if s.provider == "azure" && s.base == "" {
		return false, "Azure: вкажи регіон ресурсу Speech у полі адреси (напр. westeurope)"
	}
	if len(s.keys) == 0 && !s.info.KeyOptional {
		return false, errNoKey(s).Error()
	}
	// Many keys (a bulk add) get more time: they are checked a few at a time.
	ctx, cancel := context.WithTimeout(ctx, min(testTimeout+time.Duration(len(s.keys))*time.Second, 2*time.Minute))
	defer cancel()
	if len(s.keys) == 0 { // own server without a key
		r := c.probe(ctx, s, poolKey{})
		if r.err != nil {
			return false, r.err.Error()
		}
		return true, r.info
	}

	// Politely: at most probeParallel checks at once, started probeGap apart
	// (a pasted list of 30 keys must not look like an attack).
	results := make([]probeResult, len(s.keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, probeParallel)
	for i, k := range s.keys {
		if i > 0 && sleep(ctx, probeGap) != nil {
			results[i] = probeResult{used: -1, err: &apiErr{kind: failTransient, msg: "Перевірку скасовано"}}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = c.probe(ctx, s, k)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return false, "Перевірку скасовано"
	}
	for i, k := range s.keys {
		r := results[i]
		if r.err != nil {
			r.err = withKeyHint(s, k.key, r.err)
			results[i] = r
		}
		switch {
		case r.err == nil:
			c.pool.mark(s, k, nil, r.info)
		case r.err.kind == failAuth || r.err.kind == failQuota:
			c.pool.mark(s, k, r.err, "")
		}
		c.pool.setPlan(s, k, r.plan)
		if r.quota != nil || r.used >= 0 || r.limit > 0 {
			c.pool.report(s, k, r.used, r.limit, r.quota, r.quotaMsg)
		}
	}
	return c.summary(s, label, results)
}

// probeResult is one key's check.
type probeResult struct {
	info     string  // "тариф creator · залишилось 81 234 символи"
	err      *apiErr // nil = the key works
	used     int     // provider-reported characters this period (-1 = unknown)
	limit    int
	quota    *bool // provider says the quota is spent (true) / available (false)
	quotaMsg string
	plan     string // the account's plan as the provider names it ("free"; "" = unknown)
	detail   string // the failed request (method, URL, status, reply — httpdiag), "" = none
}

func (c *Client) probe(ctx context.Context, s settings, k poolKey) probeResult {
	ctx, tr := httpdiag.WithTrace(ctx)
	r := c.probeOnce(ctx, s, k)
	if r.err != nil {
		r.detail = tr.Detail(k.key)
	}
	return r
}

func (c *Client) probeOnce(ctx context.Context, s settings, k poolKey) probeResult {
	switch s.provider {
	case "piper":
		return c.piperProbe(ctx, s)
	case "cartesia":
		return c.cartesiaProbe(ctx, s, k.key)
	case "google":
		return c.googleProbe(ctx, s, k.key)
	case "azure":
		return c.azureProbe(ctx, s, k.key)
	case "minimax":
		return c.minimaxProbe(ctx, s, k.key)
	case "fish":
		return c.fishProbe(ctx, s, k.key)
	case "openai":
		return c.openAIProbe(ctx, s, k.key)
	case "custom":
		return c.customProbe(ctx, s, k.key)
	case "lumean":
		return c.lumeanProbe(ctx, s, k.key)
	}
	return c.elevenProbe(ctx, s, k.key)
}

// summary is the one-line result of Test for the Settings screen.
func (c *Client) summary(s settings, label string, results []probeResult) (bool, string) {
	st := c.KeyStatus()
	working := 0
	var firstErr, firstInfo, firstDetail string
	for i, r := range results {
		state := KeyOK
		if i < len(st) {
			state = st[i].State
		}
		if r.err == nil && state != KeyExhausted {
			working++
			firstInfo = firstNonEmpty(firstInfo, r.info)
		} else if r.err != nil {
			if firstErr == "" {
				firstErr, firstDetail = r.err.msg, r.detail
			}
		}
	}
	exhausted, until := 0, time.Time{}
	for _, k := range st {
		if k.State == KeyExhausted {
			exhausted++
			if until.IsZero() || k.Until.Before(until) {
				until = k.Until
			}
		}
	}
	n := len(results)
	if n == 1 {
		switch {
		case working == 1:
			return true, label + ": ключ працює" + prefixed(" · ", firstInfo)
		case exhausted == 1:
			return false, fmt.Sprintf("%s: ключ дійсний, але вичерпав ліміт — знову спрацює %s", label, dayMonth(until))
		}
		return false, withDetail(firstNonEmpty(firstErr, label+": ключ не працює"), firstDetail)
	}
	msg := fmt.Sprintf("%s: %s %d з %d %s", label, plural(working, "працює", "працюють", "працюють"), working, n, plural(n, "ключа", "ключів", "ключів"))
	if exhausted > 0 {
		msg += fmt.Sprintf(" · %s %s ліміт до %s", keysCount(exhausted), plural(exhausted, "вичерпав", "вичерпали", "вичерпали"), dayMonth(until))
	}
	if bad := n - working - exhausted; bad > 0 && firstErr != "" {
		msg += fmt.Sprintf(" · %s з помилкою: %s", keysCount(bad), firstErr)
	}
	if working > 0 && firstInfo != "" {
		msg += " · " + firstInfo
	}
	if working == 0 {
		msg = withDetail(msg, firstDetail)
	}
	return working > 0, msg
}

// withDetail puts the failed request under a check's message.
func withDetail(msg, detail string) string {
	if detail == "" || strings.Contains(msg, "Запит: ") {
		return msg
	}
	return msg + "\n" + detail
}

func prefixed(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

// withKeyHint adds to a rejected key's message whose key it looks like
// («схоже, це ключ ElevenLabs — ключі Cartesia починаються з sk_car_»).
func withKeyHint(s settings, key string, ae *apiErr) *apiErr {
	if ae == nil || ae.kind != failAuth || strings.Contains(ae.msg, "схоже, це ключ") {
		return ae
	}
	hint := ""
	if owner := config.TTSKeyOwner(key); owner != "" && owner != s.provider && s.provider != "custom" {
		p, _ := ProviderByID(owner)
		hint = "схоже, це ключ " + firstNonEmpty(p.Label, owner)
	}
	if s.provider == "cartesia" && !strings.HasPrefix(strings.TrimSpace(key), "sk_car_") {
		hint = firstNonEmpty(hint, "схоже, це не ключ Cartesia") + " — ключі Cartesia починаються з sk_car_"
	}
	if hint == "" {
		return ae
	}
	cp := *ae
	cp.msg = ae.msg + " (" + hint + ")"
	return &cp
}

// ---- helpers ----------------------------------------------------------------------------

var (
	reQuota      = regexp.MustCompile(`(?i)quota_exceeded|insufficient_credits`)
	reLibrary    = regexp.MustCompile(`(?i)library voices`)
	reDataPrefix = regexp.MustCompile(`^data:[^,]*;base64,`)
	reLanguage   = regexp.MustCompile(`(?i)language`)
)

// orDefault is v, or def when v is unset (≤ 0), clamped to [lo, hi].
func orDefault(v, def, lo, hi float64) float64 {
	if v <= 0 || v != v { // unset or NaN
		v = def
	}
	return max(lo, min(hi, v))
}

type reply struct {
	status int
	ctype  string
	body   []byte
	retry  time.Duration // Retry-After
}

func (r reply) ok() bool { return r.status >= 200 && r.status < 300 }

// do performs one request and reads the whole body under its own timeout.
func (c *Client) do(ctx context.Context, method, u string, hdr map[string]string, body []byte, timeout time.Duration, limit int64) (reply, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(rctx, method, u, rd)
	if err != nil {
		return reply{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err) // «Перевірити» shows the exact request
		return reply{}, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		httpdiag.Record(ctx, method, u, 0, nil, err)
		return reply{}, err
	}
	httpdiag.Record(ctx, method, u, res.StatusCode, b, nil)
	if int64(len(b)) > limit {
		return reply{}, fmt.Errorf("відповідь більша за %d МБ", limit>>20)
	}
	return reply{status: res.StatusCode, ctype: res.Header.Get("Content-Type"), body: b, retry: retryAfter(res.Header.Get("Retry-After"))}, nil
}

// retryAfter parses a Retry-After header (seconds or an HTTP date).
func retryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// netErr turns a transport failure into a friendly message (cancellation
// passes through): no connection is worth another try, a server that did not
// answer in minutes is not.
func netErr(ctx context.Context, who string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ue *url.Error
	host := ""
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			host = u.Host
		}
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fatal(fmt.Sprintf("%s: сервер %s не відповів вчасно — спробуй ще раз", who, host))
	}
	return &apiErr{kind: failTransient, msg: fmt.Sprintf("%s: немає з'єднання з %s — перевір інтернет або адресу в Налаштуваннях (%s)", who, host, clip(err.Error(), 160))}
}

// httpErr is the generic classification of a failed reply (providers refine it).
func httpErr(who string, r reply, detail string) *apiErr {
	detail = firstNonEmpty(detail, clip(string(r.body), 200))
	switch {
	case r.status == http.StatusUnauthorized:
		return &apiErr{kind: failAuth, msg: who + ": невірний API-ключ (401)"}
	case r.status == http.StatusForbidden:
		return &apiErr{kind: failAuth, msg: fmt.Sprintf("%s: ключ не має доступу (403): %s", who, detail)}
	case r.status == http.StatusPaymentRequired:
		return &apiErr{kind: failQuota, msg: who + ": закінчились кошти або ліміт на тарифі"}
	case r.status == http.StatusTooManyRequests:
		return &apiErr{kind: failRate, retry: r.retry, msg: who + ": забагато запитів — зачекаємо й повторимо"}
	case r.status >= 500:
		return &apiErr{kind: failTransient, msg: fmt.Sprintf("%s %d: %s", who, r.status, detail)}
	}
	return fatal(fmt.Sprintf("%s %d: %s", who, r.status, detail))
}

// audio rejects empty bodies and JSON/HTML error pages returned with 200.
func audio(r reply, who string) ([]byte, error) {
	if len(r.body) == 0 {
		return nil, fmt.Errorf("%s: порожня відповідь замість аудіо", who)
	}
	if mt, _, err := mime.ParseMediaType(r.ctype); err == nil &&
		(strings.Contains(mt, "json") || mt == "text/html") {
		return nil, fmt.Errorf("%s: сервер повернув %s замість аудіо: %s", who, mt, clip(string(r.body), 200))
	}
	return r.body, nil
}

func bearer(key string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + key}
}

// decodeB64 is as lenient as Node's Buffer.from(s, "base64").
func decodeB64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	var lastErr error
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		b, err := enc.DecodeString(s)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func copyList(l VoiceList) VoiceList {
	l.Voices = append([]Voice{}, l.Voices...)
	return l
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func runes(s string) int { return utf8.RuneCountInString(s) }

// gender normalises provider gender words to male | female | neutral.
func gender(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "male", "masculine", "man", "m":
		return "male"
	case "female", "feminine", "woman", "f":
		return "female"
	case "neutral", "gender_neutral", "non-binary", "nonbinary":
		return "neutral"
	}
	return ""
}
