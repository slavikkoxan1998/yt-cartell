package tts

// Key pool: TTS.APIKey + TTS.Keys of one provider rotate per request. The
// rules follow the user's own Cartesia client "Cartelsia" (pkg/keys/manager.go,
// pkg/orchestrator/orchestrator.go, pkg/cartesia/errors.go), adapted to every
// provider here:
//
//   - Round-robin: each request takes the next usable key after the one used
//     last (Cartelsia's rrIndex), never more requests at once on one key than
//     the provider allows (Cartelsia: two slots per key; here
//     ProviderInfo.Concurrency). When every usable key is busy the request
//     waits for a free slot.
//   - Monthly quota / credits ran out ("exhausted"): the key is skipped until
//     00:00 local time on the 1st of next month (Cartelsia froze such a key for
//     30 days; the user wants the calendar month), then tried again.
//   - The provider rejects the key (invalid, revoked, blocked): "error" — the
//     key is skipped for errorRetry (1 h), or until the key list changes or
//     ResetKeyErrors runs (Cartelsia froze it until re-validated).
//   - Rate / concurrency limit: the key "rests" for a few seconds (the
//     provider's Retry-After when it sends one) and the request moves on to
//     the next key; network and 5xx failures also retry with the next key, and
//     back off when every key failed (Cartelsia: up to 5 attempts per chunk,
//     quota and auth failures not counted).
//   - A failure that is this key's account but not the key's fault (a voice
//     of another account, a feature of a higher plan): the next key is tried
//     and the key keeps its state.
//
// The states persist (SetStateFile) in tts-keys.json next to config.json:
// key id (sha-256 of provider + key — never the key itself) → state, until,
// last error, last use, characters this month. A client starts in memory, so
// tests and in-memory settings never write anything.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
)

// Key states shown in Settings → Озвучка.
const (
	KeyOK        = "ok"        // usable
	KeyExhausted = "exhausted" // monthly quota ran out: rests until the 1st of next month
	KeyError     = "error"     // invalid / blocked key: skipped, retried after a while or when keys change
	KeyResting   = "resting"   // short pause after a rate limit
)

// KeyStateFile is the file of the key states, next to config.json.
const KeyStateFile = "tts-keys.json"

// DefaultStateFile is <settings dir>/tts-keys.json. Only for the app's
// persistent settings (config.NewStore) — never for tests.
func DefaultStateFile() string { return filepath.Join(config.Dir(), KeyStateFile) }

// Pool tunables (vars so tests can shorten them).
var (
	errorRetry  = time.Hour        // a key in "error" is tried again after this
	maxWaitRest = 2 * time.Minute  // a request waits for a resting key at most this long
	maxRest     = time.Minute      // longest computed rate-limit rest (Retry-After may ask for more)
	missTTL     = 10 * time.Minute // "this voice is not on that key's account" is remembered this long
	saveDelay   = time.Second      // usage counters are written at most once per saveDelay
	keepStale   = 120 * 24 * time.Hour
	maxRetries  = 5 // rate-limit and transient retries per request
)

// KeyStatus is the state of one TTS API key (never the key itself).
type KeyStatus struct {
	Mask      string    `json:"mask"`  // e.g. "sk_ab…12cd" (config.Mask)
	State     string    `json:"state"` // KeyOK | KeyExhausted | KeyError | KeyResting
	Until     time.Time `json:"until"` // exhausted/error/resting: usable again at this time; zero when ok
	LastError string    `json:"lastError,omitempty"`
	LastUsed  time.Time `json:"lastUsed"`  // zero = not used yet
	UsedChars int       `json:"usedChars"` // characters this calendar month (counted here, or reported by the provider)
	Limit     int       `json:"limit,omitempty"`
	// Index is the key's position in [TTS.APIKey, TTS.Keys...] (0 = APIKey);
	// for Shared keys the position in Google.Keys.
	Index int `json:"index"`
	// Shared: no TTS key is set and provider "google" borrows the Google AI
	// Studio keys (Налаштування → Google).
	Shared bool `json:"shared,omitempty"`
	// Checked is when a check («Перевірити», a bulk add) last found the key
	// working; zero = never.
	Checked time.Time `json:"checked"`
	// Plan is the account's plan as the provider names it ("free",
	// "creator"; "" = unknown) — ElevenLabs reports it.
	Plan string `json:"plan,omitempty"`
	// Info is what the last successful check said about the key ("тариф free
	// · залишилось 9 120 символів", "баланс 12.50 $").
	Info string `json:"info,omitempty"`
}

