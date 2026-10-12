package studio

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"ytcartell/internal/media"
)

// writeAsset puts data at rel inside the project folder and returns rel.
func writeAsset(t *testing.T, dir, rel string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, data, 0o644))
	return rel
}

func doneAsset(rel string, dur float64) Asset {
	return Asset{Path: rel, Status: StatusDone, Duration: dur}
}

var reCueStart = regexp.MustCompile(`(?m)^\d+\n(\d\d):(\d\d):(\d\d),(\d\d\d) --> `)

// The timeline the editor draws is exactly what the montage renders.
func TestTimelineMatchesMontage(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Таймлайн")
	must(t, err)
	dir := e.Dir(p.ID)
	clip := writeAsset(t, dir, "videos/a.mp4", testClip)
	img := writeAsset(t, dir, "images/f.jpg", testImage)
	voice := writeAsset(t, dir, "audio/v1.mp3", testVoice)
	must(t, os.MkdirAll(filepath.Join(dir, "audio"), 0o755))
	must(t, testFF.Run(context.Background(), "", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=44100:duration=3",
		"-c:a", "libmp3lame", "-b:a", "64k", filepath.Join(dir, "audio", "v2.mp3")))

	e.update(e.state(p.ID), func(p *Project) {
		p.Input.Voiceover = true
		m := &p.Montage
		m.Transition, m.TransitionDuration, m.Effect, m.Filter = "dissolve", 0.5, "panright", "warm"
		m.Subtitles, m.SubtitleStyle = "srt", SubtitleStyle{Preset: "youtube", Uppercase: true}
		p.Scenes = []Scene{
			{ID: "s1", Narration: "Перша сцена.", Video: doneAsset(clip, 2), Frame: doneAsset(img, 0), Audio: doneAsset(voice, 1.5),
				TrimIn: 0.25, Transition: "wipeleft"},
			// Voice length unknown (older project): the montage probes and stores it.
			{ID: "s2", Narration: "Друга сцена без кліпу.", Frame: doneAsset(img, 0), Audio: doneAsset("audio/v2.mp3", 0)},
			{ID: "s3", Narration: "Ні кадру, ні кліпу — пропускається.", Frame: EmptyAsset(), Video: EmptyAsset()},
			{ID: "s4", Narration: "Четверта сцена.", Video: doneAsset(clip, 2), Effect: "zoomin", Transition: "fade"},
		}
	})
	p, _ = e.Get(p.ID)
	tl := e.Timeline(p)
	if len(tl) != 3 || tl[0].SceneID != "s1" || tl[1].SceneID != "s2" || tl[2].SceneID != "s4" {
		t.Fatalf("timeline %+v", tl)
	}
	if tl[0].Transition != "wipeleft" || tl[1].Transition != "dissolve" || tl[2].Transition != "" {
		t.Fatalf("transitions %+v", tl)
	}
	if tl[0].Effect != "none" || tl[1].Effect != "panright" || tl[2].Effect != "zoomin" || !tl[0].HasClip || tl[1].HasClip {
		t.Fatalf("effects %+v", tl)
	}
	if tl[0].Overlap != 0.5 || tl[1].Overlap != 0.5 || tl[2].Overlap != 0 || tl[0].TrimIn != 0.25 || tl[0].ClipDuration != 1.75 {
		t.Fatalf("overlaps/trims %+v", tl)
	}
	if tl[1].VoiceDuration != 0 || tl[1].Start != tl[0].Start+tl[0].Duration-0.5 {
		t.Fatalf("s2 before the montage: %+v", tl[1])
	}

	// Scenes that are not ready are never filled in silently: the montage
	// is refused until the user chooses (gaps.go) …
	err = e.Run(p.ID, StageMontage, false, "")
	if m, ok := IsMissingScenes(err); !ok || len(m.Gaps) != 3 || m.Gaps[0].Number != 2 || !m.Gaps[1].NoFrame || !m.Gaps[2].NoVoice {
		t.Fatalf("montage with missing scenes: %v", err)
	}
	// … «З кадром замість відео»: s2 shows its frame, s3 (no frame) is left
	// out, s4 plays without its line — and the result says so, scene by scene.
	must(t, e.RunWith(p.ID, StageMontage, RunOptions{Missing: MissingStill}))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if g := p.Montage.Gaps; len(g) != 3 || g[0].Action != GapStill || g[1].Action != GapSkipped || g[2].Action != GapSilent {
		t.Fatalf("recorded gaps %+v", g)
	}
	for _, want := range []string{"кадр замість відео: сцена 2 (кліп ще не знято)", "пропущено: сцена 3 (кадр ще не намальовано", "без голосу: сцена 4"} {
		if !strings.Contains(p.Job.Message, want) {
			t.Fatalf("job message %q lacks %q", p.Job.Message, want)
		}
	}
	if d := p.Scenes[1].Audio.Duration; d < 2.9 || d > 3.2 {
		t.Fatalf("probed voice length not stored: %v", d)
	}
	tl = e.Timeline(p)
	if tl[1].VoiceDuration != p.Scenes[1].Audio.Duration {
		t.Fatalf("timeline voice %v", tl[1].VoiceDuration)
	}
	total := TimelineDuration(tl)
	out := p.Montage.Output
	if out.Duration != total {
		t.Fatalf("rendered %.4f s, timeline %.4f s", out.Duration, total)
	}
	info, err := testFF.Probe(context.Background(), e.Abs(p.ID, out.Path))
	must(t, err)
	if math.Abs(info.Duration-total) > 0.05 || info.Width != 320 || info.Height != 180 {
		t.Fatalf("final mp4 %+v, timeline %.3f s", info, total)
	}
	// Subtitles start with the voices, in capitals (Uppercase applies to the file).
	srt, err := os.ReadFile(e.Abs(p.ID, p.Montage.SRTPath))
	must(t, err)
	if !strings.Contains(string(srt), "ПЕРША СЦЕНА.") {
		t.Fatalf("srt:\n%s", srt)
	}
	m := reCueStart.FindAllStringSubmatch(string(srt), -1)
	if len(m) != 3 {
		t.Fatalf("cues:\n%s", srt)
	}
	for i, c := range m {
		var h, mi, s, ms float64
		for j, dst := range []*float64{&h, &mi, &s, &ms} {
			for _, d := range c[j+1] {
				*dst = *dst*10 + float64(d-'0')
			}
		}
		if at := h*3600 + mi*60 + s + ms/1000; math.Abs(at-tl[i].VoiceStart) > 0.001 {
			t.Fatalf("cue %d at %.3f, voice at %.3f", i+1, at, tl[i].VoiceStart)
		}
	}
}

