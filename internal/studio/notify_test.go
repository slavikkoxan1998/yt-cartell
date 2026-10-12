package studio

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
)

// Never a real token: the bot below is an httptest mock.
const testBotToken = "123456789:TESTtoken-abcdefghijkl"

type tgMsg struct {
	method, chat, text, parseMode string
	photo                         []byte
}

// tgBot mocks the Bot API for testBotToken.
type tgBot struct {
	mu   sync.Mutex
	msgs []tgMsg
}

func (b *tgBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	prefix := "/bot" + testBotToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	switch method := strings.TrimPrefix(r.URL.Path, prefix); method {
	case "getMe":
		io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Студія","username":"studio_test_bot"}}`)
	case "getUpdates":
		io.WriteString(w, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":3,"text":"/start",
			"chat":{"id":4242,"type":"private","first_name":"Іван","username":"ivan"}}}]}`)
	case "sendMessage", "sendPhoto":
		m := tgMsg{method: method}
		if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
			if err := r.ParseMultipartForm(32 << 20); err == nil {
				m.chat, m.text, m.parseMode = r.FormValue("chat_id"), r.FormValue("caption"), r.FormValue("parse_mode")
				if f, _, err := r.FormFile("photo"); err == nil {
					m.photo, _ = io.ReadAll(f)
					f.Close()
				}
			}
		} else {
			_ = r.ParseForm()
			m.chat, m.text, m.parseMode = r.PostForm.Get("chat_id"), r.PostForm.Get("text"), r.PostForm.Get("parse_mode")
		}
		b.mu.Lock()
		b.msgs = append(b.msgs, m)
		b.mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"ok":false,"error_code":404,"description":"Not Found"}`)
	}
}

func (b *tgBot) all() []tgMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]tgMsg(nil), b.msgs...)
}

// newTelegram points the engine at a mock bot and turns notifications on.
func newTelegram(t *testing.T, env *testEnv) *tgBot {
	t.Helper()
	bot := &tgBot{}
	srv := httptest.NewServer(bot)
	allow(srv)
	t.Cleanup(srv.Close)
	ts := env.e.tele()
	ts.mu.Lock()
	ts.base = srv.URL
	ts.mu.Unlock()
	setTelegram(env, func(c *config.Telegram) {
		*c = config.Telegram{Enabled: true, BotToken: testBotToken, ChatID: "777", OnDone: true, OnError: true, OnFinal: true}
	})
	return bot
}

func setTelegram(env *testEnv, fn func(c *config.Telegram)) {
	_ = env.store.Update(func(c *config.Config) { fn(&c.Telegram) }) // in-memory store: nothing is written
}

