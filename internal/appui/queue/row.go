package queue

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// row draws one queue item: a thumbnail (or a tone circle with the work's
// icon), what is made and for which project, the live progress or the
// error, and the row's actions. divider draws a hairline under it; wide is
// the page's roomier variant.
func (v *View) row(gtx C, c *core.Ctx, it studio.QueueItem, divider, wide, grouped bool) D {
	th := c.Th
	st := v.state(it.Key)
	v.handle(gtx, c, it, st)
	in := st.in.Animate(gtx, 1)
	pad := gui.Sp12
	if wide {
		pad = gui.Sp16
	}
	d := gui.FadeUp(gtx, in, 6, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: pad, Bottom: pad}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return lead(gtx, c, it, st) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, func(gtx C) D { return v.rowBody(gtx, c, it, st, grouped) }),
			)
		})
	})
	if divider {
		px := max(1, gtx.Dp(1))
		gui.FillRect(gtx, image.Rect(gtx.Dp(64), d.Size.Y-px, d.Size.X, d.Size.Y), th.Pal.Border)
	}
	return d
}

func (v *View) rowBody(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState, grouped bool) D {
	th := c.Th
	running := it.Status == studio.JobRunning
	var ch []layout.FlexChild
	ch = append(ch, layout.Rigid(func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, it.Label)),
			layout.Rigid(func(gtx C) D {
				return layout.Inset{Left: gui.Sp12}.Layout(gtx, func(gtx C) D { return trailing(gtx, th, it) })
			}),
		)
	}))
	ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, meta(it, grouped))))
	if running {
		if msg := message(it); msg != "" {
			ch = append(ch, layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, msg))
			}))
		}
		ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
			if indeterminate(it) {
				return gui.ProgressBar{Value: -1, Height: 6, Tone: gui.ToneRunning}.Layout(gtx, th)
			}
			st.bar.Duration, st.bar.Ease = gui.DurValue, gui.EaseOutCubic
			val := st.bar.Animate(gtx, float32(max(0, min(100, it.Progress)))/100)
			return gui.ProgressBar{Value: val, Height: 6, Tone: gui.ToneRunning}.Layout(gtx, th)
		}))
	}
	if PartlyFailed(it) {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: gui.Sp4}.Layout(gtx,
				gui.Text{Style: th.Small, Color: th.Pal.WarningInk, MaxLines: 2}.Widget(th, it.Message))
		}))
	}
	if it.Status == studio.JobError && strings.TrimSpace(it.Error) != "" {
		ch = append(ch, layout.Rigid(func(gtx C) D {
			return layout.Inset{Top: gui.Sp4}.Layout(gtx,
				gui.Text{Style: th.Small, Color: th.Pal.ErrorInk, MaxLines: 3}.Widget(th, core.ErrorText(fmt.Errorf("%s", it.Error))))
		}))
	}
	ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
		// Right-aligned under the status: the row reads left to right, the
		// actions wait at its end.
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.E.Layout(gtx, func(gtx C) D { return v.actions(gtx, c, it, st) })
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
}

// actions are the row's small buttons: «Повторити» for what can run again,
// «Зупинити» for a running job, «Відкрити проєкт» always.
func (v *View) actions(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState) D {
	th := c.Th
	// Phones keep the actions on one line: a shorter label, then no icons.
	open, tip := "Відкрити проєкт", "Відкрити проєкт"
	switch it.Regen {
	case studio.RegenFrame, studio.RegenVideo, studio.RegenVoice:
		open, tip = "Відкрити сцену", "Відкрити сцену в проєкті"
	}
	icOpen, icStop, icRetry := gui.IconArrowUpRight, gui.IconCircleStop, gui.IconRotateCcw
	if w := gtx.Constraints.Max.X; w < gtx.Dp(320) {
		open = "Відкрити"
		if w < gtx.Dp(260) {
			icOpen, icStop, icRetry = nil, nil, nil
		}
	}
	ws := []layout.Widget{func(gtx C) D {
		return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: icOpen, Text: open, Tip: tip}.Layout(gtx, th, &st.open)
	}}
	if it.Status == studio.JobRunning && it.Kind != "" {
		ws = append(ws, func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: icStop, Text: "Зупинити",
				Tip: "Зупинити цей етап — готові частини збережуться"}.Layout(gtx, th, &st.stop)
		})
	}
	// The recovery action comes last: the rightmost, where the eye ends.
	if _, partial := retryStage(it); it.Retryable() || partial {
		ws = append(ws, func(gtx C) D {
			return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: icRetry, Text: "Повторити"}.Layout(gtx, th, &st.retry)
		})
	}
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
}

