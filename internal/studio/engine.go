package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
	"ytcartell/internal/glabs"
	"ytcartell/internal/httpdiag"
	"ytcartell/internal/llm"
	"ytcartell/internal/media"
	"ytcartell/internal/tts"
)

var (
	// ErrBusy: another job is running on this project. Run returns a more
	// detailed error that matches errors.Is(err, ErrBusy).
	ErrBusy = errors.New("на проєкті вже виконується етап")
	// ErrNotFound: there is no project with this id.
	ErrNotFound = errors.New("Проєкт не знайдено")
	// ErrClosing: the app is closing (Shutdown ran), nothing new starts.
	ErrClosing = errors.New("Програма закривається")
)

const (
	projectFile = "project.json"
	// saveDelay debounces project.json writes (job ends write immediately).
	saveDelay = 400 * time.Millisecond
	// notifyGap coalesces Subscribe callbacks to at most ~8 per second per project.
	notifyGap = 125 * time.Millisecond
	// ffRetry: how often FF() retries locating a missing ffmpeg.
	ffRetry = 5 * time.Second
	// trashDir (inside a project) keeps files the project stopped referencing.
	trashDir = "trash"
)

// Engine owns the projects of the data folder, runs their jobs and talks to
// the generation services. All methods are safe for concurrent use.
type Engine struct {
	cfg    *config.Store
	gem    *gemini.Client
	text   *llm.Client
	gl     *glabs.Client
	speech *tts.Client
	// profiles: settings presets (profiles.json next to config.json; in memory
	// when cfg itself is an in-memory store).
	profiles *config.ProfileStore
	// rewriter rewrites a prompt refused for its content (rewrite.go); nil =
	// the text model. Tests set it.
	rewriter rewriteFunc
	// code draws «Анімація кодом» clips (codeanim.go; nil = the built-in
	// renderer, made on first use). SetCodeRenderer replaces it.
	code   CodeRenderer
	codeMu sync.Mutex

	// cfgMu serialises onConfig: a data folder switch (flush + load) must not
	// interleave with another settings change.
	cfgMu sync.Mutex
	// closing: Shutdown ran, no new job or regeneration starts.
	closing atomic.Bool
	// ffInst: the FFmpeg installation the user started (tools.go).
	ffInst toolJob

	mu       sync.Mutex // guards the fields below
	dataDir  string
	projects map[string]*proj
	detached []*proj // projects of a previous data folder that were still running a job
	ff       media.FF
	ffKey    string // configured ffmpeg|ffprobe paths ff was located with
	ffAt     time.Time
	ffBusy   bool // a background re-locate is running

	nmu        sync.Mutex // notifications
	subs       []subscriber
	nextSub    int
	pending    map[string]bool
	lastNotify map[string]time.Time
}

type subscriber struct {
	id int
	fn func(string)
}

// proj is the live state of one project.
type proj struct {
	id  string
	dir string

	fmu sync.RWMutex // asset file writes (read side) vs Delete (write side)
	wmu sync.Mutex   // serialises project.json writes
	mu  sync.Mutex   // guards everything below

	p     *Project
	dirty bool
	timer *time.Timer
	sig   string  // last projects-list signature
	job   *jobCtx // running job, nil when idle

	deleted atomic.Bool
	// ctx lives as long as the project; Delete and Shutdown cancel it
	// (in-flight work stops).
	ctx    context.Context
	cancel context.CancelFunc
	// bg counts the running job and regenerations (Shutdown waits for them).
	bg sync.WaitGroup
}

