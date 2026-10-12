package settings

// «Telegram-бот»: the user's own bot sends notifications (token, chat, test,
// events) and — when «Керування через бота» is on and paired — is a remote
// control of this studio (package tgbot): pairing with a one-time code, the
// people allowed in a group, live progress, an own Bot API server for videos
// over 50 MB, and what the bot can do.

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/tgbot"
)

type telegramCard struct {
	enabled         switchRow
	token           keyInput
	chat            field
	find            gui.Clickable
	finding         bool
	test            gui.Clickable
	check           check
	onDone, onError switchRow
	onFinal         switchRow
	saved           savedNote
	botFather       gui.Clickable

	// The remote control.
	bot     *tgbot.Bot // nil = tgbot.Active() (renders pin a tgbot.Preview)
	subBot  *tgbot.Bot // the bot whose changes redraw the page
	remote  switchRow
	live    switchRow
	pair    gui.Clickable
	openBot gui.Clickable
	copyCmd gui.Clickable
	cancel  gui.Clickable
	unpair  gui.Clickable
	retry   gui.Clickable
	addUser field
	add     gui.Clickable
	userErr string
	removes []gui.Clickable
	allows  []gui.Clickable
	server  field
}

// theBot is the remote control of this app (nil in headless renders).
func (s *telegramCard) theBot(c *core.Ctx) *tgbot.Bot {
	b := s.bot
	if b == nil {
		b = tgbot.Active()
	}
	if b != nil && b != s.subBot {
		s.subBot = b
		b.Subscribe(c.Invalidate)
	}
	return b
}

func (p *Page) telegramCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.telegram
	flash := func() { s.saved.want = true }
	cfg := p.now
	tg := cfg.Telegram
	bot := s.theBot(c)
	if s.find.Clicked(gtx) {
		p.findChat(c)
	}
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, c.E.TestTelegram)
	}
	if s.botFather.Clicked(gtx) {
		c.OpenPath("https://t.me/BotFather")
	}
	token := func(gtx C) D {
		return s.token.layout(gtx, th, "Токен бота", tg.BotToken, "123456789:AA…", func(v string) {
			p.cfg.set(c, func(cf *config.Config) { cf.Telegram.BotToken = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Токен бота збережено") })
		})
	}
	chat := labeled(th, "ID чату", "Куди бот пише. Найпростіше — «Підключити керування» або «Знайти мій чат».", func(gtx C) D {
		s.chat.bind(gtx, tg.ChatID, func(v string) {
			p.cfg.set(c, func(cf *config.Config) { cf.Telegram.ChatID = strings.TrimSpace(v) }, flash)
		})
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				return s.chat.Layout(gtx, th, gui.FieldStyle{Placeholder: "Напр. 123456789"})
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Icon: gui.IconSearch, Text: "Знайти мій чат", Busy: s.finding,
					Disabled: strings.TrimSpace(tg.BotToken) == ""}.Layout(gtx, th, &s.find)
			}),
		)
	})
	events := func(gtx C) D {
		return inset(gtx, th, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp16,
				func(gtx C) D {
					return s.enabled.layout(gtx, th, tg.Enabled, "Надсилати сповіщення", "Бот пише, коли відео готове або щось пішло не так.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.Telegram.Enabled = v }, flash)
					})
				},
				gui.Divider{}.Widget(th),
				gui.Text{Style: th.Label, Color: th.Pal.Text2}.Widget(th, "Коли писати"),
				func(gtx C) D {
					return s.onFinal.layout(gtx, th, tg.OnFinal, "Фінальне відео готове", "З обкладинкою, тривалістю й кнопками: надіслати відео сюди, субтитри, обкладинка.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.Telegram.OnFinal = v }, flash)
					})
				},
				func(gtx C) D {
					return s.onDone.layout(gtx, th, tg.OnDone, "Етап завершено", "План, кадри, відео, озвучка, автопілот.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.Telegram.OnDone = v }, flash)
					})
				},
				func(gtx C) D {
					return s.onError.layout(gtx, th, tg.OnError, "Сталася помилка", "Щоб одразу знати, що треба втрутитися.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.Telegram.OnError = v }, flash)
					})
				},
			)
		})
	}
	fields := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp20,
			func(gtx C) D { return fieldRow(gtx, 320, token, chat) },
			actions(func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconSend, Text: "Надіслати тест", Busy: s.check.busy}.Layout(gtx, th, &s.test)
			}),
			func(gtx C) D {
				return s.check.result(gtx, th, "Перевір токен від @BotFather, натисни «Підключити керування» або надішли боту /start і «Знайти мій чат».")
			},
			events,
			func(gtx C) D { return p.remoteBlock(gtx, c, bot) },
			func(gtx C) D { return p.serverField(gtx, c) },
		)
	}
	status := s.check.statusPill(th)
	if status == nil {
		switch {
		case tg.Enabled && tg.BotToken != "" && tg.ChatID != "":
			status = gui.StatusPill{State: gui.StateDone, Text: "Увімкнено", Small: true}.Widget(th)
		case tg.Enabled:
			status = gui.StatusPill{State: gui.StateStale, Text: "Не налаштовано", Small: true}.Widget(th)
		}
	}
	help := func(gtx C) D { return p.telegramHelp(gtx, c) }
	body := func(gtx C) D {
		if gtx.Constraints.Max.X >= gtx.Dp(760) {
			return gui.Columns{Gap: gui.Sp24}.Layout(gtx,
				gui.Col{Span: 3, W: fields},
				gui.Col{Span: 2, W: help},
			)
		}
		if tg.BotToken == "" {
			return gui.VStack(gtx, gui.Sp20, help, fields)
		}
		return gui.VStack(gtx, gui.Sp20, fields, help)
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconBot, Title: "Telegram-бот", Big: true,
		Text:  "Твій власний бот пише, коли відео готове, і — якщо ввімкнеш керування — слухається команд: новий проєкт, автопілот, готове відео прямо в чат.",
		Trail: trails(s.saved.widget(th), status),
	}, body)
}

