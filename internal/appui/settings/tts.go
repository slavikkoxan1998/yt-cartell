package settings

// «Озвучка»: the TTS provider (tts.Providers presets) with its own settings —
// every provider keeps its own keys, address, model and narrator voice
// (config.TTS.Providers): switching shows the new provider's, empty when it
// was never set up, and the voice list follows the provider. The keys rotate
// (APIKey + Keys); each key's state comes from Engine.TTSKeyStatus — working,
// exhausted until the 1st of the month, failing (masked, never shown), with
// the plan and usage a check found. Keys can be pasted many at once
// («Додати кілька»: tts.ParseKeys reads past labels and junk and refuses keys
// of another provider), then every key is checked; «Видалити всі з помилкою»
// drops the keys the provider rejected. A list of more than three keys folds
// into one summary line (keylist.go).

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
	ttscat "ytcartell/internal/tts/catalog"
)

type ttsCard struct {
	provider gui.Select
	base     field
	model    field
	modelSel gui.Select
	voiceID  field
	voice    pickers.Voice
	// add takes one key (or several on one line); in bulk mode it is a
	// multi-line area for a pasted list.
	add     field
	addBtn  gui.Clickable
	bulk    bool
	bulkBtn gui.Clickable
	cancel  gui.Clickable
	pasted  pasteNote
	dropBad gui.Clickable
	remove  []gui.Clickable
	list    keyList // folds a long key list (keylist.go)
	shared  keyList // the borrowed Google keys
	test    gui.Clickable
	check   check
	reset   gui.Clickable
	reload  gui.Clickable
	docs    gui.Clickable
	saved   savedNote
	catalog catalogUI // «Вибрати з каталогу» (catalogpick.go)
	spec    specPanel // «Власний сервер» described by a spec (audiospec.go)

	// Piper local state & widgets
	piperStatus     piperLocalStatus
	piperBin        field
	piperPickBin    gui.Clickable
	piperInstall    gui.Clickable
	piperCancel     gui.Clickable
	piperAdvToggle  gui.Clickable
	piperAdvanced   bool
	piperInstalling bool
	piperInstallErr string
	piperPct        float32
	piperMsg        string
	piperBar        gui.Anim
	piperStop       context.CancelFunc
}

type piperLocalStatus struct {
	sig  string
	at   time.Time
	bin  string
	ok   bool
	hint string
}

func (s *ttsCard) piperLocalState(c *core.Ctx, cfg config.Config, now time.Time) piperLocalStatus {
	sig := cfg.TTS.BaseURL
	if s.piperStatus.sig != sig || now.Sub(s.piperStatus.at) > 3*time.Second || s.piperStatus.at.IsZero() {
		bin, ok, hint := c.E.PiperStatus()
		s.piperStatus = piperLocalStatus{sig: sig, at: now, bin: bin, ok: ok, hint: hint}
	}
	return s.piperStatus
}

// pasteNote is the outcome of the last add («Додано 7 ключів · 2 вже були ·
// 1 не схожий на ключ»).
type pasteNote struct {
	msg, title string
	ok         bool
}

