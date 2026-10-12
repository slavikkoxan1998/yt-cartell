package tts

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
)

// rec records the requests a provider mock received.
type rec struct {
	mu   sync.Mutex
	reqs []recReq
}

type recReq struct {
	method, path, query string
	hdr                 http.Header
	body                map[string]any
	raw                 string
}

func (r *rec) add(req *http.Request) recReq {
	b, _ := io.ReadAll(req.Body)
	rr := recReq{method: req.Method, path: req.URL.Path, query: req.URL.RawQuery, hdr: req.Header.Clone(), raw: string(b)}
	req.Body = io.NopCloser(strings.NewReader(rr.raw))
	rr.body = drain(req)
	r.mu.Lock()
	r.reqs = append(r.reqs, rr)
	r.mu.Unlock()
	return rr
}

func (r *rec) last(path string) recReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.reqs) - 1; i >= 0; i-- {
		if r.reqs[i].path == path {
			return r.reqs[i]
		}
	}
	return recReq{}
}

func (r *rec) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, q := range r.reqs {
		if q.path == path {
			n++
		}
	}
	return n
}

func sub(m map[string]any, keys ...string) any {
	var v any = m
	for _, k := range keys {
		mm, _ := v.(map[string]any)
		v = mm[k]
	}
	return v
}

// ---- Providers / config ---------------------------------------------------------------

func TestProvidersPresets(t *testing.T) {
	list := Providers()
	seen := map[string]bool{}
	for _, p := range list {
		if p.ID == "" || p.Label == "" || p.Languages == "" || p.KeyHint == "" || p.Concurrency < 1 || seen[p.ID] {
			t.Errorf("preset %+v", p)
		}
		seen[p.ID] = true
		if p.ID != "custom" && !strings.HasPrefix(p.DocsURL, "https://") {
			t.Errorf("%s: docs %q", p.ID, p.DocsURL)
		}
		if p.DefaultModel != "" && (len(p.Models) == 0 || p.Models[0] != p.DefaultModel) {
			t.Errorf("%s: default model %q is not the first of %v", p.ID, p.DefaultModel, p.Models)
		}
		// Settings keep the id: config.Normalize must accept every preset.
		c := config.Config{TTS: config.TTS{Provider: p.ID}}
		c.Normalize()
		if c.TTS.Provider != p.ID {
			t.Errorf("config.Normalize turns %q into %q", p.ID, c.TTS.Provider)
		}
	}
	for _, id := range []string{"elevenlabs", "cartesia", "openai", "google", "azure", "minimax", "fish", "custom"} {
		if !seen[id] {
			t.Errorf("missing preset %s", id)
		}
	}
	list[0].Models[0] = "mutated"
	if Providers()[0].Models[0] == "mutated" {
		t.Error("Providers returns shared slices")
	}
	if p, ok := ProviderByID(" OpenAI-Compatible "); !ok || p.ID != "openai" {
		t.Errorf("alias → %+v %v", p, ok)
	}
	if normProvider("klingon") != "elevenlabs" || normProvider("") != "elevenlabs" {
		t.Error("unknown providers fall back to ElevenLabs")
	}
}

// ---- Cartesia -------------------------------------------------------------------------

func TestCartesiaSynthesize(t *testing.T) {
	r := &rec{}
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if q.hdr.Get("Authorization") != "Bearer sk_car_test_0001" || q.hdr.Get("Cartesia-Version") != cartesiaVersion {
			t.Errorf("headers %v", q.hdr)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3cartesia"))
	})
	c, cfg := cfgClient(config.TTS{Provider: "cartesia", APIKey: "sk_car_test_0001", Voice: "voice-uuid"})
	out, err := c.SynthesizeWith(context.Background(), "Привіт, друже", "", SynthOptions{LanguageCode: "uk", Speed: 1.2})
	if err != nil || string(out) != "ID3cartesia" {
		t.Fatalf("out %q err %v", out, err)
	}
	b := r.last("/tts/bytes").body
	if b["model_id"] != "sonic-3.6" || b["transcript"] != "Привіт, друже" || sub(b, "voice", "id") != "voice-uuid" || sub(b, "voice", "mode") != nil {
		t.Errorf("body %v", b)
	}
	if sub(b, "output_format", "container") != "mp3" || sub(b, "output_format", "bit_rate") != float64(128000) || b["language"] != "uk" {
		t.Errorf("body %v", b)
	}
	if sub(b, "generation_config", "speed") != 1.2 {
		t.Errorf("speed %v", b["generation_config"])
	}
	// "auto": the writing system names the language; Tagalog is "tl" there.
	_, _ = c.SynthesizeWith(context.Background(), "Это русский текст", "", SynthOptions{})
	if b := r.last("/tts/bytes").body; b["language"] != "ru" || b["generation_config"] != nil {
		t.Errorf("auto body %v", b)
	}
	_, _ = c.SynthesizeWith(context.Background(), "Magandang araw", "", SynthOptions{LanguageCode: "fil"})
	if b := r.last("/tts/bytes").body; b["language"] != "tl" {
		t.Errorf("fil → %v", b["language"])
	}
	_, _ = c.SynthesizeWith(context.Background(), "Hello there", "", SynthOptions{})
	if b := r.last("/tts/bytes").body; b["language"] != nil {
		t.Errorf("Latin text without a language sends %v", b["language"])
	}
	cfg.TTS.Model = "sonic-3"
	_, _ = c.Synthesize(context.Background(), "x", "")
	if b := r.last("/tts/bytes").body; b["model_id"] != "sonic-3" {
		t.Errorf("model %v", b["model_id"])
	}
	cfg.TTS.Voice = ""
	if _, err := c.Synthesize(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "Не вибрано голос") {
		t.Errorf("no voice → %v", err)
	}
}

func TestCartesiaErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		state  string // the key after the failure
		want   string // error prefix
	}{
		{402, `{"error_code":"quota_exceeded","title":"Quota exceeded","message":"You have run out of credits"}`, KeyExhausted, "Ключ Cartesia вичерпав ліміт до"},
		{402, `Payment Required: credit limit reached`, KeyExhausted, "Ключ Cartesia вичерпав ліміт до"}, // legacy text (Cartelsia)
		{402, `{"error_code":"plan_upgrade_required","message":"This voice is not available on the free tier"}`, KeyOK, "Cartesia: цей голос або можливість потребує вищого тарифу"},
		{401, `{"error_code":null,"title":"Unauthorized","message":"Invalid API key"}`, KeyError, "Cartesia: невірний API-ключ"},
		{429, `{"error_code":"concurrency_limited","title":"Too many concurrent requests"}`, KeyResting, "Ключ Cartesia тимчасово обмежений"},
		{400, `{"error_code":"language_not_supported","message":"hy is not supported"}`, KeyOK, "Cartesia: модель не озвучує мову"},
		{404, `{"error_code":"voice_not_found","message":"Voice not found"}`, KeyOK, "Cartesia: голос не знайдено"},
	}
	for _, tc := range cases {
		mockAll(t, func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		longRests(t)
		c, _ := cfgClient(config.TTS{Provider: "cartesia", APIKey: "sk_car_test_0001", Voice: "v"})
		_, err := c.Synthesize(context.Background(), "x", "")
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%d %s → %v, want %q", tc.status, tc.body, err, tc.want)
		}
		if st := c.KeyStatus()[0]; st.State != tc.state {
			t.Errorf("%d %s → key %s, want %s", tc.status, tc.body, st.State, tc.state)
		}
	}
}

