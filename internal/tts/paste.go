package tts

// Reading pasted keys (Settings → Озвучка and Google AI Studio, «Додати
// кілька»): one key per line or several separated by commas, spaces or
// semicolons; labels such as "key1: sk_…", "ELEVENLABS_API_KEY=sk_…", "1) sk_…"
// or a heading line are read past, quotes and brackets trimmed. A piece that
// looks like a damaged key counts as "не схожий на ключ"; a key of another
// provider (an ElevenLabs sk_… key pasted under Cartesia or Google) is refused
// and counted. ParseKeys reads a TTS provider's list, ParseGoogleKeys the
// Google AI Studio keys (AQ.… or AIza…), ParseKeyList any list.

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"ytcartell/internal/config"
)

// KeyPaste is what a paste adds to the key list.
type KeyPaste struct {
	Keys    []string // new keys, in paste order
	Dup     int      // already in the list (or repeated in the paste)
	Junk    int      // pieces that look like a damaged key
	Foreign int      // keys of another provider (not added)
	// ForeignID is the provider of the first foreign key ("elevenlabs").
	ForeignID string
}

var (
	// A key: URL-safe/base64 characters, "=" only as padding at the end.
	reKeyChars = regexp.MustCompile(`^[A-Za-z0-9._~+/\-]+=*$`)
	reDigit    = regexp.MustCompile(`[0-9]`)
	keyPrefix  = []string{"sk_", "sk-", "AIza", "AQ."}
)

// minKeyLen is the shortest key a provider is expected to have (an own
// server's token may be short).
func minKeyLen(provider string) int {
	if provider == "custom" {
		return 6
	}
	return 20
}

// looksLikeKey: one token of key characters, long enough, with a digit (or
// mixed case — random keys have them).
func looksLikeKey(tok, provider string) bool {
	if len(tok) < minKeyLen(provider) || len(tok) > 4096 || !reKeyChars.MatchString(tok) {
		return false
	}
	if strings.Contains(tok, "..") || strings.HasPrefix(strings.ToLower(tok), "www.") {
		return false
	}
	if config.TTSKeyOwner(tok) != "" || reDigit.MatchString(tok) {
		return true
	}
	return len(tok) >= 24 && strings.IndexFunc(tok, unicode.IsUpper) >= 0 && strings.IndexFunc(tok, unicode.IsLower) >= 0
}

// attemptedKey: a token that was meant as a key (a key prefix, or key
// characters with digits and "_"/"-" or several digits) but is too short or
// broken — not a plain label word.
func attemptedKey(tok string) bool {
	for _, p := range keyPrefix {
		if strings.HasPrefix(tok, p) && len(tok) > len(p) {
			return true
		}
	}
	if len(tok) < 8 || !reKeyChars.MatchString(tok) {
		return false
	}
	digits := len(reDigit.FindAllString(tok, -1))
	return digits >= 2 || (digits >= 1 && strings.ContainsAny(tok, "_-"))
}

// cleanToken strips quotes, brackets and trailing punctuation.
func cleanToken(s string) string {
	s = strings.Trim(s, "\"'`«»“”„‘’()[]{}<>")
	s = strings.TrimRight(s, ".,;:!?")
	return strings.Trim(s, "\"'`«»“”„‘’()[]{}<>")
}

// keyOf takes the key out of one pasted token ("" = none): the token itself,
// or the value of "label=value" / "label:value".
func keyOf(tok, provider string) (key string, junk bool) {
	tok = cleanToken(tok)
	if tok == "" {
		return "", false
	}
	if looksLikeKey(tok, provider) {
		return tok, false
	}
	if i := strings.IndexAny(tok, "=:"); i > 0 && !strings.Contains(tok, "://") {
		if v := cleanToken(tok[i+1:]); v != "" {
			if looksLikeKey(v, provider) {
				return v, false
			}
			return "", attemptedKey(v)
		}
		return "", false // "key1:" — a label
	}
	return "", attemptedKey(tok)
}

// ParseKeys reads pasted text for provider (gateway: an OpenAI-compatible
// server or own server, whose keys may have any shape) against the keys
// already in the list.
func ParseKeys(text, provider string, gateway bool, existing []string) KeyPaste {
	provider = normProvider(provider)
	return ParseKeyList(text, PasteRule{Owner: provider, AnyShape: provider == "custom" || (provider == "openai" && gateway)}, existing)
}

