package settings

// «Вибрати з каталогу»: the preset catalogues (providers/catalog for video,
// images and text, tts/catalog for voice-over) in a searchable chooser
// (pickers.Catalog). Picking an entry fills the card: the preset, its base
// URL, the model — and for the universal preset the API description — or,
// for voice-over, the provider, model, voice and server address. The key is
// never touched: a pick that changes the service reminds where the key comes
// from. Replacing a description or a server address the user has asks first.

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/providers/catalog"
	ttscat "ytcartell/internal/tts/catalog"

	"ytcartell/internal/tts"
)

// catalogFooter closes every catalogue list.
const catalogFooter = "Вбудований каталог: кожен запис звірено з документацією сервісу (посилання «Джерело»). Ключ вставляєш сам — каталог його не змінює."

// catalogUI is the state of one card's catalogue button and chooser.
type catalogUI struct {
	open gui.Clickable
	pick pickers.Catalog
	// The chooser's items, rebuilt only when what the card uses changes.
	key   string
	style pickers.CatalogStyle
}

// ---- labels -----------------------------------------------------------------------

var videoModeLabels = map[string]string{
	catalog.ModeT2V: "текст → відео", catalog.ModeI2V: "кадр → відео", catalog.ModeRef: "за референсами",
	catalog.ModeFirstLast: "перший і останній кадр", catalog.ModeV2V: "редагування відео",
}

var imageModeLabels = map[string]string{catalog.ModeT2I: "текст → кадр", catalog.ModeEdit: "за референсами"}

func modeWords(modes []string, labels map[string]string, order []string) string {
	var out []string
	for _, m := range order {
		if slices.Contains(modes, m) {
			out = append(out, labels[m])
		}
	}
	return strings.Join(out, ", ")
}

// lengthsText: "кліпи 3–15 с" for a run of whole seconds, else "кліпи 4, 6, 8 с".
func lengthsText(ls []int) string {
	switch {
	case len(ls) == 0:
		return ""
	case len(ls) == 1:
		return "кліп " + strconv.Itoa(ls[0]) + " с"
	case len(ls) > 2 && ls[len(ls)-1]-ls[0] == len(ls)-1:
		return "кліпи " + strconv.Itoa(ls[0]) + "–" + strconv.Itoa(ls[len(ls)-1]) + " с"
	}
	s := make([]string, len(ls))
	for i, l := range ls {
		s[i] = strconv.Itoa(l)
	}
	return "кліпи " + strings.Join(s, ", ") + " с"
}