// KeyStatus reports every configured key of the current provider, in the
// order of [APIKey, Keys...] (empty and repeated keys left out). No network;
// cheap enough for every Settings refresh.
func (c *Client) KeyStatus() []KeyStatus { return c.pool.status(c.settings()) }

// ResetKeyErrors makes keys in the "error" and "resting" states usable again
// (Settings: «Скинути помилки»). Exhausted keys keep waiting for the 1st.
func (c *Client) ResetKeyErrors() { c.pool.resetErrors() }

// SetStateFile makes the key states persistent: path is loaded now and every
// change is written there (normally DefaultStateFile()). "" keeps them in
// memory (the default). A damaged file is ignored and rewritten.
func (c *Client) SetStateFile(path string) error { return c.pool.open(path) }

// FlushKeyState writes pending key-state changes right away (app exit).
func (c *Client) FlushKeyState() error { return c.pool.flush() }

// ---- keys of the settings ---------------------------------------------------------

// poolKey is one configured key.
type poolKey struct {
	id, key, mask string
	index         int
	shared        bool
}

// keyID names a key in the state file without revealing it.
func keyID(provider, key string) string {
	h := sha256.Sum256([]byte(provider + "\x00" + key))
	return hex.EncodeToString(h[:8])
}

// poolKeys is the rotation list: APIKey, then Keys (trimmed, first occurrence
// wins). Provider "google" without any key borrows the Google AI Studio keys.
func poolKeys(provider string, t config.TTS, google []string) []poolKey {
	var out []poolKey
	seen := map[string]bool{}
	add := func(k string, i int, shared bool) {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, poolKey{id: keyID(provider, k), key: k, mask: config.Mask(k), index: i, shared: shared})
	}
	add(t.APIKey, 0, false)
	for i, k := range t.Keys {
		add(k, i+1, false)
	}
	if len(out) == 0 && provider == "google" {
		for i, k := range google {
			add(k, i, true)
		}
	}
	return out
}

// ---- failures -----------------------------------------------------------------------

// failKind says what a failed request means for the key that made it.
type failKind int

const (
	failFatal     failKind = iota // the request itself is wrong: every key would fail alike
	failSkip                      // this key's account cannot serve this request: next key, no blame
	failTransient                 // network / 5xx: retry, other keys first
	failRate                      // rate or concurrency limit: the key rests briefly
	failAuth                      // the key is invalid / revoked / blocked: "error"
	failQuota                     // the monthly quota / credits ran out: "exhausted" until the 1st
)

// apiErr is a classified provider failure with a friendly Ukrainian message.
type apiErr struct {
	kind  failKind
	msg   string
	retry time.Duration // failRate: the provider's Retry-After (0 = backoff)
	daily bool          // failRate: a per-day limit — rest until local midnight
	voice bool          // failSkip: the voice is not on this key's account
}

func (e *apiErr) Error() string { return e.msg }

func fatal(msg string) *apiErr { return &apiErr{kind: failFatal, msg: msg} }

// kindOf classifies any error of a provider call (unknown errors are fatal).
func kindOf(err error) *apiErr {
	var ae *apiErr
	if errors.As(err, &ae) {
		return ae
	}
	return &apiErr{kind: failFatal, msg: err.Error()}
}

// ---- the pool -------------------------------------------------------------------------

