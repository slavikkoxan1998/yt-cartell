package tts

// Multi-speaker dialogue in ONE request (multi-voice voice-overs): ElevenLabs
// POST /v1/text-to-dialogue/with-timestamps?output_format=mp3_44100_128
// {"inputs":[{"text","voice_id"}], "model_id":"eleven_v3", "language_code"?}
// → {"audio_base64", "voice_segments":[{"voice_id", "start_time_seconds",
// "end_time_seconds", "dialogue_input_index", …}], "alignment", …}. Eleven v3
// only, ≤ 10 distinct voices and ≤ 2 000 characters per request (checked
// against elevenlabs.io/docs/api-reference/text-to-dialogue/convert-with-timestamps,
// 2026-10). The other providers have no dialogue endpoint (Google here is
// Cloud TTS, whose Chirp voices speak one at a time): the studio voices each
// part on its own and joins them (media.JoinVoices).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
)

// Dialogue limits of ElevenLabs text-to-dialogue.
const (
	DialogueMaxVoices = 10
	DialogueMaxChars  = 2000
)

// DialogueLine is one speaker's part of a dialogue.
type DialogueLine struct {
	Text  string
	Voice string // voice id ("" = the voice from Settings)
}

// Dialogue is the voiced dialogue: MP3 bytes and where each line sits in it
// (seconds; Start[i] = End[i] = 0 when the service did not place line i).
type Dialogue struct {
	Audio      []byte
	Start, End []float64
}

// CanDialogue reports whether the configured service voices several speakers
// in one request for text in language (ISO 639-1, "" = unknown): ElevenLabs
// with a key whose model for that language is Eleven v3 (the only model of
// text-to-dialogue — a project voiced with another model keeps it, so every
// scene sounds the same).
func (c *Client) CanDialogue(ctx context.Context, language string) bool {
	s := c.settings()
	if s.provider != "elevenlabs" || len(s.keys) == 0 {
		return false
	}
	model, _ := c.ModelFor(ctx, language)
	return isV3(model)
}

// SynthesizeDialogue voices lines with their voices in one ElevenLabs
// text-to-dialogue request (see CanDialogue). opt.LanguageCode is sent when
// the model enforces it.
func (c *Client) SynthesizeDialogue(ctx context.Context, lines []DialogueLine, opt SynthOptions) (Dialogue, error) {
	s := c.settings()
	if s.provider != "elevenlabs" {
		return Dialogue{}, fatal("Діалог одним запитом уміє лише ElevenLabs")
	}
	if len(s.keys) == 0 {
		return Dialogue{}, errNoKey(s)
	}
	type input struct {
		Text    string `json:"text"`
		VoiceID string `json:"voice_id"`
	}
	inputs := make([]input, 0, len(lines))
	voices := map[string]bool{}
	chars := 0
	for _, l := range lines {
		text, voice := strings.TrimSpace(l.Text), firstNonEmpty(strings.TrimSpace(l.Voice), s.voice)
		if text == "" {
			return Dialogue{}, errors.New("Немає тексту для озвучки")
		}
		if voice == "" {
			return Dialogue{}, errors.New("Не вибрано голос для озвучки")
		}
		if err := foreignVoice(s, voice); err != nil {
			return Dialogue{}, err
		}
		voices[voice] = true
		chars += runes(text)
		inputs = append(inputs, input{text, voice})
	}
	switch {
	case len(inputs) == 0:
		return Dialogue{}, errors.New("Немає тексту для озвучки")
	case len(voices) > DialogueMaxVoices:
		return Dialogue{}, fatal(fmt.Sprintf("ElevenLabs: у діалозі щонайбільше %d голосів", DialogueMaxVoices))
	case chars > DialogueMaxChars:
		return Dialogue{}, fatal(fmt.Sprintf("ElevenLabs: діалог довший за %d символів", DialogueMaxChars))
	}
	model := "eleven_v3"
	payload := map[string]any{"inputs": inputs, "model_id": model,
		"settings": map[string]any{"stability": snapV3Stability(orDefault(opt.Stability, 0.5, 0, 1))}}
	if lang := c.enforcedLanguage(ctx, s, model, langCode(opt.LanguageCode)); lang != "" {
		payload["language_code"] = lang
	}
	body, _ := json.Marshal(payload)
	u := elevenBase + "/v1/text-to-dialogue/with-timestamps?output_format=mp3_44100_128"
	var out Dialogue
	err := c.run(ctx, s, inputs[0].VoiceID, chars, false, func(key string) (int, error) {
		hdr := map[string]string{"xi-api-key": key}
		for {
			res, err := c.do(ctx, http.MethodPost, u, hdr, body, synthTimeout, maxAudio)
			if err != nil {
				return 0, netErr(ctx, "ElevenLabs", err)
			}
			if res.ok() {
				out, err = parseDialogue(res.body, len(inputs))
				return 0, err
			}
			if _, hinted := payload["language_code"]; hinted && (res.status == 400 || res.status == 422) && reLanguage.MatchString(string(res.body)) {
				delete(payload, "language_code")
				body, _ = json.Marshal(payload)
				continue
			}
			return 0, elevenErr(res)
		}
	})
	return out, err
}

// parseDialogue reads a text-to-dialogue/with-timestamps reply for n inputs.
func parseDialogue(b []byte, n int) (Dialogue, error) {
	var r struct {
		Audio    string `json:"audio_base64"`
		Segments []struct {
			Start float64 `json:"start_time_seconds"`
			End   float64 `json:"end_time_seconds"`
			Index *int    `json:"dialogue_input_index"`
		} `json:"voice_segments"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Dialogue{}, fmt.Errorf("ElevenLabs: незрозуміла відповідь діалогу: %s", clip(string(b), 160))
	}
	audio, err := decodeB64(r.Audio)
	if err != nil || len(audio) == 0 {
		return Dialogue{}, errors.New("ElevenLabs: порожня відповідь замість аудіо діалогу")
	}
	d := Dialogue{Audio: audio, Start: make([]float64, n), End: make([]float64, n)}
	seen := make([]bool, n)
	for _, sg := range r.Segments {
		if sg.Index == nil || *sg.Index < 0 || *sg.Index >= n || !(sg.End >= sg.Start) || math.IsNaN(sg.Start) {
			continue
		}
		i := *sg.Index
		if !seen[i] || sg.Start < d.Start[i] {
			d.Start[i] = sg.Start
		}
		if !seen[i] || sg.End > d.End[i] {
			d.End[i] = sg.End
		}
		seen[i] = true
	}
	return d, nil
}
