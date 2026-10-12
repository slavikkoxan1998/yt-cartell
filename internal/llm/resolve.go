package llm

// Addresses: what the user pasted becomes the API base the protocol needs,
// and when the server has no endpoint there (404/405) the usual forms are
// tried — a user's report: https://gateway.example.com answered 404 because the
// API lives under /v1.
//
//	NormalizeBase  scheme added, the endpoint of a pasted request URL dropped
//	               (/chat/completions, /models, /messages, /api/chat …), the
//	               api-version of an Azure URL kept apart
//	Candidates     as is, +/v1, /v1, /api/v1, /openai/v1 (OpenAI-compatible) ·
//	               +/v1 (Anthropic) · the root (Ollama) · /openai/v1 (Azure)
//	Check          GET the model list on each candidate (cheap, no tokens),
//	               then — when asked — a tiny chat on the one that answered
//	               (or on each, for gateways without a list); 401/403 with the
//	               default auth tries the other headers. The result names the
//	               working form, the models and every request tried.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/httpdiag"
)

// endpointSuffixes are request paths a pasted URL may end with.
var endpointSuffixes = []string{
	"/chat/completions", "/completions", "/responses", "/embeddings", "/messages", "/models",
	"/api/chat", "/api/generate", "/api/tags", "/api/version",
}

// NormalizeBase turns what the user pasted into the API base of protocol and
// returns an api-version found in an Azure URL ("" = none).
func NormalizeBase(protocol, raw string) (base, apiVersion string) {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "\"'<>«»")
	if s == "" {
		return "", ""
	}
	if !strings.Contains(s, "://") {
		host := strings.ToLower(s)
		if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "0.0.0.0") ||
			strings.HasPrefix(host, "[::1]") || strings.HasPrefix(host, "192.168.") || strings.HasPrefix(host, "10.") {
			s = "http://" + s
		} else {
			s = "https://" + s
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return strings.TrimRight(s, "/"), ""
	}
	apiVersion = u.Query().Get("api-version")
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	path := strings.TrimRight(u.Path, "/")
	for changed := true; changed; {
		changed = false
		low := strings.ToLower(path)
		for _, suf := range endpointSuffixes {
			if strings.HasSuffix(low, suf) {
				path = strings.TrimRight(path[:len(path)-len(suf)], "/")
				changed = true
				break
			}
		}
	}
	switch protocol {
	case ProtoOllama:
		for _, suf := range []string{"/api", "/v1"} {
			if strings.HasSuffix(strings.ToLower(path), suf) {
				path = strings.TrimRight(path[:len(path)-len(suf)], "/")
			}
		}
	case ProtoAzure:
		if path == "" && azureHost(u.Host) {
			path = "/openai/v1"
		}
		if strings.EqualFold(path, "/openai") {
			path = "/openai/v1"
		}
	}
	u.Path, u.RawPath = path, ""
	return strings.TrimRight(u.String(), "/"), apiVersion
}

// azureHost: an Azure OpenAI / AI Foundry resource.
func azureHost(host string) bool {
	h := strings.ToLower(host)
	return strings.HasSuffix(h, ".openai.azure.com") || strings.HasSuffix(h, ".cognitiveservices.azure.com") ||
		strings.HasSuffix(h, ".services.ai.azure.com")
}

func originOf(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	return u.Scheme + "://" + u.Host
}