// ttsProvider is the preset of id (the first one when unknown).
func ttsProvider(id string) tts.ProviderInfo {
	list := tts.Providers()
	for _, p := range list {
		if p.ID == id {
			return p
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return tts.ProviderInfo{ID: id, Label: id}
}

// ttsKeys is the rotation list of the active provider: APIKey first, then Keys.
func ttsKeys(t config.TTS) []string {
	var out []string
	if k := strings.TrimSpace(t.APIKey); k != "" {
		out = append(out, k)
	}
	for _, k := range t.Keys {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// ttsGateway: the provider takes keys and voices of any shape (an own server,
// an OpenAI-compatible gateway).
func ttsGateway(t config.TTS) bool {
	base := strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
	return t.Provider == "custom" || (t.Provider == "openai" && base != "" && !strings.EqualFold(base, "https://api.openai.com/v1"))
}

func (p *Page) ttsCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.tts
	cfg := p.now
	flash := func() { s.saved.want = true }
	if v, ok := s.provider.Changed(gtx); ok && v != cfg.TTS.Provider {
		// The provider's own keys, model and voice come back; the voice list
		// reloads for it (core.Ctx.Voices follows the provider).
		p.cfg.set(c, func(cf *config.Config) { cf.TTS.Switch(v) }, flash)
		s.check.reset()
		s.pasted = pasteNote{}
		s.bulk = false
		s.add.SetText("")
		s.add.edited = false
	}
	if v, ok := s.modelSel.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.TTS.Model = v }, flash)
	}
	p.ttsCatalog(gtx, c, &s.catalog, cfg.TTS, flash, func() {
		// The fields adopt the picked provider, model, voice and address.
		s.base.edited, s.model.edited, s.voiceID.edited = false, false, false
		s.spec.draft, s.spec.err, s.spec.editor.edited = nil, "", false // a picked spec replaces the editor's
		s.check.reset()
		s.pasted = pasteNote{}
	})
	if v, ok := s.voice.Changed(); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.TTS.Voice = v }, flash)
	}
	s.voice.Flat = true // a field inside the card: cards never nest
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, c.E.TestTTS)
	}
	if s.reload.Clicked(gtx) {
		p.flushFields()
		p.cfg.barrier(c, c.ReloadVoices)
	}
	if s.reset.Clicked(gtx) {
		c.E.ResetTTSKeyErrors()
		c.Toast(gui.ToneSuccess, "Помилки ключів скинуто — студія спробує їх знову")
	}
	cfg = p.now
	pr := ttsProvider(cfg.TTS.Provider)
	gateway := ttsGateway(cfg.TTS)
	if s.docs.Clicked(gtx) && pr.DocsURL != "" {
		c.OpenPath(pr.DocsURL)
	}
	keys := ttsKeys(cfg.TTS)
	keyStates := c.E.TTSKeyStatus()
	borrowed := false // Google without a key of its own: the Google AI Studio keys
	anyFailed := false
	own := map[int]tts.KeyStatus{}
	for _, st := range keyStates {
		borrowed = borrowed || st.Shared
		anyFailed = anyFailed || st.State == tts.KeyError || st.State == tts.KeyResting
		if !st.Shared {
			own[st.Index] = st
		}
	}
	idx := ttsKeyIndex(cfg.TTS)
	var bad []string // keys the provider rejected
	for i, k := range keys {
		if st, ok := own[idx[i]]; ok && st.State == tts.KeyError {
			bad = append(bad, k)
		}
	}

	if len(s.remove) < len(keys) {
		s.remove = append(s.remove, make([]gui.Clickable, len(keys)-len(s.remove))...)
	}
	dropKeys := func(drop []string, done func()) { p.ttsDrop(c, drop, func() { flash(); done() }) }
	for i, k := range keys {
		if s.remove[i].Clicked(gtx) {
			k := k
			c.Confirm(core.Confirm{
				Title: "Прибрати ключ " + config.Mask(k) + "?",
				Text:  "Його буде видалено з цього комп'ютера. Озвучка піде через інші ключі.",
				Icon:  gui.IconTrash2, OK: "Прибрати", Danger: true,
				OnOK: func() { dropKeys([]string{k}, func() {}) },
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
			Text: pr.Label + " не приймає " + gui.PluralUK(n, "цей ключ", "ці ключі", "ці ключі") + ": " + strings.Join(masks, ", ") +
				". " + gui.PluralUK(n, "Він зникне", "Вони зникнуть", "Вони зникнуть") + " з цього комп'ютера, озвучка піде через інші ключі.",
			Icon: gui.IconTrash2, OK: "Прибрати", Danger: true,
			OnOK: func() {
				dropKeys(drop, func() {
					c.Toast(gui.ToneSuccess, "Прибрано "+core.Count(n, "ключ", "ключі", "ключів")+" з помилкою")
				})
			},
		})
	}

	addKeys := func(text string) { p.ttsAdd(c, text) }
	s.add.enterOnly = true
	s.add.bind(gtx, "", addKeys)
	if s.addBtn.Clicked(gtx) {
		addKeys(s.add.Editor.Text())
	}
	if s.bulkBtn.Clicked(gtx) {
		s.bulk, s.pasted = true, pasteNote{}
		s.add.Focus(gtx)
	}
	if s.cancel.Clicked(gtx) {
		s.bulk, s.pasted = false, pasteNote{}
		s.add.SetText("")
		s.add.edited = false
	}

	// Provider and model.
	popts := make([]gui.SelectOption, 0, len(tts.Providers()))
	for _, x := range tts.Providers() {
		popts = append(popts, gui.SelectOption{Value: x.ID, Label: x.Label})
	}
	provider := func(gtx C) D {
		return s.provider.Layout(gtx, th, gui.SelectStyle{Label: "Сервіс", Options: popts, Value: pr.ID, Leading: gui.IconMic,
			Hint: pr.Languages})
	}
	var model layout.Widget
	if len(pr.Models) > 0 {
		auto := "Автоматично"
		if pr.DefaultModel != "" {
			auto += " (" + pr.DefaultModel + ")"
		}
		mopts := []gui.SelectOption{{Value: "", Label: auto}}
		known := cfg.TTS.Model == ""
		for i, id := range pr.Models {
			o := gui.SelectOption{Value: id, Label: id, Hint: pr.ModelNote(id)}
			if i == 0 && o.Hint == "" {
				o.Hint = "рекомендована"
			}
			mopts = append(mopts, o)
			known = known || id == cfg.TTS.Model
		}
		if !known {
			mopts = append(mopts, gui.SelectOption{Value: cfg.TTS.Model, Label: cfg.TTS.Model, Hint: pr.ModelNote(cfg.TTS.Model)})
		}
		hint := pr.ModelNote(cfg.TTS.Model)
		if pr.ID == "elevenlabs" && cfg.TTS.Model == "" {
			hint = "Студія сама вибере модель під мову фільму."
		}
		model = func(gtx C) D {
			return s.modelSel.Layout(gtx, th, gui.SelectStyle{Label: "Модель", Options: mopts, Value: cfg.TTS.Model, Hint: hint})
		}
	} else {
		model = textIn(&s.model, th, "Модель", "Передається сервісу як є.", pr.DefaultModel, cfg.TTS.Model,
			func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.TTS.Model = strings.TrimSpace(v) }, flash) })
	}
	var base layout.Widget
	if pr.NeedsBaseURL || pr.ID == "openai" || pr.ID == "minimax" {
		label, ph, hint := "Адреса", "https://api.openai.com/v1", "Порожньо — офіційний OpenAI. Для сумісних серверів — їхня адреса."
		switch {
		case pr.ID == "azure":
			label, ph, hint = "Регіон", pr.BaseURLHint, "Регіон ресурсу Speech (westeurope) або його адреса."
		case pr.ID == "minimax":
			ph, hint = pr.BaseURLHint, "Порожньо — міжнародний акаунт. Китайський — https://api.minimax.cn."
		case pr.NeedsBaseURL:
			ph, hint = firstText(pr.BaseURLHint, "https://мій-сервер/tts"), ""
		}
		base = textIn(&s.base, th, label, hint, ph, cfg.TTS.BaseURL,
			func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.TTS.BaseURL = v }, flash) })
	}
	body := []layout.Widget{
		catalogLine(th, &s.catalog.open, len(ttscat.Embedded().Entries), "моделей, голосів і серверів озвучки", "сервіс, модель і голос заповняться самі"),
		func(gtx C) D { return fieldRow(gtx, 240, provider, model, base) },
	}
	if pr.ID == "custom" {
		body = append(body, p.ttsSpecPanel(c, flash))
	}
	if pr.Plans != "" {
		body = append(body, func(gtx C) D { return hintLine(gtx, th, gui.IconInfo, pr.Plans) })
	}
	body = append(body, func(gtx C) D { return p.ttsKeyList(gtx, c, cfg.TTS, keys, keyStates, pr, len(bad)) })

	// The narrator voice: a picker of the provider's voices, or an id for
	// servers without a list.
	list, loading, verr := c.Voices()
	if list.Provider != "" && list.Provider != pr.ID {
		// The switch is still being saved: nothing of the old provider shows.
		list, loading, verr = tts.VoiceList{Provider: pr.ID, CharsLeft: -1}, true, nil
	}
	if pr.ListsVoices && !gateway {
		// "" here is no voice of this provider yet (or the provider's own default).
		s.voice.Default, s.voice.Note = "Не вибрано", ""
		if cfg.TTS.Voice == "" {
			s.voice.Note = "вибери голос " + pr.Label + " — без нього нові проєкти не озвучаться"
			if pr.DefaultVoice != "" {
				s.voice.Default, s.voice.Note = "Типовий голос "+pr.Label, "звучатиме "+pr.DefaultVoice
			}
		}
		body = append(body, func(gtx C) D {
			return s.voice.Layout(gtx, c, "Голос оповідача за замовчуванням", cfg.TTS.Voice)
		})
		if w := ttsDefaultVoiceNote(th, pr, list, cfg.TTS.Voice); w != nil {
			body = append(body, w)
		}
	} else {
		ph := firstText(pr.VoiceHint, firstText(pr.DefaultVoice, "voice-id"))
		body = append(body, func(gtx C) D {
			return fieldRow(gtx, 240, textIn(&s.voiceID, th, "Голос (id)", "Оповідач за замовчуванням для нових проєктів.", ph, cfg.TTS.Voice,
				func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.TTS.Voice = strings.TrimSpace(v) }, flash) }))
		})
	}

	// Actions and the outcome («Скинути помилки» covers borrowed keys too:
	// their state lives in the same rotation).
	body = append(body, actions(
		checkButton(th, &s.test, &s.check, "", len(keys) == 0 && !borrowed && !pr.NeedsBaseURL && pr.ID != "piper"),
		func(gtx C) D {
			if !pr.ListsVoices || gateway || (len(keys) == 0 && !borrowed && pr.ID != "openai" && pr.ID != "piper") {
				return D{}
			}
			return gui.Button{Kind: gui.Ghost, Icon: gui.IconRefreshCw, Text: "Оновити голоси", Busy: loading}.Layout(gtx, th, &s.reload)
		},
		func(gtx C) D {
			if !anyFailed {
				return D{}
			}
			return gui.Button{Kind: gui.Ghost, Icon: gui.IconRotateCcw, Text: "Скинути помилки"}.Layout(gtx, th, &s.reset)
		},
	))
	advice := "Перевір ключ: " + pr.KeyHint + "."
	body = append(body, func(gtx C) D {
		if s.check.done && s.check.ok && len(bad) > 0 {
			// Some keys work, some were rejected: not «Усе працює».
			return resultPanel(gtx, th, false, true, s.check.msg, "", "Працюють не всі ключі")
		}
		return s.check.result(gtx, th, advice)
	})
	status := s.check.statusPill(th)
	if pr.ID == "piper" {
		st := s.piperLocalState(c, cfg, gtx.Now)
		switch {
		case s.piperInstalling:
			status = gui.StatusPill{State: gui.StateRunning, Percent: int(s.piperPct * 100), Text: "Встановлюємо " + strconv.Itoa(int(s.piperPct*100)) + "%", Small: true}.Widget(th)
		case st.ok:
			status = gui.StatusPill{State: gui.StateDone, Text: "Готово до роботи", Small: true}.Widget(th)
		default:
			status = gui.StatusPill{State: gui.StateEmpty, Text: "Не встановлено", Small: true}.Widget(th)
		}
	} else if (len(keys) > 0 || borrowed) && pr.ListsVoices && !gateway && !s.check.busy && !s.check.done {
		switch {
		case loading:
			status = gui.StatusPill{State: gui.StateRunning, Percent: -1, Text: "Перевіряємо", Small: true}.Widget(th)
		case verr != nil:
			msg := core.ErrorText(verr)
			body = append(body, func(gtx C) D {
				return resultPanel(gtx, th, false, false, msg, advice, "")
			})
			status = gui.StatusPill{State: gui.StateError, Text: "Не працює", Small: true}.Widget(th)
		case list.Provider == pr.ID && len(list.Voices) > 0:
			msg := ttsVoicesSummary(pr, list)
			body = append(body, func(gtx C) D { return resultPanel(gtx, th, false, true, msg, "", "Голоси на місці") })
			status = gui.StatusPill{State: gui.StateDone, Text: "Працює", Small: true}.Widget(th)
		}
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconMic, Title: "Озвучка",
		Text:  "Голос оповідача для нових проєктів: ElevenLabs, OpenAI, Cartesia та інші сервіси або свій сервер. У кожного сервісу — свої ключі й голос.",
		Trail: trails(s.saved.widget(th), status),
	}, body...)
}

