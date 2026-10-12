package project

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/assets"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/stt"
	"ytcartell/internal/studio"
	"ytcartell/internal/tts"
)

// webRoot is the repo folder of the web studio (…/YT-CARTELL-AI-MAKER).
const webRoot = "../../../.."

var demoVoices = tts.VoiceList{Provider: "elevenlabs", Tier: "creator", CharsLeft: 90000, Voices: []tts.Voice{
	{ID: "v-olena", Name: "Олена", Gender: "female", Accent: "ukrainian", Category: "cloned", PreviewURL: "https://example.invalid/olena.mp3", Usable: true},
	{ID: "v-rachel", Name: "Rachel", Gender: "female", Accent: "american", Age: "young", Category: "premade", PreviewURL: "https://example.invalid/rachel.mp3", Usable: true},
	{ID: "v-adam", Name: "Adam", Gender: "male", Accent: "american", Age: "middle_aged", Category: "premade", PreviewURL: "https://example.invalid/adam.mp3", Usable: true},
	{ID: "v-george", Name: "George", Gender: "male", Accent: "british", Category: "premade", PreviewURL: "https://example.invalid/george.mp3", Usable: true},
	{ID: "v-taras", Name: "Тарас", Gender: "male", Accent: "ukrainian", Category: "professional", PreviewURL: "https://example.invalid/taras.mp3", Usable: false},
}}

// frame stands in for the shell: a rail on the left (a bottom bar under
// 900 dp) around the workspace, like the app lays it out.
func frame(gtx C, th *gui.Theme, content layout.Widget) D {
	W, H := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	if W < gtx.Dp(900) {
		bar := gtx.Dp(64)
		cg := gtx
		cg.Constraints = layout.Exact(image.Pt(W, H-bar))
		content(cg)
		gui.FillRect(gtx, image.Rect(0, H-bar, W, H), th.Pal.Card)
		gui.FillRect(gtx, image.Rect(0, H-bar, W, H-bar+1), th.Pal.Border)
		return D{Size: image.Pt(W, H)}
	}
	rail := gtx.Dp(92)
	gui.FillRect(gtx, image.Rect(0, 0, rail, H), th.Pal.Card)
	gui.FillRect(gtx, image.Rect(rail-1, 0, rail, H), th.Pal.Border)
	o := op.Offset(image.Pt(rail, 0)).Push(gtx.Ops)
	cg := gtx
	cg.Constraints = layout.Exact(image.Pt(W-rail, H))
	content(cg)
	o.Pop()
	return D{Size: image.Pt(W, H)}
}

type shotEnv struct {
	t   *testing.T
	out string
	e   *studio.Engine
	c   *core.Ctx
	h   *core.HeadlessHost
	ws  *Workspace
}

func (s *shotEnv) shot(name string, w, h int, dark bool, id string, step int, prep func()) {
	s.t.Helper()
	s.c.Th.SetPaletteNow(dark)
	s.h.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: step})
	if prep != nil {
		prep()
	}
	mode := "light"
	if dark {
		mode = "dark"
	}
	path := filepath.Join(s.out, "project", fmt.Sprintf("%s-%s-%dx%d.png", name, mode, w, h))
	err := core.Shot(s.c, s.h, w, h, 1, path, func(gtx C) D {
		return frame(gtx, s.c.Th, func(gtx C) D { return s.ws.Layout(gtx, s.c, id, step) })
	})
	if err != nil {
		s.t.Fatal(err)
	}
}

// settle waits for the engine notifications of past edits (they are
// asynchronous and coalesced) and reads the fresh snapshot, so a faked
// snapshot is not replaced in the middle of a shot.
func (s *shotEnv) settle(id string) {
	time.Sleep(300 * time.Millisecond)
	s.h.Flush()
	s.c.Refresh()
	s.c.Project(id)
}

// refresh drops a faked snapshot (a no-op edit makes the engine notify).
func (s *shotEnv) refresh(id string) {
	_ = s.e.Edit(id, func(p *studio.Project) {})
	s.settle(id)
}

func TestWorkspaceShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the workspace screenshots")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	c.SetVoicesForTest(demoVoices)
	env := &shotEnv{t: t, out: out, e: e, c: c, h: h, ws: NewWorkspace()}
	only := os.Getenv("GUISHOTS_ONLY") // substring filter for quick iterations

	sizes := []struct{ w, h int }{{1920, 1080}, {1440, 900}, {390, 844}}
	want := func(name string) bool { return only == "" || strings.Contains(name, only) }

	projects := c.List()
	for k, p := range projects {
		name := fmt.Sprintf("demo%d", k+1)
		for _, step := range []int{1, 2} {
			for _, sz := range sizes {
				for _, dark := range []bool{false, true} {
					n := fmt.Sprintf("%s-step%d", name, step)
					if !want(n) || (dark && sz.w == 390 && k > 0) {
						continue
					}
					env.shot(n, sz.w, sz.h, dark, p.ID, step, nil)
				}
			}
		}
	}
	if len(projects) == 0 {
		t.Log("no demo projects (web studio data missing)")
		return
	}
	id := projects[0].ID
	// The project with the most scenes shows the scene cards.
	big := id
	for _, p := range projects {
		if pv := c.Project(p.ID); pv != nil && len(pv.P.Scenes) > len(c.Project(big).P.Scenes) {
			big = p.ID
		}
	}

	// Pickers opened on step 1.
	if want("picker") || want("menu") {
		ps := env.ws.state(id)
		closeAll := func() {
			ps.s1.style.Close()
			ps.s1.lang.Close()
			ps.s1.narrator.Close()
			ps.s1.dur.Close()
			ps.s1.vmode.Close()
			ps.s1.pipe.Close()
			env.ws.h.more.Close()
			env.ws.h.prof.Close()
		}
		tiles := func() { ps.s1.scroll.List.Position = layout.Position{First: 2} }
		for _, sz := range sizes {
			for _, dark := range []bool{false, true} {
				env.shot("picker-style", sz.w, sz.h, dark, id, 1, func() { closeAll(); tiles(); ps.s1.style.Open() })
				env.shot("picker-language", sz.w, sz.h, dark, id, 1, func() { closeAll(); tiles(); ps.s1.lang.Open() })
				env.shot("picker-voice", sz.w, sz.h, dark, id, 1, func() { closeAll(); tiles(); ps.s1.narrator.Open() })
			}
		}
		env.shot("picker-duration", 1920, 1080, false, id, 1, func() { closeAll(); ps.s1.dur.Open() })
		env.shot("picker-videomode", 1920, 1080, false, id, 1, func() {
			closeAll()
			ps.s1.scroll.List.Position = layout.Position{First: 2}
			ps.s1.vmode.Open()
		})
		env.shot("picker-pipeline", 1920, 1080, false, id, 1, func() {
			closeAll()
			ps.s1.scroll.List.Position = layout.Position{First: 2}
			ps.s1.pipe.Open()
		})
		env.shot("picker-pipeline", 390, 844, true, id, 1, func() {
			closeAll()
			ps.s1.scroll.List.Position = layout.Position{First: 2}
			ps.s1.pipe.Open()
		})
		ps.s1.scroll.List.Position = layout.Position{}
		env.shot("menu-more", 1440, 900, false, id, 1, func() { closeAll(); env.ws.h.more.Open() })
		env.shot("menu-profile", 1920, 1080, false, id, 1, func() { closeAll(); env.ws.h.prof.Open() })
		closeAll()
		env.shot("closed", 1440, 900, false, id, 1, nil) // lets the popovers finish closing
	}
	// Step 2: list mode and a jump to a scene.
	if want("scenes") {
		pv := c.Project(big)
		if pv != nil && len(pv.P.Scenes) > 2 {
			first, third := pv.P.Scenes[0].ID, pv.P.Scenes[2].ID
			for _, sz := range sizes {
				env.shot("scenes-grid", sz.w, sz.h, false, big, 2, func() { c.UI(big).ScrollTo = first })
			}
			env.shot("scenes-grid", 1920, 1080, true, big, 2, func() { c.UI(big).ScrollTo = first })
			c.UI(big).List = true
			env.shot("scenes-list", 1920, 1080, false, big, 2, func() { c.UI(big).ScrollTo = third })
			env.shot("scenes-list", 1440, 900, true, big, 2, func() { c.UI(big).ScrollTo = third })
			c.UI(big).List = false
			ps := env.ws.state(big)
			u := ps.s2.sceneState(third)
			u.open, u.rw = true, true
			env.shot("scenes-open", 1440, 900, false, big, 2, func() { c.UI(big).ScrollTo = third })
			u.open, u.rw = false, false
		}
	}

	// The lower part of step 1 (tiles, directions, plan action).
	if want("step1-lower") {
		ps := env.ws.state(big)
		for _, sz := range sizes {
			env.shot("step1-lower", sz.w, sz.h, false, big, 1, func() { ps.s1.scroll.List.Position = layout.Position{First: 2} })
		}
		env.shot("step1-lower", 1920, 1080, true, big, 1, func() { ps.s1.scroll.List.Position = layout.Position{First: 2} })
		ps.s1.scroll.List.Position = layout.Position{}
	}
	// Scene states: a line too long for its clip, a slice of the own recording.
	if want("scene-states") {
		fake := func() {
			pv := c.Project(big)
			if pv == nil || len(pv.P.Scenes) < 2 {
				return
			}
			pv.P.Scenes[0].Narration = "Українська земля завжди родила хліб, даруючи життя та достаток працьовитим родинам, які з покоління в покоління плекали кожен колосок, кожне зернятко й кожну борозну."
			pv.P.Scenes[1].AudioSource, pv.P.Scenes[1].VoiceStart, pv.P.Scenes[1].VoiceEnd = studio.AudioUpload, 12.3, 20.1
			c.UI(big).ScrollTo = pv.P.Scenes[0].ID
		}
		env.shot("scene-states", 1920, 1080, false, big, 2, fake)
		env.shot("scene-states", 1440, 900, true, big, 2, fake)
		env.refresh(big)
	}
	// «Матеріали»: three of the user's pictures (one still being described)
	// named like the plan's cast, so step 2 shows "з твого фото".
	if want("materials") {
		// The Ukrainian demo (Тарас, Осінній ліс, Рідна хата) if it is there.
		uk := big
		for _, p := range projects {
			if pv := c.Project(p.ID); pv != nil && fromName(pv.P.Characters, "Тарас") {
				uk = p.ID
			}
		}
		// Sample pictures: crops of the built-in style previews (an autumn
		// forest, a fox, a cottage), written into the demo project's own
		// folder (a temp dir).
		dir := e.Dir(uk)
		samplePicture(t, filepath.Join(dir, "materials", "fox.png"), "realism", image.Rect(130, 250, 350, 470))
		samplePicture(t, filepath.Join(dir, "materials", "forest.png"), "realism", image.Rect(0, 0, 260, 260))
		samplePicture(t, filepath.Join(dir, "materials", "home.png"), "cinematic", image.Rect(300, 90, 530, 320))
		samplePicture(t, filepath.Join(dir, "materials", "book.png"), "gouache-book", image.Rectangle{})
		samplePicture(t, filepath.Join(dir, "materials", "scarf.png"), "cinematic", image.Rect(160, 80, 340, 260))
		mats := []studio.Material{
			{ID: "mat-fox", Kind: studio.MaterialCharacter, Name: "Лисиця", Note: "Балакуча й хитра, але добра. Говорить лагідним жіночим голосом.",
				Path: "materials/fox.png", Look: "Руда лисиця з білими грудьми й пухнастим хвостом сидить на стежці серед опалого листя й уважно дивиться вгору."},
			{ID: "mat-forest", Kind: studio.MaterialLocation, Name: "Осінній ліс", Note: "Ранок, туман між деревами",
				Path: "materials/forest.png", Look: "Густий листяний ліс восени: помаранчеві й жовті крони, білі стовбури беріз, м'яке світло крізь гілля."},
			{ID: "mat-home", Kind: studio.MaterialLocation, Name: "Рідна хата", Note: "",
				Path: "materials/home.png", Look: "Дерев'яна хатинка з димарем і теплим світлом у вікнах серед осіннього лісу; вечірні сутінки."},
			{ID: "mat-book", Kind: studio.MaterialStyle, Name: "", Note: "Теплі кольори й м'які мазки, як у дитячій книжці",
				Path: "materials/book.png"}, // still being described
			{ID: "mat-scarf", Kind: studio.MaterialObject, Name: "Червоний шарф", Note: "Подарунок бабусі — Тарас не знімає його в жодній сцені",
				Path: "materials/scarf.png"}, // the model could not see it: the note is the look
		}
		// The engine is describing one picture right now.
		describing := materialDescribing
		materialDescribing = func(c *core.Ctx, id, materialID string) bool { return materialID == "mat-book" }
		defer func() { materialDescribing = describing }()
		// The plan's cast and places bound to the materials.
		bind := map[string]string{"Лисиця": "mat-fox", "Осінній ліс": "mat-forest", "Рідна хата": "mat-home"}
		ps := env.ws.state(uk)
		fake := func() {
			pv := c.Project(uk)
			if pv == nil {
				return
			}
			pv.P.Input.Materials = mats
			for i := range pv.P.Characters {
				pv.P.Characters[i].MaterialID = bind[pv.P.Characters[i].Name]
			}
			for i := range pv.P.Locations {
				pv.P.Locations[i].MaterialID = bind[pv.P.Locations[i].Name]
			}
		}
		at := func(first int, fill bool) func() {
			return func() {
				if fill {
					fake()
				} else if pv := c.Project(uk); pv != nil {
					pv.P.Input.Materials = nil // the demo has none
				}
				ps.s1.scroll.List.Position = layout.Position{First: first}
			}
		}
		for _, sz := range sizes {
			for _, dark := range []bool{false, true} {
				env.shot("materials-empty", sz.w, sz.h, dark, uk, 1, at(1, false))
				env.shot("materials-step1", sz.w, sz.h, dark, uk, 1, at(0, true))
				env.shot("materials-card", sz.w, sz.h, dark, uk, 1, at(1, true))
				env.shot("materials-step2", sz.w, sz.h, dark, uk, 2, fake)
			}
		}
		env.shot("materials-card-tall", 390, 2400, false, uk, 1, at(1, true)) // the whole card on a phone
		env.shot("materials-step2-places", 1440, 900, false, uk, 2, func() { fake(); ps.s2.scroll.List.Position = layout.Position{First: elLocHead} })
		env.shot("materials-step2-places", 390, 844, true, uk, 2, func() { fake(); ps.s2.scroll.List.Position = layout.Position{First: elLocHead} })
		ps.s2.scroll.List.Position = layout.Position{}
		ps.s1.scroll.List.Position = layout.Position{}
		env.refresh(uk)
	}
	// Dynamic clip lengths (Settings): estimates and chosen clips per scene.
	if want("dynamic") {
		_ = e.Config().Update(func(cfg *config.Config) { cfg.Video.Dynamic = true })
		env.settle(big)
		fake := func() {
			pv := c.Project(big)
			if pv == nil {
				return
			}
			for i := range pv.P.Scenes {
				pv.P.Scenes[i].ClipLen = []int{6, 8, 6, 10, 4}[i%5]
			}
			c.UI(big).ScrollTo = pv.P.Scenes[0].ID
		}
		env.shot("dynamic-step2", 1920, 1080, false, big, 2, fake)
		env.shot("dynamic-step2", 1440, 900, true, big, 2, fake)
		env.shot("dynamic-step1", 1440, 900, false, big, 1, func() { env.ws.state(big).s1.scroll.List.Position = layout.Position{First: 3} })
		_ = e.Config().Update(func(cfg *config.Config) { cfg.Video.Dynamic = false })
		env.ws.state(big).s1.scroll.List.Position = layout.Position{}
		env.refresh(big)
	}
	// Medium widths: the context column becomes a drawer.
	if want("medium") {
		env.shot("medium-step2", 1280, 800, false, big, 2, nil)
		env.shot("medium-drawer", 1280, 800, false, big, 2, func() { env.ws.drawer.Open() })
		env.shot("medium-drawer", 390, 844, false, big, 2, func() { env.ws.drawer.Open() })
		env.ws.drawer.Close()
		env.shot("closed", 1280, 800, false, big, 2, nil)
	}
	// The sound and content modes: step 1 with the «Звук» / «Що в кадрі»
	// pickers, step 2 of an in-video film (lines), a film without people
	// (objects) and motion graphics (on-screen text).
	if want("modes") {
		ps := env.ws.state(big)
		tiles := func() { ps.s1.scroll.List.Position = layout.Position{First: 3} }
		closeAll := func() { ps.s1.sound.Close(); ps.s1.content.Close() }
		inVideo := func(pv *core.ProjectView) {
			p := &pv.P
			p.Input.AudioMode, p.Input.Voiceover = studio.AudioInVideo, false
			p.NarratorVoice = "A calm, warm grandmother's voice, slow pace."
			var people []string
			for i := range p.Characters {
				p.Characters[i].Kind = ""
				p.Characters[i].Voice = []string{"a boy of about 8, soft high voice, a little shy", "a sly, gentle female voice, quick pace"}[i%2]
				people = append(people, p.Characters[i].ID)
			}
			says := [][]string{{"Хто тут? Я не боюся!", "whispers, scared"}, {"Не бійся. Я знаю дорогу додому.", "warm, calm"}}
			for i := range p.Scenes {
				s := &p.Scenes[i]
				s.Narration = ""
				s.Lines = nil
				if i%3 == 2 || len(people) == 0 {
					s.Lines = []studio.Line{{Speaker: "narrator", Text: "І вони пішли разом крізь туман."}}
					continue
				}
				for k, l := range says {
					s.Lines = append(s.Lines, studio.Line{Speaker: people[k%len(people)], Text: l[0], Delivery: l[1]})
				}
			}
			c.UI(big).ScrollTo = p.Scenes[0].ID
		}
		noPeople := func(pv *core.ProjectView) {
			p := &pv.P
			p.Input.Content = studio.ContentNoPeople
			names := []string{"Старий компас", "Карта 1912 року"}
			descs := []string{"A vintage brass compass with a cracked glass lid and a green patina.", "A yellowed hand-drawn map with ink coastlines."}
			for i := range p.Characters {
				p.Characters[i].Kind = studio.KindObject
				p.Characters[i].Name, p.Characters[i].Description, p.Characters[i].Role = names[i%2], descs[i%2], "річ, що веде історію"
			}
		}
		motion := func(pv *core.ProjectView) {
			p := &pv.P
			p.Input.Content, p.Input.Style = studio.ContentMotion, "motion-graphics"
			p.Characters = nil
			texts := []studio.OnScreen{
				{Title: "Населення Землі", Value: "8,1 млрд", Caption: "станом на 2025 рік", Anim: studio.OverlayCount},
				{Title: "Як працює фотосинтез", Caption: "світло, вода й CO₂", Anim: studio.OverlayType},
				{Value: "73%", Caption: "людей не знають цього", Anim: studio.OverlayPop},
			}
			for i := range p.Scenes {
				p.Scenes[i].OnScreen = texts[i%len(texts)]
			}
			c.UI(big).ScrollTo = p.Scenes[0].ID
		}
		fake := func(fns ...func(pv *core.ProjectView)) func() {
			return func() {
				env.refresh(big) // the previous shot's fake goes
				if pv := c.Project(big); pv != nil && len(pv.P.Scenes) > 2 {
					for _, fn := range fns {
						fn(pv)
					}
				}
			}
		}
		for _, dark := range []bool{false, true} {
			env.shot("modes-step1-sound", 1440, 900, dark, big, 1, func() { closeAll(); fake(inVideo)(); tiles(); ps.s1.sound.Open() })
			env.shot("modes-step1-content", 1440, 900, dark, big, 1, func() { closeAll(); fake(motion)(); tiles(); ps.s1.content.Open() })
		}
		env.shot("modes-step1-sound", 390, 844, false, big, 1, func() { closeAll(); fake(inVideo)(); tiles(); ps.s1.sound.Open() })
		closeAll()
		env.shot("modes-step1-tiles", 1920, 1080, false, big, 1, func() { fake(inVideo, noPeople)(); tiles() })
		ps.s1.scroll.List.Position = layout.Position{}
		for _, sz := range sizes {
			env.shot("modes-step2-invideo", sz.w, sz.h, false, big, 2, fake(inVideo))
		}
		env.shot("modes-step2-invideo", 1440, 900, true, big, 2, fake(inVideo))
		c.UI(big).List = true
		env.shot("modes-step2-invideo-list", 1920, 1080, false, big, 2, fake(inVideo))
		c.UI(big).List = false
		env.shot("modes-step2-invideo-cast", 1440, 900, false, big, 2, func() {
			fake(inVideo)()
			c.UI(big).ScrollTo = ""
			ps.s2.scroll.List.Position = layout.Position{First: elCastHead}
		})
		env.shot("modes-step2-nopeople", 1440, 900, false, big, 2, func() { fake(noPeople)(); ps.s2.scroll.List.Position = layout.Position{} })
		env.shot("modes-step2-nopeople", 390, 844, true, big, 2, func() { fake(noPeople)(); ps.s2.scroll.List.Position = layout.Position{} })
		for _, sz := range sizes {
			env.shot("modes-step2-motion", sz.w, sz.h, false, big, 2, fake(motion))
		}
		env.shot("modes-step2-motion", 1920, 1080, true, big, 2, fake(motion))
		env.refresh(big)
	}
	// «Мультиголос»: the voices card on steps 1 and 2 (narrator, «Диктор 2»,
	// the speaking cast, speaker colours) and the scenes' speaker parts.
	if want("multivoice") {
		ps := env.ws.state(big)
		multi := func(on bool) func() {
			return func() {
				env.refresh(big)
				pv := c.Project(big)
				if pv == nil || len(pv.P.Scenes) < 3 {
					return
				}
				p := &pv.P
				p.Input.Mode, p.Input.AudioMode, p.Input.Voiceover = studio.ModeStory, studio.AudioVoiceover, true
				p.Input.MultiVoice = on
				if !on {
					return
				}
				p.Input.NarratorVoiceID, p.Input.ExtraNarrators = "v-adam", []string{"v-rachel"}
				p.Montage.SubtitleStyle.SpeakerColors = true
				var people []string
				for i := range p.Characters {
					p.Characters[i].Kind = ""
					p.Characters[i].VoiceID = []string{"v-george", "v-olena"}[i%2]
					people = append(people, p.Characters[i].ID)
				}
				for i := range p.Scenes {
					s := &p.Scenes[i]
					parts := []studio.Line{{Speaker: "narrator", Text: firstText(s.Narration, "Ліс стояв тихий і золотий.")}}
					switch {
					case i%3 == 0:
						parts = append(parts, studio.Line{Speaker: "narrator2", Text: "Того року осінь прийшла на два тижні раніше."})
					case len(people) > 0:
						parts = append(parts, studio.Line{Speaker: people[i%len(people)], Text: "Не бійся, я знаю дорогу додому!", Delivery: "warm, calm"})
					}
					s.Parts = parts
				}
				c.UI(big).ScrollTo = ""
			}
		}
		step1Card := func(on bool) func() {
			return func() { multi(on)(); ps.s1.scroll.List.Position = layout.Position{First: 4} }
		}
		for _, dark := range []bool{false, true} {
			env.shot("multivoice-step1", 1440, 900, dark, big, 1, step1Card(true))
			env.shot("multivoice-step2", 1920, 1080, dark, big, 2, func() { multi(true)(); ps.s2.scroll.List.Position = layout.Position{} })
			env.shot("multivoice-step2-scenes", 1440, 900, dark, big, 2, func() {
				multi(true)()
				ps.s2.scroll.List.Position = layout.Position{First: elFirstScene}
			})
		}
		env.shot("multivoice-step1-off", 1440, 900, false, big, 1, step1Card(false))
		env.shot("multivoice-step1", 390, 844, false, big, 1, step1Card(true))
		env.shot("multivoice-step2-scenes", 390, 844, true, big, 2, func() {
			multi(true)()
			ps.s2.scroll.List.Position = layout.Position{First: elFirstScene}
		})
		ps.s1.scroll.List.Position = layout.Position{}
		ps.s2.scroll.List.Position = layout.Position{}
		env.refresh(big)
	}
	// A fresh project, empty.
	fresh, err := e.Create("Новий проєкт")
	if err != nil {
		t.Fatal(err)
	}
	if want("new") {
		for _, sz := range sizes {
			env.shot("new-step1", sz.w, sz.h, false, fresh.ID, 1, nil)
		}
		env.shot("new-step1", 1440, 900, true, fresh.ID, 1, nil)
		env.shot("new-step2", 1440, 900, false, fresh.ID, 2, nil)
	}
	// The plan being written (a faked running job).
	if want("running") {
		_ = e.Edit(fresh.ID, func(p *studio.Project) {
			p.Input.Text = "Жила-була руда лисиця, яка щоночі виходила на пагорб і розмовляла з місяцем. Одного разу місяць не з'явився — і лисиця вирушила його шукати через ліс, річку й старе місто."
		})
		env.refresh(fresh.ID)
		fake := func() {
			pv := c.Project(fresh.ID)
			pv.P.Job = &studio.Job{Kind: studio.JobPlan, Status: studio.JobRunning, Progress: 42,
				Message: "Режисер читає історію · 8 сцен", StartedAt: time.Now().Add(-25 * time.Second).UnixMilli()}
		}
		env.shot("running-step2", 1920, 1080, false, fresh.ID, 2, fake)
		env.shot("running-step1", 1440, 900, false, fresh.ID, 1, fake)
		env.shot("running-step2", 390, 844, true, fresh.ID, 2, fake)
		env.refresh(fresh.ID)
	}
	// A failed job.
	if want("error") {
		fake := func() {
			pv := c.Project(fresh.ID)
			pv.P.Job = &studio.Job{Kind: studio.JobPlan, Status: studio.JobError,
				Error:     "Текстовий ендпоінт недоступний — перевір, що CarteLink запущено.",
				StartedAt: time.Now().Add(-time.Minute).UnixMilli(), FinishedAt: time.Now().Add(-20 * time.Second).UnixMilli()}
		}
		env.shot("error-step1", 1440, 900, false, fresh.ID, 1, fake)
		env.shot("error-step1", 1920, 1080, true, fresh.ID, 1, fake)
		env.refresh(fresh.ID)
		// The autopilot stopped before the montage: scenes failed, the user decides.
		blocked := func() {
			pv := c.Project(fresh.ID)
			pv.P.Job = &studio.Job{Kind: studio.JobAutopilot, Status: studio.JobError, Blocked: studio.BlockedGaps,
				Error:     "Автопілот зупинився перед монтажем. Не всі сцени готові до монтажу (1 з них): Сцена 1 — кліп: G-Labs: PROMINENT_PERSON.",
				StartedAt: time.Now().Add(-time.Minute).UnixMilli(), FinishedAt: time.Now().Add(-20 * time.Second).UnixMilli()}
		}
		env.shot("blocked-step1", 1440, 900, false, fresh.ID, 1, blocked)
		env.shot("blocked-step1", 390, 844, false, fresh.ID, 1, blocked)
		env.refresh(fresh.ID)
	}
	// "Своя озвучка": no file yet, then a recording with its text.
	if want("voice") {
		_ = e.Edit(fresh.ID, func(p *studio.Project) { p.Input.Mode = studio.ModeVoice })
		env.refresh(fresh.ID)
		env.shot("voice-empty-step1", 1440, 900, false, fresh.ID, 1, nil)
		fake := func() {
			pv := c.Project(fresh.ID)
			pv.P.Input.VoiceFile, pv.P.Input.VoiceFileName = "audio/voice.mp3", "Моя озвучка — лисиця.mp3"
			pv.P.Input.TargetSeconds = 84
		}
		env.shot("voice-file-step1", 1920, 1080, false, fresh.ID, 1, fake)
		env.shot("voice-file-step1", 390, 844, false, fresh.ID, 1, fake)
		env.refresh(fresh.ID)

		// A ready transcript with timecodes attached (SRT): no recognition.
		tr := demoTranscript()
		attached := func() {
			pv := c.Project(fresh.ID)
			in := &pv.P.Input
			in.VoiceFile, in.VoiceFileName = "audio/voice.mp3", "Моя озвучка — лисиця.mp3"
			in.TranscriptFile, in.TargetSeconds, in.Text = "audio/transcript-lysytsia.srt", 84, tr.Text
			v := &env.ws.state(fresh.ID).s1.voice
			v.tr, v.trOK, v.trKey = tr, true, transcriptKey(pv)
		}
		for _, sz := range sizes {
			for _, dark := range []bool{false, true} {
				env.shot("voice-transcript-step1", sz.w, sz.h, dark, fresh.ID, 1, attached)
			}
		}
		env.refresh(fresh.ID)
	}
	// A deleted project.
	if want("missing") {
		env.shot("missing", 1440, 900, false, "no-such-project", 1, nil)
	}
}

