package studio

// "Своя озвучка" (Input.Mode == ModeVoice): the user's own recorded
// voice-over drives the film.
//
//	upload     SetVoiceFile / CreateFromVoice copy the recording into
//	           <project>/audio/ (duration probed with ffprobe)
//	transcribe speech-to-text with word timings (internal/stt), kept as the
//	           sidecar audio/transcript.json; the plain text lands in Input.Text
//	           — or, when the user attached a transcript with timecodes
//	           (SetTranscriptFile: SRT, VTT, a service's JSON, TXT; a copy
//	           kept as Input.TranscriptFile), that file is read into the same
//	           sidecar and speech-to-text is never called for the recording
//	plan       runPlan transcribes first when needed, then chunkSpeech splits
//	           the words into scenes aligned to the speech (sentence ends and
//	           pauses, never inside a word, each scene as long as a clip the
//	           video model can render) and the text model plans the pictures
//	           for the FIXED narration chunks (planner.go, script machinery)
//	voices     genVoice cuts each scene's [VoiceStart, VoiceEnd] out of the
//	           recording with FFmpeg (AudioSource "upload") instead of TTS
//
// A scene the user inserts later has no slice (AudioSource "") and is voiced
// by TTS as usual.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
	"ytcartell/internal/stt"
)

const (
	// AudioUpload is Scene.AudioSource of a slice of the user's own recording.
	AudioUpload = "upload"
	// JobTranscribe is the speech-to-text stage of ModeVoice (Transcribe).
	JobTranscribe JobKind = "transcribe"

	// transcriptRel is the transcript sidecar of the current recording.
	transcriptRel = "audio/transcript.json"
	maxVoiceFile  = 2 << 30

	speechLead  = 0.15  // seconds kept before a scene's first word
	speechTail  = 0.25  // seconds kept after its last word
	speechPause = 0.3   // a gap this long is a natural place to cut
	voiceFade   = 0.012 // fade in/out of a slice, so a cut never clicks
	// minVoiceClip is the shortest clip the video models render (Veo 4 s).
	minVoiceClip = 4.0
)

func init() {
	jobLabels[JobTranscribe] = "Розпізнавання мови"
	jobRestarts[JobTranscribe] = (*Engine).Transcribe // «Повторити» in the queue
	jobAssets[JobTranscribe] = nil                    // makes no scene assets
}

var errNoVoice = errors.New("Спершу завантаж свою озвучку (файл із голосом)")

// voiceExts are the recordings SetVoiceFile accepts: audio, or a video whose
// sound is the voice-over.
var voiceExts = map[string]bool{
	".mp3": true, ".wav": true, ".m4a": true, ".aac": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true, ".wma": true,
	".mp4": true, ".m4v": true, ".mov": true, ".webm": true, ".mkv": true,
}

// VoiceExts lists the file extensions SetVoiceFile accepts (for the file picker).
func VoiceExts() []string {
	return []string{"mp3", "wav", "m4a", "aac", "ogg", "opus", "flac", "wma", "mp4", "mov", "webm", "mkv"}
}

// ---- the recording ------------------------------------------------------------------------

// SetVoiceFile copies the user's own voice-over into the project (audio/),
// switches the project to ModeVoice (with voice-over on) and sets the target
// duration to the recording's length. "" removes the recording. Refused while
// a job runs on the project. Scenes cut from a previous recording keep their
// voices, flagged as outdated (plan again to cut the new one). A transcript
// attached to the previous recording (SetTranscriptFile) stays only when it
// still matches the new one — about as long (±2 s) and its timecodes fit —
// e.g. the same speech exported again; otherwise it is removed too.
func (e *Engine) SetVoiceFile(id, srcPath string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var busy error
	var prev Input
	if !e.read(st, func(p *Project) bool {
		if p.Job != nil && p.Job.Status == JobRunning {
			busy = &busyError{p.Job.Kind}
		}
		prev = p.Input
		return true
	}) {
		return ErrNotFound
	}
	if busy != nil {
		return busy
	}
	if strings.TrimSpace(srcPath) == "" {
		fromFile := attachedText(st.dir, prev)
		var drop []string
		if !e.update(st, func(p *Project) {
			drop = append(drop, p.Input.VoiceFile, p.Input.TranscriptFile)
			p.Input.VoiceFile, p.Input.VoiceFileName = "", ""
			dropAttached(&p.Input, fromFile)
			staleUploads(p)
		}) {
			return ErrNotFound
		}
		e.removeFiles(st, append(drop, transcriptRel))
		return nil
	}

	ext := strings.ToLower(filepath.Ext(srcPath))
	if !voiceExts[ext] {
		return errors.New("Це не аудіофайл (потрібен MP3, WAV, M4A, OGG, FLAC або відео MP4/MOV/WEBM зі звуком)")
	}
	info, err := os.Stat(srcPath)
	if err != nil || info.IsDir() {
		return fmt.Errorf("файл не знайдено: %s", srcPath)
	}
	if info.Size() > maxVoiceFile {
		return errors.New("Файл завеликий (максимум 2 ГБ)")
	}
	ff := e.ffNow()
	if ff.FFprobe == "" {
		if ff.FFmpeg == "" {
			return media.ErrNotFound
		}
		return media.ErrNoProbe
	}
	pi, err := ff.Probe(st.ctx, srcPath)
	switch {
	case err != nil:
		return fmt.Errorf("Не вдалося прочитати запис: %s", errText(err))
	case !pi.HasAudio:
		return errors.New("У файлі немає звуку")
	case pi.Duration < 1:
		return errors.New("Запис закороткий — потрібна хоча б секунда мовлення")
	}
	if most := maxVoiceSeconds(e.cfg.Get()); pi.Duration > most {
		return fmt.Errorf("Запис задовгий: фільм може мати до %d сцен, тобто до %s мовлення. Скороти запис або розбий його на кілька проєктів", maxScenes, voiceClock(most))
	}

	// The transcript attached to the previous recording, when it still matches.
	var keep *stt.Transcript
	fromFile := ""
	if prev.TranscriptFile != "" {
		fromFile = attachedText(st.dir, prev)
		if tr, ok := e.transcriptStillFits(st, prev, pi.Duration); ok {
			keep = &tr
		}
	}

	base := filepath.Base(srcPath)
	stem := slugTag(strings.TrimSuffix(base, filepath.Ext(base))) // readable latin, ≤ 24 chars
	rel := "audio/voiceover-" + stem + "-" + newID(6) + ext
	if err := e.copyIn(st, srcPath, rel); err != nil {
		return fmt.Errorf("не вдалося скопіювати запис: %w", err)
	}
	var drop []string
	kept := ""
	if !e.update(st, func(p *Project) {
		drop = append(drop, p.Input.VoiceFile)
		in := &p.Input
		in.VoiceFile, in.VoiceFileName = rel, clipRunes(base, 200)
		in.Mode, in.Voiceover = ModeVoice, true
		in.TargetSeconds = max(1, int(math.Round(pi.Duration)))
		if keep != nil && in.TranscriptFile == prev.TranscriptFile {
			kept = in.TranscriptFile
		} else if in.TranscriptFile != "" {
			drop = append(drop, in.TranscriptFile)
			dropAttached(in, fromFile)
		}
		staleUploads(p)
	}) {
		e.removeFiles(st, []string{rel})
		return ErrNotFound
	}
	if kept == "" {
		drop = append(drop, transcriptRel)
	}
	e.removeFiles(st, drop)
	if kept != "" {
		e.saveFileTranscript(st, rel, kept, *keep)
	}
	return nil
}

