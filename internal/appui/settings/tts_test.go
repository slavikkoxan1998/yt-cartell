package settings

// The «Озвучка» card: per-provider settings in the UI, bulk paste, dropping
// rejected keys, and the mock services its screenshots check keys against
// (no network: every other host is refused).

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
)

// Synthetic keys in the providers' formats (never real ones).
var (
	elDemoKeys = []string{
		"sk_" + strings.Repeat("0f1e", 12),
		"sk_" + strings.Repeat("7a6b", 12),
		"sk_" + strings.Repeat("c3d2", 12), // rejected by the mock
	}
	carDemoKey = "sk_car_" + strings.Repeat("Qx7", 8)
)

// ttsMock answers like ElevenLabs and Cartesia for the demo keys; anything
// else (another host, an unknown key) is refused without a network call.
type ttsMock struct{}

func (ttsMock) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(code int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	switch r.URL.Host {
	case "api.elevenlabs.io":
		switch r.Header.Get("xi-api-key") {
		case elDemoKeys[0]:
			return reply(200, `{"tier":"free","status":"free","character_count":8599,"character_limit":10000}`)
		case elDemoKeys[1]:
			return reply(200, `{"tier":"creator","status":"active","character_count":41200,"character_limit":121000}`)
		}
		return reply(401, `{"detail":{"type":"authentication_error","code":"invalid_api_key","message":"Invalid API key"}}`)
	case "api.cartesia.ai":
		if r.Header.Get("Authorization") == "Bearer "+carDemoKey {
			return reply(200, `{"data":[],"has_more":false}`)
		}
		return reply(401, `{"error_code":null,"title":"Unauthorized","message":"Invalid API key"}`)
	}
	return nil, errors.New("test: no network (" + r.URL.Host + ")")
}

var mockOnce sync.Once

// useTTSMock routes the TTS checks of this test binary to ttsMock.
func useTTSMock() { mockOnce.Do(func() { http.DefaultTransport = ttsMock{} }) }

// checkTTSKeys runs the key check against the mock (the card's states).
func checkTTSKeys(c *core.Ctx) {
	useTTSMock()
	_, _ = c.E.TestTTS(context.Background())
}

// demoTTSVoices: an ElevenLabs free account — default voices (Alice chosen),
// an own voice and library voices it cannot use through the API.
var demoTTSVoices = tts.VoiceList{Provider: "elevenlabs", Tier: "free", CharsLeft: 1401, Voices: []tts.Voice{
	{ID: "Xb7hH8MSUJpSbSDYk0k2", Name: "Alice", Gender: "female", Accent: "british", Category: "premade", Usable: true, Note: "доступний до 31 грудня 2026"},
	{ID: "v-mira", Name: "Міра", Gender: "female", Category: "cloned", Usable: true},
	{ID: "v-taras", Name: "Тарас", Gender: "male", Accent: "ukrainian", Category: "premade", Usable: true, Note: "доступний до 31 грудня 2026"},
	{ID: "v-lib", Name: "Оповідач із бібліотеки", Gender: "male", Category: "library", Usable: false, Locked: "потрібен платний тариф ElevenLabs"},
}}

// TestTTSCardPerProvider: switching the provider in the card shows that
// provider's own (empty) settings and no voice of the previous one; switching
// back brings the keys and the voice back.
func TestTTSCardPerProvider(t *testing.T) {
	c, h := demo(t)
	p := New()
	p.cfg.set(c, func(cf *config.Config) {
		cf.TTS.SetKeys(elDemoKeys[:2])
		cf.TTS.Voice = demoTTSVoices.Voices[0].ID
	}, nil)
	h.Flush()
	c.SetVoicesForTest(demoTTSVoices)
	if l, _, _ := c.Voices(); len(l.Voices) != 4 {
		t.Fatalf("pinned list: %+v", l)
	}
	p.cfg.set(c, func(cf *config.Config) { cf.TTS.Switch("cartesia") }, nil)
	if got := p.cfg.get(c).TTS; got.APIKey != "" || len(got.Keys) != 0 || got.Voice != "" {
		t.Fatalf("optimistic cartesia view shows ElevenLabs settings: %+v", got.Active())
	}
	h.Flush()
	cfg := c.E.Config().Get()
	if cfg.TTS.Provider != "cartesia" || cfg.TTS.APIKey != "" || cfg.TTS.Voice != "" {
		t.Fatalf("store: %+v", cfg.TTS.Active())
	}
	if l, _, _ := c.Voices(); len(l.Voices) != 0 || l.Provider != "cartesia" {
		t.Fatalf("Cartesia shows the ElevenLabs voices: %+v", l)
	}
	if len(c.E.TTSKeyStatus()) != 0 {
		t.Fatal("Cartesia lists the ElevenLabs keys")
	}
	p.cfg.set(c, func(cf *config.Config) { cf.TTS.Switch("elevenlabs") }, nil)
	h.Flush()
	if cfg = c.E.Config().Get(); cfg.TTS.APIKey != elDemoKeys[0] || cfg.TTS.Voice != demoTTSVoices.Voices[0].ID {
		t.Fatalf("ElevenLabs lost its settings: %+v", cfg.TTS.Active())
	}
}