// remoteBlock is the «Керування через бота» panel.
func (p *Page) remoteBlock(gtx C, c *core.Ctx, bot *tgbot.Bot) D {
	th := c.Th
	s := &p.telegram
	tg := p.now.Telegram
	flash := func() { s.saved.want = true }
	var st tgbot.State
	if bot != nil {
		st = bot.State()
	}
	waiting := st.Code != "" && time.Until(st.CodeUntil) > 0
	paired := tg.Paired()

	if s.pair.Clicked(gtx) {
		p.startPairing(c, bot)
	}
	if s.cancel.Clicked(gtx) && bot != nil {
		bot.CancelPairing()
	}
	if s.openBot.Clicked(gtx) && st.Username != "" && st.Code != "" {
		c.OpenPath("https://t.me/" + st.Username + "?start=" + st.Code)
	}
	if s.copyCmd.Clicked(gtx) && st.Code != "" {
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader("/start " + st.Code))})
		c.Toast(gui.ToneSuccess, "Скопійовано — встав це повідомлення своєму боту")
	}
	if s.unpair.Clicked(gtx) {
		c.Confirm(core.Confirm{
			Title: "Відключити керування?", Text: "Бот перестане слухатися команд, доки не підключиш його знову. Сповіщення працюватимуть.",
			Icon: gui.IconSmartphone, OK: "Відключити", Cancel: "Залишити",
			OnOK: func() {
				p.cfg.set(c, func(cf *config.Config) { cf.Telegram.OwnerID, cf.Telegram.Remote = 0, false }, flash)
			},
		})
	}
	if s.retry.Clicked(gtx) && bot != nil {
		bot.Reconnect()
	}
	if waiting {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second)}) // the countdown
	}

	// The head: what the bot does now.
	var pill layout.Widget
	line := "Вимкнено. Увімкни, щоб керувати студією з телефона."
	switch {
	case waiting:
		pill = gui.StatusPill{State: gui.StateRunning, Percent: -1, Text: "Чекаю код", Small: true}.Widget(th)
		line = "Надішли боту код нижче — і він слухатиметься лише тебе."
	case !paired:
		line = "Ще не підключено. Бот слухатиметься лише того, хто надішле йому одноразовий код."
	case !tg.Remote:
		line = "Підключено до: " + tg.OwnerName + ". Керування вимкнене."
	default:
		line = "Слухається: " + tg.OwnerName
		if len(tg.Allowed) > 0 {
			line += fmt.Sprintf(" і ще %d", len(tg.Allowed))
		}
		if strings.HasPrefix(tg.ChatID, "-") {
			line += " · у групі"
		}
		switch st.Status {
		case tgbot.StatusOn:
			pill = gui.StatusPill{State: gui.StateDone, Text: "Працює", Small: true}.Widget(th)
		case tgbot.StatusStarting:
			pill = gui.StatusPill{State: gui.StateRunning, Percent: -1, Text: "Підключаюсь", Small: true}.Widget(th)
		case tgbot.StatusOffline:
			pill = gui.StatusPill{State: gui.StateStale, Text: "Немає зв'язку", Small: true}.Widget(th)
		case tgbot.StatusConflict, tgbot.StatusWebhook, tgbot.StatusBadToken, tgbot.StatusBusyHere:
			pill = gui.StatusPill{State: gui.StateError, Text: "Не працює", Small: true}.Widget(th)
		}
	}
	problem := ""
	switch st.Status {
	case tgbot.StatusConflict, tgbot.StatusWebhook, tgbot.StatusBadToken, tgbot.StatusBusyHere, tgbot.StatusOffline:
		problem = st.Note
	}
	if problem == "" && !waiting && st.PairNote != "" {
		problem = st.PairNote // a code cancelled after wrong tries
	}

	ws := []layout.Widget{
		func(gtx C) D {
			if pill == nil || gtx.Constraints.Max.X >= gtx.Dp(420) {
				return insetHead(gtx, th, gui.IconSmartphone, "Керування через бота", line, pill)
			}
			// Phones: the pill goes under the head, the title keeps its room.
			return gui.VStack(gtx, gui.Sp8,
				func(gtx C) D {
					return insetHead(gtx, th, gui.IconSmartphone, "Керування через бота", line, nil)
				},
				func(gtx C) D { gtx.Constraints.Min.X = 0; return pill(gtx) },
			)
		},
		func(gtx C) D {
			return s.remote.layout(gtx, th, tg.Remote, "Керувати студією з Telegram",
				"Новий проєкт із тексту чи голосового, автопілот, черга, готове відео в чат.", func(v bool) {
					p.cfg.set(c, func(cf *config.Config) { cf.Telegram.Remote = v }, flash)
					if v && !paired {
						p.startPairing(c, bot)
					}
				})
		},
	}
	if waiting {
		ws = append(ws, func(gtx C) D { return p.pairingPanel(gtx, c, st) })
	} else {
		ws = append(ws, func(gtx C) D {
			var list []layout.Widget
			label := "Підключити керування"
			kind := gui.Primary
			if paired {
				label, kind = "Підключити заново", gui.Secondary
			}
			list = append(list, func(gtx C) D {
				return gui.Button{Kind: kind, Icon: gui.IconShieldCheck, Text: label, Disabled: strings.TrimSpace(tg.BotToken) == ""}.Layout(gtx, th, &s.pair)
			})
			if paired {
				list = append(list, func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Icon: gui.IconLogOut, Text: "Відключити"}.Layout(gtx, th, &s.unpair)
				})
			}
			return actions(list...)(gtx)
		})
	}
	if problem != "" {
		ws = append(ws, func(gtx C) D { return resultPanel(gtx, th, false, false, problem, "", "") }) // «Не вдалося» + why
		if st.Status == tgbot.StatusConflict || st.Status == tgbot.StatusWebhook || st.Status == tgbot.StatusBadToken || st.Status == tgbot.StatusBusyHere {
			ws = append(ws, func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Спробувати знову"}.Layout(gtx, th, &s.retry)
			})
		}
	}
	if paired {
		ws = append(ws,
			func(gtx C) D {
				return s.live.layout(gtx, th, tg.Live, "Живий прогрес у чаті", "Одне повідомлення на проєкт, що оновлюється, поки йде генерація.", func(v bool) {
					p.cfg.set(c, func(cf *config.Config) { cf.Telegram.Live = v }, flash)
				})
			},
			func(gtx C) D { return p.allowedUsers(gtx, c, st) },
		)
	}
	return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, ws...) })
}

