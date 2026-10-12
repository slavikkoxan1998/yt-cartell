package settings

// «Розпізнавання мови» for «Своя озвучка»: ElevenLabs Scribe, an OpenAI
// Whisper-compatible service, or whisper.cpp on this computer («Локально»:
// no key, no internet after a one-time download the user starts here —
// Engine.InstallLocalSTT with a progress bar).

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/dictate"
	"ytcartell/internal/gui"
	"ytcartell/internal/stt"
	"ytcartell/internal/studio"
)

type sttCard struct {
	provider gui.Segmented
	key      keyInput
	aaiKey   keyInput      // AssemblyAI (its own key)
	region   gui.Segmented // AssemblyAI: US or EU
	keyLink  gui.Clickable // «Де взяти ключ AssemblyAI»
	base     field
	model    field
	test     gui.Clickable
	check    check
	saved    savedNote

	// Local whisper.cpp.
	localModel   gui.Select
	installModel string
	install      gui.Clickable
	cancel       gui.Clickable
	installing   bool
	stop         context.CancelFunc
	pct          float32
	msg          string
	installErr   string
	bar          gui.Anim
	advanced     bool
	advToggle    gui.Clickable
	bin, mdl     field
	pickBin      gui.Clickable
	pickMdl      gui.Clickable
	status       localStatus
	spec         specPanel // «Своє API» (audiospec.go)
}

// localStatus caches Engine.LocalSTTStatus (it looks for files and the PATH).
type localStatus struct {
	sig        string
	at         time.Time
	bin, model string
	ok         bool
	hint       string
}

func (s *sttCard) localState(c *core.Ctx, cfg config.Config, now time.Time) localStatus {
	sig := cfg.STT.LocalBin + "|" + cfg.STT.LocalModel
	if s.status.sig != sig || now.Sub(s.status.at) > 3*time.Second || s.status.at.IsZero() {
		bin, model, ok, hint := c.E.LocalSTTStatus()
		s.status = localStatus{sig: sig, at: now, bin: bin, model: model, ok: ok, hint: hint}
	}
	return s.status
}