// lead is the 48 dp square in front of a row: the picture the work belongs
// to with a small status badge, or the work's icon in a tone circle.
func lead(gtx C, c *core.Ctx, it studio.QueueItem, st *rowState) D {
	th := c.Th
	sz := gtx.Dp(48)
	tone := statusTone(it.Status)
	if it.Thumb != "" {
		cg := gtx
		cg.Constraints = layout.Exact(image.Pt(sz, sz))
		st.img.Layout(cg, th, c.Img, it.Thumb, 0, gui.Thumb{Radius: 12, Placeholder: kindIcon(it)})
		if it.Status != studio.JobRunning {
			b := gtx.Dp(20)
			off := op.Offset(image.Pt(sz-b+gtx.Dp(5), sz-b+gtx.Dp(5))).Push(gtx.Ops)
			badge(gtx, th, it, b)
			off.Pop()
		}
		return D{Size: image.Pt(sz, sz)}
	}
	_, soft, ink := th.Pal.ToneColors(tone)
	if tone == gui.ToneNeutral {
		soft, ink = th.Pal.Surface, th.Pal.Text
	}
	gui.FillCircle(gtx, 48, soft)
	ic := gtx.Dp(22)
	off := op.Offset(image.Pt((sz-ic)/2, (sz-ic)/2)).Push(gtx.Ops)
	kindIcon(it).Layout(gtx, 22, ink)
	off.Pop()
	return D{Size: image.Pt(sz, sz)}
}

// badge is a small status disc with a card-coloured ring (over thumbnails).
func badge(gtx C, th *gui.Theme, it studio.QueueItem, size int) {
	tone := statusTone(it.Status)
	icon := gui.IconMinus
	switch {
	case PartlyFailed(it):
		tone, icon = gui.ToneWarning, iconExclaim
	case it.Status == studio.JobDone:
		icon = gui.IconCheck
	case it.Status == studio.JobError:
		icon = gui.IconX
	}
	strong, _, _ := th.Pal.ToneColors(tone)
	if tone == gui.ToneNeutral {
		strong = th.Pal.Text2
	}
	dp := float32(size) / gtx.Metric.PxPerDp
	gui.FillCircle(gtx, unit.Dp(dp), th.Pal.Card)
	ring := gtx.Dp(2)
	off := op.Offset(image.Pt(ring, ring)).Push(gtx.Ops)
	gui.FillCircle(gtx, unit.Dp(dp-4), strong)
	off.Pop()
	ic := gtx.Dp(12)
	off = op.Offset(image.Pt((size-ic)/2, (size-ic)/2)).Push(gtx.Ops)
	icon.LayoutStroke(gtx, 12, 2.5, gui.OnColor(strong))
	off.Pop()
}

// trailing is the right end of the first line: the percentage while
// running, else the status.
func trailing(gtx C, th *gui.Theme, it studio.QueueItem) D {
	switch it.Status {
	case studio.JobRunning:
		if indeterminate(it) {
			return gui.StatusPill{State: gui.StateRunning, Percent: -1, Small: true}.Layout(gtx, th)
		}
		return gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), MaxLines: 1}.Layout(gtx, th, fmt.Sprintf("%d%%", max(0, min(100, it.Progress))))
	case studio.JobDone:
		if PartlyFailed(it) {
			return gui.StatusPill{State: gui.StateStale, Text: "З помилками", Small: true}.Layout(gtx, th)
		}
		return gui.StatusPill{State: gui.StateDone, Small: true}.Layout(gtx, th)
	case studio.JobError:
		return gui.StatusPill{State: gui.StateError, Small: true}.Layout(gtx, th)
	default:
		return gui.StatusPill{State: gui.StateEmpty, Text: "Зупинено", Small: true}.Layout(gtx, th)
	}
}

// indeterminate: nothing to measure yet — single pictures and voices report
// no progress until they are done (the engine's contract), and a job sits
// at 0 % until its first item finishes. A sweeping bar says "working"
// where an empty one would look stuck.
func indeterminate(it studio.QueueItem) bool {
	return it.Status == studio.JobRunning && it.Progress <= 0 && it.Regen != studio.RegenVideo
}