// pairingPanel shows the one-time code and how to send it.
func (p *Page) pairingPanel(gtx C, c *core.Ctx, st tgbot.State) D {
	th := c.Th
	s := &p.telegram
	left := time.Until(st.CodeUntil).Round(time.Second)
	code := st.Code
	if len(code) == 6 {
		code = code[:3] + " " + code[3:]
	}
	box := func(gtx C) D {
		return gui.Card{Radius: gui.RadiusControl, Padding: gui.Sp16, Flat: true}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return gui.VStack(gtx, gui.Sp4,
				gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Одноразовий код"),
				gui.Text{Style: th.Stat.Sized(34)}.Widget(th, code),
				gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
					fmt.Sprintf("Надішли боту: /start %s · діє ще %d:%02d", st.Code, int(left.Minutes()), int(left.Seconds())%60)),
			)
		})
	}
	open := "Відкрити бота"
	if st.Username != "" {
		open = "Відкрити @" + st.Username
	}
	return gui.VStack(gtx, gui.Sp12,
		box,
		actions(
			func(gtx C) D {
				return gui.Button{Kind: gui.Primary, Icon: gui.IconExternalLink, Text: open, Disabled: st.Username == "", Tip: "Відкриє бота в Telegram — натисни там «Почати»"}.Layout(gtx, th, &s.openBot)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconCopy, Text: "Скопіювати команду"}.Layout(gtx, th, &s.copyCmd)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Text: "Скасувати"}.Layout(gtx, th, &s.cancel)
			},
		),
		gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th,
			"Для групи: додай бота в групу й надішли там /start і код — керувати зможеш ти й ті, кого додаси нижче."),
	)
}

