package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ytcartell/internal/config"
)

// ElevenLabs text-to-dialogue: one request with every speaker, the
// segments place the lines; only Eleven v3 offers it.
func TestSynthesizeDialogue(t *testing.T) {
	var body map[string]any
	setup(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/text-to-dialogue/with-timestamps":
			if r.URL.Query().Get("output_format") != "mp3_44100_128" || r.Header.Get("xi-api-key") != "el-key" {
				t.Errorf("request %s", r.URL)
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"audio_base64": base64.StdEncoding.EncodeToString([]byte("ID3dialogue")),
				"voice_segments": []any{
					map[string]any{"voice_id": "a", "start_time_seconds": 0.0, "end_time_seconds": 1.2, "dialogue_input_index": 0},
					map[string]any{"voice_id": "b", "start_time_seconds": 1.3, "end_time_seconds": 2.0, "dialogue_input_index": 1},
					map[string]any{"voice_id": "b", "start_time_seconds": 2.0, "end_time_seconds": 2.5, "dialogue_input_index": 1},
					map[string]any{"voice_id": "x", "start_time_seconds": 3.0, "end_time_seconds": 4.0, "dialogue_input_index": 7},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	c, cfg := cfgClient(config.TTS{Provider: "elevenlabs", APIKey: "el-key", Model: "eleven_v3"})
	if !c.CanDialogue(context.Background(), "uk") {
		t.Fatal("Eleven v3 voices dialogues")
	}
	d, err := c.SynthesizeDialogue(context.Background(), []DialogueLine{{Text: "Привіт.", Voice: "a"}, {Text: "[whispers] Тихо!", Voice: "b"}}, SynthOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(d.Audio) != "ID3dialogue" || d.Start[0] != 0 || d.End[0] != 1.2 || d.Start[1] != 1.3 || d.End[1] != 2.5 {
		t.Fatalf("dialogue: %+v", d)
	}
	inputs, _ := body["inputs"].([]any)
	if len(inputs) != 2 || body["model_id"] != "eleven_v3" || inputs[1].(map[string]any)["voice_id"] != "b" {
		t.Fatalf("body: %v", body)
	}
	// Another model, another provider: no dialogue requests.
	cfg.TTS.Model = "eleven_multilingual_v2"
	if c.CanDialogue(context.Background(), "uk") {
		t.Fatal("Multilingual v2 has no dialogue endpoint")
	}
	cfg.TTS = config.TTS{Provider: "openai", APIKey: "sk-x"}
	if c.CanDialogue(context.Background(), "") {
		t.Fatal("OpenAI has no dialogue endpoint")
	}
	if _, err := c.SynthesizeDialogue(context.Background(), []DialogueLine{{Text: "x", Voice: "alloy"}}, SynthOptions{}); err == nil {
		t.Fatal("dialogue through OpenAI must fail")
	}
	// Limits are checked before any request.
	cfg.TTS = config.TTS{Provider: "elevenlabs", APIKey: "el-key", Model: "eleven_v3"}
	if _, err := c.SynthesizeDialogue(context.Background(), []DialogueLine{{Text: strings.Repeat("я", DialogueMaxChars+1), Voice: "a"}}, SynthOptions{}); err == nil {
		t.Fatal("an over-long dialogue must fail")
	}
}
