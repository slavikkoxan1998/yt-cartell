package toolsinst

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
)

// Every test works in temp folders against httptest servers: nothing is
// downloaded from the internet and the user's folders are never touched
// (TestMain points the settings and app-data folders at a temp dir too).

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "toolsinst-home-*")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"LOCALAPPDATA", "APPDATA", "HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		os.Setenv(k, tmp)
	}
	retryPause, renamePause = time.Millisecond, time.Millisecond
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// blobServer serves files by path with Range support and counts the requests.
type blobServer struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
	block chan struct{} // non-nil: "/slow" sends half and waits for it
}

func newBlobServer(t *testing.T) *blobServer {
	s := &blobServer{files: map[string][]byte{}, hits: map[string]int{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		b, ok := s.files[r.URL.Path]
		s.hits[r.URL.Path]++
		block := s.block
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/slow" && block != nil {
			w.Header().Set("Content-Length", fmt.Sprint(len(b)))
			w.WriteHeader(http.StatusOK)
			w.Write(b[:len(b)/2])
			w.(http.Flusher).Flush()
			<-block
			return
		}
		http.ServeContent(w, r, "f", time.Time{}, bytes.NewReader(b))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *blobServer) put(p string, b []byte) string {
	s.mu.Lock()
	s.files[p] = b
	s.mu.Unlock()
	return s.URL + p
}

func (s *blobServer) count(p string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[p]
}

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func TestDownloadResumesAndVerifies(t *testing.T) {
	srv := newBlobServer(t)
	data := payload(300 << 10)
	u := srv.put("/a.zip", data)
	dst := filepath.Join(t.TempDir(), "dl", "a.zip")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	// Half of it from an earlier run.
	if err := os.WriteFile(dst+".part", data[:100<<10], 0o644); err != nil {
		t.Fatal(err)
	}
	var last int64
	err := Download(context.Background(), Asset{URL: u, Size: int64(len(data)), SHA256: sum(data)}, dst, func(done, total int64) {
		if total != int64(len(data)) || done < last {
			t.Errorf("progress %d/%d after %d", done, total, last)
		}
		last = done
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, data) || last != int64(len(data)) {
		t.Fatalf("file differs (%d bytes, progress %d)", len(got), last)
	}
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Fatal("the part is left")
	}
	// Complete already: no request.
	n := srv.count("/a.zip")
	if err := Download(context.Background(), Asset{URL: u, Size: int64(len(data)), SHA256: sum(data)}, dst, nil); err != nil || srv.count("/a.zip") != n {
		t.Fatalf("a complete file was fetched again: %v", err)
	}
}

func TestDownloadMirrorAndChecksum(t *testing.T) {
	srv := newBlobServer(t)
	data := payload(64 << 10)
	mirror := srv.put("/mirror/a.zip", data)
	dir := t.TempDir()
	// The official URL is gone (404) → the mirror serves the same file.
	a := Asset{URL: srv.URL + "/gone.zip", Mirrors: []string{mirror}, Size: int64(len(data)), SHA256: sum(data)}
	if err := Download(context.Background(), a, filepath.Join(dir, "a.zip"), nil); err != nil {
		t.Fatalf("mirror: %v", err)
	}
	// Wrong bytes → refused, nothing left behind.
	bad := srv.put("/bad.zip", payload(64 << 10)[1:])
	err := Download(context.Background(), Asset{URL: bad, SHA256: sum(data)}, filepath.Join(dir, "b.zip"), nil)
	if !errors.Is(err, errChecksum) {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.zip.part")); !os.IsNotExist(err) {
		t.Fatal("a corrupt part is left")
	}
	if _, err := os.Stat(filepath.Join(dir, "b.zip")); !os.IsNotExist(err) {
		t.Fatal("a corrupt file was installed")
	}
	// No checksum → nothing is downloaded.
	if err := Download(context.Background(), Asset{URL: mirror}, filepath.Join(dir, "c.zip"), nil); err == nil || !strings.Contains(err.Error(), "контрольної суми") {
		t.Fatalf("no checksum: %v", err)
	}
	// Everything missing → a friendly message naming the host.
	err = Download(context.Background(), Asset{URL: srv.URL + "/none", SHA256: sum(data)}, filepath.Join(dir, "d.zip"), nil)
	if err == nil || !strings.Contains(err.Error(), "Не вдалося завантажити з 127.0.0.1") {
		t.Fatalf("404: %v", err)
	}
}

func TestDownloadCancelKeepsPart(t *testing.T) {
	srv := newBlobServer(t)
	data := payload(128 << 10)
	srv.block = make(chan struct{})
	u := srv.put("/slow", data)
	dst := filepath.Join(t.TempDir(), "slow.zip")
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	errc := make(chan error, 1)
	go func() {
		errc <- Download(ctx, Asset{URL: u, Size: int64(len(data)), SHA256: sum(data)}, dst, func(done, _ int64) {
			if done >= int64(len(data)/2) {
				once.Do(cancel)
			}
		})
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop the download")
	}
	close(srv.block)
	if fileSize(dst+".part") != int64(len(data)/2) {
		t.Fatalf("part = %d bytes, want %d", fileSize(dst+".part"), len(data)/2)
	}
	// The next run resumes.
	srv.block = nil
	if err := Download(context.Background(), Asset{URL: u, Size: int64(len(data)), SHA256: sum(data)}, dst, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, data) {
		t.Fatal("resumed file differs")
	}
}

// zipOf builds a zip; names ending in "@" become symlinks.
func zipOf(t *testing.T, files map[string][]byte, mode os.FileMode) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		h := &zip.FileHeader{Name: strings.TrimSuffix(name, "@"), Method: zip.Deflate}
		h.SetMode(mode)
		if strings.HasSuffix(name, "@") {
			h.SetMode(os.ModeSymlink | 0o777)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZipSafety(t *testing.T) {
	dir := t.TempDir()
	arc := filepath.Join(dir, "a.zip")
	os.WriteFile(arc, zipOf(t, map[string][]byte{"top/bin/tool": []byte("x"), "top/link@": []byte("/etc/passwd")}, 0o755), 0o644)
	out := filepath.Join(dir, "out")
	if err := ExtractZip(arc, out, func(n string) string {
		if strings.HasSuffix(n, "/tool") {
			return "bin/renamed"
		}
		return n
	}); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(out, "bin", "renamed")) {
		t.Fatal("picked file missing")
	}
	if _, err := os.Lstat(filepath.Join(out, "top", "link")); !os.IsNotExist(err) {
		t.Fatal("a link came out of the archive")
	}
	os.WriteFile(arc, zipOf(t, map[string][]byte{"../evil": []byte("x")}, 0o644), 0o644)
	if err := ExtractZip(arc, filepath.Join(dir, "out2"), nil); err == nil || !strings.Contains(err.Error(), "небезпечний шлях") {
		t.Fatalf("path escape: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); !os.IsNotExist(err) {
		t.Fatal("escaped")
	}
	os.WriteFile(arc, zipOf(t, map[string][]byte{"a": []byte("x")}, 0o644), 0o644)
	if err := ExtractZip(arc, filepath.Join(dir, "out3"), func(string) string { return "../x" }); err == nil {
		t.Fatal("pick may not leave dst")
	}
}

// fakeFF sets up a mock FFmpeg build for goos/goarch and a fake version check.
type fakeFF struct {
	srv    *blobServer
	probes atomic.Int32
	fail   atomic.Bool
}

func setupFFmpeg(t *testing.T, goos, goarch string, archives map[string][]byte) *fakeFF {
	t.Helper()
	f := &fakeFF{srv: newBlobServer(t)}
	var b ffmpegBuild
	b.Version = "9.9.9"
	names := make([]string, 0, len(archives))
	for n := range archives {
		names = append(names, n)
	}
	for _, n := range names {
		data := archives[n]
		b.Archives = append(b.Archives, Asset{URL: f.srv.put("/"+n, data), Size: int64(len(data)), SHA256: sum(data), Name: n})
	}
	oldBuilds, oldOS, oldArch, oldProbe, oldBrew := ffmpegBuilds, hostGOOS, hostGOARCH, probeVersion, findBrew
	ffmpegBuilds = map[string]ffmpegBuild{goos + "/" + goarch: b}
	hostGOOS, hostGOARCH = goos, goarch
	findBrew = func() string { return "" }
	probeVersion = func(_ context.Context, bin string, _ ...string) (string, error) {
		f.probes.Add(1)
		if f.fail.Load() {
			return "", errors.New("exit status 0xc0000135")
		}
		if !isFile(bin) {
			return "", errors.New("missing")
		}
		return filepath.Base(bin) + " version 9.9.9-test", nil
	}
	t.Cleanup(func() {
		ffmpegBuilds, hostGOOS, hostGOARCH, probeVersion, findBrew = oldBuilds, oldOS, oldArch, oldProbe, oldBrew
	})
	return f
}

func TestInstallFFmpegWindows(t *testing.T) {
	arc := zipOf(t, map[string][]byte{
		"ffmpeg-9.9.9-essentials_build/bin/ffmpeg.exe":  []byte("FFMPEG"),
		"ffmpeg-9.9.9-essentials_build/bin/ffprobe.exe": []byte("FFPROBE"),
		"ffmpeg-9.9.9-essentials_build/bin/ffplay.exe":  []byte("FFPLAY"),
		"ffmpeg-9.9.9-essentials_build/doc/ffmpeg.html": []byte("doc"),
		"ffmpeg-9.9.9-essentials_build/LICENSE":         []byte("GPL"),
		"ffmpeg-9.9.9-essentials_build/README.txt":      []byte("readme"),
	}, 0o644)
	f := setupFFmpeg(t, "windows", "amd64", map[string][]byte{"ffmpeg-9.9.9-essentials_build.zip": arc})
	root := t.TempDir()
	dir := filepath.Join(root, "ffmpeg")
	var pcts []int
	var mu sync.Mutex
	prog := func(p int, msg string) {
		mu.Lock()
		pcts = append(pcts, p)
		mu.Unlock()
		if msg == "" {
			t.Error("empty progress message")
		}
	}
	ff, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, prog)
	if err != nil {
		t.Fatal(err)
	}
	if ff.FFmpeg != filepath.Join(dir, "bin", "ffmpeg.exe") || ff.FFprobe != filepath.Join(dir, "bin", "ffprobe.exe") ||
		ff.Source != SourceDownload || !strings.HasPrefix(ff.Version, "ffmpeg.exe version 9.9.9") {
		t.Fatalf("result %+v", ff)
	}
	for _, p := range []string{"bin/ffmpeg.exe", "bin/ffprobe.exe", "LICENSE.txt", "README.txt", manifestFile} {
		if !isFile(filepath.Join(dir, filepath.FromSlash(p))) {
			t.Errorf("%s missing", p)
		}
	}
	for _, p := range []string{"bin/ffplay.exe", "doc"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Errorf("%s should not be installed", p)
		}
	}
	if len(pcts) == 0 || pcts[len(pcts)-1] != 100 {
		t.Fatalf("progress %v", pcts)
	}
	ents, _ := os.ReadDir(root)
	if len(ents) != 1 {
		t.Fatalf("leftovers next to the install: %v", ents)
	}
	m, err := readManifest(dir)
	if err != nil || m.Version != "9.9.9" || m.Files["bin/ffmpeg.exe"].SHA256 != sum([]byte("FFMPEG")) {
		t.Fatalf("manifest %+v %v", m, err)
	}

	// Again: the installed copy checks out — nothing is downloaded.
	hits := f.srv.count("/ffmpeg-9.9.9-essentials_build.zip")
	if _, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, nil); err != nil || f.srv.count("/ffmpeg-9.9.9-essentials_build.zip") != hits {
		t.Fatalf("idempotent run downloaded again: %v", err)
	}
	// A damaged file → repaired.
	os.WriteFile(ff.FFprobe, []byte("broken"), 0o644)
	if _, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, nil); err != nil || f.srv.count("/ffmpeg-9.9.9-essentials_build.zip") != hits+1 {
		t.Fatalf("repair: %v", err)
	}
	if b, _ := os.ReadFile(ff.FFprobe); string(b) != "FFPROBE" {
		t.Fatal("not repaired")
	}
	if got, ok := installedIn(dir); !ok || got.Version != "9.9.9" {
		t.Fatalf("installedIn %+v", got)
	}
}

