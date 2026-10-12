package settings

// «Своє API»: the user's own image or video generation service (Image /
// Video.Provider "custom", config.CustomAPI) — a preset (OpenAI Images,
// fal.ai, Replicate …), its address, key and model, and a check that costs
// nothing (Engine.TestCustomImage / TestCustomVideo). The universal preset
// adds the API's JSON description (customspec.go).

import (
	"strconv"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
)

// otherModel is the select value that reveals the free-text model field.
const otherModel = "~other~"

type customPanel struct {
	other    bool // the user wants a model that is not in the list
	preset   gui.Select
	base     field
	key      keyInput
	model    field
	modelSel gui.Select
	docs     gui.Clickable
	test     gui.Clickable
	check    check
	spec     specPanel // the universal preset's description
	catalog  catalogUI // «Вибрати з каталогу» (catalogpick.go)
}

// customVals are the settings of one kind (images or video).
type customVals struct {
	preset, base, key, model, spec string
}

func imageVals(c config.CustomAPI) customVals {
	return customVals{c.ImagePreset, c.ImageBaseURL, c.ImageKey, c.ImageModel, c.ImageSpec}
}

func videoVals(c config.CustomAPI) customVals {
	return customVals{c.VideoPreset, c.VideoBaseURL, c.VideoKey, c.VideoModel, c.VideoSpec}
}

// presetOf finds a preset (the first one for "" or an unknown id).
func presetOf(list []providers.Preset, id string) providers.Preset {
	for _, p := range list {
		if p.ID == id {
			return p
		}
	}
	if len(list) > 0 {
		return list[0]
	}
	return providers.Preset{}
}

// customPanel draws the panel for images (video false) or video.
func (p *Page) customPanel(gtx C, c *core.Ctx, s *customPanel, video bool, flash func()) D {
	th := c.Th
	cfg := p.now
	list := providers.ImagePresets()
	vals := imageVals(cfg.Custom)
	title, what := "Своє API для кадрів", "кадрів"
	if video {
		list = providers.VideoPresets()
		vals = videoVals(cfg.Custom)
		title, what = "Своє API для відео", "відео"
	}
	pre := presetOf(list, vals.preset)
	universal := pre.ID == providers.PresetUniversal
	set := func(fn func(cu *config.CustomAPI)) {
		p.cfg.set(c, func(cf *config.Config) { fn(&cf.Custom) }, flash)
	}
	if v, ok := s.preset.Changed(gtx); ok {
		set(func(cu *config.CustomAPI) {
			if video {
				cu.VideoPreset, cu.VideoModel = v, ""
			} else {
				cu.ImagePreset, cu.ImageModel = v, ""
			}
		})
		s.check.reset()
	}
	if v, ok := s.modelSel.Changed(gtx); ok {
		if v == otherModel {
			s.other = true
		} else {
			s.other = false
			set(func(cu *config.CustomAPI) {
				if video {
					cu.VideoModel = v
				} else {
					cu.ImageModel = v
				}
			})
			s.model.SetText(v)
		}
	}
	p.modelCatalog(gtx, c, &s.catalog, video, vals, flash, func() {
		// The fields adopt the picked preset, address, model and description.
		s.other = false
		s.base.edited, s.model.edited = false, false
		s.spec.draft, s.spec.err, s.spec.editor.edited = nil, "", false
		s.check.reset()
	})
	if s.docs.Clicked(gtx) && pre.DocsURL != "" {
		c.OpenPath(pre.DocsURL)
	}
	if s.test.Clicked(gtx) {
		if video {
			p.runCheck(c, &s.check, c.E.TestCustomVideo)
		} else {
			p.runCheck(c, &s.check, c.E.TestCustomImage)
		}
	}
	opts := make([]gui.SelectOption, len(list))
	for i, pr := range list {
		opts[i] = gui.SelectOption{Value: pr.ID, Label: pr.Label}
	}
	presetSel := func(gtx C) D {
		return s.preset.Layout(gtx, th, gui.SelectStyle{Label: "Сервіс", Options: opts, Value: pre.ID, Leading: gui.IconServer})
	}
	// One select of the preset's models; «Інша модель…» (or a model that is
	// not in the list) reveals a field for any model id of the API.
	cur := vals.model
	if cur == "" {
		cur = pre.DefaultModel
	}
	inList := false
	for _, m := range pre.Models {
		inList = inList || m == cur
	}
	free := s.other || !inList || len(pre.Models) == 0
	model := labeled(th, "Модель", "", func(gtx C) D {
		s.model.bind(gtx, vals.model, func(v string) {
			v = strings.TrimSpace(v)
			set(func(cu *config.CustomAPI) {
				if video {
					cu.VideoModel = v
				} else {
					cu.ImageModel = v
				}
			})
		})
		var ws []layout.Widget
		if len(pre.Models) > 0 {
			mopts := make([]gui.SelectOption, 0, len(pre.Models)+1)
			for _, m := range pre.Models {
				o := gui.SelectOption{Value: m, Label: m}
				if m == pre.DefaultModel {
					o.Hint = "типова"
				}
				mopts = append(mopts, o)
			}
			mopts = append(mopts, gui.SelectOption{Value: otherModel, Label: "Інша модель…", Icon: gui.IconPencil})
			val := cur
			if free {
				val = otherModel
			}
			ws = append(ws, func(gtx C) D { return s.modelSel.Layout(gtx, th, gui.SelectStyle{Options: mopts, Value: val}) })
		}
		if free {
			st := gui.FieldStyle{Placeholder: pre.DefaultModel, Hint: "Будь-яка модель цього сервісу — як у його документації."}
			if universal {
				st = gui.FieldStyle{Placeholder: "напр. my-model-v2", Hint: "Іде в опис замість {{model}}. Порожньо — без моделі."}
			}
			ws = append(ws, func(gtx C) D { return s.model.Layout(gtx, th, st) })
		}
		return gui.VStack(gtx, gui.Sp8, ws...)
	})
	baseHint := "Порожньо — стандартна адреса сервісу."
	basePH := pre.DefaultBaseURL
	switch {
	case universal:
		baseHint, basePH = "Корінь API: шляхи з опису додаються до неї, напр. https://api.example.com/v1", "https://…"
	case basePH == "":
		baseHint, basePH = "Адреса твого сервера, напр. http://127.0.0.1:8000/v1", "https://…"
	}
	base := textIn(&s.base, th, "Адреса", baseHint, basePH, vals.base, func(v string) {
		v = strings.TrimSpace(v)
		set(func(cu *config.CustomAPI) {
			if video {
				cu.VideoBaseURL = v
			} else {
				cu.ImageBaseURL = v
			}
		})
	})
	key := func(gtx C) D {
		s.key.empty = pre.KeyHint
		return s.key.layout(gtx, th, "API-ключ", vals.key, "Встав ключ", func(v string) {
			set(func(cu *config.CustomAPI) {
				if video {
					cu.VideoKey = v
				} else {
					cu.ImageKey = v
				}
			})
			c.Toast(gui.ToneSuccess, "Ключ збережено")
		})
	}
	caps := catalogCapsLine(pre, vals, video)
	sub := "Твій сервіс " + what + ": OpenAI, fal.ai, Replicate та інші."
	if universal {
		sub = "Будь-яке API " + what + ": опиши його в JSON, візьми шаблон або налаштуй з документації."
	}
	ws := []layout.Widget{
		func(gtx C) D { return insetHead(gtx, th, gui.IconServer, title, sub, s.check.statusPill(th)) },
		catalogRow(th, &s.catalog, video),
		func(gtx C) D { return fieldRow(gtx, 220, presetSel, model) },
		func(gtx C) D { return fieldRow(gtx, 220, base, key) },
	}
	advice := "Перевір ключ, адресу й модель. " + pre.KeyHint + "."
	if universal {
		ws = append(ws, p.specWidgets(c, s, video, flash)...)
		advice = `Перевір ключ, адресу й опис API: «test» має бути запитом, що не генерує (баланс, акаунт, список моделей).`
	}
	ws = append(ws,
		func(gtx C) D {
			if caps == "" {
				return D{}
			}
			return note(gtx, th, gui.IconInfo, caps, nil)
		},
		actions(
			checkButton(th, &s.test, &s.check, "", false),
			func(gtx C) D {
				if pre.DocsURL == "" {
					return D{}
				}
				return gui.Button{Kind: gui.Ghost, Icon: gui.IconExternalLink, Text: "Документація"}.Layout(gtx, th, &s.docs)
			},
		),
		func(gtx C) D { return s.check.result(gtx, th, advice) },
	)
	return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, ws...) })
}

