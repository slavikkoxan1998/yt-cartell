package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestConcurrencyClamp: «Сцен одночасно» keeps every value up to the hard
// end (an existing 6 stays 6), never 0 or more than MaxConcurrency.
func TestConcurrencyClamp(t *testing.T) {
	for in, want := range map[int]int{-3: 1, 0: 1, 1: 1, 6: 6, 7: 7, 12: 12, 20: 20, 21: 20, 99: 20} {
		c := Default()
		c.Concurrency = in
		c.Normalize()
		if c.Concurrency != want {
			t.Errorf("Concurrency %d → %d, want %d", in, c.Concurrency, want)
		}
	}
	if MaxConcurrency != 20 || Default().Concurrency != 2 {
		t.Fatal("limits changed")
	}
}

// TestToolsDir: programs go to %LOCALAPPDATA% on Windows, the settings
// folder elsewhere.
func TestToolsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		local := filepath.Join(t.TempDir(), "local")
		t.Setenv("LOCALAPPDATA", local)
		if got := ToolsDir(); got != filepath.Join(local, "YT Cartell Studio") {
			t.Fatalf("ToolsDir = %q", got)
		}
		t.Setenv("LOCALAPPDATA", "")
	}
	if got := ToolsDir(); runtime.GOOS != "windows" && got != Dir() || runtime.GOOS == "windows" && os.Getenv("LOCALAPPDATA") == "" && got != Dir() {
		t.Fatalf("ToolsDir = %q, Dir = %q", got, Dir())
	}
}
