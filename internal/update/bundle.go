package update

// macOS .app bundles: find the bundle the app runs from, unpack a zipped
// bundle safely next to it and swap the two. Plain path and zip logic, so it
// is tested on every OS; apply_darwin.go wires it up.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxUnpacked caps what a bundle zip may unpack to (a zip bomb guard).
const maxUnpacked = 2 << 30

// bundleOf finds the .app bundle exe runs from (".../X.app/Contents/MacOS/X").
func bundleOf(exe string) (string, bool) {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" ||
		!strings.HasSuffix(strings.ToLower(filepath.Base(app)), ".app") {
		return "", false
	}
	return app, true
}

// oldBundle is where the replaced bundle goes: "X.app" → "X.old.app".
func oldBundle(bundle string) string {
	name := strings.TrimSuffix(filepath.Base(bundle), filepath.Ext(bundle))
	return filepath.Join(filepath.Dir(bundle), name+OldSuffix+".app")
}

// stageBundle unpacks the zipped bundle into a hidden folder next to bundle
// (same volume: the swap is a rename) and returns the unpacked .app.
func stageBundle(zipPath, bundle string) (app, stage string, err error) {
	parent := filepath.Dir(bundle)
	name := strings.TrimSuffix(filepath.Base(bundle), filepath.Ext(bundle))
	stage = filepath.Join(parent, fmt.Sprintf(".%s.update-%d", name, time.Now().UnixNano()))
	if err := os.Mkdir(stage, 0o755); err != nil {
		return "", "", writeError(parent, err)
	}
	fail := func(err error) (string, string, error) {
		_ = os.RemoveAll(stage)
		return "", "", err
	}
	if err := unzip(zipPath, stage); err != nil {
		return fail(err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return fail(err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".app") {
			app = filepath.Join(stage, e.Name())
			break
		}
	}
	if app == "" {
		return fail(&Error{Kind: KindServer, Msg: "В архіві оновлення немає програми (.app) — завантаж нову версію вручну."})
	}
	bin, err := os.ReadDir(filepath.Join(app, "Contents", "MacOS"))
	if err != nil || len(bin) == 0 {
		return fail(&Error{Kind: KindServer, Msg: "Програма в архіві оновлення неповна — завантаж нову версію вручну.", Err: err})
	}
	return app, stage, nil
}

// swapBundle moves newApp to bundle; the running one becomes oldBundle.
func swapBundle(newApp, bundle string) error {
	old := oldBundle(bundle)
	if err := os.RemoveAll(old); err != nil {
		old = strings.TrimSuffix(old, ".app") + fmt.Sprintf("-%d.app", time.Now().UnixNano())
	}
	if err := os.Rename(bundle, old); err != nil {
		return writeError(filepath.Dir(bundle), err)
	}
	if err := os.Rename(newApp, bundle); err != nil {
		if rerr := os.Rename(old, bundle); rerr != nil {
			return &Error{Kind: KindPermission, Msg: "Оновлення не вдалося, а попередню версію не вдалося повернути: перейменуй «" +
				filepath.Base(old) + "» на «" + filepath.Base(bundle) + "».", Err: errors.Join(err, rerr)}
		}
		return writeError(filepath.Dir(bundle), err)
	}
	return nil
}

// unzip extracts zipPath into dest the way ditto does for a bundle: unix
// modes kept (executables stay executable), AppleDouble/__MACOSX metadata
// skipped. Entries that would land outside dest (absolute paths, "..",
// symlinks pointing out) refuse the whole archive.
func unzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return &Error{Kind: KindServer, Msg: "Архів оновлення пошкоджено — завантаж його ще раз.", Err: err}
	}
	defer r.Close()
	dest, err = filepath.Abs(dest)
	if err != nil {
		return err
	}
	unsafe := func(name string) error {
		return &Error{Kind: KindChecksum, Msg: "Архів оновлення містить небезпечний шлях (" + name + ") — встановлення скасовано.", Err: fmt.Errorf("zip entry %q", name)}
	}
	var total uint64
	for _, f := range r.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || strings.HasPrefix(pathBase(name), "._") {
			continue
		}
		target, ok := within(dest, name)
		if !ok {
			return unsafe(f.Name)
		}
		mode := f.Mode()
		total += f.UncompressedSize64
		if total > maxUnpacked {
			return &Error{Kind: KindServer, Msg: "Архів оновлення завеликий — встановлення скасовано."}
		}
		switch {
		case mode.IsDir() || strings.HasSuffix(name, "/"):
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			link, err := readAll(f, 4096)
			if err != nil {
				return err
			}
			// Only links that point down from where they are ("Versions/Current"
			// in a framework): no absolute targets, no "..", so no chain of
			// links can lead out of dest.
			to := string(link)
			if to == "" || filepath.IsAbs(to) || strings.HasPrefix(to, "/") || strings.HasPrefix(to, `\`) ||
				filepath.VolumeName(filepath.FromSlash(to)) != "" || hasDotDot(to) {
				return unsafe(f.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(to, target); err != nil {
				return err
			}
		case mode.IsRegular() || mode.Type() == 0:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			perm := mode.Perm()
			if perm == 0 {
				perm = 0o644
			}
			if err := extractFile(f, target, perm|0o200); err != nil {
				return err
			}
		}
	}
	return nil
}

// within joins a slash path under dest and reports whether it stays inside.
func within(dest, name string) (string, bool) {
	if strings.HasPrefix(name, "/") || filepath.VolumeName(filepath.FromSlash(name)) != "" {
		return "", false
	}
	if hasDotDot(name) {
		return "", false
	}
	target := filepath.Join(dest, filepath.FromSlash(name))
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return target, true
}

func extractFile(f *zip.File, target string, perm fs.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1)); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, perm) // the umask must not drop the exec bits
}

func readAll(f *zip.File, max int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, max))
}

// hasDotDot reports a ".." element in a slash or backslash path.
func hasDotDot(p string) bool {
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// pathBase is the last element of a zip (slash) path.
func pathBase(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
