package settings

// Settings transfer through the page (transfer.go): export from one demo
// engine, import into another, wrong password, hints, backup + restore, and
// the headless screenshots of the sheets (GUISHOTS=<dir>). Engines are
// core.DemoEngine on temp folders with in-memory settings; files go to
// t.TempDir(); every key is a fake.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

const testPassword = "три слова разом 77"

// engineOnly is a demo engine without the web projects (faster).
func engineOnly(t *testing.T) (*core.Ctx, *core.HeadlessHost) {
	t.Helper()
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Shutdown(0) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	return c, h
}

func sourceSettings(dataDir string) func(cf *config.Config) {
	return func(cf *config.Config) {
		cf.Text.APIKey = "src-text-key-0001"
		cf.Text.BaseURL = "https://gw.source.test/v1"
		cf.Google.Keys = []string{"AIzaFAKE-SRC-1", "AIzaFAKE-SRC-2", "AIzaFAKE-SRC-3"}
		cf.TTS.Switch("cartesia")
		cf.TTS.SetKeys([]string{"sk_car_SRCSRCSRCSRC1", "sk_car_SRCSRCSRCSRC2"})
		cf.Custom.VideoPreset, cf.Custom.VideoKey = "royaltechno", "royal-src-key"
		cf.Telegram.BotToken, cf.Telegram.ChatID = "999:SRC-token", "77"
		cf.UI.Theme = "dark"
		cf.Concurrency = 7
		if dataDir != "" {
			cf.DataDir = dataDir
		}
	}
}

