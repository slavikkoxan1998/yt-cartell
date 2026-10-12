// Package tgbot turns the user's own Telegram bot (Settings → Telegram) into
// a remote control of the studio running on this computer: new projects from
// a text, a .txt file or a voice message, remakes after a YouTube example,
// the projects with their stage and buttons, the queue, profiles, a health
// check, live progress edited in place and the finished video delivered into
// the chat (compressed or in parts above the Bot API's 50 MB).
//
// Architecture:
//
//	Bot.Start      a supervisor goroutine watches the settings: it long-polls
//	               getUpdates (poll.go) only while a token is set and the
//	               remote control is on and paired — or a pairing code waits
//	dispatch       every update is authorised first (auth.go); the owner's
//	               updates go to one worker goroutine per chat (router.go),
//	               everyone else gets «Цей бот приватний» and nothing else
//	menus, flows   menus.go (projects, card, queue, profiles), newproject.go,
//	               remake.go, status.go, delivery.go (video, srt, cover,
//	               frames, voice lines)
//	live.go        one status message per running job, edited in place; the
//	               engine's TelegramHooks report every job end
//	send.go        per-chat pacing (≤ 1 message/s, ≤ 20/min in groups), 429
//	               retry_after, HTML escaping and Telegram's length limits
//
// Everything goes through the engine's public API. Nothing here runs in
// headless renders or tests unless a test starts a Bot against a mock server.
package tgbot

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

// Status is what the poller is doing.
type Status int

const (
	// StatusOff: nothing to listen to (no token, the remote control is off
	// or not paired, no pairing code waits).
	StatusOff Status = iota
	// StatusStarting: connecting to Telegram.
	StatusStarting
	// StatusOn: listening.
	StatusOn
	// StatusOffline: Telegram cannot be reached; retrying with a growing pause.
	StatusOffline
	// StatusConflict: another program reads this bot's updates (409).
	StatusConflict
	// StatusWebhook: a webhook is set on the bot (409); getUpdates cannot work.
	StatusWebhook
	// StatusBadToken: Telegram refused the token.
	StatusBadToken
	// StatusBusyHere: another copy of the studio on this computer listens.
	StatusBusyHere
)

// State is the bot's state for Settings.
type State struct {
	Status Status
	// Note explains a problem in plain Ukrainian ("" when all is well).
	Note string
	// Username is the bot's @username (without "@") once Telegram told it.
	Username string
	// Code is the pairing code while it waits (CodeUntil = its expiry).
	Code      string
	CodeUntil time.Time
	// PairNote says why a pairing code ended early ("" = nothing to say).
	PairNote string
	// Attempts are people who wrote in the control group without access
	// (newest first, ≤ 5) — Settings can allow them.
	Attempts []Attempt
}

// Attempt is someone the bot refused in the control chat.
type Attempt struct {
	ID   int64
	Name string
	At   time.Time
}

// Options tune a Bot; zero values are the production defaults.
type Options struct {
	// StateDir keeps the update offset and the poller lock ("" = config.Dir()).
	StateDir string
	// BaseURL overrides the Bot API server (tests); "" = Telegram.APIServer,
	// else api.telegram.org.
	BaseURL string
	// UploadLimit overrides the biggest upload (tests; 0 = Telegram's).
	UploadLimit int64
	// PollTimeout is the long-poll timeout in seconds (0 = 50).
	PollTimeout int
	// LiveAfter: a job started on the computer gets its live message after
	// running this long (0 = 20 s). Jobs started from the bot get one at once.
	LiveAfter time.Duration
	// Retry is the base pause after a failed poll (0 = 1 s; tests shorten it).
	Retry time.Duration
}

// Bot is the remote control. Create one per app with New, then Start; Stop
// before the process exits.
type Bot struct {
	e   *studio.Engine
	cfg *config.Store
	opt Options

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	wake   chan struct{}
	start  time.Time

	running   atomic.Bool // Start ran, Stop did not
	listening atomic.Bool // getUpdates is answering

	mu       sync.Mutex
	st       State
	pair     *pairing
	halted   string // the token|server a fatal error stopped on (cleared by Reconnect / a change)
	lastChat notify.Chat
	workers  map[int64]*worker
	sessions map[int64]*session
	subs     map[int]func()
	nextSub  int
	botID    int64

	pace    *pacer
	live    *liveState
	heavy   chan struct{} // one video delivery (ffmpeg, a big upload) at a time
	deny    chan denial
	unsub   func()
	preview bool // Preview: a pinned state, never polls
}

