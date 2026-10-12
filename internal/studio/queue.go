package studio

// The global generations queue ("Черга"): what is generating across every
// project of the data folder, and the recent results — for the queue drawer
// and page and the badge on the left rail.
//
//	running — project jobs that run, plus single assets generated outside a
//	          job (Regen: a reference, a scene frame, clip or voice)
//	recent  — the last finished job of each project (Project.Job is saved)
//	          plus the latest result of single-asset regenerations (kept in
//	          memory, newest first, at most 30)
//
// A single asset leaves no record of its own once it is finished, so a
// tracker follows them: it subscribes to the engine and on every change
// notices assets that start or stop generating outside a job. An asset that
// generates while a job which makes assets of its kind runs counts as the
// job's (Regen's short "queued" state still marks it as a single one).
//
// Refreshing: Engine.Subscribe fires for every job and asset change (job
// start, progress, cancel and end; asset queued, running, progress, done,
// error), coalesced to ≤ ~8 calls/s per project. The UI marks the queue
// dirty in its Subscribe callback (any id, "" included) and calls Queue() on
// its next frame; Queue() only reads memory, so calling it every frame that
// needs it is fine.

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// queueRecentMax bounds the in-memory results of single-asset regenerations.
const queueRecentMax = 30

// QueueItem is one row of the queue.
type QueueItem struct {
	// Key identifies the row across snapshots (one job run or one
	// regeneration): use it for widget state and list animations.
	Key          string
	ProjectID    string
	ProjectTitle string
	// Kind is the project job; "" for a single asset (then Regen is set).
	Kind JobKind
	// Regen is what a single-asset regeneration makes; "" for a job.
	Regen RegenKind
	// ItemID is the scene, character or location id of a single asset.
	ItemID string
	// Label names the work in Ukrainian: "Кадри сцен", "Відео сцени 3",
	// "Озвучка сцени 5", "Персонаж «Тарас»", "Автопілот"…
	Label string
	// Step is the project step to open for this row (1–5).
	Step int
	// Status: running | done | error | cancelled.
	Status JobStatus
	// Progress 0–100. Single images and voices report 0 until they are done
	// (show an indeterminate bar); clips and jobs report real progress.
	Progress int
	Message  string
	Error    string
	// StartedAt and FinishedAt are unix ms (FinishedAt 0 while running). For
	// a single asset StartedAt is when the queue first saw it generating.
	StartedAt  int64
	FinishedAt int64
	// Thumb is the absolute path of a representative image, "" if none.
	Thumb string
	// Failures: items a finished job left failed (Job.Failures).
	Failures int
}

// Retryable reports whether «Повторити» applies to the row: a failed or
// stopped job that Retry can start again, a failed single asset. See
// RetryItem.
func (it QueueItem) Retryable() bool {
	if it.Regen != "" {
		return it.Status == JobError
	}
	return (it.Status == JobError || it.Status == JobCancelled || it.PartlyFailed()) && restartable(it.Kind)
}

// PartlyFailed reports a job that finished "done" but left Failures items
// failed.
func (it QueueItem) PartlyFailed() bool {
	return it.Regen == "" && it.Status == JobDone && it.Failures > 0
}

// QueueCounts feed the rail badge.
type QueueCounts struct {
	Running int // len(Running)
	Errors  int // Recent rows that failed and were not fixed or retried since
}

// QueueSnapshot is the whole queue at one moment.
type QueueSnapshot struct {
	Running []QueueItem // oldest first (by StartedAt)
	Recent  []QueueItem // newest first (by FinishedAt)
	Counts  QueueCounts
}

// Queue returns what is generating across all projects and the recent
// results. Cheap: memory only (no disk or ffprobe), each project locked
// briefly — call it whenever a Subscribe notification arrives.
func (e *Engine) Queue() QueueSnapshot {
	q := e.queueTracker()
	q.mu.Lock()
	scans := e.scanAll(q, nowMs(), true)
	var snap QueueSnapshot
	titles := make(map[string]string, len(scans))
	for _, ps := range scans {
		titles[ps.id] = ps.title
		switch {
		case ps.job == nil:
		case ps.job.Status == JobRunning:
			snap.Running = append(snap.Running, *ps.job)
		default:
			snap.Recent = append(snap.Recent, *ps.job)
		}
	}
	for _, live := range q.live {
		for _, la := range live {
			snap.Running = append(snap.Running, la.item)
		}
	}
	for _, it := range q.ring {
		if title, ok := titles[it.ProjectID]; ok {
			it.ProjectTitle = title // renamed since
			snap.Recent = append(snap.Recent, it)
		}
	}
	q.mu.Unlock()

	sort.Slice(snap.Running, func(i, j int) bool {
		a, b := &snap.Running[i], &snap.Running[j]
		if a.StartedAt != b.StartedAt {
			return a.StartedAt < b.StartedAt
		}
		return a.Key < b.Key
	})
	sort.Slice(snap.Recent, func(i, j int) bool {
		a, b := &snap.Recent[i], &snap.Recent[j]
		if a.FinishedAt != b.FinishedAt {
			return a.FinishedAt > b.FinishedAt
		}
		return a.Key < b.Key
	})
	snap.Counts.Running = len(snap.Running)
	for _, it := range snap.Recent {
		if it.Status == JobError {
			snap.Counts.Errors++
		}
	}
	return snap
}

