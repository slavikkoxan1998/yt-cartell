package tts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// TestMain keeps every test away from the user's files and the real services:
// the settings folder (DefaultStateFile) points into a temp dir, and every
// endpoint at a closed port until a test installs its mock.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ytc-tts-test-")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Setenv("AppData", filepath.Join(tmp, "appdata"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	if runtime.GOOS == "darwin" {
		os.Setenv("HOME", filepath.Join(tmp, "home"))
	}
	if !strings.HasPrefix(DefaultStateFile(), tmp) {
		fmt.Println("refusing to run: the key-state file is not redirected:", DefaultStateFile())
		os.Exit(1)
	}
	nowhere := "http://127.0.0.1:9"
	elevenBase, openaiBase, cartesiaBase, googleBase, minimaxBase, fishBase = nowhere, nowhere+"/v1", nowhere, nowhere, nowhere, nowhere
	azureEndpoint = func(string) string { return nowhere }
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// mockAll points every provider endpoint at one test server and shrinks the
// backoff.
func mockAll(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	eb, ob, cb, gb, mb, fb, ae, rb := elevenBase, openaiBase, cartesiaBase, googleBase, minimaxBase, fishBase, azureEndpoint, retryBase
	elevenBase, openaiBase, cartesiaBase, googleBase, minimaxBase, fishBase = srv.URL, srv.URL+"/v1", srv.URL, srv.URL, srv.URL, srv.URL
	azureEndpoint = func(string) string { return srv.URL }
	retryBase = time.Millisecond
	t.Cleanup(func() {
		srv.Close()
		elevenBase, openaiBase, cartesiaBase, googleBase, minimaxBase, fishBase, azureEndpoint, retryBase = eb, ob, cb, gb, mb, fb, ae, rb
	})
	return srv
}

// longRests makes rate limits observable: a long rest that requests do not
// wait out (call it after mockAll, which shortens the backoff).
func longRests(t *testing.T) {
	t.Helper()
	mw, rb := maxWaitRest, retryBase
	maxWaitRest, retryBase = 0, 30*time.Second
	t.Cleanup(func() { maxWaitRest, retryBase = mw, rb })
}

// fakeClock runs at real speed from a chosen moment and can jump forward.
type fakeClock struct {
	mu     sync.Mutex
	base   time.Time
	start  time.Time
	offset time.Duration
}

func newClock(base time.Time) *fakeClock { return &fakeClock{base: base, start: time.Now()} }

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.base.Add(time.Since(f.start) + f.offset)
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.offset += d
	f.mu.Unlock()
}

// set jumps to t.
func (f *fakeClock) set(t time.Time) {
	f.mu.Lock()
	f.base, f.start, f.offset = t, time.Now(), 0
	f.mu.Unlock()
}

// keyServer is an ElevenLabs mock whose reply depends on the key.
type keyServer struct {
	mu    sync.Mutex
	used  []string                        // xi-api-key of every TTS request, in order
	reply map[string]func() (int, string) // key → status, body (missing = 200 audio)
}

func (k *keyServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") {
			w.WriteHeader(404) // /v1/models → documented tables
			return
		}
		key := r.Header.Get("xi-api-key")
		k.mu.Lock()
		k.used = append(k.used, key)
		f := k.reply[key]
		k.mu.Unlock()
		if f != nil {
			if code, body := f(); code != 200 {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3:" + key))
	}
}

func (k *keyServer) take() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := k.used
	k.used = nil
	return out
}

func (k *keyServer) set(key string, f func() (int, string)) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.reply == nil {
		k.reply = map[string]func() (int, string){}
	}
	k.reply[key] = f
}

func always(code int, body string) func() (int, string) {
	return func() (int, string) { return code, body }
}

