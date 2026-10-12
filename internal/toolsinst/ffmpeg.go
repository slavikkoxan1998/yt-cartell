package toolsinst

// FFmpeg — official builds, pinned: version, size and SHA-256 exactly as
// their publishers list them (checked 2026-10-02, FFmpeg 9.0.2, released
// 2026-09-20).
//
//	Windows x64 (also Windows on ARM, through x64 emulation): Gyan Doshi's
//	    "essentials" release build — the Windows build ffmpeg.org/download.html
//	    links first (libx264, libmp3lame, libass, freetype, fribidi, harfbuzz:
//	    everything the montage uses).
//	      https://github.com/GyanD/codexffmpeg/releases/download/9.0.2/ffmpeg-9.0.2-essentials_build.zip
//	      mirror https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-9.0.2-essentials_build.zip
//	    SHA-256 = GitHub's asset digest = gyan.dev's "….zip.sha256".
//	    Layout: ffmpeg-9.0.2-essentials_build/bin/{ffmpeg,ffprobe,ffplay}.exe —
//	    ffmpeg.exe and ffprobe.exe (+ LICENSE, README.txt) are kept.
//	macOS: Homebrew when it is installed (`brew install ffmpeg`, no admin
//	    rights: the Homebrew prefix belongs to the user); otherwise Martin
//	    Riedl's static builds, signed and notarized, native for arm64 and
//	    x86_64 (https://ffmpeg.martin-riedl.de): ffmpeg.zip + ffprobe.zip per
//	    architecture, each holding one executable; SHA-256 from the published
//	    "<file>.sha256". Quarantine is cleared after unpacking.
//	Linux: nothing is downloaded — the package manager installs FFmpeg.
//
// Layout: <dir>/bin/ffmpeg(.exe), <dir>/bin/ffprobe(.exe), <dir>/install.json
// (version, sources, size + SHA-256 of every file). EnsureFFmpeg is
// idempotent: an installed copy whose files still match install.json and
// starts is kept; anything else is installed again (a repair).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/sysx"
)

// FFmpeg is a working FFmpeg: absolute paths of both programs.
type FFmpeg struct {
	FFmpeg, FFprobe string
	Version         string // first line of `ffmpeg -version`
	Source          string // SourceDownload | SourceHomebrew
}

// Where an installed FFmpeg came from.
const (
	SourceDownload = "download" // the pinned official build in FFmpegDir
	SourceHomebrew = "homebrew" // brew install ffmpeg (macOS)
)

// Options of InstallFFmpeg.
type Options struct {
	Dir        string // install folder ("" = FFmpegDir())
	Force      bool   // install again even when the installed copy checks out
	NoHomebrew bool   // macOS: download the static build even when Homebrew exists
}

// ffmpegBuild is one pinned build for a platform: its archives, each with
// the official URL (+ mirrors) and the published size and digest.
type ffmpegBuild struct {
	Version  string
	From     string // who publishes it (shown to the user)
	Archives []Asset
}

const manifestFile = "install.json"

// Pinned builds (package vars: tests point them at a mock server).
var (
	gyanWin = ffmpegBuild{Version: "9.0.2", From: "gyan.dev", Archives: []Asset{{
		URL:     "https://github.com/GyanD/codexffmpeg/releases/download/9.0.2/ffmpeg-9.0.2-essentials_build.zip",
		Mirrors: []string{"https://www.gyan.dev/ffmpeg/builds/packages/ffmpeg-9.0.2-essentials_build.zip"},
		Size:    114768076,
		SHA256:  "60f467265b1e312373dbcd92200c2618a74850f98d3d078e94296bb3fa2047ba",
	}}}
	ffmpegBuilds = map[string]ffmpegBuild{
		"windows/amd64": gyanWin,
		"windows/arm64": gyanWin, // Windows 11 on ARM runs x64 programs
		"darwin/arm64": {Version: "9.0.2", From: "ffmpeg.martin-riedl.de", Archives: []Asset{
			{
				URL:  "https://ffmpeg.martin-riedl.de/download/macos/arm64/1789931890_9.0.2/ffmpeg.zip",
				Name: "ffmpeg-9.0.2-arm64.zip", Size: 28395699,
				SHA256: "c8ed4c4e6978a03c485edbfe4e0a5dc2380f8a30bba5150531b31b094492d924",
			},
			{
				URL:  "https://ffmpeg.martin-riedl.de/download/macos/arm64/1789931890_9.0.2/ffprobe.zip",
				Name: "ffprobe-9.0.2-arm64.zip", Size: 28317701,
				SHA256: "fcbe839537485eaee7a7a8bc5cbc0f90d53617e80943e8a5b2e31cb851197ea6",
			},
		}},
		"darwin/amd64": {Version: "9.0.2", From: "ffmpeg.martin-riedl.de", Archives: []Asset{
			{
				URL:  "https://ffmpeg.martin-riedl.de/download/macos/amd64/1789931006_9.0.2/ffmpeg.zip",
				Name: "ffmpeg-9.0.2-x64.zip", Size: 33816391,
				SHA256: "7c6b4125b191cbf773832dc51f424cf2b6bb7da43007d1e066f95909e47cacd4",
			},
			{
				URL:  "https://ffmpeg.martin-riedl.de/download/macos/amd64/1789931006_9.0.2/ffprobe.zip",
				Name: "ffprobe-9.0.2-x64.zip", Size: 33719233,
				SHA256: "2322438ed2f6319a691291b247d09c69dcaa3a982460d1f269a7e1af335cfdfd",
			},
		}},
	}

	hostGOOS, hostGOARCH = runtime.GOOS, runtime.GOARCH
	findBrew             = defaultFindBrew // "" = no Homebrew
	probeVersion         = RunVersion      // tests use a fake
)