func TestCartesiaVoices(t *testing.T) {
	r := &rec{}
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if q.path != "/voices" || q.hdr.Get("Cartesia-Version") == "" {
			t.Errorf("request %s %v", q.path, q.hdr)
		}
		if strings.Contains(q.query, "starting_after=cursor-1") {
			_, _ = w.Write([]byte(`{"data":[{"id":"v3","name":"Олена","gender":"feminine","accents":[{"accent":"Ukrainian","locale":"uk-UA","is_native":true}],"preview_file_url":"https://x/v3.mp3","is_owner":false}],"has_more":false,"next_page":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"id":"v1","name":"Barbershop Man","gender":"masculine","language":"en","is_owner":false,"preview_file_url":null},
			{"id":"v2","name":"My Clone","gender":null,"language":"uk","is_owner":true}],"has_more":true,"next_page":"cursor-1"}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "cartesia", APIKey: "sk_car_test_0001"})
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, v := range l.Voices {
		order = append(order, v.ID)
	}
	if strings.Join(order, ",") != "v2,v3,v1" { // own, then Ukrainian, then the rest
		t.Fatalf("order %v", order)
	}
	v3 := l.Voices[1]
	if v3.Gender != "female" || v3.Language != "uk-UA" || v3.Accent != "ukrainian" || v3.PreviewURL != "https://x/v3.mp3" || !v3.Usable || v3.Category != "premade" {
		t.Errorf("v3 %+v", v3)
	}
	if l.Voices[0].Category != "cloned" || l.Voices[2].Gender != "male" || l.Provider != "cartesia" || l.CharsLeft != -1 {
		t.Errorf("list %+v", l)
	}
	if !strings.Contains(r.last("/voices").query, "expand%5B%5D=preview_file_url") {
		t.Errorf("query %s", r.last("/voices").query)
	}
}

// ---- Google Cloud -----------------------------------------------------------------------

func TestGoogleSynthesize(t *testing.T) {
	r := &rec{}
	fail := ""
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if q.hdr.Get("x-goog-api-key") == "" || q.hdr.Get("Authorization") != "" {
			t.Errorf("auth %v", q.hdr)
		}
		if name, _ := sub(q.body, "voice", "name").(string); fail != "" && name == fail {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Voice '` + fail + `' does not exist.","status":"INVALID_ARGUMENT"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"audioContent":"` + base64.StdEncoding.EncodeToString([]byte("ID3google")) + `"}`))
	})
	c, cfg := cfgClient(config.TTS{Provider: "google", APIKey: "AIzaSy-test-key-01"})
	out, err := c.SynthesizeWith(context.Background(), "Привіт", "", SynthOptions{LanguageCode: "uk", Speed: 1.1})
	if err != nil || string(out) != "ID3google" {
		t.Fatalf("out %q err %v", out, err)
	}
	b := r.last("/v1/text:synthesize").body
	if sub(b, "voice", "name") != "uk-UA-Chirp3-HD-Charon" || sub(b, "voice", "languageCode") != "uk-UA" ||
		sub(b, "audioConfig", "audioEncoding") != "MP3" || sub(b, "audioConfig", "speakingRate") != 1.1 || sub(b, "input", "text") != "Привіт" {
		t.Fatalf("body %v", b)
	}
	// A persona from the list follows the film language; Chinese is cmn-CN on Google.
	cfg.TTS.Voice = "Chirp3-HD-Kore"
	_, _ = c.SynthesizeWith(context.Background(), "你好", "", SynthOptions{LanguageCode: "zh"})
	if b := r.last("/v1/text:synthesize").body; sub(b, "voice", "name") != "cmn-CN-Chirp3-HD-Kore" || sub(b, "voice", "languageCode") != "cmn-CN" {
		t.Fatalf("body %v", b)
	}
	// A Chirp 3 HD voice of another locale moves to the film's; others keep theirs.
	_, _ = c.SynthesizeWith(context.Background(), "Hallo", "en-US-Chirp3-HD-Puck", SynthOptions{LanguageCode: "de"})
	if b := r.last("/v1/text:synthesize").body; sub(b, "voice", "name") != "de-DE-Chirp3-HD-Puck" {
		t.Fatalf("body %v", b)
	}
	_, _ = c.SynthesizeWith(context.Background(), "Hallo", "uk-UA-Wavenet-A", SynthOptions{LanguageCode: "de"})
	if b := r.last("/v1/text:synthesize").body; sub(b, "voice", "name") != "uk-UA-Wavenet-A" || sub(b, "voice", "languageCode") != "uk-UA" {
		t.Fatalf("body %v", b)
	}
	// "auto" with Cyrillic text → Ukrainian.
	_, _ = c.SynthesizeWith(context.Background(), "Добрий вечір, ми з України", "Chirp3-HD-Kore", SynthOptions{})
	if b := r.last("/v1/text:synthesize").body; sub(b, "voice", "languageCode") != "uk-UA" {
		t.Fatalf("auto body %v", b)
	}
	// A locale without this persona: the locale's default voice.
	fail = "ka-GE-Chirp3-HD-Kore"
	if _, err := c.SynthesizeWith(context.Background(), "გამარჯობა", "Chirp3-HD-Kore", SynthOptions{LanguageCode: "ka"}); err != nil {
		t.Fatal(err)
	}
	if b := r.last("/v1/text:synthesize").body; sub(b, "voice", "name") != nil || sub(b, "voice", "languageCode") != "ka-GE" {
		t.Fatalf("fallback body %v", b)
	}
	// A voice id of another provider (left from ElevenLabs) is refused before
	// any request — a clear error, not a default voice.
	before := r.count("/v1/text:synthesize")
	if _, err := c.SynthesizeWith(context.Background(), "Привіт", "21m00Tcm4TlvDq8ikWAM", SynthOptions{LanguageCode: "uk"}); err == nil || !strings.Contains(err.Error(), "з іншого сервісу (ElevenLabs)") {
		t.Fatalf("foreign voice id → %v", err)
	}
	if r.count("/v1/text:synthesize") != before {
		t.Fatal("a foreign voice reached the provider")
	}
	if got := c.ForeignVoice("21m00Tcm4TlvDq8ikWAM"); got != "elevenlabs" {
		t.Fatalf("ForeignVoice = %q", got)
	}
}

func TestGoogleErrorsAndSharedKeys(t *testing.T) {
	var mu sync.Mutex
	keysSeen := map[string]int{}
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		k := req.Header.Get("x-goog-api-key")
		mu.Lock()
		keysSeen[k]++
		mu.Unlock()
		switch k {
		case "AIzaSy-disabled-01":
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Cloud Text-to-Speech API has not been used in project 123 before or it is disabled.","status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED"}]}}`))
		case "AIzaSy-invalid-002":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`))
		case "AIzaSy-busy-00003":
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded for quota metric 'Requests' and limit 'Requests per minute'","status":"RESOURCE_EXHAUSTED"}}`))
		default:
			_, _ = w.Write([]byte(`{"audioContent":"` + base64.StdEncoding.EncodeToString([]byte("ok")) + `"}`))
		}
	})
	// No TTS key: the Google AI Studio keys are borrowed and rotate too.
	cfg := &config.Config{TTS: config.TTS{Provider: "google"}, Google: config.Google{Keys: []string{"AIzaSy-disabled-01", "AIzaSy-invalid-002", "AIzaSy-busy-00003", "AIzaSy-works-0004"}}}
	c := New(func() config.Config { return *cfg })
	// The rotation may start at any key, so synthesize until every key has
	// been tried at least once (a working key always answers).
	for i := 0; i < 4; i++ {
		if out, err := c.Synthesize(context.Background(), "hi", ""); err != nil || string(out) != "ok" {
			t.Fatalf("out %q err %v", out, err)
		}
	}
	st := c.KeyStatus()
	if st[0].State != KeyError || !strings.Contains(st[0].LastError, "вимкнено Cloud Text-to-Speech API") {
		t.Errorf("disabled API: %+v", st[0])
	}
	if st[1].State != KeyError || !strings.Contains(st[1].LastError, "невірний") {
		t.Errorf("invalid key: %+v", st[1])
	}
	// The rotation is shared per provider across clients, so whether the busy
	// key was reached above depends on earlier tests; check it on its own.
	busy := New(func() config.Config {
		c := *cfg
		c.Google.Keys = []string{"AIzaSy-busy-00003"}
		return c
	})
	_, _ = busy.Synthesize(context.Background(), "hi", "")
	if bs := busy.KeyStatus(); len(bs) != 1 || bs[0].State != KeyResting {
		t.Errorf("per-minute quota is a rest, not exhaustion: %+v", bs)
	}
	if st[3].State != KeyOK || !st[3].Shared {
		t.Errorf("working key: %+v", st[3])
	}
}