type keyRec struct {
	Provider  string    `json:"provider"`
	Mask      string    `json:"mask"`
	State     string    `json:"state"`
	Until     time.Time `json:"until,omitzero"`
	LastError string    `json:"lastError,omitempty"`
	LastUsed  time.Time `json:"lastUsed,omitzero"`
	Month     string    `json:"month,omitempty"` // "2026-10": the month UsedChars counts
	UsedChars int       `json:"usedChars,omitempty"`
	Limit     int       `json:"limit,omitempty"`  // provider-reported monthly limit (0 = unknown)
	Checked   time.Time `json:"checked,omitzero"` // last check that found the key working
	Plan      string    `json:"plan,omitempty"`   // the account's plan ("free", "creator")
	Info      string    `json:"info,omitempty"`   // the last check's note (no secrets)
	strikes   int       // consecutive rate limits (backoff)
}

type stateDoc struct {
	Version int                `json:"version"`
	Sig     map[string]string  `json:"sig,omitempty"` // provider → fingerprint of its configured key list
	Keys    map[string]*keyRec `json:"keys"`
}

type pool struct {
	mu    sync.Mutex
	now   func() time.Time
	path  string // "" = memory only
	recs  map[string]*keyRec
	sig   map[string]string
	busy  map[string]int       // requests in flight per key id
	rr    map[string]int       // round-robin cursor per provider
	miss  map[string]time.Time // provider|key id|voice → the voice is not on that account (until)
	wake  chan struct{}        // closed and replaced when a slot frees up or a state changes
	dirty bool
	saveT *time.Timer

	saveMu sync.Mutex // serialises file writes
}

func newPool() *pool {
	return &pool{
		now:  time.Now,
		recs: map[string]*keyRec{},
		sig:  map[string]string{},
		busy: map[string]int{},
		rr:   map[string]int{},
		miss: map[string]time.Time{},
		wake: make(chan struct{}),
	}
}

func (p *pool) wakeLocked() {
	close(p.wake)
	p.wake = make(chan struct{})
}

func monthOf(t time.Time) string { return t.Format("2006-01") }

// firstOfNextMonth is 00:00 on the 1st of the month after t (t's location).
func firstOfNextMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, t.Location())
}