func (p *Page) sttCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.stt
	cfg := p.now
	flash := func() { s.saved.want = true }
	if v, ok := s.provider.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.STT.Provider = v }, flash)
		s.check.reset()
	}
	if v, ok := s.region.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.STT.AssemblyEU = v == "eu" }, flash)
		s.check.reset()
	}
	if s.keyLink.Clicked(gtx) {
		c.OpenPath(stt.AssemblyKeyURL)
	}
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, c.E.TestSTT)
	}
	cfg = p.now
	prov := cfg.STT.Provider
	local := prov == stt.ProviderLocal
	opts := []gui.SegOption{
		{Value: "elevenlabs", Label: "ElevenLabs Scribe"},
		{Value: "openai", Label: "OpenAI Whisper"},
		{Value: stt.ProviderAssembly, Label: "AssemblyAI"},
		{Value: stt.ProviderCustom, Label: "Своє API"},
		{Value: stt.ProviderLocal, Label: "Локально"},
	}
	providerW := labeled(th, "Сервіс", "", func(gtx C) D {
		if gtx.Constraints.Max.X < gtx.Dp(520) {
			opts[0].Label, opts[1].Label = "ElevenLabs", "OpenAI"
		}
		s.provider.Full = gtx.Constraints.Max.X < gtx.Dp(720)
		return s.provider.Layout(gtx, th, prov, opts)
	})
	body := []layout.Widget{providerW}
	advice := ""
	switch {
	case local:
		body = append(body, func(gtx C) D { return p.sttLocal(gtx, c, cfg, flash) })
		advice = "Встанови whisper.cpp кнопкою вище або вкажи шляхи до програми й моделі в «Додатково»."
	case prov == stt.ProviderCustom:
		s.key.empty = "Ключ твого сервісу (необов'язково). Як його передати, задає поле \"auth\" в описі API."
		key := func(gtx C) D {
			return s.key.layout(gtx, th, "API-ключ", cfg.STT.APIKey, "Встав ключ", func(v string) {
				p.cfg.set(c, func(cf *config.Config) { cf.STT.APIKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ розпізнавання збережено") })
			})
		}
		base := textIn(&s.base, th, "Адреса API", "Корінь API, від якого рахуються шляхи опису.", "https://api.example.com/v1", cfg.STT.BaseURL,
			func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.STT.BaseURL = v }, flash) })
		model := textIn(&s.model, th, "Модель", "Підставляється як {{model}}.", "whisper-1", cfg.STT.Model,
			func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.STT.Model = strings.TrimSpace(v) }, flash) })
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, base, key, model) }, p.sttSpecPanel(c, flash))
		advice = `Перевір адресу, ключ і опис API: «test» має бути запитом, що нічого не розпізнає (акаунт, список моделей).`
	case prov == stt.ProviderAssembly:
		s.aaiKey.empty = "Ключ — у кабінеті AssemblyAI; нові акаунти мають $50 безкоштовних кредитів."
		key := func(gtx C) D {
			return s.aaiKey.layout(gtx, th, "API-ключ AssemblyAI", cfg.STT.AssemblyKey, "Встав ключ AssemblyAI", func(v string) {
				p.cfg.set(c, func(cf *config.Config) { cf.STT.AssemblyKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ AssemblyAI збережено") })
			})
		}
		region := labeled(th, "Де обробляти", "ЄС — дані не залишають Європу (api.eu.assemblyai.com).", func(gtx C) D {
			v := ""
			if cfg.STT.AssemblyEU {
				v = "eu"
			}
			s.region.Full = true
			return s.region.Layout(gtx, th, v, []gui.SegOption{{Value: "", Label: "США"}, {Value: "eu", Label: "ЄС"}})
		})
		body = append(body,
			func(gtx C) D { return fieldRow(gtx, 240, key, region) },
			func(gtx C) D {
				return gui.Link{Text: "Де взяти ключ AssemblyAI", Trail: gui.IconArrowUpRight}.Layout(gtx, th, &s.keyLink)
			},
		)
		advice = "Перевір ключ. Українську AssemblyAI розпізнає моделлю Universal-2 (студія вибирає її сама)."
	default:
		openai := prov == "openai"
		keyPH, modelPH := "Встав ключ, якщо окремий", "scribe_v2"
		s.key.empty = "Можна лишити порожнім — візьметься збережений ключ ElevenLabs з «Озвучки»."
		if openai {
			modelPH = "whisper-1"
			s.key.empty = "Можна лишити порожнім — візьметься ключ з картки «Текст»."
		}
		key := func(gtx C) D {
			return s.key.layout(gtx, th, "API-ключ", cfg.STT.APIKey, keyPH, func(v string) {
				p.cfg.set(c, func(cf *config.Config) { cf.STT.APIKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ розпізнавання збережено") })
			})
		}
		var base layout.Widget
		if openai {
			base = textIn(&s.base, th, "Адреса", "Порожньо — адреса з картки «Текст».", cfg.Text.BaseURL, cfg.STT.BaseURL,
				func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.STT.BaseURL = v }, flash) })
		}
		model := textIn(&s.model, th, "Модель", "Порожньо — модель сервісу за замовчуванням.", modelPH, cfg.STT.Model,
			func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.STT.Model = strings.TrimSpace(v) }, flash) })
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, key, base, model) })
		advice = "Перевір ключ. Для ElevenLabs поле можна лишити порожнім — тоді використається ключ ElevenLabs, збережений в «Озвучці»."
		if openai {
			advice = "Перевір адресу й ключ. Сервіс має приймати POST /audio/transcriptions і повертати таймкоди слів (whisper-1)."
		}
	}
	body = append(body,
		actions(checkButton(th, &s.test, &s.check, "", s.installing)),
		func(gtx C) D { return s.check.result(gtx, th, advice) },
	)
	text := "Для «Своєї озвучки»: студія розпізнає твій запис і ріже сцени точно під мовлення."
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconAudioLines, Title: "Розпізнавання мови", Text: text,
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	}, body...)
}

