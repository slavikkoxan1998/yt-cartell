package settings

// The universal preset of «Своє API» («Універсальний (будь-яке API)»): the
// JSON description of the API (config.CustomAPI.ImageSpec / VideoSpec,
// providers.Spec) in an editor with live validation, ready templates
// («Шаблон…») and «Налаштувати з документації (ШІ)» — the text model reads
// the API's docs and drafts the description (Engine.SuggestSpec). A draft is
// never saved by itself: it waits in the editor until «Зберегти опис». The
// same panel describes an own voice-over or speech-recognition API
// («Власний сервер» of Озвучка, «Своє API» of Розпізнавання мови;
// config.CustomAPI.TTSSpec / STTSpec, audiospec.go): a specHost says which.

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/studio"
)

// specPanel is the state of the universal block of one customPanel.
type specPanel struct {
	tpl    gui.Select
	editor field // the spec; commits on blur like the other fields (not while it holds a draft)
	docs   field // the docs link or text (never saved)

	run, stop, save, discard gui.Clickable

	// The AI setup.
	busy   bool
	step   string
	cancel context.CancelFunc
	seq    int
	err    string
	draft  *studio.CustomSpecSuggestion // shown in the editor, not saved yet
	show   gui.Anim

	// Validation of the editor's text (memo).
	memo struct {
		text, sum string
		kind      providers.SpecKind
		warns     []string
		err       error
		ok        bool
	}
}

// specHost is what one spec panel describes: the kind of API, where the
// spec, address and model live, and how they are saved.
type specHost struct {
	kind        providers.SpecKind
	sp          *specPanel
	check       *check // the card's check (reset after a save)
	stored      string // the saved spec
	base, model string // the address and model fields now
	save        func(v, base, model string)
}

func kindOf(video bool) providers.SpecKind {
	if video {
		return providers.KindVideo
	}
	return providers.KindImage
}

// check validates text (memoised: it runs every frame).
func (s *specPanel) check(text string, video bool) (string, []string, error) {
	return s.checkKind(text, kindOf(video))
}

func (s *specPanel) checkKind(text string, kind providers.SpecKind) (string, []string, error) {
	m := &s.memo
	if !m.ok || m.text != text || m.kind != kind {
		m.text, m.kind, m.ok = text, kind, true
		m.sum, m.warns, m.err = providers.CheckSpecFor(text, kind)
	}
	return m.sum, m.warns, m.err
}

func specOf(cu config.CustomAPI, video bool) string {
	if video {
		return cu.VideoSpec
	}
	return cu.ImageSpec
}

// specWidgets are the universal blocks of the panel: the AI setup and the
// description (templates, editor, validation, placeholders).
func (p *Page) specWidgets(c *core.Ctx, s *customPanel, video bool, flash func()) []layout.Widget {
	stored := specOf(p.now.Custom, video)
	saveSpec := func(v string, base, model string) {
		p.cfg.set(c, func(cf *config.Config) {
			if video {
				cf.Custom.VideoSpec = v
				if base != "" {
					cf.Custom.VideoBaseURL = base
				}
				if model != "" {
					cf.Custom.VideoModel = model
				}
				fitLength(cf)
			} else {
				cf.Custom.ImageSpec = v
				if base != "" {
					cf.Custom.ImageBaseURL = base
				}
				if model != "" {
					cf.Custom.ImageModel = model
				}
			}
		}, flash)
		s.check.reset()
	}
	cu := p.now.Custom
	h := &specHost{kind: kindOf(video), sp: &s.spec, check: &s.check, stored: stored, base: cu.ImageBaseURL, model: cu.ImageModel, save: saveSpec}
	if video {
		h.base, h.model = cu.VideoBaseURL, cu.VideoModel
	}
	return p.specBlocks(c, h)
}

