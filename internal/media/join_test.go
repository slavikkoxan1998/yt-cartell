package media

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Multi-voice parts are joined with their pauses; each keeps its length and
// the reported places match the joined file.
func TestJoinVoices(t *testing.T) {
	f := locate(t)
	fx := synth(t, f)
	out := filepath.Join(t.TempDir(), "line.mp3")
	start, end, err := f.JoinVoices(context.Background(), []string{fx.voice, fx.music, fx.voice}, []float64{9, 0.4, 0.2}, out)
	if err != nil {
		t.Fatal(err)
	}
	vi, _ := f.Probe(context.Background(), fx.voice)
	mi, _ := f.Probe(context.Background(), fx.music)
	if start[0] != 0 || !near(end[0], vi.Duration, 0.001) || !near(start[1], end[0]+0.4, 0.001) ||
		!near(end[1], start[1]+mi.Duration, 0.001) || !near(start[2], end[1]+0.2, 0.001) {
		t.Fatalf("places: %v %v", start, end)
	}
	info, err := f.Probe(context.Background(), out)
	if err != nil || !near(info.Duration, end[2], 0.08) || !info.HasAudio {
		t.Fatalf("joined: %+v %v (want %.3f s)", info, err, end[2])
	}
	if _, _, err := f.JoinVoices(context.Background(), nil, nil, out); err == nil {
		t.Fatal("no parts must fail")
	}
}

// A timed multi-voice line shows each part at its own time (moved and
// trimmed with the line), in its speaker's colour when burnt in.
func TestVoicePartCues(t *testing.T) {
	seg := Segment{Narration: "Раз. Два!", VoiceParts: []VoicePart{
		{Text: "Раз.", Start: 0, End: 1},
		{Text: "Два!", Start: 1.4, End: 2.4, Color: "#4FC3F7"},
	}}
	sl := Slot{VoiceStart: 10, VoiceEnd: 12.2, VoiceDuration: 2.2, VoiceTrimIn: 0.2, cueStart: 10, cueSpan: 2.2}
	cs := voiceCues(seg, sl)
	if len(cs) != 2 || cs[0].start != 10 || !near(cs[0].span, 0.8, 1e-9) || !near(cs[1].start, 11.2, 1e-9) ||
		!near(cs[1].span, 1, 1e-9) || cs[1].color != "#4FC3F7" {
		t.Fatalf("cues: %+v", cs)
	}
	ass := buildASS(cs, SubtitleStyle{}.Resolve(), 1920, 1080)
	if !strings.Contains(ass, `{\1c&HF7C34F&}Два!`) || strings.Contains(ass, `}Раз.`) {
		t.Fatalf("ass:\n%s", ass)
	}
	if srt := buildSRT(cs, false); strings.Contains(srt, `\1c`) || !strings.Contains(srt, "Два!") {
		t.Fatalf("srt: %s", srt)
	}
	// Untimed parts (an older line): the narration over the whole line.
	seg.VoiceParts[1].End = 0
	if cs := voiceCues(seg, sl); len(cs) != 1 || cs[0].narration != "Раз. Два!" {
		t.Fatalf("fallback: %+v", cs)
	}
	// No voice: the narration as before.
	if cs := voiceCues(Segment{Narration: "x", VoiceParts: seg.VoiceParts}, Slot{cueStart: 3, cueSpan: 2}); len(cs) != 1 || cs[0].start != 3 {
		t.Fatalf("no voice: %+v", cs)
	}
}