// sttLocal is the «Локально» part: the state, the model to download, the
// install button with its progress, and the paths under «Додатково».
func (p *Page) sttLocal(gtx C, c *core.Ctx, cfg config.Config, flash func()) D {
	th := c.Th
	s := &p.stt
	st := s.localState(c, cfg, gtx.Now)
	models := c.E.LocalSTTModels()
	if s.installModel == "" {
		s.installModel = stt.DefaultLocalModel
	}
	if v, ok := s.localModel.Changed(gtx); ok {
		s.installModel = v
	}
	if s.install.Clicked(gtx) {
		p.installLocal(c)
	}
	if s.cancel.Clicked(gtx) && s.stop != nil {
		s.stop()
	}
	if s.advToggle.Clicked(gtx) {
		s.advanced = !s.advanced
	}
	if s.pickBin.Clicked(gtx) {
		var exts []string
		if runtime.GOOS == "windows" {
			exts = []string{"exe"}
		}
		c.PickFile("Програма whisper.cpp (whisper-cli)", exts, func(path string) {
			s.bin.SetText(path)
			p.cfg.set(c, func(cf *config.Config) { cf.STT.LocalBin = path }, flash)
		})
	}
	if s.pickMdl.Clicked(gtx) {
		c.PickFile("Модель whisper (ggml-….bin)", []string{"bin"}, func(path string) {
			s.mdl.SetText(path)
			p.cfg.set(c, func(cf *config.Config) { cf.STT.LocalModel = path }, flash)
		})
	}
	state := func(gtx C) D {
		pill := gui.StatusPill{State: gui.StateEmpty, Text: "Ще не встановлено", Small: true}
		if st.ok {
			pill = gui.StatusPill{State: gui.StateDone, Text: "Готово до роботи", Small: true}
		}
		if s.installing {
			pill = gui.StatusPill{State: gui.StateRunning, Percent: int(s.pct * 100), Small: true, Text: "Встановлюємо " + strconv.Itoa(int(s.pct*100)) + "%"}
		}
		hint := st.hint
		if hint == "" {
			hint = "Без інтернету й без ключа: розпізнає whisper.cpp на цьому комп'ютері."
		}
		return insetHead(gtx, th, gui.IconCpu, "whisper.cpp на цьому комп'ютері", hint, pill.Widget(th))
	}
	modelSel := func(gtx C) D {
		if len(models) == 0 {
			return gui.Text{Style: th.Small, Color: th.Pal.Text2}.Layout(gtx, th, "Модель: "+stt.DefaultLocalModel+" (типова).")
		}
		opts := make([]gui.SelectOption, len(models))
		note := ""
		for i, mi := range models {
			label := mi.Label
			if label == "" {
				label = mi.ID
			}
			opts[i] = gui.SelectOption{Value: mi.ID, Label: label}
			if mi.SizeMB > 0 {
				opts[i].Hint = thousands(mi.SizeMB) + " МБ"
			}
			if mi.ID == s.installModel {
				note = mi.Note
			}
		}
		return s.localModel.Layout(gtx, th, gui.SelectStyle{Label: "Модель", Options: opts, Value: s.installModel, Hint: note, Disabled: s.installing})
	}
	installRow := func(gtx C) D {
		text := "Встановити"
		if st.ok {
			text = "Встановити іншу модель"
		}
		return gui.Flow{Gap: gui.Sp12}.Layout(gtx,
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconDownload, Text: text, Busy: s.installing}.Layout(gtx, th, &s.install)
			},
			func(gtx C) D {
				if !s.installing {
					return D{}
				}
				return gui.Button{Kind: gui.Ghost, Icon: gui.IconX, Text: "Скасувати"}.Layout(gtx, th, &s.cancel)
			},
		)
	}
	progress := func(gtx C) D {
		if !s.installing {
			return D{}
		}
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D {
				return gui.ProgressBar{Value: s.pct, Tone: gui.ToneRunning, Anim: &s.bar}.Layout(gtx, th)
			},
			gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, s.msg),
		)
	}
	installErr := func(gtx C) D {
		if s.installErr == "" || s.installing {
			return D{}
		}
		if runtime.GOOS == "darwin" {
			// macOS: the program comes from Homebrew — the message says how.
			return note(gtx, th, gui.IconInfo, s.installErr, nil)
		}
		return resultPanel(gtx, th, false, false, s.installErr, "Перевір інтернет і місце на диску, тоді спробуй ще раз.", "")
	}
	advanced := func(gtx C) D {
		chev := gui.IconChevronDown
		if s.advanced {
			chev = gui.IconChevronUp
		}
		head := func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Додатково: шляхи до програми й моделі", Trail: chev}.Layout(gtx, th, &s.advToggle)
		}
		if !s.advanced {
			return head(gtx)
		}
		path := func(f *field, pick *gui.Clickable, label, hint, ph, value string, set func(string)) layout.Widget {
			return labeled(th, label, hint, func(gtx C) D {
				f.bind(gtx, value, func(v string) { set(strings.Trim(strings.TrimSpace(v), `"`)) })
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D { return f.Layout(gtx, th, gui.FieldStyle{Placeholder: ph}) }),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Icon: gui.IconFolderOpen, Tip: "Вибрати файл"}.Layout(gtx, th, pick)
					}),
				)
			})
		}
		return gui.VStack(gtx, gui.Sp12, head,
			func(gtx C) D {
				return fieldRow(gtx, 260,
					path(&s.bin, &s.pickBin, "Програма whisper.cpp", "Порожньо — шукати поруч із програмою, у даних студії, потім у PATH.", "Автоматично",
						cfg.STT.LocalBin, func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.STT.LocalBin = v }, flash) }),
					path(&s.mdl, &s.pickMdl, "Модель", "Файл ggml-….bin. Порожньо — завантажена модель.", "Автоматично",
						cfg.STT.LocalModel, func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.STT.LocalModel = v }, flash) }),
				)
			},
		)
	}
	ws := []layout.Widget{state, modelSel, installRow}
	if s.installing {
		ws = append(ws, progress)
	}
	if s.installErr != "" && !s.installing {
		ws = append(ws, installErr)
	}
	ws = append(ws, advanced)
	return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, ws...) })
}

