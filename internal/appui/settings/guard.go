package settings

// The click guard: right after a dropdown of the page closes (a pick or an
// outside press), presses on the page are swallowed for a moment. The second
// press of a double click on a list row used to land on whatever lay under
// the closed list — often a provider selector («Хто пише текст», «Кадри
// малює») that then switched to Google unasked.

import (
	"image"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/op"
	"gioui.org/op/clip"

	"ytcartell/internal/gui"
)

// clickGuard outlasts a double click (Windows: 500 ms by default, most
// clicks far faster).
const clickGuard = 450 * time.Millisecond

// popupOpen reports a dropdown of the page that is open.
func (p *Page) popupOpen() bool {
	for _, s := range []*gui.Select{&p.text.modelSel, &p.video.model, &p.video.gmodel, &p.tts.provider, &p.tts.modelSel,
		&p.frames.custom.preset, &p.frames.custom.modelSel, &p.video.custom.preset, &p.video.custom.modelSel,
		&p.frames.custom.spec.tpl, &p.video.custom.spec.tpl, &p.stt.localModel, &p.tts.spec.tpl, &p.stt.spec.tpl,
		&p.text.proto, &p.text.auth,
		&p.dict.provider, &p.dict.lang, &p.dict.device, &p.dict.silence, &p.autopilot.profile} {
		if s.IsOpen() {
			return true
		}
	}
	return p.tts.voice.IsOpen()
}

// guard runs after the page content: it notices a dropdown that closed this
// frame and, while the guard lasts, covers the content (size) with an area
// that takes the presses.
func (p *Page) guard(gtx C, size image.Point) {
	for {
		if _, ok := gtx.Event(pointer.Filter{Target: &p.guardTag, Kinds: pointer.Press | pointer.Release}); !ok {
			break
		}
	}
	open := p.popupOpen()
	if p.popWas && !open {
		p.guardUntil = gtx.Now.Add(clickGuard)
	}
	p.popWas = open
	if open || !gtx.Now.Before(p.guardUntil) {
		return
	}
	defer clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &p.guardTag)
	gtx.Execute(op.InvalidateCmd{At: p.guardUntil})
}
