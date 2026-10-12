// Package gemini is the Google AI Studio client: a round-robin key pool with
// quota failover (port of services/api_balancer.ts), generateContent for text
// and images (gemini.ts, imagegen.ts viaGoogle), Gemini Omni Flash video
// through the Interactions API (omni.go) and Veo 3.1 image-to-video through
// predictLongRunning (veo.go, until the Veo previews shut down).
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// Cooldown is the first pause of a key whose quota error does not say which
// window it hit. Gemini API limits are per minute or per day and apply per
// PROJECT (https://ai.google.dev/gemini-api/docs/rate-limits), so a key is
// parked only as long as its error says: RetryInfo.retryDelay, a per-minute
// quota for a minute, a per-day quota until the daily reset (midnight Pacific).
// Without a hint every further hit without a success in between doubles the
// pause, never past the next daily reset.
const Cooldown = time.Minute

// billingCooldown parks a key whose project ran out of prepaid credits (402).
const billingCooldown = time.Hour

var (
	// ErrNoKeys: the pool is empty.
	ErrNoKeys = errors.New("ключів Google не задано (Налаштування → Google AI Studio)")
	// ErrExhausted: every key is cooling down after a quota hit.
	ErrExhausted = errors.New("усі ключі Google вичерпали квоту й тимчасово відпочивають — додай ключ іншого проєкту Google Cloud або спробуй пізніше")
)

// keyState is one pooled key. A zero cooldownUntil means ACTIVE.
type keyState struct {
	id            int
	key           string
	cooldownUntil time.Time
	requests      int
	rateLimitHits int
	strikes       int // quota hits without a success in between
	lastError     string
	// badUntil: Google rejected the key itself (invalid, expired, no access)
	// — skipped while other keys work, until then (keystatus.go).
	badUntil time.Time
	checked  time.Time // a check or a call last found it working
}

// Pool hands out API keys round-robin. When a key hits its quota (HTTP 429, a
// RESOURCE_EXHAUSTED / "quota" error or an exhausted prepaid balance) it is
// parked for as long as the error's quota window lasts (see Cooldown) and the
// SAME call is retried on the next ACTIVE key. Only when every key is cooling
// down does the pool give up. Safe for concurrent use.
type Pool struct {
	mu       sync.Mutex
	keys     []*keyState
	cursor   int
	cooldown time.Duration // first pause without a hint (Cooldown)
	now      func() time.Time
}

// NewPool builds a pool from raw keys (trimmed, blanks and duplicates dropped).
func NewPool(keys []string) *Pool {
	p := &Pool{cooldown: Cooldown, now: time.Now}
	p.SetKeys(keys)
	return p
}

// SetKeys replaces the whole pool; every key starts ACTIVE again.
func (p *Pool) SetKeys(keys []string) {
	uniq := uniqueKeys(keys)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = make([]*keyState, 0, len(uniq))
	for i, k := range uniq {
		p.keys = append(p.keys, &keyState{id: i, key: k})
	}
	p.cursor = 0
}

// Size is the number of keys in the pool.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

// Healthy is the number of ACTIVE (not cooling down) keys.
func (p *Pool) Healthy() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reactivateElapsed()
	n := 0
	for _, s := range p.keys {
		if s.cooldownUntil.IsZero() {
			n++
		}
	}
	return n
}

// Do runs fn with an ACTIVE key. On a quota error the key is parked and fn is
// retried on the next ACTIVE key; any other error is returned immediately (it
// is not the key's fault). At most one attempt per key.
func (p *Pool) Do(ctx context.Context, fn func(key string) error) error {
	n := p.Size()
	if n == 0 {
		return ErrNoKeys
	}
	var last error
	for attempt := 0; attempt < n; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		st := p.nextActive()
		if st == nil {
			break // everything is cooling down
		}
		err := fn(st.key)
		if err == nil {
			p.succeeded(st)
			return nil
		}
		if ctx.Err() != nil || !isQuotaError(err) {
			if isKeyError(err) {
				p.markBad(st, err) // the next calls skip it (keystatus.go)
			}
			return err
		}
		p.park(st, err)
		last = err
	}
	wait := ""
	if d := p.soonest(); d > 0 {
		wait = "; найближчий звільниться через " + humanWait(d)
	}
	if last != nil {
		return fmt.Errorf("%w (остання помилка: %s%s)", ErrExhausted, clip(last.Error(), 200), wait)
	}
	if wait != "" {
		return fmt.Errorf("%w (%s)", ErrExhausted, strings.TrimPrefix(wait, "; "))
	}
	return ErrExhausted
}

