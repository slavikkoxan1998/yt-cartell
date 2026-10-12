package providers

// «Налаштувати з документації (ШІ)»: reading an API's documentation for the
// text model that drafts a universal spec (the call itself is the studio's,
// Engine.SuggestCustomSpec). FetchDocs prefers a machine-readable
// description — an OpenAPI/Swagger document linked from the page (data-url,
// spec-url, href …) or at a well-known address, then llms-full.txt /
// llms.txt — over the page's own text; everything is capped at
// MaxDocsChars, an OpenAPI document too big for that is summarised with
// the generation-related paths first. SpecPrompt is the model's
// instructions (English, with the spec reference and two examples);
// FormatSpec lays a spec out for review.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxDocsChars caps the documentation text sent to the model.
const MaxDocsChars = 120_000

// Docs fetching limits (package vars so tests can shrink them).
var (
	docsTimeout  = 25 * time.Second // the page itself
	probeTimeout = 8 * time.Second  // each linked / well-known spec address
	docsMaxBytes = int64(12 << 20)
	docsMaxProbe = 8
)

// DocsText is API documentation prepared for the model.
type DocsText struct {
	Text   string // ≤ MaxDocsChars
	Source string // the URL it came from ("" = pasted)
	Kind   string // openapi | llms | html | text
	Note   string // Ukrainian: what was read and how it was shortened
}

// FetchDocs reads an API's documentation from a URL (see the file comment).
// progress (optional) gets short Ukrainian steps. The request carries no
// key or cookie.
func FetchDocs(ctx context.Context, rawURL string, progress func(string)) (DocsText, error) {
	if progress == nil {
		progress = func(string) {}
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return DocsText{}, errors.New("посилання на документацію має починатися з http:// або https://")
	}
	progress("Відкриваю документацію…")
	page, err := getDocs(ctx, u.String(), docsTimeout)
	if err != nil {
		return DocsText{}, err
	}
	if d, ok := asSpecDoc(page, u.String()); ok {
		return d, nil
	}
	if !page.html {
		d := PrepareDocs(string(page.body))
		d.Source = u.String()
		return d, nil
	}
	// An HTML page: a linked OpenAPI / llms.txt describes the API better.
	cands := docLinks(string(page.body), page.url)
	progress("Шукаю машинний опис API (OpenAPI, llms.txt)…")
	cands = append(cands, wellKnown(page.url)...)
	tried := map[string]bool{u.String(): true, page.url: true}
	n := 0
	for _, c := range cands {
		if tried[c] || n >= docsMaxProbe {
			continue
		}
		tried[c] = true
		n++
		if ctx.Err() != nil {
			return DocsText{}, ctx.Err()
		}
		p, err := getDocs(ctx, c, probeTimeout)
		if err != nil || p.html {
			continue
		}
		if d, ok := asSpecDoc(p, c); ok {
			progress("Знайшов опис API: " + clip(c, 80))
			return d, nil
		}
	}
	text := htmlText(string(page.body))
	if len([]rune(strings.TrimSpace(text))) < 300 {
		return DocsText{}, errors.New("на сторінці майже немає тексту (її малює JavaScript), а OpenAPI-опису не знайшлося — скопіюй текст документації і встав його в поле замість посилання")
	}
	d := PrepareDocs(text)
	d.Source, d.Kind = page.url, "html"
	return d, nil
}

type docPage struct {
	url  string // after redirects
	body []byte
	html bool
	json bool
}