// FFmpegDir is where the studio installs FFmpeg: <Root>/ffmpeg (programs in bin/).
func FFmpegDir() string { return ToolDir("ffmpeg") }

// Offer says what «Встановити автоматично» does on this computer.
type Offer struct {
	OK       bool   // something can be installed here
	Homebrew bool   // macOS with Homebrew: brew install ffmpeg
	Version  string // the pinned build
	From     string // its publisher ("gyan.dev")
	Bytes    int64  // its download size (the static build)
	Note     string // !OK: what to do instead (Ukrainian)
}

// FFmpegOffer describes the automatic installation for this system.
func FFmpegOffer() Offer {
	b, ok := ffmpegBuilds[hostGOOS+"/"+hostGOARCH]
	o := Offer{OK: ok, Version: b.Version, From: b.From}
	for _, a := range b.Archives {
		o.Bytes += a.Size
	}
	if hostGOOS == "darwin" && findBrew() != "" {
		o.OK, o.Homebrew = true, true
	}
	if !o.OK {
		o.Note = errNoBuild(hostGOOS, hostGOARCH).Error()
	}
	return o
}

// InstalledFFmpeg is the studio's own FFmpeg (FFmpegDir) when both programs
// are there — a quick look, no check that they run.
func InstalledFFmpeg() (FFmpeg, bool) { return installedIn(FFmpegDir()) }

func installedIn(dir string) (FFmpeg, bool) {
	ff := FFmpeg{
		FFmpeg:  filepath.Join(dir, "bin", exe("ffmpeg", hostGOOS)),
		FFprobe: filepath.Join(dir, "bin", exe("ffprobe", hostGOOS)),
		Source:  SourceDownload,
	}
	if !isFile(ff.FFmpeg) || !isFile(ff.FFprobe) {
		return FFmpeg{}, false
	}
	if m, err := readManifest(dir); err == nil {
		ff.Version = m.Version
	}
	return ff, true
}

// EnsureFFmpeg makes a working FFmpeg available and returns it: the studio's
// installed copy when it checks out, else a fresh installation (Homebrew on
// a Mac that has it, the pinned official build otherwise). Started by the
// user; progress may be nil; cancelling ctx stops at once and a partial
// download resumes next time. Errors are Ukrainian and say what to do.
func EnsureFFmpeg(ctx context.Context, progress Progress) (FFmpeg, error) {
	return InstallFFmpeg(ctx, Options{}, progress)
}

// InstallFFmpeg is EnsureFFmpeg with options (Force = always install again).
func InstallFFmpeg(ctx context.Context, opt Options, progress Progress) (FFmpeg, error) {
	unlock, err := Lock("ffmpeg")
	if err != nil {
		return FFmpeg{}, err
	}
	defer unlock()
	rep := newReporter(progress)
	dir := opt.Dir
	if strings.TrimSpace(dir) == "" {
		dir = FFmpegDir()
	}
	dir = absPath(dir)
	rep.force(0, "Перевіряємо FFmpeg…")
	if !opt.Force {
		if ff, ok := checkInstalled(ctx, dir); ok {
			rep.force(100, "FFmpeg уже встановлено й працює")
			return ff, nil
		}
	}
	if ctx.Err() != nil {
		return FFmpeg{}, ctx.Err()
	}
	if hostGOOS == "darwin" && !opt.NoHomebrew {
		if brew := findBrew(); brew != "" {
			ff, err := brewInstall(ctx, brew, rep)
			if err == nil {
				return ff, nil
			}
			if ctx.Err() != nil {
				return FFmpeg{}, ctx.Err()
			}
			log.Printf("[toolsinst] brew install ffmpeg: %v — downloading the static build", err)
			if _, ok := ffmpegBuilds[hostGOOS+"/"+hostGOARCH]; !ok {
				return FFmpeg{}, fmt.Errorf("Homebrew не зміг встановити FFmpeg: %v", err)
			}
			rep.force(0, "Homebrew не впорався — завантажуємо готову збірку…")
		}
	}
	b, ok := ffmpegBuilds[hostGOOS+"/"+hostGOARCH]
	if !ok {
		return FFmpeg{}, errNoBuild(hostGOOS, hostGOARCH)
	}
	return installBuild(ctx, dir, b, rep)
}