// reactivateElapsed promotes keys whose cooldown has elapsed. Caller holds mu.
func (p *Pool) reactivateElapsed() {
	now := p.now()
	for _, s := range p.keys {
		if !s.cooldownUntil.IsZero() && !now.Before(s.cooldownUntil) {
			s.cooldownUntil = time.Time{}
		}
	}
}

// nextActive is the round-robin pick of the next ACTIVE key.
func (p *Pool) nextActive() *keyState {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reactivateElapsed()
	n := len(p.keys)
	now := p.now()
	// Keys Google rejected are tried only when no other key is free.
	for _, skipBad := range []bool{true, false} {
		for i := 0; i < n; i++ {
			s := p.keys[p.cursor%n]
			if s.cooldownUntil.IsZero() && !(skipBad && now.Before(s.badUntil)) {
				p.cursor = (p.cursor + 1) % n
				s.requests++
				return s
			}
			p.cursor = (p.cursor + 1) % n
		}
	}
	return nil
}

// succeeded clears the strikes of a key that worked.
func (p *Pool) succeeded(s *keyState) {
	p.mu.Lock()
	s.strikes = 0
	s.badUntil, s.checked = time.Time{}, p.now()
	p.mu.Unlock()
}

// park puts a key on cooldown after a confirmed quota hit, for as long as the
// error's quota window lasts.
func (p *Pool) park(s *keyState, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s.strikes++
	s.rateLimitHits++
	now := p.now()
	s.cooldownUntil = now.Add(p.cooldownFor(s, err, now))
	s.lastError = clip(err.Error(), 160)
}

// cooldownFor is how long a key stays parked after err. Caller holds mu.
func (p *Pool) cooldownFor(s *keyState, err error, now time.Time) time.Duration {
	daily := nextPacificMidnight(now).Sub(now) + 30*time.Second
	h := hintOf(err)
	switch {
	case h.billing:
		return billingCooldown
	case h.daily:
		return daily
	case h.retry > 0:
		return min(h.retry+time.Second, daily)
	case h.minute:
		return time.Minute
	}
	base := p.cooldown
	if base <= 0 {
		base = Cooldown
	}
	d := base << min(max(s.strikes-1, 0), 16)
	if d <= 0 || d > daily {
		d = daily
	}
	return d
}

// soonest is the time until the first parked key is back (0 = none parked).
func (p *Pool) soonest() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	var best time.Duration
	for _, s := range p.keys {
		if s.cooldownUntil.IsZero() {
			continue
		}
		if d := s.cooldownUntil.Sub(now); d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	return best
}

func uniqueKeys(keys []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// ---- quota classification ----------------------------------------------------------

// isRateLimit: an HTTP status + body that means quota / rate limit.
func isRateLimit(status int, body string) bool {
	if status == 429 {
		return true
	}
	b := strings.ToUpper(body)
	return strings.Contains(b, "RESOURCE_EXHAUSTED") || strings.Contains(b, "QUOTA") ||
		strings.Contains(b, "RATE LIMIT") || strings.Contains(b, "RATELIMIT")
}

// quotaError is implemented by errors that know whether they are a quota hit.
type quotaError interface{ quota() bool }

// isQuotaError: does a failed call look like a quota hit (→ park the key)?
func isQuotaError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var q quotaError
	if errors.As(err, &q) {
		return q.quota()
	}
	t := strings.ToUpper(err.Error())
	return strings.Contains(t, "429") || strings.Contains(t, "RESOURCE_EXHAUSTED") ||
		strings.Contains(t, "QUOTA") || strings.Contains(t, "RATE LIMIT")
}

// ---- quota windows -------------------------------------------------------------------

// quotaHint is what a quota error says about its window (google.rpc error
// details, https://github.com/googleapis/googleapis/blob/master/google/rpc/error_details.proto).
type quotaHint struct {
	retry   time.Duration // RetryInfo.retryDelay (0 = none)
	daily   bool          // a QuotaFailure violation of a per-day quota (resets at midnight Pacific)
	minute  bool          // … of a per-minute quota
	billing bool          // prepaid credits used up (HTTP 402)
}

