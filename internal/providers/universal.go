package providers

// Universal protocol (preset "universal", config.CustomAPI.ImageSpec /
// VideoSpec): one JSON description — a Spec — makes any image or video
// generation API usable without new code. Two shapes cover nearly all of
// them:
//
//	synchronous  POST → the file: a URL, a data: URI, base64 or the raw bytes
//	async jobs   POST → an id; GET the status until a field says done; the file
//	             is in the last status (or in one more call: "fetch")
//
// Spec reference (only submit is required):
//
//	{
//	  "name":   "My API",                    shown in messages ("" = the API's host)
//	  "auth":   "bearer",                    bearer (the default when a key is set) | basic | none |
//	                                         header:X-API-Key | header:Authorization:Key | query:key
//	  "submit": {"method": "POST", "path": "/v1/jobs", "headers": {…}, "body": {…template…},
//	             "encoding": "json",         json (default) | multipart | form
//	             "timeout": 600},            seconds (default: 3 min async, 8/25 min sync image/video)
//	  "id":     "id",                        JSON path of the job id in the submit reply; none = synchronous
//	  "poll":   {"method": "GET", "path": "/v1/jobs/{id}", "every": 10},
//	  "state":  "status",                    JSON path of the job state
//	  "done":   ["succeeded"], "failed": ["failed", "canceled"], "queued": ["queued"],   any case
//	  "progress": "progress",                0–1 or 0–100
//	  "fetch":  {"path": "{{status.response_url}}"},    one more call that returns the result (optional)
//	  "result": ["output.url", "output.0"],  where the file is (first that exists): a URL, a data: URI,
//	                                         base64 or an array (→ its first item); none = the reply
//	                                         (submit when synchronous, fetch) is the file itself
//	  "error":  "error.message",             the failure text
//	  "cancel": {"method": "POST", "path": "/v1/jobs/{id}/cancel"},   when the studio stops waiting
//	  "test":   {"method": "GET", "path": "/v1/account"},             «Перевірити» (never generates)
//	  "caps":   {"lengths": [4, 6, 8, 10], "maxRefs": 7, "aspects": ["16:9", "9:16"],
//	             "startFrame": true, "size": 1280}    size = the long side for {{width}}/{{height}}/{{size}}
//	}
//
// JSON paths are dotted with array indexes — data.0.url, output.images.0.url
// ("$." and "[0]" are accepted too; a name that meets an array looks into its
// first element; -1 is the last item).
//
// Placeholders of the submit call (path, headers, body): {{prompt}}
// {{model}} (the model field) {{aspect}} "16:9" {{orientation}}
// landscape|portrait|square {{seconds}} {{width}} {{height}} {{size}}
// "1280x720" {{start_image}} (a data: URI) {{start_image_b64}} (raw base64)
// {{refs}} (an array of data: URIs) {{refs_b64}} {{resolution}} "720p". A
// string that is exactly one placeholder becomes its typed value (a number,
// an array — spliced into an enclosing array; {{seconds|text}} keeps it a
// string, "8"); embedded placeholders are substituted as text; a
// placeholder without a value (no start frame, images have no seconds)
// removes its key. The later calls also know {id}
// (or {{id}}), {{submit.<path>}} (the submit reply) and {{status.<path>}}
// (the last status reply); a path that is exactly such a placeholder holding
// an absolute URL is called as is (fal's status_url / response_url).
//
// The key never appears in a spec ({{key}} is refused): "auth" adds it, and
// only to the API's own origin (the base URL's scheme and host) — results
// and URLs elsewhere are fetched without it. With refs sent the prompt gets
// the reference legend (refLegend). Safety like royaltechno.go: one
// Idempotency-Key per job (an empty "Idempotency-Key" in submit.headers
// turns it off), a submit is repeated only after 429/503 (Retry-After
// honoured) or a connection lost before any answer — never after a timeout
// or 400/401/402/403. Status checks wait out a 429's Retry-After (not a
// failure) and retry 5xx / network blips with backoff; a job the studio
// stops waiting for (blips, timeout, cancel) is cancelled when "cancel" is
// given, and its error invites a retry only if that cancel was accepted.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spec is the description of an API for the universal preset (see the
// package's universal.go header for the reference). Parse it with ParseSpec.
type Spec struct {
	Name     string            `json:"name,omitempty"`
	Auth     string            `json:"auth,omitempty"`
	Submit   *SpecCall         `json:"submit"`
	ID       string            `json:"id,omitempty"`
	Poll     *SpecCall         `json:"poll,omitempty"`
	State    string            `json:"state,omitempty"`
	Done     []string          `json:"done,omitempty"`
	Failed   []string          `json:"failed,omitempty"`
	Queued   []string          `json:"queued,omitempty"`
	Progress SpecPaths         `json:"progress,omitempty"`
	Fetch    *SpecCall         `json:"fetch,omitempty"`
	Result   SpecPaths         `json:"result,omitempty"`
	Error    SpecPaths         `json:"error,omitempty"`
	Cancel   *SpecCall         `json:"cancel,omitempty"`
	Test     *SpecCall         `json:"test,omitempty"`
	Caps     *SpecCapabilities `json:"caps,omitempty"`

	auth specAuth
	uses map[string]bool // placeholders of the submit call
	kind SpecKind        // what the API makes ("" = an image or a video; universal_audio.go)
}

