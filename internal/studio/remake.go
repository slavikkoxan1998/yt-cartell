package studio

// «Перезняти за прикладом»: the user pastes a YouTube link and says what to
// change; a video model watches the example and the studio makes a NEW
// project after it (package reference does the watching).
//
//	StartRemake   a project (the profile or «як у прикладі», the language) +
//	              Project.Reference, then the JobRemake job:
//	  1 probe     the watch page and oEmbed (no key): private, unlisted,
//	              removed, live, too long → refused at once; the thumbnail
//	  2 watch     Gemini watches and listens (Google AI Studio keys; or the
//	              text gateway when it really can — the remake dialog's «Хто дивиться відео»,
//	              config.Remake): the breakdown, saved as
//	              reference/breakdown.json and as the editable Ukrainian text
//	              Reference.Breakdown («Розбір прикладу» in step 1)
//	  3 script    the text model writes the new screenplay from that text
//	              (edited or not), the fidelity and the user's wish: EXACTLY
//	              N scenes of one clip each (N = the length / the clip length),
//	              scene k covering its share of the original's time; the
//	              project gets it as a story-mode text the planner follows
//	              scene by scene (TargetSeconds = N × clip, so the plan has N
//	              scenes), plus the look of the original: aspect, style, genre,
//	              tempo, camera, tone, sound mode, content, on-screen text and
//	              English director's notes — without a profile. With a
//	              profile its look, pacing, voice and modes win: the
//	              screenwriter is told to draw every picture in the
//	              profile's style (profileLook) and only the content follows
//	              the reference
//	  4 autopilot when asked: the same job turns into the autopilot (its kind
//	              becomes JobAutopilot, so the UI and «Повторити» treat it as one)
//
// A failed or stopped remake continues where it stopped (Reference.Stage);
// RewriteRemake writes the screenplay again from the (edited) breakdown,
// ReanalyzeRemake watches the video again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/llm"
	"ytcartell/internal/reference"
)

// JobRemake is the background stage of a remake (StartRemake).
const JobRemake JobKind = "remake"

func init() {
	jobLabels[JobRemake] = "Перезйомка"
	jobRestarts[JobRemake] = (*Engine).resumeRemake // «Повторити» in the queue
	// jobAssets has no entry on purpose: a remake that went on into the
	// autopilot reports every kind of failed item.
}

// Fidelity of a remake (Reference.Fidelity; stored ids).
const (
	FidelityExact     = "exact"     // «Точна копія»
	FidelityClose     = "close"     // «Близько до оригіналу»
	FidelityStructure = "structure" // «Лише структура й темп»
)

// Stages of a remake (Reference.Stage).
const (
	remakeAnalysed = "analysed" // the breakdown is there
	remakeScripted = "scripted" // the screenplay is in the project
)

// Reference is the YouTube video a remake follows (Project.Reference).
type Reference struct {
	URL        string `json:"url"`
	VideoID    string `json:"videoId,omitempty"`
	Wish       string `json:"wish,omitempty"`     // what the user wants changed / to see
	Fidelity   string `json:"fidelity,omitempty"` // FidelityExact | FidelityClose | FidelityStructure
	KeepLength bool   `json:"keepLength,omitempty"`
	KeepStyle  bool   `json:"keepStyle,omitempty"` // a profile was picked: its look, pacing and voice win (profileLook)
	Autopilot  bool   `json:"autopilot,omitempty"` // the autopilot follows the screenplay

	Title      string  `json:"title,omitempty"`
	Channel    string  `json:"channel,omitempty"`
	Duration   float64 `json:"duration,omitempty"`
	Aspect     string  `json:"aspect,omitempty"`
	Language   string  `json:"language,omitempty"` // spoken in the original
	Scenes     int     `json:"scenes,omitempty"`   // in the breakdown
	CutsPerMin float64 `json:"cutsPerMin,omitempty"`
	People     string  `json:"people,omitempty"`
	AudioMode  string  `json:"audioMode,omitempty"`
	Via        string  `json:"via,omitempty"`   // who watched ("Gemini · gemini-3.8-flash")
	Thumb      string  `json:"thumb,omitempty"` // reference/thumb.jpg (relative)
	// Breakdown is the editable «Розбір прикладу»: the screenplay is written
	// from this text.
	Breakdown  string `json:"breakdown,omitempty"`
	Stage      string `json:"stage,omitempty"`
	AnalyzedAt int64  `json:"analyzedAt,omitempty"`
	ScriptedAt int64  `json:"scriptedAt,omitempty"`
}

// Active reports a remake project.
func (r Reference) Active() bool { return r.URL != "" }

