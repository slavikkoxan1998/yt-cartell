package dialogs

// Headless screenshots of the sheets (skipped unless GUISHOTS=<dir>):
//
//	GUISHOTS=../../../screenshots/new/dialogs go test ./internal/appui/dialogs -run TestShots
//
// The engine is core.DemoEngine on a temp folder (fake keys, in-memory
// settings and profiles); nothing touches the network or the user's data.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/tts"
)

const webRoot = "../../../.."

var demoVoices = tts.VoiceList{Provider: "elevenlabs", Tier: "creator", CharsLeft: 81234, Voices: []tts.Voice{
	{ID: "v-olena", Name: "Олена", Gender: "female", Accent: "ukrainian", Category: "cloned", Usable: true},
	{ID: "v-rachel", Name: "Rachel", Gender: "female", Accent: "american", Category: "premade", Usable: true},
	{ID: "v-adam", Name: "Adam", Gender: "male", Accent: "american", Category: "premade", Usable: true},
}}

func demo(t *testing.T) (*core.Ctx, *core.HeadlessHost) {
	t.Helper()
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Shutdown(0) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	c.SetVoicesForTest(demoVoices)
	return c, h
}

// backdrop draws a stand-in for the app under the sheet: the rail and a
// couple of calm cards, so the scrim reads as it does in the app.
func backdrop(gtx C, c *core.Ctx) {
	th := c.Th
	size := gtx.Constraints.Max
	rw := 0
	if size.X >= gtx.Dp(900) {
		rw = gtx.Dp(92)
		gui.FillRect(gtx, image.Rect(0, 0, rw, size.Y), th.Pal.Card)
		gui.FillRect(gtx, image.Rect(rw-1, 0, rw, size.Y), th.Pal.Border)
	}
	pad := gtx.Dp(32)
	o := op.Offset(image.Pt(rw+pad, pad)).Push(gtx.Ops)
	g := gtx
	g.Constraints = layout.Constraints{Max: image.Pt(size.X-rw-2*pad, size.Y)}
	gui.VStack(g, gui.Sp24,
		gui.Text{Style: th.H1}.Widget(th, "Що знімемо сьогодні?"),
		func(gtx C) D {
			gtx.Constraints.Min.Y = gtx.Dp(260)
			return gui.Card{}.Layout(gtx, th, func(gtx C) D {
				return gui.SkeletonLines(gtx, th, 4)
			})
		},
	)
	o.Pop()
}

// mapProfile saves a user profile built from the map-history parts (map
// style and genre, very fast tempo, map camera, code animation with the map
// grammar, phrase subtitles) and returns its id.
func mapProfile(c *core.Ctx) string {
	p, err := c.E.SaveProfile(config.Profile{
		Name: "Історія на мапі", TargetSeconds: 100, Aspect: "9:16", Style: "history-map-pegs", Genre: "history map",
		Tempo: "rapid", Camera: "map-pan", ImageModel: "NARWHAL", Voiceover: true, NarratorStyle: "male-energetic",
		Tone: "witty-explainer", Notes: "Коротка дотепна історія на анімованій мапі з фігурками.",
		AudioMode: "voiceover", Content: "mixed", Overlays: "off", Pipeline: "code", CodeGrammar: "map",
		Transition: "none", SubtitlePreset: "phrase", Subtitles: "burn", MusicVolume: 0.08, Ducking: true,
	})
	if err != nil {
		return ""
	}
	return p.ID
}

// mapSheet opens the map-history profile (mapProfile).
func mapSheet(c *core.Ctx) core.Sheet {
	return core.Sheet{Kind: core.SheetProfile, ProfileID: mapProfile(c)}
}

type shotState struct {
	name  string
	sheet func(c *core.Ctx) core.Sheet
	setup func(c *core.Ctx, s *Sheets) // after Open, before the first frame
	// scroll wheels the sheet body down by this many px (lower sections).
	scroll float32
}

// pictures are synthetic photos for the materials (written once per run).
var pictures []string

