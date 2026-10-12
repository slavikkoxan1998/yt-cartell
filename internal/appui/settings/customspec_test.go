package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/io/key"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/studio"
)

const panelSpec = `{"submit": {"path": "/v1/clips", "body": {"prompt": "{{prompt}}", "image": "{{start_image}}", "seconds": "{{seconds}}"}},
 "result": "url", "caps": {"lengths": [6, 12]}}`

// The spec editor saves on blur like every field (and the clip length
// follows its caps); an AI draft is saved only by «Зберегти опис» (filling
// the empty address and model fields), «Відкинути» brings the saved spec
// back; the AI setup never runs offline.
func TestUniversalPanel(t *testing.T) {
	c, h := demo(t)
	if err := c.E.Config().Update(func(cf *config.Config) {
		cf.Video.Provider, cf.Video.Length = "custom", 8
		cf.Custom.VideoPreset, cf.Custom.VideoBaseURL, cf.Custom.VideoModel = providers.PresetUniversal, "", ""
	}); err != nil {
		t.Fatal(err)
	}
	d := &driver{t: t, c: c, h: h, p: New(), section: "video"}
	d.frame(nil)
	d.frame(nil)
	sp := &d.p.video.custom.spec
	ed := &sp.editor.Editor
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: ed.Len()}, Text: panelSpec})
	d.frame(nil)
	h.Flush()
	if c.E.Config().Get().Custom.VideoSpec != "" {
		t.Fatal("saved while typing")
	}
	d.click(600, 20) // the header: the editor loses focus
	d.frame(nil)
	d.frame(nil)
	h.Flush()
	got := c.E.Config().Get()
	if got.Custom.VideoSpec != panelSpec || got.Video.Length != 6 {
		t.Fatalf("spec %q, length %d", got.Custom.VideoSpec, got.Video.Length)
	}
	if sum, _, err := sp.check(ed.Text(), true); err != nil || !strings.Contains(sum, "/v1/clips") {
		t.Fatalf("status: %q %v", sum, err)
	}

	// An AI draft waits for review.
	tpl, _ := providers.FindSpecTemplate(true, "async-jobs")
	draft := func(spec string) {
		sp.draft = &studio.CustomSpecSuggestion{Spec: spec, BaseURL: "https://api.acme.test/v1", Model: "acme-2", Notes: []string{"Статус вгадано."}}
		sp.editor.edited, sp.editor.commit = false, nil
		sp.editor.SetText(spec)
	}
	draft(tpl.Spec)
	d.frame(nil)
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.click(600, 20)
	d.frame(nil)
	d.p.flushFields()
	h.Flush()
	if c.E.Config().Get().Custom.VideoSpec != panelSpec {
		t.Fatal("a draft was saved without «Зберегти опис»")
	}
	if ed.Text() != tpl.Spec {
		t.Fatal("the draft left the editor")
	}
	sp.save.Click()
	d.frame(nil)
	d.frame(nil)
	h.Flush()
	got = c.E.Config().Get()
	if got.Custom.VideoSpec != strings.TrimSpace(tpl.Spec) || got.Custom.VideoBaseURL != "https://api.acme.test/v1" || got.Custom.VideoModel != "acme-2" || sp.draft != nil {
		t.Fatalf("saved draft: %+v", got.Custom)
	}
	if got.Video.Length != 5 { // 6 s snapped to the template's 5/10
		t.Fatalf("length %d", got.Video.Length)
	}
	// «Відкинути»: the saved spec comes back; a filled address stays.
	draft(`{"broken": `)
	d.frame(nil)
	sp.discard.Click()
	d.frame(nil)
	d.frame(nil)
	if sp.draft != nil || ed.Text() != strings.TrimSpace(tpl.Spec) {
		t.Fatalf("discard: editor %q", ed.Text())
	}

	// Offline (demo): the AI setup explains and does nothing.
	sp.docs.SetText("https://docs.example.test/api")
	sp.run.Click()
	d.frame(nil)
	if sp.busy || !strings.Contains(sp.err, "демо-режимі") {
		t.Fatalf("offline AI: busy %v err %q", sp.busy, sp.err)
	}

	// The caps line reads the spec.
	pre, _ := providers.FindVideoPreset(providers.PresetUniversal)
	if l := capsLine(pre, "", panelSpec, true); l != "Цей опис знімає: кліпи 6, 12 с · 16:9, 9:16." {
		t.Fatalf("caps line %q", l)
	}
	if l := capsLine(pre, "", "", false); !strings.Contains(l, "без опорних фото") {
		t.Fatalf("image caps line %q", l)
	}
	if !isTemplate(true, strings.TrimSpace(tpl.Spec)) || isTemplate(true, panelSpec) {
		t.Fatal("isTemplate")
	}
}

// Headless screenshots of the universal panel (GUISHOTS=<dir>): light and
// dark, 1440 and 390 dp wide, tall enough to show the whole panel.
//
//	GUISHOTS=../../../screenshots/new/settings go test ./internal/appui/settings -run TestShotsUniversal
func TestShotsUniversal(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the settings screenshots")
	}
	async, _ := providers.FindSpecTemplate(true, "async-jobs")
	broken := `{
  "submit": {"path": "/v1/tasks", "body": {"text": "{{prompt}}", "key": "{{key}}"}},
  "id": "id",
  "state": "status",
  "result": "output.url"
}`
	type state struct {
		name  string
		spec  string
		setup func(p *Page)
	}
	states := []state{
		{name: "template", spec: async.Spec},
		{name: "errors", spec: broken},
		{name: "empty", spec: "", setup: func(p *Page) {
			sp := &p.video.custom.spec
			sp.docs.SetText("https://api.acme.ai/docs")
		}},
		{name: "ai-busy", spec: "", setup: func(p *Page) {
			sp := &p.video.custom.spec
			sp.docs.SetText("https://api.acme.ai/docs")
			sp.busy, sp.step = true, "Знайшов опис API: https://api.acme.ai/openapi.json · ШІ складає опис API…"
		}},
		{name: "ai-draft", spec: "", setup: func(p *Page) {
			sp := &p.video.custom.spec
			sp.docs.SetText("https://api.acme.ai/docs")
			d := studio.CustomSpecSuggestion{Spec: async.Spec, BaseURL: "https://api.acme.ai/v1", Model: "acme-video-2",
				Notes: []string{"Взято OpenAPI-опис API", "Значення статусу «RUNNING» вгадано — перевір у документації.", "Скасування задачі в документації не описано."}}
			sp.draft = &d
			sp.editor.commit = nil
			sp.editor.SetText(d.Spec)
		}},
	}
	for _, st := range states {
		for _, dark := range []bool{false, true} {
			for _, sz := range []shotSize{{1440, 2000}, {390, 3400}} {
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("settings-universal-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if err := c.E.Config().Update(func(cf *config.Config) {
						cf.Image.Provider, cf.Video.Provider = "glabs", "custom"
						cf.Custom.VideoPreset, cf.Custom.VideoSpec = providers.PresetUniversal, st.spec
						if st.spec != "" {
							cf.Custom.VideoBaseURL, cf.Custom.VideoModel = "https://api.acme.ai/v1", "acme-video-2"
						}
						cf.Custom.VideoKey = "demo-universal-key-not-real-0000"
					}); err != nil {
						t.Fatal(err)
					}
					c.Th.SetPaletteNow(dark)
					p := New()
					frame := 0
					draw := shell(c, func(gtx C) D {
						frame++
						if frame == 2 && st.setup != nil {
							st.setup(p)
						}
						return p.Layout(gtx, c, "video")
					})
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
