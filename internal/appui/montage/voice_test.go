package montage

// The voice lane and the line in the inspector, driven through the real
// input router: moving and trimming a line, Esc and the right click, the
// click that plays it, editing its text and «Переозвучити» (offline: refused).

import (
	"math"
	"testing"
	"unicode/utf8"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"ytcartell/internal/studio"
)

// voiceGeom is the voice block of scene id in the last frame ("" = the
// first line wide enough to grab by its body and both edges).
func voiceGeom(s *proj, id string) (clipGeom, bool) {
	for _, cg := range s.tl.geo.clips {
		if id == "" && cg.voice.Dx() >= 8*int(vHandleDp) || id != "" && cg.id == id && !cg.voice.Empty() {
			return cg, true
		}
	}
	return clipGeom{}, false
}

func sceneByID(t *testing.T, e *studio.Engine, pid, id string) studio.Scene {
	t.Helper()
	p, _ := e.Get(pid)
	for _, sc := range p.Scenes {
		if sc.ID == id {
			return sc
		}
	}
	t.Fatalf("scene %s not found", id)
	return studio.Scene{}
}

func itemByID(e *studio.Engine, pid, id string) studio.TimelineItem {
	p, _ := e.Get(pid)
	for _, it := range e.Timeline(p) {
		if it.SceneID == id {
			return it
		}
	}
	return studio.TimelineItem{}
}

func tenths(v float64) bool { return math.Abs(v*10-math.Round(v*10)) < 1e-6 }

func TestVoiceShiftByDrag(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	var target string
	var before studio.TimelineItem
	var moving bool
	var x float32 // the press point (the block moves under the pointer)
	drive(t, c, h, v, id, 14, func(frame int, r *input.Router) {
		area, found := c.Th.AnchorRect("mont.tracks")
		cg, ok := voiceGeom(s, target)
		if !found || !ok {
			return
		}
		if target == "" {
			target = cg.id
			before = itemByID(e, id, target)
		}
		g := &s.tl.geo
		y := float32(area.Min.Y + g.voiceY + g.voiceH/2)
		dx := 1.0 * g.pxs // one second later
		switch frame {
		case 5:
			x = float32(area.Min.X) + float32(cg.voice.Min.X+cg.voice.Max.X)/2
			press(r, pointer.Move, x, y)
			press(r, pointer.Press, x, y)
		case 6:
			press(r, pointer.Move, x+dx/2, y)
		case 7:
			press(r, pointer.Move, x+dx, y)
			moving = s.tl.drag.kind == dragVoiceMove && s.tl.drag.id == target
		case 8:
			press(r, pointer.Release, x+dx, y)
		}
	})
	if target == "" {
		t.Skip("the demo project has no voice lines")
	}
	if !moving {
		t.Fatal("dragging the voice block did not move the line")
	}
	sc := sceneByID(t, e, id, target)
	if math.Abs(sc.VoiceShift-(before.VoiceShift+1)) > 0.051 || !tenths(sc.VoiceShift) {
		t.Fatalf("VoiceShift = %v, want ≈ %v on a 0.1 s grid", sc.VoiceShift, before.VoiceShift+1)
	}
	if sc.VoiceTrimIn != 0 || sc.VoiceTrimOut != 0 {
		t.Fatalf("a move trimmed the line: in %v out %v", sc.VoiceTrimIn, sc.VoiceTrimOut)
	}
	after := itemByID(e, id, target)
	if math.Abs(after.VoiceStart-(before.VoiceStart+1)) > 0.06 {
		t.Fatalf("line starts at %v, want ≈ %v", after.VoiceStart, before.VoiceStart+1)
	}
	if s.film.item(target) != nil && c.UI(id).Scene != target {
		t.Fatalf("the moved line's scene is not selected (%q)", c.UI(id).Scene)
	}
}

