package config

// The Telegram section beyond the notifications: the remote control through
// the same bot (package tgbot). Security model: only the chat in ChatID is
// listened to, and there only OwnerID (who paired with the one-time code from
// Settings) and the Allowed users; nobody is the owner until a pairing.

import (
	"net/url"
	"strings"
)

// TelegramUser is a person allowed to control the studio in the owner's group.
type TelegramUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"` // "Петро (@petro)", for Settings
}

// Paired reports a finished pairing: an owner and the chat the bot listens to.
func (t Telegram) Paired() bool { return t.OwnerID != 0 && strings.TrimSpace(t.ChatID) != "" }

// RemoteOn reports that the bot takes commands: the switch is on, a token is
// set and the owner paired.
func (t Telegram) RemoteOn() bool { return t.Remote && t.BotToken != "" && t.Paired() }

// Controls reports whether user id may control the studio (the owner or an
// allowed user). The chat is checked by the caller.
func (t Telegram) Controls(id int64) bool {
	if id == 0 {
		return false
	}
	if id == t.OwnerID {
		return true
	}
	for _, u := range t.Allowed {
		if u.ID == id {
			return true
		}
	}
	return false
}

func (t *Telegram) normalize() {
	t.BotToken = strings.TrimSpace(t.BotToken)
	t.ChatID = strings.TrimSpace(t.ChatID)
	t.OwnerName = strings.TrimSpace(t.OwnerName)
	if t.OwnerID < 0 { // user ids are positive (negative ids are groups)
		t.OwnerID = 0
	}
	if t.OwnerID == 0 {
		t.OwnerName = ""
	}
	seen := map[int64]bool{t.OwnerID: true, 0: true}
	allowed := t.Allowed[:0:0]
	for _, u := range t.Allowed {
		if u.ID <= 0 || seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		u.Name = strings.TrimSpace(u.Name)
		allowed = append(allowed, u)
	}
	if len(allowed) == 0 {
		allowed = nil
	}
	t.Allowed = allowed
	t.APIServer = normalizeAPIServer(t.APIServer)
}

// normalizeAPIServer keeps an http(s) base URL without a trailing slash ("" =
// api.telegram.org); anything else is dropped.
func normalizeAPIServer(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	if strings.EqualFold(u.Host, "api.telegram.org") {
		return "" // that is the default
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/")
}

// clone copies the slices (Store.Get hands out independent copies).
func (t Telegram) clone() Telegram {
	t.Allowed = append([]TelegramUser(nil), t.Allowed...)
	return t
}