// The project maps onto the montage settings: defaults, per-scene controls,
// missing files left out, voice only with voiceover on.
func TestMontageOptionsMapping(t *testing.T) {
	dir := t.TempDir()
	img := writeAsset(t, dir, "images/f.jpg", []byte("x"))
	vid := writeAsset(t, dir, "videos/a.mp4", []byte("x"))
	aud := writeAsset(t, dir, "audio/v.mp3", []byte("x"))
	p := Project{Input: DefaultInput(), Montage: DefaultMontage()}
	p.Montage.Transition = ""
	p.Montage.MusicPath = "user/music.mp3"
	p.Montage.SubtitleStyle = SubtitleStyle{Preset: "shorts", Size: 70, Position: "top", Color: "#FF0000", Outline: true, Box: true, Uppercase: true}
	p.Scenes = []Scene{
		{ID: "a", Narration: "A", Video: doneAsset(vid, 4), Frame: doneAsset(img, 0), Audio: doneAsset(aud, 2), TrimIn: 1, TrimOut: 0.5, Transition: "radial", Effect: "shake"},
		{ID: "b", Video: doneAsset("videos/missing.mp4", 8), Frame: doneAsset(img, 0)},
	}
	o := montageOptions(&p, dir)
	if o.Transition != media.DefaultTransition || o.TransitionDuration != 0 || o.DefaultEffect != "" || o.Filter != "" || o.Subtitles != "srt" {
		t.Fatalf("defaults %+v", o)
	}
	if o.OutDir != filepath.Join(dir, "montage") || o.MusicPath != filepath.Join(dir, "user", "music.mp3") || o.Aspect != "16:9" {
		t.Fatalf("paths %+v", o)
	}
	want := media.SubtitleStyle{Preset: "shorts", Size: 70, Position: "top", Color: "#FF0000", Outline: true, Box: true, Uppercase: true}
	if o.SubtitleStyle != want {
		t.Fatalf("style %+v", o.SubtitleStyle)
	}
	a, b := o.Segments[0], o.Segments[1]
	if a.Video != filepath.Join(dir, "videos", "a.mp4") || a.VideoDur != 4 || a.Voice == "" || a.VoiceDur != 2 ||
		a.TrimIn != 1 || a.TrimOut != 0.5 || a.Transition != "radial" || a.Effect != "shake" || a.Narration != "A" {
		t.Fatalf("scene a %+v", a)
	}
	if b.Video != "" || b.VideoDur != 0 || b.Frame == "" {
		t.Fatalf("missing clip must fall back to the frame: %+v", b)
	}
	if a.OwnVoice {
		t.Fatal("a TTS voice is not an own recording")
	}
	// A slice of the user's own recording keeps its built-in lead/tail.
	p.Scenes[0].AudioSource = AudioUpload
	if a := montageOptions(&p, dir).Segments[0]; !a.OwnVoice || a.VoiceLead != speechLead || a.VoiceTail != speechTail {
		t.Fatalf("own voice %+v", a)
	}
	p.Scenes[0].AudioSource = ""
	p.Input.Voiceover = false
	if o := montageOptions(&p, dir); o.Segments[0].Voice != "" {
		t.Fatal("voice with voiceover off")
	}
	// Timeline without any picture is empty.
	if tl := timelineOf(Project{Scenes: []Scene{{ID: "x"}}}, dir); len(tl) != 0 || TimelineDuration(tl) != 0 {
		t.Fatalf("%+v", tl)
	}
}

