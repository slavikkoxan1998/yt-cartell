package settings

// «Кадри», «Відео» and «G-Labs Studio»: who draws the frames and who shoots the
// clips (G-Labs / Google / «Своє API»), each card with that provider's own
// settings and its own «Перевірити»; the G-Labs address and key, shared by
// both, live once in the «G-Labs Studio» card (shown while either uses it).
//
// The provider selectors show the page's settings of this frame (p.now, which
// a change updates at once — store.go): what they show is what is saved.

import (
	"strconv"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/studio"
)

var glabsModels = []gui.SelectOption{
	{Value: "veo_31_fast", Label: "Veo 3.1 Fast", Hint: "швидка"},
	{Value: "veo_31_lite", Label: "Veo 3.1 Lite", Hint: "найдешевша"},
	{Value: "veo_31_quality", Label: "Veo 3.1 Quality", Hint: "найкраща"},
	{Value: "veo_31_lite_relaxed", Label: "Veo 3.1 Lite Relaxed", Hint: "Ultra"},
	{Value: "omni_flash", Label: "Omni Flash", Hint: "4–10 с · 3×3"},
}

// mediaProviders are the options of the «Кадри» / «Відео» selectors.
var mediaProviders = []gui.SegOption{{Value: "glabs", Label: "G-Labs"}, {Value: "google", Label: "Google"}, {Value: "custom", Label: "Своє API"}}

// framesCard is «Кадри».
type framesCard struct {
	provider gui.Segmented
	shown    string // the value the selector showed last frame (tests)
	model    field  // Google image model
	toGoogle gui.Clickable
	toGlabs  gui.Clickable
	test     gui.Clickable
	check    check
	saved    savedNote
	custom   customPanel
}

// videoCard is «Відео».
type videoCard struct {
	provider gui.Segmented
	shown    string
	model    gui.Select // G-Labs model
	gmodel   gui.Select // Google model
	res      gui.Segmented
	length   gui.Segmented
	dynamic  switchRow
	ultra    switchRow
	toGoogle gui.Clickable
	toGlabs  gui.Clickable
	test     gui.Clickable
	check    check
	saved    savedNote
	custom   customPanel
}

// glabsCard is «G-Labs Studio»: the address and key both cards use.
type glabsCard struct {
	base  field
	key   keyInput
	test  gui.Clickable
	check check
	saved savedNote
}

// setMediaProvider switches who draws frames (video false) or shoots clips,
// picking the first own-API preset the first time and keeping the clip length
// among the new provider's lengths.
func setMediaProvider(cf *config.Config, video bool, v string) {
	if video {
		cf.Video.Provider = v
		if v == "custom" && cf.Custom.VideoPreset == "" {
			if l := providers.VideoPresets(); len(l) > 0 {
				cf.Custom.VideoPreset = l[0].ID
			}
		}
		fitLength(cf)
		return
	}
	cf.Image.Provider = v
	if v == "custom" && cf.Custom.ImagePreset == "" {
		if l := providers.ImagePresets(); len(l) > 0 {
			cf.Custom.ImagePreset = l[0].ID
		}
	}
}

// clipLengths are the clip lengths the configured video model renders, from
// the engine (studio.VideoCapsFor on the page's settings, so a change shows at
// once): G-Labs with or without Google AI Ultra, Google (Omni / Veo), the own
// API's preset and model. all adds what G-Labs renders with Ultra — shown
// dimmed, as a hint.
func clipLengths(c config.Config) (allowed, all []int) {
	allowed = studio.VideoCapsFor(c).Lengths
	all = allowed
	if c.Video.Provider == "glabs" && !c.Video.Ultra {
		u := c
		u.Video.Ultra = true
		all = studio.VideoCapsFor(u).Lengths
	}
	return allowed, all
}

// glabsUltraHints warns about what G-Labs renders only on Google AI Ultra
// accounts (without one it fails the task): the veo_31_lite_relaxed model
// and 4K. "" when nothing needs Ultra.
func glabsUltraHints(v config.Video) (model, res string) {
	if v.Ultra {
		return "", ""
	}
	if v.GlabsModel == "veo_31_lite_relaxed" {
		model = "Veo 3.1 Lite Relaxed — лише з Google AI Ultra: без неї G-Labs відхилить кожен кліп."
	}
	if v.Resolution == "4K" {
		res = "4K — лише з Google AI Ultra: без неї G-Labs відхилить кожен кліп. Обери 1080p або увімкни Ultra."
	}
	return model, res
}

// fitLength keeps Video.Length among the allowed lengths after a model,
// provider or Ultra change: the nearest one, the longer on a tie.
func fitLength(cf *config.Config) {
	caps := studio.VideoCapsFor(*cf)
	if n := len(caps.Lengths); n > 0 {
		cf.Video.Length = caps.Snap(float64(cf.Video.Length), caps.Lengths[n-1])
	}
}

