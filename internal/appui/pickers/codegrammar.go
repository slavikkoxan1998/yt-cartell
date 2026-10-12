package pickers

import (
	"context"
	"image"
	"sync"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

// ---- grammar previews ---------------------------------------------------------------------

// grammarPreview is the state of one grammar's example still.
type grammarPreview struct {
	path    string
	err     error
	loading bool
}

var (
	gpMu    sync.Mutex
	gpByKey = map[string]*grammarPreview{}
)

// GrammarThumb is the still of a grammar's own example in the project's
// format (rendered once in the background by the engine and kept in the
// data folder): ok is false while it renders; err says why there is none.
func GrammarThumb(c *core.Ctx, id, aspect string, px int) (img paint.ImageOp, ok bool, err error) {
	if c == nil || c.E == nil || c.Img == nil || id == "" {
		return paint.ImageOp{}, false, nil
	}
	key := id + "|" + aspect
	gpMu.Lock()
	st := gpByKey[key]
	if st == nil {
		st = &grammarPreview{loading: true}
		gpByKey[key] = st
		gpMu.Unlock()
		c.Background(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			path, err := c.E.GrammarPreview(ctx, id, aspect)
			gpMu.Lock()
			st.path, st.err, st.loading = path, err, false
			gpMu.Unlock()
			c.Invalidate()
		})
		return paint.ImageOp{}, false, nil
	}
	path, perr, loading := st.path, st.err, st.loading
	gpMu.Unlock()
	if loading {
		return paint.ImageOp{}, false, nil
	}
	if perr != nil {
		return paint.ImageOp{}, false, perr
	}
	img, _, ok = c.Img.Get(path, 0, px)
	return img, ok, nil
}

// ---- the field ------------------------------------------------------------------------------

// CodeGrammarLabel is the name of a grammar choice ("" = auto).
func CodeGrammarLabel(c *core.Ctx, id string) string {
	if id == "" {
		return "Авто — під зміст сцени"
	}
	if c != nil && c.E != nil {
		if g, ok := c.E.CodeGrammarByID(id); ok {
			return g.Label
		}
	}
	return id
}

// CodeGrammar is the «Граматика анімації» field of a «Анімація кодом» film:
// auto (the text model picks per scene) or one of the renderer's grammars,
// each shown with a still of its own example in the film's format.
type CodeGrammar struct {
	pick[string]
	Look
	// Aspect is the film's format ("16:9" | "9:16") of the previews.
	Aspect string
	// AutoLabel replaces the auto option's name (a scene: «Як у фільмі»).
	AutoLabel string

	btn  gui.Clickable
	chev gui.Anim
	fx   flatFX
	pop  panel
	rows []gui.Clickable
	list layout.List
}

// Layout draws the field.
func (s *CodeGrammar) Layout(gtx C, c *core.Ctx, label, value string) D {
	th := c.Th
	gs := c.E.CodeGrammars()
	if len(s.rows) != len(gs)+1 {
		s.rows = make([]gui.Clickable, len(gs)+1)
	}
	value = s.shown(gtx, value)
	for i := range s.rows {
		if s.rows[i].Clicked(gtx) {
			v := ""
			if i > 0 {
				v = gs[i-1].ID
			}
			s.set(gtx, v)
			value = v
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
	auto := s.AutoLabel
	if auto == "" {
		auto = CodeGrammarLabel(c, "")
	}
	name, caption := auto, "ШІ обирає графік, мапу, текст чи схему для кожної сцени"
	visual := visIcon(th, gui.IconWandSparkles, s.Flat)
	if value != "" {
		name, caption = CodeGrammarLabel(c, value), "кожна сцена цією граматикою"
		if g, ok := c.E.CodeGrammarByID(value); ok && g.Description != "" {
			caption = g.Description
		}
		if img, ok, _ := GrammarThumb(c, value, s.Aspect, 240); ok {
			visual = visThumb(th, gui.Thumb{Image: img, Fit: gui.FitCover})
		} else {
			visual = visIcon(th, gui.IconChartNoAxesColumn, s.Flat)
		}
	}
	if s.Note != "" {
		caption = s.Note
	}
	t := tile{Label: label, Value: name, Caption: caption, Visual: visual, Open: s.pop.open, Flat: s.Flat,
		Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := t.layout(gtx, th, &s.btn, &s.chev, &s.fx)
	popover(gtx, th, &s.pop, d, 560, func(gtx C) D {
		return layout.UniformInset(gui.Sp16).Layout(gtx, func(gtx C) D {
			gtx.Constraints.Max.Y = min(gtx.Constraints.Max.Y, gtx.Dp(620))
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold)}.Widget(th, "Як малюємо анімацію")),
				layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
					"Анімацію малює код: дані, слова й схеми з'являються точно на словах диктора. Персонажі — окремими картинками.")),
				layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
				layout.Flexed(1, func(gtx C) D {
					s.list.Axis = layout.Vertical
					return s.list.Layout(gtx, len(gs)+1, func(gtx C, i int) D {
						if i == 0 {
							return grammarRow(gtx, c, &s.rows[0], "", auto, "ШІ сам обирає граматику для кожної сцени за її змістом", s.Aspect, value == "")
						}
						g := gs[i-1]
						return grammarRow(gtx, c, &s.rows[i], g.ID, g.Label, g.Description, s.Aspect, value == g.ID)
					})
				}),
			)
		})
	})
	return d
}

