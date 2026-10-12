package produce

// Step 3 of a «Анімація кодом» film — «Анімація»: no frames; the voices are
// made first, then the text model writes every scene's animation (a
// codeanim spec timed to the narration). Each scene card shows the spec in
// a friendly editor (the cues: time, text, caption) with the raw JSON one
// switch away and live validation, the scene's own grammar, the pictures
// the spec uses and «Попередній перегляд кадру» (a still rendered here).
// «Зберегти й перерендерити» stores the spec and renders the clip again; a
// scene whose animation failed says exactly why and shows that it will be
// its frame instead.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// codeView is the code-animation step for every project.
type codeView struct {
	projects map[string]*codeState
}

type codeState struct {
	list   pageList
	cards  map[string]*codeCard
	prep   gui.Clickable
	render gui.Clickable
	ring   gui.Anim
	toPlan gui.Clickable
	side   sideState
}

// codeCard is one scene's editor.
type codeCard struct {
	grammar  pickers.CodeGrammar
	img      gui.ImageView
	mode     gui.Segmented // "fields" | "json"
	lastMode string
	json     gui.TextField
	at       []gui.TextField
	text     []gui.TextField
	sub      []gui.TextField

	draft    json.RawMessage // the spec being edited ("" = the stored one)
	base     string          // the stored spec the draft started from
	problems string          // validation of the draft
	checked  string          // the draft the problems belong to

	save, rewrite, preview, retry, reset gui.Clickable
}

func newCodeView() *codeView { return &codeView{projects: map[string]*codeState{}} }

func (v *codeView) state(id string) *codeState {
	st := v.projects[id]
	if st == nil {
		st = &codeState{cards: map[string]*codeCard{}}
		v.projects[id] = st
	}
	return st
}

func (st *codeState) card(id string) *codeCard {
	cc := st.cards[id]
	if cc == nil {
		cc = &codeCard{}
		st.cards[id] = cc
	}
	return cc
}

// ---- counts ----------------------------------------------------------------------------------

type codeStats struct {
	scenes, voiced, written, ready, fallback, busy, errs int
}

func countCode(p *studio.Project) codeStats {
	n := codeStats{scenes: len(p.Scenes)}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if s.Audio.Status == studio.StatusDone || strings.TrimSpace(s.Narration) == "" {
			n.voiced++
		}
		switch studio.CodeSceneState(s) {
		case "fallback":
			n.fallback++
		case "running":
			n.busy++
		case "error":
			n.errs++
		case "ready":
			n.ready++
		}
		if len(s.Code.Spec) > 0 {
			n.written++
		}
	}
	return n
}

// ---- layout ------------------------------------------------------------------------------------

func (v *codeView) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := v.state(pv.ID)
	p := &pv.P
	if len(p.Scenes) == 0 {
		if st.toPlan.Clicked(gtx) {
			goStep(c, pv.ID, 2)
		}
		return emptyColumn(gtx, c, pv, &st.toPlan, gui.IconChartNoAxesColumn,
			"Анімації з'являться після плану",
			"У режимі «Анімація кодом» кожна сцена — це анімація, яку малює код: графіки, мапи, текст, схеми. Спершу створи план на кроці «План».",
			[]howStep{
				{gui.IconMic, "Спершу озвучка", "Анімація триває рівно як голос сцени, тож спершу студія озвучує текст."},
				{gui.IconWandSparkles, "ШІ пише анімацію", "Для кожної сцени — опис: що з'являється і на якому слові диктора."},
				{gui.IconFilm, "Рендер тут", "Студія малює кліпи на цьому комп'ютері — без генерації відео."},
			})
	}
	n := countCode(p)
	inset := gtx.Dp(bleed)
	els := []layout.Widget{func(gtx C) D {
		return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp32}.Layout(gtx, func(gtx C) D { return v.header(gtx, c, pv, st, n) })
	}}
	for i := range p.Scenes {
		els = append(els, func(gtx C) D {
			return layout.Inset{Bottom: gui.Sp24}.Layout(gtx, func(gtx C) D { return v.sceneCard(gtx, c, pv, st, i) })
		})
	}
	u := c.UI(pv.ID)
	if id := u.ScrollTo; id != "" {
		u.ScrollTo = ""
		if _, i := pv.Scene(id); i >= 0 {
			st.list.ScrollTo(gtx, 1+i, gtx.Dp(gui.Sp8))
			u.Scene = id
		}
	}
	return st.list.Layout(gtx, th, len(els), inset, func(gtx C, i int) D { return els[i](gtx) })
}

