package tgbot

// The menus: main menu and help, the projects list and a project's card,
// the queue and the profiles. Each returns the HTML and the inline keyboard
// of one message (callers send it or edit it in place).

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

// The reply keyboard under the input field.
const (
	btnNew      = "🎬 Новий проєкт"
	btnRemake   = "🔁 За прикладом"
	btnProjects = "📁 Проєкти"
	btnQueue    = "⏳ Черга"
	btnProfiles = "⚙️ Профілі"
	btnStatus   = "📊 Статус"
)

var mainKeyboard = notify.ReplyKeyboard{
	Rows:        [][]string{{btnNew, btnRemake}, {btnProjects, btnQueue}, {btnProfiles, btnStatus}},
	Persistent:  true,
	Resize:      true,
	Placeholder: "Історія, ідея або команда",
}

// menuAction maps a reply-keyboard press (or the same words typed) to a
// callback verb.
func menuAction(text string) string {
	t := strings.ToLower(strings.TrimLeftFunc(strings.TrimSpace(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 'А' && r <= 'я' || strings.ContainsRune("ІіЇїЄєҐґ", r))
	}))
	switch t {
	case "новий проєкт":
		return "new"
	case "за прикладом":
		return "rmk"
	case "проєкти":
		return "l"
	case "черга":
		return "q"
	case "профілі":
		return "f"
	case "статус":
		return "st"
	case "меню":
		return "m"
	case "допомога":
		return "h"
	}
	return ""
}

func btn(text, data string) notify.Button { return notify.Button{Text: text, Data: data} }

func row(bs ...notify.Button) []notify.Button { return bs }

// mainMenu is the inline main menu (groups, «Меню» buttons).
func mainMenu() (string, notify.InlineKeyboard) {
	return "🏠 <b>Меню</b>\nЩо робимо?", notify.InlineKeyboard{
		row(btn(btnNew, "new"), btn(btnRemake, "rmk")),
		row(btn(btnProjects, "l:0"), btn(btnQueue, "q")),
		row(btn(btnProfiles, "f:0"), btn(btnStatus, "st")),
		row(btn("❓ Що я вмію", "h")),
	}
}

const helpText = `<b>Що я вмію</b>
🎬 <b>Новий проєкт</b> — надішли історію текстом, файлом .txt або голосовим, вибери профіль, тривалість, мову, звук і запусти.
🔁 <b>За прикладом</b> — посилання на YouTube + побажання → нове відео за зразком.
📁 <b>Проєкти</b> — стан і прогрес; зупинити, повторити невдале, глянути кадри й озвучку, змонтувати, забрати відео, обкладинку й субтитри.
⏳ <b>Черга</b> — що зараз генерується; зупинити одне або все.
⚙️ <b>Профілі</b> — профіль за замовчуванням для автопілота.
📊 <b>Статус</b> — сервіси й ключі, місце на диску, версія.

Поки йде генерація, я оновлюю одне повідомлення з прогресом, а готове відео надсилаю з кнопками.
Команди: /menu /new /remake /projects /queue /status /cancel`

// ---- project state --------------------------------------------------------------------

// state is a project's one-line status: icon, words, progress (-1 = none).
func projState(p studio.Project) (icon, words string, pct int, running bool) {
	if j := p.Job; j != nil {
		switch j.Status {
		case studio.JobRunning:
			msg := strings.TrimSpace(j.Message)
			label := studio.JobLabel(j.Kind)
			if msg == "" || msg == "Запуск…" {
				msg = label
			} else if !strings.HasPrefix(msg, label) && j.Kind != studio.JobAutopilot {
				msg = label + " · " + msg
			}
			return "⏳", msg, j.Progress, true
		case studio.JobError:
			if blockedJob(j) && !videoReady(p) {
				return "⏸", studio.JobLabel(j.Kind) + " чекає на твоє рішення перед монтажем", -1, false
			}
			if !videoReady(p) {
				return "⚠️", studio.JobLabel(j.Kind) + ": " + firstLine(j.Error, 140), -1, false
			}
		case studio.JobCancelled:
			if !videoReady(p) {
				return "⏹", studio.JobLabel(j.Kind) + " — зупинено", -1, false
			}
		}
	}
	if videoReady(p) {
		return "✅", "відео готове · " + clock(p.Montage.Output.Duration), -1, false
	}
	return "📝", fmt.Sprintf("крок %d з 5 · %s", max(1, p.Step), stepName(p.Step)), -1, false
}