// installLocal downloads whisper.cpp and the chosen model in the background
// with a progress bar (never in demo mode: it reaches the internet).
func (p *Page) installLocal(c *core.Ctx) {
	s := &p.stt
	if s.installing {
		return
	}
	if c.Offline {
		s.installErr = "Встановлення недоступне в демо-режимі."
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.installing, s.stop, s.pct, s.msg, s.installErr = true, cancel, 0, "Готуємо завантаження…", ""
	model := s.installModel
	c.Background(func() {
		err := c.E.InstallLocalSTT(ctx, model, func(pct int, msg string) {
			c.Post(func() {
				s.pct = float32(max(0, min(100, pct))) / 100
				if msg != "" {
					s.msg = msg
				}
			})
		})
		cancel()
		c.Post(func() {
			s.installing, s.stop = false, nil
			s.status.at = time.Time{} // look again
			switch {
			case err == nil:
				c.Toast(gui.ToneSuccess, "Розпізнавання на цьому комп'ютері готове")
			case ctx.Err() != nil:
				s.installErr = ""
				c.Toast(gui.ToneInfo, "Встановлення скасовано — наступного разу продовжимо з того ж місця")
			default:
				s.installErr = core.ErrorText(err)
			}
		})
	})
}

// ---- «Диктування» ---------------------------------------------------------------------------
//
// Voice typing in the Home story box (package dictate): the service
// («Автоматично» picks the first that is ready), its own key / address /
// model where it has them (empty = the keys the app already has), the
// language, the microphone (FFmpeg's list, refreshed on request), the pause
// that ends it, and «Перевірити» (opens a session or reads the account, never
// records).

type dictCard struct {
	provider gui.Select
	lang     gui.Select
	device   gui.Select
	silence  gui.Select
	key      keyInput
	base     field
	model    field
	refresh  gui.Clickable
	keyLink  gui.Clickable
	test     gui.Clickable
	check    check
	saved    savedNote

	devs    []dictate.Device
	devErr  string
	devBusy bool
	devDone bool

	plan   dictate.Plan
	planAt time.Time
	sig    string
}

// dictAuto is the Select value of «Автоматично» ("" in the settings).
const dictAuto = "auto"

// dictDefaultMic is the Select value of the default microphone.
const dictDefaultMic = "\x00default"

// fields are the card's bound text fields (flushed before checks).
func (s *dictCard) fields() []*field { return []*field{&s.key.field, &s.base, &s.model} }

// resolved is dictate.Resolve for this frame's settings (it looks for files:
// cached, refreshed when the settings change or every 2 s).
func (s *dictCard) resolved(cfg config.Config, now time.Time) dictate.Plan {
	sig := fmt.Sprintf("%+v|%s|%s|%d|%d|%s|%s|%t", cfg.Dictation, cfg.STT.Provider, cfg.STT.LocalBin+cfg.STT.LocalModel,
		len(cfg.Google.Keys), len(cfg.TTS.Of("elevenlabs").AllKeys()), cfg.Text.BaseURL, config.Mask(cfg.STT.AssemblyKey), cfg.STT.AssemblyEU)
	if sig != s.sig || now.Sub(s.planAt) > 2*time.Second {
		s.plan, s.planAt, s.sig = dictate.Resolve(cfg), now, sig
	}
	return s.plan
}

// listMics asks FFmpeg for the microphones in the background (never in demo
// renders: they set the list themselves).
func (p *Page) listMics(c *core.Ctx, force bool) {
	s := &p.dict
	if c.Offline || s.devBusy || (s.devDone && !force) {
		return
	}
	s.devBusy = true
	e := c.E
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		devs, err := dictate.ListDevices(ctx, e.FF().FFmpeg)
		c.Post(func() {
			s.devBusy, s.devDone, s.devs, s.devErr = false, true, devs, ""
			switch {
			case err != nil:
				s.devErr = core.ErrorText(err)
			case len(devs) == 0:
				s.devErr = "Мікрофонів не знайдено — підключи мікрофон і натисни «Оновити»."
			}
		})
	})
}

