package montage

// «Не всі сцени готові»: the montage never fills in a failed scene by itself
// (studio/gaps.go). Before a render with scenes that have no clip, no frame
// or no voice line, this dialog lists every one of them with the service's
// exact error and asks — «Змонтувати без них», «З кадром замість відео» or
// «Зупинитись». The final card lists what the last render left out or
// showed as a still.

import (
	"errors"
	"fmt"
	"image"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// gapsDialog is the montage's question about scenes that are not ready.
type gapsDialog struct {
	dlg               gui.Dialog
	skip, still, stop gui.Clickable
	gaps              []studio.MontageGap
}

func (g *gapsDialog) open(gaps []studio.MontageGap) {
	g.gaps = append([]studio.MontageGap(nil), gaps...)
	g.dlg.Open()
}

// runMontage starts the render — or, when scenes are not ready, asks first.
func (s *proj) runMontage(c *core.Ctx, pv *core.ProjectView) {
	if gaps := studio.MontageGaps(&pv.P, ""); len(gaps) > 0 {
		s.gaps.open(gaps)
		return
	}
	s.startMontage(c, pv.ID, studio.MissingAsk)
}

// startMontage runs the montage with the user's choice for scenes that are
// not ready (MissingAsk when there are none).
func (s *proj) startMontage(c *core.Ctx, id, missing string) {
	if c.Offline {
		c.Error(errors.New("Демо-режим: генерація вимкнена"))
		return
	}
	err := c.E.RunWith(id, studio.StageMontage, studio.RunOptions{Missing: missing})
	if m, ok := studio.IsMissingScenes(err); ok && missing == studio.MissingAsk {
		s.gaps.open(m.Gaps) // files gone since the last look
		return
	}
	if err != nil {
		c.Error(err)
		return
	}
	s.memo.watching, s.memo.startSig = true, s.outSig
}

// gapsLayout queues the dialog (nothing while closed).
func (s *proj) gapsLayout(gtx C, c *core.Ctx, pv *core.ProjectView) {
	th := c.Th
	g := &s.gaps
	if u := c.UI(pv.ID); u.Gaps { // the job banner's «Обрати, як змонтувати»
		u.Gaps = false
		if gaps := studio.MontageGaps(&pv.P, ""); len(gaps) > 0 {
			g.open(gaps)
		} else {
			s.startMontage(c, pv.ID, studio.MissingAsk)
		}
	}
	if g.stop.Clicked(gtx) {
		g.dlg.Close()
	}
	busy := pv.Running()
	if g.skip.Clicked(gtx) && !busy {
		g.dlg.Close()
		s.startMontage(c, pv.ID, studio.MissingSkip)
	}
	if g.still.Clicked(gtx) && !busy {
		g.dlg.Close()
		s.startMontage(c, pv.ID, studio.MissingStill)
	}
	n := len(g.gaps)
	frames := 0 // scenes a still can stand in for
	for _, gp := range g.gaps {
		if gp.NoClip && !gp.NoFrame {
			frames++
		}
	}
	stillTip := "Сцени без кліпу покажуть свій кадр із рухом; сцени без кадру випадуть, без голосу — пройдуть без репліки"
	if frames == 0 {
		stillTip = "Кадра немає в жодної з цих сцен — вони випадуть; сцени без голосу пройдуть без репліки"
	}
	g.dlg.Layout(gtx, th, gui.DialogStyle{
		Title: fmt.Sprintf("Не всі сцени готові — %s", count(n, "сцена", "сцени", "сцен")),
		Text:  "Студія нічого не підставляє сама. Ось що не вийшло і чому — обери, як монтувати:",
		Icon:  gui.IconAlertTriangle,
		Tone:  gui.ToneError,
		Width: 640,
		Body: func(gtx C) D {
			return gapsList(gtx, th, g.gaps, 8)
		},
		Actions: []layout.Widget{
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Text: "Зупинитись", Tip: "Нічого не монтувати — перезняти сцени на кроках 3–4"}.Layout(gtx, th, &g.stop)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconSkipForward, Text: "Змонтувати без них", Disabled: busy,
					Tip: "Ці сцени (і їхній голос) не потраплять у фільм"}.Layout(gtx, th, &g.skip)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Primary, Icon: gui.IconImage, Text: "З кадром замість відео", Disabled: busy,
					Tip: stillTip}.Layout(gtx, th, &g.still)
			},
		},
	})
}

// gapsList draws scenes that are not ready: "Сцена 3" and the exact reason
// (+ a hint), at most max of them (then "і ще N").
func gapsList(gtx C, th *gui.Theme, gaps []studio.MontageGap, max int) D {
	var ws []layout.Widget
	for i, g := range gaps {
		if i == max {
			rest := len(gaps) - max
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "і ще "+count(rest, "сцена", "сцени", "сцен")+" — усі вони червоні на кроках 3–4."))
			break
		}
		ws = append(ws, gapRow(th, g))
	}
	return gui.VStack(gtx, gui.Sp8, ws...)
}

// gapRow is one scene of gapsList.
func gapRow(th *gui.Theme, g studio.MontageGap) layout.Widget {
	head := fmt.Sprintf("Сцена %d", g.Number)
	switch g.Action {
	case studio.GapSkipped:
		head += " — пропущена"
	case studio.GapStill:
		head += " — кадр замість відео"
	case studio.GapSilent:
		head += " — без голосу"
	default:
		var what []string
		if g.NoClip && g.NoFrame {
			what = append(what, "немає ні кліпу, ні кадру")
		} else if g.NoClip {
			what = append(what, "немає кліпу")
		}
		if g.NoVoice {
			what = append(what, "немає голосу")
		}
		if len(what) > 0 {
			head += " — " + strings.Join(what, ", ")
		}
	}
	reason := strings.TrimSpace(g.Reason)
	if h := studio.FailureHint(reason); h != "" {
		reason += "\nПідказка: " + h
	}
	return func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontSemiBold), Color: th.Pal.ErrorInk, MaxLines: 1}.Widget(th, head)),
			layout.Rigid(func(gtx C) D {
				if reason == "" {
					return D{}
				}
				return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 6}.Layout(gtx, th, reason)
			}),
		)
	}
}

// gapsNote is the final card's list: before a render the scenes that are
// not ready (the montage will ask), after one what it left out or replaced.
func (s *proj) gapsNote(gtx C, c *core.Ctx, pv *core.ProjectView, done bool) D {
	th := c.Th
	var gaps []studio.MontageGap
	title := ""
	tone := gui.ToneError
	if done {
		gaps = pv.P.Montage.Gaps
		title = "У фільмі не всі сцени як у плані:"
		tone = gui.ToneWarning
	} else {
		gaps = studio.MontageGaps(&pv.P, "")
		title = count(len(gaps), "сцена не готова", "сцени не готові", "сцен не готові") + " — перед монтажем студія спитає, як бути:"
	}
	if len(gaps) == 0 {
		return D{}
	}
	_, soft, ink := th.Pal.ToneColors(tone)
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(12)
	g := gtx
	g.Constraints = layout.Constraints{Max: pt(max(0, w-2*pad), 1<<16)}
	d, call := measure(g, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return gui.VStack(gtx, gui.Sp8,
			gui.Text{Style: th.Small.Weighted(gui.FontSemiBold), Color: ink}.Widget(th, title),
			func(gtx C) D { return gapsList(gtx, th, gaps, 6) },
		)
	})
	size := pt(w, d.Size.Y+2*pad)
	fillRR(gtx, image.Rectangle{Max: size}, gtx.Dp(gui.RadiusControl), soft)
	o := opOffset(gtx, pad, pad)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}