func TestVoiceTrimByDrag(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	var target string
	var before, mid studio.TimelineItem
	var trimming bool
	var at float32 // the press point of the gesture (the edge follows the pointer)
	drive(t, c, h, v, id, 26, func(frame int, r *input.Router) {
		area, found := c.Th.AnchorRect("mont.tracks")
		cg, ok := voiceGeom(s, target)
		if !found || !ok {
			return
		}
		if target == "" {
			target = cg.id
			before = itemByID(e, id, target)
		}
		g := &s.tl.geo
		y := float32(area.Min.Y + g.voiceY + g.voiceH/2)
		left := float32(area.Min.X + cg.voice.Min.X + 2)
		right := float32(area.Min.X + cg.voice.Max.X - 2)
		switch frame {
		// 1. The tail: one second off.
		case 5:
			at = right
			press(r, pointer.Move, at, y)
			press(r, pointer.Press, at, y)
		case 6:
			press(r, pointer.Move, at-0.5*g.pxs, y)
			trimming = s.tl.drag.kind == dragVoiceTrimR
		case 7:
			press(r, pointer.Move, at-1.0*g.pxs, y)
		case 8:
			press(r, pointer.Release, at-1.0*g.pxs, y)
		// 2. The head: half a second off, the words stay where they were.
		case 12:
			mid = itemByID(e, id, target)
			at = left
			press(r, pointer.Move, at, y)
			press(r, pointer.Press, at, y)
		case 13:
			press(r, pointer.Move, at+0.5*g.pxs, y)
		case 14:
			press(r, pointer.Release, at+0.5*g.pxs, y)
		// 3. The tail again, far past the head: 0.3 s of the line stay.
		case 19:
			press(r, pointer.Move, right, y)
			press(r, pointer.Press, right, y)
		case 20:
			press(r, pointer.Move, float32(area.Min.X+2), y)
		case 21:
			press(r, pointer.Release, float32(area.Min.X+2), y)
		}
	})
	if target == "" {
		t.Skip("the demo project has no voice lines")
	}
	if !trimming {
		t.Fatal("the right edge of the voice block did not start a trim")
	}
	if mid.VoiceTrimOut-before.VoiceTrimOut < 0.95 || mid.VoiceTrimOut-before.VoiceTrimOut > 1.05 {
		t.Fatalf("tail trim = %v, want ≈ 1 more than %v", mid.VoiceTrimOut, before.VoiceTrimOut)
	}
	sc := sceneByID(t, e, id, target)
	if math.Abs(sc.VoiceTrimIn-(before.VoiceTrimIn+0.5)) > 0.051 || !tenths(sc.VoiceTrimIn) {
		t.Fatalf("head trim = %v, want ≈ %v", sc.VoiceTrimIn, before.VoiceTrimIn+0.5)
	}
	after := itemByID(e, id, target)
	// The words did not move: the recording starts where it did.
	if got, want := after.VoiceStart-after.VoiceTrimIn, mid.VoiceStart-mid.VoiceTrimIn; math.Abs(got-want) > 0.01 {
		t.Fatalf("head trim moved the words: recording starts at %v, was %v", got, want)
	}
	if left := after.VoiceDuration; left < minVoiceKeep-1e-6 || left > minVoiceKeep+0.1+1e-6 {
		t.Fatalf("after trimming past the head %v s of the line are left, want 0.3…0.4", left)
	}
}

func TestVoiceDragEscAndRightClick(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	var target string
	var cancelled bool
	var afterEsc studio.Scene
	drive(t, c, h, v, id, 20, func(frame int, r *input.Router) {
		area, found := c.Th.AnchorRect("mont.tracks")
		cg, ok := voiceGeom(s, target)
		if !found || !ok {
			return
		}
		if target == "" {
			target = cg.id
			// Moved and trimmed before: the right click puts it all back.
			_ = e.Edit(id, func(p *studio.Project) {
				for i := range p.Scenes {
					if p.Scenes[i].ID == target {
						p.Scenes[i].VoiceShift, p.Scenes[i].VoiceTrimOut = 0.7, 0.4
					}
				}
			})
			c.Refresh()
			return
		}
		g := &s.tl.geo
		y := float32(area.Min.Y + g.voiceY + g.voiceH/2)
		x := float32(area.Min.X) + float32(cg.voice.Min.X+cg.voice.Max.X)/2
		switch frame {
		case 6: // a move…
			press(r, pointer.Move, x, y)
			press(r, pointer.Press, x, y)
		case 7:
			press(r, pointer.Move, x+40, y)
		case 8: // …cancelled with Esc: the release stores nothing
			r.Queue(key.Event{Name: key.NameEscape, State: key.Press})
		case 9:
			cancelled = s.tl.drag.kind == dragDead
			press(r, pointer.Move, x+80, y)
			press(r, pointer.Release, x+80, y)
		case 13:
			afterEsc = sceneByID(t, e, id, target)
			r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonSecondary, Position: f32.Pt(x, y), PointerID: 1})
		case 14:
			r.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(x, y), PointerID: 1})
		}
	})
	if target == "" {
		t.Skip("the demo project has no voice lines")
	}
	if !cancelled {
		t.Fatal("Esc did not cancel the drag")
	}
	if afterEsc.VoiceShift != 0.7 || afterEsc.VoiceTrimOut != 0.4 {
		t.Fatalf("the cancelled drag stored a change: shift %v out %v", afterEsc.VoiceShift, afterEsc.VoiceTrimOut)
	}
	sc := sceneByID(t, e, id, target)
	if sc.VoiceShift != 0 || sc.VoiceTrimIn != 0 || sc.VoiceTrimOut != 0 {
		t.Fatalf("after Esc and the right click: shift %v in %v out %v, want all 0", sc.VoiceShift, sc.VoiceTrimIn, sc.VoiceTrimOut)
	}
}