// CancelAll asks every running job to stop, like Cancel on each project
// (also a job of a previous data folder that still runs in the background).
// Single-asset regenerations cannot be stopped — they finish. Returns how
// many jobs were asked to stop.
func (e *Engine) CancelAll() int {
	e.mu.Lock()
	states := append([]*proj(nil), e.detached...)
	for _, st := range e.projects {
		states = append(states, st)
	}
	e.mu.Unlock()
	n := 0
	for _, st := range states {
		if e.cancelJob(st) {
			n++
		}
	}
	return n
}

// jobStages maps a job back to the stage that runs it.
var jobStages = map[JobKind]Stage{
	JobPlan:      StagePlan,
	JobRefs:      StageRefs,
	JobFrames:    StageFrames,
	JobVideos:    StageVideos,
	JobVoices:    StageVoices,
	JobRender:    StageRender,
	JobMontage:   StageMontage,
	JobCover:     StageCover,
	JobAutopilot: StageAutopilot,
}

// restartable: Retry knows how to start a job of kind k again.
func restartable(k JobKind) bool {
	_, stage := jobStages[k]
	_, registered := jobRestarts[k]
	return stage || registered
}

// Retry re-runs the project's last job when it failed or was stopped: the
// same stage with force=false, so finished items are skipped (the autopilot
// continues where it stopped); a cover or autopilot job reuses the saved
// cover prompt. Errors like Run: one matching ErrBusy while a job runs,
// ErrNotFound, validation errors.
func (e *Engine) Retry(projectID string) error {
	st := e.state(projectID)
	if st == nil {
		return ErrNotFound
	}
	st.mu.Lock()
	if st.deleted.Load() {
		st.mu.Unlock()
		return ErrNotFound
	}
	j := st.p.Job
	if st.job != nil || (j != nil && j.Status == JobRunning) {
		var k JobKind
		if j != nil {
			k = j.Kind
		}
		st.mu.Unlock()
		return &busyError{k}
	}
	var err error
	switch {
	case j == nil:
		err = errors.New("На цьому проєкті ще нічого не запускали — повторювати нічого")
	case j.Status != JobError && j.Status != JobCancelled && !(j.Status == JobDone && j.Failures > 0):
		err = errors.New("Останній етап завершився успішно — повторювати нічого")
	}
	if err != nil {
		st.mu.Unlock()
		return err
	}
	kind, prompt := j.Kind, ""
	if kind == JobCover || kind == JobAutopilot {
		prompt = st.p.Montage.CoverPrompt
	}
	st.mu.Unlock()
	if restart, ok := jobRestarts[kind]; ok {
		return restart(e, projectID)
	}
	stage, ok := jobStages[kind]
	if !ok {
		return fmt.Errorf("Етап «%s» не можна повторити звідси — запусти його ще раз у проєкті", queueJobLabel(kind, false))
	}
	return e.Run(projectID, stage, false, prompt)
}

// jobRestarts start again the job kinds that are not started by Run with a
// stage of their own. A file adding such a kind registers it in an init
// func, like jobLabels: jobRestarts[JobTranscribe] = (*Engine).Transcribe.
var jobRestarts = map[JobKind]func(e *Engine, projectID string) error{}

// RetryItem is «Повторити» on a queue row: a job → Retry, a single asset →
// Regen (with the default prompt: a custom reference prompt is not kept).
func (e *Engine) RetryItem(it QueueItem) error {
	if it.Regen != "" {
		return e.Regen(it.ProjectID, it.Regen, it.ItemID, "")
	}
	return e.Retry(it.ProjectID)
}

// ---- labels ------------------------------------------------------------------------------