func (v *codeView) header(gtx C, c *core.Ctx, pv *core.ProjectView, st *codeState, n codeStats) D {
	th := c.Th
	p := &pv.P
	running := pv.Running()
	kind := pv.RunningKind()
	if st.prep.Clicked(gtx) {
		c.Run(pv.ID, studio.StageFrames, false, "")
	}
	if st.render.Clicked(gtx) {
		c.Run(pv.ID, studio.StageRender, false, "")
	}
	left := func(gtx C) D {
		frac := float32(0)
		if n.scenes > 0 {
			frac = float32(n.written) / float32(n.scenes)
		}
		tone := gui.ToneNeutral
		if n.written == n.scenes {
			tone = gui.ToneSuccess
		}
		line := fmt.Sprintf("Озвучено %d з %d · написано %d · відрендерено %d", n.voiced, n.scenes, n.written, n.ready)
		if n.fallback > 0 {
			line += fmt.Sprintf(" · з кадру замість анімації: %d", n.fallback)
		}
		grammar := "Граматика: " + pickers.CodeGrammarLabel(c, p.Input.CodeGrammar)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.ProgressRing{Value: frac, Size: 64, Tone: tone, Anim: &st.ring}.Widget(th)),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(label(th, th.H3, th.Pal.Text, 2, fmt.Sprintf("Анімації: %d з %d написано", n.written, n.scenes))),
					layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
					layout.Rigid(func(gtx C) D {
						if !running {
							return D{}
						}
						return label(th, th.Small.Weighted(gui.FontMedium), th.Pal.RunningInk, 1, "Зараз: "+firstText(p.Job.Message, "студія працює")+".")(gtx)
					}),
					layout.Rigid(label(th, th.Small, th.Pal.Text2, 2, line)),
					layout.Rigid(label(th, th.Small, th.Pal.Text3, 1, grammar+" · кадрів і відеомоделі не потрібно")),
				)
			}),
		)
	}
	right := func(gtx C) D {
		var ws []layout.Widget
		if n.written > 0 {
			ws = append(ws, func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Text: "Відрендерити", Icon: gui.IconFilm, Busy: kind == studio.JobRender,
					Disabled: running, Tip: "Намалювати кліпи з написаних анімацій (крок «Рендер»)"}.Layout(gtx, th, &st.render)
			})
		}
		ws = append(ws, func(gtx C) D {
			b := gui.Button{Kind: gui.Primary, Icon: gui.IconWandSparkles, Busy: kind == studio.JobFrames, Disabled: running,
				Text: "Озвучити й написати анімації", Tip: "Спершу озвучка кожної сцени, тоді ШІ пише анімацію точно під слова"}
			if n.written+n.fallback >= n.scenes {
				b.Kind, b.Text = gui.Tinted, "Дописати змінені"
				b.Tip = "Переписати лише сцени, чий текст або граматика змінились"
			}
			return b.Layout(gtx, th, &st.prep)
		})
		return gui.Flow{Gap: gui.Sp12}.Layout(gtx, ws...)
	}
	return gui.Card{Padding: headPad(gtx)}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return spread(gtx, gui.Sp24, 300, left, right)
	})
}