// errNoBuild: there is nothing to download for this system.
func errNoBuild(goos, goarch string) error {
	switch goos {
	case "linux":
		return errors.New("На Linux FFmpeg ставить менеджер пакетів: sudo apt install ffmpeg (Ubuntu, Debian), sudo dnf install ffmpeg (Fedora) або sudo pacman -S ffmpeg (Arch). Потім натисни «Перевірити»")
	case "darwin":
		return errors.New("Для цього Mac готової збірки немає — встанови Homebrew (brew.sh) і виконай у Терміналі brew install ffmpeg")
	}
	return fmt.Errorf("Для цієї системи (%s/%s) готової збірки FFmpeg немає — встанови його вручну: кнопка «Встановити вручну» підкаже як", goos, goarch)
}

// ---- the pinned build ------------------------------------------------------------------------

func installBuild(ctx context.Context, dir string, b ffmpegBuild, rep *reporter) (FFmpeg, error) {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return FFmpeg{}, writeErr(err)
	}
	cleanOld(dir, ".ffmpeg-unpack-")
	dlRoot := filepath.Join(parent, ".download")
	dl := filepath.Join(dlRoot, "ffmpeg-"+safeName(b.Version))
	cleanDownloads(dlRoot, "ffmpeg-", filepath.Base(dl))
	var total, base int64
	for _, a := range b.Archives {
		total += a.Size
	}
	var archives []string
	for _, a := range b.Archives {
		dst := filepath.Join(dl, a.FileName())
		err := Download(ctx, a, dst, func(done, _ int64) {
			rep.report(span(base+done, total, 0, 90), fmt.Sprintf("Завантажуємо FFmpeg %s · %s з %s", b.Version, FormatSize(base+done), FormatSize(total)))
		})
		if err != nil {
			return FFmpeg{}, err
		}
		base += a.Size
		archives = append(archives, dst)
	}
	rep.force(91, "Розпаковуємо FFmpeg…")
	staging, err := os.MkdirTemp(parent, ".ffmpeg-unpack-")
	if err != nil {
		return FFmpeg{}, writeErr(err)
	}
	defer os.RemoveAll(staging) // gone already after a successful swap
	for _, arc := range archives {
		if err := ExtractZip(arc, staging, pickFFmpeg(hostGOOS)); err != nil {
			return FFmpeg{}, err
		}
		if ctx.Err() != nil {
			return FFmpeg{}, ctx.Err()
		}
	}
	bins := []string{filepath.Join("bin", exe("ffmpeg", hostGOOS)), filepath.Join("bin", exe("ffprobe", hostGOOS))}
	for _, rel := range bins {
		p := filepath.Join(staging, rel)
		if !isFile(p) {
			return FFmpeg{}, fmt.Errorf("В архіві FFmpeg немає %s — спробуй пізніше або встанови FFmpeg вручну", filepath.Base(rel))
		}
		if hostGOOS != "windows" {
			_ = os.Chmod(p, 0o755)
		}
	}
	Unquarantine(staging)
	rep.force(95, "Перевіряємо FFmpeg…")
	var version string
	for i, rel := range bins {
		v, err := probeVersion(ctx, filepath.Join(staging, rel))
		if ctx.Err() != nil {
			return FFmpeg{}, ctx.Err()
		}
		if err != nil {
			return FFmpeg{}, fmt.Errorf("Завантажений FFmpeg не запускається (%v). Можливо, його заблокував антивірус — спробуй ще раз або встанови FFmpeg вручну", err)
		}
		if i == 0 {
			version = v
		}
	}
	m := manifest{Tool: "ffmpeg", Version: b.Version, Installed: time.Now().UTC(), Files: map[string]fileSum{}}
	for _, a := range b.Archives {
		m.Sources = append(m.Sources, a.URL)
	}
	if err := filepath.WalkDir(staging, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(staging, p)
		sum, err := FileSHA256(p)
		if err != nil {
			return err
		}
		m.Files[filepath.ToSlash(rel)] = fileSum{Size: fileSize(p), SHA256: sum}
		return nil
	}); err != nil {
		return FFmpeg{}, writeErr(err)
	}
	if err := writeManifest(staging, m); err != nil {
		return FFmpeg{}, err
	}
	if err := ReplaceDir(staging, dir); err != nil {
		if errors.Is(err, ErrInUse) {
			return FFmpeg{}, errors.New("FFmpeg зараз працює (монтаж, мініатюри чи програвання) — дочекайся кінця й натисни ще раз")
		}
		return FFmpeg{}, err
	}
	_ = os.RemoveAll(dl)
	_ = os.Remove(dlRoot) // only when empty
	rep.force(100, "Готово: FFmpeg "+b.Version+" встановлено")
	return FFmpeg{
		FFmpeg:  filepath.Join(dir, bins[0]),
		FFprobe: filepath.Join(dir, bins[1]),
		Version: version,
		Source:  SourceDownload,
	}, nil
}

