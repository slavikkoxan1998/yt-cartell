package tts

import (
	"strings"
	"testing"

	"ytcartell/internal/config"
)

// Google AI Studio issues "AQ." auth keys since spring 2026; legacy AIza
// keys still parse. Fake keys only.
func TestParseGoogleKeysNewFormat(t *testing.T) {
	aq1 := "AQ.Ab8RN6Lq" + strings.Repeat("x9Y_", 12)
	aq2 := "AQ.Ab8RN6Mz" + strings.Repeat("k3-P", 10)
	legacy := "AIzaSy" + strings.Repeat("A1b2", 8) + "C"
	p := ParseGoogleKeys(aq1+"\n"+aq2+", "+legacy+"\nkey: "+aq1+"\nsk_car_abcdefghijklmnop", nil)
	if len(p.Keys) != 3 || p.Dup != 1 || p.Foreign != 1 || p.Junk != 0 {
		t.Fatalf("%+v · %s", p, p.SummaryFor(GoogleKeysLabel))
	}
	for _, k := range []string{aq1, aq2, legacy} {
		if config.TTSKeyOwner(k) != "google" {
			t.Fatalf("%s is a Google key", k[:8])
		}
	}
	if config.TTSKeyOwner("AQ.short") != "" {
		t.Fatal("too short")
	}
}
