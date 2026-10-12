package settings

// «Google AI Studio»: the Gemini API keys that text, frames, video (and Google
// Cloud TTS without its own key) use when Google is chosen. Keys are added one
// at a time or many at once («Додати кілька»: tts.ParseGoogleKeys reads past
// labels, quotes and junk, refuses other services' keys), then every key is
// checked with a free models.list call (Engine.CheckGoogleKeys, a few at a
// time). The list shows each key's state from the rotation (gemini.Pool):
// working, resting after a quota hit and until when, rejected — folded when
// long (keylist.go). The models live in the cards that use them (Текст,
// Кадри, Відео).
//
// The add field is a scratch input: it is never synced from the settings, and
// Enter or «Додати» takes whatever it holds.

import (
	"strings"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
)

// googleKeysURL is where Gemini API keys are made.
const googleKeysURL = "https://aistudio.google.com/apikey"

type googleCard struct {
	add      field // one key, several on a line, or (bulk) a pasted list
	addBtn   gui.Clickable
	bulk     bool
	bulkBtn  gui.Clickable
	cancel   gui.Clickable
	pasted   pasteNote
	dropBad  gui.Clickable
	clearAll gui.Clickable
	remove   []gui.Clickable
	list     keyList
	docs     gui.Clickable
	test     gui.Clickable
	check    check
	saved    savedNote
	// status is the rotation's view of the keys (tests pin it).
	status func() []gemini.KeyStatus
}

// googleVideoOptions are the Google video models served today: Gemini Omni
// Flash, and the Veo 3.1 previews until their shutdown (then they disappear).
// An id typed in an older version stays visible as is.
func googleVideoOptions(current string) []gui.SelectOption {
	var opts []gui.SelectOption
	found := false
	for _, m := range config.GoogleVideoModels(time.Now()) {
		o := gui.SelectOption{Value: m.ID, Label: m.Label}
		switch {
		case config.GoogleOmni(m.ID):
			o.Hint = "до 10 с · 3×3"
		case !m.Retires.IsZero():
			o.Hint = "вимикається " + m.Retires.Format("2") + " " + monthGen[m.Retires.Month()]
		}
		found = found || m.ID == current
		opts = append(opts, o)
	}
	if c := strings.TrimSpace(current); c != "" && !found {
		opts = append(opts, gui.SelectOption{Value: c, Label: c})
	}
	return opts
}

