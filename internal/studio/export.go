package studio

// Export to an editor («Експортувати в…», step 5): the film as a project for
// DaVinci Resolve, Premiere Pro, Final Cut Pro, CapCut, Kdenlive/Shotcut or
// any OpenTimelineIO reader — instead of (or besides) the FFmpeg montage.
// Only the scene assets are needed; nothing is rendered into a film.
//
// The package, <project>/export/<editor>-<date>/ (+ an optional .zip):
//
//	media/          the files the timeline uses, numbered by scene:
//	                001_scene.mp4 (the clip as generated — the timeline
//	                holds the in/out points), 001_frame.jpg (a scene without
//	                a clip, conformed to the film's size), 001_hold.jpg (the
//	                clip's last frame when the scene holds it), 001_voice.mp3,
//	                001_text.png (the on-screen text, transparent, full
//	                frame), music.<ext>. Hard links when possible (no space
//	                used), else copies. CapCut: 001_scene.mp4 is the scene cut
//	                to exactly its part of the film (CapCut reads no in/out).
//	stems/          voice.wav / music.wav: every line in place, the ducked
//	                music — whole-film WAVs (CapCut, «Лише файли», optional)
//	timeline.*      the editor's file(s); edit-list.csv — the cut list
//	subtitles.srt   the montage's subtitles · cover.jpg · README.txt
//
// The timeline is laid out by media.Layout — the same plan the montage
// renders and the editor shows — with durations probed from the files, then
// converted to the frame rate of the clips (package nle).

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ytcartell/internal/cover"
	"ytcartell/internal/media"
	"ytcartell/internal/nle"
)

// Export targets (ExportOptions.Target).
const (
	ExportResolve  = "resolve"
	ExportPremiere = "premiere"
	ExportFinalCut = "finalcut"
	ExportCapCut   = "capcut"
	ExportKdenlive = "kdenlive" // Kdenlive / Shotcut
	ExportOTIO     = "otio"
	ExportFiles    = "files"
)

// ExportTarget describes one editor the film can go to.
type ExportTarget struct {
	ID      string
	Label   string // "DaVinci Resolve"
	Formats string // what the package holds: "FCPXML 1.10 + EDL"
	// Timeline is true when the editor gets a timeline file (CapCut and
	// «Лише файли» get numbered media and a cut list).
	Timeline bool
	// Steps are the short import steps (Ukrainian, «ти»).
	Steps []string
	// Note is an honest remark shown with the target ("" = none).
	Note string
}

var exportTargets = []ExportTarget{
	{ID: ExportResolve, Label: "DaVinci Resolve", Formats: "FCP 7 XML + FCPXML 1.10 + EDL", Timeline: true,
		Steps: []string{
			"Створи проєкт у DaVinci Resolve 18 або новішому (частота кадрів — як у фільмі, див. README).",
			"File → Import → Timeline… і вибери timeline.xml — сцени, текст, звук кліпів, голос і музика стануть на свої доріжки.",
			"Resolve сам додасть файли з папки media у Media Pool.",
			"Субтитри: перетягни subtitles.srt у Media Pool, а звідти — на таймлайн (з'явиться доріжка субтитрів).",
		},
		Note: "Є й timeline.fcpxml (FCPXML 1.10): ті самі кліпи, але звук Resolve розкладає по доріжках як вийде, а кадри без відео — без руху."},
	{ID: ExportPremiere, Label: "Premiere Pro", Formats: "Final Cut Pro 7 XML (xmeml)", Timeline: true,
		Steps: []string{
			"У Premiere Pro: File → Import… і вибери timeline.xml.",
			"Premiere створить послідовність з кліпами, голосом, музикою й текстом на окремих доріжках.",
			"Субтитри: File → Import… → subtitles.srt, потім перетягни файл на таймлайн (Captions).",
		}},
	{ID: ExportFinalCut, Label: "Final Cut Pro", Formats: "FCPXML 1.10 (.fcpxmld)", Timeline: true,
		Steps: []string{
			"Скопіюй усю папку на Mac (разом із media).",
			"У Final Cut Pro: File → Import → XML… і вибери timeline.fcpxmld (або timeline.fcpxml).",
			"Якщо файли не знайдуться — File → Relink Files… і вкажи папку media.",
			"Субтитри: File → Import → Captions… → subtitles.srt.",
		}},
	{ID: ExportCapCut, Label: "CapCut", Formats: "пронумеровані файли + SRT", Timeline: false,
		Steps: []string{
			"Створи новий проєкт у CapCut і натисни «Імпорт» → вибери всі файли *_scene.mp4 з папки media.",
			"Виділи їх у медіатеці (вони вже по порядку) і перетягни на таймлайн — склейки будуть точно як у фільмі.",
			"Перетягни stems/voice.wav і stems/music.wav на аудіодоріжки з самого початку (0:00) — голос і музика стануть на свої місця.",
			"Субтитри: Текст → Локальні субтитри (Local captions) → subtitles.srt.",
		},
		Note: "CapCut не імпортує монтажні файли (XML, OTIO), а його чернетки шифруються або змінюються з кожною версією — тому це пакет файлів, а не чернетка CapCut."},
	{ID: ExportKdenlive, Label: "Kdenlive / Shotcut", Formats: "OpenTimelineIO + MLT", Timeline: true,
		Steps: []string{
			"Kdenlive 25.04+: File → OpenTimelineIO Import… → timeline.otio.",
			"Shotcut: File → Open… → timeline.mlt.",
			"Субтитри: у Kdenlive — Project → Subtitles → Import Subtitle File; у Shotcut — фільтр «Subtitle Burn In» або панель Subtitles → Import.",
		}},
	{ID: ExportOTIO, Label: "OpenTimelineIO", Formats: ".otio", Timeline: true,
		Steps: []string{
			"Відкрий timeline.otio у програмі, що читає OpenTimelineIO (DaVinci Resolve 18.5+: File → Import → Timeline; Kdenlive 25.04+; Nuke Studio, Hiero, RV).",
			"Або сконвертуй його будь-яким конвертером OpenTimelineIO у потрібний формат.",
		}},
	{ID: ExportFiles, Label: "Лише файли (папка)", Formats: "файли + монтажний лист", Timeline: false,
		Steps: []string{
			"У папці media — усі файли фільму, пронумеровані за сценами.",
			"edit-list.csv — монтажний лист: що, звідки (вхід/вихід) і куди ставити на таймлайні.",
			"stems/voice.wav і stems/music.wav — голос і музика на всю довжину фільму: постав їх з 0:00.",
		}},
}

// ExportTargets lists the editors in menu order.
func ExportTargets() []ExportTarget { return append([]ExportTarget(nil), exportTargets...) }

// ExportTargetByID finds a target.
func ExportTargetByID(id string) (ExportTarget, bool) {
	for _, t := range exportTargets {
		if t.ID == id {
			return t, true
		}
	}
	return ExportTarget{}, false
}

