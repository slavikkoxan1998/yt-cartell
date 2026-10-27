package settings

import (
	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

var stockProviders = []gui.SegOption{
	{Value: "auto", Label: "Авто (всі 4)"},
	{Value: "pexels", Label: "Pexels"},
	{Value: "pixabay", Label: "Pixabay"},
	{Value: "coverr", Label: "Coverr"},
	{Value: "wikimedia", Label: "Wikimedia"},
}

var stockOrientations = []gui.SegOption{
	{Value: "landscape", Label: "16:9 Горизонтальні"},
	{Value: "portrait", Label: "9:16 Shorts / Reels"},
	{Value: "all", Label: "Будь-які"},
}

type stockCard struct {
	enabled     switchRow
	provider    gui.Segmented
	orientation gui.Segmented
	pexelsKey   keyInput
	pixabayKey  keyInput
	coverrKey   keyInput
	saved       savedNote
}

func (p *Page) stockCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.stock
	cfg := p.now
	flash := func() { s.saved.want = true }

	enabled := func(gtx C) D {
		return s.enabled.layout(gtx, th, cfg.Stock.Enabled, "Використовувати безкоштовні стокові відео",
			"Студія шукатиме та підбиратиме футажі за описом сцени з Pexels, Pixabay, Coverr або Wikimedia Commons замість ШІ",
			func(v bool) {
				p.cfg.set(c, func(cf *config.Config) { cf.Stock.Enabled = v }, flash)
			})
	}

	curProvider := cfg.Stock.Provider
	if curProvider == "" {
		curProvider = "auto"
	}
	if v, ok := s.provider.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Stock.Provider = v }, flash)
		curProvider = v
	}
	providerRow := labeled(th, "Пріоритетне джерело стоків", "", func(gtx C) D {
		return s.provider.Layout(gtx, th, curProvider, stockProviders)
	})

	curOrient := cfg.Stock.Orientation
	if curOrient == "" {
		curOrient = "landscape"
	}
	if v, ok := s.orientation.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.Stock.Orientation = v }, flash)
		curOrient = v
	}
	orientRow := labeled(th, "Співвідношення сторін футажів", "", func(gtx C) D {
		return s.orientation.Layout(gtx, th, curOrient, stockOrientations)
	})

	pexelsField := func(gtx C) D {
		return s.pexelsKey.layout(gtx, th, "Pexels API Key", cfg.Stock.PexelsKey, "Встав ключ з pexels.com/api", func(v string) {
			p.cfg.set(c, func(cf *config.Config) { cf.Stock.PexelsKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ Pexels збережено") })
		})
	}

	pixabayField := func(gtx C) D {
		return s.pixabayKey.layout(gtx, th, "Pixabay API Key", cfg.Stock.PixabayKey, "Встав ключ з pixabay.com/api/docs", func(v string) {
			p.cfg.set(c, func(cf *config.Config) { cf.Stock.PixabayKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ Pixabay збережено") })
		})
	}

	coverrField := func(gtx C) D {
		return s.coverrKey.layout(gtx, th, "Coverr API Key (опціонально)", cfg.Stock.CoverrKey, "Встав ключ з coverr.co (або залиш пустим)", func(v string) {
			p.cfg.set(c, func(cf *config.Config) { cf.Stock.CoverrKey = v }, func() { flash(); c.Toast(gui.ToneSuccess, "Ключ Coverr збережено") })
		})
	}

	body := []layout.Widget{
		enabled,
		providerRow,
		orientRow,
		func(gtx C) D { return fieldRow(gtx, 240, pexelsField, pixabayField) },
		coverrField,
		func(gtx C) D {
			hint := "Wikimedia Commons працює автоматично без ключів. Для Pexels і Pixabay зареєструйся та встав безкоштовні ключі."
			return softWarning(gtx, th, hint)
		},
	}

	return card(gtx, th, m.narrow, head{
		Icon: gui.IconFilm, Title: "Стокові відео (Pexels, Pixabay, Coverr, Wikimedia)",
		Text:  "Автоматичний пошук та підбір безкоштовних HD-футажів за промтами замість генерації ШІ-відео.",
		Trail: trails(s.saved.widget(th)),
	}, body...)
}