// sceneCard is one scene: title and state, its grammar, the editor, the
// preview, the pictures.
func (v *codeView) sceneCard(gtx C, c *core.Ctx, pv *core.ProjectView, st *codeState, i int) D {
	th := c.Th
	p := &pv.P
	s := &p.Scenes[i]
	cc := st.card(s.ID)
	cc.events(gtx, c, pv, s)
	head := func(gtx C) D {
		title := func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(label(th, th.Label.Weighted(gui.FontSemiBold), th.Pal.Text, 1, fmt.Sprintf("Сцена %d · %s", i+1, firstText(s.Title, "без назви")))),
				layout.Rigid(label(th, th.Small, th.Pal.Text2, 2, firstText(s.Narration, "без тексту"))),
			)
		}
		pill := func(gtx C) D { return codePill(s).Layout(gtx, th) }
		return spread(gtx, gui.Sp16, 260, title, pill)
	}
	grammar := func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
		cc.grammar.Flat, cc.grammar.Aspect = true, p.Input.Aspect
		cc.grammar.AutoLabel = "Як у фільмі (авто)"
		if p.Input.CodeGrammar != "" {
			cc.grammar.AutoLabel = "Як у фільмі — " + pickers.CodeGrammarLabel(c, p.Input.CodeGrammar)
		}
		if s.Code.Suggested != "" && p.Input.CodeGrammar == "" && s.Code.Grammar == "" {
			cc.grammar.Note = "план радить: " + pickers.CodeGrammarLabel(c, s.Code.Suggested)
		} else {
			cc.grammar.Note = ""
		}
		return cc.grammar.Layout(gtx, c, "Граматика сцени", s.Code.Grammar)
	}
	ws := []layout.Widget{head, grammar}
	if s.Code.Fallback {
		ws = append(ws, func(gtx C) D {
			return notice{Tone: gui.ToneWarning, Icon: gui.IconAlertTriangle, Title: "Анімація не вийшла — сцена буде кадром з рухом камери",
				Text: s.Code.Error, Lines: 6, Small: true, Action: func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Text: "Спробувати анімацію ще раз", Icon: gui.IconRefreshCw, Size: gui.Sm}.Layout(gtx, th, &cc.retry)
				}}.Layout(gtx, th)
		})
	} else if s.Video.Status == studio.StatusError {
		ws = append(ws, func(gtx C) D {
			return notice{Tone: gui.ToneError, Icon: gui.IconAlertTriangle, Title: "Рендер не вдався", Text: assetErr(s.Video), Lines: errLines, Small: true}.Layout(gtx, th)
		})
	}
	if len(s.Code.Spec) == 0 && !s.Code.Fallback {
		ws = append(ws, func(gtx C) D {
			txt := "Анімацію ще не написано. Вона з'явиться після озвучки — ШІ розставить усе точно під слова диктора."
			if strings.TrimSpace(s.MotionPrompt) != "" {
				txt += " Задум з плану: " + s.MotionPrompt
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(label(th, th.Small, th.Pal.Text2, 4, txt)),
				layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Text: "Написати анімацію", Icon: gui.IconWandSparkles, Size: gui.Sm,
						Busy: busy(s.Code.Preview), Disabled: busy(s.Video)}.Layout(gtx, th, &cc.rewrite)
				}),
			)
		})
	} else if len(s.Code.Spec) > 0 {
		ws = append(ws, func(gtx C) D { return cc.editor(gtx, c, pv, s) })
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return vstack(gtx, gui.Sp16, ws...)
	})
}

// codePill is the scene's state.
func codePill(s *studio.Scene) gui.StatusPill {
	switch studio.CodeSceneState(s) {
	case "fallback":
		return gui.StatusPill{State: gui.StateStale, Percent: -1, Text: "кадр замість анімації"}
	case "running":
		return gui.StatusPill{State: gui.StateRunning, Percent: s.Video.Progress, Text: "рендер"}
	case "error":
		return gui.StatusPill{State: gui.StateError, Percent: -1, Text: "помилка рендеру"}
	case "ready":
		return gui.StatusPill{State: gui.StateDone, Percent: -1, Text: "відрендерено"}
	case "spec":
		if s.Video.Stale {
			return gui.StatusPill{State: gui.StateStale, Percent: -1, Text: "змінено — перерендерити"}
		}
		return gui.StatusPill{State: gui.StateQueued, Percent: -1, Text: "написано"}
	}
	return gui.StatusPill{State: gui.StateEmpty, Percent: -1, Text: "ще не написано"}
}

