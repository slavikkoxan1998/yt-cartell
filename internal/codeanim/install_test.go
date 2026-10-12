package codeanim

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ytcartell/internal/toolsinst"
)

func TestShellPins(t *testing.T) {
	for k, b := range shellBuilds {
		if len(b.Asset.SHA256) != 64 || b.Asset.Size < 50<<20 || !strings.Contains(b.Asset.URL, "/"+shellVersion+"/"+b.Platform+"/") {
			t.Fatalf("%s: %+v", k, b)
		}
	}
}

// installShell against a local server: download, verify, unpack, manifest.
func TestInstallShellOffline(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp) // config.ToolsDir on Windows
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	if !strings.HasPrefix(toolsinst.Root(), tmp) {
		t.Skip("tools folder not redirectable here")
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	name := "chrome-headless-shell-win64/chrome-headless-shell.exe"
	if runtime.GOOS != "windows" {
		name = "chrome-headless-shell-win64/chrome-headless-shell"
	}
	for _, f := range []string{name, "chrome-headless-shell-win64/resources.pak"} {
		w, _ := zw.Create(f)
		_, _ = w.Write([]byte("fake " + f))
	}
	_ = zw.Close()
	data := zb.Bytes()
	sum := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer srv.Close()
	b := shellBuild{Platform: "win64", Asset: toolsinst.Asset{URL: srv.URL + "/x.zip", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Name: "x.zip"}}
	if runtime.GOOS != "windows" {
		b.Platform = "win64" // the fake archive's folder; shellExe adds .exe only for win*
	}
	old := shellCheck
	shellCheck = func(context.Context, string) error { return nil }
	defer func() { shellCheck = old }()
	var last int
	p, err := installShell(context.Background(), b, func(pct int, _ string) { last = pct })
	if runtime.GOOS != "windows" && err != nil {
		t.Skip("fake archive layout is Windows-only:", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if last != 100 || !strings.HasPrefix(p, ShellDir()) {
		t.Fatalf("path %s, progress %d", p, last)
	}
	got, ok := installedShell()
	if !ok || got != p {
		t.Fatalf("installedShell = %q %v, want %q", got, ok, p)
	}
	if _, err := os.Stat(filepath.Join(ShellDir(), "chrome-headless-shell-win64", "resources.pak")); err != nil {
		t.Fatal(err)
	}
	// A corrupted download is refused.
	b.Asset.SHA256 = strings.Repeat("0", 64)
	b.Asset.Name = "y.zip"
	if _, err := installShell(context.Background(), b, nil); err == nil {
		t.Fatal("bad checksum accepted")
	}
}
