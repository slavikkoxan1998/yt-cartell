package studio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// gateGlabs holds G-Labs submissions of kind ("image" | "video") until
// release is closed; started gets a signal per held submission.
func gateGlabs(t *testing.T, env *testEnv, kinds ...string) (started chan string, release chan struct{}) {
	t.Helper()
	started, release = make(chan string, 16), make(chan struct{})
	held := map[string]bool{}
	for _, k := range kinds {
		held["/api/"+k+"/generate"] = true
	}
	wrapGlabs(t, env, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPost && held[r.URL.Path] {
			started <- r.URL.Path
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		next.ServeHTTP(w, r)
	})
	return started, release
}

func waitSignal[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(20 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func drain[T any](ch chan T) {
	for len(ch) > 0 {
		<-ch
	}
}

// pick is the row of a job kind or a single-asset kind (zero row if none).
func pick(items []QueueItem, kind JobKind, regen RegenKind) QueueItem {
	for _, it := range items {
		if it.Kind == kind && it.Regen == regen {
			return it
		}
	}
	return QueueItem{}
}

func blockText(env *testEnv, on bool) {
	env.text.mu.Lock()
	env.text.block = on
	env.text.mu.Unlock()
}

// TestQueueSnapshot: two projects — one running a job, one with a failed job
// and a reference being regenerated — then CancelAll and Retry.
func TestQueueSnapshot(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	if q := e.Queue(); len(q.Running)+len(q.Recent) != 0 || q.Counts != (QueueCounts{}) {
		t.Fatalf("empty engine: %+v", q)
	}

	// Project A: a plan hanging in the text model.
	blockText(env, true)
	a, _ := e.Create("Проєкт А")
	must(t, e.Edit(a.ID, func(p *Project) {
		p.Input.Text, p.Input.TargetSeconds = "Хлопчик загубився в осінньому лісі.", 24
	}))
	must(t, e.Run(a.ID, StagePlan, false, ""))
	waitSignal(t, env.text.started, "the bible request")

	// Project B: a failed montage, then its reference regenerated.
	b, _ := e.Create("Проєкт Б")
	must(t, e.Edit(b.ID, func(p *Project) {
		p.Characters = append(p.Characters, Character{Name: "Герой", Description: "a tall hero"})
	}))
	must(t, e.Run(b.ID, StageMontage, false, ""))
	if p := waitJob(t, e, b.ID); p.Job.Status != JobError {
		t.Fatalf("montage without clips must fail: %+v", p.Job)
	}
	started, release := gateGlabs(t, env, "image")
	bp, _ := e.Get(b.ID)
	hero := bp.Characters[0]
	must(t, e.Regen(b.ID, RegenCharacter, hero.ID, ""))
	waitSignal(t, started, "the reference submission")

	q := e.Queue()
	if len(q.Running) != 2 || len(q.Recent) != 1 || q.Counts != (QueueCounts{Running: 2, Errors: 1}) {
		t.Fatalf("snapshot %+v", q)
	}
	job, single, failed := pick(q.Running, JobPlan, ""), pick(q.Running, "", RegenCharacter), q.Recent[0]
	ap, _ := e.Get(a.ID)
	if job.Kind != JobPlan || job.Regen != "" || job.ProjectID != a.ID || job.ProjectTitle != "Проєкт А" || job.Label != "План" ||
		job.Status != JobRunning || job.Step != 2 || job.StartedAt != ap.Job.StartedAt || job.FinishedAt != 0 ||
		job.Key != fmt.Sprintf("job:%s:%d", a.ID, ap.Job.StartedAt) || job.Message != ap.Job.Message || job.Retryable() {
		t.Fatalf("running job %+v", job)
	}
	if single.Kind != "" || single.Regen != RegenCharacter || single.ItemID != hero.ID || single.ProjectID != b.ID ||
		single.ProjectTitle != "Проєкт Б" || single.Label != "Персонаж «Герой»" || single.Status != JobRunning ||
		single.Step != 3 || single.Message != "Генерується…" || single.StartedAt < job.StartedAt || single.Thumb != "" {
		t.Fatalf("running reference %+v", single)
	}
	if failed.Kind != JobMontage || failed.ProjectID != b.ID || failed.Label != "Монтаж" || failed.Status != JobError ||
		!strings.Contains(failed.Error, "Немає жодного кліпу") || failed.FinishedAt == 0 || failed.Step != 5 || !failed.Retryable() {
		t.Fatalf("failed job %+v", failed)
	}
	if err := e.Retry(a.ID); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "«План»") {
		t.Fatalf("retry while running: %v", err)
	}

	// The regenerated reference moves to the recent results.
	close(release)
	bp = waitFor(t, e, b.ID, "the reference", func(p Project) bool { return p.Characters[0].Image.Status == StatusDone })
	q = e.Queue()
	if len(q.Running) != 1 || q.Running[0].Key != job.Key || len(q.Recent) != 2 || q.Counts != (QueueCounts{Running: 1, Errors: 1}) {
		t.Fatalf("after the reference: %+v", q)
	}
	done := q.Recent[0]
	if done.Key != single.Key || done.Regen != RegenCharacter || done.Status != JobDone || done.Progress != 100 ||
		done.Message != "Готово" || done.FinishedAt < done.StartedAt || done.Thumb != e.Abs(b.ID, bp.Characters[0].Image.Path) ||
		q.Recent[1].Key != failed.Key || done.Retryable() {
		t.Fatalf("finished reference %+v", done)
	}

	// CancelAll stops A's plan.
	if n := e.CancelAll(); n != 1 {
		t.Fatalf("CancelAll stopped %d jobs", n)
	}
	if ap = waitJob(t, e, a.ID); ap.Job.Status != JobCancelled {
		t.Fatalf("job %+v", ap.Job)
	}
	q = e.Queue()
	var stopped *QueueItem
	for i := range q.Recent {
		if q.Recent[i].Kind == JobPlan {
			stopped = &q.Recent[i]
		}
	}
	// Newest first; the reference may have finished in the same millisecond.
	if len(q.Running) != 0 || len(q.Recent) != 3 || stopped == nil || stopped.ProjectID != a.ID || stopped.Status != JobCancelled ||
		stopped.FinishedAt < q.Recent[0].FinishedAt || !stopped.Retryable() || q.Counts != (QueueCounts{Errors: 1}) || e.CancelAll() != 0 {
		t.Fatalf("after CancelAll: %+v", q)
	}

	// Retry re-runs the stopped plan; this time the model answers.
	blockText(env, false)
	must(t, e.Retry(a.ID))
	ap = waitJob(t, e, a.ID)
	mustJobDone(t, ap)
	if ap.Job.Kind != JobPlan || len(ap.Scenes) != 3 {
		t.Fatalf("retried plan %+v, %d scenes", ap.Job, len(ap.Scenes))
	}
	if err := e.Retry(a.ID); err == nil || err.Error() != "Останній етап завершився успішно — повторювати нічого" {
		t.Fatalf("retry after success: %v", err)
	}
	c, _ := e.Create("Порожній")
	if err := e.Retry(c.ID); err == nil || !strings.Contains(err.Error(), "ще нічого не запускали") {
		t.Fatalf("retry without a job: %v", err)
	}
	if err := e.Retry("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retry of a missing project: %v", err)
	}

	// A deleted project leaves the queue with its results.
	must(t, e.Delete(b.ID))
	for _, it := range e.Queue().Recent {
		if it.ProjectID == b.ID {
			t.Fatalf("deleted project still queued: %+v", it)
		}
	}
}

