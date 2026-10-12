package settings

// «Підключення»: text (here), frames, video and G-Labs Studio (media.go),
// voice-over (tts.go), Google AI Studio (google.go), FFmpeg, speech
// recognition and the render concurrency.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// keyInput is a password field for an API key. It never shows the stored
// key: the hint shows its mask, and a pasted key replaces it on Enter or when
// the field loses focus (an empty field changes nothing).
type keyInput struct {
	field
	// empty is the hint while no key is stored ("" = the usual one).
	empty string
}

func (k *keyInput) layout(gtx C, th *gui.Theme, label, stored, fresh string, save func(string)) D {
	k.bind(gtx, "", func(v string) {
		k.SetText("")
		if v = strings.TrimSpace(v); v != "" {
			save(v)
		}
	})
	hint, ph := "Ще не задано — встав ключ і натисни Enter", fresh
	if k.empty != "" {
		hint = k.empty
	}
	if strings.TrimSpace(stored) != "" {
		hint, ph = "Збережено: "+config.Mask(stored), "Встав новий, щоб замінити"
	}
	return k.Layout(gtx, th, gui.FieldStyle{Label: label, Placeholder: ph, Hint: hint, Password: true})
}

// textIn is a plain bound text field with a label.
func textIn(f *field, th *gui.Theme, label, hint, placeholder, value string, save func(string)) layout.Widget {
	return func(gtx C) D {
		f.bind(gtx, value, save)
		return f.Layout(gtx, th, gui.FieldStyle{Label: label, Hint: hint, Placeholder: placeholder})
	}
}

// actions is the button row of a card (wraps when narrow).
func actions(ws ...layout.Widget) layout.Widget {
	return func(gtx C) D {
		var list []layout.Widget
		for _, w := range ws {
			if w != nil {
				list = append(list, w)
			}
		}
		return gui.Flow{Gap: gui.Sp12}.Layout(gtx, list...)
	}
}

// checkButton is the «Перевірити» button of a card.
func checkButton(th *gui.Theme, c *gui.Clickable, k *check, text string, disabled bool) layout.Widget {
	if text == "" {
		text = "Перевірити"
	}
	return func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Icon: gui.IconPlug, Text: text, Busy: k.busy, Disabled: disabled}.Layout(gtx, th, c)
	}
}

// ---- Текст ------------------------------------------------------------------------------

type textCard struct {
	provider   gui.Segmented
	base       field
	key        keyInput
	model      field
	apiVer     field // Azure deployment URLs
	load       gui.Clickable
	models     []string
	modelsFor  string // the address the list was loaded for
	modelsBusy bool
	modelSel   gui.Select
	proto      gui.Select // (unused: the protocol is the segmented above; kept for the click guard)
	auth       gui.Select
	test       gui.Clickable
	check      check
	saved      savedNote
	toGoogle   gui.Clickable
	back       gui.Clickable // «Повернути OpenAI-сумісний»
	gmodel     field         // the Gemini model (Google.StoryModel)
	shown      string        // the provider the selector showed last frame (tests)
	catalog    catalogUI     // «Вибрати з каталогу»
}

// textOpenAISet: the address-and-key side holds the user's own endpoint or
// key (so «Повернути» brings a working setup back).
func textOpenAISet(t config.Text) bool {
	return strings.TrimSpace(t.APIKey) != "" || strings.TrimRight(t.BaseURL, "/") != config.Default().Text.BaseURL
}

// textDefaults: an address the user never chose (a protocol's default) —
// switching the protocol replaces it with the new protocol's own.
func textDefaultBase(base string) bool {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		return true
	}
	for _, p := range config.TextProviders {
		if d := config.TextDefaultBaseURL(p); d != "" && strings.EqualFold(b, d) {
			return true
		}
	}
	return false
}

// textProto is how the card describes a protocol: the address field's
// placeholder and hint, the key's placeholder, the model's label.
type textProto struct {
	base, baseHint, keyPH, modelLabel, modelPH, keyWarn string
}