func TestGoogleVoices(t *testing.T) {
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/voices" {
			t.Errorf("path %s", req.URL.Path)
		}
		_, _ = w.Write([]byte(`{"voices":[
			{"languageCodes":["en-US"],"name":"en-US-Chirp3-HD-Charon","ssmlGender":"MALE","naturalSampleRateHertz":24000},
			{"languageCodes":["uk-UA"],"name":"uk-UA-Chirp3-HD-Charon","ssmlGender":"MALE","naturalSampleRateHertz":24000},
			{"languageCodes":["uk-UA"],"name":"uk-UA-Chirp3-HD-Aoede","ssmlGender":"FEMALE","naturalSampleRateHertz":24000},
			{"languageCodes":["uk-UA"],"name":"uk-UA-Wavenet-A","ssmlGender":"FEMALE","naturalSampleRateHertz":24000},
			{"languageCodes":["en-US"],"name":"en-US-Wavenet-B","ssmlGender":"MALE","naturalSampleRateHertz":24000},
			{"languageCodes":["en-US"],"name":"Kore","ssmlGender":"FEMALE"}]}`))
	})
	c, cfg := cfgClient(config.TTS{Provider: "google", APIKey: "AIzaSy-test-key-01"})
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Voices) != 2 || l.Voices[0].ID != "Chirp3-HD-Aoede" || l.Voices[0].Gender != "female" || l.Voices[1].ID != "Chirp3-HD-Charon" || l.Voices[1].Language != "" {
		t.Fatalf("personas %+v", l.Voices)
	}
	cfg.TTS.Model = "Wavenet"
	l, err = c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Voices) != 2 || l.Voices[0].ID != "uk-UA-Wavenet-A" || l.Voices[0].Accent != "ukrainian" || l.Voices[1].Accent != "american" {
		t.Fatalf("wavenet %+v", l.Voices)
	}
}

// ---- Azure --------------------------------------------------------------------------------

const azureVoicesJSON = `[
 {"ShortName":"uk-UA-PolinaNeural","DisplayName":"Polina","LocalName":"Поліна","Gender":"Female","Locale":"uk-UA","Status":"GA"},
 {"ShortName":"uk-UA-OstapNeural","DisplayName":"Ostap","LocalName":"Остап","Gender":"Male","Locale":"uk-UA","Status":"GA"},
 {"ShortName":"en-US-JennyNeural","DisplayName":"Jenny","Gender":"Female","Locale":"en-US","Status":"GA"},
 {"ShortName":"en-US-AndrewMultilingualNeural","DisplayName":"Andrew Multilingual","Gender":"Male","Locale":"en-US","SecondaryLocaleList":["de-DE","uk-UA"],"Status":"GA"},
 {"ShortName":"de-DE-KatjaNeural","DisplayName":"Katja","Gender":"Female","Locale":"de-DE","Status":"GA"}]`

