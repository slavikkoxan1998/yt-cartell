package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain refuses every connection that is not to this machine: the tests
// talk to httptest servers only, never to GitHub.
func TestMain(m *testing.M) {
	guard := http.DefaultTransport.(*http.Transport).Clone()
	dial := (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	guard.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("test guard: refusing %s (no real network in tests)", addr)
		}
		return dial(ctx, network, addr)
	}
	http.DefaultTransport = guard
	os.Exit(m.Run())
}

var (
	_ Updater = (*Client)(nil)
	_ Updater = (*Fake)(nil)
)

func TestVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2.0.0", "2.0.0", 0},
		{"v2.1.0", "2.0.9", 1},
		{"2.0", "2.0.0", 0},
		{"2.0.0-beta.1", "2.0.0", -1},
		{"2.0.0-beta.2", "2.0.0-beta.11", -1},
		{"2.0.0-alpha", "2.0.0-alpha.1", -1},
		{"2.0.0-1", "2.0.0-alpha", -1},
		{"10.0.0", "9.9.9", 1},
		{"2.0.0+build7", "2.0.0", 0},
	} {
		a, ok1 := ParseVersion(c.a)
		b, ok2 := ParseVersion(c.b)
		if !ok1 || !ok2 {
			t.Fatalf("parse %q / %q failed", c.a, c.b)
		}
		if got := a.Compare(b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"", "v", "latest", "1.2.3.4", "1..2", "x1.0"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("ParseVersion(%q) accepted", bad)
		}
	}
	if !Newer("2.1.0", "2.0.0") || Newer("2.0.0", "2.0.0") || Newer("nightly", "2.0.0") || !Newer("0.0.1", "dev") {
		t.Error("Newer")
	}
}

func TestReleaseNewer(t *testing.T) {
	r := Release{Tag: "v2.1.0", Version: "2.1.0"}
	if !r.Newer("2.0.0") || r.Newer("2.1.0") || r.Newer("3.0.0") {
		t.Error("plain release")
	}
	for _, x := range []Release{
		{Version: "2.1.0", Draft: true},
		{Version: "2.1.0", Prerelease: true},
		{Version: "2.1.0-rc.1"},
		{Version: "latest"},
	} {
		if x.Newer("2.0.0") {
			t.Errorf("%+v counted as newer", x)
		}
	}
}

func TestAssetFor(t *testing.T) {
	r := Release{Assets: []Asset{
		{Name: "YT-Cartell-Studio-2.1.0-macos-universal.zip"},
		{Name: "YT-Cartell-Studio-2.1.0-windows-x64.exe"},
		{Name: "YT-Cartell-Studio-2.1.0-macos-arm64.zip"},
		{Name: "SHA256SUMS.txt"},
		{Name: "other-tool-macos-x64.zip"},
	}}
	for _, c := range []struct{ os, arch, want string }{
		{"windows", "amd64", "YT-Cartell-Studio-2.1.0-windows-x64.exe"},
		{"windows", "arm64", "YT-Cartell-Studio-2.1.0-windows-x64.exe"},
		{"darwin", "arm64", "YT-Cartell-Studio-2.1.0-macos-arm64.zip"},
		{"darwin", "amd64", "YT-Cartell-Studio-2.1.0-macos-universal.zip"}, // never another tool's zip
		{"linux", "amd64", ""},
	} {
		a, ok := r.AssetFor(c.os, c.arch)
		if a.Name != c.want || ok != (c.want != "") {
			t.Errorf("%s/%s → %q, want %q", c.os, c.arch, a.Name, c.want)
		}
	}
	if s, ok := r.Checksums(); !ok || s.Name != SumsName {
		t.Error("Checksums")
	}
}