// syncLocked brings the records of s's keys up to date at t: new keys start
// "ok", expired blocks end, a new month restarts the character count, and a
// changed key list gives failing keys another chance.
func (p *pool) syncLocked(s settings, t time.Time) {
	changed := false
	ids := make([]string, 0, len(s.keys))
	month := monthOf(t)
	for _, k := range s.keys {
		ids = append(ids, k.id)
		r := p.recs[k.id]
		if r == nil {
			r = &keyRec{Provider: s.provider, State: KeyOK}
			p.recs[k.id] = r
		}
		r.Mask = k.mask
		if r.State == "" {
			r.State = KeyOK
		}
		if r.State != KeyOK && !t.Before(r.Until) {
			r.State, r.Until, r.LastError, r.strikes = KeyOK, time.Time{}, "", 0
			changed = true
		}
		if r.Month != "" && r.Month != month {
			r.Month, r.UsedChars = month, 0
			changed = true
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(s.provider + "\x00" + strings.Join(ids, ",")))
	sig := hex.EncodeToString(sum[:8])
	if old, ok := p.sig[s.provider]; !ok || old != sig {
		if ok { // the user edited the keys: failing keys get another chance
			for _, k := range s.keys {
				if r := p.recs[k.id]; r.State == KeyError || r.State == KeyResting {
					r.State, r.Until, r.LastError, r.strikes = KeyOK, time.Time{}, "", 0
				}
			}
		}
		p.sig[s.provider] = sig
		changed = true
	}
	if changed {
		p.saveLocked(false)
		p.wakeLocked()
	}
}

// lease is one request's hold on a key; finish it with ok or fail.
type lease struct {
	p    *pool
	k    poolKey
	prov string
	done bool
}

// acquire picks the key for one request: round-robin over the usable keys
// not in excl (sticky: the first usable one in settings order, for voice
// lists). It waits while every usable key is busy or resting briefly; it
// fails with *noKeyError when no key can serve the request.
func (p *pool) acquire(ctx context.Context, s settings, excl map[string]bool, sticky bool) (*lease, error) {
	for {
		p.mu.Lock()
		t := p.now()
		p.syncLocked(s, t)
		l, wait, err := p.pickLocked(s, excl, sticky, t)
		ch := p.wake
		p.mu.Unlock()
		if l != nil || err != nil {
			return l, err
		}
		var timeout <-chan time.Time
		var timer *time.Timer
		if wait > 0 {
			timer = time.NewTimer(wait)
			timeout = timer.C
		}
		select {
		case <-ctx.Done():
		case <-ch:
		case <-timeout:
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
}

// pickLocked returns a lease, or how long to wait (0 = until woken), or the
// reason why no key can serve.
func (p *pool) pickLocked(s settings, excl map[string]bool, sticky bool, t time.Time) (*lease, time.Duration, error) {
	n := len(s.keys)
	if n == 0 {
		return nil, 0, errNoKey(s)
	}
	limit := max(1, s.info.Concurrency)
	start := 0
	if !sticky {
		start = p.rr[s.provider] % n
	}
	busy := false
	wait := time.Duration(-1)
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		k := s.keys[idx]
		if excl[k.id] {
			continue
		}
		r := p.recs[k.id]
		if r.State == KeyResting {
			if d := r.Until.Sub(t); d <= maxWaitRest && (wait < 0 || d < wait) {
				wait = max(d, time.Millisecond)
			}
			continue
		}
		if r.State != KeyOK {
			continue
		}
		if p.busy[k.id] >= limit {
			busy = true
			continue
		}
		p.busy[k.id]++
		if !sticky {
			p.rr[s.provider] = idx + 1
		}
		return &lease{p: p, k: k, prov: s.provider}, 0, nil
	}
	switch {
	case busy && wait > 0:
		return nil, wait, nil
	case busy:
		return nil, 0, nil
	case wait > 0:
		return nil, wait, nil
	}
	return nil, 0, p.noKeyLocked(s, excl, t)
}

// ok records a successful request: used characters (provider-reported or
// counted), last use; a resting key works again.
func (l *lease) ok(used int) {
	p := l.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if l.release() {
		return
	}
	defer p.wakeLocked()
	t := p.now()
	r := p.recs[l.k.id]
	if r == nil {
		return
	}
	r.LastUsed = t
	if m := monthOf(t); r.Month != m {
		r.Month, r.UsedChars = m, 0
	}
	r.UsedChars += max(0, used)
	r.strikes = 0
	if r.State == KeyResting {
		r.State, r.Until = KeyOK, time.Time{}
	}
	if r.State == KeyOK {
		r.LastError = ""
	}
	p.saveLocked(false)
}

// fail records a failed request on the key (see failKind).
func (l *lease) fail(ae *apiErr, voice string) {
	p := l.p
	p.mu.Lock()
	defer p.mu.Unlock()
	if l.release() {
		return
	}
	defer p.wakeLocked()
	t := p.now()
	r := p.recs[l.k.id]
	if r == nil {
		return
	}
	switch ae.kind {
	case failQuota:
		r.State, r.Until, r.LastError, r.strikes = KeyExhausted, firstOfNextMonth(t), ae.msg, 0
		p.saveLocked(true)
	case failAuth:
		r.State, r.Until, r.LastError, r.strikes = KeyError, t.Add(errorRetry), ae.msg, 0
		p.saveLocked(true)
	case failRate:
		r.strikes++
		d := ae.retry
		if d <= 0 {
			d = min(maxRest, retryBase*time.Duration(min(r.strikes, 8)))
		}
		until := t.Add(d)
		if ae.daily {
			y, m, day := t.Date()
			until = time.Date(y, m, day+1, 0, 0, 0, 0, t.Location())
		}
		if r.State == KeyOK || r.State == KeyResting {
			r.State, r.Until, r.LastError = KeyResting, until, ae.msg
		}
		p.saveLocked(false)
	case failSkip:
		if ae.voice && voice != "" {
			p.miss[l.prov+"|"+l.k.id+"|"+voice] = t.Add(missTTL)
		}
	}
}

// cancel gives the key back without judging it (the request was cancelled).
func (l *lease) cancel() {
	l.p.mu.Lock()
	defer l.p.mu.Unlock()
	if !l.release() {
		l.p.wakeLocked()
	}
}

// release frees the slot (caller holds p.mu); true when already released.
func (l *lease) release() bool {
	if l.done {
		return true
	}
	l.done = true
	if l.p.busy[l.k.id]--; l.p.busy[l.k.id] <= 0 {
		delete(l.p.busy, l.k.id)
	}
	return false
}

// missing are the keys known not to have voice (none when that is all of them).
func (p *pool) missing(s settings, voice string) map[string]bool {
	excl := map[string]bool{}
	if voice == "" {
		return excl
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	for _, k := range s.keys {
		key := s.provider + "|" + k.id + "|" + voice
		if until, ok := p.miss[key]; ok {
			if t.Before(until) {
				excl[k.id] = true
			} else {
				delete(p.miss, key)
			}
		}
	}
	if len(excl) >= len(s.keys) {
		return map[string]bool{}
	}
	return excl
}

// report stores what the provider says about a key: its monthly usage and
// limit (ElevenLabs subscription), and whether its quota is spent (true:
// exhausted until the 1st) or available again (false: an exhausted key is
// usable). quota nil = unknown.
func (p *pool) report(s settings, k poolKey, used, limit int, quota *bool, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	p.syncLocked(s, t)
	r := p.recs[k.id]
	if r == nil {
		return
	}
	if used >= 0 {
		r.Month, r.UsedChars = monthOf(t), used
	}
	if limit > 0 {
		r.Limit = limit
	}
	if quota != nil {
		switch {
		case *quota && r.State != KeyExhausted:
			r.State, r.Until, r.LastError = KeyExhausted, firstOfNextMonth(t), msg
		case !*quota && r.State == KeyExhausted:
			r.State, r.Until, r.LastError = KeyOK, time.Time{}, ""
		}
	}
	p.saveLocked(true)
	p.wakeLocked()
}

// mark sets a key's state after a check (TestTTS): ok clears an error and
// remembers when the key was found working, with the check's note.
func (p *pool) mark(s settings, k poolKey, ae *apiErr, info string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	p.syncLocked(s, t)
	r := p.recs[k.id]
	if r == nil {
		return
	}
	switch {
	case ae == nil:
		if r.State == KeyError || r.State == KeyResting {
			r.State, r.Until, r.LastError, r.strikes = KeyOK, time.Time{}, "", 0
		}
		r.Checked, r.Info = t, info
	case ae.kind == failAuth:
		r.State, r.Until, r.LastError = KeyError, t.Add(errorRetry), ae.msg
	case ae.kind == failQuota:
		r.State, r.Until, r.LastError = KeyExhausted, firstOfNextMonth(t), ae.msg
	default:
		return
	}
	p.saveLocked(true)
	p.wakeLocked()
}

// setPlan remembers the plan a key's account is on ("free", "creator"…).
func (p *pool) setPlan(s settings, k poolKey, plan string) {
	plan = strings.ToLower(strings.TrimSpace(plan))
	if plan == "" || k.id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.syncLocked(s, p.now())
	if r := p.recs[k.id]; r != nil && r.Plan != plan {
		r.Plan = plan
		p.saveLocked(false)
	}
}

// plans are the known plans of s's keys that can serve requests now (a key
// in "error" or "exhausted" cannot), by key id.
func (p *pool) plans(s settings) map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	for _, k := range s.keys {
		if r := p.recs[k.id]; r != nil && r.Plan != "" && (r.State == KeyOK || r.State == KeyResting) {
			out[k.id] = r.Plan
		}
	}
	return out
}

// usable is the first key that is not blocked ("" when none): voice and
// model lists of a stable account.
func (p *pool) usable(s settings) poolKey {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	p.syncLocked(s, t)
	for _, k := range s.keys {
		if r := p.recs[k.id]; r != nil && (r.State == KeyOK || r.State == KeyResting) {
			return k
		}
	}
	if len(s.keys) > 0 {
		return s.keys[0]
	}
	return poolKey{}
}

func (p *pool) status(s settings) []KeyStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	p.syncLocked(s, t)
	month := monthOf(t)
	out := make([]KeyStatus, 0, len(s.keys))
	for _, k := range s.keys {
		r := p.recs[k.id]
		st := KeyStatus{Mask: k.mask, State: r.State, LastError: r.LastError, LastUsed: r.LastUsed, Limit: r.Limit, Index: k.index, Shared: k.shared,
			Checked: r.Checked, Plan: r.Plan, Info: r.Info}
		if r.State != KeyOK {
			st.Until = r.Until
		}
		if r.Month == month {
			st.UsedChars = r.UsedChars
		}
		out = append(out, st)
	}
	return out
}

func (p *pool) resetErrors() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.recs {
		if r.State == KeyError || r.State == KeyResting {
			r.State, r.Until, r.LastError, r.strikes = KeyOK, time.Time{}, "", 0
		}
	}
	clear(p.miss)
	p.saveLocked(true)
	p.wakeLocked()
}

