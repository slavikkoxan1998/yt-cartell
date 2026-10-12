// Package media wraps the external FFmpeg / ffprobe executables: locating them,
// probing files, grabbing thumbnails and assembling the final film (a port of
// apps/api/src/studio/montage.ts).
//
// FFmpeg is never embedded: it is found via the configured path, next to the
// executable, in the studio's own installation (package toolsinst) or on
// PATH. Every child process runs without a console window.
package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ytcartell/internal/config"
	"ytcartell/internal/sysx"
)

// FF holds absolute paths of the two executables ("" = not found).
type FF struct{ FFmpeg, FFprobe string }

// ErrNotFound: no ffmpeg executable could be located.
var ErrNotFound = errors.New("FFmpeg не знайдено — встанови його в налаштуваннях («Встановити автоматично») або вкажи шлях до ffmpeg")

// ErrNoProbe: ffmpeg was found but ffprobe was not.
var ErrNoProbe = errors.New("ffprobe не знайдено — він постачається разом із FFmpeg; поклади ffprobe поруч з ffmpeg або вкажи шлях у налаштуваннях")

// Locate finds ffmpeg and ffprobe: configured path (file or folder) → next to
// the executable → the studio's own copy (InstalledDir) → PATH (scoop shims
// resolved to the real binary) → common install folders. ffprobe is looked up next to the found ffmpeg first. The
// returned FF is filled as far as possible even when an error is returned.
func Locate(cfg config.Config) (FF, error) {
	var f FF
	var errs []error
	ffmpeg, err := find("ffmpeg", cfg.FFmpegPath, "")
	if err != nil {
		errs = append(errs, err)
	}
	f.FFmpeg = ffmpeg
	sibling := ""
	if ffmpeg != "" {
		sibling = filepath.Dir(ffmpeg)
	}
	ffprobe, err := find("ffprobe", cfg.FFprobePath, sibling)
	if err != nil && ffmpeg != "" {
		errs = append(errs, err)
	}
	f.FFprobe = ffprobe
	return f, errors.Join(errs...)
}

// OK reports whether both executables were found.
func (f FF) OK() bool { return f.FFmpeg != "" && f.FFprobe != "" }

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isFile(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func find(name, configured, sibling string) (string, error) {
	bin := exeName(name)
	// A configured path that no longer exists (e.g. after "brew upgrade" moved
	// the binary) falls back to auto-detection; its error is reported only
	// when nothing else is found either.
	var configuredErr error
	if c := strings.Trim(strings.TrimSpace(configured), `"`); c != "" {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			for _, p := range []string{filepath.Join(c, bin), filepath.Join(c, "bin", bin)} {
				if isFile(p) {
					return abs(p), nil
				}
			}
		} else if err == nil {
			return abs(resolveShim(c)), nil
		}
		configuredErr = fmt.Errorf("%s не знайдено за шляхом %s", name, c)
	}
	p, err := findAuto(name, bin, sibling)
	if err != nil && configuredErr != nil {
		return "", configuredErr
	}
	return p, err
}