// noGoogleKeys is the warning of a card set to Google without Google keys.
func (p *Page) noGoogleKeys(th *gui.Theme, what string, back, toKeys *gui.Clickable) layout.Widget {
	if len(p.now.Google.Keys) > 0 {
		return nil
	}
	return func(gtx C) D {
		return warnBox(gtx, th, "Ключів Google ще немає",
			what+" через Google не вийде, доки не додаси ключ у картці «Google AI Studio».",
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconKeyRound, Text: "Додати ключі Google"}.Layout(gtx, th, toKeys)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Повернути G-Labs"}.Layout(gtx, th, back)
			})
	}
}

// ---- Кадри ------------------------------------------------------------------------------------

func (p *Page) framesCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.frames
	flash := func() { s.saved.want = true }
	if v, ok := s.provider.Changed(gtx); ok && v != p.now.Image.Provider {
		p.cfg.set(c, func(cf *config.Config) { setMediaProvider(cf, false, v) }, flash)
		s.check.reset()
	}
	if s.toGoogle.Clicked(gtx) {
		p.goTo(gtx, "google")
	}
	if s.toGlabs.Clicked(gtx) {
		p.cfg.set(c, func(cf *config.Config) { setMediaProvider(cf, false, "glabs") }, flash)
	}
	cfg := p.now
	prov := cfg.Image.Provider
	if s.test.Clicked(gtx) {
		switch prov {
		case "glabs":
			p.runCheck(c, &s.check, c.E.TestGlabs)
		case "google":
			p.runCheck(c, &s.check, c.E.TestGoogle)
		}
	}
	s.shown = prov
	body := []layout.Widget{
		labeled(th, "Хто малює кадри", "", func(gtx C) D {
			s.provider.Full = true
			return s.provider.Layout(gtx, th, prov, mediaProviders)
		}),
	}
	advice := ""
	switch prov {
	case "glabs":
		body = append(body, func(gtx C) D {
			return hintLine(gtx, th, gui.IconImage, "Nano Banana через G-Labs Studio і твій Google Flow. Модель (Nano Banana 2 чи Pro) вибирається в проєкті, на кроці «Сценарій»; адреса й ключ — у картці «G-Labs Studio».")
		})
		advice = "Відкрий G-Labs Studio → Webhook API → Generate → Start Server і перевір адресу та ключ."
	case "google":
		body = append(body,
			func(gtx C) D {
				return fieldRow(gtx, 240, textIn(&s.model, th, "Модель кадрів Google", "Порожньо — "+config.Default().Google.ImageModel+".",
					"gemini-3.1-flash-image", cfg.Google.ImageModel, func(v string) {
						p.cfg.set(c, func(cf *config.Config) { cf.Google.ImageModel = strings.TrimSpace(v) }, flash)
					}))
			},
			p.noGoogleKeys(th, "Малювати кадри", &s.toGlabs, &s.toGoogle))
		advice = "Перевір ключі в картці «Google AI Studio» — можливо, ключ вичерпав денний ліміт."
	case "custom":
		body = append(body, func(gtx C) D { return p.customPanel(gtx, c, &s.custom, false, flash) })
	}
	if prov != "custom" { // the own API's panel has its own check
		body = append(body,
			actions(checkButton(th, &s.test, &s.check, "", false)),
			func(gtx C) D { return s.check.result(gtx, th, advice) })
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconImage, Title: "Кадри",
		Text:  "Хто малює кадри сцен, героїв і обкладинку: G-Labs Studio, Google Gemini або твій власний сервіс.",
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	}, body...)
}

// ---- Відео ------------------------------------------------------------------------------------