// queueJobLabel names a job in the queue and in notifications.
func queueJobLabel(k JobKind, grid bool) string {
	switch k {
	case JobFrames:
		if grid {
			return "Розкадровка 3×3"
		}
		return "Кадри сцен"
	case JobVideos:
		return "Відео сцен"
	case JobVoices:
		return "Озвучка сцен"
	}
	if l := jobLabels[k]; l != "" {
		return l
	}
	return string(k)
}

// assetLabel names a single asset: n is the scene number, name the
// character/location name.
func assetLabel(kind RegenKind, n int, name string, grid bool) string {
	named := func(what string) string {
		if name = strings.TrimSpace(name); name == "" {
			return what
		}
		return what + " «" + clipRunes(name, 60) + "»"
	}
	switch kind {
	case RegenCharacter:
		return named("Персонаж")
	case RegenLocation:
		return named("Локація")
	case RegenFrame:
		if grid {
			return fmt.Sprintf("Розкадровка сцени %d", n)
		}
		return fmt.Sprintf("Кадр сцени %d", n)
	case RegenVideo:
		return fmt.Sprintf("Відео сцени %d", n)
	case RegenVoice:
		return fmt.Sprintf("Озвучка сцени %d", n)
	}
	return string(kind)
}

// jobStep is the project step a job belongs to (cur for the autopilot).
func jobStep(k JobKind, cur int) int {
	switch k {
	case JobPlan:
		return 2
	case JobRefs, JobFrames:
		return 3
	case JobVideos, JobVoices, JobRender:
		return 4
	case JobMontage, JobCover:
		return 5
	}
	return max(1, min(5, cur))
}

func regenStep(k RegenKind) int {
	if k == RegenVideo || k == RegenVoice {
		return 4
	}
	return 3
}

// jobAssets lists the single-asset kinds each job kind generates itself:
// while such a job runs, a running asset of those kinds is the job's, not a
// row of its own. A file adding a job kind registers it in an init func,
// like jobLabels (nil = it makes none); a kind missing here is assumed to
// make every kind.
var jobAssets = map[JobKind][]RegenKind{
	JobPlan:      nil,
	JobRefs:      {RegenCharacter, RegenLocation},
	JobFrames:    {RegenCharacter, RegenLocation, RegenFrame}, // missing references first
	JobVideos:    {RegenVideo},
	JobVoices:    {RegenVoice},
	JobRender:    {RegenVideo, RegenVoice},
	JobMontage:   nil,
	JobCover:     nil,
	JobAutopilot: {RegenCharacter, RegenLocation, RegenFrame, RegenVideo, RegenVoice},
}

// jobCovers reports whether a running job of kind k ("" = none) generates
// assets of kind r itself.
func jobCovers(k JobKind, r RegenKind) bool {
	if k == "" {
		return false
	}
	kinds, known := jobAssets[k]
	return !known || slices.Contains(kinds, r)
}

// regenSlots locate the asset a regeneration kind targets.
var regenSlots = map[RegenKind]slot{
	RegenCharacter: slotCharacter,
	RegenLocation:  slotLocation,
	RegenFrame:     slotFrame,
	RegenVideo:     slotVideo,
	RegenVoice:     slotAudio,
}

func regenAsset(p *Project, kind RegenKind, id string) *Asset {
	s, ok := regenSlots[kind]
	if !ok {
		return nil
	}
	return assetRef{s, id}.in(p)
}

func absIn(dir, rel string) string {
	switch {
	case rel == "":
		return ""
	case filepath.IsAbs(rel):
		return rel
	}
	return filepath.Join(dir, filepath.FromSlash(rel))
}

// jobItem is the row of a project's running or last finished job.
func jobItem(st *proj, p *Project) QueueItem {
	j := p.Job
	return QueueItem{
		Key:          fmt.Sprintf("job:%s:%d", st.id, j.StartedAt),
		ProjectID:    st.id,
		ProjectTitle: p.Title,
		Kind:         j.Kind,
		Label:        queueJobLabel(j.Kind, p.Input.StoryboardMode == StoryboardGrid),
		Step:         jobStep(j.Kind, p.Step),
		Status:       j.Status,
		Progress:     j.Progress,
		Message:      j.Message,
		Error:        j.Error,
		StartedAt:    j.StartedAt,
		FinishedAt:   j.FinishedAt,
		Thumb:        absIn(st.dir, thumbRel(p)),
		Failures:     j.Failures,
	}
}