// findAuto looks next to the other tool, next to the exe, in the studio's
// own installation (Settings → «Встановити автоматично»), in PATH and in the
// usual install folders.
func findAuto(name, bin, sibling string) (string, error) {
	if sibling != "" {
		if p := filepath.Join(sibling, bin); isFile(p) {
			return p, nil
		}
	}
	if dir := exeDir(); dir != "" {
		for _, p := range []string{
			filepath.Join(dir, bin),
			filepath.Join(dir, "ffmpeg", bin),
			filepath.Join(dir, "ffmpeg", "bin", bin),
			filepath.Join(dir, "bin", bin),
		} {
			if isFile(p) {
				return p, nil
			}
		}
	}
	if dir := InstalledDir(); dir != "" {
		if p := filepath.Join(dir, bin); isFile(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return abs(resolveShim(p)), nil
	}
	for _, dir := range extraDirs() {
		if p := filepath.Join(dir, bin); isFile(p) {
			return resolveShim(p), nil
		}
	}
	if name == "ffmpeg" {
		return "", ErrNotFound
	}
	return "", ErrNoProbe
}

// InstalledDir is the folder of the FFmpeg the studio installs itself
// (package toolsinst): <config.ToolsDir()>/ffmpeg/bin. A package var so
// tests can point it at a temp folder.
var InstalledDir = func() string { return filepath.Join(config.ToolsDir(), "ffmpeg", "bin") }

// IsInstalled reports whether path is inside the studio's own FFmpeg folder.
func IsInstalled(path string) bool {
	dir := InstalledDir()
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && filepath.IsLocal(rel)
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// extraDirs are common install locations that may be missing from PATH
// (apps started from Finder/Explorer do not inherit a login shell's PATH).
func extraDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		var ds []string
		if home != "" {
			ds = append(ds, filepath.Join(home, "scoop", "shims"))
		}
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			ds = append(ds, filepath.Join(v, "Microsoft", "WinGet", "Links"))
		}
		if v := os.Getenv("ProgramFiles"); v != "" {
			ds = append(ds, filepath.Join(v, "ffmpeg", "bin"))
		}
		if v := os.Getenv("ProgramData"); v != "" {
			ds = append(ds, filepath.Join(v, "chocolatey", "bin"))
		}
		return append(ds, `C:\ffmpeg\bin`)
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	default:
		ds := []string{"/usr/bin", "/usr/local/bin", "/snap/bin"}
		if home != "" {
			ds = append(ds, filepath.Join(home, ".local", "bin"))
		}
		return ds
	}
}

// resolveShim maps a scoop shim (foo.exe + foo.shim with `path = "..."`) to the
// real executable, so killing the process really stops ffmpeg.
func resolveShim(p string) string {
	shim := strings.TrimSuffix(p, filepath.Ext(p)) + ".shim"
	fh, err := os.Open(shim)
	if err != nil {
		return p
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok || strings.TrimSpace(k) != "path" {
			continue
		}
		target := strings.Trim(strings.TrimSpace(v), `"`)
		if isFile(target) {
			return target
		}
		break
	}
	return p
}

// command builds a child process bound to ctx, without a console window.
func command(ctx context.Context, bin string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	hideConsole(cmd)
	// Don't hang forever on pipes kept open by a grandchild after a kill.
	cmd.WaitDelay = 3 * time.Second
	return cmd
}

// Version returns the first line of `ffmpeg -version`, e.g. "ffmpeg version 8.0 …".
func (f FF) Version(ctx context.Context) (string, error) {
	if f.FFmpeg == "" {
		return "", ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := sysx.Output(command(ctx, f.FFmpeg, "-version"))
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("ffmpeg не відповідає: %w", ctx.Err())
		}
		return "", fmt.Errorf("ffmpeg не запускається: %w", err)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New("ffmpeg не повернув версію")
	}
	return line, nil
}

// Run executes ffmpeg with "-hide_banner -loglevel error -nostdin -y" prepended
// (like the TS run()), in dir when non-empty. On failure the error carries the
// tail of stderr; a cancelled ctx returns ctx.Err().
func (f FF) Run(ctx context.Context, dir string, args ...string) error {
	return f.run(ctx, dir, nil, args...)
}

// run is Run with an optional stdout sink (used for -progress pipe:1).
func (f FF) run(ctx context.Context, dir string, stdout io.Writer, args ...string) error {
	if f.FFmpeg == "" {
		return ErrNotFound
	}
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
	cmd := command(ctx, f.FFmpeg, full...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	stderr := &tailBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	err := sysx.Run(cmd)
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("ffmpeg %d: %s", ee.ExitCode(), stderr.Tail(400))
	}
	return fmt.Errorf("FFmpeg не знайдено: %w", err)
}

// tailBuffer is an io.Writer that keeps only the last max bytes.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

// Tail returns the last n runes, trimmed.
func (t *tailBuffer) Tail(n int) string {
	t.mu.Lock()
	b := append([]byte(nil), t.b...)
	t.mu.Unlock()
	// Drop a rune cut in half by the byte window.
	for len(b) > 0 && !utf8.RuneStart(b[0]) {
		b = b[1:]
	}
	s := strings.TrimSpace(strings.ToValidUTF8(string(b), ""))
	if r := []rune(s); len(r) > n {
		s = string(r[len(r)-n:])
	}
	return s
}
