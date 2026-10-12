package tgbot

import (
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

const refusedClip = "G-Labs [400]: PROMINENT_PERSON (flag=[3])"

// failScene2 makes the second seeded scene's clip a refusal with its exact error.
func failScene2(p *studio.Project) {
	p.Scenes[1].Video = studio.Asset{Status: studio.StatusError, Error: refusedClip}
}

// hasData: the keyboard has a button whose data starts with prefix.
func hasData(kb notify.InlineKeyboard, prefix string) bool {
	for _, r := range kb {
		for _, b := range r {
			if strings.HasPrefix(b.Data, prefix) {
				return true
			}
		}
	}
	return false
}

// TestMontageGapsQuestion: «✂️ Монтаж» with a failed scene shows the scene
// with its exact error and hint in a message (not a popup) and the choices
// of the step-5 dialog; «Зупинитись» renders nothing, «З кадром замість
// відео» starts the montage through RunWith.
func TestMontageGapsQuestion(t *testing.T) {
	t.Parallel()
	const pid = "proj000031"
	h := newHarness(t, hopts{seeds: []seed{{id: pid, title: "Пропуски", scenes: 3, mut: failScene2}}})
	h.fake.say(4242, owner, "/start p_"+key(pid))
	card := h.fake.waitText(ownerChat, "📂 <b>«Пропуски»</b>")
	h.fake.press(4242, owner, "✂️ Монтаж")
	txt := h.fake.waitIn(card.id, "монтаж чекає на твоє рішення")
	for _, want := range []string{"Не готово сцен: <b>1 з 3</b>", "<b>Сцена 2 — немає кліпу</b>", "кліп: " + refusedClip,
		"💡 фільтр Google побачив упізнавану реальну людину", "🎬 <b>Без них</b>", "🖼 <b>З кадром</b> — сцени без кліпу покажуть свій кадр"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("the question lacks %q:\n%s", want, txt)
		}
	}
	for _, c := range h.fake.of("answerCallbackQuery", "") {
		if strings.Contains(c.form.Get("text"), "Не всі сцени") {
			t.Fatalf("the refusal went into a popup: %q", c.form.Get("text"))
		}
	}
	k := key(pid)
	for _, data := range []string{"gs:" + k, "gf:" + k, "gr:" + k, "gx:" + k} {
		if !hasData(h.fake.last(ownerChat).kb, data) {
			t.Fatalf("no button %s under %q", data, txt)
		}
	}

	h.fake.press(4242, owner, "⏹ Зупинитись")
	h.fake.waitIn(card.id, "Нічого не монтую")
	if p, _ := h.e.Get(pid); p.Job != nil {
		t.Fatalf("a job after «Зупинитись»: %+v", p.Job)
	}

	h.fake.press(4242, owner, "✂️ Монтаж")
	h.fake.waitIn(card.id, "монтаж чекає")
	h.fake.press(4242, owner, "🖼 З кадром замість відео")
	h.fake.wait("the montage job", func() bool {
		p, _ := h.e.Get(pid)
		return p.Job != nil && p.Job.Kind == studio.JobMontage
	})
	h.fake.wait("the card to follow the montage", func() bool { return !strings.Contains(h.fake.text(card.id), "монтаж чекає") })
}

