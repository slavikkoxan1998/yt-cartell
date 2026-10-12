package studio

// Settings helpers: the Google AI Studio key states and their check, and a
// voice looked up by its id («Вставити ID голосу» in the voice pickers).

import (
	"context"

	"ytcartell/internal/gemini"
	"ytcartell/internal/tts"
)

// GoogleKeyStatus is the state of every Google AI Studio key (no network):
// working, resting after a quota hit (until when), rejected.
func (e *Engine) GoogleKeyStatus() []gemini.KeyStatus { return e.gem.KeyStatus() }

// CheckGoogleKeys checks every Google key with the free models.list call (a
// few at a time) and sums the result up in one line.
func (e *Engine) CheckGoogleKeys(ctx context.Context) (bool, string) {
	ok, msg := e.gem.CheckKeys(ctx)
	return ok, clipRunes(msg, 300)
}

// LookupVoice finds a voice of the configured TTS provider by its id
// (Cartesia, ElevenLabs) — a free read, never a synthesis.
func (e *Engine) LookupVoice(ctx context.Context, id string) (tts.Voice, error) {
	return e.speech.LookupVoice(ctx, id)
}
