package providers

import (
	"regexp"
	"strings"
)

// Secret-looking tokens masked in every message even when they are not the
// configured key (an API may echo a key back, e.g. "Incorrect API key
// provided: sk-…").
var reSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(bearer|key|token|basic)\s+[A-Za-z0-9._~+/=:\-]{12,}`),                                     // auth headers
	regexp.MustCompile(`\b(sk|rk|pk)-[A-Za-z0-9_\-*]{12,}`),                                                              // OpenAI-style
	regexp.MustCompile(`\br8_[A-Za-z0-9]{12,}`),                                                                          // Replicate
	regexp.MustCompile(`\bkey_[A-Za-z0-9]{16,}`),                                                                         // Runway
	regexp.MustCompile(`\bluma-[A-Za-z0-9\-]{16,}`),                                                                      // Luma
	regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}:[0-9a-fA-F]{16,}`), // fal.ai id:secret
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{6,}`),                              // JWT (MiniMax)
	regexp.MustCompile(`(?i)([?&](key|api_key|apikey|token|access_token)=)[^&\s"']+`),                                    // keys in URLs
}

// redact removes the keys (and anything that looks like a secret) from s.
// Keys shorter than 6 characters are ignored: replacing them would garble
// the text and they cannot be real secrets.
func redact(s string, keys ...string) string {
	for _, k := range keys {
		if k = strings.TrimSpace(k); len(k) >= 6 {
			s = strings.ReplaceAll(s, k, "•••")
		}
	}
	for i, re := range reSecrets {
		if i == len(reSecrets)-1 {
			s = re.ReplaceAllString(s, "${1}•••")
			continue
		}
		s = re.ReplaceAllStringFunc(s, maskToken)
	}
	return s
}

// maskToken keeps an auth scheme word ("Bearer", "Key") and masks the rest.
func maskToken(tok string) string {
	if i := strings.IndexAny(tok, " \t"); i > 0 {
		return tok[:i] + " •••"
	}
	return "•••"
}