// PasteRule says which keys a list takes.
type PasteRule struct {
	// Owner is the provider id the list belongs to: a key whose shape surely
	// belongs to another one (config.TTSKeyOwner) is refused as foreign.
	Owner string
	// AnyShape: an own server or a gateway — any key-like token, short ones
	// too, and no key is foreign.
	AnyShape bool
	// Accept, when set, must take a key-like token; refused ones count as
	// "не схожий на ключ" (Google: only AQ.… or AIza… keys).
	Accept func(key string) bool
}

// GoogleKeysLabel names the Google AI Studio key list in summaries.
const GoogleKeysLabel = "Google AI Studio"

// ParseGoogleKeys reads pasted Google AI Studio (Gemini API) keys: the new
// auth keys "AQ.…" or the legacy "AIza" + 35 characters; other providers'
// keys are refused, broken ones counted.
func ParseGoogleKeys(text string, existing []string) KeyPaste {
	return ParseKeyList(text, PasteRule{Owner: "google", Accept: func(k string) bool { return config.TTSKeyOwner(k) == "google" }}, existing)
}

// ParseKeyList reads pasted text by rule against the keys already in the list.
func ParseKeyList(text string, rule PasteRule, existing []string) KeyPaste {
	provider := rule.Owner
	var out KeyPaste
	seen := map[string]bool{}
	for _, k := range existing {
		if k = strings.TrimSpace(k); k != "" {
			seen[k] = true
		}
	}
	anyKey := rule.AnyShape
	if anyKey {
		provider = "custom" // short tokens are keys too
	}
	for _, line := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		toks := strings.FieldsFunc(line, func(r rune) bool { return unicode.IsSpace(r) || r == ',' || r == ';' || r == '|' })
		found, junk := 0, 0
		for _, tok := range toks {
			k, bad := keyOf(tok, provider)
			if k == "" {
				if bad {
					junk++
				}
				continue
			}
			found++
			if owner := config.TTSKeyOwner(k); !anyKey && owner != "" && owner != rule.Owner {
				out.Foreign++
				if out.ForeignID == "" {
					out.ForeignID = owner
				}
				continue
			}
			if rule.Accept != nil && !rule.Accept(k) {
				found--
				junk++
				continue
			}
			if seen[k] {
				out.Dup++
				continue
			}
			seen[k] = true
			out.Keys = append(out.Keys, k)
		}
		// A label next to a key ("key1 sk_…") is not junk; a line of only
		// broken pieces counts once per piece.
		if found == 0 {
			out.Junk += junk
		}
	}
	return out
}

// Summary is the one-line outcome: «Додано 7 ключів · 2 вже були · 1 не
// схожий на ключ» (+ «1 — ключ ElevenLabs, не Cartesia»).
func (p KeyPaste) Summary(provider string) string {
	this, _ := ProviderByID(provider)
	return p.SummaryFor(firstNonEmpty(this.Label, provider))
}

// SummaryFor is Summary naming the list by label («… не Google AI Studio»).
func (p KeyPaste) SummaryFor(label string) string {
	var parts []string
	if n := len(p.Keys); n > 0 {
		parts = append(parts, fmt.Sprintf("Додано %d %s", n, plural(n, "ключ", "ключі", "ключів")))
	} else {
		parts = append(parts, "Нових ключів немає")
	}
	if n := p.Dup; n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", n, plural(n, "вже був", "вже були", "вже були")))
	}
	if n := p.Junk; n > 0 {
		parts = append(parts, fmt.Sprintf("%d %s на ключ", n, plural(n, "не схожий", "не схожі", "не схожі")))
	}
	if n := p.Foreign; n > 0 {
		other, _ := ProviderByID(p.ForeignID)
		if p.ForeignID == "google" {
			other.Label = "Google AI Studio"
		}
		parts = append(parts, fmt.Sprintf("%d — %s %s, не %s", n, plural(n, "ключ", "ключі", "ключів"), firstNonEmpty(other.Label, "іншого сервісу"), label))
	}
	return strings.Join(parts, " · ")
}