// transcriptStillFits: the transcript attached to the previous recording
// (prev) also matches a new recording of dur seconds — about as long (±2 s)
// and its timecodes fit; the transcript is returned fitted to it.
func (e *Engine) transcriptStillFits(st *proj, prev Input, dur float64) (stt.Transcript, bool) {
	old, err := e.voiceDuration(st.ctx, st, prev.VoiceFile)
	if err != nil || math.Abs(old-dur) > transcriptSlack {
		return stt.Transcript{}, false
	}
	tr, err := readAttachedFile(st.dir, prev.TranscriptFile)
	if err != nil || fitTranscript(&tr, dur) != nil {
		return stt.Transcript{}, false
	}
	return tr, true
}

// copyIn copies a user file into the project folder as rel (never
// resurrecting a deleted project).
func (e *Engine) copyIn(st *proj, src, rel string) error {
	st.fmu.RLock()
	defer st.fmu.RUnlock()
	if st.deleted.Load() {
		return ErrNotFound
	}
	dst := filepath.Join(st.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		_ = os.Remove(dst)
	}
	return cerr
}

// staleUploads flags the voices cut from the previous recording.
func staleUploads(p *Project) {
	for i := range p.Scenes {
		if s := &p.Scenes[i]; s.AudioSource == AudioUpload && s.Audio.Status == StatusDone {
			s.Audio.Stale = true
		}
	}
}

// maxVoiceSeconds is the longest recording a film can cover: maxScenes clips
// of the longest length the video model renders.
func maxVoiceSeconds(c config.Config) float64 { return float64(maxScenes) * voiceLimits(c).max }

// CreateFromVoice makes a new project from the user's own voice-over (the
// Home quick action): the profile is applied like CreateWithProfile, then the
// recording is set (ModeVoice). When the recording is refused the new
// project is removed again. title "" lets the plan name the film.
func (e *Engine) CreateFromVoice(title, srcPath, profileID string) (Project, error) {
	if e.closing.Load() {
		return Project{}, ErrClosing
	}
	if strings.TrimSpace(srcPath) == "" {
		return Project{}, errNoVoice
	}
	p, err := e.CreateWithProfile(title, profileID)
	if err != nil {
		if p.ID != "" {
			_ = e.Delete(p.ID)
		}
		return Project{}, err
	}
	if err := e.SetVoiceFile(p.ID, srcPath); err != nil {
		_ = e.Delete(p.ID)
		return Project{}, err
	}
	q, ok := e.Get(p.ID)
	if !ok {
		return Project{}, ErrNotFound
	}
	return q, nil
}

// StartVoiceAutopilot is StartAutopilot for the user's own voice-over: a new
// project from the recording (CreateFromVoice; profileID "" applies the
// default autopilot profile) and the autopilot on it — transcription, plan
// timed to the speech, references, frames, clips, voices cut from the
// recording, montage. The new project's id is returned even when starting
// the job failed after it was created.
func (e *Engine) StartVoiceAutopilot(title, srcPath, profileID string) (projectID string, err error) {
	return e.StartVoiceAutopilotWith(title, srcPath, "", profileID)
}

// StartVoiceAutopilotWith is StartVoiceAutopilot with the recording's ready
// transcript (transcriptPath, SetTranscriptFile; "" = recognise the speech):
// the autopilot then never calls speech-to-text. When the recording or the
// transcript is refused, no project is left behind.
func (e *Engine) StartVoiceAutopilotWith(title, srcPath, transcriptPath, profileID string) (projectID string, err error) {
	if e.closing.Load() {
		return "", ErrClosing
	}
	pid := strings.TrimSpace(profileID)
	if pid == "" {
		pid = strings.TrimSpace(e.cfg.Get().Autopilot.DefaultProfileID)
		if _, ok := e.profiles.Get(pid); !ok {
			pid = ""
		}
	}
	p, err := e.CreateFromVoice(title, srcPath, pid)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(transcriptPath) != "" {
		if err := e.SetTranscriptFile(p.ID, transcriptPath); err != nil {
			_ = e.Delete(p.ID)
			return "", err
		}
	}
	st := e.state(p.ID)
	if st == nil {
		return p.ID, ErrNotFound
	}
	// Run(StageAutopilot) wants a story text, which a recording only has after
	// the transcription: the autopilot's plan stage does that itself.
	return p.ID, e.startJobKind(st, JobAutopilot, func(p *Project) error {
		if p.Input.VoiceFile == "" {
			return errNoVoice
		}
		return nil
	}, func(j *jobCtx) (string, error) { return e.runAutopilot(j, false, "") })
}

