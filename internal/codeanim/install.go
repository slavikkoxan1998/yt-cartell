package codeanim

// chrome-headless-shell (Chrome for Testing) — installed only when the user
// asks for it (InstallBrowser) and only when no system browser exists.
//
// Pinned: Stable 155.0.8059.39 from
// https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json
// (checked 2026-10-08). The endpoint lists URLs only; sizes and the MD5 are
// Google Cloud Storage's object metadata (x-goog-stored-content-length,
// x-goog-hash), the SHA-256 was computed from the same bytes, whose MD5
// matched Google's.
//
// Layout: <config.ToolsDir()>/chrome-headless-shell/chrome-headless-shell-<platform>/chrome-headless-shell(.exe)
// plus install.json (version, URL, SHA-256).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"ytcartell/internal/toolsinst"
)

const shellVersion = "155.0.8059.39"

type shellBuild struct {
	Platform string
	Asset    toolsinst.Asset
}

func cft(platform string, size int64, sha string) shellBuild {
	return shellBuild{Platform: platform, Asset: toolsinst.Asset{
		URL:    "https://storage.googleapis.com/chrome-for-testing-public/" + shellVersion + "/" + platform + "/chrome-headless-shell-" + platform + ".zip",
		Size:   size,
		SHA256: sha,
		Name:   "chrome-headless-shell-" + shellVersion + "-" + platform + ".zip",
	}}
}

var shellBuilds = map[string]shellBuild{
	"windows/amd64": cft("win64", 124737213, "20798f7c51a22def0b3d02e526a8f52e7ee2c7eddcde9d81a4c23f618e8b5436"),     // md5 584e2ae0ecc33a117ec30d14e5fe8160
	"windows/arm64": cft("win64", 124737213, "20798f7c51a22def0b3d02e526a8f52e7ee2c7eddcde9d81a4c23f618e8b5436"),     // x64 emulation
	"darwin/arm64":  cft("mac-arm64", 102429510, "b3e093c06001c41e68decbc8dd4a62f9efe4bf4e4dd247a686ea531863448d75"), // md5 211604d63af27ecd1a4854580b709527
	"darwin/amd64":  cft("mac-x64", 107758350, "6338a784c691f42dd1ed6aeeef650171c7d72cf74bdb8450d0079864e72a8074"),   // md5 72c38bacd8c89216dce74a8b28d19ddb
}

const shellTool = "chrome-headless-shell"

// ShellDir is where InstallBrowser puts the headless shell.
func ShellDir() string { return toolsinst.ToolDir(shellTool) }

func shellExe(dir, platform string) string {
	name := "chrome-headless-shell"
	if strings.HasPrefix(platform, "win") {
		name += ".exe"
	}
	return filepath.Join(dir, "chrome-headless-shell-"+platform, name)
}

type shellManifest struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Exe     string `json:"exe"` // relative to the folder
}

// installedShell returns the installed headless shell, if any.
func installedShell() (string, bool) {
	dir := ShellDir()
	raw, err := os.ReadFile(filepath.Join(dir, "install.json"))
	if err != nil {
		return "", false
	}
	var m shellManifest
	if json.Unmarshal(raw, &m) != nil || m.Exe == "" || !filepath.IsLocal(m.Exe) {
		return "", false
	}
	p := filepath.Join(dir, m.Exe)
	return p, isFile(p)
}

// BrowserOffer says whether InstallBrowser can install something here and
// how big the download is (0 = nothing to install for this system).
func BrowserOffer() (version string, bytes int64, ok bool) {
	b, ok := shellBuilds[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", 0, false
	}
	return shellVersion, b.Asset.Size, true
}

// InstallBrowser makes sure a browser for animations exists: a found one is
// returned as is; otherwise the pinned chrome-headless-shell is downloaded
// (size + SHA-256 checked, resumable, cancellable), unpacked into ShellDir
// and checked to start. progress (may be nil) gets percent 0–100 and a
// Ukrainian line.
func InstallBrowser(ctx context.Context, progress func(pct int, msg string)) (string, error) {
	report := func(pct int, msg string) {
		if progress != nil {
			progress(pct, msg)
		}
	}
	if p := findBrowser(); p != "" {
		report(100, "Браузер для анімацій уже є")
		return p, nil
	}
	b, ok := shellBuilds[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", errors.New("Для цієї системи рушій анімацій автоматично не встановлюється — встанови Google Chrome або Chromium")
	}
	return installShell(ctx, b, report)
}

// shellCheck runs the installed shell once (a package var: tests use a fake).
var shellCheck = func(ctx context.Context, exe string) error {
	_, err := toolsinst.RunVersion(ctx, exe, "--version")
	return err
}

func installShell(ctx context.Context, b shellBuild, report func(int, string)) (string, error) {
	if report == nil {
		report = func(int, string) {}
	}
	unlock, err := toolsinst.Lock(shellTool)
	if err != nil {
		return "", err
	}
	defer unlock()

	root := toolsinst.Root()
	dl := filepath.Join(root, ".download", shellTool, b.Asset.FileName())
	total := b.Asset.Size
	report(0, "Завантажуємо рушій анімацій")
	err = toolsinst.Download(ctx, b.Asset, dl, func(done, all int64) {
		if all <= 0 {
			all = total
		}
		pct := 0
		if all > 0 {
			pct = int(done * 90 / all)
		}
		report(pct, fmt.Sprintf("Завантажуємо рушій анімацій · %s з %s", toolsinst.FormatSize(done), toolsinst.FormatSize(all)))
	})
	if err != nil {
		return "", err
	}
	report(90, "Розпаковуємо рушій анімацій")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(root, "."+shellTool+"-unpack-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if err := toolsinst.ExtractZip(dl, stage, nil); err != nil {
		return "", err
	}
	exe := shellExe(stage, b.Platform)
	if !isFile(exe) {
		return "", errors.New("В архіві рушія анімацій немає програми — спробуй ще раз")
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(exe, 0o755)
	}
	toolsinst.Unquarantine(stage)
	rel, _ := filepath.Rel(stage, exe)
	m, _ := json.MarshalIndent(shellManifest{Version: shellVersion, URL: b.Asset.URL, SHA256: b.Asset.SHA256, Exe: filepath.ToSlash(rel)}, "", "  ")
	if err := os.WriteFile(filepath.Join(stage, "install.json"), m, 0o644); err != nil {
		return "", err
	}
	dir := ShellDir()
	if err := toolsinst.ReplaceDir(stage, dir); err != nil {
		if errors.Is(err, toolsinst.ErrInUse) {
			return "", errors.New("Рушій анімацій зараз працює — дочекайся кінця рендеру й спробуй ще раз")
		}
		return "", err
	}
	_ = os.Remove(dl)
	p := filepath.Join(dir, rel)
	if err := shellCheck(ctx, p); err != nil {
		return "", fmt.Errorf("Рушій анімацій встановлено, але він не запускається: %v", err)
	}
	report(100, "Рушій анімацій встановлено")
	return p, nil
}