func refsText(n int) string {
	if n <= 0 {
		return ""
	}
	return "до " + strconv.Itoa(n) + " " + gui.PluralUK(n, "референса", "референсів", "референсів")
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func aspectsText(as []string) string {
	if len(as) > 6 {
		return strings.Join(as[:6], ", ") + " …"
	}
	return strings.Join(as, ", ")
}

// hostGroup is the «Сервіс» filter: hosts by how many entries they have.
func hostGroup(key, label string, hosts map[string]string, counts map[string]int) pickers.CatalogGroup {
	g := pickers.CatalogGroup{Key: key, Label: label}
	for h, l := range hosts {
		g.Options = append(g.Options, pickers.CatalogOption{Value: h, Label: l})
	}
	sort.SliceStable(g.Options, func(i, j int) bool {
		a, b := g.Options[i], g.Options[j]
		if counts[a.Value] != counts[b.Value] {
			return counts[a.Value] > counts[b.Value]
		}
		return a.Label < b.Label
	})
	return g
}

func modeGroup(labels map[string]string, order []string) pickers.CatalogGroup {
	g := pickers.CatalogGroup{Key: "mode", Label: "Що робить"}
	for _, m := range order {
		g.Options = append(g.Options, pickers.CatalogOption{Value: m, Label: labels[m]})
	}
	return g
}

var (
	videoModeOrder = []string{catalog.ModeT2V, catalog.ModeI2V, catalog.ModeRef, catalog.ModeFirstLast, catalog.ModeV2V}
	imageModeOrder = []string{catalog.ModeT2I, catalog.ModeEdit}
)

// ---- video and images ---------------------------------------------------------------

// modelCatalogStyle is the chooser of video (or image) models; cur marks
// what the card uses now.
func modelCatalogStyle(video bool, cur customVals) pickers.CatalogStyle {
	hosts, counts := map[string]string{}, map[string]int{}
	var items []pickers.CatalogItem
	add := func(e catalog.Entry, meta string, badges []pickers.CatalogBadge, tags []string) {
		hosts[e.Host] = firstText(hosts[e.Host], e.HostLabel)
		counts[e.Host]++
		tags = append(tags, "host="+e.Host)
		for _, m := range e.Modes {
			tags = append(tags, "mode="+m)
		}
		if e.Popular >= 2 {
			tags = append(tags, "popular")
		}
		if e.Preset == providers.PresetUniversal {
			badges = append(badges, pickers.CatalogBadge{Text: "опис API", Tone: gui.ToneNeutral})
		}
		note := e.Notes
		if e.Price != "" {
			note = joinNonEmpty(" ", note, "Ціна: "+e.Price+".")
		}
		items = append(items, pickers.CatalogItem{
			ID: e.ID, Title: e.Label,
			Sub:  joinNonEmpty(" · ", e.HostLabel, e.Vendor, e.Model),
			Meta: meta, Note: note, Badges: badges, Popular: e.Popular >= 3,
			Current: cur.preset == e.Preset && strings.EqualFold(firstText(cur.model, presetDefaultModel(video, cur.preset)), e.Model),
			Tags:    tags, Search: e.Haystack(), Link: e.Source,
		})
	}
	st := pickers.CatalogStyle{Footer: catalogFooter}
	if video {
		for _, e := range catalog.Video() {
			var badges []pickers.CatalogBadge
			var tags []string
			switch e.Audio {
			case catalog.AudioSpeech:
				badges = append(badges, pickers.CatalogBadge{Text: "голос у відео", Tone: gui.ToneSuccess})
				tags = append(tags, "audio", "speech")
			case catalog.AudioSound:
				badges = append(badges, pickers.CatalogBadge{Text: "звук", Tone: gui.ToneNeutral})
				tags = append(tags, "audio")
			}
			if e.MaxRefs > 0 {
				tags = append(tags, "refs")
			}
			meta := joinNonEmpty(" · ", modeWords(e.Modes, videoModeLabels, videoModeOrder), lengthsText(e.Lengths),
				aspectsText(e.Aspects), strings.Join(e.Resolutions, ", "), refsText(e.MaxRefs), promptText(e.PromptMax))
			add(e.Entry, meta, badges, tags)
		}
		st.Title, st.Subtitle = "Каталог моделей відео", strconv.Itoa(len(items))+" моделей на fal.ai, Replicate, Runway, Luma, MiniMax, RoyalTechno і власних API сервісів. Вибір заповнить сервіс, адресу й модель."
		st.Groups = []pickers.CatalogGroup{hostGroup("host", "Сервіс", hosts, counts), modeGroup(videoModeLabels, videoModeOrder)}
		st.Toggles = []pickers.CatalogToggle{
			{Key: "speech", Label: "Голос у відео", Icon: gui.IconMessageCircle},
			{Key: "audio", Label: "Зі звуком", Icon: gui.IconVolume2},
			{Key: "refs", Label: "Бере референси", Icon: gui.IconImages},
			{Key: "popular", Label: "Популярне", Icon: gui.IconStar},
		}
		st.Items = items
		return st
	}
	for _, e := range catalog.Image() {
		var badges []pickers.CatalogBadge
		var tags []string
		if e.MaxRefs > 0 {
			tags = append(tags, "refs")
		}
		if e.Text {
			badges = append(badges, pickers.CatalogBadge{Text: "пише текст", Tone: gui.ToneNeutral})
			tags = append(tags, "text")
		}
		meta := joinNonEmpty(" · ", modeWords(e.Modes, imageModeLabels, imageModeOrder), refsText(e.MaxRefs),
			aspectsText(e.Aspects), strings.Join(e.Sizes, ", "), promptText(e.PromptMax))
		add(e.Entry, meta, badges, tags)
	}
	st.Title, st.Subtitle = "Каталог моделей кадрів", strconv.Itoa(len(items))+" моделей на OpenAI, fal.ai, Replicate, RoyalTechno і власних API сервісів. Вибір заповнить сервіс, адресу й модель."
	st.Groups = []pickers.CatalogGroup{hostGroup("host", "Сервіс", hosts, counts), modeGroup(imageModeLabels, imageModeOrder)}
	st.Toggles = []pickers.CatalogToggle{
		{Key: "refs", Label: "Бере референси", Icon: gui.IconImages},
		{Key: "text", Label: "Добре пише текст", Icon: gui.IconType},
		{Key: "popular", Label: "Популярне", Icon: gui.IconStar},
	}
	st.Items = items
	return st
}

func promptText(n int) string {
	if n <= 0 {
		return ""
	}
	return "промпт до " + thousands(n) + " симв."
}

func presetDefaultModel(video bool, preset string) string {
	find := providers.FindImagePreset
	if video {
		find = providers.FindVideoPreset
	}
	if p, ok := find(preset); ok {
		return p.DefaultModel
	}
	return ""
}

// modelEntry finds a catalogue entry by id.
func modelEntry(video bool, id string) (catalog.Entry, bool) {
	if video {
		for _, e := range catalog.Video() {
			if e.ID == id {
				return e.Entry, true
			}
		}
		return catalog.Entry{}, false
	}
	for _, e := range catalog.Image() {
		if e.ID == id {
			return e.Entry, true
		}
	}
	return catalog.Entry{}, false
}

// applyModelEntry fills the card's settings from e. The base URL stays empty
// when it is the preset's own; the key is kept.
func applyModelEntry(cu *config.CustomAPI, video bool, e catalog.Entry) {
	base := strings.TrimRight(strings.TrimSpace(e.BaseURL), "/")
	find := providers.FindImagePreset
	if video {
		find = providers.FindVideoPreset
	}
	if p, ok := find(e.Preset); ok && strings.EqualFold(base, strings.TrimRight(p.DefaultBaseURL, "/")) {
		base = ""
	}
	spec := ""
	if e.Preset == providers.PresetUniversal {
		spec = providers.FormatSpec([]byte(e.SpecText()))
	}
	if video {
		cu.VideoPreset, cu.VideoBaseURL, cu.VideoModel = e.Preset, base, e.Model
		if e.Preset == providers.PresetUniversal {
			cu.VideoSpec = spec
		}
		return
	}
	cu.ImagePreset, cu.ImageBaseURL, cu.ImageModel = e.Preset, base, e.Model
	if e.Preset == providers.PresetUniversal {
		cu.ImageSpec = spec
	}
}

// modelCatalog handles the card's chooser: a pick is applied (asking first
// when it would replace the user's own API description) and the chooser is
// queued above the window.
func (p *Page) modelCatalog(gtx C, c *core.Ctx, ui *catalogUI, video bool, vals customVals, flash func(), onPick func()) {
	if ui.open.Clicked(gtx) {
		ui.pick.Open()
	}
	if id, ok := ui.pick.Changed(); ok {
		if e, found := modelEntry(video, id); found {
			apply := func() {
				if onPick != nil {
					onPick()
				}
				p.cfg.set(c, func(cf *config.Config) {
					applyModelEntry(&cf.Custom, video, e)
					if video {
						fitLength(cf) // the clip length follows the new model
					}
				}, flash)
				msg := "Вибрано: " + e.Label + " · " + e.HostLabel
				if e.Preset != vals.preset || e.Preset == providers.PresetUniversal {
					msg += ". Встав ключ цього сервісу й натисни «Перевірити»"
				}
				c.Toast(gui.ToneSuccess, msg)
			}
			own := strings.TrimSpace(vals.spec)
			if e.Preset == providers.PresetUniversal && own != "" &&
				providers.FormatSpec([]byte(own)) != providers.FormatSpec([]byte(e.SpecText())) {
				c.Confirm(core.Confirm{
					Title: "Замінити опис API?",
					Text:  "Твій опис API заміниться описом " + e.HostLabel + " для " + e.Label + ". Старий опис не збережеться.",
					Icon:  gui.IconServer, OK: "Замінити",
					OnOK: apply,
				})
			} else {
				apply()
			}
		}
	}
	ui.pick.Layout(gtx, c, ui.memo(vals.preset+"|"+vals.model, func() pickers.CatalogStyle { return modelCatalogStyle(video, vals) }))
}

// catalogRow is the «Вибрати з каталогу» button of the own-API card with a
// line on what the catalogue holds.
func catalogRow(th *gui.Theme, ui *catalogUI, video bool) func(gtx C) D {
	n, what := len(catalog.Embedded().Image), "моделей кадрів"
	if video {
		n, what = len(catalog.Embedded().Video), "моделей відео"
	}
	return catalogLine(th, &ui.open, n, what, "сервіс, адреса й модель заповняться самі")
}

// catalogLine: a tinted button «Вибрати з каталогу» + «162 моделей відео: …».
func catalogLine(th *gui.Theme, btn *gui.Clickable, n int, what, tail string) func(gtx C) D {
	return func(gtx C) D {
		if n == 0 {
			return D{}
		}
		return gui.Flow{Gap: gui.Sp12, RowGap: gui.Sp8}.Layout(gtx,
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconLayoutGrid, Text: "Вибрати з каталогу"}.Layout(gtx, th, btn)
			},
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, strconv.Itoa(n)+" "+what+" з параметрами — пошук і фільтри; "+tail+"."),
		)
	}
}