func TestAzureSynthesize(t *testing.T) {
	r := &rec{}
	region := ""
	srv := mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if q.hdr.Get("Ocp-Apim-Subscription-Key") != "azure-key-000001" || q.hdr.Get("User-Agent") == "" {
			t.Errorf("headers %v", q.hdr)
		}
		if q.path == "/cognitiveservices/voices/list" {
			_, _ = w.Write([]byte(azureVoicesJSON))
			return
		}
		if q.path != "/cognitiveservices/v1" || q.hdr.Get("Content-Type") != "application/ssml+xml" || q.hdr.Get("X-Microsoft-OutputFormat") != azureFormat {
			t.Errorf("request %s %v", q.path, q.hdr)
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3azure"))
	})
	azureEndpoint = func(reg string) string { region = reg; return srv.URL } // restored by mockAll
	c, cfg := cfgClient(config.TTS{Provider: "azure", APIKey: "azure-key-000001", BaseURL: "WestEurope"})
	out, err := c.SynthesizeWith(context.Background(), "Привіт & <вітаю>", "", SynthOptions{LanguageCode: "uk", Speed: 1.1})
	if err != nil || string(out) != "ID3azure" || region != "westeurope" {
		t.Fatalf("out %q err %v region %q", out, err, region)
	}
	ssml := r.last("/cognitiveservices/v1").raw
	for _, want := range []string{`<voice name="uk-UA-OstapNeural">`, `xml:lang="uk-UA"`, `<prosody rate="+10%">`, `Привіт &amp; &lt;вітаю&gt;`} {
		if !strings.Contains(ssml, want) {
			t.Errorf("SSML %s lacks %s", ssml, want)
		}
	}
	// A multilingual voice speaks the film's language through <lang>.
	_, _ = c.SynthesizeWith(context.Background(), "Hallo", "en-US-AndrewMultilingualNeural", SynthOptions{LanguageCode: "de"})
	if ssml := r.last("/cognitiveservices/v1").raw; !strings.Contains(ssml, `<lang xml:lang="de-DE">Hallo</lang>`) || !strings.Contains(ssml, `xml:lang="en-US"`) {
		t.Errorf("SSML %s", ssml)
	}
	// A voice of another language: a same-gender voice of the film's language.
	_, _ = c.SynthesizeWith(context.Background(), "Привіт", "en-US-JennyNeural", SynthOptions{LanguageCode: "uk"})
	if ssml := r.last("/cognitiveservices/v1").raw; !strings.Contains(ssml, `name="uk-UA-PolinaNeural"`) {
		t.Errorf("SSML %s", ssml)
	}
	_, _ = c.SynthesizeWith(context.Background(), "Guten Tag", "uk-UA-OstapNeural", SynthOptions{LanguageCode: "de"})
	if ssml := r.last("/cognitiveservices/v1").raw; !strings.Contains(ssml, `name="de-DE-KatjaNeural"`) && !strings.Contains(ssml, `AndrewMultilingual`) {
		t.Errorf("SSML %s", ssml)
	}
	// An endpoint URL instead of a region.
	cfg.TTS.BaseURL = srv.URL + "/"
	region = ""
	if _, err := c.Synthesize(context.Background(), "x", "uk-UA-PolinaNeural"); err != nil || region != "" {
		t.Fatalf("url endpoint: %v %q", err, region)
	}
	cfg.TTS.BaseURL = ""
	if _, err := c.Synthesize(context.Background(), "x", ""); err == nil || !strings.Contains(err.Error(), "регіон") {
		t.Fatalf("no region → %v", err)
	}
}

func TestAzureErrorsAndVoices(t *testing.T) {
	mode := ""
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/cognitiveservices/voices/list" {
			_, _ = w.Write([]byte(azureVoicesJSON))
			return
		}
		switch mode {
		case "quota":
			w.WriteHeader(429)
			_, _ = w.Write([]byte("You have reached the quota with your free-tier (F0) Speech resource."))
		case "rate":
			w.WriteHeader(429)
			_, _ = w.Write([]byte("Requests have exceeded the rate limit of your current speech services F0 pricing tier."))
		case "auth":
			w.WriteHeader(401)
		}
	})
	longRests(t)
	for m, want := range map[string]string{"quota": KeyExhausted, "rate": KeyResting, "auth": KeyError} {
		mode = m
		c, _ := cfgClient(config.TTS{Provider: "azure", APIKey: "azure-key-000001", BaseURL: "westeurope"})
		_, err := c.Synthesize(context.Background(), "x", "uk-UA-OstapNeural")
		if st := c.KeyStatus()[0]; err == nil || st.State != want {
			t.Errorf("%s → %v, key %+v", m, err, st)
		}
	}
	c, _ := cfgClient(config.TTS{Provider: "azure", APIKey: "azure-key-000001", BaseURL: "westeurope"})
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Voices) != 5 || !strings.HasPrefix(l.Voices[0].ID, "uk-UA-") || !strings.HasPrefix(l.Voices[1].ID, "uk-UA-") {
		t.Fatalf("Ukrainian first: %+v", l.Voices)
	}
	for _, v := range l.Voices {
		if v.ID == "en-US-AndrewMultilingualNeural" && (v.Language != "" || v.Gender != "male") {
			t.Errorf("multilingual %+v", v)
		}
		if v.ID == "uk-UA-PolinaNeural" && (v.Gender != "female" || v.Accent != "ukrainian" || v.Name != "Polina") {
			t.Errorf("polina %+v", v)
		}
	}
}

// ---- MiniMax --------------------------------------------------------------------------------