// SpecCall is one HTTP call of a Spec.
type SpecCall struct {
	Method   string            `json:"method,omitempty"` // default POST (submit, cancel) or GET
	Path     string            `json:"path"`             // relative to the base URL, or absolute
	Headers  map[string]string `json:"headers,omitempty"`
	Body     any               `json:"body,omitempty"`     // a JSON template
	Encoding string            `json:"encoding,omitempty"` // json (default) | multipart | form
	Every    float64           `json:"every,omitempty"`    // poll: seconds between status checks
	Timeout  float64           `json:"timeout,omitempty"`  // seconds
}

// SpecCapabilities is what the API's model accepts ("caps").
type SpecCapabilities struct {
	Lengths    []int    `json:"lengths,omitempty"`    // clip lengths, s (video)
	MaxRefs    *int     `json:"maxRefs,omitempty"`    // references one request takes
	Aspects    []string `json:"aspects,omitempty"`    // "16:9", "9:16", "1:1" …
	StartFrame *bool    `json:"startFrame,omitempty"` // takes a start frame (video)
	Size       int      `json:"size,omitempty"`       // long side in px for {{width}}/{{height}}/{{size}}
}

// SpecPaths is one JSON path or a list of them (the first that exists wins).
type SpecPaths []string

// UnmarshalJSON accepts "a.b" or ["a.b", "c"].
func (p *SpecPaths) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*p = nil
		return nil
	}
	var one string
	if json.Unmarshal(b, &one) == nil {
		*p = nil
		if strings.TrimSpace(one) != "" {
			*p = SpecPaths{one}
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		// Reported by validate with the field's name (the decoder would lose it).
		*p = SpecPaths{badPaths}
		return nil
	}
	*p = list
	return nil
}

// badPaths marks a paths field that was neither a string nor a list.
const badPaths = "\x00"

// MarshalJSON writes a single path as a string.
func (p SpecPaths) MarshalJSON() ([]byte, error) {
	if len(p) == 1 {
		return json.Marshal(p[0])
	}
	return json.Marshal([]string(p))
}

// SpecError lists what is wrong with a spec, in Ukrainian ("field: what").
type SpecError struct{ Problems []string }

func (e *SpecError) Error() string {
	if len(e.Problems) == 1 {
		return "опис API: " + e.Problems[0]
	}
	return "опис API: " + strings.Join(e.Problems, "; ")
}

const (
	specMaxText     = 64 << 10
	specMaxProblems = 6
	specDefaultSize = 1280
	specDefaultRefs = 4
)

// specSecond is one second of "every" / "timeout" (tests shrink it).
var specSecond = time.Second

// Placeholders.
var (
	reSpecVar = regexp.MustCompile(`\{\{\s*([A-Za-z_][^{}\s]*)\s*\}\}`)
	reSpecID  = regexp.MustCompile(`\{id\}`)
	reAspect  = regexp.MustCompile(`^\d{1,2}:\d{1,2}$`)
	reHeader  = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]+$")
)

