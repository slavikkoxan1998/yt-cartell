package tts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiperAssetAndPaths(t *testing.T) {
	url, archType, err := piperReleaseAsset()
	if err != nil {
		t.Fatalf("unexpected error resolving piper asset: %v", err)
	}
	if !strings.HasPrefix(url, "https://github.com/rhasspy/piper/releases/download/") {
		t.Errorf("unexpected asset url: %s", url)
	}
	if archType != "tar.gz" && archType != "zip" {
		t.Errorf("unexpected archType: %s", archType)
	}
}

func TestFindPiperLocations(t *testing.T) {
	// 1. Should find existing system binary if present (e.g. /usr/local/bin/piper)
	bin, err := FindPiper("")
	if err == nil {
		if bin == "" {
			t.Fatal("FindPiper returned empty string with nil error")
		}
		if _, serr := os.Stat(bin); serr != nil {
			t.Fatalf("FindPiper returned non-existent path: %s", bin)
		}
		statusBin, ok, hint := PiperStatus("")
		if !ok || statusBin != bin || !strings.Contains(hint, "знайдено") {
			t.Fatalf("PiperStatus mismatch: bin=%s ok=%v hint=%s", statusBin, ok, hint)
		}
	}

	// 2. Custom path testing with a dummy executable in a temp directory
	tmpDir := t.TempDir()
	dummyName := exeName("piper")
	dummyBin := filepath.Join(tmpDir, dummyName)
	if err := os.WriteFile(dummyBin, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Test exact file path
	found, err := FindPiper(dummyBin)
	if err != nil || found != dummyBin {
		t.Fatalf("FindPiper failed with custom file path: got=%s err=%v", found, err)
	}

	// Test directory path containing piper
	foundDir, err := FindPiper(tmpDir)
	if err != nil || foundDir != dummyBin {
		t.Fatalf("FindPiper failed with custom dir path: got=%s err=%v", foundDir, err)
	}
}

func TestPiperVoicesCatalog(t *testing.T) {
	c := &Client{}
	vl, err := c.piperVoices(context.Background(), settings{})
	if err != nil {
		t.Fatalf("piperVoices error: %v", err)
	}
	if len(vl.Voices) == 0 {
		t.Fatal("expected piper voices, got none")
	}
	hasTetiana := false
	for _, v := range vl.Voices {
		if v.ID == "tetiana" {
			hasTetiana = true
			break
		}
	}
	if !hasTetiana {
		t.Error("expected voice tetiana in piper catalog")
	}
}

func TestPiperProbe(t *testing.T) {
	c := &Client{}
	s := settings{provider: "piper"}
	res := c.piperProbe(context.Background(), s)
	// If piper is installed, it should probe successfully or report ffmpeg status
	if res.err != nil {
		if !strings.Contains(res.err.Error(), "не знайдено") && !strings.Contains(res.err.Error(), "FFmpeg") {
			t.Errorf("unexpected probe error: %v", res.err)
		}
	} else {
		if !strings.Contains(res.info, "знайдено") {
			t.Errorf("unexpected probe info: %s", res.info)
		}
	}
}

func TestPiperSynthesizeLive(t *testing.T) {
	bin, err := FindPiper("")
	if err != nil {
		t.Skip("Piper not installed, skipping live synthesis test")
	}
	c := &Client{}
	s := settings{provider: "piper", base: bin}
	data, err := c.piper(context.Background(), s, "Привіт, це перевірка озвучки.", "tetiana", SynthOptions{})
	if err != nil {
		t.Fatalf("Live synthesis failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Synthesized data is empty")
	}
	t.Logf("Synthesized %d bytes of audio with Piper (%s)", len(data), bin)
}

func TestEnsurePiperSharedLibs(t *testing.T) {
	tmpDir := t.TempDir()
	// Create dummy real files
	_ = os.WriteFile(filepath.Join(tmpDir, "libespeak-ng.so.1.52.0.1"), []byte("espeak"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "libpiper_phonemize.so.1.2.0"), []byte("phonemize"), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDir, "libonnxruntime.so.1.14.1"), []byte("onnx"), 0o644)

	ensurePiperSharedLibs(tmpDir)

	for _, req := range []string{"libespeak-ng.so.1", "libespeak-ng.so", "libpiper_phonemize.so.1", "libpiper_phonemize.so", "libonnxruntime.so"} {
		p := filepath.Join(tmpDir, req)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist after ensurePiperSharedLibs, got err: %v", req, err)
		}
	}
}
