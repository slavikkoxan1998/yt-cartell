package studio

import (
	"context"
	"log"

	"ytcartell/internal/config"
	"ytcartell/internal/tts"
)

// PiperStatus reports whether Piper TTS is ready: the executable path,
// ok, and a Ukrainian status hint.
func (e *Engine) PiperStatus() (bin string, ok bool, hint string) {
	cfg := e.cfg.Get()
	custom := cfg.TTS.BaseURL
	if cfg.TTS.Provider != "piper" && cfg.TTS.Providers != nil {
		if p, ok := cfg.TTS.Providers["piper"]; ok {
			custom = p.BaseURL
		}
	}
	return tts.PiperStatus(custom)
}

// InstallPiper downloads and installs Piper TTS into the app's piper directory,
// verifies it, and stores the path in config (TTS.BaseURL).
func (e *Engine) InstallPiper(ctx context.Context, progress func(int, string)) (string, error) {
	if e.closing.Load() {
		return "", ErrClosing
	}
	bin, err := tts.InstallPiper(ctx, progress)
	if err != nil {
		return "", err
	}
	log.Printf("[studio] local Piper TTS installed: %s", bin)
	err = e.cfg.Update(func(c *config.Config) {
		if c.TTS.Provider == "piper" {
			c.TTS.BaseURL = bin
		}
		if c.TTS.Providers != nil {
			if p, ok := c.TTS.Providers["piper"]; ok {
				p.BaseURL = bin
				c.TTS.Providers["piper"] = p
			}
		}
	})
	return bin, err
}