// ExportOptions say what goes into the package.
type ExportOptions struct {
	Target string
	// What to include (the timeline's timing never changes: a scene keeps
	// the length its voice gives it even when the voice is left out).
	Music, Voice, Subtitles, Overlays, Cover bool
	// Stems adds stems/voice.wav and stems/music.wav (always on for CapCut
	// and «Лише файли»).
	Stems bool
	// Zip also packs the folder into <folder>.zip next to it.
	Zip bool
	// Partial exports what there is when clips or voices are missing
	// («Експортувати те, що є»); without it such a project is refused with
	// ErrExportIncomplete.
	Partial bool
	// Dir is the parent folder ("" = <project>/export).
	Dir string
	// Progress (optional) reports 0–100 and a Ukrainian line; any goroutine.
	Progress func(pct int, msg string)
}

// DefaultExportOptions includes everything the project has.
func DefaultExportOptions(target string) ExportOptions {
	return ExportOptions{Target: target, Music: true, Voice: true, Subtitles: true, Overlays: true, Cover: true,
		Stems: target == ExportCapCut || target == ExportFiles}
}

// ErrExportIncomplete: clips or voices are missing and Partial is off.
var ErrExportIncomplete = errors.New("не все готово для експорту")

// ExportReadiness is what a project can export now.
type ExportReadiness struct {
	Scenes int // scenes in the plan
	// OnTimeline: scenes the film uses (a clip or at least a frame).
	OnTimeline int
	Clips      int // scenes with a clip
	Stills     int // scenes shown as a frame (no clip yet)
	Skipped    int // scenes with neither: left out of the film
	// Voices / MissingVoices: voice-over lines ready / not made yet.
	Voices, MissingVoices int
	// What the project has to offer.
	Music, Subtitles, Overlays, Cover bool
	// Blocker says why nothing can be exported ("" = it can).
	Blocker string
}

// Complete reports a project with every clip and voice line.
func (r ExportReadiness) Complete() bool {
	return r.Stills == 0 && r.Skipped == 0 && r.MissingVoices == 0
}

// Missing is a short Ukrainian line about what is not ready ("" = nothing).
func (r ExportReadiness) Missing() string {
	var parts []string
	if n := r.Stills + r.Skipped; n > 0 {
		s := fmt.Sprintf("%d %s без відео", n, pluralUK(n, "сцена", "сцени", "сцен"))
		if r.Stills > 0 && r.Skipped == 0 {
			s += " (буде кадр із рухом)"
		} else if r.Skipped > 0 {
			s += fmt.Sprintf(" (%d без кадру — їх не буде)", r.Skipped)
		}
		parts = append(parts, s)
	}
	if r.MissingVoices > 0 {
		parts = append(parts, fmt.Sprintf("%d %s без озвучки", r.MissingVoices, pluralUK(r.MissingVoices, "сцена", "сцени", "сцен")))
	}
	return strings.Join(parts, ", ")
}

func pluralUK(n int, one, few, many string) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}

// ExportCheck reports what project id can export now (cheap: file checks only).
func (e *Engine) ExportCheck(id string) (ExportReadiness, error) {
	p, ok := e.Get(id)
	if !ok {
		return ExportReadiness{}, ErrNotFound
	}
	r := exportReadiness(&p, e.Dir(id))
	if r.Blocker == "" && !e.FF().OK() {
		r.Blocker = "Потрібен FFmpeg: встанови його в налаштуваннях («Встановити автоматично»)."
	}
	return r, nil
}

func exportReadiness(p *Project, dir string) ExportReadiness {
	abs := func(rel string) string {
		if rel == "" {
			return ""
		}
		return filepath.Join(dir, filepath.FromSlash(rel))
	}
	r := ExportReadiness{Scenes: len(p.Scenes)}
	voiceover := p.Input.Voiceover && p.Input.SoundMode() == AudioVoiceover
	for _, s := range p.Scenes {
		clip, frame := isFile(abs(s.Video.Path)), isFile(abs(s.Frame.Path))
		switch {
		case clip:
			r.Clips++
		case frame:
			r.Stills++
		default:
			r.Skipped++
		}
		if clip || frame {
			r.OnTimeline++
		}
		if voiceover && strings.TrimSpace(s.Narration) != "" {
			if isFile(abs(s.Audio.Path)) {
				r.Voices++
			} else {
				r.MissingVoices++
			}
		}
		if strings.TrimSpace(s.Narration) != "" || len(cleanLines(s.Lines)) > 0 {
			r.Subtitles = true
		}
		if !s.OnScreen.Empty() && p.Input.TextOverlays() {
			r.Overlays = true
		}
	}
	r.Music = isFile(abs(p.Montage.MusicPath))
	r.Cover = isFile(abs(p.Montage.CoverFinal.Path)) || isFile(abs(p.Montage.Cover.Path))
	if r.OnTimeline == 0 {
		r.Blocker = "Немає жодного кліпу чи кадру — спершу згенеруй кадри (крок 3) або відео (крок 4)."
	}
	return r
}

// ExportResult describes a finished package.
type ExportResult struct {
	Dir      string // the export folder
	Zip      string // the archive ("" = none)
	Target   ExportTarget
	Timeline string // the main timeline file ("" for CapCut and «Лише файли»)
	Files    int    // files in the folder
	// Notes: what the editor gets only approximately (Ukrainian).
	Notes []string
	// Partial: clips or voices were missing.
	Partial bool
}

// ExportTimeline builds the export package of project id and returns its
// folder (the Telegram bot and other callers; the UI uses
// ExportTimelineContext for cancel and the details).
func (e *Engine) ExportTimeline(projectID string, opt ExportOptions) (string, error) {
	res, err := e.ExportTimelineContext(context.Background(), projectID, opt)
	return res.Dir, err
}

// ExportTimelineContext builds the export package (see the file comment).
// It runs on the caller's goroutine — seconds for the timeline formats
// (links, probes, a few stills), longer for CapCut (every scene is cut)
// and the stems; ctx cancels it (the half-made folder is removed).
func (e *Engine) ExportTimelineContext(ctx context.Context, projectID string, opt ExportOptions) (ExportResult, error) {
	tgt, ok := ExportTargetByID(opt.Target)
	if !ok {
		return ExportResult{}, fmt.Errorf("невідомий редактор: %q", opt.Target)
	}
	p, ok := e.Get(projectID)
	if !ok {
		return ExportResult{}, ErrNotFound
	}
	dir := e.Dir(projectID)
	ready := exportReadiness(&p, dir)
	if ready.Blocker != "" {
		return ExportResult{}, errors.New(ready.Blocker)
	}
	if !ready.Complete() && !opt.Partial {
		return ExportResult{}, fmt.Errorf("%w: %s", ErrExportIncomplete, ready.Missing())
	}
	ff := e.ffNow()
	if !ff.OK() {
		return ExportResult{}, media.ErrNotFound
	}
	if opt.Target == ExportCapCut || opt.Target == ExportFiles {
		opt.Stems = true
	}
	parent := opt.Dir
	if parent == "" {
		parent = filepath.Join(dir, "export")
	}
	x := &exporter{e: e, ff: ff, ctx: ctx, p: &p, dir: dir, opt: opt, tgt: tgt, ready: ready}
	res, err := x.run(parent)
	if err != nil && x.out != "" {
		_ = os.RemoveAll(x.out)
		if res.Zip != "" {
			_ = os.Remove(res.Zip)
		}
	}
	if err != nil && ctx.Err() != nil {
		return ExportResult{}, ctx.Err()
	}
	return res, err
}

