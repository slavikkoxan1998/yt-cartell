package studio

// Follow-ups of the reliability fixes (Oct 2026): a re-voiced line that
// outgrows its clip makes the clip outdated; the on-camera dialogue budget
// runs at the owner's real ~2 words/s.

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
)

// The line against the clip, the way the montage plays them: the voice +
// 0.6 s of air + the transitions' overlaps vs the clip minus its trims.
func TestVoiceOutgrowsClip(t *testing.T) {
	p := Project{Input: DefaultInput(), Montage: DefaultMontage()}
	p.Input.Voiceover = true
	for i := 0; i < 3; i++ {
		p.Scenes = append(p.Scenes, Scene{ID: fmt.Sprintf("s%d", i), Narration: "Рядок.",
			Audio: Asset{Status: StatusDone, Path: "audio/v.mp3", Duration: 7.36},
			Video: Asset{Status: StatusDone, Path: "videos/c.mp4", Duration: 8}})
	}
	s := &p.Scenes[1]
	// The default dip: 7.36 + 0.6 fits into 8 s.
	if need, clip, short := VoiceOutgrowsClip(&p, s); short || clip != 8 || need < 7.95 || need > 7.97 {
		t.Fatalf("dip: need %v clip %v short %v", need, clip, short)
	}
	// KTXzWcfeA_: dissolves of 0.3 s take ~0.58 s more — the clip froze.
	p.Montage.Transition, p.Montage.TransitionDuration = "dissolve", 0.3
	if need, _, short := VoiceOutgrowsClip(&p, s); !short || need < 8.5 || need > 8.6 {
		t.Fatalf("crossfades: need %v short %v", need, short)
	}
	p.Montage.Transition = ""
	// What the clip plays counts: trims shorten it.
	s.TrimIn, s.TrimOut = 0.3, 0.2
	if _, clip, short := VoiceOutgrowsClip(&p, s); !short || clip != 7.5 {
		t.Fatalf("trimmed: clip %v short %v", clip, short)
	}
	s.TrimIn, s.TrimOut = 0, 0
	// No probed length: the chosen length; nothing known: never short.
	s.Video.Duration, s.ClipLen = 0, 6
	if _, clip, short := VoiceOutgrowsClip(&p, s); !short || clip != 6 {
		t.Fatalf("ClipLen: clip %v short %v", clip, short)
	}
	s.ClipLen = 0
	if _, _, short := VoiceOutgrowsClip(&p, s); short {
		t.Fatal("unknown clip length")
	}
	s.Video.Duration = 6
	for name, mut := range map[string]func(s *Scene, p *Project){
		"own recording": func(s *Scene, _ *Project) { s.AudioSource = AudioUpload },
		"stale voice":   func(s *Scene, _ *Project) { s.Audio.Stale = true },
		"no clip":       func(s *Scene, _ *Project) { s.Video = EmptyAsset() },
		"failed voice":  func(s *Scene, _ *Project) { s.Audio.Status = StatusError },
		"no voice-over": func(_ *Scene, p *Project) { p.Input.Voiceover = false },
	} {
		q := cloneProject(&p)
		mut(&q.Scenes[1], &q)
		if _, _, short := VoiceOutgrowsClip(&q, &q.Scenes[1]); short {
			t.Errorf("%s: short", name)
		}
	}
	if _, _, short := VoiceOutgrowsClip(&p, s); !short {
		t.Fatal("7.36 s line vs a 6 s clip")
	}
}

