package toolsinst

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Limits of an archive (an official build is far below them).
var (
	maxUnpacked int64 = 2 << 30 // bytes the kept entries may unpack to
	maxEntries        = 20000
)

// ExtractZip unpacks regular files of archive into dst. pick (nil = keep
// every file under its own name) gets each entry's slash-separated name
// ("\" separators of PowerShell's Compress-Archive are accepted) and returns
// the relative path to write it to, or "" to skip it. Names leaving dst,
// links and devices are refused or skipped; CRCs are checked. Executable
// bits of the entry are kept (0o755), other files get 0o644.
func ExtractZip(archive, dst string, pick func(name string) string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) { // names are checked below
		return fmt.Errorf("Архів пошкоджений — спробуй ще раз (%v)", err)
	}
	defer zr.Close()
	if len(zr.File) > maxEntries {
		return errors.New("В архіві забагато файлів — встановлення зупинено")
	}
	var total int64
	for _, f := range zr.File {
		name := strings.Trim(strings.ReplaceAll(f.Name, `\`, "/"), "/")
		if name == "" || f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, `\`) {
			continue
		}
		if !filepath.IsLocal(filepath.FromSlash(path.Clean(name))) {
			return fmt.Errorf("Архів містить небезпечний шлях «%s» — встановлення зупинено", f.Name)
		}
		if !f.Mode().IsRegular() {
			continue // links and devices never come out of an archive
		}
		rel := name
		if pick != nil {
			if rel = pick(name); rel == "" {
				continue
			}
		}
		rel = filepath.FromSlash(path.Clean(strings.ReplaceAll(rel, `\`, "/")))
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("Неправильний шлях «%s» для файлу з архіву", rel)
		}
		if total += int64(f.UncompressedSize64); total > maxUnpacked || f.UncompressedSize64 > uint64(maxUnpacked) {
			return errors.New("Архів завеликий — встановлення зупинено")
		}
		if err := unzipFile(f, filepath.Join(dst, rel)); err != nil {
			return err
		}
	}
	return nil
}

func unzipFile(f *zip.File, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return writeErr(err)
	}
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("Архів пошкоджений: %v", err)
	}
	defer rc.Close()
	perm := os.FileMode(0o644)
	if f.Mode().Perm()&0o111 != 0 {
		perm = 0o755
	}
	w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return writeErr(err)
	}
	n, cerr := io.Copy(w, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
	if err := w.Close(); cerr == nil {
		cerr = err
	}
	switch {
	case cerr != nil && (errors.Is(cerr, zip.ErrChecksum) || errors.Is(cerr, zip.ErrFormat) || errors.Is(cerr, io.ErrUnexpectedEOF)):
		return fmt.Errorf("Архів пошкоджений: %v", cerr)
	case cerr != nil:
		return writeErr(cerr)
	case n != int64(f.UncompressedSize64):
		return errors.New("Архів пошкоджений: неправильний розмір файлу")
	}
	return os.Chmod(out, perm) // an existing file keeps its mode through O_TRUNC
}

// ErrInUse: the installed program's files are open (it is running).
var ErrInUse = errors.New("files in use")

// ReplaceDir moves src to dst, replacing a previous dst only once the new
// one is in place (a failed swap restores the old folder). Both must be on
// the same volume. ErrInUse (wrapped) when the old folder cannot be moved
// away — its program is running.
func ReplaceDir(src, dst string) error {
	old := ""
	if _, err := os.Stat(dst); err == nil {
		old = dst + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := rename(dst, old); err != nil {
			return fmt.Errorf("%w: %v", ErrInUse, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return writeErr(err)
	}
	if err := rename(src, dst); err != nil {
		if old != "" {
			_ = os.Rename(old, dst)
		}
		return writeErr(err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// rename retries a little: on Windows an antivirus scanning the fresh
// executables holds them open for a moment.
func rename(src, dst string) error {
	var err error
	for i := 0; i < renameTries; i++ {
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
		time.Sleep(renamePause)
	}
	return err
}

var (
	renameTries = 6
	renamePause = 250 * time.Millisecond
)

// cleanOld removes leftovers of earlier runs next to dir: "<dir>.old-*"
// folders a swap could not delete and stale "<prefix>*" staging folders.
func cleanOld(dir, stagingPrefix string) {
	parent := filepath.Dir(dir)
	ents, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	base := filepath.Base(dir)
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() && (strings.HasPrefix(n, base+".old-") || stagingPrefix != "" && strings.HasPrefix(n, stagingPrefix)) {
			_ = os.RemoveAll(filepath.Join(parent, n))
		}
	}
}