// ---- a ready transcript ---------------------------------------------------------------------

const (
	// maxTranscriptFile: a transcript is text (ElevenLabs' text-to-speech
	// "with timestamps" JSON also carries the audio, in base64).
	maxTranscriptFile = 64 << 20
	// transcriptSlack: how far (s) a transcript's phrases may run past the
	// end of the recording, and how much a replacement recording may differ
	// in length for its predecessor's transcript to stay.
	transcriptSlack = 2.0
	// providerFile is voiceTranscript.Provider of a transcript read from the
	// user's file (Model is then its format: "srt", "json"…).
	providerFile = "file"
)

var errNoVoiceForText = errors.New("Спершу додай запис голосу, а тоді — текст із таймкодами до нього")

// docExts are documents people keep transcripts in that the studio cannot read.
var docExts = map[string]bool{".doc": true, ".docx": true, ".odt": true, ".rtf": true, ".pdf": true, ".pages": true}

// TranscriptExts lists the file extensions SetTranscriptFile is made for
// (the file picker): SRT, WebVTT, the JSON of the speech-to-text services and
// TXT with timestamps.
func TranscriptExts() []string {
	exts := stt.TranscriptExts()
	out := make([]string, len(exts))
	for i, x := range exts {
		out[i] = strings.TrimPrefix(x, ".")
	}
	return out
}

// SetTranscriptFile attaches a ready transcript with timecodes to the
// project's recording, so its speech is never recognised: SRT, WebVTT, the
// JSON of ElevenLabs, OpenAI/Whisper, Deepgram, AssemblyAI or whisper.cpp,
// TXT with timestamps — anything stt.ParseTranscript reads. The file is
// checked against the recording (refused when its phrases run more than 2 s
// past the end: the text of another recording, or shifted timecodes),
// copied into the project (audio/, Input.TranscriptFile) and read into the
// transcript sidecar the speech-to-text path writes; its plain text goes into
// Input.Text. From then on the plan, the autopilot and Transcribe use it.
// "" removes it — with the text it gave, unless edited — and the next plan
// recognises the speech again. Refused while a job runs and without a
// recording (SetVoiceFile first).
func (e *Engine) SetTranscriptFile(id, srcPath string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var in Input
	var busy error
	if !e.read(st, func(p *Project) bool {
		if p.Job != nil && p.Job.Status == JobRunning {
			busy = &busyError{p.Job.Kind}
		}
		in = p.Input
		return true
	}) {
		return ErrNotFound
	}
	if busy != nil {
		return busy
	}
	if strings.TrimSpace(srcPath) == "" {
		return e.detachTranscript(st, in)
	}
	if in.VoiceFile == "" {
		return errNoVoiceForText
	}
	tr, data, err := readTranscriptFile(srcPath)
	if err != nil {
		return err
	}
	dur, err := e.voiceDuration(st.ctx, st, in.VoiceFile)
	if err != nil {
		return err
	}
	if err := fitTranscript(&tr, dur); err != nil {
		return err
	}

	rel, err := e.writeAsset(st, "audio", transcriptCopyName(filepath.Base(srcPath)), data)
	if err != nil {
		return fmt.Errorf("не вдалося скопіювати файл із текстом: %w", err)
	}
	var old string
	var refused error
	if !e.update(st, func(p *Project) {
		switch {
		case p.Job != nil && p.Job.Status == JobRunning:
			refused = &busyError{p.Job.Kind}
		case p.Input.VoiceFile != in.VoiceFile:
			refused = errors.New("Запис щойно змінився — додай текст із таймкодами ще раз")
		default:
			in := &p.Input
			old, in.TranscriptFile = in.TranscriptFile, rel
			in.Text = clipRunes(tr.Text, 200_000)
			in.Mode, in.Voiceover = ModeVoice, true
		}
	}) || refused != nil {
		e.removeFiles(st, []string{rel})
		if refused != nil {
			return refused
		}
		return ErrNotFound
	}
	e.saveFileTranscript(st, in.VoiceFile, rel, tr)
	if old != "" && old != rel {
		e.removeFiles(st, []string{old})
	}
	return nil
}

// reLangHint is a language in a transcript's name ("video.uk.srt").
var reLangHint = regexp.MustCompile(`^\.[a-z]{2,3}(?:-[a-z0-9]{2,8})?$`)