// ttsAdd adds pasted keys to the active provider: one, several on a line, or
// a list (bulk mode). The outcome stays in the card («Додано 7 ключів · 2 вже
// були · 1 не схожий на ключ»); after the save every key is checked. false =
// nothing new (the text stays in the field to be fixed).
func (p *Page) ttsAdd(c *core.Ctx, text string) bool {
	s := &p.tts
	if strings.TrimSpace(text) == "" {
		return false
	}
	t := p.cfg.get(c).TTS
	res := tts.ParseKeys(text, t.Provider, ttsGateway(t), ttsKeys(t))
	note := pasteNote{msg: res.Summary(t.Provider)}
	switch {
	case len(res.Keys) == 1:
		note.ok, note.title = true, "Ключ додано — перевіряємо його"
	case len(res.Keys) > 1:
		note.ok, note.title = true, "Ключі додано — перевіряємо кожен"
	case res.Junk == 0 && res.Foreign == 0:
		note.ok, note.title = true, "Ці ключі вже є"
	}
	if res.Foreign > 0 {
		note.msg += ". Ключі іншого сервісу додай, вибравши той сервіс угорі."
	}
	s.pasted = note
	if len(res.Keys) == 0 {
		s.add.edited = true // keep the text: there is something to fix
		return false
	}
	s.add.SetText("")
	s.add.edited = false
	s.bulk = false
	add := res.Keys
	p.cfg.set(c, func(cf *config.Config) { cf.TTS.SetKeys(append(ttsKeys(cf.TTS), add...)) }, func() {
		s.saved.want = true
		p.runCheck(c, &s.check, c.E.TestTTS) // every key, a few at a time
		c.ReloadVoices()
	})
	return true
}