// RemakeOptions describe a new remake.
type RemakeOptions struct {
	URL      string // a YouTube link (watch, youtu.be, Shorts …)
	Wish     string // «Що змінити / що хочу бачити» ("" = the same topic, new words)
	Fidelity string // "" = FidelityClose

	KeepLength    bool // the original's length
	TargetSeconds int  // when !KeepLength (0 = the profile's)

	Language    string // "" + LanguageSet = auto (the wish's, else the original's)
	LanguageSet bool
	ProfileID   string // "" + NoProfile = everything «як у прикладі»
	NoProfile   bool
	Autopilot   bool
}

const (
	remakeDir    = "reference"
	maxWish      = 4000
	maxBreakdown = 120_000
	scriptRunes  = 60_000 // of the breakdown sent to the script writer
)

// Seams for tests.
var (
	remakeProbe   = reference.Probe
	remakeThumb   = reference.FetchThumb
	remakeAnalyze = reference.Analyze
	// remakeWatchers builds the watchers to try, in order.
	remakeWatchers = func(e *Engine, meta reference.Meta) []reference.Watcher { return e.watchers(meta) }
)

var errNoRemake = errors.New("Це не проєкт «за прикладом»")

// ---- the service that watches -----------------------------------------------------------------

// RemakeService is who will watch the example (for the remake sheet).
type RemakeService struct {
	Analysis string // config.Remake.Analysis: "" auto | "google" | "text"
	Via      string // "google" | "text" | "" (none ready)
	Model    string
	Keys     int  // Google AI Studio keys
	Ready    bool // a remake can start
	TextCan  bool // the text gateway proved it can watch YouTube (this session)
	TextNot  bool // … proved it cannot
	Note     string
}

// googleModel is the Gemini model that watches.
func googleModel(cfg config.Config) string {
	if m := strings.TrimSpace(cfg.Remake.Model); m != "" {
		return m
	}
	if m := strings.TrimSpace(cfg.Google.StoryModel); m != "" {
		return m
	}
	return "gemini-3.8-flash"
}

// textGateway: the OpenAI-compatible endpoint is configured.
func textGateway(cfg config.Config) bool {
	return cfg.Text.OpenAICompatible() && strings.TrimSpace(cfg.Text.BaseURL) != "" && strings.TrimSpace(cfg.Text.Model) != ""
}

// RemakeService says who will watch the example with the current settings
// (no network: what the text gateway can do is known once it was tried).
func (e *Engine) RemakeService() RemakeService {
	cfg := e.cfg.Get()
	s := RemakeService{Analysis: cfg.Remake.Analysis, Keys: len(cfg.Google.Keys)}
	textOK := textGateway(cfg)
	if textOK {
		s.TextCan, _ = reference.TextCan(cfg.Text.BaseURL, cfg.Text.Model)
		if can, known := reference.TextCan(cfg.Text.BaseURL, cfg.Text.Model); known && !can {
			s.TextNot = true
		}
	}
	google := func() {
		s.Via, s.Model, s.Ready = "google", googleModel(cfg), true
		s.Note = fmt.Sprintf("Відео подивиться й послухає Gemini (%s) через твій ключ Google AI Studio.", s.Model)
	}
	text := func() {
		s.Via, s.Model, s.Ready = "text", cfg.Text.Model, true
		s.Note = fmt.Sprintf("Відео подивиться текстовий сервіс (%s). Студія спершу перевірить, що він справді бачить YouTube.", s.Model)
		if s.TextCan {
			s.Note = fmt.Sprintf("Відео подивиться текстовий сервіс (%s) — він уміє дивитися YouTube.", s.Model)
		}
	}
	noKey := "Додай ключ Google AI Studio в Налаштуваннях → Підключення — це все, що потрібно, щоб студія подивилась відео."
	switch cfg.Remake.Analysis {
	case config.RemakeGoogle:
		if s.Keys > 0 {
			google()
		} else {
			s.Note = noKey
		}
	case config.RemakeText:
		switch {
		case !textOK:
			s.Note = "Текстовий сервіс не налаштовано (Налаштування → Підключення). Або вибери Google AI Studio."
		case s.TextNot:
			s.Note = "Текстовий сервіс не вміє дивитися YouTube. " + noKey
		default:
			text()
		}
	default:
		switch {
		case s.Keys > 0:
			google()
		case textOK && !s.TextNot:
			text()
		default:
			s.Note = noKey
		}
	}
	return s
}

// watchers are the models to try with the current settings, in order.
func (e *Engine) watchers(meta reference.Meta) []reference.Watcher {
	cfg := e.cfg.Get()
	var g, t reference.Watcher
	if len(cfg.Google.Keys) > 0 {
		g = &reference.Google{Pool: e.gem.Pool(), Model: googleModel(cfg)}
	}
	if textGateway(cfg) {
		t = &reference.Text{BaseURL: cfg.Text.BaseURL, Key: cfg.Text.APIKey, Model: cfg.Text.Model, Truth: meta}
	}
	var out []reference.Watcher
	add := func(w reference.Watcher) {
		if w != nil {
			out = append(out, w)
		}
	}
	switch cfg.Remake.Analysis {
	case config.RemakeGoogle:
		add(g)
	case config.RemakeText:
		add(t)
	default:
		add(g)
		add(t)
	}
	return out
}