// addPictures fills a materials editor like a user would.
func addPictures(m *materialsEditor) {
	kinds := []string{studio.MaterialCharacter, studio.MaterialLocation, studio.MaterialObject}
	names := []string{"Лисичка Руда", "Старий млин", "Срібний ключ"}
	notes := []string{"Руда лисиця з білим кінчиком хвоста, зелений шарф", "На пагорбі над річкою, вечір", "Старовинний, з різьбленням у формі місяця"}
	for i, path := range pictures {
		m.addFile(path)
		d := m.items[len(m.items)-1]
		d.kind = kinds[i%len(kinds)]
		d.name.SetText(names[i%len(names)])
		d.note.SetText(notes[i%len(notes)])
	}
}

// writePictures paints three small warm pictures (a figure, a landscape,
// an object) into dir.
func writePictures(t *testing.T, dir string) []string {
	t.Helper()
	paint := func(name string, f func(x, y int) color.NRGBA) string {
		img := image.NewNRGBA(image.Rect(0, 0, 240, 240))
		for y := 0; y < 240; y++ {
			for x := 0; x < 240; x++ {
				img.SetNRGBA(x, y, f(x, y))
			}
		}
		path := filepath.Join(dir, name)
		fh, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer fh.Close()
		if err := png.Encode(fh, img); err != nil {
			t.Fatal(err)
		}
		return path
	}
	sky := func(y int, a, b color.NRGBA) color.NRGBA { return gui.LerpColor(a, b, float32(y)/240) }
	return []string{
		paint("fox.png", func(x, y int) color.NRGBA {
			c := sky(y, color.NRGBA{255, 214, 170, 255}, color.NRGBA{255, 240, 220, 255})
			dx, dy := x-120, y-130
			if dx*dx+dy*dy < 70*70 {
				c = color.NRGBA{232, 116, 52, 255}
			}
			if dx*dx/4+(dy+60)*(dy+60) < 30*30 {
				c = color.NRGBA{250, 250, 245, 255}
			}
			return c
		}),
		paint("mill.png", func(x, y int) color.NRGBA {
			c := sky(y, color.NRGBA{126, 170, 214, 255}, color.NRGBA{230, 240, 248, 255})
			if y > 150+int(10*float64(x%60)/60) {
				c = color.NRGBA{98, 150, 96, 255}
			}
			if x > 100 && x < 140 && y > 80 && y < 170 {
				c = color.NRGBA{150, 104, 72, 255}
			}
			return c
		}),
		paint("key.png", func(x, y int) color.NRGBA {
			c := color.NRGBA{58, 52, 72, 255}
			dx, dy := x-90, y-120
			if dx*dx+dy*dy < 36*36 && dx*dx+dy*dy > 20*20 {
				c = color.NRGBA{210, 214, 222, 255}
			}
			if x > 120 && x < 200 && y > 112 && y < 128 {
				c = color.NRGBA{210, 214, 222, 255}
			}
			return c
		}),
	}
}

func firstProject(c *core.Ctx) string {
	for _, p := range c.List() {
		if p.Scenes > 0 {
			return p.ID
		}
	}
	return ""
}

var story = "Жила-була руда лисиця, яка щоночі виходила на пагорб і розмовляла з місяцем. Одного разу місяць не з'явився — і лисиця вирушила його шукати через ліс, річку й старе місто."