// The editor's voice moves and trims reach the timeline and the render: the
// timeline shows the effective line (and the raw file for trim handles), the
// film matches it, moves never shift the picture, subtitles follow the lines.
func TestTimelineVoiceEdits(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Голос у редакторі")
	must(t, err)
	dir := e.Dir(p.ID)
	clip := writeAsset(t, dir, "videos/a.mp4", testClip)
	voice := writeAsset(t, dir, "audio/v.mp3", testVoice)
	scenes := func(edit bool) []Scene {
		ss := []Scene{
			{ID: "s1", Narration: "Перша.", Video: doneAsset(clip, 2), Audio: doneAsset(voice, 1.5)},
			{ID: "s2", Narration: "Друга.", Video: doneAsset(clip, 2), Audio: doneAsset(voice, 1.5)},
			{ID: "s3", Narration: "Третя.", Video: doneAsset(clip, 2), Audio: doneAsset(voice, 1.5)},
		}
		if edit {
			ss[0].VoiceTrimIn, ss[0].VoiceTrimOut = 0.2, 0.3 // 1.0 s left
			ss[1].VoiceShift = 0.8                           // later, under scene 3
			ss[2].VoiceShift = 30                            // far past the end: clamped
		}
		return ss
	}
	e.update(e.state(p.ID), func(p *Project) {
		p.Input.Voiceover = true
		p.Montage.Transition, p.Montage.Subtitles = "dissolve", "srt"
		p.Scenes = scenes(false)
	})
	p, _ = e.Get(p.ID)
	before := e.Timeline(p)
	e.update(e.state(p.ID), func(p *Project) { p.Scenes = scenes(true) })
	p, _ = e.Get(p.ID)
	tl := e.Timeline(p)
	total := TimelineDuration(tl)

	a, b, c := tl[0], tl[1], tl[2]
	if a.VoiceRawDuration != 1.5 || a.VoiceTrimIn != 0.2 || a.VoiceTrimOut != 0.3 || math.Abs(a.VoiceDuration-1.0) > 1e-9 ||
		a.VoiceStart != a.VoiceDefaultStart || math.Abs(a.VoiceEnd-(a.VoiceStart+1.0)) > 1e-9 {
		t.Fatalf("trimmed line %+v", a)
	}
	if math.Abs(b.VoiceStart-(b.VoiceDefaultStart+0.8)) > 1e-4 || math.Abs(b.VoiceShift-0.8) > 1e-4 || b.VoiceEnd <= c.Start {
		t.Fatalf("moved line %+v (scene 3 starts %.3f)", b, c.Start)
	}
	if math.Abs(c.VoiceEnd-total) > 1e-4 || c.VoiceShift >= 30 || c.VoiceShift <= 0 {
		t.Fatalf("clamped line %+v (film %.3f)", c, total)
	}
	// Moves keep the picture; the trim shortened only scene 1 (voice-driven).
	for i := 1; i < 3; i++ {
		if tl[i].Duration != before[i].Duration {
			t.Fatalf("scene %d changed length: %v → %v", i+1, before[i].Duration, tl[i].Duration)
		}
	}
	if tl[0].Duration >= before[0].Duration {
		t.Fatalf("the trimmed voice-driven scene should get shorter: %v → %v", before[0].Duration, tl[0].Duration)
	}

	must(t, e.Run(p.ID, StageMontage, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if out := p.Montage.Output; out.Duration != total {
		t.Fatalf("rendered %.4f s, timeline %.4f s", out.Duration, total)
	}
	srt, err := os.ReadFile(e.Abs(p.ID, p.Montage.SRTPath))
	must(t, err)
	m := reCueStart.FindAllStringSubmatch(string(srt), -1)
	if len(m) != 3 {
		t.Fatalf("cues:\n%s", srt)
	}
	for i, cue := range m {
		var h, mi, s, ms float64
		for j, dst := range []*float64{&h, &mi, &s, &ms} {
			for _, d := range cue[j+1] {
				*dst = *dst*10 + float64(d-'0')
			}
		}
		if at := h*3600 + mi*60 + s + ms/1000; math.Abs(at-tl[i].VoiceStart) > 0.001 {
			t.Fatalf("cue %d at %.3f, line at %.3f", i+1, at, tl[i].VoiceStart)
		}
	}
}
