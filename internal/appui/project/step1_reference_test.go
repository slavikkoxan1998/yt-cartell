package project

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/reference"
	"ytcartell/internal/studio"
)

const sampleBreakdown = `{"seen":true,"title":"How I hook viewers in 3 seconds","durationSeconds":95,"language":"en","aspect":"9:16",
"summary":"Ведучий показує три прийоми хука для шортсів.","hook":{"end":6,"what":"Ведучий різко нахиляється до камери: «Stop scrolling!»","why":"Пряме звертання й рух у перший кадр"},
"structure":[{"start":0,"end":6,"name":"Хук","purpose":"зупинити гортання"},{"start":6,"end":70,"name":"Три прийоми","purpose":"користь"},{"start":70,"end":95,"name":"Заклик","purpose":"підписка"}],
"scenes":[{"start":0,"end":6,"shot":"крупний план, різкий наїзд","visual":"Чоловік у червоній худі нахиляється до камери в яскравій студії","onScreenText":"STOP","speaker":"on-camera","speech":"Stop scrolling!","sound":"удар басу"},
{"start":6,"end":24,"shot":"середній план","visual":"Той самий чоловік за білим столом, жестикулює","onScreenText":"TRICK #1","speaker":"on-camera","speech":"Trick number one: start in motion."},
{"start":24,"end":70,"shot":"швидкі склейки","visual":"Приклади відео на телефоні, руки гортають стрічку","speaker":"voice-over","speech":"Look how fast these grab you.","sound":"лоу-фай біт"},
{"start":70,"end":95,"shot":"крупний план","visual":"Ведучий усміхається й показує на кнопку підписки","speaker":"on-camera","speech":"Follow for part two."}],
"cutsPerMinute":24,"avgShotSeconds":2.5,"people":"characters","audioMode":"invideo","narrationStyle":"Енергійно, на ти, короткі фрази",
"music":"Лоу-фай біт, удари на склейках","visualStyle":"Живе відео, яскраве студійне світло, великі білі титри","palette":"червоний, білий, чорний",
"pacing":"Дуже швидко на початку, далі рівно","whyItWorks":["Рух і звертання в першу секунду","Нумеровані прийоми тримають увагу","Титри дублюють головне"]}`

// writeThumb paints a stand-in YouTube thumbnail.
func writeThumb(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 480, 270))
	for y := 0; y < 270; y++ {
		for x := 0; x < 480; x++ {
			c := color.RGBA{uint8(40 + y/3), uint8(30 + x/12), 60, 255}
			if (x-300)*(x-300)+(y-130)*(y-130) < 70*70 {
				c = color.RGBA{220, 50, 50, 255}
			}
			if x > 40 && x < 220 && y > 180 && y < 230 {
				c = color.RGBA{250, 250, 250, 255}
			}
			img.Set(x, y, c)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatal(err)
	}
}

