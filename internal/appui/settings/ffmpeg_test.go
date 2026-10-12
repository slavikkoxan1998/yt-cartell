package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

// Headless screenshots of the FFmpeg and «Сцен одночасно» cards (skipped
// unless GUISHOTS=<dir>):
//
//	GUISHOTS=../../../screenshots/new/settings go test ./internal/appui/settings -run TestToolShots
//
// The FFmpeg card is pinned to a fixed state (ffPreview): nothing is
// located, installed or downloaded.

var (
	winOffer   = studio.ToolOffer{OK: true, Version: "9.0.2", From: "gyan.dev", Bytes: 114768076}
	ownFFmpeg  = media.FF{FFmpeg: `C:\Users\Олена\AppData\Local\YT Cartell Studio\ffmpeg\bin\ffmpeg.exe`, FFprobe: `C:\Users\Олена\AppData\Local\YT Cartell Studio\ffmpeg\bin\ffprobe.exe`}
	ffPreviews = map[string]ffPreview{
		"missing":    {offer: winOffer, goos: "windows"},
		"manual":     {offer: winOffer, goos: "windows"},
		"manual-mac": {offer: studio.ToolOffer{OK: true, Homebrew: true, Version: "9.0.2"}, goos: "darwin"},
		"installing": {offer: winOffer, goos: "windows", inst: studio.ToolInstall{Running: true, Pct: 46, Msg: "Завантажуємо FFmpeg 9.0.2 · 50,3 МБ з 109 МБ"}},
		"failed": {offer: winOffer, goos: "windows", inst: studio.ToolInstall{Err: "Завантаження з github.com зупинилось — перевір інтернет і спробуй ще раз: продовжимо з того ж місця",
			At: time.Unix(1, 0)}},
		"installed": {offer: winOffer, goos: "windows", ff: ownFFmpeg, managed: true,
			inst: studio.ToolInstall{Done: true, Pct: 100, Version: "ffmpeg version 9.0.2-essentials_build-www.gyan.dev Copyright (c) 2000-2026", At: time.Unix(1, 0)}},
	}
)

type toolShot struct {
	name, section string
	ff            string // ffPreviews key ("" = the real card)
	config        func(cf *config.Config)
}

var toolShots = []toolShot{
	{name: "ffmpeg-missing", section: "ffmpeg", ff: "missing"},
	{name: "ffmpeg-manual", section: "ffmpeg", ff: "manual"},
	{name: "ffmpeg-manual-mac", section: "ffmpeg", ff: "manual-mac"},
	{name: "ffmpeg-installing", section: "ffmpeg", ff: "installing"},
	{name: "ffmpeg-failed", section: "ffmpeg", ff: "failed"},
	{name: "ffmpeg-installed", section: "ffmpeg", ff: "installed"},
	{name: "speed", section: "speed", config: func(cf *config.Config) { cf.Concurrency = 6 }},
	{name: "speed-high", section: "speed", config: func(cf *config.Config) { cf.Concurrency = 9 }},
	{name: "speed-royal", section: "speed", config: func(cf *config.Config) {
		cf.Video.Provider, cf.Custom.VideoPreset, cf.Concurrency = "custom", "royaltechno", 14
	}},
	{name: "speed-google", section: "speed", config: func(cf *config.Config) {
		cf.Video.Provider, cf.Concurrency = "google", 6
	}},
}

func TestToolShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the FFmpeg and concurrency screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	sizes := []shotSize{{1440, 900}, {390, 844}}
	for _, st := range toolShots {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range sizes {
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("settings-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if st.config != nil {
						if err := c.E.Config().Update(st.config); err != nil {
							t.Fatal(err)
						}
					}
					c.Th.SetPaletteNow(dark)
					p := New()
					if st.ff != "" {
						pv := ffPreviews[st.ff]
						p.ff.preview = &pv
						p.ff.manual = strings.HasPrefix(st.ff, "manual")
					}
					draw := shell(c, func(gtx C) D { return p.Layout(gtx, c, st.section) })
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// TestFFmpegCardBits: the manual steps name the official pages and
// commands per system, sizes and versions read well, and the install button
// does nothing in demo mode (it would reach the internet).
func TestFFmpegCardBits(t *testing.T) {
	join := func(goos string) string {
		var b strings.Builder
		for _, s := range manualSteps(goos) {
			b.WriteString(s.text + "|" + strings.Join(s.cmds, ",") + "|")
			for _, l := range s.links {
				b.WriteString(l.url + ",")
			}
			if s.pick {
				b.WriteString("pick")
			}
			b.WriteString("\n")
		}
		return b.String()
	}
	win := join("windows")
	for _, want := range []string{"winget install Gyan.FFmpeg", "scoop install ffmpeg", "https://www.gyan.dev/ffmpeg/builds/",
		"https://github.com/BtbN/FFmpeg-Builds/releases", "https://ffmpeg.org/download.html", "pick"} {
		if !strings.Contains(win, want) {
			t.Errorf("windows steps lack %q", want)
		}
	}
	if mac := join("darwin"); !strings.Contains(mac, "brew install ffmpeg") || !strings.Contains(mac, "https://ffmpeg.org/download.html") {
		t.Errorf("mac steps: %s", mac)
	}
	if lin := join("linux"); !strings.Contains(lin, "sudo apt install ffmpeg") {
		t.Errorf("linux steps: %s", lin)
	}
	for _, steps := range [][]manualStep{manualSteps("windows"), manualSteps("darwin"), manualSteps("linux")} {
		var cmds, links int
		for _, s := range steps {
			cmds += len(s.cmds)
			links += len(s.links)
		}
		var card ffCard
		if cmds > len(card.copies) || links > len(card.links) {
			t.Fatalf("not enough buttons: %d commands, %d links", cmds, links)
		}
	}
	if sizeMB(114768076) != "109 МБ" || sizeMB(28395699+28317701) != "54 МБ" {
		t.Fatalf("sizes %s %s", sizeMB(114768076), sizeMB(28395699+28317701))
	}
	if v := ffVersion("ffmpeg version 9.0.2-essentials_build-www.gyan.dev Copyright (c) 2000-2026 the FFmpeg developers"); v != "9.0.2-essentials_build-www.gyan.dev" {
		t.Fatalf("version %q", v)
	}

	c, _ := demo(t) // offline
	p := New()
	p.startFFInstall(c)
	if p.ff.localErr == "" || c.E.FFmpegInstall().Running {
		t.Fatal("demo mode started an installation")
	}
}

// TestSpeedCardClamp: the slider never stores more than the video service
// allows, and a stored 6 survives a service with a lower maximum.
func TestSpeedCardClamp(t *testing.T) {
	c, h := demo(t)
	p := New()
	p.cfg.set(c, func(cf *config.Config) { cf.Concurrency = 6 }, nil)
	p.cfg.set(c, func(cf *config.Config) { cf.Video.Provider = "google" }, nil)
	h.Flush()
	cfg := c.E.Config().Get()
	if cfg.Concurrency != 6 || studio.ClipConcurrency(cfg) != 4 {
		t.Fatalf("stored %d, effective %d", cfg.Concurrency, studio.ClipConcurrency(cfg))
	}
	p.cfg.set(c, func(cf *config.Config) { cf.Video.Provider, cf.Concurrency = "glabs", 15 }, nil)
	h.Flush()
	if cfg = c.E.Config().Get(); cfg.Concurrency != 15 || studio.ClipConcurrency(cfg) != 10 {
		t.Fatalf("stored %d, effective %d", cfg.Concurrency, studio.ClipConcurrency(cfg))
	}
}