// TestTTSBulkAddAndDrop: a pasted list is read past labels and junk, an
// ElevenLabs key is refused under Cartesia, and the rejected keys go at once.
func TestTTSBulkAddAndDrop(t *testing.T) {
	c, h := demo(t)
	p := New()
	p.cfg.set(c, func(cf *config.Config) { cf.TTS.SetKeys([]string{elDemoKeys[0]}) }, nil)
	h.Flush()
	paste := "Мої ключі:\nkey1: " + elDemoKeys[0] + "\nkey2: " + elDemoKeys[1] + ", " + elDemoKeys[2] + "\nsk_123\n" + carDemoKey
	if !p.ttsAdd(c, paste) {
		t.Fatal("nothing added")
	}
	h.Flush()
	if got := ttsKeys(c.E.Config().Get().TTS); strings.Join(got, ",") != strings.Join(elDemoKeys, ",") {
		t.Fatalf("keys = %d %v", len(got), got)
	}
	if p.tts.pasted.msg != "Додано 2 ключі · 1 вже був · 1 не схожий на ключ · 1 — ключ Cartesia, не ElevenLabs. Ключі іншого сервісу додай, вибравши той сервіс угорі." || !p.tts.pasted.ok {
		t.Fatalf("summary = %+v", p.tts.pasted)
	}
	// Offline (headless) the check refuses to run; against the mock it marks
	// the third key rejected, and «Видалити всі з помилкою» drops it.
	checkTTSKeys(c)
	var bad []string
	keys := ttsKeys(c.E.Config().Get().TTS)
	for _, st := range c.E.TTSKeyStatus() {
		if st.State == tts.KeyError {
			bad = append(bad, keys[st.Index])
		}
	}
	if len(bad) != 1 || bad[0] != elDemoKeys[2] {
		t.Fatalf("rejected = %v (%+v)", bad, c.E.TTSKeyStatus())
	}
	st := c.E.TTSKeyStatus()
	if st[0].Plan != "free" || st[0].Limit != 10000 || st[1].Plan != "creator" || st[0].Checked.IsZero() {
		t.Fatalf("states: %+v", st)
	}
	if m := keyMeta(st[0]); m != "тариф free · використано 8 599 з 10 000 символів" {
		t.Fatalf("meta = %q", m)
	}
	p.ttsDrop(c, bad, nil)
	h.Flush()
	if got := ttsKeys(c.E.Config().Get().TTS); len(got) != 2 {
		t.Fatalf("after drop: %v", got)
	}
	// Under Cartesia an ElevenLabs key is refused and the text stays.
	p.cfg.set(c, func(cf *config.Config) { cf.TTS.Switch("cartesia") }, nil)
	h.Flush()
	if p.ttsAdd(c, elDemoKeys[1]) || !strings.Contains(p.tts.pasted.msg, "ключ ElevenLabs, не Cartesia") || p.tts.pasted.ok {
		t.Fatalf("foreign key: %+v", p.tts.pasted)
	}
}

// ---- screenshot states (shots_test.go) ------------------------------------------------

func ttsShotElevenLabs(cf *config.Config) {
	cf.TTS.SetKeys(elDemoKeys)
	cf.TTS.Voice = demoTTSVoices.Voices[0].ID
}

func ttsShotOneKey(cf *config.Config) {
	cf.TTS.SetKeys(elDemoKeys[:1])
	cf.TTS.Voice = demoTTSVoices.Voices[0].ID
}

// ttsShotChecked: the keys after «Перевірити» against the mock.
func ttsShotChecked(c *core.Ctx, p *Page) {
	c.SetVoicesForTest(demoTTSVoices)
	useTTSMock()
	ok, msg := c.E.TestTTS(context.Background())
	p.tts.check = check{done: true, ok: ok, msg: msg}
}

// demoPaste is a list as people keep it in a note.
var demoPaste = "Ключі ElevenLabs:\nосновний: " + elDemoKeys[0] + "\nkey2 = " + elDemoKeys[1] + "\n" + elDemoKeys[2] + "\nsk_9f3\n" + carDemoKey