// NewEngine builds the service clients (gemini, llm, glabs, tts), locates
// FFmpeg and loads every project of cfg.DataDir. A missing FFmpeg is not an
// error (TestFFmpeg / FF report it). The returned engine is usable even when
// err != nil (the data folder could not be read — the user can pick another
// one in Settings).
func NewEngine(cfg *config.Store) (*Engine, error) {
	e := &Engine{
		cfg:        cfg,
		projects:   map[string]*proj{},
		pending:    map[string]bool{},
		lastNotify: map[string]time.Time{},
	}
	e.gem = gemini.New(cfg.Get)
	e.text = llm.New(cfg.Get, e.gem)
	e.wireText() // textcheck.go: a working address form found at run time is stored
	e.gl = glabs.New(cfg.Get)
	e.speech = tts.New(cfg.Get)
	ps, perr := config.ProfileStoreFor(cfg)
	if perr != nil { // the store is still usable (starter profiles)
		log.Printf("[studio] profiles: %v", perr)
	}
	e.profiles = ps
	// TTS key rotation state (exhausted/failed keys) lives next to config.json
	// for a real settings store; in-memory stores keep it in memory.
	if ps != nil && ps.Path() != "" {
		if err := e.speech.SetStateFile(tts.DefaultStateFile()); err != nil {
			log.Printf("[studio] tts key state: %v", err)
		}
	}

	c := cfg.Get()
	e.locateFF(c, true)
	e.dataDir = filepath.Clean(c.DataDir)
	states, err := e.loadAll(e.dataDir)
	e.projects = states
	cfg.Subscribe(e.onConfig)
	return e, err
}

// Config is the shared settings store.
func (e *Engine) Config() *config.Store { return e.cfg }

// FF returns the located ffmpeg/ffprobe without blocking (the UI calls it
// during layout). It is re-located whenever the configured paths change;
// while missing, one background retry runs at most every few seconds and
// subscribers are notified ("") when it finds something new.
func (e *Engine) FF() media.FF {
	e.mu.Lock()
	ff := e.ff
	retry := !ff.OK() && !e.ffBusy && time.Since(e.ffAt) > ffRetry
	if retry {
		e.ffBusy = true
	}
	e.mu.Unlock()
	if retry {
		go func() {
			found, _ := e.locateFF(e.cfg.Get(), false)
			e.mu.Lock()
			e.ffBusy = false
			e.mu.Unlock()
			if found != ff {
				e.notify("")
			}
		}()
	}
	return ff
}

// ffNow is FF for background work: a stale missing FFmpeg is re-located
// right away (a job must not fail only because the retry has not run yet).
func (e *Engine) ffNow() media.FF {
	e.mu.Lock()
	ff := e.ff
	stale := !ff.OK() && time.Since(e.ffAt) > ffRetry
	e.mu.Unlock()
	if stale {
		ff, _ = e.locateFF(e.cfg.Get(), false)
	}
	return ff
}

func ffKeyOf(c config.Config) string { return c.FFmpegPath + "|" + c.FFprobePath }

// locateFF finds ffmpeg/ffprobe for c and stores the result. Without force
// it is stored only while the configured paths are still the ones the
// current value was located with: a retry started before a settings change
// must not overwrite the new location.
func (e *Engine) locateFF(c config.Config, force bool) (media.FF, error) {
	ff, err := media.Locate(c)
	key := ffKeyOf(c)
	e.mu.Lock()
	if force || key == e.ffKey {
		e.ff, e.ffKey, e.ffAt = ff, key, time.Now()
	}
	e.mu.Unlock()
	return ff, err
}

// onConfig reacts to settings changes: re-locate FFmpeg, switch data folder.
// Deliveries are serialised and act on the latest settings, so two quick
// updates can neither load a folder twice nor end in a stale folder.
func (e *Engine) onConfig(config.Config) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	c := e.cfg.Get()
	e.mu.Lock()
	relocate := ffKeyOf(c) != e.ffKey
	dir := filepath.Clean(c.DataDir)
	moved := dir != e.dataDir
	e.mu.Unlock()
	if relocate {
		e.locateFF(c, true)
	}
	if !moved || e.closing.Load() {
		return
	}
	if err := e.Flush(); err != nil {
		log.Printf("[studio] flush before switching data folder: %v", err)
	}
	states, err := e.loadAll(dir)
	if err != nil {
		log.Printf("[studio] data folder %s: %v", dir, err)
	}
	e.mu.Lock()
	// Projects of the old folder that still run a job keep going in the
	// background and save into their own folder; they just leave the list
	// (Flush and Shutdown still cover them).
	for _, st := range e.projects {
		st.mu.Lock()
		if st.job != nil {
			e.detached = append(e.detached, st)
		}
		st.mu.Unlock()
	}
	e.projects, e.dataDir = states, dir
	e.mu.Unlock()
	e.notify("")
}