func TestInstallFFmpegMac(t *testing.T) {
	setupFFmpeg(t, "darwin", "arm64", map[string][]byte{
		"ffmpeg-9.9.9-arm64.zip":  zipOf(t, map[string][]byte{"ffmpeg": []byte("FF")}, 0o755),
		"ffprobe-9.9.9-arm64.zip": zipOf(t, map[string][]byte{"ffprobe": []byte("FP")}, 0o755),
	})
	dir := filepath.Join(t.TempDir(), "ffmpeg")
	ff, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ff.FFmpeg != filepath.Join(dir, "bin", "ffmpeg") || ff.FFprobe != filepath.Join(dir, "bin", "ffprobe") {
		t.Fatalf("result %+v", ff)
	}
	if o := FFmpegOffer(); !o.OK || o.Homebrew || o.Version != "9.9.9" || o.Bytes <= 0 {
		t.Fatalf("offer %+v", o)
	}
}

func TestInstallFFmpegFailures(t *testing.T) {
	arc := zipOf(t, map[string][]byte{"x/bin/ffmpeg.exe": []byte("FF"), "x/bin/ffprobe.exe": []byte("FP")}, 0o644)
	f := setupFFmpeg(t, "windows", "amd64", map[string][]byte{"a.zip": arc})
	dir := filepath.Join(t.TempDir(), "ffmpeg")

	// A program that does not start is never installed.
	f.fail.Store(true)
	if _, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, nil); err == nil || !strings.Contains(err.Error(), "антивірус") {
		t.Fatalf("broken program: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("a broken FFmpeg was installed")
	}
	f.fail.Store(false)

	// One installation at a time.
	unlock, err := Lock("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second install: %v", err)
	}
	unlock()

	// Cancelled before it starts.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InstallFFmpeg(ctx, Options{Dir: dir}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}

	// An archive without ffprobe.
	f2 := setupFFmpeg(t, "windows", "amd64", map[string][]byte{"b.zip": zipOf(t, map[string][]byte{"x/bin/ffmpeg.exe": []byte("FF")}, 0o644)})
	_ = f2
	if _, err := InstallFFmpeg(context.Background(), Options{Dir: dir, Force: true}, nil); err == nil || !strings.Contains(err.Error(), "ffprobe") {
		t.Fatalf("no ffprobe: %v", err)
	}

	// Linux: nothing to download, the package manager is named.
	hostGOOS, hostGOARCH = "linux", "amd64"
	if _, err := InstallFFmpeg(context.Background(), Options{Dir: dir}, nil); err == nil || !strings.Contains(err.Error(), "apt install ffmpeg") {
		t.Fatalf("linux: %v", err)
	}
	if o := FFmpegOffer(); o.OK || !strings.Contains(o.Note, "apt") {
		t.Fatalf("linux offer %+v", o)
	}
}

