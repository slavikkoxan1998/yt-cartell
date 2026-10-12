package glabs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

// fast shrinks the poll timings for the duration of a test.
func fast(t *testing.T) {
	t.Helper()
	pi, pt := pollInterval, pollTimeout
	pollInterval, pollTimeout = 2*time.Millisecond, 5*time.Second
	t.Cleanup(func() { pollInterval, pollTimeout = pi, pt })
}

func testClient(base, key string) *Client {
	cfg := config.Config{
		Glabs: config.Glabs{BaseURL: base + "/", APIKey: key},
		Video: config.Video{Provider: "glabs", GlabsModel: "veo_31_quality", Resolution: "1080p", Length: 6},
	}
	return New(func() config.Config { return cfg })
}

// mock is a scripted G-Labs server.
type mock struct {
	t        *testing.T
	mu       sync.Mutex
	submits  []map[string]json.RawMessage
	statuses []string // status replies, consumed in order (last one repeats)
	codes    []int    // optional HTTP codes per status call (0 = 200)
	hits     atomic.Int32
	files    map[string]string // path → body
	fileType string
	keys     []string // X-API-Key per request path
	stops    []string // escaped paths of POST /api/stop… requests (+ " " + body)
	srv      *httptest.Server
}

func newMock(t *testing.T, statuses ...string) *mock {
	m := &mock{t: t, statuses: statuses, files: map[string]string{}, fileType: "application/octet-stream"}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mock) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.keys = append(m.keys, r.URL.Path+"="+r.Header.Get("X-API-Key"))
	m.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/stop"):
		b, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.stops = append(m.stops, r.URL.EscapedPath()+" "+string(b))
		m.mu.Unlock()
		_, _ = w.Write([]byte(`{"stopped":["task/1"],"skipped":[],"count":1}`))
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/generate"):
		if r.Header.Get("X-API-Key") != "k1" {
			w.WriteHeader(401)
			return
		}
		var body map[string]json.RawMessage
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil {
			m.t.Errorf("submit body is not JSON: %v", err)
		}
		m.mu.Lock()
		m.submits = append(m.submits, body)
		m.mu.Unlock()
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"task_id":"task/1"}`))
	case strings.HasPrefix(r.URL.Path, "/api/status/"):
		if got := strings.TrimPrefix(r.URL.EscapedPath(), "/api/status/"); got != "task%2F1" {
			m.t.Errorf("status path id = %q, want escaped task id", got)
		}
		n := int(m.hits.Add(1)) - 1
		if n < len(m.codes) && m.codes[n] != 0 {
			w.WriteHeader(m.codes[n])
			return
		}
		i := min(n, len(m.statuses)-1)
		_, _ = w.Write([]byte(m.statuses[i]))
	default:
		body, ok := m.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", m.fileType)
		_, _ = w.Write([]byte(body))
	}
}

func pngBytes() []byte {
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
	return b
}

func TestImageSubmitPollDownloadNamedRefs(t *testing.T) {
	fast(t)
	m := newMock(t,
		`{"status":"pending"}`,
		`{"status":"running"}`,
		`{"status":"completed","results":["api/files/img.jpg"]}`)
	m.files["/api/files/img.jpg"] = "not-a-real-jpeg"
	c := testClient(m.srv.URL, "k1")

	png := pngBytes()
	data, mimeType, err := c.Image(context.Background(), gen.ImageRequest{
		Prompt:  "@taras at the gate",
		UIModel: "GEM_PIX_2",
		Aspect:  "21:9",
		Refs: []gen.RefImage{
			{Data: png, MIME: "image/png", Name: "taras.png"},
			{Data: []byte{0xff, 0xd8, 0xff, 0xe0}, MIME: "image/jpeg"},
			{Data: nil, Name: "empty.png"}, // skipped
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not-a-real-jpeg" || mimeType != "image/jpeg" {
		t.Fatalf("got %q %q", data, mimeType)
	}
	if len(m.submits) != 1 {
		t.Fatalf("submits = %d", len(m.submits))
	}
	s := m.submits[0]
	want := map[string]string{
		"prompt":       `"@taras at the gate"`,
		"model":        `"nano_banana_pro"`,
		"aspect_ratio": `"16:9"`,
		"reference_images": `[{"data":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(png) +
			`","name":"taras.png"},"data:image/jpeg;base64,/9j/4A=="]`,
	}
	for k, v := range want {
		if string(s[k]) != v {
			t.Errorf("%s = %s, want %s", k, s[k], v)
		}
	}
	if len(s) != len(want) {
		t.Errorf("unexpected fields in image body: %v", keys(s))
	}
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestImageRefsCappedAndMIMESniffed(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"completed","results":["/api/files/a.bin"]}`)
	m.files["/api/files/a.bin"] = string(pngBytes())
	c := testClient(m.srv.URL, "k1")
	refs := make([]gen.RefImage, 12)
	for i := range refs {
		refs[i] = gen.RefImage{Data: pngBytes()} // no MIME → sniffed
	}
	_, mimeType, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p", Aspect: "3:4", Refs: refs})
	if err != nil {
		t.Fatal(err)
	}
	if mimeType != "image/png" {
		t.Errorf("mime = %q", mimeType)
	}
	var got []string
	if err := json.Unmarshal(m.submits[0]["reference_images"], &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 || !strings.HasPrefix(got[0], "data:image/png;base64,") {
		t.Errorf("refs = %d, first %q", len(got), got[0][:30])
	}
	if a := string(m.submits[0]["aspect_ratio"]); a != `"3:4"` {
		t.Errorf("aspect = %s", a)
	}
	if md := string(m.submits[0]["model"]); md != `"nano_banana_2"` {
		t.Errorf("model = %s", md)
	}
}

func TestVideoStartImageProgressAndPick(t *testing.T) {
	fast(t)
	m := newMock(t,
		`{"status":"pending"}`,
		`{"status":"running"}`,
		`{"status":"running"}`,
		"") // replaced below once the URL is known
	m.statuses[3] = `{"status":"completed","results":["` + m.srv.URL + `/out/cover.png","` + m.srv.URL + `/out/clip.MP4?sig=1"]}`
	m.files["/out/clip.MP4"] = "mp4-bytes"
	m.files["/out/cover.png"] = "png-bytes"
	c := testClient(m.srv.URL, "k1")

	type ev struct {
		pct   int
		phase string
	}
	var evs []ev
	out, err := c.Video(context.Background(), gen.VideoRequest{
		Prompt: "slow push in",
		Start:  &gen.RefImage{Data: []byte{1, 2, 3}, MIME: "image/webp"},
		Aspect: "9:16",
	}, func(p int, ph string) { evs = append(evs, ev{p, ph}) })
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "mp4-bytes" {
		t.Fatalf("picked %q, want the mp4", out)
	}
	s := m.submits[0]
	want := map[string]string{
		"prompt":           `"slow push in"`,
		"model":            `"veo_31_quality"`,
		"aspect_ratio":     `"9:16"`,
		"mode":             `"start_image"`,
		"reference_images": `["data:image/webp;base64,AQID"]`,
		"resolution":       `["1080p"]`,
		"video_length":     `6`,
	}
	for k, v := range want {
		if string(s[k]) != v {
			t.Errorf("%s = %s, want %s", k, s[k], v)
		}
	}
	if len(s) != len(want) {
		t.Errorf("unexpected fields in video body: %v", keys(s))
	}
	if len(evs) != 5 || evs[0] != (ev{10, "queued"}) || evs[1] != (ev{10, "queued"}) ||
		evs[2].phase != "rendering" || evs[2].pct < 20 || evs[3].pct > 90 || evs[4] != (ev{95, "rendered"}) {
		t.Errorf("progress = %v", evs)
	}
	// the key goes back to the G-Labs server for downloads, never elsewhere
	for _, k := range m.keys {
		if !strings.HasSuffix(k, "=k1") {
			t.Errorf("request without key: %s", k)
		}
	}
}

func TestVideoTextToVideoOverrides(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"completed","results":["/f/x.mp4"]}`)
	m.files["/f/x.mp4"] = "v"
	c := testClient(m.srv.URL, "k1")
	if _, err := c.Video(context.Background(), gen.VideoRequest{
		Prompt: "p", Aspect: "1:1", Seconds: 7, Model: "veo_31_lite", Resolution: "4K",
	}, nil); err != nil {
		t.Fatal(err)
	}
	s := m.submits[0]
	for k, v := range map[string]string{
		"mode": `"text_to_video"`, "reference_images": `[]`, "aspect_ratio": `"16:9"`,
		"model": `"veo_31_lite"`, "resolution": `["4K"]`, "video_length": `8`,
	} {
		if string(s[k]) != v {
			t.Errorf("%s = %s, want %s", k, s[k], v)
		}
	}
}