// ---- events -------------------------------------------------------------------------------------

var errDemo = errors.New("Демо-режим: генерація вимкнена")

func (cc *codeCard) events(gtx C, c *core.Ctx, pv *core.ProjectView, s *studio.Scene) {
	id, sid := pv.ID, s.ID
	if v, ok := cc.grammar.Changed(); ok {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ID == sid {
					p.Scenes[i].Code.Grammar = v
				}
			}
		})
	}
	stored := string(s.Code.Spec)
	if cc.draft != nil && cc.base != stored && !cc.dirty() {
		cc.draft = nil // the engine wrote a new spec: follow it
	}
	if cc.retry.Clicked(gtx) {
		c.Regen(id, studio.RegenVideo, sid, "")
	}
	if cc.rewrite.Clicked(gtx) {
		cc.draft = nil
		if c.Offline {
			c.Error(errDemo)
		} else {
			c.Do(func() error { return c.E.RewriteCode(id, sid) }, nil)
		}
	}
	if cc.preview.Clicked(gtx) {
		at := -1.0
		if c.Offline {
			c.Error(errDemo)
		} else {
			if cc.dirty() { // preview what is on screen: save first
				cc.store(c, id, sid)
			}
			c.Do(func() error { return c.E.CodePreview(id, sid, at) }, nil)
		}
	}
	if cc.reset.Clicked(gtx) {
		cc.draft = nil
	}
	if cc.save.Clicked(gtx) && cc.dirty() && cc.problems == "" {
		cc.store(c, id, sid)
		if s.Code.Fallback || (s.Audio.Status != studio.StatusDone && strings.TrimSpace(s.Narration) != "" && pv.P.Input.Voiceover) {
			c.Toast(gui.ToneInfo, "Збережено — кліп відрендериться після озвучки")
		} else {
			c.Regen(id, studio.RegenVideo, sid, "")
		}
	}
}

// dirty: the draft differs from the stored spec.
func (cc *codeCard) dirty() bool {
	return cc.draft != nil && !bytes.Equal(bytes.TrimSpace(cc.draft), bytes.TrimSpace([]byte(cc.base)))
}

// store saves the draft as the scene's spec.
func (cc *codeCard) store(c *core.Ctx, id, sid string) {
	draft := append(json.RawMessage(nil), cc.draft...)
	if c.Edit(id, func(p *studio.Project) {
		for i := range p.Scenes {
			if p.Scenes[i].ID == sid {
				p.Scenes[i].Code.Spec = draft
			}
		}
	}) {
		cc.base, cc.draft = string(draft), nil
	}
}

// ---- the editor ----------------------------------------------------------------------------------

