package studio

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func fileExists(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		t.Fatalf("missing file %s (%v)", path, err)
	}
}

// TestPipeline runs the whole studio against mocks: plan → frames (refs
// first) → regen → rewrite → render → montage → cover.
func TestPipeline(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e

	var notes, listNotes atomic.Int32
	unsub := e.Subscribe(func(id string) {
		if id == "" {
			listNotes.Add(1)
		} else {
			notes.Add(1)
		}
	})
	defer unsub()

	p, err := e.Create("  ")
	must(t, err)
	if p.Title != "Новий проєкт" || p.Step != 1 || p.Input.Aspect != "16:9" || p.Montage.Transition != "fade" {
		t.Fatalf("new project: %+v", p)
	}
	id := p.ID
	fileExists(t, filepath.Join(e.Dir(id), projectFile))
	must(t, e.Edit(id, func(p *Project) {
		p.Input.Text = "Маленький хлопчик Тарас загубився в осінньому лісі. Його знайшла лисиця і вивела додому."
		p.Input.TargetSeconds = 24
		p.Input.Style = "watercolor"
	}))
	p, _ = e.Get(id)
	if n, clip, words := e.Budget(p); n != 3 || clip != 8 || words != 18 {
		t.Fatalf("budget %d %d %d", n, clip, words)
	}

	// ---- plan
	must(t, e.Run(id, StagePlan, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "План готовий: 3 сцен ≈ 0:24" {
		t.Fatalf("plan message %q", p.Job.Message)
	}
	if p.Title != "Стежка додому" || p.Logline == "" || p.Step != 2 {
		t.Fatalf("plan header: %q %q %d", p.Title, p.Logline, p.Step)
	}
	if !strings.HasPrefix(p.StyleGuide, "Watercolor painting:") || !strings.HasSuffix(p.StyleGuide, "Soft autumn palette, warm light.") {
		t.Fatalf("style guide %q", p.StyleGuide)
	}
	if len(p.Characters) != 2 || len(p.Locations) != 1 || len(p.Scenes) != 3 {
		t.Fatalf("plan sizes: %d chars %d locs %d scenes", len(p.Characters), len(p.Locations), len(p.Scenes))
	}
	taras, fox, forest := p.Characters[0], p.Characters[1], p.Locations[0]
	if taras.Tag != "taras" || fox.Tag != "lysytsia" || forest.Tag != "osinnii_lis" || fox.Gender != "female" {
		t.Fatalf("tags: %+v %+v %+v", taras, fox, forest)
	}
	if p.Input.NarratorVoiceID != "v-adam" || fox.VoiceID != "v-bella" || taras.VoiceID != "" {
		t.Fatalf("voices: narrator %q fox %q taras %q", p.Input.NarratorVoiceID, fox.VoiceID, taras.VoiceID)
	}
	s1 := p.Scenes[1]
	if s1.Speaker != fox.ID || len(s1.CharacterIDs) != 2 || s1.CharacterIDs[1] != fox.ID || s1.LocationID != forest.ID {
		t.Fatalf("scene 2 cast: %+v", s1)
	}
	for i, s := range p.Scenes {
		if !strings.Contains(s.ImagePrompt, "scene "+string(rune('1'+i))) || s.MotionPrompt == "" || s.Shot != "medium shot" || s.Narration == "" {
			t.Fatalf("scene %d prompts: %+v", i, s)
		}
	}
	env.text.mu.Lock()
	bp := env.text.biblePrompts[0]
	env.text.mu.Unlock()
	for _, want := range []string{"EXACTLY 3 scenes", "Visual style of the film: Watercolor painting", "at most 18 words", "### INPUT\nМаленький хлопчик"} {
		if !strings.Contains(bp, want) {
			t.Fatalf("bible prompt lacks %q", want)
		}
	}

	// ---- frames (references first) + busy
	must(t, e.Run(id, StageFrames, false, ""))
	if err := e.Run(id, StageRender, false, ""); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "Розкадровка") {
		t.Fatalf("second job must be busy, got %v", err)
	}
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Розкадровка готова (3 кадрів)" || p.Step != 3 {
		t.Fatalf("frames: %q step %d", p.Job.Message, p.Step)
	}
	for _, a := range []Asset{p.Characters[0].Image, p.Characters[1].Image, p.Locations[0].Image, p.Scenes[0].Frame, p.Scenes[1].Frame, p.Scenes[2].Frame} {
		if a.Status != StatusDone || !strings.HasPrefix(a.Path, "images/") || a.UpdatedAt == 0 || a.Progress != 100 {
			t.Fatalf("asset %+v", a)
		}
		fileExists(t, e.Abs(id, a.Path))
	}
	imgs := env.gl.requests("image")
	if len(imgs) != 6 {
		t.Fatalf("image requests %d", len(imgs))
	}
	refAspects := map[string]int{}
	for _, r := range imgs[:3] {
		refAspects[r.aspect]++
		if r.refs != 0 || !strings.Contains(r.prompt, noText) || r.model != "nano_banana_2" {
			t.Fatalf("reference request %+v", r)
		}
	}
	if refAspects["3:4"] != 2 || refAspects["16:9"] != 1 {
		t.Fatalf("reference aspects %v", refAspects)
	}
	sawFox := false
	for _, r := range imgs[3:] {
		names := strings.Join(r.refNames, ",")
		switch names {
		case "taras.jpg,osinnii_lis.jpg":
		case "taras.jpg,lysytsia.jpg,osinnii_lis.jpg": // shortest tag first
			sawFox = true
			if !strings.Contains(r.prompt, "@lysytsia is Лисиця — identical face") {
				t.Fatalf("frame prompt: %s", r.prompt)
			}
		default:
			t.Fatalf("frame refs %q", names)
		}
		if !strings.Contains(r.prompt, "Location: @osinnii_lis (Осінній ліс)") || !strings.HasSuffix(r.prompt, noText) {
			t.Fatalf("frame prompt: %s", r.prompt)
		}
	}
	if !sawFox {
		t.Fatal("scene 2 frame did not get the fox reference")
	}

	// ---- single frame regen
	oldFrame := p.Scenes[0].Frame
	must(t, e.Regen(id, RegenFrame, p.Scenes[0].ID, ""))
	if err := e.Regen(id, RegenFrame, p.Scenes[0].ID, ""); err == nil {
		t.Fatal("regen of a busy item must be refused")
	}
	if err := e.Regen(id, RegenFrame, "nope", ""); err == nil || err.Error() != "сцену не знайдено" {
		t.Fatalf("regen unknown: %v", err)
	}
	p = waitFor(t, e, id, "regen", func(p Project) bool {
		f := p.Scenes[0].Frame
		return f.Status == StatusDone && f.Path != oldFrame.Path
	})
	// Moved to the trash at once, or a moment later when the file was busy.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(e.Abs(id, oldFrame.Path)); os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(e.Abs(id, oldFrame.Path)); !os.IsNotExist(err) {
		t.Fatalf("replaced frame file should leave its place: %v", err)
	}
	fileExists(t, filepath.Join(e.Dir(id), trashDir, filepath.FromSlash(oldFrame.Path))) // kept, like the web

	// ---- rewrite with a wish
	must(t, e.Rewrite(context.Background(), id, p.Scenes[1].ID, "  більше туману "))
	p, _ = e.Get(id)
	if !strings.HasSuffix(p.Scenes[1].ImagePrompt, "| wish: більше туману") || !p.Scenes[1].Frame.Stale || SceneState(p.Scenes[1]) != "stale" {
		t.Fatalf("rewrite: %+v", p.Scenes[1])
	}
	if err := e.Rewrite(context.Background(), id, "nope", ""); err == nil {
		t.Fatal("rewrite unknown scene")
	}

	// ---- render: clips + voices
	must(t, e.Run(id, StageRender, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Відео й озвучка готові (6)" || p.Step != 4 {
		t.Fatalf("render: %q step %d", p.Job.Message, p.Step)
	}
	for i, s := range p.Scenes {
		if s.Video.Status != StatusDone || !strings.HasPrefix(s.Video.Path, "videos/") || math.Abs(s.Video.Duration-2) > 0.2 {
			t.Fatalf("scene %d video %+v", i, s.Video)
		}
		if s.Audio.Status != StatusDone || !strings.HasPrefix(s.Audio.Path, "audio/") || s.Audio.Duration < 1.4 || s.Audio.Duration > 1.8 {
			t.Fatalf("scene %d audio %+v", i, s.Audio)
		}
		fileExists(t, e.Abs(id, s.Video.Path))
		fileExists(t, e.Abs(id, s.Audio.Path))
		if SceneState(s) != "shot" && i != 1 { // scene 2 frame is still flagged stale
			t.Fatalf("scene %d state %s", i, SceneState(s))
		}
	}
	if v := eleven.voiceFor(p.Scenes[1].Narration); v != "v-bella" {
		t.Fatalf("fox line voiced by %q", v)
	}
	if v := eleven.voiceFor(p.Scenes[0].Narration); v != "v-adam" {
		t.Fatalf("narration voiced by %q", v)
	}
	vids := env.gl.requests("video")
	if len(vids) != 3 {
		t.Fatalf("video requests %d", len(vids))
	}
	for _, r := range vids {
		if r.mode != "start_image" || r.model != "veo_31_fast" || r.aspect != "16:9" || !strings.Contains(r.prompt, "Keep every character exactly as in the first frame") {
			t.Fatalf("video request %+v", r)
		}
	}
	// Everything is done: a second render has nothing to do.
	must(t, e.Run(id, StageRender, false, ""))
	if p = waitJob(t, e, id); p.Job.Message != "Усе вже згенеровано" {
		t.Fatalf("idle render: %q", p.Job.Message)
	}

	// ---- montage
	must(t, e.Run(id, StageMontage, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	out := p.Montage.Output
	if out.Status != StatusDone || !strings.HasPrefix(out.Path, "montage/final-") || p.Step != 5 || !strings.HasPrefix(p.Job.Message, "Фінальне відео готове · 0:0") {
		t.Fatalf("montage: %+v step %d %q", out, p.Step, p.Job.Message)
	}
	fileExists(t, e.Abs(id, out.Path))
	if !strings.HasPrefix(p.Montage.SRTPath, "montage/subtitles-") {
		t.Fatalf("srt path %q", p.Montage.SRTPath)
	}
	srt, err := os.ReadFile(e.Abs(id, p.Montage.SRTPath))
	if err != nil || !strings.Contains(string(srt), p.Scenes[0].Narration) {
		t.Fatalf("srt: %v %q", err, srt)
	}
	want := 0.0
	for _, s := range p.Scenes {
		want += math.Max(s.Video.Duration, s.Audio.Duration+0.25+0.35)
	}
	if math.Abs(out.Duration-want) > 0.05 {
		t.Fatalf("montage duration %.3f, want %.3f", out.Duration, want)
	}
	info, err := e.FF().Probe(context.Background(), e.Abs(id, out.Path))
	must(t, err)
	if math.Abs(info.Duration-want) > 0.35 || !info.HasAudio || info.Width != 320 || info.Height != 180 {
		t.Fatalf("final mp4: %+v, want ≈%.2f s", info, want)
	}

	// ---- cover
	must(t, e.Run(id, StageCover, false, "Тарас і лисиця на стежці"))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if c := p.Montage.Cover; c.Status != StatusDone || !strings.HasPrefix(c.Path, "images/cover-") || p.Montage.CoverPrompt != "Тарас і лисиця на стежці" {
		t.Fatalf("cover: %+v %q", c, p.Montage.CoverPrompt)
	}
	imgs = env.gl.requests("image")
	cov := imgs[len(imgs)-1]
	if cov.model != "nano_banana_pro" || strings.Join(cov.refNames, ",") != "taras.jpg,lysytsia.jpg" || !strings.HasPrefix(cov.prompt, "Тарас і лисиця на стежці\nArt style: ") {
		t.Fatalf("cover request %+v", cov)
	}

	// The cover stage also renders the YouTube-ready final cover (text overlay).
	if f := p.Montage.CoverFinal; f.Status != StatusDone || !strings.HasPrefix(f.Path, "images/cover-final-") {
		t.Fatalf("final cover: %+v", f)
	}

	// ---- list, notifications, persistence
	list := e.List()
	if len(list) != 1 || list[0].ID != id || list[0].Step != 5 || list[0].Scenes != 3 || !strings.HasSuffix(filepath.ToSlash(list[0].ThumbPath), p.Montage.CoverFinal.Path) || list[0].Running != "" {
		t.Fatalf("list %+v", list)
	}
	time.Sleep(200 * time.Millisecond)
	if notes.Load() == 0 || listNotes.Load() == 0 {
		t.Fatalf("notifications: %d project, %d list", notes.Load(), listNotes.Load())
	}
	must(t, e.Flush())
	e2, err := NewEngine(config.NewStoreWith(env.store.Get()))
	must(t, err)
	p2, ok := e2.Get(id)
	if !ok || p2.Montage.Output.Path != out.Path || len(p2.Scenes) != 3 || p2.Scenes[2].Audio.Path != p.Scenes[2].Audio.Path || p2.Job.Status != JobDone {
		t.Fatalf("reloaded project differs: %+v", p2.Job)
	}
}

// seedScenes makes a project with n planned scenes whose frames are imported images.
func seedScenes(t *testing.T, e *Engine, n int) Project {
	t.Helper()
	img := filepath.Join(t.TempDir(), "frame.jpg")
	must(t, os.WriteFile(img, testImage, 0o644))
	p, err := e.Create("Seed")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text = "Текст."
		p.Input.NarratorVoiceID = "v-adam"
		for i := 0; i < n; i++ {
			p.Scenes = append(p.Scenes, Scene{Title: "S", Narration: "Рядок для озвучки.", ImagePrompt: "a frame", MotionPrompt: "pan"})
		}
	}))
	p, _ = e.Get(p.ID)
	for _, s := range p.Scenes {
		must(t, e.ImportImage(p.ID, RegenFrame, s.ID, img))
	}
	p, _ = e.Get(p.ID)
	for _, s := range p.Scenes {
		if s.Frame.Status != StatusDone || s.ID == "" {
			t.Fatalf("seed frame %+v", s)
		}
	}
	return p
}