// pickFFmpeg keeps ffmpeg and ffprobe (→ bin/) and the licence/readme of
// the archive's top folder; ffplay, docs and presets stay out.
func pickFFmpeg(goos string) func(string) string {
	same := func(a, b string) bool {
		if goos == "windows" {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	return func(name string) string {
		base := path.Base(name)
		depth := strings.Count(name, "/")
		switch {
		case same(base, exe("ffmpeg", goos)) || same(base, exe("ffprobe", goos)):
			if goos == "windows" {
				base = strings.ToLower(base)
			}
			return "bin/" + base
		case depth <= 1 && (strings.EqualFold(base, "LICENSE") || strings.EqualFold(base, "LICENSE.txt")):
			return "LICENSE.txt"
		case depth <= 1 && strings.EqualFold(base, "README.txt"):
			return "README.txt"
		}
		return ""
	}
}

// cleanDownloads removes "<prefix>*" download folders other than keep (an
// older version's leftovers).
func cleanDownloads(root, prefix, keep string) {
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) && e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// ---- the installed copy ----------------------------------------------------------------------

type manifest struct {
	Tool      string             `json:"tool"`
	Version   string             `json:"version"`
	Sources   []string           `json:"sources"`
	Files     map[string]fileSum `json:"files"` // slash path inside the folder → size + digest
	Installed time.Time          `json:"installed"`
}

type fileSum struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func readManifest(dir string) (manifest, error) {
	var m manifest
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func writeManifest(dir string, m manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), b, 0o644); err != nil {
		return writeErr(err)
	}
	return nil
}

// checkInstalled: the copy in dir is complete, unchanged since it was
// installed (sizes and SHA-256 of install.json) and starts.
func checkInstalled(ctx context.Context, dir string) (FFmpeg, bool) {
	ff, ok := installedIn(dir)
	if !ok {
		return FFmpeg{}, false
	}
	m, err := readManifest(dir)
	if err != nil || len(m.Files) == 0 {
		return FFmpeg{}, false
	}
	for rel, want := range m.Files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if !filepath.IsLocal(filepath.FromSlash(rel)) || fileSize(p) != want.Size {
			return FFmpeg{}, false
		}
		if sum, err := FileSHA256(p); err != nil || sum != want.SHA256 {
			return FFmpeg{}, false
		}
		if ctx.Err() != nil {
			return FFmpeg{}, false
		}
	}
	for i, bin := range []string{ff.FFmpeg, ff.FFprobe} {
		v, err := probeVersion(ctx, bin)
		if err != nil {
			return FFmpeg{}, false
		}
		if i == 0 {
			ff.Version = v
		}
	}
	return ff, true
}

// ---- Homebrew --------------------------------------------------------------------------------

func defaultFindBrew() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if isFile(p) {
			return p
		}
	}
	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	return ""
}

