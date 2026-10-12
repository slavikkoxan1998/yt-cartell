package tgbot

// «🔁 За прикладом»: a YouTube link, a wish, then one options message
// (fidelity, length, profile, mode) and Engine.StartRemake — a model watches
// the example and the studio makes a new project after it.

import (
	"context"
	"strconv"
	"strings"

	"ytcartell/internal/notify"
	"ytcartell/internal/reference"
	"ytcartell/internal/studio"
)

type remakeDraft struct {
	nonce     string
	url       string
	videoID   string
	wish      string
	fidelity  string
	keep      bool // the original's length
	secs      int
	profileID string // "" + noProfile = «як у прикладі»
	noProfile bool
	auto      bool
	menu      string
}

var fidelities = []choice{
	{studio.FidelityClose, "Близько до оригіналу"},
	{studio.FidelityExact, "Точна копія"},
	{studio.FidelityStructure, "Лише структура й темп"},
}

// startRemake asks for the link (when a service can watch it).
func (b *Bot) startRemake(ctx context.Context, s *session, id int) {
	s.remake = nil
	if svc := b.e.RemakeService(); !svc.Ready {
		s.flow = flowNone
		b.show(ctx, s.chat, id, "🔁 <b>За прикладом</b>\n"+esc(svc.Note), nil)
		return
	}
	s.flow = flowRemakeLink
	b.show(ctx, s.chat, id, "🔁 <b>За прикладом</b>\nНадішли посилання на відео YouTube — його подивиться модель, а студія зробить нове відео за зразком.",
		notify.InlineKeyboard{row(btn("✖️ Скасувати", "r:-:no"))})
}

func (b *Bot) remakeLink(ctx context.Context, s *session, text string) {
	id := reference.YouTubeID(strings.TrimSpace(text))
	if id == "" {
		b.send(ctx, s.chat, "Це не схоже на посилання на відео YouTube 🙂 Надішли посилання виду youtube.com/watch?v=… або youtu.be/…", nil)
		return
	}
	d := &remakeDraft{nonce: nonce(), url: strings.TrimSpace(text), videoID: id, fidelity: studio.FidelityClose,
		keep: true, noProfile: true, auto: true}
	s.remake, s.flow = d, flowRemakeWish
	b.send(ctx, s.chat, "💬 Що змінити або що хочеш побачити? Напиши кількома словами — або натисни «Без побажань».",
		&notify.SendOptions{Markup: notify.InlineKeyboard{row(btn("Без побажань", "r:"+d.nonce+":wish"), btn("✖️ Скасувати", "r:"+d.nonce+":no"))}})
}

func (b *Bot) remakeWish(ctx context.Context, s *session, text string) {
	d := s.remake
	if d == nil {
		s.flow = flowNone
		return
	}
	d.wish = strings.TrimSpace(text)
	s.flow = flowNone
	html, kb := b.remakeView(d)
	b.send(ctx, s.chat, html, &notify.SendOptions{Markup: kb})
}