// TestCancelKeepsInFlight: stopping a render lets the submitted clip finish
// and starts nothing new.
func TestCancelKeepsInFlight(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, func(c *config.Config) { c.Concurrency = 1 })
	e := env.e
	p := seedScenes(t, e, 3)
	must(t, e.Run(p.ID, StageVideos, false, ""))
	deadline := time.Now().Add(10 * time.Second)
	for len(env.gl.requests("video")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no clip submitted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.Cancel(p.ID)
	if q, _ := e.Get(p.ID); q.Job.Message != "Зупиняємо… (поточні задачі доробляться)" || q.Job.Status != JobRunning {
		t.Fatalf("cancelling: %+v", q.Job)
	}
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobCancelled || p.Job.Message != "Зупинено" {
		t.Fatalf("job %+v", p.Job)
	}
	done := 0
	for _, s := range p.Scenes {
		switch s.Video.Status {
		case StatusDone:
			done++
			fileExists(t, e.Abs(p.ID, s.Video.Path))
		case StatusEmpty:
		default:
			t.Fatalf("video %+v", s.Video)
		}
	}
	if done != 1 || len(env.gl.requests("video")) != 1 {
		t.Fatalf("in-flight clip must finish, nothing new may start: done %d, submitted %d", done, len(env.gl.requests("video")))
	}
	// A new job can start after the cancelled one.
	must(t, e.Run(p.ID, StageVoices, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if p.Job.Message != "Відео й озвучка готові (3)" {
		t.Fatalf("voices: %q", p.Job.Message)
	}
}

// TestCancelPlanAbortsModel: stopping the plan aborts the text-model call at once.
func TestCancelPlanAbortsModel(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	env.text.mu.Lock()
	env.text.block = true
	env.text.mu.Unlock()
	e := env.e
	p, _ := e.Create("")
	must(t, e.Edit(p.ID, func(p *Project) { p.Input.Text = "Історія." }))
	must(t, e.Run(p.ID, StagePlan, false, ""))
	select {
	case <-env.text.started:
	case <-time.After(10 * time.Second):
		t.Fatal("bible request never arrived")
	}
	t0 := time.Now()
	e.Cancel(p.ID)
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobCancelled || len(p.Scenes) != 0 {
		t.Fatalf("job %+v scenes %d", p.Job, len(p.Scenes))
	}
	if time.Since(t0) > 5*time.Second {
		t.Fatalf("cancel took %s", time.Since(t0))
	}
}

// TestScriptMode: the user's script is split into scenes and kept verbatim.
func TestScriptMode(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	text := "Перше речення цієї історії розповідає про осінній ліс і маленького хлопчика. " +
		"Друге речення продовжує оповідь! Третє речення має трохи більше слів, щоб точно не влізти в одну сцену разом з іншими? " +
		"Четверте.\n\nП'яте речення завершує нашу коротку історію про дорогу додому."
	p, _ := e.Create("Мій сценарій")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Mode = ModeScript
		p.Input.Text = text
	}))
	p, _ = e.Get(p.ID)
	n, _, words := e.Budget(p)
	chunks := splitScript(text, words)
	if n != len(chunks) || n < 2 {
		t.Fatalf("budget %d chunks %d", n, len(chunks))
	}
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if p.Title != "Мій сценарій" {
		t.Fatalf("user title must be kept, got %q", p.Title)
	}
	if len(p.Scenes) != len(chunks) {
		t.Fatalf("scenes %d chunks %d", len(p.Scenes), len(chunks))
	}
	for i, s := range p.Scenes {
		if s.Narration != chunks[i] {
			t.Fatalf("scene %d narration %q, want verbatim %q", i, s.Narration, chunks[i])
		}
	}
	env.text.mu.Lock()
	bp := env.text.biblePrompts[0]
	env.text.mu.Unlock()
	if !strings.Contains(bp, "already split into") || !strings.Contains(bp, "[1] "+chunks[0]) || !strings.Contains(bp, "VERBATIM") {
		t.Fatalf("script bible prompt: %s", bp)
	}
}

