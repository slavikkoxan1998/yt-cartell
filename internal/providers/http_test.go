package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/gen"
)

// fast shrinks the timings for the duration of a test.
func fast(t *testing.T) {
	t.Helper()
	pi, ipt, vpt, bb, bm, ram, ss := pollInterval, imagePollTimeout, videoPollTimeout, backoffBase, backoffMax, retryAfterMax, specSecond
	pollInterval, imagePollTimeout, videoPollTimeout = 2*time.Millisecond, 5*time.Second, 5*time.Second
	backoffBase, backoffMax, retryAfterMax = time.Millisecond, 4*time.Millisecond, 5*time.Millisecond
	specSecond = time.Millisecond // a spec's "every": 10 → 10 ms
	rit, rbu, rbm := royalImagePollTimeout, royalBackoffUnit, royalBackoffMax
	royalImagePollTimeout, royalBackoffUnit, royalBackoffMax = 5*time.Second, time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() {
		pollInterval, imagePollTimeout, videoPollTimeout, backoffBase, backoffMax, retryAfterMax, specSecond = pi, ipt, vpt, bb, bm, ram, ss
		royalImagePollTimeout, royalBackoffUnit, royalBackoffMax = rit, rbu, rbm
	})
}

func testEndpoint(base, key string) *endpoint {
	return &endpoint{
		name: "Test", base: base, key: key, model: "m1", hc: &http.Client{},
		auth: func(h http.Header, k string) { h.Set("Authorization", "Bearer "+k) },
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	const key = "sk-secret-0123456789abcdef"
	cases := []struct {
		status int
		body   string
		want   string
		retry  bool
	}{
		{401, `{"error":{"message":"Incorrect API key provided: ` + key + `"}}`, "ключ не прийнято (401)", false},
		{402, `{"title":"Payment required","detail":"You have insufficient credit"}`, "закінчились кошти", false},
		{429, `{"error":{"message":"You exceeded your current quota","code":"insufficient_quota"}}`, "закінчились кошти", false},
		{429, `{"detail":"Request was throttled"}`, "забагато запитів (429)", true},
		{403, `{"detail":"User is locked. Reason: Exhausted balance."}`, "закінчились кошти", false},
		{403, `{"detail":"Forbidden"}`, "доступ заборонено (403)", false},
		{400, `{"error":{"message":"Your request was rejected by the safety system","code":"moderation_blocked"}}`, "фільтром безпеки", false},
		{422, `{"detail":[{"loc":["body","duration"],"msg":"unexpected value","type":"literal_error"}]}`, "duration: unexpected value", false},
		{404, `{"detail":"Not found"}`, "перевір назву моделі «m1»", false},
		{413, ``, "запит завеликий (413)", false},
		{500, `<html>oops</html>`, "тимчасово недоступний (500)", true},
		{503, `{"message":"overloaded"}`, "спробуй пізніше: overloaded", true},
	}
	ep := testEndpoint("http://x", key)
	for _, c := range cases {
		e := ep.httpError(reply{status: c.status, body: []byte(c.body)})
		if !strings.Contains(e.Msg, c.want) {
			t.Errorf("%d %s → %q, want it to contain %q", c.status, c.body, e.Msg, c.want)
		}
		if e.Retry != c.retry {
			t.Errorf("%d %s → retry %v, want %v", c.status, c.body, e.Retry, c.retry)
		}
		if strings.Contains(e.Msg, key) || strings.Contains(e.Msg, "0123456789abcdef") {
			t.Errorf("key leaked: %q", e.Msg)
		}
	}
}

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz":                          "Bearer abcdefghij",
		"Incorrect API key provided: sk-proj-AAAAAAAAAAAAAAAAAAAA":                  "sk-proj-AAAA",
		"token r8_0123456789abcdefghij in body":                                     "r8_0123456789",
		"Key 0f0e0d0c-1111-2222-3333-444455556666:00112233445566778899aabbccddeeff": "0f0e0d0c-1111",
		"https://x/files?key=SECRETVALUE123&x=1":                                    "SECRETVALUE123",
	}
	for in, leak := range cases {
		out := redact(in)
		if strings.Contains(out, leak) {
			t.Errorf("redact(%q) = %q still contains %q", in, out, leak)
		}
	}
	if got := redact("my key is hunter2-long-key", "hunter2-long-key"); strings.Contains(got, "hunter2") {
		t.Errorf("configured key not redacted: %q", got)
	}
	if got := redact("abc", "abc"); got != "abc" {
		t.Errorf("a too short key must not garble the text: %q", got)
	}
}