// Candidates are the address forms tried for base, the given one first.
func Candidates(protocol, base string) []string {
	base = strings.TrimRight(base, "/")
	origin := originOf(base)
	low := strings.ToLower(base)
	var list []string
	switch protocol {
	case ProtoAnthropic:
		list = []string{base}
		if !strings.HasSuffix(low, "/v1") {
			list = append(list, base+"/v1")
		}
		list = append(list, origin+"/v1")
	case ProtoOllama:
		list = []string{base, origin}
	case ProtoAzure:
		list = []string{base}
		if !azureDeployment(base) {
			list = append(list, origin+"/openai/v1")
		}
	default:
		list = []string{base}
		if !strings.HasSuffix(low, "/v1") {
			list = append(list, base+"/v1")
		}
		list = append(list, origin+"/v1", origin+"/api/v1", origin+"/openai/v1")
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, b := range list {
		if k := strings.ToLower(b); !seen[k] {
			seen[k] = true
			out = append(out, b)
		}
	}
	return out
}

// ---- the session's resolved addresses ----------------------------------------------------

type resolvedBases struct {
	mu sync.Mutex
	m  map[string]string // protocol|normalised base → working base
	on func(from, to string)
}

func baseKey(conn Conn, norm string) string { return conn.Protocol + "|" + strings.ToLower(norm) }

// OnResolved registers fn, called (on the generating goroutine) when the
// configured address turned out to need another form: from is the address
// as configured, to the one that answered. The engine stores it.
func (c *Client) OnResolved(fn func(from, to string)) {
	c.bases.mu.Lock()
	c.bases.on = fn
	c.bases.mu.Unlock()
}

// baseFor is the address to call for conn: a form found earlier this
// session, else the normalised configured one.
func (c *Client) baseFor(conn Conn) string {
	norm, _ := NormalizeBase(conn.Protocol, conn.BaseURL)
	c.bases.mu.Lock()
	defer c.bases.mu.Unlock()
	if b, ok := c.bases.m[baseKey(conn, norm)]; ok {
		return b
	}
	return norm
}

func (c *Client) remember(conn Conn, to string) {
	norm, _ := NormalizeBase(conn.Protocol, conn.BaseURL)
	c.bases.mu.Lock()
	if c.bases.m == nil {
		c.bases.m = map[string]string{}
	}
	c.bases.m[baseKey(conn, norm)] = to
	on := c.bases.on
	c.bases.mu.Unlock()
	if on != nil && !strings.EqualFold(strings.TrimRight(conn.BaseURL, "/"), to) {
		on(strings.TrimRight(strings.TrimSpace(conn.BaseURL), "/"), to)
	}
}

// rediscover finds the address form the server answers on (after a 404 on
// cur): the model list on every other candidate. ok false = none answered.
func (c *Client) rediscover(ctx context.Context, conn Conn, cur string) (string, bool) {
	norm, _ := NormalizeBase(conn.Protocol, conn.BaseURL)
	for _, cand := range Candidates(conn.Protocol, norm) {
		if strings.EqualFold(cand, cur) {
			continue
		}
		u := conn.modelsURL(cand)
		if u == "" {
			continue
		}
		res, raw, err := c.send(ctx, conn, http.MethodGet, u, nil, modelsTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return "", false
			}
			continue
		}
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			if _, ok := parseModels(conn, raw); ok {
				c.remember(conn, cand)
				return cand, true
			}
		}
	}
	return "", false
}

// ---- check -----------------------------------------------------------------------------------

// Attempt is one request of a check.
type Attempt struct {
	Method, URL string // the URL without the key
	Status      int    // 0 = no answer
	Note        string // what it showed
}

// Probe is the outcome of Check.
type Probe struct {
	Conn    Conn          // the working connection: BaseURL is the form that answered, Auth what it took
	Models  []string      // the server's model list (nil = it has none)
	Reply   string        // the chat probe's answer ("" = not run)
	Latency time.Duration // of the chat probe
	Fixed   bool          // BaseURL differs from what was configured (normalised or another form)
	AuthSet bool          // Auth differs: another header worked
	Tried   []Attempt
}

// Summary is a one-line Ukrainian description of a successful probe.
func (p Probe) Summary() string {
	var parts []string
	if p.Reply != "" {
		parts = append(parts, fmt.Sprintf("Відповідь за %d мс (%s @ %s): %s", p.Latency.Milliseconds(), p.Conn.Model, p.Conn.BaseURL, clip(strings.TrimSpace(p.Reply), 40)))
	} else {
		parts = append(parts, ProtocolLabel(p.Conn.Protocol)+" @ "+p.Conn.BaseURL+" відповідає")
	}
	if p.Models != nil {
		parts = append(parts, fmt.Sprintf("моделей: %d", len(p.Models)))
	}
	return strings.Join(parts, " · ")
}

// CheckError is a failed Check: the friendly error plus every request tried.
type CheckError struct {
	Err   error
	Tried []Attempt
	key   string
}