func (p *Page) videoCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.video
	flash := func() { s.saved.want = true }
	if v, ok := s.provider.Changed(gtx); ok && v != p.now.Video.Provider {
		p.cfg.set(c, func(cf *config.Config) { setMediaProvider(cf, true, v) }, flash)
		s.check.reset()
	}
	if s.toGoogle.Clicked(gtx) {
		p.goTo(gtx, "google")
	}
	if s.toGlabs.Clicked(gtx) {
		p.cfg.set(c, func(cf *config.Config) { setMediaProvider(cf, true, "glabs") }, flash)
	}
	if v, ok := s.model.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Video.GlabsModel = v; fitLength(cf) }, flash)
	}
	if v, ok := s.gmodel.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Google.VeoModel = v; fitLength(cf) }, flash)
	}
	if v, ok := s.res.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Video.Resolution = v }, flash)
	}
	if v, ok := s.length.Changed(gtx); ok {
		if n, err := strconv.Atoi(v); err == nil {
			p.cfg.set(c, func(cf *config.Config) { cf.Video.Length = n }, flash)
		}
	}
	cfg := p.now
	prov := cfg.Video.Provider
	if s.test.Clicked(gtx) {
		switch prov {
		case "glabs":
			p.runCheck(c, &s.check, c.E.TestGlabs)
		case "google":
			p.runCheck(c, &s.check, c.E.TestGoogle)
		}
	}
	s.shown = prov
	body := []layout.Widget{
		labeled(th, "Хто знімає відео", "", func(gtx C) D {
			s.provider.Full = true
			return s.provider.Layout(gtx, th, prov, mediaProviders)
		}),
	}
	length := p.lengthField(th, s, cfg)
	advice := ""
	switch prov {
	case "glabs":
		modelHint, resHint := glabsUltraHints(cfg.Video)
		model := labeled(th, "", modelHint, func(gtx C) D {
			return s.model.Layout(gtx, th, gui.SelectStyle{Label: "Модель відео", Options: glabsModels, Value: cfg.Video.GlabsModel, Leading: gui.IconFilm})
		})
		res := labeled(th, "Роздільність", resHint, func(gtx C) D {
			s.res.Full = true
			// 4K is an upscale only Google AI Ultra accounts get (G-Labs fails the task otherwise).
			return s.res.Layout(gtx, th, cfg.Video.Resolution, []gui.SegOption{{Value: "720p", Label: "720p"}, {Value: "1080p", Label: "1080p"},
				{Value: "4K", Label: "4K", Disabled: !cfg.Video.Ultra && cfg.Video.Resolution != "4K"}})
		})
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, model, res, length) })
		advice = "Відкрий G-Labs Studio → Webhook API → Generate → Start Server і перевір адресу та ключ."
	case "google":
		model := func(gtx C) D {
			hint := ""
			if !config.GoogleOmni(cfg.Google.VeoModel) {
				hint = "Veo 3.1 у Gemini API вимикається 22 жовтня 2026 — переходь на Gemini Omni Flash."
			}
			return s.gmodel.Layout(gtx, th, gui.SelectStyle{Label: "Модель відео Google", Options: googleVideoOptions(cfg.Google.VeoModel),
				Value: cfg.Google.VeoModel, Placeholder: config.GoogleOmniModel, Hint: hint, Leading: gui.IconFilm})
		}
		var res layout.Widget
		if config.GoogleRendersOmni(cfg.Google.VeoModel) {
			// Gemini Omni renders 720p natively; 1080p and 4K are upscaled (slower).
			res = labeled(th, "Роздільність", "1080p і 4K Gemini Omni масштабує з 720p — це довше.", func(gtx C) D {
				s.res.Full = true
				return s.res.Layout(gtx, th, cfg.Video.Resolution, []gui.SegOption{{Value: "720p", Label: "720p"}, {Value: "1080p", Label: "1080p"}, {Value: "4K", Label: "4K"}})
			})
		}
		body = append(body, func(gtx C) D { return fieldRow(gtx, 240, model, res, length) },
			p.noGoogleKeys(th, "Знімати відео", &s.toGlabs, &s.toGoogle))
		advice = "Перевір ключі в картці «Google AI Studio» — можливо, ключ вичерпав денний ліміт."
	case "custom":
		body = append(body,
			func(gtx C) D { return p.customPanel(gtx, c, &s.custom, true, flash) },
			func(gtx C) D { return fieldRow(gtx, 240, length) })
	}
	body = append(body, func(gtx C) D {
		ws := []layout.Widget{
			func(gtx C) D {
				return s.dynamic.layout(gtx, th, cfg.Video.Dynamic, "Динамічна довжина сцен",
					"Кліп рівно під репліку: 5,7 с мови → 6 с відео, а не 10.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.Video.Dynamic = v }, flash)
					})
			},
		}
		if prov == "glabs" {
			ws = append(ws, func(gtx C) D {
				return s.ultra.layout(gtx, th, cfg.Video.Ultra, "Google AI Ultra",
					"Є підписка Ultra — Veo зніме й кліпи на 4 і 6 с, доступні 4K і Veo 3.1 Lite Relaxed. Omni Flash знімає 4–10 с і без неї.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) {
							cf.Video.Ultra = v
							if !v && cf.Video.Resolution == "4K" { // 4K needs Ultra
								cf.Video.Resolution = "1080p"
							}
							fitLength(cf)
						}, flash)
					})
			})
		}
		return lines(gtx, th, ws...)
	})
	if prov != "custom" {
		body = append(body,
			actions(checkButton(th, &s.test, &s.check, "", false)),
			func(gtx C) D { return s.check.result(gtx, th, advice) })
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconClapperboard, Title: "Відео",
		Text:  "Хто знімає кліпи сцен — G-Labs (Veo 3.1, Omni Flash), Google (Gemini Omni) або твій сервіс — їхня довжина та якість.",
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	}, body...)
}