func TestUpdatedFrom(t *testing.T) {
	for _, args := range [][]string{
		{"app", "--updated-from=2.0.0"},
		{"app", "-updated-from=2.0.0"},
		{"app", "--updated-from", "2.0.0"},
	} {
		if v, ok := UpdatedFrom(args); !ok || v != "2.0.0" {
			t.Errorf("UpdatedFrom(%q) = %q, %v", args, v, ok)
		}
	}
	if _, ok := UpdatedFrom([]string{"app", "-psn_0_1"}); ok {
		t.Error("no flag")
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	sums := ParseSums([]byte("\xef\xbb\xbf" + strings.ToUpper(h) + "  YT-Cartell-Studio-2.0.0-windows-x64.exe\r\n" +
		h + " *dist/YT-Cartell-Studio-2.0.0-macos-arm64.zip\n# comment\nshort  x\n"))
	if sums["YT-Cartell-Studio-2.0.0-windows-x64.exe"] != h || sums["YT-Cartell-Studio-2.0.0-macos-arm64.zip"] != h || len(sums) != 2 {
		t.Fatalf("sums = %v", sums)
	}
}

func TestParseNotes(t *testing.T) {
	md := "## Що нового\r\n\r\n- **Оновлення** з [GitHub](https://github.com)\n  у два кліки\n* `Своя озвучка` швидша\n  - вкладений пункт\n1. Перший\n2) Другий\n- [x] Задача\n\n" +
		"Звичайний абзац,\nрозбитий на рядки. <!-- приховано -->\n\n---\n![img](a.png)\n> Цитата <b>жирна</b>\n\n```\ncode line\n```\n"
	got := ParseNotes(md)
	want := []Block{
		{Kind: Heading, Text: "Що нового"},
		{Kind: Bullet, Text: "Оновлення з GitHub у два кліки"},
		{Kind: Bullet, Text: "Своя озвучка швидша"},
		{Kind: Bullet, Text: "вкладений пункт", Level: 1},
		{Kind: Bullet, Text: "Перший", Marker: "1."},
		{Kind: Bullet, Text: "Другий", Marker: "2."},
		{Kind: Bullet, Text: "Задача"},
		{Kind: Para, Text: "Звичайний абзац, розбитий на рядки."},
		{Kind: Para, Text: "Цитата жирна"},
		{Kind: Para, Text: "code line"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// ---- GitHub API ------------------------------------------------------------------------------

const releaseJSON = `{"tag_name":"v2.1.0","name":"YT Cartell Studio v2.1.0 — тест","body":"- Нове","html_url":"https://github.com/o/r/releases/tag/v2.1.0",
"draft":false,"prerelease":false,"published_at":"2026-09-30T10:00:00Z","assets":[
{"name":"YT-Cartell-Studio-2.1.0-windows-x64.exe","browser_download_url":"https://example.invalid/a.exe","size":10,"state":"uploaded"},
{"name":"YT-Cartell-Studio-2.1.0-macos-arm64.zip","browser_download_url":"https://example.invalid/a.zip","size":10,"state":"starter"}]}`

func TestLatestETag(t *testing.T) {
	var hits, notModified atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/repos/o/r/releases/latest" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "YT-Cartell-Studio/") || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("headers: %v", r.Header)
		}
		if r.Header.Get("If-None-Match") == `W/"v1"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"v1"`)
		io.WriteString(w, releaseJSON)
	}))
	defer srv.Close()
	cache := filepath.Join(t.TempDir(), CacheName)
	c := &Client{API: srv.URL, Owner: "o", Repo: "r", CacheFile: cache}
	for i := 0; i < 2; i++ {
		rel, err := c.Latest(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rel.Version != "2.1.0" || rel.Title == "" || len(rel.Assets) != 1 || rel.Published.IsZero() || !rel.Newer("2.0.0") {
			t.Fatalf("release = %+v", rel)
		}
	}
	if notModified.Load() != 1 {
		t.Fatalf("304s = %d, want 1", notModified.Load())
	}
	// A new client (the next start) reuses the cache file.
	c2 := &Client{API: srv.URL, Owner: "o", Repo: "r", CacheFile: cache}
	if rel, err := c2.Latest(context.Background()); err != nil || rel.Tag != "v2.1.0" {
		t.Fatalf("cached: %+v %v", rel, err)
	}
	if notModified.Load() != 2 {
		t.Fatalf("the cache file was not used (304s = %d)", notModified.Load())
	}
	// Another repository never uses that cache.
	c3 := &Client{API: srv.URL, Owner: "o", Repo: "other", CacheFile: cache}
	if _, err := c3.Latest(context.Background()); KindOf(err) != KindNoRelease {
		t.Fatalf("other repo: %v", err)
	}
}

