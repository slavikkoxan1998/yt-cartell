package stt

// Installing local speech recognition — only ever when the user presses the
// button in Settings, and only from the official sources:
//
//	program  Windows: the CPU build attached to the whisper.cpp GitHub
//	         releases (release.yml packs build/bin/Release with
//	         Compress-Archive, so the zip holds Release/whisper-cli.exe with
//	         whisper.dll, ggml*.dll — one CPU backend per instruction set —
//	         and SDL2.dll). The binaries ride on the "b<build>" pre-releases
//	         (the "v1.x.y" releases carry none), so the newest release that
//	         has the asset wins:
//	           GET https://api.github.com/repos/ggml-org/whisper.cpp/releases
//	           → assets[].{name, size, digest "sha256:…", browser_download_url}
//	         When the API cannot be read (60 requests an hour without a login)
//	         the pinned build below is used.
//	         macOS/Linux: there is no official command-line build (only an
//	         XCFramework for apps): Homebrew's whisper-cpp is used when found,
//	         else the user is told to run `brew install whisper-cpp`.
//	model    https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-<id>.bin
//	         (the URL of the repo's models/download-ggml-model.sh), checked
//	         against the catalogue's size and SHA-256 (or the X-Linked-Etag
//	         SHA-256 Hugging Face publishes on the redirect).
//
// Layout: <dir>/bin (the program and its DLLs, swapped in as a whole),
// <dir>/models/ggml-<id>.bin. Downloads go to "<file>.part" first, resume
// with HTTP Range after a broken connection, a stall or a cancel, and are
// renamed into place only once verified; a part that fails the check is
// deleted.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// fileSpec is one file to download.
type fileSpec struct {
	URL    string
	Size   int64  // bytes (0 = unknown)
	SHA256 string // lower-case hex ("" = not published)
	Tag    string // the GitHub release (program archives)
}

// Official sources and tunables (package vars so tests point them at a mock).
var (
	releasesAPI   = "https://api.github.com/repos/ggml-org/whisper.cpp/releases?per_page=30"
	releasePrefix = "https://github.com/ggml-org/whisper.cpp/releases/download/"
	modelBase     = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/"

	// pinnedTag / pinnedAssets: the build used when the releases API cannot
	// be read — sizes and SHA-256 digests as GitHub publishes them for
	// release b5130 (2026-09-11).
	pinnedTag    = "b5454"
	pinnedAssets = map[string]fileSpec{
		"whisper-bin-x64.zip":             {Size: 8573270, SHA256: "f9ec6c52a2e949b62ab51fa21d0d497958f9e41c3010c157c4e42932d5316f3c"},
		"whisper-bin-Win32.zip":           {Size: 5384054, SHA256: "7c0b2c10a0c30acddcf77c3d72f31a02c3a09a11a2b1e13d6693d3d0f1f7d169"},
		"whisper-bin-win-cpu-arm64.zip":   {Size: 4361895, SHA256: "799543b926ab5b6c2d60cab269a2092e0ae8d27820e9e15429e59de3699546fc"},
		"whisper-bin-ubuntu-x64.tar.gz":   {Size: 10364195, SHA256: "a72becf15d7917f990f6313867a52638b82b7f9ef237fb0c980dac56a135781c"},
		"whisper-bin-ubuntu-arm64.tar.gz": {Size: 4608377, SHA256: "6b95ebfc60447df48e70ef00a73bdc3f41679ed2d01ef465827206a2ff325149"},
	}

	installGOOS, installGOARCH = runtime.GOOS, runtime.GOARCH

	installHTTP     = &http.Client{} // nil Transport = http.DefaultTransport
	apiTimeout      = 30 * time.Second
	downloadStall   = 60 * time.Second // no byte for this long: reconnect and resume
	downloadRetries = 4
	retryPause      = 2 * time.Second
)

const (
	userAgent   = "YT-Cartell-Studio"
	maxUnpacked = 1 << 30 // bytes a program archive may unpack to
	maxEntries  = 4000
)

var installMu sync.Mutex // one installation at a time