// ---- no usable key --------------------------------------------------------------------

// noKeyError: no configured key can serve the request right now.
type noKeyError struct {
	label                                     string
	total, exhausted, errored, resting, other int
	until, restUntil                          time.Time // earliest end of exhausted / resting
	lastErr, restErr                          string
	now                                       time.Time
}

func (p *pool) noKeyLocked(s settings, excl map[string]bool, t time.Time) *noKeyError {
	e := &noKeyError{label: s.info.Label, total: len(s.keys), now: t}
	for _, k := range s.keys {
		r := p.recs[k.id]
		switch {
		case r.State == KeyExhausted:
			e.exhausted++
			if e.until.IsZero() || r.Until.Before(e.until) {
				e.until = r.Until
			}
		case r.State == KeyError:
			e.errored++
			e.lastErr = firstNonEmpty(e.lastErr, r.LastError)
		case r.State == KeyResting && !excl[k.id]:
			e.resting++
			e.restErr = firstNonEmpty(e.restErr, r.LastError)
			if e.restUntil.IsZero() || r.Until.Before(e.restUntil) {
				e.restUntil = r.Until
			}
		default: // tried in this request
			e.other++
		}
	}
	return e
}

// blocked: some key is unusable because of its state (not only this request).
func (e *noKeyError) blocked() bool { return e.exhausted+e.errored+e.resting > 0 }