// monthGen are Ukrainian month names in the genitive ("22 жовтня").
var monthGen = [...]string{"", "січня", "лютого", "березня", "квітня", "травня", "червня",
	"липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}

// googleUses: what the Google keys serve now.
func googleUses(cfg config.Config) []string {
	var uses []string
	if cfg.Text.Provider == "google" {
		uses = append(uses, "текст")
	}
	if cfg.Image.Provider == "google" {
		uses = append(uses, "кадри")
	}
	if cfg.Video.Provider == "google" {
		uses = append(uses, "відео")
	}
	if cfg.TTS.Provider == "google" && len(ttsKeys(cfg.TTS)) == 0 {
		uses = append(uses, "озвучка") // Google Cloud TTS borrows these keys
	}
	return uses
}

// googleKeyItems pairs the keys with their rotation states (by place).
func googleKeyItems(keys []string, st []gemini.KeyStatus, remove []gui.Clickable) []keyItem {
	byIdx := map[int]gemini.KeyStatus{}
	for _, s := range st {
		byIdx[s.Index] = s
	}
	items := make([]keyItem, 0, len(keys))
	for i, k := range keys {
		s, ok := byIdx[i]
		if ok && s.Mask != config.Mask(k) {
			ok = false // the list changed since the pool saw it
		}
		it := keyItem{name: config.Mask(k), state: kNew, pill: gui.StatusPill{State: gui.StateEmpty, Text: "ще не перевірений", Small: true}}
		if i < len(remove) {
			it.remove = &remove[i]
		}
		if ok {
			switch s.State {
			case gemini.KeyOK:
				it.state, it.pill = kOK, gui.StatusPill{State: gui.StateDone, Text: "працює", Small: true}
				switch {
				case s.Requests > 0:
					it.meta = "запитів у цьому сеансі: " + itoa(s.Requests)
				case !s.Checked.IsZero():
					it.meta = "перевірено о " + s.Checked.Local().Format("15:04")
				}
			case gemini.KeyResting:
				it.state, it.pill = kSpent, gui.StatusPill{State: gui.StateQueued, Text: "відпочиває до " + untilText(s.Until), Small: true}
				it.meta = firstText(s.LastError, "Google обмежив ключ — студія бере інші, доки він відпочиває")
			case gemini.KeyError:
				it.state, it.pill = kErr, gui.StatusPill{State: gui.StateError, Text: "помилка", Small: true}
				it.meta, it.metaErr = firstText(s.LastError, "Google не приймає цей ключ"), true
			}
		}
		items = append(items, it)
	}
	return items
}

// untilText: "14:05", or "завтра 09:00".
func untilText(t time.Time) string {
	l, now := t.Local(), time.Now().Local()
	if l.YearDay() != now.YearDay() || l.Year() != now.Year() {
		return "завтра " + l.Format("15:04")
	}
	return l.Format("15:04")
}

// googleUsage is the second line of a folded list.
func googleUsage(st []gemini.KeyStatus) string {
	req, resting := 0, 0
	var soonest time.Time
	for _, s := range st {
		req += s.Requests
		if s.State == gemini.KeyResting {
			resting++
			if soonest.IsZero() || s.Until.Before(soonest) {
				soonest = s.Until
			}
		}
	}
	parts := []string{"запитів у цьому сеансі: " + itoa(req)}
	if resting > 0 {
		parts = append(parts, "найближчий звільниться о "+untilText(soonest))
	}
	return strings.Join(parts, " · ")
}

func (p *Page) googleCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.google
	cfg := p.now
	flash := func() { s.saved.want = true }
	keys := cfg.Google.Keys
	if len(s.remove) < len(keys) {
		s.remove = append(s.remove, make([]gui.Clickable, len(keys)-len(s.remove))...)
	}
	status := c.E.GoogleKeyStatus
	if s.status != nil {
		status = s.status
	}
	st := status()
	items := googleKeyItems(keys, st, s.remove)
	var bad []string
	for i, it := range items {
		if it.state == kErr {
			bad = append(bad, keys[i])
		}
	}

	// The add field: Enter or «Додати» takes what it holds (never synced).
	s.add.Changed(gtx)
	if s.add.Submitted(gtx) {
		p.googleAdd(c, s.add.Editor.Text())
	}
	if s.addBtn.Clicked(gtx) {
		p.googleAdd(c, s.add.Editor.Text())
	}
	if s.bulkBtn.Clicked(gtx) {
		s.bulk, s.pasted = true, pasteNote{}
		s.add.Focus(gtx)
	}
	if s.cancel.Clicked(gtx) {
		s.bulk, s.pasted = false, pasteNote{}
		s.add.SetText("")
	}
	if s.docs.Clicked(gtx) {
		c.OpenPath(googleKeysURL)
	}
	for i := range keys {
		if s.remove[i].Clicked(gtx) {
			k := keys[i]
			c.Confirm(core.Confirm{
				Title: "Прибрати ключ " + config.Mask(k) + "?",
				Text:  "Його буде видалено з цього комп'ютера. Кроки, що працюють через Google, використовуватимуть інші ключі.",
				Icon:  gui.IconTrash2, OK: "Прибрати", Danger: true,
				OnOK: func() { p.googleDrop(c, []string{k}, flash) },
			})
		}
	}
	if s.dropBad.Clicked(gtx) && len(bad) > 0 {
		drop := append([]string(nil), bad...)
		masks := make([]string, 0, len(drop))
		for _, k := range drop {
			masks = append(masks, config.Mask(k))
		}
		n := len(drop)
		c.Confirm(core.Confirm{
			Title: "Прибрати " + core.Count(n, "ключ", "ключі", "ключів") + " з помилкою?",
			Text: "Google не приймає " + gui.PluralUK(n, "цей ключ", "ці ключі", "ці ключі") + ": " + strings.Join(masks, ", ") +
				". " + gui.PluralUK(n, "Він зникне", "Вони зникнуть", "Вони зникнуть") + " з цього комп'ютера, решта ключів працює далі.",
			Icon: gui.IconTrash2, OK: "Прибрати", Danger: true,
			OnOK: func() {
				p.googleDrop(c, drop, func() {
					flash()
					c.Toast(gui.ToneSuccess, "Прибрано "+core.Count(n, "ключ", "ключі", "ключів")+" з помилкою")
				})
			},
		})
	}
	if s.clearAll.Clicked(gtx) {
		n := len(keys)
		c.Confirm(core.Confirm{
			Title: "Прибрати всі ключі Google?",
			Text:  core.Count(n, "ключ", "ключі", "ключів") + " буде видалено з цього комп'ютера. Кроки, що працюють через Google, зупиняться, доки не додаси нові.",
			Icon:  gui.IconTrash2, OK: "Прибрати всі", Danger: true,
			OnOK: func() {
				p.cfg.set(c, func(cf *config.Config) { cf.Google.Keys = nil }, func() { flash(); s.check.reset() })
			},
		})
	}
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, c.E.CheckGoogleKeys)
	}

	uses := googleUses(cfg)
	usage := "Зараз не використовується — потрібні, лише якщо в картках вище вибрано Google."
	if len(uses) > 0 {
		usage = "Використовується для: " + strings.Join(uses, ", ") + "."
	}
	empty := s.add.Editor.Len() == 0
	header := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				title := "Ключі Gemini API"
				if n := len(keys); n > 1 {
					title += " · " + core.Count(n, "ключ", "ключі", "ключів")
				}
				return gui.Text{Style: th.Label, MaxLines: 1}.Layout(gtx, th, title)
			}),
			layout.Rigid(func(gtx C) D {
				return gui.Link{Text: "Де взяти ключ", Trail: gui.IconExternalLink, Style: th.Label.Sized(14)}.Layout(gtx, th, &s.docs)
			}),
		)
	}
	rule := gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
		"Ключі чергуються. Вичерпаний відпочиває, доки Google не скине ліміт (хвилинний — за хвилину, денний — опівночі за тихоокеанським часом); недійсний пропускається. Ліміти діють на проєкт Google, тож бери ключі з різних проєктів.")
	keyList := func(gtx C) D {
		if len(keys) == 0 {
			return note(gtx, th, gui.IconKeyRound, "Ключів ще немає. Створи ключ на aistudio.google.com → Get API key і встав його нижче.", nil)
		}
		return s.list.layout(gtx, th, items, googleUsage(st), spentResting)
	}
	addRow := func(gtx C) D {
		if s.bulk {
			return gui.VStack(gtx, gui.Sp12,
				func(gtx C) D {
					return s.add.Layout(gtx, th, gui.FieldStyle{
						Label:       "Кілька ключів Google",
						Placeholder: "Встав ключі AQ.… або AIza… — кожен з нового рядка, через кому чи пробіл",
						Hint:        "Підписи на кшталт «key1:» і лапки студія пропустить. Ключі видно лише тут, доки їх не додаси.",
						MinLines:    4, MaxLines: 8,
					})
				},
				func(gtx C) D {
					return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
						func(gtx C) D {
							return gui.Button{Kind: gui.Secondary, Icon: gui.IconPlus, Text: "Додати й перевірити", Disabled: empty}.Layout(gtx, th, &s.addBtn)
						},
						func(gtx C) D {
							return gui.Button{Kind: gui.Ghost, Text: "Скасувати"}.Layout(gtx, th, &s.cancel)
						},
					)
				},
			)
		}
		field := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					ph := "Встав ключ AQ.… або AIza…"
					if len(keys) > 0 {
						ph = "Додати ще один ключ AQ.… або AIza…"
					}
					return s.add.Layout(gtx, th, gui.FieldStyle{Placeholder: ph, Password: true})
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Icon: gui.IconPlus, Text: "Додати", Disabled: empty}.Layout(gtx, th, &s.addBtn)
				}),
			)
		}
		more := func(gtx C) D {
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp4}.Layout(gtx,
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconClipboardPaste, Text: "Додати кілька",
						Tip: "Встав список ключів — студія сама їх розбере й перевірить"}.Layout(gtx, th, &s.bulkBtn)
				},
				func(gtx C) D {
					if len(bad) == 0 {
						return D{}
					}
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2,
						Text: "Видалити всі з помилкою (" + itoa(len(bad)) + ")"}.Layout(gtx, th, &s.dropBad)
				},
			)
		}
		return gui.VStack(gtx, gui.Sp8, field, more)
	}
	pasted := func(gtx C) D {
		if s.pasted.msg == "" {
			return D{}
		}
		return resultPanel(gtx, th, false, s.pasted.ok, s.pasted.msg, "", s.pasted.title)
	}
	var clearBtn layout.Widget
	if len(keys) > 1 {
		clearBtn = func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Icon: gui.IconTrash2, Text: "Прибрати всі"}.Layout(gtx, th, &s.clearAll)
		}
	}
	title := "Google AI Studio"
	if n := len(keys); n > 0 {
		title += " · " + core.Count(n, "ключ", "ключі", "ключів")
	}
	checkText := "Перевірити ключі"
	if len(keys) == 1 {
		checkText = "Перевірити ключ"
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconSparkles, Title: title, Text: usage,
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	},
		func(gtx C) D { return gui.VStack(gtx, gui.Sp8, header, rule, keyList, addRow, pasted) },
		actions(checkButton(th, &s.test, &s.check, checkText, len(keys) == 0), clearBtn),
		func(gtx C) D {
			if s.check.done && s.check.ok && len(bad) > 0 {
				return resultPanel(gtx, th, false, true, s.check.msg, "", "Працюють не всі ключі")
			}
			return s.check.result(gtx, th, "Перевір ключі на aistudio.google.com — можливо, ключ вичерпав денний ліміт або його видалено.")
		},
	)
}