// exporter is one export run.
type exporter struct {
	e     *Engine
	ff    media.FF
	ctx   context.Context
	p     *Project
	dir   string
	opt   ExportOptions
	tgt   ExportTarget
	ready ExportReadiness

	out   string // the export folder
	opts  media.MontageOptions
	slots []media.Slot
	total float64
	W, H  int
	rate  nle.Rate
	probe map[string]media.StreamInfo // by absolute path
	files int
	notes []string
	rows  []editRow
	texts []textRow // CapCut / files: where the on-screen texts go
	mu    sync.Mutex
}

func (x *exporter) progress(pct int, msg string) {
	if x.opt.Progress != nil {
		x.opt.Progress(max(0, min(100, pct)), msg)
	}
}

func (x *exporter) note(s string) {
	for _, n := range x.notes {
		if n == s {
			return
		}
	}
	x.notes = append(x.notes, s)
}

// sceneNo is the 1-based number of the scene behind segment i.
func sceneNo(i int) int { return i + 1 }

func (x *exporter) run(parent string) (ExportResult, error) {
	res := ExportResult{Target: x.tgt, Partial: !x.ready.Complete()}
	x.progress(1, "Перевіряємо файли")
	if err := x.plan(); err != nil {
		return res, err
	}
	if err := x.ctx.Err(); err != nil {
		return res, err
	}
	out, err := uniqueDir(parent, x.tgt.ID+"-"+time.Now().Format("2006-01-02"))
	if err != nil {
		return res, fmt.Errorf("не вдалося створити папку експорту: %w", err)
	}
	x.out = out
	res.Dir = out
	if err := os.MkdirAll(filepath.Join(out, "media"), 0o755); err != nil {
		return res, err
	}

	tl, err := x.timeline()
	if err != nil {
		return res, err
	}
	x.progress(70, "Пишемо файли для редактора")
	if res.Timeline, err = x.writeTimeline(tl); err != nil {
		return res, err
	}
	if x.opt.Subtitles {
		if cues := media.SubtitleCues(x.opts); len(cues) > 0 {
			if err := x.write("subtitles.srt", []byte(media.SRT(cues))); err != nil {
				return res, err
			}
		}
	}
	if x.opt.Cover {
		if err := x.cover(); err != nil {
			return res, err
		}
	}
	if x.opt.Stems {
		if err := x.stems(); err != nil {
			return res, err
		}
	}
	if err := x.write("edit-list.csv", x.editList()); err != nil {
		return res, err
	}
	res.Notes = x.notes
	if err := x.write("README.txt", x.readme(tl, res)); err != nil {
		return res, err
	}
	res.Files = x.files
	if x.opt.Zip {
		x.progress(92, "Пакуємо в ZIP")
		z := out + ".zip"
		if err := zipDir(x.ctx, out, z); err != nil {
			_ = os.Remove(z)
			return res, fmt.Errorf("не вдалося створити ZIP: %w", err)
		}
		res.Zip = z
	}
	x.progress(100, "Готово")
	return res, nil
}

// uniqueDir creates parent/name (name-2, name-3 … when taken).
func uniqueDir(parent, name string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	for i := 1; i < 1000; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s-%d", name, i)
		}
		d := filepath.Join(parent, n)
		if err := os.Mkdir(d, 0o755); err == nil {
			return d, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", errors.New("забагато експортів за день")
}

func (x *exporter) write(rel string, data []byte) error {
	path := filepath.Join(x.out, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("не вдалося записати %s: %w", rel, err)
	}
	x.files++
	return nil
}

// ---- the plan --------------------------------------------------------------------------------

// plan probes every file the film uses and lays the timeline out with the
// real durations.
func (x *exporter) plan() error {
	x.opts = montageOptions(x.p, x.dir)
	x.probe = map[string]media.StreamInfo{}
	var paths []string
	for _, s := range x.opts.Segments {
		for _, f := range []string{s.Video, s.Voice} {
			if f != "" && isFile(f) {
				paths = append(paths, f)
			}
		}
	}
	if isFile(x.opts.MusicPath) {
		paths = append(paths, x.opts.MusicPath)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	var firstErr error
	done := 0
	for _, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			info, err := x.ff.ProbeStreams(x.ctx, path)
			x.mu.Lock()
			defer x.mu.Unlock()
			done++
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", filepath.Base(path), err)
				}
				return
			}
			x.probe[path] = info
			x.progress(1+9*done/max(1, len(paths)), "Перевіряємо файли")
		}()
	}
	wg.Wait()
	if err := x.ctx.Err(); err != nil {
		return err
	}
	if firstErr != nil {
		return fmt.Errorf("не вдалося прочитати файл: %w", firstErr)
	}
	// Real durations (the montage probes only what it does not know).
	for i := range x.opts.Segments {
		s := &x.opts.Segments[i]
		if info, ok := x.probe[s.Video]; ok && info.Duration > 0 {
			s.VideoDur = info.Duration
		}
		if info, ok := x.probe[s.Voice]; ok && info.Duration > 0 {
			s.VoiceDur = info.Duration
		}
		if s.Video != "" && !isFile(s.Video) {
			s.Video = ""
		}
		if s.Frame != "" && !isFile(s.Frame) {
			s.Frame = ""
		}
		if s.Voice != "" && !isFile(s.Voice) {
			s.Voice = ""
		}
	}
	x.slots, x.total = media.Layout(x.opts)
	if len(x.slots) == 0 {
		return errors.New("Немає жодного кліпу чи кадру")
	}
	// Frame size: the first clip's (like the montage), else the format's.
	x.W, x.H = 1280, 720
	if x.p.Input.Aspect == "9:16" {
		x.W, x.H = 720, 1280
	}
	fps := map[nle.Rate]int{}
	first := true
	for _, sl := range x.slots {
		s := x.opts.Segments[sl.Segment]
		info, ok := x.probe[s.Video]
		if !sl.HasClip || !ok {
			continue
		}
		if first && info.Width > 1 && info.Height > 1 {
			x.W, x.H = info.Width-info.Width%2, info.Height-info.Height%2
			first = false
		}
		if f := info.FPS(); f > 0 {
			fps[nle.RateOf(f)]++
		}
	}
	// The timeline runs at the clips' rate (the most common one), 24 without clips.
	best := 0
	x.rate = nle.Rate24
	for r, n := range fps {
		if n > best || n == best && r.FPS() > x.rate.FPS() {
			x.rate, best = r, n
		}
	}
	return nil
}