const (
	quotaBody = `{"detail":{"type":"payment_required","code":"insufficient_credits","message":"Not enough credits"}}`
	authBody  = `{"detail":{"type":"authentication_error","code":"invalid_api_key","message":"Invalid API key"}}`
	rateBody  = `{"detail":{"type":"rate_limit_error","code":"concurrent_limit_exceeded","message":"Too many concurrent requests"}}`
)

// elevenKeys is an ElevenLabs client with keys rotating and a fake clock at
// 15 Oct 2026, 12:00 local time.
func elevenKeys(t *testing.T, keys ...string) (*Client, *config.Config, *fakeClock) {
	t.Helper()
	c, cfg := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: keys[0], Keys: keys[1:], Voice: "v1"})
	clk := newClock(time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local))
	c.pool.now = clk.now
	return c, cfg, clk
}

func statusOf(t *testing.T, c *Client) map[string]KeyStatus {
	t.Helper()
	out := map[string]KeyStatus{}
	for _, st := range c.KeyStatus() {
		out[st.Mask] = st
	}
	return out
}

func say(t *testing.T, c *Client, text string) string {
	t.Helper()
	b, err := c.Synthesize(context.Background(), text, "")
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	return string(b)
}

func TestRotationRoundRobin(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, _ := elevenKeys(t, "key-aaaaaaaa-1", "key-bbbbbbbb-2", "key-cccccccc-3")
	for i := 0; i < 6; i++ {
		say(t, c, fmt.Sprint("line ", i))
	}
	got := strings.Join(ks.take(), ",")
	want := "key-aaaaaaaa-1,key-bbbbbbbb-2,key-cccccccc-3,key-aaaaaaaa-1,key-bbbbbbbb-2,key-cccccccc-3"
	if got != want {
		t.Fatalf("order\n got %s\nwant %s", got, want)
	}
	for _, st := range c.KeyStatus() {
		if st.State != KeyOK || st.UsedChars != 12 || st.LastUsed.IsZero() {
			t.Errorf("status %+v", st) // two lines of 6 characters each
		}
		if strings.Contains(st.Mask, "aaaaaaaa") || strings.Contains(st.Mask, "bbbbbbbb") {
			t.Errorf("mask reveals the key: %q", st.Mask)
		}
	}
}

func TestRotationRespectsConcurrency(t *testing.T) {
	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	mockAll(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") {
			w.WriteHeader(404)
			return
		}
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	})
	c, _, _ := elevenKeys(t, "only-key-0000001")
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Synthesize(context.Background(), "hello", "")
			errs <- err
		}()
	}
	for deadline := time.Now().Add(5 * time.Second); inFlight.Load() < 2 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // the other four had time to (wrongly) start
	if p := peak.Load(); p != 2 {
		t.Fatalf("%d requests at once on one ElevenLabs key, want 2", p)
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestExhaustedKeyRestsUntilFirstOfNextMonth(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, clk := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	ks.set("key-one-0000001", always(402, quotaBody))

	if out := say(t, c, "first"); out != "mp3:key-two-0000002" {
		t.Fatalf("out %q", out)
	}
	if got := strings.Join(ks.take(), ","); got != "key-one-0000001,key-two-0000002" {
		t.Fatalf("calls %s", got)
	}
	one := c.KeyStatus()[0]
	if one.State != KeyExhausted || !one.Until.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("exhausted key: %+v", one)
	}
	if !strings.Contains(one.LastError, "закінчились") {
		t.Errorf("last error %q", one.LastError)
	}
	// Skipped for the rest of the month, even when its quota came back.
	ks.set("key-one-0000001", nil)
	clk.set(time.Date(2026, 10, 31, 23, 59, 0, 0, time.Local))
	say(t, c, "second")
	say(t, c, "third")
	if got := strings.Join(ks.take(), ","); got != "key-two-0000002,key-two-0000002" {
		t.Fatalf("an exhausted key was used before the 1st: %s", got)
	}
	// 00:00 on the 1st: the key is back.
	clk.set(time.Date(2026, 11, 1, 0, 0, 1, 0, time.Local))
	if st := c.KeyStatus()[0]; st.State != KeyOK || !st.Until.IsZero() || st.LastError != "" {
		t.Fatalf("after the 1st: %+v", st)
	}
	say(t, c, "fourth")
	say(t, c, "fifth")
	if got := strings.Join(ks.take(), ","); !strings.Contains(got, "key-one-0000001") {
		t.Fatalf("renewed key not used: %s", got)
	}
}