func TestFailureHints(t *testing.T) {
	fast(t)
	cases := []struct {
		body string
		code int
		want string
	}{
		{`{"status":"failed","error_code":429,"error":"quota"}`, 429, "G-Labs [429]: quota — вичерпано ліміт/кредити"},
		{`{"status":"failed","error_code":"403","error_detail":"session"}`, 403, "G-Labs [403]: session — проблема з сесією"},
		{`{"status":"failed","error_code":400,"error":"Request contains an invalid argument."}`, 400, "G-Labs відхилив запит: змінити промпт або референси · G-Labs [400]: Request contains"},
		{`{"status":"failed","error_code":500,"error":"INTERNAL"}`, 500, "G-Labs [500]: INTERNAL — тимчасовий збій"},
		{`{"status":"failed"}`, 0, "G-Labs: невідома помилка"},
	}
	for _, tc := range cases {
		m := newMock(t, tc.body)
		_, err := testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
		var ge *Error
		if !errors.As(err, &ge) || ge.Code != tc.code || !strings.Contains(ge.Msg, tc.want) {
			t.Errorf("%s → %v (code %v), want %q", tc.body, err, ge, tc.want)
		}
	}
}

func TestLostTaskIsFatal(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"running"}`)
	m.codes = []int{404}
	_, err := testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "задачу втрачено") {
		t.Fatalf("err = %v", err)
	}
	if h := m.hits.Load(); h != 1 {
		t.Errorf("status polled %d times after 404", h)
	}
}

func TestBlipsTolerated(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"completed","results":["/f/a.png"]}`)
	m.files["/f/a.png"] = string(pngBytes())
	m.codes = []int{500, 502, 503, 500, 500}
	if _, _, err := testClient(m.srv.URL, "k1").Image(context.Background(), gen.ImageRequest{Prompt: "p"}); err != nil {
		t.Fatalf("5 blips should be tolerated: %v", err)
	}

	m2 := newMock(t, `{"status":"running"}`)
	m2.codes = []int{500, 500, 500, 500, 500, 500, 500}
	_, _, err := testClient(m2.srv.URL, "k1").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "відповів 500") {
		t.Fatalf("err = %v", err)
	}
	if h := m2.hits.Load(); h != int32(maxBlips+1) {
		t.Errorf("hits = %d, want %d", h, maxBlips+1)
	}
}