// specBlocks are the AI setup and the description of any spec host.
func (p *Page) specBlocks(c *core.Ctx, h *specHost) []layout.Widget {
	return []layout.Widget{
		func(gtx C) D { return p.specAI(gtx, c, h) },
		func(gtx C) D { return p.specEditor(gtx, c, h) },
	}
}

// ---- the description -------------------------------------------------------------------------

func (p *Page) specEditor(gtx C, c *core.Ctx, h *specHost) D {
	th := c.Th
	sp := h.sp
	stored, saveSpec := h.stored, h.save
	if v, ok := sp.tpl.Changed(gtx); ok {
		if t, found := providers.FindSpecTemplateFor(h.kind, v); found {
			apply := func() {
				sp.draft, sp.err = nil, ""
				sp.editor.edited = false
				sp.editor.SetText(t.Spec)
				stored = t.Spec // this frame's bind below must not put the old text back
				saveSpec(t.Spec, t.BaseURL, t.Model)
				c.Toast(gui.ToneSuccess, "Шаблон «"+t.Label+"» застосовано — заміни поля під своє API")
			}
			cur := strings.TrimSpace(sp.editor.Editor.Text())
			if cur == "" || isTemplateKind(h.kind, cur) {
				apply()
			} else {
				c.Confirm(core.Confirm{
					Title: "Замінити опис шаблоном?", Icon: gui.IconFileText, OK: "Замінити",
					Text: "Поточний опис API зникне, а адреса й модель стануть як у шаблоні «" + t.Label + "».",
					OnOK: apply,
				})
			}
		}
	}
	if sp.draft != nil {
		// A draft is reviewed, not saved on blur: only drain the edits.
		sp.editor.commit = nil
		sp.editor.Changed(gtx)
	} else {
		sp.editor.bind(gtx, stored, func(v string) { saveSpec(strings.TrimSpace(v), "", "") })
	}
	text := sp.editor.Editor.Text()

	opts := make([]gui.SelectOption, 0, 6)
	for _, t := range providers.SpecTemplatesFor(h.kind) {
		opts = append(opts, gui.SelectOption{Value: t.ID, Label: t.Label})
	}
	tplSel := func(gtx C) D {
		gtx.Constraints.Min.X = 0
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(300))
		return sp.tpl.Layout(gtx, th, gui.SelectStyle{Options: opts, Placeholder: "Шаблон…", Leading: gui.IconLayers})
	}
	title := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.Label}.Widget(th, "Опис API (JSON)")),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, specSubtitle(h.kind))),
		)
	}
	head := func(gtx C) D {
		if gtx.Constraints.Max.X < gtx.Dp(520) {
			return gui.VStack(gtx, gui.Sp8, title, tplSel)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, title),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(tplSel),
		)
	}
	editor := func(gtx C) D {
		ph := "Обери шаблон або склади опис з документації вище — він з'явиться тут. Або встав свій JSON."
		return sp.editor.Layout(gtx, th, gui.FieldStyle{MinLines: 8, MaxLines: 22, Placeholder: ph,
			Style: th.Small.Sized(13).Lined(1.45)})
	}
	ws := []layout.Widget{head}
	if sp.draft != nil {
		ws = append(ws, func(gtx C) D { return p.draftReview(gtx, c, h) })
	}
	ws = append(ws, editor,
		func(gtx C) D { return sp.status(gtx, th, text, h.kind) },
		func(gtx C) D { return placeholdersHelp(gtx, th, h.kind) },
	)
	return gui.VStack(gtx, gui.Sp12, ws...)
}

// specSubtitle is the line under «Опис API (JSON)».
func specSubtitle(kind providers.SpecKind) string {
	switch kind {
	case providers.KindTTS:
		return "Як надіслати текст на озвучку і де взяти аудіо."
	case providers.KindSTT:
		return "Як надіслати запис і де у відповіді слова з таймкодами."
	}
	return "Як запустити генерацію, де статус і де файл."
}

// isTemplate: text is one of the templates unchanged (nothing to lose).
func isTemplate(video bool, text string) bool { return isTemplateKind(kindOf(video), text) }