var active atomic.Pointer[Bot]

// Active is the started bot of this process (nil in headless renders and tests).
func Active() *Bot { return active.Load() }

// New creates the remote control of e; it does nothing until Start.
func New(e *studio.Engine, opt Options) *Bot {
	if opt.StateDir == "" {
		opt.StateDir = config.Dir()
	}
	if opt.PollTimeout <= 0 {
		opt.PollTimeout = 50
	}
	if opt.LiveAfter <= 0 {
		opt.LiveAfter = 20 * time.Second
	}
	if opt.Retry <= 0 {
		opt.Retry = time.Second
	}
	b := &Bot{
		e: e, opt: opt,
		wake:     make(chan struct{}, 1),
		workers:  map[int64]*worker{},
		sessions: map[int64]*session{},
		subs:     map[int]func(){},
		pace:     newPacer(),
		heavy:    make(chan struct{}, 1),
		deny:     make(chan denial, 32),
	}
	if e != nil {
		b.cfg = e.Config()
	}
	b.live = newLive(b)
	b.ctx, b.cancel = context.WithCancel(context.Background())
	return b
}

// Preview is a bot pinned to st for headless renders of Settings: it never
// polls and Pair shows a fixed code.
func Preview(st State) *Bot {
	b := &Bot{st: st, preview: true, subs: map[int]func(){}}
	b.ctx, b.cancel = context.WithCancel(context.Background())
	return b
}

// Start runs the supervisor (and makes the bot Active). Safe to call once.
func (b *Bot) Start() {
	if b.preview || b.e == nil || !b.running.CompareAndSwap(false, true) {
		return
	}
	b.start = time.Now()
	active.Store(b)
	b.e.SetTelegramHooks(studio.TelegramHooks{FinalMarkup: b.finalMarkup, ErrorMarkup: b.errorMarkup, JobEnded: b.live.ended, FindChat: b.findChat})
	b.cfg.Subscribe(func(config.Config) { b.kick() })
	b.unsub = b.e.Subscribe(func(string) { b.live.touch() })
	b.wg.Add(3)
	go b.supervise()
	go b.live.loop()
	go b.denyLoop()
}

// Stop ends polling and every worker, waiting up to timeout for messages in
// flight (live messages of jobs the shutdown stopped).
func (b *Bot) Stop(timeout time.Duration) {
	if b.preview || !b.running.CompareAndSwap(true, false) {
		return
	}
	active.CompareAndSwap(b, nil)
	if b.unsub != nil {
		b.unsub()
	}
	b.e.SetTelegramHooks(studio.TelegramHooks{})
	b.live.flush(timeout)
	b.cancel()
	done := make(chan struct{})
	go func() { b.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("[tgbot] stop: still busy after %s", timeout)
	}
}