// catalogCapsLine is capsLine with what the catalogue knows of the model
// (modes, sound, resolutions); "" from the catalogue = capsLine as it was.
func catalogCapsLine(pre providers.Preset, vals customVals, video bool) string {
	base := capsLine(pre, vals.model, vals.spec, video)
	model := firstText(vals.model, pre.DefaultModel)
	if video {
		e, ok := catalog.FindVideo(pre.ID, model)
		if !ok {
			return base
		}
		extra := joinNonEmpty(" · ", modeWords(e.Modes, videoModeLabels, videoModeOrder), strings.Join(e.Resolutions, ", "), audioText(e.Audio))
		if extra == "" {
			return base
		}
		return joinNonEmpty(" ", base, "За каталогом: "+extra+".")
	}
	e, ok := catalog.FindImage(pre.ID, model)
	if !ok {
		return base
	}
	extra := joinNonEmpty(" · ", modeWords(e.Modes, imageModeLabels, imageModeOrder), strings.Join(e.Sizes, ", "))
	if extra == "" {
		return base
	}
	return joinNonEmpty(" ", base, "За каталогом: "+extra+".")
}

func audioText(a string) string {
	switch a {
	case catalog.AudioSpeech:
		return "звук і голос у відео"
	case catalog.AudioSound:
		return "звук без мовлення"
	case catalog.AudioNone:
		return "без звуку"
	}
	return ""
}

