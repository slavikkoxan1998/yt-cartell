package home

import (
	"context"
	"image/color"
	"strings"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/tts"
)

// Service checks (parity with the old projects board): whether the text,
// picture/video, voice and montage services are set up and answering. The
// G-Labs health check runs in the background whenever a page opens; the
// rest reads the settings, the located FFmpeg and the voice account.

type probe struct {
	done, ok bool
	msg      string
}

type services struct {
	seq   int
	glabs probe
	ffAt  time.Time
	ffOK  bool
}

// refresh starts the background checks when a page opens (entered).
func (s *services) refresh(gtx C, c *core.Ctx, entered bool) {
	if gtx.Now.Sub(s.ffAt) > 2*time.Second || entered {
		s.ffAt = gtx.Now
		s.ffOK = c.E.FF().FFmpeg != ""
	}
	if !entered {
		return
	}
	s.seq++
	seq := s.seq
	s.glabs = probe{}
	cfg := c.E.Config().Get()
	if c.Offline || strings.TrimSpace(cfg.Glabs.APIKey) == "" || !usesGlabs(cfg) {
		return
	}
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ok, msg := c.E.TestGlabs(ctx)
		c.Post(func() {
			if seq == s.seq {
				s.glabs = probe{done: true, ok: ok, msg: msg}
			}
		})
	})
}

func usesGlabs(cfg config.Config) bool {
	return cfg.Video.Provider == "glabs" || cfg.Image.Provider == "glabs"
}

type svcState uint8

const (
	svcUnknown svcState = iota // not checked (offline)
	svcChecking
	svcOK
	svcBad
)

type svcChip struct {
	name, hint string
	state      svcState
}

func okState(ok bool) svcState {
	if ok {
		return svcOK
	}
	return svcBad
}

func ttsConfigured(t config.TTS, googleKeys int) bool {
	info, _ := tts.ProviderByID(t.Provider)
	if info.NeedsBaseURL && strings.TrimSpace(t.BaseURL) == "" {
		return false // the custom server URL / the Azure region
	}
	if info.KeyOptional || strings.TrimSpace(t.APIKey) != "" || len(t.Keys) > 0 {
		return true
	}
	return t.Provider == "google" && googleKeys > 0 // borrows the Google AI keys
}

func ttsName(p string) string {
	if info, ok := tts.ProviderByID(p); ok && info.Label != "" {
		return info.Label
	}
	return "ElevenLabs"
}

// chips are the four service statuses.
func (s *services) chips(c *core.Ctx) []svcChip {
	cfg := c.E.Config().Get()
	var out []svcChip

	text := svcChip{name: "Текст"}
	if cfg.Text.Provider == "google" {
		text.hint, text.state = "Google · "+cfg.Google.StoryModel, okState(len(cfg.Google.Keys) > 0)
	} else {
		text.hint, text.state = cfg.Text.Model, okState(!textNeedsKey(cfg.Text))
	}
	out = append(out, text)

	media := svcChip{name: "Кадри й відео"}
	switch {
	case usesGlabs(cfg):
		switch {
		case strings.TrimSpace(cfg.Glabs.APIKey) == "":
			media.hint, media.state = "G-Labs · немає ключа", svcBad
		case s.glabs.done && s.glabs.ok:
			media.hint, media.state = "G-Labs онлайн", svcOK
		case s.glabs.done:
			media.hint, media.state = "G-Labs не відповідає", svcBad
		case c.Offline:
			media.hint, media.state = "G-Labs · не перевірено", svcUnknown
		default:
			media.hint, media.state = "перевіряємо…", svcChecking
		}
	default:
		media.hint, media.state = "Google", okState(len(cfg.Google.Keys) > 0)
	}
	out = append(out, media)

	voice := svcChip{name: "Озвучка"}
	list, loading, err := c.Voices()
	switch {
	case !ttsConfigured(cfg.TTS, len(cfg.Google.Keys)):
		voice.hint, voice.state = "не налаштовано", svcBad
	case err != nil:
		voice.hint, voice.state = ttsName(cfg.TTS.Provider)+" · помилка ключа", svcBad
	case list.Tier != "":
		voice.hint, voice.state = ttsName(cfg.TTS.Provider)+" · "+list.Tier, svcOK
	case loading:
		voice.hint, voice.state = "перевіряємо…", svcChecking
	default:
		voice.hint, voice.state = ttsName(cfg.TTS.Provider)+" · налаштовано", svcOK
	}
	out = append(out, voice)

	mont := svcChip{name: "Монтаж"}
	if s.ffOK {
		mont.hint, mont.state = "FFmpeg", svcOK
	} else {
		mont.hint, mont.state = "FFmpeg не знайдено", svcBad
	}
	out = append(out, mont)
	return out
}

// dot is the status colour of a service chip.
func (st svcState) dot(th *gui.Theme) color.NRGBA {
	switch st {
	case svcOK:
		return th.Pal.Success
	case svcBad:
		return th.Pal.Error
	case svcChecking:
		return th.Pal.Running
	}
	return th.Pal.Text3
}

// textNeedsKey: the text endpoint has no key but needs one (a local Ollama
// and an endpoint set to «Без ключа» do not).
func textNeedsKey(t config.Text) bool {
	return strings.TrimSpace(t.APIKey) == "" && t.Provider != "ollama" && t.Auth != "none"
}

// setupMissing lists what a fresh install still lacks to make a video.
func setupMissing(cfg config.Config) []string {
	var miss []string
	googleNeeded := false
	if cfg.Text.Provider == "google" {
		googleNeeded = true
	} else if textNeedsKey(cfg.Text) {
		miss = append(miss, "ключ текстового сервісу")
	}
	if usesGlabs(cfg) && strings.TrimSpace(cfg.Glabs.APIKey) == "" {
		miss = append(miss, "ключ G-Labs Studio (кадри й відео)")
	}
	if cfg.Image.Provider == "google" || cfg.Video.Provider == "google" {
		googleNeeded = true
	}
	if googleNeeded && len(cfg.Google.Keys) == 0 {
		miss = append(miss, "ключ Google AI Studio")
	}
	if cfg.Image.Provider == "custom" && strings.TrimSpace(cfg.Custom.ImageKey) == "" {
		if pr, ok := providers.FindImagePreset(cfg.Custom.ImagePreset); !ok || !pr.KeyOptional {
			miss = append(miss, "ключ свого API для кадрів")
		}
	}
	if cfg.Video.Provider == "custom" && strings.TrimSpace(cfg.Custom.VideoKey) == "" {
		if pr, ok := providers.FindVideoPreset(cfg.Custom.VideoPreset); !ok || !pr.KeyOptional {
			miss = append(miss, "ключ свого API для відео")
		}
	}
	if !ttsConfigured(cfg.TTS, len(cfg.Google.Keys)) {
		if cfg.TTS.Provider == "custom" {
			miss = append(miss, "адреса сервісу озвучки")
		} else {
			miss = append(miss, "ключ озвучки ("+ttsName(cfg.TTS.Provider)+")")
		}
	}
	return miss
}