// TestAutopilotStoppedBeforeMontage: the autopilot redoes what failed, the
// scene is still not ready and nobody chose what to do — it stops before
// the montage. The live message turns into the list with the exact error
// and the choices, the studio's notification carries the same buttons, the
// card says it waits, and «Перезняти невдалі» continues the autopilot.
func TestAutopilotStoppedBeforeMontage(t *testing.T) {
	t.Parallel()
	const pid = "proj000032"
	h := newHarness(t, hopts{
		seeds: []seed{{id: pid, title: "Автопілот стоп", scenes: 2, mut: failScene2}},
		cfg:   func(c *config.Config) { c.Autopilot.Retries, c.Autopilot.OnFailed = 0, "" },
	})
	h.fake.say(4242, owner, "/start p_"+key(pid))
	card := h.fake.waitText(ownerChat, "📂 <b>«Автопілот стоп»</b>")
	h.fake.press(4242, owner, "🚀 Автопілот")
	txt := h.fake.waitIn(card.id, "автопілот зупинився перед монтажем")
	p, _ := h.e.Get(pid)
	if !blockedJob(p.Job) {
		t.Fatalf("job %+v", p.Job)
	}
	// The clip was tried again (G-Labs is not reachable here): the list
	// shows the error of that try, exactly.
	if !strings.Contains(txt, "<b>Сцена 2 — немає кліпу</b>\nкліп: "+esc(strings.TrimSpace(p.Scenes[1].Video.Error))) ||
		!strings.Contains(txt, "Не готово сцен: <b>1 з 2</b>") {
		t.Fatalf("live message:\n%s\nscene 2: %+v", txt, p.Scenes[1].Video)
	}
	k := key(pid)
	// The studio's own error notification carries the montage choices.
	note := h.fake.waitText(ownerChat, "⚠️ <b>«Автопілот стоп»</b>")
	if !hasData(note.kb, "gs:"+k) || !hasData(note.kb, "gf:"+k) || !hasData(note.kb, "gr:"+k) || !hasData(note.kb, "gx:"+k) {
		t.Fatalf("notification buttons %+v", note.kb)
	}
	if kb := h.bot.errorMarkup(pid, studio.Job{Kind: studio.JobPlan, Status: studio.JobError}); kb != nil {
		t.Fatal("buttons under an ordinary error")
	}

	// No live message and no notification: the bot asks by itself.
	before := len(h.fake.of("sendMessage", ownerChat))
	h.bot.live.ended(pid, *p.Job, false)
	h.fake.wait("the question sent", func() bool {
		for _, c := range h.fake.of("sendMessage", ownerChat)[before:] {
			if strings.Contains(c.text, "автопілот зупинився перед монтажем") {
				return true
			}
		}
		return false
	})

	// The card says it waits and offers the same choices.
	h.fake.say(4242, owner, "/start p_"+key(pid))
	c2 := h.fake.waitText(ownerChat, "чекає на твоє рішення перед монтажем")
	if !strings.Contains(c2.text, "Не готово сцен: 1 з 2") || !hasData(c2.kb, "pm:"+k) {
		t.Fatalf("card %q %+v", c2.text, c2.kb)
	}
	if _, b, ok := h.fake.button(ownerChat, "✂️ Обрати, як змонтувати"); !ok || b.Data != "pm:"+k {
		t.Fatal("no «Обрати, як змонтувати» on the card")
	}
	if _, b, ok := h.fake.button(ownerChat, "🔁 Перезняти невдалі"); !ok || !strings.HasPrefix(b.Data, "pr:") {
		t.Fatal("no «Перезняти невдалі» on the card")
	}

	// «Перезняти невдалі» under the question continues the autopilot.
	started := p.Job.StartedAt
	h.fake.pressData(4242, owner, note.id, "gr:"+k)
	h.fake.wait("the autopilot again", func() bool {
		q, _ := h.e.Get(pid)
		return q.Job != nil && q.Job.Kind == studio.JobAutopilot && q.Job.StartedAt > started
	})
}

// The question stays within Telegram's limits with many long errors.
func TestGapsViewLong(t *testing.T) {
	p := studio.Project{ID: "p", Title: "Довгий"}
	var gaps []studio.MontageGap
	for i := 0; i < 40; i++ {
		p.Scenes = append(p.Scenes, studio.Scene{ID: "s"})
		gaps = append(gaps, studio.MontageGap{Number: i + 1, NoClip: true, NoFrame: true,
			Reason: "кадр: G-Labs відхилив запит: " + strings.Repeat("INVALID_ARGUMENT <x> ", 60)})
	}
	html, kb := gapsView(p, gaps, true)
	n, err := checkHTML(html)
	if err != nil || n > notify.MaxMessage {
		t.Fatalf("%d visible characters, %v", n, err)
	}
	if !strings.Contains(html, "і ще ") || !strings.Contains(html, "кадру немає в жодної з цих сцен") || len(kb) != 4 {
		t.Fatalf("long list:\n%s", html)
	}
	if err := kb.Check(); err != nil {
		t.Fatal(err)
	}
}
