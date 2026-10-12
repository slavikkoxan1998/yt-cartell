package produce_test

// Whole-window screenshots of steps 3 and 4 inside the real shell and
// project workspace (rail, header, job banner, tabs, context column):
//
//	GUISHOTS=<dir> go test ./internal/appui/produce -run TestWindowShots

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/appui"
	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
)

func TestWindowShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "../../../..")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	var wide, vert string
	for _, s := range e.List() {
		switch {
		case s.Scenes > 0 && s.Aspect == "9:16":
			vert = s.ID
		case s.Scenes > 0:
			wide = s.ID
		}
	}
	if wide == "" || vert == "" {
		t.Skip("the web studio demo projects are not there")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, sc := range []struct {
		name string
		id   string
		step int
	}{{"step3", wide, 3}, {"step3-vertical", vert, 3}, {"step4", wide, 4}, {"step4-vertical", vert, 4}} {
		for _, sz := range [][2]int{{1920, 1080}, {1440, 900}, {390, 844}} {
			for _, dark := range []bool{false, true} {
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("window-%s-%s-%dx%d", sc.name, theme, sz[0], sz[1])
				if only != "" && !strings.Contains(name, only) {
					continue
				}
				if dark && sz[0] != 1920 {
					continue
				}
				s := appui.Headless(e)
				s.SetDark(dark)
				s.Navigate(core.Route{Page: core.PageProject, ProjectID: sc.id, Step: sc.step})
				img, err := s.Snapshot(sz[0], sz[1], gui.Frames(6), gui.Settle(90))
				s.Close()
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if err := gui.SavePNG(img, filepath.Join(out, name+".png")); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