func TestMiniMaxSynthesizeAndUsage(t *testing.T) {
	r := &rec{}
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if q.hdr.Get("Authorization") != "Bearer mm-key-0000001" {
			t.Errorf("auth %v", q.hdr)
		}
		_, _ = w.Write([]byte(`{"data":{"audio":"` + hex.EncodeToString([]byte("ID3minimax")) + `","status":2},
			"extra_info":{"usage_characters":42,"audio_format":"mp3"},"base_resp":{"status_code":0,"status_msg":"success"}}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "minimax", APIKey: "mm-key-0000001", Voice: "English_expressive_narrator"})
	out, err := c.SynthesizeWith(context.Background(), "Привіт", "", SynthOptions{LanguageCode: "uk", Speed: 0.9})
	if err != nil || string(out) != "ID3minimax" {
		t.Fatalf("out %q err %v", out, err)
	}
	b := r.last("/v1/t2a_v2").body
	if b["model"] != "speech-2.8-hd" || b["language_boost"] != "Ukrainian" || b["output_format"] != "hex" || b["stream"] != false ||
		sub(b, "voice_setting", "voice_id") != "English_expressive_narrator" || sub(b, "voice_setting", "speed") != 0.9 ||
		sub(b, "audio_setting", "format") != "mp3" {
		t.Fatalf("body %v", b)
	}
	if st := c.KeyStatus()[0]; st.UsedChars != 42 {
		t.Errorf("billed characters %d, want the provider's 42", st.UsedChars)
	}
	_, _ = c.SynthesizeWith(context.Background(), "Hello", "", SynthOptions{})
	if b := r.last("/v1/t2a_v2").body; b["language_boost"] != "auto" {
		t.Errorf("auto → %v", b["language_boost"])
	}
}

func TestMiniMaxErrors(t *testing.T) {
	for _, tc := range []struct {
		code  int
		state string
		want  string
	}{
		{1008, KeyExhausted, "Ключ MiniMax Speech вичерпав ліміт до"},
		{2049, KeyError, "MiniMax: невірний API-ключ"},
		{1004, KeyError, "MiniMax: невірний API-ключ"},
		{1002, KeyResting, "Ключ MiniMax Speech тимчасово обмежений"},
		{2056, KeyResting, "Ключ MiniMax Speech тимчасово обмежений"},
		{1026, KeyOK, "MiniMax відхилив текст"},
		{2013, KeyOK, "MiniMax: сервіс не прийняв запит (2013)"},
	} {
		mockAll(t, func(w http.ResponseWriter, req *http.Request) {
			fmt.Fprintf(w, `{"base_resp":{"status_code":%d,"status_msg":"err %d"}}`, tc.code, tc.code)
		})
		longRests(t)
		c, _ := cfgClient(config.TTS{Provider: "minimax", APIKey: "mm-key-0000001", Voice: "v"})
		_, err := c.Synthesize(context.Background(), "x", "")
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%d → %v, want %q", tc.code, err, tc.want)
		}
		if st := c.KeyStatus()[0]; st.State != tc.state {
			t.Errorf("%d → key %s, want %s", tc.code, st.State, tc.state)
		}
	}
}

func TestMiniMaxVoices(t *testing.T) {
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		if b := drain(req); req.URL.Path != "/v1/get_voice" || b["voice_type"] != "all" {
			t.Errorf("request %s %v", req.URL.Path, b)
		}
		_, _ = w.Write([]byte(`{"system_voice":[
			{"voice_id":"Wise_Woman","voice_name":"Wise Woman","description":["calm"]},
			{"voice_id":"English_expressive_narrator","voice_name":"Expressive Narrator","description":["male narrator"]}],
			"voice_cloning":[{"voice_id":"my-clone-1","description":[],"created_time":"2026-09-01"}],
			"voice_generation":[],"base_resp":{"status_code":0}}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "minimax", APIKey: "mm-key-0000001"})
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Voices) != 3 || l.Voices[0].ID != "my-clone-1" || l.Voices[0].Category != "cloned" {
		t.Fatalf("voices %+v", l.Voices)
	}
	for _, v := range l.Voices {
		if v.ID == "Wise_Woman" && v.Gender != "female" {
			t.Errorf("wise woman %+v", v)
		}
		if v.ID == "English_expressive_narrator" && (v.Gender != "male" || v.Accent != "english") {
			t.Errorf("narrator %+v", v)
		}
	}
}

// ---- Fish Audio -----------------------------------------------------------------------------

func TestFishSynthesizeAndVoices(t *testing.T) {
	r := &rec{}
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if q.hdr.Get("Authorization") != "Bearer fish-key-000001" {
			t.Errorf("auth %v", q.hdr)
		}
		switch q.path {
		case "/v1/tts":
			if q.hdr.Get("model") != "s2.1-pro" {
				t.Errorf("model header %q", q.hdr.Get("model"))
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3fish"))
		case "/model":
			switch {
			case strings.Contains(q.query, "self=true"):
				_, _ = w.Write([]byte(`{"total":1,"items":[{"_id":"own1","title":"Мій голос","tags":["male"],"languages":["uk"],"samples":[{"audio":"https://x/own1.mp3"}],"state":"trained"}]}`))
			case strings.Contains(q.query, "language=uk"):
				_, _ = w.Write([]byte(`{"total":2,"items":[{"_id":"pub-uk","title":"Диктор","tags":["female","narrator"],"languages":["uk"],"state":"trained"},
					{"_id":"own1","title":"Мій голос","languages":["uk"],"state":"trained"}]}`))
			default:
				_, _ = w.Write([]byte(`{"total":2,"items":[{"_id":"pub-en","title":"Narrator","tags":["male"],"languages":["en"],"state":"trained"},
					{"_id":"broken","title":"Training","state":"training"}]}`))
			}
		}
	})
	c, _ := cfgClient(config.TTS{Provider: "fish", APIKey: "fish-key-000001", Voice: "own1"})
	out, err := c.SynthesizeWith(context.Background(), "Привіт", "", SynthOptions{LanguageCode: "uk", Speed: 1.2})
	if err != nil || string(out) != "ID3fish" {
		t.Fatalf("out %q err %v", out, err)
	}
	b := r.last("/v1/tts").body
	if b["reference_id"] != "own1" || b["format"] != "mp3" || b["text"] != "Привіт" || sub(b, "prosody", "speed") != 1.2 || b["language"] != nil {
		t.Fatalf("body %v", b)
	}
	l, err := c.Voices(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, v := range l.Voices {
		ids = append(ids, v.ID)
	}
	if strings.Join(ids, ",") != "own1,pub-uk,pub-en" {
		t.Fatalf("voices %v", ids)
	}
	if v := l.Voices[0]; v.Category != "cloned" || v.PreviewURL != "https://x/own1.mp3" || v.Gender != "male" || v.Language != "uk" {
		t.Errorf("own %+v", v)
	}
	if v := l.Voices[1]; v.Category != "library" || v.Gender != "female" {
		t.Errorf("public %+v", v)
	}
}

