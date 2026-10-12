package home

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// hideFFmpeg makes this test's engine find no FFmpeg: no PATH, a temp home
// and app-data folder (scoop shims, WinGet links, the studio's own copy).
func hideFFmpeg(t *testing.T) {
	tmp := t.TempDir()
	for _, k := range []string{"PATH", "Path"} {
		t.Setenv(k, "")
	}
	for _, k := range []string{"USERPROFILE", "HOME", "LOCALAPPDATA", "ProgramFiles", "ProgramData"} {
		t.Setenv(k, tmp)
	}
}

// ffCtx is an offline demo engine with its keys (so only FFmpeg is missing)
// and without FFmpeg; nil when FFmpeg is still found (C:\ffmpeg\bin…).
func ffCtx(t *testing.T) (*core.Ctx, *core.HeadlessHost) {
	t.Helper()
	hideFFmpeg(t)
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Shutdown(2 * time.Second) })
	if e.FF().FFmpeg != "" {
		t.Skipf("FFmpeg is still found at %s", e.FF().FFmpeg)
	}
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	return c, h
}

var ffHintStates = map[string]*studio.ToolInstall{
	"missing":    nil,
	"installing": {Running: true, Pct: 63, Msg: "Завантажуємо FFmpeg 9.0.2 · 69,1 МБ з 109 МБ"},
	"failed":     {Err: "Немає з'єднання з github.com — перевір інтернет і спробуй ще раз", At: time.Unix(1, 0)},
}

// TestFFmpegHint: with FFmpeg missing Головна shows the call-out with the
// install buttons; a running installation shows its progress; in demo mode
// nothing starts.
func TestFFmpegHint(t *testing.T) {
	c, h := ffCtx(t)
	p := New()
	draw(c, h, 1440, 900, 2, func(gtx C) D { return p.Home(gtx, c) })
	if p.svc.ffOK {
		t.Fatal("FFmpeg found")
	}
	if _, ok := p.setupCard(c); !ok {
		t.Fatal("no call-out while FFmpeg is missing")
	}
	for name, st := range ffHintStates {
		p.ff.pin = st
		draw(c, h, 390, 844, 2, func(gtx C) D { return p.Home(gtx, c) })
		if _, ok := p.setupCard(c); !ok {
			t.Fatalf("%s: no call-out", name)
		}
	}
	// A run that just ended: FFmpeg is looked up again at once.
	p.ff.pin, p.ff.was, p.svc.ffAt = &studio.ToolInstall{Done: true, At: time.Now()}, true, time.Now()
	p.ffWatch(c)
	if !p.svc.ffAt.IsZero() || p.ff.was {
		t.Fatal("finished run not noticed")
	}
	// Offline: «Встановити автоматично» explains instead of starting.
	p.ff.pin = nil
	p.ff.install.Click()
	draw(c, h, 1440, 900, 1, func(gtx C) D { return p.Home(gtx, c) })
	if c.E.FFmpegInstall().Running || p.ff.localErr == "" {
		t.Fatalf("demo mode: running %v, message %q", c.E.FFmpegInstall().Running, p.ff.localErr)
	}
}

// TestFFmpegHintShots renders the call-out (GUISHOTS=<dir>).
func TestFFmpegHintShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the FFmpeg call-out on Головна")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, name := range []string{"missing", "installing", "failed"} {
		if only != "" && !strings.Contains(","+only+",", ",home-ffmpeg-"+name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range [][2]int{{1440, 900}, {390, 844}} {
				theme := "light"
				if dark {
					theme = "dark"
				}
				file := fmt.Sprintf("home-ffmpeg-%s-%s-%dx%d.png", name, theme, sz[0], sz[1])
				t.Run(file, func(t *testing.T) {
					c, h := ffCtx(t)
					c.Th.SetPaletteNow(dark)
					p := New()
					p.ff.pin = ffHintStates[name]
					if err := core.Shot(c, h, sz[0], sz[1], 1, filepath.Join(out, file),
						func(gtx C) D { return p.Home(gtx, c) }, gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