// ttsDrop removes keys from the active provider's list.
func (p *Page) ttsDrop(c *core.Ctx, drop []string, done func()) {
	gone := map[string]bool{}
	for _, k := range drop {
		gone[k] = true
	}
	p.cfg.set(c, func(cf *config.Config) {
		var out []string
		for _, x := range ttsKeys(cf.TTS) {
			if !gone[x] {
				out = append(out, x)
			}
		}
		cf.TTS.SetKeys(out)
	}, func() {
		if done != nil {
			done()
		}
		c.ReloadVoices()
	})
}

// ttsVoicesSummary is the voice list in one line: how many, the plan, the
// characters left and what the free plan cannot use.
func ttsVoicesSummary(pr tts.ProviderInfo, list tts.VoiceList) string {
	locked := 0
	for _, v := range list.Voices {
		if !v.Usable {
			locked++
		}
	}
	msg := core.Count(len(list.Voices), "голос", "голоси", "голосів")
	if list.Tier != "" {
		msg += " · тариф " + list.Tier
	}
	if list.CharsLeft >= 0 {
		msg += " · лишилось " + thousands(list.CharsLeft) + " " + gui.PluralUK(list.CharsLeft, "символ", "символи", "символів")
	}
	switch {
	case pr.ID == "elevenlabs" && list.Tier == "free" && locked > 0:
		msg += fmt.Sprintf(". На безкоштовному тарифі через API працюють стандартні й власні голоси; %d з бібліотеки позначено «потрібен платний тариф».", locked)
	case pr.ID == "elevenlabs" && list.Tier == "free":
		msg += ". На безкоштовному тарифі через API працюють стандартні й власні голоси; голоси з бібліотеки — лише на платному."
	case locked > 0:
		msg += fmt.Sprintf(". %d недоступні з цією моделлю чи тарифом — їх позначено.", locked)
	}
	return msg
}