func videoReady(p studio.Project) bool {
	o := p.Montage.Output
	return o.Status == studio.StatusDone && o.Path != ""
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return clipName(s, n)
}

// ---- projects list --------------------------------------------------------------------

const listPage = 6

// projectsList is page of the projects, newest first.
func (b *Bot) projectsList(page int) (string, notify.InlineKeyboard) {
	all := b.e.List()
	if len(all) == 0 {
		return "📁 <b>Проєкти</b>\nТут поки порожньо. Створи перший — надішли історію або натисни «Новий проєкт».",
			notify.InlineKeyboard{row(btn(btnNew, "new"))}
	}
	pages := (len(all) + listPage - 1) / listPage
	page = max(0, min(page, pages-1))
	var sb strings.Builder
	fmt.Fprintf(&sb, "📁 <b>Проєкти</b> · %d", len(all))
	if pages > 1 {
		fmt.Fprintf(&sb, " · сторінка %d з %d", page+1, pages)
	}
	sb.WriteString("\n")
	var kb notify.InlineKeyboard
	for i, s := range all[page*listPage : min(len(all), (page+1)*listPage)] {
		p, ok := b.e.Get(s.ID)
		if !ok {
			continue
		}
		icon, words, pct, _ := projState(p)
		fmt.Fprintf(&sb, "\n%d. %s\n%s %s", page*listPage+i+1, title(p.Title), icon, esc(words))
		if pct >= 0 {
			sb.WriteString(" · " + strconv.Itoa(pct) + " %")
		}
		kb = append(kb, row(btn(icon+" "+clipName(p.Title, 40), "p:"+key(p.ID))))
	}
	var nav []notify.Button
	if page > 0 {
		nav = append(nav, btn("◀️", "l:"+strconv.Itoa(page-1)))
	}
	nav = append(nav, btn("🔄", "l:"+strconv.Itoa(page)))
	if page < pages-1 {
		nav = append(nav, btn("▶️", "l:"+strconv.Itoa(page+1)))
	}
	kb = append(kb, nav)
	return sb.String(), kb
}

// ---- project card ---------------------------------------------------------------------

// nextStage is the step «Далі» runs ("" = nothing to do by hand).
func nextStage(p studio.Project) (studio.Stage, string) {
	if len(p.Scenes) == 0 {
		if strings.TrimSpace(p.Input.Text) != "" || p.Input.VoiceFile != "" {
			return studio.StagePlan, "📝 Скласти план"
		}
		return "", ""
	}
	for _, c := range p.Characters {
		if c.Image.Status != studio.StatusDone {
			return studio.StageRefs, "👤 Референси"
		}
	}
	for _, l := range p.Locations {
		if l.Image.Status != studio.StatusDone {
			return studio.StageRefs, "👤 Референси"
		}
	}
	voice := p.Input.SoundMode() == studio.AudioVoiceover
	for _, s := range p.Scenes {
		if s.Frame.Status != studio.StatusDone {
			return studio.StageFrames, "🖼 Кадри"
		}
	}
	for _, s := range p.Scenes {
		spoken := strings.TrimSpace(s.Narration) != "" || s.AudioSource != ""
		if s.Video.Status != studio.StatusDone || voice && spoken && s.Audio.Status != studio.StatusDone {
			return studio.StageRender, "🎬 Відео й озвучка"
		}
	}
	if !videoReady(p) || p.Montage.Output.Stale {
		return studio.StageMontage, "✂️ Змонтувати"
	}
	return "", ""
}

func hasFrames(p studio.Project) bool {
	for _, s := range p.Scenes {
		if s.Frame.Status == studio.StatusDone && s.Frame.Path != "" {
			return true
		}
	}
	return false
}

func hasVoices(p studio.Project) bool {
	for _, s := range p.Scenes {
		if s.Audio.Status == studio.StatusDone && s.Audio.Path != "" {
			return true
		}
	}
	return false
}

func hasClips(p studio.Project) bool {
	for _, s := range p.Scenes {
		if s.Video.Status == studio.StatusDone || s.Frame.Status == studio.StatusDone {
			return true
		}
	}
	return false
}

// coverPath is the best cover picture of a project ("" = none).
func (b *Bot) coverPath(p studio.Project) string {
	for _, a := range []studio.Asset{p.Montage.CoverFinal, p.Montage.Cover} {
		if a.Status == studio.StatusDone && a.Path != "" {
			if path := b.e.Abs(p.ID, a.Path); isFile(path) {
				return path
			}
		}
	}
	return ""
}