// f24 converts a montage frame (1/24 s) to a timeline frame.
func (x *exporter) f24(f int) int64 { return x.rate.Frames(float64(f) / media.FPS) }

// sec converts seconds to a timeline frame.
func (x *exporter) sec(s float64) int64 { return x.rate.Frames(s) }

// ---- media files -----------------------------------------------------------------------------

// linkMedia puts src into media/<name> (a hard link, else a copy) and
// describes it.
func (x *exporter) linkMedia(src, name string, kind nle.Kind) (*nle.Media, error) {
	rel := "media/" + name
	dst := filepath.Join(x.out, "media", name)
	if err := linkOrCopy(src, dst); err != nil {
		return nil, fmt.Errorf("не вдалося скопіювати %s: %w", filepath.Base(src), err)
	}
	x.files++
	m := &nle.Media{ID: "m" + strings.TrimSuffix(name, filepath.Ext(name)), Name: name, Rel: rel, Abs: dst, Kind: kind}
	if info, ok := x.probe[src]; ok {
		m.Duration = info.Duration
		m.Width, m.Height = info.Width, info.Height
		m.HasAudio = info.HasAudio
		m.SampleRate, m.Channels = info.SampleRate, info.Channels
		if f := info.FPS(); f > 0 {
			m.Rate = nle.RateOf(f)
		}
	}
	return m, nil
}

// linkOrCopy hard-links src at dst (same volume, no space used — the studio
// never rewrites its media in place), else copies it.
func linkOrCopy(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if os.Link(src, dst) == nil {
		return nil
	}
	return copyFile(src, dst)
}

// stillMedia writes a JPEG still (a frame conformed to the film, or a
// clip's held frame) and describes it.
func (x *exporter) stillMedia(src string, at float64, conform bool, name string, w, h int) (*nle.Media, error) {
	dst := filepath.Join(x.out, "media", name)
	cw, ch := 0, 0
	if conform {
		cw, ch = x.W, x.H
		w, h = x.W, x.H
	}
	err := x.ff.StillFrame(x.ctx, src, at, cw, ch, dst)
	if err != nil && at > 0 && x.ctx.Err() == nil {
		// A clip shorter than it claims: its last frame a little earlier.
		err = x.ff.StillFrame(x.ctx, src, math.Max(0, at-0.5), cw, ch, dst)
	}
	if err != nil {
		return nil, fmt.Errorf("кадр %s: %w", name, err)
	}
	x.files++
	return &nle.Media{ID: "m" + strings.TrimSuffix(name, ".jpg"), Name: name, Rel: "media/" + name, Abs: dst, Kind: nle.Picture, Width: w, Height: h}, nil
}

// ---- the timeline ----------------------------------------------------------------------------

// sceneTitle is "Сцена 3 · Назва" (the title shortened).
func (x *exporter) sceneTitle(i int) string {
	t := strings.TrimSpace(x.p.Scenes[i].Title)
	if utf8.RuneCountInString(t) > 48 {
		t = string([]rune(t)[:47]) + "…"
	}
	if t == "" {
		return fmt.Sprintf("Сцена %d", sceneNo(i))
	}
	return fmt.Sprintf("Сцена %d · %s", sceneNo(i), t)
}