// Open opens the panel (as if the field was pressed).
func (s *CodeGrammar) Open() { s.pop.openNow() }

// Close closes the panel.
func (s *CodeGrammar) Close() { s.pop.open = false }

// grammarRow is one grammar: its example still, the name and what it shows.
func grammarRow(gtx C, c *core.Ctx, click *gui.Clickable, id, name, desc, aspect string, on bool) D {
	th := c.Th
	w := gtx.Constraints.Max.X
	h := gtx.Dp(76)
	size := image.Pt(w, h)
	click.Clickable.Layout(gtx, func(gtx C) D {
		defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
		pointer.CursorPointer.Add(gtx.Ops)
		return D{Size: size}
	})
	if click.Hovered() || on {
		gui.FillRRect(gtx, image.Rect(0, 1, w, h-1), 12, th.Pal.Surface)
	}
	pad := gtx.Dp(8)
	tw, thh := gtx.Dp(104), gtx.Dp(58)
	if aspect == "9:16" {
		tw = gtx.Dp(34)
	}
	r := image.Rect(pad, (h-thh)/2, pad+tw, (h-thh)/2+thh)
	func() {
		defer op.Offset(r.Min).Push(gtx.Ops).Pop()
		g := gtx
		g.Constraints = layout.Exact(r.Size())
		if id == "" {
			gui.FillRRect(gtx, image.Rectangle{Max: r.Size()}, 8, th.Pal.Surface)
			ic := gtx.Dp(22)
			defer op.Offset(image.Pt((r.Dx()-ic)/2, (r.Dy()-ic)/2)).Push(gtx.Ops).Pop()
			gui.IconWandSparkles.LayoutStroke(gtx, 22, 1.75, th.Pal.Text2)
			return
		}
		img, ok, err := GrammarThumb(c, id, aspect, 240)
		gui.Thumb{Image: img, Fit: gui.FitCover, Radius: 8, Loading: !ok && err == nil, Placeholder: gui.IconChartNoAxesColumn}.Layout(g, th)
	}()
	x := pad + max(tw, gtx.Dp(104)) + gtx.Dp(12)
	right := w - pad
	if on {
		ic := gtx.Dp(18)
		t := op.Offset(image.Pt(right-ic, (h-ic)/2)).Push(gtx.Ops)
		gui.IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.ActionText)
		t.Pop()
		right -= ic + gtx.Dp(8)
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(max(0, right-x), h)}
	rec := op.Record(gtx.Ops)
	d := layout.Flex{Axis: layout.Vertical}.Layout(lg,
		layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium).Lined(1.3), MaxLines: 1}.Widget(th, name)),
		layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, desc)),
	)
	call := rec.Stop()
	t := op.Offset(image.Pt(x, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return D{Size: size}
}