func textProtoOf(p string) textProto {
	switch p {
	case "anthropic":
		return textProto{"https://api.anthropic.com/v1", "Anthropic Messages API (ключ у заголовку x-api-key).", "sk-ant-…",
			"Модель", "claude-sonnet-4-5", "Ключа ще немає — встав ключ sk-ant-… з console.anthropic.com."}
	case "ollama":
		return textProto{"http://127.0.0.1:11434", "Ollama на цьому комп'ютері або https://ollama.com (тоді потрібен ключ).", "лише для Ollama Cloud",
			"Модель", "qwen3:8b", ""}
	case "azure":
		return textProto{"https://ресурс.openai.azure.com", "Ресурс Azure OpenAI (v1 API) або адреса розгортання …/openai/deployments/назва.", "ключ ресурсу",
			"Розгортання (deployment)", "gpt-5-mini", "Ключа ще немає — встав ключ ресурсу Azure (Keys and Endpoint)."}
	}
	return textProto{"http://127.0.0.1:8317/v1", "Будь-який OpenAI-сумісний сервіс. /v1 чи /api/v1 студія знайде сама.", "cg-… / sk-…",
		"Модель", "gemini-3.8-flash", "Ключа ще немає. Якщо твій сервіс (CarteLink, OpenRouter, OpenAI…) його вимагає — встав ключ, інакше план не складеться."}
}

