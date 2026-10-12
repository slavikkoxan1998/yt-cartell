package studio

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

const apStory = "Маленький хлопчик Тарас загубився в осінньому лісі. Його знайшла лисиця і вивела додому."

// quickProfile is a 3-scene profile (24 s of 8 s clips) for fast autopilot runs.
func quickProfile(t *testing.T, e *Engine) config.Profile {
	t.Helper()
	pr, err := e.SaveProfile(config.Profile{
		Name: "Тест · 3 сцени", TargetSeconds: 24, Aspect: "16:9", Style: "watercolor", Tempo: "balanced", Camera: "cinematic",
		ImageModel: "NARWHAL", Voiceover: true, Transition: "fade", Subtitles: "srt", MusicVolume: 0.2, AmbientVolume: 0.25, Ducking: true,
	})
	must(t, err)
	return pr
}

// wrapGlabs puts h in front of the G-Labs mock: the engine talks to h, which
// may delay, fail or pass requests on to next.
func wrapGlabs(t *testing.T, env *testEnv, h func(w http.ResponseWriter, r *http.Request, next http.Handler)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h(w, r, env.gl) }))
	allow(srv)
	t.Cleanup(srv.Close)
	_ = env.store.Update(func(c *config.Config) { c.Glabs.BaseURL = srv.URL }) // in-memory store: nothing is written
}

func videoRequestsFor(env *testEnv, marker string) int {
	n := 0
	for _, r := range env.gl.requests("video") {
		if strings.Contains(r.prompt, marker) {
			n++
		}
	}
	return n
}

func bibleCount(env *testEnv) int {
	env.text.mu.Lock()
	defer env.text.mu.Unlock()
	return env.text.bibles
}