func (e *noKeyError) Error() string {
	label := firstNonEmpty(e.label, "озвучки")
	switch {
	case e.total > 0 && e.exhausted == e.total:
		if e.total == 1 {
			return fmt.Sprintf("Ключ %s вичерпав ліміт до %s — додай ще ключ у Налаштуваннях → Озвучка", label, dayMonth(e.until))
		}
		return fmt.Sprintf("Усі ключі вичерпали ліміт до %s (%s) — додай ще ключ у Налаштуваннях → Озвучка", dayMonth(e.until), label)
	case e.total > 0 && e.errored == e.total:
		if e.total == 1 && e.lastErr != "" {
			return e.lastErr
		}
		return fmt.Sprintf("Жоден ключ %s не працює: %s — перевір ключі в Налаштуваннях → Озвучка", label, firstNonEmpty(e.lastErr, "сервіс їх відхилив"))
	case e.total > 0 && e.resting == e.total:
		if e.total == 1 {
			return fmt.Sprintf("Ключ %s тимчасово обмежений до %s: %s", label, e.when(e.restUntil), firstNonEmpty(e.restErr, "забагато запитів"))
		}
		return fmt.Sprintf("Усі ключі %s тимчасово обмежені до %s — спробуй пізніше", label, e.when(e.restUntil))
	}
	var parts []string
	if n := e.exhausted; n > 0 {
		parts = append(parts, fmt.Sprintf("%s %s ліміт до %s", keysCount(n), plural(n, "вичерпав", "вичерпали", "вичерпали"), dayMonth(e.until)))
	}
	if n := e.errored; n > 0 {
		parts = append(parts, fmt.Sprintf("%s з помилкою (%s)", keysCount(n), firstNonEmpty(e.lastErr, "сервіс відхилив")))
	}
	if n := e.resting; n > 0 {
		parts = append(parts, fmt.Sprintf("%s тимчасово %s до %s", keysCount(n), plural(n, "обмежений", "обмежені", "обмежені"), e.when(e.restUntil)))
	}
	if n := e.other; n > 0 {
		parts = append(parts, fmt.Sprintf("%s не %s для цього запиту", keysCount(n), plural(n, "підійшов", "підійшли", "підійшли")))
	}
	return fmt.Sprintf("Немає вільних ключів %s: %s", label, strings.Join(parts, "; "))
}