// Shutdown stops everything before the app exits: no new job or
// regeneration starts, running ones are cancelled at once (text calls,
// downloads and ffmpeg stop; a generation still in flight is abandoned), and
// it waits up to timeout for them to wind down and record their state. Then
// it flushes every pending save. Call it when the window closes, before
// os.Exit: Windows does not kill child processes together with their parent.
func (e *Engine) Shutdown(timeout time.Duration) error {
	e.closing.Store(true)
	e.CancelFFmpegInstall()
	e.mu.Lock()
	states := append([]*proj(nil), e.detached...)
	for _, st := range e.projects {
		states = append(states, st)
	}
	e.mu.Unlock()
	for _, st := range states {
		// Under st.mu: a Run/Regen that passed the closing check has already
		// registered in st.bg.
		st.mu.Lock()
		if jc := st.job; jc != nil {
			jc.flag.Store(true)
		}
		st.mu.Unlock()
		st.cancel()
	}
	done := make(chan struct{})
	go func() {
		for _, st := range states {
			st.bg.Wait()
		}
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		log.Printf("[studio] shutdown: background work did not stop within %s", timeout)
	}
	return e.Flush()
}

// ---- notifications ------------------------------------------------------------------

// Subscribe registers fn, called with a project id after that project changed
// ("" = the projects list changed). Calls come from any goroutine, never under
// an engine lock, and are coalesced (≤ ~8/s per project). cancel unregisters.
func (e *Engine) Subscribe(fn func(projectID string)) (cancel func()) {
	e.nmu.Lock()
	id := e.nextSub
	e.nextSub++
	e.subs = append(e.subs, subscriber{id, fn})
	e.nmu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.nmu.Lock()
			for i, s := range e.subs {
				if s.id == id {
					e.subs = append(e.subs[:i:i], e.subs[i+1:]...)
					break
				}
			}
			e.nmu.Unlock()
		})
	}
}

func (e *Engine) notify(id string) {
	e.nmu.Lock()
	if e.pending[id] {
		e.nmu.Unlock()
		return
	}
	e.pending[id] = true
	wait := notifyGap - time.Since(e.lastNotify[id])
	e.nmu.Unlock()
	fire := func() {
		e.nmu.Lock()
		delete(e.pending, id)
		e.lastNotify[id] = time.Now()
		subs := append([]subscriber(nil), e.subs...)
		e.nmu.Unlock()
		for _, s := range subs {
			s.fn(id)
		}
	}
	if wait <= 0 {
		go fire()
	} else {
		time.AfterFunc(wait, fire)
	}
}

func (e *Engine) emit(st *proj, listChanged bool) {
	e.notify(st.id)
	if listChanged {
		e.notify("")
	}
}

// ---- project state plumbing ----------------------------------------------------------

func newProj(id, dir string, p *Project) *proj {
	st := &proj{id: id, dir: dir, p: p}
	st.ctx, st.cancel = context.WithCancel(context.Background())
	st.sig = listSig(p)
	return st
}

func (e *Engine) state(id string) *proj {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.projects[id]
}

func (e *Engine) all() []*proj {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*proj, 0, len(e.projects))
	for _, st := range e.projects {
		out = append(out, st)
	}
	return out
}

// commitLocked marks the project changed (updatedAt, debounced save) and
// reports whether the projects list needs a refresh. Caller holds st.mu.
func (e *Engine) commitLocked(st *proj) bool {
	st.p.UpdatedAt = nowMs()
	st.dirty = true
	if st.timer == nil {
		st.timer = time.AfterFunc(saveDelay, func() {
			if err := e.writeNow(st); err != nil {
				log.Printf("[studio:%s] save: %v", st.id, err)
			}
		})
	}
	sig := listSig(st.p)
	changed := sig != st.sig
	st.sig = sig
	return changed
}

// update applies fn to the live project under its lock, schedules a save and
// notifies subscribers. False when the project was deleted.
func (e *Engine) update(st *proj, fn func(p *Project)) bool {
	st.mu.Lock()
	if st.deleted.Load() {
		st.mu.Unlock()
		return false
	}
	fn(st.p)
	list := e.commitLocked(st)
	st.mu.Unlock()
	e.emit(st, list)
	return true
}

