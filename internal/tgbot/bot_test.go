package tgbot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

// TestPrivate: strangers get «Цей бот приватний» and nothing else — no
// project names, no actions, at most once a minute per chat.
func TestPrivate(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{seeds: []seed{{id: "proj000001", title: "Секретний фільм", scenes: 2}}})
	k := key("proj000001")
	h.fake.say(666, stranger, "/start")
	h.fake.say(666, stranger, "Покажи проєкти")
	h.fake.say(666, stranger, "/projects")
	q1 := h.fake.pressData(666, stranger, 1, "pd:"+k)
	q2 := h.fake.pressData(4242, stranger, 1, "pd:"+k) // a forged press "in the owner's chat"
	h.fake.say(-100500, stranger, "/start@studio_test_bot")
	h.fake.say(-100500, stranger, "просто текст у групі")
	h.fake.wait("the refusals", func() bool {
		answered := 0
		for _, c := range h.fake.of("answerCallbackQuery", "") {
			id := c.form.Get("callback_query_id")
			if (id == q1 || id == q2) && c.form.Get("text") == textPrivate && c.form.Get("show_alert") == "true" {
				answered++
			}
		}
		return answered == 2 && len(h.fake.of("sendMessage", "666")) == 1 && len(h.fake.of("sendMessage", "-100500")) == 1
	})
	time.Sleep(300 * time.Millisecond)
	for _, c := range h.fake.all() {
		if c.chat == "666" || c.chat == "-100500" {
			if c.method != "sendMessage" || c.text != textPrivate {
				t.Errorf("a stranger got %s %q", c.method, c.text)
			}
		}
		if strings.Contains(c.text, "Секретний") {
			t.Errorf("the project name leaked: %s %q", c.method, c.text)
		}
	}
	if n := len(h.fake.of("sendMessage", "666")); n != 1 {
		t.Errorf("the stranger got %d refusals, want 1 a minute", n)
	}
	if _, ok := h.e.Get("proj000001"); !ok {
		t.Fatal("a stranger deleted a project")
	}
	if len(h.chatCalls(ownerChat)) != 0 {
		t.Fatalf("the owner's chat got messages: %s", h.fake.dump(ownerChat))
	}
}

// TestPairing: a one-time code from Settings; five wrong tries end it; the
// right one saves the owner and the chat and greets them with the menu.
func TestPairing(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{tg: func(tg *config.Telegram) { tg.OwnerID, tg.OwnerName, tg.ChatID, tg.Remote = 0, "", "", false }, noWait: true})
	time.Sleep(100 * time.Millisecond)
	if st := h.bot.State(); st.Status != StatusOff || len(h.fake.of("getUpdates", "")) != 0 {
		t.Fatalf("polls without pairing or remote control: %+v", st)
	}
	st := h.bot.Pair()
	if len(st.Code) != 6 || time.Until(st.CodeUntil) < 9*time.Minute {
		t.Fatalf("code %+v", st)
	}
	h.fake.wait("listening", func() bool { return h.bot.State().Status == StatusOn })
	if h.bot.State().Username != "studio_test_bot" {
		t.Fatalf("username %q", h.bot.State().Username)
	}
	wrong := fmt.Sprintf("%06d", (atoi(st.Code)+1)%1_000_000)
	for i := 0; i < 5; i++ {
		h.fake.say(666, stranger, "/start "+wrong)
	}
	h.fake.wait("the code to be cancelled", func() bool { return h.bot.State().Code == "" && h.bot.State().PairNote != "" })
	h.fake.say(666, stranger, "/start "+st.Code) // the old code no longer works
	time.Sleep(200 * time.Millisecond)
	if tg := h.store.Get().Telegram; tg.OwnerID != 0 {
		t.Fatalf("paired after the code was cancelled: %+v", tg)
	}

	st = h.bot.Pair()
	h.fake.wait("listening again", func() bool { return h.bot.State().Status == StatusOn })
	h.fake.say(4242, owner, "/start "+st.Code[:3]+" "+st.Code[3:])
	m := h.fake.waitText(ownerChat, "Керування підключено")
	tg := h.store.Get().Telegram
	if tg.OwnerID != owner.ID || tg.ChatID != ownerChat || !tg.Remote || tg.OwnerName != "Іван (@ivan)" || !m.reply {
		t.Fatalf("pairing saved %+v, keyboard %v", tg, m.reply)
	}
	h.fake.wait("the command menu", func() bool {
		for _, c := range h.fake.of("setMyCommands", "") {
			if c.form.Get("scope") == `{"type":"chat","chat_id":4242}` && strings.Contains(c.form.Get("commands"), `"new"`) {
				return true
			}
		}
		return false
	})
	// One-time: the same code from someone else is refused.
	h.fake.say(666, stranger, "/start "+st.Code)
	h.fake.wait("the refusal", func() bool { return len(h.fake.of("sendMessage", "666")) > 0 })
	if h.store.Get().Telegram.OwnerID != owner.ID {
		t.Fatal("the code worked twice")
	}
}

