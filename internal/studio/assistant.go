package studio

// «Помічник підключення» (Settings → Підключення, Home's set-up call-out):
// the user pastes anything — addresses, keys, documentation links, notes
// («ось мій ендпоінт https://… ключ sk-… модель …») — and gets a plan
// (Текст → X, Кадри → Y, Відео → Z, Озвучка → W, Розпізнавання → V), each
// step checked, applied on «Застосувати» after a backup of the settings.
//
//	1. ParseAssist   pure: URLs, keys (known prefixes, long tokens after
//	                 «ключ»/«key»), models («модель …», known families),
//	                 voices, docs links; the words of a line hint its role
//	                 (текст, кадри, відео, озвучка, розпізнавання); items on
//	                 one line (or a key/model under an address) belong together
//	2. known hosts   api.openai.com, api.anthropic.com, openrouter.ai, Groq,
//	                 ElevenLabs, Cartesia, Lumean, fal.ai, Replicate … and key
//	                 shapes (sk-ant-, sk-or-v1-, gsk_, AIza/AQ., sk_car_, r8_ …)
//	                 decide without a single request
//	3. probes        an unknown address is asked only cheap GETs: the
//	                 OpenAI-compatible model list (with /v1, /api/v1 …, llm
//	                 Check), Anthropic's, Ollama's /api/tags, ElevenLabs'
//	                 /v1/voices, G-Labs' /api/health. Known hosts are never
//	                 path-scanned (Lumean bans IPs for that)
//	4. the text model (when one works) classifies what is still unclear —
//	   it sees the text with every key replaced by [KEY1] … and every stored
//	   secret masked; a documentation link becomes a universal spec
//	   (SuggestSpec) for its role
//	5. checks        every step runs the service's own «Перевірити» on the
//	                 proposed settings (nothing is generated or billed, the
//	                 text check asks one word); failures show the exact request
//	6. ApplyAssist   config.Store.SaveBackup, then one Store.Update with the
//	                 steps left on
//
// Works without any text model: everything but step 4 is heuristics.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
	"ytcartell/internal/glabs"
	"ytcartell/internal/httpdiag"
	"ytcartell/internal/llm"
	"ytcartell/internal/providers"
	"ytcartell/internal/stt"
	"ytcartell/internal/tts"
)

// Roles of the assistant's steps.
const (
	RoleText  = "text"
	RoleImage = "image"
	RoleVideo = "video"
	RoleTTS   = "tts"
	RoleSTT   = "stt"
)

// AssistRoleLabel is the Ukrainian name of a role.
func AssistRoleLabel(role string) string {
	switch role {
	case RoleText:
		return "Текст"
	case RoleImage:
		return "Кадри"
	case RoleVideo:
		return "Відео"
	case RoleTTS:
		return "Озвучка"
	case RoleSTT:
		return "Розпізнавання"
	}
	return role
}

var assistRoles = []string{RoleText, RoleImage, RoleVideo, RoleTTS, RoleSTT}

// AssistItem is one thing found in the pasted text.
type AssistItem struct {
	Kind    string // url | docs | key | model | voice
	Display string // shown to the user (keys masked)
	Service string // what it is ("" = not recognised)
	Line    int    // 1-based line of the input
	value   string // the URL, key, model or voice as pasted (never logged or sent to a model)
	role    string // the role the line's words name ("" = none)
}

// AssistStep is one proposed change, checked.
type AssistStep struct {
	Role   string   // RoleText …
	Target string   // «OpenRouter · deepseek/deepseek-chat»
	Lines  []string // what will be written (keys masked)
	Status string   // "ok" | "fail" | "skip" (not checked)
	Msg    string   // the check's outcome (the exact request when it failed)
	On     bool     // apply it (the UI toggles; failed steps start off)
	apply  func(c *config.Config)
}

// AssistPlan is the assistant's answer.
type AssistPlan struct {
	Items []AssistItem
	Steps []*AssistStep
	Notes []string // what was not used, and why
	AI    string   // how the text model helped ("" = not used)
}

// AssistOn: steps left on.
func (p *AssistPlan) AssistOn() []*AssistStep {
	var out []*AssistStep
	if p == nil {
		return nil
	}
	for _, s := range p.Steps {
		if s.On {
			out = append(out, s)
		}
	}
	return out
}

// ---- 1. parsing -------------------------------------------------------------------------

var (
	reAssistURL   = regexp.MustCompile(`(?i)\bhttps?://[^\s<>"'«»\x60]+`)
	reAssistHost  = regexp.MustCompile(`(?i)(?:^|[\s(,;])((?:localhost|127\.0\.0\.1|\[::1\]|(?:[a-z0-9-]+\.)+[a-z]{2,})(?::\d{2,5})?(?:/[^\s<>"'«»]*)?)`)
	reAssistToken = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_\-.:]{11,}[A-Za-z0-9]`)
	reKeyWord     = regexp.MustCompile(`(?i)(ключ|key|token|токен|secret|api[-_ ]?key|apikey|bearer)\s*[:=—-]?\s*$`)
	reModelWord   = regexp.MustCompile(`(?i)(модель|моделі|model|models)\s*[:=—-]?\s*$`)
	reVoiceWord   = regexp.MustCompile(`(?i)(голос|voice|voice[_ ]?id)\s*[:=—-]?\s*$`)
	reModelLike   = regexp.MustCompile(`(?i)^(gpt-|o[1-9]|chatgpt|claude-|gemini-|gemma|llama|mistral|mixtral|qwen|deepseek|grok|command|phi-|eleven_|scribe_|whisper|tts-1|gpt-4o-mini-tts|sonic|veo|kling|flux|nano-banana|sora|gen-4|ray-|hailuo|seedance|seedream|wan|imagen|speech-|s2|s1$)`)
	reAssistDocs  = regexp.MustCompile(`(?i)(/docs?\b|/documentation|/reference|api-reference|/developers?\b|openapi|swagger|llms(-full)?\.txt|redoc|readme\.io|gitbook)`)
)

// knownKey is a key shape that names its service.
type knownKey struct {
	re      *regexp.Regexp
	service string
}

// assistKeyShapes in the order they are tried (specific before generic).
var assistKeyShapes = []knownKey{
	{regexp.MustCompile(`^sk-ant-[A-Za-z0-9_\-]{20,}$`), "anthropic"},
	{regexp.MustCompile(`^sk-or-v1-[A-Za-z0-9]{20,}$`), "openrouter"},
	{regexp.MustCompile(`^sk_car_[A-Za-z0-9_\-]{12,}$`), "cartesia"},
	{regexp.MustCompile(`^sk_[0-9a-f]{40,64}(_residency_[a-z0-9]+)?$`), "elevenlabs"},
	{regexp.MustCompile(`^(AIza[0-9A-Za-z_\-]{35}|AQ\.[A-Za-z0-9_\-]{16,}(\.[A-Za-z0-9_\-]+)*)$`), "google"},
	{regexp.MustCompile(`^gsk_[A-Za-z0-9]{20,}$`), "groq"},
	{regexp.MustCompile(`^xai-[A-Za-z0-9]{20,}$`), "xai"},
	{regexp.MustCompile(`^r8_[A-Za-z0-9]{20,}$`), "replicate"},
	{regexp.MustCompile(`^key_[0-9a-f]{40,}$`), "runway"},
	{regexp.MustCompile(`^luma-[A-Za-z0-9_\-]{12,}$`), "luma"},
	{regexp.MustCompile(`^rt_live_[A-Za-z0-9_\-]{8,}$`), "royaltechno"},
	{regexp.MustCompile(`^fw_[A-Za-z0-9]{12,}$`), "fireworks"},
	{regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:[0-9a-f]{20,}$`), "fal"},
	{regexp.MustCompile(`^cg-[A-Za-z0-9_\-]{8,}$`), "cartelink"},
	// Plain "sk-…" keys are OpenAI's legacy shape but also DeepSeek's and
	// many gateways' — only the project/service-account shapes name OpenAI.
	{regexp.MustCompile(`^sk-(proj|svcacct|admin)-[A-Za-z0-9_\-]{20,}$`), "openai"},
}