// read runs fn on the live project under its lock (no change recorded).
func (e *Engine) read(st *proj, fn func(p *Project) bool) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted.Load() {
		return false
	}
	return fn(st.p)
}

// writeNow writes project.json if there are unsaved changes.
func (e *Engine) writeNow(st *proj) error {
	st.wmu.Lock()
	defer st.wmu.Unlock()
	st.mu.Lock()
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	if st.deleted.Load() || !st.dirty {
		st.mu.Unlock()
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	err := enc.Encode(st.p)
	st.dirty = false
	st.mu.Unlock()
	if err == nil {
		if err = os.MkdirAll(st.dir, 0o755); err == nil {
			err = writeFileAtomic(filepath.Join(st.dir, projectFile), buf.Bytes())
		}
	}
	if err != nil {
		// Keep it dirty and try again a bit later.
		st.mu.Lock()
		if !st.deleted.Load() {
			st.dirty = true
			if st.timer == nil {
				st.timer = time.AfterFunc(2*time.Second, func() { _ = e.writeNow(st) })
			}
		}
		st.mu.Unlock()
		return fmt.Errorf("не вдалося зберегти проєкт: %w", err)
	}
	return nil
}

// Flush writes every pending (debounced) project save to disk right away.
// Call it when the app exits so the latest edits and job states are not lost;
// it is safe to call at any time.
func (e *Engine) Flush() error {
	var errs []error
	if err := e.speech.FlushKeyState(); err != nil {
		errs = append(errs, err)
	}
	e.mu.Lock()
	states := append([]*proj(nil), e.detached...)
	e.mu.Unlock()
	for _, st := range append(states, e.all()...) {
		if err := e.writeNow(st); err != nil {
			errs = append(errs, err)
		}
	}
	if len(states) > 0 { // forget detached projects whose job has ended
		e.mu.Lock()
		keep := e.detached[:0]
		for _, st := range e.detached {
			st.mu.Lock()
			if st.job != nil || st.dirty {
				keep = append(keep, st)
			}
			st.mu.Unlock()
		}
		e.detached = keep
		e.mu.Unlock()
	}
	return errors.Join(errs...)
}

// ---- loading ---------------------------------------------------------------------------

func projectsRoot(dataDir string) string { return filepath.Join(dataDir, "projects") }

func (e *Engine) loadAll(dataDir string) (map[string]*proj, error) {
	states := map[string]*proj{}
	root := projectsRoot(dataDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return states, fmt.Errorf("не вдалося створити папку даних %s: %w", root, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return states, fmt.Errorf("не вдалося прочитати папку даних %s: %w", root, err)
	}
	for _, en := range entries {
		id := en.Name()
		if !en.IsDir() || !reProjectID.MatchString(id) {
			continue
		}
		dir := filepath.Join(root, id)
		p, err := loadProject(dir, id)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("[studio] skip %s: %v", dir, err)
			}
			continue
		}
		states[id] = newProj(id, dir, p)
	}
	return states, nil
}

func loadProject(dir, id string) (*Project, error) {
	b, err := os.ReadFile(filepath.Join(dir, projectFile))
	if err != nil {
		return nil, err
	}
	p, err := decodeProject(b)
	if err != nil {
		return nil, err
	}
	p.ID = id
	normalize(p)
	return p, nil
}

// decodeProject parses a saved project; fields missing in older files keep
// their defaults (TS {...DEFAULT_INPUT, ...p.input}). A wrongly typed field is
// skipped instead of failing the whole project.
func decodeProject(b []byte) (*Project, error) {
	p := &Project{Input: DefaultInput(), Montage: DefaultMontage()}
	if err := json.Unmarshal(b, p); err != nil {
		var te *json.UnmarshalTypeError
		if !errors.As(err, &te) {
			return nil, err
		}
	}
	return p, nil
}