func (p *Page) textCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.text
	cfg := p.now
	flash := func() { s.saved.want = true }
	if v, ok := s.provider.Changed(gtx); ok && v != cfg.Text.Provider {
		p.cfg.set(c, func(cf *config.Config) {
			if v != "google" && cf.Text.Provider != "google" && textDefaultBase(cf.Text.BaseURL) {
				cf.Text.BaseURL = config.TextDefaultBaseURL(v)
			}
			if v != "google" && cf.Text.Provider != "google" {
				cf.Text.Auth = "" // the new protocol's own header
			}
			cf.Text.Provider = v
		}, flash)
		s.check.reset()
		s.models = nil
	}
	if s.back.Clicked(gtx) {
		p.cfg.set(c, func(cf *config.Config) { cf.Text.Provider = "openai" }, flash)
		s.check.reset()
	}
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, c.E.TestText)
	}
	if s.load.Clicked(gtx) {
		p.loadModels(c)
	}
	if v, ok := s.modelSel.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Text.Model = v }, flash)
		s.model.SetText(v)
	}
	if v, ok := s.auth.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Text.Auth = v }, flash)
		s.check.reset()
	}
	if s.toGoogle.Clicked(gtx) {
		p.goTo(gtx, "google")
	}
	p.textCatalog(gtx, c, &s.catalog, cfg.Text, flash, func() { s.base.edited, s.model.edited = false, false; s.check.reset() })
	cfg = p.now // what a change above made (store.go keeps p.now current)
	remote := cfg.Text.Provider != "google"
	s.shown = cfg.Text.Provider
	if s.modelsFor != "" && s.modelsFor != cfg.Text.Provider+"|"+cfg.Text.BaseURL {
		s.models = nil // the list belongs to another address
	}
	body := []layout.Widget{
		labeled(th, "Хто пише текст", "", func(gtx C) D {
			wide := gtx.Constraints.Max.X >= gtx.Dp(600)
			opts := []gui.SegOption{
				{Value: "openai", Label: "OpenAI-сумісний"},
				{Value: "anthropic", Label: "Anthropic"},
				{Value: "ollama", Label: "Ollama"},
				{Value: "azure", Label: "Azure"},
				{Value: "google", Label: "Google Gemini"},
			}
			if !wide {
				opts[0].Label, opts[4].Label = "OpenAI", "Gemini"
			}
			s.provider.Full = !wide
			return s.provider.Layout(gtx, th, cfg.Text.Provider, opts)
		}),
		textCatalogRow(th, &s.catalog),
	}
	if remote {
		tp := textProtoOf(cfg.Text.Provider)
		base := textIn(&s.base, th, "Адреса", tp.baseHint, tp.base, cfg.Text.BaseURL,
			func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.Text.BaseURL = v }, flash) })
		key := func(gtx C) D {
			return s.key.layout(gtx, th, "API-ключ", cfg.Text.APIKey, tp.keyPH, func(v string) {
				p.cfg.set(c, func(cf *config.Config) { cf.Text.APIKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ тексту збережено") })
			})
		}
		model := labeled(th, tp.modelLabel, "", func(gtx C) D {
			s.model.bind(gtx, cfg.Text.Model, func(v string) {
				p.cfg.set(c, func(cf *config.Config) { cf.Text.Model = strings.TrimSpace(v) }, flash)
			})
			return gui.VStack(gtx, gui.Sp8,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx C) D {
							return s.model.Layout(gtx, th, gui.FieldStyle{Placeholder: tp.modelPH})
						}),
						layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
						layout.Rigid(func(gtx C) D {
							return gui.Button{Kind: gui.Tinted, Icon: gui.IconRefreshCw, Busy: s.modelsBusy, Tip: "Завантажити список моделей"}.Layout(gtx, th, &s.load)
						}),
					)
				},
				func(gtx C) D {
					if len(s.models) == 0 {
						return D{}
					}
					opts := make([]gui.SelectOption, len(s.models))
					for i, id := range s.models {
						opts[i] = gui.SelectOption{Value: id, Label: id}
					}
					return s.modelSel.Layout(gtx, th, gui.SelectStyle{Options: opts, Value: cfg.Text.Model, Search: true,
						Placeholder: fmt.Sprintf("Вибрати зі списку (%d)", len(opts)), SearchPlaceholder: "Пошук моделі"})
				},
			)
		})
		authOpts := []gui.SelectOption{
			{Value: "", Label: "Авто (" + authAutoLabel(cfg.Text.Provider) + ")"},
			{Value: "bearer", Label: "Authorization: Bearer"},
			{Value: "x-api-key", Label: "x-api-key"},
			{Value: "api-key", Label: "api-key (Azure)"},
			{Value: "none", Label: "Без ключа"},
		}
		auth := func(gtx C) D {
			return s.auth.Layout(gtx, th, gui.SelectStyle{Label: "Авторизація", Options: authOpts, Value: cfg.Text.Auth,
				Hint: "Як ключ іде на сервер. «Перевірити» сам знайде потрібний заголовок."})
		}
		var apiVer layout.Widget
		if cfg.Text.Provider == "azure" && strings.Contains(strings.ToLower(cfg.Text.BaseURL), "/deployments/") {
			apiVer = textIn(&s.apiVer, th, "api-version", "Для адреси розгортання. Порожньо — 2024-10-21.", "2024-10-21", cfg.Text.APIVersion,
				func(v string) {
					p.cfg.set(c, func(cf *config.Config) { cf.Text.APIVersion = strings.TrimSpace(v) }, flash)
				})
		}
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, base, key, model) })
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, auth, apiVer) })
		if strings.TrimSpace(cfg.Text.APIKey) == "" && tp.keyWarn != "" && cfg.Text.Auth != "none" {
			body = append(body, func(gtx C) D { return softWarning(gtx, th, tp.keyWarn) })
		}
	} else {
		model := textIn(&s.gmodel, th, "Модель Gemini", "Порожньо — "+config.Default().Google.StoryModel+".", "gemini-3.8-flash", cfg.Google.StoryModel,
			func(v string) {
				p.cfg.set(c, func(cf *config.Config) { cf.Google.StoryModel = strings.TrimSpace(v) }, flash)
			})
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, model) })
		if n := len(cfg.Google.Keys); n == 0 {
			text := "Текст вибрано писати через Google Gemini, але ключів Google немає — план і сценарій не складуться."
			if textOpenAISet(cfg.Text) {
				text += " Твій OpenAI-сумісний сервіс (" + cfg.Text.BaseURL + ") збережено — його можна повернути одним натиском."
			}
			body = append(body, func(gtx C) D {
				var back layout.Widget
				if textOpenAISet(cfg.Text) {
					back = func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Повернути OpenAI-сумісний"}.Layout(gtx, th, &s.back)
					}
				}
				return warnBox(gtx, th, "Ключів Google немає", text, back, func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconKeyRound, Text: "Додати ключі Google"}.Layout(gtx, th, &s.toGoogle)
				})
			})
		} else {
			body = append(body, func(gtx C) D {
				return hintLine(gtx, th, gui.IconKeyRound, "Ключі — у картці «Google AI Studio»: "+core.Count(n, "ключ", "ключі", "ключів")+", вони чергуються.")
			})
		}
	}
	advice := "Перевір адресу, ключ і модель. Якщо це CarteLink чи Ollama — переконайся, що програма запущена."
	body = append(body,
		actions(checkButton(th, &s.test, &s.check, "", false)),
		func(gtx C) D { return s.check.result(gtx, th, advice) })
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconScrollText, Title: "Текст і сценарій",
		Text:  "Пише план: персонажів, локації, озвучку й промти. Підійде будь-який сервіс: OpenAI-сумісний, Anthropic, Ollama, Azure чи Gemini.",
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	}, body...)
}

// authAutoLabel is the header «Авто» sends for a protocol.
func authAutoLabel(p string) string {
	switch p {
	case "anthropic":
		return "x-api-key"
	case "azure":
		return "api-key"
	}
	return "Bearer"
}