// transcriptCopyName names the project's copy of a transcript file:
// "transcript-<latin name>-<id>[.<language>].<ext>" — a language hint in the
// name ("Казка.uk.srt") is kept, the parser reads it from there.
func transcriptCopyName(base string) string {
	ext := strings.ToLower(filepath.Ext(base))
	if len(ext) < 2 || len(ext) > 6 || slugTag(ext[1:]) != ext[1:] {
		ext = ".txt"
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	hint := strings.ToLower(filepath.Ext(stem))
	if reLangHint.MatchString(hint) {
		stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	} else {
		hint = ""
	}
	return "transcript-" + slugTag(stem) + "-" + newID(6) + hint + ext
}

// detachTranscript removes the attached transcript: its copy, the sidecar
// read from it and the text it gave (unless the user edited it).
func (e *Engine) detachTranscript(st *proj, in Input) error {
	if in.TranscriptFile == "" {
		return nil
	}
	fromFile := attachedText(st.dir, in)
	var rel string
	if !e.update(st, func(p *Project) {
		rel = p.Input.TranscriptFile
		dropAttached(&p.Input, fromFile)
	}) {
		return ErrNotFound
	}
	drop := []string{rel}
	if vt, ok := readSidecar(st.dir); ok && vt.TranscriptFile != "" { // speech-to-text's own stays
		drop = append(drop, transcriptRel)
	}
	e.removeFiles(st, drop)
	return nil
}

// attachedText is the text the attached transcript of in gave (from its
// sidecar), "" when unknown.
func attachedText(dir string, in Input) string {
	if in.TranscriptFile == "" {
		return ""
	}
	if vt, ok := readSidecar(dir); ok && vt.TranscriptFile == in.TranscriptFile {
		return vt.Transcript.Text
	}
	return ""
}

// dropAttached detaches the transcript file from in; the text it gave
// (fromFile) goes with it unless the user edited it.
func dropAttached(in *Input, fromFile string) {
	if in.TranscriptFile == "" {
		return
	}
	if fromFile != "" && in.Text == clipRunes(fromFile, 200_000) {
		in.Text = ""
	}
	in.TranscriptFile = ""
}

// readTranscriptFile reads and parses a transcript file (the user's, or its
// copy in the project); data is the file as it is.
func readTranscriptFile(path string) (tr stt.Transcript, data []byte, err error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case voiceExts[ext]:
		return tr, nil, errors.New("Це запис, а не текст. Потрібен файл із текстом і таймкодами: SRT, VTT, JSON або TXT")
	case docExts[ext]:
		return tr, nil, errors.New("Документи Word і PDF студія не читає — збережи текст із таймкодами як TXT або SRT")
	}
	info, err := os.Stat(path)
	switch {
	case err != nil || info.IsDir():
		return tr, nil, fmt.Errorf("Файл із текстом не знайдено: %s", path)
	case info.Size() == 0:
		return tr, nil, errors.New("Файл із текстом порожній")
	case info.Size() > maxTranscriptFile:
		return tr, nil, errors.New("Файл із текстом завеликий (максимум 64 МБ)")
	}
	if data, err = os.ReadFile(path); err != nil {
		return tr, nil, fmt.Errorf("Не вдалося прочитати файл із текстом: %w", err)
	}
	if tr, err = stt.ParseTranscript(data, filepath.Base(path)); err != nil {
		return stt.Transcript{}, nil, err
	}
	return tr, data, nil
}

// readAttachedFile parses the project's copy rel of the attached transcript.
func readAttachedFile(dir, rel string) (stt.Transcript, error) {
	src := filepath.Join(dir, filepath.FromSlash(rel))
	if !validTranscriptRel(rel) || !isFile(src) {
		return stt.Transcript{}, errors.New("Файл із текстом і таймкодами зник із проєкту — додай його ще раз або прибери, щоб студія розпізнала мову сама")
	}
	tr, _, err := readTranscriptFile(src)
	return tr, err
}