// normalize fills fields missing in older projects and resets states that
// cannot survive a restart (port of store.ts normalize).
func normalize(p *Project) {
	if p.Characters == nil {
		p.Characters = []Character{}
	}
	if p.Locations == nil {
		p.Locations = []Location{}
	}
	if p.Scenes == nil {
		p.Scenes = []Scene{}
	}
	if p.Step < 1 {
		p.Step = 1
	}
	if p.Step > 5 {
		p.Step = 5
	}
	m := &p.Montage
	if m.Transition == "" {
		m.Transition = "fade"
	}
	if m.Subtitles == "" {
		m.Subtitles = "srt"
	}
	// Nothing survives a restart.
	if p.Job != nil && p.Job.Status == JobRunning {
		p.Job.Status = JobError
		p.Job.Error = "Програму перезапускали — запусти етап ще раз"
		p.Job.FinishedAt = nowMs()
	}
	reset := func(a *Asset) {
		if a.Status == StatusRunning || a.Status == StatusQueued {
			if a.Path != "" {
				a.Status, a.Error = StatusDone, ""
			} else {
				a.Status, a.Error = StatusError, "перервано перезапуском"
			}
		}
		if a.Status == "" || (a.Status == StatusDone && a.Path == "") {
			a.Status = StatusEmpty
		}
	}
	for i := range p.Characters {
		reset(&p.Characters[i].Image)
	}
	for i := range p.Locations {
		reset(&p.Locations[i].Image)
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if s.CharacterIDs == nil {
			s.CharacterIDs = []string{}
		}
		if s.Speaker == "" {
			s.Speaker = "narrator"
		}
		reset(&s.Frame)
		reset(&s.Board) // older projects have no storyboard sheet: "" → empty
		reset(&s.Video)
		reset(&s.Audio)
	}
	normalizeCode(p, reset) // code animation pictures and previews (codeanim.go)
	reset(&m.Output)
	reset(&m.Cover)
	reset(&m.CoverFinal) // cover with its text overlay (newer projects only)
	// Language, tone, storyboard mode and profile id are kept as saved (an id
	// this version does not know still reaches the model as free text).
	p.Input.Language = strings.TrimSpace(p.Input.Language)
	p.Input.Tone = strings.TrimSpace(p.Input.Tone)
	normalizeVoice(p)     // own voice-over: audio source, slice ranges (ownvoice.go)
	normalizeMaterials(p) // start materials (materials.go)
}

// ---- list / CRUD -------------------------------------------------------------------------

func thumbRel(p *Project) string {
	if f := p.Montage.CoverFinal; f.Path != "" && f.Status == StatusDone && !CoverFinalOutdated(*p) {
		return f.Path
	}
	if p.Montage.Cover.Path != "" {
		return p.Montage.Cover.Path
	}
	for _, s := range p.Scenes {
		if s.Frame.Path != "" {
			return s.Frame.Path
		}
	}
	for _, c := range p.Characters {
		if c.Image.Path != "" {
			return c.Image.Path
		}
	}
	return ""
}

func runningKind(p *Project) JobKind {
	if p.Job != nil && p.Job.Status == JobRunning {
		return p.Job.Kind
	}
	return ""
}

// listSig captures what the projects list shows (except the time).
func listSig(p *Project) string {
	return fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s\x00%s", p.Title, p.Step, len(p.Scenes), p.Input.Aspect, thumbRel(p), runningKind(p))
}

// List returns every project, most recently changed first.
func (e *Engine) List() []Summary {
	states := e.all()
	out := make([]Summary, 0, len(states))
	for _, st := range states {
		st.mu.Lock()
		p := st.p
		s := Summary{
			ID:        p.ID,
			Title:     p.Title,
			Step:      p.Step,
			UpdatedAt: p.UpdatedAt,
			Scenes:    len(p.Scenes),
			Aspect:    p.Input.Aspect,
			Running:   runningKind(p),
		}
		if rel := thumbRel(p); rel != "" {
			s.ThumbPath = filepath.Join(st.dir, filepath.FromSlash(rel))
		}
		st.mu.Unlock()
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Create makes a new empty project (step 1) and saves it right away.
func (e *Engine) Create(title string) (Project, error) {
	e.mu.Lock()
	root := projectsRoot(e.dataDir)
	var id string
	for {
		id = newID(10)
		if _, taken := e.projects[id]; taken {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, id)); err == nil {
			continue
		}
		break
	}
	e.mu.Unlock()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Project{}, fmt.Errorf("не вдалося створити папку проєкту: %w", err)
	}
	now := nowMs()
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Новий проєкт"
	}
	p := &Project{
		ID:         id,
		Title:      clipRunes(title, 120),
		CreatedAt:  now,
		UpdatedAt:  now,
		Step:       1,
		Input:      DefaultInput(),
		Characters: []Character{},
		Locations:  []Location{},
		Scenes:     []Scene{},
		Montage:    DefaultMontage(),
	}
	st := newProj(id, dir, p)
	st.dirty = true
	if err := e.writeNow(st); err != nil {
		_ = os.RemoveAll(dir)
		return Project{}, err
	}
	e.mu.Lock()
	e.projects[id] = st
	e.mu.Unlock()
	e.notify("")
	e.notify(id)
	return cloneProject(p), nil
}

