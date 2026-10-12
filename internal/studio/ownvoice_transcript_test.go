package studio

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/stt"
)

// ---- a ready transcript with timecodes (SetTranscriptFile) ---------------------------------------

// fairyTaleSRT is the fairy tale as subtitles, one cue per sentence, timed
// like speechOf (shifted by shift seconds).
func fairyTaleSRT(shift float64) (string, stt.Transcript) {
	tr := speechOf(fairyTale, 0.32, 0.5, false)
	clock := func(sec float64) string {
		ms := int(math.Round(sec * 1000))
		return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	}
	var b strings.Builder
	k := 0
	for i, s := range fairyTale {
		ws := tr.Words[k : k+len(strings.Fields(s))]
		k += len(ws)
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, clock(ws[0].Start+shift), clock(ws[len(ws)-1].End+shift), s)
	}
	return b.String(), tr
}

func writeTemp(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	must(t, os.WriteFile(path, []byte(data), 0o644))
	return path
}

// TestVoiceTranscriptFile: a transcript with timecodes attached to the
// recording replaces speech recognition — checked against the recording,
// planned from, re-read by Transcribe, kept or dropped with the recording.
func TestVoiceTranscriptFile(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, sm, _ := voiceEnv(t, nil)
	e := env.e
	srt, tale := fairyTaleSRT(0)
	srtPath := writeTemp(t, "Казка.uk.srt", srt)
	if strings.Join(TranscriptExts(), " ") != "srt vtt json txt" {
		t.Fatalf("exts %v", TranscriptExts())
	}

	// Without a recording there is nothing to time.
	plain, err := e.Create("Без запису")
	must(t, err)
	if err := e.SetTranscriptFile(plain.ID, srtPath); err == nil || !strings.Contains(err.Error(), "Спершу додай запис голосу") {
		t.Fatalf("no recording: %v", err)
	}
	if err := e.SetTranscriptFile("nope", srtPath); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}

	p, err := e.CreateFromVoice("", voiceFile(t, "казка.mp3", 26), "")
	must(t, err)
	id := p.ID

	// Refused files leave the project as it was.
	shifted, _ := fairyTaleSRT(10) // an intro before the voice: 10 s too late
	for _, tc := range []struct{ path, want string }{
		{voiceFile(t, "голос.mp3", 2), "Це запис, а не текст"},
		{writeTemp(t, "текст.docx", "PK…"), "Документи Word і PDF"},
		{writeTemp(t, "текст.txt", strings.Join(fairyTale, "\n")), "немає таймкодів"},
		{writeTemp(t, "зсув.srt", shifted), "Таймкоди не збігаються із записом: фрази в тексті доходять до 0:32, а запис триває 0:26"},
		{filepath.Join(t.TempDir(), "нема.srt"), "не знайдено"},
	} {
		if err := e.SetTranscriptFile(id, tc.path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v, want …%s…", filepath.Base(tc.path), err, tc.want)
		}
	}
	st := e.state(id)
	e.update(st, func(p *Project) { p.Job = &Job{Kind: JobPlan, Status: JobRunning} })
	if err := e.SetTranscriptFile(id, srtPath); !errors.Is(err, ErrBusy) {
		t.Fatalf("while a job runs: %v", err)
	}
	e.update(st, func(p *Project) { p.Job = nil })
	if q, _ := e.Get(id); q.Input.TranscriptFile != "" || q.Input.Text != "" {
		t.Fatalf("refused files changed the project: %+v", q.Input)
	}

	// ---- attach
	must(t, e.SetTranscriptFile(id, srtPath))
	p, _ = e.Get(id)
	in := p.Input
	if !strings.HasPrefix(in.TranscriptFile, "audio/transcript-kazka-") || !strings.HasSuffix(in.TranscriptFile, ".uk.srt") ||
		in.Text != tale.Text || in.Mode != ModeVoice || !in.Voiceover {
		t.Fatalf("attached: %+v", in)
	}
	fileExists(t, e.Abs(id, in.TranscriptFile))
	tr, ok := e.VoiceTranscript(id)
	if !ok || len(tr.Words) != len(tale.Words) || len(tr.Segments) != len(fairyTale) || tr.Language != "uk" || math.Abs(tr.Duration-26) > 0.2 {
		t.Fatalf("transcript %v: %d words, %d phrases, %q, %.2f s", ok, len(tr.Words), len(tr.Segments), tr.Language, tr.Duration)
	}
	must(t, e.Edit(id, func(p *Project) { p.Input.TranscriptFile = "audio/other.srt" })) // engine-owned
	if q, _ := e.Get(id); q.Input.TranscriptFile != in.TranscriptFile {
		t.Fatalf("transcript file edited: %q", q.Input.TranscriptFile)
	}

	// ---- the plan cuts the scenes by the file's timings: no speech-to-text
	must(t, e.Run(id, StagePlan, false, ""))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	want := chunkSpeech(tr, voiceLimits(env.store.Get()))
	if sm.count() != 0 || len(p.Scenes) != len(want) || len(want) < 3 {
		t.Fatalf("plan: %d stt calls, %d scenes (want %d)", sm.count(), len(p.Scenes), len(want))
	}
	for i, s := range p.Scenes {
		if c := want[i]; s.AudioSource != AudioUpload || s.VoiceStart != c.Start || s.VoiceEnd != c.End || s.Narration != c.Text {
			t.Fatalf("scene %d: %+v, want %+v", i, s, c)
		}
	}

	// Transcribe reads the file again; so does a plan that lost the sidecar.
	must(t, e.Transcribe(id))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Message != "Текст із таймкодами прочитано · 53 слова · 0:26" || len(tale.Words) != 53 || sm.count() != 0 {
		t.Fatalf("transcribe %+v, %d stt calls", p.Job, sm.count())
	}
	must(t, os.Remove(e.Abs(id, transcriptRel)))
	must(t, e.Run(id, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, id))
	if sm.count() != 0 {
		t.Fatalf("re-plan recognised the speech (%d calls)", sm.count())
	}
	fileExists(t, e.Abs(id, transcriptRel))

	// ---- a new recording as long keeps the transcript, another length drops it
	must(t, e.SetVoiceFile(id, voiceFile(t, "казка.wav", 26)))
	q, _ := e.Get(id)
	if q.Input.TranscriptFile != in.TranscriptFile || q.Input.Text != tale.Text {
		t.Fatalf("same-length recording lost the transcript: %+v", q.Input)
	}
	if tr2, ok := e.VoiceTranscript(id); !ok || len(tr2.Words) != len(tale.Words) {
		t.Fatal("transcript of the new recording")
	}
	if vt, ok := readSidecar(e.Dir(id)); !ok || vt.VoiceFile != q.Input.VoiceFile || vt.TranscriptFile != in.TranscriptFile || vt.Provider != "file" || vt.Model != "srt" {
		t.Fatalf("sidecar %+v", vt)
	}
	must(t, e.SetVoiceFile(id, voiceFile(t, "коротка.mp3", 15)))
	q, _ = e.Get(id)
	if q.Input.TranscriptFile != "" || q.Input.Text != "" {
		t.Fatalf("a shorter recording kept the transcript: %+v", q.Input)
	}
	if _, err := os.Stat(e.Abs(id, in.TranscriptFile)); !os.IsNotExist(err) {
		t.Fatal("the dropped transcript's copy stays")
	}
	if err := e.SetTranscriptFile(id, srtPath); err == nil || !strings.Contains(err.Error(), "Таймкоди не збігаються") {
		t.Fatalf("the fairy tale on a 15 s recording: %v", err)
	}

	// ---- removing it: speech-to-text takes over
	must(t, e.SetVoiceFile(id, voiceFile(t, "казка3.mp3", 26)))
	must(t, e.SetTranscriptFile(id, srtPath))
	q, _ = e.Get(id)
	copyRel := q.Input.TranscriptFile
	must(t, e.SetTranscriptFile(id, ""))
	q, _ = e.Get(id)
	if q.Input.TranscriptFile != "" || q.Input.Text != "" {
		t.Fatalf("after removal: %+v", q.Input)
	}
	if _, err := os.Stat(e.Abs(id, copyRel)); !os.IsNotExist(err) {
		t.Fatal("the removed transcript's copy stays")
	}
	if _, ok := e.VoiceTranscript(id); ok {
		t.Fatal("transcript after removal")
	}
	must(t, e.SetTranscriptFile(id, "")) // nothing attached: no-op
	must(t, e.Transcribe(id))
	p = waitJob(t, e, id)
	mustJobDone(t, p)
	if sm.count() != 1 || !strings.HasPrefix(p.Job.Message, "Мову розпізнано") {
		t.Fatalf("after removal the speech is recognised: %d calls, %q", sm.count(), p.Job.Message)
	}

	// Attaching over a recognised transcript replaces it; removing the
	// recording drops the transcript too.
	must(t, e.SetTranscriptFile(id, srtPath))
	if vt, ok := readSidecar(e.Dir(id)); !ok || vt.Provider != "file" {
		t.Fatalf("sidecar after attaching %+v", vt)
	}
	must(t, e.SetVoiceFile(id, ""))
	if q, _ = e.Get(id); q.Input.TranscriptFile != "" || q.Input.VoiceFile != "" {
		t.Fatalf("after removing the recording: %+v", q.Input)
	}
}

