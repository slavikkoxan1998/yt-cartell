package queue

import (
	"image"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

func TestRowHelpers(t *testing.T) {
	job := studio.QueueItem{Kind: studio.JobVideos, Label: "Відео сцен", Status: studio.JobDone, Message: "Готово 1/3, помилок: 2"}
	if !PartlyFailed(job) {
		t.Fatal("a job with failed clips is not flagged")
	}
	if st, ok := retryStage(job); !ok || st != studio.StageVideos {
		t.Fatalf("retry stage = %q %v", st, ok)
	}
	job.Message = "Відео й озвучка готові (3)"
	if PartlyFailed(job) {
		t.Fatal("a clean job is flagged")
	}
	run := studio.QueueItem{Kind: studio.JobAutopilot, Label: "Автопілот", Status: studio.JobRunning, Message: "Автопілот · Кадри · 3/8"}
	if got := message(run); got != "Кадри · 3/8" {
		t.Fatalf("message = %q", got)
	}
	if !indeterminate(run) {
		t.Fatal("a job at 0 % should sweep")
	}
	run.Progress = 40
	if indeterminate(run) {
		t.Fatal("a job with progress should show it")
	}
	clip := studio.QueueItem{Regen: studio.RegenVideo, Status: studio.JobRunning}
	if indeterminate(clip) {
		t.Fatal("a clip reports real progress")
	}
	if s := Subtitle(studio.QueueSnapshot{Counts: studio.QueueCounts{Running: 2, Errors: 1}}); s != "Генерується: 2 · 1 помилка" {
		t.Fatalf("subtitle = %q", s)
	}
}

func TestFeedAndViews(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Config().Update(func(c *config.Config) {
		c.Text.BaseURL = "http://127.0.0.1:1/v1"
		c.Glabs.BaseURL = "http://127.0.0.1:1"
		c.TTS.APIKey = ""
	})
	defer e.Shutdown(2 * time.Second)
	c, h := core.NewHeadless(e)
	defer c.Close()
	f := NewFeed(e, nil)
	defer f.Close()
	if n := f.Running(); n != 0 {
		t.Fatalf("running = %d", n)
	}
	// A plan that fails at once (the text endpoint is a closed port) shows
	// up as a recent error.
	p, err := e.Create("Помилка")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Edit(p.ID, func(q *studio.Project) {
		q.Input.Text = "Коротка історія для перевірки черги."
	})
	if err := e.Run(p.ID, studio.StagePlan, false, ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s := f.Snapshot(); s.Counts.Errors > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	snap := f.Snapshot()
	if snap.Counts.Errors == 0 || len(snap.Recent) == 0 || !snap.Recent[0].Retryable() {
		t.Fatalf("snapshot = %+v", snap)
	}
	v := New(f)
	var router input.Router
	var ops op.Ops
	for i, sz := range [][2]int{{1440, 900}, {390, 844}} {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Now: gui.SnapshotEpoch.Add(time.Duration(i) * time.Second),
			Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(sz[0], sz[1])), Source: router.Source()}
		core.Frame(c, h, func(gtx C) D {
			v.Page(gtx, c)
			return v.DrawerBody(gtx, c)
		})(gtx)
		router.Frame(&ops)
	}
	// «Відкрити проєкт» on the row navigates to the project's step.
	st := v.rows[snap.Recent[0].Key]
	if st == nil {
		t.Fatal("no row state")
	}
	st.open.Click()
	ops.Reset()
	gtx := layout.Context{Ops: &ops, Now: gui.SnapshotEpoch.Add(5 * time.Second),
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(800, 600)), Source: router.Source()}
	core.Frame(c, h, func(gtx C) D { return v.DrawerBody(gtx, c) })(gtx)
	if r := h.Current(); r.Page != core.PageProject || r.ProjectID != p.ID {
		t.Fatalf("route = %+v", r)
	}
}