// ttsDefaultVoiceNote warns that the chosen ElevenLabs default (premade)
// voice is retired on 31 December 2026 and names replacements (nil = none).
func ttsDefaultVoiceNote(th *gui.Theme, pr tts.ProviderInfo, list tts.VoiceList, id string) layout.Widget {
	if pr.ID != "elevenlabs" || list.Provider != pr.ID || id == "" || !time.Now().Before(tts.ElevenDefaultVoicesEnd) {
		return nil
	}
	var cur tts.Voice
	found := false
	for _, v := range list.Voices {
		if v.ID == id {
			cur, found = v, true
		}
	}
	if !found || cur.Category != "premade" {
		return nil
	}
	var same, other []string
	for _, v := range list.Voices {
		if !v.Usable || v.Category == "premade" || v.Name == "" {
			continue
		}
		if cur.Gender != "" && v.Gender == cur.Gender {
			same = append(same, v.Name)
		} else {
			other = append(other, v.Name)
		}
	}
	names := append(same, other...)
	if len(names) > 3 {
		names = names[:3]
	}
	tip := "власний голос (Voice Design чи клон) або голос із бібліотеки — на платному тарифі"
	if len(names) > 0 {
		tip = "зі своїх голосів підійде " + strings.Join(names, ", ")
	}
	text := firstText(cur.Name, "Цей голос") + " — стандартний голос ElevenLabs: він доступний до 31 грудня 2026. Заздалегідь вибери заміну — " + tip + "."
	return func(gtx C) D { return note(gtx, th, gui.IconClock, text, nil) }
}

// hintLine is a quiet line with an icon (no panel): notes about a provider.
func hintLine(gtx C, th *gui.Theme, icon *gui.Icon, text string) D {
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 1, Right: gui.Sp8}.Layout(gtx, icon.Widget(16, th.Pal.Text3))
		}),
		layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, text)),
	)
}