// assetName is the official CPU build for an architecture ("" = none
// for this OS: macOS gets the program from Homebrew).
func assetName(goos, goarch string) string {
	if goos == "windows" {
		switch goarch {
		case "amd64":
			return "whisper-bin-x64.zip"
		case "386":
			return "whisper-bin-Win32.zip"
		case "arm64":
			return "whisper-bin-win-cpu-arm64.zip"
		}
	} else if goos == "linux" {
		switch goarch {
		case "amd64":
			return "whisper-bin-ubuntu-x64.tar.gz"
		case "arm64":
			return "whisper-bin-ubuntu-arm64.tar.gz"
		}
	}
	return ""
}

// InstallLocal makes local speech recognition ready in dir ("" = LocalDir):
// a working whisper.cpp program — one already found (dir/bin, next to the
// app, PATH, Homebrew) or, on Windows, the official release build — and the
// model (a LocalModels id, "" = DefaultLocalModel). It returns both paths.
// On macOS/Linux without the program it downloads nothing and the error says
// how to get it (brew install whisper-cpp). progress(pct, message) may be nil.
// Cancelling ctx stops at once; a partly downloaded model resumes next time.
// Started by the user only (Settings → «Встановити»).
func InstallLocal(ctx context.Context, dir, model string, progress func(pct int, msg string)) (bin, modelPath string, err error) {
	m, dir, err := beginInstall(dir, model)
	if err != nil {
		return "", "", err
	}
	defer installMu.Unlock()
	rep := newReporter(progress)
	rep.force(0, "Шукаємо whisper.cpp…")
	bin = usableBin(ctx, dir)
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	prog := &installProgress{rep: rep}
	var spec fileSpec
	if bin == "" {
		name := assetName(installGOOS, installGOARCH)
		if name == "" {
			return "", "", errNoProgramFor(installGOOS)
		}
		if spec, err = resolveAsset(ctx, name); err != nil {
			return "", "", err
		}
		prog.total += spec.Size
	}
	mdst := filepath.Join(dir, "models", modelFile(m.ID))
	if !modelComplete(mdst, m) {
		prog.total += m.size
	}
	if bin == "" {
		if bin, err = installProgram(ctx, dir, spec, prog); err != nil {
			return "", "", err
		}
		rep.force(prog.pct(), "Перевіряємо whisper.cpp…")
		if _, err = probeWhisper(ctx, bin, true); err != nil {
			return "", "", err
		}
	}
	if modelPath, err = fetchModel(ctx, dir, m, prog); err != nil {
		return "", "", err
	}
	rep.force(100, "Готово: whisper.cpp і модель "+m.ID+" встановлено")
	return bin, modelPath, nil
}

// InstallLocalModel downloads only the model into dir/models ("" dir =
// LocalDir) — for a whisper.cpp program the user set up themselves.
func InstallLocalModel(ctx context.Context, dir, model string, progress func(pct int, msg string)) (string, error) {
	m, dir, err := beginInstall(dir, model)
	if err != nil {
		return "", err
	}
	defer installMu.Unlock()
	prog := &installProgress{rep: newReporter(progress)}
	if !modelComplete(filepath.Join(dir, "models", modelFile(m.ID)), m) {
		prog.total = m.size
	}
	p, err := fetchModel(ctx, dir, m, prog)
	if err != nil {
		return "", err
	}
	prog.rep.force(100, "Готово: модель "+m.ID+" завантажено")
	return p, nil
}

// beginInstall checks the model id, creates dir and takes installMu (the
// caller unlocks it when err is nil).
func beginInstall(dir, model string) (localModel, string, error) {
	id := strings.ToLower(strings.TrimSpace(model))
	if id == "" {
		id = DefaultLocalModel
	}
	m, ok := lookupModel(id)
	if !ok {
		return localModel{}, "", fmt.Errorf("Невідома модель «%s» — вибери одну зі списку", model)
	}
	if strings.TrimSpace(dir) == "" {
		dir = LocalDir()
	}
	dir = absPath(dir)
	if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
		return localModel{}, "", fmt.Errorf("Не вдалося створити папку %s: %v", dir, err)
	}
	if !installMu.TryLock() {
		return localModel{}, "", errors.New("Встановлення вже триває — дочекайся, поки воно завершиться")
	}
	return m, dir, nil
}

