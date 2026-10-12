package media

import (
	"os"
	"path/filepath"
	"testing"

	"ytcartell/internal/config"
)

// TestLocateInstalledCopy: the studio's own FFmpeg (Settings → «Встановити
// автоматично», package toolsinst) is found without any settings, before
// PATH; a configured path still wins.
func TestLocateInstalledCopy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "YT Cartell Studio", "ffmpeg", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(dir, exeName(n)), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := InstalledDir
	InstalledDir = func() string { return dir }
	t.Cleanup(func() { InstalledDir = old })

	f, err := Locate(config.Config{})
	if err != nil || f.FFmpeg != filepath.Join(dir, exeName("ffmpeg")) || f.FFprobe != filepath.Join(dir, exeName("ffprobe")) {
		t.Fatalf("installed copy not found first: %+v %v", f, err)
	}
	if !IsInstalled(f.FFmpeg) || IsInstalled(filepath.Join(filepath.Dir(dir), "other", "ffmpeg")) || IsInstalled("") {
		t.Fatal("IsInstalled")
	}

	// A configured folder wins over the installed copy.
	own := t.TempDir()
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(own, exeName(n)), []byte("fake"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if g, err := Locate(config.Config{FFmpegPath: own}); err != nil || filepath.Dir(g.FFmpeg) != own || filepath.Dir(g.FFprobe) != own {
		t.Fatalf("configured folder: %+v %v", g, err)
	}

	// The default folder follows config.ToolsDir.
	InstalledDir = old
	if want := filepath.Join(config.ToolsDir(), "ffmpeg", "bin"); InstalledDir() != want {
		t.Fatalf("InstalledDir = %q, want %q", InstalledDir(), want)
	}
}