// TestMenusAndDelete: the menu, the projects list, a card, and a delete that
// needs a second press.
func TestMenusAndDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{seeds: []seed{
		{id: "proj000001", title: "Перший <тест> & Ко", scenes: 2},
		{id: "proj000002", title: "Другий", scenes: 3},
	}})
	h.fake.say(4242, owner, "/start")
	if m := h.fake.waitText(ownerChat, "Привіт"); !m.reply {
		t.Fatal("no main keyboard")
	}
	h.fake.say(4242, owner, "📁 Проєкти")
	list := h.fake.waitText(ownerChat, "<b>Проєкти</b> · 2")
	if !strings.Contains(list.text, "«Перший &lt;тест&gt; &amp; Ко»") {
		t.Fatalf("list %q", list.text)
	}
	id := h.fake.press(4242, owner, "📝 Другий")
	if txt := h.fake.waitIn(id, "📂 <b>«Другий»</b>"); !strings.Contains(txt, "3 сцени") {
		t.Fatalf("the list did not turn into the card: %q", txt)
	}
	h.fake.press(4242, owner, "🗑 Видалити")
	h.fake.waitIn(id, "Видалити проєкт")
	h.fake.press(4242, owner, "Скасувати")
	h.fake.waitIn(id, "📂")
	if _, ok := h.e.Get("proj000002"); !ok {
		t.Fatal("deleted without the second press")
	}
	h.fake.press(4242, owner, "🗑 Видалити")
	h.fake.press(4242, owner, "🗑 Так, видалити")
	h.fake.waitIn(id, "видалено")
	if _, ok := h.e.Get("proj000002"); ok {
		t.Fatal("the project is still there")
	}
	// An old confirmation token does nothing.
	qid := h.fake.pressData(4242, owner, id, "x:deadbeef")
	h.fake.wait("the stale answer", func() bool {
		for _, c := range h.fake.of("answerCallbackQuery", "") {
			if c.form.Get("callback_query_id") == qid && strings.Contains(c.form.Get("text"), "минув") {
				return true
			}
		}
		return false
	})
	// Queue, profiles and help answer too.
	h.fake.say(4242, owner, "⏳ Черга")
	h.fake.waitText(ownerChat, "Зараз нічого не генерується")
	h.fake.say(4242, owner, "⚙️ Профілі")
	h.fake.waitText(ownerChat, "Профіль за замовчуванням")
	h.fake.press(4242, owner, "Shorts")
	if def := h.store.Get().Autopilot.DefaultProfileID; def != "starter-shorts" {
		t.Fatalf("default profile %q", def)
	}
	h.fake.say(4242, owner, "/help")
	h.fake.waitText(ownerChat, "Що я вмію")
	// Every answer was answered and every keyboard checked by the fake.
	for _, c := range h.fake.of("answerCallbackQuery", "") {
		_ = c
	}
}

