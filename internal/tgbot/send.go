package tgbot

// Sending within Telegram's limits (core.telegram.org/bots/faq): in one chat
// about one message a second (short bursts are tolerated), in a group at
// most 20 a minute. Every send and edit takes a slot of its chat; live
// progress edits only take a free one (try) and skip otherwise. A 429 blocks
// the chat for retry_after and the call is repeated (at most twice).

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/notify"
)

const (
	burst       = 3               // private chats: messages that may go at once
	gapPrivate  = time.Second     // … then one a second
	groupWindow = time.Minute     // groups: at most groupMax in a minute
	groupMax    = 19              // (20 allowed; one spare for the final notice)
	gapGroup    = 3 * time.Second // and spread out
	maxRetries  = 2
)

type pacer struct {
	mu    sync.Mutex
	chats map[string]*chatPace
	// fast (tests) keeps only the 429 blocks.
	fast bool
}

type chatPace struct {
	tokens  float64
	at      time.Time   // last refill
	sent    []time.Time // groups: sends within the window
	blocked time.Time   // 429: nothing before this
	next    time.Time   // groups: the earliest next send
}

func newPacer() *pacer { return &pacer{chats: map[string]*chatPace{}} }

func isGroup(chat string) bool { return strings.HasPrefix(chat, "-") }

// reserve books the next slot of chat and returns how long to wait for it
// (when take is false nothing is booked unless it is free now).
func (p *pacer) reserve(chat string, take bool) (time.Duration, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	c := p.chats[chat]
	if c == nil {
		c = &chatPace{tokens: burst, at: now}
		p.chats[chat] = c
	}
	wait := time.Duration(0)
	if c.blocked.After(now) {
		wait = c.blocked.Sub(now)
	}
	if p.fast {
		return wait, take || wait <= 0
	}
	if isGroup(chat) {
		kept := c.sent[:0]
		for _, t := range c.sent {
			if now.Sub(t) < groupWindow {
				kept = append(kept, t)
			}
		}
		c.sent = kept
		at := now.Add(wait)
		if c.next.After(at) {
			at = c.next
		}
		if len(c.sent) >= groupMax {
			if free := c.sent[len(c.sent)-groupMax].Add(groupWindow); free.After(at) {
				at = free
			}
		}
		wait = at.Sub(now)
		if !take && wait > 0 {
			return wait, false
		}
		c.sent = append(c.sent, at)
		c.next = at.Add(gapGroup)
		return wait, true
	}
	// Private chats: a token bucket.
	c.tokens = min(burst, c.tokens+now.Sub(c.at).Seconds()/gapPrivate.Seconds())
	c.at = now
	if c.tokens < 1 {
		wait = max(wait, time.Duration((1-c.tokens)*float64(gapPrivate)))
	}
	if !take && wait > 0 {
		return wait, false
	}
	c.tokens--
	return wait, true
}

// wait blocks until chat may get a message (false when ctx ended).
func (p *pacer) wait(ctx context.Context, chat string) bool {
	d, _ := p.reserve(chat, true)
	if d <= 0 {
		return ctx.Err() == nil
	}
	return sleep(ctx, d)
}

// try takes a slot only when one is free right now.
func (p *pacer) try(chat string) bool {
	_, ok := p.reserve(chat, false)
	return ok
}

// block keeps chat quiet for d (Telegram's retry_after).
func (p *pacer) block(chat string, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.chats[chat]
	if c == nil {
		c = &chatPace{tokens: 0, at: time.Now()}
		p.chats[chat] = c
	}
	if until := time.Now().Add(d); until.After(c.blocked) {
		c.blocked = until
	}
}

// retry runs fn in chat's pace, again after a 429.
func (b *Bot) retry(ctx context.Context, chat string, fn func() error) error {
	var err error
	for i := 0; i <= maxRetries; i++ {
		if !b.pace.wait(ctx, chat) {
			return ctx.Err()
		}
		if err = fn(); err == nil {
			return nil
		}
		d := notify.RetryAfter(err)
		if d <= 0 || d > 2*time.Minute {
			return err
		}
		b.pace.block(chat, d)
	}
	return err
}

// send sends an HTML message in chat's pace.
func (b *Bot) send(ctx context.Context, chat, html string, opt *notify.SendOptions) (notify.Message, error) {
	var m notify.Message
	err := b.retry(ctx, chat, func() error {
		var err error
		m, err = b.client().Send(ctx, chat, html, opt)
		return err
	})
	b.logErr("send", err)
	return m, err
}

// edit replaces a message's text and keyboard in chat's pace.
func (b *Bot) edit(ctx context.Context, chat string, id int, html string, kb notify.InlineKeyboard) error {
	err := b.retry(ctx, chat, func() error { return b.client().Edit(ctx, chat, id, html, kb) })
	if !notify.IsGone(err) {
		b.logErr("edit", err)
	}
	return err
}

// show edits message id when there is one, else sends a new message (and
// sends anew when the old one cannot be edited any more). Returns the id.
func (b *Bot) show(ctx context.Context, chat string, id int, html string, kb notify.InlineKeyboard) int {
	if id > 0 {
		err := b.edit(ctx, chat, id, html, kb)
		if err == nil {
			return id
		}
		if !notify.IsGone(err) {
			return id
		}
	}
	var opt *notify.SendOptions
	if kb != nil {
		opt = &notify.SendOptions{Markup: kb}
	}
	m, err := b.send(ctx, chat, html, opt)
	if err != nil {
		return 0
	}
	return m.MessageID
}

// remove deletes a message (best effort).
func (b *Bot) remove(ctx context.Context, chat string, id int) error {
	if id <= 0 {
		return nil
	}
	err := b.retry(ctx, chat, func() error { return b.client().Delete(ctx, chat, id) })
	if err != nil && !notify.IsGone(err) {
		b.logErr("delete", err)
	}
	return err
}

// answer answers a button press (never paced: not a chat message).
func (b *Bot) answer(ctx context.Context, id, text string, alert bool) {
	if err := b.client().Answer(ctx, id, text, alert); err != nil && ctx.Err() == nil {
		// Old presses can no longer be answered — harmless.
		if te, ok := err.(*notify.Error); !ok || te.Code != 400 {
			b.logErr("answer", err)
		}
	}
}

func (b *Bot) logErr(what string, err error) {
	if err != nil && b.ctx.Err() == nil {
		log.Printf("[tgbot] %s: %s", what, errText(err, b.tg().BotToken))
	}
}
