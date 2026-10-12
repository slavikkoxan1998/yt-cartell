package studio

// Local speech recognition (Settings → Розпізнавання мови → «Локально»):
// whisper.cpp runs on the user's computer — no key, no upload, no internet
// after the one-time download the user starts with a button. The engine only
// installs it into the settings folder and remembers the paths; finding,
// running and checking it is internal/stt (TestSTT and Transcribe go through
// stt.Client like the online services).

import (
	"context"
	"log"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/stt"
)

// Seams for tests: the installers and their folder.
var (
	installLocal      = stt.InstallLocal
	installLocalModel = stt.InstallLocalModel
	sttLocalDir       = stt.LocalDir
)

// InstallLocalSTT downloads whisper.cpp and the model (a stt.LocalModels id,
// "" = stt.DefaultLocalModel) into the app's settings folder (stt.LocalDir)
// and stores both paths in Settings (STT.LocalBin, STT.LocalModel); the
// provider is left as it is. A program the user set in Settings stays: then
// only the model is downloaded. On macOS the program comes from Homebrew —
// without it nothing is downloaded and the error says `brew install
// whisper-cpp`. Started by the user only; progress(pct, message) may be nil
// and is called from this goroutine at most ~8 times a second. Cancelling
// ctx stops the download (a partly downloaded model resumes next time).
func (e *Engine) InstallLocalSTT(ctx context.Context, model string, progress func(int, string)) error {
	if e.closing.Load() {
		return ErrClosing
	}
	cfg := e.cfg.Get()
	dir := sttLocalDir()
	if strings.TrimSpace(cfg.STT.LocalBin) != "" {
		if bin, _, _, _ := stt.LocalStatus(cfg); bin != "" {
			path, err := installLocalModel(ctx, dir, model, progress)
			if err != nil {
				return err
			}
			log.Printf("[studio] local speech recognition: model %s (own program %s)", path, bin)
			return e.cfg.Update(func(c *config.Config) { c.STT.LocalModel = path })
		}
	}
	bin, path, err := installLocal(ctx, dir, model, progress)
	if err != nil {
		return err
	}
	log.Printf("[studio] local speech recognition: %s, model %s", bin, path)
	return e.cfg.Update(func(c *config.Config) { c.STT.LocalBin, c.STT.LocalModel = bin, path })
}

// LocalSTTStatus reports whether local speech recognition is ready: the
// whisper.cpp program and model found (paths), ok, and a Ukrainian status
// line (what is ready or what to do). It only looks for files.
func (e *Engine) LocalSTTStatus() (bin, model string, ok bool, hint string) {
	return stt.LocalStatus(e.cfg.Get())
}

// LocalSTTModels lists the models InstallLocalSTT can download.
func (e *Engine) LocalSTTModels() []stt.ModelInfo { return stt.LocalModels() }
