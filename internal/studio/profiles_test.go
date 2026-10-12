package studio

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

func waitCount(t *testing.T, n *atomic.Int32, above int32, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() <= above {
		if time.Now().After(deadline) {
			t.Fatalf("no notification: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEngineProfiles(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	var listNotes atomic.Int32
	unsub := e.Subscribe(func(id string) {
		if id == "" {
			listNotes.Add(1)
		}
	})
	defer unsub()

	// The engine of an in-memory settings store gets in-memory starter profiles.
	if list := e.Profiles(); len(list) != len(config.StarterProfiles()) || list[0].ID != "starter-youtube" || e.profiles.Path() != "" {
		t.Fatalf("starters %+v (path %q)", list, e.profiles.Path())
	}
	base := listNotes.Load()
	pr, err := e.SaveProfile(config.Profile{
		Name: "Фольк-хорор", TargetSeconds: 120, Aspect: "9:16", Style: "petrykivka", Genre: "folk horror", Tempo: "fast",
		Camera: "drone", ImageModel: "GEM_PIX_2", Voiceover: true, Language: "en", Tone: "eerie", StoryboardMode: "grid3x3",
		ChainFrames: true, Transition: "none", Subtitles: "burn", MusicVolume: 0.3, AmbientVolume: 0.1, Ducking: false,
	})
	must(t, err)
	if pr.ID == "" || pr.CreatedAt == 0 || len(e.Profiles()) != len(config.StarterProfiles())+1 {
		t.Fatalf("created %+v", pr)
	}
	waitCount(t, &listNotes, base, "profile created")
	if odd, err := e.SaveProfile(config.Profile{Name: "odd", Camera: "warp"}); err != nil || odd.Camera != "" {
		t.Fatalf("unknown camera must be dropped: %+v %v", odd, err)
	}

	// Apply: creative settings + montage defaults; text, mode, plan untouched.
	p, err := e.Create("Проєкт")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text, p.Input.Mode = "Історія про відьму.", ModeScript
		p.Input.NarratorVoiceID, p.Input.Notes = "v-keep", "мої нотатки"
		p.Scenes = append(p.Scenes, Scene{Title: "S", Narration: "Рядок."})
	}))
	p, _ = e.Get(p.ID)
	must(t, e.ApplyProfile(p.ID, pr.ID))
	q, _ := e.Get(p.ID)
	in, m := q.Input, q.Montage
	if in.TargetSeconds != 120 || in.Aspect != "9:16" || in.Style != "petrykivka" || in.Genre != "folk horror" || in.Tempo != "fast" ||
		in.Camera != "drone" || in.ImageModel != "GEM_PIX_2" || !in.Voiceover || in.Language != "en" || in.Tone != "eerie" ||
		in.StoryboardMode != "grid3x3" || !in.ChainFrames {
		t.Fatalf("applied input %+v", in)
	}
	if in.NarratorVoiceID != "v-keep" || in.Notes != "мої нотатки" || in.Text != "Історія про відьму." || in.Mode != ModeScript {
		t.Fatalf("profile without voice/notes must keep the project's; text/mode untouched: %+v", in)
	}
	if len(q.Scenes) != 1 || q.Scenes[0].ID != p.Scenes[0].ID || q.Scenes[0].Narration != "Рядок." {
		t.Fatalf("scenes touched: %+v", q.Scenes)
	}
	if m.Transition != "none" || m.Subtitles != "burn" || m.MusicVolume != 0.3 || m.AmbientVolume != 0.1 || m.Ducking || q.ProfileID != pr.ID {
		t.Fatalf("montage %+v profile %q", m, q.ProfileID)
	}
	// A profile with its own voice and notes sets them.
	pr.NarratorVoiceID, pr.Notes = "v-prof", "Нотатки профілю"
	pr, err = e.SaveProfile(pr)
	must(t, err)
	must(t, e.ApplyProfile(p.ID, pr.ID))
	if q, _ = e.Get(p.ID); q.Input.NarratorVoiceID != "v-prof" || q.Input.Notes != "Нотатки профілю" {
		t.Fatalf("voice/notes %+v", q.Input)
	}
	if err := e.ApplyProfile(p.ID, "nope"); !errors.Is(err, config.ErrProfileNotFound) {
		t.Fatalf("unknown profile: %v", err)
	}
	if err := e.ApplyProfile("missing", pr.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}

	// Save a project's settings as a profile.
	fp, err := e.ProfileFromProject(p.ID, "")
	must(t, err)
	if fp.ID == pr.ID || fp.Name != "Проєкт" || fp.Aspect != "9:16" || fp.Style != "petrykivka" || fp.Language != "en" ||
		fp.Tone != "eerie" || fp.Transition != "none" || fp.NarratorVoiceID != "v-prof" || !fp.ChainFrames {
		t.Fatalf("profile from project %+v", fp)
	}
	if named, _ := e.ProfileFromProject(p.ID, "Мій пресет"); named.Name != "Мій пресет" {
		t.Fatalf("name %q", named.Name)
	}
	if _, err := e.ProfileFromProject("missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}

	// Create with a profile.
	before := len(e.List())
	if _, err := e.CreateWithProfile("X", "nope"); !errors.Is(err, config.ErrProfileNotFound) || len(e.List()) != before {
		t.Fatalf("unknown profile must not create a project: %v", err)
	}
	c, err := e.CreateWithProfile("Шортс", "starter-shorts")
	must(t, err)
	if c.ProfileID != "starter-shorts" || c.Input.Aspect != "9:16" || c.Input.TargetSeconds != 48 || c.Input.Camera != "dynamic" ||
		c.Input.Tone != "youtuber" || c.Montage.Subtitles != "burn" || c.Montage.Transition != "none" || c.Title != "Шортс" {
		t.Fatalf("created with profile %+v %+v", c.Input, c.Montage)
	}
	if d, _ := e.CreateWithProfile("", ""); d.ProfileID != "" || d.Input.Aspect != "16:9" || d.Title != "Новий проєкт" {
		t.Fatalf("without a profile %+v", d)
	}

	// Update and delete; projects keep what they got.
	pr.Name = "Перейменований"
	if upd, err := e.SaveProfile(pr); err != nil || upd.Name != "Перейменований" || upd.CreatedAt != pr.CreatedAt {
		t.Fatalf("update %+v %v", upd, err)
	}
	if _, err := e.SaveProfile(config.Profile{ID: "nope", Name: "x"}); !errors.Is(err, config.ErrProfileNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
	base = listNotes.Load()
	must(t, e.DeleteProfile(pr.ID))
	if err := e.DeleteProfile(pr.ID); !errors.Is(err, config.ErrProfileNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if _, ok := e.Profile(pr.ID); ok {
		t.Fatal("deleted profile still there")
	}
	waitCount(t, &listNotes, base, "profile deleted")
	if q, _ = e.Get(p.ID); q.Input.Style != "petrykivka" || q.ProfileID != pr.ID {
		t.Fatalf("project changed by deleting its profile: %+v", q.Input)
	}
}

func TestEngineProfilesPersist(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	dir := t.TempDir()
	ps, err := config.NewProfileStoreAt(dir)
	must(t, err)
	env.e.profiles = ps
	saved, err := env.e.SaveProfile(config.Profile{Name: "На диску", Style: "chalkboard"})
	must(t, err)
	again, err := config.NewProfileStoreAt(dir)
	must(t, err)
	if got, ok := again.Get(saved.ID); !ok || got.Style != "chalkboard" || len(again.List()) != len(config.StarterProfiles())+1 {
		t.Fatalf("reloaded %+v %v", got, ok)
	}
}