func TestExhaustedInDecemberRestsUntilJanuary(t *testing.T) {
	if got := firstOfNextMonth(time.Date(2026, 12, 31, 23, 0, 0, 0, time.Local)); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("got %v", got)
	}
	if got := dayMonth(time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local)); got != "1 січня" {
		t.Fatalf("got %q", got)
	}
}

func TestAllKeysExhausted(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, _ := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	ks.set("key-one-0000001", always(401, `{"detail":{"status":"quota_exceeded","message":"This request exceeds your quota"}}`))
	ks.set("key-two-0000002", always(402, quotaBody))
	_, err := c.Synthesize(context.Background(), "hello", "")
	if err == nil || !strings.HasPrefix(err.Error(), "Усі ключі вичерпали ліміт до 1 листопада") {
		t.Fatalf("err = %v", err)
	}
	// The next request does not even call the service.
	ks.take()
	if _, err := c.Synthesize(context.Background(), "again", ""); err == nil || !strings.Contains(err.Error(), "Усі ключі вичерпали ліміт до 1 листопада") {
		t.Fatalf("err = %v", err)
	}
	if calls := ks.take(); len(calls) != 0 {
		t.Fatalf("exhausted keys were called: %v", calls)
	}
}

func TestErrorKeySkippedRetriedAndReset(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, cfg, clk := elevenKeys(t, "bad-key-0000001", "good-key-000002")
	ks.set("bad-key-0000001", always(401, authBody))

	if out := say(t, c, "one"); out != "mp3:good-key-000002" {
		t.Fatalf("out %q", out)
	}
	bad := c.KeyStatus()[0]
	if bad.State != KeyError || bad.LastError != "ElevenLabs: невірний API-ключ" {
		t.Fatalf("bad key: %+v", bad)
	}
	if d := bad.Until.Sub(clk.now()); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("error rest %v, want ~1 h", d)
	}
	ks.take()
	say(t, c, "two")
	say(t, c, "three")
	if got := strings.Join(ks.take(), ","); got != "good-key-000002,good-key-000002" {
		t.Fatalf("an erroring key was used: %s", got)
	}
	// An hour later it gets another chance (and fails again).
	clk.advance(61 * time.Minute)
	say(t, c, "four")
	say(t, c, "five")
	if got := strings.Join(ks.take(), ","); !strings.Contains(got, "bad-key-0000001") {
		t.Fatalf("not retried after an hour: %s", got)
	}
	if st := c.KeyStatus()[0]; st.State != KeyError {
		t.Fatalf("state %+v", st)
	}
	// «Скинути помилки»: usable right away.
	ks.set("bad-key-0000001", nil)
	c.ResetKeyErrors()
	if st := c.KeyStatus()[0]; st.State != KeyOK || st.LastError != "" {
		t.Fatalf("after reset %+v", st)
	}
	// Editing the key list also gives failing keys another chance.
	ks.set("bad-key-0000001", always(401, authBody))
	say(t, c, "six")
	say(t, c, "seven")
	if st := c.KeyStatus()[0]; st.State != KeyError {
		t.Fatalf("state %+v", st)
	}
	cfg.TTS.Keys = append(cfg.TTS.Keys, "new-key-0000003")
	if st := c.KeyStatus()[0]; st.State != KeyOK {
		t.Fatalf("after editing the keys: %+v", st)
	}
}

