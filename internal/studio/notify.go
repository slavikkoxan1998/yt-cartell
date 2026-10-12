package studio

// Telegram notifications about finished jobs (Settings → «Сповіщення
// Telegram»), sent through the user's own bot with internal/notify:
//
//	error (OnError)  ⚠️ «Title» — <label>: <error>
//	done  (OnDone)   ✅ «Title» — <label>: <message> (<how long the job took>)
//	final (OnFinal)  a montage or autopilot job (or another job that rendered
//	                 the film again, e.g. a translation) left a final video:
//	                 its cover (CoverFinal, else Cover, else the first scene
//	                 frame) with "🎬 Відео готове: «Title» · 1:02 · 16:9" and
//	                 the mp4 path; it replaces the plain "done" message
//
// With the remote control on (package tgbot, remote.go) the final message
// carries its buttons (send the video here, subtitles, cover, open the
// project), an error message the bot's buttons for it (the montage choices
// of an autopilot that stopped before the montage) and the bot hears about
// every finished job (TelegramHooks).
//
// A cancelled job sends nothing, and nothing is sent while notifications are
// off or the token or chat is missing. Sending never blocks or fails a job:
// runJob hands the finished job over and a goroutine does the rest; failures
// are only logged, without the token.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
)

// teleState is the notifier state of one engine (the Engine struct lives in
// engine.go).
type teleState struct {
	mu   sync.Mutex
	base string // Bot API base URL; "" = api.telegram.org (tests use a mock)
	// pending counts job-end notifications that are not finished yet.
	pending atomic.Int64
	// hooks connect the remote control (remote.go); guarded by mu.
	hooks TelegramHooks
}

var teleStates sync.Map // *Engine → *teleState

func (e *Engine) tele() *teleState {
	if v, ok := teleStates.Load(e); ok {
		return v.(*teleState)
	}
	v, _ := teleStates.LoadOrStore(e, &teleState{})
	return v.(*teleState)
}

// telegram is a Bot API client for token.
func (e *Engine) telegram(token string) *notify.Client {
	t := e.tele()
	t.mu.Lock()
	base := t.base
	t.mu.Unlock()
	if base == "" {
		base = e.cfg.Get().Telegram.APIServer // "" = api.telegram.org
	}
	return notify.New(token, base)
}

// TestTelegram checks the bot token and sends "✅ YT Cartell Studio
// підключено" to the chat from Settings (the «Перевірити» button); when
// something is missing it says what.
func (e *Engine) TestTelegram(ctx context.Context) (bool, string) {
	c := e.cfg.Get().Telegram
	switch {
	case c.BotToken == "":
		return false, "Вкажи токен бота — його видає @BotFather"
	case c.ChatID == "":
		return false, "Вкажи ID чату або натисни «Знайти мій чат»"
	}
	client := e.telegram(c.BotToken)
	name, err := client.GetMe(ctx)
	if err == nil {
		err = client.SendMessage(ctx, c.ChatID, "✅ YT Cartell Studio підключено")
	}
	if err != nil {
		return false, clipRunes(notify.Redact(err.Error(), c.BotToken), 300)
	}
	msg := "Тестове повідомлення надіслано — бот @" + name
	if !c.Enabled {
		msg += ". Увімкни сповіщення, щоб отримувати їх"
	}
	return true, msg
}

// FindTelegramChat returns the chat that wrote to the bot most recently
// («Знайти мій чат» after the user sent /start to the bot). The settings are
// not changed: the UI puts chatID into the chat field and saves it.
func (e *Engine) FindTelegramChat(ctx context.Context) (chatID, title string, err error) {
	token := e.cfg.Get().Telegram.BotToken
	if token == "" {
		return "", "", errors.New("Спершу вкажи токен бота — його видає @BotFather")
	}
	if find := e.telegramHooks().FindChat; find != nil {
		return find(ctx)
	}
	ch, err := e.telegram(token).FindChat(ctx)
	if err != nil {
		return "", "", err
	}
	return ch.ID, ch.Title, nil
}

// madeFinal: the finished job left a final video — a montage or autopilot
// job, or any job that rendered the film during its run.
func madeFinal(job Job, n notice) bool {
	return job.Status == JobDone && n.video != "" &&
		(job.Kind == JobMontage || job.Kind == JobAutopilot || n.videoAt >= job.StartedAt)
}