// timeline builds the editor timeline and puts its media into the folder.
func (x *exporter) timeline() (*nle.Timeline, error) {
	p := x.p
	t := &nle.Timeline{Name: strings.TrimSpace(p.Title), Rate: x.rate, Width: x.W, Height: x.H, SampleRate: media.SampleRate}
	if t.Name == "" {
		t.Name = "YT Cartell Studio"
	}
	t.Length = x.sec(x.total)
	n := len(x.slots)
	// Cuts: between overlapping scenes in the middle of the overlap.
	cut := make([]int, n+1) // montage frames
	for k, sl := range x.slots {
		cut[k] = sl.StartFrame() + sl.InFrames()/2
	}
	cut[0] = 0
	last := x.slots[n-1]
	cut[n] = last.StartFrame() + last.Frames()

	v1 := nle.Track{Name: "V1 · Сцени", Kind: nle.VideoTrack}
	a1 := nle.Track{Name: "A1 · Звук кліпів", Kind: nle.AudioTrack, SceneSound: true}
	capcut := x.opt.Target == ExportCapCut
	steps := float64(n)
	for k, sl := range x.slots {
		if err := x.ctx.Err(); err != nil {
			return nil, err
		}
		seg := x.opts.Segments[sl.Segment]
		i := sl.Segment
		num := fmt.Sprintf("%03d", sceneNo(i))
		title := x.sceneTitle(i)
		S, F := sl.StartFrame(), sl.Frames()
		a, b := cut[k]-S, cut[k+1]-S // the slot's own frames on V1
		cf := sl.ClipFrames()
		x.progress(10+int(50*float64(k)/steps), fmt.Sprintf("Сцена %d з %d", k+1, n))
		vol := clamp01(x.opts.AmbientVolume)
		if seg.ClipVolume > 0 {
			vol = clamp01(seg.ClipVolume)
		}
		t.Markers = append(t.Markers, nle.Marker{At: x.f24(cut[k]), Name: title})

		if capcut {
			// CapCut reads no in/out points: the scene cut to its frames.
			name := num + "_scene.mp4"
			dst := filepath.Join(x.out, "media", name)
			info := x.probe[seg.Video]
			if err := x.ff.CutClip(x.ctx, x.opts, x.slots, k, a, b, x.W, x.H, info.HasAudio, dst); err != nil {
				return nil, err
			}
			x.files++
			m := &nle.Media{ID: "m" + num, Name: name, Rel: "media/" + name, Abs: dst, Kind: nle.Movie,
				Duration: float64(b-a) / media.FPS, Rate: nle.Rate24, Width: x.W, Height: x.H, HasAudio: sl.HasClip && info.HasAudio}
			c := nle.Clip{Media: m, Name: title, Start: x.f24(cut[k]), Scene: sceneNo(i), Role: nle.RoleScene}
			c.Len = x.f24(cut[k+1]) - c.Start
			v1.Clips = append(v1.Clips, c)
			x.rows = append(x.rows, editRow{Track: "V1", Scene: sceneNo(i), File: m.Rel, In: 0, Out: float64(b-a) / media.FPS,
				Start: float64(cut[k]) / media.FPS, Len: float64(b-a) / media.FPS, Transition: x.transitionLabel(k)})
			continue
		}

		if sl.HasClip {
			src := seg.Video
			m, err := x.linkMedia(src, num+"_scene"+strings.ToLower(filepath.Ext(src)), nle.Movie)
			if err != nil {
				return nil, err
			}
			if end := min(b, cf); end > a {
				c := nle.Clip{Media: m, Name: title, Start: x.f24(S + a), Scene: sceneNo(i), Role: nle.RoleScene}
				c.Len = x.f24(S+end) - c.Start
				c.In = x.sec(sl.TrimIn + float64(a)/media.FPS)
				c.Mute = !m.HasAudio || vol <= 0
				if !c.Mute {
					c.Gain = gainDB(vol)
				}
				x.motion(&c, sl, a, end, F)
				v1.Clips = append(v1.Clips, c)
				if !c.Mute {
					s := c
					s.Role, s.Motion = nle.RoleSound, nil
					a1.Clips = append(a1.Clips, s)
				}
				x.rows = append(x.rows, editRow{Track: "V1", Scene: sceneNo(i), File: m.Rel, In: sl.TrimIn + float64(a)/media.FPS,
					Out: sl.TrimIn + float64(end)/media.FPS, Start: float64(S+a) / media.FPS, Len: float64(end-a) / media.FPS,
					Transition: x.transitionLabel(k)})
			}
			if b > cf {
				// The scene holds the clip's last frame (its voice is longer).
				// The clip's last used frame (a quarter frame early: seeking
				// keeps the first frame at or after the time).
				at := sl.TrimIn + (float64(cf-1)-0.25)/media.FPS
				info := x.probe[src]
				hm, err := x.stillMedia(src, at, false, num+"_hold.jpg", info.Width, info.Height)
				if err != nil {
					return nil, err
				}
				from := max(a, cf)
				c := nle.Clip{Media: hm, Name: title + " · утримання кадру", Start: x.f24(S + from), Scene: sceneNo(i), Role: nle.RoleHold}
				c.Len = x.f24(S+b) - c.Start
				x.motion(&c, sl, from, b, F)
				if c.Len > 0 {
					v1.Clips = append(v1.Clips, c)
					x.rows = append(x.rows, editRow{Track: "V1", Scene: sceneNo(i), File: hm.Rel, Start: float64(S+from) / media.FPS,
						Len: float64(b-from) / media.FPS, Note: "утримання останнього кадру"})
				}
			}
		} else {
			m, err := x.stillMedia(seg.Frame, 0, true, num+"_frame.jpg", x.W, x.H)
			if err != nil {
				return nil, err
			}
			c := nle.Clip{Media: m, Name: title + " · кадр", Start: x.f24(cut[k]), Scene: sceneNo(i), Role: nle.RoleStill}
			c.Len = x.f24(cut[k+1]) - c.Start
			x.motion(&c, sl, a, b, F)
			v1.Clips = append(v1.Clips, c)
			x.rows = append(x.rows, editRow{Track: "V1", Scene: sceneNo(i), File: m.Rel, Start: float64(cut[k]) / media.FPS,
				Len: float64(b-a) / media.FPS, Transition: x.transitionLabel(k), Note: "кадр з ефектом «" + effectName(sl.Effect) + "»"})
		}
	}
	if x.tgt.Timeline {
		x.transitions(&v1, &a1, cut)
	} else if x.opt.Target == ExportCapCut {
		x.note("Сцени склеєні різко, без переходів і без згасання на початку й у кінці — додай їх у CapCut (переходи CapCut накладають сцени, тож став короткі й там, де немає голосу).")
	} else {
		x.note("Переходи між сценами (їхні назви — в edit-list.csv) зроби в редакторі.")
	}
	t.Video = append(t.Video, v1)

	if x.opt.Overlays && p.Input.TextOverlays() {
		if v2, err := x.overlays(); err != nil {
			return nil, err
		} else if len(v2.Clips) > 0 {
			t.Video = append(t.Video, v2)
		}
	}
	if len(a1.Clips) > 0 {
		t.Audio = append(t.Audio, a1)
	}
	if x.opt.Voice {
		lanes, err := x.voices()
		if err != nil {
			return nil, err
		}
		t.Audio = append(t.Audio, lanes...)
	}
	if x.opt.Music && isFile(x.opts.MusicPath) {
		mt, err := x.music()
		if err != nil {
			return nil, err
		}
		t.Audio = append(t.Audio, mt)
	}
	if !x.opt.Voice && x.ready.Voices > 0 {
		x.note("Озвучку не включено — сцени все одно тривають стільки, скільки з голосом.")
	}
	x.styleNotes()
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("таймлайн не зійшовся: %w", err)
	}
	return t, nil
}

func gainDB(v float64) float64 {
	if v <= 0 {
		return -96
	}
	return math.Round(20*math.Log10(v)*100) / 100
}

func effectName(id string) string {
	if e, ok := media.EffectByID(id); ok {
		return e.Label
	}
	return id
}

// transitionLabel names the transition into scene slot k ("" for the first).
func (x *exporter) transitionLabel(k int) string {
	if k == 0 {
		return ""
	}
	if t, ok := media.TransitionByID(x.slots[k-1].Transition); ok {
		return t.Label
	}
	return ""
}

// motion adds the slot's motion effect over its local frames from…to as
// transform keyframes (eased moves get more keys).
func (x *exporter) motion(c *nle.Clip, sl media.Slot, from, to, F int) {
	mv, ok := media.EffectMove(sl.Effect, sl.HasClip, x.W, x.H)
	if !ok || to <= from || c.Len <= 0 {
		return
	}
	if mv.Shake {
		x.note("Ефект «Ручна камера» перенесено як нерухоме наближення — тремтіння додай у редакторі.")
	}
	if sl.HasClip {
		x.note("Ефект руху на кліпах перенесено ключовими кадрами масштабу й позиції.")
	}
	steps := 1
	if mv.Ease {
		steps = 8
	}
	den := float64(max(F-1, 1))
	for s := 0; s <= steps; s++ {
		j := from + (to-1-from)*s/steps
		sc, px, py, rot := mv.At(float64(j) / den)
		at := int64(float64(c.Len-1) * float64(s) / float64(steps))
		if len(c.Motion) > 0 && c.Motion[len(c.Motion)-1].At == at {
			continue
		}
		c.Motion = append(c.Motion, nle.Key{At: at, Scale: sc, X: px, Y: py, Rotation: rot})
	}
}