// TestNewProjectStepByStep: the story, every option, «Запустити».
func TestNewProjectStepByStep(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{})
	story := "Кіт Мурчик шукав дім у великому місті. Він зустрів добру дівчинку й нарешті знайшов родину."
	h.fake.say(4242, owner, "🎬 Новий проєкт")
	h.fake.waitText(ownerChat, "Надішли історію")
	h.fake.say(4242, owner, story)
	opts := h.fake.waitText(ownerChat, "<b>Новий проєкт</b>\n<i>")
	if !strings.Contains(opts.text, "Режим: <b>Автопілот</b>") {
		t.Fatalf("options %q", opts.text)
	}
	h.fake.press(4242, owner, "⏱")
	h.fake.press(4242, owner, "3 хв")
	h.fake.press(4242, owner, "🔀 Режим: Автопілот")
	h.fake.press(4242, owner, "🌐 Мова")
	h.fake.press(4242, owner, "Англійська")
	h.fake.press(4242, owner, "📐 9:16")
	h.fake.press(4242, owner, "🎙 Звук")
	h.fake.press(4242, owner, "Без голосу")
	h.fake.press(4242, owner, "🎞 Що в кадрі")
	h.fake.press(4242, owner, "Моушн-графіка")
	h.fake.press(4242, owner, "🏭 Виробництво")
	h.fake.press(4242, owner, "Тільки картинки")
	txt := h.fake.waitIn(opts.id, "Виробництво: <b>Тільки картинки")
	for _, want := range []string{"Покроково", "3 хв", "Англійська", "Без голосу", "Моушн-графіка", "9:16", "Тільки картинки"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("options miss %q: %q", want, txt)
		}
	}
	h.fake.press(4242, owner, "▶️ Запустити")
	h.fake.wait("the project", func() bool { return len(h.e.List()) == 1 })
	p, _ := h.e.Get(h.e.List()[0].ID)
	in := p.Input
	if in.TargetSeconds != 180 || in.Language != "en" || in.Aspect != "9:16" || in.AudioMode != studio.AudioSilent ||
		in.Content != studio.ContentMotion || in.Pipeline != studio.PipelineStills || in.Text != story || p.Job != nil || p.Title != "Кіт Мурчик шукав дім у великому" {
		t.Fatalf("project %q %+v job %+v", p.Title, in, p.Job)
	}
	h.fake.waitIn(opts.id, "📂")

	// «Своя тривалість» and «Інша мова» by text.
	h.fake.say(4242, owner, "/new")
	h.fake.say(4242, owner, story)
	h.fake.wait("new options", func() bool {
		m := h.fake.last(ownerChat)
		return strings.Contains(m.text, "<b>Новий проєкт</b>\n<i>") && m.id != opts.id
	})
	h.fake.press(4242, owner, "⏱")
	h.fake.press(4242, owner, "✏️ Своя тривалість")
	h.fake.waitText(ownerChat, "Скільки має тривати")
	h.fake.say(4242, owner, "1:30")
	h.fake.waitText(ownerChat, "1 хв 30 с")
	h.fake.press(4242, owner, "🌐 Мова")
	h.fake.press(4242, owner, "✏️ Інша мова")
	h.fake.say(4242, owner, "італійська")
	h.fake.waitText(ownerChat, "Мова: <b>Італійська</b>")
}

// TestAutopilotLiveError: «Автопілот» from the bot; the launch message
// becomes the live message and shows the error with «Повторити».
func TestAutopilotLiveError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{}) // the text model at 127.0.0.1:8317 is not there: the plan fails
	h.fake.say(4242, owner, "Пес Барбос рятує місто від повені. Він будить сусідів і веде їх на пагорб.")
	h.fake.press(4242, owner, "🎬 Так, новий проєкт")
	id := h.fake.press(4242, owner, "▶️ Запустити")
	h.fake.wait("the error in the live message", func() bool {
		t := h.fake.text(id)
		return strings.HasPrefix(t, "⚠️") && strings.Contains(t, "Автопілот")
	})
	if _, b, ok := h.fake.button(ownerChat, "🔁 Повторити"); !ok || !strings.HasPrefix(b.Data, "pr:") {
		t.Fatal("no «Повторити» under the error")
	}
	// The studio's own error notification went out too.
	h.fake.waitText(ownerChat, "⚠️ <b>«")
}

// TestLiveProgress: a job started on the computer gets a silent live
// message after LiveAfter; «Зупинити» stops it and the message says so.
func TestLiveProgress(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	text := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // the plan's text request hangs until the job is stopped
		case <-release:
		case <-r.Context().Done():
		}
		w.WriteHeader(500)
	}))
	allow(text.URL)
	t.Cleanup(func() { close(release); text.Close() })
	h := newHarness(t, hopts{
		seeds: []seed{{id: "proj000009", title: "Довгий план"}},
		cfg:   func(c *config.Config) { c.Text.BaseURL = text.URL + "/v1" },
		opt:   Options{LiveAfter: 150 * time.Millisecond},
	})
	if err := h.e.Run("proj000009", studio.StagePlan, false, ""); err != nil {
		t.Fatal(err)
	}
	live := h.fake.waitText(ownerChat, "⏳ <b>«Довгий план»</b>")
	if !live.silent || !strings.Contains(live.text, "▱") {
		t.Fatalf("live message %+v", live)
	}
	h.fake.press(4242, owner, "⏹ Зупинити")
	h.fake.waitIn(live.id, "⏹ <b>«Довгий план»</b> — План: зупинено")
	if _, b, ok := h.fake.button(ownerChat, "🔁 Продовжити"); !ok || b.Data != "pr:"+key("proj000009") {
		t.Fatal("no «Продовжити»")
	}
	// A finished job whose notification went out: its live message goes.
	h.bot.live.mu.Lock()
	h.bot.live.msgs["proj000009"] = &liveMsg{chat: ownerChat, id: live.id}
	h.bot.live.mu.Unlock()
	h.bot.live.ended("proj000009", studio.Job{Kind: studio.JobPlan, Status: studio.JobDone}, true)
	h.fake.wait("the live message deleted", func() bool { return !h.fake.exists(live.id) })
}