// remakeProject makes a demo remake project (analysed and scripted).
func remakeProject(t *testing.T, e *studio.Engine) string {
	t.Helper()
	b, err := reference.ParseBreakdown(sampleBreakdown)
	if err != nil {
		t.Fatal(err)
	}
	text := b.Text(func(code string) string {
		if l, ok := studio.LanguageByCode(code); ok {
			return strings.ToLower(l.Label)
		}
		return ""
	})
	p, err := e.CreateProject(studio.NewProjectOptions{Title: "Хук для кав'ярні", Mode: studio.ModeStory,
		Text: "Хук для кав'ярні\n\nСцена 1 · Стоп-кадр\nКадр: Бариста в зеленому фартуху різко нахиляється до камери над стійкою з кавою.\nТекст на екрані: «СТОП»\nГоворить у кадрі: «Стоп! Ти ще п'єш каву без цього?»\n\nСцена 2 · Прийом перший\nКадр: Та сама бариста за дерев'яною стійкою показує на кавомолку.\nГоворить у кадрі: «Перший секрет — свіжий помел.»"})
	if err != nil {
		t.Fatal(err)
	}
	writeThumb(t, filepath.Join(e.Dir(p.ID), "reference", "thumb.jpg"))
	if err := e.Edit(p.ID, func(q *studio.Project) {
		q.Input.Aspect, q.Input.TargetSeconds = "9:16", 96
		q.Reference = studio.Reference{URL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", VideoID: "dQw4w9WgXcQ",
			Wish: "Про мою кав'ярню, героїня — бариста", Fidelity: studio.FidelityExact, KeepLength: true,
			Title: b.Title, Channel: "Hook Lab", Duration: 95, Aspect: "9:16", Language: "en", Scenes: len(b.Scenes),
			CutsPerMin: b.CutsPerMin, People: b.People, AudioMode: b.AudioMode, Via: "Gemini · gemini-3.8-flash",
			Thumb: "reference/thumb.jpg", Breakdown: text, Stage: "scripted", AnalyzedAt: 1, ScriptedAt: 1}
	}); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// TestRemakeCardShots renders «Розбір прикладу» in its states (GUISHOTS).
func TestRemakeCardShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the remake card")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	c.SetVoicesForTest(demoVoices)
	env := &shotEnv{t: t, out: out, e: e, c: c, h: h, ws: NewWorkspace()}
	id := remakeProject(t, e)

	// A remake still running: a fresh project without a breakdown.
	runID := remakeProject(t, e)
	_ = e.Edit(runID, func(q *studio.Project) {
		q.Title = "Перезйомка за прикладом"
		q.Input.Text = ""
		q.Reference.Breakdown, q.Reference.Stage, q.Reference.Scenes, q.Reference.Via = "", "", 0, ""
		q.Reference.Autopilot = true
	})
	env.settle(id)
	env.settle(runID)
	fake := func(pid string, j *studio.Job) func() {
		return func() {
			env.settle(pid)
			if pv := c.Project(pid); pv != nil {
				pv.P.Job = j
			}
		}
	}
	running := &studio.Job{Kind: studio.JobRemake, Status: studio.JobRunning, Progress: 38, Message: "Перезйомка · Дивиться частину 0:00–1:35…"}
	failed := &studio.Job{Kind: studio.JobRemake, Status: studio.JobError, Error: "Це приватне відео — його бачить лише автор. Встав посилання на публічне відео."}
	ps := env.ws.state(id)
	for _, sz := range []struct{ w, h int }{{1440, 900}, {390, 844}} {
		for _, dark := range []bool{false, true} {
			env.shot("remake-done", sz.w, sz.h, dark, id, 1, func() { ps.s1.ref.closed = false })
			env.shot("remake-running", sz.w, sz.h, dark, runID, 1, fake(runID, running))
			env.shot("remake-failed", sz.w, sz.h, dark, runID, 1, fake(runID, failed))
		}
	}
	env.shot("remake-collapsed", 1440, 900, false, id, 1, func() { ps.s1.ref.closed = true })
	ps.s1.ref.closed = false
}

// TestRemakeCardStates checks what the card offers in each state.
func TestRemakeCardStates(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	id := remakeProject(t, e)
	h.Flush()
	c.Refresh()
	pv := c.Project(id)
	if pv == nil || !pv.P.Reference.Active() {
		t.Fatal("no remake project")
	}
	if note := refNote(pv.P.Reference, false); !strings.Contains(note, "«How I hook viewers in 3 seconds»") || !strings.Contains(note, "1:35") || !strings.Contains(note, "Точна копія") {
		t.Fatalf("note = %q", note)
	}
	ws := NewWorkspace()
	draw := func(gtx C) D { return ws.Layout(gtx, c, id, 1) }
	if _, err := gui.Snapshot(1440, 900, 1, core.Frame(c, h, draw), gui.Frames(2)); err != nil {
		t.Fatal(err)
	}
	// A remake action in the demo context is refused, not started.
	ws.state(id).s1.ref.run(c, func() error { t.Fatal("must not run offline"); return nil })
	if soundLabel(studio.AudioInVideo) == "" || peopleLabel(studio.ContentMotion) == "" {
		t.Fatal("labels")
	}
}