func TestVoiceClickSelectsScene(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	var target string
	drive(t, c, h, v, id, 10, func(frame int, r *input.Router) {
		area, found := c.Th.AnchorRect("mont.tracks")
		if !found {
			return
		}
		g := &s.tl.geo
		if target == "" {
			// The last line: not the scene selected by default.
			for _, cg := range g.clips {
				if !cg.voice.Empty() {
					target = cg.id
				}
			}
		}
		cg, ok := voiceGeom(s, target)
		if !ok {
			return
		}
		y := float32(area.Min.Y + g.voiceY + g.voiceH/2)
		x := float32(area.Min.X) + float32(cg.voice.Min.X+cg.voice.Max.X)/2
		switch frame {
		case 5:
			press(r, pointer.Move, x, y)
			press(r, pointer.Press, x, y)
		case 6:
			press(r, pointer.Release, x, y)
		}
	})
	if target == "" {
		t.Skip("the demo project has no voice lines")
	}
	if got := c.UI(id).Scene; got != target {
		t.Fatalf("selected %q, want the clicked line's scene %q", got, target)
	}
	if s.tl.voice != target {
		t.Fatalf("the click did not play the line (%q)", s.tl.voice)
	}
	if sc := sceneByID(t, e, id, target); sc.VoiceShift != 0 {
		t.Fatalf("a click moved the line by %v", sc.VoiceShift)
	}
}

// TestLineTextAndRevoice edits the line in the inspector (typed, Ctrl+Enter
// stores it) and presses «Переозвучити»: the headless context is offline, so
// c.Regen refuses — a toast, no job — while the text stays stored.
func TestLineTextAndRevoice(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	if !c.Offline {
		t.Fatal("the headless context must be offline")
	}
	v := New().(*view)
	p, _ := e.Get(id)
	var sc0 studio.Scene
	for _, sc := range p.Scenes {
		if sc.Audio.Path != "" {
			sc0 = sc
			break
		}
	}
	if sc0.ID == "" {
		t.Skip("the demo project has no voiced scene")
	}
	if !p.Input.Voiceover {
		t.Skip("the demo project has the voice-over off")
	}
	c.UI(id).Scene = sc0.ID
	const text = "Нова репліка для перевірки."
	toasts := -1
	drive(t, c, h, v, id, 22, func(frame int, r *input.Router) {
		switch frame {
		case 4, 12:
			name := "mont.line"
			if frame == 12 {
				name = "mont.revoice"
				toasts = c.Th.Toasts()
			}
			a, ok := c.Th.AnchorRect(name)
			if !ok {
				t.Errorf("anchor %s not drawn", name)
				return
			}
			x, y := float32(a.Min.X+a.Max.X)/2, float32(a.Min.Y+a.Max.Y)/2
			if frame == 4 {
				y = float32(a.Min.Y + 20) // in the text, above the hint
			}
			press(r, pointer.Move, x, y)
			press(r, pointer.Press, x, y)
			press(r, pointer.Release, x, y)
		case 6:
			r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: utf8.RuneCountInString(sc0.Narration)}, Text: text})
		case 8:
			r.Queue(key.Event{Name: key.NameReturn, Modifiers: key.ModShortcut, State: key.Press})
		}
	})
	if got := sceneByID(t, e, id, sc0.ID).Narration; got != text {
		t.Fatalf("narration = %q, want %q", got, text)
	}
	if toasts < 0 || c.Th.Toasts() <= toasts {
		t.Fatalf("«Переозвучити» offline showed no refusal (toasts %d → %d)", toasts, c.Th.Toasts())
	}
	if q := e.Queue(); len(q.Running) != 0 {
		t.Fatalf("«Переозвучити» offline started %d jobs", len(q.Running))
	}
	if sc := sceneByID(t, e, id, sc0.ID); sc.Audio.Path != sc0.Audio.Path || sc.Audio.Status == studio.StatusRunning || sc.Audio.Status == studio.StatusQueued {
		t.Fatalf("the voice changed offline: %+v", sc.Audio)
	}
}
