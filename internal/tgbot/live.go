package tgbot

// Live progress: while a job runs, ONE message per project shows its stage
// and a progress bar, edited in place — at most every 3 s per project and
// only when the chat has a free slot (send.go: ≤ 1 message/s, ≤ 20/min in
// groups), so progress never crowds out answers. Jobs started from the bot
// get it at once (their launch message turns into it), jobs started on the
// computer after Options.LiveAfter. Settings «Живий прогрес» switches it off.
//
// The end comes from the engine (TelegramHooks.JobEnded): when a job-end
// notification went out (the cover with «Відео готове» and its buttons)
// the live message of a finished job is deleted; otherwise — and always for
// errors, which get «Повторити» — it is edited into the result (an autopilot
// that stopped before the montage: the scenes and the choices, gaps.go).

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

const (
	liveGap = 3 * time.Second // between edits of one project's message
	// liveOrphan: a message whose job stopped running without an end event
	// (the project was deleted, the data folder switched) is closed after this.
	liveOrphan = 30 * time.Second
)

type liveState struct {
	b       *Bot
	mu      sync.Mutex
	msgs    map[string]*liveMsg // project id → its live message
	watched map[string]watchReq // jobs started from the bot
	dirty   chan struct{}
	ends    chan endEvent
	busy    atomic.Int32
	closing atomic.Bool // the studio is closing: stopped jobs say so
}

type liveMsg struct {
	chat    string
	id      int
	text    string
	edited  time.Time
	started int64     // the job's StartedAt
	gone    time.Time // when the job was last seen not running
}

type watchReq struct {
	chat string
	id   int // the message to turn into the live one (0 = a new one)
	at   time.Time
}

type endEvent struct {
	pid      string
	job      studio.Job
	notified bool
}

func newLive(b *Bot) *liveState {
	return &liveState{b: b, msgs: map[string]*liveMsg{}, watched: map[string]watchReq{},
		dirty: make(chan struct{}, 1), ends: make(chan endEvent, 64)}
}

// touch: the engine changed something (Subscribe; never blocks).
func (l *liveState) touch() {
	select {
	case l.dirty <- struct{}{}:
	default:
	}
}

// watch makes the next run of pid live at once, in message id when > 0.
func (l *liveState) watch(pid, chat string, id int) {
	l.mu.Lock()
	l.watched[pid] = watchReq{chat: chat, id: id, at: time.Now()}
	l.mu.Unlock()
	l.touch()
}

// ended is the engine's JobEnded hook (never blocks).
func (l *liveState) ended(pid string, job studio.Job, notified bool) {
	select {
	case l.ends <- endEvent{pid: pid, job: job, notified: notified}:
	default:
	}
}

// Closing marks the studio as closing: jobs the shutdown stops say so.
func (b *Bot) Closing() { b.live.closing.Store(true) }

// flush handles the end events still queued (the shutdown's stopped jobs).
func (l *liveState) flush(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && (len(l.ends) > 0 || l.busy.Load() > 0) {
		time.Sleep(20 * time.Millisecond)
	}
}

func (l *liveState) loop() {
	b := l.b
	defer b.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case ev := <-l.ends:
			l.busy.Add(1)
			l.finish(ev)
			l.busy.Add(-1)
		case <-l.dirty:
			l.refresh()
		case <-tick.C:
			l.refresh()
		}
	}
}

// on: the remote control listens; auto: jobs started on the computer get
// live messages too («Живий прогрес»; the bot's own launches always do).
func (l *liveState) on() (on, auto bool) {
	tg := l.b.tg()
	on = tg.RemoteOn() && l.b.listening.Load()
	return on, on && tg.Live
}

