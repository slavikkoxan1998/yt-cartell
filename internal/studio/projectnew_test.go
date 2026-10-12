package studio

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/config"
)

func TestCreateProjectChecks(t *testing.T) {
	t.Parallel()
	env, _ := matEnv(t, nil)
	e := env.e
	photo := picture(t, "Тарас.jpg", 64, 96, color.RGBA{200, 80, 40, 255})
	txt := filepath.Join(t.TempDir(), "notes.txt")
	must(t, os.WriteFile(txt, []byte("x"), 0o644))
	for _, tc := range []struct {
		opt  NewProjectOptions
		want string
	}{
		{NewProjectOptions{Autopilot: true}, "Спершу напиши історію або сценарій"},
		{NewProjectOptions{Mode: ModeVoice}, errNoVoice.Error()},
		{NewProjectOptions{Mode: "film"}, "Невідомий режим"},
		{NewProjectOptions{Aspect: "4:3"}, "Невідомий формат кадру"},
		{NewProjectOptions{Language: "xx"}, "Невідома мова"},
		{NewProjectOptions{Materials: []MaterialInput{{Path: photo}, {Path: txt}}}, "notes.txt: Це не зображення"},
		{NewProjectOptions{Materials: []MaterialInput{{Kind: "dragon", Path: photo}}}, "Невідомий вид матеріалу"},
		{NewProjectOptions{ProfileID: "nope"}, config.ErrProfileNotFound.Error()},
	} {
		if _, err := e.CreateProject(tc.opt); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v (want %q)", tc.opt, err, tc.want)
		}
	}
	if n := len(e.List()); n != 0 {
		t.Fatalf("a refused project was left behind (%d)", n)
	}

	// The options override the profile.
	pr, err := e.SaveProfile(config.Profile{Name: "Вертикаль", TargetSeconds: 120, Aspect: "9:16", Style: "watercolor", Language: "uk", Voiceover: true})
	must(t, err)
	p, err := e.CreateProject(NewProjectOptions{
		Title: "Мій фільм", Text: "  Історія про Тараса.  ", ProfileID: pr.ID, Language: "EN", Aspect: "16:9", TargetSeconds: 45,
		Materials: []MaterialInput{{Name: "Тарас", Note: "хлопчик", Path: photo}},
	})
	must(t, err)
	if p.Title != "Мій фільм" || p.ProfileID != pr.ID || p.Input.Style != "watercolor" || p.Input.Language != "en" || p.Input.Aspect != "16:9" ||
		p.Input.TargetSeconds != 45 || p.Input.Text != "Історія про Тараса." || p.Input.Mode != ModeStory || p.Job != nil {
		t.Fatalf("project %q %+v", p.Title, p.Input)
	}
	if len(p.Input.Materials) != 1 || p.Input.Materials[0].Name != "Тарас" {
		t.Fatalf("materials %+v", p.Input.Materials)
	}
	fileExists(t, e.Abs(p.ID, p.Input.Materials[0].Path))
	waitLook(t, e, p.ID, p.Input.Materials[0].ID)

	// Script mode keeps its text; the profile's values stay where nothing is given.
	q, err := e.CreateProject(NewProjectOptions{Text: "Перше. Друге.", Mode: ModeScript, ProfileID: pr.ID})
	must(t, err)
	if q.Input.Mode != ModeScript || q.Input.Aspect != "9:16" || q.Input.TargetSeconds != 120 || q.Input.Language != "uk" || q.Title != "Новий проєкт" {
		t.Fatalf("script project %+v", q.Input)
	}
	// A transcript the engine cannot attach removes the new project again.
	before := len(e.List())
	if _, err := e.CreateProject(NewProjectOptions{Text: "x", TranscriptFile: filepath.Join(t.TempDir(), "none.srt")}); err == nil {
		t.Fatal("a missing transcript was accepted")
	}
	if len(e.List()) != before {
		t.Fatal("the failed project was left behind")
	}
}