func getDocs(ctx context.Context, u string, timeout time.Duration) (docPage, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
	if err != nil {
		return docPage{}, errors.New("некоректне посилання на документацію")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; YT-Cartell-Studio docs reader)")
	req.Header.Set("Accept", "application/json, text/markdown, text/plain;q=0.9, text/html;q=0.8, */*;q=0.5")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return docPage{}, ctx.Err()
		}
		return docPage{}, fmt.Errorf("документація не відкрилась (%s) — перевір посилання та інтернет", shortErr(err))
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return docPage{}, fmt.Errorf("документація не відкрилась (HTTP %d) — перевір посилання", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, docsMaxBytes))
	if err != nil {
		return docPage{}, fmt.Errorf("документацію не дочитано (%s)", shortErr(err))
	}
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	trim := bytes.TrimSpace(b)
	p := docPage{url: res.Request.URL.String(), body: b}
	p.json = strings.Contains(ct, "json") || bytes.HasPrefix(trim, []byte("{"))
	p.html = !p.json && (strings.Contains(ct, "html") || bytes.HasPrefix(bytes.ToLower(trim), []byte("<!doctype html")) ||
		bytes.Contains(bytes.ToLower(trim[:min(len(trim), 2048)]), []byte("<html")))
	return p, nil
}

// asSpecDoc accepts an OpenAPI/Swagger document (JSON or YAML) or an
// llms.txt; ok false for anything else.
func asSpecDoc(p docPage, src string) (DocsText, bool) {
	trim := bytes.TrimSpace(p.body)
	if p.json {
		var doc map[string]any
		if json.Unmarshal(trim, &doc) == nil && (doc["openapi"] != nil || doc["swagger"] != nil || doc["paths"] != nil) {
			d := openAPIDocs(doc, trim)
			d.Source = src
			return d, true
		}
		return DocsText{}, false
	}
	low := bytes.ToLower(trim[:min(len(trim), 4096)])
	if bytes.HasPrefix(low, []byte("openapi:")) || bytes.HasPrefix(low, []byte("swagger:")) || bytes.Contains(low, []byte("\nopenapi:")) {
		d := PrepareDocs(string(trim))
		d.Source, d.Kind = src, "openapi"
		d.Note = "Взято OpenAPI-опис (YAML): " + clip(src, 80) + suffixNote(d.Note)
		return d, true
	}
	if strings.HasSuffix(strings.ToLower(strings.SplitN(src, "?", 2)[0]), ".txt") && !p.html && len(trim) > 200 {
		d := PrepareDocs(string(trim))
		d.Source, d.Kind = src, "llms"
		d.Note = "Взято текст для ШІ (llms.txt): " + clip(src, 80) + suffixNote(d.Note)
		return d, true
	}
	return DocsText{}, false
}

func suffixNote(n string) string {
	if n == "" {
		return ""
	}
	return " · " + n
}

var (
	reSpecLink = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:data-url|spec-url|data-spec-url|specurl|spec_url)\s*[=:]\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?i)\burl\s*:\s*["']([^"']+\.(?:json|ya?ml))["']`),
		regexp.MustCompile(`(?i)["']([^"'\s<>]*(?:openapi|swagger)[^"'\s<>]*\.(?:json|ya?ml))["']`),
		regexp.MustCompile(`(?i)href\s*=\s*["']([^"']*llms(?:-full)?\.txt)["']`),
	}
)