// when is "14:30" today, else "2 листопада о 14:30".
func (e *noKeyError) when(t time.Time) string {
	t = t.In(e.now.Location())
	if y, m, d := t.Date(); y == e.now.Year() && m == e.now.Month() && d == e.now.Day() {
		return t.Format("15:04")
	}
	return dayMonth(t) + " о " + t.Format("15:04")
}

var monthsGen = [12]string{"січня", "лютого", "березня", "квітня", "травня", "червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}

// dayMonth is "1 листопада".
func dayMonth(t time.Time) string { return fmt.Sprintf("%d %s", t.Day(), monthsGen[t.Month()-1]) }

// plural picks the Ukrainian form for n: one (1, 21…), few (2–4, 22–24…), many.
func plural(n int, one, few, many string) string {
	n %= 100
	switch {
	case n%10 == 1 && n != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n < 12 || n > 14):
		return few
	}
	return many
}

func keysCount(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "ключ", "ключі", "ключів"))
}

// ---- persistence ------------------------------------------------------------------------

func (p *pool) open(path string) error {
	p.mu.Lock()
	p.path = path
	p.mu.Unlock()
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("не вдалося прочитати стан ключів озвучки: %w", err)
	}
	var doc stateDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("стан ключів озвучки пошкоджено, починаємо з чистого: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, r := range doc.Keys {
		if r == nil || p.recs[id] != nil {
			continue
		}
		switch r.State {
		case KeyOK, KeyExhausted, KeyError, KeyResting:
		default:
			r.State = KeyOK
		}
		p.recs[id] = r
	}
	for prov, sig := range doc.Sig {
		if _, ok := p.sig[prov]; !ok {
			p.sig[prov] = sig
		}
	}
	p.wakeLocked()
	return nil
}

// saveLocked marks the state dirty and writes it: now (state changes) or
// debounced (usage counters). Caller holds p.mu.
func (p *pool) saveLocked(now bool) {
	if p.path == "" {
		return
	}
	p.dirty = true
	if now {
		if p.saveT != nil {
			p.saveT.Stop()
			p.saveT = nil
		}
		go func() {
			if err := p.flush(); err != nil {
				log.Printf("[tts] key state: %v", err)
			}
		}()
		return
	}
	if p.saveT == nil {
		p.saveT = time.AfterFunc(saveDelay, func() {
			if err := p.flush(); err != nil {
				log.Printf("[tts] key state: %v", err)
			}
		})
	}
}