func isTemplateKind(kind providers.SpecKind, text string) bool {
	for _, t := range providers.SpecTemplatesFor(kind) {
		if strings.TrimSpace(t.Spec) == text {
			return true
		}
	}
	return false
}

// status is the live validation of the editor's text: what the API does, or
// what is wrong (field: what), plus warnings.
func (sp *specPanel) status(gtx C, th *gui.Theme, text string, kind providers.SpecKind) D {
	if strings.TrimSpace(text) == "" {
		return D{}
	}
	sum, warns, err := sp.checkKind(text, kind)
	icon, ink, title := gui.IconCheckCircle, th.Pal.SuccessInk, "Опис коректний"
	var lines []string
	if err != nil {
		icon, ink, title = gui.IconCircleAlert, th.Pal.ErrorInk, "Опис має помилки"
		var se *providers.SpecError
		if errors.As(err, &se) {
			lines = se.Problems
		} else {
			lines = []string{err.Error()}
		}
	} else {
		lines = []string{sum}
	}
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: 1, Right: gui.Sp8}.Layout(gtx, icon.Widget(18, ink))
		}),
		layout.Flexed(1, func(gtx C) D {
			ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontSemiBold), Color: ink}.Widget(th, title))}
			for _, l := range lines {
				ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text}.Widget(th, bullet(len(lines), l))))
			}
			for _, w := range warns {
				ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.WarningInk}.Widget(th, "Увага: "+w+".")))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}),
	)
}

func bullet(n int, s string) string {
	if n > 1 {
		return "• " + s
	}
	return s
}

// placeholdersHelp is the short reference under the editor.
func placeholdersHelp(gtx C, th *gui.Theme, kind providers.SpecKind) D {
	mod := " ({{seconds|text}} — як текст)"
	switch kind {
	case providers.KindTTS:
		mod = " ({{speed|text}} — як текст)"
	case providers.KindSTT:
		mod = " ({{audio}} з multipart — файлом)"
	}
	text := "Підстановки: " + strings.Join(providers.SpecPlaceholdersFor(kind), " ") +
		mod + ". У шляхах після запуску: {id}, {{submit.поле}}, {{status.поле}}. " +
		"Ключ сюди не пиши: його додає поле \"auth\" (bearer, header:Назва, query:назва, none)."
	return note(gtx, th, gui.IconInfo, text, nil)
}

// ---- «Налаштувати з документації (ШІ)» -------------------------------------------------------

func (p *Page) specAI(gtx C, c *core.Ctx, h *specHost) D {
	th := c.Th
	sp := h.sp
	if _, ok := sp.docs.Changed(gtx); ok {
		sp.err = ""
	}
	if sp.run.Clicked(gtx) {
		p.suggestSpec(c, h)
	}
	if sp.stop.Clicked(gtx) && sp.cancel != nil {
		sp.cancel()
		sp.step = "Скасовуємо…"
	}
	head := func(gtx C) D {
		return tileHead(gtx, th, gui.IconWandSparkles, "Налаштувати з документації (ШІ)",
			"Встав посилання на документацію API або її текст — текстова модель студії складе опис. Ключ їй не передається.")
	}
	docs := func(gtx C) D {
		return sp.docs.Layout(gtx, th, gui.FieldStyle{MinLines: 2, MaxLines: 5, Leading: gui.IconLink,
			Placeholder: "Посилання на документацію або її текст", Disabled: sp.busy})
	}
	act := func(gtx C) D {
		if sp.busy {
			step := func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Top: 1}.Layout(gtx, gui.Spinner{Size: 18, Color: th.Pal.RunningInk}.Widget(th))
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text, MaxLines: 3}.Widget(th, firstNonEmptyStr(sp.step, "Працюємо…"))),
				)
			}
			stop := func(gtx C) D {
				gtx.Constraints.Min.X = 0
				return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconX, Text: "Скасувати"}.Layout(gtx, th, &sp.stop)
			}
			if gtx.Constraints.Max.X < gtx.Dp(420) {
				return gui.VStack(gtx, gui.Sp8, step, stop)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, step),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(stop),
			)
		}
		return gui.Button{Kind: gui.Secondary, Icon: gui.IconWandSparkles, Text: "Скласти опис"}.Layout(gtx, th, &sp.run)
	}
	ws := []layout.Widget{head, docs, act}
	if sp.err != "" {
		ws = append(ws, func(gtx C) D { return resultPanel(gtx, th, false, false, sp.err, "", "") })
	}
	return flatTile(gtx, th, ws...)
}