func (p *Page) dictCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.dict
	cfg := p.now
	flash := func() { s.saved.want = true }
	set := func(fn func(d *config.Dictation)) {
		p.cfg.set(c, func(cf *config.Config) { fn(&cf.Dictation) }, flash)
		s.check.reset()
	}
	if v, ok := s.provider.Changed(gtx); ok {
		if v == dictAuto {
			v = ""
		}
		set(func(d *config.Dictation) { d.Provider = v })
		s.key.SetText("")
	}
	if v, ok := s.lang.Changed(gtx); ok {
		if v == dictAuto {
			v = ""
		}
		set(func(d *config.Dictation) { d.Language = v })
	}
	if v, ok := s.device.Changed(gtx); ok {
		if v == dictDefaultMic {
			v = ""
		}
		set(func(d *config.Dictation) { d.Device = v })
	}
	if v, ok := s.silence.Changed(gtx); ok {
		n, _ := strconv.Atoi(v)
		set(func(d *config.Dictation) { d.SilenceSec = n })
	}
	if s.refresh.Clicked(gtx) {
		p.listMics(c, true)
	}
	if s.keyLink.Clicked(gtx) {
		c.OpenPath(dictKeyURL(cfg.Dictation.Provider))
	}
	if s.test.Clicked(gtx) {
		e := c.E
		p.runCheck(c, &s.check, func(ctx context.Context) (bool, string) {
			return dictate.Check(ctx, dictate.Resolve(e.Config().Get()))
		})
	}
	p.listMics(c, false)
	cfg = p.now
	d := cfg.Dictation
	plan := s.resolved(cfg, gtx.Now)

	// The service.
	provVal := d.Provider
	if provVal == "" {
		provVal = dictAuto
	}
	autoHint := "нічого не готово"
	if d.Provider == "" && plan.Ready {
		if info, ok := dictate.ProviderByID(plan.Provider); ok {
			autoHint = "зараз: " + info.Short
		}
	}
	popts := []gui.SelectOption{{Value: dictAuto, Label: "Автоматично", Hint: autoHint, Icon: gui.IconSparkles}}
	for _, info := range dictate.Providers() {
		hint := "фразами"
		switch {
		case info.ID == dictate.Local:
			hint = "без інтернету"
		case info.Streaming:
			hint = "у реальному часі"
		}
		popts = append(popts, gui.SelectOption{Value: info.ID, Label: info.Label, Hint: hint})
	}
	provHint := "Перший готовий: сервіс із «Розпізнавання мови», потім ElevenLabs, Deepgram, OpenAI, whisper.cpp, Google."
	if info, ok := dictate.ProviderByID(d.Provider); ok {
		provHint = info.Hint
	}
	providerW := func(gtx C) D {
		return s.provider.Layout(gtx, th, gui.SelectStyle{Label: "Сервіс", Options: popts, Value: provVal, Hint: provHint})
	}

	// The language.
	lopts := []gui.SelectOption{{Value: dictAuto, Label: "Визначати самому", Icon: gui.IconSparkles}}
	for _, l := range studio.Languages {
		lopts = append(lopts, gui.SelectOption{Value: l.Code, Label: l.Label, Hint: l.Native})
	}
	langVal := d.Language
	if langVal == "" {
		langVal = dictAuto
	}
	langHint := "Якщо говориш однією мовою — вибери її: розпізнає точніше й швидше."
	switch {
	case d.Language == "" && (plan.Provider == dictate.Local || plan.Provider == dictate.OpenAIChunks):
		langHint = "Порада: вибери мову — на коротких фразах Whisper інколи плутає українську з російською."
	case d.Language == "" && plan.Provider == dictate.Deepgram:
		langHint = "Deepgram сам визначає лише 10 мов (без української) — для української вибери її тут."
	}
	langW := func(gtx C) D {
		return s.lang.Layout(gtx, th, gui.SelectStyle{Label: "Мова", Options: lopts, Value: langVal, Hint: langHint, Search: true,
			SearchPlaceholder: "Пошук мови", Leading: gui.IconLanguages})
	}

	// Where the service stands.
	status := func(gtx C) D {
		title, text := "Автоматично", ""
		if plan.Provider != "" {
			title = plan.Label
			if info, ok := dictate.ProviderByID(plan.Provider); ok && plan.Auto {
				title = "Автоматично: " + info.Short
			}
		}
		pill := gui.StatusPill{State: gui.StateDone, Text: "Готово", Small: true}
		switch {
		case !plan.Ready:
			pill = gui.StatusPill{State: gui.StateEmpty, Text: "Не налаштовано", Small: true}
			text = plan.Problem
		case plan.Provider == dictate.Local:
			text = "Без інтернету: whisper.cpp на цьому комп'ютері"
		case plan.KeyFrom != "":
			text = "Ключ " + plan.KeyFrom
		default:
			text = "Свій ключ"
		}
		if plan.Ready && plan.Model != "" && plan.Provider != dictate.Local {
			text += " · модель " + plan.Model
		}
		return inset(gtx, th, func(gtx C) D { return insetHead(gtx, th, gui.IconMic, title, text, pill.Widget(th)) })
	}

	// The service's own settings.
	var own []layout.Widget
	keyW := func(label, stored, empty, ph string, save func(d *config.Dictation, v string)) layout.Widget {
		return func(gtx C) D {
			s.key.empty = empty
			return s.key.layout(gtx, th, label, stored, ph, func(v string) {
				p.cfg.set(c, func(cf *config.Config) { save(&cf.Dictation, v) }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ диктування збережено") })
			})
		}
	}
	text := func(label, hint, ph, value string, save func(d *config.Dictation, v string)) layout.Widget {
		return textIn(&s.model, th, label, hint, ph, value, func(v string) {
			set(func(dd *config.Dictation) { save(dd, strings.TrimSpace(v)) })
		})
	}
	switch d.Provider {
	case dictate.ElevenLabs:
		own = append(own, keyW("API-ключ ElevenLabs", d.ElevenKey, "Порожньо — ключ ElevenLabs з «Озвучки» або «Розпізнавання мови».", "Встав ключ, якщо окремий",
			func(dd *config.Dictation, v string) { dd.ElevenKey = v }))
	case dictate.OpenAI:
		own = append(own,
			keyW("API-ключ OpenAI", d.OpenAIKey, "Порожньо — ключ OpenAI, який студія вже має (лише api.openai.com).", "sk-…",
				func(dd *config.Dictation, v string) { dd.OpenAIKey = v }),
			text("Модель", "Порожньо — "+dictate.OpenAIRealtimeDef+". Також: gpt-4o-transcribe, gpt-transcribe.", dictate.OpenAIRealtimeDef, d.OpenAIModel,
				func(dd *config.Dictation, v string) { dd.OpenAIModel = v }))
	case dictate.OpenAIChunks:
		own = append(own,
			textIn(&s.base, th, "Адреса", "Порожньо — адреса з «Розпізнавання мови» або «Тексту». Groq: https://api.groq.com/openai/v1", plan.BaseURL, d.ChunksURL,
				func(v string) { set(func(dd *config.Dictation) { dd.ChunksURL = strings.TrimSpace(v) }) }),
			keyW("API-ключ", d.ChunksKey, "Порожньо — ключ від цієї ж адреси з інших карток.", "Встав ключ, якщо окремий",
				func(dd *config.Dictation, v string) { dd.ChunksKey = v }),
			text("Модель", "Порожньо — звична модель сервісу.", firstNonEmpty(plan.Model, dictate.WhisperCompatDef), d.ChunksModel,
				func(dd *config.Dictation, v string) { dd.ChunksModel = v }))
	case dictate.Deepgram:
		own = append(own, keyW("API-ключ Deepgram", d.DeepgramKey, "Ключ — у консолі Deepgram.", "Встав ключ Deepgram",
			func(dd *config.Dictation, v string) { dd.DeepgramKey = v }))
	case dictate.AssemblyAI:
		own = append(own, keyW("API-ключ AssemblyAI", d.AssemblyKey, "Порожньо — ключ AssemblyAI з «Розпізнавання мови».", "Встав ключ AssemblyAI",
			func(dd *config.Dictation, v string) { dd.AssemblyKey = v }))
	case dictate.Gemini:
		own = append(own,
			keyW("Ключ Google AI Studio", d.GoogleKey, "Порожньо — ключі з картки «Google AI Studio».", "AQ.… або AIza…",
				func(dd *config.Dictation, v string) { dd.GoogleKey = v }),
			text("Модель", "Порожньо — "+dictate.GeminiDef+".", dictate.GeminiDef, d.GeminiModel,
				func(dd *config.Dictation, v string) { dd.GeminiModel = v }))
	}

	// The microphone and the pause.
	mopts := []gui.SelectOption{{Value: dictDefaultMic, Label: "Типовий мікрофон", Icon: gui.IconMic}}
	found := d.Device == ""
	for _, dv := range s.devs {
		mopts = append(mopts, gui.SelectOption{Value: dv.Name, Label: dv.Name})
		found = found || dv.Name == d.Device
	}
	if !found {
		mopts = append(mopts, gui.SelectOption{Value: d.Device, Label: d.Device, Hint: "не підключений"})
	}
	micVal := d.Device
	if micVal == "" {
		micVal = dictDefaultMic
	}
	micHint := "Типовий — перший у списку системи."
	switch {
	case s.devBusy:
		micHint = "Шукаємо мікрофони…"
	case s.devErr != "":
		micHint = s.devErr
	case len(s.devs) > 0:
		micHint = "Знайдено: " + core.Count(len(s.devs), "мікрофон", "мікрофони", "мікрофонів") + "."
	}
	micW := func(gtx C) D {
		sel := func(gtx C) D {
			return s.device.Layout(gtx, th, gui.SelectStyle{Label: "Мікрофон", Options: mopts, Value: micVal, Hint: micHint, Leading: gui.IconMic})
		}
		btn := func(gtx C) D {
			return gui.Button{Kind: gui.Tinted, Icon: gui.IconRefreshCw, Busy: s.devBusy, Tip: "Оновити список мікрофонів"}.Layout(gtx, th, &s.refresh)
		}
		// The button sits level with the select box (under its label).
		labelH := gtx.Sp(th.Label.Size)*13/10 + gtx.Dp(gui.Sp8)
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Flexed(1, sel),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: unit.Dp(float32(labelH) / gtx.Metric.PxPerDp)}.Layout(gtx, btn)
			}),
		)
	}
	sopts := []gui.SelectOption{{Value: "3", Label: "3 с"}, {Value: "5", Label: "5 с"}, {Value: "8", Label: "8 с", Hint: "типово"},
		{Value: "15", Label: "15 с"}, {Value: "30", Label: "30 с"}, {Value: "60", Label: "1 хв"}, {Value: "-1", Label: "Не зупиняти"}}
	silVal := strconv.Itoa(d.SilenceSec)
	if d.SilenceSec == 0 {
		silVal = strconv.Itoa(config.DefaultSilenceSec)
	}
	silW := func(gtx C) D {
		return s.silence.Layout(gtx, th, gui.SelectStyle{Label: "Зупиняти після тиші", Options: sopts, Value: silVal, Leading: gui.IconTimer,
			Hint: "Ще зупиняє «Стоп», Esc або повторне натискання мікрофона."})
	}

	body := []layout.Widget{
		func(gtx C) D { return fieldRow(gtx, 260, providerW, langW) },
		status,
	}
	if len(own) > 0 {
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, own...) })
	}
	if dictKeyURL(d.Provider) != "" {
		body = append(body, func(gtx C) D {
			return gui.Link{Text: "Де взяти ключ", Trail: gui.IconArrowUpRight}.Layout(gtx, th, &s.keyLink)
		})
	}
	if d.Provider == dictate.Local {
		body = append(body, func(gtx C) D {
			return note(gtx, th, gui.IconInfo, "Програму й модель встановлює картка «Розпізнавання мови» (вибери «Локально» → «Встановити»). Якщо поруч є whisper-server, текст з'являється під час мовлення.", nil)
		})
	}
	body = append(body,
		func(gtx C) D { return fieldRow(gtx, 260, micW, silW) },
		actions(checkButton(th, &s.test, &s.check, "", false)),
		func(gtx C) D {
			return s.check.result(gtx, th, "Перевір ключ і мережу. Мікрофон перевіряється натисканням на нього в «Новій історії».")
		},
	)
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconMic, Title: "Диктування",
		Text:  "Мікрофон у «Новій історії»: говориш — і текст з'являється в полі. Сервіс тут окремий від розпізнавання записів.",
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	}, body...)
}

// dictKeyURL is where a service's key comes from ("" = none to get).
func dictKeyURL(provider string) string {
	switch provider {
	case dictate.ElevenLabs:
		return "https://elevenlabs.io/app/settings/api-keys"
	case dictate.OpenAI:
		return "https://platform.openai.com/api-keys"
	case dictate.Deepgram:
		return "https://console.deepgram.com"
	case dictate.AssemblyAI:
		return stt.AssemblyKeyURL
	case dictate.Gemini:
		return "https://aistudio.google.com/apikey"
	}
	return ""
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