func (cc *codeCard) editor(gtx C, c *core.Ctx, pv *core.ProjectView, s *studio.Scene) D {
	th := c.Th
	if cc.draft == nil {
		cc.base = string(s.Code.Spec)
		cc.draft = append(json.RawMessage(nil), s.Code.Spec...)
		cc.json.SetText(string(s.Code.Spec))
		cc.checked = ""
	}
	if v, ok := cc.mode.Changed(gtx); ok {
		cc.lastMode = v
	}
	if v, ok := cc.json.Changed(gtx); ok {
		cc.draft = json.RawMessage(v)
	}
	cues, data, ok := studio.CodeCues(cc.draft)
	// Fields → draft.
	cc.grow(len(cues))
	for k := range cues {
		at, okAt := cues[k].At, true
		changed := false
		if v, ch := cc.at[k].Changed(gtx); ch {
			if f, good := studio.ParseCueTime(v); good {
				at, changed = f, true
			} else {
				okAt = false
			}
		}
		text, sub := cues[k].Text, cues[k].Sub
		if v, ch := cc.text[k].Changed(gtx); ch {
			text, changed = v, true
		}
		if v, ch := cc.sub[k].Changed(gtx); ch {
			sub, changed = v, true
		}
		if changed && okAt {
			if raw, good := studio.SetCodeCue(cc.draft, k, at, text, sub); good {
				cc.draft = raw
				cc.json.SetText(string(raw))
			}
		}
	}
	if string(cc.draft) != cc.checked {
		cc.checked = string(cc.draft)
		cc.problems = c.E.ValidateCodeSpec(&pv.P, s.ID, cc.draft)
	}
	cues, data, ok = studio.CodeCues(cc.draft)
	view := "fields"
	if cc.lastMode == "json" || !ok {
		view = "json"
	}
	left := func(gtx C) D {
		ws := []layout.Widget{
			func(gtx C) D {
				return cc.mode.Layout(gtx, th, view, []gui.SegOption{
					{Value: "fields", Label: "Поля", Icon: gui.IconList, Disabled: !ok},
					{Value: "json", Label: "JSON", Icon: gui.IconText},
				})
			},
		}
		if view == "json" {
			ws = append(ws, func(gtx C) D {
				return cc.json.Layout(gtx, th, gui.FieldStyle{Label: "Опис анімації (codeanim spec)", MinLines: 8, MaxLines: 18,
					Style: th.Small, Error: cc.problems, Hint: "Час «at» — секунда, коли елемент з'являється; кадр і тривалість студія ставить сама."})
			})
		} else {
			if t := dataSummary(data); t != "" {
				ws = append(ws, label(th, th.Small, th.Pal.Text2, 2, t))
			}
			for k := range cues {
				ws = append(ws, func(gtx C) D { return cc.cueRow(gtx, th, k, cues[k]) })
			}
			if cc.problems != "" {
				ws = append(ws, label(th, th.Small, th.Pal.ErrorInk, 4, "Не відрендериться: "+cc.problems))
			}
		}
		ws = append(ws, func(gtx C) D { return cc.actions(gtx, th, s) })
		return vstack(gtx, gui.Sp12, ws...)
	}
	right := func(gtx C) D { return cc.previewPane(gtx, c, pv, s) }
	W := gtx.Constraints.Max.X
	if W < gtx.Dp(820) {
		return vstack(gtx, gui.Sp16, right, left)
	}
	pw := min(gtx.Dp(420), W*2/5)
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Flexed(1, left),
		layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints = layout.Constraints{Min: image.Pt(pw, 0), Max: image.Pt(pw, gtx.Constraints.Max.Y)}
			return right(gtx)
		}),
	)
}

func (cc *codeCard) grow(n int) {
	for len(cc.at) < n {
		cc.at = append(cc.at, gui.TextField{})
		cc.text = append(cc.text, gui.TextField{})
		cc.sub = append(cc.sub, gui.TextField{})
	}
}

// cueRow edits one cue: when it appears, what it is, its texts.
func (cc *codeCard) cueRow(gtx C, th *gui.Theme, k int, cue studio.CodeCue) D {
	cc.at[k].Sync(gtx, trimNum(cue.At))
	cc.text[k].Sync(gtx, cue.Text)
	cc.sub[k].Sync(gtx, cue.Sub)
	kind := cue.Kind
	if cue.Image != "" {
		kind += " · " + cue.Image
	}
	if gtx.Constraints.Max.X < gtx.Dp(560) { // phones: time and kind, then the texts
		return vstack(gtx, gui.Sp8,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(96), gtx.Constraints.Max.Y))
						gtx.Constraints.Min.Y = 0
						return cc.at[k].Layout(gtx, th, gui.FieldStyle{Placeholder: "с", Style: th.Small})
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D { return gui.Badge{Text: clipText(kind, 28)}.Layout(gtx, th) }),
				)
			},
			func(gtx C) D {
				return cc.text[k].Layout(gtx, th, gui.FieldStyle{Placeholder: "Текст", Style: th.Small})
			},
			func(gtx C) D {
				return cc.sub[k].Layout(gtx, th, gui.FieldStyle{Placeholder: "Підпис", Style: th.Small})
			},
		)
	}
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(76), gtx.Constraints.Max.Y))
			gtx.Constraints.Min.Y = 0
			return cc.at[k].Layout(gtx, th, gui.FieldStyle{Label: "Час, с", Style: th.Small})
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Rigid(func(gtx C) D {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(110), gtx.Constraints.Max.Y))
			gtx.Constraints.Min.Y = 0
			return layout.Inset{Top: gui.Sp20}.Layout(gtx, func(gtx C) D { return gui.Badge{Text: clipText(kind, 22)}.Layout(gtx, th) })
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return cc.text[k].Layout(gtx, th, gui.FieldStyle{Label: "Текст", Style: th.Small, Placeholder: "—"})
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return cc.sub[k].Layout(gtx, th, gui.FieldStyle{Label: "Підпис", Style: th.Small, Placeholder: "—"})
		}),
	)
}

