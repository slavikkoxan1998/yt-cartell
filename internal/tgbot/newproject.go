package tgbot

// «🎬 Новий проєкт»: the story (a text message, a .txt file or a voice
// message — recognised and shown for confirmation), then one options
// message edited in place: profile, mode (Автопілот / Покроково), length,
// language, sound, what is in frame, aspect, «▶️ Запустити». Everything
// goes to Engine.CreateProject like Home does.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

type draft struct {
	nonce     string
	text      string
	profileID string // with noProfile false: this profile
	noProfile bool   // «Типові налаштування»
	auto      bool   // Автопілот (else Покроково)
	secs      int    // 0 = the profile's
	lang      string // audience language ("" = auto) when langSet
	langSet   bool
	sound     string // "" = the profile's
	content   string
	aspect    string
	// pipeline («Режим виробництва») when pipelineSet (else the profile's)
	pipeline    string
	pipelineSet bool
	// grammar of a «Анімація кодом» film when grammarSet ("" = auto; else the profile's)
	grammar    string
	grammarSet bool
	msgID      int
	menu       string // the open sub-menu ("" = the main options)
	page       int    // profiles page
}

func nonce() string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Sound and content choices (labels like Home's pickers).
var (
	soundModes = []choice{
		{studio.AudioVoiceover, "Озвучка диктора"},
		{studio.AudioInVideo, "Голос у відео"},
		{studio.AudioSilent, "Без голосу"},
	}
	contentModes = []choice{
		{studio.ContentCharacters, "Персонажі"},
		{studio.ContentNoPeople, "Без людей"},
		{studio.ContentMotion, "Моушн-графіка"},
		{studio.ContentMixed, "Змішано"},
	}
	pipelines = []choice{
		{studio.PipelineFull, studio.PipelineLabel(studio.PipelineFull)},
		{studio.PipelineNoRefs, studio.PipelineLabel(studio.PipelineNoRefs)},
		{studio.PipelineStills, studio.PipelineLabel(studio.PipelineStills)},
		{studio.PipelineNoRefsStills, studio.PipelineLabel(studio.PipelineNoRefsStills)},
		{studio.PipelineCode, studio.PipelineLabel(studio.PipelineCode)},
		{studio.PipelineNoRefsCode, studio.PipelineLabel(studio.PipelineNoRefsCode)},
	}
	lengths   = []int{30, 60, 180, 300}
	topLangs  = []string{"uk", "en", "pl", "de", "es", "fr", "pt", "it"}
	reMinutes = regexp.MustCompile(`^(\d+(?:[.,]\d+)?)\s*(хв|хвилин[аиу]?|min|m|мин)\.?$`)
	reSeconds = regexp.MustCompile(`^(\d+)\s*(с|сек|секунд[аи]?|s|sec)?\.?$`)
	reClock   = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
)

type choice struct{ value, label string }

func choiceLabel(list []choice, v string) string {
	for _, c := range list {
		if c.value == v {
			return c.label
		}
	}
	return list[0].label
}

func langLabel(code string) string {
	if code == "" {
		return "авто"
	}
	if l, ok := studio.LanguageByCode(code); ok {
		return l.Label
	}
	return code
}

// startNew asks for the story.
func (b *Bot) startNew(ctx context.Context, s *session, id int) {
	s.flow, s.draft = flowNewText, nil
	b.show(ctx, s.chat, id, "🎬 <b>Новий проєкт</b>\nНадішли історію, сценарій чи ідею — текстом, файлом .txt або голосовим.",
		notify.InlineKeyboard{row(btn("✖️ Скасувати", "n:-:no"))})
}

// newDraft starts the options for text (in message id when > 0).
func (b *Bot) newDraft(ctx context.Context, s *session, text string, id int) {
	cfg := b.cfg.Get()
	d := &draft{nonce: nonce(), text: text, auto: true, noProfile: true}
	if pid := cfg.Autopilot.DefaultProfileID; pid != "" {
		if _, ok := b.e.Profile(pid); ok {
			d.profileID, d.noProfile = pid, false
		}
	}
	s.flow, s.draft = flowNone, d
	html, kb := b.draftView(d)
	d.msgID = b.show(ctx, s.chat, id, html, kb)
}

// profile is the draft's profile (false = the app defaults).
func (b *Bot) draftProfile(d *draft) (config.Profile, bool) {
	if d.noProfile {
		return config.Profile{}, false
	}
	return b.e.Profile(d.profileID)
}

// values are the draft's effective settings.
func (b *Bot) draftValues(d *draft) (name string, secs int, lang, sound, content, aspect string) {
	in := studio.DefaultInput()
	name = "Типові налаштування"
	prof, has := b.draftProfile(d)
	secs, lang, aspect = in.TargetSeconds, "", in.Aspect
	sound, content = studio.AudioVoiceover, studio.ContentCharacters
	if has {
		name = prof.Name
		if prof.TargetSeconds > 0 {
			secs = prof.TargetSeconds
		}
		lang = prof.Language
		if prof.Aspect != "" {
			aspect = prof.Aspect
		}
		switch {
		case prof.AudioMode != "":
			sound = prof.AudioMode
		case !prof.Voiceover:
			sound = studio.AudioSilent
		}
		if prof.Content != "" {
			content = prof.Content
		}
	}
	if d.secs > 0 {
		secs = d.secs
	}
	if d.langSet {
		lang = d.lang
	}
	if d.sound != "" {
		sound = d.sound
	}
	if d.content != "" {
		content = d.content
	}
	if d.aspect != "" {
		aspect = d.aspect
	}
	return
}

// draftView is the options message.
func (b *Bot) draftView(d *draft) (string, notify.InlineKeyboard) {
	name, secs, lang, sound, content, aspect := b.draftValues(d)
	mode, modeHint := "Автопілот", "студія сама пройде всі кроки й змонтує відео"
	if !d.auto {
		mode, modeHint = "Покроково", "створю проєкт, а кроки запускатимеш сам"
	}
	var sb strings.Builder
	sb.WriteString("🎬 <b>Новий проєкт</b>\n<i>" + esc(excerpt(d.text, 220)) + "</i>\n")
	n := len([]rune(d.text))
	sb.WriteString(count(n, "символ", "символи", "символів") + "\n")
	sb.WriteString("\n👤 Профіль: <b>" + esc(name) + "</b>")
	sb.WriteString("\n🔀 Режим: <b>" + mode + "</b> — " + modeHint)
	sb.WriteString("\n⏱ Тривалість: <b>" + length(secs) + "</b> · 🌐 Мова: <b>" + esc(langLabel(lang)) + "</b>")
	sb.WriteString("\n🎙 Звук: <b>" + choiceLabel(soundModes, sound) + "</b>")
	sb.WriteString("\n🎞 У кадрі: <b>" + choiceLabel(contentModes, content) + "</b> · 📐 <b>" + esc(aspect) + "</b>")
	sb.WriteString("\n🏭 Виробництво: <b>" + studio.PipelineLabel(b.draftPipeline(d)) + "</b>")
	if (studio.Input{Pipeline: b.draftPipeline(d)}).CodeAnim() {
		sb.WriteString("\n🧩 Анімація: <b>" + esc(b.grammarLabel(b.draftGrammar(d))) + "</b> — малює код (графіки, мапи, текст, схеми), без генерації відео")
	}
	if sound == studio.AudioInVideo {
		if studio.VideoSpeech(b.cfg.Get()) == studio.SpeechNo {
			sb.WriteString("\n⚠️ Відеомодель у налаштуваннях знімає без звуку — «Голос у відео» потребує Veo 3.1 або Omni Flash.")
		}
	}
	return sb.String(), b.draftKeyboard(d, secs, lang, sound, content, aspect)
}

func (b *Bot) draftKeyboard(d *draft, secs int, lang, sound, content, aspect string) notify.InlineKeyboard {
	cb := func(field, value string) string {
		if value == "" {
			return "n:" + d.nonce + ":" + field
		}
		return "n:" + d.nonce + ":" + field + ":" + value
	}
	mark := func(on bool, label string) string {
		if on {
			return "✓ " + label
		}
		return label
	}
	back := row(btn("◀️ Назад", cb("back", "")))
	switch d.menu {
	case "pf":
		list := b.e.Profiles()
		const per = 8
		pages := max(1, (len(list)+per-1)/per)
		page := max(0, min(d.page, pages-1))
		var kb notify.InlineKeyboard
		if page == 0 {
			kb = append(kb, row(btn(mark(d.noProfile, "Типові налаштування"), cb("ps", "-"))))
		}
		for _, p := range list[page*per : min(len(list), (page+1)*per)] {
			label := clipName(p.Name, 34)
			if p.Aspect != "" {
				label += " · " + p.Aspect
			}
			kb = append(kb, row(btn(mark(!d.noProfile && p.ID == d.profileID, label), cb("ps", key(p.ID)))))
		}
		if pages > 1 {
			var nav []notify.Button
			if page > 0 {
				nav = append(nav, btn("◀️", cb("pf", strconv.Itoa(page-1))))
			}
			if page < pages-1 {
				nav = append(nav, btn("▶️", cb("pf", strconv.Itoa(page+1))))
			}
			kb = append(kb, nav)
		}
		return append(kb, back)
	case "ln":
		var r []notify.Button
		for _, v := range lengths {
			r = append(r, btn(mark(secs == v, length(v)), cb("lv", strconv.Itoa(v))))
		}
		return notify.InlineKeyboard{r, row(btn("✏️ Своя тривалість", cb("lc", ""))), back}
	case "lg":
		kb := notify.InlineKeyboard{row(btn(mark(lang == "", "Авто — як у тексті"), cb("la", "auto")))}
		var r []notify.Button
		for _, code := range topLangs {
			if _, ok := studio.LanguageByCode(code); !ok {
				continue
			}
			r = append(r, btn(mark(lang == code, langLabel(code)), cb("la", code)))
			if len(r) == 2 {
				kb, r = append(kb, r), nil
			}
		}
		if len(r) > 0 {
			kb = append(kb, r)
		}
		return append(kb, row(btn("✏️ Інша мова", cb("lo", ""))), back)
	case "sd":
		var kb notify.InlineKeyboard
		for _, c := range soundModes {
			kb = append(kb, row(btn(mark(sound == c.value, c.label), cb("sv", c.value))))
		}
		return append(kb, back)
	case "ct":
		var kb notify.InlineKeyboard
		for _, c := range contentModes {
			kb = append(kb, row(btn(mark(content == c.value, c.label), cb("cv", c.value))))
		}
		return append(kb, back)
	case "pp":
		var kb notify.InlineKeyboard
		cur := b.draftPipeline(d)
		for _, c := range pipelines {
			kb = append(kb, row(btn(mark(cur == c.value, c.label), cb("pv", pipelineKey(c.value)))))
		}
		return append(kb, back)
	case "gr":
		cur := b.draftGrammar(d)
		kb := notify.InlineKeyboard{row(btn(mark(cur == "", b.grammarLabel("")), cb("gv", "auto")))}
		for _, g := range b.e.CodeGrammars() {
			kb = append(kb, row(btn(mark(cur == g.ID, clipName(g.Label, 40)), cb("gv", g.ID))))
		}
		return append(kb, back)
	}
	other := "9:16"
	if aspect == "9:16" {
		other = "16:9"
	}
	modeBtn := "🔀 Режим: Автопілот"
	if !d.auto {
		modeBtn = "🔀 Режим: Покроково"
	}
	kb := notify.InlineKeyboard{
		row(btn("👤 Профіль", cb("pf", "0")), btn(modeBtn, cb("md", ""))),
		row(btn("⏱ "+length(secs), cb("ln", "")), btn("🌐 Мова: "+langLabel(lang), cb("lg", ""))),
		row(btn("🎙 Звук", cb("sd", "")), btn("🎞 Що в кадрі", cb("ct", "")), btn("📐 "+other, cb("as", other))),
		row(btn("🏭 Виробництво: "+studio.PipelineLabel(b.draftPipeline(d)), cb("pp", ""))),
	}
	if (studio.Input{Pipeline: b.draftPipeline(d)}).CodeAnim() {
		kb = append(kb, row(btn("🧩 Анімація: "+clipName(b.grammarLabel(b.draftGrammar(d)), 30), cb("gr", ""))))
	}
	return append(kb, row(notify.Button{Text: "▶️ Запустити", Data: cb("go", ""), Style: "success"}, btn("✖️ Скасувати", cb("no", ""))))
}

// draftPress handles a button of the options message.
func (b *Bot) draftPress(c *press, n, field, value string) {
	s := c.s
	if field == "no" && n == "-" { // «Скасувати» while asking for the story
		s.flow = flowNone
		c.show("Добре, скасовано. Надішли історію, коли захочеш.", nil)
		return
	}
	d := s.draft
	if d == nil || d.nonce != n {
		c.toast("Ця чернетка вже неактуальна — почни новий проєкт", true)
		return
	}
	d.msgID = c.q.Message.MessageID
	switch field {
	case "pf":
		d.menu, d.page = "pf", atoi(value)
	case "ps":
		if value == "-" {
			d.noProfile, d.profileID = true, ""
		} else if pid, _, ok := b.profileByKey(value); ok {
			d.noProfile, d.profileID = false, pid
		}
		// The profile decides again: the overrides go.
		d.secs, d.lang, d.langSet, d.sound, d.content, d.aspect = 0, "", false, "", "", ""
		d.pipeline, d.pipelineSet = "", false
		d.grammar, d.grammarSet = "", false
		d.menu = ""
	case "md":
		d.auto = !d.auto
	case "ln", "lg", "sd", "ct", "pp", "gr":
		d.menu = field
	case "lv":
		d.secs, d.menu = atoi(value), ""
	case "lc":
		s.flow = flowNewLen
		c.toast("", false)
		b.send(c.ctx, s.chat, "⏱ Скільки має тривати відео? Напиши, наприклад: <b>90 с</b>, <b>2 хв</b> або <b>1:30</b>.", nil)
		return
	case "la":
		d.lang, d.langSet, d.menu = "", true, ""
		if value != "auto" {
			d.lang = value
		}
	case "lo":
		s.flow = flowNewLang
		c.toast("", false)
		b.send(c.ctx, s.chat, "🌐 Якою мовою зробити фільм? Напиши назву або код: <b>італійська</b>, <b>Japanese</b>, <b>ko</b>…", nil)
		return
	case "sv":
		d.sound, d.menu = value, ""
	case "cv":
		d.content, d.menu = value, ""
	case "pv":
		if v := pipelineValue(value); studio.ValidPipeline(v) {
			d.pipeline, d.pipelineSet = v, true
		}
		d.menu = ""
	case "gv":
		d.grammar, d.grammarSet, d.menu = "", true, ""
		if value != "auto" {
			if _, ok := b.e.CodeGrammarByID(value); ok {
				d.grammar = value
			}
		}
	case "as":
		if value == "16:9" || value == "9:16" {
			d.aspect = value
		}
	case "back":
		d.menu = ""
	case "go":
		b.launch(c, d)
		return
	case "no":
		s.draft, s.flow = nil, flowNone
		c.show("Добре, проєкт не створюю. Надішли історію, коли захочеш.", nil)
		return
	}
	html, kb := b.draftView(d)
	c.show(html, kb)
}

// draftLength reads «Своя тривалість».
func (b *Bot) draftLength(ctx context.Context, s *session, text string) {
	d := s.draft
	if d == nil {
		s.flow = flowNone
		return
	}
	sec, ok := parseLength(text)
	if !ok {
		b.send(ctx, s.chat, "Не зрозумів тривалість 🙂 Напиши, наприклад: <b>45 с</b>, <b>2 хв</b> або <b>1:30</b> (від 10 с до 60 хв).", nil)
		return
	}
	d.secs, d.menu = sec, ""
	s.flow = flowNone
	b.redraw(ctx, s, d)
}

// draftLanguage reads «Інша мова».
func (b *Bot) draftLanguage(ctx context.Context, s *session, text string) {
	d := s.draft
	if d == nil {
		s.flow = flowNone
		return
	}
	code, ok := findLanguage(text)
	if !ok {
		b.send(ctx, s.chat, "Не знайшов такої мови 🙂 Спробуй назву українською чи англійською або код (it, ja, ko).", nil)
		return
	}
	d.lang, d.langSet, d.menu = code, true, ""
	s.flow = flowNone
	b.redraw(ctx, s, d)
}

// redraw sends the options anew under the user's answer (the old message
// loses its buttons).
func (b *Bot) redraw(ctx context.Context, s *session, d *draft) {
	if d.msgID > 0 {
		_ = b.retry(ctx, s.chat, func() error { return b.client().EditMarkup(ctx, s.chat, d.msgID, nil) })
	}
	html, kb := b.draftView(d)
	d.msgID = b.show(ctx, s.chat, 0, html, kb)
}

// launch creates the project (and starts the autopilot).
func (b *Bot) launch(c *press, d *draft) {
	opt := studio.NewProjectOptions{Title: titleFrom(d.text), Text: d.text, Mode: studio.ModeStory,
		Autopilot: d.auto, NoProfile: d.noProfile, AudioMode: d.sound, Content: d.content, Aspect: d.aspect,
		Pipeline: b.draftPipeline(d), PipelineSet: d.pipelineSet}
	if d.grammarSet {
		opt.CodeGrammar = d.grammar
	}
	if !d.noProfile {
		opt.ProfileID = d.profileID
	}
	if d.langSet {
		opt.Language, opt.LanguageSet = d.lang, true
	}
	if d.secs > 0 {
		opt.TargetSeconds = d.secs
	}
	p, err := b.e.CreateProject(opt)
	if err != nil && p.ID == "" {
		c.toast(clipName(userErr(err), 190), true)
		return
	}
	c.s.draft = nil
	if err != nil { // created, the autopilot did not start
		html, kb := b.card(p)
		c.show(html+"\n\n⚠️ Автопілот не запустився: "+esc(userErr(err)), kb)
		return
	}
	if !d.auto {
		c.toast("Проєкт створено", false)
		html, kb := b.card(p)
		c.show(html, kb)
		return
	}
	c.toast("🚀 Автопілот запущено", false)
	k := key(p.ID)
	id := c.show("🚀 Автопілот запущено: "+title(p.Title)+"\nЯ оновлюватиму це повідомлення, поки йде робота.",
		notify.InlineKeyboard{row(btn("⏹ Зупинити", "ps:"+k), btn("📂 Проєкт", "p:"+k))})
	b.live.watch(p.ID, c.s.chat, id)
}

// parseLength reads "90", "90 с", "2 хв", "1.5 хв", "1:30" (10 s … 60 min).
func parseLength(text string) (int, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	sec := 0
	if m := reClock.FindStringSubmatch(t); m != nil {
		sec = atoi(m[1])*60 + atoi(m[2])
	} else if m := reMinutes.FindStringSubmatch(t); m != nil {
		f, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", "."), 64)
		if err != nil {
			return 0, false
		}
		sec = int(f*60 + 0.5)
	} else if m := reSeconds.FindStringSubmatch(t); m != nil {
		sec = atoi(m[1])
	} else {
		return 0, false
	}
	if sec < 10 || sec > 3600 {
		return 0, false
	}
	return sec, true
}

// findLanguage matches a typed language: code, Ukrainian, English or native name.
func findLanguage(text string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimSuffix(strings.TrimSuffix(t, " мова"), " language")
	if t == "" {
		return "", false
	}
	for _, l := range studio.Languages {
		if t == l.Code || t == strings.ToLower(l.Label) || t == strings.ToLower(l.English) || t == strings.ToLower(l.Native) {
			return l.Code, true
		}
	}
	for _, l := range studio.Languages { // "італійську", "англ"
		if len([]rune(t)) >= 4 && strings.HasPrefix(strings.ToLower(l.Label), string([]rune(t)[:4])) {
			return l.Code, true
		}
	}
	return "", false
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// draftPipeline is the draft's production mode: the user's pick, else the
// profile's.
func (b *Bot) draftPipeline(d *draft) string {
	if d.pipelineSet {
		return d.pipeline
	}
	if prof, ok := b.draftProfile(d); ok && studio.ValidPipeline(prof.Pipeline) {
		return prof.Pipeline
	}
	return studio.PipelineFull
}

// draftGrammar is the draft's animation grammar («Анімація кодом»): the
// user's pick, else the profile's ("" = auto).
func (b *Bot) draftGrammar(d *draft) string {
	if d.grammarSet {
		return d.grammar
	}
	if prof, ok := b.draftProfile(d); ok {
		return prof.CodeGrammar
	}
	return ""
}

// grammarLabel names a grammar choice.
func (b *Bot) grammarLabel(id string) string {
	if id == "" {
		return "Авто — під зміст сцени"
	}
	if g, ok := b.e.CodeGrammarByID(id); ok {
		return g.Label
	}
	return id
}

// pipelineKey / pipelineValue: the full pipeline is "" — "full" in a button.
func pipelineKey(v string) string {
	if v == "" {
		return "full"
	}
	return v
}

func pipelineValue(k string) string {
	if k == "full" {
		return ""
	}
	return k
}