// specRequestVars are the placeholders of a generation request.
var specRequestVars = []string{"prompt", "model", "aspect", "orientation", "seconds", "width", "height", "size",
	"start_image", "start_image_b64", "refs", "refs_b64", "resolution"}

// specSecretVars would put a secret into the request text — refused.
var specSecretVars = []string{"key", "api_key", "apikey", "token", "secret", "password", "authorization"}

// SpecPlaceholders lists the placeholders of a request (for help texts).
func SpecPlaceholders() []string {
	out := make([]string, len(specRequestVars))
	for i, v := range specRequestVars {
		out[i] = "{{" + v + "}}"
	}
	return out
}

// ParseSpec reads and checks a spec. Errors are *SpecError with friendly
// Ukrainian problems ("submit.path: …").
func ParseSpec(text string) (*Spec, error) { return parseSpecKind(text, "") }

// parseSpecKind is ParseSpec for a kind of API (KindTTS / KindSTT change the
// placeholders; "" = images and video).
func parseSpecKind(text string, kind SpecKind) (*Spec, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, &SpecError{Problems: []string{"порожній — обери шаблон, налаштуй з документації або встав JSON"}}
	}
	if len(text) > specMaxText {
		return nil, &SpecError{Problems: []string{fmt.Sprintf("завеликий (%d КБ, можна до %d КБ)", len(text)>>10, specMaxText>>10)}}
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	s := Spec{kind: kind}
	if err := dec.Decode(&s); err != nil {
		return nil, &SpecError{Problems: []string{jsonProblem(text, err)}}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &SpecError{Problems: []string{"після JSON-об'єкта є зайвий текст — опис має бути одним об'єктом {…}"}}
	}
	s.normalize()
	if probs := s.validate(); len(probs) > 0 {
		return &s, &SpecError{Problems: probs}
	}
	return &s, nil
}

// jsonProblem explains a decoding error with the line and column.
func jsonProblem(text string, err error) string {
	var se *json.SyntaxError
	var te *json.UnmarshalTypeError
	switch {
	case errors.As(err, &se):
		line, col := lineCol(text, int(se.Offset))
		msg := se.Error()
		switch {
		case strings.Contains(msg, "unexpected end of JSON input"):
			msg = "JSON обірвано — бракує закривної дужки чи лапки"
		case strings.Contains(msg, "looking for beginning of object key string"):
			msg = "очікувалась назва поля в лапках (зайва кома перед «}»?)"
		case strings.Contains(msg, "after object key:value pair"):
			msg = "бракує коми між полями"
		case strings.Contains(msg, "after array element"):
			msg = "бракує коми між елементами списку"
		case strings.Contains(msg, "looking for beginning of value"):
			msg = "очікувалось значення (рядок у лапках, число, true/false, {…} чи […])"
		}
		return fmt.Sprintf("помилка JSON у рядку %d, позиції %d: %s", line, col, msg)
	case errors.As(err, &te):
		field := te.Field
		if field == "" {
			field = "опис"
		}
		return fmt.Sprintf("%s: неправильний тип — потрібно %s", field, typeName(te.Type))
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		line, _ := lineCol(text, len(text))
		return fmt.Sprintf("JSON обірвано в рядку %d — бракує закривної дужки чи лапки", line)
	}
	msg := err.Error()
	if f, ok := strings.CutPrefix(msg, "json: unknown field "); ok {
		return fmt.Sprintf("невідоме поле %s — перевір назву (є: name, auth, submit, id, poll, state, done, failed, queued, progress, fetch, result, error, cancel, test, caps)", f)
	}
	return strings.TrimPrefix(msg, "json: ")
}