func (b *Bot) remakeView(d *remakeDraft) (string, notify.InlineKeyboard) {
	var sb strings.Builder
	sb.WriteString("🔁 <b>За прикладом</b>\n▶️ youtu.be/" + esc(d.videoID))
	if d.wish != "" {
		sb.WriteString("\n💬 <i>" + esc(excerpt(d.wish, 300)) + "</i>")
	} else {
		sb.WriteString("\n💬 Без побажань — та сама тема, нові слова")
	}
	sb.WriteString("\n\n🎯 Точність: <b>" + choiceLabel(fidelities, d.fidelity) + "</b>")
	ln := "як в оригіналі"
	if !d.keep {
		ln = length(d.secs)
	}
	sb.WriteString("\n⏱ Тривалість: <b>" + ln + "</b>")
	prof := "як у прикладі"
	if !d.noProfile {
		if p, ok := b.e.Profile(d.profileID); ok {
			prof = p.Name
		}
	}
	sb.WriteString("\n👤 Профіль: <b>" + esc(prof) + "</b>")
	mode := "Автопілот — до готового відео"
	if !d.auto {
		mode = "Покроково — розбір і сценарій, далі сам"
	}
	sb.WriteString("\n🔀 Режим: <b>" + mode + "</b>")

	cb := func(field, value string) string {
		if value == "" {
			return "r:" + d.nonce + ":" + field
		}
		return "r:" + d.nonce + ":" + field + ":" + value
	}
	mark := func(on bool, label string) string {
		if on {
			return "✓ " + label
		}
		return label
	}
	back := row(btn("◀️ Назад", cb("back", "")))
	switch d.menu {
	case "ln":
		kb := notify.InlineKeyboard{row(btn(mark(d.keep, "Як в оригіналі"), cb("lv", "0")))}
		var r []notify.Button
		for _, v := range lengths {
			r = append(r, btn(mark(!d.keep && d.secs == v, length(v)), cb("lv", strconv.Itoa(v))))
		}
		return sb.String(), append(kb, r, back)
	case "pf":
		kb := notify.InlineKeyboard{row(btn(mark(d.noProfile, "Як у прикладі"), cb("ps", "-")))}
		for i, p := range b.e.Profiles() {
			if i == 10 {
				break
			}
			kb = append(kb, row(btn(mark(!d.noProfile && p.ID == d.profileID, clipName(p.Name, 40)), cb("ps", key(p.ID)))))
		}
		return sb.String(), append(kb, back)
	}
	return sb.String(), notify.InlineKeyboard{
		row(btn("🎯 Точність", cb("fd", "")), btn("⏱ Тривалість", cb("ln", ""))),
		row(btn("👤 Профіль", cb("pf", "")), btn("🔀 Режим", cb("md", ""))),
		row(notify.Button{Text: "▶️ Запустити", Data: cb("go", ""), Style: "success"}, btn("✖️ Скасувати", cb("no", ""))),
	}
}

func (b *Bot) remakePress(c *press, n, field, value string) {
	s := c.s
	if field == "no" && n == "-" {
		s.flow = flowNone
		c.show("Добре, скасовано.", nil)
		return
	}
	d := s.remake
	if d == nil || d.nonce != n {
		c.toast("Ця чернетка вже неактуальна — почни ще раз", true)
		return
	}
	switch field {
	case "wish":
		d.wish = ""
		s.flow = flowNone
		html, kb := b.remakeView(d)
		c.show(html, kb)
		return
	case "fd":
		for i, f := range fidelities {
			if f.value == d.fidelity {
				d.fidelity = fidelities[(i+1)%len(fidelities)].value
				break
			}
		}
	case "ln", "pf":
		d.menu = field
	case "lv":
		v := atoi(value)
		d.keep, d.secs, d.menu = v <= 0, v, ""
	case "ps":
		d.noProfile, d.profileID, d.menu = true, "", ""
		if value != "-" {
			if pid, _, ok := b.profileByKey(value); ok {
				d.noProfile, d.profileID = false, pid
			}
		}
	case "md":
		d.auto = !d.auto
	case "back":
		d.menu = ""
	case "no":
		s.remake, s.flow = nil, flowNone
		c.show("Добре, не перезнімаю.", nil)
		return
	case "go":
		opt := studio.RemakeOptions{URL: d.url, Wish: d.wish, Fidelity: d.fidelity, KeepLength: d.keep,
			LanguageSet: true, ProfileID: d.profileID, NoProfile: d.noProfile, Autopilot: d.auto}
		if !d.keep {
			opt.TargetSeconds = d.secs
		}
		p, err := b.e.StartRemake(opt)
		if err != nil {
			c.toast(clipName(userErr(err), 190), true)
			return
		}
		s.remake = nil
		c.toast("🔁 Дивлюся приклад", false)
		k := key(p.ID)
		next := "далі автопілот зробить відео"
		if !d.auto {
			next = "розбір і сценарій з'являться в проєкті"
		}
		id := c.show("🔁 Перезйомка за прикладом запущена: "+title(p.Title)+"\nМодель дивиться відео, "+next+". Я оновлюватиму це повідомлення.",
			notify.InlineKeyboard{row(btn("⏹ Зупинити", "ps:"+k), btn("📂 Проєкт", "p:"+k))})
		b.live.watch(p.ID, s.chat, id)
		return
	}
	html, kb := b.remakeView(d)
	c.show(html, kb)
}