func (e *CheckError) Error() string {
	msg := e.Err.Error()
	if len(e.Tried) > 1 {
		var b strings.Builder
		b.WriteString(msg)
		b.WriteString("\nСпробувано:")
		for _, a := range e.Tried {
			b.WriteString("\n• " + httpdiag.Line(a.Method, a.URL, a.Status, e.key))
			if a.Status == 0 {
				b.WriteString(" → немає відповіді")
			}
		}
		return b.String()
	}
	return msg
}

func (e *CheckError) Unwrap() error { return e.Err }

// Check tests conn without generating anything worth paying for: the model
// list on each address form, then (chat true) a one-word chat. The address
// form, the auth header and the models found come back in Probe; the
// working form is remembered for the session (OnResolved is not called —
// the caller decides what to store).
func (c *Client) Check(ctx context.Context, conn Conn, chat bool) (Probe, error) {
	in := conn
	norm, ver := NormalizeBase(conn.Protocol, conn.BaseURL)
	if norm == "" {
		return Probe{}, errors.New("Текстовий ендпоінт не задано (Налаштування → Текст і сценарій)")
	}
	if u, err := url.Parse(norm); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Probe{}, fmt.Errorf("Текстовий ендпоінт: некоректна адреса «%s» — потрібно http(s)://…", httpdiag.URL(norm, conn.APIKey))
	}
	conn.BaseURL = norm
	if ver != "" && conn.APIVersion == "" {
		conn.APIVersion = ver
	}
	p := Probe{Conn: conn}
	note := func(method, u string, status int, n string) {
		p.Tried = append(p.Tried, Attempt{Method: method, URL: httpdiag.URL(u, conn.APIKey), Status: status, Note: n})
	}
	fail := func(err error) (Probe, error) {
		return p, &CheckError{Err: err, Tried: p.Tried, key: conn.APIKey}
	}

	// 1. The model list on each form (no tokens spent).
	found := ""
	var authErr, firstErr error
	cands := Candidates(conn.Protocol, norm)
	for _, cand := range cands {
		u := conn.modelsURL(cand)
		if u == "" {
			continue
		}
		res, raw, err := c.send(ctx, conn, http.MethodGet, u, nil, modelsTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return p, ctx.Err()
			}
			note(http.MethodGet, u, 0, "")
			var ne *netError
			if errors.As(err, &ne) && !ne.retry {
				return fail(err) // the host is down: no other form helps
			}
			firstErr = firstErrOf(firstErr, err)
			continue
		}
		note(http.MethodGet, u, res.StatusCode, "")
		switch st := res.StatusCode; {
		case st >= 200 && st < 300:
			if models, ok := parseModels(conn, raw); ok {
				found, p.Models = cand, models
			}
		case st == 401 || st == 403:
			found = cand
			if alt, ok := c.tryAuth(ctx, conn, u); ok {
				conn.Auth, p.AuthSet = alt, true
				if res2, raw2, err2 := c.send(ctx, conn, http.MethodGet, u, nil, modelsTimeout); err2 == nil && res2.StatusCode < 300 {
					p.Models, _ = parseModels(conn, raw2)
				}
			} else {
				he := &HTTPError{Msg: fmt.Sprintf("Текстовий ендпоінт відхилив ключ (%d). Перевір API-ключ у Налаштуваннях.", st),
					Method: http.MethodGet, URL: u, Status: st, Body: raw, Hint: authHint(conn), key: conn.APIKey}
				if strings.TrimSpace(conn.APIKey) == "" {
					he.Msg = fmt.Sprintf("Текстовий ендпоінт вимагає ключ (%d) — встав API-ключ", st)
				}
				authErr = he
			}
		default:
			firstErr = firstErrOf(firstErr, &HTTPError{Msg: fmt.Sprintf("Текстовий ендпоінт відповів помилкою %d", st),
				Method: http.MethodGet, URL: u, Status: st, Body: raw, Hint: pathHint(conn, u), key: conn.APIKey})
		}
		if found != "" {
			break
		}
	}
	if authErr != nil {
		p.Conn.BaseURL = found
		p.Fixed = !strings.EqualFold(found, strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"))
		return fail(authErr)
	}
	if found != "" {
		conn.BaseURL = found
		p.Conn = conn
	}
	if !chat {
		if found == "" {
			if firstErr == nil {
				firstErr = errors.New("Текстовий ендпоінт не віддав список моделей")
			}
			return fail(firstErr)
		}
		p.Fixed = !strings.EqualFold(found, strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"))
		c.rememberQuiet(in, found)
		return p, nil
	}

	// 2. A one-word chat — on the form that listed models, or on each.
	if strings.TrimSpace(conn.Model) == "" {
		if found != "" {
			p.Fixed = !strings.EqualFold(found, strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"))
			return fail(errors.New("Сервер відповідає, але модель не вибрано — вибери її зі списку або впиши назву"))
		}
		return fail(errors.New("Модель не вказано — впиши назву моделі"))
	}
	chatCands := cands
	if found != "" {
		chatCands = []string{found}
	}
	r := Request{Prompt: "Reply with exactly: OK", Temperature: 0, Timeout: 60 * time.Second}
	var lastErr error
	for _, cand := range chatCands {
		cc := conn
		cc.BaseURL = cand
		t0 := time.Now()
		temp := temperatureFor(cc.Model, r)
		maxTok := anthropicMaxTokens
		var reply string
		var err error
		for try := 0; try < 3; try++ {
			body, berr := requestBody(cc, r, false, temp, maxTok)
			if berr != nil {
				return fail(berr)
			}
			reply, _, err = c.remoteOnce(ctx, cc, cand, body, r.Timeout, false)
			var tr *tempRejected
			var mr *maxTokensRejected
			switch {
			case errors.As(err, &tr) && temp != nil:
				temp = nil
				continue
			case errors.As(err, &mr) && maxTok != anthropicMaxTokensSmall:
				maxTok = anthropicMaxTokensSmall
				continue
			}
			break
		}
		u := cc.chatURL(cand)
		if err == nil {
			note(http.MethodPost, u, 200, "")
			p.Conn, p.Reply, p.Latency = cc, reply, time.Since(t0)
			p.Fixed = !strings.EqualFold(cand, strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"))
			c.rememberQuiet(in, cand)
			return p, nil
		}
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
		var he *HTTPError
		st := 0
		if errors.As(err, &he) {
			st = he.Status
		}
		note(http.MethodPost, u, st, "")
		lastErr = err
		if he != nil && he.pathMissing() {
			continue
		}
		// The address answers; something else is wrong (model, key, balance).
		p.Conn = cc
		p.Fixed = !strings.EqualFold(cand, strings.TrimRight(strings.TrimSpace(in.BaseURL), "/"))
		return fail(err)
	}
	if lastErr == nil {
		lastErr = firstErr
	}
	if lastErr == nil {
		lastErr = errors.New("Текстовий ендпоінт не відповів")
	}
	if len(chatCands) > 1 {
		lastErr = fmt.Errorf("Жодна форма адреси не підійшла (пробували з /v1, /api/v1, /openai/v1): %w", lastErr)
	}
	return fail(lastErr)
}

// rememberQuiet stores a working form for the session without OnResolved.
func (c *Client) rememberQuiet(conn Conn, to string) {
	norm, _ := NormalizeBase(conn.Protocol, conn.BaseURL)
	c.bases.mu.Lock()
	if c.bases.m == nil {
		c.bases.m = map[string]string{}
	}
	c.bases.m[baseKey(conn, norm)] = to
	c.bases.mu.Unlock()
}

func firstErrOf(cur, next error) error {
	if cur != nil {
		return cur
	}
	return next
}

// tryAuth tries the other auth headers on u after a 401/403 with the
// default one (only when the user left «Авторизація» on auto).
func (c *Client) tryAuth(ctx context.Context, conn Conn, u string) (string, bool) {
	if conn.Auth != "" || strings.TrimSpace(conn.APIKey) == "" {
		return "", false
	}
	def := conn.AuthMode()
	for _, mode := range []string{"bearer", "x-api-key", "api-key"} {
		if mode == def {
			continue
		}
		cc := conn
		cc.Auth = mode
		res, _, err := c.send(ctx, cc, http.MethodGet, u, nil, modelsTimeout)
		if err == nil && res.StatusCode >= 200 && res.StatusCode < 300 {
			return mode, true
		}
	}
	return "", false
}

// ---- models -------------------------------------------------------------------------------

// parseModels reads a model list: {data:[{id}]} (OpenAI, Anthropic, Azure),
// a bare array (Together), {models:[{name|model|id}]} (Ollama, Gemini).
// ok false = not a model list (e.g. an HTML page).
func parseModels(conn Conn, raw []byte) ([]string, bool) {
	type entry struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	var list []entry
	var j struct {
		Data   *[]entry `json:"data"`
		Models *[]entry `json:"models"`
	}
	if err := json.Unmarshal(raw, &j); err == nil && (j.Data != nil || j.Models != nil) {
		if j.Data != nil {
			list = *j.Data
		}
		if len(list) == 0 && j.Models != nil {
			list = *j.Models
		}
	} else if err := json.Unmarshal(raw, &list); err != nil {
		var names []string // a bare list of ids
		if json.Unmarshal(raw, &names) != nil {
			return nil, false
		}
		for _, n := range names {
			list = append(list, entry{ID: n})
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, m := range list {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			id = strings.TrimSpace(m.Model)
		}
		if id == "" {
			id = strings.TrimPrefix(strings.TrimSpace(m.Name), "models/")
		}
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, true
}

// Models lists the model ids the configured text endpoint offers (Anthropic
// pages through has_more; an Azure deployment URL has only its deployment).
func (c *Client) Models(ctx context.Context) ([]string, error) {
	cfg := c.cfg()
	if cfg.Text.Provider == "google" {
		return nil, errors.New("Список моделей Google — у картці «Google AI Studio»")
	}
	return c.ModelsOf(ctx, ConnOf(cfg.Text))
}

// ModelsOf lists the models of conn (another address form is tried after a
// 404, like generation does).
func (c *Client) ModelsOf(ctx context.Context, conn Conn) ([]string, error) {
	if strings.TrimSpace(conn.BaseURL) == "" {
		return nil, errors.New("Текстовий ендпоінт не задано (Налаштування → Текст і сценарій)")
	}
	base := c.baseFor(conn)
	if conn.Protocol == ProtoAzure && azureDeployment(base) {
		dep := base[strings.LastIndex(base, "/")+1:]
		return []string{dep}, nil
	}
	list, err := c.modelsAt(ctx, conn, base)
	var he *HTTPError
	if errors.As(err, &he) && he.pathMissing() {
		if nb, ok := c.rediscover(ctx, conn, base); ok {
			return c.modelsAt(ctx, conn, nb)
		}
	}
	return list, err
}

func (c *Client) modelsAt(ctx context.Context, conn Conn, base string) ([]string, error) {
	u := conn.modelsURL(base)
	var all []string
	for page := 0; page < 10; page++ {
		res, raw, err := c.send(ctx, conn, http.MethodGet, u, nil, modelsTimeout)
		if err != nil {
			return nil, err
		}
		he := &HTTPError{Method: http.MethodGet, URL: u, Status: res.StatusCode, Body: raw, key: conn.APIKey}
		switch st := res.StatusCode; {
		case st == 401 || st == 403:
			he.Msg, he.Hint = fmt.Sprintf("Ендпоінт відхилив ключ (%d). Перевір API-ключ у Налаштуваннях.", st), authHint(conn)
			return nil, he
		case st < 200 || st > 299:
			he.Msg = fmt.Sprintf("Список моделей недоступний (%d)", st)
			if st == 404 || st == 405 {
				he.Hint = pathHint(conn, u)
			}
			return nil, he
		}
		list, ok := parseModels(conn, raw)
		if !ok {
			he.Msg, he.Hint = "Ендпоінт повернув не список моделей", "схоже, адреса веде не на API — перевір її"
			return nil, he
		}
		all = append(all, list...)
		if conn.Protocol != ProtoAnthropic {
			break
		}
		var pg struct {
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if json.Unmarshal(raw, &pg) != nil || !pg.HasMore || pg.LastID == "" {
			break
		}
		u = conn.modelsURL(base) + "&after_id=" + url.QueryEscape(pg.LastID)
	}
	if conn.Protocol == ProtoOllama {
		sort.Strings(all)
	}
	return all, nil
}