func firstText(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// ttsKeyIndex is the place of each key of ttsKeys in [APIKey, Keys...]: the
// Index its state is reported under.
func ttsKeyIndex(t config.TTS) []int {
	var out []int
	if strings.TrimSpace(t.APIKey) != "" {
		out = append(out, 0)
	}
	for i, k := range t.Keys {
		if strings.TrimSpace(k) != "" {
			out = append(out, i+1)
		}
	}
	return out
}

// ttsKeyList is the rotation list: each key masked with its state, the add
// field (one key, or a pasted list in bulk mode) and the rule in one line.
// With no key of its own, Google borrows the Google AI Studio keys: they are
// listed apart (states only; they are managed in the Google AI Studio card).
func (p *Page) ttsKeyList(gtx C, c *core.Ctx, t config.TTS, keys []string, status []tts.KeyStatus, pr tts.ProviderInfo, bad int) D {
	th := c.Th
	if pr.ID == "piper" {
		return p.ttsPiperPanel(gtx, c, p.now, func() { p.tts.saved.want = true })
	}
	s := &p.tts
	own := map[int]tts.KeyStatus{}
	var shared []tts.KeyStatus
	for _, st := range status {
		if st.Shared {
			shared = append(shared, st)
		} else {
			own[st.Index] = st
		}
	}
	idx := ttsKeyIndex(t)
	seen := map[string]tts.KeyStatus{} // a repeated key: the state of its first copy
	var items []keyItem
	var statuses []tts.KeyStatus
	for i, k := range keys {
		st, ok := own[idx[i]]
		if prev, dup := seen[k]; dup {
			st = prev
		} else if ok {
			seen[k] = st
		}
		name := config.Mask(k)
		if i == 0 {
			name += " · основний"
		}
		items = append(items, ttsKeyItem(name, st, &s.remove[i]))
		statuses = append(statuses, st)
	}
	var borrowedItems []keyItem
	if len(keys) == 0 && len(shared) > 0 {
		for _, st := range shared {
			borrowedItems = append(borrowedItems, ttsKeyItem(st.Mask, st, nil))
		}
	}
	empty := s.add.Editor.Len() == 0
	addRow := func(gtx C) D {
		if s.bulk {
			return gui.VStack(gtx, gui.Sp12,
				func(gtx C) D {
					return s.add.Layout(gtx, th, gui.FieldStyle{
						Label:       "Кілька ключів " + pr.Label,
						Placeholder: "Встав ключі — кожен з нового рядка, через кому чи пробіл",
						Hint:        "Підписи на кшталт «key1:» студія пропустить. Ключі видно лише тут, доки їх не додаси.",
						MinLines:    4, MaxLines: 8,
						PasteButton: true,
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
					ph := "Встав ключ"
					if len(keys) > 0 {
						ph = "Додати ще один ключ"
					}
					return s.add.Layout(gtx, th, gui.FieldStyle{Placeholder: ph, Password: true, PasteButton: true})
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
					if bad == 0 {
						return D{}
					}
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2,
						Text: "Видалити всі з помилкою (" + itoa(bad) + ")"}.Layout(gtx, th, &s.dropBad)
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
	hint := pr.KeyHint
	return gui.VStack(gtx, gui.Sp8,
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					title := "Ключі " + pr.Label
					if n := len(keys); n > 1 {
						title += " · " + core.Count(n, "ключ", "ключі", "ключів")
					}
					return gui.Text{Style: th.Label, MaxLines: 1}.Layout(gtx, th, title)
				}),
				layout.Rigid(func(gtx C) D {
					if pr.DocsURL == "" {
						return D{}
					}
					return gui.Link{Text: "Де взяти ключ", Trail: gui.IconExternalLink, Style: th.Label.Sized(14)}.Layout(gtx, th, &s.docs)
				}),
			)
		},
		gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Ключі чергуються; вичерпаний відпочиває до 1 числа, помилковий пропускається."),
		func(gtx C) D {
			switch {
			case len(items) > 0:
				return s.list.layout(gtx, th, items, ttsUsage(statuses), spentExhausted)
			case len(borrowedItems) > 0:
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return insetHead(gtx, th, gui.IconLink, "Позичені ключі Google",
							"Свого ключа тут немає — озвучка бере ключі з картки «Google AI Studio».", nil)
					},
					func(gtx C) D { return s.shared.layout(gtx, th, borrowedItems, ttsUsage(shared), spentExhausted) },
				)
			}
			return note(gtx, th, gui.IconKeyRound, "Ключів "+pr.Label+" ще немає. "+hint+".", nil)
		},
		addRow,
		pasted,
	)
}