// ---- starting -------------------------------------------------------------------------------

// StartRemake creates a project after a YouTube example and starts the
// JobRemake job (probe → watch → screenplay → the autopilot when asked).
// Inputs are checked first; the project is removed again when the job cannot
// start.
func (e *Engine) StartRemake(opt RemakeOptions) (Project, error) {
	if e.closing.Load() {
		return Project{}, ErrClosing
	}
	url, err := reference.NormalizeURL(opt.URL)
	if err != nil {
		return Project{}, err
	}
	if s := e.RemakeService(); !s.Ready {
		return Project{}, errors.New(s.Note)
	}
	fid := normFidelity(opt.Fidelity)
	pid := strings.TrimSpace(opt.ProfileID)
	if opt.NoProfile {
		pid = ""
	}
	np := NewProjectOptions{Title: "Перезйомка за прикладом", ProfileID: pid, NoProfile: pid == "",
		Language: opt.Language, LanguageSet: opt.LanguageSet, Mode: ModeStory}
	if !opt.KeepLength && opt.TargetSeconds > 0 {
		np.TargetSeconds = opt.TargetSeconds
	}
	p, err := e.CreateProject(np)
	if err != nil {
		return Project{}, err
	}
	st := e.state(p.ID)
	if st == nil {
		return Project{}, ErrNotFound
	}
	e.update(st, func(q *Project) {
		q.Reference = Reference{
			URL: url, VideoID: reference.YouTubeID(url), Wish: clipRunes(strings.TrimSpace(opt.Wish), maxWish),
			Fidelity: fid, KeepLength: opt.KeepLength, KeepStyle: pid != "", Autopilot: opt.Autopilot,
		}
	})
	if err := e.startRemakeJob(st); err != nil {
		_ = e.Delete(p.ID)
		return Project{}, err
	}
	q, ok := e.Get(p.ID)
	if !ok {
		return Project{}, ErrNotFound
	}
	return q, nil
}

func normFidelity(f string) string {
	switch f {
	case FidelityExact, FidelityStructure:
		return f
	}
	return FidelityClose
}

func (e *Engine) startRemakeJob(st *proj) error {
	return e.startJobKind(st, JobRemake, func(p *Project) error {
		if !p.Reference.Active() {
			return errNoRemake
		}
		return nil
	}, e.runRemake)
}

// resumeRemake is «Повторити»: the remake continues where it stopped.
func (e *Engine) resumeRemake(id string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	return e.startRemakeJob(st)
}

// RewriteRemake writes the screenplay again from the (edited) breakdown.
// The project's text and look are replaced; a plan already made stays until
// the user plans again.
func (e *Engine) RewriteRemake(id string) error {
	return e.restartRemake(id, remakeAnalysed)
}

// ReanalyzeRemake lets the model watch the example again (the edited
// breakdown is replaced), then writes the screenplay.
func (e *Engine) ReanalyzeRemake(id string) error {
	return e.restartRemake(id, "")
}

func (e *Engine) restartRemake(id, stage string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var err error
	if !e.read(st, func(p *Project) bool {
		switch {
		case !p.Reference.Active():
			err = errNoRemake
		case stage == remakeAnalysed && strings.TrimSpace(p.Reference.Breakdown) == "":
			err = errors.New("Розбору ще немає — спершу дай студії подивитися приклад")
		case p.Job != nil && p.Job.Status == JobRunning:
			err = &busyError{p.Job.Kind}
		}
		return true
	}) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	e.update(st, func(p *Project) {
		p.Reference.Stage = stage
		p.Reference.Autopilot = false // a rewrite stops at the screenplay
	})
	return e.startRemakeJob(st)
}

// ---- the job ----------------------------------------------------------------------------------

// Progress of the remake job (percent).
const (
	rmProbeTo  = 5
	rmWatchTo  = 72
	rmScriptTo = 99
)

func (e *Engine) runRemake(j *jobCtx) (string, error) {
	snap := j.snapshot()
	ref := snap.Reference
	if !ref.Active() {
		return "", errNoRemake
	}
	if ref.Stage == "" {
		if err := e.remakeWatch(j, ref); err != nil {
			return "", err
		}
		if j.cancelled() {
			return "Зупинено", nil
		}
	}
	if ref = j.snapshot().Reference; ref.Stage == remakeAnalysed {
		if err := e.remakeScript(j, ref); err != nil {
			return "", err
		}
		if j.cancelled() {
			return "Зупинено", nil
		}
	}
	snap = j.snapshot()
	n, _, _ := e.Budget(snap)
	if snap.Reference.Autopilot {
		// The same job goes on as the autopilot: the UI, the queue and
		// «Повторити» see an autopilot from here on.
		e.update(j.st, func(p *Project) {
			if p.Job != nil && p.Job.Status == JobRunning {
				p.Job.Kind = JobAutopilot
			}
		})
		return e.runAutopilot(j, false, "")
	}
	return fmt.Sprintf("Розбір і сценарій готові · %s — перевір і склади план", sceneCount(n)), nil
}

