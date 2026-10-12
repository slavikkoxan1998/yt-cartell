package settings

// The «Telegram-бот» card: its states rendered headlessly (GUISHOTS=<dir>,
// like TestShots) and the allowed-users input. The bot is a tgbot.Preview —
// nothing polls, nothing reaches Telegram.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/tgbot"
)

const shotToken = "123456789:DEMO-token-not-real"

var telegramShots = []struct {
	name string
	cfg  func(t *config.Telegram)
	bot  tgbot.State
	pair bool
}{
	{name: "telegram-start", cfg: func(t *config.Telegram) {
		*t = config.Telegram{Enabled: true, BotToken: shotToken, ChatID: "123456789", OnDone: true, OnError: true, OnFinal: true, Live: true}
	}},
	{name: "telegram-pairing", pair: true, bot: tgbot.State{Status: tgbot.StatusOn, Username: "ytcartell_demo_bot"}, cfg: func(t *config.Telegram) {
		*t = config.Telegram{Enabled: true, BotToken: shotToken, OnDone: true, OnError: true, OnFinal: true, Live: true, Remote: true}
	}},
	{name: "telegram-group", bot: tgbot.State{Status: tgbot.StatusOn, Username: "ytcartell_demo_bot",
		Attempts: []tgbot.Attempt{{ID: 555001, Name: "Оля Коваль (@olia_k)", At: time.Now()}}},
		cfg: func(t *config.Telegram) {
			*t = config.Telegram{Enabled: true, BotToken: shotToken, ChatID: "-1001234567890", OnDone: true, OnError: true, OnFinal: true,
				Remote: true, OwnerID: 4242, OwnerName: "Іван Петренко (@ivan_p)", Live: true,
				Allowed: []config.TelegramUser{{ID: 777, Name: "Петро (@petro)"}, {ID: 778}}}
		}},
	{name: "telegram-conflict", bot: tgbot.State{Status: tgbot.StatusConflict, Username: "ytcartell_demo_bot",
		Note: "Цього бота читає інша програма чи копія студії на іншому комп'ютері. Закрий її або візьми окремого бота, потім натисни «Спробувати знову»."},
		cfg: func(t *config.Telegram) {
			*t = config.Telegram{Enabled: true, BotToken: shotToken, ChatID: "4242", OnFinal: true, OnError: true,
				Remote: true, OwnerID: 4242, OwnerName: "Іван Петренко (@ivan_p)", APIServer: "http://127.0.0.1:8081"}
		}},
}

func TestTelegramShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the Telegram card")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, st := range telegramShots {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range []shotSize{{1440, 2300}, {390, 3600}} { // tall: the whole card
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("settings-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if err := c.E.Config().Update(func(cf *config.Config) { st.cfg(&cf.Telegram) }); err != nil {
						t.Fatal(err)
					}
					c.Th.SetPaletteNow(dark)
					p := New()
					bot := tgbot.Preview(st.bot)
					if st.pair {
						bot.Pair()
					}
					p.telegram.bot = bot
					draw := shell(c, func(gtx C) D { return p.Layout(gtx, c, "telegram") })
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// TestTelegramAllowedUsers: ids are added once, names stay, @names are
// refused with a hint; a refused group member can be allowed.
func TestTelegramAllowedUsers(t *testing.T) {
	c, h := demo(t)
	p := New()
	p.telegram.bot = tgbot.Preview(tgbot.State{Attempts: []tgbot.Attempt{{ID: 555, Name: "Оля"}}})
	p.addAllowed(c, "@petro")
	if p.telegram.userErr == "" {
		t.Fatal("an @name was taken for an id")
	}
	p.addAllowed(c, " id 777 ")
	p.addAllowed(c, "777")
	p.allowUser(c, 555, "Оля")
	h.Flush()
	got := c.E.Config().Get().Telegram.Allowed
	if len(got) != 2 || got[0].ID != 777 || got[1] != (config.TelegramUser{ID: 555, Name: "Оля"}) || p.telegram.userErr != "" {
		t.Fatalf("allowed %+v (error %q)", got, p.telegram.userErr)
	}
	if len(p.telegram.bot.State().Attempts) != 0 {
		t.Fatal("the allowed person is still in the refused list")
	}
	// Pairing without a token asks for the token first; with one, the code shows.
	p.now = c.E.Config().Get()
	p.startPairing(c, p.telegram.bot)
	if p.telegram.bot.State().Code != "" {
		t.Fatal("a code without a token")
	}
	_ = c.E.Config().Update(func(cf *config.Config) { cf.Telegram.BotToken = shotToken })
	p.now = c.E.Config().Get()
	p.startPairing(c, p.telegram.bot)
	h.Flush()
	if code := p.telegram.bot.State().Code; len(code) != 6 {
		t.Fatalf("no code: %q", code)
	}
}