func TestCallRetriesRateLimitThenSucceeds(t *testing.T) {
	fast(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()
	ep := testEndpoint(srv.URL, "k-123456")
	res, err := ep.call(context.Background(), request{method: http.MethodPost, url: "/x", body: []byte(`{}`), create: true})
	if err != nil || string(res.body) != `{"ok":true}` || hits.Load() != 3 {
		t.Fatalf("res=%q err=%v hits=%d", res.body, err, hits.Load())
	}
}

func TestCallCreateNotRetriedOn500(t *testing.T) {
	fast(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()
	ep := testEndpoint(srv.URL, "k-123456")
	_, err := ep.call(context.Background(), request{method: http.MethodPost, url: "/x", body: []byte(`{}`), create: true})
	if err == nil || hits.Load() != 1 {
		t.Fatalf("create must not be repeated after a 500: err=%v hits=%d", err, hits.Load())
	}
	hits.Store(0)
	_, err = ep.call(context.Background(), request{method: http.MethodGet, url: "/x"})
	if err == nil || int(hits.Load()) != maxRetries+1 {
		t.Fatalf("GET must be retried: err=%v hits=%d", err, hits.Load())
	}
}

func TestDownloadLimitAndAuthOrigin(t *testing.T) {
	fast(t)
	var auths []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, "api="+r.Header.Get("Authorization"))
		fmt.Fprint(w, strings.Repeat("a", 100))
	}))
	defer api.Close()
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, "cdn="+r.Header.Get("Authorization"))
		fmt.Fprint(w, strings.Repeat("b", 100))
	}))
	defer cdn.Close()
	ep := testEndpoint(api.URL, "k-123456")
	if _, _, err := ep.download(context.Background(), cdn.URL+"/f.mp4", 50); err == nil || !strings.Contains(err.Error(), "завелик") {
		t.Fatalf("over-limit download: %v", err)
	}
	if b, _, err := ep.download(context.Background(), "/files/f.png", 1000); err != nil || len(b) != 100 {
		t.Fatalf("relative download: %d %v", len(b), err)
	}
	if b, _, err := ep.download(context.Background(), cdn.URL+"/f.mp4", 1000); err != nil || len(b) != 100 {
		t.Fatalf("cdn download: %d %v", len(b), err)
	}
	want := []string{"cdn=", "api=Bearer k-123456", "cdn="}
	if strings.Join(auths, "|") != strings.Join(want, "|") {
		t.Errorf("auth per origin = %v, want %v (the key goes only to the API origin)", auths, want)
	}
	if _, _, err := ep.download(context.Background(), "file:///etc/passwd", 1000); err == nil {
		t.Error("non-http scheme must be refused")
	}
	data, mt, err := ep.download(context.Background(), "data:image/png;base64,aGVsbG8=", 1000)
	if err != nil || string(data) != "hello" || mt != "image/png" {
		t.Errorf("data URI: %q %q %v", data, mt, err)
	}
}

func TestPollProgressBlipsAndCancel(t *testing.T) {
	fast(t)
	ep := testEndpoint("http://x", "k")
	var seen []int
	n := 0
	err := ep.poll(context.Background(), time.Second, time.Second, func(p int, _ string) { seen = append(seen, p) },
		func(ctx context.Context) (pollState, error) {
			n++
			switch n {
			case 1:
				return pollState{queued: true}, nil
			case 2, 3:
				return pollState{}, &Error{Msg: "blip", Retry: true}
			case 4:
				return pollState{pct: 50}, nil
			}
			return pollState{done: true}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen[0] != 10 || seen[1] != 55 || seen[2] != 92 {
		t.Errorf("progress = %v", seen)
	}
	// a fatal error stops at once
	err = ep.poll(context.Background(), time.Second, time.Second, nil, func(ctx context.Context) (pollState, error) {
		return pollState{}, &Error{Msg: "fatal"}
	})
	if err == nil || err.Error() != "fatal" {
		t.Errorf("fatal: %v", err)
	}
	// cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = ep.poll(ctx, time.Second, time.Second, nil, func(ctx context.Context) (pollState, error) {
		return pollState{pct: -1}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
}

func TestRefLegend(t *testing.T) {
	refs := []gen.RefImage{{Data: []byte{1}, Name: "taras.jpg"}, {Data: []byte{1}}, {Data: []byte{1}, Name: "@kyiv.png"}}
	got := refLegend(refs)
	if !strings.Contains(got, "image 1 = @taras, image 3 = @kyiv") {
		t.Errorf("legend = %q", got)
	}
	if refLegend(refs[1:2]) != "" {
		t.Error("unnamed refs need no legend")
	}
}