func sceneCount(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d сцена", n)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return fmt.Sprintf("%d сцени", n)
	}
	return fmt.Sprintf("%d сцен", n)
}

// remakeWatch: probe, thumbnail, the model watches, the breakdown is stored.
func (e *Engine) remakeWatch(j *jobCtx, ref Reference) error {
	j.progress(1, "Перевіряю посилання…")
	meta, err := remakeProbe(j.ctx, nil, ref.VideoID)
	if err != nil {
		return err
	}
	dir := filepath.Join(j.st.dir, remakeDir)
	thumb := ""
	if err := os.MkdirAll(dir, 0o755); err == nil {
		if err := remakeThumb(j.ctx, nil, ref.VideoID, filepath.Join(dir, "thumb.jpg")); err == nil {
			thumb = remakeDir + "/thumb.jpg"
		}
	}
	e.update(j.st, func(p *Project) {
		r := &p.Reference
		r.Title, r.Channel, r.Duration = firstNonBlank(meta.Title, r.Title), firstNonBlank(meta.Channel, r.Channel), meta.Duration
		if thumb != "" {
			r.Thumb = thumb
		}
	})
	j.progress(rmProbeTo, "Дивиться відео…")
	if j.cancelled() {
		return nil
	}
	ws := remakeWatchers(e, meta)
	if len(ws) == 0 {
		return errors.New(e.RemakeService().Note)
	}
	var b reference.Breakdown
	var firstErr error
	for i, w := range ws {
		b, err = remakeAnalyze(j.ctx, w, reference.Options{URL: ref.URL, Meta: meta, Progress: func(pct float64, msg string) {
			j.progress(rmProbeTo+pct*(rmWatchTo-rmProbeTo)/100, msg)
		}})
		if err == nil || j.ctx.Err() != nil || !fallbackKind(reference.KindOf(err)) {
			break
		}
		if firstErr == nil {
			firstErr = err
		}
		if i < len(ws)-1 {
			log.Printf("[studio:%s] remake: %s could not watch (%v) — trying %s", j.st.id, w.Label(), err, ws[i+1].Label())
		}
	}
	if err != nil {
		if j.cancelled() {
			return nil
		}
		// Every service failed for its own reasons: the first (the one the
		// user chose or has keys for) explains best what to fix.
		if firstErr != nil && fallbackKind(reference.KindOf(err)) {
			return firstErr
		}
		return err
	}
	if raw, err := json.MarshalIndent(b, "", "  "); err == nil {
		_ = writeFileAtomic(filepath.Join(dir, "breakdown.json"), raw)
	}
	text := clipRunes(b.Text(languageLabel), maxBreakdown)
	e.update(j.st, func(p *Project) {
		r := &p.Reference
		r.Title = firstNonBlank(r.Title, b.Title)
		if b.Duration > 0 {
			r.Duration = b.Duration
		}
		r.Aspect, r.Language, r.Scenes, r.CutsPerMin = b.Aspect, b.Language, len(b.Scenes), b.CutsPerMin
		r.People, r.AudioMode, r.Via = b.People, b.AudioMode, b.Via
		r.Breakdown, r.Stage, r.AnalyzedAt = text, remakeAnalysed, nowMs()
	})
	return nil
}

// fallbackKind: an error the next watcher may not have (no key, quota, a
// gateway that cannot watch) — not one about the video itself.
func fallbackKind(k reference.Kind) bool {
	switch k {
	case reference.KindNoService, reference.KindQuota, reference.KindNotSeen, reference.KindOther, reference.KindNetwork:
		return true
	}
	return false
}

// languageLabel is the Ukrainian name of a language code, lower-case.
func languageLabel(code string) string {
	if l, ok := LanguageByCode(code); ok {
		return strings.ToLower(l.Label)
	}
	return ""
}

// ---- the screenplay ---------------------------------------------------------------------------

const remakeMarker = "You are the screenwriter and director of a studio that makes AI-generated YouTube videos"

// remakePlan is how long the new film is and how it is cut.
type remakePlan struct {
	scenes, clip, words int
	refDur              float64
	language            string // English name ("" = auto)
}

