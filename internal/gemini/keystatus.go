package gemini

// Key states for Settings → Google AI Studio: which keys work, which rest after
// a quota hit (and until when), which Google rejected; and a check of every key
// with a free models.list call (a few at a time, politely).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
)

// Key states (KeyStatus.State).
const (
	KeyOK      = "ok"      // worked last time (a call or a check)
	KeyUnknown = "unknown" // not used or checked yet
	KeyResting = "resting" // parked after a quota hit until Until
	KeyError   = "error"   // Google rejected the key (invalid, expired, no access)
)

// badFor is how long a rejected key is skipped while other keys work.
var badFor = time.Hour

// KeyStatus is one Google key's state (never the key itself).
type KeyStatus struct {
	Index     int       // position in Google.Keys (without blanks and repeats)
	Mask      string    // config.Mask
	State     string    // KeyOK | KeyUnknown | KeyResting | KeyError
	Until     time.Time // resting: back then; error: skipped until then
	LastError string    // the last quota or key error, plain words
	Requests  int       // calls made with it since the app started
	Checked   time.Time // last time a call or a check found it working
}

// Status reports every pooled key in order.
func (p *Pool) Status() []KeyStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reactivateElapsed()
	now := p.now()
	out := make([]KeyStatus, 0, len(p.keys))
	for i, s := range p.keys {
		st := KeyStatus{Index: i, Mask: config.Mask(s.key), State: KeyUnknown, Requests: s.requests, Checked: s.checked, LastError: s.lastError}
		switch {
		case now.Before(s.badUntil):
			st.State, st.Until = KeyError, s.badUntil
		case !s.cooldownUntil.IsZero():
			st.State, st.Until = KeyResting, s.cooldownUntil
		case !s.checked.IsZero():
			st.State, st.LastError = KeyOK, ""
		}
		out = append(out, st)
	}
	return out
}

// markBad skips a key Google rejected for badFor (other keys first).
func (p *Pool) markBad(s *keyState, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s.badUntil = p.now().Add(badFor)
	s.lastError = keyErrorText(err)
}

// report records a check of key: nil = it works, a quota error parks it, a
// rejected key is skipped; other failures (network, 5xx) change nothing.
func (p *Pool) report(key string, err error) {
	p.mu.Lock()
	var st *keyState
	for _, s := range p.keys {
		if s.key == key {
			st = s
		}
	}
	p.mu.Unlock()
	if st == nil {
		return
	}
	switch {
	case err == nil:
		p.mu.Lock()
		st.badUntil, st.checked, st.strikes, st.lastError = time.Time{}, p.now(), 0, ""
		if !st.cooldownUntil.IsZero() && st.cooldownUntil.Sub(p.now()) <= time.Minute {
			st.cooldownUntil = time.Time{} // a minute window that is over already
		}
		p.mu.Unlock()
	case isQuotaError(err):
		p.park(st, err)
	case isKeyError(err):
		p.markBad(st, err)
	}
}

// isKeyError: Google refused the key itself (not the request).
func isKeyError(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	b := strings.ToUpper(ae.Body)
	return strings.Contains(b, "API_KEY_INVALID") || strings.Contains(b, "API KEY NOT VALID") ||
		strings.Contains(b, "API_KEY_EXPIRED") || strings.Contains(b, "API_KEY_SERVICE_BLOCKED") ||
		strings.Contains(b, "CONSUMER_SUSPENDED") || strings.Contains(b, "PERMISSION_DENIED") ||
		ae.Status == http.StatusUnauthorized || (ae.Status == http.StatusForbidden && !strings.Contains(b, "LOCATION"))
}

// keyErrorText is a rejected key's reason in plain words.
func keyErrorText(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		b := strings.ToUpper(ae.Body)
		switch {
		case strings.Contains(b, "API_KEY_EXPIRED"):
			return "ключ прострочено — створи новий в AI Studio"
		case strings.Contains(b, "API_KEY_INVALID") || strings.Contains(b, "API KEY NOT VALID"):
			return "ключ недійсний — перевір, чи скопійовано його повністю"
		case strings.Contains(b, "CONSUMER_SUSPENDED"):
			return "проєкт Google цього ключа заблоковано"
		case strings.Contains(b, "SERVICE_BLOCKED") || strings.Contains(b, "PERMISSION_DENIED") || ae.Status == http.StatusForbidden:
			return "ключ не має доступу до Gemini API — увімкни API у проєкті"
		}
		if h := ae.hint(); h != "" {
			return h
		}
	}
	return clip(err.Error(), 160)
}

