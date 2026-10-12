package pickers

import (
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/tts"
)

// A project keeps the voice ids it was planned with: after the provider
// changed, another provider's id reads «Голос іншого сервісу» (never the raw
// id), and the pickers list only the current provider's voices.
func TestVoicesFollowTheProvider(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, _ := core.NewHeadless(e)
	defer c.Close()
	_ = e.Config().Update(func(cf *config.Config) { cf.TTS.Switch("elevenlabs") })
	c.SetVoicesForTest(tts.VoiceList{Provider: "elevenlabs", Voices: []tts.Voice{{ID: "JBFqnCBsd6RMkjVDRZzb", Name: "George", Usable: true}}})
	if got := VoiceTitle(c, "JBFqnCBsd6RMkjVDRZzb"); got != "George" {
		t.Fatalf("own voice: %q", got)
	}
	_ = e.Config().Update(func(cf *config.Config) { cf.TTS.Switch("cartesia") })
	if l, _, _ := c.Voices(); len(l.Voices) != 0 {
		t.Fatalf("Cartesia shows ElevenLabs voices: %+v", l.Voices)
	}
	if got := VoiceTitle(c, "JBFqnCBsd6RMkjVDRZzb"); got != ForeignVoice {
		t.Fatalf("ElevenLabs voice under Cartesia: %q", got)
	}
	if got := VoiceTitle(c, "a0e99841-438c-4a64-b679-ae501e7d6091"); got != UnknownVoice {
		t.Fatalf("a Cartesia id not in the list: %q", got)
	}
	if VoiceTitle(c, "") != "" {
		t.Fatal("empty id")
	}
}

func TestVoiceMetaLibraryAndNotes(t *testing.T) {
	m := VoiceMeta(tts.Voice{Gender: "male", Category: "library", Usable: false, Locked: tts.LockedPaidEleven})
	if m != "чоловічий · з бібліотеки · потрібен платний тариф ElevenLabs" {
		t.Fatalf("library: %q", m)
	}
	m = VoiceMeta(tts.Voice{Gender: "female", Category: "premade", Usable: true, Note: "доступний до 31 грудня 2026"})
	if !strings.HasSuffix(m, "стандартний · доступний до 31 грудня 2026") {
		t.Fatalf("premade: %q", m)
	}
}