// errNoProgramFor: the program cannot be downloaded for this OS.
func errNoProgramFor(goos string) error {
	if goos == "darwin" {
		return errors.New("На Mac програму whisper.cpp ставить Homebrew: відкрий Термінал, виконай brew install whisper-cpp (якщо Homebrew ще немає — brew.sh) і натисни «Встановити» ще раз — модель завантажиться сама")
	}
	return errors.New("Для цієї системи немає готової програми whisper.cpp: встанови whisper-cli через менеджер пакетів або Homebrew (brew install whisper-cpp) і натисни «Встановити» ще раз — модель завантажиться сама")
}

// usableBin is a whisper.cpp that already works: the install folder first,
// then wherever LocalStatus finds one without Settings (next to the app,
// LocalDir, PATH, Homebrew…).
func usableBin(ctx context.Context, dir string) string {
	cands := []string{binIn(dir, false)}
	if p, err := findBin(""); err == nil {
		cands = append(cands, p)
	}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if _, err := probeWhisper(ctx, c, true); err == nil {
			return c
		}
	}
	return ""
}

// ---- the program ------------------------------------------------------------------------------

// resolveAsset finds the newest official build of name, falling back to the
// pinned one when the GitHub API cannot be read.
func resolveAsset(ctx context.Context, name string) (fileSpec, error) {
	spec, err := latestAsset(ctx, name)
	if err == nil {
		return spec, nil
	}
	if ctx.Err() != nil {
		return fileSpec{}, ctx.Err()
	}
	p, ok := pinnedAssets[name]
	if !ok {
		return fileSpec{}, fmt.Errorf("Не вдалося знайти whisper.cpp на GitHub: %v", err)
	}
	log.Printf("[stt] whisper.cpp releases: %v — using the pinned build %s", err, pinnedTag)
	p.URL, p.Tag = releasePrefix+pinnedTag+"/"+name, pinnedTag
	return p, nil
}

func latestAsset(ctx context.Context, name string) (fileSpec, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesAPI, nil)
	if err != nil {
		return fileSpec{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	res, err := installHTTP.Do(req)
	if err != nil {
		return fileSpec{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fileSpec{}, fmt.Errorf("GitHub API %d", res.StatusCode)
	}
	var rels []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
		Assets  []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
			URL    string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&rels); err != nil {
		return fileSpec{}, fmt.Errorf("GitHub API: %v", err)
	}
	for _, r := range rels {
		if r.Draft {
			continue
		}
		for _, a := range r.Assets {
			// Only the official repository's own downloads.
			if a.Name != name || a.Size <= 0 || a.State != "" && a.State != "uploaded" || !strings.HasPrefix(a.URL, releasePrefix) {
				continue
			}
			sha := ""
			if h, ok := strings.CutPrefix(strings.ToLower(a.Digest), "sha256:"); ok && isHex64(h) {
				sha = h
			}
			return fileSpec{URL: a.URL, Size: a.Size, SHA256: sha, Tag: r.TagName}, nil
		}
	}
	return fileSpec{}, fmt.Errorf("no %s in the recent releases", name)
}

// installProgram downloads and unpacks the release archive into dir/bin
// (swapped in as a whole) and returns the path of whisper-cli.
func installProgram(ctx context.Context, dir string, spec fileSpec, prog *installProgress) (string, error) {
	dl := filepath.Join(dir, ".download", safeName(spec.Tag))
	if err := os.MkdirAll(dl, 0o755); err != nil {
		return "", writeErr(err)
	}
	archive := filepath.Join(dl, safeName(path.Base(spec.URL)))
	if err := download(ctx, spec, archive, func(done int64) { prog.bytes("whisper.cpp", done, spec.Size) }); err != nil {
		return "", err
	}
	prog.done(spec.Size)
	prog.rep.force(prog.pct(), "Розпаковуємо whisper.cpp…")
	staging, err := os.MkdirTemp(dir, ".unpack-")
	if err != nil {
		return "", writeErr(err)
	}
	defer os.RemoveAll(staging)
	if strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz") {
		if err := extractTarGz(archive, staging); err != nil {
			return "", err
		}
	} else {
		if err := extractZip(archive, staging); err != nil {
			return "", err
		}
	}
	cli := findInTree(staging, exe("whisper-cli"), 4)
	if cli == "" {
		return "", errors.New("В архіві whisper.cpp немає програми whisper-cli — спробуй пізніше або повідом про це розробникам")
	}
	final := filepath.Join(dir, "bin")
	if err := replaceDir(filepath.Dir(cli), final); err != nil {
		return "", err
	}
	_ = os.RemoveAll(filepath.Join(dir, ".download"))
	return filepath.Join(final, filepath.Base(cli)), nil
}