// TestVideoDelivery: «Надіслати відео» uploads the mp4 with streaming,
// length, size, thumbnail and cover; subtitles, the cover, frames and a
// voice line come as files.
func TestVideoDelivery(t *testing.T) {
	t.Parallel()
	video := testVideo(t, 2, "320x180", "300k")
	h := newHarness(t, hopts{seeds: []seed{{id: "proj000003", title: "Стежка додому", video: video, scenes: 12, cover: true, srt: true}}})
	k := key("proj000003")
	mk := h.bot.finalMarkup("proj000003")
	labels := ""
	for _, r := range mk {
		for _, b := range r {
			labels += b.Text + "|"
			if len(b.Data) > notify.MaxCallbackData {
				t.Fatalf("callback data %q", b.Data)
			}
		}
	}
	if labels != "⬇️ Надіслати відео сюди|📄 Субтитри .srt|🖼 Обкладинка в повній якості|📂 Відкрити проєкт|" {
		t.Fatalf("final buttons %s", labels)
	}

	h.fake.pressData(4242, owner, 0, "v:"+k)
	h.fake.wait("the video", func() bool { return len(h.fake.of("sendVideo", ownerChat)) == 1 })
	v := h.fake.of("sendVideo", ownerChat)[0]
	want, _ := os.ReadFile(video)
	if !bytes.Equal(v.files["video"].data, want) || v.files["video"].name != "Стежка додому.mp4" ||
		v.form.Get("supports_streaming") != "true" || v.form.Get("duration") != "2" ||
		v.form.Get("width") != "320" || v.form.Get("height") != "180" || !strings.Contains(v.text, "«Стежка додому»") {
		t.Fatalf("sendVideo %v %s", v.form, v.files["video"].name)
	}
	thumb := v.files["thumbnail"].data
	img, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil || img.Bounds().Dx() > 320 || len(thumb) >= 200<<10 || v.form.Get("cover") != "attach://cover_image" || len(v.files["cover_image"].data) == 0 {
		t.Fatalf("thumbnail %v (%d bytes) cover %q", err, len(thumb), v.form.Get("cover"))
	}
	h.fake.wait("the progress message to go", func() bool { return len(h.fake.of("deleteMessage", ownerChat)) == 1 })

	h.fake.pressData(4242, owner, 0, "s:"+k)
	h.fake.pressData(4242, owner, 0, "pc:"+k)
	h.fake.pressData(4242, owner, 0, "pf:"+k+":0")
	h.fake.pressData(4242, owner, 0, "pw:"+k+":1")
	h.fake.wait("the files", func() bool {
		return len(h.fake.of("sendDocument", ownerChat)) == 2 && len(h.fake.of("sendMediaGroup", ownerChat)) == 1 &&
			len(h.fake.of("sendAudio", ownerChat)) == 1
	})
	names := map[string]bool{}
	for _, d := range h.fake.of("sendDocument", ownerChat) {
		names[d.files["document"].name] = true
	}
	if !names["Стежка додому.srt"] || !names["Стежка додому — обкладинка.jpg"] {
		t.Fatalf("documents %v", names)
	}
	album := h.fake.of("sendMediaGroup", ownerChat)[0]
	if len(album.files) != 10 || !strings.Contains(album.text, "кадри 1–10 з 12") {
		t.Fatalf("album: %d files, %q", len(album.files), album.text)
	}
	h.fake.waitText(ownerChat, "Показано 10 з 12")
	h.fake.press(4242, owner, "▶️ Наступні кадри")
	h.fake.wait("the second album", func() bool {
		return len(h.fake.of("sendPhoto", ownerChat))+len(h.fake.of("sendMediaGroup", ownerChat)) == 2
	})
	a := h.fake.of("sendAudio", ownerChat)[0]
	if a.form.Get("title") != "Сцена 2" || !strings.Contains(a.text, "сцена 2") {
		t.Fatalf("voice line %v %q", a.form, a.text)
	}
}