// PartlyFailed reports a job that finished but left items behind (the
// engine's Job.Failures; older saved jobs only say so in the message, like
// "Готово 1/3, помилок: 2").
func PartlyFailed(it studio.QueueItem) bool {
	if it.PartlyFailed() {
		return true
	}
	return it.Status == studio.JobDone && it.Kind != "" && strings.Contains(strings.ToLower(it.Message), "помил")
}

// meta is the second line: the project and when.
func meta(it studio.QueueItem, grouped bool) string {
	title := strings.TrimSpace(it.ProjectTitle)
	if title == "" {
		title = "Без назви"
	}
	switch {
	case it.Status == studio.JobRunning && it.StartedAt > 0:
		if d := time.Since(time.UnixMilli(it.StartedAt)); d >= time.Minute {
			return title + " · іде " + elapsed(d)
		}
		return title + " · щойно почалося"
	case it.FinishedAt > 0 && grouped:
		// Under a day heading the time of day is enough.
		return title + " · " + time.UnixMilli(it.FinishedAt).Local().Format("15:04")
	case it.FinishedAt > 0:
		return title + " · " + core.Updated(it.FinishedAt)
	}
	return title
}

// message is the engine's progress line without the part the row already
// shows ("Автопілот · Кадри сцен · 3/8" under the label «Автопілот»).
func message(it studio.QueueItem) string {
	msg := strings.TrimSpace(it.Message)
	if msg == "" || msg == it.Label {
		return ""
	}
	if rest, ok := strings.CutPrefix(msg, it.Label+" · "); ok {
		return rest
	}
	return msg
}

func elapsed(d time.Duration) string {
	m := int(d.Minutes())
	if m < 60 {
		return fmt.Sprintf("%d хв", m)
	}
	return fmt.Sprintf("%d год %d хв", m/60, m%60)
}

func statusTone(s studio.JobStatus) gui.Tone {
	switch s {
	case studio.JobRunning:
		return gui.ToneRunning
	case studio.JobDone:
		return gui.ToneSuccess
	case studio.JobError:
		return gui.ToneError
	}
	return gui.ToneNeutral
}

// kindIcon is the icon of the work a row stands for.
func kindIcon(it studio.QueueItem) *gui.Icon {
	switch it.Regen {
	case studio.RegenCharacter:
		return gui.IconUserRound
	case studio.RegenLocation:
		return gui.IconMapPin
	case studio.RegenFrame:
		return gui.IconImage
	case studio.RegenVideo:
		return gui.IconFilm
	case studio.RegenVoice:
		return gui.IconMic
	}
	switch it.Kind {
	case studio.JobPlan:
		return gui.IconListOrdered
	case studio.JobRefs:
		return gui.IconUsers
	case studio.JobFrames:
		return gui.IconImages
	case studio.JobVideos:
		return gui.IconFilm
	case studio.JobVoices:
		return gui.IconMic
	case studio.JobRender:
		return gui.IconClapperboard
	case studio.JobMontage:
		return gui.IconScissors
	case studio.JobCover:
		return gui.IconImagePlus
	case studio.JobAutopilot:
		return gui.IconRocket
	case "translate":
		return gui.IconLanguages
	case "transcribe":
		return gui.IconAudioLines
	}
	return gui.IconSparkles
}

// retryStage is the stage that re-runs only the failed items of a job that
// finished with errors (Engine.Retry refuses a "done" job).
func retryStage(it studio.QueueItem) (studio.Stage, bool) {
	if !PartlyFailed(it) {
		return "", false
	}
	switch it.Kind {
	case studio.JobRefs:
		return studio.StageRefs, true
	case studio.JobFrames:
		return studio.StageFrames, true
	case studio.JobVideos:
		return studio.StageVideos, true
	case studio.JobVoices:
		return studio.StageVoices, true
	case studio.JobRender:
		return studio.StageRender, true
	case studio.JobAutopilot:
		return studio.StageAutopilot, true
	}
	return "", false
}

// iconExclaim is a bare "!" for the tiny status badge (the kit's alert
// icons carry a frame that is illegible at 12 dp).
var iconExclaim = func() *gui.Icon {
	ic, err := gui.NewIcon("exclaim", `<path d="M12 5v9"/><path d="M12 19h.01"/>`)
	if err != nil {
		return gui.IconMinus
	}
	return ic
}()
