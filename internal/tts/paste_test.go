package tts

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// Synthetic keys in the providers' formats (never real ones).
var (
	pElKey  = func(c string) string { return "sk_" + strings.Repeat(c, 48) }
	pCarKey = func(c string) string { return "sk_car_" + strings.Repeat(c+"Q7", 8) }
)

func TestParseKeysBulk(t *testing.T) {
	existing := []string{pCarKey("a")}
	text := strings.Join([]string{
		"Мої ключі Cartesia:",
		"key1: " + pCarKey("b"),
		"CARTESIA_API_KEY=" + pCarKey("c"),
		`"` + pCarKey("d") + `", ` + pCarKey("e") + "; " + pCarKey("f"),
		"1) " + pCarKey("g"),
		"  " + pCarKey("h") + "   ",
		pCarKey("a"), // already in the list
		pCarKey("b"), // repeated in the paste
		"sk_car_abc", // a truncated key
		"# a comment " + pCarKey("z"),
		pElKey("1"), // an ElevenLabs key under Cartesia
		"",
		"ключ від Олі", // a note: ignored
		"https://play.cartesia.ai/keys",
	}, "\n")
	got := ParseKeys(text, "cartesia", false, existing)
	want := []string{pCarKey("b"), pCarKey("c"), pCarKey("d"), pCarKey("e"), pCarKey("f"), pCarKey("g"), pCarKey("h")}
	if strings.Join(got.Keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v", got.Keys)
	}
	if got.Dup != 2 || got.Junk != 1 || got.Foreign != 1 || got.ForeignID != "elevenlabs" {
		t.Fatalf("counts = %+v", got)
	}
	if s := got.Summary("cartesia"); s != "Додано 7 ключів · 2 вже були · 1 не схожий на ключ · 1 — ключ ElevenLabs, не Cartesia" {
		t.Fatalf("summary = %q", s)
	}
	// Nothing new.
	if s := ParseKeys(pCarKey("a"), "cartesia", false, existing).Summary("cartesia"); s != "Нових ключів немає · 1 вже був" {
		t.Fatalf("summary = %q", s)
	}
	// One plain key (the single-line field) and a key list joined by spaces.
	if p := ParseKeys(pElKey("2")+" "+pElKey("3"), "elevenlabs", false, nil); len(p.Keys) != 2 || p.Junk != 0 {
		t.Fatalf("spaces: %+v", p)
	}
	// An own server takes any token, short ones too; an OpenAI-compatible
	// gateway takes an ElevenLabs-looking key.
	if p := ParseKeys("secret42\n"+pElKey("4"), "custom", false, nil); len(p.Keys) != 2 || p.Foreign != 0 {
		t.Fatalf("custom: %+v", p)
	}
	if p := ParseKeys(pElKey("5"), "openai", true, nil); len(p.Keys) != 1 {
		t.Fatalf("gateway: %+v", p)
	}
	if p := ParseKeys(pElKey("5"), "openai", false, nil); len(p.Keys) != 0 || p.Foreign != 1 {
		t.Fatalf("official OpenAI took an ElevenLabs key: %+v", p)
	}
	// Plural forms.
	if s := (KeyPaste{Keys: []string{"a"}, Dup: 5, Junk: 2}).Summary("fish"); s != "Додано 1 ключ · 5 вже були · 2 не схожі на ключ" {
		t.Fatalf("plural: %q", s)
	}
}

// Library voices on a free plan are marked; a paid second key lifts the mark.
func TestElevenLibraryVoicesAndPlans(t *testing.T) {
	var subTier atomic.Value
	subTier.Store(`{"tier":"free","status":"free","character_count":100,"character_limit":10000}`)
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/voices":
			_, _ = w.Write([]byte(`{"voices":[
				{"voice_id":"prem","name":"Rachel","category":"premade","is_owner":false},
				{"voice_id":"mine","name":"Me","category":"cloned","is_owner":true},
				{"voice_id":"lib","name":"Library","category":"professional","is_owner":false},
				{"voice_id":"lib2","name":"Shared","category":"generated","is_owner":false}],"has_more":false}`))
		case "/v1/user/subscription":
			if r.Header.Get("xi-api-key") == "paid-key-000000002" {
				_, _ = w.Write([]byte(`{"tier":"creator","status":"active","character_count":1,"character_limit":100000}`))
				return
			}
			_, _ = w.Write([]byte(subTier.Load().(string)))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	c, cfg := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "free-key-000000001"})
	l, err := c.Voices(context.Background(), true)
	if err != nil || l.Tier != "free" {
		t.Fatalf("list %+v err %v", l, err)
	}
	byID := map[string]Voice{}
	for _, v := range l.Voices {
		byID[v.ID] = v
	}
	if !byID["prem"].Usable || !byID["mine"].Usable || byID["mine"].Category != "cloned" {
		t.Fatalf("own/default voices: %+v", l.Voices)
	}
	for _, id := range []string{"lib", "lib2"} {
		if v := byID[id]; v.Usable || v.Locked != LockedPaidEleven || v.Category != "library" {
			t.Fatalf("%s = %+v", id, v)
		}
	}
	if st := c.KeyStatus(); st[0].Plan != "free" {
		t.Fatalf("plan not remembered: %+v", st)
	}
	// A second key on a paid plan (known from a check) can voice them.
	cfg.TTS.Keys = []string{"paid-key-000000002"}
	if ok, msg := c.Test(context.Background()); !ok || !strings.Contains(msg, "2 з 2") {
		t.Fatalf("test: %v %s", ok, msg)
	}
	st := c.KeyStatus()
	if st[1].Plan != "creator" || st[1].Checked.IsZero() || !strings.Contains(st[1].Info, "тариф creator") {
		t.Fatalf("checked key: %+v", st[1])
	}
	l, _ = c.Voices(context.Background(), true)
	for _, v := range l.Voices {
		if !v.Usable {
			t.Fatalf("a paid key is known, yet %s is locked", v.ID)
		}
	}
}

func TestOpenAIVoicesByModel(t *testing.T) {
	c, cfg := cfgClient(config.TTS{Provider: "openai", APIKey: "sk-test"})
	l, _ := c.Voices(context.Background(), false)
	for _, v := range l.Voices {
		if !v.Usable {
			t.Fatalf("gpt-4o-mini-tts: %s locked", v.ID)
		}
	}
	cfg.TTS.Model = "tts-1-hd"
	l, _ = c.Voices(context.Background(), false)
	locked := 0
	for _, v := range l.Voices {
		if !v.Usable {
			locked++
			if v.Locked == "" {
				t.Fatalf("%s locked without a reason", v.ID)
			}
		}
	}
	if locked != 4 || len(l.Voices) != 13 {
		t.Fatalf("tts-1-hd: %d of %d locked", locked, len(l.Voices))
	}
}

// Checks run a few at a time and spaced out.
func TestTestIsPolite(t *testing.T) {
	var cur, peak atomic.Int32
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		n := cur.Add(1)
		defer cur.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte(`{"tier":"creator","character_count":1,"character_limit":10}`))
	})
	gap := probeGap
	probeGap = 5 * time.Millisecond
	t.Cleanup(func() { probeGap = gap })
	keys := make([]string, 9)
	for i := range keys {
		keys[i] = "key-number-0000" + string(rune('a'+i))
	}
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: keys[0], Keys: keys[1:]})
	if ok, msg := c.Test(context.Background()); !ok || !strings.Contains(msg, "9 з 9") {
		t.Fatalf("%v %s", ok, msg)
	}
	if p := peak.Load(); p > int32(probeParallel) {
		t.Fatalf("%d checks at once", p)
	}
}
