package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time          { return f.t }
func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func newFakePool(keys ...string) (*Pool, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)}
	p := NewPool(keys)
	p.now = clk.now
	return p, clk
}

func quota429() error {
	return &APIError{Label: "t", Status: 429, Body: `{"error":{"status":"RESOURCE_EXHAUSTED"}}`}
}

func TestPoolDedupAndSize(t *testing.T) {
	p := NewPool([]string{" a ", "b", "", "a", "c"})
	if p.Size() != 3 || p.Healthy() != 3 {
		t.Fatalf("size=%d healthy=%d", p.Size(), p.Healthy())
	}
	p.SetKeys(nil)
	if p.Size() != 0 {
		t.Fatal("SetKeys(nil) must empty the pool")
	}
	if err := p.Do(context.Background(), func(string) error { return nil }); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("empty pool: %v", err)
	}
}

func TestPoolRoundRobin(t *testing.T) {
	p := NewPool([]string{"a", "b", "c"})
	var got []string
	for i := 0; i < 6; i++ {
		_ = p.Do(context.Background(), func(k string) error { got = append(got, k); return nil })
	}
	if strings.Join(got, "") != "abcabc" {
		t.Fatalf("round robin = %v", got)
	}
}

func TestPoolFailoverAndCooldown(t *testing.T) {
	p, clk := newFakePool("a", "b")
	var calls []string
	fn := func(k string) error {
		calls = append(calls, k)
		if k == "a" {
			return quota429()
		}
		return nil
	}
	if err := p.Do(context.Background(), fn); err != nil {
		t.Fatalf("failover should succeed: %v", err)
	}
	if strings.Join(calls, ",") != "a,b" {
		t.Fatalf("calls = %v", calls)
	}
	if p.Healthy() != 1 {
		t.Fatalf("healthy = %d, want 1", p.Healthy())
	}
	// While a cools down every call goes to b.
	calls = nil
	for i := 0; i < 3; i++ {
		_ = p.Do(context.Background(), fn)
	}
	if strings.Join(calls, ",") != "b,b,b" {
		t.Fatalf("during cooldown calls = %v", calls)
	}
	// A 429 without details: parked for Cooldown, back afterwards.
	clk.advance(Cooldown - time.Second)
	if p.Healthy() != 1 {
		t.Fatal("key reactivated too early")
	}
	clk.advance(time.Second)
	if p.Healthy() != 2 {
		t.Fatalf("after cooldown healthy = %d", p.Healthy())
	}
	calls = nil
	_ = p.Do(context.Background(), func(k string) error { calls = append(calls, k); return nil })
	_ = p.Do(context.Background(), func(k string) error { calls = append(calls, k); return nil })
	if !strings.Contains(strings.Join(calls, ","), "a") {
		t.Fatalf("a never used again after cooldown: %v", calls)
	}
}

func TestPoolNonQuotaErrorPropagates(t *testing.T) {
	p := NewPool([]string{"a", "b"})
	boom := errors.New("400 bad request")
	var calls int
	err := p.Do(context.Background(), func(string) error { calls++; return boom })
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if p.Healthy() != 2 {
		t.Fatal("non-quota error must not park the key")
	}
}

func TestPoolAllExhausted(t *testing.T) {
	p, _ := newFakePool("a", "b", "c")
	var calls int
	err := p.Do(context.Background(), func(string) error { calls++; return quota429() })
	if !errors.Is(err, ErrExhausted) || calls != 3 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if !strings.Contains(err.Error(), "квоту") {
		t.Fatalf("error should carry the last failure: %v", err)
	}
	calls = 0
	err = p.Do(context.Background(), func(string) error { calls++; return nil })
	if !errors.Is(err, ErrExhausted) || calls != 0 {
		t.Fatalf("all cooling: err=%v calls=%d", err, calls)
	}
	p.SetKeys([]string{"a", "b", "c"})
	if p.Healthy() != 3 {
		t.Fatal("SetKeys must reset cooldowns")
	}
}

func TestPoolContext(t *testing.T) {
	p := NewPool([]string{"a", "b"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := p.Do(ctx, func(string) error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled ctx: err=%v called=%v", err, called)
	}
	// A quota-looking error caused by cancellation must not park the key.
	ctx2, cancel2 := context.WithCancel(context.Background())
	err := p.Do(ctx2, func(string) error { cancel2(); return quota429() })
	if err == nil || p.Healthy() != 2 {
		t.Fatalf("err=%v healthy=%d", err, p.Healthy())
	}
}

func TestPoolConcurrent(t *testing.T) {
	p := NewPool([]string{"a", "b", "c", "d"})
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				_ = p.Do(context.Background(), func(k string) error {
					if k == "d" {
						return quota429()
					}
					return nil
				})
				_ = p.Healthy()
			}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	if p.Healthy() != 3 {
		t.Fatalf("healthy = %d, want 3", p.Healthy())
	}
}

func TestIsQuotaError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{quota429(), true},
		{&APIError{Status: 400, Body: "Quota exceeded for metric"}, true},
		{&APIError{Status: 400, Body: `{"error":{"message":"API key not valid"}}`}, false},
		{&APIError{Status: 503, Body: "unavailable"}, false},
		{&opError{st: opStatus{Code: 8}}, true}, // google.rpc.Code RESOURCE_EXHAUSTED
		{&opError{st: opStatus{Code: 13, Message: "Quota exceeded for aiplatform"}}, true},
		{&opError{st: opStatus{Code: 429}}, false}, // not a google.rpc code
		{&opError{st: opStatus{Code: 3, Message: "bad prompt"}}, false},
		{&omniError{status: "failed", details: []string{"RESOURCE_EXHAUSTED: rate limit"}}, true},
		{&omniError{status: "failed", details: []string{"prompt blocked"}}, false},
		{&APIError{Status: 402, Body: "credits"}, true},
		{errors.New("veo op error: RESOURCE_EXHAUSTED"), true},
		{fmt.Errorf("wrapped: %w", quota429()), true},
		{errors.New("boom"), false},
		{context.Canceled, false},
		{fmt.Errorf("x: %w", context.DeadlineExceeded), false},
		{nil, false},
	}
	for i, c := range cases {
		if got := isQuotaError(c.err); got != c.want {
			t.Errorf("#%d isQuotaError(%v) = %v, want %v", i, c.err, got, c.want)
		}
	}
}