// quotaHinter is implemented by errors that carry google.rpc details.
type quotaHinter interface{ quotaHint() quotaHint }

func hintOf(err error) quotaHint {
	var h quotaHinter
	if errors.As(err, &h) {
		return h.quotaHint()
	}
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "perday") {
		return quotaHint{daily: true}
	}
	return quotaHint{}
}

// detailsHint reads RetryInfo and QuotaFailure out of google.rpc.Status.details.
func detailsHint(details []json.RawMessage) quotaHint {
	var h quotaHint
	for _, raw := range details {
		var d struct {
			Type       string `json:"@type"`
			RetryDelay string `json:"retryDelay"`
			Violations []struct {
				QuotaID     string `json:"quotaId"`
				QuotaMetric string `json:"quotaMetric"`
				Subject     string `json:"subject"`
				Description string `json:"description"`
			} `json:"violations"`
		}
		if json.Unmarshal(raw, &d) != nil {
			continue
		}
		switch {
		case strings.HasSuffix(d.Type, "google.rpc.RetryInfo"):
			if r, err := time.ParseDuration(strings.TrimSpace(d.RetryDelay)); err == nil && r > 0 {
				h.retry = r
			}
		case strings.HasSuffix(d.Type, "google.rpc.QuotaFailure"):
			for _, v := range d.Violations {
				id := strings.ToLower(v.QuotaID + " " + v.QuotaMetric + " " + v.Description)
				switch {
				case strings.Contains(id, "perday") || strings.Contains(id, "per_day") || strings.Contains(id, "per day"):
					h.daily = true
				case strings.Contains(id, "perminute") || strings.Contains(id, "per_minute") || strings.Contains(id, "per minute"):
					h.minute = true
				}
			}
		}
	}
	return h
}

// bodyHint reads the details of a Google error body ({"error":{…}} or
// [{"error":{…}}]).
func bodyHint(body string) quotaHint {
	type gErr struct {
		Error struct {
			Details []json.RawMessage `json:"details"`
		} `json:"error"`
	}
	var one gErr
	if json.Unmarshal([]byte(body), &one) == nil && len(one.Error.Details) > 0 {
		return detailsHint(one.Error.Details)
	}
	var many []gErr
	if json.Unmarshal([]byte(body), &many) == nil && len(many) > 0 {
		return detailsHint(many[0].Error.Details)
	}
	return quotaHint{}
}

// nextPacificMidnight is the next 00:00 US Pacific time after t — when the
// Gemini API daily quotas reset.
func nextPacificMidnight(t time.Time) time.Time {
	off := pacificOffset(t)
	l := t.UTC().Add(off)
	day := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, time.UTC)
	mid := day.Add(-off)
	if o := pacificOffset(mid); o != off { // DST changed in between
		mid = day.Add(-o)
	}
	return mid
}

// pacificOffset is the UTC offset of US Pacific time at t: PDT (−7 h) from
// the second Sunday of March 02:00 PST to the first Sunday of November 02:00
// PDT, else PST (−8 h). Computed here: the Windows build has no tz database.
func pacificOffset(t time.Time) time.Duration {
	u := t.UTC()
	start := nthSunday(u.Year(), time.March, 2).Add(10 * time.Hour) // 02:00 PST
	end := nthSunday(u.Year(), time.November, 1).Add(9 * time.Hour) // 02:00 PDT
	if !u.Before(start) && u.Before(end) {
		return -7 * time.Hour
	}
	return -8 * time.Hour
}

func nthSunday(year int, m time.Month, n int) time.Time {
	first := time.Date(year, m, 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 0, (7-int(first.Weekday()))%7+7*(n-1))
}

// humanWait: "40 с", "12 хв", "3 год 20 хв".
func humanWait(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d с", max(1, int(math.Ceil(d.Seconds()))))
	case d < time.Hour:
		return fmt.Sprintf("%d хв", int(math.Ceil(d.Minutes())))
	}
	h := int(d / time.Hour)
	m := int(math.Ceil(float64(d%time.Hour) / float64(time.Minute)))
	if m == 60 {
		h, m = h+1, 0
	}
	if m == 0 {
		return fmt.Sprintf("%d год", h)
	}
	return fmt.Sprintf("%d год %d хв", h, m)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary.
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