// ---- voice-over ---------------------------------------------------------------------

var ttsKindLabels = map[string]string{ttscat.KindModel: "Моделі", ttscat.KindVoice: "Голоси", ttscat.KindServer: "Свої сервери"}

// ttsProviderOK: the studio speaks the provider id.
func ttsProviderOK(id string) bool { _, ok := tts.ProviderByID(id); return ok }

// ttsCatalogStyle is the voice-over chooser; cur marks what the card uses now.
func ttsCatalogStyle(cur config.TTS) pickers.CatalogStyle {
	vendors, counts := map[string]string{}, map[string]int{}
	var items []pickers.CatalogItem
	for _, e := range ttscat.All() {
		key, label := ttsService(e)
		vendors[key] = label
		counts[key]++
		tags := []string{"vendor=" + key, "kind=" + e.Kind}
		var badges []pickers.CatalogBadge
		if e.Ukrainian {
			tags = append(tags, "uk")
			badges = append(badges, pickers.CatalogBadge{Text: "українська", Tone: gui.ToneSuccess})
		}
		ready := e.Supported(ttsProviderOK)
		if ready {
			tags = append(tags, "ready")
		}
		if e.SpecText() != "" {
			badges = append(badges, pickers.CatalogBadge{Text: "опис API", Tone: gui.ToneNeutral})
		}
		if e.SelfHosted {
			tags = append(tags, "self")
			badges = append(badges, pickers.CatalogBadge{Text: "свій сервер", Tone: gui.ToneNeutral})
		}
		if e.Popular >= 2 {
			tags = append(tags, "popular")
		}
		langs := e.LanguagesNote
		if langs == "" && e.Languages > 0 {
			langs = strconv.Itoa(e.Languages) + " " + gui.PluralUK(e.Languages, "мова", "мови", "мов")
		}
		meta := joinNonEmpty(" · ", langs, charsText(e.MaxChars), concText(e.Concurrency), genderText(e.Gender))
		sub := e.Vendor
		if pr, ok := tts.ProviderByID(e.Provider); ok {
			sub = pr.Label
		}
		sub = joinNonEmpty(" · ", sub, e.Model, firstText(e.VoiceName, e.Voice), e.BaseURL)
		note := e.Notes
		if e.Price != "" {
			note = joinNonEmpty(" ", note, "Ціна: "+e.Price+".")
		}
		it := pickers.CatalogItem{
			ID: e.ID, Title: e.Label, Sub: sub, Meta: meta, Note: note, Badges: badges, Popular: e.Popular >= 3,
			Tags: tags, Search: e.Haystack(), Link: e.Source,
			Current: ready && e.Provider == cur.Provider && (e.Voice == "" || e.Voice == cur.Voice) &&
				(e.Model == "" || e.Model == cur.Model) && (e.BaseURL == "" || strings.EqualFold(strings.TrimRight(e.BaseURL, "/"), strings.TrimRight(cur.BaseURL, "/"))),
		}
		if !ready {
			it.Disabled = "Студія ще не озвучує через цей сервіс напряму — тут його можливості й документація."
		}
		items = append(items, it)
	}
	g := hostGroup("vendor", "Сервіс", vendors, counts)
	kinds := pickers.CatalogGroup{Key: "kind", Label: "Що це"}
	for _, k := range []string{ttscat.KindModel, ttscat.KindVoice, ttscat.KindServer} {
		kinds.Options = append(kinds.Options, pickers.CatalogOption{Value: k, Label: ttsKindLabels[k]})
	}
	return pickers.CatalogStyle{
		Title:    "Каталог озвучки",
		Subtitle: strconv.Itoa(len(items)) + " моделей, голосів і серверів. Вибір заповнить сервіс, модель, голос і адресу — ключ лишається твоїм.",
		Items:    items, Groups: []pickers.CatalogGroup{g, kinds},
		Toggles: []pickers.CatalogToggle{
			{Key: "uk", Label: "Українська", Icon: gui.IconLanguages},
			{Key: "ready", Label: "Працює в студії", Icon: gui.IconCheck},
			{Key: "self", Label: "Свій сервер", Icon: gui.IconHardDrive},
			{Key: "popular", Label: "Популярне", Icon: gui.IconStar},
		},
		Placeholder: "Пошук: сервіс, модель, голос, мова",
		Footer:      catalogFooter,
	}
}

