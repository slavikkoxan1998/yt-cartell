package studio

// Failure texts: what a failed generation shows (scene cards, the produce
// screen, the queue, the autopilot summary, Telegram). The rule is the
// owner's: the EXACT error of the service — its name, the HTTP status / code
// and its own message verbatim — never a generic "не вдалося". Keys and
// tokens are masked; a short Ukrainian hint (FailureHint) is added by the
// screens and the Telegram message, never stored in place of the message.

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"ytcartell/internal/config"
)

// maxAssetError is how much of a failure an asset keeps (the services'
// messages are short; a JSON body quoted by a gateway may be longer).
const maxAssetError = 1500

// failureText is the stored text of a failed generation: the error exactly
// as the service reported it (errText), secrets masked.
func (e *Engine) failureText(err error) string {
	return clipRunes(e.redact(errText(err)), maxAssetError)
}

// secretKeyRe matches settings fields that hold a secret (by JSON name).
var secretKeyRe = regexp.MustCompile(`(?i)(key|token|secret|session|password)`)

// Shapes of keys that may sit in an error message even when they are not in
// the settings (a gateway echoing a header, a URL with ?key=).
var (
	keyShapes = regexp.MustCompile(`\b(sk-[A-Za-z0-9_\-]{12,}|sk_[A-Za-z0-9_\-]{12,}|AIza[0-9A-Za-z_\-]{30,}|AQ\.[A-Za-z0-9_\-]{20,}|r8_[A-Za-z0-9]{20,}|key-[A-Za-z0-9]{20,})`)
	keyParams = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|token|access_token)=)[^&\s"']+`)
	bearerRe  = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{8,}`)
)

// redact masks every secret of the settings (and anything shaped like a key)
// in s.
func (e *Engine) redact(s string) string {
	if s == "" {
		return s
	}
	var secrets []string
	if e != nil && e.cfg != nil {
		secrets = settingsSecrets(e.cfg.Get())
	}
	return redactSecrets(s, secrets)
}

// redactSecrets masks secrets (exact values, longest first) and key shapes.
func redactSecrets(s string, secrets []string) string {
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	for _, k := range secrets {
		if len(k) >= 8 && strings.Contains(s, k) {
			s = strings.ReplaceAll(s, k, config.Mask(k))
		}
	}
	s = keyShapes.ReplaceAllStringFunc(s, config.Mask)
	s = keyParams.ReplaceAllString(s, "${1}•••")
	return bearerRe.ReplaceAllString(s, "${1}•••")
}

// settingsSecrets lists the secret values of c: every string (or list of
// strings) stored under a JSON name that says key, token, secret, session
// or password — new connections are covered without a list to maintain.
func settingsSecrets(c config.Config) []string {
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var root any
	if json.Unmarshal(b, &root) != nil {
		return nil
	}
	var out []string
	var walk func(v any, secret bool)
	walk = func(v any, secret bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				walk(vv, secret || secretKeyRe.MatchString(k))
			}
		case []any:
			for _, vv := range x {
				walk(vv, secret)
			}
		case string:
			if secret && len(strings.TrimSpace(x)) >= 8 {
				out = append(out, strings.TrimSpace(x))
			}
		}
	}
	walk(root, false)
	return out
}

// FailureHint is a short Ukrainian hint for a failure message (the exact
// text of a provider error, e.g. an asset's Error): what the code means and
// what to change. "" when the message needs no hint or already has one.
func FailureHint(msg string) string {
	m := strings.ToUpper(msg)
	switch {
	case strings.TrimSpace(msg) == "", strings.Contains(msg, "Підказка:"):
		return ""
	case strings.Contains(m, "PROMINENT_PERSON"):
		return "фільтр Google побачив упізнавану реальну людину (знаменитість, політик). Зроби героя вигаданим: прибери імена реальних людей з опису й промптів і перемалюй референс, якщо він схожий на когось відомого."
	case strings.Contains(m, "REPUTATIONAL"):
		return "фільтр Google вважає сцену репутаційно чутливою (реальні події, політика, трагедії, бренди). Пом'якши опис кадру й руху: без реальних імен, дат і закликів."
	case strings.Contains(m, "DANGEROUS"):
		return "фільтр Google побачив небезпечні дії чи насильство. Пом'якши дію в описі руху (без зброї, крові, травм) і перезніми."
	case strings.Contains(m, "UNSAFE_GENERATION"), strings.Contains(m, "SEXUAL"), strings.Contains(m, "CHILD"), strings.Contains(m, "VIOLEN"):
		return "фільтр безпеки Google відхилив результат. Зміни промпт або референси (менше насильства, без дітей у небезпеці, без відвертих сцен)."
	case strings.Contains(m, "INVALID_ARGUMENT"):
		return "Google відхилив запит ще до генерації — найчастіше через вміст промпту чи референсів (знаменитості, насильство, діти, реальні події). Спрости опис руху або перемалюй кадр; розкадровка 3×3 відхиляється частіше за один кадр."
	case strings.Contains(m, "RESOURCE_EXHAUSTED"), strings.Contains(m, "QUOTA"), strings.Contains(m, "[429]"), strings.Contains(m, " 429"):
		if strings.Contains(msg, " — ") {
			return "" // the provider's message already says what to do
		}
		return "вичерпано ліміт або кредити сервісу — зачекай або додай ще акаунт/ключ."
	case strings.Contains(m, "UPLOAD_ERROR"), strings.Contains(m, "INTERNAL"), strings.Contains(m, "[500]"), strings.Contains(m, "[502]"), strings.Contains(m, "[503]"), strings.Contains(m, "UNAVAILABLE"):
		if strings.Contains(msg, "тимчасовий збій") {
			return ""
		}
		return "тимчасовий збій на боці сервісу — просто повтори."
	case strings.Contains(m, "TIMEOUT"), strings.Contains(msg, "не відповів вчасно"), strings.Contains(msg, "не встиг"):
		return "сервіс не встиг відповісти — повтори; якщо так щоразу, обери коротший кліп або іншу модель."
	case strings.Contains(m, "[401]"), strings.Contains(m, "UNAUTHENTICATED"), strings.Contains(m, "API_KEY_INVALID"):
		return "сервіс не прийняв ключ — перевір його в Налаштуваннях → Підключення."
	case strings.Contains(m, "[403]") && !strings.Contains(msg, " — "):
		return "немає доступу: перевір ключ, тариф або сесію акаунта в сервісі."
	}
	return ""
}