// TestRetryFailedPlan: Retry (through RetryItem) re-runs the failed stage.
func TestRetryFailedPlan(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	var bibles atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if strings.Contains(string(body), "award-winning film director") && bibles.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"title\":\"x\",\"scenes\":[]}"}}]}`)
			return
		}
		env.text.ServeHTTP(w, r)
	}))
	allow(srv)
	t.Cleanup(srv.Close)
	_ = env.store.Update(func(c *config.Config) { c.Text.BaseURL = srv.URL + "/v1" }) // in-memory store

	p, _ := e.Create("Повтор")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text, p.Input.TargetSeconds = "Хлопчик загубився в осінньому лісі.", 24
	}))
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobError || p.Job.Error != "Модель не повернула сцен — спробуй ще раз" {
		t.Fatalf("first plan %+v", p.Job)
	}
	q := e.Queue()
	if len(q.Recent) != 1 || q.Recent[0].Kind != JobPlan || !q.Recent[0].Retryable() || q.Counts.Errors != 1 {
		t.Fatalf("queue %+v", q)
	}
	must(t, e.RetryItem(q.Recent[0]))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if p.Job.Kind != JobPlan || len(p.Scenes) != 3 || bibles.Load() != 2 {
		t.Fatalf("retry: %+v, %d scenes, %d bible requests", p.Job, len(p.Scenes), bibles.Load())
	}
	if q = e.Queue(); q.Counts.Errors != 0 || q.Recent[0].Status != JobDone {
		t.Fatalf("queue after the retry %+v", q)
	}
}

// TestQueueSingleAssetError: a failed regeneration is a recent error until
// it is retried (RetryItem → Regen); only the latest result is kept.
func TestQueueSingleAssetError(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	e.Queue() // the queue follows single assets from now on
	p, _ := e.Create("Помилка")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Locations = append(p.Locations, Location{Name: "Ліс", Description: "FAILME autumn forest"})
	}))
	p, _ = e.Get(p.ID)
	loc := p.Locations[0]
	env.gl.mu.Lock()
	env.gl.failOn = "FAILME"
	env.gl.mu.Unlock()
	must(t, e.Regen(p.ID, RegenLocation, loc.ID, ""))
	waitFor(t, e, p.ID, "the failure", func(p Project) bool { return p.Locations[0].Image.Status == StatusError })
	q := e.Queue()
	if len(q.Running) != 0 || len(q.Recent) != 1 || q.Counts != (QueueCounts{Errors: 1}) {
		t.Fatalf("queue %+v", q)
	}
	failed := q.Recent[0]
	if failed.Regen != RegenLocation || failed.ItemID != loc.ID || failed.Label != "Локація «Ліс»" || failed.Status != JobError ||
		!strings.Contains(failed.Error, "G-Labs [400]: prompt rejected") || failed.Message != failed.Error || !failed.Retryable() {
		t.Fatalf("failed reference %+v", failed)
	}

	env.gl.mu.Lock()
	env.gl.failOn = ""
	env.gl.mu.Unlock()
	must(t, e.RetryItem(failed))
	q = e.Queue()
	if len(q.Running) != 1 || q.Running[0].Regen != RegenLocation || q.Counts != (QueueCounts{Running: 1}) {
		t.Fatalf("while retrying the error is gone: %+v", q)
	}
	p = waitFor(t, e, p.ID, "the retry", func(p Project) bool { return p.Locations[0].Image.Status == StatusDone })
	q = e.Queue()
	if len(q.Running) != 0 || len(q.Recent) != 1 || q.Recent[0].Status != JobDone ||
		q.Recent[0].Thumb != e.Abs(p.ID, p.Locations[0].Image.Path) || q.Counts != (QueueCounts{}) {
		t.Fatalf("after the retry %+v", q)
	}

	// Deleting the location drops its result.
	must(t, e.Edit(p.ID, func(p *Project) { p.Locations = p.Locations[:0] }))
	if q = e.Queue(); len(q.Recent) != 0 {
		t.Fatalf("result of a deleted item %+v", q.Recent)
	}
}

// TestQueueJobOwnsItsAssets: assets a running job makes are the job's row;
// a regeneration of another kind at the same time is its own row.
func TestQueueJobOwnsItsAssets(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, func(c *config.Config) { c.Concurrency = 1 })
	e := env.e
	p := seedScenes(t, e, 2)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Characters = append(p.Characters, Character{Name: "Тарас", Description: "boy"})
	}))
	started, release := gateGlabs(t, env, "image", "video")
	e.Queue()
	must(t, e.Run(p.ID, StageVideos, false, ""))
	waitSignal(t, started, "the first clip")
	p, _ = e.Get(p.ID)
	must(t, e.Regen(p.ID, RegenCharacter, p.Characters[0].ID, ""))
	waitSignal(t, started, "the reference")
	q := e.Queue()
	if len(q.Running) != 2 || pick(q.Running, JobVideos, "").Label != "Відео сцен" ||
		pick(q.Running, "", RegenCharacter).Label != "Персонаж «Тарас»" {
		t.Fatalf("running %+v", q.Running)
	}
	if p, _ = e.Get(p.ID); p.Scenes[0].Video.Status != StatusRunning {
		t.Fatalf("the clip must be generating now: %+v", p.Scenes[0].Video)
	}
	close(release)
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	waitFor(t, e, p.ID, "the reference", func(p Project) bool { return p.Characters[0].Image.Status == StatusDone })
	q = e.Queue()
	if len(q.Running) != 0 || len(q.Recent) != 2 || q.Counts != (QueueCounts{}) {
		t.Fatalf("finished %+v", q)
	}
	for _, it := range q.Recent {
		if it.Regen == RegenVideo {
			t.Fatalf("a clip of the job was counted as a single asset: %+v", it)
		}
	}
}

// TestCancelAll stops every running job, idle projects are untouched.
func TestCancelAll(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	blockText(env, true)
	var ids []string
	for i := 0; i < 2; i++ {
		p, _ := e.Create(fmt.Sprintf("План %d", i+1))
		must(t, e.Edit(p.ID, func(p *Project) { p.Input.Text = "Історія." }))
		must(t, e.Run(p.ID, StagePlan, false, ""))
		ids = append(ids, p.ID)
	}
	idle, _ := e.Create("Спокій")
	if q := e.Queue(); q.Counts.Running != 2 {
		t.Fatalf("running %+v", q.Running)
	}
	if n := e.CancelAll(); n != 2 {
		t.Fatalf("CancelAll stopped %d", n)
	}
	for _, id := range ids {
		if p := waitJob(t, e, id); p.Job.Status != JobCancelled {
			t.Fatalf("job %+v", p.Job)
		}
	}
	if p, _ := e.Get(idle.ID); p.Job != nil {
		t.Fatalf("idle project got a job %+v", p.Job)
	}
	if q := e.Queue(); q.Counts != (QueueCounts{}) || len(q.Recent) != 2 {
		t.Fatalf("after CancelAll %+v", q)
	}
}

// TestAutopilotAcceptsOwnVoice (Run in jobs.go): a project with its own
// voice-over and no text may start the autopilot — the plan transcribes the
// recording first.
func TestAutopilotAcceptsOwnVoice(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("Свій голос")
	st := e.state(p.ID)
	e.update(st, func(p *Project) { p.Input.Mode = ModeVoice })
	if err := e.Run(p.ID, StageAutopilot, false, ""); err == nil || err.Error() != "Спершу напиши історію або сценарій" {
		t.Fatalf("voice mode without a recording: %v", err)
	}
	e.update(st, func(p *Project) { p.Input.VoiceFile = "audio/missing-voice.mp3" })
	must(t, e.Run(p.ID, StageAutopilot, false, ""))
	// The recording is not there, so the plan's transcription fails.
	if p = waitJob(t, e, p.ID); p.Job.Kind != JobAutopilot || p.Job.Status != JobError || p.Job.Error == "Спершу напиши історію або сценарій" {
		t.Fatalf("job %+v", p.Job)
	}
}

// TestRetryRegisteredKind: a job kind another file adds (not started by
// Run) registers how to restart it and which assets it makes. Not parallel:
// it changes the package registries while no parallel test runs yet.
func TestRetryRegisteredKind(t *testing.T) {
	const kind JobKind = "queue-test-kind"
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("Реєстр")
	e.update(e.state(p.ID), func(p *Project) {
		p.Job = &Job{Kind: kind, Status: JobError, Error: "x", StartedAt: 1, FinishedAt: 2}
	})
	it := e.Queue().Recent[0]
	if err := e.Retry(p.ID); err == nil || it.Retryable() ||
		err.Error() != "Етап «queue-test-kind» не можна повторити звідси — запусти його ще раз у проєкті" {
		t.Fatalf("unregistered kind: %v (retryable %v)", err, it.Retryable())
	}
	if !jobCovers(kind, RegenVoice) || jobStep(kind, 2) != 2 {
		t.Fatal("an unknown kind is assumed to make every asset")
	}
	var restarted string
	jobRestarts[kind] = func(_ *Engine, id string) error { restarted = id; return nil }
	jobAssets[kind] = nil
	defer delete(jobRestarts, kind)
	defer delete(jobAssets, kind)
	must(t, e.RetryItem(it))
	if restarted != p.ID || jobCovers(kind, RegenVoice) || !it.Retryable() {
		t.Fatalf("registered kind: restarted %q, covers voices %v", restarted, jobCovers(kind, RegenVoice))
	}
}

func TestQueueHelpers(t *testing.T) {
	labels := [][2]string{
		{queueJobLabel(JobFrames, false), "Кадри сцен"},
		{queueJobLabel(JobFrames, true), "Розкадровка 3×3"},
		{queueJobLabel(JobAutopilot, false), "Автопілот"},
		{queueJobLabel(JobRender, false), "Відео й озвучка"},
		{queueJobLabel("queue-test-unknown", false), "queue-test-unknown"},
		{assetLabel(RegenVideo, 3, "", false), "Відео сцени 3"},
		{assetLabel(RegenVoice, 5, "", false), "Озвучка сцени 5"},
		{assetLabel(RegenFrame, 2, "", true), "Розкадровка сцени 2"},
		{assetLabel(RegenCharacter, 0, " Тарас ", false), "Персонаж «Тарас»"},
		{assetLabel(RegenLocation, 0, "", false), "Локація"},
	}
	for _, l := range labels {
		if l[0] != l[1] {
			t.Errorf("label %q, want %q", l[0], l[1])
		}
	}
	covers := []struct {
		job  JobKind
		kind RegenKind
		want bool
	}{
		{"", RegenFrame, false}, {JobFrames, RegenCharacter, true}, {JobFrames, RegenVideo, false},
		{JobVideos, RegenVideo, true}, {JobVideos, RegenVoice, false}, {JobRender, RegenVoice, true},
		{JobPlan, RegenFrame, false}, {JobMontage, RegenVideo, false}, {JobAutopilot, RegenLocation, true},
		{"something-new", RegenVoice, true},
	}
	for _, c := range covers {
		if jobCovers(c.job, c.kind) != c.want {
			t.Errorf("jobCovers(%s, %s) != %v", c.job, c.kind, c.want)
		}
	}
	for k, s := range jobStages {
		if string(k) != string(s) || !restartable(k) {
			t.Errorf("stage of %s: %s", k, s)
		}
	}
	if restartable("future") || jobStep(JobAutopilot, 4) != 4 || jobStep(JobCover, 1) != 5 || regenStep(RegenVoice) != 4 {
		t.Error("stage/step fallbacks")
	}
	q := &queueTracker{}
	for i := 0; i < queueRecentMax+5; i++ {
		q.push(QueueItem{ProjectID: "p", Regen: RegenFrame, ItemID: fmt.Sprint(i % 33)})
	}
	if len(q.ring) != queueRecentMax || q.ring[0].ItemID != fmt.Sprint((queueRecentMax+4)%33) {
		t.Fatalf("ring %d, newest %q", len(q.ring), q.ring[0].ItemID)
	}
	seen := map[string]bool{}
	for _, it := range q.ring {
		if seen[it.ItemID] {
			t.Fatalf("two results of one asset: %s", it.ItemID)
		}
		seen[it.ItemID] = true
	}
}