func TestTransferThroughThePage(t *testing.T) {
	src, sh := engineOnly(t)
	shared := t.TempDir() // a folder both "computers" have
	if err := src.E.Config().Update(sourceSettings(shared)); err != nil {
		t.Fatal(err)
	}
	pageA := New()
	io := &pageA.data.io
	file := filepath.Join(t.TempDir(), "out", "settings") // no extension: added
	io.pickSave = func(title, name string, then func(string)) {
		if !strings.HasSuffix(name, ".ytcs") || !strings.Contains(name, "YT-Cartell-Studio-settings-") {
			t.Errorf("suggested name %q", name)
		}
		then(file)
	}
	io.exp.open(src)
	if !io.exp.on[secConnections] || !io.exp.on[secProfiles] || !io.exp.on[secDefaults] || io.exp.on[secKeyStates] {
		t.Fatalf("default sections %v", io.exp.on)
	}
	// A short or mismatched password is refused before any dialog.
	io.exp.pw1.SetText("коротко")
	io.exp.pw2.SetText("коротко")
	pageA.startExport(src)
	if io.exp.err == "" || io.exp.saved != "" {
		t.Fatalf("short password accepted: %q", io.exp.err)
	}
	io.exp.pw1.SetText(testPassword)
	io.exp.pw2.SetText(testPassword + "!")
	pageA.startExport(src)
	if !strings.Contains(io.exp.err, "не збігаються") {
		t.Fatalf("mismatch: %q", io.exp.err)
	}
	io.exp.pw2.SetText(testPassword)
	pageA.startExport(src)
	sh.Flush()
	if io.exp.err != "" || io.exp.saved != file+".ytcs" {
		t.Fatalf("export: err %q saved %q", io.exp.err, io.exp.saved)
	}
	if io.exp.pw1.Text() != "" {
		t.Fatal("the password stays in the field after the export")
	}
	raw, err := os.ReadFile(io.exp.saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "src-text-key-0001") || strings.Contains(string(raw), "sk_car_SRC") {
		t.Fatal("a key is readable in the encrypted file")
	}

	// The other computer.
	dst, dh := engineOnly(t)
	before := dst.E.Config().Get()
	pageB := New()
	imp := &pageB.data.io.imp
	pageB.loadImportFile(dst, io.exp.saved)
	dh.Flush()
	if imp.file == nil || imp.b != nil || !imp.file.Encrypted() {
		t.Fatalf("password stage: file %v bundle %v", imp.file != nil, imp.b != nil)
	}
	imp.pw.SetText("не той пароль")
	imp.unlock(dst)
	dh.Flush()
	if imp.b != nil || !strings.Contains(imp.pwErr, "Пароль не підходить") {
		t.Fatalf("wrong password: %q", imp.pwErr)
	}
	if got := dst.E.Config().Get(); got.Text.APIKey != before.Text.APIKey || len(dst.E.SettingsBackups()) != 0 {
		t.Fatal("a wrong password changed something")
	}
	imp.pw.SetText(testPassword)
	imp.unlock(dst)
	dh.Flush()
	if imp.b == nil || imp.pwErr != "" {
		t.Fatalf("right password: %q", imp.pwErr)
	}
	// The data folder exists here too: offered, off until asked.
	if !imp.offers(hintDataDir) || !imp.available(hintDataDir) || imp.plan.Hints.DataDir {
		t.Fatalf("data folder hint: offers %v available %v on %v", imp.offers(hintDataDir), imp.available(hintDataDir), imp.plan.Hints.DataDir)
	}
	if got := connectionsSummary(imp.b.Connections); !strings.Contains(got, "Cartesia 2") || !strings.Contains(got, "Google: 3 ключі") || !strings.Contains(got, "RoyalTechno") {
		t.Fatalf("summary: %s", got)
	}
	pageB.applyImport(dst)
	dh.Flush()
	if !imp.done || imp.err != "" {
		t.Fatalf("import: done %v err %q", imp.done, imp.err)
	}
	got := dst.E.Config().Get()
	if got.Text.APIKey != "src-text-key-0001" || len(got.Google.Keys) != 3 || got.TTS.Provider != "cartesia" ||
		len(got.TTS.Of("cartesia").AllKeys()) != 2 || got.UI.Theme != "dark" || got.Concurrency != 7 || got.Custom.VideoKey != "royal-src-key" {
		t.Fatalf("imported: %+v", got.Text)
	}
	if got.DataDir != before.DataDir {
		t.Fatalf("the data folder moved without asking: %s", got.DataDir)
	}
	if len(imp.lines) == 0 || pageB.data.io.result == "" {
		t.Fatal("no report")
	}
	bks := dst.E.SettingsBackups()
	if len(bks) != 1 || bks[0].Config.Text.APIKey != before.Text.APIKey {
		t.Fatalf("backup: %d", len(bks))
	}

	// Again, with the data folder asked for: the engine switches to it.
	pageB.loadImportFile(dst, io.exp.saved)
	dh.Flush()
	imp.pw.SetText(testPassword)
	imp.unlock(dst)
	dh.Flush()
	imp.plan.Hints.DataDir = true
	imp.plan.Profiles.Mode = config.ModeMerge
	pageB.applyImport(dst)
	dh.Flush()
	if got := dst.E.Config().Get().DataDir; !samePath(got, shared) {
		t.Fatalf("data folder %s, want %s", got, shared)
	}
	if imp.res.ProfilesAdded != 0 || imp.res.ProfilesSame != len(config.StarterProfiles()) {
		t.Fatalf("merging the same profiles again: added %d same %d", imp.res.ProfilesAdded, imp.res.ProfilesSame)
	}

	// «Відновити попередні»: the first import's backup puts everything back.
	oldest := dst.E.SettingsBackups()
	pageB.restoreSettings(dst, oldest[len(oldest)-1])
	dh.Flush()
	back := dst.E.Config().Get()
	if back.Text.APIKey != before.Text.APIKey || back.UI.Theme != before.UI.Theme || !samePath(back.DataDir, before.DataDir) || len(back.Google.Keys) != 0 {
		t.Fatalf("restored: key %q theme %q dir %s", back.Text.APIKey, back.UI.Theme, back.DataDir)
	}
}