// Get returns a deep-copy snapshot of a project (safe to read without locks).
func (e *Engine) Get(id string) (Project, bool) {
	st := e.state(id)
	if st == nil {
		return Project{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted.Load() {
		return Project{}, false
	}
	return cloneProject(st.p), true
}

// Delete removes a project and its whole folder (a running job is stopped).
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	st := e.projects[id]
	delete(e.projects, id)
	e.mu.Unlock()
	if st == nil {
		return ErrNotFound
	}
	st.cancel() // stops in-flight network work and ffmpeg
	st.fmu.Lock()
	st.wmu.Lock()
	st.mu.Lock()
	st.deleted.Store(true)
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	if st.job != nil {
		st.job.flag.Store(true)
		st.job.stop()
	}
	st.mu.Unlock()
	_ = os.Remove(filepath.Join(st.dir, projectFile)) // first, so a half-removed folder never reloads
	err := removeAllRetry(st.dir)
	st.wmu.Unlock()
	st.fmu.Unlock()
	e.notify("")
	e.notify(id)
	if err != nil {
		return fmt.Errorf("Проєкт видалено, але частину файлів не вдалося прибрати (відкриті в іншій програмі?): %s", st.dir)
	}
	return nil
}

// Dir is the absolute project folder.
func (e *Engine) Dir(id string) string {
	if st := e.state(id); st != nil {
		return st.dir
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return filepath.Join(projectsRoot(e.dataDir), id)
}

// Abs is the absolute path of a project-relative asset path ("" for "").
func (e *Engine) Abs(id, rel string) string {
	if rel == "" {
		return ""
	}
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(e.Dir(id), filepath.FromSlash(rel))
}

// discardFiles moves generated files the project no longer references (a
// replaced or re-planned reference, frame, clip or voice) to
// <project>/trash/, keeping their sub-folder. Like the web studio, nothing
// paid for is deleted: trash goes away only with the project. Best effort: a
// file held open by a player simply stays where it is.
func (e *Engine) discardFiles(st *proj, rels []string) {
	st.fmu.RLock() // never resurrect a deleted project folder
	defer st.fmu.RUnlock()
	if st.deleted.Load() {
		return
	}
	trash := filepath.Join(st.dir, trashDir)
	for _, rel := range rels {
		if rel == "" || filepath.IsAbs(rel) {
			continue
		}
		abs := filepath.Join(st.dir, filepath.FromSlash(rel))
		if !within(st.dir, abs) || within(trash, abs) || !isFile(abs) {
			continue
		}
		dst := freePath(filepath.Join(trash, filepath.FromSlash(rel)))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
			if os.Rename(abs, dst) != nil {
				// Held open for a moment (a player, antivirus, the indexer):
				// move it a little later without blocking the caller.
				go e.retryDiscard(st, abs, dst)
			}
		}
	}
}

// retryDiscard retries moving one file into the trash for a few seconds.
func (e *Engine) retryDiscard(st *proj, abs, dst string) {
	for i := 1; i <= 8; i++ {
		time.Sleep(time.Duration(i) * 150 * time.Millisecond)
		st.fmu.RLock()
		gone := st.deleted.Load() || !isFile(abs)
		var err error
		if !gone {
			err = os.Rename(abs, dst)
		}
		st.fmu.RUnlock()
		if gone || err == nil {
			return
		}
	}
	log.Printf("[studio:%s] could not move %s to the trash", st.id, filepath.Base(abs))
}

// freePath is path, or path with "-2", "-3"… before the extension when taken.
func freePath(path string) string {
	taken := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	if !taken(path) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		if p := fmt.Sprintf("%s-%d%s", stem, i, ext); !taken(p) {
			return p
		}
	}
}

