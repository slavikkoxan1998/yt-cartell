package montage

import (
	"os"
	"path/filepath"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/studio"
)

// gapsView is the demo project with two failed scenes: a clip refused by
// the safety filter and a storyboard refused before anything was drawn.
func gapsView(pv *core.ProjectView) *core.ProjectView {
	cp := *pv
	cp.P.Scenes = append([]studio.Scene(nil), pv.P.Scenes...)
	cp.Ver += 1000
	cp.P.Scenes[0].Video = studio.Asset{Status: studio.StatusError,
		Error: "G-Labs відхилив запит: змінити промпт або референси — фільтр безпеки Google (PROMINENT_PERSON)"}
	if len(cp.P.Scenes) > 2 {
		refused := "G-Labs відхилив запит: змінити промпт або референси · G-Labs [400]: INVALID_ARGUMENT (flag=[3])"
		cp.P.Scenes[2].Frame = studio.Asset{Status: studio.StatusError, Error: refused}
		cp.P.Scenes[2].Board = studio.Asset{Status: studio.StatusError, Error: refused}
		cp.P.Scenes[2].Video = studio.EmptyAsset()
	}
	return &cp
}

// TestGapsDialog: «Змонтувати відео» with failed scenes opens the question
// (never a silent still), listing each scene with its exact error; the
// workspace banner's request opens it once.
func TestGapsDialog(t *testing.T) {
	e := demo(t)
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var id string
	for _, s := range e.List() {
		if p, _ := e.Get(s.ID); len(p.Scenes) > 2 && p.Input.Aspect != "9:16" {
			id = s.ID
		}
	}
	if id == "" {
		t.Skip("no demo project")
	}
	c.Refresh()
	pv := gapsView(c.Project(id))
	v := New().(*view)
	s := v.state(id)
	s.runMontage(c, pv) // asks; the headless ctx would refuse a real render anyway
	if !s.gaps.dlg.IsOpen() || len(s.gaps.gaps) != 2 || s.gaps.gaps[0].Number != 1 || !s.gaps.gaps[1].NoFrame {
		t.Fatalf("dialog %v gaps %+v", s.gaps.dlg.IsOpen(), s.gaps.gaps)
	}
	s.gaps.dlg.Close()
	c.UI(id).Gaps = true
	draw := harness(c, v, func() *core.ProjectView { return pv }, 1400, true)
	out := os.Getenv("GUISHOTS")
	if out == "" {
		out = t.TempDir()
	}
	if err := core.Shot(c, h, 1848, 1100, 1, filepath.Join(out, "gaps-dialog-1400-light.png"), draw); err != nil {
		t.Fatal(err)
	}
	if c.UI(id).Gaps || !s.gaps.dlg.IsOpen() {
		t.Fatal("the banner's request did not open the dialog")
	}
	s.gaps.dlg.Close()
	s.fake.fresh = true
	s.scrollTo = "final"
	if err := core.Shot(c, h, 1848, 1100, 1, filepath.Join(out, "gaps-final-1400-light.png"), draw); err != nil {
		t.Fatal(err)
	}
}