func typeName(t reflect.Type) string {
	if t == nil {
		return "інше значення"
	}
	switch t.String() {
	case "string":
		return "рядок у лапках"
	case "float64", "int", "json.Number":
		return "число"
	case "bool", "*bool":
		return "true або false"
	case "[]string", "providers.SpecPaths":
		return "рядок або список рядків"
	case "[]int":
		return "список чисел"
	case "*int":
		return "ціле число"
	case "map[string]string":
		return "об'єкт із рядками"
	}
	if strings.HasPrefix(t.String(), "*providers.") || strings.HasPrefix(t.String(), "providers.") {
		return "об'єкт {…}"
	}
	return t.String()
}

func lineCol(text string, off int) (int, int) {
	off = max(0, min(off, len(text)))
	line := 1 + strings.Count(text[:off], "\n")
	col := off - strings.LastIndex(text[:off], "\n")
	return line, col
}

func (s *Spec) normalize() {
	s.Name, s.Auth, s.ID, s.State = strings.TrimSpace(s.Name), strings.TrimSpace(s.Auth), strings.TrimSpace(s.ID), strings.TrimSpace(s.State)
	for _, c := range []*SpecCall{s.Submit, s.Poll, s.Fetch, s.Cancel, s.Test} {
		if c != nil {
			c.Method = strings.ToUpper(strings.TrimSpace(c.Method))
			c.Path = strings.TrimSpace(c.Path)
			c.Encoding = strings.ToLower(strings.TrimSpace(c.Encoding))
		}
	}
	trimList := func(l []string) []string {
		out := l[:0:0]
		for _, v := range l {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	s.Done, s.Failed, s.Queued = trimList(s.Done), trimList(s.Failed), trimList(s.Queued)
	s.Progress, s.Result, s.Error = trimList(s.Progress), trimList(s.Result), trimList(s.Error)
	if s.Caps != nil {
		ls := slices.Clone(s.Caps.Lengths)
		sort.Ints(ls)
		s.Caps.Lengths = slices.Compact(ls)
		s.Caps.Aspects = trimList(s.Caps.Aspects)
	}
	s.uses = map[string]bool{}
	if s.Submit != nil {
		collectVars(s.Submit.Path, s.uses)
		for _, v := range s.Submit.Headers {
			collectVars(v, s.uses)
		}
		walkStrings(s.Submit.Body, func(str string) { collectVars(str, s.uses) })
	}
}

// collectVars records the placeholder names in str (without a "|text"
// modifier; an unknown modifier is recorded as "|<placeholder>").
func collectVars(str string, into map[string]bool) {
	for _, m := range reSpecVar.FindAllStringSubmatch(str, -1) {
		name, mod, _ := strings.Cut(m[1], "|")
		into[name] = true
		if mod != "" && mod != "text" {
			into["|"+m[1]] = true
		}
	}
	if reSpecID.MatchString(str) {
		into["id"] = true
	}
}

func walkStrings(v any, fn func(string)) {
	switch x := v.(type) {
	case string:
		fn(x)
	case map[string]any:
		for k, e := range x {
			fn(k)
			walkStrings(e, fn)
		}
	case []any:
		for _, e := range x {
			walkStrings(e, fn)
		}
	}
}

// validate returns the problems of a decoded, normalized spec.
func (s *Spec) validate() []string {
	var probs []string
	add := func(format string, a ...any) {
		if len(probs) < specMaxProblems {
			probs = append(probs, fmt.Sprintf(format, a...))
		}
	}
	auth, err := parseAuth(s.Auth)
	if err != nil {
		add("auth: %v", err)
	}
	s.auth = auth
	if s.Submit == nil || s.Submit.Path == "" {
		add(`submit.path: вкажи шлях запиту, що запускає генерацію, напр. "/v1/images"`)
	}
	calls := []struct {
		name string
		c    *SpecCall
	}{{"submit", s.Submit}, {"poll", s.Poll}, {"fetch", s.Fetch}, {"cancel", s.Cancel}, {"test", s.Test}}
	for _, nc := range calls {
		c := nc.c
		if c == nil {
			continue
		}
		if nc.name != "submit" && c.Path == "" {
			add(`%s.path: порожній — вкажи шлях або прибери "%s"`, nc.name, nc.name)
		}
		switch c.Method {
		case "", http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			add("%s.method: «%s» — можна GET, POST, PUT, PATCH або DELETE", nc.name, clip(c.Method, 20))
		}
		switch c.Encoding {
		case "", "json":
		case "multipart", "form":
			if _, ok := c.Body.(map[string]any); !ok && c.Body != nil {
				add(`%s.body: для encoding "%s" тіло має бути об'єктом {…}`, nc.name, c.Encoding)
			}
		default:
			add(`%s.encoding: «%s» — можна json, multipart або form`, nc.name, clip(c.Encoding, 20))
		}
		for h := range c.Headers {
			if !reHeader.MatchString(h) {
				add("%s.headers: некоректна назва заголовка «%s»", nc.name, clip(h, 40))
			}
			if strings.EqualFold(h, "Authorization") || strings.EqualFold(h, "X-API-Key") || strings.EqualFold(h, "Api-Key") {
				add(`%s.headers: «%s» не вписують в опис — ключ додає поле "auth"`, nc.name, h)
			}
		}
		if c.Every < 0 || c.Every > 600 || (c.Every > 0 && c.Every < 0.5) {
			add("%s.every: %.4g с — можна від 0,5 до 600 секунд", nc.name, c.Every)
		}
		if c.Timeout < 0 || c.Timeout > 7200 || (c.Timeout > 0 && c.Timeout < 5) {
			add("%s.timeout: %.4g с — можна від 5 до 7200 секунд", nc.name, c.Timeout)
		}
		// Placeholders.
		vars := map[string]bool{}
		collectVars(c.Path, vars)
		for _, v := range c.Headers {
			collectVars(v, vars)
		}
		walkStrings(c.Body, func(str string) { collectVars(str, vars) })
		names := make([]string, 0, len(vars))
		for v := range vars {
			names = append(names, v)
		}
		sort.Strings(names)
		for _, v := range names {
			low := strings.ToLower(v)
			switch {
			case strings.HasPrefix(v, "|"):
				add("%s: {{%s}} — невідомий модифікатор (є лише |text: значення як текст, напр. {{seconds|text}})", nc.name, v[1:])
			case slices.Contains(specSecretVars, low):
				add(`%s: {{%s}} — ключ не вписують в опис, його додає поле "auth" (і лише для адреси API)`, nc.name, v)
			case nc.name == "test" && v != "model":
				add("%s: {{%s}} тут немає — перевірка йде без генерації (можна лише {{model}})", nc.name, v)
			case slices.Contains(s.requestVars(), v):
			case nc.name == "submit" && (v == "id" || strings.HasPrefix(v, "submit.") || strings.HasPrefix(v, "status.")):
				add("submit: %s ще немає під час запуску — він з'являється у відповіді", placeholderName(v))
			case v == "id":
				if s.ID == "" {
					add(`%s: {id} без поля "id" — вкажи, де в відповіді номер задачі`, nc.name)
				}
			case strings.HasPrefix(v, "submit.") || strings.HasPrefix(v, "status."):
				if _, err := splitPath(v[strings.IndexByte(v, '.')+1:]); err != nil {
					add("%s: {{%s}} — %v", nc.name, v, err)
				}
			default:
				add("%s: невідома підстановка {{%s}} — є: %s", nc.name, v, strings.Join(SpecPlaceholdersFor(s.kind), " "))
			}
		}
	}
	if s.Submit != nil && s.Submit.Path != "" {
		if p := s.missingInput(); p != "" {
			add("%s", p)
		}
	}
	async := s.ID != ""
	if async {
		switch {
		case s.Poll == nil:
			add(`poll: потрібен для асинхронних задач, напр. {"path": "/v1/jobs/{id}"}`)
		case s.Poll.Path != "" && !refersJob(s.Poll.Path):
			add(`poll.path: додай {id} — номер задачі з відповіді (поле "id")`)
		}
		if s.State == "" && len(s.Result) == 0 {
			add(`state: вкажи поле статусу (або "result" — тоді студія чекає, доки з'явиться файл)`)
		}
		if len(s.Result) == 0 && s.Fetch == nil && s.kind != KindSTT { // a transcript may be the whole status
			add(`result: вкажи, де в статусі посилання на файл, напр. "output.url" (або додай "fetch")`)
		}
	} else if s.Poll != nil {
		add(`poll: задано, але немає "id" — звідки студії взяти номер задачі? Додай "id" або прибери "poll"`)
	}
	if s.State != "" && len(s.Done) == 0 {
		add(`done: перелічи значення «%s», що означають «готово», напр. ["succeeded"]`, s.State)
	}
	if s.Cancel != nil && s.Cancel.Path != "" && !refersJob(s.Cancel.Path) {
		add(`cancel.path: додай {id}, щоб скасувати саме цю задачу`)
	}
	for _, f := range []struct {
		name  string
		paths []string
	}{{"id", []string{s.ID}}, {"state", []string{s.State}}, {"progress", s.Progress}, {"result", s.Result}, {"error", s.Error}} {
		for _, p := range f.paths {
			if p == "" {
				continue
			}
			if p == badPaths {
				add(`%s: потрібен шлях у лапках, напр. "output.url", або список шляхів`, f.name)
				continue
			}
			if _, err := splitPath(p); err != nil {
				add("%s: «%s» — %v", f.name, clip(p, 60), err)
			}
		}
	}
	if c := s.Caps; c != nil {
		for _, l := range c.Lengths {
			if l < 1 || l > 120 {
				add("caps.lengths: %d с — можна від 1 до 120", l)
				break
			}
		}
		if c.MaxRefs != nil && (*c.MaxRefs < 0 || *c.MaxRefs > 16) {
			add("caps.maxRefs: %d — можна від 0 до 16", *c.MaxRefs)
		}
		for _, a := range c.Aspects {
			if !reAspect.MatchString(a) || strings.HasPrefix(a, "0") || strings.HasSuffix(a, ":0") {
				add(`caps.aspects: «%s» — потрібно як "16:9"`, clip(a, 20))
				break
			}
		}
		if c.Size != 0 && (c.Size < 256 || c.Size > 4096) {
			add("caps.size: %d — довша сторона від 256 до 4096 пікселів", c.Size)
		}
	}
	return probs
}

func placeholderName(v string) string {
	if v == "id" {
		return "{id}"
	}
	return "{{" + v + "}}"
}

// refersJob: a later call's path names the job ({id}, {{id}}, a URL from a reply).
func refersJob(path string) bool {
	vars := map[string]bool{}
	collectVars(path, vars)
	for v := range vars {
		if v == "id" || strings.HasPrefix(v, "submit.") || strings.HasPrefix(v, "status.") {
			return true
		}
	}
	return false
}

// ---- auth -----------------------------------------------------------------------------

// specAuth is how the key travels: kind "" (bearer when there is a key),
// bearer, basic, header (name, prefix), query (name) or none.
type specAuth struct{ kind, name, prefix string }

func parseAuth(a string) (specAuth, error) {
	a = strings.TrimSpace(a)
	kind, rest, _ := strings.Cut(a, ":")
	switch strings.ToLower(kind) {
	case "":
		return specAuth{}, nil
	case "bearer", "basic", "none":
		if rest != "" {
			break
		}
		return specAuth{kind: strings.ToLower(kind)}, nil
	case "header":
		name, prefix, _ := strings.Cut(rest, ":")
		name, prefix = strings.TrimSpace(name), strings.TrimSpace(prefix)
		if name == "" || !reHeader.MatchString(name) {
			return specAuth{}, fmt.Errorf("«%s» — після header: потрібна назва заголовка, напр. header:X-API-Key", clip(a, 40))
		}
		return specAuth{kind: "header", name: name, prefix: prefix}, nil
	case "query":
		name := strings.TrimSpace(rest)
		if name == "" || strings.ContainsAny(name, "&=?# ") {
			return specAuth{}, fmt.Errorf("«%s» — після query: потрібна назва параметра, напр. query:key", clip(a, 40))
		}
		return specAuth{kind: "query", name: name}, nil
	}
	return specAuth{}, fmt.Errorf("невідомий спосіб «%s» — можна bearer, basic, header:Назва, header:Назва:Префікс, query:назва або none", clip(a, 40))
}

// needsKey: the auth uses a key, so an empty one is an error.
func (a specAuth) needsKey() bool { return a.kind != "" && a.kind != "none" }

// header returns the function that sets the auth header (nil = none).
func (a specAuth) header() func(h http.Header, key string) {
	switch a.kind {
	case "", "bearer":
		return bearer
	case "basic":
		return func(h http.Header, key string) {
			if !strings.Contains(key, ":") {
				key += ":"
			}
			h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(key)))
		}
	case "header":
		return func(h http.Header, key string) {
			v := key
			if a.prefix != "" {
				v = a.prefix + " " + key
			}
			h.Set(a.name, v)
		}
	}
	return nil
}

