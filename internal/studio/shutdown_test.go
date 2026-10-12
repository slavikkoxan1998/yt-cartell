package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// TestShutdownStopsJobs: closing the app cancels a running job at once,
// records it on disk and refuses new work.
func TestShutdownStopsJobs(t *testing.T) {
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
	must(t, e.Shutdown(10*time.Second))
	if d := time.Since(t0); d > 5*time.Second {
		t.Fatalf("shutdown took %s", d)
	}
	q, _ := e.Get(p.ID)
	if q.Job == nil || q.Job.Status != JobCancelled {
		t.Fatalf("job after shutdown %+v", q.Job)
	}
	b, err := os.ReadFile(filepath.Join(e.Dir(p.ID), projectFile))
	must(t, err)
	var saved Project
	must(t, json.Unmarshal(b, &saved))
	if saved.Job == nil || saved.Job.Status != JobCancelled {
		t.Fatalf("saved job %+v", saved.Job)
	}
	if err := e.Run(p.ID, StagePlan, false, ""); !errors.Is(err, ErrClosing) {
		t.Fatalf("run after shutdown: %v", err)
	}
	if err := e.Regen(p.ID, RegenFrame, "x", ""); !errors.Is(err, ErrClosing) {
		t.Fatalf("regen after shutdown: %v", err)
	}
}

// Two settings saves racing with a data folder switch must load the new
// folder once (one live copy per project) and end in the latest folder.
func TestConfigSwitchSerialised(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	dirs := []string{t.TempDir(), t.TempDir()}
	for _, d := range dirs {
		// Enough projects that loading a folder takes a moment.
		for k := 0; k < 120; k++ {
			id := "same-proj"
			if k > 0 {
				id = fmt.Sprintf("filler-%03d", k)
			}
			pd := filepath.Join(d, "projects", id)
			must(t, os.MkdirAll(pd, 0o755))
			must(t, os.WriteFile(filepath.Join(pd, projectFile), []byte(`{"title":"Спільний","step":2}`), 0o644))
		}
	}
	for i := 0; i < 16; i++ {
		dir := dirs[i%2]
		seen := map[*proj]bool{}
		var mu sync.Mutex
		stop := make(chan struct{})
		polled := make(chan struct{})
		go func() {
			defer close(polled)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if st := e.state("same-proj"); st != nil && within(dir, st.dir) {
					mu.Lock()
					seen[st] = true
					mu.Unlock()
				}
				time.Sleep(50 * time.Microsecond)
			}
		}()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = env.store.Update(func(c *config.Config) { c.DataDir = dir })
		}()
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(i%4) * 200 * time.Microsecond)
			_ = env.store.Update(func(c *config.Config) { c.TTS.Voice = fmt.Sprint("voice-", i) })
		}()
		wg.Wait()
		close(stop)
		<-polled
		if st := e.state("same-proj"); st != nil {
			seen[st] = true
		}
		if len(seen) != 1 {
			t.Fatalf("trial %d: folder loaded %d times", i, len(seen))
		}
		e.mu.Lock()
		got := e.dataDir
		e.mu.Unlock()
		if got != filepath.Clean(dir) {
			t.Fatalf("trial %d: engine in %s, settings say %s", i, got, dir)
		}
	}
}

// FF never locates on the caller's goroutine; a background retry fills it in
// and notifies.
func TestFFRetriesInBackground(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	var listed atomic.Int32
	unsub := e.Subscribe(func(id string) {
		if id == "" {
			listed.Add(1)
		}
	})
	defer unsub()
	e.mu.Lock()
	e.ff, e.ffAt = media.FF{}, time.Now().Add(-time.Minute)
	e.mu.Unlock()
	if ff := e.FF(); ff.OK() {
		t.Fatal("FF located synchronously")
	}
	e.mu.Lock()
	busy := e.ffBusy
	e.mu.Unlock()
	if !busy && !e.FF().OK() {
		t.Fatal("no background retry started")
	}
	deadline := time.Now().Add(10 * time.Second)
	for !e.FF().OK() {
		if time.Now().After(deadline) {
			t.Fatal("background retry never found ffmpeg")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for listed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no notification after ffmpeg was found")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A stale retry (paths changed meanwhile) must not overwrite the result.
	e.mu.Lock()
	want := e.ff
	e.ffKey = "other|paths"
	e.mu.Unlock()
	_, _ = e.locateFF(config.Config{FFmpegPath: filepath.Join(t.TempDir(), "missing", "ffmpeg")}, false)
	e.mu.Lock()
	kept := e.ff == want
	e.mu.Unlock()
	if !kept {
		t.Fatal("stale locate overwrote FF")
	}
}

// Files a project stops referencing move to trash/ (never deleted; web parity).
func TestDiscardMovesToTrash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := newProj("p", dir, &Project{})
	write := func(rel string) {
		t.Helper()
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		must(t, os.WriteFile(abs, []byte(rel), 0o644))
	}
	write("images/a.png")
	write("trash/images/a.png") // an older one with the same name
	write("videos/v.mp4")
	write("trash/old.mp4")
	outside := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-outside.txt")
	must(t, os.WriteFile(outside, []byte("x"), 0o644))
	defer os.Remove(outside)

	e := &Engine{}
	e.discardFiles(st, []string{"images/a.png", "videos/v.mp4", "trash/old.mp4", "../" + filepath.Base(outside), "", "images/missing.png"})
	for _, gone := range []string{"images/a.png", "videos/v.mp4"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Fatalf("%s still in place", gone)
		}
	}
	for rel, want := range map[string]string{
		"trash/images/a.png":   "trash/images/a.png",
		"trash/images/a-2.png": "images/a.png",
		"trash/videos/v.mp4":   "videos/v.mp4",
		"trash/old.mp4":        "trash/old.mp4",
	} {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || string(b) != want {
			t.Fatalf("%s: %q %v", rel, b, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("a file outside the project was touched")
	}
	// A deleted project is left alone.
	write("images/b.png")
	st.deleted.Store(true)
	e.discardFiles(st, []string{"images/b.png"})
	if _, err := os.Stat(filepath.Join(dir, "images", "b.png")); err != nil {
		t.Fatal("discard touched a deleted project")
	}
}

// A generation stopped by closing the app keeps the previous file.
func TestFailOnCancelKeepsPreviousFile(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Scenes = []Scene{{ID: "s1", Title: "Одна", Narration: "Текст.", CharacterIDs: []string{}}, {ID: "s2", Title: "Дві", Narration: "Текст.", CharacterIDs: []string{}}}
	}))
	p, _ = e.Get(p.ID)
	if len(p.Scenes) != 2 {
		t.Fatalf("scenes %d", len(p.Scenes))
	}
	st := e.state(p.ID)
	e.update(st, func(p *Project) {
		p.Scenes[0].Frame = Asset{Status: StatusRunning, Path: "images/old.png"}
		p.Scenes[1].Frame = Asset{Status: StatusRunning}
	})
	e.fail(st, assetRef{slotFrame, p.Scenes[0].ID}, fmt.Errorf("image: %w", context.Canceled))
	e.fail(st, assetRef{slotFrame, p.Scenes[1].ID}, context.Canceled)
	q, _ := e.Get(p.ID)
	if f := q.Scenes[0].Frame; f.Status != StatusDone || f.Error != "" {
		t.Fatalf("with a previous file: %+v", f)
	}
	if f := q.Scenes[1].Frame; f.Status != StatusError || f.Error != "Зупинено" {
		t.Fatalf("without a previous file: %+v", f)
	}
}