// validTranscriptRel: an attached transcript lives in the project's audio/.
func validTranscriptRel(rel string) bool {
	return strings.HasPrefix(rel, "audio/") && !strings.Contains(rel, "..") && !strings.ContainsAny(rel, `:\`)
}

// voiceDuration probes the length (s) of the project's recording rel.
func (e *Engine) voiceDuration(ctx context.Context, st *proj, rel string) (float64, error) {
	src := filepath.Join(st.dir, filepath.FromSlash(rel))
	if rel == "" || !isFile(src) {
		return 0, errors.New("Файл озвучки не знайдено — завантаж його ще раз")
	}
	ff := e.ffNow()
	if ff.FFprobe == "" {
		if ff.FFmpeg == "" {
			return 0, media.ErrNotFound
		}
		return 0, media.ErrNoProbe
	}
	pi, err := ff.Probe(ctx, src)
	switch {
	case err != nil:
		return 0, fmt.Errorf("Не вдалося прочитати запис: %s", errText(err))
	case !(pi.Duration > 0):
		return 0, errors.New("Не вдалося визначити тривалість запису")
	}
	return pi.Duration, nil
}

// fitTranscript checks a ready transcript against its recording of dur
// seconds: refused when a phrase starts — or, before the last one, ends —
// more than transcriptSlack after the recording ends (the text of another
// recording, or timecodes shifted by an intro). Otherwise its times are
// clamped to the recording (subtitles often keep the last line on screen a
// little longer) and Duration becomes the recording's length.
func fitTranscript(tr *stt.Transcript, dur float64) error {
	if !(dur > 0) {
		return nil
	}
	reach := 0.0
	for i, s := range tr.Segments {
		reach = math.Max(reach, s.Start)
		if i < len(tr.Segments)-1 {
			reach = math.Max(reach, s.End)
		}
	}
	if len(tr.Segments) == 0 {
		for _, w := range tr.Words {
			reach = math.Max(reach, w.Start)
		}
	}
	if reach > dur+transcriptSlack {
		return fmt.Errorf("Таймкоди не збігаються із записом: фрази в тексті доходять до %s, а запис триває %s. Схоже, це текст іншого запису або таймкоди зсунуті — перевір файли",
			voiceClock(reach), voiceClock(dur))
	}
	for i := range tr.Words {
		w := &tr.Words[i]
		w.Start = math.Min(w.Start, dur)
		w.End = math.Max(w.Start, math.Min(w.End, dur))
	}
	for i := range tr.Segments {
		s := &tr.Segments[i]
		s.Start = math.Min(s.Start, dur)
		s.End = math.Max(s.Start, math.Min(s.End, dur))
	}
	tr.Duration = dur
	return nil
}

// saveFileTranscript writes the sidecar of a transcript read from the
// attached file fileRel. A failure only costs a re-read: the sidecar no
// longer matches and readAttached parses the file again.
func (e *Engine) saveFileTranscript(st *proj, voiceRel, fileRel string, tr stt.Transcript) {
	vt := voiceTranscript{VoiceFile: voiceRel, TranscriptFile: fileRel, Provider: providerFile,
		Model: strings.TrimPrefix(strings.ToLower(filepath.Ext(fileRel)), "."), CreatedAt: nowMs(), Transcript: tr}
	if err := e.saveTranscript(st, vt); err != nil {
		log.Printf("[studio:%s] transcript sidecar not saved (the file is read again next time): %v", st.id, err)
	}
}

// readAttached is the transcript of the attached file in.TranscriptFile: its
// sidecar, or — when there is none, it is stale or fresh is set — the file
// read again, fitted to the recording and stored as the sidecar. The text
// goes into Input.Text when that is empty (fresh: always). Progress lands in
// [from, from+span] of the job. Speech-to-text is never called.
func (e *Engine) readAttached(j *jobCtx, in Input, from, span float64, fresh bool) (stt.Transcript, error) {
	if !fresh {
		if tr, ok := sidecarFor(j.st.dir, in); ok && len(tr.Words) > 0 {
			if strings.TrimSpace(in.Text) == "" {
				e.fillText(j.st, in, tr.Text)
			}
			return tr, nil
		}
	}
	j.progress(from+span*0.1, "Читаємо текст із таймкодами…")
	tr, err := readAttachedFile(j.st.dir, in.TranscriptFile)
	if err != nil {
		return stt.Transcript{}, err
	}
	dur, err := e.voiceDuration(j.ctx, j.st, in.VoiceFile)
	if err != nil {
		if j.ctx.Err() != nil {
			return stt.Transcript{}, j.ctx.Err()
		}
		return stt.Transcript{}, err
	}
	if err := fitTranscript(&tr, dur); err != nil {
		return stt.Transcript{}, err
	}
	e.saveFileTranscript(j.st, in.VoiceFile, in.TranscriptFile, tr)
	if fresh || strings.TrimSpace(in.Text) == "" {
		e.fillText(j.st, in, tr.Text)
	}
	j.progress(from+span, "Текст узято з файлу · "+wordCount(len(tr.Words)))
	return tr, nil
}

// fillText puts a transcript's text into Input.Text unless the recording or
// its transcript changed meanwhile.
func (e *Engine) fillText(st *proj, in Input, text string) {
	e.update(st, func(p *Project) {
		if p.Input.VoiceFile == in.VoiceFile && p.Input.TranscriptFile == in.TranscriptFile {
			p.Input.Text = clipRunes(text, 200_000)
		}
	})
}

// TestSTT checks the speech-to-text service of Settings without transcribing.
func (e *Engine) TestSTT(ctx context.Context) (bool, string) { return e.stt().Test(ctx) }

func (e *Engine) stt() *stt.Client { return stt.New(e.cfg.Get) }

// ---- transcription --------------------------------------------------------------------------

// Transcribe starts the JobTranscribe stage: the project's recording is
// transcribed again (word timings saved next to it, the text put into
// Input.Text). With an attached transcript (SetTranscriptFile) that file is
// only read again — no speech-to-text. The plan transcribes by itself when
// there is no transcript.
func (e *Engine) Transcribe(id string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	return e.startJobKind(st, JobTranscribe, func(p *Project) error {
		if p.Input.VoiceFile == "" {
			return errNoVoice
		}
		return nil
	}, func(j *jobCtx) (string, error) {
		attached := j.snapshot().Input.TranscriptFile != "" // fixed while the job runs
		tr, err := e.transcribeVoice(j, 0, 100)
		if err != nil {
			return "", err
		}
		if attached {
			return "Текст із таймкодами прочитано · " + wordCount(len(tr.Words)) + " · " + voiceClock(tr.Duration), nil
		}
		return "Мову розпізнано · " + wordCount(len(tr.Words)) + " · " + voiceClock(tr.Duration), nil
	})
}

// voiceTranscript is the sidecar: the transcript of one recording.
type voiceTranscript struct {
	VoiceFile string `json:"voiceFile"`
	// TranscriptFile is the attached file the transcript was read from
	// (Input.TranscriptFile; Provider "file"), "" = speech-to-text.
	TranscriptFile string         `json:"transcriptFile,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Model          string         `json:"model,omitempty"`
	CreatedAt      int64          `json:"createdAt"`
	Transcript     stt.Transcript `json:"transcript"`
}

// VoiceTranscript returns the transcript (with word timings) of the
// project's current recording — recognised, or read from the attached
// transcript file; false when there is none yet.
func (e *Engine) VoiceTranscript(id string) (stt.Transcript, bool) {
	p, ok := e.Get(id)
	if !ok || p.Input.VoiceFile == "" {
		return stt.Transcript{}, false
	}
	in, dir := p.Input, e.Dir(id)
	if tr, ok := sidecarFor(dir, in); ok {
		return tr, true
	}
	if in.TranscriptFile != "" { // no sidecar for it (yet): the file itself
		if tr, err := readAttachedFile(dir, in.TranscriptFile); err == nil {
			return tr, true
		}
	}
	return stt.Transcript{}, false
}

// sidecarFor is the sidecar transcript that belongs to in: recognised from
// its recording (in has no transcript file), or read from its attached file.
func sidecarFor(dir string, in Input) (stt.Transcript, bool) {
	vt, ok := readSidecar(dir)
	if !ok || vt.VoiceFile != in.VoiceFile || vt.TranscriptFile != in.TranscriptFile {
		return stt.Transcript{}, false
	}
	return vt.Transcript, true
}

// readSidecar reads the transcript sidecar of a project folder.
func readSidecar(dir string) (voiceTranscript, bool) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(transcriptRel)))
	if err != nil {
		return voiceTranscript{}, false
	}
	var vt voiceTranscript
	if json.Unmarshal(b, &vt) != nil {
		return voiceTranscript{}, false
	}
	return vt, true
}

// loadTranscript is the sidecar transcript of the recording voiceRel,
// whatever made it (speech-to-text or an attached file).
func loadTranscript(dir, voiceRel string) (stt.Transcript, bool) {
	vt, ok := readSidecar(dir)
	if !ok || vt.VoiceFile != voiceRel {
		return stt.Transcript{}, false
	}
	return vt.Transcript, true
}