// loadModels asks the text endpoint for its models (after saving edits).
func (p *Page) loadModels(c *core.Ctx) {
	s := &p.text
	if s.modelsBusy || c.Offline {
		return
	}
	p.flushFields()
	s.modelsBusy = true
	p.cfg.barrier(c, func() {
		var models []string
		c.Background(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			list, err := c.E.TextModels(ctx)
			models = list
			c.Post(func() {
				s.modelsBusy = false
				if err != nil {
					c.Toast(gui.ToneError, "Не вдалося отримати список моделей: "+firstLineOf(core.ErrorText(err)))
					s.check.done, s.check.ok, s.check.msg = true, false, "Список моделей: "+core.ErrorText(err)
					return
				}
				s.models = models
				s.modelsFor = p.now.Text.Provider + "|" + p.now.Text.BaseURL
				c.Toast(gui.ToneSuccess, "Доступно моделей: "+strconv.Itoa(len(models)))
			})
		})
	})
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ---- Кадри, Відео, G-Labs Studio: media.go · Google AI Studio: google.go ---------------------------

// thousands groups digits: 12 345.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

// ---- FFmpeg: ffmpeg.go -----------------------------------------------------------------------------

// ---- Швидкість -----------------------------------------------------------------------------------

type speedCard struct {
	slider gui.Slider
	saved  savedNote
}

// speedCard is «Сцен одночасно»: the slider ends where the video service's
// documented limit does (studio.MaxConcurrency: G-Labs 10, RoyalTechno 20 …),
// says why, warns above the recommended value and shows the frame and voice
// pools that follow from the settings. A stored value above the service's
// maximum is kept (another service may allow it) and clamped per job.
func (p *Page) speedCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.speed
	cfg := p.now
	if v, ok := s.slider.Changed(gtx); ok {
		n := max(1, min(studio.MaxConcurrency(cfg).Max, int(v+0.5)))
		if n != cfg.Concurrency {
			p.cfg.set(c, func(cf *config.Config) { cf.Concurrency = n }, func() { s.saved.want = true })
		}
	}
	cfg = p.now
	lim := studio.MaxConcurrency(cfg)
	n := studio.ClipConcurrency(cfg)
	body := []layout.Widget{
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return s.slider.Layout(gtx, th, gui.SliderStyle{Value: float32(n), Min: 1, Max: float32(lim.Max), Step: 1,
						Format: func(v float32) string { return strconv.Itoa(int(v + 0.5)) }})
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp20}.Layout),
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Dp(92)
					return gui.Text{Style: th.H3, MaxLines: 1}.Layout(gtx, th, core.Count(n, "сцена", "сцени", "сцен"))
				}),
			)
		},
		func(gtx C) D {
			return gui.VStack(gtx, gui.Sp4,
				gui.Text{Style: th.Label}.Widget(th, fmt.Sprintf("До %d для %s · рекомендовано до %d", lim.Max, lim.Service, lim.Recommended)),
				gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, lim.Why),
			)
		},
	}
	if n > lim.Recommended {
		body = append(body, func(gtx C) D {
			return softWarning(gtx, th, fmt.Sprintf("Більше за %d — швидше, але частіше впирається в ліміти %s: завдання довше чекають у черзі або повертаються з помилкою.", lim.Recommended, lim.Service))
		})
	}
	if cfg.Concurrency > lim.Max {
		body = append(body, func(gtx C) D {
			return note(gtx, th, gui.IconInfo, fmt.Sprintf("Збережено %d — для %s студія знімає не більше %d одночасно.", cfg.Concurrency, lim.Service, lim.Max), nil)
		})
	}
	body = append(body, func(gtx C) D {
		return gui.Text{Style: th.Small, Color: th.Pal.Text3}.Layout(gtx, th, fmt.Sprintf(
			"Кадри малюються по %d, озвучка — по %d одночасно (скільки дозволяють сервіс і ключі). Ланцюжок кадрів завжди знімає сцени по одній — так зберігається плавність.",
			studio.ImageConcurrency(cfg), studio.VoiceConcurrency(cfg)))
	})
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconGauge, Title: "Сцен одночасно",
		Text:  "Скільки відеокліпів студія знімає паралельно в одному проєкті. Більше — швидше, але частіше впирається в ліміти сервісів.",
		Trail: s.saved.widget(th),
	}, body...)
}