func keyService(k string) string {
	for _, s := range assistKeyShapes {
		if s.re.MatchString(k) {
			return s.service
		}
	}
	return ""
}

// looksLikeKey: a long token with letters and digits (and not a URL, a
// model id or a word).
func looksLikeKey(tok string, afterKeyWord bool) bool {
	if keyService(tok) != "" {
		return true
	}
	if strings.Contains(tok, "://") || strings.Count(tok, ".") > 2 || reModelLike.MatchString(tok) {
		return false
	}
	letters, digits := 0, 0
	for _, r := range tok {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letters++
		}
	}
	if afterKeyWord {
		return len(tok) >= 12 && letters > 0 && digits > 0
	}
	return len(tok) >= 24 && letters >= 4 && digits >= 4
}

// roleHint is the role a line's words name ("" = none or several).
func roleHint(line string) string {
	low := strings.ToLower(line)
	hits := map[string]bool{}
	for role, words := range map[string][]string{
		RoleText:  {"текст", "сценар", "llm", "chat", "чат", "gpt", "мовна модель", "text"},
		RoleImage: {"кадр", "зображ", "картин", "фото", "image", "picture", "img"},
		RoleVideo: {"відео", "video", "кліп", "анімац"},
		RoleTTS:   {"озвуч", "голос", "tts", "voice", "speech", "диктор", "speak"},
		RoleSTT:   {"розпізн", "транскри", "stt", "whisper", "субтитр", "transcri", "scribe"},
	} {
		for _, w := range words {
			if strings.Contains(low, w) {
				hits[role] = true
				break
			}
		}
	}
	if hits[RoleSTT] {
		delete(hits, RoleTTS) // «розпізнавання голосу» is STT
	}
	if len(hits) != 1 {
		return ""
	}
	for r := range hits {
		return r
	}
	return ""
}

func trimURL(u string) string {
	u = strings.TrimRight(u, ".,;:!?)]}>»\"'")
	return u
}

// ParseAssist finds the addresses, keys, models and voices in pasted text.
func ParseAssist(text string) []AssistItem {
	var items []AssistItem
	seen := map[string]bool{}
	add := func(it AssistItem) {
		k := it.Kind + "|" + it.value
		if it.value == "" || seen[k] {
			return
		}
		seen[k] = true
		items = append(items, it)
	}
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		n := i + 1
		hint := roleHint(line)
		taken := []string{} // spans already used (URLs)
		for _, m := range reAssistURL.FindAllString(line, -1) {
			u := trimURL(m)
			taken = append(taken, m)
			kind := "url"
			if reAssistDocs.MatchString(u) {
				kind = "docs"
			}
			add(AssistItem{Kind: kind, Display: httpdiag.URL(u), Line: n, value: u, role: hint})
		}
		rest := line
		for _, t := range taken {
			rest = strings.Replace(rest, t, " ", 1)
		}
		// Bare hosts ("gateway.example.com/v1", "127.0.0.1:8317").
		for _, m := range reAssistHost.FindAllStringSubmatch(rest, -1) {
			h := trimURL(m[1])
			if strings.Contains(h, "@") || reModelLike.MatchString(h) || !strings.Contains(h, ".") && !strings.HasPrefix(h, "localhost") {
				continue
			}
			low := strings.ToLower(h)
			if strings.HasSuffix(low, ".txt") || strings.HasSuffix(low, ".json") && !strings.Contains(low, "/") {
				continue
			}
			scheme := "https://"
			if strings.HasPrefix(low, "localhost") || strings.HasPrefix(low, "127.") || strings.HasPrefix(low, "[::1]") {
				scheme = "http://"
			}
			u := scheme + h
			kind := "url"
			if reAssistDocs.MatchString(u) {
				kind = "docs"
			}
			add(AssistItem{Kind: kind, Display: httpdiag.URL(u), Line: n, value: u, role: hint})
			rest = strings.Replace(rest, m[1], " ", 1)
		}
		// Tokens: keys, models, voices — by the word before them or their shape.
		locs := reAssistToken.FindAllStringIndex(rest, -1)
		for _, loc := range locs {
			tok := strings.TrimRight(rest[loc[0]:loc[1]], ".:")
			before := rest[:loc[0]]
			switch {
			case reKeyWord.MatchString(before) && looksLikeKey(tok, true), looksLikeKey(tok, false):
				add(AssistItem{Kind: "key", Display: httpdiag.Mask(tok), Service: keyService(tok), Line: n, value: tok, role: hint})
			case reModelWord.MatchString(before) || reModelLike.MatchString(tok):
				add(AssistItem{Kind: "model", Display: tok, Line: n, value: tok, role: hint})
			case reVoiceWord.MatchString(before):
				add(AssistItem{Kind: "voice", Display: tok, Line: n, value: tok, role: hint})
			}
		}
		// Short model ids after «модель» (e.g. "модель o3", "model qwen3:8b").
		for _, m := range regexp.MustCompile(`(?i)(?:модель|model)\s*[:=—-]?\s*([A-Za-z0-9][A-Za-z0-9_\-./:]{1,80})`).FindAllStringSubmatch(rest, -1) {
			if tok := strings.TrimRight(m[1], ".,;:"); !looksLikeKey(tok, false) {
				add(AssistItem{Kind: "model", Display: tok, Line: n, value: tok, role: hint})
			}
		}
		for _, m := range regexp.MustCompile(`(?i)(?:голос|voice(?:[_ ]?id)?)\s*[:=—-]?\s*([A-Za-z0-9][A-Za-z0-9_\-]{2,80})`).FindAllStringSubmatch(rest, -1) {
			if tok := m[1]; !looksLikeKey(tok, false) && !seen["model|"+tok] {
				add(AssistItem{Kind: "voice", Display: tok, Line: n, value: tok, role: hint})
			}
		}
	}
	for i := range items {
		if items[i].Kind == "url" || items[i].Kind == "docs" {
			if svc := hostService(items[i].value); svc != nil {
				items[i].Service = svc.name
			}
		}
		if items[i].Kind == "key" && items[i].Service != "" {
			if svc := serviceByID(items[i].Service); svc != nil {
				items[i].Service = svc.name
			}
		}
	}
	return items
}

// ---- 2. known services ------------------------------------------------------------------

// service is what the assistant knows about a provider.
type service struct {
	id, name string
	hosts    []string // host suffixes (with port for local programs)
	roles    []string // what it can do here
	// text: the protocol and API base; tts/stt: provider ids; image/video:
	// own-API presets; google: Google AI Studio keys; glabs: G-Labs Studio.
	textProto, textBase, textModel string
	tts, stt                       string
	sttBase, sttModel              string
	image, video                   string
	google, glabs                  bool
}