// capsLine says what the preset's model takes: reference images and
// formats for pictures, clip lengths and formats for video (the universal
// preset: what its spec takes).
func capsLine(pre providers.Preset, model, spec string, video bool) string {
	if pre.ID == providers.PresetUniversal {
		caps := providers.SpecCaps(spec)
		if video {
			return "Цей опис знімає: " + videoCapsText(caps.Video) + "."
		}
		return "Цей опис бере: " + imageCapsText(caps.Image) + "."
	}
	if video {
		if t := videoCapsText(providers.VideoCapsFor(pre.ID, model)); t != "" {
			return "Ця модель знімає: " + t + "."
		}
		return ""
	}
	return "Ця модель бере: " + imageCapsText(providers.ImageCapsFor(pre.ID, model)) + "."
}

// videoCapsText: "кліпи 4, 8 с · до 3 опорних кадрів · 16:9, 9:16" ("" = nothing known).
func videoCapsText(vc providers.VideoCaps) string {
	var parts []string
	if len(vc.Lengths) > 0 {
		ls := make([]string, len(vc.Lengths))
		for i, l := range vc.Lengths {
			ls[i] = strconv.Itoa(l)
		}
		parts = append(parts, "кліпи "+strings.Join(ls, ", ")+" с")
	}
	if vc.MaxRefs > 0 {
		parts = append(parts, "до "+strconv.Itoa(vc.MaxRefs)+" опорних кадрів")
	}
	if len(vc.Aspects) > 0 {
		parts = append(parts, strings.Join(vc.Aspects, ", "))
	}
	return strings.Join(parts, " · ")
}

// imageCapsText: "до 10 опорних фото героїв і місць · 16:9, 9:16".
func imageCapsText(ic providers.ImageCaps) string {
	var parts []string
	switch {
	case ic.MaxRefs > 1:
		parts = append(parts, "до "+strconv.Itoa(ic.MaxRefs)+" опорних фото героїв і місць")
	case ic.MaxRefs == 1:
		parts = append(parts, "одне вихідне фото (image-to-image)")
	default:
		parts = append(parts, "без опорних фото — герої можуть змінюватися між кадрами")
	}
	if len(ic.Aspects) > 0 {
		parts = append(parts, strings.Join(ic.Aspects, ", "))
	}
	return strings.Join(parts, " · ")
}