// docLinks finds OpenAPI / llms.txt links in an HTML page (absolute URLs).
func docLinks(page, base string) []string {
	b, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, re := range reSpecLink {
		for _, m := range re.FindAllStringSubmatch(page, 20) {
			ref, err := url.Parse(html.UnescapeString(strings.TrimSpace(m[1])))
			if err != nil {
				continue
			}
			abs := b.ResolveReference(ref)
			if abs.Scheme != "http" && abs.Scheme != "https" {
				continue
			}
			s := abs.String()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	// llms-full.txt (the whole docs) before llms.txt (often an index).
	sort.SliceStable(out, func(i, j int) bool { return docRank(out[i]) < docRank(out[j]) })
	return out
}

func docRank(u string) int {
	l := strings.ToLower(u)
	switch {
	case strings.Contains(l, "openapi") || strings.Contains(l, "swagger") || strings.HasSuffix(l, ".json"):
		return 0
	case strings.Contains(l, "llms-full"):
		return 1
	}
	return 2
}

// wellKnown are the usual addresses of a machine-readable API description.
func wellKnown(page string) []string {
	b, err := url.Parse(page)
	if err != nil {
		return nil
	}
	origin := b.Scheme + "://" + b.Host
	out := []string{origin + "/openapi.json", origin + "/swagger.json", origin + "/v1/openapi.json", origin + "/api/openapi.json"}
	if dir := strings.TrimRight(b.Path, "/"); dir != "" && dir != "/" {
		out = append(out, origin+dir+"/openapi.json")
	}
	return append(out, origin+"/llms-full.txt", origin+"/llms.txt")
}

// ---- text preparation ------------------------------------------------------------------------

var (
	reDropBlock = regexp.MustCompile(`(?is)<(script|style|svg|noscript|template|iframe)\b.*?</\s*(script|style|svg|noscript|template|iframe)\s*>`)
	reComment   = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBreak     = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/h[1-6]|/tr|/pre|/section|/article|/table|/ul|/ol|hr)\b[^>]*>`)
	reItem      = regexp.MustCompile(`(?i)<\s*li\b[^>]*>`)
	reCell      = regexp.MustCompile(`(?i)<\s*/?\s*t[dh]\b[^>]*>`)
	reHeading   = regexp.MustCompile(`(?i)<\s*h([1-6])\b[^>]*>`)
	reTag       = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces    = regexp.MustCompile(`[ \t\x{00a0}]+`)
	reBlank     = regexp.MustCompile(`\n{3,}`)
)

// htmlText is the readable text of an HTML page (code blocks kept).
func htmlText(page string) string {
	s := reDropBlock.ReplaceAllString(page, " ")
	s = reComment.ReplaceAllString(s, " ")
	s = reHeading.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(reHeading.FindStringSubmatch(m)[1])
		return "\n\n" + strings.Repeat("#", n) + " "
	})
	s = reBreak.ReplaceAllString(s, "\n")
	s = reItem.ReplaceAllString(s, "\n- ")
	s = reCell.ReplaceAllString(s, " | ")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(reBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// docsKeywords mark the parts of a documentation that matter for the spec.
var docsKeywords = []string{"generat", "image", "video", "job", "task", "predict", "status", "poll", "result", "output",
	"prompt", "aspect", "duration", "second", "authori", "bearer", "api key", "api-key", "x-api-key", "header", "endpoint",
	"request", "response", "post ", "get ", "/v1", "url", "base64", "webhook", "cancel", "model", "error", "rate limit",
	"speech", "voice", "audio", "transcri", "tts", "language", "timestamp"}

// PrepareDocs prepares pasted (or fetched) documentation text: an OpenAPI
// JSON document is summarised, anything longer than MaxDocsChars is cut
// down to the parts that matter for generation (in their order).
func PrepareDocs(text string) DocsText {
	text = strings.TrimSpace(strings.ToValidUTF8(text, ""))
	trim := []byte(text)
	if bytes.HasPrefix(trim, []byte("{")) {
		var doc map[string]any
		if json.Unmarshal(trim, &doc) == nil && (doc["openapi"] != nil || doc["swagger"] != nil || doc["paths"] != nil) {
			return openAPIDocs(doc, trim)
		}
	}
	if len(text) <= MaxDocsChars {
		return DocsText{Text: text, Kind: "text"}
	}
	return DocsText{Text: relevantText(text, MaxDocsChars), Kind: "text",
		Note: fmt.Sprintf("документація велика (%d тис. символів) — взято головне про генерацію", len([]rune(text))/1000)}
}

// relevantText keeps the start and the paragraphs with the most keywords,
// in their order, within limit bytes.
func relevantText(text string, limit int) string {
	paras := strings.Split(text, "\n\n")
	type scored struct {
		i, score int
	}
	head := min(len(text), limit/10) // the intro: base URL, auth
	used := 0
	keep := map[int]bool{}
	for i, p := range paras {
		if used+len(p)+2 > head {
			break
		}
		keep[i] = true
		used += len(p) + 2
	}
	var list []scored
	for i, p := range paras {
		if keep[i] {
			continue
		}
		low := strings.ToLower(p)
		s := 0
		for _, k := range docsKeywords {
			s += strings.Count(low, k)
		}
		if strings.Contains(p, "{") && strings.Contains(p, "\"") {
			s += 3 // JSON examples
		}
		list = append(list, scored{i, s * 1000 / max(200, len(p))})
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].score > list[b].score })
	for _, sc := range list {
		if sc.score == 0 {
			break
		}
		p := paras[sc.i]
		if len(p) > limit/4 {
			p = p[:limit/4]
			paras[sc.i] = p
		}
		if used+len(p)+2 > limit {
			continue
		}
		keep[sc.i] = true
		used += len(p) + 2
	}
	var b strings.Builder
	for i, p := range paras {
		if keep[i] {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(p)
		}
	}
	return strings.ToValidUTF8(b.String(), "")
}

// ---- OpenAPI ---------------------------------------------------------------------------------

// openAPIDocs sends a small OpenAPI document as is (compact JSON) and
// summarises a big one: servers, auth, then every operation (the
// generation-related ones first) with its parameters, body and reply schemas.
func openAPIDocs(doc map[string]any, raw []byte) DocsText {
	var compact bytes.Buffer
	if json.Compact(&compact, raw) == nil && compact.Len() <= MaxDocsChars {
		return DocsText{Text: "OpenAPI document (JSON):\n" + compact.String(), Kind: "openapi", Note: "Взято OpenAPI-опис API"}
	}
	o := &oapi{doc: doc}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	info, _ := doc["info"].(map[string]any)
	line("OpenAPI summary (the full document is %d KB): %s %s", len(raw)>>10, str(info["title"]), str(info["version"]))
	if d := str(info["description"]); d != "" {
		line("About: %s", clip(d, 1500))
	}
	if servers, ok := doc["servers"].([]any); ok {
		for _, s := range servers {
			if m, ok := s.(map[string]any); ok {
				line("Server: %s %s", str(m["url"]), clip(str(m["description"]), 120))
			}
		}
	}
	if h := str(doc["host"]); h != "" { // Swagger 2
		line("Server: %s://%s%s", firstNonEmpty(firstStr(doc["schemes"]), "https"), h, str(doc["basePath"]))
	}
	schemes := map[string]any{}
	if c, ok := doc["components"].(map[string]any); ok {
		if s, ok := c["securitySchemes"].(map[string]any); ok {
			schemes = s
		}
	}
	if s, ok := doc["securityDefinitions"].(map[string]any); ok {
		schemes = s
	}
	for name, s := range schemes {
		m, _ := s.(map[string]any)
		line("Auth %q: type=%s scheme=%s in=%s name=%s %s", name, str(m["type"]), str(m["scheme"]), str(m["in"]), str(m["name"]), clip(str(m["description"]), 200))
	}
	type op struct {
		method, path string
		m            map[string]any
		score        int
	}
	var ops []op
	paths, _ := doc["paths"].(map[string]any)
	for p, v := range paths {
		item, _ := v.(map[string]any)
		for _, meth := range []string{"get", "post", "put", "patch", "delete"} {
			m, ok := item[meth].(map[string]any)
			if !ok {
				continue
			}
			text := strings.ToLower(p + " " + str(m["summary"]) + " " + str(m["operationId"]) + " " + fmt.Sprint(m["tags"]))
			score := 0
			for _, k := range []string{"generat", "image", "video", "job", "task", "predict", "render", "status", "result", "account", "balance", "model", "cancel"} {
				if strings.Contains(text, k) {
					score++
				}
			}
			ops = append(ops, op{strings.ToUpper(meth), p, m, score})
		}
	}
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].score != ops[j].score {
			return ops[i].score > ops[j].score
		}
		return ops[i].path+ops[i].method < ops[j].path+ops[j].method
	})
	line("")
	line("Operations (%d; generation-related first):", len(ops))
	skipped := 0
	for _, op := range ops {
		var ob strings.Builder
		fmt.Fprintf(&ob, "\n%s %s — %s\n", op.method, op.path, clip(firstNonEmpty(str(op.m["summary"]), str(op.m["description"]), str(op.m["operationId"])), 300))
		if d := str(op.m["description"]); d != "" && d != str(op.m["summary"]) {
			fmt.Fprintf(&ob, "  Description: %s\n", clip(d, 600))
		}
		if ps, ok := op.m["parameters"].([]any); ok {
			for _, p := range ps {
				pm, _ := o.resolve(p).(map[string]any)
				fmt.Fprintf(&ob, "  Param %s (%s)%s: %s %s\n", str(pm["name"]), str(pm["in"]), req(pm["required"]), o.schema(pm["schema"], 0), clip(str(pm["description"]), 150))
			}
		}
		if rb, ok := o.resolve(op.m["requestBody"]).(map[string]any); ok {
			if content, ok := rb["content"].(map[string]any); ok {
				for ct, c := range content {
					cm, _ := c.(map[string]any)
					fmt.Fprintf(&ob, "  Body (%s): %s\n", ct, o.schema(cm["schema"], 0))
					if ex := cm["example"]; ex != nil {
						if eb, err := json.Marshal(ex); err == nil {
							fmt.Fprintf(&ob, "  Body example: %s\n", clip(string(eb), 800))
						}
					}
				}
			}
		}
		if rs, ok := op.m["responses"].(map[string]any); ok {
			codes := make([]string, 0, len(rs))
			for code := range rs {
				codes = append(codes, code)
			}
			sort.Strings(codes)
			for _, code := range codes {
				rm, _ := o.resolve(rs[code]).(map[string]any)
				desc := clip(str(rm["description"]), 120)
				schema := ""
				if content, ok := rm["content"].(map[string]any); ok {
					for ct, c := range content {
						cm, _ := c.(map[string]any)
						schema = ct + " " + o.schema(cm["schema"], 0)
						break
					}
				} else if s := rm["schema"]; s != nil { // Swagger 2
					schema = o.schema(s, 0)
				}
				if strings.HasPrefix(code, "2") || schema != "" && len(schema) < 400 {
					fmt.Fprintf(&ob, "  Reply %s: %s %s\n", code, desc, schema)
				}
			}
		}
		if b.Len()+ob.Len() > MaxDocsChars-200 {
			skipped++
			continue
		}
		b.WriteString(ob.String())
	}
	if skipped > 0 {
		line("\n(%d less related operations left out for length)", skipped)
	}
	return DocsText{Text: b.String(), Kind: "openapi",
		Note: fmt.Sprintf("Взято OpenAPI-опис API — завеликий (%d КБ), тож надіслано стислий зміст", len(raw)>>10)}
}

type oapi struct{ doc map[string]any }

// resolve follows a local $ref ("#/components/…", "#/definitions/…").
func (o *oapi) resolve(v any) any {
	for i := 0; i < 8; i++ {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#/") {
			return v
		}
		var cur any = o.doc
		for _, part := range strings.Split(ref[2:], "/") {
			cm, ok := cur.(map[string]any)
			if !ok {
				return v
			}
			cur = cm[strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")]
		}
		v = cur
	}
	return v
}

// schema renders a JSON schema compactly: {name: type enum[…] (required) — description; …}.
func (o *oapi) schema(v any, depth int) string {
	m, ok := o.resolve(v).(map[string]any)
	if !ok {
		return ""
	}
	if depth > 4 {
		return "{…}"
	}
	for _, k := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := m[k].([]any); ok && len(list) > 0 {
			parts := make([]string, 0, len(list))
			for _, e := range list[:min(len(list), 4)] {
				parts = append(parts, o.schema(e, depth+1))
			}
			return k + "(" + strings.Join(parts, " | ") + ")"
		}
	}
	t := str(m["type"])
	if props, ok := m["properties"].(map[string]any); ok {
		required := map[string]bool{}
		if rl, ok := m["required"].([]any); ok {
			for _, r := range rl {
				required[str(r)] = true
			}
		}
		names := make([]string, 0, len(props))
		for n := range props {
			names = append(names, n)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, n := range names {
			pm, _ := o.resolve(props[n]).(map[string]any)
			p := n + ": " + o.schema(pm, depth+1)
			if required[n] {
				p += " (required)"
			}
			if d := str(pm["description"]); d != "" {
				p += " — " + clip(d, 100)
			}
			parts = append(parts, p)
		}
		return "{" + strings.Join(parts, "; ") + "}"
	}
	if t == "array" {
		return "array of " + o.schema(m["items"], depth+1)
	}
	out := firstNonEmpty(t, "any")
	if f := str(m["format"]); f != "" {
		out += " " + f
	}
	if e, ok := m["enum"].([]any); ok {
		vals := make([]string, 0, len(e))
		for _, x := range e[:min(len(e), 20)] {
			vals = append(vals, fmt.Sprint(x))
		}
		out += " enum[" + strings.Join(vals, ",") + "]"
	}
	if d, ok := m["default"]; ok {
		out += fmt.Sprintf(" default=%v", d)
	}
	return out
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case nil:
		return ""
	case float64, bool:
		return fmt.Sprint(x)
	}
	return ""
}

func firstStr(v any) string {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return str(l[0])
	}
	return ""
}

func req(v any) string {
	if b, _ := v.(bool); b {
		return " required"
	}
	return ""
}

// ---- the model's instructions -------------------------------------------------------------

// SpecPrompt is the system instructions for drafting a spec for images
// (video false) or video from documentation. The model must answer with
// ONE JSON object: {"spec": {…}, "baseUrl": "…", "model": "…", "notes": […]}.
func SpecPrompt(video bool) string {
	kind, unit := "ONE image (a still frame of a scene)", "image"
	if video {
		kind, unit = "ONE short video clip (image-to-video: animate the scene's start frame, or text-to-video)", "video"
	}
	var b strings.Builder
	w := func(s string) { b.WriteString(s + "\n") }
	w("You configure the \"universal\" adapter of YT Cartell Studio, a desktop app that makes videos from stories. The adapter talks to any")
	w("HTTP API that generates " + unit + "s, driven by a JSON description (the \"spec\"). Read the API documentation the user gives you and")
	w("write the spec that generates " + kind + " per request.")
	w("")
	w("Reply with ONLY one JSON object, no prose and no code fences:")
	w(`{"spec": {…the spec…}, "baseUrl": "https://api.example.com/v1", "model": "model-id", "notes": ["…", "…"]}`)
	w(`- baseUrl: the API root every spec path is relative to (scheme + host, plus a common prefix like /v1 when all endpoints share it); "" if unknown.`)
	w(`- model: the model id to put in the user's model field (the docs' recommended or most capable ` + unit + ` model); "" when the API has no model choice.`)
	w(`- notes: 0–6 short sentences in UKRAINIAN, informal "ти", about what you could NOT determine from the docs and guessed, or what the user`)
	w(`  must check (e.g. "Значення статусу вгадано — перевір у документації"). Empty list when everything is clear.`)
	w("")
	w("SPEC REFERENCE (only submit is required; omit what the API does not need):")
	w(`{`)
	w(`  "name": "Service name",`)
	w(`  "auth": "bearer" | "basic" | "none" | "header:X-API-Key" | "header:Authorization:Key" (= "Authorization: Key <key>") | "query:key",`)
	w(`  "submit": {"method": "POST", "path": "/generations", "headers": {"X-Extra": "value"}, "encoding": "json" | "multipart" | "form",`)
	w(`             "body": {…template…}, "timeout": 600},`)
	w(`  "id": "JSON path of the job id in the submit reply — OMIT for synchronous APIs that return the file directly",`)
	w(`  "poll": {"method": "GET", "path": "/jobs/{id}", "every": 5},      (async only; every = seconds between status checks)`)
	w(`  "state": "status",                                                (JSON path of the job status in the poll reply)`)
	w(`  "done": ["succeeded"], "failed": ["failed", "canceled"], "queued": ["queued", "pending"],   (exact status values from the docs)`)
	w(`  "progress": "progress",                                           (optional: 0–1 or 0–100)`)
	w(`  "fetch": {"method": "GET", "path": "/jobs/{id}/result"},         (optional: one more call that returns the result after done)`)
	w(`  "result": ["output.url", "data.0.b64_json"],                     (JSON paths of the file: URL, data: URI or base64; first that exists wins;`)
	w(`                                                                    omit only when the reply/fetch body IS the file bytes)`)
	w(`  "error": "error.message",                                         (JSON path(s) of the failure text)`)
	w(`  "cancel": {"method": "POST", "path": "/jobs/{id}/cancel"},       (if documented)`)
	w(`  "test": {"method": "GET", "path": "/account"},                    (a cheap authenticated GET that generates nothing: account, balance or models — if documented)`)
	w(`  "caps": {"lengths": [5, 10], "maxRefs": 4, "aspects": ["16:9", "9:16"], "startFrame": true, "size": 1280}`)
	w(`}`)
	w("JSON paths are dotted with array indexes: data.0.url, output.images.0.url.")
	w("Placeholders inside submit (path, headers, body string values):")
	w(`  {{prompt}} the text prompt · {{model}} the model field · {{aspect}} "16:9" · {{orientation}} landscape|portrait|square`)
	w(`  {{seconds}} clip length (number; {{seconds|text}} gives the string "8") · {{width}} {{height}} pixels · {{size}} "1280x720"`)
	w(`  {{start_image}} the start frame / source image as a data: URI · {{start_image_b64}} the same as raw base64`)
	w(`  {{refs}} an array of reference images (data: URIs) for character consistency · {{refs_b64}} the same as raw base64 · {{resolution}} "720p"`)
	w(`A string that is exactly one placeholder becomes its typed value (number, array); a placeholder without a value removes its key, so`)
	w(`optional inputs are safe to include. Later calls may use {id} (the job id), {{submit.<path>}} (a value from the submit reply, e.g. a`)
	w(`status URL: "path": "{{submit.status_url}}") and {{status.<path>}} (a value from the last status reply).`)
	w("")
	w("RULES")
	w(`- NEVER put the API key, a token or a placeholder for it anywhere in the spec (no {{key}}, no Authorization header): "auth" handles it.`)
	w(`- Use paths relative to baseUrl. Use the endpoint that creates ONE ` + unit + ` from a text prompt (plus images when supported).`)
	w(`- Copy field names, status values and paths exactly as the docs write them. Do not invent endpoints.`)
	if video {
		w(`- Send the start frame with {{start_image}} (or {{start_image_b64}}) in the image-to-video field. If the API takes several reference`)
		w(`  images (for consistent characters), send {{refs}} and set caps.maxRefs. Durations: {{seconds}} + caps.lengths = the allowed values.`)
	} else {
		w(`- Reference images of the characters/places go in {{refs}} (set caps.maxRefs to the documented limit). If the API takes only ONE`)
		w(`  input image, use {{start_image}} instead. Text-only APIs: no image placeholders.`)
	}
	w(`- caps.aspects: only the ratios the API supports ("16:9", "9:16", "1:1", …). Use {{aspect}} for ratio strings, {{orientation}}`)
	w(`  for landscape/portrait values, {{width}}/{{height}} or {{size}} for pixel sizes (then set caps.size to the long side).`)
	w(`- Multipart uploads: "encoding": "multipart"; a data: URI value becomes a file part.`)
	w(`- If the docs return the file bytes directly (e.g. "Accept: image/*"), omit "result" and set the Accept header in submit.headers.`)
	w(`- Async jobs need id + poll + state + done (+ failed, queued) + result (or fetch). Leave out optional parameters the studio does not need.`)
	w("")
	w("EXAMPLE 1 — async jobs API (video):")
	w(`{"spec": {"name": "Acme Video", "auth": "bearer",`)
	w(`  "submit": {"method": "POST", "path": "/tasks", "body": {"model": "{{model}}", "prompt": "{{prompt}}", "image": "{{start_image}}",`)
	w(`    "duration": "{{seconds}}", "ratio": "{{aspect}}"}},`)
	w(`  "id": "task_id", "poll": {"method": "GET", "path": "/tasks/{id}", "every": 10}, "state": "status",`)
	w(`  "done": ["SUCCEEDED"], "failed": ["FAILED", "CANCELLED"], "queued": ["PENDING"], "progress": "progress",`)
	w(`  "result": "output.video_url", "error": "failure_reason", "cancel": {"method": "DELETE", "path": "/tasks/{id}"},`)
	w(`  "test": {"method": "GET", "path": "/organization"}, "caps": {"lengths": [5, 10], "aspects": ["16:9", "9:16", "1:1"]}},`)
	w(` "baseUrl": "https://api.acme.ai/v1", "model": "acme-video-2", "notes": ["Значення статусу взято з прикладу — перевір, чи немає ще \"RUNNING\"."]}`)
	w("")
	w("EXAMPLE 2 — synchronous API that returns base64 (images):")
	w(`{"spec": {"name": "OpenAI Images", "submit": {"method": "POST", "path": "/images/generations",`)
	w(`  "body": {"model": "{{model}}", "prompt": "{{prompt}}", "n": 1, "size": "{{size}}"}},`)
	w(`  "result": ["data.0.b64_json", "data.0.url"], "error": "error.message", "test": {"method": "GET", "path": "/models"},`)
	w(`  "caps": {"aspects": ["16:9", "9:16", "1:1"], "size": 1536}},`)
	w(` "baseUrl": "https://api.openai.com/v1", "model": "gpt-image-2", "notes": []}`)
	return strings.TrimRight(b.String(), "\n")
}

// ---- layout --------------------------------------------------------------------------------

// FormatSpec lays out a JSON spec for review, keeping the key order: two
// spaces per level, short arrays and objects on one line. Invalid JSON is
// returned unchanged.
func FormatSpec(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(raw)))
	dec.UseNumber()
	n, err := readNode(dec)
	if err != nil {
		return string(raw)
	}
	var b strings.Builder
	n.write(&b, 0)
	return b.String()
}

// node is an ordered JSON value.
type node struct {
	kind   byte // '{', '[', or 0 for a scalar
	keys   []string
	kids   []*node
	scalar string // encoded
}

func readNode(dec *json.Decoder) (*node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		n := &node{kind: byte(t)}
		for dec.More() {
			if n.kind == '{' {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				n.keys = append(n.keys, k)
			}
			kid, err := readNode(dec)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, kid)
		}
		if _, err := dec.Token(); err != nil { // the closing delimiter
			return nil, err
		}
		return n, nil
	default:
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(t); err != nil {
			return nil, err
		}
		return &node{scalar: strings.TrimSpace(buf.String())}, nil
	}
}

func (n *node) compact() string {
	var b strings.Builder
	switch n.kind {
	case 0:
		return n.scalar
	case '{':
		b.WriteString("{")
		for i, k := range n.kids {
			if i > 0 {
				b.WriteString(", ")
			}
			kb, _ := json.Marshal(n.keys[i])
			b.Write(kb)
			b.WriteString(": ")
			b.WriteString(k.compact())
		}
		b.WriteString("}")
	default:
		b.WriteString("[")
		for i, k := range n.kids {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(k.compact())
		}
		b.WriteString("]")
	}
	return b.String()
}

func (n *node) write(b *strings.Builder, depth int) {
	if c := n.compact(); n.kind == 0 || len(n.kids) == 0 || (depth > 0 && len(c) <= 72-2*depth && !n.hasObject(depth)) {
		b.WriteString(c)
		return
	}
	open, end := "{", "}"
	if n.kind == '[' {
		open, end = "[", "]"
	}
	ind := strings.Repeat("  ", depth+1)
	b.WriteString(open + "\n")
	for i, k := range n.kids {
		b.WriteString(ind)
		if n.kind == '{' {
			kb, _ := json.Marshal(n.keys[i])
			b.Write(kb)
			b.WriteString(": ")
		}
		k.write(b, depth+1)
		if i < len(n.kids)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.Repeat("  ", depth) + end)
}

// hasObject: a body-level object with nested objects stays multi-line.
func (n *node) hasObject(depth int) bool {
	if depth > 1 {
		return false
	}
	for _, k := range n.kids {
		if k.kind == '{' && len(k.kids) > 0 {
			return true
		}
	}
	return false
}