// transitions puts the montage's transitions on the cuts of V1 (and the
// clips' sound crossfades on A1) and the film's opening/closing fades on
// its first/last picture.
func (x *exporter) transitions(v1, a1 *nle.Track, cut []int) {
	approx := map[string]int{}
	shortened := 0
	for k := 1; k < len(x.slots); k++ {
		prev, sl := x.slots[k-1], x.slots[k]
		at := x.f24(cut[k])
		var tr nle.Transition
		switch prev.TransitionKind() {
		case media.KindXFade:
			in := sl.InFrames()
			tr = nle.Transition{At: at, Len: x.f24(sl.StartFrame()+in) - x.f24(sl.StartFrame()), Kind: nle.Dissolve, Source: prev.Transition}
			if prev.Transition != "dissolve" {
				approx[prev.Transition]++
			}
		case media.KindDip:
			_, fo, _, col := prev.Fades()
			fi, _, _, _ := sl.Fades()
			tr = nle.Transition{At: at, Len: x.sec(fo) + x.sec(fi), Kind: nle.DipBlack, Source: prev.Transition}
			if col == "white" {
				tr.Kind = nle.DipWhite
			}
		default:
			continue
		}
		if tr.Len < 2 {
			continue
		}
		if l := handles(v1, tr, x.rate); l < tr.Len {
			if tr.Kind != nle.Dissolve {
				// A dip needs media past the cut in editors (Resolve leaves
				// such a clip offline): fade the two clips instead — exactly
				// what the montage does.
				if ia, ib := clipEnding(v1, at), clipStarting(v1, at); ia >= 0 && ib >= 0 {
					_, fo, _, col := prev.Fades()
					fi, _, _, _ := sl.Fades()
					v1.Clips[ia].FadeOut = max(1, x.sec(fo))
					v1.Clips[ib].FadeIn = max(1, x.sec(fi))
					if col == "white" {
						x.note("Перехід «Висвітлення» без запасу кадрів перенесено як затемнення (згасання в чорне).")
					}
				}
				continue
			}
			shortened++
			tr.Len = l
			if tr.Len < 2 {
				continue
			}
		}
		v1.Transitions = append(v1.Transitions, tr)
		// The clips' own sound crossfades with the picture.
		if tr.Kind == nle.Dissolve {
			if ia, ib := clipEnding(a1, at), clipStarting(a1, at); ia >= 0 && ib >= 0 {
				st := tr
				st.Len = min(tr.Len, handles(a1, tr, x.rate))
				if st.Len >= 2 {
					a1.Transitions = append(a1.Transitions, st)
				}
			}
		}
	}
	for id, n := range approx {
		if t, ok := media.TransitionByID(id); ok {
			x.note(fmt.Sprintf("Перехід «%s» (%d) перенесено як розчинення тієї ж тривалості — за бажання заміни його в редакторі.", t.Label, n))
		}
	}
	if shortened > 0 {
		x.note(fmt.Sprintf("%d %s скорочено: у кліпу не вистачає кадрів за склейкою.", shortened, pluralUK(shortened, "перехід", "переходи", "переходів")))
	}
	// Opening and closing fades of the film.
	if len(v1.Clips) > 0 {
		if fi, _, _, _ := x.slots[0].Fades(); fi > 0 {
			v1.Clips[0].FadeIn = max(1, x.sec(fi))
		}
		if _, fo, _, _ := x.slots[len(x.slots)-1].Fades(); fo > 0 {
			v1.Clips[len(v1.Clips)-1].FadeOut = max(1, x.sec(fo))
		}
	}
}

func clipEnding(tr *nle.Track, at int64) int {
	for i, c := range tr.Clips {
		if c.End() == at {
			return i
		}
	}
	return -1
}

func clipStarting(tr *nle.Track, at int64) int {
	for i, c := range tr.Clips {
		if c.Start == at {
			return i
		}
	}
	return -1
}

// handles is the longest centred transition (at most x.Len) the two clips
// around the cut x.At can carry: a transition of L frames shows L/2 frames
// of the incoming clip's media before its in point and the rest of the
// outgoing clip's media after its out point (pictures have no limit), and
// stays inside both clips.
func handles(tr *nle.Track, x nle.Transition, r nle.Rate) int64 {
	ia, ib := clipEnding(tr, x.At), clipStarting(tr, x.At)
	if ia < 0 || ib < 0 {
		return 0
	}
	a, b := tr.Clips[ia], tr.Clips[ib]
	after, before := a.Len, b.Len // frames available after / before the cut
	before = min(a.Len, before)
	after = min(b.Len, after)
	if a.Media.Kind != nle.Picture && a.Media.Duration > 0 {
		after = min(after, r.Frames(a.Media.Duration)-(a.In+a.Len))
	}
	if b.Media.Kind != nle.Picture {
		before = min(before, b.In)
	}
	if after <= 0 || before < 0 {
		return 0
	}
	// floor(L/2) ≤ before and ceil(L/2) ≤ after.
	m := min(before, after)
	l := 2 * m
	if after > m {
		l++
	}
	return min(x.Len, l)
}

