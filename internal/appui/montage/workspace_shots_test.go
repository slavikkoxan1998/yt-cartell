package montage_test

// Step 5 inside the real project workspace (header, step tabs, context
// column), rendered like the app window: GUISHOTS=<dir> go test -run TestWorkspaceShots.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/project"
	"ytcartell/internal/studio"
)

func TestWorkspaceShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "../../../..")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var wide, tall string
	for _, s := range e.List() {
		p, _ := e.Get(s.ID)
		switch {
		case len(p.Scenes) == 0:
		case p.Input.Aspect == "9:16":
			tall = s.ID
		default:
			wide = s.ID
		}
	}
	if wide == "" {
		t.Skip("no demo project")
	}
	_ = e.Edit(wide, func(p *studio.Project) {
		p.Montage.CoverText = studio.CoverText{Title: "Він *повернувся* додому", Badge: "НОВЕ"}
		p.Montage.SubtitleStyle = studio.SubtitleStyle{Preset: "youtube"}
	})
	sizes := [][2]int{{1920, 1080}, {1440, 900}, {390, 844}}
	for _, id := range []string{wide, tall} {
		if id == "" {
			continue
		}
		name := "wide"
		if id == tall {
			name = "tall"
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range sizes {
				ws := project.NewWorkspace()
				c.Th.SetPaletteNow(dark)
				mode := "light"
				if dark {
					mode = "dark"
				}
				file := filepath.Join(out, fmt.Sprintf("workspace-%s-%s-%dx%d.png", name, mode, sz[0], sz[1]))
				draw := func(gtx core.C) core.D { return ws.Layout(gtx, c, id, 5) }
				if err := core.Shot(c, h, sz[0], sz[1], 1, file, draw); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
