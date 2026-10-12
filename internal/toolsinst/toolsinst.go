// Package toolsinst installs the helper programs the studio runs (FFmpeg
// today; the downloader is generic so yt-dlp and others can follow) into the
// studio's own folder — only when the user presses a button, only from
// official, versioned sources, never with admin rights:
//
//	<config.ToolsDir()>/<tool>/…          the installed program (swapped in as a whole)
//	<config.ToolsDir()>/.download/<id>/   downloads in progress ("<file>.part", resumed)
//	<config.ToolsDir()>/.<tool>-unpack-*  staging while unpacking (same volume → atomic rename)
//
// The building blocks are exported for the next tool: Asset + Download
// (pinned size and SHA-256, mirrors, resume with HTTP Range, a stall timeout,
// retries, cancel), FetchChecksums (SHA256SUMS-style files), ExtractZip (no
// path escapes, no links, size limits), ReplaceDir (atomic swap with
// rollback), Lock (one installation per tool), RunVersion and Unquarantine.
// Messages returned to the caller are Ukrainian and say what to do.
//
// FFmpeg (ffmpeg.go): EnsureFFmpeg / InstallFFmpeg. Package media finds the
// result through config.ToolsDir() — this package imports only config and sysx,
// so any package may use it.
package toolsinst

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/sysx"
)

// Progress receives the overall percent (0–100) and a short Ukrainian line
// ("Завантажуємо FFmpeg · 12,4 МБ з 109 МБ"). It may be called from any
// goroutine; nil is allowed everywhere.
type Progress func(pct int, msg string)

// Root is the folder the studio installs programs into (config.ToolsDir).
func Root() string { return config.ToolsDir() }

// ToolDir is the folder of one program: <Root>/<name>.
func ToolDir(name string) string { return filepath.Join(Root(), name) }

// ---- one installation per tool ------------------------------------------------------------

var (
	locksMu sync.Mutex
	locks   = map[string]bool{}
)

// ErrBusy: an installation of this tool is already running.
var ErrBusy = errors.New("Встановлення вже триває — дочекайся, поки воно завершиться")

// Lock marks tool as being installed; the second caller gets ErrBusy until
// unlock runs.
func Lock(tool string) (unlock func(), err error) {
	locksMu.Lock()
	defer locksMu.Unlock()
	if locks[tool] {
		return nil, ErrBusy
	}
	locks[tool] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			locksMu.Lock()
			delete(locks, tool)
			locksMu.Unlock()
		})
	}, nil
}

// ---- running the installed program --------------------------------------------------------------

// RunVersion runs bin with args ("-version" when none) and returns the first
// line of its output — the check that an installed program really starts.
func RunVersion(ctx context.Context, bin string, args ...string) (string, error) {
	if len(args) == 0 {
		args = []string{"-version"}
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := sysx.Command(ctx, bin, args...)
	cmd.WaitDelay = 3 * time.Second
	out, err := sysx.Output(cmd)
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s не відповідає", filepath.Base(bin))
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("%s завершився з кодом %d", filepath.Base(bin), ee.ExitCode())
		}
		return "", fmt.Errorf("%s не запускається: %v", filepath.Base(bin), err)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("%s не повідомив версію", filepath.Base(bin))
	}
	return line, nil
}

var versionTimeout = 20 * time.Second

// Unquarantine clears macOS's quarantine flag from path (recursively) so
// Gatekeeper does not stop a downloaded program; elsewhere it does nothing.
// Errors are ignored: a file without the flag is the usual case.
func Unquarantine(path string) {
	if runtime.GOOS != "darwin" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = sysx.Run(sysx.Command(ctx, "/usr/bin/xattr", "-dr", "com.apple.quarantine", path))
}

// exe adds ".exe" on Windows.
func exe(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