// extractZip unpacks the regular files of a zip into dst: "\" separators
// (Windows PowerShell's Compress-Archive) are accepted, paths leaving dst,
// links and oversized archives are refused, CRCs are checked by archive/zip.
func extractZip(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) { // names are checked below
		return fmt.Errorf("Архів whisper.cpp пошкоджений — спробуй ще раз (%v)", err)
	}
	defer zr.Close()
	if len(zr.File) > maxEntries {
		return errors.New("Архів whisper.cpp має забагато файлів")
	}
	var total uint64
	for _, f := range zr.File {
		name := strings.Trim(strings.ReplaceAll(f.Name, `\`, "/"), "/")
		if name == "" {
			continue
		}
		rel := filepath.FromSlash(path.Clean(name))
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("Архів whisper.cpp містить небезпечний шлях «%s» — встановлення зупинено", f.Name)
		}
		out := filepath.Join(dst, rel)
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, `\`) {
			if err := os.MkdirAll(out, 0o755); err != nil {
				return writeErr(err)
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue // links and devices never come out of an archive
		}
		if total += f.UncompressedSize64; total > maxUnpacked {
			return errors.New("Архів whisper.cpp завеликий — встановлення зупинено")
		}
		if err := unzipFile(f, out); err != nil {
			return err
		}
	}
	return nil
}

// extractTarGz unpacks .tar.gz archives (whisper.cpp Linux builds) into dst.
func extractTarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("Архів whisper.cpp не відкривається: %v", err)
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("Архів whisper.cpp пошкоджений (gzip): %v", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	var total uint64
	entries := 0
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("Архів whisper.cpp пошкоджений (tar): %v", err)
		}
		entries++
		if entries > maxEntries {
			return errors.New("Архів whisper.cpp має забагато файлів")
		}
		name := strings.Trim(strings.ReplaceAll(header.Name, `\`, "/"), "/")
		if name == "" {
			continue
		}
		rel := filepath.FromSlash(path.Clean(name))
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("Архів whisper.cpp містить небезпечний шлях «%s» — встановлення зупинено", header.Name)
		}
		out := filepath.Join(dst, rel)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o755); err != nil {
				return writeErr(err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return writeErr(err)
			}
			if total += uint64(header.Size); total > maxUnpacked {
				return errors.New("Архів whisper.cpp завеликий — встановлення зупинено")
			}
			mode := os.FileMode(header.Mode)
			if mode&0o111 != 0 {
				mode = 0o755
			} else {
				mode = 0o644
			}
			w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return writeErr(err)
			}
			_, cerr := io.Copy(w, io.LimitReader(tr, header.Size+1))
			if err := w.Close(); cerr == nil {
				cerr = err
			}
			if cerr != nil {
				return writeErr(cerr)
			}
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
		return fmt.Errorf("Архів whisper.cpp пошкоджений: %v", err)
	}
	defer rc.Close()
	w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return writeErr(err)
	}
	n, cerr := io.Copy(w, io.LimitReader(rc, int64(f.UncompressedSize64)+1))
	if err := w.Close(); cerr == nil {
		cerr = err
	}
	switch {
	case cerr != nil && (errors.Is(cerr, zip.ErrChecksum) || errors.Is(cerr, zip.ErrFormat) || errors.Is(cerr, io.ErrUnexpectedEOF)):
		return fmt.Errorf("Архів whisper.cpp пошкоджений: %v", cerr)
	case cerr != nil:
		return writeErr(cerr)
	case n != int64(f.UncompressedSize64):
		return errors.New("Архів whisper.cpp пошкоджений: неправильний розмір файлу")
	}
	return nil
}

// findInTree is the shallowest file called name under root (case-insensitive
// on Windows), at most depth folders down.
func findInTree(root, name string, depth int) string {
	level := []string{root}
	for d := 0; d <= depth && len(level) > 0; d++ {
		var next []string
		for _, dir := range level {
			ents, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range ents {
				p := filepath.Join(dir, e.Name())
				switch {
				case e.IsDir():
					next = append(next, p)
				case e.Name() == name || runtime.GOOS == "windows" && strings.EqualFold(e.Name(), name):
					return p
				}
			}
		}
		level = next
	}
	return ""
}

// replaceDir moves src to dst, replacing a previous dst only once the new
// one is in place (a failed swap restores the old folder).
func replaceDir(src, dst string) error {
	old := ""
	if _, err := os.Stat(dst); err == nil {
		old = dst + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := os.Rename(dst, old); err != nil {
			return errors.New("Не вдалося оновити whisper.cpp: його файли зайняті — дочекайся кінця розпізнавання й спробуй ще раз")
		}
	}
	if err := os.Rename(src, dst); err != nil {
		if old != "" {
			_ = os.Rename(old, dst)
		}
		return fmt.Errorf("Не вдалося встановити whisper.cpp: %v", err)
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}

// ---- the model --------------------------------------------------------------------------------

// fetchModel downloads the catalogue model m into dir/models (kept when it
// is already complete).
func fetchModel(ctx context.Context, dir string, m localModel, prog *installProgress) (string, error) {
	dst := filepath.Join(dir, "models", modelFile(m.ID))
	if modelComplete(dst, m) {
		return dst, nil
	}
	spec := fileSpec{URL: modelBase + modelFile(m.ID), Size: m.size, SHA256: m.sha256}
	label := "модель " + m.ID
	if err := download(ctx, spec, dst, func(done int64) { prog.bytes(label, done, m.size) }); err != nil {
		return "", err
	}
	prog.done(m.size)
	if err := checkModel(dst); err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	return dst, nil
}

// modelComplete: the model file is there in full (it was verified when it
// was downloaded; a quick look at the size and the ggml header is enough).
func modelComplete(path string, m localModel) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Size() == m.size && checkModel(path) == nil
}

// ---- downloads --------------------------------------------------------------------------------

var errStalled = errors.New("download stalled")

// permanent marks a download error that another attempt cannot fix.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// dlState is one file's download across attempts: the SHA-256 of the part
// so far and the digests the server published.
type dlState struct {
	h         hash.Hash
	hashed    int64
	published []string
}

// download fetches spec into dst through dst+".part" (see the file comment).
// onBytes gets the bytes of the file on disk so far.
func download(ctx context.Context, spec fileSpec, dst string, onBytes func(done int64)) error {
	part := dst + ".part"
	ds := &dlState{h: sha256.New()}
	host := hostOf(spec.URL)
	for attempt := 0; ; attempt++ {
		err := fetchOnce(ctx, spec, part, ds, onBytes)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err() // the part stays: the next run resumes
		}
		var p permanent
		if errors.As(err, &p) || attempt >= downloadRetries {
			return dlErr(host, err)
		}
		if serr := sleep(ctx, retryPause*time.Duration(attempt+1)); serr != nil {
			return serr
		}
	}
	if err := ds.verify(part, spec); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := os.Rename(part, dst); err != nil {
		return writeErr(err)
	}
	return nil
}

// fetchOnce makes one request, resuming the part when it has bytes.
func fetchOnce(ctx context.Context, spec fileSpec, part string, ds *dlState, onBytes func(int64)) error {
	have := fileSize(part)
	if spec.Size > 0 && have > spec.Size {
		_ = os.Remove(part)
		have = 0
	}
	if err := ds.catchUp(part, have); err != nil {
		return err
	}
	if spec.Size > 0 && have == spec.Size {
		return nil // complete from an earlier run
	}
	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(downloadStall, func() { cancel(errStalled) })
	defer stall.Stop()
	req, err := http.NewRequestWithContext(actx, http.MethodGet, spec.URL, nil)
	if err != nil {
		return permanent{err}
	}
	req.Header.Set("User-Agent", userAgent)
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	res, err := installHTTP.Do(req)
	if err != nil {
		return stalledOr(actx, err)
	}
	defer res.Body.Close()
	ds.collect(res)
	switch st := res.StatusCode; {
	case st == http.StatusPartialContent && have > 0 && rangeFrom(res.Header.Get("Content-Range")) == have:
	case st == http.StatusOK:
		have = 0 // the server sends the whole file
		ds.reset()
	case st == http.StatusPartialContent || st == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part) // the part does not fit this file: start over
		ds.reset()
		return fmt.Errorf("сервер не продовжив завантаження (%d)", st)
	case st == http.StatusNotFound || st == http.StatusGone:
		return permanent{fmt.Errorf("файл не знайдено на сервері (%d)", st)}
	case st == http.StatusUnauthorized || st == http.StatusForbidden:
		return permanent{fmt.Errorf("сервер не дає завантажити файл (%d)", st)}
	default:
		return fmt.Errorf("сервер відповів %d", st)
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if have > 0 {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return permanent{writeErr(err)}
	}
	room := int64(math.MaxInt64)
	if spec.Size > 0 {
		room = spec.Size - have
	}
	buf := make([]byte, 256<<10)
	done := have
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			stall.Reset(downloadStall)
			if int64(n) > room {
				f.Close()
				_ = os.Remove(part)
				ds.reset()
				return permanent{errors.New("файл на сервері більший, ніж має бути")}
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return permanent{writeErr(werr)}
			}
			ds.h.Write(buf[:n])
			ds.hashed += int64(n)
			room -= int64(n)
			done += int64(n)
			if onBytes != nil {
				onBytes(done)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return stalledOr(actx, rerr)
		}
	}
	if err := f.Close(); err != nil {
		return permanent{writeErr(err)}
	}
	if spec.Size > 0 && done < spec.Size {
		return io.ErrUnexpectedEOF // resumed by the next attempt
	}
	return nil
}

func stalledOr(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errStalled) {
		return errStalled
	}
	return err
}

// catchUp makes the hash cover exactly the first have bytes of the part
// (bytes left by an earlier run or attempt).
func (d *dlState) catchUp(part string, have int64) error {
	if d.hashed == have {
		return nil
	}
	d.reset()
	if have <= 0 {
		return nil
	}
	f, err := os.Open(part)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(d.h, io.LimitReader(f, have))
	d.hashed = n
	if err != nil || n != have {
		d.reset()
		_ = os.Remove(part)
		return fmt.Errorf("не вдалося прочитати недокачаний файл: %v", err)
	}
	return nil
}

func (d *dlState) reset() {
	d.h.Reset()
	d.hashed = 0
}

// collect keeps the SHA-256 Hugging Face publishes for an LFS file
// (X-Linked-Etag on its redirect) from every response of the chain.
func (d *dlState) collect(res *http.Response) {
	for r, hops := res, 0; r != nil && hops < 12; hops++ {
		if v := strings.ToLower(strings.Trim(strings.TrimPrefix(r.Header.Get("X-Linked-Etag"), "W/"), `"`)); isHex64(v) {
			seen := false
			for _, p := range d.published {
				seen = seen || p == v
			}
			if !seen {
				d.published = append(d.published, v)
			}
		}
		if r.Request == nil {
			break
		}
		r = r.Request.Response
	}
}