// ttsKeyItem is one TTS key of the list: its masked name, its state and — for
// the card's own keys — a remove button (nil for a borrowed key).
func ttsKeyItem(name string, st tts.KeyStatus, remove *gui.Clickable) keyItem {
	it := keyItem{name: name, state: kNew, pill: gui.StatusPill{State: gui.StateEmpty, Text: "ще не перевірений", Small: true},
		meta: keyMeta(st), metaErr: st.State == tts.KeyError, remove: remove}
	switch st.State {
	case tts.KeyOK:
		if !st.LastUsed.IsZero() || !st.Checked.IsZero() { // a key nobody used or checked is not known to work
			it.state, it.pill = kOK, gui.StatusPill{State: gui.StateDone, Text: "працює", Small: true}
		}
	case tts.KeyExhausted:
		it.state, it.pill = kSpent, gui.StatusPill{State: gui.StateStale, Text: "вичерпано до " + dayMonth(st.Until), Small: true}
	case tts.KeyError:
		it.state, it.pill = kErr, gui.StatusPill{State: gui.StateError, Text: "помилка", Small: true}
	case tts.KeyResting:
		it.state, it.pill = kSpent, gui.StatusPill{State: gui.StateQueued, Text: "відпочиває до " + st.Until.Local().Format("15:04"), Small: true}
	}
	return it
}

// ttsUsage is the usage line of a folded list: «використано 412 000 з 1 040
// 000 символів» when the plans are known, else the characters voiced this
// month here ("" = nothing known).
func ttsUsage(st []tts.KeyStatus) string {
	used, limit, counted := 0, 0, 0
	for _, s := range st {
		if s.Limit > 0 {
			used += s.UsedChars
			limit += s.Limit
		} else {
			counted += s.UsedChars
		}
	}
	switch {
	case limit > 0 && counted > 0:
		return "використано " + thousands(used) + " з " + thousands(limit) + " символів (де тариф відомий) · ще " + thousands(counted) + " цього місяця"
	case limit > 0:
		return "використано " + thousands(used) + " з " + thousands(limit) + " " + gui.PluralUK(limit, "символу", "символів", "символів")
	case counted > 0:
		return "цього місяця озвучено " + thousands(counted) + " " + gui.PluralUK(counted, "символ", "символи", "символів")
	}
	return "використано: ще невідомо — студія порахує після озвучки"
}

// keyMeta is the line under a key: its error, or the plan and the usage a
// check or the provider reported ("тариф free · використано 8 599 з 10 000
// символів"), or the check's note ("баланс 12.50 $").
func keyMeta(st tts.KeyStatus) string {
	if st.State == tts.KeyError && st.LastError != "" {
		return st.LastError
	}
	var parts []string
	if st.Plan != "" {
		parts = append(parts, "тариф "+st.Plan)
	}
	switch {
	case st.Limit > 0:
		parts = append(parts, "використано "+thousands(st.UsedChars)+" з "+thousands(st.Limit)+" "+gui.PluralUK(st.Limit, "символу", "символів", "символів"))
	case st.UsedChars > 0:
		parts = append(parts, "цього місяця "+thousands(st.UsedChars)+" "+gui.PluralUK(st.UsedChars, "символ", "символи", "символів"))
	}
	if len(parts) == 0 {
		return st.Info
	}
	return strings.Join(parts, " · ")
}

// dayMonth is "1 лис" (the day a key is usable again).
func dayMonth(t time.Time) string {
	if t.IsZero() {
		return "1 числа"
	}
	months := [...]string{"січ", "лют", "бер", "кві", "тра", "чер", "лип", "сер", "вер", "жов", "лис", "гру"}
	t = t.Local()
	return fmt.Sprintf("%d %s", t.Day(), months[t.Month()-1])
}