// overlays renders the on-screen texts as transparent full-frame PNGs on V2.
func (x *exporter) overlays() (nle.Track, error) {
	v2 := nle.Track{Name: "V2 · Текст у кадрі", Kind: nle.VideoTrack}
	size := image.Pt(x.W, x.H)
	for _, sl := range x.slots {
		sc := x.p.Scenes[sl.Segment]
		if sc.OnScreen.Empty() {
			continue
		}
		if err := x.ctx.Err(); err != nil {
			return v2, err
		}
		dur := float64(sl.Frames()) / media.FPS
		show := float64(sl.InFrames())/media.FPS + overlayLead
		hide := dur - float64(sl.OutFrames())/media.FPS - overlayTail
		if hide-show < overlayMin {
			continue
		}
		clip, err := cover.NewOverlayClip(overlayOf(x.p, sc.OnScreen), size, show, hide)
		if err != nil {
			return v2, fmt.Errorf("текст сцени %d: %w", sceneNo(sl.Segment), err)
		}
		box := clip.Bounds()
		if box.Empty() {
			continue
		}
		// The design at rest: the middle of the longest still run.
		rest, best, run := (show+hide)/2, 0, 0
		for f := 0; f <= int((hide-show)*media.FPS); f++ {
			tt := show + float64(f)/media.FPS
			if still, id := clip.Still(tt); still && id == 1 {
				run++
				if run > best {
					best, rest = run, tt-float64(run-1)/media.FPS/2
				}
			} else {
				run = 0
			}
		}
		frame := image.NewRGBA(image.Rectangle{Max: size})
		draw.Draw(frame, box, clip.Frame(rest), image.Point{}, draw.Src)
		name := fmt.Sprintf("%03d_text.png", sceneNo(sl.Segment))
		path := filepath.Join(x.out, "media", name)
		if err := writePNG(path, frame); err != nil {
			return v2, err
		}
		x.files++
		m := &nle.Media{ID: "m" + strings.TrimSuffix(name, ".png"), Name: name, Rel: "media/" + name, Abs: path, Kind: nle.Picture,
			Width: x.W, Height: x.H, Alpha: true}
		start := float64(sl.StartFrame())/media.FPS + show
		c := nle.Clip{Media: m, Name: x.sceneTitle(sl.Segment) + " · текст", Start: x.sec(start), Scene: sceneNo(sl.Segment), Role: nle.RoleText}
		c.Len = x.sec(start+hide-show) - c.Start
		c.FadeIn, c.FadeOut = min(x.sec(0.3), c.Len/3), min(x.sec(0.3), c.Len/3)
		if c.Len > 0 {
			v2.Clips = append(v2.Clips, c)
			x.texts = append(x.texts, textRow{Scene: sceneNo(sl.Segment), File: m.Rel, Start: start, Len: hide - show})
			x.rows = append(x.rows, editRow{Track: "V2", Scene: sceneNo(sl.Segment), File: m.Rel, Start: start, Len: hide - show, Note: "текст у кадрі"})
		}
	}
	switch {
	case len(v2.Clips) == 0:
	case x.tgt.Timeline:
		x.note("Анімований текст у кадрі перенесено як прозорі PNG із плавною появою — анімацію (вліт, лічильник) за бажання зроби в редакторі.")
	default:
		x.note("Текст у кадрі — прозорі PNG без анімації: постав їх у час, указаний нижче, а появу зроби в редакторі.")
	}
	return v2, nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// voices lays the voice lines out on as many lanes as moved lines need.
func (x *exporter) voices() ([]nle.Track, error) {
	var lanes []nle.Track
	for _, sl := range x.slots {
		seg := x.opts.Segments[sl.Segment]
		if seg.Voice == "" || sl.VoiceDuration <= 0 {
			continue
		}
		if err := x.ctx.Err(); err != nil {
			return nil, err
		}
		num := fmt.Sprintf("%03d", sceneNo(sl.Segment))
		m, err := x.linkMedia(seg.Voice, num+"_voice"+strings.ToLower(filepath.Ext(seg.Voice)), nle.Sound)
		if err != nil {
			return nil, err
		}
		c := nle.Clip{Media: m, Name: x.sceneTitle(sl.Segment) + " · голос", Start: x.sec(sl.VoiceStart), Scene: sceneNo(sl.Segment), Role: nle.RoleVoice}
		c.Len = x.sec(sl.VoiceEnd) - c.Start
		c.In = x.sec(sl.VoiceTrimIn)
		// Never past the file's last whole frame (editors show the rest as missing).
		if have := int64(math.Floor(m.Duration*x.rate.FPS() + 1e-6)); m.Duration > 0 && c.In+c.Len > have {
			c.Len = max(1, have-c.In)
		}
		if c.Len <= 0 {
			continue
		}
		x.rows = append(x.rows, editRow{Track: "A", Scene: sceneNo(sl.Segment), File: m.Rel, In: sl.VoiceTrimIn,
			Out: sl.VoiceTrimIn + sl.VoiceDuration, Start: sl.VoiceStart, Len: sl.VoiceDuration, Note: "голос"})
		placed := false
		for li := range lanes {
			l := &lanes[li]
			if l.Clips[len(l.Clips)-1].End() <= c.Start {
				l.Clips = append(l.Clips, c)
				placed = true
				break
			}
		}
		if !placed {
			lanes = append(lanes, nle.Track{Name: fmt.Sprintf("A%d · Голос", len(lanes)+2), Kind: nle.AudioTrack, Clips: []nle.Clip{c}})
		}
	}
	for i := range lanes {
		sort.SliceStable(lanes[i].Clips, func(a, b int) bool { return lanes[i].Clips[a].Start < lanes[i].Clips[b].Start })
	}
	if len(lanes) > 0 && x.tgt.Timeline {
		x.note("Голос стоїть з точністю до кадру (±½ кадру); точна до семпла доріжка — stems/voice.wav, якщо її увімкнено.")
	}
	return lanes, nil
}

// duckDB is how far the montage's sidechain compressor (threshold −34 dBFS,
// ratio 12) pulls the music under a typical voice line (≈ −20 dBFS).
const duckDB = -12

// music loops the music over the film with its level, the fade-out and the
// ducking as volume keyframes.
func (x *exporter) music() (nle.Track, error) {
	mt := nle.Track{Name: "Музика", Kind: nle.AudioTrack}
	src := x.opts.MusicPath
	m, err := x.linkMedia(src, "music"+strings.ToLower(filepath.Ext(src)), nle.Sound)
	if err != nil {
		return mt, err
	}
	base := gainDB(clamp01(x.opts.MusicVolume))
	keys := x.musicKeys(base)
	L := m.Duration
	if L <= 0.5 {
		return mt, errors.New("музика: файл порожній або не читається")
	}
	for at := 0.0; at < x.total-0.01; at += L {
		start := x.sec(at)
		end := min(x.sec(at+L), x.sec(x.total))
		if have := x.rate.Frames(L); end-start > have {
			end = start + have
		}
		if end <= start {
			break
		}
		c := nle.Clip{Media: m, Name: "Музика", Start: start, Len: end - start, Role: nle.RoleMusic, Gain: base}
		// Keyframes inside this piece (+ its edges).
		lv := func(s float64) float64 { return levelAt(keys, s) }
		c.Levels = append(c.Levels, nle.Level{At: 0, DB: lv(x.rate.Seconds(start))})
		for _, k := range keys {
			f := x.sec(k.t) - start
			if f > 0 && f < c.Len-1 && (len(c.Levels) == 0 || c.Levels[len(c.Levels)-1].At < f) {
				c.Levels = append(c.Levels, nle.Level{At: f, DB: k.db})
			}
		}
		if lastAt := c.Len - 1; c.Levels[len(c.Levels)-1].At < lastAt {
			c.Levels = append(c.Levels, nle.Level{At: lastAt, DB: lv(x.rate.Seconds(start + lastAt))})
		}
		if allSame(c.Levels) {
			c.Gain, c.Levels = c.Levels[0].DB, nil
		}
		mt.Clips = append(mt.Clips, c)
		x.rows = append(x.rows, editRow{Track: "M", File: m.Rel, In: 0, Out: x.rate.Seconds(end - start), Start: at, Len: x.rate.Seconds(end - start), Note: "музика"})
	}
	if x.opts.Ducking && x.tgt.Timeline {
		x.note(fmt.Sprintf("Приглушення музики під голосом перенесено ключовими кадрами гучності (%d дБ); точна копія фільму — stems/music.wav, якщо її увімкнено.", duckDB))
	}
	return mt, nil
}

type levelKey struct{ t, db float64 }

// musicKeys are the music's level changes over the film (seconds, dB).
func (x *exporter) musicKeys(base float64) []levelKey {
	var keys []levelKey
	if x.opts.Ducking {
		// Speech: the voice lines and the clips that talk.
		var spans [][2]float64
		for _, sl := range x.slots {
			seg := x.opts.Segments[sl.Segment]
			if seg.Voice != "" && sl.VoiceDuration > 0 {
				spans = append(spans, [2]float64{sl.VoiceStart, sl.VoiceEnd})
			}
			if seg.ClipSpeech && sl.HasClip {
				spans = append(spans, [2]float64{sl.Start, sl.Start + sl.Duration})
			}
		}
		sort.Slice(spans, func(a, b int) bool { return spans[a][0] < spans[b][0] })
		var merged [][2]float64
		for _, s := range spans {
			if n := len(merged); n > 0 && s[0] <= merged[n-1][1]+0.6 {
				merged[n-1][1] = math.Max(merged[n-1][1], s[1])
				continue
			}
			merged = append(merged, s)
		}
		low := base + duckDB
		for _, s := range merged {
			a := math.Max(0, s[0]-0.05)
			keys = append(keys, levelKey{a, base}, levelKey{a + 0.05, low}, levelKey{s[1], low}, levelKey{s[1] + 0.45, base})
		}
	}
	fade := math.Max(0, x.total-2.5)
	var out []levelKey
	for _, k := range keys {
		if k.t < fade {
			out = append(out, k)
		}
	}
	out = append(out, levelKey{fade, levelAt(keys, fade, base)}, levelKey{x.total, -96})
	return out
}

// levelAt interpolates the keys at s (before the first: its value; base
// when given and there are no keys).
func levelAt(keys []levelKey, s float64, base ...float64) float64 {
	if len(keys) == 0 {
		if len(base) > 0 {
			return base[0]
		}
		return 0
	}
	if s <= keys[0].t {
		if len(base) > 0 {
			return base[0]
		}
		return keys[0].db
	}
	for i := 1; i < len(keys); i++ {
		a, b := keys[i-1], keys[i]
		if s <= b.t {
			if b.t == a.t {
				return b.db
			}
			return a.db + (b.db-a.db)*(s-a.t)/(b.t-a.t)
		}
	}
	return keys[len(keys)-1].db
}

func allSame(ls []nle.Level) bool {
	for _, l := range ls {
		if l.DB != ls[0].DB {
			return false
		}
	}
	return true
}

// styleNotes lists the parts of the montage look editors cannot read.
func (x *exporter) styleNotes() {
	if f, ok := media.FilterByID(x.opts.Filter); ok && f.ID != "none" {
		x.note("Кольоровий фільтр «" + f.Label + "» не переноситься — зроби корекцію кольору в редакторі.")
	}
	if x.opt.Subtitles && x.p.Montage.Subtitles == "burn" {
		x.note("Субтитри йдуть окремим файлом subtitles.srt — їхній вигляд налаштуй у редакторі.")
	}
	white := false
	for _, sl := range x.slots {
		if _, _, _, col := sl.Fades(); col == "white" {
			white = true
		}
	}
	if white && x.tgt.Timeline {
		x.note("Перехід «Висвітлення» (через біле) у редакторі може стати затемненням — вибери білий колір у переході.")
	}
}

// ---- files of the package ----------------------------------------------------------------------

// writeTimeline writes the target's timeline files; the main one is returned.
func (x *exporter) writeTimeline(t *nle.Timeline) (string, error) {
	type file struct {
		name string
		data func() ([]byte, error)
	}
	var files []file
	main := ""
	fcpxml := func() ([]byte, error) { return nle.FCPXML(t, nle.FCPXMLOptions{}) }
	switch x.opt.Target {
	case ExportResolve:
		// Resolve 21.1 (checked through its scripting API): the FCP 7 XML
		// comes in exact — every track in place, dips, every keyframe; from
		// FCPXML it packs the sound onto whatever track is free and reads
		// no keyframes on pictures (StaticStills).
		resolve := func() ([]byte, error) { return nle.FCPXML(t, nle.FCPXMLOptions{StaticStills: true}) }
		files = []file{{"timeline.xml", func() ([]byte, error) { return nle.XMEML(t, nle.XMEMLOptions{}) }},
			{"timeline.fcpxml", resolve}, {"timeline.edl", func() ([]byte, error) { return nle.EDL(t) }}}
		main = "timeline.xml"
	case ExportFinalCut:
		files = []file{{"timeline.fcpxml", fcpxml}, {"timeline.fcpxmld/Info.fcpxml", fcpxml}}
		main = "timeline.fcpxmld"
	case ExportPremiere:
		files = []file{{"timeline.xml", func() ([]byte, error) { return nle.XMEML(t, nle.XMEMLOptions{}) }}}
		main = "timeline.xml"
	case ExportKdenlive:
		files = []file{{"timeline.otio", func() ([]byte, error) { return nle.OTIO(t, nle.OTIOOptions{}) }},
			{"timeline.mlt", func() ([]byte, error) { return nle.MLT(t, nle.MLTOptions{}) }}}
		main = "timeline.otio"
	case ExportOTIO:
		files = []file{{"timeline.otio", func() ([]byte, error) { return nle.OTIO(t, nle.OTIOOptions{}) }}}
		main = "timeline.otio"
	}
	for _, f := range files {
		data, err := f.data()
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.name, err)
		}
		if err := x.write(f.name, data); err != nil {
			return "", err
		}
	}
	if main == "" {
		return "", nil
	}
	return filepath.Join(x.out, filepath.FromSlash(main)), nil
}