// verify: the part has the expected size and a SHA-256 the catalogue,
// GitHub or the server vouches for (the size alone when none is known).
func (d *dlState) verify(part string, spec fileSpec) error {
	size := fileSize(part)
	if spec.Size > 0 && size != spec.Size {
		return fmt.Errorf("Завантажений файл має неправильний розмір (%s замість %s) — спробуй ще раз", fmtSize(size), fmtSize(spec.Size))
	}
	if err := d.catchUp(part, size); err != nil {
		return errors.New("Не вдалося перевірити завантажений файл — спробуй ще раз")
	}
	accept := append([]string{}, d.published...)
	if spec.SHA256 != "" {
		accept = append(accept, strings.ToLower(spec.SHA256))
	}
	if len(accept) == 0 {
		return nil
	}
	sum := hex.EncodeToString(d.h.Sum(nil))
	for _, a := range accept {
		if a == sum {
			return nil
		}
	}
	return errors.New("Завантажений файл пошкоджений (контрольна сума не збігається) — спробуй ще раз")
}

// rangeFrom is the first byte of "Content-Range: bytes 100-199/200" (-1 when unreadable).
func rangeFrom(v string) int64 {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "bytes"))
	from, _, ok := strings.Cut(v, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return 0
	}
	return st.Size()
}

