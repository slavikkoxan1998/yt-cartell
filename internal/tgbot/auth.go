package tgbot

// Who may control the studio.
//
//   - Only the chat in Telegram.ChatID is listened to, and there only the
//     owner (Telegram.OwnerID) and the allowed users; bots never.
//   - Nobody is the owner until a pairing: Settings shows a one-time 6-digit
//     code (crypto/rand, valid 10 min, 5 wrong tries end it); the owner sends
//     "/start <code>" (or opens t.me/<bot>?start=<code>) and the chat and
//     the person are saved.
//   - Everyone else gets «Цей бот приватний» and nothing else (at most once a
//     minute per chat, only in private chats or for commands, a global cap):
//     no project names, no hint that a pairing exists.
//   - Destructive actions (delete a project, stop everything) need a second
//     press within two minutes (confirm tokens, router.go).

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
)

const (
	pairTTL      = 10 * time.Minute
	pairMaxTries = 5
	// denyGap: a refused chat hears «приватний» at most this often.
	denyGap = time.Minute
	// denyPerMinute caps the refusals sent in a minute over all chats.
	denyPerMinute = 20
	maxAttempts   = 5
)

const (
	textPrivate = "Цей бот приватний."
)

type pairing struct {
	code  string
	until time.Time
	tries int
}

// Pair starts (or restarts) a pairing: a new one-time code, valid 10
// minutes; the bot listens until it is used or expires.
func (b *Bot) Pair() State {
	if b.preview {
		b.mu.Lock()
		b.st.Code, b.st.CodeUntil = "482913", time.Now().Add(pairTTL)
		b.mu.Unlock()
		return b.State()
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		log.Printf("[tgbot] pairing code: %v", err)
		return b.State()
	}
	b.mu.Lock()
	b.pair = &pairing{code: fmt.Sprintf("%06d", n.Int64()), until: b.now().Add(pairTTL)}
	b.st.PairNote = ""
	b.mu.Unlock()
	b.changed()
	b.kick()
	return b.State()
}

// CancelPairing drops a waiting code.
func (b *Bot) CancelPairing() {
	b.mu.Lock()
	b.pair = nil
	if b.preview {
		b.st.Code, b.st.CodeUntil = "", time.Time{}
	}
	b.mu.Unlock()
	b.changed()
	b.kick()
}

// pairCode finds a pairing code in a message: "/start 123456",
// "/start@bot 123 456" or just the six digits.
func pairCode(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "/start") {
		t = strings.TrimPrefix(t, "/start")
		if strings.HasPrefix(t, "@") {
			if i := strings.IndexFunc(t, unicode.IsSpace); i >= 0 {
				t = t[i:]
			} else {
				return ""
			}
		}
	}
	var digits []rune
	for _, r := range t {
		switch {
		case r >= '0' && r <= '9':
			digits = append(digits, r)
		case r == ' ' || r == '-' || r == '_':
		default:
			return ""
		}
	}
	if len(digits) != 6 {
		return ""
	}
	return string(digits)
}

// tryPair handles a pairing message; true when it paired. A wrong code
// counts as a try (5 end the code) and the sender is refused like anyone.
func (b *Bot) tryPair(m *notify.Message) bool {
	code := pairCode(m.Text)
	if code == "" || m.From == nil || m.From.IsBot {
		return false
	}
	b.mu.Lock()
	p := b.pair
	if p == nil || !b.now().Before(p.until) {
		b.mu.Unlock()
		return false
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(p.code)) != 1 {
		p.tries++
		if p.tries >= pairMaxTries {
			b.pair = nil
			b.st.PairNote = "Код підключення скасовано: забагато невдалих спроб. Створи новий."
		}
		b.mu.Unlock()
		b.changed()
		b.kick()
		return false
	}
	b.pair = nil
	b.mu.Unlock()

	from := *m.From
	chat := m.Chat
	err := b.cfg.Update(func(c *config.Config) {
		t := &c.Telegram
		t.ChatID = notify.ChatID(chat.ID)
		t.OwnerID = from.ID
		t.OwnerName = from.Name()
		t.Remote = true
	})
	if err != nil {
		log.Printf("[tgbot] pairing: saving the settings: %v", err)
	}
	b.mu.Lock()
	b.st.PairNote = ""
	b.mu.Unlock()
	b.changed()
	b.kick()
	log.Printf("[tgbot] paired with user %d in chat %d", from.ID, chat.ID)
	return true
}

// allowed reports whether from may control the studio in chat.
func (b *Bot) allowed(chat notify.MsgChat, from notify.User) bool {
	tg := b.tg()
	if !tg.RemoteOn() || from.IsBot || from.ID == 0 {
		return false
	}
	if strconv.FormatInt(chat.ID, 10) != tg.ChatID {
		return false
	}
	return tg.Controls(from.ID)
}

// noteAttempt remembers someone refused in the control chat (a group member
// without access) so Settings can allow them.
func (b *Bot) noteAttempt(chat notify.MsgChat, from notify.User) {
	tg := b.tg()
	if from.IsBot || strconv.FormatInt(chat.ID, 10) != tg.ChatID || chat.Type == "private" {
		return
	}
	b.mu.Lock()
	list := []Attempt{{ID: from.ID, Name: from.Name(), At: b.now()}}
	for _, a := range b.st.Attempts {
		if a.ID != from.ID && len(list) < maxAttempts {
			list = append(list, a)
		}
	}
	b.st.Attempts = list
	b.mu.Unlock()
	b.changed()
}

// denial is a refusal to send: «Цей бот приватний» to a chat, or the answer
// to a stranger's button press.
type denial struct {
	chat    string
	queryID string
	text    string
}

// refuse queues a refusal (dropped when the queue is full: a flood).
func (b *Bot) refuse(d denial) {
	select {
	case b.deny <- d:
	default:
	}
}

// denyLoop sends the refusals: each chat at most once a minute, all of them
// at most 20 a minute; button presses are always answered (no message).
func (b *Bot) denyLoop() {
	defer b.wg.Done()
	last := map[string]time.Time{}
	var sent []time.Time
	for {
		select {
		case <-b.ctx.Done():
			return
		case d := <-b.deny:
			ctx, cancel := context.WithTimeout(b.ctx, notify.Timeout)
			c := b.client()
			if d.queryID != "" {
				_ = c.Answer(ctx, d.queryID, d.text, true)
				cancel()
				continue
			}
			now := b.now()
			recent := sent[:0]
			for _, t := range sent {
				if now.Sub(t) < time.Minute {
					recent = append(recent, t)
				}
			}
			sent = recent
			if now.Sub(last[d.chat]) < denyGap || len(sent) >= denyPerMinute {
				cancel()
				continue
			}
			last[d.chat] = now
			sent = append(sent, now)
			if len(last) > 1000 { // forget old chats
				for k, t := range last {
					if now.Sub(t) > denyGap {
						delete(last, k)
					}
				}
			}
			if _, err := c.Send(ctx, d.chat, notify.Escape(d.text), nil); err != nil {
				log.Printf("[tgbot] refusal: %s", notify.Redact(err.Error(), b.tg().BotToken))
			}
			cancel()
		}
	}
}