// retryable: «Повторити невдалі» applies (a failed or stopped job, or one
// that left failed items).
func retryable(p studio.Project) bool {
	j := p.Job
	return j != nil && (j.Status == studio.JobError || j.Status == studio.JobCancelled || j.Status == studio.JobDone && j.Failures > 0)
}

// card is a project's card.
func (b *Bot) card(p studio.Project) (string, notify.InlineKeyboard) {
	k := key(p.ID)
	icon, words, pct, running := projState(p)
	var sb strings.Builder
	sb.WriteString("📂 " + title(p.Title))
	fmt.Fprintf(&sb, "\nКрок %d з 5 · %s", max(1, p.Step), stepName(p.Step))
	if n := len(p.Scenes); n > 0 {
		sb.WriteString(" · " + count(n, "сцена", "сцени", "сцен"))
	}
	if p.Input.Aspect != "" {
		sb.WriteString(" · " + esc(p.Input.Aspect))
	}
	sb.WriteString("\n" + icon + " " + esc(words))
	if pct >= 0 {
		sb.WriteString("\n" + bar(pct))
	}
	if j := p.Job; j != nil && j.Status == studio.JobDone && j.Failures > 0 && !running {
		sb.WriteString("\n⚠️ Не вдалося: " + count(j.Failures, "елемент", "елементи", "елементів"))
	}
	blocked := blockedJob(p.Job) && !running
	if blocked {
		if n := len(studio.MontageGaps(&p, "")); n > 0 {
			fmt.Fprintf(&sb, "\nНе готово сцен: %d з %d — обери, як змонтувати.", n, len(p.Scenes))
		}
	}
	if p.Reference.Active() && p.Reference.Title != "" {
		sb.WriteString("\n🔁 За прикладом: " + esc(clipName(p.Reference.Title, 60)))
	}

	var kb notify.InlineKeyboard
	if running {
		kb = append(kb, row(btn("⏹ Зупинити", "ps:"+k), btn("🔄 Оновити", "p:"+k)))
	} else {
		var r []notify.Button
		if st, label := nextStage(p); st != "" {
			r = append(r, btn(label, "pn:"+k))
		}
		if !videoReady(p) && (strings.TrimSpace(p.Input.Text) != "" || len(p.Scenes) > 0 || p.Input.VoiceFile != "") {
			r = append(r, btn("🚀 Автопілот", "pa:"+k))
		}
		if len(r) > 0 {
			kb = append(kb, r)
		}
		if retryable(p) {
			label := "🔁 Повторити невдалі"
			if blocked {
				label = "🔁 Перезняти невдалі"
			}
			kb = append(kb, row(btn(label, "pr:"+k)))
		}
	}
	var media []notify.Button
	if hasFrames(p) {
		media = append(media, btn("🖼 Кадри", "pf:"+k+":0"))
	}
	if hasVoices(p) {
		media = append(media, btn("🎙 Озвучка", "pv:"+k+":0"))
	}
	if len(media) > 0 {
		kb = append(kb, media)
	}
	if !running && hasClips(p) {
		label := "✂️ Монтаж"
		switch {
		case videoReady(p):
			label = "✂️ Змонтувати знову"
		case blocked:
			label = "✂️ Обрати, як змонтувати" // the montage question (gaps.go)
		}
		kb = append(kb, row(btn(label, "pm:"+k)))
	}
	if videoReady(p) {
		r := []notify.Button{btn("⬇️ Надіслати відео", "v:"+k)}
		if p.Montage.SRTPath != "" {
			r = append(r, btn("📄 .srt", "s:"+k))
		}
		kb = append(kb, r)
	}
	if b.coverPath(p) != "" {
		kb = append(kb, row(btn("🖼 Обкладинка", "pc:"+k)))
	} else if videoReady(p) && !running {
		kb = append(kb, row(btn("🎨 Зробити обкладинку", "pc:"+k)))
	}
	kb = append(kb, row(btn("🗑 Видалити", "pd:"+k), btn("◀️ Проєкти", "l:0")))
	return sb.String(), kb
}

// ---- queue -----------------------------------------------------------------------------