// jobFailures counts the items of the kinds job k generates that are left
// failed (0 for jobs that make none).
func jobFailures(p *Project, k JobKind) int {
	kinds, known := jobAssets[k]
	if !known {
		kinds = []RegenKind{RegenCharacter, RegenLocation, RegenFrame, RegenVideo, RegenVoice}
	}
	n := 0
	for _, r := range kinds {
		switch r {
		case RegenCharacter:
			for _, c := range p.Characters {
				n += errCount(c.Image)
			}
		case RegenLocation:
			for _, l := range p.Locations {
				n += errCount(l.Image)
			}
		case RegenFrame:
			for _, s := range p.Scenes {
				n += errCount(s.Frame)
			}
		case RegenVideo:
			for _, s := range p.Scenes {
				n += errCount(s.Video)
			}
		case RegenVoice:
			for _, s := range p.Scenes {
				n += errCount(s.Audio)
			}
		}
	}
	return n
}

func errCount(a Asset) int {
	if a.Status == StatusError {
		return 1
	}
	return 0
}

// ---- tracker -------------------------------------------------------------------------------

type assetKey struct {
	kind RegenKind
	id   string
}

// liveAsset is a single asset seen generating outside a job.
type liveAsset struct {
	started   int64  // when the tracker first saw it (unix ms)
	updatedAt int64  // Asset.UpdatedAt then: a new file changes it
	seen      uint64 // scan that last saw it
	item      QueueItem
}

type queueTracker struct {
	once sync.Once
	mu   sync.Mutex // guards the fields below; taken BEFORE a project's lock
	gen  uint64
	live map[string]map[assetKey]*liveAsset // project id → single assets generating
	ring []QueueItem                        // finished single assets, newest first
}

// queueTrackers holds the tracker of each engine (the Engine struct itself
// lives in engine.go).
var queueTrackers sync.Map // *Engine → *queueTracker

// queueTracker returns the engine's tracker, subscribing it on first use.
func (e *Engine) queueTracker() *queueTracker {
	v, ok := queueTrackers.Load(e)
	if !ok {
		v, _ = queueTrackers.LoadOrStore(e, &queueTracker{live: map[string]map[assetKey]*liveAsset{}})
	}
	q := v.(*queueTracker)
	q.once.Do(func() {
		e.Subscribe(func(id string) { e.observeQueue(q, id) })
	})
	return q
}

// observeQueue follows one change notification.
func (e *Engine) observeQueue(q *queueTracker, id string) {
	now := nowMs()
	q.mu.Lock()
	defer q.mu.Unlock()
	if id == "" { // the list changed: projects created, deleted, another folder
		e.scanAll(q, now, false)
		return
	}
	if st := e.state(id); st != nil {
		e.scanProject(q, st, now, false)
	} else {
		delete(q.live, id)
	}
}

// projScan is what one project shows in the queue itself.
type projScan struct {
	id, title string
	job       *QueueItem // running or last finished job, nil if none
}

// scanAll refreshes the tracker from every project of the list and forgets
// projects that left it (q.mu held); rows builds the projects' job rows.
func (e *Engine) scanAll(q *queueTracker, now int64, rows bool) []projScan {
	states := e.all()
	out := make([]projScan, 0, len(states))
	ids := make(map[string]bool, len(states))
	for _, st := range states {
		if ps, ok := e.scanProject(q, st, now, rows); ok {
			out = append(out, ps)
			ids[ps.id] = true
		}
	}
	for pid := range q.live {
		if !ids[pid] {
			delete(q.live, pid)
		}
	}
	q.ring = slices.DeleteFunc(q.ring, func(it QueueItem) bool { return !ids[it.ProjectID] })
	return out
}

// assetScan walks the assets of one locked project.
type assetScan struct {
	q    *queueTracker
	st   *proj
	p    *Project
	live map[assetKey]*liveAsset
	job  JobKind // running job, "" when idle
	grid bool
	gen  uint64
	now  int64
}

