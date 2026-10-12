package studio

import (
	"context"
	"errors"
	"testing"
	"time"

	"ytcartell/internal/toolsinst"
)

// TestInstallFFmpegState: the engine runs one FFmpeg installation at a time
// in the background, publishes its progress and result, notifies "" and can
// be cancelled. The installer is a fake: nothing is downloaded.
func TestInstallFFmpegState(t *testing.T) {
	env := newEnv(t, nil)
	e := env.e
	release := make(chan struct{})
	var calls int
	old := ensureFFmpeg
	ensureFFmpeg = func(ctx context.Context, progress toolsinst.Progress) (toolsinst.FFmpeg, error) {
		calls++
		progress(40, "Завантажуємо FFmpeg 9.0.2 · 44 МБ з 109 МБ")
		select {
		case <-release:
			return toolsinst.FFmpeg{FFmpeg: "x", FFprobe: "y", Version: "ffmpeg version 9.0.2-test"}, nil
		case <-ctx.Done():
			return toolsinst.FFmpeg{}, ctx.Err()
		}
	}
	t.Cleanup(func() { ensureFFmpeg = old })

	notified := make(chan struct{}, 64)
	cancelSub := e.Subscribe(func(id string) {
		if id == "" {
			select {
			case notified <- struct{}{}:
			default:
			}
		}
	})
	defer cancelSub()

	wait := func(cond func(ToolInstall) bool) ToolInstall {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if st := e.FFmpegInstall(); cond(st) {
				return st
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("timeout: %+v", e.FFmpegInstall())
		return ToolInstall{}
	}

	if !e.InstallFFmpeg() {
		t.Fatal("did not start")
	}
	if e.InstallFFmpeg() {
		t.Fatal("a second installation started")
	}
	st := wait(func(s ToolInstall) bool { return s.Pct == 40 })
	if !st.Running || st.Msg == "" {
		t.Fatalf("running state %+v", st)
	}
	close(release)
	st = wait(func(s ToolInstall) bool { return !s.Running })
	if !st.Done || st.Pct != 100 || st.Err != "" || st.Version != "ffmpeg version 9.0.2-test" {
		t.Fatalf("done state %+v", st)
	}
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("no notification")
	}

	// Cancel: no error is shown, the state says so.
	release = make(chan struct{})
	if !e.InstallFFmpeg() {
		t.Fatal("did not start again")
	}
	wait(func(s ToolInstall) bool { return s.Pct == 40 })
	e.CancelFFmpegInstall()
	st = wait(func(s ToolInstall) bool { return !s.Running })
	if st.Done || st.Err != "" || !st.Cancelled {
		t.Fatalf("cancelled state %+v", st)
	}

	// A failure keeps its message for the UI.
	ensureFFmpeg = func(context.Context, toolsinst.Progress) (toolsinst.FFmpeg, error) {
		return toolsinst.FFmpeg{}, errors.New("Не вистачає місця на диску — звільни місце й спробуй ще раз")
	}
	e.InstallFFmpeg()
	st = wait(func(s ToolInstall) bool { return !s.Running && s.Err != "" })
	if st.Done {
		t.Fatalf("failed state %+v", st)
	}
	if calls != 2 {
		t.Fatalf("installer ran %d times", calls)
	}

	// Closing: nothing new starts.
	e.closing.Store(true)
	if e.InstallFFmpeg() {
		t.Fatal("started while closing")
	}
	e.closing.Store(false)
}
