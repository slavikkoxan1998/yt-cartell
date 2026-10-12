package update

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ytcartell/internal/sysx"
)

// OldSuffix marks the build an update replaced (removed at the next start).
const OldSuffix = ".old"

// launch starts the installed build (tests replace it).
var launch = sysx.Launch

// Apply installs a verified download (Download's path) in place of the
// running app and returns what starts it. Nothing is started yet: the
// caller closes the window, flushes its data, calls the Restart and exits.
// KindManual means this install cannot be updated in place (open the
// release page instead); on any other error the running build is untouched.
func Apply(staged string) (Restart, error) {
	if _, err := os.Stat(staged); err != nil {
		return nil, &Error{Msg: "Файл оновлення зник — завантаж його ще раз.", Err: err}
	}
	return apply(staged)
}

// Installable reports whether Apply can install in place on this computer
// (nil) or why not — a KindManual or KindPermission error: the UI then
// offers the release page instead of a download that could not be used.
func Installable() error { return installable() }

// writable checks that dir accepts new files (a probe file, removed at once).
func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".yt-cartell-write-check-*")
	if err != nil {
		return writeError(dir, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// errManual is the KindManual error with a reason.
func errManual(why string) error {
	return &Error{Kind: KindManual, Msg: why + " Завантаж нову версію зі сторінки релізу."}
}

// runningExe is the running executable with symlinks resolved.
func runningExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// swapFile puts newPath where target is; target becomes target+".old"
// (renaming a running exe is allowed on Windows and Unix). On failure the
// running build is put back.
func swapFile(newPath, target string) error {
	old := target + OldSuffix
	if err := os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		// A previous .old is still locked (that build still runs): use a fresh name.
		old = fmt.Sprintf("%s.%d%s", target, time.Now().UnixNano(), OldSuffix)
	}
	if err := retry(func() error { return os.Rename(target, old) }); err != nil {
		return writeError(filepath.Dir(target), err)
	}
	if err := moveFile(newPath, target, 0o755); err != nil {
		if rerr := retry(func() error { return os.Rename(old, target) }); rerr != nil {
			return &Error{Kind: KindPermission, Msg: "Оновлення не вдалося, а попередню версію не вдалося повернути: перейменуй «" +
				filepath.Base(old) + "» назад на «" + filepath.Base(target) + "» у папці " + filepath.Dir(target) + ".", Err: errors.Join(err, rerr)}
		}
		return writeError(filepath.Dir(target), err)
	}
	return nil
}

// moveFile renames src to dst, copying when they sit on different volumes
// (the temp folder on C:, the app on D:). The copy lands next to dst first,
// so dst is never half-written.
func moveFile(src, dst string, mode fs.FileMode) error {
	if err := retry(func() error { return os.Rename(src, dst) }); err == nil {
		_ = os.Chmod(dst, mode)
		return nil
	}
	tmp := dst + ".new"
	if err := copyFile(src, tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := retry(func() error { return os.Rename(tmp, dst) }); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(src)
	return nil
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// retry runs fn up to 8 times over ~2 s: antivirus scanners briefly lock a
// freshly written exe.
func retry(fn func() error) error {
	var err error
	for i := 0; i < 8; i++ {
		if err = fn(); err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

// writeError explains a failed write into the app's folder.
func writeError(dir string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return &Error{Kind: KindPermission, Msg: "Немає прав змінювати файли в папці програми (" + dir + "). Перенеси студію в папку, де можна писати, або встанови нову версію вручну.", Err: err}
	}
	return &Error{Kind: KindPermission, Msg: "Не вдалося замінити файл програми: " + err.Error(), Err: err}
}

// ---- start-up cleanup --------------------------------------------------------------------------

// Cleanup removes what an earlier update left behind: the replaced build
// (<exe>.old on Windows, "<name>.old.app" next to the bundle on macOS),
// unpacked bundles of an interrupted install, and downloads in dir ("" =
// DefaultDir()) that are not newer than current. Run it on its own
// goroutine at start-up: the previous process may still be exiting, so it
// retries for up to 20 s.
func Cleanup(dir, current string) {
	if dir == "" {
		dir = DefaultDir()
	}
	cleanDownloads(dir, current)
	exe, err := runningExe()
	if err != nil {
		return
	}
	targets := leftovers(exe)
	for i := 0; i < 40 && len(targets) > 0; i++ {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		var keep []string
		for _, t := range targets {
			if err := os.RemoveAll(t); err != nil {
				keep = append(keep, t)
			}
		}
		targets = keep
	}
}

// leftovers lists the remains of an earlier update around exe.
func leftovers(exe string) []string {
	var out []string
	add := func(pattern string) {
		m, _ := filepath.Glob(pattern)
		out = append(out, m...)
	}
	add(globEscape(exe) + "*" + OldSuffix) // <exe>.old, <exe>.<n>.old
	if bundle, ok := bundleOf(exe); ok {
		parent, name := filepath.Dir(bundle), strings.TrimSuffix(filepath.Base(bundle), ".app")
		add(filepath.Join(globEscape(parent), globEscape(name)+"*"+OldSuffix+".app"))
		add(filepath.Join(globEscape(parent), "."+globEscape(name)+".update-*"))
	}
	return out
}

// versionInName finds "-<x.y.z…>-" in a download's name.
var versionInName = regexp.MustCompile(`-(\d+\.\d+\.\d+[0-9A-Za-z.\-]*?)-(?:windows|macos)-`)

// cleanDownloads removes downloads that are not newer than current.
func cleanDownloads(dir, current string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		m := versionInName.FindStringSubmatch(e.Name())
		if m != nil && Newer(m[1], current) {
			continue // a newer build waiting for its install
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	if rest, err := os.ReadDir(dir); err == nil && len(rest) == 0 {
		_ = os.Remove(dir)
	}
}