func (e *Engine) remakePlanFor(p *Project) remakePlan {
	clip := e.planClipFor(p.Input)
	target := float64(p.Input.TargetSeconds)
	ref := p.Reference
	if ref.KeepLength && ref.Duration > 0 {
		target = ref.Duration
	}
	if target <= 0 {
		target = 60
	}
	n := int(math.Round(target / float64(max(1, clip))))
	n = max(1, min(maxScenes, n))
	return remakePlan{scenes: n, clip: clip, words: wordsFor(p.Input, clip), refDur: ref.Duration, language: languageName(p.Input.Language)}
}

func fidelityRule(f string) string {
	switch f {
	case FidelityExact:
		return `FIDELITY: EXACT COPY. Recreate the reference as faithfully as possible: the same scenes in the same order and at the same moments, the same shot sizes, camera moves, actions, settings, objects, on-screen text, sound and pacing. The narration keeps the same meaning, beats and tone, rewritten in your own words. Change only what the author asks for.`
	case FidelityStructure:
		return `FIDELITY: STRUCTURE AND PACING ONLY. Keep the hook pattern, the beats and their share of time, the rhythm of the cuts, the narration style and energy. The topic, characters, places and pictures are NEW — taken from the author's wish.`
	}
	return `FIDELITY: CLOSE TO THE ORIGINAL. Keep the scene order, the shot types, the hook, the pacing and the narration style; adapt the content (topic, details, examples, characters, places) to the author's wish.`
}

// remakePrompt is the screenwriter's request.
func remakePrompt(p *Project, pl remakePlan) (system, prompt string) {
	ref := p.Reference
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line(remakeMarker + ` (an AI narrator, generated pictures and clips). The author wants a NEW video made after a reference video. You get the reference's breakdown — scene by scene, with timestamps (the author may have edited it) — and the author's wish. Write the screenplay of the new video.`)
	line(``)
	line(fidelityRule(ref.Fidelity))
	line(``)
	line(`Always:`)
	line(`- The author's wish wins wherever it differs from the reference (topic, language, characters, setting, details).`)
	line(`- Write the narration and dialogue anew in your own words; never copy the reference's sentences (a quote of up to 8 words only when it is essential, e.g. a catchphrase).`)
	line(`- Never recreate a real, identifiable person (a presenter, a celebrity) or the reference's logos, channel name or brand: make an original character of the same type (age, look, outfit, energy) instead.`)
	line(`  (Historical figures a history story is about may appear — drawn in the film's own style; the rule is about the reference's presenters and hosts.)`)
	refDur := pl.refDur
	if refDur <= 0 {
		refDur = float64(pl.scenes * pl.clip)
	}
	line(`- EXACTLY %d scenes. %s Scene k (1…%d) covers about [(k−1)·%.1f … k·%.1f] seconds of the reference: recreate what happens there.`,
		pl.scenes, sceneShape(p, pl.clip), pl.scenes, refDur/float64(pl.scenes), refDur/float64(pl.scenes))
	lang := pl.language
	langRule := "the language of the author's wish; when the wish is empty or shows no language — the reference's spoken language"
	if lang != "" {
		langRule = lang
	}
	line(`- Language of the narration, dialogue, title and on-screen text: %s.`, langRule)
	line(`- "narration": what is spoken in the scene, at most %d words (speakable in about %d seconds); "" when nobody speaks there. Keep the reference's way of speaking (person, energy, direct address).`, pl.words, pl.clip)
	line(`- "picture": what we see, specific and visual — who or what is in frame, their look, action, the setting, light, shot size and camera movement — in UKRAINIAN, 20–45 words.`)
	if ref.KeepStyle {
		for _, l := range profileLook(p) {
			line("%s", l)
		}
	}
	line(``)
	line(`Return ONLY a JSON object:`)
	line(`{`)
	line(`  "title": the new video's title,`)
	line(`  "language": ISO 639-1 code of the narration's language,`)
	line(`  "scenes": [ { "title": a short scene name, "picture": …, "onScreenText": on-screen text or "", "speaker": "narrator" | "on-camera" | a character's name, "narration": … } ],`)
	line(`  "notes": ENGLISH director's notes for the image and video planner, at most 1500 characters: the visual style, palette, lighting, camera language, shot sizes, pacing and recurring graphic devices to keep — no story content,`)
	line(`  "style": style id,`)
	line(`  "customStyle": "" — or, only when no listed style fits the reference's look, an ENGLISH image-style prompt of 1–2 sentences,`)
	line(`  "genre": genre id or "",`)
	line(`  "tempo": tempo id,`)
	line(`  "camera": camera id,`)
	line(`  "tone": tone id or "",`)
	line(`  "audioMode": "voiceover" (a narrator off screen) | "invideo" (people speak on camera) | "silent" (music only),`)
	line(`  "content": "characters" | "nopeople" | "motion" (motion graphics) | "mixed",`)
	line(`  "textOverlays": true when the reference relies on on-screen text,`)
	line(`  "aspect": "16:9" | "9:16"`)
	line(`}`)
	line(``)
	line(`Styles ("id" — name — suits):`)
	for _, s := range Styles {
		line(`  "%s" — %s — %s`, s.ID, s.Label, strings.TrimPrefix(s.Suits, "підходить для "))
	}
	choices := func(title string, list []Choice) {
		line(`%s ("id" — name):`, title)
		for _, c := range list {
			line(`  "%s" — %s`, c.ID, c.Label)
		}
	}
	choices("Genres", Genres)
	choices("Tempos", Tempos)
	choices("Cameras", Cameras)
	choices("Tones of the narration", Tones)
	system = strings.TrimRight(b.String(), "\n")

	var in strings.Builder
	fmt.Fprintf(&in, "Reference breakdown:\n\n%s\n\n", clipRunes(strings.TrimSpace(ref.Breakdown), scriptRunes))
	if ref.Duration > 0 {
		fmt.Fprintf(&in, "Reference length: %.0f s. ", ref.Duration)
	}
	fmt.Fprintf(&in, "New video: %d scenes × %d s ≈ %d s.\n\n", pl.scenes, pl.clip, pl.scenes*pl.clip)
	wish := strings.TrimSpace(ref.Wish)
	if wish == "" {
		wish = "(none — keep the reference's topic, but write everything anew)"
	}
	fmt.Fprintf(&in, "The author's wish (what to change, what to see):\n%s", wish)
	return system, in.String()
}