// TestAutopilotFullRun: story text → plan → refs → frames → clips + voices →
// montage → cover in one job, with the default autopilot profile applied.
func TestAutopilotFullRun(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, func(c *config.Config) {
		c.Autopilot = config.Autopilot{MakeCover: true, Retries: 1, ContinueOnError: false}
	})
	e := env.e
	pr := quickProfile(t, e)
	_ = env.store.Update(func(c *config.Config) { c.Autopilot.DefaultProfileID = pr.ID })

	id, err := e.StartAutopilot("", apStory, "")
	must(t, err)
	if err := e.Run(id, StagePlan, false, ""); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "«Автопілот»") {
		t.Fatalf("a second job must be refused: %v", err)
	}
	// Sample the job while it runs (one goroutine: samples stay in order).
	var mu sync.Mutex
	var msgs []string
	var progress []int
	stop, polled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(polled)
		for {
			if p, ok := e.Get(id); ok && p.Job != nil && p.Job.Status == JobRunning {
				mu.Lock()
				msgs = append(msgs, p.Job.Message)
				progress = append(progress, p.Job.Progress)
				mu.Unlock()
			}
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
			}
		}
	}()
	p := waitJob(t, e, id)
	close(stop)
	<-polled
	mustJobDone(t, p)
	if p.Job.Kind != JobAutopilot || p.Job.Progress != 100 || !strings.HasPrefix(p.Job.Message, "Автопілот завершено · Фінальне відео готове · 0:") ||
		strings.Contains(p.Job.Message, "без кліпу") {
		t.Fatalf("job %+v", p.Job)
	}
	if p.ProfileID != pr.ID || p.Input.Style != "watercolor" || p.Input.TargetSeconds != 24 || p.Input.Mode != ModeStory || p.Input.Text != apStory {
		t.Fatalf("project input %+v profile %q", p.Input, p.ProfileID)
	}
	if p.Title != "Стежка додому" || p.Step != 5 || len(p.Scenes) != 3 || len(p.Characters) != 2 {
		t.Fatalf("plan: %q step %d, %d scenes", p.Title, p.Step, len(p.Scenes))
	}
	for i, s := range p.Scenes {
		if s.Frame.Status != StatusDone || s.Video.Status != StatusDone || s.Audio.Status != StatusDone {
			t.Fatalf("scene %d: %s %s %s", i, s.Frame.Status, s.Video.Status, s.Audio.Status)
		}
	}
	for _, c := range p.Characters {
		if c.Image.Status != StatusDone {
			t.Fatalf("reference %+v", c.Image)
		}
	}
	out := p.Montage.Output
	if out.Status != StatusDone || !strings.HasPrefix(out.Path, "montage/final-") || out.Duration < 5 {
		t.Fatalf("montage %+v", out)
	}
	fileExists(t, e.Abs(id, out.Path))
	if info, err := e.FF().Probe(t.Context(), e.Abs(id, out.Path)); err != nil || !info.HasAudio || info.Duration < 5 {
		t.Fatalf("final mp4 %+v %v", info, err)
	}
	if c := p.Montage.Cover; c.Status != StatusDone || !strings.HasPrefix(c.Path, "images/cover-") {
		t.Fatalf("cover %+v", c)
	}
	fileExists(t, e.Abs(id, p.Montage.Cover.Path))

	mu.Lock()
	seen, prefixed := len(msgs), 0
	for i, m := range msgs {
		if strings.HasPrefix(m, "Автопілот · ") {
			prefixed++
		} else if m != "Запуск…" {
			t.Errorf("stage message without the autopilot prefix: %q", m)
		}
		if i > 0 && progress[i] < progress[i-1] {
			t.Errorf("progress went back: %v", progress)
		}
	}
	mu.Unlock()
	if seen == 0 || prefixed == 0 {
		t.Fatalf("no autopilot progress seen (%d messages)", seen)
	}

	// Again on the finished project: nothing is re-planned or re-generated
	// (the cover is kept), only the montage is rebuilt.
	images, videos := len(env.gl.requests("image")), len(env.gl.requests("video"))
	must(t, e.Run(id, StageAutopilot, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if bibleCount(env) != 1 || len(env.gl.requests("image")) != images || len(env.gl.requests("video")) != videos {
		t.Fatalf("second run regenerated: bibles %d, images %d→%d, videos %d→%d", bibleCount(env), images,
			len(env.gl.requests("image")), videos, len(env.gl.requests("video")))
	}
	if p.Montage.Output.Status != StatusDone || !strings.HasPrefix(p.Job.Message, "Автопілот завершено") {
		t.Fatalf("second run: %+v %+v", p.Job, p.Montage.Output)
	}
}

// TestAutopilotRetriesFailedClip: a clip that fails once for a transient
// reason (G-Labs UPLOAD_ERROR|500) is regenerated in the retry round (and
// only that clip).
func TestAutopilotRetriesFailedClip(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, func(c *config.Config) {
		c.Autopilot = config.Autopilot{MakeCover: false, Retries: 1, ContinueOnError: false}
	})
	e := env.e
	var failed atomic.Bool
	wrapGlabs(t, env, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.HasPrefix(r.URL.Path, "/api/status/") { // the failure is transient this time, not a rejection
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			body := rec.Body.String()
			if strings.Contains(body, `"prompt rejected"`) {
				body = `{"status":"failed","error_code":0,"error":"Upload failed for image 1: UPLOAD_ERROR|500: INTERNAL (flag=[13])"}`
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write([]byte(body))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/video/generate" {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			if strings.Contains(string(body), "scene 2.") && failed.CompareAndSwap(false, true) {
				env.gl.mu.Lock()
				env.gl.failOn = "scene 2."
				env.gl.mu.Unlock()
				next.ServeHTTP(w, r) // this submission fails (prompt rejected)
				env.gl.mu.Lock()
				env.gl.failOn = ""
				env.gl.mu.Unlock()
				return
			}
		}
		next.ServeHTTP(w, r)
	})
	id, err := e.StartAutopilot("Повтор", apStory, quickProfile(t, e).ID)
	must(t, err)
	p := waitJob(t, e, id)
	mustJobDone(t, p)
	if !failed.Load() || videoRequestsFor(env, "scene 2.") != 2 || videoRequestsFor(env, "scene 1.") != 1 || videoRequestsFor(env, "scene 3.") != 1 {
		t.Fatalf("retry must re-run only the failed clip: scene 1 %d, scene 2 %d, scene 3 %d",
			videoRequestsFor(env, "scene 1."), videoRequestsFor(env, "scene 2."), videoRequestsFor(env, "scene 3."))
	}
	for i, s := range p.Scenes {
		if s.Video.Status != StatusDone {
			t.Fatalf("scene %d video %+v", i, s.Video)
		}
	}
	if p.Montage.Output.Status != StatusDone || p.Montage.Cover.Status != StatusEmpty || strings.Contains(p.Job.Message, "без кліпу") {
		t.Fatalf("result %+v cover %+v %q", p.Montage.Output, p.Montage.Cover, p.Job.Message)
	}
}