func TestFishBalanceRunsOut(t *testing.T) {
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(402)
		_, _ = w.Write([]byte(`{"status":402,"message":"Insufficient balance"}`))
	})
	c, _ := cfgClient(config.TTS{Provider: "fish", APIKey: "fish-key-000001", Keys: []string{"fish-key-000002"}, Voice: "v"})
	_, err := c.Synthesize(context.Background(), "x", "")
	if err == nil || !strings.HasPrefix(err.Error(), "Усі ключі вичерпали ліміт до ") {
		t.Fatalf("err %v", err)
	}
}

// ---- OpenAI -----------------------------------------------------------------------------------

func TestOpenAILanguageAndQuota(t *testing.T) {
	r := &rec{}
	mode := ""
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		r.add(req)
		switch mode {
		case "quota":
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota"}}`))
			return
		case "rate":
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached","type":"requests","code":"rate_limit_exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	})
	c, cfg := cfgClient(config.TTS{Provider: "openai", APIKey: "sk-openai-0001"})
	if _, err := c.SynthesizeWith(context.Background(), "Привіт", "marin", SynthOptions{LanguageCode: "uk"}); err != nil {
		t.Fatal(err)
	}
	if b := r.last("/v1/audio/speech").body; b["instructions"] != "Speak Ukrainian with a natural native pronunciation." || b["voice"] != "marin" {
		t.Fatalf("body %v", b)
	}
	cfg.TTS.Model = "tts-1"
	_, _ = c.SynthesizeWith(context.Background(), "Привіт", "", SynthOptions{LanguageCode: "uk"})
	if b := r.last("/v1/audio/speech").body; b["instructions"] != nil {
		t.Fatalf("tts-1 takes no instructions: %v", b)
	}
	longRests(t)
	for m, want := range map[string]string{"quota": KeyExhausted, "rate": KeyResting} {
		mode = m
		c, _ := cfgClient(config.TTS{Provider: "openai", APIKey: "sk-openai-0001"})
		_, err := c.Synthesize(context.Background(), "x", "")
		if st := c.KeyStatus()[0]; err == nil || st.State != want {
			t.Errorf("%s → %v %+v", m, err, st)
		}
	}
}

// ---- Test (cheap checks) -------------------------------------------------------------------