func TestExhaustedSurvivesResetAndKeyEdits(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, cfg, _ := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	ks.set("key-one-0000001", always(402, quotaBody))
	say(t, c, "x")
	c.ResetKeyErrors()
	cfg.TTS.Keys = []string{"key-two-0000002", "key-three-00003"}
	if st := c.KeyStatus()[0]; st.State != KeyExhausted {
		t.Fatalf("exhausted key reset: %+v", st)
	}
}

func TestOnlyKeyFailures(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, _ := elevenKeys(t, "solo-key-000001")
	ks.set("solo-key-000001", always(401, authBody))
	if _, err := c.Synthesize(context.Background(), "x", ""); err == nil || err.Error() != "ElevenLabs: невірний API-ключ" {
		t.Fatalf("err %v", err)
	}
	// The next request fails at once with the same words, without a call.
	ks.take()
	if _, err := c.Synthesize(context.Background(), "x", ""); err == nil || err.Error() != "ElevenLabs: невірний API-ключ" {
		t.Fatalf("err %v", err)
	}
	if n := len(ks.take()); n != 0 {
		t.Fatalf("%d calls with a failing key", n)
	}
	// Mixed: one exhausted, one failing.
	c2, _, _ := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	ks.set("key-one-0000001", always(402, quotaBody))
	ks.set("key-two-0000002", always(401, authBody))
	_, err := c2.Synthesize(context.Background(), "x", "")
	if err == nil || !strings.Contains(err.Error(), "1 ключ вичерпав ліміт до 1 листопада") || !strings.Contains(err.Error(), "1 ключ з помилкою (ElevenLabs: невірний API-ключ)") {
		t.Fatalf("err %v", err)
	}
}

func TestTransientFailureMovesToNextKey(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, _ := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	ks.set("key-one-0000001", always(503, "upstream down"))
	if out := say(t, c, "x"); out != "mp3:key-two-0000002" {
		t.Fatalf("out %q", out)
	}
	if st := c.KeyStatus()[0]; st.State != KeyOK {
		t.Fatalf("a 5xx blamed the key: %+v", st)
	}
}

func TestRateLimitedKeyRests(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, _ := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	var n atomic.Int32
	ks.set("key-one-0000001", func() (int, string) {
		if n.Add(1) == 1 {
			return 429, rateBody
		}
		return 200, ""
	})
	saved := retryBase
	retryBase = 30 * time.Second // a long rest: visible in the status
	t.Cleanup(func() { retryBase = saved })
	if out := say(t, c, "x"); out != "mp3:key-two-0000002" {
		t.Fatalf("out %q", out)
	}
	st := c.KeyStatus()[0]
	if st.State != KeyResting || st.Until.IsZero() || !strings.Contains(st.LastError, "забагато") {
		t.Fatalf("rate-limited key: %+v", st)
	}
	c.ResetKeyErrors()
	if st := c.KeyStatus()[0]; st.State != KeyOK {
		t.Fatalf("reset %+v", st)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	var calls atomic.Int32
	mockAll(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") {
			w.WriteHeader(404)
			return
		}
		calls.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(rateBody))
	})
	c, _, clk := elevenKeys(t, "solo-key-000001")
	_, err := c.Synthesize(context.Background(), "x", "")
	if err == nil || !strings.Contains(err.Error(), "тимчасово обмежений до 12:10") {
		t.Fatalf("err %v", err)
	}
	st := c.KeyStatus()[0]
	if d := st.Until.Sub(clk.now()); st.State != KeyResting || d < 9*time.Minute || d > 10*time.Minute {
		t.Fatalf("status %+v (rest %v)", st, d)
	}
	if calls.Load() != 1 {
		t.Fatalf("%d calls (a 10-minute Retry-After must not be waited out)", calls.Load())
	}
}