// KeyStatus is the state of every Google key (no network).
func (c *Client) KeyStatus() []KeyStatus {
	c.sync()
	return c.pool.Status()
}

// Key checks: at most checkParallel at once, started checkGap apart.
var (
	checkParallel = 3
	checkGap      = 120 * time.Millisecond
)

// CheckKeys calls the free models.list with every key (GET models?pageSize=1)
// and records what each answered: working, resting after a quota hit,
// rejected. ok = at least one key works; msg sums it up.
func (c *Client) CheckKeys(ctx context.Context) (bool, string) {
	cfg := c.sync()
	keys := uniqueKeys(cfg.Google.Keys)
	if len(keys) == 0 {
		return false, ErrNoKeys.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, min(30*time.Second+time.Duration(len(keys))*time.Second, 3*time.Minute))
	defer cancel()
	errs := make([]error, len(keys))
	var wg sync.WaitGroup
	sem := make(chan struct{}, checkParallel)
	for i, k := range keys {
		if i > 0 {
			select {
			case <-ctx.Done():
				errs[i] = ctx.Err()
				continue
			case <-time.After(checkGap):
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			actx, acancel := context.WithTimeout(ctx, 20*time.Second)
			defer acancel()
			_, errs[i] = c.call(actx, http.MethodGet, base+"/models?pageSize=1", k, nil, "google:check")
		}()
	}
	wg.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return false, "Перевірку скасовано"
	}
	var firstErr string
	other := 0
	for i, k := range keys {
		c.pool.report(k, errs[i])
		if err := errs[i]; err != nil && !isQuotaError(err) && !isKeyError(err) {
			other++
			if firstErr == "" {
				firstErr = clip(err.Error(), 200)
			}
		}
	}
	return checkSummary(c.pool.Status(), other, firstErr)
}

// checkSummary: «Google: працюють 5 з 6 ключів · 1 відпочиває до 14:05 · 1 з
// помилкою: ключ недійсний …».
func checkSummary(st []KeyStatus, other int, otherErr string) (bool, string) {
	ok, resting, bad := 0, 0, 0
	var until time.Time
	badMsg := ""
	for _, s := range st {
		switch s.State {
		case KeyOK:
			ok++
		case KeyResting:
			resting++
			if until.IsZero() || s.Until.Before(until) {
				until = s.Until
			}
		case KeyError:
			bad++
			if badMsg == "" {
				badMsg = s.LastError
			}
		}
	}
	n := len(st)
	if n == 1 {
		switch {
		case ok == 1:
			return true, "Google: ключ працює"
		case resting == 1:
			return false, "Google: ключ дійсний, але вичерпав ліміт — відпочиває до " + hhmm(until)
		case bad == 1:
			return false, "Google: " + badMsg
		}
		return false, "Google: " + firstNonBlank(otherErr, "ключ не відповів — спробуй ще раз")
	}
	msg := fmt.Sprintf("Google: %s %d з %d ключів", pl(ok, "працює", "працюють", "працюють"), ok, n)
	if resting > 0 {
		msg += fmt.Sprintf(" · %d %s до %s", resting, pl(resting, "відпочиває", "відпочивають", "відпочивають"), hhmm(until))
	}
	if bad > 0 {
		msg += fmt.Sprintf(" · %d з помилкою: %s", bad, badMsg)
	}
	if other > 0 && otherErr != "" {
		msg += fmt.Sprintf(" · %d не %s: %s", other, pl(other, "відповів", "відповіли", "відповіли"), otherErr)
	}
	return ok > 0, msg
}

// hhmm: "14:05", or "завтра 09:00" past today.
func hhmm(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	l := t.Local()
	now := time.Now().Local()
	if l.YearDay() != now.YearDay() || l.Year() != now.Year() {
		return "завтра " + l.Format("15:04")
	}
	return l.Format("15:04")
}

func pl(n int, one, few, many string) string {
	n %= 100
	switch {
	case n >= 11 && n <= 14:
		return many
	case n%10 == 1:
		return one
	case n%10 >= 2 && n%10 <= 4:
		return few
	}
	return many
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
