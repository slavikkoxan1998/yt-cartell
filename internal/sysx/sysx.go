// Package sysx wraps the few OS services the desktop app needs: running child
// processes without a console window flashing (ffmpeg, ffprobe, dialogs),
// opening files/folders with the default app, revealing a file in the file
// manager, and native open/save/folder dialogs.
//
// Dialogs shell out to what every OS already ships: PowerShell + WinForms on
// Windows, osascript on macOS, zenity (then kdialog) on Linux. Every dialog
// returns "" and a nil error when the user cancels.
package sysx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Command is exec.CommandContext with the console window hidden on Windows
// (CREATE_NO_WINDOW), so ffmpeg/ffprobe never flash a terminal. A nil ctx is
// treated as context.Background().
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	c := exec.CommandContext(ctx, name, args...)
	hideConsole(c)
	return c
}

// Start is c.Start(), except that on Windows the child dies with the app:
// it joins a kill-on-close Job Object, so ffmpeg does not outlive a closed
// window, os.Exit or a crash. Use Start/Run/Output for every long-lived or
// heavy child (ffmpeg, ffprobe).
func Start(c *exec.Cmd) error { return start(c) }

// Run is c.Run() via Start.
func Run(c *exec.Cmd) error {
	if err := Start(c); err != nil {
		return err
	}
	return c.Wait()
}

// Output is c.Output() via Start: stdout is returned (also on failure) and,
// when c.Stderr is unset, the head of stderr lands in *exec.ExitError.Stderr.
func Output(c *exec.Cmd) ([]byte, error) {
	if c.Stdout != nil {
		return nil, errors.New("exec: Stdout already set")
	}
	var stdout bytes.Buffer
	c.Stdout = &stdout
	var stderr *headBuffer
	if c.Stderr == nil {
		stderr = &headBuffer{max: 32 << 10}
		c.Stderr = stderr
	}
	err := Run(c)
	var ee *exec.ExitError
	if stderr != nil && errors.As(err, &ee) {
		ee.Stderr = stderr.b
	}
	return stdout.Bytes(), err
}

// headBuffer keeps the first max bytes written to it.
type headBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (h *headBuffer) Write(p []byte) (int, error) {
	h.mu.Lock()
	if room := h.max - len(h.b); room > 0 {
		h.b = append(h.b, p[:min(room, len(p))]...)
	}
	h.mu.Unlock()
	return len(p), nil
}

// OpenPath opens a file or folder (or an http/https URL) with the default app.
func OpenPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("не вказано шлях")
	}
	if isURL(path) {
		return openDefault(path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("не знайдено: %s", abs)
	}
	return openDefault(abs)
}

// RevealPath shows the file selected in Explorer/Finder/the file manager.
// A missing file falls back to opening its folder.
func RevealPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("не вказано шлях")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		dir := filepath.Dir(abs)
		if st, derr := os.Stat(dir); derr == nil && st.IsDir() {
			return openDefault(dir)
		}
		return fmt.Errorf("не знайдено: %s", abs)
	}
	return reveal(abs)
}

// PickFolder asks the user for a folder. start (optional) is preselected.
// Returns "" and nil when cancelled.
func PickFolder(title, start string) (string, error) {
	if start != "" {
		if abs, err := filepath.Abs(start); err == nil {
			start = abs
		}
		if st, err := os.Stat(start); err != nil || !st.IsDir() {
			start = ""
		}
	}
	return pickFolder(title, start)
}

// PickFile asks the user for one existing file. exts limits the choice, e.g.
// []string{"mp3", "wav"} (dots/asterisks are tolerated); empty = any file.
// Returns "" and nil when cancelled.
func PickFile(title string, exts []string) (string, error) {
	return pickFile(title, cleanExts(exts))
}

// PickSaveFile asks where to save a file. defaultName is either a bare file
// name ("final.mp4") or a full path whose folder is opened first. The file's
// extension becomes the dialog filter. Returns "" and nil when cancelled.
func PickSaveFile(title, defaultName string) (string, error) {
	dir, name := "", strings.TrimSpace(defaultName)
	if name != "" && filepath.IsAbs(name) {
		dir, name = filepath.Dir(name), filepath.Base(name)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			dir = ""
		}
	}
	return pickSave(title, dir, name, extOf(name))
}

// ExeDir is the folder of the running executable (symlinks resolved), or the
// working directory when that cannot be determined.
func ExeDir() string {
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		return filepath.Dir(exe)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// ---- shared helpers -----------------------------------------------------------

func isURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// cleanExts turns ".MP3", "*.wav", "mp3" into "mp3", "wav"; drops junk and dupes.
func cleanExts(exts []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range exts {
		e = strings.ToLower(strings.TrimLeft(strings.TrimSpace(e), "*."))
		ok := e != ""
		for _, r := range e {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				ok = false
				break
			}
		}
		if ok && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

func extOf(name string) string {
	e := cleanExts([]string{filepath.Ext(name)})
	if len(e) == 0 {
		return ""
	}
	return e[0]
}

// dialogOutput turns a dialog helper's stdout into a path ("" = cancelled).
func dialogOutput(b []byte) string {
	s := strings.TrimPrefix(string(b), string(rune(0xFEFF)))
	return strings.TrimSpace(strings.TrimRight(s, "\r\n\x00"))
}

// tail keeps the last n bytes of a (stderr) message for error reports.
func tail(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		i := len(s) - n
		for i < len(s) && s[i]&0xC0 == 0x80 { // stay on a rune boundary
			i++
		}
		s = "…" + s[i:]
	}
	return s
}

// startDetached launches a GUI helper (explorer, open, xdg-open) and reaps it
// in the background — those helpers may linger or return odd exit codes.
func startDetached(c *exec.Cmd) error {
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}