// remakeReply is the screenwriter's answer.
type remakeReply struct {
	Title, Language, Notes     string
	Style, CustomStyle, Genre  string
	Tempo, Camera, Tone        string
	AudioMode, Content, Aspect string
	TextOverlays, OverlaysSet  bool
	Scenes                     []remakeScene
}

type remakeScene struct{ Title, Picture, OnScreen, Speaker, Narration string }

// parseRemakeReply reads the screenwriter's JSON (fences, prose, slips and a
// cut-off end tolerated).
func parseRemakeReply(raw string) (remakeReply, bool) {
	var o *jobj
	for _, cand := range []string{raw, repairJSON(raw)} {
		b, err := llm.ExtractJSON(cand)
		if err != nil {
			continue
		}
		v, err := parseJSON(b)
		if err != nil {
			continue
		}
		if x, ok := v.(*jobj); ok {
			o = x
			break
		}
	}
	if o == nil {
		return remakeReply{}, false
	}
	if inner, ok := get(o, "screenplay").(*jobj); ok {
		o = inner
	}
	str := func(v any, k string) string { return strings.TrimSpace(jsString(get(v, k))) }
	r := remakeReply{
		Title: str(o, "title"), Language: str(o, "language"), Notes: str(o, "notes"),
		Style: str(o, "style"), CustomStyle: str(o, "customStyle"), Genre: str(o, "genre"),
		Tempo: str(o, "tempo"), Camera: str(o, "camera"), Tone: str(o, "tone"),
		AudioMode: strings.ToLower(str(o, "audioMode")), Content: strings.ToLower(str(o, "content")), Aspect: str(o, "aspect"),
	}
	r.TextOverlays, r.OverlaysSet = parseBool(get(o, "textOverlays"))
	for _, s := range asArray(get(o, "scenes")) {
		sc := remakeScene{Title: str(s, "title"), Picture: str(s, "picture"), OnScreen: str(s, "onScreenText"),
			Speaker: str(s, "speaker"), Narration: str(s, "narration")}
		if sc.Picture == "" {
			sc.Picture = str(s, "visual")
		}
		if sc.Picture != "" || sc.Narration != "" {
			r.Scenes = append(r.Scenes, sc)
		}
	}
	return r, len(r.Scenes) > 0
}

// remakeScript writes the screenplay and applies it to the project.
func (e *Engine) remakeScript(j *jobCtx, ref Reference) error {
	snap := j.snapshot()
	pl := e.remakePlanFor(&snap)
	j.progress(rmWatchTo+1, fmt.Sprintf("Пишу сценарій · %s", sceneCount(pl.scenes)))
	system, prompt := remakePrompt(&snap, pl)
	stop := creepProgress(j, rmWatchTo+1, rmScriptTo-2, time.Duration(30+pl.scenes*3)*time.Second, "Пишу сценарій…")
	var reply remakeReply
	var ok bool
	for attempt := 0; attempt < 2 && !ok; attempt++ {
		sys := system
		if attempt > 0 {
			sys += "\n\nIMPORTANT: your previous reply could not be read as JSON. Reply again with ONE valid JSON object only — no prose, no code fences."
		}
		raw, err := e.text.Text(j.ctx, llm.Request{System: sys, Prompt: prompt, Temperature: 0.7, Timeout: 300 * time.Second})
		if err != nil {
			stop()
			return err
		}
		reply, ok = parseRemakeReply(raw)
	}
	stop()
	if !ok {
		return errors.New("Модель не змогла написати сценарій за прикладом (відповідь не схожа на JSON) — спробуй ще раз")
	}
	if j.cancelled() {
		return nil
	}
	e.applyRemake(j.st, reply, pl)
	j.progress(rmScriptTo, "Сценарій готовий")
	return nil
}

