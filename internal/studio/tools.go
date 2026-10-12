package studio

// Helper programs the studio installs itself (package toolsinst): FFmpeg via
// «Встановити автоматично» in Settings or on Головна. The installation runs
// on the engine, so every screen sees the same progress; it survives route
// changes and stops with Shutdown.

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"ytcartell/internal/media"
	"ytcartell/internal/toolsinst"
)

// ToolInstall is the state of an installation the user started.
type ToolInstall struct {
	Running   bool
	Pct       int    // 0–100
	Msg       string // the installer's current line
	Err       string // the last run failed with this ("" = no failure)
	Done      bool   // the last run finished well
	Cancelled bool   // the last run was cancelled (a partial download resumes next time)
	Version   string // what the last good run installed ("ffmpeg version 9.0.2-…")
	At        time.Time
}

// toolJob is the engine's FFmpeg installation.
type toolJob struct {
	mu     sync.Mutex
	st     ToolInstall
	cancel context.CancelFunc
}

// ensureFFmpeg installs FFmpeg (tests replace it; never the network there).
var ensureFFmpeg = toolsinst.EnsureFFmpeg

// InstallFFmpeg starts the FFmpeg installation in the background (Homebrew
// or the pinned official build; a re-run repairs a damaged copy). false when
// one is already running or the app is closing. Progress and the result
// come through FFmpegInstall and a "" notification.
func (e *Engine) InstallFFmpeg() bool {
	if e.closing.Load() {
		return false
	}
	t := &e.ffInst
	t.mu.Lock()
	if t.st.Running {
		t.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.st = ToolInstall{Running: true, Msg: "Готуємо встановлення…", At: time.Now()}
	t.mu.Unlock()
	e.notify("")
	go func() {
		defer cancel()
		ff, err := ensureFFmpeg(ctx, func(pct int, msg string) {
			t.mu.Lock()
			t.st.Pct = max(0, min(100, pct))
			if msg != "" {
				t.st.Msg = msg
			}
			t.mu.Unlock()
			e.notify("")
		})
		if err == nil {
			// Locate finds the new copy (a configured path that is gone
			// falls back to auto-detection).
			e.locateFF(e.cfg.Get(), true)
		}
		t.mu.Lock()
		t.st.Running, t.st.At = false, time.Now()
		t.cancel = nil
		switch {
		case err == nil:
			t.st.Done, t.st.Pct, t.st.Version = true, 100, ff.Version
		case errors.Is(err, context.Canceled):
			t.st.Cancelled, t.st.Msg = true, "Скасовано"
		default:
			t.st.Err = err.Error()
			log.Printf("[studio] FFmpeg install: %v", err)
		}
		t.mu.Unlock()
		e.notify("")
	}()
	return true
}

// CancelFFmpegInstall stops a running FFmpeg installation (a partial
// download resumes next time).
func (e *Engine) CancelFFmpegInstall() {
	t := &e.ffInst
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// FFmpegInstall is the current state of the FFmpeg installation.
func (e *Engine) FFmpegInstall() ToolInstall {
	t := &e.ffInst
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.st
}

// ToolOffer says what «Встановити автоматично» does on this computer.
type ToolOffer struct {
	OK       bool   // something can be installed here
	Homebrew bool   // macOS with Homebrew: brew install ffmpeg
	Version  string // the pinned official build ("9.0.2")
	From     string // its publisher ("gyan.dev")
	Bytes    int64  // its download size
	Note     string // !OK: what to do instead (Ukrainian)
}

// FFmpegOffer describes the automatic FFmpeg installation for this system
// (it looks for Homebrew on a Mac: call it once per visit, not every frame).
func (e *Engine) FFmpegOffer() ToolOffer {
	o := toolsinst.FFmpegOffer()
	return ToolOffer{OK: o.OK, Homebrew: o.Homebrew, Version: o.Version, From: o.From, Bytes: o.Bytes, Note: o.Note}
}

// FFmpegManaged reports whether the located ffmpeg is the studio's own copy.
func (e *Engine) FFmpegManaged() bool { return media.IsInstalled(e.FF().FFmpeg) }