func (e *Engine) saveTranscript(st *proj, vt voiceTranscript) error {
	b, err := json.Marshal(vt)
	if err != nil {
		return err
	}
	st.fmu.RLock()
	defer st.fmu.RUnlock()
	if st.deleted.Load() {
		return ErrNotFound
	}
	path := filepath.Join(st.dir, filepath.FromSlash(transcriptRel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

// transcribeVoice runs speech-to-text on the project's recording, stores the
// sidecar and puts the text into Input.Text — or, with an attached
// transcript file, reads that instead (readAttached). The online services get
// a compact MP3 (sttUpload), whisper.cpp on this computer the original.
// Progress lands in [from, from+span] of the job.
func (e *Engine) transcribeVoice(j *jobCtx, from, span float64) (stt.Transcript, error) {
	snap := j.snapshot()
	rel := snap.Input.VoiceFile
	if rel == "" {
		return stt.Transcript{}, errNoVoice
	}
	if snap.Input.TranscriptFile != "" { // the user's own transcript: read, never recognised
		return e.readAttached(j, snap.Input, from, span, true)
	}
	src := filepath.Join(j.st.dir, filepath.FromSlash(rel))
	if !isFile(src) {
		return stt.Transcript{}, errors.New("Файл озвучки не знайдено — завантаж його ще раз")
	}
	client := e.stt()
	provider, model := client.Model()
	upload := src // whisper.cpp on this computer converts the original itself
	if provider != stt.ProviderLocal {
		j.progress(from+span*0.05, "Готуємо запис для розпізнавання…")
		compact, cleanup, err := e.sttUpload(j.ctx, src)
		if err != nil {
			return stt.Transcript{}, err
		}
		defer cleanup()
		upload = compact
	}

	// whisper.cpp reports as it goes (its percentages land in
	// [15 %, 95 %] of the span); the online services say nothing until they
	// answer, so the elapsed time shows meanwhile.
	j.progress(from+span*0.15, "Розпізнаємо мову…")
	var reported atomic.Bool
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		t0 := time.Now()
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if !reported.Load() {
					j.note("Розпізнаємо мову · " + voiceClock(time.Since(t0).Seconds()))
				}
			}
		}
	}()
	tr, err := client.TranscribeProgress(j.ctx, upload, snap.Input.Language, func(pct int, msg string) {
		reported.Store(true)
		j.progress(from+span*(0.15+0.8*float64(max(0, min(100, pct)))/100), msg)
	})
	close(stop)
	<-done
	if err != nil {
		return stt.Transcript{}, err
	}
	if len(tr.Words) == 0 {
		return stt.Transcript{}, errors.New("У записі не знайдено мовлення — перевір файл або мову запису")
	}
	if err := e.saveTranscript(j.st, voiceTranscript{VoiceFile: rel, Provider: provider, Model: model, CreatedAt: nowMs(), Transcript: tr}); err != nil {
		return stt.Transcript{}, fmt.Errorf("не вдалося зберегти розпізнаний текст: %w", err)
	}
	e.update(j.st, func(p *Project) {
		if p.Input.VoiceFile == rel { // not replaced meanwhile
			p.Input.Text = clipRunes(tr.Text, 200_000)
		}
	})
	j.progress(from+span, "Мову розпізнано · "+wordCount(len(tr.Words)))
	return tr, nil
}

// transcriptFor is the transcript of the project's current recording:
// the attached transcript file's (never speech-to-text), else the sidecar,
// else the recording transcribed first (progress in [from, from+span]).
func (e *Engine) transcriptFor(j *jobCtx, snap *Project, from, span float64) (stt.Transcript, error) {
	if snap.Input.VoiceFile == "" {
		return stt.Transcript{}, errNoVoice
	}
	if snap.Input.TranscriptFile != "" {
		return e.readAttached(j, snap.Input, from, span, false)
	}
	if tr, ok := sidecarFor(j.st.dir, snap.Input); ok && len(tr.Words) > 0 {
		if strings.TrimSpace(snap.Input.Text) == "" {
			e.update(j.st, func(p *Project) {
				if p.Input.VoiceFile == snap.Input.VoiceFile {
					p.Input.Text = clipRunes(tr.Text, 200_000)
				}
			})
		}
		return tr, nil
	}
	return e.transcribeVoice(j, from, span)
}

// sttUpload prepares the recording for upload: a compact mono MP3 (16 kHz,
// 48 kbps — about an hour fits the 25 MB OpenAI limit, and uploads are
// quick). Without FFmpeg, or when it cannot read the file, the original is
// sent as is.
func (e *Engine) sttUpload(ctx context.Context, src string) (string, func(), error) {
	none := func() {}
	ff := e.ffNow()
	if ff.FFmpeg == "" {
		return src, none, nil
	}
	f, err := os.CreateTemp("", "ytc-stt-*.mp3")
	if err != nil {
		return src, none, nil
	}
	tmp := f.Name()
	f.Close()
	cleanup := func() { _ = os.Remove(tmp) }
	if err := ff.Run(ctx, "", "-i", src, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "libmp3lame", "-b:a", "48k", tmp); err != nil {
		cleanup()
		if ctx.Err() != nil {
			return "", none, ctx.Err()
		}
		log.Printf("[studio] voice-over not compressed for upload, sending the original: %v", err)
		return src, none, nil
	}
	return tmp, cleanup, nil
}

// ---- scenes timed to the speech -------------------------------------------------------------

// speechChunk is one scene of a voice-over: its verbatim text and its slice
// of the recording.
type speechChunk struct {
	Text       string
	Start, End float64 // Scene.VoiceStart / VoiceEnd
	Words      int
}

// chunkLimits: scene length bounds in seconds (min/max = the shortest and
// longest clip the video model renders) and the preferred length.
type chunkLimits struct{ min, target, max float64 }