func quotaBody(details ...string) error {
	return &APIError{Label: "t", Status: 429, Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota","details":[` +
		strings.Join(details, ",") + `]}}`}
}

const (
	retry43   = `{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"43s"}`
	perDay    = `{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaMetric":"generativelanguage.googleapis.com/generate_content_free_tier_requests","quotaId":"GenerateRequestsPerDayPerProjectPerModel-FreeTier","quotaValue":"50"}]}`
	perMinute = `{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"GenerateRequestsPerMinutePerProjectPerModel-FreeTier"}]}`
)

// A key is parked only as long as its quota window lasts: RetryInfo, a
// per-minute quota for a minute, a per-day quota until midnight Pacific (even
// when the error also carries a short RetryInfo).
func TestPoolQuotaWindows(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"retry info", quotaBody(retry43), 44 * time.Second},
		{"per minute", quotaBody(perMinute), time.Minute},
		{"per minute + retry", quotaBody(perMinute, retry43), 44 * time.Second},
		// 2026-09-30 12:00 UTC = 05:00 PDT → midnight PDT is in 19 h (+30 s margin).
		{"per day", quotaBody(perDay, retry43), 19*time.Hour + 30*time.Second},
		{"billing 402", &APIError{Status: 402, Body: "{}"}, billingCooldown},
		{"op RESOURCE_EXHAUSTED + details", &opError{st: opStatus{Code: 8, Details: []json.RawMessage{json.RawMessage(retry43)}}}, 44 * time.Second},
		{"no details", quota429(), Cooldown},
	}
	for _, c := range cases {
		p, clk := newFakePool("a", "b")
		_ = p.Do(context.Background(), func(k string) error {
			if k == "a" {
				return c.err
			}
			return nil
		})
		got := p.keys[0].cooldownUntil.Sub(clk.t)
		if got != c.want {
			t.Errorf("%s: parked for %v, want %v", c.name, got, c.want)
		}
	}
}

// Without a hint, repeated hits double the pause (never past the daily
// reset); a success resets it.
func TestPoolEscalatesWithoutHints(t *testing.T) {
	p, clk := newFakePool("a")
	fail := func(string) error { return quota429() }
	var got []time.Duration
	for i := 0; i < 4; i++ {
		_ = p.Do(context.Background(), fail)
		got = append(got, p.keys[0].cooldownUntil.Sub(clk.t))
		clk.advance(got[i])
	}
	want := []time.Duration{Cooldown, 2 * Cooldown, 4 * Cooldown, 8 * Cooldown}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pauses = %v, want %v", got, want)
		}
	}
	err := p.Do(context.Background(), fail) // parked again; all keys cooling
	if !errors.Is(err, ErrExhausted) || !strings.Contains(err.Error(), "звільниться через") {
		t.Fatalf("exhausted: %v", err)
	}
	clk.advance(time.Hour)
	if err := p.Do(context.Background(), func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = p.Do(context.Background(), fail)
	if d := p.keys[0].cooldownUntil.Sub(clk.t); d != Cooldown {
		t.Fatalf("after a success the pause starts over: %v", d)
	}
	// Capped at the daily reset.
	p.keys[0].strikes = 40
	p.keys[0].cooldownUntil = time.Time{}
	_ = p.Do(context.Background(), fail)
	if d, limit := p.keys[0].cooldownUntil.Sub(clk.t), nextPacificMidnight(clk.t).Sub(clk.t)+30*time.Second; d != limit {
		t.Fatalf("cap: %v, want %v", d, limit)
	}
}

func TestNextPacificMidnight(t *testing.T) {
	cases := map[time.Time]time.Time{
		// PDT (UTC−7): 2026-10-02 12:00 UTC = 05:00 PDT → 2026-10-03 07:00 UTC.
		time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC): time.Date(2026, 10, 3, 7, 0, 0, 0, time.UTC),
		// PST (UTC−8): 2026-12-15 07:59 UTC = 23:59 PST (14th) → 08:00 UTC on the 15th.
		time.Date(2026, 12, 15, 7, 59, 0, 0, time.UTC): time.Date(2026, 12, 15, 8, 0, 0, 0, time.UTC),
		// DST ends 2026-11-01 02:00 PDT: midnight of Nov 2 is PST.
		time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC): time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC),
		// DST starts 2027-03-14 02:00 PST: from the evening before, midnight is still PST.
		time.Date(2027, 3, 14, 6, 0, 0, 0, time.UTC): time.Date(2027, 3, 14, 8, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		if got := nextPacificMidnight(in); !got.Equal(want) {
			t.Errorf("nextPacificMidnight(%v) = %v, want %v", in, got, want)
		}
	}
	if humanWait(40*time.Second) != "40 с" || humanWait(12*time.Minute) != "12 хв" || humanWait(3*time.Hour+20*time.Minute) != "3 год 20 хв" {
		t.Fatal("humanWait")
	}
}

func TestClip(t *testing.T) {
	if got := clip("привіт світ", 5); got != "пр…" {
		t.Fatalf("clip = %q", got)
	}
	if clip(" ok ", 10) != "ok" {
		t.Fatal("clip trims")
	}
}