var services = []service{
	{id: "openai", name: "OpenAI", hosts: []string{"api.openai.com"}, roles: []string{RoleText, RoleTTS, RoleSTT, RoleImage},
		textProto: "openai", textBase: "https://api.openai.com/v1", textModel: "gpt-5-mini", tts: "openai", stt: "openai", sttBase: "https://api.openai.com/v1", image: providers.PresetOpenAI},
	{id: "anthropic", name: "Anthropic (Claude)", hosts: []string{"api.anthropic.com"}, roles: []string{RoleText},
		textProto: "anthropic", textBase: "https://api.anthropic.com/v1", textModel: "claude-sonnet-4-5"},
	{id: "openrouter", name: "OpenRouter", hosts: []string{"openrouter.ai"}, roles: []string{RoleText},
		textProto: "openai", textBase: "https://openrouter.ai/api/v1"},
	{id: "groq", name: "Groq", hosts: []string{"api.groq.com"}, roles: []string{RoleText, RoleSTT},
		textProto: "openai", textBase: "https://api.groq.com/openai/v1", stt: "openai", sttBase: "https://api.groq.com/openai/v1", sttModel: "whisper-large-v3"},
	{id: "xai", name: "xAI (Grok)", hosts: []string{"api.x.ai"}, roles: []string{RoleText}, textProto: "openai", textBase: "https://api.x.ai/v1"},
	{id: "deepseek", name: "DeepSeek", hosts: []string{"api.deepseek.com"}, roles: []string{RoleText}, textProto: "openai", textBase: "https://api.deepseek.com"},
	{id: "mistral", name: "Mistral", hosts: []string{"api.mistral.ai"}, roles: []string{RoleText}, textProto: "openai", textBase: "https://api.mistral.ai/v1"},
	{id: "together", name: "Together AI", hosts: []string{"api.together.ai", "api.together.xyz"}, roles: []string{RoleText}, textProto: "openai", textBase: "https://api.together.ai/v1"},
	{id: "fireworks", name: "Fireworks", hosts: []string{"api.fireworks.ai"}, roles: []string{RoleText}, textProto: "openai", textBase: "https://api.fireworks.ai/inference/v1"},
	{id: "google", name: "Google AI Studio", hosts: []string{"generativelanguage.googleapis.com", "aistudio.google.com", "ai.google.dev"},
		roles: []string{RoleText, RoleImage, RoleVideo}, google: true},
	{id: "ollama", name: "Ollama", hosts: []string{"localhost:11434", "127.0.0.1:11434", "ollama.com"}, roles: []string{RoleText}, textProto: "ollama"},
	{id: "azure", name: "Azure OpenAI", hosts: []string{".openai.azure.com", ".cognitiveservices.azure.com", ".services.ai.azure.com"}, roles: []string{RoleText}, textProto: "azure"},
	{id: "cartelink", name: "CarteLink", hosts: []string{"127.0.0.1:8317", "localhost:8317"}, roles: []string{RoleText}, textProto: "openai"},
	{id: "glabs", name: "G-Labs Studio", hosts: []string{"127.0.0.1:8765", "localhost:8765"}, roles: []string{RoleImage, RoleVideo}, glabs: true},
	{id: "elevenlabs", name: "ElevenLabs", hosts: []string{"api.elevenlabs.io", "elevenlabs.io"}, roles: []string{RoleTTS, RoleSTT}, tts: "elevenlabs", stt: "elevenlabs"},
	{id: "cartesia", name: "Cartesia", hosts: []string{"api.cartesia.ai", "cartesia.ai"}, roles: []string{RoleTTS}, tts: "cartesia"},
	{id: "lumean", name: "Lumean", hosts: []string{"api.lumean.app", "lumean.app"}, roles: []string{RoleTTS}, tts: "lumean"},
	{id: "fish", name: "Fish Audio", hosts: []string{"api.fish.audio", "fish.audio"}, roles: []string{RoleTTS}, tts: "fish"},
	{id: "minimax", name: "MiniMax", hosts: []string{"api.minimax.io", "api.minimaxi.com", "api.minimax.cn"}, roles: []string{RoleTTS, RoleVideo}, tts: "minimax", video: providers.PresetMiniMax},
	{id: "assemblyai", name: "AssemblyAI", hosts: []string{"api.assemblyai.com", "api.eu.assemblyai.com"}, roles: []string{RoleSTT}, stt: stt.ProviderAssembly},
	{id: "fal", name: "fal.ai", hosts: []string{"fal.run", "queue.fal.run", "fal.ai"}, roles: []string{RoleImage, RoleVideo}, image: providers.PresetFal, video: providers.PresetFal},
	{id: "replicate", name: "Replicate", hosts: []string{"api.replicate.com", "replicate.com"}, roles: []string{RoleImage, RoleVideo}, image: providers.PresetReplicate, video: providers.PresetReplicate},
	{id: "runway", name: "Runway", hosts: []string{"api.dev.runwayml.com", "runwayml.com"}, roles: []string{RoleVideo}, video: providers.PresetRunway},
	{id: "luma", name: "Luma", hosts: []string{"agents.lumalabs.ai", "api.lumalabs.ai", "lumalabs.ai"}, roles: []string{RoleVideo}, video: providers.PresetLuma},
	{id: "royaltechno", name: "RoyalTechno", hosts: []string{"api.royaltechno.cc", "royaltechno.cc"}, roles: []string{RoleImage, RoleVideo}, image: providers.PresetRoyal, video: providers.PresetRoyal},
}

func serviceByID(id string) *service {
	for i := range services {
		if services[i].id == id {
			return &services[i]
		}
	}
	return nil
}

// hostService is the known service of a URL (nil = unknown).
func hostService(raw string) *service {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	host := strings.ToLower(u.Host)
	for i := range services {
		for _, h := range services[i].hosts {
			if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) || (!strings.Contains(h, ":") && strings.HasSuffix(host, "."+h)) ||
				(!strings.Contains(h, ":") && strings.TrimSuffix(host, ":443") == h) {
				return &services[i]
			}
		}
	}
	return nil
}

// ---- grouping ---------------------------------------------------------------------------

// group is an address with the key, model and voice that go with it (or a
// key alone).
type group struct {
	url, docs  *AssistItem
	key        *AssistItem
	model      *AssistItem
	voice      *AssistItem
	role       string
	llmRole    string
	llmProto   string
	llmDocsFor string
}

func (g *group) roleWanted() string { return firstNonEmptyStr(g.role, g.llmRole) }