// ---- JSON paths --------------------------------------------------------------------------

var reBracket = regexp.MustCompile(`\[\s*(?:"([^"]*)"|'([^']*)'|(-?\d+))\s*\]`)

// splitPath turns "data[0].url" / "$.data.0.url" into its segments.
func splitPath(p string) ([]string, error) {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "$")
	p = reBracket.ReplaceAllStringFunc(p, func(m string) string {
		sm := reBracket.FindStringSubmatch(m)
		return "." + sm[1] + sm[2] + sm[3]
	})
	p = strings.TrimPrefix(p, ".")
	if p == "" {
		return nil, errors.New("порожній шлях")
	}
	segs := strings.Split(p, ".")
	for _, s := range segs {
		if strings.TrimSpace(s) == "" {
			return nil, errors.New(`порожня частина шляху (дві крапки поспіль?) — пиши як "data.0.url"`)
		}
	}
	return segs, nil
}

// lookupPath finds a value in decoded JSON; nil values count as missing. A
// name that meets an array looks into its first element.
func lookupPath(v any, path string) (any, bool) {
	segs, err := splitPath(path)
	if err != nil {
		return nil, false
	}
	cur := v
	for _, seg := range segs {
		for {
			arr, ok := cur.([]any)
			if !ok {
				break
			}
			if n, err := strconv.Atoi(seg); err == nil {
				if n < 0 {
					n += len(arr)
				}
				if n < 0 || n >= len(arr) {
					return nil, false
				}
				cur = arr[n]
				seg = ""
				break
			}
			if len(arr) == 0 {
				return nil, false
			}
			cur = arr[0]
		}
		if seg == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	if cur == nil {
		return nil, false
	}
	return cur, true
}

// scalarText renders a JSON scalar as text ("" for objects, arrays, null).
func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// ---- capabilities ----------------------------------------------------------------------------

// universalDefaults are the caps of a universal API without (valid) "caps".
func universalDefaults() Caps {
	return Caps{
		Image: ImageCaps{Aspects: []string{"16:9", "9:16", "1:1"}},
		Video: VideoCaps{Lengths: []int{5, 10}, StartFrame: true, Aspects: []string{"16:9", "9:16"}},
	}
}

// SpecCaps returns what the API described by spec accepts — clip lengths,
// references, aspect ratios — from its "caps" and the placeholders its
// submit body uses. An empty or invalid spec gives the defaults.
func SpecCaps(spec string) Caps {
	capsMemo.Lock()
	defer capsMemo.Unlock()
	if capsMemo.ok && capsMemo.spec == spec { // the settings screen asks every frame
		return capsMemo.caps.clone()
	}
	c := universalDefaults()
	if s, err := ParseSpec(spec); err == nil && s != nil {
		c = s.caps()
	}
	capsMemo.spec, capsMemo.caps, capsMemo.ok = spec, c, true
	return c.clone()
}

var capsMemo struct {
	sync.Mutex
	spec string
	caps Caps
	ok   bool
}

func (c Caps) clone() Caps {
	c.Image.Aspects = slices.Clone(c.Image.Aspects)
	c.Video.Aspects = slices.Clone(c.Video.Aspects)
	c.Video.Lengths = slices.Clone(c.Video.Lengths)
	return c
}

func (s *Spec) caps() Caps {
	out := universalDefaults()
	usesRefs := s.uses["refs"] || s.uses["refs_b64"]
	usesStart := s.uses["start_image"] || s.uses["start_image_b64"]
	c := s.Caps
	if c == nil {
		c = &SpecCapabilities{}
	}
	maxRefs := 0
	if usesRefs {
		maxRefs = specDefaultRefs
		if c.MaxRefs != nil {
			maxRefs = *c.MaxRefs
		}
	}
	if len(c.Lengths) > 0 {
		out.Video.Lengths = slices.Clone(c.Lengths)
	}
	if len(c.Aspects) > 0 {
		out.Video.Aspects = slices.Clone(c.Aspects)
		out.Image.Aspects = slices.Clone(c.Aspects)
	}
	out.Video.MaxRefs = maxRefs
	out.Video.Components = maxRefs > 0
	out.Video.StartFrame = usesStart || usesRefs
	if c.StartFrame != nil {
		out.Video.StartFrame = *c.StartFrame && (usesStart || usesRefs)
	}
	switch {
	case maxRefs > 0:
		out.Image.MaxRefs = maxRefs
	case usesStart:
		out.Image.MaxRefs = 1
	}
	return out
}

// size is the long side for {{width}}/{{height}}/{{size}}.
func (s *Spec) size() int {
	if s.Caps != nil && s.Caps.Size > 0 {
		return s.Caps.Size
	}
	return specDefaultSize
}

// origin is the scheme+host of an absolute submit path ("" otherwise).
func (s *Spec) origin() string {
	if s == nil || s.Submit == nil {
		return ""
	}
	u, err := url.Parse(s.Submit.Path)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// ---- summary for the Settings screen ------------------------------------------------------

// CheckSpec validates a spec for images (video false) or video and returns
// a one-line Ukrainian summary of how it works plus warnings — things that
// work but are probably not what the user wants.
func CheckSpec(text string, video bool) (summary string, warnings []string, err error) {
	s, err := ParseSpec(text)
	if err != nil {
		return "", nil, err
	}
	method := func(c *SpecCall, def string) string {
		if c.Method != "" {
			return c.Method
		}
		return def
	}
	var parts []string
	if s.ID == "" {
		parts = append(parts, fmt.Sprintf("Синхронне API: %s %s", method(s.Submit, "POST"), clip(s.Submit.Path, 60)))
	} else {
		every := "кожні 5 с"
		if video {
			every = "кожні 10 с"
		}
		if s.Poll.Every > 0 {
			every = fmt.Sprintf("кожні %s с", strconv.FormatFloat(s.Poll.Every, 'f', -1, 64))
		}
		parts = append(parts, fmt.Sprintf("Задачі: %s %s → статус %s %s", method(s.Submit, "POST"), clip(s.Submit.Path, 50),
			clip(s.Poll.Path, 50), every))
	}
	switch {
	case s.Fetch != nil && len(s.Result) == 0:
		parts = append(parts, "файл — відповідь "+clip(s.Fetch.Path, 50))
	case len(s.Result) > 0:
		parts = append(parts, "файл: "+strings.Join(s.Result, " або "))
	default:
		parts = append(parts, "файл — сама відповідь")
	}
	summary = strings.Join(parts, " · ")

	caps := s.caps()
	usesStart := s.uses["start_image"] || s.uses["start_image_b64"]
	usesRefs := s.uses["refs"] || s.uses["refs_b64"]
	if video {
		if !usesStart && !usesRefs {
			warnings = append(warnings, "кадр сцени не передається ({{start_image}} чи {{refs}}) — відео зніметься лише з тексту")
		}
		if !s.uses["seconds"] {
			warnings = append(warnings, "довжина кліпу не передається ({{seconds}}) — сервіс вибере свою")
		}
	} else if caps.Image.MaxRefs == 0 {
		warnings = append(warnings, "опорні фото не передаються ({{refs}}) — герої можуть змінюватися між кадрами")
	}
	if s.ID != "" && s.Cancel == nil {
		warnings = append(warnings, "немає cancel — задачу, яку студія перестала чекати, сервіс доробить (і, можливо, порахує)")
	}
	return summary, warnings, nil
}