// lengthField is «Довжина однієї сцени»: the lengths the video model renders,
// the ones only Google AI Ultra unlocks dimmed.
func (p *Page) lengthField(th *gui.Theme, s *videoCard, cfg config.Config) layout.Widget {
	allowed, all := clipLengths(cfg)
	var lengths []gui.SegOption
	ok := map[int]bool{}
	for _, l := range allowed {
		ok[l] = true
	}
	for _, l := range all {
		lengths = append(lengths, gui.SegOption{Value: strconv.Itoa(l), Label: strconv.Itoa(l) + " с", Disabled: !ok[l]})
	}
	var lens []string
	for _, l := range allowed {
		lens = append(lens, strconv.Itoa(l))
	}
	var extra []string // what G-Labs renders only with Google AI Ultra
	for _, l := range all {
		if !ok[l] {
			extra = append(extra, strconv.Itoa(l))
		}
	}
	hint := "Ця модель знімає " + strings.Join(lens, ", ") + " с."
	if n := len(extra); n > 0 {
		hint += " " + strings.Join(extra[:n-1], ", ")
		if n > 1 {
			hint += " і "
		}
		hint += extra[n-1] + " с — з Google AI Ultra."
	}
	if cfg.Video.Dynamic {
		hint = "Для сцен без озвучки. Решту студія підбирає під репліку: " + strings.Join(lens, ", ") + " с."
	}
	if len(allowed) == 0 {
		hint = "Довжину кліпу задає сама модель."
	}
	return labeled(th, "Довжина однієї сцени", hint, func(gtx C) D {
		s.length.Full = true
		if len(lengths) == 0 {
			return gui.Text{Style: th.Body, Color: th.Pal.Text2}.Layout(gtx, th, "Як у моделі")
		}
		return s.length.Layout(gtx, th, strconv.Itoa(cfg.Video.Length), lengths)
	})
}

// ---- G-Labs Studio ----------------------------------------------------------------------------

// glabsUses names what G-Labs serves ("" = nothing: the card hides).
func glabsUses(cfg config.Config) string {
	var uses []string
	if cfg.Image.Provider == "glabs" {
		uses = append(uses, "кадри")
	}
	if cfg.Video.Provider == "glabs" {
		uses = append(uses, "відео")
	}
	return strings.Join(uses, " і ")
}

// glabsText is the line under «G-Labs Studio».
func glabsText(uses string) string {
	if strings.Contains(uses, " і ") {
		return "Через G-Labs ідуть " + uses + ": адреса й ключ — спільні для обох карток."
	}
	return "Через G-Labs ідуть " + uses + ". Адреса Webhook API і ключ — з G-Labs Studio на цьому комп'ютері."
}

func (p *Page) glabsCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.glabs
	cfg := p.now
	uses := glabsUses(cfg)
	if uses == "" {
		return D{}
	}
	flash := func() { s.saved.want = true }
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, c.E.TestGlabs)
	}
	base := textIn(&s.base, th, "Адреса Webhook API", "", "http://127.0.0.1:8765", cfg.Glabs.BaseURL,
		func(v string) { p.cfg.set(c, func(cf *config.Config) { cf.Glabs.BaseURL = v }, flash) })
	key := func(gtx C) D {
		return s.key.layout(gtx, th, "API-ключ", cfg.Glabs.APIKey, "Ключ із вкладки Webhook API", func(v string) {
			p.cfg.set(c, func(cf *config.Config) { cf.Glabs.APIKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ G-Labs збережено") })
		})
	}
	body := []layout.Widget{func(gtx C) D { return fieldRow(gtx, 240, base, key) }}
	if strings.TrimSpace(cfg.Glabs.APIKey) == "" {
		body = append(body, func(gtx C) D {
			return softWarning(gtx, th, "Ключа G-Labs ще немає — без нього "+uses+" не згенеруються. Візьми його в G-Labs Studio → Webhook API.")
		})
	}
	body = append(body,
		actions(checkButton(th, &s.test, &s.check, "Перевірити G-Labs", false)),
		func(gtx C) D {
			return s.check.result(gtx, th, "Відкрий G-Labs Studio → Webhook API → Generate → Start Server і перевір адресу та ключ.")
		})
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconServer, Title: "G-Labs Studio",
		Text:  glabsText(uses),
		Trail: trails(s.saved.widget(th), s.check.statusPill(th)),
	}, body...)
}