func (p *Page) ttsPiperPanel(gtx C, c *core.Ctx, cfg config.Config, flash func()) D {
	th := c.Th
	s := &p.tts
	st := s.piperLocalState(c, cfg, gtx.Now)

	if s.piperInstall.Clicked(gtx) {
		p.installPiper(c)
	}
	if s.piperCancel.Clicked(gtx) && s.piperStop != nil {
		s.piperStop()
	}
	if s.piperAdvToggle.Clicked(gtx) {
		s.piperAdvanced = !s.piperAdvanced
	}
	if s.piperPickBin.Clicked(gtx) {
		var exts []string
		if runtime.GOOS == "windows" {
			exts = []string{"exe"}
		}
		c.PickFile("Програма Piper TTS (piper)", exts, func(path string) {
			s.base.SetText(path)
			p.cfg.set(c, func(cf *config.Config) { cf.TTS.BaseURL = path }, flash)
		})
	}

	state := func(gtx C) D {
		pill := gui.StatusPill{State: gui.StateEmpty, Text: "Не знайдено", Small: true}
		if st.ok {
			pill = gui.StatusPill{State: gui.StateDone, Text: "Готово до роботи", Small: true}
		}
		if s.piperInstalling {
			pill = gui.StatusPill{State: gui.StateRunning, Percent: int(s.piperPct * 100), Small: true, Text: "Встановлюємо " + strconv.Itoa(int(s.piperPct*100)) + "%"}
		}
		hint := st.hint
		if hint == "" {
			hint = "Локальний синтез мови на CPU без інтернету (Linux Mint)."
		}
		return insetHead(gtx, th, gui.IconCpu, "Piper TTS на цьому комп'ютері", hint, pill.Widget(th))
	}

	installRow := func(gtx C) D {
		text := "Завантажити Piper"
		kind := gui.Primary
		if st.ok {
			text = "Перевстановити Piper"
			kind = gui.Secondary
		}
		return gui.Flow{Gap: gui.Sp12}.Layout(gtx,
			func(gtx C) D {
				return gui.Button{Kind: kind, Icon: gui.IconDownload, Text: text, Busy: s.piperInstalling}.Layout(gtx, th, &s.piperInstall)
			},
			func(gtx C) D {
				if !s.piperInstalling {
					return D{}
				}
				return gui.Button{Kind: gui.Ghost, Icon: gui.IconX, Text: "Скасувати"}.Layout(gtx, th, &s.piperCancel)
			},
		)
	}

	progress := func(gtx C) D {
		if !s.piperInstalling {
			return D{}
		}
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D {
				return gui.ProgressBar{Value: s.piperPct, Tone: gui.ToneRunning, Anim: &s.piperBar}.Layout(gtx, th)
			},
			gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, s.piperMsg),
		)
	}

	installErr := func(gtx C) D {
		if s.piperInstallErr == "" || s.piperInstalling {
			return D{}
		}
		return resultPanel(gtx, th, false, false, s.piperInstallErr, "Перевір підключення до мережі та спробуй ще раз.", "")
	}

	advanced := func(gtx C) D {
		chev := gui.IconChevronDown
		if s.piperAdvanced {
			chev = gui.IconChevronUp
		}
		head := func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Додатково: власний шлях до програми Piper", Trail: chev}.Layout(gtx, th, &s.piperAdvToggle)
		}
		if !s.piperAdvanced {
			return head(gtx)
		}
		return gui.VStack(gtx, gui.Sp12, head,
			labeled(th, "Шлях до Piper", "Порожньо — шукати автоматично в системі, PATH та ~/.local/bin.", func(gtx C) D {
				s.base.bind(gtx, cfg.TTS.BaseURL, func(v string) {
					p.cfg.set(c, func(cf *config.Config) { cf.TTS.BaseURL = strings.Trim(strings.TrimSpace(v), `"`) }, flash)
				})
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D { return s.base.Layout(gtx, th, gui.FieldStyle{Placeholder: "Автоматично"}) }),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Icon: gui.IconFolderOpen, Tip: "Вибрати файл"}.Layout(gtx, th, &s.piperPickBin)
					}),
				)
			}),
		)
	}

	ws := []layout.Widget{state, installRow}
	if s.piperInstalling {
		ws = append(ws, progress)
	}
	if s.piperInstallErr != "" && !s.piperInstalling {
		ws = append(ws, installErr)
	}
	ws = append(ws, advanced)
	return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, ws...) })
}

func (p *Page) installPiper(c *core.Ctx) {
	s := &p.tts
	if s.piperInstalling {
		return
	}
	if c.Offline {
		s.piperInstallErr = "Встановлення недоступне в демо-режимі."
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.piperInstalling, s.piperStop, s.piperPct, s.piperMsg, s.piperInstallErr = true, cancel, 0, "Шукаємо та завантажуємо Piper…", ""
	c.Background(func() {
		bin, err := c.E.InstallPiper(ctx, func(pct int, msg string) {
			c.Post(func() {
				s.piperPct = float32(max(0, min(100, pct))) / 100
				if msg != "" {
					s.piperMsg = msg
				}
			})
		})
		cancel()
		c.Post(func() {
			s.piperInstalling, s.piperStop = false, nil
			if err != nil {
				s.piperInstallErr = core.ErrorText(err)
				c.Toast(gui.ToneError, "Помилка встановлення Piper")
				return
			}
			p.cfg.set(c, func(cf *config.Config) {
				cf.TTS.BaseURL = bin
			}, func() {
				c.Toast(gui.ToneSuccess, "Piper TTS успішно встановлено!")
				c.ReloadVoices()
			})
		})
	})
}