// removeFiles deletes copies of the user's own files (imported music or
// pictures) that are no longer referenced; generated files go through
// discardFiles. Best effort: a file held open by a player stays behind.
func (e *Engine) removeFiles(st *proj, rels []string) {
	for _, rel := range rels {
		if rel == "" || filepath.IsAbs(rel) {
			continue
		}
		abs := filepath.Join(st.dir, filepath.FromSlash(rel))
		if within(st.dir, abs) {
			_ = os.Remove(abs)
		}
	}
}

// writeAsset stores generated/imported bytes as <project>/<sub>/<name> and
// returns the relative path. Never resurrects a deleted project folder.
func (e *Engine) writeAsset(st *proj, sub, name string, data []byte) (string, error) {
	st.fmu.RLock()
	defer st.fmu.RUnlock()
	if st.deleted.Load() {
		return "", ErrNotFound
	}
	dir := filepath.Join(st.dir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("не вдалося створити папку %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("не вдалося записати файл: %w", err)
	}
	return sub + "/" + name, nil
}

// ---- service checks (Settings screen) --------------------------------------------------

// ClipSeconds is the clip length of one scene (Settings → video length, 4/6/8).
func (e *Engine) ClipSeconds() int {
	// The length the configured model really renders (custom providers snap
	// to their own lengths, G-Labs/Veo to 4/6/8/10).
	return videoSeconds(e.cfg.Get())
}

// TestText checks the text provider (textcheck.go): the address forms, the
// key, then a tiny prompt; a form that works is stored.
func (e *Engine) TestText(ctx context.Context) (bool, string) { return e.testText(ctx) }

// TestGlabs checks that G-Labs Studio answers and accepts the key.
func (e *Engine) TestGlabs(ctx context.Context) (bool, string) {
	ctx, tr := httpdiag.WithTrace(ctx)
	ok, msg := e.gl.Health(ctx)
	if !ok {
		msg = tr.Append(msg, e.cfg.Get().Glabs.APIKey) // the exact request under the message
	}
	return ok, msg
}

// TestGoogle checks that at least one Google key works.
func (e *Engine) TestGoogle(ctx context.Context) (bool, string) {
	if len(e.cfg.Get().Google.Keys) == 0 {
		return false, "Ключів Google не задано"
	}
	ctx, tr := httpdiag.WithTrace(ctx)
	if err := e.gem.Test(ctx); err != nil {
		return false, tr.Append(clipRunes(errText(err), 300), e.cfg.Get().Google.Keys...)
	}
	return true, "Ключ Google працює"
}

// TestFFmpeg re-locates FFmpeg/ffprobe and reports the version.
func (e *Engine) TestFFmpeg(ctx context.Context) (bool, string) {
	ff, err := e.locateFF(e.cfg.Get(), true)
	if ff.FFmpeg == "" {
		if err == nil {
			err = media.ErrNotFound
		}
		return false, err.Error()
	}
	v, verr := ff.Version(ctx)
	if verr != nil {
		return false, verr.Error()
	}
	if ff.FFprobe == "" {
		return false, media.ErrNoProbe.Error()
	}
	return true, v
}

// TextModels lists model ids of the OpenAI-compatible text endpoint.
func (e *Engine) TextModels(ctx context.Context) ([]string, error) { return e.text.Models(ctx) }

// Voices lists the TTS account voices (cached 10 min unless force).
func (e *Engine) Voices(ctx context.Context, force bool) (tts.VoiceList, error) {
	return e.speech.Voices(ctx, force)
}

// ---- voice-over options ----------------------------------------------------------------

// toneVoice nudges the voice settings per narration tone (0 = default).
var toneVoice = map[string]struct{ stability, speed float64 }{
	"storyteller":     {0.5, 0.97},
	"documentary":     {0.6, 1},
	"trailer":         {0.35, 0.95},
	"youtuber":        {0.35, 1.05},
	"calm":            {0.7, 0.93},
	"humor":           {0.4, 1.03},
	"eerie":           {0.45, 0.93},
	"kids":            {0.55, 0.95},
	"motivational":    {0.4, 1.03},
	"witty-explainer": {0.35, 1.1}, // a fast witty explainer: ~190 words a minute
}

// ttsOptions are the synthesis options of a project's voice-over: the
// audience language (tts picks the model that speaks it and enforces it where
// the model allows) and tone-based voice settings. With the language on
// "auto" a writing system only one catalogue language uses (Georgian,
// Armenian, Hebrew, Thai…) still selects a model that speaks it. Pure: safe
// under the project lock.
func (e *Engine) ttsOptions(p *Project) tts.SynthOptions {
	opt := tts.SynthOptions{LanguageCode: strings.TrimSpace(p.Input.Language)}
	if opt.LanguageCode == "" {
		var sample strings.Builder
		for _, s := range p.Scenes {
			if sample.Len() > 4000 {
				break
			}
			sample.WriteString(s.Narration)
			sample.WriteByte(' ')
		}
		if sample.Len() == 0 {
			sample.WriteString(clipRunes(p.Input.Text, 4000))
		}
		opt.LanguageCode = scriptLanguage(sample.String())
	}
	if v, ok := toneVoice[p.Input.Tone]; ok {
		opt.Stability, opt.Speed = v.stability, v.speed
	}
	return opt
}

// voiceOptions is ttsOptions of a live project, for a voice generation
// running outside the project lock (zero options when the project is gone).
func (e *Engine) voiceOptions(st *proj) tts.SynthOptions {
	var opt tts.SynthOptions
	e.read(st, func(p *Project) bool {
		opt = e.ttsOptions(p)
		return true
	})
	return opt
}

// uniqueScripts are writing systems used by exactly one catalogue language
// that eleven_multilingual_v2 cannot speak.
var uniqueScripts = []struct {
	table *unicode.RangeTable
	code  string
}{
	{unicode.Georgian, "ka"}, {unicode.Armenian, "hy"}, {unicode.Hebrew, "he"}, {unicode.Thai, "th"},
	{unicode.Lao, "lo"}, {unicode.Myanmar, "my"}, {unicode.Gujarati, "gu"}, {unicode.Gurmukhi, "pa"},
	{unicode.Telugu, "te"}, {unicode.Kannada, "kn"}, {unicode.Malayalam, "ml"}, {unicode.Oriya, "or"},
	{unicode.Ethiopic, "am"},
}

// scriptLanguage guesses the language of text from its writing system when
// that is unambiguous (most letters in one of uniqueScripts), else "".
func scriptLanguage(text string) string {
	counts := make([]int, len(uniqueScripts))
	letters := 0
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Mc, r) {
			continue
		}
		letters++
		for i, s := range uniqueScripts {
			if unicode.Is(s.table, r) {
				counts[i]++
				break
			}
		}
	}
	best := -1
	for i, n := range counts {
		if n*2 > letters && (best < 0 || n > counts[best]) {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	return uniqueScripts[best].code
}

// ---- TTS keys (Settings → Озвучка) -------------------------------------------------------

// TestTTS checks every voice-over key with a cheap authenticated call
// (account, balance or voice list — never a paid synthesis) and updates the
// key states shown by TTSKeyStatus: an invalid key → "error", a working one
// clears its error, a quota the provider reports as spent → "exhausted".
func (e *Engine) TestTTS(ctx context.Context) (bool, string) {
	ok, msg := e.speech.Test(ctx)
	return ok, clipRunes(msg, 300)
}

// TTSKeyStatus is the state of every TTS key (masked) for the Settings screen.
func (e *Engine) TTSKeyStatus() []tts.KeyStatus { return e.speech.KeyStatus() }

// ResetTTSKeyErrors makes keys that failed usable again (exhausted keys keep
// waiting for the 1st of the month).
func (e *Engine) ResetTTSKeyErrors() { e.speech.ResetKeyErrors() }