// creepProgress moves the job's progress from → to over about est while a
// single long request runs; stop ends it.
func creepProgress(j *jobCtx, from, to float64, est time.Duration, msg string) (stop func()) {
	ctx, cancel := context.WithCancel(j.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		start := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				x := time.Since(start).Seconds() / est.Seconds()
				j.progress(from+(to-from)*(1-math.Exp(-2*x)), msg)
			}
		}
	}()
	return func() { cancel(); <-done }
}

// applyRemake puts the screenplay into the project, with the reference's look —
// unless a profile was picked (Reference.KeepStyle): then the profile's look,
// pacing, voice and modes stay and only its notes get the remake's lead.
func (e *Engine) applyRemake(st *proj, r remakeReply, pl remakePlan) {
	e.update(st, func(p *Project) {
		in := &p.Input
		ref := &p.Reference
		lang := in.Language
		if lang == "" {
			lang = matchLanguage(r.Language)
		}
		in.Mode = ModeStory
		in.Language = lang
		in.Text = clipRunes(remakeScreenplay(r), 200_000)
		in.TargetSeconds = len(r.Scenes) * pl.clip
		if ref.KeepStyle {
			// A profile was picked: its look, pacing, voice and modes win; the
			// analysis gave only the content (the screenplay above).
			in.Notes = clipRunes(remakeNotes(stripRemakeLead(in.Notes), ref.Fidelity), 2000)
			if t := clipRunes(strings.TrimSpace(r.Title), 120); t != "" {
				p.Title = t
			}
			ref.Stage, ref.ScriptedAt = remakeScripted, nowMs()
			return
		}
		if a, ok := parseAspect(r.Aspect); ok {
			in.Aspect = a
		} else if ref.Aspect == "9:16" {
			in.Aspect = "9:16"
		} else if ref.Aspect != "" {
			in.Aspect = "16:9"
		}
		if id, ok := matchID(r.Style, len(Styles), func(i int) (string, string) { return Styles[i].ID, Styles[i].Label }); ok {
			in.Style = id
		}
		in.CustomStyle = clipRunes(r.CustomStyle, 1000)
		if id, ok := matchChoice(Genres, r.Genre); ok {
			in.Genre = id
		} else if r.Genre == "" {
			in.Genre = ""
		}
		if id, ok := matchChoice(Tempos, r.Tempo); ok {
			in.Tempo = id
		}
		if id, ok := matchChoice(Cameras, r.Camera); ok {
			in.Camera = id
		}
		if id, ok := matchChoice(Tones, r.Tone); ok {
			in.Tone = id
		}
		audio := firstNonBlank(oneOfStr(r.AudioMode, AudioVoiceover, AudioInVideo, AudioSilent),
			oneOfStr(ref.AudioMode, AudioVoiceover, AudioInVideo, AudioSilent))
		if audio != "" {
			in.AudioMode = audio
			in.Voiceover = audio == AudioVoiceover
		}
		if c := firstNonBlank(oneOfStr(r.Content, ContentCharacters, ContentNoPeople, ContentMotion, ContentMixed),
			oneOfStr(ref.People, ContentCharacters, ContentNoPeople, ContentMotion, ContentMixed)); c != "" {
			in.Content = c
		}
		if r.OverlaysSet && r.TextOverlays {
			in.Overlays = "on"
		}
		in.Notes = clipRunes(remakeNotes(r.Notes, ref.Fidelity), 2000)
		if t := clipRunes(strings.TrimSpace(r.Title), 120); t != "" {
			p.Title = t
		}
		ref.Stage, ref.ScriptedAt = remakeScripted, nowMs()
	})
}

func oneOfStr(v string, allowed ...string) string {
	if oneOf(v, allowed...) {
		return v
	}
	return ""
}

// remakeNotes are the director's notes: the reference's look, and for an
// exact copy a reminder to follow the screenplay shot by shot.
func remakeNotes(notes, fidelity string) string {
	notes = strings.TrimSpace(notes)
	lead := remakeLead
	if fidelity == FidelityExact {
		lead = remakeLeadExact
	}
	if notes == "" {
		return lead
	}
	return lead + " " + notes
}

