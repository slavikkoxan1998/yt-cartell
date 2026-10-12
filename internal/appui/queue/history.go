package queue

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// histRow is one row of «Нещодавно» on the page (and of the single
// pictures that generate): its 16:9 picture, what was made and for which
// project, the time, the result, «Відкрити» and «Повторити».
func (v *View) histRow(gtx C, c *core.Ctx, it studio.QueueItem, divider bool) D {
	th := c.Th
	st := v.state(it.Key)
	v.handle(gtx, c, it, st)
	in := st.in.Animate(gtx, 1)
	w := gtx.Constraints.Max.X
	small := w < gtx.Dp(440)
	// Roomy rows keep the actions on the result's line.
	inline := w >= gtx.Dp(520)
	tw := gtx.Dp(120)
	if small {
		tw = gtx.Dp(96)
	}
	d := gui.FadeUp(gtx, in, 6, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: gui.Sp12, Bottom: gui.Sp12}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return thumbBox(gtx, c, &st.img, it.Thumb, kindIcon(it), small) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, func(gtx C) D { return v.histBody(gtx, c, it, st, inline) }),
			)
		})
	})
	if divider {
		px := max(1, gtx.Dp(1))
		gui.FillRect(gtx, image.Rect(tw+gtx.Dp(16), d.Size.Y-px, d.Size.X, d.Size.Y), th.Pal.Border)
	}
	return d
}

func (v *View) histBody(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState, inline bool) D {
	th := c.Th
	var ch []layout.FlexChild
	ch = append(ch, layout.Rigid(func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, it.Label)),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: gui.Sp12}.Layout(gtx, func(gtx C) D { return trailing(gtx, th, it) })
			}),
		)
	}))
	ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, meta(it, true))))
	if it.Status == studio.JobRunning {
		ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
			if indeterminate(it) {
				return gui.ProgressBar{Value: -1, Height: 6, Tone: gui.ToneRunning}.Layout(gtx, th)
			}
			st.bar.Duration, st.bar.Ease = gui.DurValue, gui.EaseOutCubic
			val := st.bar.Animate(gtx, float32(max(0, min(100, it.Progress)))/100)
			return gui.ProgressBar{Value: val, Height: 6, Tone: gui.ToneRunning}.Layout(gtx, th)
		}))
	}
	res, ink := result(th, it)
	lines := 2
	if it.Status == studio.JobError {
		lines = 3
	}
	acts := func(gtx C) D { return v.actions(gtx, c, it, st) }
	if inline {
		ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout), layout.Rigid(func(gtx C) D {
			return layout.Flex{Alignment: layout.End}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					if res == "" {
						return D{Size: image.Pt(gtx.Constraints.Min.X, 0)}
					}
					return layout.Inset{Bottom: gui.Sp8}.Layout(gtx, gui.Text{Style: th.Small, Color: ink, MaxLines: lines}.Widget(th, res))
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(acts),
			)
		}))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	}
	if res != "" {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: gui.Sp4}.Layout(gtx, gui.Text{Style: th.Small, Color: ink, MaxLines: lines}.Widget(th, res))
		}))
	}
	ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.E.Layout(gtx, acts)
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// result is what a row says about the outcome (and in which ink): the
// error, the items left failed, the engine's summary ("Відео й озвучка
// готові (8)"), or for a running row its progress line.
func result(th *gui.Theme, it studio.QueueItem) (string, color.NRGBA) {
	msg := strings.TrimSpace(it.Message)
	switch {
	case it.Status == studio.JobRunning:
		return message(it), th.Pal.Text3
	case it.Status == studio.JobError:
		e := strings.TrimSpace(it.Error)
		if e == "" {
			e = msg
		}
		if e == "" {
			return "Не вдалося — спробуй ще раз.", th.Pal.ErrorInk
		}
		return core.ErrorText(fmt.Errorf("%s", e)), th.Pal.ErrorInk
	case PartlyFailed(it):
		return msg, th.Pal.WarningInk
	case it.Status == studio.JobCancelled:
		return "Зупинено — готові частини збережено.", th.Pal.Text2
	case msg == "" || msg == "Готово" || msg == it.Label:
		return "", th.Pal.Text2
	}
	return msg, th.Pal.Text2
}