// waitNotices waits until every job-end notification of e is sent.
func waitNotices(t *testing.T, e *Engine) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for e.tele().pending.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("telegram notifications did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// expectMsgs checks how many messages the bot has and returns the last one.
func expectMsgs(t *testing.T, bot *tgBot, n int, after string) tgMsg {
	t.Helper()
	msgs := bot.all()
	if len(msgs) != n {
		var texts []string
		for _, m := range msgs {
			texts = append(texts, m.method+": "+m.text)
		}
		t.Fatalf("%s: want %d messages, got %d:\n%s", after, n, len(msgs), strings.Join(texts, "\n"))
	}
	if n == 0 {
		return tgMsg{}
	}
	m := msgs[n-1]
	if m.chat != "777" || m.parseMode != "HTML" {
		t.Fatalf("%s: chat %q parse mode %q", after, m.chat, m.parseMode)
	}
	return m
}

// TestTelegramJobNotices: one message per finished job with the right
// template; nothing for cancelled jobs or when notifications are off.
func TestTelegramJobNotices(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	bot := newTelegram(t, env)

	// A finished stage.
	p, _ := e.Create("")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text = "Маленький хлопчик Тарас загубився в осінньому лісі."
		p.Input.TargetSeconds = 24
	}))
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	waitNotices(t, e)
	m := expectMsgs(t, bot, 1, "plan")
	want := "✅ <b>«Стежка додому»</b> — План: План готовий: 3 сцен ≈ 0:24 ("
	if m.method != "sendMessage" || !strings.HasPrefix(m.text, want) || !strings.HasSuffix(m.text, ")") {
		t.Fatalf("done message %s %q", m.method, m.text)
	}

	// A failed stage; the title is escaped.
	q, _ := e.Create("Том & <Джеррі>")
	must(t, e.Run(q.ID, StageMontage, false, ""))
	waitJob(t, e, q.ID)
	waitNotices(t, e)
	m = expectMsgs(t, bot, 2, "failed montage")
	if m.text != "⚠️ <b>«Том &amp; &lt;Джеррі&gt;»</b> — Монтаж: Немає жодного кліпу чи кадру — спершу згенеруй відео" {
		t.Fatalf("error message %q", m.text)
	}

	// A cancelled job says nothing.
	drain(env.text.started)
	blockText(env, true)
	must(t, e.Run(p.ID, StagePlan, true, ""))
	waitSignal(t, env.text.started, "the bible request")
	e.Cancel(p.ID)
	if p = waitJob(t, e, p.ID); p.Job.Status != JobCancelled {
		t.Fatalf("job %+v", p.Job)
	}
	blockText(env, false)
	waitNotices(t, e)
	expectMsgs(t, bot, 2, "cancelled plan")

	// Switches: OnError off, notifications off, no chat.
	for i, off := range []func(c *config.Telegram){
		func(c *config.Telegram) { c.OnError = false },
		func(c *config.Telegram) { c.OnError, c.Enabled = true, false },
		func(c *config.Telegram) { c.Enabled, c.ChatID = true, "" },
		func(c *config.Telegram) { c.ChatID, c.BotToken = "777", "" },
	} {
		setTelegram(env, off)
		must(t, e.Run(q.ID, StageMontage, false, ""))
		waitJob(t, e, q.ID)
		waitNotices(t, e)
		expectMsgs(t, bot, 2, fmt.Sprintf("switch %d", i))
	}
	// OnDone off: a stage that finished says nothing either.
	setTelegram(env, func(c *config.Telegram) { c.BotToken, c.OnDone = testBotToken, false })
	must(t, e.Run(p.ID, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	waitNotices(t, e)
	expectMsgs(t, bot, 2, "OnDone off")
	// …while errors still arrive.
	must(t, e.Run(q.ID, StageMontage, false, ""))
	waitJob(t, e, q.ID)
	waitNotices(t, e)
	expectMsgs(t, bot, 3, "OnDone off, error")
}

// TestTelegramFinalVideo: a finished montage sends the cover with the
// "video is ready" caption instead of the plain message.
func TestTelegramFinalVideo(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	bot := newTelegram(t, env)
	p := seedScenes(t, e, 2)
	must(t, e.Edit(p.ID, func(p *Project) { p.Title = "Кіно <№1> & Ко" }))

	montage := func() Project {
		t.Helper()
		// The scenes have frames only: the user chose «З кадром замість відео».
		must(t, e.RunWith(p.ID, StageMontage, RunOptions{Missing: MissingStill}))
		q := waitJob(t, e, p.ID)
		mustJobDone(t, q)
		waitNotices(t, e)
		return q
	}
	caption := func(q Project) string {
		sec := int(q.Montage.Output.Duration + 0.5)
		// Every scene shown as a still is listed with why — never silently.
		gaps := "\n⚠️ Не всі сцени як у плані:" +
			"\n• Сцена 1 (кадр замість відео) — кліп ще не знято; озвучку ще не зроблено" +
			"\n• Сцена 2 (кадр замість відео) — кліп ще не знято; озвучку ще не зроблено"
		return fmt.Sprintf("🎬 Відео готове: <b>«Кіно &lt;№1&gt; &amp; Ко»</b> · %d:%02d · 16:9\n<code>%s</code>",
			sec/60, sec%60, notify.Escape(e.Abs(q.ID, q.Montage.Output.Path))) + gaps
	}

	// No cover yet: the first scene frame.
	q := montage()
	m := expectMsgs(t, bot, 1, "montage")
	frame, _ := os.ReadFile(e.Abs(q.ID, q.Scenes[0].Frame.Path))
	if m.method != "sendPhoto" || m.text != caption(q) || len(frame) == 0 || string(m.photo) != string(frame) {
		t.Fatalf("final message %s %q (%d photo bytes, frame %d)", m.method, m.text, len(m.photo), len(frame))
	}

	// With a cover and its text version, the text version wins.
	st := e.state(p.ID)
	cover, err := e.writeAsset(st, "images", "cover-test.jpg", testImage)
	must(t, err)
	final, err := e.writeAsset(st, "images", "cover-final-test.jpg", append(append([]byte(nil), testImage...), 0))
	must(t, err)
	e.update(st, func(p *Project) {
		p.Montage.Cover = Asset{Path: cover, Status: StatusDone, UpdatedAt: nowMs()}
		p.Montage.CoverFinal = Asset{Path: final, Status: StatusDone, UpdatedAt: nowMs()}
	})
	q = montage()
	m = expectMsgs(t, bot, 2, "montage with a cover")
	want, _ := os.ReadFile(e.Abs(q.ID, final))
	if m.method != "sendPhoto" || m.text != caption(q) || string(m.photo) != string(want) {
		t.Fatalf("final message with a cover %s %q", m.method, m.text)
	}

	// OnFinal off: the plain "done" message.
	setTelegram(env, func(c *config.Telegram) { c.OnFinal = false })
	q = montage()
	m = expectMsgs(t, bot, 3, "OnFinal off")
	if m.method != "sendMessage" || !strings.HasPrefix(m.text, "✅ <b>«Кіно &lt;№1&gt; &amp; Ко»</b> — Монтаж: "+q.Job.Message+" (") {
		t.Fatalf("done message %s %q", m.method, m.text)
	}
}

func TestTelegramSettings(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	ctx := context.Background()
	if ok, msg := e.TestTelegram(ctx); ok || msg != "Вкажи токен бота — його видає @BotFather" {
		t.Fatalf("no token: %v %q", ok, msg)
	}
	if _, _, err := e.FindTelegramChat(ctx); err == nil || !strings.Contains(err.Error(), "вкажи токен бота") {
		t.Fatalf("find without a token: %v", err)
	}
	bot := newTelegram(t, env)
	ok, msg := e.TestTelegram(ctx)
	if !ok || msg != "Тестове повідомлення надіслано — бот @studio_test_bot" {
		t.Fatalf("test: %v %q", ok, msg)
	}
	if m := expectMsgs(t, bot, 1, "test"); m.method != "sendMessage" || m.text != "✅ YT Cartell Studio підключено" {
		t.Fatalf("test message %+v", m)
	}
	setTelegram(env, func(c *config.Telegram) { c.Enabled = false })
	if ok, msg = e.TestTelegram(ctx); !ok || !strings.Contains(msg, "Увімкни сповіщення") {
		t.Fatalf("test while off: %v %q", ok, msg)
	}
	id, title, err := e.FindTelegramChat(ctx)
	if err != nil || id != "4242" || title != "Іван (@ivan)" {
		t.Fatalf("find: %q %q %v", id, title, err)
	}

	setTelegram(env, func(c *config.Telegram) { c.ChatID = "" })
	if ok, msg = e.TestTelegram(ctx); ok || msg != "Вкажи ID чату або натисни «Знайти мій чат»" {
		t.Fatalf("no chat: %v %q", ok, msg)
	}
	other := "987654321:OTHERtoken-zyxwvutsrq"
	setTelegram(env, func(c *config.Telegram) { c.ChatID, c.BotToken = "777", other })
	ok, msg = e.TestTelegram(ctx)
	if ok || !strings.HasPrefix(msg, "Токен бота не підходить") || strings.Contains(msg, "OTHERtoken") {
		t.Fatalf("wrong token: %v %q", ok, msg)
	}
	expectMsgs(t, bot, 2, "settings")
}

func TestNoticeHelpers(t *testing.T) {
	cases := [][2]string{
		{clock(0), ""}, {clock(62.4), "1:02"}, {clock(59.6), "1:00"}, {clock(3723), "1:02:03"},
		{jobTime(300), "менше секунди"}, {jobTime(45_000), "45 с"}, {jobTime(185_000), "3 хв 5 с"},
		{jobTime(120_000), "2 хв"}, {jobTime(4_800_000), "1 год 20 хв"}, {jobTime(7_200_000), "2 год"},
	}
	for _, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%q, want %q", c[0], c[1])
		}
	}
	st := &proj{id: "p", dir: "/data/p"}
	p := &Project{Title: "T", Input: Input{Aspect: "9:16"}, Scenes: []Scene{{}, {Frame: Asset{Path: "images/f2.jpg", Status: StatusDone}}}}
	p.Montage.Output = Asset{Path: "montage/final.mp4", Status: StatusDone, Duration: 62}
	p.Montage.Cover = Asset{Path: "images/cover.jpg", Status: StatusDone}
	p.Montage.CoverFinal = Asset{Path: "images/cover-final.jpg", Status: StatusError}
	n := noticeOf(st, p)
	if n.video != absIn(st.dir, "montage/final.mp4") || n.videoDur != 62 || n.aspect != "9:16" ||
		len(n.photos) != 2 || n.photos[0] != absIn(st.dir, "images/cover.jpg") || n.photos[1] != absIn(st.dir, "images/f2.jpg") {
		t.Fatalf("notice %+v", n)
	}
	p.Montage.Output.UpdatedAt = 5000
	n = noticeOf(st, p)
	for _, c := range []struct {
		job  Job
		want bool
	}{
		{Job{Kind: JobMontage, Status: JobDone, StartedAt: 9000}, true},
		{Job{Kind: JobAutopilot, Status: JobDone, StartedAt: 9000}, true},
		{Job{Kind: "translate", Status: JobDone, StartedAt: 4000}, true}, // rendered the film during its run
		{Job{Kind: "translate", Status: JobDone, StartedAt: 9000}, false},
		{Job{Kind: JobVoices, Status: JobDone, StartedAt: 9000}, false},
		{Job{Kind: JobMontage, Status: JobError, StartedAt: 1}, false},
	} {
		if madeFinal(c.job, n) != c.want {
			t.Errorf("madeFinal(%+v) != %v", c.job, c.want)
		}
	}
	p.Montage.Output.Status = StatusError
	if n = noticeOf(st, p); n.video != "" || madeFinal(Job{Kind: JobMontage, Status: JobDone}, n) {
		t.Fatalf("a failed output is no final video: %+v", n)
	}
}