// ---- progress and messages ----------------------------------------------------------------------

// installProgress spreads 0–98 % over the bytes of every download of one
// installation (the rest is unpacking and checks).
type installProgress struct {
	rep   *reporter
	total int64 // bytes of all downloads
	base  int64 // bytes of the finished ones
}

func (p *installProgress) pct() int {
	if p.total <= 0 {
		return 0
	}
	return int(min(98, p.base*98/p.total))
}

func (p *installProgress) bytes(label string, done, size int64) {
	pct := 0
	if p.total > 0 {
		pct = int(min(98, (p.base+done)*98/p.total))
	}
	p.rep.report(pct, fmt.Sprintf("Завантажуємо %s · %s з %s", label, fmtSize(done), fmtSize(size)))
}

func (p *installProgress) done(size int64) { p.base += size }

// fmtSize: "8,2 МБ", "465 МБ", "1,5 ГБ" (binary units, like the file managers).
func fmtSize(b int64) string {
	mb := float64(b) / (1 << 20)
	switch {
	case mb >= 1000:
		return strings.Replace(strconv.FormatFloat(mb/1024, 'f', 1, 64), ".", ",", 1) + " ГБ"
	case mb >= 100:
		return strconv.Itoa(int(mb+0.5)) + " МБ"
	}
	return strings.Replace(strconv.FormatFloat(mb, 'f', 1, 64), ".", ",", 1) + " МБ"
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return "сервера"
}