func TestTransferPlainAndDamaged(t *testing.T) {
	src, sh := engineOnly(t)
	if err := src.E.Config().Update(sourceSettings("")); err != nil {
		t.Fatal(err)
	}
	p := New()
	io := &p.data.io
	dir := t.TempDir()
	io.pickSave = func(_, _ string, then func(string)) { then(filepath.Join(dir, "plain.ytcs")) }
	io.exp.open(src)
	io.exp.plain = true
	p.startExport(src)
	sh.Flush()
	if io.exp.saved == "" {
		t.Fatalf("plain export: %q", io.exp.err)
	}
	dst, dh := engineOnly(t)
	q := New()
	q.loadImportFile(dst, io.exp.saved)
	dh.Flush()
	if q.data.io.imp.b == nil {
		t.Fatal("a plain file should open without a password")
	}
	// A damaged file is refused with a toast; nothing opens.
	raw, _ := os.ReadFile(io.exp.saved)
	bad := filepath.Join(dir, "bad.ytcs")
	_ = os.WriteFile(bad, []byte(strings.Replace(string(raw), "src-text-key-0001", "src-text-key-0002", 1)), 0o600)
	r := New()
	r.loadImportFile(dst, bad)
	dh.Flush()
	if r.data.io.imp.file != nil || r.data.io.imp.drawer.IsOpen() {
		t.Fatal("a damaged file opened")
	}
	if _, err := config.ReadExportFile(bad); !errors.Is(err, config.ErrDamaged) {
		t.Fatalf("damaged: %v", err)
	}
}

func TestPasswordStrength(t *testing.T) {
	for pw, want := range map[string]int{
		"":                   0,
		"1234567":            0,
		"12345678":           1,
		"aaaaaaaaaaaa":       1,
		"котик2026":          1,
		"Котик-2026!":        2,
		"довгий пароль":      2,
		"три слова разом 77": 3,
		"Correct-Horse-9":    3,
		"зелений кіт спить вдень": 3,
	} {
		if got := strength(pw); got != want {
			t.Errorf("strength(%q) = %d, want %d", pw, got, want)
		}
	}
}

// ---- screenshots ----------------------------------------------------------------------------------

// transferFile writes a settings file of rich fake settings for the import shots.
func transferFile(t *testing.T, password string) string {
	t.Helper()
	cf := config.Default()
	cf.Text.APIKey = "demo-text-key"
	cf.Glabs.APIKey = "demo-glabs-key"
	cf.Google.Keys = []string{"AIzaDEMO-1", "AIzaDEMO-2", "AIzaDEMO-3"}
	cf.TTS.Switch("elevenlabs")
	cf.TTS.SetKeys([]string{"sk_" + strings.Repeat("ab", 20)})
	cf.TTS.Switch("cartesia")
	var keys []string
	for i := 0; i < 52; i++ {
		keys = append(keys, fmt.Sprintf("sk_car_DEMODEMODEMO%02d", i))
	}
	cf.TTS.SetKeys(keys)
	cf.Custom.VideoPreset, cf.Custom.VideoKey = "royaltechno", "demo-royal-key"
	cf.Telegram.BotToken = "123:DEMO"
	cf.UI.Theme = "dark"
	cf.Concurrency = 4
	cf.DataDir = `D:\Відео\YT Cartell Studio`
	cf.FFmpegPath = `C:\ffmpeg\bin\ffmpeg.exe`
	cf.Dictation.Device = "Мікрофон (USB Audio Device)"
	cf.Normalize()
	profiles := config.StarterProfiles()
	profiles = append(profiles, config.Profile{ID: "p-a", Name: "Історії про космос"}, config.Profile{ID: "p-b", Name: "Новини тижня"})
	states := `{"version":1,"keys":{"a1":{"provider":"cartesia","state":"exhausted"},"a2":{"provider":"cartesia","state":"ok"}}}`
	data, err := config.Export(cf, profiles, []byte(states), config.ExportOptions{Connections: true, Profiles: true, Defaults: true, KeyStates: true,
		Password: password, AppVersion: "2.0.0", Now: time.Date(2026, 10, 2, 15, 30, 0, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), config.ExportFileName(time.Date(2026, 10, 2, 0, 0, 0, 0, time.Local)))
	if err := config.WriteExportFile(path, data); err != nil {
		t.Fatal(err)
	}
	return path
}

type transferShot struct {
	name  string
	setup func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page)
}

