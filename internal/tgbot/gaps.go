package tgbot

// «Не всі сцени готові»: the montage never fills in a failed scene by itself
// (studio/gaps.go). When a montage from the bot is refused (MissingScenesError)
// or the autopilot stopped before the montage (Job.Blocked = BlockedGaps), the
// bot shows every scene that is not ready with the service's exact error (and
// a hint) in a message — not in a 200-character popup — and the choices of the
// step-5 dialog, through the same engine API:
//
//	gs:<k>  «Змонтувати без них»      RunWith(StageMontage, MissingSkip)
//	gf:<k>  «З кадром замість відео»  RunWith(StageMontage, MissingStill)
//	gr:<k>  «Перезняти невдалі»       the stopped autopilot: Retry (as the
//	                                  studio's banner); else the autopilot
//	                                  without re-planning — it redoes only
//	                                  what is missing, then the montage asks
//	                                  again if something still fails
//	gx:<k>  «Зупинитись»              nothing is rendered
//
// The same buttons go under the studio's own error notification of such an
// autopilot (TelegramHooks.ErrorMarkup); a live message of it turns into the
// list, and without either the bot sends the list itself.

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

const (
	// gapsReason is how much of one scene's error the list keeps.
	gapsReason = 600
	// gapsBudget is how many visible characters the scene list may take
	// (the rest of the message stays well under Telegram's 4096).
	gapsBudget = 2900
)

// blockedJob: the job stopped before the montage because scenes are not ready.
func blockedJob(j *studio.Job) bool {
	return j != nil && j.Status == studio.JobError && j.Blocked == studio.BlockedGaps
}

// gapsOf lists the scenes of p the montage cannot show as planned (files
// that are gone count).
func (b *Bot) gapsOf(p studio.Project) []studio.MontageGap {
	return studio.MontageGaps(&p, b.e.Dir(p.ID))
}

// gapsKeyboard are the montage choices for the project with key k.
func gapsKeyboard(k string) notify.InlineKeyboard {
	return notify.InlineKeyboard{
		row(btn("🎬 Змонтувати без них", "gs:"+k)),
		row(btn("🖼 З кадром замість відео", "gf:"+k)),
		row(btn("🔁 Перезняти невдалі", "gr:"+k)),
		row(btn("⏹ Зупинитись", "gx:"+k), btn("📂 Проєкт", "p:"+k)),
	}
}

// gapsView is the question: every scene that is not ready with its exact
// error, what each choice does, and the buttons. stopped: the autopilot
// stopped before the montage (else a montage pressed here was refused).
func gapsView(p studio.Project, gaps []studio.MontageGap, stopped bool) (string, notify.InlineKeyboard) {
	var sb strings.Builder
	if stopped {
		sb.WriteString("⏸ " + title(p.Title) + " — автопілот зупинився перед монтажем")
	} else {
		sb.WriteString("✂️ " + title(p.Title) + " — монтаж чекає на твоє рішення")
	}
	fmt.Fprintf(&sb, "\nНе готово сцен: <b>%d з %d</b>. Студія нічого не підставляє сама — ось що не вийшло і чому:\n", len(gaps), len(p.Scenes))
	used, frames := 0, 0
	for i, g := range gaps {
		if g.NoClip && !g.NoFrame {
			frames++
		}
		item, plain := gapItem(g)
		if used+plain > gapsBudget && i > 0 {
			rest := len(gaps) - i
			sb.WriteString("\n… і ще " + count(rest, "сцена", "сцени", "сцен") + " — повний список у студії на кроці «Монтаж».\n")
			for _, g := range gaps[i:] { // the still tip counts every scene
				if g.NoClip && !g.NoFrame {
					frames++
				}
			}
			break
		}
		used += plain
		sb.WriteString(item)
	}
	still := "сцени без кліпу покажуть свій кадр із рухом; сцени без кадру випадуть, без голосу — пройдуть без репліки."
	if frames == 0 {
		still = "кадру немає в жодної з цих сцен — вони випадуть; сцени без голосу пройдуть без репліки."
	}
	sb.WriteString("\n<b>Як монтувати?</b>")
	sb.WriteString("\n🎬 <b>Без них</b> — ці сцени (і їхній голос) не потраплять у фільм.")
	sb.WriteString("\n🖼 <b>З кадром</b> — " + still)
	sb.WriteString("\n🔁 <b>Перезняти</b> — ще раз спробувати невдалі сцени (витрачає кредити), потім змонтувати.")
	sb.WriteString("\n⏹ <b>Зупинитись</b> — нічого не монтувати.")
	return sb.String(), gapsKeyboard(key(p.ID))
}