// TestVoiceAutopilotWithTranscript: the autopilot from a recording and its
// transcript runs end to end without speech-to-text.
func TestVoiceAutopilotWithTranscript(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, sm, _ := voiceEnv(t, func(c *config.Config) {
		c.Autopilot = config.Autopilot{MakeCover: false, Retries: 0, ContinueOnError: false}
	})
	e := env.e
	srt, tale := fairyTaleSRT(0)
	voice := voiceFile(t, "казка.wav", 26)

	// A transcript that does not match: nothing starts, nothing is left behind.
	bad, _ := fairyTaleSRT(30)
	if _, err := e.StartVoiceAutopilotWith("Моя казка", voice, writeTemp(t, "bad.srt", bad), ""); err == nil || len(e.List()) != 0 {
		t.Fatalf("a mismatching transcript: %v, %d projects", err, len(e.List()))
	}

	id, err := e.StartVoiceAutopilotWith("Моя казка", voice, writeTemp(t, "казка.srt", srt), "")
	must(t, err)
	p := waitJob(t, e, id)
	mustJobDone(t, p)
	if p.Job.Kind != JobAutopilot || !strings.HasPrefix(p.Job.Message, "Автопілот завершено · Фінальне відео готове") || sm.count() != 0 {
		t.Fatalf("autopilot %+v, stt calls %d", p.Job, sm.count())
	}
	if p.Title != "Моя казка" || p.Input.Text != tale.Text || p.Input.TranscriptFile == "" || len(p.Scenes) < 3 {
		t.Fatalf("project %q %q %q %d scenes", p.Title, p.Input.Text, p.Input.TranscriptFile, len(p.Scenes))
	}
	for i, s := range p.Scenes {
		if s.AudioSource != AudioUpload || s.Audio.Status != StatusDone || s.Video.Status != StatusDone {
			t.Fatalf("scene %d: %s %+v %+v", i, s.AudioSource, s.Audio, s.Video)
		}
	}
	fileExists(t, e.Abs(id, p.Montage.Output.Path))
}