// brewInstall runs `brew install ffmpeg` (a no-op when it is installed
// already), reporting its output as progress, and returns brew's FFmpeg.
func brewInstall(ctx context.Context, brew string, rep *reporter) (FFmpeg, error) {
	rep.force(2, "Встановлюємо FFmpeg через Homebrew — це може тривати кілька хвилин…")
	cmd := sysx.Command(ctx, brew, "install", "ffmpeg")
	binDir := filepath.Dir(brew)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_COLOR=1", "HOMEBREW_NO_EMOJI=1", "NONINTERACTIVE=1")
	cmd.WaitDelay = 5 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	bp := &brewProgress{}
	var tail []string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanLines(pr, func(line string) {
			line = strings.TrimSpace(line)
			if line == "" {
				return
			}
			bp.line(line)
			tail = append(tail, line)
			if len(tail) > 6 {
				tail = tail[1:]
			}
			rep.report(bp.pct(), "Homebrew: "+clip(strings.TrimPrefix(line, "==> "), 90))
		})
	}()
	err := sysx.Run(cmd)
	pw.Close()
	wg.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return FFmpeg{}, ctx.Err()
		}
		return FFmpeg{}, fmt.Errorf("brew install ffmpeg: %v (%s)", err, clip(strings.Join(tail, " · "), 300))
	}
	ff := FFmpeg{FFmpeg: filepath.Join(binDir, "ffmpeg"), FFprobe: filepath.Join(binDir, "ffprobe"), Source: SourceHomebrew}
	rep.force(97, "Перевіряємо FFmpeg…")
	v, err := probeVersion(ctx, ff.FFmpeg)
	if err != nil {
		return FFmpeg{}, err
	}
	if _, err := probeVersion(ctx, ff.FFprobe); err != nil {
		return FFmpeg{}, err
	}
	ff.Version = v
	rep.force(100, "Готово: FFmpeg встановлено через Homebrew")
	return ff, nil
}

// scanLines splits r on \n and \r (brew redraws its download bars with \r).
func scanLines(r io.Reader, fn func(string)) {
	buf := make([]byte, 32<<10)
	var line []byte
	for {
		n, err := r.Read(buf)
		for _, c := range buf[:n] {
			if c == '\n' || c == '\r' {
				fn(string(line))
				line = line[:0]
				continue
			}
			if len(line) < 4096 {
				line = append(line, c)
			}
		}
		if err != nil {
			if len(line) > 0 {
				fn(string(line))
			}
			return
		}
	}
}

// brewProgress estimates how far `brew install ffmpeg` is from its output:
// with the dependency list known, by bottles fetched and poured; otherwise a
// slow creep towards 90 %.
type brewProgress struct {
	deps           int
	fetched, pours int
	lines          int
	best           int // the percent never moves back
}

func (b *brewProgress) line(s string) {
	b.lines++
	switch {
	case strings.HasPrefix(s, "==> Fetching dependencies for ffmpeg:"), strings.HasPrefix(s, "==> Installing dependencies for ffmpeg:"):
		if _, list, ok := strings.Cut(s, ":"); ok && b.deps == 0 {
			b.deps = len(strings.Split(strings.TrimSpace(list), ","))
		}
	case strings.HasPrefix(s, "==> Pouring "):
		b.pours++
	case strings.HasPrefix(s, "==> Fetching "), strings.HasPrefix(s, "✔︎ Bottle "), strings.HasPrefix(s, "✔ Bottle "):
		b.fetched++
	}
}

func (b *brewProgress) pct() int {
	p := 3 + int(87*(1-math.Exp(-float64(b.lines)/60)))
	if b.deps > 0 {
		n := float64(b.deps + 1)
		f := math.Min(1, float64(b.fetched)/n)
		q := math.Min(1, float64(b.pours)/n)
		p = 3 + int(42*f+50*q)
	}
	b.best = max(b.best, p)
	return b.best
}

// ---- progress --------------------------------------------------------------------------------

// reporter forwards progress at most ~10 times a second; report never moves
// the percent back, force sets it as it is.
type reporter struct {
	fn   Progress
	mu   sync.Mutex
	pct  int
	last time.Time
}

func newReporter(fn Progress) *reporter { return &reporter{fn: fn} }

func (r *reporter) report(pct int, msg string) {
	if r.fn == nil {
		return
	}
	r.mu.Lock()
	pct = max(r.pct, min(100, pct))
	if time.Since(r.last) < 100*time.Millisecond && pct < 100 {
		r.pct = pct
		r.mu.Unlock()
		return
	}
	r.pct, r.last = pct, time.Now()
	r.mu.Unlock()
	r.fn(pct, msg)
}

func (r *reporter) force(pct int, msg string) {
	if r.fn == nil {
		return
	}
	r.mu.Lock()
	r.pct, r.last = max(0, min(100, pct)), time.Now()
	pct = r.pct
	r.mu.Unlock()
	r.fn(pct, msg)
}

// span maps done/total onto [from, to] percent.
func span(done, total int64, from, to int) int {
	if total <= 0 {
		return from
	}
	return from + int(min(1, float64(done)/float64(total))*float64(to-from))
}