func TestSubmitErrors(t *testing.T) {
	fast(t)
	m := newMock(t, `{}`)
	_, _, err := testClient(m.srv.URL, "wrong").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	var ge *Error
	if !errors.As(err, &ge) || ge.Code != 401 || !strings.Contains(ge.Msg, "невірний API-ключ") {
		t.Errorf("401 → %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "image") {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("boom"))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`)) // no task_id
	}))
	defer srv.Close()
	c := testClient(srv.URL, "k1")
	if _, _, err := c.Image(context.Background(), gen.ImageRequest{}); err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("500 → %v", err)
	}
	if _, err := c.Video(context.Background(), gen.VideoRequest{}, nil); err == nil || !strings.Contains(err.Error(), "task_id") {
		t.Errorf("no task_id → %v", err)
	}
}

func TestCompletedWithoutFiles(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"completed","results":["",null]}`)
	_, _, err := testClient(m.srv.URL, "k1").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "без файлів") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadFailure(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"completed","results":["/missing.mp4"]}`)
	_, err := testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "не вдалося завантажити") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeout(t *testing.T) {
	fast(t)
	pollTimeout = 40 * time.Millisecond
	m := newMock(t, `{"status":"running"}`)
	_, err := testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "не завершилась") {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelWhilePolling(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"running"}`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := testClient(m.srv.URL, "k1").Video(ctx, gen.VideoRequest{Prompt: "p"}, nil)
		done <- err
	}()
	for m.hits.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Video did not stop after cancel")
	}
}

func TestUnreachableAndMissingSettings(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	_, _, err := testClient(base, "k1").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "G-Labs Studio не відповідає на "+base) || !strings.Contains(err.Error(), "Start Server") {
		t.Errorf("unreachable → %v", err)
	}
	if _, _, err := testClient("", "k1").Image(context.Background(), gen.ImageRequest{}); err == nil || !strings.Contains(err.Error(), "не задано адресу") {
		t.Errorf("no base → %v", err)
	}
	if _, err := testClient("http://127.0.0.1:1", " ").Video(context.Background(), gen.VideoRequest{}, nil); err == nil || !strings.Contains(err.Error(), "не задано API-ключ") {
		t.Errorf("no key → %v", err)
	}
}

func TestHealth(t *testing.T) {
	var healthKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			healthKey = r.Header.Get("X-API-Key")
			_, _ = w.Write([]byte(`{"status":"ok","uptime":125.4}`))
		case "/api/tasks":
			if r.Header.Get("X-API-Key") != "k1" {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srv.Close()

	ok, msg := testClient(srv.URL, "k1").Health(context.Background())
	if !ok || msg != "G-Labs онлайн · працює 2 хв" {
		t.Errorf("health = %v %q", ok, msg)
	}
	if healthKey != "" {
		t.Errorf("/api/health must not get the key")
	}
	if ok, msg := testClient(srv.URL, "bad").Health(context.Background()); ok || msg != "Сервер працює, але API-ключ невірний" {
		t.Errorf("bad key = %v %q", ok, msg)
	}
	if ok, msg := testClient(srv.URL, "").Health(context.Background()); ok || !strings.Contains(msg, "не задано API-ключ") {
		t.Errorf("no key = %v %q", ok, msg)
	}
	base := srv.URL
	srv.Close()
	if ok, msg := testClient(base, "k1").Health(context.Background()); ok || !strings.Contains(msg, "не відповідає") {
		t.Errorf("down = %v %q", ok, msg)
	}
}

func TestImageModelAndMIME(t *testing.T) {
	for in, want := range map[string]string{"GEM_PIX": "nano_banana_2_lite", "GEM_PIX_2": "nano_banana_pro", "NARWHAL": "nano_banana_2", "": "nano_banana_2"} {
		if got := ImageModel(in); got != want {
			t.Errorf("ImageModel(%q) = %q", in, got)
		}
	}
	cases := []struct{ ctype, url, data, want string }{
		{"image/webp; charset=binary", "/a.png", "x", "image/webp"},
		{"application/octet-stream", "/a.png", string(pngBytes()), "image/png"},
		{"", "/a.JPEG?x=1", "x", "image/jpeg"},
		{"", "/a.webp", "x", "image/webp"},
		{"", "/a", "x", "image/png"},
	}
	for _, tc := range cases {
		if got := imageMIME(tc.ctype, tc.url, []byte(tc.data)); got != tc.want {
			t.Errorf("imageMIME(%q,%q) = %q, want %q", tc.ctype, tc.url, got, tc.want)
		}
	}
}

// The failures seen on a real G-Labs (Omni Flash components) are classified:
// content rejections are final, server trouble and quotas are transient.
func TestFailureClassification(t *testing.T) {
	cases := []struct {
		body   string
		reject string
		retry  bool
		msg    string
	}{
		{`{"status":"failed","error_code":400,"error":"INVALID_ARGUMENT (flag=[3])","error_detail":"400: INVALID_ARGUMENT (flag=[3])"}`,
			RejectInvalid, false, RejectedPrefix + " · G-Labs [400]: INVALID_ARGUMENT (flag=[3])"},
		{`{"status":"failed","error_code":400,"error":"400: INVALID_ARGUMENT (flag=[3])"}`, RejectInvalid, false, ""}, // older builds
		{`{"status":"failed","error_code":0,"error":"REPUTATIONAL","error_detail":"REPUTATIONAL"}`,
			RejectSafety, false, RejectedPrefix + " — фільтр безпеки Google (REPUTATIONAL)"},
		{`{"status":"failed","error_code":0,"error":"PROMINENT_PERSON"}`, RejectSafety, false, ""},
		{`{"status":"failed","error_code":0,"error":"DANGEROUS"}`, RejectSafety, false, ""},
		{`{"status":"failed","error_code":400,"error":"CONTENT_POLICY — blocked"}`, RejectSafety, false, ""},
		{`{"status":"failed","error_code":0,"error":"Upload failed for image 1: UPLOAD_ERROR|500: INTERNAL (flag=[13])"}`, "", true, ""},
		{`{"status":"failed","error_code":500,"error":"INTERNAL"}`, "", true, ""},
		{`{"status":"failed","error_code":429,"error":"Daily video quota exhausted"}`, "", true, ""},
		{`{"status":"failed","error_code":0,"error":"Timeout: no result generated"}`, "", true, ""},
		{`{"status":"failed","error_code":0,"error":"Task timed out (no completion signal received)"}`, "", true, ""},
		{`{"status":"failed","error_code":403,"error":"PERMISSION_DENIED"}`, "", false, ""},
		{`{"status":"failed","error_code":0,"error":"No active accounts available"}`, "", false, ""},
		{`{"status":"failed","error_code":499,"error":"STOPPED_BY_CLIENT"}`, "", false, ""},
	}
	for _, tc := range cases {
		var ts taskState
		if err := json.Unmarshal([]byte(tc.body), &ts); err != nil {
			t.Fatal(err)
		}
		e := describeFailure(ts)
		if e.Reject != tc.reject || e.Retry != tc.retry || Rejected(e.Msg) != (tc.reject != "") || Rejection(e) != tc.reject {
			t.Errorf("%s → reject %q retry %v msg %q", tc.body, e.Reject, e.Retry, e.Msg)
		}
		if tc.msg != "" && e.Msg != tc.msg {
			t.Errorf("%s → %q, want %q", tc.body, e.Msg, tc.msg)
		}
	}
	if Rejected("G-Labs [429]: ліміт") || Rejection(errors.New("x")) != "" {
		t.Fatal("not a rejection")
	}
}

// A task the client gives up on (cancel, timeout) is stopped by its id; a
// task G-Labs finished (or lost) is not.
func TestStopAbandonedTask(t *testing.T) {
	fast(t)
	wantStop := []string{`/api/stop/task%2F1 {"task_id":"task/1"}`}

	// cancel while polling
	m := newMock(t, `{"status":"running"}`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := testClient(m.srv.URL, "k1").Video(ctx, gen.VideoRequest{Prompt: "p"}, nil)
		done <- err
	}()
	for m.hits.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	m.mu.Lock()
	got := append([]string(nil), m.stops...)
	m.mu.Unlock()
	if strings.Join(got, "|") != wantStop[0] {
		t.Fatalf("stops after cancel = %q, want %q", got, wantStop)
	}

	// our own poll timeout
	pollTimeout = 30 * time.Millisecond
	m = newMock(t, `{"status":"running"}`)
	_, err := testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	var ge *Error
	if !errors.As(err, &ge) || !ge.Retry || len(m.stops) != 1 || m.stops[0] != wantStop[0] {
		t.Fatalf("timeout: err %v, stops %q", err, m.stops)
	}
	pollTimeout = 5 * time.Second

	// finished tasks are never stopped
	for _, st := range []string{`{"status":"failed","error_code":400,"error":"INVALID_ARGUMENT"}`, `{"status":"completed","results":["/f/x.mp4"]}`} {
		m = newMock(t, st)
		m.files["/f/x.mp4"] = "v"
		_, _ = testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
		if len(m.stops) != 0 {
			t.Fatalf("%s: stopped %q", st, m.stops)
		}
	}
	m = newMock(t, `{"status":"running"}`)
	m.codes = []int{404}
	_, _ = testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if len(m.stops) != 0 {
		t.Fatalf("lost task stopped: %q", m.stops)
	}
	// never a stop without an id (that would stop every task)
	c := testClient(m.srv.URL, "k1")
	c.stop(conn{base: m.srv.URL, key: "k1"}, " ")
	if len(m.stops) != 0 {
		t.Fatalf("stop without id sent: %q", m.stops)
	}
}

// Result URLs carry G-Labs' bind address; /api/files/ ones are fetched from
// the configured address instead (tunnels, LAN, 0.0.0.0).
func TestResultURLUsesConfiguredBase(t *testing.T) {
	fast(t)
	m := newMock(t, `{"status":"completed","results":["http://0.0.0.0:8765/api/files/clip%201.mp4?v=2"]}`)
	m.files["/api/files/clip 1.mp4"] = "mp4"
	out, err := testClient(m.srv.URL, "k1").Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil)
	if err != nil || string(out) != "mp4" {
		t.Fatalf("download = %q, %v", out, err)
	}
	for base, cases := range map[string][][2]string{
		"http://h:1/pre": {
			{"http://192.168.1.5:8765/api/files/a.png", "http://h:1/pre/api/files/a.png"},
			{"api/files/a.png", "http://h:1/pre/api/files/a.png"},
			{"/api/files/a.png", "http://h:1/pre/api/files/a.png"},
			{"https://cdn.example.com/out/a.png", "https://cdn.example.com/out/a.png"},
		},
	} {
		for _, c := range cases {
			if got := fileURL(base, c[0]); got != c[1] {
				t.Errorf("fileURL(%q) = %q, want %q", c[0], got, c[1])
			}
		}
	}
}

// 403 = the licence is not MAX, 413 = the body is over 50 MB.
func TestMaxPlanAndTooLarge(t *testing.T) {
	code := 403
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_, _ = w.Write([]byte(`{"status":"ok","uptime":3}`))
		default:
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"Webhook requires MAX plan"}`))
		}
	}))
	defer srv.Close()
	c := testClient(srv.URL, "k1")
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p"}, nil); err == nil || err.Error() != "Webhook потребує тарифу MAX у G-Labs" {
		t.Errorf("403 submit → %v", err)
	}
	if ok, msg := c.Health(context.Background()); ok || msg != "Webhook потребує тарифу MAX у G-Labs" {
		t.Errorf("403 health → %v %q", ok, msg)
	}
	code = 413
	_, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "50 МБ") || !strings.Contains(err.Error(), "референсів") {
		t.Errorf("413 → %v", err)
	}
}