// TestAutopilotStopsBeforeMontage: a clip that G-Labs refuses (400) stops the
// autopilot before the montage unless ContinueOnError — the retry round does
// not resubmit the refused request; then the montage uses the still frame
// and the plan is not redone.
func TestAutopilotStopsBeforeMontage(t *testing.T) {
	needMedia(t)
	t.Parallel()
	// The legacy ContinueOnError (on by default, never asked) no longer lets
	// the autopilot swap a failed clip for a still: only an explicit choice.
	env := newEnv(t, func(c *config.Config) {
		c.Autopilot = config.Autopilot{MakeCover: false, Retries: 1, ContinueOnError: true}
	})
	e := env.e
	env.gl.mu.Lock()
	env.gl.failOn = "scene 2." // only the motion prompt of scene 2 ends like this
	env.gl.mu.Unlock()
	id, err := e.StartAutopilot("", apStory, quickProfile(t, e).ID)
	must(t, err)
	p := waitJob(t, e, id)
	if p.Job.Status != JobError || p.Job.Blocked != BlockedGaps ||
		!strings.HasPrefix(p.Job.Error, "Автопілот зупинився перед монтажем. Не всі сцени готові до монтажу (1 з них): Сцена 2 — кліп: G-Labs відхилив запит: змінити промпт або референси") {
		t.Fatalf("job %+v", p.Job)
	}
	if p.Montage.Output.Status != StatusEmpty || videoRequestsFor(env, "scene 2.") != 1 || p.Scenes[1].Video.Status != StatusError ||
		!strings.HasPrefix(p.Scenes[1].Video.Error, "G-Labs відхилив запит: змінити промпт або референси") {
		t.Fatalf("montage %+v, scene 2 attempts %d", p.Montage.Output, videoRequestsFor(env, "scene 2."))
	}

	// The user's answer in the montage dialog: «З кадром замість відео».
	must(t, e.RunWith(id, StageMontage, RunOptions{Missing: MissingStill}))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if g := p.Montage.Gaps; len(g) != 1 || g[0].Number != 2 || g[0].Action != GapStill ||
		!strings.Contains(p.Job.Message, "кадр замість відео: сцена 2 (кліп: G-Labs відхилив запит") {
		t.Fatalf("montage with a still: %q %+v", p.Job.Message, p.Montage.Gaps)
	}

	// Chosen once for every autopilot in Settings: «Змонтувати без них».
	_ = env.store.Update(func(c *config.Config) { c.Autopilot.OnFailed = config.FailedSkip })
	must(t, e.Run(id, StageAutopilot, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if bibleCount(env) != 1 {
		t.Fatalf("the plan was redone (%d bible requests)", bibleCount(env))
	}
	if p.Montage.Output.Status != StatusDone || !strings.Contains(p.Job.Message, "пропущено: сцена 2 (кліп: G-Labs відхилив запит") ||
		len(p.Montage.Gaps) != 1 || p.Montage.Gaps[0].Action != GapSkipped {
		t.Fatalf("montage without scene 2: %q %+v", p.Job.Message, p.Montage.Output)
	}
	if n := videoRequestsFor(env, "scene 2."); n != 2 { // the new run tries it once; its retry round does not
		t.Fatalf("scene 2 attempts %d, want 2", n)
	}
	fileExists(t, e.Abs(id, p.Montage.Output.Path))
	if tl := e.Timeline(p); len(tl) != len(p.Scenes) { // the editor previews every scene that has a picture
		t.Fatalf("timeline %d items", len(tl))
	}
}

// TestAutopilotCancel: Cancel stops the autopilot inside the plan (the model
// call is aborted) and inside a production stage (nothing new starts, the
// submitted generation finishes, no montage).
func TestAutopilotCancel(t *testing.T) {
	t.Parallel()
	t.Run("plan", func(t *testing.T) {
		t.Parallel()
		env := newEnv(t, nil)
		env.text.mu.Lock()
		env.text.block = true
		env.text.mu.Unlock()
		e := env.e
		id, err := e.StartAutopilot("", apStory, "")
		must(t, err)
		select {
		case <-env.text.started:
		case <-time.After(10 * time.Second):
			t.Fatal("bible request never arrived")
		}
		t0 := time.Now()
		e.Cancel(id)
		p := waitJob(t, e, id)
		if p.Job.Status != JobCancelled || p.Job.Kind != JobAutopilot || len(p.Scenes) != 0 || time.Since(t0) > 5*time.Second {
			t.Fatalf("job %+v scenes %d after %s", p.Job, len(p.Scenes), time.Since(t0))
		}
	})
	t.Run("production", func(t *testing.T) {
		needMedia(t)
		t.Parallel()
		env := newEnv(t, nil)
		e := env.e
		started := make(chan struct{}, 16)
		release := make(chan struct{})
		wrapGlabs(t, env, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.Method == http.MethodPost && r.URL.Path == "/api/image/generate" {
				started <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			next.ServeHTTP(w, r)
		})
		id, err := e.StartAutopilot("", apStory, quickProfile(t, e).ID)
		must(t, err)
		select {
		case <-started:
		case <-time.After(20 * time.Second):
			t.Fatal("no reference image was submitted")
		}
		e.Cancel(id)
		if q, _ := e.Get(id); q.Job.Status != JobRunning || q.Job.Message != "Зупиняємо… (поточні задачі доробляться)" {
			t.Fatalf("cancelling %+v", q.Job)
		}
		close(release)
		p := waitJob(t, e, id)
		if p.Job.Status != JobCancelled || p.Job.Message != "Зупинено" {
			t.Fatalf("job %+v", p.Job)
		}
		if n := len(env.gl.requests("video")); n != 0 || p.Montage.Output.Status != StatusEmpty {
			t.Fatalf("work after cancel: %d clips, montage %+v", n, p.Montage.Output)
		}
		for _, s := range p.Scenes {
			if s.Frame.Status != StatusEmpty {
				t.Fatalf("a frame was started after cancel: %+v", s.Frame)
			}
		}
	})
}

func TestAutopilotValidation(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	if err := e.Run(p.ID, StageAutopilot, false, ""); err == nil || err.Error() != "Спершу напиши історію або сценарій" {
		t.Fatalf("autopilot without text: %v", err)
	}
	if _, err := e.StartAutopilot("x", "   ", ""); err == nil || err.Error() != "Спершу напиши історію або сценарій" {
		t.Fatalf("empty story: %v", err)
	}
	before := len(e.List())
	if _, err := e.StartAutopilot("x", apStory, "no-such-profile"); !errors.Is(err, config.ErrProfileNotFound) || len(e.List()) != before {
		t.Fatalf("unknown explicit profile: %v (projects %d→%d)", err, before, len(e.List()))
	}
	// A default profile that no longer exists falls back to the app defaults.
	_ = env.store.Update(func(c *config.Config) { c.Autopilot.DefaultProfileID = "deleted-profile" })
	env.text.mu.Lock()
	env.text.block = true // keep the job in its plan stage
	env.text.mu.Unlock()
	id, err := e.StartAutopilot("", apStory, "")
	must(t, err)
	q, _ := e.Get(id)
	if q.ProfileID != "" || q.Input.Aspect != "16:9" || q.Job == nil || q.Job.Kind != JobAutopilot {
		t.Fatalf("fallback project %+v %+v", q.Input, q.Job)
	}
	if err := e.Run(id, StageAutopilot, false, ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	e.Cancel(id)
	if q = waitJob(t, e, id); q.Job.Status != JobCancelled {
		t.Fatalf("job %+v", q.Job)
	}
}

// TestJobSubRange: inside the autopilot every runner's 0–100 lands in its
// slice and its messages get the stage prefix.
func TestJobSubRange(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	st := e.state(p.ID)
	e.update(st, func(p *Project) { p.Job = &Job{Kind: JobAutopilot, Status: JobRunning} })
	defer e.update(st, func(p *Project) { p.Job.Status = JobDone })
	j := &jobCtx{e: e, st: st, work: st.ctx}
	job := func() Job { q, _ := e.Get(p.ID); return *q.Job }

	j.progress(40, "План")
	if g := job(); g.Progress != 40 || g.Message != "План" {
		t.Fatalf("full range %+v", g)
	}
	j.within(25, 20, "Автопілот · ")
	j.progress(50, "Кадри сцен · 1/2")
	if g := job(); g.Progress != 35 || g.Message != "Автопілот · Кадри сцен · 1/2" {
		t.Fatalf("sub-range %+v", g)
	}
	j.progress(250, "")
	if g := job(); g.Progress != 45 || g.Message != "Автопілот · Кадри сцен · 1/2" {
		t.Fatalf("clamped %+v", g)
	}
	j.note("Знімаємо сцену 2 (ланцюжок)…")
	if g := job(); g.Progress != 45 || g.Message != "Автопілот · Знімаємо сцену 2 (ланцюжок)…" {
		t.Fatalf("note %+v", g)
	}
	j.within(80, 20, "Автопілот · Повтор 1/1 · ")
	counter(j, 4, "Кадри сцен", 0, 100)()
	if g := job(); g.Progress != 85 || g.Message != "Автопілот · Повтор 1/1 · Кадри сцен · 1/4" {
		t.Fatalf("counter %+v", g)
	}
	j.within(0, 0, "")
	j.progress(10, "x")
	if g := job(); g.Progress != 10 || g.Message != "x" {
		t.Fatalf("reset %+v", g)
	}
	if f := (apFailures{frames: 1, clips: 2}); f.String() != "кадрів: 1, кліпів: 2" || f.none() || !(apFailures{}).none() {
		t.Fatalf("failures %q", f.String())
	}
}
