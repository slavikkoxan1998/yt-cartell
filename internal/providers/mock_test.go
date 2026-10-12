package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

// Small fixtures: a PNG/JPEG header is enough for MIME sniffing.
var (
	pngBytes  = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 64)...)
	mp4Bytes  = append([]byte("\x00\x00\x00\x18ftypmp42"), make([]byte, 64)...)
)

// seen is one request the mock received.
type seen struct {
	method, path, query string
	header              http.Header
	body                []byte
}

func (s seen) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(s.body, &m); err != nil {
		t.Fatalf("%s %s: body is not JSON: %v (%q)", s.method, s.path, err, clip(string(s.body), 200))
	}
	return m
}

// mockAPI is a scripted provider server: route "METHOD /path" (or a prefix
// ending in "*") → handler.
type mockAPI struct {
	t      *testing.T
	mu     sync.Mutex
	reqs   []seen
	routes map[string]http.HandlerFunc
	srv    *httptest.Server
}

func newMockAPI(t *testing.T) *mockAPI {
	m := &mockAPI{t: t, routes: map[string]http.HandlerFunc{}}
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAPI) on(route string, h http.HandlerFunc) { m.routes[route] = h }

func (m *mockAPI) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.reqs = append(m.reqs, seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, header: r.Header.Clone(), body: b})
	m.mu.Unlock()
	r.Body = io.NopCloser(strings.NewReader(string(b)))
	key := r.Method + " " + r.URL.Path
	if h, ok := m.routes[key]; ok {
		h(w, r)
		return
	}
	best := ""
	for route := range m.routes {
		if p, ok := strings.CutSuffix(route, "*"); ok && strings.HasPrefix(key, p) && len(p) > len(best) {
			best = route
		}
	}
	if best != "" {
		m.routes[best](w, r)
		return
	}
	m.t.Logf("mock: unrouted %s", key)
	http.NotFound(w, r)
}

// requests returns the received requests matching "METHOD /path" prefix.
func (m *mockAPI) requests(prefix string) []seen {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []seen
	for _, s := range m.reqs {
		if strings.HasPrefix(s.method+" "+s.path, prefix) {
			out = append(out, s)
		}
	}
	return out
}

func (m *mockAPI) all() []seen {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]seen(nil), m.reqs...)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if s, ok := v.(string); ok {
		io.WriteString(w, s)
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func serveBytes(ctype string, data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ctype)
		w.Write(data)
	}
}

// customClient builds a Client over an in-memory config.
func customClient(mut func(c *config.Config)) *Client {
	c := config.Default()
	c.Image.Provider, c.Video.Provider = "custom", "custom"
	mut(&c)
	c.Normalize()
	return New(func() config.Config { return c })
}

// noKeyLeak fails when any received header/body/URL or message contains key
// outside the expected auth header.
func noKeyLeak(t *testing.T, m *mockAPI, key, authHeader string, msgs ...string) {
	t.Helper()
	for _, s := range m.all() {
		if strings.Contains(s.path, key) || strings.Contains(s.query, key) || strings.Contains(string(s.body), key) {
			t.Errorf("%s %s: key in URL/body", s.method, s.path)
		}
		for h, vals := range s.header {
			if strings.EqualFold(h, authHeader) {
				continue
			}
			for _, v := range vals {
				if strings.Contains(v, key) {
					t.Errorf("%s %s: key in header %s", s.method, s.path, h)
				}
			}
		}
	}
	for _, msg := range msgs {
		if strings.Contains(msg, key) {
			t.Errorf("key in message %q", msg)
		}
	}
}

func startFrame() *gen.RefImage { return &gen.RefImage{Data: jpegBytes, MIME: "image/jpeg"} }