func TestRunValidation(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	cases := map[Stage]string{
		StageRefs:   "Немає персонажів і локацій — спершу створи план",
		StageFrames: "Немає сцен — спершу створи план",
		StageRender: "Спершу згенеруй кадри (Розкадровка)",
		StageVideos: "Спершу згенеруй кадри (Розкадровка)",
		"bogus":     "невідомий етап «bogus»",
	}
	for st, want := range cases {
		if err := e.Run(p.ID, st, false, ""); err == nil || err.Error() != want {
			t.Fatalf("%s: %v", st, err)
		}
	}
	must(t, e.Run(p.ID, StagePlan, false, ""))
	if p = waitJob(t, e, p.ID); p.Job.Status != JobError || p.Job.Error != "Спершу напиши історію або сценарій" {
		t.Fatalf("plan w/o text: %+v", p.Job)
	}
	must(t, e.Run(p.ID, StageMontage, false, ""))
	if p = waitJob(t, e, p.ID); p.Job.Status != JobError || !strings.Contains(p.Job.Error, "Немає жодного кліпу") || p.Montage.Output.Status != StatusEmpty {
		t.Fatalf("montage w/o clips: %+v %+v", p.Job, p.Montage.Output)
	}
	if err := e.Run("missing", StagePlan, false, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := e.Edit("missing", func(*Project) {}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, ok := e.Get("missing"); ok {
		t.Fatal("get missing")
	}
	e.Cancel("missing")
	e.Cancel(p.ID) // idle: no-op
}

// TestAssetsFilesAndDelete covers ImportImage, SetMusic, orphan clean-up on
// edit, regeneration failures and project deletion.
func TestAssetsFilesAndDelete(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	tmp := t.TempDir()
	img := filepath.Join(tmp, "hero.png")
	must(t, os.WriteFile(img, testImage, 0o644)) // JPEG bytes behind a .png name: sniffed
	song := filepath.Join(tmp, "Моя пісня.mp3")
	must(t, os.WriteFile(song, testVoice, 0o644))
	notImage := filepath.Join(tmp, "notes.txt")
	must(t, os.WriteFile(notImage, []byte("hello"), 0o644))

	p, _ := e.Create("Файли")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Characters = append(p.Characters, Character{Name: "Герой", Description: "hero"})
		p.Scenes = append(p.Scenes, Scene{Title: "one", ImagePrompt: "FAILME please", Narration: "Текст"})
	}))
	p, _ = e.Get(p.ID)
	c, s := p.Characters[0], p.Scenes[0]
	if c.Tag != "heroi" || c.ID == "" {
		t.Fatalf("new character %+v", c)
	}
	must(t, e.ImportImage(p.ID, RegenCharacter, c.ID, img))
	if err := e.ImportImage(p.ID, RegenCharacter, c.ID, notImage); err == nil {
		t.Fatal("text file accepted as image")
	}
	if err := e.ImportImage(p.ID, RegenVideo, s.ID, img); err == nil {
		t.Fatal("image as video accepted")
	}
	p, _ = e.Get(p.ID)
	charFile := e.Abs(p.ID, p.Characters[0].Image.Path)
	if !strings.HasSuffix(charFile, ".jpg") || p.Characters[0].Image.Status != StatusDone {
		t.Fatalf("imported image %+v", p.Characters[0].Image)
	}
	fileExists(t, charFile)

	must(t, e.SetMusic(p.ID, song))
	p, _ = e.Get(p.ID)
	music := e.Abs(p.ID, p.Montage.MusicPath)
	if p.Montage.MusicName != "Моя пісня.mp3" || !strings.HasPrefix(p.Montage.MusicPath, "user/music-moia_pisnia-") {
		t.Fatalf("music %q %q", p.Montage.MusicPath, p.Montage.MusicName)
	}
	fileExists(t, music)
	// Edit cannot point the music elsewhere…
	must(t, e.Edit(p.ID, func(p *Project) { p.Montage.MusicPath = "../../evil.mp3"; p.Montage.MusicVolume = 0.4 }))
	p, _ = e.Get(p.ID)
	if e.Abs(p.ID, p.Montage.MusicPath) != music || p.Montage.MusicVolume != 0.4 {
		t.Fatal("music path is engine-owned")
	}
	// …SetMusic("") clears it and removes the copy.
	must(t, e.SetMusic(p.ID, ""))
	if _, err := os.Stat(music); !os.IsNotExist(err) {
		t.Fatal("music copy not removed")
	}

	// A failing generation lands on the asset with the provider's message.
	env.gl.mu.Lock()
	env.gl.failOn = "FAILME"
	env.gl.mu.Unlock()
	must(t, e.Regen(p.ID, RegenFrame, s.ID, ""))
	p = waitFor(t, e, p.ID, "frame error", func(p Project) bool { return p.Scenes[0].Frame.Status == StatusError })
	if !strings.Contains(p.Scenes[0].Frame.Error, "G-Labs [400]: prompt rejected") || SceneState(p.Scenes[0]) != "error" {
		t.Fatalf("frame error %q", p.Scenes[0].Frame.Error)
	}
	// Video without a frame fails at once.
	must(t, e.Regen(p.ID, RegenVideo, s.ID, ""))
	p = waitFor(t, e, p.ID, "video error", func(p Project) bool { return p.Scenes[0].Video.Status == StatusError })
	if p.Scenes[0].Video.Error != "Спершу потрібен кадр сцени" {
		t.Fatalf("video error %q", p.Scenes[0].Video.Error)
	}

	// Deleting the character moves its reference file to trash/.
	charRel, _ := filepath.Rel(e.Dir(p.ID), charFile)
	must(t, e.Edit(p.ID, func(p *Project) { p.Characters = p.Characters[:0] }))
	if _, err := os.Stat(charFile); !os.IsNotExist(err) {
		t.Fatal("orphaned reference still in place")
	}
	fileExists(t, filepath.Join(e.Dir(p.ID), trashDir, charRel))

	dir := e.Dir(p.ID)
	must(t, e.Delete(p.ID))
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("project folder not removed")
	}
	if _, ok := e.Get(p.ID); ok || len(e.List()) != 0 {
		t.Fatal("deleted project still listed")
	}
	if err := e.Delete(p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestServiceChecks(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	ctx := context.Background()
	if ok, msg := e.TestText(ctx); !ok || !strings.Contains(msg, "(mock-model @ http://127.0.0.1:") || !strings.Contains(msg, ": OK") || !strings.Contains(msg, "моделей: 2") {
		t.Fatalf("text: %v %s", ok, msg)
	}
	if models, err := e.TextModels(ctx); err != nil || strings.Join(models, ",") != "mock-a,mock-b" {
		t.Fatalf("models %v %v", models, err)
	}
	if ok, msg := e.TestGlabs(ctx); !ok || !strings.HasPrefix(msg, "G-Labs онлайн") {
		t.Fatalf("glabs: %v %s", ok, msg)
	}
	if ok, msg := e.TestGoogle(ctx); ok || msg != "Ключів Google не задано" {
		t.Fatalf("google: %v %s", ok, msg)
	}
	if ok, msg := e.TestFFmpeg(ctx); !ok || !strings.HasPrefix(msg, "ffmpeg version") {
		t.Fatalf("ffmpeg: %v %s", ok, msg)
	}
	vl, err := e.Voices(ctx, true)
	must(t, err)
	if len(vl.Voices) != 3 || vl.Tier != "free" || vl.Voices[2].Usable {
		t.Fatalf("voices %+v", vl)
	}
	if e.ClipSeconds() != 8 || !e.FF().OK() || e.Config() != env.store {
		t.Fatal("clip/ff/config")
	}
	_ = env.store.Update(func(c *config.Config) { c.Video.Length = 6 }) // in-memory change applies even if the file write races
	if e.ClipSeconds() != 6 {
		t.Fatal("clip seconds follow settings")
	}
}

func TestSubscribeCoalesces(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	time.Sleep(200 * time.Millisecond)
	var n atomic.Int32
	unsub := e.Subscribe(func(id string) {
		if id == p.ID {
			n.Add(1)
		}
	})
	for i := 0; i < 200; i++ {
		must(t, e.Edit(p.ID, func(p *Project) { p.Logline = strings.Repeat("x", i%7) }))
	}
	time.Sleep(400 * time.Millisecond)
	got := n.Load()
	if got < 1 || got > 6 {
		t.Fatalf("200 quick edits → %d notifications", got)
	}
	unsub()
	must(t, e.Edit(p.ID, func(p *Project) { p.Logline = "done" }))
	time.Sleep(300 * time.Millisecond)
	if n.Load() != got {
		t.Fatal("notified after unsubscribe")
	}
}

func TestLoadNormalizesAndSwitchesDataDir(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	other := t.TempDir()
	dir := filepath.Join(other, "projects", "old-proj")
	must(t, os.MkdirAll(dir, 0o755))
	must(t, os.WriteFile(filepath.Join(dir, projectFile), []byte(`{"id":"whatever","title":"Старий","step":4,
		"scenes":[{"id":"s1","frame":{"status":"running","path":"images/f.jpg"},"video":{"status":"running"}}],
		"job":{"kind":"render","status":"running","progress":50,"message":"Відео · 1/2","startedAt":1}}`), 0o644))
	var listed atomic.Int32
	unsub := env.e.Subscribe(func(id string) {
		if id == "" {
			listed.Add(1)
		}
	})
	defer unsub()
	_ = env.store.Update(func(c *config.Config) { c.DataDir = other })
	list := env.e.List()
	if len(list) != 1 || list[0].ID != "old-proj" || list[0].Title != "Старий" {
		t.Fatalf("list after switching folder: %+v", list)
	}
	p, _ := env.e.Get("old-proj")
	if p.Job.Status != JobError || p.Scenes[0].Frame.Status != StatusDone || p.Scenes[0].Video.Status != StatusError || p.Job.Message != "Відео · 1/2" {
		t.Fatalf("normalize on load: %+v %+v", p.Job, p.Scenes[0])
	}
	time.Sleep(200 * time.Millisecond)
	if listed.Load() == 0 {
		t.Fatal("list change not notified")
	}
}
