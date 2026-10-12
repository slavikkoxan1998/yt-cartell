package tgbot

// Routing: every update is authorised in dispatch (on the poller's
// goroutine, cheap); the owner's updates go to one worker per chat, in
// order, which owns that chat's session (the flow in progress, drafts,
// confirmations). Every button press is answered (the spinner stops).
//
// Callback data (≤ 64 bytes; <k> = a project key, text.go):
//
//	m h st new rmk q qa xn          menu, help, status, new project, remake,
//	                                queue, stop all (asks), cancel a question
//	l:<page> f:<page> fs:<pk|->     projects list, profiles, set the default
//	p pa pn ps pr pm pc pd :<k>     card, autopilot, next stage, stop, retry,
//	                                montage, cover, delete (asks)
//	pf:<k>:<from> pv:<k>:<page>     frames album, voice lines
//	pw:<k>:<scene>                  one voice line as audio
//	gs gf gr gx :<k>                scenes not ready for the montage: without
//	                                them, frames instead, re-shoot, stop (gaps.go)
//	v vz vp s qc :<k>               video, compressed, in parts, subtitles,
//	                                stop from the queue
//	x:<token>                       a confirmed question
//	n:<draft>:<field>[:<value>]     the new-project draft (newproject.go)
//	r:<draft>:<field>[:<value>]     the remake draft (remake.go)
//	nw nx                           make a project from the text just sent / not

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

// Flows: what the next text message is for.
const (
	flowNone       = ""
	flowNewText    = "new.text"
	flowNewLen     = "new.len"
	flowNewLang    = "new.lang"
	flowRemakeLink = "remake.link"
	flowRemakeWish = "remake.wish"
)

const confirmTTL = 2 * time.Minute

type session struct {
	chat    string
	flow    string
	draft   *draft
	remake  *remakeDraft
	pending string // a text waiting for «Створити проєкт?»
	confirm *confirm
}

type confirm struct {
	tok, action, pid string
	until            time.Time
}

type job struct {
	u      notify.Update
	paired bool
}

type worker struct{ ch chan job }

// dispatch authorises an update and hands it to its chat's worker.
func (b *Bot) dispatch(u notify.Update) {
	switch {
	case u.CallbackQuery != nil:
		q := u.CallbackQuery
		if q.Message == nil { // inline-mode messages: the bot has none
			b.refuse(denial{queryID: q.ID, text: textPrivate})
			return
		}
		b.noteChat(q.Message.Chat)
		if !b.allowed(q.Message.Chat, q.From) {
			b.noteAttempt(q.Message.Chat, q.From)
			b.refuse(denial{queryID: q.ID, text: textPrivate})
			return
		}
		b.enqueue(q.Message.Chat.ID, job{u: u})
	case u.Message != nil:
		m := u.Message
		if m.From == nil || m.From.IsBot {
			return
		}
		b.noteChat(m.Chat)
		if b.tryPair(m) {
			b.enqueue(m.Chat.ID, job{u: u, paired: true})
			return
		}
		if !b.allowed(m.Chat, *m.From) {
			b.noteAttempt(m.Chat, *m.From)
			if _, _, cmd := b.command(strings.TrimSpace(m.Text)); m.Chat.Type == "private" || cmd {
				b.refuse(denial{chat: notify.ChatID(m.Chat.ID), text: textPrivate})
			}
			return
		}
		b.enqueue(m.Chat.ID, job{u: u})
	}
}

// noteChat remembers the last chat that wrote («Знайти мій чат»).
func (b *Bot) noteChat(c notify.MsgChat) {
	b.mu.Lock()
	b.lastChat = notify.Chat{ID: notify.ChatID(c.ID), Title: c.Name(), Type: c.Type}
	b.mu.Unlock()
}