func (cc *codeCard) actions(gtx C, th *gui.Theme, s *studio.Scene) D {
	dirty := cc.dirty()
	ws := []layout.Widget{
		func(gtx C) D {
			return gui.Button{Kind: gui.Primary, Text: "Зберегти й перерендерити", Icon: gui.IconSave, Size: gui.Sm,
				Disabled: !dirty || cc.problems != "" || busy(s.Video)}.Layout(gtx, th, &cc.save)
		},
		func(gtx C) D {
			return gui.Button{Kind: gui.Tinted, Text: "Написати заново (ШІ)", Icon: gui.IconWandSparkles, Size: gui.Sm,
				Busy: busy(s.Code.Preview) && !dirty, Disabled: busy(s.Video)}.Layout(gtx, th, &cc.rewrite)
		},
	}
	if dirty {
		ws = append(ws, func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Text: "Скасувати правки", Size: gui.Sm}.Layout(gtx, th, &cc.reset)
		})
	} else if s.Code.Edited {
		ws = append(ws, label(th, th.Small, th.Pal.Text3, 1, "твої правки — збережено"))
	}
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
}

// previewPane is the preview still and the pictures of the spec.
func (cc *codeCard) previewPane(gtx C, c *core.Ctx, pv *core.ProjectView, s *studio.Scene) D {
	th := c.Th
	ratio := float32(16) / 9
	if pv.P.Input.Aspect == "9:16" {
		ratio = float32(9) / 16
	}
	pvw := s.Code.Preview
	dur := s.Code.Duration
	if dur <= 0 {
		dur = studio.CodeSpecDuration(s.Code.Spec)
	}
	ws := []layout.Widget{
		func(gtx C) D {
			if ratio < 1 {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(220))
			}
			path := ""
			if pvw.Path != "" {
				path = c.E.Abs(pv.ID, pvw.Path)
			}
			return cc.img.Layout(gtx, th, c.Img, path, pvw.UpdatedAt, gui.Thumb{Ratio: ratio, Fit: gui.FitContain, Loading: busy(pvw),
				Placeholder: gui.IconChartNoAxesColumn})
		},
		func(gtx C) D {
			caption := "Кадр ще не намальовано"
			switch {
			case busy(pvw):
				caption = "Малюємо кадр…"
			case pvw.Status == studio.StatusError:
				caption = "Кадр не вдався: " + pvw.Error
			case pvw.Path != "":
				caption = "Кадр на " + secs(s.Code.PreviewAt) + " з " + secs(dur)
				if pvw.Stale {
					caption += " · анімацію змінено"
				}
			}
			tone := th.Pal.Text2
			if pvw.Status == studio.StatusError {
				tone = th.Pal.ErrorInk
			}
			return label(th, th.Small, tone, 3, caption)(gtx)
		},
		func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Text: "Попередній перегляд кадру", Icon: gui.IconEye, Size: gui.Sm,
				Busy: busy(pvw), Disabled: cc.problems != ""}.Layout(gtx, th, &cc.preview)
		},
	}
	if len(s.Code.Images) > 0 {
		var parts []string
		for _, im := range s.Code.Images {
			state := "чекає"
			switch {
			case busy(im.Image):
				state = "малюється"
			case im.Image.Status == studio.StatusError:
				state = "помилка"
			case im.Image.Path != "":
				state = "готова"
			}
			kind := "картинка"
			if im.Kind == studio.CodeSprite {
				kind = "спрайт"
			}
			parts = append(parts, fmt.Sprintf("%s (%s, %s)", im.Name, kind, state))
		}
		ws = append(ws, label(th, th.Small, th.Pal.Text3, 3, "Картинки: "+strings.Join(parts, ", ")))
	}
	if d := dur; d > 0 {
		t := "Тривалість " + secs(d) + " — як озвучка сцени"
		if s.Code.Duration <= 0 {
			t = "Тривалість у описі " + secs(d) + " — підженеться під озвучку перед рендером"
		} else if s.Code.Estimated {
			t = "Тривалість ≈ " + secs(d) + " (за оцінкою — точна після озвучки)"
		}
		ws = append(ws, label(th, th.Small, th.Pal.Text3, 2, t))
	}
	return vstack(gtx, gui.Sp8, ws...)
}