func TestLatestErrors(t *testing.T) {
	reset := time.Now().Add(20 * time.Minute).Unix()
	var mode atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 0:
			http.NotFound(w, r)
		case 1:
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset))
			w.WriteHeader(http.StatusForbidden)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		case 3:
			io.WriteString(w, "<html>not json</html>")
		case 4:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	c := &Client{API: srv.URL, Owner: "o", Repo: "r"}
	check := func(m int32, want Kind) *Error {
		t.Helper()
		mode.Store(m)
		_, err := c.Latest(context.Background())
		var e *Error
		if !errors.As(err, &e) || e.Kind != want || e.Msg == "" {
			t.Fatalf("mode %d: %v (kind %d), want kind %d", m, err, KindOf(err), want)
		}
		return e
	}
	check(0, KindNoRelease)
	if e := check(1, KindRateLimited); e.Reset.Unix() != reset || !strings.Contains(e.Msg, time.Unix(reset, 0).Format("15:04")) {
		t.Errorf("rate limit: %+v", e)
	}
	check(2, KindServer)
	check(3, KindServer)
	check(4, KindRateLimited)
	srv.Close()
	check(0, KindOffline)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Latest(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

// ---- downloads -------------------------------------------------------------------------------

// assetServer serves one file (Range-capable) and its SHA256SUMS.txt.
type assetServer struct {
	*httptest.Server
	data    []byte
	sums    string
	hits    atomic.Int32
	ranges  atomic.Int32
	stall   chan struct{} // closes the body after half when set
	unblock chan struct{}
}

func newAssetServer(t *testing.T, name string, data []byte) *assetServer {
	sum := sha256.Sum256(data)
	s := &assetServer{data: data, sums: hex.EncodeToString(sum[:]) + "  " + name + "\n"}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + SumsName:
			io.WriteString(w, s.sums)
		case "/" + name:
			s.hits.Add(1)
			if r.Header.Get("Range") != "" {
				s.ranges.Add(1)
			}
			if s.stall != nil {
				w.Header().Set("Content-Length", fmt.Sprint(len(data)))
				w.Write(data[:len(data)/2])
				w.(http.Flusher).Flush()
				close(s.stall)
				<-s.unblock
				return
			}
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *assetServer) release(name string) (Release, Asset) {
	a := Asset{Name: name, URL: s.URL + "/" + name, Size: int64(len(s.data))}
	return Release{Tag: "v2.1.0", Version: "2.1.0", Assets: []Asset{a, {Name: SumsName, URL: s.URL + "/" + SumsName}}}, a
}

const testAsset = "YT-Cartell-Studio-2.1.0-windows-x64.exe"

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestDownloadVerifiesAndReuses(t *testing.T) {
	data := payload(300 << 10)
	s := newAssetServer(t, testAsset, data)
	rel, a := s.release(testAsset)
	c := &Client{Dir: t.TempDir()}
	var last, total int64
	path, err := c.download(context.Background(), rel, a, func(d, tt int64) { last, total = d, tt })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) || last != int64(len(data)) || total != int64(len(data)) {
		t.Fatalf("file %d bytes, progress %d/%d", len(got), last, total)
	}
	if _, err := c.download(context.Background(), rel, a, nil); err != nil || s.hits.Load() != 1 {
		t.Fatalf("second download: %v, hits %d", err, s.hits.Load())
	}
}