// refresh creates and edits the live messages of the running jobs.
func (l *liveState) refresh() {
	b := l.b
	l.mu.Lock()
	idle := len(l.msgs) == 0 && len(l.watched) == 0
	l.mu.Unlock()
	on, auto := l.on()
	if !on {
		return
	}
	snap := b.e.Queue()
	if idle && len(snap.Running) == 0 {
		return
	}
	now := time.Now()
	chat := b.tg().ChatID
	running := map[string]bool{}
	for _, it := range snap.Running {
		if it.Regen != "" || it.Kind == "" {
			continue
		}
		running[it.ProjectID] = true
		l.mu.Lock()
		lm := l.msgs[it.ProjectID]
		w, watched := l.watched[it.ProjectID]
		if lm != nil && watched && w.id > 0 && w.id != lm.id { // the bot showed a fresh message for it
			lm.id, lm.chat, lm.text = w.id, w.chat, ""
			l.watched[it.ProjectID] = watchReq{chat: w.chat, at: w.at}
		}
		l.mu.Unlock()
		if lm == nil {
			if !watched && (!auto || now.Sub(time.UnixMilli(it.StartedAt)) < b.opt.LiveAfter) {
				continue
			}
			lm = &liveMsg{chat: chat, started: it.StartedAt}
			if watched {
				lm.chat, lm.id = w.chat, w.id
			}
			if lm.id == 0 {
				if !b.pace.try(lm.chat) {
					continue
				}
				ctx, cancel := context.WithTimeout(b.ctx, notify.Timeout)
				html, kb := liveView(it, now)
				m, err := b.client().Send(ctx, lm.chat, html, &notify.SendOptions{Markup: kb, Silent: true})
				cancel()
				if err != nil {
					b.logErr("live", err)
					continue
				}
				lm.id, lm.text, lm.edited = m.MessageID, html, now
			}
			l.mu.Lock()
			l.msgs[it.ProjectID] = lm
			l.mu.Unlock()
		}
		lm.gone = time.Time{}
		html, kb := liveView(it, now)
		if html == lm.text || now.Sub(lm.edited) < liveGap || !b.pace.try(lm.chat) {
			continue
		}
		ctx, cancel := context.WithTimeout(b.ctx, notify.Timeout)
		err := b.client().Edit(ctx, lm.chat, lm.id, html, kb)
		cancel()
		lm.edited = now
		switch {
		case err == nil:
			lm.text = html
		case notify.IsGone(err): // the user deleted it: stop following
			l.mu.Lock()
			delete(l.msgs, it.ProjectID)
			l.watched[it.ProjectID] = watchReq{chat: lm.chat}
			l.mu.Unlock()
		case notify.RetryAfter(err) > 0:
			b.pace.block(lm.chat, notify.RetryAfter(err))
		default:
			b.logErr("live edit", err)
		}
	}
	// Messages whose job is no longer running and no end came.
	l.mu.Lock()
	var orphans []string
	for pid, lm := range l.msgs {
		if running[pid] {
			continue
		}
		if lm.gone.IsZero() {
			lm.gone = now
		} else if now.Sub(lm.gone) > liveOrphan {
			orphans = append(orphans, pid)
		}
	}
	var early []string // launched from the bot and over before the loop saw them run
	for pid, w := range l.watched {
		switch {
		case running[pid] || l.msgs[pid] != nil:
		case w.id > 0 && now.Sub(w.at) > 2*time.Second:
			early = append(early, pid)
		case now.Sub(w.at) > 2*liveOrphan:
			delete(l.watched, pid)
		}
	}
	l.mu.Unlock()
	for _, pid := range early {
		if p, ok := b.e.Get(pid); ok && p.Job != nil && p.Job.Status != studio.JobRunning {
			l.finish(endEvent{pid: pid, job: *p.Job})
		} else if !ok {
			l.mu.Lock()
			delete(l.watched, pid)
			l.mu.Unlock()
		}
	}
	for _, pid := range orphans {
		job := studio.Job{Status: studio.JobCancelled, Message: "Зупинено"}
		if p, ok := b.e.Get(pid); ok && p.Job != nil {
			job = *p.Job
		}
		l.finish(endEvent{pid: pid, job: job})
	}
}

// liveView is the live message of a running job.
func liveView(it studio.QueueItem, now time.Time) (string, notify.InlineKeyboard) {
	what := it.Label
	if m := it.Message; m != "" && m != "Запуск…" {
		what = m
	}
	html := "⏳ " + title(it.ProjectTitle) + "\n" + esc(clipName(what, 200)) + "\n" + bar(it.Progress)
	if it.StartedAt > 0 {
		html += " · " + span(int(now.Sub(time.UnixMilli(it.StartedAt)).Seconds()))
	}
	k := key(it.ProjectID)
	return html, notify.InlineKeyboard{row(btn("⏹ Зупинити", "ps:"+k), btn("📂 Проєкт", "p:"+k))}
}

