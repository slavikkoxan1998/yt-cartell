package settings

// Settings → «Диктування» and the AssemblyAI choice of «Розпізнавання мови».
// Screenshots (skipped unless GUISHOTS=<dir>):
//
//	GUISHOTS=../../../screenshots/new/settings go test ./internal/appui/settings -run TestDictateShots

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/dictate"
	"ytcartell/internal/gui"
)

var demoMics = []dictate.Device{{Name: "Microphone (Realtek(R) Audio)", ID: "@device_cm_{1}"}, {Name: "Rode NT-USB", ID: "@device_cm_{2}"}}

var dictShots = []shotState{
	{name: "dictate", section: "dictate", setup: func(c *core.Ctx, p *Page) { p.dict.devs, p.dict.devDone = demoMics, true }},
	{name: "dictate-deepgram", section: "dictate", setup: func(c *core.Ctx, p *Page) {
		p.dict.devs, p.dict.devDone = demoMics, true
		p.dict.check = check{done: true, ok: true, msg: "Deepgram Nova-3: ключ працює"}
	}, config: func(cf *config.Config) {
		cf.Dictation = config.Dictation{Provider: "deepgram", Language: "uk", Device: "Rode NT-USB", SilenceSec: 15, DeepgramKey: "dg-demo-not-a-real-key-0000"}
	}},
	{name: "dictate-local", section: "dictate", setup: func(c *core.Ctx, p *Page) {
		p.dict.devErr = "Мікрофонів не знайдено — підключи мікрофон і натисни «Оновити»."
		p.dict.devDone = true
	}, config: func(cf *config.Config) { cf.Dictation = config.Dictation{Provider: "local", SilenceSec: -1} }},
	{name: "stt-assemblyai", section: "stt", config: func(cf *config.Config) {
		cf.STT.Provider, cf.STT.AssemblyEU = "assemblyai", true
	}},
}

func TestDictateShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the dictation settings screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, st := range dictShots {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range []shotSize{{1440, 900}, {390, 844}} {
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("settings-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if st.config != nil {
						if err := c.E.Config().Update(st.config); err != nil {
							t.Fatal(err)
						}
					}
					c.Th.SetPaletteNow(dark)
					p := New()
					frame := 0
					draw := shell(c, func(gtx C) D {
						frame++
						if frame == 2 && st.setup != nil {
							st.setup(c, p)
						}
						return p.Layout(gtx, c, st.section)
					})
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// TestDictateCard: the card lays out for every service at phone and
// desktop widths, picks write config.Dictation, «Автоматично» stores "".
func TestDictateCard(t *testing.T) {
	c, h := demo(t)
	p := New()
	lay := func(w int) {
		gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Constraints{Max: image.Pt(w, 4000)}}
		p.now = p.cfg.get(c)
		if d := p.dictCard(gtx, c, metrics{narrow: w < 600}); d.Size.Y == 0 {
			t.Fatalf("empty card at %d px", w)
		}
	}
	for _, id := range append([]string{""}, config.DictationProviders...) {
		p.cfg.set(c, func(cf *config.Config) { cf.Dictation.Provider = id }, nil)
		h.Flush()
		lay(700)
		lay(360)
	}
	if got := c.E.Config().Get().Dictation.Provider; got != "local" {
		t.Fatalf("provider %q", got)
	}
	if tabOf("dictate") != 0 {
		t.Fatal("the dictation card is not under «Підключення»")
	}
	if dictKeyURL(dictate.AssemblyAI) == "" || dictKeyURL(dictate.Local) != "" {
		t.Fatal("key links")
	}
}