func (b *Bot) queueView() (string, notify.InlineKeyboard) {
	q := b.e.Queue()
	var sb strings.Builder
	var kb notify.InlineKeyboard
	sb.WriteString("⏳ <b>Черга</b>")
	if len(q.Running) == 0 {
		sb.WriteString("\nЗараз нічого не генерується.")
	} else {
		sb.WriteString("\nЗараз генерується: " + strconv.Itoa(len(q.Running)))
	}
	jobs := 0
	for i, it := range q.Running {
		if i == 12 {
			sb.WriteString("\n… і ще " + strconv.Itoa(len(q.Running)-i))
			break
		}
		label := it.Label
		if m := strings.TrimSpace(it.Message); m != "" && it.Regen == "" && m != "Запуск…" {
			label = m
		}
		fmt.Fprintf(&sb, "\n\n%s\n%s", title(it.ProjectTitle), esc(clipName(label, 120)))
		if it.Regen == "" || it.Progress > 0 {
			sb.WriteString("\n" + bar(it.Progress))
		}
		if it.Regen == "" && jobs < 8 {
			jobs++
			kb = append(kb, row(btn("⏹ "+clipName(it.ProjectTitle, 34), "qc:"+key(it.ProjectID))))
		}
	}
	errs := 0
	for _, it := range q.Recent {
		if it.Status != studio.JobError || errs == 3 {
			continue
		}
		if errs == 0 {
			sb.WriteString("\n\n<b>Нещодавні помилки</b>")
		}
		errs++
		fmt.Fprintf(&sb, "\n⚠️ %s — %s: %s", title(it.ProjectTitle), esc(it.Label), esc(firstLine(it.Error, 120)))
		if it.Regen == "" && it.Retryable() {
			kb = append(kb, row(btn("🔁 "+clipName(it.ProjectTitle, 34), "pr:"+key(it.ProjectID))))
		}
	}
	var last []notify.Button
	if jobs > 0 {
		last = append(last, notify.Button{Text: "⏹ Зупинити все", Data: "qa", Style: "danger"})
	}
	last = append(last, btn("🔄 Оновити", "q"))
	kb = append(kb, last)
	return sb.String(), kb
}

// ---- profiles --------------------------------------------------------------------------

const profilesPage = 8

func (b *Bot) profilesView(page int) (string, notify.InlineKeyboard) {
	list := b.e.Profiles()
	def := b.cfg.Get().Autopilot.DefaultProfileID
	defName := "типові налаштування студії"
	for _, p := range list {
		if p.ID == def {
			defName = p.Name
		}
	}
	text := "⚙️ <b>Профілі</b>\nПрофіль за замовчуванням: <b>" + esc(defName) + "</b>\n" +
		"Його бере автопілот і новий проєкт, якщо профіль не вибрано. Натисни, щоб змінити."
	pages := max(1, (len(list)+profilesPage-1)/profilesPage)
	page = max(0, min(page, pages-1))
	var kb notify.InlineKeyboard
	mark := func(on bool) string {
		if on {
			return "✅ "
		}
		return ""
	}
	if page == 0 {
		kb = append(kb, row(btn(mark(def == "")+"Типові налаштування", "fs:-")))
	}
	for _, p := range list[page*profilesPage : min(len(list), (page+1)*profilesPage)] {
		label := mark(p.ID == def) + clipName(p.Name, 36)
		if p.Aspect != "" {
			label += " · " + p.Aspect
		}
		kb = append(kb, row(btn(label, "fs:"+key(p.ID))))
	}
	if pages > 1 {
		var nav []notify.Button
		if page > 0 {
			nav = append(nav, btn("◀️", "f:"+strconv.Itoa(page-1)))
		}
		if page < pages-1 {
			nav = append(nav, btn("▶️", "f:"+strconv.Itoa(page+1)))
		}
		kb = append(kb, nav)
	}
	return text, kb
}

// profileByKey finds a profile id by its key ("-" = none).
func (b *Bot) profileByKey(k string) (string, string, bool) {
	if k == "-" {
		return "", "Типові налаштування", true
	}
	for _, p := range b.e.Profiles() {
		if key(p.ID) == k {
			return p.ID, p.Name, true
		}
	}
	return "", "", false
}

// errText for the user: the engine's friendly message.
func userErr(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, studio.ErrNotFound):
		return "Проєкт не знайдено — можливо, його вже видалено."
	case errors.Is(err, studio.ErrClosing):
		return "Студія закривається — спробуй, коли вона знову запуститься."
	}
	return err.Error()
}

// setDefaultProfile makes pid the profile autopilot and new projects take
// ("" = the app defaults).
func (b *Bot) setDefaultProfile(pid string) error {
	return b.cfg.Update(func(c *config.Config) { c.Autopilot.DefaultProfileID = pid })
}