func charsText(n int) string {
	if n <= 0 {
		return ""
	}
	return "до " + thousands(n) + " симв. за запит"
}

func concText(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n) + " " + gui.PluralUK(n, "запит", "запити", "запитів") + " одночасно"
}

func genderText(g string) string {
	switch strings.ToLower(g) {
	case "female":
		return "жіночий"
	case "male":
		return "чоловічий"
	case "neutral":
		return "нейтральний"
	}
	return ""
}

func ttsEntry(id string) (ttscat.Entry, bool) {
	for _, e := range ttscat.All() {
		if e.ID == id {
			return e, true
		}
	}
	return ttscat.Entry{}, false
}

// applyTTSEntry switches to the entry's provider and sets its model, voice
// and address (an empty one keeps what the provider has); an entry with a
// spec («Власний сервер» through the universal adapter) stores its spec.
func applyTTSEntry(cf *config.Config, e ttscat.Entry) {
	t := &cf.TTS
	if spec := e.SpecText(); spec != "" {
		cf.Custom.TTSSpec = providers.FormatSpec([]byte(spec))
	}
	if t.Provider != e.Provider {
		t.Switch(e.Provider)
	}
	if e.Model != "" {
		t.Model = e.Model
	}
	if e.Voice != "" {
		t.Voice = e.Voice
	}
	if e.BaseURL != "" {
		t.BaseURL = e.BaseURL
	}
}