// TestVideoOverLimit: above the upload limit the bot offers «Стиснути» and
// «Частинами»; both deliver files under the limit.
func TestVideoOverLimit(t *testing.T) {
	t.Parallel()
	video := testVideo(t, 4, "640x360", "6M")
	st, _ := os.Stat(video)
	limit := st.Size() / 3
	h := newHarness(t, hopts{seeds: []seed{{id: "proj000004", title: "Великий фільм", video: video, scenes: 1}},
		opt: Options{UploadLimit: limit}})
	h.fake.pressData(4242, owner, 0, "v:"+key("proj000004"))
	offer := h.fake.waitText(ownerChat, "важить")
	if !strings.Contains(offer.text, "Частинами") {
		t.Fatalf("offer %q", offer.text)
	}
	h.fake.press(4242, owner, "✂️ Надіслати частинами")
	h.fake.wait("the parts", func() bool {
		parts := h.fake.of("sendVideo", ownerChat)
		return len(parts) >= 3 && strings.Contains(parts[len(parts)-1].text, fmt.Sprintf("частина %d з %d", len(parts), len(parts)))
	})
	parts := h.fake.of("sendVideo", ownerChat)
	for i, p := range parts {
		if n := int64(len(p.files["video"].data)); n == 0 || n > partSize(limit) {
			t.Fatalf("part %d: %d bytes (limit %d)", i+1, n, partSize(limit))
		}
		if !strings.Contains(p.text, fmt.Sprintf("частина %d з %d", i+1, len(parts))) {
			t.Fatalf("part %d caption %q", i+1, p.text)
		}
	}
	h.fake.wait("the parts task to end", func() bool { return len(h.bot.heavy) == 0 })
	h.fake.press(4242, owner, "🗜 Стиснути")
	h.fake.wait("the compressed video", func() bool { return len(h.fake.of("sendVideo", ownerChat)) == len(parts)+1 })
	all := h.fake.of("sendVideo", ownerChat)
	c := all[len(all)-1]
	if n := int64(len(c.files["video"].data)); n == 0 || n > limit-headroom(limit) || c.form.Get("duration") != "4" {
		t.Fatalf("compressed: %d bytes (limit %d), duration %q", n, limit, c.form.Get("duration"))
	}
}

// TestVoiceAndTextFile: a voice message is recognised and confirmed; a
// Windows-1251 .txt file is read.
func TestVoiceAndTextFile(t *testing.T) {
	t.Parallel()
	stt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/audio/transcriptions") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"language": "uk", "duration": 2.0, "text": "Історія про кота, який шукав дім.",
			"words": []map[string]any{{"word": "Історія", "start": 0.0, "end": 0.5}, {"word": "про", "start": 0.5, "end": 0.7},
				{"word": "кота,", "start": 0.7, "end": 1.0}, {"word": "який", "start": 1.0, "end": 1.2}, {"word": "шукав", "start": 1.2, "end": 1.5},
				{"word": "дім.", "start": 1.5, "end": 2.0}}})
	}))
	allow(stt.URL)
	t.Cleanup(stt.Close)
	h := newHarness(t, hopts{cfg: func(c *config.Config) {
		c.STT = config.STT{Provider: "openai", BaseURL: stt.URL, APIKey: "test-stt-key", Model: "whisper-1"}
	}})
	h.fake.mu.Lock()
	h.fake.files["voice/file_1.oga"] = []byte("OggS-not-really-audio")
	h.fake.files["docs/story.txt"] = []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xB3, 0xF2, 0x20, 0xE7, 0x20, 0xF4, 0xE0, 0xE9, 0xEB, 0xF3, 0x20, 0x2D, 0x20, 0xE4, 0xEE, 0xE2, 0xE3, 0xE0, 0x20, 0xB3, 0xF1, 0xF2, 0xEE, 0xF0, 0xB3, 0xFF}
	h.fake.mu.Unlock()
	h.fake.sendMsg(notify.Message{MessageID: 1, From: &owner, Chat: notify.MsgChat{ID: 4242, Type: "private"}, Date: time.Now().Unix(),
		Voice: &notify.File{FileID: "voice/file_1.oga", Duration: 2, FileSize: 21, MimeType: "audio/ogg"}})
	heard := h.fake.waitText(ownerChat, "Ось що я почув")
	if !strings.Contains(heard.text, "Історія про кота, який шукав дім.") {
		t.Fatalf("heard %q", heard.text)
	}
	h.fake.press(4242, owner, "✅ Так, далі")
	if txt := h.fake.waitIn(heard.id, "<b>Новий проєкт</b>"); !strings.Contains(txt, "кота") {
		t.Fatalf("no options after the voice: %q", txt)
	}
	h.fake.sendMsg(notify.Message{MessageID: 2, From: &owner, Chat: notify.MsgChat{ID: 4242, Type: "private"}, Date: time.Now().Unix(),
		Document: &notify.File{FileID: "docs/story.txt", FileName: "story.txt", MimeType: "text/plain", FileSize: 30}})
	m := h.fake.waitText(ownerChat, "Зробити відео з цього тексту?")
	if !strings.Contains(m.text, "Привіт з файлу - довга історія") {
		t.Fatalf("decoded %q", m.text)
	}
}