// ttsShotPaste: the bulk area open with a pasted list.
func ttsShotPaste(c *core.Ctx, p *Page) {
	c.SetVoicesForTest(demoTTSVoices)
	p.tts.bulk = true
	p.tts.add.Editor.SingleLine = false // as after the first frame of the bulk area
	p.tts.add.SetText(demoPaste)
	p.tts.add.edited = true
}

// ttsShotBulk: what the paste added («Додано 2 ключі · …») and the check of
// every key that followed.
func ttsShotBulk(c *core.Ctx, p *Page) {
	c.SetVoicesForTest(demoTTSVoices)
	t := c.E.Config().Get().TTS
	res := tts.ParseKeys(demoPaste, t.Provider, false, ttsKeys(t))
	_ = c.E.Config().Update(func(cf *config.Config) { cf.TTS.SetKeys(append(ttsKeys(cf.TTS), res.Keys...)) })
	p.tts.pasted = pasteNote{ok: true, title: "Ключі додано — перевіряємо кожен",
		msg: res.Summary(t.Provider) + ". Ключі іншого сервісу додай, вибравши той сервіс угорі."}
	useTTSMock()
	ok, msg := c.E.TestTTS(context.Background())
	p.tts.check = check{done: true, ok: ok, msg: msg}
}

// TestTTSCardShots renders the «Озвучка» states on a tall window, so the whole
// card (keys, voice, checks) is in one picture (GUISHOTS=<dir>, like TestShots).
func TestTTSCardShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the screenshots")
	}
	for _, st := range shotStates {
		if st.section != "tts" {
			continue
		}
		for _, dark := range []bool{false, true} {
			st, dark := st, dark
			theme := map[bool]string{false: "light", true: "dark"}[dark]
			name := "tts-card-" + st.name + "-" + theme + ".png"
			t.Run(name, func(t *testing.T) {
				c, h := demo(t)
				if st.config != nil {
					if err := c.E.Config().Update(st.config); err != nil {
						t.Fatal(err)
					}
				}
				c.Th.SetPaletteNow(dark)
				p := New()
				frame := 0
				draw := shell(c, func(gtx C) D {
					frame++
					if frame == 2 && st.setup != nil {
						st.setup(c, p)
					}
					return p.Layout(gtx, c, st.section)
				})
				if err := core.Shot(c, h, 1440, 1900, 1, filepath.Join(out, name), draw, gui.Frames(6), gui.Settle(120)); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// Picking a Cartesia voice in «Озвучка» saves it as Cartesia's own voice: it
// stays after a switch to ElevenLabs and back, and the field names it.
func TestCartesiaVoicePickPersists(t *testing.T) {
	c, h := demo(t)
	_ = c.E.Config().Update(func(cf *config.Config) {
		cf.TTS.Switch("cartesia")
		cf.TTS.SetKeys([]string{carDemoKey})
	})
	olena, taras := "a0e99841-438c-4a64-b679-ae501e7d6091", "b7d50908-b17c-442d-ad8d-810c63997ed9"
	c.SetVoicesForTest(tts.VoiceList{Provider: "cartesia", CharsLeft: -1, Voices: []tts.Voice{
		{ID: olena, Name: "Олена", Gender: "female", Language: "uk-UA", Usable: true, Category: "premade"},
		{ID: taras, Name: "Тарас", Gender: "male", Language: "uk-UA", Usable: true, Category: "premade"},
	}})
	d := &driver{t: t, c: c, h: h, p: New(), section: "tts"}
	d.settle(4)
	d.clickRect(d.mustLabel("Не вибрано"))
	d.settle(4)
	if !d.p.tts.voice.IsOpen() {
		t.Fatal("the voice list did not open")
	}
	d.clickRect(d.mustLabel("Тарас"))
	d.settle(15) // the list fades out
	got := c.E.Config().Get().TTS
	if got.Voice != taras || got.Of("cartesia").Voice != taras {
		t.Fatalf("voice %q (cartesia %q)", got.Voice, got.Of("cartesia").Voice)
	}
	if len(d.labels("Тарас")) == 0 || len(d.labels("Не вибрано")) != 0 {
		t.Fatal("the field does not name the picked voice")
	}
	_ = c.E.Config().Update(func(cf *config.Config) { cf.TTS.Switch("elevenlabs") })
	_ = c.E.Config().Update(func(cf *config.Config) { cf.TTS.Switch("cartesia") })
	if v := c.E.Config().Get().TTS.Voice; v != taras {
		t.Fatalf("after switching back: %q", v)
	}
}