func (b *Bot) enqueue(chat int64, j job) {
	b.mu.Lock()
	w := b.workers[chat]
	if w == nil {
		w = &worker{ch: make(chan job, 64)}
		b.workers[chat] = w
		s := &session{chat: notify.ChatID(chat)}
		b.sessions[chat] = s
		b.wg.Add(1)
		go b.work(s, w)
	}
	b.mu.Unlock()
	select {
	case w.ch <- j:
	default:
		log.Printf("[tgbot] chat %d: too many updates, one dropped", chat)
		if q := j.u.CallbackQuery; q != nil {
			b.refuse(denial{queryID: q.ID, text: "Зачекай секунду — я ще виконую попереднє."})
		}
	}
}

func (b *Bot) work(s *session, w *worker) {
	defer b.wg.Done()
	for {
		select {
		case <-b.ctx.Done():
			return
		case j := <-w.ch:
			b.handle(s, j)
		}
	}
}

func (b *Bot) handle(s *session, j job) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tgbot] panic: %v\n%s", r, debug.Stack())
		}
	}()
	ctx := b.ctx
	switch {
	case j.paired:
		b.welcome(ctx, s, true)
		b.setCommands(ctx, b.client())
	case j.u.CallbackQuery != nil:
		b.onCallback(ctx, s, j.u.CallbackQuery)
	case j.u.Message != nil:
		b.onMessage(ctx, s, j.u.Message)
	}
}

// welcome greets the owner with the main keyboard.
func (b *Bot) welcome(ctx context.Context, s *session, paired bool) {
	s.flow = flowNone
	text := "👋 Привіт! Я пульт YT Cartell Studio на твоєму комп'ютері.\nНадішли історію — і я зроблю з неї відео. Або вибери дію в меню внизу."
	if paired {
		text = "✅ Керування підключено — тепер я слухаюся лише тебе.\nНадішли історію або вибери дію в меню внизу. /help — що я вмію."
	}
	b.send(ctx, s.chat, text, b.menuMarkup(s.chat))
}

// menuMarkup is the main menu for chat: the reply keyboard in a private chat,
// inline buttons in a group (with privacy mode on, bots do not see the taps of
// a reply keyboard there).
func (b *Bot) menuMarkup(chat string) *notify.SendOptions {
	if isGroup(chat) {
		_, kb := mainMenu()
		return &notify.SendOptions{Markup: kb}
	}
	return &notify.SendOptions{Markup: mainKeyboard}
}

// ---- messages ---------------------------------------------------------------------------

// command parses "/cmd@bot args"; a command for another bot is not ours.
func (b *Bot) command(text string) (cmd, arg string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, arg, _ := strings.Cut(text[1:], " ")
	cmd, at, _ := strings.Cut(head, "@")
	if at != "" {
		b.mu.Lock()
		me := b.st.Username
		b.mu.Unlock()
		if !strings.EqualFold(at, me) {
			return "", "", false
		}
	}
	return strings.ToLower(cmd), strings.TrimSpace(arg), true
}

func (b *Bot) onMessage(ctx context.Context, s *session, m *notify.Message) {
	text := strings.TrimSpace(m.Text)
	if cmd, arg, ok := b.command(text); ok {
		switch cmd {
		case "start", "menu":
			if pk, ok := strings.CutPrefix(arg, "p_"); ok { // deep link t.me/<bot>?start=p_<key>
				if p, ok := b.projectByKey(pk); ok {
					html, kb := b.card(p)
					b.send(ctx, s.chat, html, &notify.SendOptions{Markup: kb})
					return
				}
			}
			b.welcome(ctx, s, false)
		case "help":
			b.send(ctx, s.chat, helpText, b.menuMarkup(s.chat))
		case "new":
			b.startNew(ctx, s, 0)
		case "remake":
			b.startRemake(ctx, s, 0)
		case "projects":
			html, kb := b.projectsList(0)
			b.send(ctx, s.chat, html, &notify.SendOptions{Markup: kb})
		case "queue":
			html, kb := b.queueView()
			b.send(ctx, s.chat, html, &notify.SendOptions{Markup: kb})
		case "profiles":
			html, kb := b.profilesView(0)
			b.send(ctx, s.chat, html, &notify.SendOptions{Markup: kb})
		case "status":
			b.status(ctx, s.chat, 0)
		case "cancel":
			s.flow, s.draft, s.remake, s.pending, s.confirm = flowNone, nil, nil, "", nil
			b.send(ctx, s.chat, "Скасовано. Що далі?", &notify.SendOptions{Markup: mainKeyboard})
		default:
			b.send(ctx, s.chat, "Не знаю такої команди 🙂 /help — що я вмію.", nil)
		}
		return
	}
	if act := menuAction(text); act != "" {
		s.flow = flowNone
		b.menu(ctx, s, act, 0)
		return
	}
	switch {
	case m.Voice != nil || m.Audio != nil:
		b.onVoice(ctx, s, m)
		return
	case m.Document != nil:
		b.onDocument(ctx, s, m)
		return
	case text == "":
		b.send(ctx, s.chat, "Надішли історію текстом, файлом .txt або голосовим — або вибери дію в меню.", nil)
		return
	}
	switch s.flow {
	case flowNewText:
		b.newDraft(ctx, s, text, 0)
	case flowNewLen:
		b.draftLength(ctx, s, text)
	case flowNewLang:
		b.draftLanguage(ctx, s, text)
	case flowRemakeLink:
		b.remakeLink(ctx, s, text)
	case flowRemakeWish:
		b.remakeWish(ctx, s, text)
	default:
		b.offerNew(ctx, s, text, 0)
	}
}