// ttsCatalog handles the voice-over chooser (see modelCatalog).
func (p *Page) ttsCatalog(gtx C, c *core.Ctx, ui *catalogUI, cur config.TTS, flash func(), onPick func()) {
	if ui.open.Clicked(gtx) {
		ui.pick.Open()
	}
	if id, ok := ui.pick.Changed(); ok {
		if e, found := ttsEntry(id); found && e.Supported(ttsProviderOK) {
			apply := func() {
				if onPick != nil {
					onPick()
				}
				p.cfg.set(c, func(cf *config.Config) { applyTTSEntry(cf, e) }, func() {
					flash()
					c.ReloadVoices()
				})
				msg := "Вибрано: " + e.Label
				if pr, ok := tts.ProviderByID(e.Provider); ok && e.Provider != cur.Provider {
					msg += ". Ключі " + pr.Label + " — " + pr.KeyHint
				}
				c.Toast(gui.ToneSuccess, msg)
			}
			// Replacing the user's own API description, or the address of another
			// server than the one set up for this provider: ask.
			ownSpec := strings.TrimSpace(p.now.Custom.TTSSpec)
			own := strings.TrimRight(strings.TrimSpace(cur.Of(e.Provider).BaseURL), "/")
			if e.SpecText() != "" && ownSpec != "" && providers.FormatSpec([]byte(ownSpec)) != providers.FormatSpec([]byte(e.SpecText())) {
				c.Confirm(core.Confirm{
					Title: "Замінити опис API озвучки?",
					Text:  "Твій опис «Власного сервера» заміниться описом " + e.Vendor + ". Старий опис не збережеться.",
					Icon:  gui.IconServer, OK: "Замінити",
					OnOK: apply,
				})
			} else if e.BaseURL != "" && own != "" && !strings.EqualFold(own, strings.TrimRight(e.BaseURL, "/")) {
				c.Confirm(core.Confirm{
					Title: "Змінити адресу сервера?",
					Text:  "Зараз озвучка йде через " + own + ". Після вибору — через " + e.BaseURL + ".",
					Icon:  gui.IconServer, OK: "Змінити",
					OnOK: apply,
				})
			} else {
				apply()
			}
		}
	}
	ui.pick.Layout(gtx, c, ui.memo(strings.Join([]string{cur.Provider, cur.Model, cur.Voice, cur.BaseURL}, "|"), func() pickers.CatalogStyle { return ttsCatalogStyle(cur) }))
}

// ---- text (LLM) providers ---------------------------------------------------------------
//
// The text card (connections.go) hooks the chooser in like the other cards:
//
//	catalog catalogUI                                   // in textCard
//	p.textCatalog(gtx, c, &s.catalog, cfg.Text, flash, func() { s.base.edited, s.model.edited = false, false; s.check.reset() })
//	textCatalogRow(th, &s.catalog),                     // in the card body

var textProtocolLabels = map[string]string{
	"openai": "OpenAI-сумісний", "anthropic": "Anthropic", "gemini": "Gemini", "azure-openai": "Azure OpenAI",
	"vertex": "Vertex AI", "bedrock": "Bedrock", "cohere": "Cohere", "ollama": "Ollama",
}

// textProvider is the config.Text.Provider that speaks a catalogue protocol
// ("" = none: the protocol needs what the studio cannot do — an OAuth token,
// signed requests — and why).
func textProvider(protocol string) (id, why string) {
	switch protocol {
	case "openai":
		return "openai", ""
	case "anthropic":
		return "anthropic", ""
	case "azure-openai":
		return "azure", ""
	case "gemini":
		return "google", ""
	case "ollama":
		return "ollama", ""
	case "vertex":
		return "", "Vertex AI приймає лише тимчасовий OAuth-токен, а не постійний ключ — підключи Gemini через Google AI Studio."
	}
	return "", "Студія ще не говорить цим протоколом — тут його можливості й документація."
}