func TestBrewProgress(t *testing.T) {
	var b brewProgress
	last := b.pct()
	lines := []string{
		"==> Auto-updating Homebrew...",
		"==> Fetching dependencies for ffmpeg: aom, dav1d, lame",
		"==> Fetching aom", "==> Fetching dav1d", "==> Fetching lame", "==> Fetching ffmpeg",
		"==> Pouring aom--3.12.arm64_sequoia.bottle.tar.gz", "==> Pouring dav1d--1.5.arm64_sequoia.bottle.tar.gz",
		"==> Pouring lame--3.100.arm64_sequoia.bottle.tar.gz", "==> Pouring ffmpeg--9.0.2.arm64_sequoia.bottle.tar.gz",
	}
	for _, l := range lines {
		b.line(l)
		if p := b.pct(); p < last || p > 95 {
			t.Fatalf("%q → %d after %d", l, p, last)
		} else {
			last = p
		}
	}
	if b.deps != 3 || last != 95 {
		t.Fatalf("deps %d, pct %d", b.deps, last)
	}
	var u brewProgress // no dependency list: a creep that never reaches 100
	for i := 0; i < 1000; i++ {
		u.line("something")
	}
	if p := u.pct(); p < 80 || p > 90 {
		t.Fatalf("creep %d", p)
	}
}