func (x *exporter) cover() error {
	for _, a := range []Asset{x.p.Montage.CoverFinal, x.p.Montage.Cover} {
		src := filepath.Join(x.dir, filepath.FromSlash(a.Path))
		if a.Path == "" || !isFile(src) {
			continue
		}
		if err := linkOrCopy(src, filepath.Join(x.out, "cover"+strings.ToLower(filepath.Ext(src)))); err != nil {
			return fmt.Errorf("обкладинка: %w", err)
		}
		x.files++
		return nil
	}
	return nil
}

// stems writes stems/voice.wav and stems/music.wav (the montage's own mix of each).
func (x *exporter) stems() error {
	if x.opt.Voice {
		x.progress(75, "Голос однією доріжкою")
		ok, err := x.ff.VoiceStem(x.ctx, x.opts, filepath.Join(x.out, "stems", "voice.wav"))
		if err != nil {
			return fmt.Errorf("доріжка голосу: %w", err)
		}
		if ok {
			x.files++
		}
	}
	if x.opt.Music && isFile(x.opts.MusicPath) {
		x.progress(84, "Музика однією доріжкою")
		ok, err := x.ff.MusicStem(x.ctx, x.opts, filepath.Join(x.out, "stems", "music.wav"))
		if err != nil {
			return fmt.Errorf("доріжка музики: %w", err)
		}
		if ok {
			x.files++
		}
	}
	return nil
}

// zipDir packs dir into dst (media stored as is, text deflated); the paths
// inside start with the folder's name.
func zipDir(ctx context.Context, dir, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	base := filepath.Base(dir)
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = base + "/" + filepath.ToSlash(rel)
		h.Method = zip.Store
		switch strings.ToLower(filepath.Ext(path)) {
		case ".txt", ".srt", ".csv", ".xml", ".fcpxml", ".otio", ".mlt", ".edl", ".wav":
			h.Method = zip.Deflate
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		return err
	})
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