// TestConflictAndRetryAfter: three 409s stop the poller until «Спробувати
// знову»; a webhook stops it at once; 429 waits retry_after.
func TestConflictAndRetryAfter(t *testing.T) {
	t.Parallel()
	var mode atomic.Int32 // 1 = conflict, 2 = webhook
	h := newHarness(t, hopts{noWait: true})
	h.fake.mu.Lock()
	h.fake.fail = func(method string, form url.Values) (int, string) {
		if method != "getUpdates" {
			return 0, ""
		}
		switch mode.Load() {
		case 1:
			return 409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request; make sure that only one bot instance is running"}`
		case 2:
			return 409, `{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active; use deleteWebhook to delete the webhook first"}`
		}
		return 0, ""
	}
	h.fake.mu.Unlock()
	mode.Store(1)
	h.fake.wait("the conflict stop", func() bool {
		st := h.bot.State()
		return st.Status == StatusConflict && strings.Contains(st.Note, "Спробувати знову")
	})
	n := len(h.fake.of("getUpdates", ""))
	time.Sleep(300 * time.Millisecond)
	if m := len(h.fake.of("getUpdates", "")); m != n || n > maxConflicts+1 {
		t.Fatalf("still polling after the conflict stop: %d → %d", n, m)
	}
	mode.Store(0)
	h.bot.Reconnect()
	h.fake.wait("listening after Reconnect", func() bool { return h.bot.State().Status == StatusOn })
	mode.Store(2)
	h.fake.wait("the webhook stop", func() bool { return h.bot.State().Status == StatusWebhook })
	mode.Store(0)
	h.bot.Reconnect()
	h.fake.wait("listening again", func() bool { return h.bot.State().Status == StatusOn })

	// 429 on a reply: waited out, then delivered.
	var once atomic.Bool
	h.fake.mu.Lock()
	h.fake.fail = func(method string, form url.Values) (int, string) {
		if method == "sendMessage" && once.CompareAndSwap(false, true) {
			return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`
		}
		return 0, ""
	}
	h.fake.mu.Unlock()
	h.fake.say(4242, owner, "/help")
	h.fake.waitText(ownerChat, "Що я вмію")
	sends := h.fake.of("sendMessage", ownerChat)
	if len(sends) != 2 || sends[1].at.Sub(sends[0].at) < 900*time.Millisecond {
		t.Fatalf("429 not waited out: %d sends", len(sends))
	}
}

// TestBadTokenAndOffset: a refused token stops the poller; the offset is
// saved before handling and used after a restart.
func TestBadTokenAndOffset(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{})
	h.fake.say(4242, owner, "/help")
	h.fake.waitText(ownerChat, "Що я вмію")
	b, err := os.ReadFile(filepath.Join(h.dir, stateFile))
	var st offsetState
	if err != nil || json.Unmarshal(b, &st) != nil || st.Bot != 987 || st.Offset != 2 {
		t.Fatalf("state file %s %v", b, err)
	}
	h.bot.Stop(2 * time.Second)
	n := len(h.fake.of("getUpdates", ""))
	h.bot = New(h.e, Options{StateDir: h.dir, PollTimeout: 1, Retry: 20 * time.Millisecond})
	h.bot.pace.fast = true
	h.bot.Start()
	h.fake.wait("the restarted poll", func() bool { return len(h.fake.of("getUpdates", "")) > n })
	h.fake.mu.Lock()
	first := h.fake.offsets[n]
	h.fake.mu.Unlock()
	if first != 2 {
		t.Fatalf("restart polled from %d, want 2", first)
	}
	time.Sleep(300 * time.Millisecond)
	if n := len(h.fake.of("sendMessage", ownerChat)); n != 1 {
		t.Fatalf("the command ran %d times", n)
	}

	// Another token: 401 → stopped with a note.
	_ = h.store.Update(func(c *config.Config) { c.Telegram.BotToken = "111:WRONGtoken-zzzzzzzzzz" })
	h.fake.wait("the bad token", func() bool { return h.bot.State().Status == StatusBadToken })
	if note := h.bot.State().Note; strings.Contains(note, "WRONG") {
		t.Fatalf("the token leaked into the note: %q", note)
	}
}

// TestStatusAndRemake: the health check answers without the network; the
// remake flow validates the link.
func TestStatusAndRemake(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{cfg: func(c *config.Config) { c.Google.Keys = []string{"AIzaSy-test-key-not-real-000000000000"} }})
	h.fake.say(4242, owner, "📊 Статус")
	st := h.fake.waitText(ownerChat, "<b>Сервіси</b>")
	for _, want := range []string{"YT Cartell Studio", "Текст", "Озвучка", "FFmpeg", "Диск із проєктами"} {
		if !strings.Contains(st.text, want) {
			t.Fatalf("status misses %q:\n%s", want, st.text)
		}
	}
	h.fake.say(4242, owner, "🔁 За прикладом")
	h.fake.waitText(ownerChat, "Надішли посилання на відео YouTube")
	h.fake.say(4242, owner, "це не посилання")
	h.fake.waitText(ownerChat, "не схоже на посилання")
	h.fake.say(4242, owner, "https://youtu.be/dQw4w9WgXcQ")
	h.fake.waitText(ownerChat, "Що змінити")
	h.fake.say(4242, owner, "про котів замість людей")
	opts := h.fake.waitText(ownerChat, "<b>За прикладом</b>\n▶️ youtu.be/dQw4w9WgXcQ")
	h.fake.press(4242, owner, "🎯 Точність")
	h.fake.waitIn(opts.id, "Точна копія")
	h.fake.press(4242, owner, "🔀 Режим")
	h.fake.press(4242, owner, "▶️ Запустити")
	h.fake.wait("the remake project", func() bool { return len(h.e.List()) == 1 })
	p, _ := h.e.Get(h.e.List()[0].ID)
	if p.Reference.URL == "" || p.Reference.Wish != "про котів замість людей" || p.Reference.Fidelity != studio.FidelityExact || p.Reference.Autopilot {
		t.Fatalf("remake %+v", p.Reference)
	}
}

// TestFindChatWhileListening: «Знайти мій чат» never calls getUpdates while
// the bot polls (it would cut the long poll).
func TestFindChatWhileListening(t *testing.T) {
	t.Parallel()
	h := newHarness(t, hopts{})
	h.fake.say(-100777, member, "привіт")
	h.fake.wait("the update", func() bool {
		id, _, err := h.e.FindTelegramChat(t.Context())
		return err == nil && id == "-100777"
	})
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]int{"90": 90, "90 с": 90, "2 хв": 120, "1,5 хв": 90, "1:30": 90, "5": 0, "2 год": 0, "61 хв": 0} {
		got, ok := parseLength(in)
		if got != want || ok != (want > 0) {
			t.Errorf("parseLength(%q) = %d %v", in, got, ok)
		}
	}
	for in, want := range map[string]string{"італійська": "it", "Japanese": "ja", "ko": "ko", "англ": "en", "клінгонська": ""} {
		if got, _ := findLanguage(in); got != want {
			t.Errorf("findLanguage(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"/start 123456": "123456", "/start@bot 123 456": "123456", "123-456": "123456", "/start p_abc": "", "1234567": "", "/start": ""} {
		if got := pairCode(in); got != want {
			t.Errorf("pairCode(%q) = %q", in, got)
		}
	}
	if menuAction("📁 Проєкти") != "l" || menuAction("проєкти") != "l" || menuAction("⚙️ Профілі") != "f" || menuAction("проєкти мої") != "" {
		t.Error("menuAction")
	}
	if k := key(strings.Repeat("x", 64)); len(k) != 8 || k == key("y") {
		t.Errorf("key %q", k)
	}
	if bar(42) != "▰▰▰▰▱▱▱▱▱▱ 42 %" || bar(100) != "▰▰▰▰▰▰▰▰▰▰ 100 %" {
		t.Errorf("bar %q", bar(42))
	}
	if titleFrom("Кіт Мурчик шукав дім. Потім…") != "Кіт Мурчик шукав дім" {
		t.Errorf("title %q", titleFrom("Кіт Мурчик шукав дім. Потім…"))
	}
	if decodeText([]byte{0xFF, 0xFE, 0x1F, 0x04, 0x40, 0x04}) != "Пр" || decodeText([]byte{0xEF, 0xBB, 0xBF, 'h', 'i'}) != "hi" {
		t.Error("decodeText")
	}
	// The re-encode budget: 48 MB over 5 min of 1080p → 720p and ~1.2 Mbit/s.
	p, err := planEncode(48<<20, 300, 1920, 1080, true)
	if err != nil || p.scale != "scale=-2:720" || p.video < 1000 || p.video > 1300 || p.audio != 96 {
		t.Errorf("plan %+v %v", p, err)
	}
	if p, _ := planEncode(48<<20, 60, 1080, 1920, true); p.scale != "" || p.audio != 128 {
		t.Errorf("short vertical film: %+v", p)
	}
	if p, _ := planEncode(48<<20, 600, 1080, 1920, true); p.scale != "scale=480:-2" {
		t.Errorf("long vertical film: %+v", p)
	}
	if _, err := planEncode(48<<20, 3*3600, 1920, 1080, true); err == nil {
		t.Error("a 3-hour film squeezed into 48 MB")
	}
	if headroom(notify.MaxUpload) != 1<<20 || partSize(notify.MaxUpload) != 49<<20 {
		t.Error("headroom")
	}
}

func TestPacer(t *testing.T) {
	p := newPacer()
	for i := 0; i < burst; i++ {
		if !p.try("5") {
			t.Fatalf("burst message %d waits", i)
		}
	}
	if p.try("5") {
		t.Fatal("a fourth message at once")
	}
	if d, _ := p.reserve("5", true); d < 500*time.Millisecond || d > gapPrivate {
		t.Fatalf("private wait %s", d)
	}
	g := "-100"
	if !p.try(g) || p.try(g) {
		t.Fatal("groups: one message, then a gap")
	}
	var last time.Duration
	for i := 1; i < groupMax; i++ {
		last, _ = p.reserve(g, true)
	}
	if last < time.Duration(groupMax-2)*gapGroup {
		t.Fatalf("group spacing %s", last)
	}
	if d, _ := p.reserve(g, true); d < groupWindow-time.Second {
		t.Fatalf("the 20th group message in a minute waits only %s", d)
	}
	p.block("7", 2*time.Second)
	if p.try("7") {
		t.Fatal("a blocked chat sends")
	}
	_ = strconv.Itoa
}

// TestGroup: paired in a group, the owner and the allowed users control it,
// other members are refused (and listed for Settings); the menu is inline.
func TestGroup(t *testing.T) {
	t.Parallel()
	const group = -1001234
	h := newHarness(t, hopts{seeds: []seed{{id: "proj000007", title: "Груповий", scenes: 1}},
		tg: func(tg *config.Telegram) { tg.ChatID = strconv.Itoa(group) }})
	gchat := strconv.Itoa(group)
	h.fake.say(group, owner, "/start@studio_test_bot")
	m := h.fake.waitText(gchat, "Привіт")
	if m.reply || len(m.kb) == 0 {
		t.Fatalf("group menu: reply keyboard %v, inline rows %d", m.reply, len(m.kb))
	}
	h.fake.say(group, member, "/projects")
	h.fake.wait("the member refused and listed", func() bool {
		st := h.bot.State()
		return len(st.Attempts) == 1 && st.Attempts[0].ID == member.ID && len(h.fake.of("sendMessage", gchat)) == 2
	})
	if last := h.fake.last(gchat); last.text != textPrivate {
		t.Fatalf("member got %q", last.text)
	}
	_ = h.store.Update(func(c *config.Config) {
		c.Telegram.Allowed = []config.TelegramUser{{ID: member.ID, Name: member.Name()}}
	})
	h.fake.say(group, member, "/projects")
	h.fake.waitText(gchat, "<b>Проєкти</b> · 1")
	h.fake.press(group, member, "📝 Груповий")
	h.fake.waitText(gchat, "📂 <b>«Груповий»</b>")
	// A command for another bot is not ours: no refusal, no answer.
	n := len(h.fake.of("sendMessage", gchat))
	h.fake.say(group, stranger, "/start@other_bot")
	time.Sleep(300 * time.Millisecond)
	if len(h.fake.of("sendMessage", gchat)) != n {
		t.Fatal("answered a command for another bot")
	}
}