func groupItems(items []AssistItem) []*group {
	var gs []*group
	var last *group // the last address group
	byLine := map[int]*group{}
	for i := range items {
		it := &items[i]
		switch it.Kind {
		case "url", "docs":
			g := &group{role: it.role}
			if it.Kind == "docs" {
				g.docs = it
			} else {
				g.url = it
			}
			// A docs link on the line of an address documents it.
			if prev := byLine[it.Line]; prev != nil && ((prev.url != nil && it.Kind == "docs" && prev.docs == nil) || (prev.docs != nil && it.Kind == "url" && prev.url == nil)) {
				if it.Kind == "docs" {
					prev.docs = it
				} else {
					prev.url = it
				}
				continue
			}
			gs = append(gs, g)
			byLine[it.Line] = g
			last = g
		}
	}
	attach := func(it *AssistItem, slot func(g *group) **AssistItem) {
		if g := byLine[it.Line]; g != nil && *slot(g) == nil {
			*slot(g) = it
			return
		}
		// Under an address: the nearest address above without one.
		var best *group
		for _, g := range gs {
			line := 0
			if g.url != nil {
				line = g.url.Line
			} else if g.docs != nil {
				line = g.docs.Line
			}
			if line <= it.Line && *slot(g) == nil && (it.Kind != "key" || keyFits(it, g)) {
				best = g
			}
		}
		if best != nil && (it.Kind != "key" || it.Service == "" || best.url == nil || hostService(best.url.value) == nil ||
			hostService(best.url.value).name == it.Service) {
			*slot(best) = it
			return
		}
		if it.Kind == "key" {
			ng := &group{key: it, role: it.role}
			gs = append(gs, ng)
		}
	}
	_ = last
	for i := range items {
		it := &items[i]
		switch it.Kind {
		case "key":
			attach(it, func(g *group) **AssistItem { return &g.key })
		case "model":
			attach(it, func(g *group) **AssistItem { return &g.model })
		case "voice":
			attach(it, func(g *group) **AssistItem { return &g.voice })
		}
	}
	return gs
}

// keyFits: a key of a known service only joins that service's address (or an
// unknown one).
func keyFits(k *AssistItem, g *group) bool {
	if k.Service == "" || g.url == nil {
		return true
	}
	svc := hostService(g.url.value)
	return svc == nil || svc.name == k.Service
}

// ---- 3–5. analysis ----------------------------------------------------------------------

// candidate is one way to fill a role.
type candidate struct {
	role   string
	target string
	lines  []string
	from   int // the group's position (earlier = preferred)
	apply  func(c *config.Config)
	check  func(ctx context.Context) (bool, string)
	weak   bool // a guess from a model list: proposed off
	// fallback: taken only when nothing else fills the role (a Google key
	// pasted without a word about what it is for).
	fallback bool
}

// assistTimeouts (tests shrink them).
var (
	assistProbeTimeout = 20 * time.Second
	assistLLMTimeout   = 90 * time.Second
)

