package montage

// «Експортувати в…» in the step: the project menu's request opens the
// drawer with what the project has; picking an editor in the menu opens it
// for that editor; switches and the drawer's options reach the engine.

import (
	"testing"

	"gioui.org/io/input"

	"ytcartell/internal/studio"
)

func TestExportOpensFromProjectMenu(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	c.UI(id).Export = true // what the project's ⋯ menu sets before navigating here
	drive(t, c, h, v, id, 3, func(int, *input.Router) {})
	st := &v.state(id).exp
	if c.UI(id).Export {
		t.Fatal("the request was not taken")
	}
	if !st.drawer.IsOpen() || st.target != studio.ExportResolve || st.phase != exportIdle {
		t.Fatalf("drawer open=%v target=%q phase=%v", st.drawer.IsOpen(), st.target, st.phase)
	}
	want, _ := e.ExportCheck(id)
	if st.ready.Scenes != want.Scenes || st.ready.Clips != want.Clips || st.optVoice != (want.Voices > 0) || st.optMusic != want.Music {
		t.Fatalf("readiness %+v vs %+v, switches voice=%v music=%v", st.ready, want, st.optVoice, st.optMusic)
	}
}

func TestExportMenuPicksEditor(t *testing.T) {
	e, c, h, id := demoCtx(t)
	defer e.Shutdown(0)
	defer c.Close()
	v := New().(*view)
	s := v.state(id)
	// A pick of the menu (as the popover reports it) opens the drawer for CapCut.
	s.openExport(c, c.Project(id), studio.ExportCapCut)
	drive(t, c, h, v, id, 3, func(int, *input.Router) {})
	if !s.exp.drawer.IsOpen() || s.exp.target != studio.ExportCapCut {
		t.Fatalf("drawer %v target %q", s.exp.drawer.IsOpen(), s.exp.target)
	}
	// CapCut always gets the stems and never a timeline file; switches pass through.
	s.exp.optMusic, s.exp.optZip, s.exp.dir = false, true, `C:\Export`
	o := s.exp.options()
	if !o.Stems || o.Music || !o.Zip || o.Dir != `C:\Export` || o.Target != studio.ExportCapCut {
		t.Fatalf("options %+v", o)
	}
	if o.Partial != !s.exp.ready.Complete() {
		t.Fatalf("partial %v for readiness %+v", o.Partial, s.exp.ready)
	}
	// An offline ctx (headless renders, tests) never starts an export.
	s.startExport(c, c.Project(id))
	if s.exp.phase != exportIdle || s.exp.stopFn != nil {
		t.Fatalf("offline export started: phase %v", s.exp.phase)
	}
}