// textCatalogStyle is the chooser of text providers; cur marks the one in use.
func textCatalogStyle(cur config.Text) pickers.CatalogStyle {
	counts := map[string]int{}
	var items []pickers.CatalogItem
	for _, e := range catalog.Text() {
		counts[e.Protocol]++
		tags := []string{"protocol=" + e.Protocol}
		var badges []pickers.CatalogBadge
		if e.Local {
			tags = append(tags, "local")
			badges = append(badges, pickers.CatalogBadge{Text: "локально", Tone: gui.ToneNeutral})
		}
		if e.Vision {
			tags = append(tags, "vision")
		}
		if e.Popular >= 2 {
			tags = append(tags, "popular")
		}
		models := e.Models
		if len(models) > 4 {
			models = append(slices.Clone(models[:4]), "…")
		}
		meta := joinNonEmpty(" · ", strings.Join(models, ", "), map[bool]string{true: "бачить зображення"}[e.Vision],
			map[bool]string{true: "JSON-режим"}[e.JSON], map[bool]string{true: "без ключа"}[e.KeyOptional])
		id, why := textProvider(e.Protocol)
		it := pickers.CatalogItem{
			ID: e.ID, Title: e.Label, Sub: joinNonEmpty(" · ", textProtocolLabels[e.Protocol], e.BaseURL),
			Meta: meta, Note: joinNonEmpty(" ", e.Notes, e.KeyHint+"."), Badges: badges, Popular: e.Popular >= 3,
			Tags: tags, Search: e.Haystack(), Link: firstText(e.Docs, e.Source), Disabled: why,
			Current: id != "" && id == cur.Provider && (id == "google" ||
				strings.EqualFold(strings.TrimRight(e.BaseURL, "/"), strings.TrimRight(cur.BaseURL, "/"))),
		}
		items = append(items, it)
	}
	g := pickers.CatalogGroup{Key: "protocol", Label: "Протокол"}
	for _, p := range catalog.TextProtocols {
		if counts[p] > 0 {
			g.Options = append(g.Options, pickers.CatalogOption{Value: p, Label: textProtocolLabels[p]})
		}
	}
	return pickers.CatalogStyle{
		Title:    "Каталог текстових моделей",
		Subtitle: strconv.Itoa(len(items)) + " сервісів і локальних серверів. Вибір заповнить протокол, адресу й модель — ключ лишається твоїм.",
		Items:    items, Groups: []pickers.CatalogGroup{g},
		Toggles: []pickers.CatalogToggle{
			{Key: "local", Label: "Локально", Icon: gui.IconHardDrive},
			{Key: "vision", Label: "Бачить зображення", Icon: gui.IconEye},
			{Key: "popular", Label: "Популярне", Icon: gui.IconStar},
		},
		Placeholder: "Пошук: сервіс, модель, адреса",
		Footer:      catalogFooter,
	}
}

// applyTextEntry sets the protocol, address and model of e (Gemini: the
// Google AI Studio side and its story model). The key is kept.
func applyTextEntry(cf *config.Config, e catalog.TextEntry) bool {
	id, _ := textProvider(e.Protocol)
	if id == "" {
		return false
	}
	cf.Text.Provider = id
	if id == "google" {
		if e.DefaultModel != "" {
			cf.Google.StoryModel = e.DefaultModel
		}
		return true
	}
	cf.Text.BaseURL = e.BaseURL
	if e.DefaultModel != "" {
		cf.Text.Model = e.DefaultModel
	}
	return true
}

// textCatalog handles the text chooser (see modelCatalog).
func (p *Page) textCatalog(gtx C, c *core.Ctx, ui *catalogUI, cur config.Text, flash func(), onPick func()) {
	if ui.open.Clicked(gtx) {
		ui.pick.Open()
	}
	if id, ok := ui.pick.Changed(); ok {
		for _, e := range catalog.Text() {
			if e.ID != id {
				continue
			}
			if onPick != nil {
				onPick()
			}
			p.cfg.set(c, func(cf *config.Config) { applyTextEntry(cf, e) }, flash)
			msg := "Вибрано: " + e.Label
			if strings.Contains(e.BaseURL, "{") {
				msg += ". Заміни в адресі " + e.BaseURL[strings.Index(e.BaseURL, "{"):strings.Index(e.BaseURL, "}")+1] + " на своє"
			} else if !e.KeyOptional && e.Protocol != "gemini" {
				msg += ". " + e.KeyHint
			}
			c.Toast(gui.ToneSuccess, msg)
		}
	}
	ui.pick.Layout(gtx, c, ui.memo(cur.Provider+"|"+cur.BaseURL, func() pickers.CatalogStyle { return textCatalogStyle(cur) }))
}

// textCatalogRow is the text card's «Вибрати з каталогу» line.
func textCatalogRow(th *gui.Theme, ui *catalogUI) func(gtx C) D {
	return catalogLine(th, &ui.open, len(catalog.Embedded().Text), "сервісів тексту", "протокол, адреса й модель заповняться самі")
}

// memo is the chooser's style for key (built again only when key changes;
// the catalogue itself never changes while the app runs).
func (ui *catalogUI) memo(key string, build func() pickers.CatalogStyle) pickers.CatalogStyle {
	if ui.style.Title == "" || ui.key != key {
		ui.key, ui.style = key, build()
	}
	return ui.style
}

// ttsService is the «Сервіс» filter of an entry: where the request goes —
// self-hosted servers together, a gateway by its own name ("Together AI /
// hexgrad" → Together AI).
func ttsService(e ttscat.Entry) (key, label string) {
	if e.SelfHosted {
		return "~self", "Свій сервер"
	}
	label, _, _ = strings.Cut(e.Vendor, " / ")
	label = strings.TrimSpace(label)
	return strings.ToLower(label), label
}