// AnalyzeConnections reads pasted text and returns a checked plan. progress
// (optional, any goroutine) gets short Ukrainian steps. Nothing is saved.
func (e *Engine) AnalyzeConnections(ctx context.Context, text string, progress func(string)) (*AssistPlan, error) {
	if progress == nil {
		progress = func(string) {}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("Встав сюди адреси, ключі, посилання на документацію або просто нотатки — помічник розбереться")
	}
	plan := &AssistPlan{Items: ParseAssist(text)}
	if len(plan.Items) == 0 {
		return plan, errors.New("Не знайшов тут ні адрес, ні ключів. Встав, наприклад: «https://api.example.com ключ sk-… модель …»")
	}
	progress("Розбираю, що ти вставив…")
	groups := groupItems(plan.Items)
	cfg := e.cfg.Get()

	// 4. The text model, when one works, classifies what is still unclear.
	if unclear(groups) {
		progress("Питаю текстову модель, що це за адреси (ключі їй не передаються)…")
		if note, err := e.assistLLM(ctx, text, plan.Items, groups); err == nil {
			plan.AI = note
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		} else {
			plan.Notes = append(plan.Notes, "Текстова модель не допомогла ("+clipRunes(firstLine(errText(err)), 120)+") — розібрав сам")
		}
	}

	var cands []*candidate
	for i, g := range groups {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		cs, notes := e.groupCandidates(ctx, cfg, g, i, progress)
		cands = append(cands, cs...)
		plan.Notes = append(plan.Notes, notes...)
	}
	plan.Notes = append(plan.Notes, leftovers(plan.Items, groups)...)

	// One candidate per role: the first strong one; the others are notes.
	picked := map[string]*candidate{}
	for _, c := range cands {
		cur := picked[c.role]
		if cur == nil || (cur.weak && !c.weak) || (cur.fallback && !c.fallback && !c.weak) {
			if cur != nil {
				plan.Notes = append(plan.Notes, AssistRoleLabel(cur.role)+": є й інший варіант — "+cur.target)
			}
			picked[c.role] = c
			continue
		}
		plan.Notes = append(plan.Notes, AssistRoleLabel(c.role)+": є й інший варіант — "+c.target)
	}
	if len(picked) == 0 {
		plan.Notes = append(plan.Notes, "Нічого з цього не вдалося застосувати — додай ключ до адреси або посилання на документацію API")
		return plan, nil
	}

	// 5. Checks, a few at a time.
	progress("Перевіряю кожне підключення…")
	steps := make([]*AssistStep, 0, len(picked))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for _, role := range assistRoles {
		c := picked[role]
		if c == nil {
			continue
		}
		st := &AssistStep{Role: role, Target: c.target, Lines: c.lines, Status: "skip", apply: c.apply}
		steps = append(steps, st)
		if c.check == nil {
			st.On = !c.weak
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			ok, msg := c.check(cctx)
			st.Msg = msg
			st.Status = "fail"
			if ok {
				st.Status = "ok"
			}
			st.On = ok && !c.weak
			// A check may have fixed the target (the address form, the model).
			st.Target, st.Lines, st.apply = c.target, c.lines, c.apply
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	plan.Steps = steps
	return plan, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// unclear: an address of an unknown service without a role, or a key nobody
// recognises — the text model may help.
func unclear(gs []*group) bool {
	for _, g := range gs {
		if g.docs != nil && g.roleWanted() == "" {
			return true
		}
		if g.url != nil && hostService(g.url.value) == nil && g.roleWanted() == "" && g.key == nil {
			return true
		}
		if g.url == nil && g.docs == nil && g.key != nil && g.key.Service == "" {
			return true
		}
	}
	return false
}

// candidateCfg is the settings with apply on top (for a check).
func (e *Engine) candidateCfg(apply func(c *config.Config)) func() config.Config {
	c := e.cfg.Get()
	apply(&c)
	c.Normalize()
	return func() config.Config { return c }
}

func maskKey(k string) string { return httpdiag.Mask(k) }

// groupCandidates turns one group into candidates (probing unknown addresses).
func (e *Engine) groupCandidates(ctx context.Context, cfg config.Config, g *group, pos int, progress func(string)) ([]*candidate, []string) {
	key, model, voice := "", "", ""
	if g.key != nil {
		key = g.key.value
	}
	if g.model != nil {
		model = g.model.value
	}
	if g.voice != nil {
		voice = g.voice.value
	}
	// A documentation link with a role and a working text model: a spec.
	if g.docs != nil && g.url == nil {
		if svc := hostService(g.docs.value); svc != nil && g.roleWanted() == "" {
			return e.knownCandidates(svc, g, key, model, voice, pos, ""), nil
		}
		return e.docsCandidates(ctx, g, key, model, voice, pos, progress)
	}
	var svc *service
	base := ""
	if g.url != nil {
		base = g.url.value
		svc = hostService(base)
	} else if g.key != nil && g.key.Service != "" {
		svc = serviceByID(keyID(g.key.value))
	}
	if svc != nil {
		return e.knownCandidates(svc, g, key, model, voice, pos, base), nil
	}
	if g.url == nil {
		if g.key != nil {
			return nil, []string{"Ключ " + g.key.Display + " — не впізнаю сервіс; додай до нього адресу API"}
		}
		return nil, nil
	}
	progress("Перевіряю " + hostOf(base) + "…")
	return e.probeCandidates(ctx, g, base, key, model, voice, pos)
}

func keyID(k string) string { return keyService(k) }

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// wants: the group's role allows role.
func (g *group) wants(role string) bool {
	r := g.roleWanted()
	return r == "" || r == role
}

// knownCandidates: the service's roles (only the one the line names, when it does).
func (e *Engine) knownCandidates(svc *service, g *group, key, model, voice string, pos int, base string) []*candidate {
	var out []*candidate
	for _, role := range svc.roles {
		if !g.wants(role) {
			continue
		}
		switch role {
		case RoleText:
			if svc.google {
				c := e.googleCandidate(RoleText, key, model, pos, svc)
				c.fallback = g.roleWanted() == ""
				out = append(out, c)
				continue
			}
			t := config.Text{Provider: svc.textProto, BaseURL: firstNonEmptyStr(svc.textBase, base), APIKey: key, Model: model}
			if base != "" && svc.textBase == "" {
				t.BaseURL = base
			}
			if svc.id == "cartelink" && base != "" {
				t.BaseURL = base
			}
			if t.Model == "" {
				t.Model = svc.textModel
			}
			out = append(out, e.textCandidate(t, svc.name, pos, false))
		case RoleImage, RoleVideo:
			switch {
			case svc.google:
				c := e.googleCandidate(role, key, model, pos, svc)
				c.fallback = g.roleWanted() == ""
				// A working frames/video service is not switched away silently.
				cur := e.cfg.Get()
				prov := cur.Image.Provider
				if role == RoleVideo {
					prov = cur.Video.Provider
				}
				c.weak = g.roleWanted() == "" && prov != "google" && (prov == "custom" || strings.TrimSpace(cur.Glabs.APIKey) != "")
				out = append(out, c)
			case svc.glabs:
				out = append(out, e.glabsCandidate(role, base, key, pos))
			default:
				preset := svc.image
				if role == RoleVideo {
					preset = svc.video
				}
				if preset == "" {
					continue
				}
				out = append(out, e.customCandidate(role, preset, "", key, model, "", svc.name, pos, false))
			}
		case RoleTTS:
			tb := ""
			if svc.id == "minimax" && base != "" && !strings.Contains(base, "minimax.io") {
				tb = originOf(base)
			}
			out = append(out, e.ttsCandidate(svc.tts, tb, key, "", voice, "", svc.name, pos, false))
		case RoleSTT:
			sb, sm := svc.sttBase, svc.sttModel
			out = append(out, e.sttCandidate(svc.stt, sb, key, sm, "", svc.name, pos, false))
		}
	}
	return out
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

// ---- candidates per role --------------------------------------------------------------------

// textModelPick chooses a model from a list: the configured one, then
// well-known good ones, then the first.
func textModelPick(cur string, models []string) string {
	if len(models) == 0 {
		return cur
	}
	if cur != "" && slices.Contains(models, cur) {
		return cur
	}
	for _, pref := range []string{"gemini-3.8-flash", "gemini-3.5-flash", "gpt-5-mini", "gpt-5", "claude-sonnet", "deepseek-chat", "deepseek-v4", "grok-4", "llama-3.3-70b", "qwen3"} {
		for _, m := range models {
			if strings.Contains(strings.ToLower(m), pref) {
				return m
			}
		}
	}
	for _, m := range models {
		low := strings.ToLower(m)
		if !strings.Contains(low, "embed") && !strings.Contains(low, "tts") && !strings.Contains(low, "whisper") && !strings.Contains(low, "image") {
			return m
		}
	}
	return models[0]
}

func (e *Engine) textCandidate(t config.Text, name string, pos int, weak bool) *candidate {
	c := &candidate{role: RoleText, from: pos, weak: weak}
	describe := func() {
		c.target = name + " · " + firstNonEmptyStr(t.Model, "модель — вибрати")
		c.lines = []string{"Протокол: " + llm.ProtocolLabel(t.Provider), "Адреса: " + t.BaseURL}
		if t.APIKey != "" {
			c.lines = append(c.lines, "Ключ: "+maskKey(t.APIKey))
		}
		if t.Auth != "" {
			c.lines = append(c.lines, "Авторизація: "+authLabel(t.Auth))
		}
		if t.Model != "" {
			c.lines = append(c.lines, "Модель: "+t.Model)
		}
		tt := t
		c.apply = func(cf *config.Config) {
			cf.Text = config.Text{Provider: tt.Provider, BaseURL: tt.BaseURL, APIKey: tt.APIKey, Model: tt.Model, Auth: tt.Auth, APIVersion: tt.APIVersion}
		}
	}
	describe()
	c.check = func(ctx context.Context) (bool, string) {
		// The model list first (it may pick the model), then one word.
		if t.Model == "" {
			if p, err := e.CheckText(ctx, t, false); err == nil {
				t.BaseURL, t.Auth = p.Conn.BaseURL, p.Conn.Auth
				t.Model = textModelPick(e.cfg.Get().Text.Model, p.Models)
			}
		}
		p, err := e.CheckText(ctx, t, true)
		if p.Conn.BaseURL != "" {
			t.BaseURL = p.Conn.BaseURL
		}
		if p.AuthSet {
			t.Auth = p.Conn.Auth
		}
		if p.Conn.APIVersion != "" {
			t.APIVersion = p.Conn.APIVersion
		}
		if err != nil && strings.Contains(err.Error(), "модель не вибрано") && len(p.Models) > 0 {
			t.Model = textModelPick("", p.Models)
			p, err = e.CheckText(ctx, t, true)
		}
		describe()
		if err != nil {
			return false, errText(err)
		}
		return true, p.Summary()
	}
	return c
}

func (e *Engine) googleCandidate(role, key, model string, pos int, svc *service) *candidate {
	c := &candidate{role: role, from: pos}
	c.target = "Google AI Studio"
	switch role {
	case RoleText:
		c.target += " · " + firstNonEmptyStr(model, e.cfg.Get().Google.StoryModel)
	case RoleImage:
		c.target += " · Nano Banana"
	case RoleVideo:
		c.target += " · Gemini Omni"
	}
	if key != "" {
		c.lines = append(c.lines, "Ключ Google: "+maskKey(key)+" (додається до списку)")
	}
	c.apply = func(cf *config.Config) {
		if key != "" && !slices.Contains(cf.Google.Keys, key) {
			cf.Google.Keys = append(append([]string(nil), cf.Google.Keys...), key)
		}
		switch role {
		case RoleText:
			cf.Text.Provider = "google"
			if model != "" {
				cf.Google.StoryModel = model
			}
		case RoleImage:
			cf.Image.Provider = "google"
		case RoleVideo:
			cf.Video.Provider = "google"
		}
	}
	c.check = func(ctx context.Context) (bool, string) {
		if key == "" && len(e.cfg.Get().Google.Keys) == 0 {
			return false, "Ключа Google немає — встав ключ AIza… чи AQ.… з aistudio.google.com"
		}
		get := func() config.Config {
			cf := e.cfg.Get()
			if key != "" {
				cf.Google.Keys = []string{key}
			}
			return cf
		}
		if err := gemini.New(get).Test(ctx); err != nil {
			return false, errText(err)
		}
		return true, "Ключ Google працює"
	}
	return c
}

func (e *Engine) glabsCandidate(role, base, key string, pos int) *candidate {
	c := &candidate{role: role, from: pos, target: "G-Labs Studio"}
	b := strings.TrimRight(originOf(base), "/")
	c.lines = []string{"Адреса: " + firstNonEmptyStr(b, "як зараз")}
	if key != "" {
		c.lines = append(c.lines, "Ключ: "+maskKey(key))
	}
	c.apply = func(cf *config.Config) {
		if b != "" {
			cf.Glabs.BaseURL = b
		}
		if key != "" {
			cf.Glabs.APIKey = key
		}
		if role == RoleImage {
			cf.Image.Provider = "glabs"
		} else {
			cf.Video.Provider = "glabs"
		}
	}
	c.check = func(ctx context.Context) (bool, string) { return glabs.New(e.candidateCfg(c.apply)).Health(ctx) }
	return c
}

// customCandidate: «Своє API» for frames or video (a preset, or the universal one with a spec).
func (e *Engine) customCandidate(role, preset, base, key, model, spec, name string, pos int, weak bool) *candidate {
	c := &candidate{role: role, from: pos, weak: weak}
	c.target = name
	if model != "" {
		c.target += " · " + model
	}
	c.lines = []string{"Своє API: " + name}
	if base != "" {
		c.lines = append(c.lines, "Адреса: "+base)
	}
	if key != "" {
		c.lines = append(c.lines, "Ключ: "+maskKey(key))
	}
	if model != "" {
		c.lines = append(c.lines, "Модель: "+model)
	}
	if spec != "" {
		c.lines = append(c.lines, "Опис API складено з документації")
	}
	c.apply = func(cf *config.Config) {
		if role == RoleImage {
			cf.Image.Provider = "custom"
			cf.Custom.ImagePreset, cf.Custom.ImageBaseURL, cf.Custom.ImageKey, cf.Custom.ImageModel = preset, base, key, model
			if spec != "" {
				cf.Custom.ImageSpec = spec
			}
		} else {
			cf.Video.Provider = "custom"
			cf.Custom.VideoPreset, cf.Custom.VideoBaseURL, cf.Custom.VideoKey, cf.Custom.VideoModel = preset, base, key, model
			if spec != "" {
				cf.Custom.VideoSpec = spec
			}
		}
	}
	c.check = func(ctx context.Context) (bool, string) {
		cl := providers.New(e.candidateCfg(c.apply))
		if role == RoleImage {
			return cl.TestImage(ctx)
		}
		return cl.TestVideo(ctx)
	}
	return c
}

func (e *Engine) ttsCandidate(provider, base, key, model, voice, spec, name string, pos int, weak bool) *candidate {
	c := &candidate{role: RoleTTS, from: pos, weak: weak}
	pi, _ := tts.ProviderByID(provider)
	c.target = firstNonEmptyStr(name, pi.Label)
	c.lines = []string{"Сервіс: " + firstNonEmptyStr(pi.Label, provider)}
	if base != "" {
		c.lines = append(c.lines, "Адреса: "+base)
	}
	if key != "" {
		c.lines = append(c.lines, "Ключ: "+maskKey(key)+" (додається до ключів сервісу)")
	}
	if model != "" {
		c.lines = append(c.lines, "Модель: "+model)
	}
	if voice != "" {
		c.lines = append(c.lines, "Голос: "+voice)
	}
	if spec != "" {
		c.lines = append(c.lines, "Опис API складено з документації")
	}
	c.apply = func(cf *config.Config) {
		cf.TTS.Switch(provider)
		if key != "" {
			cf.TTS.SetKeys(append(cf.TTS.Active().AllKeys(), key))
		}
		if base != "" {
			cf.TTS.BaseURL = base
		}
		if model != "" {
			cf.TTS.Model = model
		}
		if voice != "" {
			cf.TTS.Voice = voice
		}
		if spec != "" {
			cf.Custom.TTSSpec = spec
		}
	}
	c.check = func(ctx context.Context) (bool, string) {
		get := e.candidateCfg(func(cf *config.Config) {
			c.apply(cf)
			if key != "" {
				cf.TTS.SetKeys([]string{key}) // check the new key alone
			}
		})
		return tts.New(get).Test(ctx)
	}
	return c
}

func (e *Engine) sttCandidate(provider, base, key, model, spec, name string, pos int, weak bool) *candidate {
	c := &candidate{role: RoleSTT, from: pos, weak: weak, target: name}
	c.lines = []string{"Сервіс: " + name}
	if base != "" {
		c.lines = append(c.lines, "Адреса: "+base)
	}
	if key != "" {
		c.lines = append(c.lines, "Ключ: "+maskKey(key))
	}
	if model != "" {
		c.lines = append(c.lines, "Модель: "+model)
	}
	if spec != "" {
		c.lines = append(c.lines, "Опис API складено з документації")
	}
	c.apply = func(cf *config.Config) {
		cf.STT.Provider = provider
		switch provider {
		case stt.ProviderAssembly:
			if key != "" {
				cf.STT.AssemblyKey = key
			}
		default:
			cf.STT.BaseURL, cf.STT.Model = base, model
			if key != "" {
				cf.STT.APIKey = key
			}
		}
		if spec != "" {
			cf.Custom.STTSpec = spec
		}
	}
	c.check = func(ctx context.Context) (bool, string) { return stt.New(e.candidateCfg(c.apply)).Test(ctx) }
	return c
}

// ---- probing an unknown address -------------------------------------------------------------

// probeCandidates asks an unknown address cheap GETs to learn what it is.
func (e *Engine) probeCandidates(ctx context.Context, g *group, base, key, model, voice string, pos int) ([]*candidate, []string) {
	pctx, cancel := context.WithTimeout(ctx, assistProbeTimeout*3)
	defer cancel()
	low := strings.ToLower(base)
	host := hostOf(base)
	// The path may say what it is.
	switch {
	case strings.Contains(low, "/audio/speech"):
		b := base[:strings.Index(low, "/audio/speech")]
		return []*candidate{e.ttsCandidate("openai", b, key, model, voice, "", host+" (OpenAI-сумісна озвучка)", pos, false)}, nil
	case strings.Contains(low, "/audio/transcriptions"):
		b := base[:strings.Index(low, "/audio/transcriptions")]
		return []*candidate{e.sttCandidate("openai", b, key, firstNonEmptyStr(model, "whisper-1"), "", host+" (Whisper-сумісний)", pos, false)}, nil
	case strings.Contains(low, "/images/generations"):
		b := base[:strings.Index(low, "/images/generations")]
		return []*candidate{e.customCandidate(RoleImage, providers.PresetOpenAICompat, b, key, model, "", host+" (OpenAI-сумісні кадри)", pos, false)}, nil
	case strings.Contains(low, "/text-to-speech"):
		tpl, _ := providers.FindSpecTemplateFor(providers.KindTTS, "elevenlabs")
		return []*candidate{e.ttsCandidate("custom", originOf(base), key, model, voice, tpl.Spec, host+" (ElevenLabs-сумісний)", pos, false)}, nil
	}
	var out []*candidate
	var notes []string
	if g.wants(RoleText) || g.wants(RoleTTS) || g.wants(RoleSTT) || g.wants(RoleImage) {
		proto := firstNonEmptyStr(g.llmProto, "openai")
		if proto != "openai" && proto != "anthropic" && proto != "ollama" && proto != "azure" {
			proto = "openai"
		}
		t := config.Text{Provider: proto, BaseURL: base, APIKey: key, Model: model}
		p, err := e.CheckText(pctx, t, false)
		if err != nil && proto == "openai" {
			// Anthropic-style (x-api-key + anthropic-version) — only after a refusal.
			var ce *llm.CheckError
			if errors.As(err, &ce) && strings.Contains(err.Error(), "відхилив ключ") {
				ta := t
				ta.Provider = "anthropic"
				if pa, erra := e.CheckText(pctx, ta, false); erra == nil {
					t, p, err = ta, pa, nil
				}
			}
		}
		switch {
		case err == nil:
			t.BaseURL, t.Auth = p.Conn.BaseURL, p.Conn.Auth
			if t.Model == "" {
				t.Model = textModelPick(e.cfg.Get().Text.Model, p.Models)
			}
			name := host
			if g.wants(RoleText) && !onlyAudio(p.Models) {
				out = append(out, e.textCandidate(t, name, pos, g.roleWanted() == ""))
				out[len(out)-1].weak = false
			}
			// What else the gateway's models can do (proposed off: unverified).
			if hasModel(p.Models, "tts", "speech") && g.wants(RoleTTS) {
				out = append(out, e.ttsCandidate("openai", t.BaseURL, key, firstModel(p.Models, "tts", "speech"), voice, "", name+" (OpenAI-сумісна озвучка)", pos, g.roleWanted() != RoleTTS))
			}
			if hasModel(p.Models, "whisper", "transcribe") && g.wants(RoleSTT) {
				out = append(out, e.sttCandidate("openai", t.BaseURL, key, firstModel(p.Models, "whisper"), "", name+" (Whisper-сумісний)", pos, g.roleWanted() != RoleSTT))
			}
			if hasModel(p.Models, "image", "dall-e", "flux", "imagen", "nano-banana", "seedream") && g.wants(RoleImage) {
				out = append(out, e.customCandidate(RoleImage, providers.PresetOpenAICompat, t.BaseURL, key, firstModel(p.Models, "image", "dall-e", "flux", "imagen", "nano-banana", "seedream"), "", name+" (OpenAI-сумісні кадри)", pos, g.roleWanted() != RoleImage))
			}
			if len(out) > 0 {
				return out, nil
			}
		case strings.Contains(err.Error(), "відхилив ключ") || strings.Contains(err.Error(), "вимагає ключ"):
			// The server is an OpenAI-compatible API that refuses the key: say so
			// with the exact request (a failing step the user can fix).
			if g.wants(RoleText) {
				c := e.textCandidate(t, host, pos, false)
				msg := errText(err)
				c.check = func(context.Context) (bool, string) { return false, msg }
				return []*candidate{c}, nil
			}
		default:
			notes = append(notes, host+": не схоже на OpenAI-сумісне API — "+clipRunes(firstLine(errText(err)), 140))
		}
	}
	origin := originOf(base)
	// Ollama.
	if g.wants(RoleText) {
		if ok, _ := e.probeGET(pctx, origin+"/api/tags", nil, "models"); ok {
			t := config.Text{Provider: "ollama", BaseURL: origin, APIKey: key, Model: model}
			return []*candidate{e.textCandidate(t, host+" (Ollama)", pos, false)}, nil
		}
	}
	// ElevenLabs-compatible voices.
	if g.wants(RoleTTS) && key != "" {
		if ok, _ := e.probeGET(pctx, origin+"/v1/voices", map[string]string{"xi-api-key": key}, "voices"); ok {
			tpl, _ := providers.FindSpecTemplateFor(providers.KindTTS, "elevenlabs")
			return []*candidate{e.ttsCandidate("custom", origin, key, model, voice, tpl.Spec, host+" (ElevenLabs-сумісний)", pos, false)}, nil
		}
	}
	// G-Labs Studio.
	if g.wants(RoleImage) || g.wants(RoleVideo) {
		if ok, _ := e.probeGET(pctx, origin+"/api/health", nil, ""); ok {
			return []*candidate{e.glabsCandidate(RoleImage, origin, key, pos), e.glabsCandidate(RoleVideo, origin, key, pos)}, nil
		}
	}
	if g.docs != nil {
		cs, n := e.docsCandidates(ctx, g, key, model, voice, pos, func(string) {})
		return cs, append(notes, n...)
	}
	notes = append(notes, "Не зрозумів, що за сервіс "+host+" — додай посилання на його документацію API, і ШІ складе опис")
	return nil, notes
}

func onlyAudio(models []string) bool {
	if len(models) == 0 {
		return false
	}
	for _, m := range models {
		low := strings.ToLower(m)
		if !strings.Contains(low, "tts") && !strings.Contains(low, "whisper") && !strings.Contains(low, "speech") && !strings.Contains(low, "transcri") {
			return false
		}
	}
	return true
}

func hasModel(models []string, subs ...string) bool { return firstModel(models, subs...) != "" }

func firstModel(models []string, subs ...string) string {
	for _, m := range models {
		low := strings.ToLower(m)
		for _, s := range subs {
			if strings.Contains(low, s) {
				return m
			}
		}
	}
	return ""
}

// probeGET is one cheap GET: ok when it answers 2xx JSON with field (""
// = any JSON object).
func (e *Engine) probeGET(ctx context.Context, u string, hdr map[string]string, field string) (bool, int) {
	rctx, cancel := context.WithTimeout(ctx, assistProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
	if err != nil {
		return false, 0
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, 0
	}
	defer res.Body.Close()
	var m map[string]any
	if res.StatusCode < 200 || res.StatusCode > 299 || json.NewDecoder(res.Body).Decode(&m) != nil {
		return false, res.StatusCode
	}
	if field == "" {
		return true, res.StatusCode
	}
	_, ok := m[field]
	return ok, res.StatusCode
}

// docsCandidates: a documentation link → a universal spec for its role
// (the text model reads the docs; nothing is saved).
func (e *Engine) docsCandidates(ctx context.Context, g *group, key, model, voice string, pos int, progress func(string)) ([]*candidate, []string) {
	role := firstNonEmptyStr(g.llmDocsFor, g.roleWanted())
	docs := g.docs.value
	if role == "" || role == RoleText {
		return nil, []string{"Документація " + hostOf(docs) + ": не зрозумів, для чого вона (кадри, відео, озвучка чи розпізнавання) — допиши це словом у тому ж рядку"}
	}
	kind := map[string]providers.SpecKind{RoleImage: providers.KindImage, RoleVideo: providers.KindVideo, RoleTTS: providers.KindTTS, RoleSTT: providers.KindSTT}[role]
	progress("ШІ читає документацію " + hostOf(docs) + " (" + AssistRoleLabel(role) + ")…")
	res, err := e.SuggestSpec(ctx, kind, docs, progress)
	if err != nil {
		return nil, []string{"Документація " + hostOf(docs) + ": " + clipRunes(firstLine(errText(err)), 160)}
	}
	if len(res.Problems) > 0 {
		return nil, []string{"Документація " + hostOf(docs) + ": ШІ склав опис з помилками (" + strings.Join(res.Problems, "; ") + ") — доправ його в «Власний сервер» → Опис API"}
	}
	base := ""
	if g.url != nil {
		base = strings.TrimRight(g.url.value, "/")
	}
	base = firstNonEmptyStr(base, res.BaseURL)
	model = firstNonEmptyStr(model, res.Model)
	name := hostOf(firstNonEmptyStr(base, docs)) + " (опис з документації)"
	var c *candidate
	switch role {
	case RoleImage, RoleVideo:
		c = e.customCandidate(role, providers.PresetUniversal, base, key, model, res.Spec, name, pos, false)
	case RoleTTS:
		c = e.ttsCandidate("custom", base, key, model, voice, res.Spec, name, pos, false)
	case RoleSTT:
		c = e.sttCandidate(stt.ProviderCustom, base, key, model, res.Spec, name, pos, false)
	}
	var notes []string
	for _, n := range res.Notes {
		notes = append(notes, AssistRoleLabel(role)+": "+n)
	}
	return []*candidate{c}, notes
}

// leftovers are notes on models and voices no address took.
func leftovers(items []AssistItem, gs []*group) []string {
	used := map[*AssistItem]bool{}
	for _, g := range gs {
		used[g.model], used[g.voice] = true, true
	}
	var out []string
	for i := range items {
		it := &items[i]
		if (it.Kind == "model" || it.Kind == "voice") && !used[it] {
			what := "Модель"
			if it.Kind == "voice" {
				what = "Голос"
			}
			out = append(out, what+" «"+it.Display+"» — не зрозумів, до якої адреси; впиши її в тому ж рядку")
		}
	}
	return out
}

// ---- 4. the text model's help ---------------------------------------------------------------

// assistLLM asks the configured text model what the unclear items are. It
// sees the text with every key replaced by [KEY1] … and every stored secret
// masked; its answers only fill what heuristics left open.
func (e *Engine) assistLLM(ctx context.Context, text string, items []AssistItem, gs []*group) (string, error) {
	cfg := e.cfg.Get()
	if cfg.Text.Provider == "google" && len(cfg.Google.Keys) == 0 {
		return "", errors.New("текстову модель не налаштовано")
	}
	masked, refs := maskForLLM(text, items, configSecrets(cfg))
	var b strings.Builder
	b.WriteString("A user pasted connection details for a video-making app. Keys are already replaced by [KEYn]; never guess them.\n")
	b.WriteString("Roles: text (LLM for scripts), image (still frames), video (clips), tts (voice-over), stt (speech-to-text).\n")
	b.WriteString("For each URL ref, say which role it serves, the API protocol (openai = OpenAI-compatible, anthropic, ollama, azure, elevenlabs, other),\n")
	b.WriteString("which KEY ref belongs to it, the model id if one is named, and for documentation links which role the API is for.\n")
	b.WriteString("Reply with ONLY JSON: {\"urls\":[{\"ref\":\"URL1\",\"role\":\"text|image|video|tts|stt|unknown\",\"protocol\":\"…\",\"key\":\"KEY1|\",\"model\":\"\",\"docs_for\":\"\"}],\"notes\":[\"short Ukrainian notes, informal ти\"]}\n\n")
	b.WriteString("Pasted text:\n")
	b.WriteString(masked)
	var out struct {
		URLs []struct {
			Ref, Role, Protocol, Key, Model string
			DocsFor                         string `json:"docs_for"`
		} `json:"urls"`
		Notes []string `json:"notes"`
	}
	lctx, cancel := context.WithTimeout(ctx, assistLLMTimeout)
	defer cancel()
	if err := e.text.JSON(lctx, llm.Request{Prompt: b.String(), Temperature: 0, Timeout: assistLLMTimeout}, &out); err != nil {
		return "", err
	}
	used := 0
	for _, u := range out.URLs {
		val, ok := refs[u.Ref]
		if !ok {
			continue
		}
		for _, g := range gs {
			var it *AssistItem
			if g.url != nil && g.url.value == val {
				it = g.url
			} else if g.docs != nil && g.docs.value == val {
				it = g.docs
			}
			if it == nil {
				continue
			}
			if slices.Contains(assistRoles, u.Role) && g.role == "" {
				g.llmRole = u.Role
				used++
			}
			g.llmProto = strings.ToLower(strings.TrimSpace(u.Protocol))
			if slices.Contains(assistRoles, u.DocsFor) {
				g.llmDocsFor = u.DocsFor
			}
			if k, ok := refs[u.Key]; ok && g.key == nil {
				for i := range items {
					if items[i].Kind == "key" && items[i].value == k {
						g.key = &items[i]
					}
				}
			}
			if m := strings.TrimSpace(u.Model); m != "" && g.model == nil && len(m) < 120 {
				g.model = &AssistItem{Kind: "model", Display: m, value: m}
			}
		}
	}
	if used == 0 {
		return "ШІ переглянув вставлене, нового не додав", nil
	}
	return fmt.Sprintf("ШІ підказав призначення %s", countUK(used, "адреси", "адрес", "адрес")), nil
}

func countUK(n int, one, few, many string) string {
	m := n % 100
	switch {
	case m/10 != 1 && m%10 == 1:
		return fmt.Sprintf("%d %s", n, one)
	case m/10 != 1 && m%10 >= 2 && m%10 <= 4:
		return fmt.Sprintf("%d %s", n, few)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// maskForLLM replaces keys with [KEYn] and URLs with [URLn] (the URL kept in
// clear after the ref, its key-like query parameters masked) and masks every
// stored secret. refs maps a ref to the value.
func maskForLLM(text string, items []AssistItem, secrets []string) (string, map[string]string) {
	refs := map[string]string{}
	type rep struct{ from, to string }
	var reps []rep
	nk, nu := 0, 0
	for _, it := range items {
		switch it.Kind {
		case "key":
			nk++
			ref := fmt.Sprintf("KEY%d", nk)
			refs[ref] = it.value
			reps = append(reps, rep{it.value, "[" + ref + "]"})
		case "url", "docs":
			nu++
			ref := fmt.Sprintf("URL%d", nu)
			refs[ref] = it.value
			reps = append(reps, rep{it.value, "[" + ref + ": " + httpdiag.URL(it.value) + "]"})
		}
	}
	sort.SliceStable(reps, func(i, j int) bool { return len(reps[i].from) > len(reps[j].from) })
	out := text
	for _, r := range reps {
		out = strings.ReplaceAll(out, r.from, "\x00"+r.to+"\x00")
	}
	out = strings.ReplaceAll(out, "\x00", "")
	out = maskSecrets(out, secrets)
	out = httpdiag.Redact(out)
	return clipRunes(out, 8000), refs
}

// ---- 6. applying ------------------------------------------------------------------------------

// AssistResult is what ApplyAssist did.
type AssistResult struct {
	Applied []string // role labels
	Backup  config.Backup
}

// ApplyAssist saves a backup of the settings and profiles, then applies the
// steps that are on in one settings change.
func (e *Engine) ApplyAssist(plan *AssistPlan) (AssistResult, error) {
	var res AssistResult
	steps := plan.AssistOn()
	if len(steps) == 0 {
		return res, errors.New("Не вибрано жодного кроку")
	}
	if e.closing.Load() {
		return res, ErrClosing
	}
	bk, err := e.cfg.SaveBackup(e.profiles.List(), time.Now())
	if err != nil {
		return res, fmt.Errorf("нічого не змінено — не вдалося зберегти копію поточних налаштувань: %w", err)
	}
	res.Backup = bk
	if err := e.cfg.Update(func(c *config.Config) {
		for _, s := range steps {
			if s.apply != nil {
				s.apply(c)
			}
		}
	}); err != nil {
		return res, err
	}
	for _, s := range steps {
		res.Applied = append(res.Applied, AssistRoleLabel(s.Role))
	}
	return res, nil
}
