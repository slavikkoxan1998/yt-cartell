package appui

import (
	"errors"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/update"
)

// fakeRelease is a release newer than any real build, with a build for
// every platform the studio ships.
func fakeRelease() update.Release {
	return update.Release{
		Tag: "v99.1.0", Version: "99.1.0", Title: "YT Cartell Studio v99.1.0 — тест",
		Notes:     "## Що нового\n\n- **Оновлення** в один клік\n- Швидший монтаж\n\nДякуємо, що ти з нами!",
		URL:       "https://github.com/gitkalenyuk/yt-cartell-studio/releases/tag/v99.1.0",
		Published: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC),
		Assets: []update.Asset{
			{Name: "YT-Cartell-Studio-99.1.0-windows-x64.exe", URL: "https://example.invalid/w.exe", Size: 41 << 20},
			{Name: "YT-Cartell-Studio-99.1.0-macos-arm64.zip", URL: "https://example.invalid/a.zip", Size: 39 << 20},
			{Name: "YT-Cartell-Studio-99.1.0-macos-x64.zip", URL: "https://example.invalid/x.zip", Size: 40 << 20},
			{Name: update.SumsName, URL: "https://example.invalid/sums"},
		},
	}
}

// waitPhase lays frames until the updater reaches phase (downloads run on
// their own goroutine).
func waitPhase(t *testing.T, s *Shell, phase updPhase) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.upd.phase != phase {
		if time.Now().After(deadline) {
			t.Fatalf("phase = %d, want %d (err %q)", s.upd.phase, phase, s.upd.err)
		}
		frames(s, 1440, 900, 1)
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHeadlessNeverChecks(t *testing.T) {
	s := Headless(testEngine(t, false))
	defer s.Close()
	if s.upd.src != nil {
		t.Fatal("a headless shell has an updater")
	}
	s.OpenUpdates()
	frames(s, 1440, 900, 3)
	if s.upd.phase != updIdle {
		t.Fatalf("phase = %d", s.upd.phase)
	}
}

func TestUpdateFlow(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	hold := make(chan struct{})
	f := &update.Fake{Release: fakeRelease(), Hold: hold}
	s.SetUpdater(f)

	s.OpenUpdates() // checks at once
	waitPhase(t, s, updAvailable)
	if s.UpdateAvailable() != "99.1.0" || !s.upd.drawer.IsOpen() {
		t.Fatalf("available %q, open %v", s.UpdateAvailable(), s.upd.drawer.IsOpen())
	}
	cfg := e.Config().Get()
	if cfg.UI.UpdateSeen != "99.1.0" || cfg.UI.UpdateChecked == 0 {
		t.Fatalf("not remembered: %+v", cfg.UI)
	}

	// Download, cancel half-way, download again.
	s.startDownload()
	waitPhase(t, s, updDownloading)
	for s.upd.done.Load() == 0 {
		frames(s, 1440, 900, 1)
	}
	s.cancelDownload()
	if s.upd.phase != updAvailable {
		t.Fatalf("after cancel: %d", s.upd.phase)
	}
	close(hold)
	s.startDownload()
	waitPhase(t, s, updReady)

	// Nothing generates: the restart installs at once and asks Run to relaunch.
	s.restartForUpdate()
	deadline := time.Now().Add(5 * time.Second)
	for s.relaunch == nil && time.Now().Before(deadline) {
		frames(s, 1440, 900, 1)
	}
	if s.relaunch == nil {
		t.Fatalf("no relaunch (phase %d, err %q)", s.upd.phase, s.upd.err)
	}
	if err := s.relaunch(); err != nil {
		t.Fatal(err)
	}
	checks, downloads, applies, restarts := f.Counts()
	if checks != 1 || downloads != 2 || applies != 1 || restarts != 1 {
		t.Fatalf("calls: %d checks, %d downloads, %d applies, %d restarts", checks, downloads, applies, restarts)
	}
}

func TestUpdateQuietCheck(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	off := &update.Fake{Err: &update.Error{Kind: update.KindOffline, Msg: "Немає з'єднання з GitHub"}}
	s.SetUpdater(off)
	s.checkUpdate(false)
	frames(s, 1440, 900, 3)
	if s.upd.phase != updIdle || s.th.Toasts() != 0 {
		t.Fatalf("an offline start-up check was not quiet: phase %d, %d toasts", s.upd.phase, s.th.Toasts())
	}
	// Asked by hand, the same failure is explained.
	s.checkUpdate(true)
	frames(s, 1440, 900, 3)
	if s.upd.phase != updFailed || s.upd.err == "" {
		t.Fatalf("manual check: phase %d, err %q", s.upd.phase, s.upd.err)
	}

	s.SetUpdater(&update.Fake{Release: fakeRelease()})
	s.upd.phase = updIdle
	s.checkUpdate(false)
	frames(s, 1440, 900, 4)
	if s.upd.phase != updAvailable || s.th.Toasts() != 1 {
		t.Fatalf("newer at start-up: phase %d, %d toasts", s.upd.phase, s.th.Toasts())
	}
	// No second toast for the same version.
	s.upd.phase = updIdle
	s.checkUpdate(false)
	frames(s, 1440, 900, 4)
	if s.th.Toasts() != 1 {
		t.Fatalf("toasts = %d", s.th.Toasts())
	}
}

func TestUpdateAutoCheckRespectsSettings(t *testing.T) {
	e := testEngine(t, false)
	s := Headless(e)
	defer s.Close()
	f := &update.Fake{Release: fakeRelease()}
	s.SetUpdater(f)
	_ = e.Config().Update(func(c *config.Config) { c.UI.AutoUpdate = false })
	s.autoCheckUpdate()
	_ = e.Config().Update(func(c *config.Config) { c.UI.AutoUpdate = true; c.UI.UpdateChecked = time.Now().Add(-time.Hour).Unix() })
	s.autoCheckUpdate()
	frames(s, 1440, 900, 2)
	if c, _, _, _ := f.Counts(); c != 0 {
		t.Fatalf("checked %d times (off, then checked an hour ago)", c)
	}
	_ = e.Config().Update(func(c *config.Config) { c.UI.UpdateChecked = time.Now().Add(-7 * time.Hour).Unix() })
	s.autoCheckUpdate()
	frames(s, 1440, 900, 3)
	if c, _, _, _ := f.Counts(); c != 1 {
		t.Fatalf("checks = %d after 7 h", c)
	}
}

func TestUpdateInstallFailureKeepsApp(t *testing.T) {
	s := Headless(testEngine(t, false))
	defer s.Close()
	s.SetUpdater(&update.Fake{Release: fakeRelease(), ApplyErr: &update.Error{Kind: update.KindPermission, Msg: "Немає прав"}})
	s.OpenUpdates()
	waitPhase(t, s, updAvailable)
	s.startDownload()
	waitPhase(t, s, updReady)
	s.restartForUpdate()
	waitPhase(t, s, updFailed)
	if s.relaunch != nil || s.upd.stage != stageInstall || s.upd.canInstall() {
		t.Fatalf("relaunch %v, stage %d, canInstall %v", s.relaunch != nil, s.upd.stage, s.upd.canInstall())
	}
}

// The sheet in every phase, wide and phone-sized (no panics, the layout runs).
func TestUpdateSheetPhases(t *testing.T) {
	s := Headless(testEngine(t, false))
	defer s.Close()
	rel := fakeRelease()
	s.ShowUpdate(rel, -1)
	for _, ph := range []updPhase{updIdle, updChecking, updLatest, updAvailable, updDownloading, updReady, updInstalling, updFailed} {
		s.upd.phase = ph
		s.upd.err = "Немає з'єднання з GitHub — перевір інтернет і спробуй ще раз."
		for _, sz := range [][2]int{{1440, 900}, {390, 844}} {
			frames(s, sz[0], sz[1], 2)
		}
	}
	s.upd.install = errors.New("Студію запущено не з пакета.")
	s.upd.phase = updAvailable
	frames(s, 1440, 900, 2)
}