func TestFitTranscriptAndNormalize(t *testing.T) {
	seg := func(text string, a, b float64) stt.Segment { return stt.Segment{Text: text, Start: a, End: b} }
	word := func(text string, a, b float64) stt.Word { return stt.Word{Text: text, Start: a, End: b} }
	// The last line lingers on screen past the end: fitted to the recording.
	tr := stt.Transcript{
		Words:    []stt.Word{word("Раз", 1, 2), word("два", 8, 9), word("три", 9.5, 13)},
		Segments: []stt.Segment{seg("Раз", 1, 2), seg("два три", 8, 13)},
	}
	must(t, fitTranscript(&tr, 10))
	if tr.Duration != 10 || tr.Words[2] != word("три", 9.5, 10) || tr.Segments[1] != seg("два три", 8, 10) {
		t.Fatalf("fitted %+v", tr)
	}
	// A phrase starting — or one before the last ending — past the end + 2 s.
	for _, segs := range [][]stt.Segment{
		{seg("a", 1, 2), seg("b", 12.5, 13)},
		{seg("a", 1, 12.5), seg("b", 12.5, 13)},
	} {
		tr := stt.Transcript{Words: []stt.Word{word("a", 1, 2)}, Segments: segs}
		if err := fitTranscript(&tr, 10); err == nil || !strings.Contains(err.Error(), "0:13, а запис триває 0:10") {
			t.Fatalf("%+v: %v", segs, err)
		}
	}
	tr = stt.Transcript{Words: []stt.Word{word("a", 11.9, 12.5)}, Segments: []stt.Segment{seg("a", 11.9, 12.5)}}
	must(t, fitTranscript(&tr, 10)) // within the 2 s slack
	if tr.Words[0] != word("a", 10, 10) {
		t.Fatalf("clamped %+v", tr.Words)
	}

	for base, want := range map[string]string{
		"Казка.uk.srt": "transcript-kazka-*.uk.srt", "Talk.en-US.VTT": "transcript-talk-*.en-us.vtt",
		"notes.v2.txt": "transcript-notesv2-*.txt", "без розширення": "transcript-bez_rozshyrennia-*.txt",
	} {
		got := transcriptCopyName(base)
		pre, suf, _ := strings.Cut(want, "*")
		if !strings.HasPrefix(got, pre) || !strings.HasSuffix(got, suf) || len(got) != len(want)-1+6 {
			t.Errorf("copy name of %q: %q, want %q", base, got, want)
		}
	}

	for _, tc := range []struct{ voice, file, want string }{
		{"audio/v.mp3", "audio/transcript-a-1.srt", "audio/transcript-a-1.srt"},
		{"", "audio/transcript-a-1.srt", ""}, // a translation drops the recording
		{"audio/v.mp3", "../outside.srt", ""},
		{"audio/v.mp3", "C:/Users/x.srt", ""},
	} {
		p := &Project{Input: Input{Mode: ModeVoice, VoiceFile: tc.voice, TranscriptFile: tc.file}}
		normalizeVoice(p)
		if p.Input.TranscriptFile != tc.want {
			t.Errorf("normalize %q/%q → %q", tc.voice, tc.file, p.Input.TranscriptFile)
		}
	}
}

// TestVoiceTranscribeLocal: with speech recognition on this computer
// (whisper.cpp) the recording goes to the local program, never online.
func TestVoiceTranscribeLocal(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, sm, _ := voiceEnv(t, func(c *config.Config) {
		c.STT.Provider = stt.ProviderLocal
		c.STT.LocalBin = filepath.Join(t.TempDir(), "no-whisper", "whisper-cli.exe") // not installed
	})
	e := env.e
	p, err := e.CreateFromVoice("", voiceFile(t, "казка.mp3", 5), "")
	must(t, err)
	must(t, e.Transcribe(p.ID))
	p = waitJob(t, e, p.ID)
	if p.Job.Status != JobError || !strings.Contains(strings.ToLower(p.Job.Error), "whisper") || sm.count() != 0 {
		t.Fatalf("local transcription: %+v, %d online calls", p.Job, sm.count())
	}
}