func TestDownloadResumes(t *testing.T) {
	data := payload(200 << 10)
	s := newAssetServer(t, testAsset, data)
	rel, a := s.release(testAsset)
	dir := t.TempDir()
	c := &Client{Dir: dir}
	sum := sha256.Sum256(data)
	part := filepath.Join(dir, testAsset) + "." + hex.EncodeToString(sum[:])[:12] + ".part"
	if err := os.WriteFile(part, data[:77777], 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, testAsset) + ".0123456789ab.part"
	_ = os.WriteFile(stale, []byte("old"), 0o644)
	path, err := c.download(context.Background(), rel, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
		t.Fatal("resumed file differs")
	}
	if s.ranges.Load() != 1 {
		t.Fatalf("range requests = %d", s.ranges.Load())
	}
	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a stale part of another checksum stayed")
	}
}

func TestDownloadRefusesBadChecksums(t *testing.T) {
	data := payload(50 << 10)
	s := newAssetServer(t, testAsset, data)
	rel, a := s.release(testAsset)
	dir := t.TempDir()
	c := &Client{Dir: dir}

	s.sums = strings.Repeat("0", 64) + "  " + testAsset + "\n"
	if _, err := c.download(context.Background(), rel, a, nil); KindOf(err) != KindChecksum {
		t.Fatalf("mismatch: %v", err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("files left after a mismatch: %v", left)
	}
	s.sums = strings.Repeat("0", 64) + "  another-file.exe\n"
	if _, err := c.download(context.Background(), rel, a, nil); KindOf(err) != KindChecksum {
		t.Fatalf("missing line: %v", err)
	}
	hits := s.hits.Load()
	rel.Assets = rel.Assets[:1] // no SHA256SUMS.txt at all
	if _, err := c.download(context.Background(), rel, a, nil); KindOf(err) != KindChecksum {
		t.Fatalf("no sums: %v", err)
	}
	if s.hits.Load() != hits {
		t.Fatal("downloaded a build it could not verify")
	}
}

func TestDownloadCancelKeepsPart(t *testing.T) {
	data := payload(256 << 10)
	s := newAssetServer(t, testAsset, data)
	s.stall, s.unblock = make(chan struct{}), make(chan struct{})
	defer close(s.unblock)
	rel, a := s.release(testAsset)
	dir := t.TempDir()
	c := &Client{Dir: dir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The user presses «Скасувати» once the first bytes are on disk.
	var once sync.Once
	_, err := c.download(ctx, rel, a, func(done, _ int64) {
		if done > 0 {
			once.Do(cancel)
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	parts, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	if len(parts) != 1 {
		t.Fatalf("parts = %v", parts)
	}
	if st, err := os.Stat(parts[0]); err != nil || st.Size() == 0 || st.Size() >= int64(len(data)) {
		t.Fatalf("part: %v %v", st, err)
	}
}

func TestThrottle(t *testing.T) {
	var n int
	f := Throttle(time.Hour, func(int64, int64) { n++ })
	f(1, 10)
	f(2, 10)
	f(10, 10)
	if n != 2 {
		t.Fatalf("calls = %d, want 2 (first and final)", n)
	}
}

// ---- install -----------------------------------------------------------------------------------

func TestSwapFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "app.exe")
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	write(target, "v1")
	write(target+OldSuffix, "v0") // left over from an earlier update
	staged := filepath.Join(t.TempDir(), "new.exe")
	write(staged, "v2")
	if err := swapFile(staged, target); err != nil {
		t.Fatal(err)
	}
	if read(target) != "v2" || read(target+OldSuffix) != "v1" {
		t.Fatalf("target %q, old %q", read(target), read(target+OldSuffix))
	}
	if _, err := os.Stat(staged); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the staged file stayed")
	}
	if got := leftovers(target); len(got) != 1 || got[0] != target+OldSuffix {
		t.Fatalf("leftovers = %v", got)
	}
	// A failed move puts the running build back.
	if err := swapFile(filepath.Join(dir, "missing.exe"), target); err == nil || read(target) != "v2" {
		t.Fatalf("rollback: %v, target %q", err, read(target))
	}
}

func TestCleanDownloads(t *testing.T) {
	dir := t.TempDir()
	keep := "YT-Cartell-Studio-2.1.0-windows-x64.exe.0123456789ab.part"
	for _, n := range []string{"YT-Cartell-Studio-1.9.0-windows-x64.exe", "YT-Cartell-Studio-2.0.0-macos-arm64.zip", keep, "junk.tmp"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	cleanDownloads(dir, "2.0.0")
	left, _ := os.ReadDir(dir)
	if len(left) != 1 || left[0].Name() != keep {
		t.Fatalf("left = %v", left)
	}
}

func TestBundleOf(t *testing.T) {
	exe := filepath.Join(string(filepath.Separator)+"Applications", "YT Cartell Studio.app", "Contents", "MacOS", "YT Cartell Studio")
	b, ok := bundleOf(exe)
	if !ok || filepath.Base(b) != "YT Cartell Studio.app" {
		t.Fatalf("bundleOf = %q, %v", b, ok)
	}
	if filepath.Base(oldBundle(b)) != "YT Cartell Studio.old.app" {
		t.Fatal(oldBundle(b))
	}
	if _, ok := bundleOf(filepath.Join("usr", "local", "bin", "yt")); ok {
		t.Fatal("a bare binary is not a bundle")
	}
}

type zipEntry struct {
	name, body string
	mode       fs.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h.SetMode(mode)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, e.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "b.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStageAndSwapBundle(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "Studio.app")
	bin := filepath.Join(bundle, "Contents", "MacOS", "Studio")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(bin, []byte("old"), 0o755)
	entries := []zipEntry{
		{name: "Studio.app/", mode: fs.ModeDir | 0o755},
		{name: "Studio.app/Contents/MacOS/Studio", body: "new", mode: 0o755},
		{name: "Studio.app/Contents/Info.plist", body: "<plist/>"},
		{name: "__MACOSX/Studio.app/._Info.plist", body: "junk"},
	}
	if runtime.GOOS != "windows" { // symlinks need a privilege on Windows
		entries = append(entries, zipEntry{name: "Studio.app/Contents/Frameworks/Current", body: "A", mode: fs.ModeSymlink | 0o777})
	}
	z := makeZip(t, entries...)
	app, stage, err := stageBundle(z, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "Studio"))
		if err != nil || st.Mode().Perm()&0o111 == 0 {
			t.Fatalf("exec bit lost: %v %v", st.Mode(), err)
		}
	}
	if err := swapBundle(app, bundle); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(stage)
	if b, _ := os.ReadFile(bin); string(b) != "new" {
		t.Fatalf("bundle binary = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(oldBundle(bundle), "Contents", "MacOS", "Studio")); string(b) != "old" {
		t.Fatalf("old bundle binary = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "__MACOSX")); err == nil {
		t.Fatal("__MACOSX extracted")
	}
}

func TestUnzipRefusesEscapes(t *testing.T) {
	for _, e := range []zipEntry{
		{name: "../evil", body: "x"},
		{name: "a/../../evil", body: "x"},
		{name: "/abs/evil", body: "x"},
		{name: `..\evil`, body: "x"},
		{name: "a/link", body: "../../etc", mode: fs.ModeSymlink | 0o777},
		{name: "a/link2", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
		{name: "a/link3", body: "b/../..", mode: fs.ModeSymlink | 0o777},
	} {
		dest := t.TempDir()
		err := unzip(makeZip(t, e), filepath.Join(dest, "out"))
		if KindOf(err) != KindChecksum {
			t.Errorf("%q → %v", e.name, err)
		}
		if _, err := os.Stat(filepath.Join(dest, "evil")); err == nil {
			t.Errorf("%q escaped", e.name)
		}
	}
}

func TestFake(t *testing.T) {
	f := &Fake{Release: Release{Version: "2.1.0"}}
	var got int64
	path, err := f.Download(context.Background(), f.Release, func(d, _ int64) { got = d })
	if err != nil || path == "" || got != 40<<20 {
		t.Fatalf("%q %v %d", path, err, got)
	}
	restart, err := f.Apply(path)
	if err != nil || restart("2.0.0") != nil {
		t.Fatal(err)
	}
	if c, d, a, r := f.Counts(); c != 0 || d != 1 || a != 1 || r != 1 {
		t.Fatalf("counts %d %d %d %d", c, d, a, r)
	}
}