// allowedUsers lists who else may control the studio in a group.
func (p *Page) allowedUsers(gtx C, c *core.Ctx, st tgbot.State) D {
	th := c.Th
	s := &p.telegram
	tg := p.now.Telegram
	flash := func() { s.saved.want = true }
	group := strings.HasPrefix(tg.ChatID, "-")
	if !group && len(tg.Allowed) == 0 {
		return gui.Text{Style: th.Small, Color: th.Pal.Text3}.Layout(gtx, th,
			"Хочеш керувати разом із командою? Додай бота в групу й підключи керування там — потім тут можна дозволити інших.")
	}
	for len(s.removes) < len(tg.Allowed) {
		s.removes = append(s.removes, gui.Clickable{})
	}
	for i := range tg.Allowed {
		if s.removes[i].Clicked(gtx) {
			id := tg.Allowed[i].ID
			p.cfg.set(c, func(cf *config.Config) {
				var keep []config.TelegramUser
				for _, u := range cf.Telegram.Allowed {
					if u.ID != id {
						keep = append(keep, u)
					}
				}
				cf.Telegram.Allowed = keep
			}, flash)
		}
	}
	for len(s.allows) < len(st.Attempts) {
		s.allows = append(s.allows, gui.Clickable{})
	}
	for i, a := range st.Attempts {
		if s.allows[i].Clicked(gtx) {
			p.allowUser(c, a.ID, a.Name)
		}
	}
	if s.add.Clicked(gtx) {
		s.addUser.flush()
	}
	s.addUser.enterOnly = true
	s.addUser.bind(gtx, "", func(v string) { p.addAllowed(c, v) })

	ws := []layout.Widget{gui.Text{Style: th.Label}.Widget(th, "Хто ще може керувати в групі")}
	if len(tg.Allowed) > 0 {
		ws = append(ws, func(gtx C) D {
			var chips []layout.Widget
			for i, u := range tg.Allowed {
				name := u.Name
				if name == "" {
					name = "id " + strconv.FormatInt(u.ID, 10)
				}
				chips = append(chips, func(gtx C) D {
					return gui.Chip{Text: name, Icon: gui.IconUser, Small: true, Remove: &s.removes[i]}.Layout(gtx, th, nil)
				})
			}
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, chips...)
		})
	}
	ws = append(ws, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				return s.addUser.Layout(gtx, th, gui.FieldStyle{Placeholder: "ID користувача", Error: s.userErr})
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Icon: gui.IconUserPlus, Text: "Додати"}.Layout(gtx, th, &s.add)
			}),
		)
	})
	if len(st.Attempts) > 0 {
		ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Писали в групі без доступу:"))
		for i, a := range st.Attempts {
			ws = append(ws, func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, gui.Text{Style: th.Small, MaxLines: 1}.Widget(th, a.Name+" · id "+strconv.FormatInt(a.ID, 10))),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconUserPlus, Text: "Дозволити"}.Layout(gtx, th, &s.allows[i])
					}),
				)
			})
		}
	}
	return gui.VStack(gtx, gui.Sp8, ws...)
}

// addAllowed adds a typed user id ("123456789", "id 123").
func (p *Page) addAllowed(c *core.Ctx, v string) {
	s := &p.telegram
	t := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "id"))
	if t == "" {
		s.userErr = ""
		return
	}
	id, err := strconv.ParseInt(t, 10, 64)
	if err != nil || id <= 0 {
		s.userErr = "Потрібен числовий ID (його покаже, напр., @userinfobot), не @ім'я"
		return
	}
	s.userErr = ""
	p.allowUser(c, id, "")
}

func (p *Page) allowUser(c *core.Ctx, id int64, name string) {
	s := &p.telegram
	if b := s.theBot(c); b != nil {
		b.ForgetAttempt(id)
	}
	p.cfg.set(c, func(cf *config.Config) {
		for _, u := range cf.Telegram.Allowed {
			if u.ID == id {
				return
			}
		}
		cf.Telegram.Allowed = append(cf.Telegram.Allowed, config.TelegramUser{ID: id, Name: name})
	}, func() {
		s.saved.want = true
		c.Toast(gui.ToneSuccess, "Додано — тепер ця людина може керувати студією в групі")
	})
}