// dataSummary is the grammar-level data of a spec in one line.
func dataSummary(data map[string]any) string {
	if len(data) == 0 {
		return ""
	}
	var parts []string
	for _, k := range []string{"title", "unit", "center", "eyebrow", "subtitle", "source", "region"} {
		if v, ok := data[k].(string); ok && strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Дані: " + strings.Join(parts, " · ")
}

func trimNum(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" {
		return "0"
	}
	return s
}

// ---- side and next ------------------------------------------------------------------------------

func (v *codeView) Side(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := v.state(pv.ID)
	p := &pv.P
	n := countCode(p)
	first := func(pred func(s *studio.Scene) bool) string {
		for i := range p.Scenes {
			if pred(&p.Scenes[i]) {
				return p.Scenes[i].ID
			}
		}
		return ""
	}
	rows := []sideRow{
		{Tone: gui.ToneSuccess, Label: "Відрендерено", Count: n.ready, Always: true},
		{Tone: gui.ToneRunning, Label: "Рендеряться", Count: n.busy, Scene: first(func(s *studio.Scene) bool { return busy(s.Video) })},
		{Tone: gui.ToneNeutral, Label: "Написано, ще не відрендерено", Count: n.written - n.ready - n.busy - n.errs,
			Scene: first(func(s *studio.Scene) bool { return studio.CodeSceneState(s) == "spec" })},
		{Tone: gui.ToneWarning, Label: "Кадр замість анімації", Count: n.fallback, Scene: first(func(s *studio.Scene) bool { return s.Code.Fallback })},
		{Tone: gui.ToneError, Label: "Помилки рендеру", Count: n.errs, Scene: first(func(s *studio.Scene) bool { return studio.CodeSceneState(s) == "error" })},
	}
	if id := st.side.clicked(gtx, rows); id != "" {
		c.UI(pv.ID).ScrollTo = id
	}
	return sideStack(gtx,
		func(gtx C) D {
			return st.side.card(gtx, th, "Стан анімацій", fmt.Sprintf("%d з %d", n.written, n.scenes), rows)
		},
		func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLightbulb, Title: "Анімація кодом",
				Text: "Анімацію малює код: графіки, мапи, текст і схеми з'являються точно на словах диктора, без генерації відео. " +
					"Персонажі — окремими картинками. Змінюй тексти й час прямо в картці сцени, а «Попередній перегляд кадру» покаже результат."}.Layout(gtx, th, nil)
		},
	)
}

func (v *codeView) Next(c *core.Ctx, pv *core.ProjectView) core.Next {
	p := &pv.P
	id := pv.ID
	if pv.Running() {
		return core.Next{Icon: gui.IconWandSparkles, Title: "Студія працює…", Text: firstText(p.Job.Message, "Озвучка й анімації."), Busy: true}
	}
	n := countCode(p)
	if n.written+n.fallback < n.scenes {
		return core.Next{Icon: gui.IconWandSparkles, Title: "Озвучити й написати анімації",
			Text: "Спершу озвучка, потім ШІ пише анімацію кожної сцени точно під слова.", Do: func() { c.Run(id, studio.StageFrames, false, "") }}
	}
	return core.Next{Icon: gui.IconFilm, Title: "Далі: рендер анімацій",
		Text: "Усі анімації написано — студія намалює кліпи на цьому комп'ютері.", Do: func() { goStep(c, id, 4) }}
}