// googleAdd adds pasted Google keys: one, several on a line, or a list (bulk
// mode). The outcome stays in the card; after the save every key is checked.
// false = nothing new (the text stays in the field to be fixed).
func (p *Page) googleAdd(c *core.Ctx, text string) bool {
	s := &p.google
	if strings.TrimSpace(text) == "" {
		return false
	}
	have := p.cfg.get(c).Google.Keys
	res := tts.ParseGoogleKeys(text, have)
	note := pasteNote{msg: res.SummaryFor(tts.GoogleKeysLabel)}
	switch {
	case len(res.Keys) == 1:
		note.ok, note.title = true, "Ключ додано — перевіряємо його"
	case len(res.Keys) > 1:
		note.ok, note.title = true, "Ключі додано — перевіряємо кожен"
	case res.Junk == 0 && res.Foreign == 0:
		note.ok, note.title = true, "Ці ключі вже є"
	default:
		note.title = "Ключів Google не знайдено"
	}
	if res.Junk > 0 {
		note.msg += ". Ключ Google AI Studio починається з AQ. (новий формат) або AIza (старий)."
	}
	s.pasted = note
	if len(res.Keys) == 0 {
		return false // keep the text: there is something to fix
	}
	s.add.SetText("")
	s.bulk = false
	add := res.Keys
	p.cfg.set(c, func(cf *config.Config) { cf.Google.Keys = append(cf.Google.Keys, add...) }, func() {
		s.saved.want = true
		p.runCheck(c, &s.check, c.E.CheckGoogleKeys) // every key, a few at a time
	})
	return true
}

// googleDrop removes keys from the Google list.
func (p *Page) googleDrop(c *core.Ctx, drop []string, done func()) {
	gone := map[string]bool{}
	for _, k := range drop {
		gone[k] = true
	}
	p.cfg.set(c, func(cf *config.Config) {
		var out []string
		for _, x := range cf.Google.Keys {
			if !gone[x] {
				out = append(out, x)
			}
		}
		cf.Google.Keys = out
	}, done)
}
