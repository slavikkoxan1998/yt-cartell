package gemini

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// CheckKeys calls models.list once per key (the key in the header, never the
// URL) and records each answer: working, resting after a quota hit (and until
// when), rejected; the rotation then skips the rejected key while others work.
func TestCheckKeysAndStatus(t *testing.T) {
	c, m, _ := setup(t, func(_ *mock, l reqLog, w http.ResponseWriter, _ *http.Request) {
		if l.Path != "/v1beta/models" || l.Query != "pageSize=1" && !strings.Contains(l.Path, "generateContent") {
			t.Errorf("unexpected %s %s?%s", l.Method, l.Path, l.Query)
		}
		switch l.Key {
		case "k-quota":
			writeJSON(w, 429, map[string]any{"error": map[string]any{"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "Quota exceeded",
				"details": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "40s"}}}})
		case "k-bad":
			writeJSON(w, 400, map[string]any{"error": map[string]any{"code": 400, "status": "INVALID_ARGUMENT",
				"message": "API key not valid. Please pass a valid API key.", "details": []any{map[string]any{"reason": "API_KEY_INVALID"}}}})
		default:
			writeJSON(w, 200, map[string]any{"models": []any{map[string]any{"name": "models/gemini-3.8-flash"}}})
		}
	}, "k-ok-1", "k-quota", "k-bad", "k-ok-2")
	if st := c.KeyStatus(); len(st) != 4 || st[0].State != KeyUnknown || st[0].Mask == "" {
		t.Fatalf("before: %+v", st)
	}
	ok, msg := c.CheckKeys(context.Background())
	if !ok || !strings.Contains(msg, "працюють 2 з 4 ключів") || !strings.Contains(msg, "1 відпочиває до") || !strings.Contains(msg, "1 з помилкою: ключ недійсний") {
		t.Fatalf("summary %v %q", ok, msg)
	}
	if n := len(m.all()); n != 4 {
		t.Fatalf("%d requests for 4 keys", n)
	}
	st := c.KeyStatus()
	if st[0].State != KeyOK || st[0].Checked.IsZero() || st[3].State != KeyOK {
		t.Fatalf("ok keys: %+v", st)
	}
	if st[1].State != KeyResting || st[1].Until.Sub(time.Now()) < 30*time.Second || st[1].Until.Sub(time.Now()) > time.Minute {
		t.Fatalf("resting key: %+v", st[1])
	}
	if st[2].State != KeyError || !strings.Contains(st[2].LastError, "недійсний") {
		t.Fatalf("bad key: %+v", st[2])
	}
	// The rotation skips the resting and the rejected key.
	used := map[string]int{}
	for i := 0; i < 6; i++ {
		_ = c.pool.Do(context.Background(), func(key string) error { used[key]++; return nil })
	}
	if used["k-bad"] != 0 || used["k-quota"] != 0 || used["k-ok-1"] != 3 || used["k-ok-2"] != 3 {
		t.Fatalf("rotation %v", used)
	}
	// A key that works again is cleared by the next check.
	c.pool.report("k-bad", nil)
	if c.KeyStatus()[2].State != KeyOK {
		t.Fatal("a working key stays in error")
	}
}

// Every key rejected: the pool still tries them (nothing better to do).
func TestBadKeysAreLastResort(t *testing.T) {
	p, _ := newFakePool("a", "b")
	for _, k := range []string{"a", "b"} {
		p.report(k, &APIError{Status: 400, Body: `{"error":{"details":[{"reason":"API_KEY_INVALID"}]}}`})
	}
	calls := 0
	if err := p.Do(context.Background(), func(string) error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatalf("err %v, calls %d", err, calls)
	}
}
