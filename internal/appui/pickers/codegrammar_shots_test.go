package pickers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// TestCodeGrammarShots renders the «Режим виробництва» panel with the
// code-animation modes and the «Граматика анімації» field and panel (the
// grammar previews are rendered by the real renderer when a browser exists).
//
//	GUISHOTS=<dir> go test ./internal/appui/pickers -run TestCodeGrammarShots
func TestCodeGrammarShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the picker screenshots")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	p, err := e.Create("Код")
	if err != nil {
		t.Fatal(err)
	}
	type state struct {
		pipe    Pipeline
		grammar CodeGrammar
		value   string
		aspect  string
	}
	lay := func(s *state) func(gtx C) D {
		return func(gtx C) D {
			s.pipe.Cost = func(v string) string { return core.PipelineCost(e.PipelineCostOf(p, v)) }
			s.grammar.Aspect = s.aspect
			return layout.UniformInset(gui.Sp32).Layout(gtx, func(gtx C) D {
				gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(980))
				return gui.Grid{MinWidth: 300, Gap: gui.Sp16, Stretch: true}.Layout(gtx, 2, func(gtx C, i int) D {
					if i == 0 {
						return s.pipe.Layout(gtx, c, "Режим виробництва", studio.PipelineCode)
					}
					return s.grammar.Layout(gtx, c, "Граматика анімації", s.value)
				})
			})
		}
	}
	// Warm the grammar previews (rendered in the background, once).
	warm := &state{aspect: "16:9"}
	warm.grammar.Open()
	for i := 0; i < 2; i++ {
		_ = core.Shot(c, h, 1440, 900, 1, filepath.Join(t.TempDir(), "warm.png"), lay(warm))
		time.Sleep(200 * time.Millisecond)
	}
	only := os.Getenv("GUISHOTS_ONLY")
	shots := []struct {
		name string
		set  func(s *state)
	}{
		{"code-pipeline", func(s *state) { s.pipe.Open() }},
		{"code-grammar", func(s *state) { s.grammar.Open() }},
		{"code-grammar-chosen", func(s *state) { s.value = "t3_finance_chart" }},
		{"code-grammar-vertical", func(s *state) { s.aspect = "9:16"; s.grammar.Open() }},
	}
	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		c.Th.SetPaletteNow(dark)
		for _, sh := range shots {
			if only != "" && !strings.Contains(sh.name, only) {
				continue
			}
			if dark && sh.name != "code-grammar" {
				continue
			}
			s := &state{aspect: "16:9"}
			sh.set(s)
			path := filepath.Join(out, "pickers", sh.name+"-"+mode+"-1440x900.png")
			if err := core.Shot(c, h, 1440, 900, 1, path, lay(s)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