func TestVoiceOfAnotherAccount(t *testing.T) {
	var mu sync.Mutex
	var log []string
	mockAll(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") {
			w.WriteHeader(404)
			return
		}
		key, voice := r.Header.Get("xi-api-key"), strings.TrimPrefix(r.URL.Path, "/v1/text-to-speech/")
		mu.Lock()
		log = append(log, key+">"+voice)
		mu.Unlock()
		if voice == "cloned" && key != "key-two-0000002" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"detail":{"type":"not_found","code":"voice_not_found","message":"A voice with the voice_id cloned was not found."}}`))
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	})
	c, _, _ := elevenKeys(t, "key-one-0000001", "key-two-0000002")
	for i := 0; i < 3; i++ {
		if _, err := c.Synthesize(context.Background(), "x", "cloned"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := strings.Join(log, ",")
	log = nil
	mu.Unlock()
	// The first request finds out; later ones go straight to the right account.
	if got != "key-one-0000001>cloned,key-two-0000002>cloned,key-two-0000002>cloned,key-two-0000002>cloned" {
		t.Fatalf("calls %s", got)
	}
	if st := c.KeyStatus()[0]; st.State != KeyOK {
		t.Fatalf("a missing voice blamed the key: %+v", st)
	}
	// Other voices still rotate over both keys.
	_, _ = c.Synthesize(context.Background(), "x", "premade")
	_, _ = c.Synthesize(context.Background(), "x", "premade")
	mu.Lock()
	got = strings.Join(log, ",")
	mu.Unlock()
	if !strings.Contains(got, "key-one-0000001>premade") {
		t.Fatalf("calls %s", got)
	}
}

func TestKeyStatusOrderMaskAndShared(t *testing.T) {
	c, _ := cfgClient(config.TTS{Provider: "cartesia", APIKey: " sk_car_first_000001 ", Keys: []string{"", "sk_car_second_00002", "sk_car_first_000001", "sk_car_third_000003"}})
	st := c.KeyStatus()
	if len(st) != 3 {
		t.Fatalf("%d statuses: %+v", len(st), st)
	}
	for i, want := range []int{0, 2, 4} {
		if st[i].Index != want || st[i].State != KeyOK || st[i].Shared {
			t.Errorf("status %d: %+v", i, st[i])
		}
		if strings.Contains(st[i].Mask, "first") || strings.Contains(st[i].Mask, "second") || st[i].Mask == "" {
			t.Errorf("mask %q", st[i].Mask)
		}
	}
	// Google Cloud without a TTS key borrows the Google AI Studio keys.
	cfg := &config.Config{TTS: config.TTS{Provider: "google"}, Google: config.Google{Keys: []string{"AIzaSy-google-key-1", "AIzaSy-google-key-2"}}}
	g := New(func() config.Config { return *cfg })
	gs := g.KeyStatus()
	if len(gs) != 2 || !gs[0].Shared || gs[1].Index != 1 {
		t.Fatalf("shared google keys: %+v", gs)
	}
	cfg.TTS.APIKey = "AIzaSy-own-tts-key"
	if gs := g.KeyStatus(); len(gs) != 1 || gs[0].Shared {
		t.Fatalf("own key: %+v", gs)
	}
	// Another provider: no keys at all.
	if st := (New(func() config.Config { return config.Config{TTS: config.TTS{Provider: "fish"}} })).KeyStatus(); len(st) != 0 {
		t.Fatalf("no keys: %+v", st)
	}
}

func TestUsedCharsRestartEveryMonth(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, clk := elevenKeys(t, "solo-key-000001")
	say(t, c, "Привіт, світе") // 13 characters
	if st := c.KeyStatus()[0]; st.UsedChars != 13 {
		t.Fatalf("used %d", st.UsedChars)
	}
	clk.set(time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local))
	if st := c.KeyStatus()[0]; st.UsedChars != 0 {
		t.Fatalf("a new month starts at 0, got %d", st.UsedChars)
	}
	say(t, c, "abc")
	if st := c.KeyStatus()[0]; st.UsedChars != 3 {
		t.Fatalf("used %d", st.UsedChars)
	}
}

func TestStatePersistsInTheGivenFile(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	dir := t.TempDir()
	path := filepath.Join(dir, KeyStateFile)

	c, cfg, clk := elevenKeys(t, "sk_secret_one_0001", "sk_secret_two_0002", "sk_secret_tri_0003")
	if err := c.SetStateFile(path); err != nil {
		t.Fatal(err)
	}
	ks.set("sk_secret_one_0001", always(402, quotaBody))
	ks.set("sk_secret_two_0002", always(401, authBody))
	say(t, c, "hello")
	if err := c.FlushKeyState(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"sk_secret_one_0001", "sk_secret_two_0002", "sk_secret_tri_0003"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("the state file contains a key")
		}
	}
	var doc stateDoc
	if err := json.Unmarshal(b, &doc); err != nil || doc.Version != 1 || len(doc.Keys) != 3 {
		t.Fatalf("file %s (%v)", b, err)
	}

	// A new client (next app start) reads the same states.
	c2 := New(func() config.Config { return *cfg })
	c2.pool.now = clk.now
	if err := c2.SetStateFile(path); err != nil {
		t.Fatal(err)
	}
	st := c2.KeyStatus()
	if st[0].State != KeyExhausted || !st[0].Until.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("exhausted lost: %+v", st[0])
	}
	if st[1].State != KeyError || st[1].LastError == "" {
		t.Fatalf("error lost: %+v", st[1])
	}
	if st[2].State != KeyOK || st[2].UsedChars != 5 || st[2].LastUsed.IsZero() {
		t.Fatalf("usage lost: %+v", st[2])
	}
	ks.take()
	say(t, c2, "again")
	if got := strings.Join(ks.take(), ","); got != "sk_secret_tri_0003" {
		t.Fatalf("restored states ignored: %s", got)
	}

	// A damaged file: an error, states start clean, the next save rewrites it.
	if err := os.WriteFile(path, []byte("{oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	c3 := New(func() config.Config { return *cfg })
	if err := c3.SetStateFile(path); err == nil {
		t.Fatal("damaged file accepted silently")
	}
	if st := c3.KeyStatus(); st[0].State != KeyOK {
		t.Fatalf("clean start: %+v", st[0])
	}
	say(t, c3, "x")
	if err := c3.FlushKeyState(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); json.Unmarshal(b, &doc) != nil {
		t.Fatalf("not rewritten: %s", b)
	}
}

func TestMemoryStateWritesNothing(t *testing.T) {
	ks := &keyServer{}
	mockAll(t, ks.handler(t))
	c, _, _ := elevenKeys(t, "key-one-0000001")
	ks.set("key-one-0000001", always(402, quotaBody))
	_, _ = c.Synthesize(context.Background(), "x", "")
	if err := c.FlushKeyState(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DefaultStateFile()); !os.IsNotExist(err) {
		t.Fatalf("an in-memory client wrote %s (%v)", DefaultStateFile(), err)
	}
}

func TestCancelWhileWaitingForAKey(t *testing.T) {
	release := make(chan struct{})
	mockAll(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") {
			w.WriteHeader(404)
			return
		}
		<-release
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("mp3"))
	})
	defer close(release)
	c, _, _ := elevenKeys(t, "solo-key-000001")
	for i := 0; i < 2; i++ { // fill both slots
		go func() { _, _ = c.Synthesize(context.Background(), "busy", "") }()
	}
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Synthesize(ctx, "waits", ""); err != context.DeadlineExceeded {
		t.Fatalf("err %v", err)
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{1: "ключ", 2: "ключі", 4: "ключі", 5: "ключів", 11: "ключів", 12: "ключів", 21: "ключ", 22: "ключі", 25: "ключів", 111: "ключів"} {
		if got := plural(n, "ключ", "ключі", "ключів"); got != want {
			t.Errorf("%d → %s, want %s", n, got, want)
		}
	}
}

// drain reads a request body (helper for the provider mocks).
func drain(r *http.Request) map[string]any {
	b, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