// menu opens a main-menu item as a new message.
func (b *Bot) menu(ctx context.Context, s *session, act string, id int) {
	var html string
	var kb notify.InlineKeyboard
	switch act {
	case "new":
		b.startNew(ctx, s, id)
		return
	case "rmk":
		b.startRemake(ctx, s, id)
		return
	case "st":
		b.status(ctx, s.chat, id)
		return
	case "l":
		html, kb = b.projectsList(0)
	case "q":
		html, kb = b.queueView()
	case "f":
		html, kb = b.profilesView(0)
	case "h":
		html, kb = helpText, notify.InlineKeyboard{row(btn("🏠 Меню", "m"))}
	default:
		html, kb = mainMenu()
	}
	b.show(ctx, s.chat, id, html, kb)
}

// offerNew asks whether to make a project from a text that came outside a flow.
func (b *Bot) offerNew(ctx context.Context, s *session, text string, id int) {
	if len([]rune(text)) < 12 {
		b.send(ctx, s.chat, "Не зовсім зрозумів 🙂 Надішли історію чи ідею для відео або вибери дію в меню.", &notify.SendOptions{Markup: mainKeyboard})
		return
	}
	s.pending = text
	html := "🎬 Зробити відео з цього тексту?\n<i>" + esc(excerpt(text, 300)) + "</i>"
	b.show(ctx, s.chat, id, html, notify.InlineKeyboard{row(btn("🎬 Так, новий проєкт", "nw"), btn("✖️ Ні", "nx"))})
}

// ---- button presses ----------------------------------------------------------------------

// press is one button press being handled.
type press struct {
	b        *Bot
	ctx      context.Context
	s        *session
	q        *notify.CallbackQuery
	answered bool
}

// toast answers the press with a short note (or a dialog).
func (c *press) toast(text string, alert bool) {
	if c.answered {
		return
	}
	c.answered = true
	c.b.answer(c.ctx, c.q.ID, text, alert)
}

// show replaces the pressed message (a text message) or sends a new one
// (under a photo, video or document — those have no text to edit).
func (c *press) show(html string, kb notify.InlineKeyboard) int {
	c.toast("", false)
	id := c.q.Message.MessageID
	if c.q.Message.Text == "" {
		id = 0
	}
	return c.b.show(c.ctx, c.s.chat, id, html, kb)
}

// fresh sends a new message (the pressed one stays).
func (c *press) fresh(html string, kb notify.InlineKeyboard) int {
	c.toast("", false)
	return c.b.show(c.ctx, c.s.chat, 0, html, kb)
}