// voiceLimits: Veo renders 4–8 s clips, G-Labs Omni Flash up to 10 s; scenes
// aim a bit under the clip length chosen in Settings.
func voiceLimits(c config.Config) chunkLimits {
	most := 8.0
	if omniFlash(c) {
		most = 10
	}
	target := math.Max(minVoiceClip, math.Min(most, float64(videoSeconds(c))*0.85))
	return chunkLimits{min: minVoiceClip, target: target, max: most}
}

// timedWord is a transcript word placed in the transcript text: from/to are
// rune offsets of the word there (-1 when the word is not found in the text).
type timedWord struct {
	text       string
	start, end float64
	from, to   int
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

func normWord(s string) []rune {
	var out []rune
	for _, r := range s {
		if isWordRune(r) {
			out = append(out, unicode.ToLower(r))
		}
	}
	return out
}

// alignWords finds every timed word in the transcript text, in order, so the
// scenes get the text verbatim (with its punctuation — Whisper's word list
// has none) and sentence ends are known. Words the text lacks stay unplaced;
// after a miss the search widens so both resynchronise.
func alignWords(text []rune, words []stt.Word) []timedWord {
	var norm []rune
	var at []int
	for i, r := range text {
		if isWordRune(r) {
			norm = append(norm, unicode.ToLower(r))
			at = append(at, i)
		}
	}
	startsWord := func(i int) bool {
		return i == 0 || !isWordRune(text[i-1]) || stt.Unspaced(text[i]) || stt.Unspaced(text[i-1])
	}
	endsWord := func(i int) bool {
		return i == len(text)-1 || !isWordRune(text[i+1]) || stt.Unspaced(text[i]) || stt.Unspaced(text[i+1])
	}
	out := make([]timedWord, len(words))
	cur, misses := 0, 0
	for k, w := range words {
		out[k] = timedWord{text: w.Text, start: w.Start, end: w.End, from: -1, to: -1}
		wn := normWord(w.Text)
		if len(wn) == 0 {
			continue
		}
		window := min(48+4*len(wn)+256*misses, 4096)
		found := -1
	search:
		for q := cur; q+len(wn) <= len(norm) && q <= cur+window; q++ {
			for i, r := range wn {
				if norm[q+i] != r {
					continue search
				}
			}
			if startsWord(at[q]) && endsWord(at[q+len(wn)-1]) {
				found = q
				break
			}
		}
		if found < 0 {
			misses++
			continue
		}
		misses = 0
		out[k].from, out[k].to = at[found], at[found+len(wn)-1]+1
		cur = found + len(wn)
	}
	return out
}

// chunkSpeech splits a transcript into scenes timed to the speech. Cuts fall
// only between words, preferably at sentence ends and pauses (≥ 0.3 s), and
// every scene lasts between lim.min and lim.max (a single word longer than
// that stays whole; the last scene takes the remainder). Each scene keeps a
// little air before its first and after its last word, never reaching into a
// neighbour, and its text is the transcript's own, verbatim.
func chunkSpeech(tr stt.Transcript, lim chunkLimits) []speechChunk {
	text := []rune(tr.Text)
	words := alignWords(text, tr.Words)
	n := len(words)
	if n == 0 {
		return nil
	}
	// startAt[i]/endAt[i]: where a scene starting / ending at word i is cut.
	startAt, endAt := make([]float64, n), make([]float64, n)
	startAt[0] = math.Max(0, words[0].start-speechLead)
	endAt[n-1] = words[n-1].end + speechTail
	if tr.Duration > words[n-1].end {
		endAt[n-1] = math.Min(endAt[n-1], tr.Duration)
	}
	for i := 0; i+1 < n; i++ {
		a, b := words[i].end, words[i+1].start
		if gap := b - a; gap >= 0 {
			endAt[i] = a + math.Min(speechTail, gap/2)
			startAt[i+1] = b - math.Min(speechLead, gap/2)
		} else { // overlapping word times: cut in the middle
			endAt[i] = (a + b) / 2
			startAt[i+1] = endAt[i]
		}
	}
	span := func(a, b int) float64 { return endAt[b-1] - startAt[a] } // scene of words [a, b)

	// Dynamic programming over the cut points: best[b] is the cheapest way
	// to split words [0, b).
	best, from := make([]float64, n+1), make([]int, n+1)
	for i := 1; i <= n; i++ {
		best[i] = math.Inf(1)
	}
	for b := 1; b <= n; b++ {
		cut := 0.0
		if b < n {
			cut = cutCost(text, words[b-1], words[b])
		}
		for a := b - 1; a >= 0 && b-a <= 400; a-- {
			d := span(a, b)
			if d > lim.max && b-a > 1 {
				if d > lim.max+1 {
					break // earlier starts only make it longer
				}
				continue
			}
			if math.IsInf(best[a], 1) {
				continue
			}
			if c := best[a] + spanCost(d, lim, a == 0 && b == n) + cut; c < best[b] {
				best[b], from[b] = c, a
			}
		}
	}
	var starts []int
	for b := n; b > 0; b = from[b] {
		starts = append(starts, from[b])
	}
	for i, j := 0, len(starts)-1; i < j; i, j = i+1, j-1 {
		starts[i], starts[j] = starts[j], starts[i]
	}

	// The text of scene k runs from its first placed word (with the opening
	// punctuation right before it) to the next scene's: every character of
	// the transcript lands in exactly one scene.
	offs := make([]int, len(starts)+1)
	for k, s := range starts {
		end := n
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		offs[k] = -1
		for i := s; i < end; i++ {
			if words[i].from >= 0 {
				offs[k] = openingStart(text, words[i].from)
				break
			}
		}
	}
	offs[0], offs[len(starts)] = 0, len(text)
	out := make([]speechChunk, len(starts))
	for k, s := range starts {
		end := n
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		c := speechChunk{Start: round3(startAt[s]), End: round3(endAt[end-1]), Words: end - s}
		if offs[k] >= 0 {
			next := len(text)
			for m := k + 1; m <= len(starts); m++ {
				if offs[m] >= offs[k] {
					next = offs[m]
					break
				}
			}
			c.Text = strings.Join(strings.Fields(string(text[offs[k]:next])), " ")
		}
		if c.Text == "" { // none of its words is in the text: their own spelling
			parts := make([]string, 0, end-s)
			for _, w := range words[s:end] {
				parts = append(parts, w.text)
			}
			c.Text = strings.Join(parts, " ")
		}
		out[k] = c
	}
	return out
}

// openingStart moves a scene's text start back over opening punctuation
// glued to its first word («, (, ¿…) and over a dialogue dash before it.
func openingStart(text []rune, i int) int {
	for i > 0 && (unicode.In(text[i-1], unicode.Ps, unicode.Pi) || text[i-1] == '¿' || text[i-1] == '¡' ||
		(text[i-1] == '"' || text[i-1] == '\'') && (i-1 == 0 || unicode.IsSpace(text[i-2]))) {
		i--
	}
	j := i
	for j > 0 && text[j-1] == ' ' {
		j--
	}
	if j > 0 && strings.ContainsRune("—–-", text[j-1]) && j != i && (j-1 == 0 || unicode.IsSpace(text[j-2])) {
		return j - 1
	}
	return i
}

// spanCost prefers scenes near the target length; shorter than the
// shortest clip is strongly avoided (unless the whole recording is that
// short), longer than the longest only happens for a single long word.
func spanCost(d float64, lim chunkLimits, whole bool) float64 {
	x := (d - lim.target) / lim.target
	c := 4 * x * x
	if d > lim.max {
		c += 50 + 10*(d-lim.max)
	}
	if d < lim.min && !whole {
		c += 8 + 20*(lim.min-d)*(lim.min-d)
	}
	return c
}

const (
	sentenceMarks = ".!?…。！？"
	clauseMarks   = ",;:—–、，；："
)

// cutCost is the price of a scene cut between two words: free at a sentence
// end followed by a pause, dearest in the middle of a phrase.
func cutCost(text []rune, prev, next timedWord) float64 {
	between := prev.text // the word's own punctuation (Scribe keeps it)
	if prev.to >= 0 && next.from >= prev.to {
		between = string(text[prev.to:next.from])
	}
	c := 4.0
	switch {
	case strings.ContainsAny(between, sentenceMarks):
		c = 0
	case strings.ContainsAny(between, clauseMarks):
		c = 1.5
	}
	switch gap := next.start - prev.end; {
	case gap >= 2*speechPause:
	case gap >= speechPause:
		c += 0.5
	default:
		c += 1.5
	}
	return c
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// voiceClock formats seconds as m:ss.
func voiceClock(sec float64) string {
	s := int(math.Round(math.Max(0, sec)))
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// wordCount is "1 слово", "3 слова", "53 слова", "12 слів".
func wordCount(n int) string {
	return strconv.Itoa(n) + " " + ukPlural(n, "слово", "слова", "слів")
}

// ---- voices cut from the recording ------------------------------------------------------------

// cutVoice cuts [start, end] out of the recording rel as MP3 bytes, with
// short fades so the cut never clicks.
func (e *Engine) cutVoice(ctx context.Context, st *proj, rel string, start, end float64) ([]byte, error) {
	if rel == "" {
		return nil, errNoVoice
	}
	src := filepath.Join(st.dir, filepath.FromSlash(rel))
	if !isFile(src) {
		return nil, errors.New("Файл озвучки не знайдено — завантаж його ще раз (крок 1)")
	}
	if !(end > start) {
		return nil, errors.New("У сцени немає відрізка озвучки — вкажи його початок і кінець")
	}
	ff := e.ffNow()
	if ff.FFmpeg == "" {
		return nil, media.ErrNotFound
	}
	if ff.FFprobe != "" {
		if info, err := ff.Probe(ctx, src); err == nil && info.Duration > 0 {
			end = math.Min(end, info.Duration)
		}
	}
	start = math.Max(0, start)
	d := end - start
	if d < 0.05 {
		return nil, errors.New("Відрізок озвучки виходить за межі запису — перевір початок і кінець сцени")
	}
	f, err := os.CreateTemp("", "ytc-voice-*.mp3")
	if err != nil {
		return nil, err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	fade := math.Min(voiceFade, d/4)
	sec := func(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }
	af := fmt.Sprintf("afade=t=in:st=0:d=%s,afade=t=out:st=%s:d=%s", sec(fade), sec(d-fade), sec(fade))
	if err := ff.Run(ctx, "", "-ss", sec(start), "-t", sec(d), "-i", src, "-vn", "-af", af,
		"-ar", "44100", "-c:a", "libmp3lame", "-b:a", "192k", tmp); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("не вдалося вирізати озвучку: %w", err)
	}
	data, err := os.ReadFile(tmp)
	if err != nil || len(data) == 0 {
		return nil, errors.New("не вдалося вирізати озвучку: порожній файл")
	}
	return data, nil
}

// ---- project state ----------------------------------------------------------------------------

// normalizeVoice fixes the ModeVoice fields of a loaded (or copied) project:
// an unknown audio source falls back to TTS, a slice needs a valid range, a
// film cut to the user's voice always has its voice-over on, and a
// transcript file needs its recording (a translation drops the recording)
// and a path inside audio/.
func normalizeVoice(p *Project) {
	if p.Input.Mode == ModeVoice {
		p.Input.Voiceover = true
	}
	if in := &p.Input; in.TranscriptFile != "" && (in.VoiceFile == "" || !validTranscriptRel(in.TranscriptFile)) {
		in.TranscriptFile = ""
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if !finiteNonNeg(s.VoiceStart) {
			s.VoiceStart = 0
		}
		if !finiteNonNeg(s.VoiceEnd) {
			s.VoiceEnd = 0
		}
		if s.AudioSource != AudioUpload || s.VoiceEnd <= s.VoiceStart {
			s.AudioSource = ""
		}
	}
}

func finiteNonNeg(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 0 }
