package stt

// Provider "custom": any speech-recognition API described by a spec
// (config.CustomAPI.STTSpec) through the universal adapter of package
// providers (KindSTT — {{audio}} as a file part with "encoding": "multipart",
// {{audio_b64}}, {{audio_name}}, {{audio_mime}}, {{language}}, {{model}};
// synchronous or async jobs). Whatever the API returns — JSON of ElevenLabs,
// Whisper, Deepgram, AssemblyAI …, SRT, VTT — is read by ParseTranscript,
// so word timings work without per-service code. Address, key and model are
// the STT section's own (BaseURL "" = the origin of an absolute submit path).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	universal "ytcartell/internal/providers"
)

// ProviderCustom is the STT provider id of an own API.
const ProviderCustom = "custom"

func (c *Client) customAPI() universal.AudioAPI {
	cfg := c.get()
	return universal.AudioAPI{Kind: universal.KindSTT, BaseURL: strings.TrimRight(strings.TrimSpace(cfg.STT.BaseURL), "/"),
		Key: strings.TrimSpace(cfg.STT.APIKey), Model: strings.TrimSpace(cfg.STT.Model), Spec: cfg.Custom.STTSpec, HTTP: c.http}
}

// transcribeCustom uploads path through the spec and parses the reply.
func (c *Client) transcribeCustom(ctx context.Context, path, lang string) (Transcript, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Transcript{}, fmt.Errorf("Файл запису не знайдено: %s", filepath.Base(path))
	}
	if len(data) == 0 {
		return Transcript{}, errors.New("Файл запису порожній")
	}
	out, err := c.customAPI().Transcribe(ctx, universal.TranscribeRequest{Audio: data, Name: filepath.Base(path), Language: strings.ToLower(strings.TrimSpace(lang))})
	if err != nil {
		if ctx.Err() != nil {
			return Transcript{}, ctx.Err()
		}
		return Transcript{}, err
	}
	tr, err := ParseTranscript(out, "")
	if err != nil {
		if errors.Is(err, ErrNoTimecodes) {
			return Transcript{}, errors.New("Своє API розпізнавання повернуло текст без таймкодів слів — у описі API попроси слова з часом (verbose_json, timestamps=word …)")
		}
		return Transcript{}, fmt.Errorf("Своє API розпізнавання: %w", err)
	}
	if tr.Language == "" {
		tr.Language = strings.ToLower(strings.TrimSpace(lang))
	}
	return tr, nil
}

// testCustom is the spec's "test" call (nothing is transcribed).
func (c *Client) testCustom(ctx context.Context) (bool, string) {
	if strings.TrimSpace(c.get().Custom.STTSpec) == "" {
		return false, "Своє API розпізнавання: опису API ще немає — обери шаблон або «Налаштувати з документації»"
	}
	return c.customAPI().Test(ctx)
}