func (b *Bot) onCallback(ctx context.Context, s *session, q *notify.CallbackQuery) {
	c := &press{b: b, ctx: ctx, s: s, q: q}
	defer c.toast("", false)
	parts := strings.Split(q.Data, ":")
	verb := parts[0]
	arg := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	num := func(i int) int { n, _ := strconv.Atoi(arg(i)); return n }
	project := func() (studio.Project, bool) {
		p, ok := b.projectByKey(arg(1))
		if !ok {
			c.toast("Проєкт не знайдено — можливо, його вже видалено.", true)
		}
		return p, ok
	}
	id := q.Message.MessageID
	if q.Message.Text == "" {
		id = 0
	}
	switch verb {
	case "m", "h", "st", "new", "rmk", "q":
		c.toast("", false)
		b.menu(ctx, s, verb, id)
	case "l":
		html, kb := b.projectsList(num(1))
		c.show(html, kb)
	case "f":
		html, kb := b.profilesView(num(1))
		c.show(html, kb)
	case "fs":
		pid, name, ok := b.profileByKey(arg(1))
		if !ok {
			c.toast("Профіль не знайдено", true)
			return
		}
		if err := b.setDefaultProfile(pid); err != nil {
			c.toast(userErr(err), true)
			return
		}
		c.toast("Типовий профіль: "+clipName(name, 60), false)
		html, kb := b.profilesView(0)
		c.show(html, kb)
	case "p":
		if p, ok := project(); ok {
			html, kb := b.card(p)
			c.show(html, kb)
		}
	case "pa", "pn", "pm":
		if p, ok := project(); ok {
			b.runStage(c, p, verb)
		}
	case "ps", "qc":
		if p, ok := project(); ok {
			b.e.Cancel(p.ID)
			c.toast("⏹ Зупиняю — поточні задачі доробляться", false)
			if verb == "qc" {
				html, kb := b.queueView()
				c.show(html, kb)
			} else if p, ok := b.e.Get(p.ID); ok {
				html, kb := b.card(p)
				c.show(html, kb)
			}
		}
	case "pr":
		if p, ok := project(); ok {
			if err := b.e.Retry(p.ID); err != nil {
				if !b.missingScenes(c, p, err) {
					c.toast(userErr(err), true)
				}
				return
			}
			b.live.watch(p.ID, s.chat, 0)
			c.toast("🔁 Повторюю те, що не вдалося", false)
			if p, ok := b.e.Get(p.ID); ok {
				html, kb := b.card(p)
				c.show(html, kb)
			}
		}
	case "gs", "gf":
		if p, ok := project(); ok {
			missing := studio.MissingSkip
			if verb == "gf" {
				missing = studio.MissingStill
			}
			b.montageWith(c, p, missing)
		}
	case "gr":
		if p, ok := project(); ok {
			b.reshoot(c, p)
		}
	case "gx":
		if p, ok := project(); ok {
			b.gapsStop(c, p)
		}
	case "pd":
		if p, ok := project(); ok {
			c.ask("del", p.ID, "🗑 Видалити проєкт "+title(p.Title)+"?\nУсі його кадри, відео й озвучку буде стерто з комп'ютера.", "🗑 Так, видалити")
		}
	case "qa":
		c.ask("stopall", "", "⏹ Зупинити все, що зараз генерується?\nГотові кадри, відео й озвучка збережуться, продовжити можна кнопкою «Повторити».", "⏹ Так, зупинити все")
	case "x":
		b.confirmed(c, arg(1))
	case "xn":
		cf := s.confirm
		s.confirm = nil
		if cf != nil && cf.pid != "" {
			if p, ok := b.e.Get(cf.pid); ok {
				html, kb := b.card(p)
				c.show(html, kb)
				return
			}
			html, kb := b.projectsList(0)
			c.show(html, kb)
			return
		}
		html, kb := b.queueView()
		c.show(html, kb)
	case "pf":
		if p, ok := project(); ok {
			c.toast("Надсилаю кадри…", false)
			b.sendFrames(ctx, s.chat, p, num(2))
		}
	case "pv":
		if p, ok := project(); ok {
			html, kb := b.voicesView(p, num(2))
			c.fresh(html, kb)
		}
	case "pw":
		if p, ok := project(); ok {
			c.toast("Надсилаю озвучку…", false)
			b.sendVoice(ctx, s.chat, p, num(2))
		}
	case "pc":
		if p, ok := project(); ok {
			b.coverAction(c, p)
		}
	case "v", "vz", "vp":
		if p, ok := project(); ok {
			b.videoAction(c, p, verb)
		}
	case "s":
		if p, ok := project(); ok {
			c.toast("Надсилаю субтитри…", false)
			b.sendSRT(ctx, s.chat, p)
		}
	case "n":
		b.draftPress(c, arg(1), arg(2), strings.Join(parts[min(3, len(parts)):], ":"))
	case "r":
		b.remakePress(c, arg(1), arg(2), strings.Join(parts[min(3, len(parts)):], ":"))
	case "nw":
		if s.pending == "" {
			c.toast("Цей текст уже неактуальний — надішли його ще раз", true)
			return
		}
		text := s.pending
		s.pending = ""
		c.toast("", false)
		b.newDraft(ctx, s, text, id)
	case "nx":
		s.pending = ""
		c.show("Добре, нічого не роблю. Надішли історію, коли захочеш, або вибери дію в меню.", nil)
	default:
		c.toast("Ця кнопка застаріла — відкрий меню ще раз", true)
	}
}

