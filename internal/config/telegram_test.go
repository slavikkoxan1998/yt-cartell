package config

import (
	"encoding/json"
	"testing"
)

func TestTelegramNormalize(t *testing.T) {
	c := Default()
	if !c.Telegram.Live || c.Telegram.Remote || c.Telegram.Paired() {
		t.Fatalf("defaults: %+v", c.Telegram)
	}
	c.Telegram = Telegram{BotToken: " 1:x ", ChatID: " 42 ", OwnerID: 42, OwnerName: " Іван ", Remote: true,
		Allowed:   []TelegramUser{{ID: 7, Name: " Петро "}, {ID: 7}, {ID: 42}, {ID: 0}, {ID: -5}, {ID: 9}},
		APIServer: " 127.0.0.1:8081/ "}
	c.Normalize()
	tg := c.Telegram
	if tg.BotToken != "1:x" || tg.ChatID != "42" || tg.OwnerName != "Іван" || len(tg.Allowed) != 2 ||
		tg.Allowed[0] != (TelegramUser{ID: 7, Name: "Петро"}) || tg.Allowed[1].ID != 9 || tg.APIServer != "http://127.0.0.1:8081" {
		t.Fatalf("normalized: %+v", tg)
	}
	if !tg.RemoteOn() || !tg.Controls(42) || !tg.Controls(9) || tg.Controls(8) || tg.Controls(0) {
		t.Fatalf("access: %+v", tg)
	}
	for in, want := range map[string]string{
		"https://api.telegram.org/": "", "ftp://x": "", "https://user:pw@host": "", "https://bot.example.com/api/": "https://bot.example.com/api",
	} {
		if got := normalizeAPIServer(in); got != want {
			t.Errorf("APIServer %q → %q, want %q", in, got, want)
		}
	}
	// Unpaired: no owner name, no remote.
	c.Telegram.OwnerID = 0
	c.Normalize()
	if c.Telegram.OwnerName != "" || c.Telegram.RemoteOn() {
		t.Fatalf("unpaired: %+v", c.Telegram)
	}
	// An old config.json without "live" keeps the live progress on.
	var old Config = Default()
	if err := json.Unmarshal([]byte(`{"telegram":{"enabled":true,"botToken":"1:x","chatId":"5"}}`), &old); err != nil || !old.Telegram.Live {
		t.Fatalf("old file: %v %+v", err, old.Telegram)
	}
}

func TestTelegramStoreCopies(t *testing.T) {
	s := NewStoreWith(Config{Telegram: Telegram{OwnerID: 1, ChatID: "1", Allowed: []TelegramUser{{ID: 2}, {ID: 3}}}})
	got := s.Get()
	got.Telegram.Allowed[0].ID = 99
	if s.Get().Telegram.Allowed[0].ID != 2 {
		t.Fatal("Get shares the allowed users with the store")
	}
	_ = s.Update(func(c *Config) { c.Telegram.Allowed = append(c.Telegram.Allowed[:1], TelegramUser{ID: 4}) })
	if a := s.Get().Telegram.Allowed; len(a) != 2 || a[1].ID != 4 || got.Telegram.Allowed[1].ID != 3 {
		t.Fatalf("update: %+v, the earlier copy: %+v", a, got.Telegram.Allowed)
	}
}
