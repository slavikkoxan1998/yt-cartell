package tgbot

// The long-polling loop: getMe, the command menu, then getUpdates with the
// saved offset until the run is cancelled.
//
//	409 Conflict "terminated by other getUpdates request"  another program
//	    reads the bot: StatusConflict, a pause (30 s, 60 s), after the third
//	    one in a row the bot stops until Settings «Спробувати знову» — two
//	    pollers would steal each other's updates
//	409 "… webhook is active"  StatusWebhook, stops at once
//	401/404  StatusBadToken, stops until the token changes
//	429      waits retry_after
//	network / 5xx  StatusOffline, retried after 1, 2, 4 … 60 s

import (
	"context"
	"log"
	"time"

	"ytcartell/internal/notify"
)

// allowedUpdates are the update kinds the bot asks for.
var allowedUpdates = []string{"message", "callback_query"}

const maxConflicts = 3

// poll runs until ctx ends (false) or a fatal error (true).
func (b *Bot) poll(ctx context.Context, key string) (fatal bool) {
	lock := newLock(b.opt.StateDir)
	for !lock.acquire() {
		b.setStatus(StatusBusyHere, "Цього бота вже слухає інша копія студії на цьому комп'ютері. Закрий її — і керування перейде сюди.")
		if !sleep(ctx, 20*time.Second) {
			return false
		}
	}
	defer lock.release()

	b.setStatus(StatusStarting, "")
	client := b.client()
	backoff := b.opt.Retry
	var me notify.User
	for {
		var err error
		me, err = client.Me(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return false
		}
		if notify.Unauthorized(err) {
			b.halt(key, StatusBadToken, "Telegram не прийняв токен бота — перевір його в @BotFather і встав ще раз.")
			return true
		}
		b.setStatus(StatusOffline, "Немає зв'язку з Telegram — пробую ще раз. "+errText(err, b.tg().BotToken))
		if !sleep(ctx, backoff) {
			return false
		}
		backoff = min(backoff*2, time.Minute)
	}
	b.mu.Lock()
	b.st.Username = me.Username
	b.botID = me.ID
	b.mu.Unlock()
	b.changed()
	b.setCommands(ctx, client)

	offset := loadOffset(b.opt.StateDir, me.ID)
	conflicts := 0
	backoff = b.opt.Retry
	for {
		ups, err := client.GetUpdates(ctx, offset, b.opt.PollTimeout, allowedUpdates)
		if ctx.Err() != nil {
			return false
		}
		if err != nil {
			b.listening.Store(false)
			if conflict, webhook := notify.Conflict(err); conflict {
				if webhook {
					b.halt(key, StatusWebhook, "У бота ввімкнено вебхук — його повідомлення забирає інша програма. Вимкни вебхук (або створи окремого бота для студії) і натисни «Спробувати знову».")
					return true
				}
				conflicts++
				if conflicts >= maxConflicts {
					b.halt(key, StatusConflict, "Цього бота читає інша програма чи копія студії на іншому комп'ютері. Закрий її або візьми окремого бота, потім натисни «Спробувати знову».")
					return true
				}
				b.setStatus(StatusConflict, "Цього бота щойно читала інша програма — чекаю й пробую ще раз.")
				if !sleep(ctx, time.Duration(conflicts)*30*b.opt.Retry) { // 30 s, 60 s
					return false
				}
				continue
			}
			if notify.Unauthorized(err) {
				b.halt(key, StatusBadToken, "Telegram не прийняв токен бота — перевір його в @BotFather і встав ще раз.")
				return true
			}
			if d := notify.RetryAfter(err); d > 0 {
				if !sleep(ctx, d) {
					return false
				}
				continue
			}
			b.setStatus(StatusOffline, "Немає зв'язку з Telegram — пробую ще раз. "+errText(err, b.tg().BotToken))
			if !sleep(ctx, backoff) {
				return false
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		conflicts = 0
		backoff = b.opt.Retry
		b.listening.Store(true)
		b.setStatus(StatusOn, "")
		if len(ups) == 0 {
			continue
		}
		for _, u := range ups {
			offset = max(offset, u.UpdateID+1)
		}
		// Saved before handling: a crash never repeats a command.
		if err := saveOffset(b.opt.StateDir, me.ID, offset); err != nil {
			log.Printf("[tgbot] saving the update offset: %v", err)
		}
		for _, u := range ups {
			b.dispatch(u)
		}
	}
}

// setCommands puts the command menu into the control chat only (strangers
// see none).
func (b *Bot) setCommands(ctx context.Context, c *notify.Client) {
	tg := b.tg()
	if !tg.Paired() {
		return
	}
	cmds := []notify.Command{
		{Command: "menu", Description: "Головне меню"},
		{Command: "new", Description: "Новий проєкт"},
		{Command: "remake", Description: "Перезняти за прикладом"},
		{Command: "projects", Description: "Проєкти"},
		{Command: "queue", Description: "Черга"},
		{Command: "status", Description: "Стан студії"},
		{Command: "cancel", Description: "Скасувати поточну дію"},
		{Command: "help", Description: "Що я вмію"},
	}
	if err := c.SetCommands(ctx, cmds, tg.ChatID); err != nil && ctx.Err() == nil {
		log.Printf("[tgbot] commands: %s", errText(err, tg.BotToken))
	}
}

// sleep waits d unless ctx ends first (false then).
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// errText is an error for the user, never with the token.
func errText(err error, token string) string {
	if err == nil {
		return ""
	}
	return notify.Redact(err.Error(), token)
}

// halt stops polling with key until the settings change or Reconnect: the
// mark and the status change together, so a Reconnect after the status shows
// always wins.
func (b *Bot) halt(key string, s Status, note string) {
	b.mu.Lock()
	b.halted = key
	b.st.Status, b.st.Note = s, note
	b.mu.Unlock()
	b.changed()
}