// TestCreateProjectAutopilot: one call — story, a character photo, the
// autopilot — makes the whole film with the user's character.
func TestCreateProjectAutopilot(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, mm := matEnv(t, func(c *config.Config) {
		c.Autopilot = config.Autopilot{MakeCover: false, Retries: 0, ContinueOnError: true}
	})
	e := env.e
	p, err := e.CreateProject(NewProjectOptions{
		Text: apStory, TargetSeconds: 24, Autopilot: true,
		Materials: []MaterialInput{{Name: "Тарас", Path: picture(t, "Тарас.jpg", 96, 128, color.RGBA{200, 80, 40, 255})}},
	})
	must(t, err)
	if p.Job == nil || p.Job.Kind != JobAutopilot {
		t.Fatalf("job %+v", p.Job)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if looks, bibles := mm.count(); looks != 1 || bibles != 1 {
		t.Fatalf("looks %d, bibles %d", looks, bibles)
	}
	var tc *Character
	for i := range p.Characters {
		if p.Characters[i].MaterialID != "" {
			tc = &p.Characters[i]
		}
	}
	if tc == nil || tc.Name != "Тарас" || !strings.HasPrefix(tc.Description, "LOOK character") {
		t.Fatalf("the user's character is missing: %+v", p.Characters)
	}
	// Only the model's own character was drawn; the film is there.
	for _, r := range env.gl.requests("image") {
		if strings.Contains(r.prompt, "Character reference image of Тарас") {
			t.Fatal("the user's character was generated")
		}
	}
	if p.Montage.Output.Status != StatusDone {
		t.Fatalf("film %+v", p.Montage.Output)
	}
	fileExists(t, e.Abs(p.ID, p.Montage.Output.Path))
}

// TestCreateProjectVoice: a recording makes a ModeVoice project; the
// autopilot starts on it without any text (its plan transcribes first).
func TestCreateProjectVoice(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, _, _ := voiceEnv(t, nil)
	e := env.e
	rec := voiceFile(t, "озвучка.mp3", 3)
	p, err := e.CreateProject(NewProjectOptions{VoiceFile: rec, Language: "uk"})
	must(t, err)
	if p.Input.Mode != ModeVoice || p.Input.VoiceFile == "" || !p.Input.Voiceover || p.Input.TargetSeconds != 3 || p.Input.Language != "uk" || p.Job != nil {
		t.Fatalf("voice project %+v", p.Input)
	}
	fileExists(t, e.Abs(p.ID, p.Input.VoiceFile))

	q, err := e.CreateProject(NewProjectOptions{VoiceFile: rec, Autopilot: true})
	must(t, err)
	if q.Job == nil || q.Job.Kind != JobAutopilot || q.Input.Text != "" {
		t.Fatalf("autopilot on a fresh recording: %+v", q.Job)
	}
	e.Cancel(q.ID)
	if q = waitJob(t, e, q.ID); q.Job.Status == JobError {
		t.Fatalf("job %+v", q.Job)
	}
	if _, err := e.CreateProject(NewProjectOptions{VoiceFile: filepath.Join(t.TempDir(), "none.mp3")}); err == nil {
		t.Fatal("a missing recording was accepted")
	}
}

// TestCreateProjectExplicitChoices: «Типові налаштування» beats the default
// autopilot profile; «Мова: авто» beats the profile's language.
func TestCreateProjectExplicitChoices(t *testing.T) {
	t.Parallel()
	env, _ := matEnv(t, nil)
	e := env.e
	pr, err := e.SaveProfile(config.Profile{Name: "Українською", Aspect: "9:16", Language: "uk", Voiceover: true})
	must(t, err)
	_ = env.store.Update(func(c *config.Config) { c.Autopilot.DefaultProfileID = pr.ID }) // in-memory store

	withDefault, err := e.CreateProject(NewProjectOptions{Text: apStory, Autopilot: true})
	must(t, err)
	plain, err := e.CreateProject(NewProjectOptions{Text: apStory, Autopilot: true, NoProfile: true, ProfileID: pr.ID})
	must(t, err)
	for _, id := range []string{withDefault.ID, plain.ID} {
		e.Cancel(id)
		waitJob(t, e, id)
	}
	if withDefault.ProfileID != pr.ID || withDefault.Input.Aspect != "9:16" || plain.ProfileID != "" || plain.Input.Aspect != "16:9" || plain.Input.Language != "" {
		t.Fatalf("profiles: default %q %q, none %q %q", withDefault.ProfileID, withDefault.Input.Aspect, plain.ProfileID, plain.Input.Aspect)
	}

	for _, opt := range []NewProjectOptions{
		{ProfileID: pr.ID, LanguageSet: true},
		{ProfileID: pr.ID, Language: "auto"},
	} {
		p, err := e.CreateProject(opt)
		must(t, err)
		if p.Input.Language != "" || p.Input.Aspect != "9:16" {
			t.Fatalf("%+v: language %q", opt, p.Input.Language)
		}
	}
	p, err := e.CreateProject(NewProjectOptions{ProfileID: pr.ID})
	must(t, err)
	if p.Input.Language != "uk" {
		t.Fatalf("profile language %q", p.Input.Language)
	}
}