// gapItem is one scene of the list (HTML) and its visible length.
func gapItem(g studio.MontageGap) (html string, visible int) {
	head := fmt.Sprintf("Сцена %d", g.Number)
	var what []string
	switch {
	case g.NoClip && g.NoFrame:
		what = append(what, "немає ні кліпу, ні кадру")
	case g.NoClip:
		what = append(what, "немає кліпу")
	}
	if g.NoVoice {
		what = append(what, "немає голосу")
	}
	if len(what) > 0 {
		head += " — " + strings.Join(what, ", ")
	}
	text := "\n• " + head
	html = "\n• <b>" + esc(head) + "</b>"
	if r := strings.TrimSpace(g.Reason); r != "" {
		r = clipName(r, gapsReason)
		text += "\n" + r
		html += "\n" + esc(r)
		if h := studio.FailureHint(g.Reason); h != "" {
			text += "\n💡 " + h
			html += "\n💡 " + esc(h)
		}
	}
	text += "\n"
	html += "\n"
	return html, utf8.RuneCountInString(text)
}

// missingScenes shows the montage question when err is a refused montage;
// false for any other error.
func (b *Bot) missingScenes(c *press, p studio.Project, err error) bool {
	m, ok := studio.IsMissingScenes(err)
	if !ok {
		return false
	}
	gaps := m.Gaps
	if len(gaps) == 0 {
		gaps = b.gapsOf(p)
	}
	html, kb := gapsView(p, gaps, false)
	c.show(html, kb)
	return true
}

// montageWith renders the film with the user's choice for the scenes that
// are not ready (the step-5 dialog's buttons).
func (b *Bot) montageWith(c *press, p studio.Project, missing string) {
	if err := b.e.RunWith(p.ID, studio.StageMontage, studio.RunOptions{Missing: missing}); err != nil {
		c.toast(userErr(err), true)
		return
	}
	label := "✂️ Монтую без невдалих сцен"
	if missing == studio.MissingStill {
		label = "✂️ Монтую — кадри замість невдалих кліпів"
	}
	b.started(c, p.ID, label)
}

// reshoot is «Перезняти невдалі»: the autopilot that stopped before the
// montage continues (Retry — what the studio's banner does); otherwise the
// autopilot runs without re-planning, redoing only what is missing.
func (b *Bot) reshoot(c *press, p studio.Project) {
	var err error
	if blockedJob(p.Job) {
		err = b.e.Retry(p.ID)
	} else {
		err = b.e.Run(p.ID, studio.StageAutopilot, false, p.Montage.CoverPrompt)
	}
	if err != nil {
		c.toast(userErr(err), true)
		return
	}
	b.started(c, p.ID, "🔁 Перезнімаю невдалі сцени")
}

// gapsStop is «Зупинитись»: nothing is rendered; the card stays.
func (b *Bot) gapsStop(c *press, p studio.Project) {
	c.toast("Добре, нічого не монтую", false)
	html, kb := b.card(p)
	c.show("⏹ Нічого не монтую. Невдалі сцени можна перезняти кнопкою «🔁 Перезняти невдалі» або в студії на кроках 3–4.\n\n"+html, kb)
}

// started turns the pressed message into the card of a job just started and
// follows it live.
func (b *Bot) started(c *press, pid, label string) {
	c.toast(label, false)
	if q, ok := b.e.Get(pid); ok {
		html, kb := b.card(q)
		id := c.show(html, kb)
		b.live.watch(pid, c.s.chat, id)
	}
}

// errorMarkup is the engine's ErrorMarkup hook: the montage choices under
// the error notification of an autopilot that stopped before the montage.
func (b *Bot) errorMarkup(pid string, job studio.Job) notify.InlineKeyboard {
	if !b.running.Load() || !b.tg().RemoteOn() || !blockedJob(&job) {
		return nil
	}
	return gapsKeyboard(key(pid))
}

// announceGaps sends the montage question of a stopped autopilot that had no
// live message and no notification with the buttons.
func (l *liveState) announceGaps(ev endEvent) {
	b := l.b
	if on, _ := l.on(); !on {
		return
	}
	p, ok := b.e.Get(ev.pid)
	if !ok {
		return
	}
	gaps := b.gapsOf(p)
	if len(gaps) == 0 {
		return
	}
	html, kb := gapsView(p, gaps, true)
	ctx, cancel := context.WithTimeout(b.ctx, 2*notify.Timeout)
	defer cancel()
	b.send(ctx, b.tg().ChatID, html, &notify.SendOptions{Markup: kb})
}