// fromName reports whether the cast has a character with that name.
func fromName(cast []studio.Character, name string) bool {
	for _, ch := range cast {
		if ch.Name == name {
			return true
		}
	}
	return false
}

// demoTranscript is a transcript as a ready SRT file gives it: phrases with
// their times (word timings spread evenly over each phrase).
func demoTranscript() stt.Transcript {
	phrases := []string{
		"Жила-була руда лисиця.",
		"Щоночі вона виходила на пагорб і розмовляла з місяцем.",
		"Місяць розповідав їй про далекі міста й тихі ріки.",
		"А лисиця — про ліс, про зайців і про перший сніг.",
		"Та одного вечора місяць не з'явився.",
		"Лисиця чекала до ранку, але небо лишалося порожнім.",
		"Тоді вона вирушила його шукати.",
		"Через темний ліс, де навіть сови мовчали.",
		"Через річку, холодну й швидку.",
		"Аж до старого міста, де ліхтарі світили замість зірок.",
		"Там, на даху найвищої вежі, сидів маленький хлопчик.",
		"Він тримав у руках срібну кулю — і вона тихо сяяла.",
	}
	var tr stt.Transcript
	at := 0.4
	var texts []string
	for _, p := range phrases {
		ws := strings.Fields(p)
		dur := 0.42 * float64(len(ws))
		tr.Segments = append(tr.Segments, stt.Segment{Text: p, Start: at, End: at + dur})
		for i, w := range ws {
			s := at + dur*float64(i)/float64(len(ws))
			tr.Words = append(tr.Words, stt.Word{Text: w, Start: s, End: s + dur/float64(len(ws))})
		}
		texts = append(texts, p)
		at += dur + 1.1
	}
	tr.Text, tr.Language, tr.Duration = strings.Join(texts, " "), "uk", at
	return tr
}

// samplePicture writes a crop of a built-in style preview as a PNG (the
// whole picture for an empty rectangle).
func samplePicture(t *testing.T, path, style string, r image.Rectangle) {
	t.Helper()
	src, err := jpeg.Decode(bytes.NewReader(assets.StylePreview(style)))
	if err != nil {
		t.Logf("no %s preview: %v", style, err)
		return
	}
	if r.Empty() {
		r = src.Bounds()
	}
	r = r.Intersect(src.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
