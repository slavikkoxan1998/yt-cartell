package gui

import (
	"image"
	"sync"
	"time"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// Toasts: short messages that slide in at the bottom-right (220 ms), stay a
// few seconds (hovering pauses the timer) and slide away. Post them from
// anywhere — th.Toast is safe on any goroutine:
//
//	th.Toast(gui.ToneSuccess, "Проєкт збережено")

// ToastTTL is how long a toast stays (errors stay twice as long).
var ToastTTL = 4 * time.Second

type toast struct {
	tone  Tone
	text  string
	born  time.Time // first frame shown
	shown time.Duration
	enter Anim
	leave Anim
	going bool
	close Clickable
	hover int // hover tag
	over  bool
	last  time.Time
}

type toastHost struct {
	mu         sync.Mutex
	incoming   []*toast
	items      []*toast
	invalidate func()
}

// Toast shows a message with the tone's icon. Safe from any goroutine
// (install th.SetInvalidate so a background toast wakes the window).
func (th *Theme) Toast(tone Tone, text string) {
	h := &th.svc.toasts
	h.mu.Lock()
	h.incoming = append(h.incoming, &toast{tone: tone, text: text})
	h.mu.Unlock()
	th.invalidate()
}

// Toasts returns the number of toasts on screen (tests).
func (th *Theme) Toasts() int { return len(th.svc.toasts.items) }

func toneIcon(t Tone) *Icon {
	switch t {
	case ToneSuccess:
		return IconCheckCircle
	case ToneError:
		return IconXCircle
	case ToneWarning:
		return IconAlertTriangle
	case ToneRunning:
		return IconLoaderCircle
	case ToneAction:
		return IconSparkles
	default:
		return IconInfo
	}
}

func (th *Theme) layoutToasts(gtx C) {
	h := &th.svc.toasts
	h.mu.Lock()
	h.items = append(h.items, h.incoming...)
	h.incoming = h.incoming[:0]
	h.mu.Unlock()
	if len(h.items) == 0 {
		return
	}
	const maxShown = 4
	if n := len(h.items); n > maxShown*2 {
		h.items = h.items[n-maxShown*2:]
	}
	m := op.Record(gtx.Ops)
	W, H := th.svc.win.X, th.svc.win.Y
	margin := gtx.Dp(24)
	width := min(gtx.Dp(380), W-2*margin)
	y := H - margin
	gap := gtx.Dp(12)
	// A fresh slice: filling h.items[:0] while reading it backwards would
	// overwrite toasts not yet visited.
	kept := make([]*toast, 0, len(h.items))
	// Newest at the bottom; iterate from newest upwards.
	for i := len(h.items) - 1; i >= 0; i-- {
		t := h.items[i]
		if t.close.Clicked(gtx) {
			t.going = true
		}
		// Hover pauses the clock.
		for {
			e, ok := gtx.Event(pointer.Filter{Target: &t.hover, Kinds: pointer.Enter | pointer.Leave})
			if !ok {
				break
			}
			if pe, ok := e.(pointer.Event); ok {
				t.over = pe.Kind == pointer.Enter
			}
		}
		if t.born.IsZero() {
			t.born, t.last = gtx.Now, gtx.Now
			t.enter.Duration, t.enter.Ease = DurToast, EaseOutCubic
			t.enter.Set(0)
		}
		if !t.over && !t.going {
			t.shown += gtx.Now.Sub(t.last)
		}
		t.last = gtx.Now
		ttl := ToastTTL
		if t.tone == ToneError {
			ttl *= 2
		}
		if t.shown >= ttl || len(h.items)-i > maxShown {
			t.going = true
		}
		in := t.enter.Animate(gtx, 1)
		out := float32(0)
		if t.going {
			t.leave.Duration, t.leave.Ease = 180*time.Millisecond, EaseInCubic
			out = t.leave.Animate(gtx, 1)
			if out >= 1 {
				continue // gone
			}
		} else if !t.over {
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(ttl - t.shown)})
		}
		kept = append(kept, t)
		lg := gtx
		lg.Constraints = layout.Exact(pt(width, 0))
		lg.Constraints.Max.Y = H
		lg.Constraints.Min.Y = 0
		d, call := record(lg, func(gtx C) D { return th.toastCard(gtx, t) })
		y -= d.Size.Y
		dx := int(float32(gtx.Dp(32)) * ((1 - in) + out))
		alpha := in * (1 - out)
		func() {
			defer op.Offset(pt(W-margin-width+dx, y)).Push(gtx.Ops).Pop()
			defer opacity(gtx, alpha)()
			call.Add(gtx.Ops)
		}()
		y -= gap
	}
	// kept is newest-first; restore oldest-first order.
	for l, r := 0, len(kept)-1; l < r; l, r = l+1, r-1 {
		kept[l], kept[r] = kept[r], kept[l]
	}
	h.items = kept
	op.Defer(gtx.Ops, m.Stop())
}

func (th *Theme) toastCard(gtx C, t *toast) D {
	pad := gtx.Dp(16)
	_, soft, ink := th.Pal.ToneColors(t.tone)
	w := gtx.Constraints.Max.X
	icBox := gtx.Dp(36)
	closeW := gtx.Dp(32)
	tg := gtx
	tg.Constraints = layout.Constraints{Max: pt(max(0, w-2*pad-icBox-gtx.Dp(12)-closeW-gtx.Dp(8)), 1<<20)}
	td, tc := record(tg, func(gtx C) D {
		return Text{Style: th.Body.Lined(1.4), Color: th.Pal.Text, MaxLines: 4}.Layout(gtx, th, t.text)
	})
	h := max(td.Size.Y, icBox) + 2*pad
	r := rectWH(w, h)
	Shadow(gtx, th, r, RadiusInner, ElevPop)
	Box(gtx, r, RadiusInner, th.Pal.Card, th.Pal.Border)
	// Hover area pauses the timer.
	func() {
		area := clipRect(gtx, r.Size())
		defer area.Pop()
		evtag(gtx, &t.hover)
	}()
	// Icon in a soft circle.
	func() {
		defer op.Offset(pt(pad, (h-icBox)/2)).Push(gtx.Ops).Pop()
		FillCircle(gtx, unit.Dp(float32(icBox)/gtx.Metric.PxPerDp), soft)
		defer op.Offset(pt((icBox-gtx.Dp(20))/2, (icBox-gtx.Dp(20))/2)).Push(gtx.Ops).Pop()
		toneIcon(t.tone).Layout(gtx, 20, ink)
	}()
	func() {
		defer op.Offset(pt(pad+icBox+gtx.Dp(12), (h-td.Size.Y)/2)).Push(gtx.Ops).Pop()
		tc.Add(gtx.Ops)
	}()
	func() {
		defer op.Offset(pt(w-pad-closeW+gtx.Dp(6), (h-closeW)/2)).Push(gtx.Ops).Pop()
		IconButton{Icon: IconX, Size: 32, Tip: "Закрити"}.Layout(gtx, th, &t.close)
	}()
	return D{Size: image.Pt(w, h)}
}