// tileHead is the title row of a flatTile: the icon on a surface square, a
// title (up to two lines on phones) and a line of text.
func tileHead(gtx C, th *gui.Theme, icon *gui.Icon, title, text string) D {
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			s := gtx.Dp(36)
			gui.FillRRect(gtx, image.Rect(0, 0, s, s), gui.RadiusSmall+2, th.Pal.Surface)
			is := gtx.Dp(18)
			o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
			icon.Layout(gtx, 18, th.Pal.Text)
			o.Pop()
			return D{Size: image.Pt(s, s)}
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), MaxLines: 2}.Widget(th, title)),
				layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, text)),
			)
		}),
	)
}

// flatTile is a card-coloured block inside the panel's inset (tighter on phones).
func flatTile(gtx C, th *gui.Theme, ws ...layout.Widget) D {
	pad := gtx.Dp(gui.Sp16)
	if gtx.Constraints.Max.X < gtx.Dp(360) {
		pad = gtx.Dp(gui.Sp12)
	}
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(max(0, W-2*pad), 0), Max: image.Pt(max(0, W-2*pad), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D { return gui.VStack(gtx, gui.Sp12, ws...) })
	size := image.Pt(W, d.Size.Y+2*pad)
	gui.Box(gtx, image.Rectangle{Max: size}, gui.RadiusControl, th.Pal.Card, th.Pal.Border)
	o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// suggestSpec runs the AI setup in the background (progress, cancel).
func (p *Page) suggestSpec(c *core.Ctx, h *specHost) {
	sp := h.sp
	kind := h.kind
	if sp.busy {
		return
	}
	docs := strings.TrimSpace(sp.docs.Editor.Text())
	switch {
	case docs == "":
		sp.err = "Встав посилання на документацію API (напр. https://api.example.com/docs) або сам її текст."
		return
	case c.Offline:
		sp.err = "Налаштування з документації недоступне в демо-режимі."
		return
	}
	p.flushFields()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	sp.busy, sp.err, sp.step, sp.cancel = true, "", "Починаємо…", cancel
	sp.seq++
	seq := sp.seq
	p.cfg.barrier(c, func() { // the model field the user just typed goes with the request
		c.Background(func() {
			defer cancel()
			res, err := c.E.SuggestSpec(ctx, kind, docs, func(msg string) {
				c.Post(func() {
					if seq == sp.seq && sp.busy {
						sp.step = msg
					}
				})
			})
			c.Post(func() {
				if seq != sp.seq {
					return
				}
				sp.busy, sp.cancel, sp.step = false, nil, ""
				switch {
				case errors.Is(err, context.Canceled):
					c.Toast(gui.ToneNeutral, "Налаштування скасовано")
				case errors.Is(err, context.DeadlineExceeded):
					sp.err = "ШІ не встиг за 8 хвилин — спробуй коротший текст документації (лише розділ про генерацію)."
				case err != nil:
					sp.err = core.ErrorText(err)
				default:
					sp.draft = &res
					sp.editor.edited = false
					sp.editor.commit = nil
					sp.editor.SetText(res.Spec)
					sp.show.Set(0)
				}
			})
		})
	})
}

// draftReview is the AI's draft above the editor: what it found, what it
// could not determine, «Зберегти опис» / «Відкинути».
func (p *Page) draftReview(gtx C, c *core.Ctx, h *specHost) D {
	th := c.Th
	sp := h.sp
	d := sp.draft
	saveSpec := h.save
	curBase, curModel := h.base, h.model
	// The address and the model fill only empty fields.
	base, model := "", ""
	if d.BaseURL != "" && strings.TrimSpace(curBase) == "" {
		base = d.BaseURL
	}
	if d.Model != "" && strings.TrimSpace(curModel) == "" {
		model = d.Model
	}
	if sp.save.Clicked(gtx) {
		text := strings.TrimSpace(sp.editor.Editor.Text())
		sp.draft = nil
		sp.editor.edited = false
		saveSpec(text, base, model)
		c.Toast(gui.ToneSuccess, "Опис API збережено — натисни «Перевірити»")
		return D{}
	}
	if sp.discard.Clicked(gtx) {
		sp.draft = nil
		sp.editor.edited = false
		sp.editor.SetText(h.stored)
		return D{}
	}
	var facts []string
	switch {
	case base != "":
		facts = append(facts, "Адреса API: "+base+" — запишу в поле «Адреса».")
	case d.BaseURL != "" && strings.TrimRight(strings.TrimSpace(curBase), "/") != d.BaseURL:
		facts = append(facts, "У документації адреса "+d.BaseURL+" — у тебе вказана інша, лишаю твою.")
	case d.BaseURL == "" && strings.TrimSpace(curBase) == "":
		facts = append(facts, "Адресу API в документації не знайдено — впиши її в поле «Адреса».")
	}
	switch {
	case model != "":
		facts = append(facts, "Модель: "+model+" — запишу в поле «Модель».")
	case d.Model != "" && strings.TrimSpace(curModel) != d.Model:
		facts = append(facts, "ШІ радить модель "+d.Model+" — у тебе вказана "+curModel+".")
	}
	sp.show.Duration = gui.DurDialog
	a := sp.show.Animate(gtx, 1)
	return gui.FadeUp(gtx, a, 6, func(gtx C) D {
		pad := gtx.Dp(gui.Sp12 + 2)
		w := gtx.Constraints.Max.X
		cg := gtx
		cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad), gtx.Constraints.Max.Y)}
		dd, call := record(cg, func(gtx C) D {
			sub := "Опис нижче складено з документації — перевір його і збережи. Доти нічого не змінено."
			if len(d.Problems) > 0 {
				sub = "ШІ не впорався з усім — виправ помилки в описі нижче, потім збережи."
			}
			ch := []layout.FlexChild{
				layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.RunningInk}.Widget(th, "Чернетка від ШІ")),
				layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, sub)),
			}
			row := func(text string, col color.NRGBA) {
				ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							sz := gtx.Dp(6)
							o := op.Offset(image.Pt(0, gtx.Dp(7))).Push(gtx.Ops)
							defer o.Pop()
							gui.FillRRect(gtx, image.Rect(0, 0, sz, sz), gui.RadiusPill, col)
							return D{Size: image.Pt(sz, sz+gtx.Dp(7))}
						}),
						layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
						layout.Flexed(1, gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: th.Pal.Text}.Widget(th, text)),
					)
				}))
			}
			for _, f := range facts {
				row(f, th.Pal.Running)
			}
			for _, n := range d.Notes {
				row(n, th.Pal.Warning)
			}
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout), layout.Rigid(func(gtx C) D {
				return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
					func(gtx C) D {
						return gui.Button{Kind: gui.Primary, Size: gui.Sm, Icon: gui.IconSave, Text: "Зберегти опис"}.Layout(gtx, th, &sp.save)
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconX, Text: "Відкинути"}.Layout(gtx, th, &sp.discard)
					},
				)
			}))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		})
		h := dd.Size.Y + 2*pad
		gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.RunningSoft)
		o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(w, h)}
	})
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