// jobNotice registers a job-end notification before the job's final state
// becomes visible (so waiting for notifications cannot race the job's end)
// and returns what runJob calls, after the save, with the finished job (nil
// when this run did not finish it).
func (e *Engine) jobNotice() func(st *proj, final *Job) {
	t := e.tele()
	t.pending.Add(1)
	return func(st *proj, final *Job) {
		async := false
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[studio:%s] telegram: %v", st.id, r)
			}
			if !async {
				t.pending.Add(-1)
			}
		}()
		if final == nil {
			return // not finished by this run (or deleted)
		}
		job := *final
		if job.Status != JobDone && job.Status != JobError {
			e.jobEnded(st.id, job, false) // stopped: nothing to say
			return
		}
		tg := e.cfg.Get().Telegram
		switch {
		case !tg.Enabled || tg.BotToken == "" || tg.ChatID == "",
			job.Status == JobError && !tg.OnError,
			job.Status == JobDone && !tg.OnDone && !tg.OnFinal:
			e.jobEnded(st.id, job, false)
			return
		}
		async = true
		go func() {
			defer t.pending.Add(-1)
			sent := false
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[studio:%s] telegram: %v", st.id, r)
				}
				e.jobEnded(st.id, job, sent)
			}()
			sent = e.sendJobNotice(st, job, tg)
		}()
	}
}

// notice is what a job-end message needs from the project.
type notice struct {
	title, aspect string
	grid          bool
	video         string   // absolute path of the final mp4, "" if none
	videoDur      float64  // seconds
	videoAt       int64    // when it was rendered (Asset.UpdatedAt)
	photos        []string // cover candidates, best first (absolute)
	// gaps are the scenes the film left out or replaced (Montage.Gaps);
	// failed lists every failed item of the job with its exact error.
	gaps   []MontageGap
	failed []string
}

func noticeOf(st *proj, p *Project) notice {
	n := notice{title: p.Title, aspect: p.Input.Aspect, grid: p.Input.StoryboardMode == StoryboardGrid}
	if o := p.Montage.Output; o.Status == StatusDone && o.Path != "" {
		n.video, n.videoDur, n.videoAt = absIn(st.dir, o.Path), o.Duration, o.UpdatedAt
		n.gaps = append(n.gaps, p.Montage.Gaps...)
	}
	if p.Job != nil {
		n.failed = failureLines(p, p.Job.Kind)
	}
	for _, a := range []Asset{p.Montage.CoverFinal, p.Montage.Cover} {
		if a.Status == StatusDone && a.Path != "" {
			n.photos = append(n.photos, absIn(st.dir, a.Path))
		}
	}
	for _, s := range p.Scenes {
		if s.Frame.Path != "" {
			n.photos = append(n.photos, absIn(st.dir, s.Frame.Path))
			break
		}
	}
	return n
}

// sendJobNotice builds and sends the message for a finished job; true when
// a message went out.
func (e *Engine) sendJobNotice(st *proj, job Job, tg config.Telegram) bool {
	var n notice
	if !e.read(st, func(p *Project) bool { n = noticeOf(st, p); return true }) {
		return false // deleted meanwhile
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*notify.Timeout+5*time.Second)
	defer cancel()
	client := e.telegram(tg.BotToken)
	title := "<b>«" + notify.Escape(n.title) + "»</b>"
	label := notify.Escape(queueJobLabel(job.Kind, n.grid))
	var err error
	sent := true
	switch {
	case job.Status == JobError:
		reason := job.Error
		if reason == "" {
			reason = job.Message
		}
		text := fmt.Sprintf("⚠️ %s — %s: %s", title, label, notify.Escape(reason))
		if h := FailureHint(reason); h != "" {
			text += "\n💡 " + notify.Escape(h)
		}
		// With the remote control on, its buttons (an autopilot stopped before
		// the montage: the montage choices).
		html := notify.ClipHTML(text+failedBlock(n.failed), notify.MaxMessage)
		if kb := e.errorMarkup(st.id, job); len(kb) > 0 {
			_, err = client.Send(ctx, tg.ChatID, html, &notify.SendOptions{Markup: kb})
		} else {
			err = client.SendMessage(ctx, tg.ChatID, html)
		}
	case tg.OnFinal && madeFinal(job, n) && isFile(n.video):
		err = e.sendFinal(ctx, st, client, tg.ChatID, n, title)
	case tg.OnDone:
		var opt *notify.SendOptions
		if madeFinal(job, n) {
			opt = &notify.SendOptions{Markup: e.finalMarkup(st.id)}
		}
		text := fmt.Sprintf("✅ %s — %s: %s (%s)", title, label, notify.Escape(job.Message), jobTime(job.FinishedAt-job.StartedAt))
		_, err = client.Send(ctx, tg.ChatID, notify.ClipHTML(text+failedBlock(n.failed), notify.MaxMessage), opt)
	default:
		sent = false
	}
	if err != nil {
		log.Printf("[studio:%s] telegram: %s", st.id, notify.Redact(err.Error(), tg.BotToken))
	}
	return sent && err == nil
}