// ask edits the message into a yes/no question for a destructive action.
func (c *press) ask(action, pid, question, yes string) {
	tok := make([]byte, 4)
	_, _ = rand.Read(tok)
	c.s.confirm = &confirm{tok: hex.EncodeToString(tok), action: action, pid: pid, until: c.b.now().Add(confirmTTL)}
	c.show(question, notify.InlineKeyboard{row(
		notify.Button{Text: yes, Data: "x:" + c.s.confirm.tok, Style: "danger"},
		btn("Скасувати", "xn"),
	)})
}

// confirmed runs the action of a question answered «Так».
func (b *Bot) confirmed(c *press, tok string) {
	cf := c.s.confirm
	if cf == nil || cf.tok != tok || !b.now().Before(cf.until) {
		c.s.confirm = nil
		c.toast("Час підтвердження минув — натисни ще раз", true)
		return
	}
	c.s.confirm = nil
	switch cf.action {
	case "del":
		p, ok := b.e.Get(cf.pid)
		if !ok {
			c.toast("Проєкт уже видалено", false)
			return
		}
		if err := b.e.Delete(cf.pid); err != nil {
			c.show("⚠️ "+esc(userErr(err)), notify.InlineKeyboard{row(btn("◀️ Проєкти", "l:0"))})
			return
		}
		c.toast("Видалено", false)
		c.show("🗑 Проєкт "+title(p.Title)+" видалено.", notify.InlineKeyboard{row(btn("◀️ Проєкти", "l:0"))})
	case "stopall":
		n := b.e.CancelAll()
		c.toast(fmt.Sprintf("⏹ Зупиняю: %d", n), false)
		html, kb := b.queueView()
		c.show(html, kb)
	}
}

// runStage starts the autopilot, the next stage or the montage.
func (b *Bot) runStage(c *press, p studio.Project, verb string) {
	stage, label := studio.StageAutopilot, "🚀 Автопілот запущено"
	switch verb {
	case "pn":
		var l string
		stage, l = nextStage(p)
		if stage == "" {
			c.toast("Тут уже все зроблено 🙂", false)
			return
		}
		label = "▶️ Запущено: " + strings.TrimSpace(strings.TrimLeftFunc(l, func(r rune) bool { return r > 0x2000 || r == ' ' }))
	case "pm":
		stage, label = studio.StageMontage, "✂️ Монтую"
	}
	if err := b.e.Run(p.ID, stage, false, ""); err != nil {
		// Scenes not ready for the montage: the list with the exact errors
		// and the choices, not a popup (gaps.go).
		if !b.missingScenes(c, p, err) {
			c.toast(userErr(err), true)
		}
		return
	}
	// The card becomes the live progress message of this run.
	b.started(c, p.ID, label)
}