// finish closes the live message of a finished job.
func (l *liveState) finish(ev endEvent) {
	b := l.b
	l.mu.Lock()
	lm := l.msgs[ev.pid]
	if w, ok := l.watched[ev.pid]; lm == nil && ok && w.id > 0 {
		lm = &liveMsg{chat: w.chat, id: w.id} // a launch message the loop had not taken over yet
	}
	delete(l.msgs, ev.pid)
	delete(l.watched, ev.pid)
	l.mu.Unlock()
	if lm == nil || lm.id == 0 {
		// An autopilot stopped before the montage with no live message: the
		// notification carries the choices; without one the bot asks itself.
		if blockedJob(&ev.job) && !ev.notified {
			l.announceGaps(ev)
		}
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 2*notify.Timeout)
	defer cancel()
	if ev.notified && ev.job.Status == studio.JobDone {
		if err := b.remove(ctx, lm.chat, lm.id); err == nil {
			return
		}
	}
	html, kb := l.result(ev)
	b.edit(ctx, lm.chat, lm.id, html, kb)
}

// result is the live message turned into the job's outcome.
func (l *liveState) result(ev endEvent) (string, notify.InlineKeyboard) {
	b := l.b
	p, ok := b.e.Get(ev.pid)
	if !ok {
		return "🗑 Проєкт видалено.", nil
	}
	k := key(p.ID)
	label := esc(studio.JobLabel(ev.job.Kind))
	j := ev.job
	var html string
	kb := notify.InlineKeyboard{}
	switch j.Status {
	case studio.JobDone:
		html = "✅ " + title(p.Title) + " — " + label + ": " + esc(firstLine(j.Message, 200))
		if j.FinishedAt > j.StartedAt && j.StartedAt > 0 {
			html += " · " + span(int((j.FinishedAt-j.StartedAt)/1000))
		}
		if j.Failures > 0 {
			kb = append(kb, row(btn("🔁 Повторити невдалі", "pr:"+k)))
		}
		if videoReady(p) {
			kb = append(kb, row(btn("⬇️ Надіслати відео", "v:"+k)))
		}
	case studio.JobError:
		if blockedJob(&j) {
			// Stopped before the montage: every scene that is not ready with
			// its exact error, and the choices (gaps.go).
			if gaps := b.gapsOf(p); len(gaps) > 0 {
				return gapsView(p, gaps, true)
			}
		}
		reason := j.Error
		if reason == "" {
			reason = j.Message
		}
		html = "⚠️ " + title(p.Title) + " — " + label + ": " + esc(firstLine(reason, 300))
		kb = append(kb, row(btn("🔁 Повторити", "pr:"+k)))
	default:
		html = "⏹ " + title(p.Title) + " — " + label + ": зупинено"
		if l.closing.Load() {
			html += ", бо студію на комп'ютері закрито. Продовжиш кнопкою «Повторити», коли вона знову запуститься."
		}
		kb = append(kb, row(btn("🔁 Продовжити", "pr:"+k)))
	}
	kb = append(kb, row(btn("📂 Проєкт", "p:"+k)))
	return html, kb
}

// finalMarkup is the engine's FinalMarkup hook: the buttons under «Відео
// готове» (§ delivery).
func (b *Bot) finalMarkup(pid string) notify.InlineKeyboard {
	if !b.running.Load() || !b.tg().RemoteOn() {
		return nil
	}
	p, ok := b.e.Get(pid)
	if !ok {
		return nil
	}
	k := key(pid)
	kb := notify.InlineKeyboard{row(btn("⬇️ Надіслати відео сюди", "v:"+k))}
	var r []notify.Button
	if p.Montage.SRTPath != "" {
		r = append(r, btn("📄 Субтитри .srt", "s:"+k))
	}
	if b.coverPath(p) != "" {
		r = append(r, btn("🖼 Обкладинка в повній якості", "pc:"+k))
	}
	if len(r) == 2 {
		kb = append(kb, row(r[0]), row(r[1]))
	} else if len(r) == 1 {
		kb = append(kb, r)
	}
	return append(kb, row(btn("📂 Відкрити проєкт", "p:"+k)))
}