func TestCheckKeysNeverSynthesizes(t *testing.T) {
	r := &rec{}
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		q := r.add(req)
		if strings.HasPrefix(q.path, "/v1/text-to-speech") {
			t.Errorf("a check synthesized speech: %s", q.path)
		}
		switch q.hdr.Get("xi-api-key") {
		case "el-good-0000001":
			_, _ = w.Write([]byte(`{"tier":"creator","character_count":1000,"character_limit":100000}`))
		case "el-spent-000002":
			_, _ = w.Write([]byte(`{"tier":"free","character_count":10000,"character_limit":10000}`))
		case "el-tts-only-003":
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"detail":{"status":"missing_permissions","message":"The API key you used is missing the permission user_read"}}`))
		default:
			w.WriteHeader(401)
			_, _ = w.Write([]byte(authBody))
		}
	})
	c, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "el-good-0000001", Keys: []string{"el-spent-000002", "el-tts-only-003", "el-bad-00000004"}})
	ok, msg := c.Test(context.Background())
	if !ok || !strings.Contains(msg, "працюють 2 з 4 ключів") || !strings.Contains(msg, "1 ключ вичерпав ліміт до") || !strings.Contains(msg, "невірний API-ключ") {
		t.Fatalf("ok %v msg %q", ok, msg)
	}
	st := c.KeyStatus()
	if st[0].State != KeyOK || st[0].UsedChars != 1000 || st[0].Limit != 100000 {
		t.Errorf("good %+v", st[0])
	}
	if st[1].State != KeyExhausted || st[1].UsedChars != 10000 {
		t.Errorf("spent %+v", st[1])
	}
	if st[2].State != KeyOK || st[3].State != KeyError {
		t.Errorf("restricted %+v bad %+v", st[2], st[3])
	}
	// One key: its own words.
	c1, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "el-good-0000001"})
	if ok, msg := c1.Test(context.Background()); !ok || msg != "ElevenLabs: ключ працює · тариф creator · залишилось 99 000 символів" {
		t.Fatalf("one key: %v %q", ok, msg)
	}
	c2, _ := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "el-bad-00000004"})
	if ok, msg := c2.Test(context.Background()); ok || !strings.HasPrefix(msg, "ElevenLabs: невірний API-ключ"+string(rune(10))+"Запит: GET ") ||
		!strings.Contains(msg, "→ 401 Unauthorized") || strings.Contains(msg, "el-bad-00000004") {
		t.Fatalf("bad key: %v %q", ok, msg)
	}
	// A check clears an error once the key works again.
	c1.pool.mark(c1.settings(), c1.settings().keys[0], &apiErr{kind: failAuth, msg: "old"}, "")
	if c1.KeyStatus()[0].State != KeyError {
		t.Fatal("mark")
	}
	c1.Test(context.Background())
	if st := c1.KeyStatus()[0]; st.State != KeyOK || st.LastError != "" {
		t.Fatalf("not cleared: %+v", st)
	}
}

func TestCheckOtherProviders(t *testing.T) {
	mockAll(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/voices": // Cartesia
			if req.Header.Get("Authorization") != "Bearer sk_car_ok_00001" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
		case "/v1/voices": // Google
			_, _ = w.Write([]byte(`{"voices":[{"name":"uk-UA-Chirp3-HD-Charon","languageCodes":["uk-UA"],"ssmlGender":"MALE"}]}`))
		case "/cognitiveservices/voices/list": // Azure
			_, _ = w.Write([]byte(azureVoicesJSON))
		case "/v1/get_voice": // MiniMax
			_, _ = w.Write([]byte(`{"voice_cloning":[],"base_resp":{"status_code":2049,"status_msg":"invalid api key"}}`))
		case "/wallet/self/api-credit": // Fish
			_, _ = w.Write([]byte(`{"credit":"12.500000","cumulative_top_up":"20"}`))
		case "/v1/models": // OpenAI
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini-tts"}]}`))
		case "/tts": // own server
			w.WriteHeader(405)
		default:
			t.Errorf("unexpected %s", req.URL.Path)
		}
	})
	cases := []struct {
		cfg  config.TTS
		ok   bool
		want string
	}{
		{config.TTS{Provider: "cartesia", APIKey: "sk_car_ok_00001"}, true, "Cartesia: ключ працює · модель sonic-3.6"},
		{config.TTS{Provider: "cartesia", APIKey: "sk_car_bad_0002"}, false, "Cartesia: невірний API-ключ"},
		{config.TTS{Provider: "google", APIKey: "AIzaSy-test-key-01"}, true, "1 голос для української"},
		{config.TTS{Provider: "azure", APIKey: "azure-key-000001", BaseURL: "westeurope"}, true, "українських 2"},
		{config.TTS{Provider: "azure", APIKey: "azure-key-000001"}, false, "вкажи регіон"},
		{config.TTS{Provider: "minimax", APIKey: "mm-key-0000001"}, false, "MiniMax: невірний API-ключ"},
		{config.TTS{Provider: "fish", APIKey: "fish-key-000001"}, true, "баланс 12.50 $"},
		{config.TTS{Provider: "openai", APIKey: "sk-openai-0001"}, true, "модель gpt-4o-mini-tts"},
		{config.TTS{Provider: "custom", BaseURL: "PLACEHOLDER/tts"}, true, "сервер відповідає"},
		{config.TTS{Provider: "fish"}, false, "Ключ Fish Audio не задано"},
	}
	for _, tc := range cases {
		if tc.cfg.Provider == "custom" {
			tc.cfg.BaseURL = strings.Replace(tc.cfg.BaseURL, "PLACEHOLDER", cartesiaBase, 1)
		}
		c, _ := cfgClient(tc.cfg)
		ok, msg := c.Test(context.Background())
		if ok != tc.ok || !strings.Contains(msg, tc.want) {
			t.Errorf("%+v → %v %q, want %v %q", tc.cfg, ok, msg, tc.ok, tc.want)
		}
	}
}

// ---- languages ------------------------------------------------------------------------------

func TestGuessLanguageAndLocales(t *testing.T) {
	for text, want := range map[string]string{
		"Привіт, як справи?":     "uk",
		"Добрий день":            "uk", // no telling letters: the app's audience
		"Это очень хорошо":       "ru",
		"Здравей, как си? Ъгъл":  "bg",
		"Ђорђе и Љубица":         "sr",
		"こんにちは、世界":               "ja",
		"你好世界":                   "zh",
		"안녕하세요":                  "ko",
		"Καλημέρα":               "el",
		"Hello world":            "",
		"12345 !!!":              "",
		"Hello everyone, привіт": "", // no majority script
	} {
		if got := guessLanguage(text); got != want {
			t.Errorf("guessLanguage(%q) = %q, want %q", text, got, want)
		}
	}
	for in, want := range map[[2]string]string{
		{"google", "uk"}: "uk-UA", {"google", "zh"}: "cmn-CN", {"azure", "zh"}: "zh-CN", {"google", "ar"}: "ar-XA",
		{"azure", "pt-PT"}: "pt-PT", {"google", "ukr"}: "uk-UA", {"azure", ""}: "", {"azure", "xx"}: "",
	} {
		if got := localeFor(in[0], in[1]); got != want {
			t.Errorf("localeFor(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