// sendFinal sends the "video is ready" photo; without a usable picture the
// same text goes as a message.
func (e *Engine) sendFinal(ctx context.Context, st *proj, client *notify.Client, chat string, n notice, title string) error {
	caption := "🎬 Відео готове: " + title
	if d := clock(n.videoDur); d != "" {
		caption += " · " + d
	}
	if n.aspect != "" {
		caption += " · " + notify.Escape(n.aspect)
	}
	caption += "\n<code>" + notify.Escape(n.video) + "</code>"
	if len(n.gaps) > 0 {
		// Never a silent replacement: every scene left out or shown as a
		// still, with why (the full list follows as a message when long).
		var b strings.Builder
		b.WriteString("\n⚠️ Не всі сцени як у плані:")
		for _, g := range n.gaps {
			b.WriteString("\n• " + notify.Escape(g.Line()))
		}
		gaps := b.String()
		if len([]rune(caption+gaps)) <= notify.MaxCaption {
			caption += gaps
		} else {
			caption += fmt.Sprintf("\n⚠️ Не всі сцени як у плані (%d) — список нижче.", len(n.gaps))
			defer func() {
				if _, err := client.Send(ctx, chat, notify.ClipHTML(title+gaps, notify.MaxMessage), nil); err != nil {
					log.Printf("[studio:%s] telegram: %s", st.id, notify.Redact(err.Error(), ""))
				}
			}()
		}
	}
	opt := &notify.SendOptions{Markup: e.finalMarkup(st.id)} // the remote control's buttons (remote.go)
	for _, photo := range n.photos {
		if !isFile(photo) {
			continue
		}
		_, err := client.SendPhotoWith(ctx, chat, photo, caption, opt)
		var te *notify.Error
		if err == nil || !(errors.Is(err, notify.ErrPhoto) || errors.As(err, &te) && (te.Code == 400 || te.Code == 413)) {
			return err
		}
		log.Printf("[studio:%s] telegram: the cover was not accepted (%s), sending the text", st.id, notify.Redact(err.Error(), ""))
		break
	}
	_, err := client.Send(ctx, chat, caption, opt)
	return err
}

// clock is a video length as "1:02" ("1:02:03" from an hour), "" for 0.
func clock(sec float64) string {
	s := int(math.Round(sec))
	switch {
	case s <= 0:
		return ""
	case s >= 3600:
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// jobTime is how long a job took, in plain words: "45 с", "3 хв 5 с",
// "1 год 20 хв".
func jobTime(ms int64) string {
	s := int(math.Round(float64(ms) / 1000))
	switch {
	case s < 1:
		return "менше секунди"
	case s < 60:
		return fmt.Sprintf("%d с", s)
	case s < 3600:
		if s%60 == 0 {
			return fmt.Sprintf("%d хв", s/60)
		}
		return fmt.Sprintf("%d хв %d с", s/60, s%60)
	}
	if m := s / 60 % 60; m > 0 {
		return fmt.Sprintf("%d год %d хв", s/3600, m)
	}
	return fmt.Sprintf("%d год", s/3600)
}

// failureLines are the failed items a job of kind k left, each with its
// exact error (and a hint): "Сцена 3 · кліп: G-Labs: PROMINENT_PERSON".
func failureLines(p *Project, k JobKind) []string {
	kinds, known := jobAssets[k]
	if !known {
		kinds = []RegenKind{RegenCharacter, RegenLocation, RegenFrame, RegenVideo, RegenVoice}
	}
	var out []string
	add := func(who string, a Asset) {
		if a.Status != StatusError {
			return
		}
		line := who + ": " + firstNonBlank(strings.TrimSpace(a.Error), "сервіс не повернув текст помилки")
		if h := FailureHint(a.Error); h != "" {
			line += " (" + h + ")"
		}
		out = append(out, line)
	}
	for _, r := range kinds {
		switch r {
		case RegenCharacter:
			for _, c := range p.Characters {
				add("Персонаж «"+c.Name+"»", c.Image)
			}
		case RegenLocation:
			for _, l := range p.Locations {
				add("Локація «"+l.Name+"»", l.Image)
			}
		case RegenFrame:
			for i, s := range p.Scenes {
				add(fmt.Sprintf("Сцена %d · кадр", i+1), s.Frame)
			}
		case RegenVideo:
			for i, s := range p.Scenes {
				add(fmt.Sprintf("Сцена %d · кліп", i+1), s.Video)
			}
		case RegenVoice:
			for i, s := range p.Scenes {
				add(fmt.Sprintf("Сцена %d · озвучка", i+1), s.Audio)
			}
		}
	}
	return out
}

// failedBlock is the HTML list of failed items under a message ("" = none).
func failedBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n❌ Не вдалося:")
	for _, l := range lines {
		b.WriteString("\n• " + notify.Escape(clipRunes(l, 600)))
	}
	return b.String()
}
