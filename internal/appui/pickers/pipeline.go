package pickers

import (
	"gioui.org/io/key"
	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// PipelineModes are the production modes (studio.Input.Pipeline) in display order.
var PipelineModes = []ModeInfo{
	{studio.PipelineFull, studio.PipelineLabel(studio.PipelineFull), "референси, кадри, відео й монтаж", gui.IconFilm},
	{studio.PipelineNoRefs, studio.PipelineLabel(studio.PipelineNoRefs), "без портретів героїв і місць — вигляд словами", gui.IconZap},
	{studio.PipelineStills, studio.PipelineLabel(studio.PipelineStills), "без відео: кадри оживають рухом камери", gui.IconImages},
	{studio.PipelineNoRefsStills, studio.PipelineLabel(studio.PipelineNoRefsStills), "найшвидше й найдешевше: лише кадри", gui.IconGauge},
	{studio.PipelineCode, studio.PipelineLabel(studio.PipelineCode), "графіки, мапи, текст, схеми — малює код, без відео", gui.IconChartNoAxesColumn},
	{studio.PipelineNoRefsCode, studio.PipelineLabel(studio.PipelineNoRefsCode), "анімація кодом, персонажі без портретів", gui.IconShapes},
}

// PipelineHelp explains the production modes (the panel's foot note).
const PipelineHelp = "Повний: студія малює героїв і місця, кадри й знімає відео. «Без референсів»: героїв і місця не малюють окремо — їхній вигляд описано в кожному промті (стабільність трохи нижча). " +
	"«Тільки картинки»: відео не знімається — кожна сцена це її кадр із рухом камери (наближення, панорама, Кен Бернс, дрейф, легке тремтіння), " +
	"завдовжки як її озвучка; рух можна змінити на кроці «Монтаж». " +
	"«Анімація кодом»: анімація малюється кодом — графіки, мапи, текст, схеми — без генерації відео; персонажі — картинками. " +
	"Спершу озвучка, потім ШІ пише анімацію кожної сцени точно під слова диктора, а студія рендерить її на цьому комп'ютері."

// Pipeline is the «Режим виробництва» field: which stages the film skips.
// Cost, when set, is the line under the field and each option ("≈ 8 кадрів
// · 0 кліпів · ~5 хв") — what the mode generates.
type Pipeline struct {
	pick[string]
	Look
	Cost func(v string) string

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  panel
	rows [6]gui.Clickable
}

// Layout draws the field.
func (s *Pipeline) Layout(gtx C, c *core.Ctx, label, value string) D {
	th := c.Th
	value = s.shown(gtx, value)
	for i := range s.rows {
		if s.rows[i].Clicked(gtx) {
			s.set(gtx, PipelineModes[i].Value)
			value = PipelineModes[i].Value
			s.pop.hide(gtx, &s.btn)
		}
	}
	for _, k := range s.pop.keyPresses(gtx) {
		if k == key.NameEscape || k == key.NameReturn || k == key.NameEnter {
			s.pop.hide(gtx, &s.btn)
		}
	}
	if s.pop.takeOutside() {
		s.pop.hide(gtx, &s.btn)
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		s.pop.toggle(gtx, &s.btn)
	}
	cur := ModeByValue(PipelineModes, value)
	caption := cur.Caption
	if s.Cost != nil {
		if t := s.Cost(cur.Value); t != "" {
			caption = t
		}
	}
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: cur.Label, Caption: caption, Visual: visIcon(th, cur.Icon, s.Flat), Open: s.pop.open, Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	popover(gtx, th, &s.pop, d, 440, func(gtx C) D {
		ws := []layout.Widget{gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Що робить студія")}
		for i, m := range PipelineModes {
			row := m
			if s.Cost != nil {
				if t := s.Cost(m.Value); t != "" {
					row.Caption = t
				}
			}
			ws = append(ws, modeRow(th, &s.rows[i], row, m.Value == cur.Value))
		}
		ws = append(ws, gui.Divider{}.Widget(th), gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, PipelineHelp))
		return layout.UniformInset(gui.Sp16).Layout(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, ws...) })
	})
	return d
}

// Open opens the panel (as if the field was pressed).
func (s *Pipeline) Open() { s.pop.openNow() }

// Close closes the panel.
func (s *Pipeline) Close() { s.pop.open = false }