func TestParseChecksums(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("B", 64)
	m := ParseChecksums(strings.NewReader(a + "  ffmpeg.zip\n" + b + " *yt-dlp.exe\nnot a line\n" + a + "\n"))
	if m["ffmpeg.zip"] != a || m["yt-dlp.exe"] != strings.ToLower(b) || m[""] != a || len(m) != 3 {
		t.Fatalf("%v", m)
	}
	srv := newBlobServer(t)
	u := srv.put("/ffmpeg.zip.sha256", []byte(a+"  ffmpeg.zip\n"))
	got, err := FetchChecksums(context.Background(), u)
	if err != nil || got["ffmpeg.zip"] != a {
		t.Fatalf("fetch %v %v", got, err)
	}
}

func TestRootsAndReplaceDir(t *testing.T) {
	if FFmpegDir() != filepath.Join(config.ToolsDir(), "ffmpeg") || ToolDir("yt-dlp") != filepath.Join(Root(), "yt-dlp") {
		t.Fatal("folders")
	}
	tmp := t.TempDir()
	dst := filepath.Join(tmp, "tool")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "old"), []byte("1"), 0o644)
	src := filepath.Join(tmp, "new")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "new"), []byte("2"), 0o644)
	if err := ReplaceDir(src, dst); err != nil {
		t.Fatal(err)
	}
	if !isFile(filepath.Join(dst, "new")) || isFile(filepath.Join(dst, "old")) {
		t.Fatal("not swapped")
	}
	ents, _ := os.ReadDir(tmp)
	if len(ents) != 1 {
		t.Fatalf("leftovers %v", ents)
	}
	if FormatSize(114768076) != "109 МБ" || FormatSize(28395699) != "27,1 МБ" {
		t.Fatalf("sizes %s %s", FormatSize(114768076), FormatSize(28395699))
	}
}