var transferShots = []transferShot{
	{"export", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		_ = c.E.Config().Update(func(cf *config.Config) {
			cf.Google.Keys = []string{"AIzaDEMO-1", "AIzaDEMO-2", "AIzaDEMO-3"}
			cf.TTS.Switch("cartesia")
			cf.TTS.SetKeys([]string{"sk_car_DEMODEMODEMO01", "sk_car_DEMODEMODEMO02"})
			cf.Custom.VideoPreset, cf.Custom.VideoKey = "royaltechno", "demo-royal-key"
		})
		p.data.io.exp.open(c)
		p.data.io.exp.pw1.SetText("котик2026")
		p.data.io.exp.pw2.SetText("котик2025")
	}},
	{"export-plain", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.data.io.exp.open(c)
		p.data.io.exp.plain = true
	}},
	{"export-done", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.data.io.exp.open(c)
		p.data.io.exp.saved = `C:\Users\Owner\Documents\YT-Cartell-Studio-settings-2026-10-02.ytcs`
	}},
	{"import-password", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.loadImportFile(c, transferFile(t, testPassword))
		h.Flush()
		s := &p.data.io.imp
		s.pw.SetText("не той")
		s.pwErr = config.ErrWrongPassword.Error()
	}},
	{"import", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.loadImportFile(c, transferFile(t, testPassword))
		h.Flush()
		s := &p.data.io.imp
		s.pw.SetText(testPassword)
		s.unlock(c)
		h.Flush()
		// What a newer studio's file and this computer would add.
		s.b.Warnings = append(s.b.Warnings, "Файл зроблено новішою версією студії (2.3.0): 3 параметри пропустимо — решту перенесемо. Онови студію, щоб перенести все.")
		s.here.DataDir, s.mic = true, micMissing
		s.plan.Profiles.Mode = config.ModeMerge
	}},
	{"import-hints", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.loadImportFile(c, transferFile(t, ""))
		h.Flush()
		s := &p.data.io.imp
		// The sections collapsed so the paths are in view; the folder exists here.
		s.plan.Connections.On, s.plan.Profiles.On, s.plan.Defaults.On, s.plan.KeyStates.On = false, false, false, false
		s.here.DataDir, s.plan.Hints.DataDir, s.mic = true, true, micMissing
	}},
	{"import-done", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.loadImportFile(c, transferFile(t, ""))
		h.Flush()
		p.applyImport(c)
		h.Flush()
	}},
	{"data", func(t *testing.T, c *core.Ctx, h *core.HeadlessHost, p *Page) {
		p.data.io.result = "Налаштування з файлу YT-Cartell-Studio-settings-2026-10-02.ytcs перенесено. Перевір підключення кнопками «Перевірити»."
		if _, err := c.E.Config().SaveBackup(c.E.Profiles(), time.Date(2026, 10, 2, 15, 31, 0, 0, time.Local)); err != nil {
			t.Fatal(err)
		}
		p.data.io.backupsOK = false
	}},
}

func TestTransferShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the settings transfer screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, st := range transferShots {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range []shotSize{{1440, 900}, {390, 844}} {
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("settings-transfer-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					c.Th.SetPaletteNow(dark)
					p := New()
					frame := 0
					draw := shell(c, func(gtx C) D {
						frame++
						if frame == 2 {
							st.setup(t, c, h, p)
						}
						return p.Layout(gtx, c, "data")
					})
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(8), gui.Settle(400)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
