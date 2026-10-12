package tgbot

// «📊 Статус»: the services of Settings checked in parallel (the same
// «Перевірити» checks — cheap reads, never a generation), what the voice-over
// keys report about their balance, free disk space of the data folder, the
// queue and the app version.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/notify"
	"ytcartell/internal/tts"
	"ytcartell/internal/version"
)

const statusTimeout = 40 * time.Second

type serviceCheck struct {
	name string
	fn   func(context.Context) (bool, string)
}

// status shows «Перевіряю…» in message id (or a new one) and edits in the
// results when every check answered.
func (b *Bot) status(ctx context.Context, chat string, id int) {
	msgID := b.show(ctx, chat, id, "📊 <b>Стан студії</b>\nПеревіряю сервіси…", nil)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		html := b.statusText(ctx)
		b.show(ctx, chat, msgID, html, notify.InlineKeyboard{row(btn("🔄 Перевірити ще раз", "st"), btn("🏠 Меню", "m"))})
	}()
}

func (b *Bot) checks() []serviceCheck {
	cfg := b.cfg.Get()
	e := b.e
	text := "Текст"
	if cfg.Text.Provider == "google" {
		text += " · Google " + cfg.Google.StoryModel
	} else if cfg.Text.Model != "" {
		text += " · " + cfg.Text.Model
	}
	list := []serviceCheck{{text, e.TestText}}
	seen := map[string]bool{}
	for _, p := range []struct{ provider, what string }{{cfg.Image.Provider, "кадри"}, {cfg.Video.Provider, "відео"}} {
		switch p.provider {
		case "glabs":
			if !seen["glabs"] {
				list = append(list, serviceCheck{"G-Labs", e.TestGlabs})
			}
		case "google":
			if !seen["google"] {
				list = append(list, serviceCheck{fmt.Sprintf("Google AI Studio · ключів: %d", len(cfg.Google.Keys)), e.TestGoogle})
			}
		case "custom":
			if p.what == "кадри" {
				list = append(list, serviceCheck{"Своє API · кадри", e.TestCustomImage})
			} else {
				list = append(list, serviceCheck{"Своє API · відео", e.TestCustomVideo})
			}
		}
		seen[p.provider] = true
	}
	voice := "Озвучка"
	if info, ok := tts.ProviderByID(cfg.TTS.Provider); ok {
		voice += " · " + info.Label
	}
	list = append(list,
		serviceCheck{voice, e.TestTTS},
		serviceCheck{"Розпізнавання мови", e.TestSTT},
		serviceCheck{"FFmpeg", e.TestFFmpeg},
	)
	return list
}

func (b *Bot) statusText(parent context.Context) string {
	checks := b.checks()
	type result struct {
		ok  bool
		msg string
	}
	res := make([]result, len(checks))
	ctx, cancel := context.WithTimeout(parent, statusTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					res[i] = result{false, "внутрішня помилка перевірки"}
				}
			}()
			ok, msg := c.fn(ctx)
			res[i] = result{ok, msg}
		}()
	}
	wg.Wait()

	var sb strings.Builder
	sb.WriteString("📊 <b>Стан студії</b>\n")
	sb.WriteString("YT Cartell Studio " + esc(version.String()))
	if !b.start.IsZero() {
		sb.WriteString(" · працює " + span(int(time.Since(b.start).Seconds())))
	}
	q := b.e.Queue()
	sb.WriteString(fmt.Sprintf("\n⏳ Генерується: %d", q.Counts.Running))
	if q.Counts.Errors > 0 {
		sb.WriteString(fmt.Sprintf(" · помилок нещодавно: %d", q.Counts.Errors))
	}
	sb.WriteString("\n\n<b>Сервіси</b>")
	for i, c := range checks {
		icon := "✅"
		if !res[i].ok {
			icon = "⚠️"
		}
		msg := firstLine(res[i].msg, 160)
		if msg == "" && ctx.Err() != nil {
			msg = "не відповів вчасно"
		}
		sb.WriteString("\n" + icon + " " + esc(c.name))
		if msg != "" {
			sb.WriteString(" — " + esc(msg))
		}
	}
	// What the voice-over keys know about their balance.
	keys := b.e.TTSKeyStatus()
	if len(keys) > 0 {
		okKeys := 0
		var infos []string
		for _, k := range keys {
			if k.State == tts.KeyOK {
				okKeys++
			}
			if k.Info != "" && len(infos) < 3 {
				infos = append(infos, k.Mask+": "+k.Info)
			}
		}
		sb.WriteString(fmt.Sprintf("\n🔑 Ключі озвучки: %d, робочих %d", len(keys), okKeys))
		for _, s := range infos {
			sb.WriteString("\n   " + esc(clipName(s, 120)))
		}
	}
	dir := b.cfg.Get().DataDir
	for { // a data folder not made yet: its drive
		if _, err := os.Stat(dir); err == nil {
			break
		}
		up := filepath.Dir(dir)
		if up == dir {
			break
		}
		dir = up
	}
	if free, total, err := diskFree(dir); err == nil && total > 0 {
		sb.WriteString(fmt.Sprintf("\n\n💾 Диск із проєктами: вільно %s з %s", notify.FormatSize(int64(free)), notify.FormatSize(int64(total))))
		if free < 5<<30 {
			sb.WriteString(" — ⚠️ місця мало")
		}
	}
	return sb.String()
}