// scanProject refreshes the tracker from one project (q.mu held, st.mu taken
// briefly): single assets that started or stopped generating, results that
// are resolved; with rows it also returns the project's job row. ok is false
// for a deleted project.
func (e *Engine) scanProject(q *queueTracker, st *proj, now int64, rows bool) (ps projScan, ok bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted.Load() {
		delete(q.live, st.id)
		return projScan{}, false
	}
	p := st.p
	q.gen++
	s := assetScan{q: q, st: st, p: p, live: q.live[st.id], job: runningKind(p),
		grid: p.Input.StoryboardMode == StoryboardGrid, gen: q.gen, now: now}
	for i := range p.Characters {
		c := &p.Characters[i]
		s.visit(RegenCharacter, c.ID, &c.Image, 0, c.Name, c.Image.Path)
	}
	for i := range p.Locations {
		l := &p.Locations[i]
		s.visit(RegenLocation, l.ID, &l.Image, 0, l.Name, l.Image.Path)
	}
	for i := range p.Scenes {
		sc := &p.Scenes[i]
		s.visit(RegenFrame, sc.ID, &sc.Frame, i+1, "", sc.Frame.Path)
		s.visit(RegenVideo, sc.ID, &sc.Video, i+1, "", sc.Frame.Path)
		s.visit(RegenVoice, sc.ID, &sc.Audio, i+1, "", sc.Frame.Path)
	}
	for key, la := range s.live {
		if la.seen != s.gen { // the item is gone (deleted, re-planned)
			delete(s.live, key)
		}
	}
	if s.live != nil && len(s.live) == 0 {
		delete(q.live, st.id)
	}
	// Results that tell nothing any more: the item is gone, or its error was
	// fixed or is being retried.
	q.ring = slices.DeleteFunc(q.ring, func(it QueueItem) bool {
		if it.ProjectID != st.id {
			return false
		}
		a := regenAsset(p, it.Regen, it.ItemID)
		return a == nil || (it.Status == JobError && a.Status != StatusError)
	})
	ps = projScan{id: st.id, title: p.Title}
	if rows && p.Job != nil {
		it := jobItem(st, p)
		ps.job = &it
	}
	return ps, true
}

// visit checks one asset: n is the scene number, name the cast name, thumb
// the relative path of the picture to show.
func (s *assetScan) visit(kind RegenKind, id string, a *Asset, n int, name, thumb string) {
	busy := a.Status == StatusRunning || a.Status == StatusQueued
	if !busy && len(s.live) == 0 {
		return
	}
	key := assetKey{kind, id}
	la := s.live[key]
	if !busy {
		if la != nil {
			delete(s.live, key)
			s.finished(la, a, thumb)
		}
		return
	}
	if la == nil {
		// A job that makes assets of this kind owns them; Regen's short
		// "queued" state is the only mark of a single one.
		if jobCovers(s.job, kind) && a.Status != StatusQueued {
			return
		}
		la = &liveAsset{started: s.now, updatedAt: a.UpdatedAt}
		if s.live == nil {
			s.live = map[assetKey]*liveAsset{}
			s.q.live[s.st.id] = s.live
		}
		s.live[key] = la
	}
	la.seen = s.gen
	msg := "Генерується…"
	if a.Status == StatusQueued {
		msg = "У черзі"
	}
	la.item = QueueItem{
		Key:          fmt.Sprintf("asset:%s:%s:%s:%d", s.st.id, kind, id, la.started),
		ProjectID:    s.st.id,
		ProjectTitle: s.p.Title,
		Regen:        kind,
		ItemID:       id,
		Label:        assetLabel(kind, n, name, s.grid),
		Step:         regenStep(kind),
		Status:       JobRunning,
		Progress:     max(0, min(100, a.Progress)),
		Message:      msg,
		StartedAt:    la.started,
		Thumb:        absIn(s.st.dir, thumb),
	}
}

// finished records how a single asset ended: a new file or an error. One
// that stopped without a result (the app is closing, nothing to voice) is
// just forgotten.
func (s *assetScan) finished(la *liveAsset, a *Asset, thumb string) {
	it := la.item
	switch {
	case a.Status == StatusError:
		it.Status, it.Error, it.Message, it.FinishedAt = JobError, a.Error, a.Error, s.now
	case a.Status == StatusDone && a.UpdatedAt != la.updatedAt:
		it.Status, it.Message, it.Progress, it.FinishedAt = JobDone, "Готово", 100, a.UpdatedAt
	default:
		return
	}
	it.FinishedAt = max(it.FinishedAt, it.StartedAt)
	it.ProjectTitle = s.p.Title
	it.Thumb = absIn(s.st.dir, thumb)
	s.q.push(it)
}

// push adds a finished single asset to the ring: newest first, only the
// latest result per asset, at most queueRecentMax.
func (q *queueTracker) push(it QueueItem) {
	ring := make([]QueueItem, 0, min(len(q.ring)+1, queueRecentMax))
	ring = append(ring, it)
	for _, old := range q.ring {
		if len(ring) == queueRecentMax {
			break
		}
		if old.ProjectID == it.ProjectID && old.Regen == it.Regen && old.ItemID == it.ItemID {
			continue
		}
		ring = append(ring, old)
	}
	q.ring = ring
}