// flush writes the state file when something changed (atomic: temp + rename).
func (p *pool) flush() error {
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	p.mu.Lock()
	if p.saveT != nil {
		p.saveT.Stop()
		p.saveT = nil
	}
	if !p.dirty || p.path == "" {
		p.mu.Unlock()
		return nil
	}
	t := p.now()
	doc := stateDoc{Version: 1, Sig: map[string]string{}, Keys: map[string]*keyRec{}}
	for prov, sig := range p.sig {
		doc.Sig[prov] = sig
	}
	for id, r := range p.recs {
		if last := maxTime(maxTime(r.LastUsed, r.Until), r.Checked); !last.IsZero() && t.Sub(last) > keepStale {
			continue // a key not seen for months (any state: its block ended long ago)
		}
		cp := *r
		doc.Keys[id] = &cp
	}
	path := p.path
	p.dirty = false
	p.mu.Unlock()

	b, err := json.MarshalIndent(doc, "", "  ")
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			tmp := path + ".tmp"
			if err = os.WriteFile(tmp, b, 0o600); err == nil {
				err = os.Rename(tmp, path)
			}
		}
	}
	if err != nil {
		p.mu.Lock()
		p.dirty = true
		p.mu.Unlock()
		return fmt.Errorf("не вдалося зберегти стан ключів озвучки: %w", err)
	}
	return nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// ---- the request loop ---------------------------------------------------------------

// run performs one provider request (op gets the key; "" for a provider that
// works without one) with the pool: the next usable key, other keys when this
// one failed for its own reason, retries of rate limits and transient failures
// (at most maxRetries, other keys first). op returns the characters the
// provider billed (0 = count the text: chars). sticky keeps the first usable
// key (voice lists) instead of rotating.
func (c *Client) run(ctx context.Context, s settings, voice string, chars int, sticky bool, op func(key string) (used int, err error)) error {
	if len(s.keys) == 0 {
		if s.info.KeyOptional {
			_, err := op("")
			return err
		}
		return errNoKey(s)
	}
	excl := c.pool.missing(s, voice) // out for this request (starts with keys known to lack the voice)
	temp := map[string]bool{}        // failed transiently in this request
	var lastErr error
	retries := 0
	for {
		both := excl
		if len(temp) > 0 {
			both = make(map[string]bool, len(excl)+len(temp))
			for id := range excl {
				both[id] = true
			}
			for id := range temp {
				both[id] = true
			}
		}
		l, err := c.pool.acquire(ctx, s, both, sticky)
		if err != nil {
			var nk *noKeyError
			if errors.As(err, &nk) && lastErr != nil && !nk.blocked() {
				return lastErr // every key was tried for this request: the provider's own words
			}
			return err
		}
		used, err := guarded(op, l.k.key)
		if err == nil {
			if used <= 0 {
				used = chars
			}
			l.ok(used)
			return nil
		}
		if ctx.Err() != nil {
			l.cancel()
			return ctx.Err()
		}
		ae := withKeyHint(s, l.k.key, kindOf(err))
		l.fail(ae, voice)
		lastErr = ae
		switch ae.kind {
		case failFatal:
			return ae
		case failRate, failTransient:
			if retries++; retries > maxRetries {
				return ae
			}
			temp[l.k.id] = true
			if len(temp)+len(excl) >= len(s.keys) { // every key had its turn: again, after a pause
				clear(temp)
				if ae.kind == failTransient {
					if err := sleep(ctx, retryBase*time.Duration(retries)); err != nil {
						return err
					}
				}
			}
		default: // auth, quota, skip: this key is out for this request
			excl[l.k.id] = true
			delete(temp, l.k.id)
		}
	}
}

// guarded runs op so that a panic in a provider adapter becomes an error
// (logged) instead of leaving the key's slot taken forever.
func guarded(op func(key string) (int, error), key string) (used int, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tts] panic in a provider call: %v\n%s", r, debug.Stack())
			used, err = 0, fatal(fmt.Sprintf("Внутрішня помилка озвучки: %v", r))
		}
	}()
	return op(key)
}

// errNoKey: the provider needs a key and none is set.
func errNoKey(s settings) error {
	label := firstNonEmpty(s.info.Label, "озвучки")
	if s.provider == "elevenlabs" {
		return errors.New("Ключ ElevenLabs не задано (Налаштування → Озвучка)")
	}
	return fmt.Errorf("Ключ %s не задано (Налаштування → Озвучка)", label)
}