var shotStates = []shotState{
	{name: "new-open", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetNewProject} }},
	{name: "new-filled", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetNewProject, Text: story} },
		setup: func(c *core.Ctx, s *Sheets) { s.newProject.profileID = "starter-kids" }},
	{name: "new-error", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetNewProject} },
		setup: func(c *core.Ctx, s *Sheets) { s.newProject.start(c, true) }},
	{name: "new-language-open", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetNewProject, Text: story} },
		setup: func(c *core.Ctx, s *Sheets) { s.newProject.lang.Open() }},
	{name: "new-modes", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetNewProject, Text: story} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.newProject.soundV, s.newProject.soundSet = studio.AudioInVideo, true
			s.newProject.contentV, s.newProject.contentSet = pickers.ContentValue{Content: studio.ContentNoPeople, Overlays: "on"}, true
		}, scroll: 5000},
	{name: "profile-modes", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile, ProfileID: "starter-kids"} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.profile.draft.AudioMode, s.profile.draft.Voiceover = studio.AudioInVideo, false
			s.profile.draft.Content = studio.ContentMixed
		}, scroll: 900},
	{name: "profile-map", sheet: mapSheet},
	{name: "profile-map-voice", sheet: mapSheet, scroll: 900},
	{name: "profile-map-montage", sheet: mapSheet, scroll: 5000},
	{name: "profile-map-style-open", sheet: mapSheet,
		setup: func(c *core.Ctx, s *Sheets) { s.profile.style.OpenSearch("мап") }},
	{name: "profile-style-open", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile, ProfileID: "starter-kids"} },
		setup: func(c *core.Ctx, s *Sheets) { s.profile.style.Open() }},
	{name: "new-materials", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetNewProject, Text: story} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.newProject.profileID = "starter-kids"
			addPictures(&s.newProject.mat)
		}},
	{name: "translate-open", sheet: func(c *core.Ctx) core.Sheet {
		return core.Sheet{Kind: core.SheetTranslate, ProjectID: firstProject(c)}
	}},
	{name: "translate-pick", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetTranslate} },
		setup: func(c *core.Ctx, s *Sheets) { s.translate.voiceKeep = false }},
	{name: "translate-error", sheet: func(c *core.Ctx) core.Sheet {
		return core.Sheet{Kind: core.SheetTranslate, ProjectID: firstProject(c)}
	}, setup: func(c *core.Ctx, s *Sheets) {
		s.translate.err = "Цей проєкт уже мовою «Англійська» — вибери іншу мову"
	}},
	{name: "voice-open", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetOwnVoice} }},
	{name: "voice-filled", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetOwnVoice} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.ownVoice.path, s.ownVoice.size = `C:\Users\demo\Music\Лисиця і місяць — озвучка.mp3`, 7_340_032
			s.ownVoice.title.SetText("Лисиця і місяць")
		}},
	{name: "voice-transcript", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetOwnVoice} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.ownVoice.path, s.ownVoice.size = `C:\Users\demo\Music\Лисиця і місяць — озвучка.mp3`, 7_340_032
			s.ownVoice.tpath, s.ownVoice.tsize = `C:\Users\demo\Music\Лисиця і місяць.srt`, 4_096
			addPictures(&s.ownVoice.mat)
			s.ownVoice.mat.items = s.ownVoice.mat.items[:1]
		}},
	{name: "voice-error", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetOwnVoice} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.ownVoice.path, s.ownVoice.size = `C:\Users\demo\Music\intro.wav`, 120_000
			s.ownVoice.err = "Запис закороткий — потрібна хоча б секунда мовлення"
		}},
	{name: "voice-project", sheet: func(c *core.Ctx) core.Sheet {
		return core.Sheet{Kind: core.SheetOwnVoice, ProjectID: firstProject(c)}
	}},
	{name: "profile-new", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile} }},
	{name: "profile-edit", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile, ProfileID: "starter-shorts"} }},
	{name: "profile-from-project", sheet: func(c *core.Ctx) core.Sheet {
		return core.Sheet{Kind: core.SheetProfile, FromProject: firstProject(c)}
	}},
	{name: "profile-montage", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile, ProfileID: "starter-kids"} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.profile.draft.Subtitles = "burn"
			s.profile.draft.Transition = "dissolve"
			s.profile.draft.Filter = "warm"
		}, scroll: 5000},
	{name: "profile-ai", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.profile.ai.desc.SetText("Казки на ніч для малечі 4–7 років: 3 хвилини, м'яка акварель, українською, спокійний голос.")
			p := s.profile.draft
			p.Name = "Казки на ніч"
			p.TargetSeconds, p.Style, p.Genre, p.Tone, p.Tempo = 180, "watercolor", "bedtime", "kids", "slow"
			p.Language, p.Camera, p.MusicVolume = "uk", "medium", 0.12
			s.profile.applySuggestion(p, []string{
				"Акварель — м'які кольори без різких контрастів, добре для сну.",
				"3 хвилини — стільки уваги вистачає малечі перед сном.",
				"Повільний темп і лагідний оповідач заспокоюють.",
			})
		}},
	{name: "profile-error", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfile} },
		setup: func(c *core.Ctx, s *Sheets) { s.profile.name.SetText(""); s.profile.submit(c) }},
	{name: "profiles", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfiles} }},
	{name: "profiles-stacked", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetProfiles} },
		setup: func(c *core.Ctx, s *Sheets) {
			s.Open(c, core.Sheet{Kind: core.SheetProfile, ProfileID: "starter-kids"})
		}},
}

func TestShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the sheet screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	pictures = writePictures(t, t.TempDir())
	sizes := []struct{ w, h int }{{1920, 1080}, {1440, 900}, {390, 844}}
	for _, st := range shotStates {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range sizes {
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("dialogs-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					c.Th.SetPaletteNow(dark)
					s := New()
					s.Open(c, st.sheet(c))
					if st.setup != nil {
						st.setup(c, s)
					}
					draw := func(gtx C) D {
						backdrop(gtx, c)
						s.Layout(gtx, c)
						return D{Size: gtx.Constraints.Max}
					}
					opts := []gui.SnapOption{gui.Frames(6), gui.Settle(120)}
					if st.scroll != 0 {
						// Wheel over the sheet (it hugs the right edge).
						at := f32.Pt(float32(sz.w)-60, float32(sz.h)/2)
						opts = append(opts, gui.BeforeFrame(func(frame int, r *input.Router) {
							if frame == 3 {
								r.Queue(pointer.Event{Kind: pointer.Move, Position: at, Source: pointer.Mouse},
									pointer.Event{Kind: pointer.Scroll, Position: at, Source: pointer.Mouse, Scroll: f32.Pt(0, st.scroll)})
							}
						}))
					}
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, opts...); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// TestOpenReplacesAndStacks checks the sheet stacking rules.
func TestOpenReplacesAndStacks(t *testing.T) {
	c, _ := demo(t)
	s := New()
	s.Open(c, core.Sheet{Kind: core.SheetNewProject})
	s.Open(c, core.Sheet{Kind: core.SheetTranslate})
	if s.newProject.isOpen() || !s.translate.isOpen() {
		t.Fatal("opening a sheet must replace the open one")
	}
	s.Open(c, core.Sheet{Kind: core.SheetProfiles})
	s.Open(c, core.Sheet{Kind: core.SheetProfile, ProfileID: "starter-kids"})
	if !s.profiles.isOpen() || !s.profile.isOpen() || s.translate.isOpen() {
		t.Fatal("the profile editor must stack over the manager")
	}
	if top := s.order[len(s.order)-1]; top != sheet(&s.profile) {
		t.Fatal("the editor must be on top")
	}
	if s.profile.draft.ID != "starter-kids" || s.profile.isNew {
		t.Fatalf("editor draft = %+v", s.profile.draft)
	}
}

// TestProfileSave saves a new profile through the editor (in memory).
func TestProfileSave(t *testing.T) {
	c, h := demo(t)
	s := New()
	var got string
	s.Open(c, core.Sheet{Kind: core.SheetProfile, Done: func(id string) { got = id }})
	s.profile.name.SetText("Тестовий профіль")
	s.profile.draft.Aspect = "9:16"
	s.profile.submit(c)
	h.Flush()
	if got == "" {
		t.Fatalf("Done not called (err %q)", s.profile.err)
	}
	pr, ok := c.E.Profile(got)
	if !ok || pr.Name != "Тестовий профіль" || pr.Aspect != "9:16" {
		t.Fatalf("saved profile = %+v", pr)
	}
}

// TestNewProjectSteps creates a project step by step with the chosen values.
func TestNewProjectSteps(t *testing.T) {
	c, h := demo(t)
	s := New()
	var got string
	s.Open(c, core.Sheet{Kind: core.SheetNewProject, Text: story, Done: func(id string) { got = id }})
	s.newProject.profileID = ""
	s.newProject.language, s.newProject.langSet = "en", true
	s.newProject.seconds, s.newProject.durSet = 180, true
	s.newProject.aspectV, s.newProject.aspectSet = "9:16", true
	pictures = writePictures(t, t.TempDir())
	addPictures(&s.newProject.mat)
	if n := len(s.newProject.mat.inputs()); n != 3 {
		t.Fatalf("materials = %d, want 3", n)
	}
	s.newProject.start(c, false)
	h.Flush()
	if got == "" {
		t.Fatalf("no project (err %q)", s.newProject.err)
	}
	p, ok := c.E.Get(got)
	if !ok || p.Input.Language != "en" || p.Input.TargetSeconds != 180 || p.Input.Aspect != "9:16" || !strings.HasPrefix(p.Input.Text, "Жила-була") {
		t.Fatalf("project input = %+v", p.Input)
	}
	if r := h.Current(); r.Page != core.PageProject || r.ProjectID != got || r.Step != 1 {
		t.Fatalf("route = %+v", r)
	}
	if len(p.Input.Materials) != 3 || p.Input.Materials[0].Name != "Лисичка Руда" || p.Input.Materials[1].Kind != studio.MaterialLocation {
		t.Fatalf("materials = %+v", p.Input.Materials)
	}

	// «Мова: авто» over a profile that speaks English stays auto.
	pr, err := c.E.SaveProfile(config.Profile{Name: "Англійські казки", Language: "en", Aspect: "9:16", TargetSeconds: 120})
	if err != nil {
		t.Fatal(err)
	}
	got = ""
	s.Open(c, core.Sheet{Kind: core.SheetNewProject, Text: story, Done: func(id string) { got = id }})
	s.newProject.profileID = pr.ID
	s.newProject.language, s.newProject.langSet = "", true
	s.newProject.start(c, false)
	h.Flush()
	if p, ok = c.E.Get(got); !ok || p.Input.Language != "" || p.Input.Aspect != "9:16" || p.Input.TargetSeconds != 120 {
		t.Fatalf("auto language over the profile: %+v (err %q)", p.Input, s.newProject.err)
	}

	// «Типові налаштування» means no profile — not even the autopilot's
	// default one (checked on the options: the autopilot is not started here).
	if err := c.E.Config().Update(func(cf *config.Config) { cf.Autopilot.DefaultProfileID = pr.ID }); err != nil {
		t.Fatal(err)
	}
	s.Open(c, core.Sheet{Kind: core.SheetNewProject, Text: story})
	if s.newProject.profileID != pr.ID {
		t.Fatalf("the sheet starts on %q, want the autopilot's profile", s.newProject.profileID)
	}
	if opt := s.newProject.options(c, story, true); opt.NoProfile || opt.ProfileID != pr.ID || opt.Language != "en" || !opt.LanguageSet {
		t.Fatalf("options with the profile = %+v", opt)
	}
	s.newProject.profileID = "" // «Типові налаштування»
	if opt := s.newProject.options(c, story, true); !opt.NoProfile || opt.Language != "" || !opt.LanguageSet || opt.Aspect != "16:9" {
		t.Fatalf("options with the app defaults = %+v", opt)
	}
}

// TestProfileSuggestionHighlights applies an AI suggestion: the draft takes
// it, the changed fields are marked, a hand edit clears a mark, nothing is
// saved, and offline the request is refused without calling the engine.
func TestProfileSuggestionHighlights(t *testing.T) {
	c, _ := demo(t)
	s := New()
	s.Open(c, core.Sheet{Kind: core.SheetProfile})
	before := len(c.E.Profiles())
	p := s.profile.draft
	p.Name, p.Style, p.TargetSeconds = "Казки на ніч", "watercolor", 180
	s.profile.applySuggestion(p, []string{"Акварель — м'які кольори."})
	d := s.profile.draft
	if d.Style != "watercolor" || d.TargetSeconds != 180 || d.Name != "Казки на ніч" {
		t.Fatalf("draft = %+v", d)
	}
	for _, k := range []string{"style", "dur", "name"} {
		if !s.profile.ai.changed[k] {
			t.Fatalf("field %q not marked (%v)", k, s.profile.ai.changed)
		}
	}
	if s.profile.ai.changed["aspect"] {
		t.Fatal("an unchanged field is marked")
	}
	s.profile.touch("style")
	if s.profile.ai.changed["style"] {
		t.Fatal("a hand edit must clear the mark")
	}
	if len(c.E.Profiles()) != before {
		t.Fatal("a suggestion must not save anything")
	}
	s.profile.ai.desc.SetText("Шортси про космос")
	s.profile.suggest(c)
	if s.profile.ai.busy || s.profile.ai.err == "" {
		t.Fatalf("offline suggest: busy %v, err %q", s.profile.ai.busy, s.profile.ai.err)
	}
}