// Re-voicing a line (one voice or several speakers) that comes out longer
// than the scene's clip marks the clip outdated; a line that still fits
// leaves it alone.
func TestRevoiceStalesShortClip(t *testing.T) {
	needMedia(t)
	t.Parallel()
	texts := map[string]float64{
		"Довга нова репліка.":   9,   // 9 + 0.6 > 8
		"Коротка.":              3,   // fits
		"Перша частина. Друга.": 8.2, // the parts of a multi-voice scene, one voice: one request
	}
	tm := &ttsTimed{audio: map[string][]byte{}, at: map[string]time.Time{}}
	for text, sec := range texts {
		out := filepath.Join(t.TempDir(), "v.mp3")
		must(t, testFF.Run(t.Context(), "", "-f", "lavfi", "-i", "sine=frequency=330:sample_rate=44100", "-t", fmt.Sprint(sec),
			"-c:a", "libmp3lame", "-b:a", "64k", out))
		b, err := os.ReadFile(out)
		must(t, err)
		tm.audio[text] = b
	}
	ts := httptest.NewServer(tm)
	allow(ts)
	t.Cleanup(ts.Close)
	env := newEnv(t, func(c *config.Config) { c.TTS = config.TTS{Provider: "custom", BaseURL: ts.URL + "/tts"} })
	e := env.e
	p, err := e.Create("Переозвучка")
	must(t, err)
	dir := e.Dir(p.ID)
	clip := writeAsset(t, dir, "videos/clip.mp4", []byte("x"))
	voice := writeAsset(t, dir, "audio/old.mp3", []byte("x"))
	e.update(e.state(p.ID), func(p *Project) {
		p.Input.Voiceover, p.Input.MultiVoice = true, true
		p.Scenes = []Scene{
			{ID: "long", Narration: "Довга нова репліка.", Video: doneAsset(clip, 8), Audio: doneAsset(voice, 4), ClipLen: 8},
			{ID: "short", Narration: "Коротка.", Video: doneAsset(clip, 8), Audio: doneAsset(voice, 4)},
			{ID: "multi", Narration: "Перша частина. Друга.", Video: doneAsset(clip, 8), Audio: doneAsset(voice, 4),
				Parts: []Line{{Speaker: "narrator", Text: "Перша частина."}, {Speaker: "narrator", Text: "Друга."}}},
		}
	})
	for _, sid := range []string{"long", "short", "multi"} {
		must(t, e.Regen(p.ID, RegenVoice, sid, ""))
	}
	q := waitFor(t, e, p.ID, "three new voices", func(p Project) bool {
		for _, s := range p.Scenes {
			if s.Audio.Status != StatusDone || s.Audio.Path == voice {
				return false
			}
		}
		return true
	})
	for i, want := range []bool{true, false, true} {
		s := q.Scenes[i]
		if s.Video.Stale != want || s.Video.Status != StatusDone || s.Video.Path != clip {
			t.Errorf("scene %s (voice %.1f s): clip %+v, want stale %v", s.ID, s.Audio.Duration, s.Video, want)
		}
		if _, _, short := VoiceOutgrowsClip(&q, &s); short != want {
			t.Errorf("scene %s: VoiceOutgrowsClip %v", s.ID, short)
		}
	}
	if q.Scenes[0].ClipLen != 0 {
		t.Fatalf("the new line's clip length is chosen again: %d", q.Scenes[0].ClipLen)
	}
	if parts := q.Scenes[2].Parts; len(parts) != 2 || !(parts[1].End > parts[0].End) {
		t.Fatalf("multi-voice parts %+v", parts)
	}
	// Shooting the clip again clears the flag (finish); the stale clip is
	// counted for «Перезняти змінені», never re-shot by itself.
	if SceneState(q.Scenes[0]) != "stale" {
		t.Fatalf("scene state %q", SceneState(q.Scenes[0]))
	}
}

// The owner's Ukrainian lines run at ~2.0 words/s (the slowest tenth ~1.7):
// the on-camera budget uses 2.0 and the clip length the same estimate.
func TestDialogueBudgetTwoWordsPerSecond(t *testing.T) {
	if talkWPS != 2.0 {
		t.Fatalf("talkWPS %v", talkWPS)
	}
	for clip, want := range map[int]int{4: 5, 6: 9, 8: 13, 10: 17} {
		if got := DialogueWords(clip); got != want {
			t.Errorf("DialogueWords(%d) = %d, want %d", clip, got, want)
		}
	}
	ten := Line{Speaker: "a", Text: strings.TrimSpace(strings.Repeat("слово ", 10))}
	if got := DialogueSeconds([]Line{ten}); got != 5 {
		t.Fatalf("10 words: %v s", got)
	}
	// A full budget of words said at 2 w/s fits the clip it was written for.
	c := config.Default()
	c.Video = config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10, Dynamic: true}
	for _, clip := range []int{6, 8, 10} {
		text := strings.TrimSpace(strings.Repeat("так ", DialogueWords(clip)))
		s := &Scene{Lines: []Line{{Speaker: "a", Text: text}}}
		if got := sceneClip(c, s); got > clip {
			t.Errorf("a %d-s budget (%d words) needs a %d-s clip", clip, DialogueWords(clip), got)
		}
		// The screens' estimate is the clip length's own.
		need := DialogueClipSeconds(s.Lines)
		if want := (providers.VideoCaps{Lengths: clipLengths(c)}).Cover(need-0.05, videoSeconds(c)); sceneClip(c, s) != want {
			t.Errorf("clip %d: DialogueClipSeconds %v → %d, sceneClip %d", clip, need, want, sceneClip(c, s))
		}
	}
	if DialogueClipSeconds(nil) != 0 {
		t.Fatal("no lines")
	}
	// The plan's prompt asks for the same rate.
	p := planWith(func(in *Input) { in.AudioMode, in.Voiceover = AudioInVideo, false })
	sys, _ := biblePrompt(p, 3, DialogueWords(8), nil, 8)
	if !strings.Contains(sys, "at most 13 words in total") || !strings.Contains(sys, "about 2 words per second") || strings.Contains(sys, "2.5 words") {
		t.Fatalf("bible prompt:\n%s", sys)
	}
}