// State returns the bot's state for Settings.
func (b *Bot) State() State {
	if b == nil {
		return State{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.st
	st.Attempts = append([]Attempt(nil), b.st.Attempts...)
	if p := b.pair; p != nil && b.now().Before(p.until) {
		st.Code, st.CodeUntil = p.code, p.until
	} else if !b.preview {
		st.Code, st.CodeUntil = "", time.Time{}
	}
	return st
}

// Subscribe calls fn (on any goroutine) after the state changed.
func (b *Bot) Subscribe(fn func()) (cancel func()) {
	b.mu.Lock()
	id := b.nextSub
	b.nextSub++
	b.subs[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

// Reconnect tries again after a conflict, a refused token or a webhook.
func (b *Bot) Reconnect() {
	if b.preview {
		return
	}
	b.mu.Lock()
	b.halted = ""
	b.mu.Unlock()
	b.kick()
}

// ForgetAttempt removes someone from the refused list (allowed or dismissed).
func (b *Bot) ForgetAttempt(id int64) {
	b.mu.Lock()
	out := b.st.Attempts[:0:0]
	for _, a := range b.st.Attempts {
		if a.ID != id {
			out = append(out, a)
		}
	}
	b.st.Attempts = out
	b.mu.Unlock()
	b.changed()
}

// setStatus records the poller's state.
func (b *Bot) setStatus(s Status, note string) {
	b.mu.Lock()
	same := b.st.Status == s && b.st.Note == note
	b.st.Status, b.st.Note = s, note
	b.mu.Unlock()
	if !same {
		b.changed()
	}
}

func (b *Bot) changed() {
	b.mu.Lock()
	fns := make([]func(), 0, len(b.subs))
	for _, f := range b.subs {
		fns = append(fns, f)
	}
	b.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

func (b *Bot) kick() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *Bot) now() time.Time { return time.Now() }

// tg is the Telegram section of the settings.
func (b *Bot) tg() config.Telegram { return b.cfg.Get().Telegram }

// client is a Bot API client for the current token and server.
func (b *Bot) client() *notify.Client {
	tg := b.tg()
	return notify.New(tg.BotToken, b.baseURL(tg))
}

func (b *Bot) baseURL(tg config.Telegram) string {
	if b.opt.BaseURL != "" {
		return b.opt.BaseURL
	}
	return tg.APIServer
}

// uploadLimit is the biggest file the bot may send.
func (b *Bot) uploadLimit(c *notify.Client) int64 {
	if b.opt.UploadLimit > 0 {
		return b.opt.UploadLimit
	}
	return c.UploadLimit()
}

// ---- the supervisor ----------------------------------------------------------------------

// desired says whether to poll and with which token|server.
func (b *Bot) desired() (bool, string) {
	tg := b.tg()
	key := tg.BotToken + "|" + b.baseURL(tg)
	if tg.BotToken == "" {
		return false, key
	}
	b.mu.Lock()
	pairing := b.pair != nil && b.now().Before(b.pair.until)
	b.mu.Unlock()
	return tg.RemoteOn() || pairing, key
}

type pollRun struct {
	key    string
	cancel context.CancelFunc
	done   chan bool // true = a fatal stop (bad token, webhook, conflicts)
}

func (b *Bot) supervise() {
	defer b.wg.Done()
	var cur *pollRun
	stop := func() {
		if cur != nil {
			cur.cancel()
			<-cur.done
			cur = nil
		}
	}
	tick := time.NewTicker(15 * time.Second) // pairing codes expire on their own
	defer tick.Stop()
	for {
		want, key := b.desired()
		b.mu.Lock()
		if b.halted != "" && b.halted != key {
			b.halted = "" // the token or server changed: try again
		}
		halted := b.halted == key
		b.mu.Unlock()
		if cur != nil && (!want || cur.key != key) {
			stop()
		}
		if !want {
			b.listening.Store(false)
			if !halted {
				b.setStatus(StatusOff, "")
			}
		}
		if want && cur == nil && !halted {
			ctx, cancel := context.WithCancel(b.ctx)
			cur = &pollRun{key: key, cancel: cancel, done: make(chan bool, 1)}
			go func(r *pollRun) { r.done <- b.poll(ctx, r.key) }(cur)
		}
		var done chan bool
		if cur != nil {
			done = cur.done
		}
		select {
		case <-b.ctx.Done():
			stop()
			b.listening.Store(false)
			return
		case <-b.wake:
		case <-tick.C:
		case fatal := <-done: // a fatal stop marked itself halted (poll.go)
			b.listening.Store(false)
			cur.cancel()
			cur = nil
			if !fatal {
				select { // a short pause before the next run
				case <-b.ctx.Done():
				case <-time.After(b.opt.Retry):
				}
			}
		}
	}
}

// findChat answers «Знайти мій чат»: while the bot listens it knows the last
// chat itself (a getUpdates call would cut its long poll).
func (b *Bot) findChat(ctx context.Context) (string, string, error) {
	if !b.listening.Load() {
		ch, err := b.client().FindChat(ctx)
		return ch.ID, ch.Title, err
	}
	b.mu.Lock()
	ch := b.lastChat
	b.mu.Unlock()
	if ch.ID == "" {
		return "", "", notify.ErrNoChat
	}
	return ch.ID, ch.Title, nil
}

// clipName shortens a project title for buttons.
func clipName(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
