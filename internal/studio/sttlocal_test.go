package studio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/stt"
)

// fakeInstallers replaces the stt installers (no network: their downloads
// are tested in internal/stt against a mock) and the install folder.
type fakeInstallers struct {
	dir              string
	full, modelOnly  int
	gotDir, gotModel string
	err              error
}

func useFakeInstallers(t *testing.T) *fakeInstallers {
	t.Helper()
	f := &fakeInstallers{dir: filepath.Join(t.TempDir(), "whisper")}
	oldFull, oldModel, oldDir := installLocal, installLocalModel, sttLocalDir
	t.Cleanup(func() { installLocal, installLocalModel, sttLocalDir = oldFull, oldModel, oldDir })
	sttLocalDir = func() string { return f.dir }
	model := func(dir, id string) string {
		p := filepath.Join(dir, "models", "ggml-"+id+".bin")
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("lmgg-model"), 0o644)
		return p
	}
	installLocal = func(ctx context.Context, dir, id string, progress func(int, string)) (string, string, error) {
		f.full++
		f.gotDir, f.gotModel = dir, id
		if f.err != nil {
			return "", "", f.err
		}
		progress(40, "Завантажуємо whisper.cpp")
		progress(100, "Готово")
		bin := filepath.Join(dir, "bin", "whisper-cli.exe")
		_ = os.MkdirAll(filepath.Dir(bin), 0o755)
		_ = os.WriteFile(bin, []byte("exe"), 0o755)
		return bin, model(dir, id), nil
	}
	installLocalModel = func(ctx context.Context, dir, id string, progress func(int, string)) (string, error) {
		f.modelOnly++
		f.gotDir, f.gotModel = dir, id
		if f.err != nil {
			return "", f.err
		}
		progress(100, "Готово")
		return model(dir, id), nil
	}
	return f
}

func TestInstallLocalSTTStoresPaths(t *testing.T) {
	env := newEnv(t, func(c *config.Config) { c.STT.Provider = stt.ProviderLocal })
	f := useFakeInstallers(t)
	var pcts []int
	if err := env.e.InstallLocalSTT(context.Background(), "small", func(pct int, msg string) { pcts = append(pcts, pct) }); err != nil {
		t.Fatal(err)
	}
	if f.full != 1 || f.modelOnly != 0 || f.gotDir != f.dir || f.gotModel != "small" {
		t.Fatalf("installer calls: %+v", f)
	}
	c := env.store.Get()
	if c.STT.LocalBin != filepath.Join(f.dir, "bin", "whisper-cli.exe") || c.STT.LocalModel != filepath.Join(f.dir, "models", "ggml-small.bin") {
		t.Fatalf("settings: %+v", c.STT)
	}
	if c.STT.Provider != stt.ProviderLocal {
		t.Fatalf("provider changed: %q", c.STT.Provider)
	}
	if len(pcts) != 2 || pcts[1] != 100 {
		t.Errorf("progress: %v", pcts)
	}
	bin, model, ok, hint := env.e.LocalSTTStatus()
	if !ok || bin != c.STT.LocalBin || model != c.STT.LocalModel || hint == "" {
		t.Fatalf("status: %q %q %v %q", bin, model, ok, hint)
	}
	if ms := env.e.LocalSTTModels(); len(ms) == 0 || ms[0].ID == "" {
		t.Fatalf("models: %+v", ms)
	}

	// Another model with the program in place: the program path stays, only
	// the model is downloaded (the user's own program is never replaced).
	if err := env.e.InstallLocalSTT(context.Background(), "large-v3-turbo", ignoreProgress); err != nil {
		t.Fatal(err)
	}
	c2 := env.store.Get()
	if f.full != 1 || f.modelOnly != 1 || c2.STT.LocalBin != c.STT.LocalBin || filepath.Base(c2.STT.LocalModel) != "ggml-large-v3-turbo.bin" {
		t.Fatalf("model only: %+v, %+v", f, c2.STT)
	}
}

func ignoreProgress(int, string) {}

func TestInstallLocalSTTErrorsKeepSettings(t *testing.T) {
	env := newEnv(t, func(c *config.Config) {
		c.STT.Provider = stt.ProviderLocal
		c.STT.LocalBin = filepath.Join(t.TempDir(), "gone", "whisper-cli.exe") // a stale path: reinstall
	})
	f := useFakeInstallers(t)
	f.err = errors.New("На Mac програму whisper.cpp ставить Homebrew: brew install whisper-cpp")
	err := env.e.InstallLocalSTT(context.Background(), "", nil)
	if err == nil || !strings.Contains(err.Error(), "brew") {
		t.Fatalf("err = %v", err)
	}
	if f.full != 1 || f.modelOnly != 0 || f.gotModel != "" {
		t.Fatalf("calls: %+v", f)
	}
	if c := env.store.Get(); !strings.Contains(c.STT.LocalBin, "gone") || c.STT.LocalModel != "" {
		t.Fatalf("settings changed on error: %+v", c.STT)
	}
	// The service check of a missing program never runs anything.
	ok, msg := env.e.TestSTT(context.Background())
	if ok || !strings.Contains(msg, "gone") {
		t.Fatalf("TestSTT: %v %q", ok, msg)
	}
	// Closing: nothing new starts.
	env.e.closing.Store(true)
	defer env.e.closing.Store(false)
	if err := env.e.InstallLocalSTT(context.Background(), "small", nil); !errors.Is(err, ErrClosing) {
		t.Fatalf("closing: %v", err)
	}
}

func TestLocalProviderSurvivesNormalize(t *testing.T) {
	c := config.Default()
	c.STT.Provider = stt.ProviderLocal
	c.Normalize()
	if c.STT.Provider != stt.ProviderLocal {
		t.Fatalf("provider %q", c.STT.Provider)
	}
}