// serverField is the optional own Bot API server.
func (p *Page) serverField(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.telegram
	tg := p.now.Telegram
	s.server.bind(gtx, tg.APIServer, func(v string) {
		v = strings.TrimSpace(v)
		p.cfg.set(c, func(cf *config.Config) { cf.Telegram.APIServer = v }, func() {
			s.saved.want = true
			if v != "" && p.cfg.get(c).Telegram.APIServer == "" {
				c.Toast(gui.ToneError, "Адреса сервера має бути на кшталт http://127.0.0.1:8081")
			}
		})
	})
	return s.server.Layout(gtx, th, gui.FieldStyle{
		Label:       "Свій Bot API сервер (необов'язково)",
		Placeholder: "Напр. http://127.0.0.1:8081",
		Hint:        "Telegram приймає від ботів відео до 50 МБ. Свій сервер telegram-bot-api знімає обмеження до 2000 МБ. Порожньо — api.telegram.org.",
	})
}

// startPairing asks the bot for a one-time code (after the token is saved).
func (p *Page) startPairing(c *core.Ctx, bot *tgbot.Bot) {
	if strings.TrimSpace(p.now.Telegram.BotToken) == "" {
		c.Toast(gui.ToneInfo, "Спершу встав токен бота від @BotFather")
		return
	}
	if bot == nil {
		c.Toast(gui.ToneInfo, "Керування запускається разом зі студією — у демо-режимі його немає.")
		return
	}
	p.flushFields()
	p.cfg.barrier(c, func() {
		bot.Pair()
		c.Invalidate()
	})
}

// telegramHelp is the right column: how to connect, what the bot can do.
func (p *Page) telegramHelp(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.telegram
	return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		bullet := func(text string) layout.Widget {
			return func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 3, Right: gui.Sp8}.Layout(gtx, gui.IconCheck.Widget(14, th.Pal.SuccessInk))
					}),
					layout.Flexed(1, gui.Text{Style: th.Small.Sized(14).Lined(1.4)}.Widget(th, text)),
				)
			}
		}
		return gui.VStack(gtx, gui.Sp12,
			gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Як підключити за хвилину"),
			func(gtx C) D {
				return numberedSteps(gtx, th,
					"У Telegram відкрий @BotFather, надішли /newbot і скопіюй токен у поле «Токен бота».",
					"Натисни «Підключити керування» й надішли боту код — або просто /start і «Знайти мій чат», якщо потрібні лише сповіщення.",
					"Натисни «Надіслати тест».",
				)
			},
			func(gtx C) D {
				return gui.Link{Text: "Відкрити @BotFather", Trail: gui.IconExternalLink}.Layout(gtx, th, &s.botFather)
			},
			gui.Divider{}.Widget(th),
			gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Що вміє бот"),
			bullet("Новий проєкт з тексту, файлу .txt чи голосового: профіль, режим, тривалість, мова, звук, формат."),
			bullet("«За прикладом»: посилання на YouTube і побажання."),
			bullet("Проєкти й черга: прогрес, зупинити, повторити, кадри, озвучка, монтаж."),
			bullet("Готове відео, обкладинка й субтитри — прямо в чат."),
			bullet("Стан сервісів і ключів, місце на диску."),
			func(gtx C) D {
				return note(gtx, th, gui.IconShieldCheck, "Бот слухається лише тебе й тих, кого ти додаси. Решта бачать «Цей бот приватний». Видалення й «Зупинити все» він перепитує.", nil)
			},
		)
	})
}

// findChat asks the bot for the chat that wrote to it last and saves its id.
func (p *Page) findChat(c *core.Ctx) {
	s := &p.telegram
	if s.finding {
		return
	}
	p.flushFields()
	if c.Offline {
		c.Toast(gui.ToneInfo, "Пошук чату недоступний у демо-режимі.")
		return
	}
	s.finding = true
	p.cfg.barrier(c, func() {
		c.Background(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			id, title, err := c.E.FindTelegramChat(ctx)
			c.Post(func() {
				s.finding = false
				if err != nil {
					c.Error(err)
					return
				}
				s.chat.SetText(id)
				p.cfg.set(c, func(cf *config.Config) { cf.Telegram.ChatID = id }, func() {
					s.saved.want = true
					name := strings.TrimSpace(title)
					if name == "" {
						name = id
					}
					c.Toast(gui.ToneSuccess, "Знайдено чат «"+name+"» — тепер натисни «Надіслати тест»")
				})
			})
		})
	})
}