// dlErr is the message of a download that failed for good.
func dlErr(host string, err error) error {
	var msg userError
	if errors.As(err, &msg) { // already a message for the user (a full disk…)
		return msg
	}
	var p permanent
	if errors.As(err, &p) {
		return fmt.Errorf("Не вдалося завантажити з %s: %v", host, p.error)
	}
	var ne net.Error
	switch {
	case errors.Is(err, errStalled) || errors.As(err, &ne) && ne.Timeout():
		return fmt.Errorf("Завантаження з %s зупинилось — перевір інтернет і натисни «Встановити» ще раз: продовжимо з того ж місця", host)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("З'єднання з %s обривається — натисни «Встановити» ще раз: продовжимо з того ж місця", host)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("Немає з'єднання з %s — перевір інтернет і спробуй ще раз (%s)", host, clip(ue.Err.Error(), 120))
	}
	return fmt.Errorf("Не вдалося завантажити з %s: %s — спробуй ще раз", host, clip(err.Error(), 160))
}

// userError is a message ready for the user (Ukrainian, says what to do).
type userError struct{ error }

func (u userError) Unwrap() error { return u.error }

// writeErr is a friendly message for a failed write (a full disk first of all).
func writeErr(err error) error {
	var en syscall.Errno
	if errors.As(err, &en) && (runtime.GOOS == "windows" && (en == 112 || en == 39) || runtime.GOOS != "windows" && en == syscall.ENOSPC) {
		return userError{errors.New("Не вистачає місця на диску — звільни місце й спробуй ще раз")}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return userError{fmt.Errorf("Не вдалося записати файл %s: %v", filepath.Base(pe.Path), pe.Err)}
	}
	return userError{fmt.Errorf("Не вдалося записати файл: %v", err)}
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// safeName keeps a release tag or asset name usable as a file name.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if out := strings.Trim(b.String(), "."); out != "" {
		return out
	}
	return "download"
}