// remakeScreenplay is the story text of the project: the planner reads it
// scene by scene; the user reads and edits it in step 1.
func remakeScreenplay(r remakeReply) string {
	var b strings.Builder
	if t := strings.TrimSpace(r.Title); t != "" {
		b.WriteString(t + "\n\n")
	}
	for i, s := range r.Scenes {
		head := fmt.Sprintf("Сцена %d", i+1)
		if s.Title != "" {
			head += " · " + s.Title
		}
		b.WriteString(head + "\n")
		if s.Picture != "" {
			b.WriteString("Кадр: " + s.Picture + "\n")
		}
		if s.OnScreen != "" {
			b.WriteString("Текст на екрані: «" + s.OnScreen + "»\n")
		}
		if n := strings.TrimSpace(s.Narration); n != "" {
			who := "Голос за кадром"
			switch sp := strings.TrimSpace(s.Speaker); strings.ToLower(sp) {
			case "", "narrator", "voice-over", "voiceover", "оповідач", "диктор":
			case "on-camera", "on camera":
				who = "Говорить у кадрі"
			default:
				who = "Говорить " + sp
			}
			b.WriteString(who + ": «" + strings.Trim(n, "«»\"") + "»\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// sceneShape says what one scene of the new film is: a continuous clip of
// the video model (the reference's quick cuts merged), or — in a
// «Тільки картинки» film — one still picture the montage moves over (a
// rapid film keeps the reference's quick cuts: one cut, one scene).
func sceneShape(p *Project, clip int) string {
	switch {
	case p.Input.CodeAnim():
		return fmt.Sprintf(`Each scene is ONE animated beat of about %d seconds drawn by code (an animated map, chart or kinetic text timed to the narration), so keep the reference's beats: one beat of the reference = one scene.`, clip)
	case p.Input.StillsOnly() && sceneSeconds(p.Input) > 0:
		return fmt.Sprintf(`Each scene is ONE still picture on screen for about %d seconds (the montage moves the camera over it), so keep the reference's quick cutting: one beat or cut of the reference = one scene.`, clip)
	case p.Input.StillsOnly():
		return fmt.Sprintf(`Each scene is ONE still picture on screen for about %d seconds (the montage moves the camera over it), so merge the reference's quick cuts into one picture.`, clip)
	}
	return fmt.Sprintf(`Each scene is ONE continuous shot of about %d seconds made by a video model, so merge the reference's quick cuts into one continuous moment.`, clip)
}

// profileLook is the screenwriter's brief when the author picked a profile:
// the profile's look, pacing and voice are FIXED — the reference only gives
// the content, structure, beats, timing and the narration's meaning.
func profileLook(p *Project) []string {
	in := p.Input
	out := []string{
		``,
		`THE LOOK IS FIXED by the author's profile — it wins over the reference wherever they differ. Take from the reference ONLY the content: the story, the structure and order of the beats, their timing, the hook, the facts and numbers, the jokes and the meaning of the narration. Fidelity applies to that content, never to the look.`,
	}
	if s := StylePrompt(in.Style, in.CustomStyle); s != "" {
		out = append(out, `- Visual style of EVERY picture (describe each "picture" in this style even when the reference looks different; characters are drawn the same way): `+s)
	}
	if d := styleDirection(in); d != "" {
		out = append(out, `- `+d)
	}
	for _, h := range []string{genreHint(in.Genre), hintOf(Tempos, in.Tempo), hintOf(Cameras, in.Camera), toneHint(in.Tone)} {
		if h != "" {
			out = append(out, `- `+h)
		}
	}
	if n := strings.TrimSpace(stripRemakeLead(in.Notes)); n != "" {
		out = append(out, `- The author's notes for every film of this profile: `+n)
	}
	format := "horizontal 16:9"
	if in.Aspect == "9:16" {
		format = "vertical 9:16"
	}
	out = append(out, `- Frame format: `+format+`.`,
		`- "notes", "style", "customStyle", "genre", "tempo", "camera", "tone", "audioMode", "content", "textOverlays" and "aspect" are already decided by the profile: return them, they are ignored.`)
	return out
}

// Leads remakeNotes puts before the director's notes.
const (
	remakeLead      = "Remake of a reference video: follow the story text scene by scene — one plan scene per «Сцена»; its «Кадр» is the picture, its quoted line the narration."
	remakeLeadExact = "Exact remake of a reference video: follow the story text scene by scene and shot by shot — one plan scene per «Сцена», the picture exactly as its «Кадр» says, its quoted line as the narration."
)

// stripRemakeLead is the notes without the lead an earlier screenplay put
// before them (the profile's own notes, when a profile was picked).
func stripRemakeLead(notes string) string {
	notes = strings.TrimSpace(notes)
	for _, lead := range []string{remakeLead, remakeLeadExact} {
		if strings.HasPrefix(notes, lead) {
			return strings.TrimSpace(strings.TrimPrefix(notes, lead))
		}
	}
	return notes
}
