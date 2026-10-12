package studio

// Jobs: one background stage per project (port of the store.ts job runner and
// POST /projects/:id/run/:stage).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
)

// Stage is a background stage the UI can start.
type Stage string

const (
	StagePlan    Stage = "plan"
	StageRefs    Stage = "refs"
	StageFrames  Stage = "frames"
	StageRender  Stage = "render" // videos + voices
	StageVideos  Stage = "videos"
	StageVoices  Stage = "voices"
	StageMontage Stage = "montage"
	StageCover   Stage = "cover"
	// StageAutopilot runs plan → refs → frames → render (+ retries) → montage
	// (→ cover) as one job (see autopilot.go).
	StageAutopilot Stage = "autopilot"
)

// RegenKind is what a single-item regeneration targets.
type RegenKind string

const (
	RegenCharacter RegenKind = "character"
	RegenLocation  RegenKind = "location"
	RegenFrame     RegenKind = "frame"
	RegenVideo     RegenKind = "video"
	RegenVoice     RegenKind = "voice"
)

var jobLabels = map[JobKind]string{
	JobPlan:      "План",
	JobRefs:      "Референси",
	JobFrames:    "Розкадровка",
	JobVideos:    "Відео",
	JobVoices:    "Озвучка",
	JobRender:    "Відео й озвучка",
	JobMontage:   "Монтаж",
	JobCover:     "Обкладинка",
	JobAutopilot: "Автопілот",
}

// busyError is returned by Run while another job is running.
type busyError struct{ kind JobKind }

func (b *busyError) Error() string {
	label := jobLabels[b.kind]
	if label == "" {
		label = string(b.kind)
	}
	return fmt.Sprintf("Зараз уже виконується етап «%s». Дочекайся завершення або зупини його.", label)
}

func (b *busyError) Is(target error) bool { return target == ErrBusy }

// jobCtx is what a running stage sees (TS JobCtx).
type jobCtx struct {
	e  *Engine
	st *proj
	// ctx is cancelled by Cancel: text-model calls and ffmpeg stop at once.
	ctx  context.Context
	stop context.CancelFunc
	// work is NOT cancelled by Cancel: paid image/video/voice generations that
	// are already submitted finish and are kept ("поточні задачі доробляться").
	// It ends only when the project is deleted.
	work context.Context
	flag atomic.Bool
	// sub, when set, maps the 0–100 progress of a stage runner into a slice
	// of a bigger job (the autopilot) and prefixes its messages.
	sub atomic.Pointer[subRange]
	// retrying is on during the autopilot's retry rounds: the runners then
	// leave items G-Labs refused for their content alone (jobCtx.skip).
	retrying atomic.Bool
	// missing is what the montage does with scenes that are not ready
	// (RunOptions.Missing; gaps.go).
	missing string
}

type subRange struct {
	from, span float64
	prefix     string
}

func (j *jobCtx) cancelled() bool { return j.flag.Load() || j.work.Err() != nil }

// within makes the following progress reports land in [from, from+span] of
// the job with prefix before every message; span ≤ 0 restores the full range.
func (j *jobCtx) within(from, span float64, prefix string) {
	if span <= 0 {
		j.sub.Store(nil)
		return
	}
	j.sub.Store(&subRange{from: from, span: span, prefix: prefix})
}

// progress sets the job's percentage and (unless stopping) its message.
func (j *jobCtx) progress(pct float64, msg string) {
	if r := j.sub.Load(); r != nil {
		pct = r.from + r.span*max(0, min(100, pct))/100
		if msg != "" {
			msg = r.prefix + msg
		}
	}
	j.e.update(j.st, func(p *Project) {
		if p.Job == nil || p.Job.Status != JobRunning {
			return
		}
		p.Job.Progress = max(0, min(100, int(math.Floor(pct+0.5))))
		if msg != "" && !j.flag.Load() {
			p.Job.Message = msg
		}
	})
}

// note replaces the running job's message and keeps its progress (inside
// the autopilot the stage prefix applies, like for progress).
func (j *jobCtx) note(msg string) {
	if msg == "" {
		return
	}
	if r := j.sub.Load(); r != nil {
		msg = r.prefix + msg
	}
	j.e.update(j.st, func(p *Project) {
		if p.Job != nil && p.Job.Status == JobRunning && !j.flag.Load() {
			p.Job.Message = msg
		}
	})
}

// snapshot is a deep copy of the live project.
func (j *jobCtx) snapshot() Project {
	j.st.mu.Lock()
	defer j.st.mu.Unlock()
	return cloneProject(j.st.p)
}

// RunOptions are the options of RunWith.
type RunOptions struct {
	// Force regenerates items that are already done (autopilot: re-plans a
	// project that already has a plan).
	Force bool
	// Prompt is the cover prompt (StageCover, the autopilot's cover).
	Prompt string
	// Missing is what the montage (StageMontage, the autopilot's montage)
	// does with scenes that are not ready (gaps.go): MissingAsk refuses
	// with a MissingScenesError until the user chooses MissingSkip or
	// MissingStill; the autopilot without a choice here follows
	// config.Autopilot.OnFailed.
	Missing string
}

// Run starts a background stage and returns at once; progress and the result
// land in Project.Job (Subscribe to follow). Only one job per project.
// force regenerates items that are already done (autopilot: re-plans a
// project that already has a plan); prompt is the cover prompt. A montage
// with scenes that are not ready is refused (MissingScenesError) — see
// RunWith.
func (e *Engine) Run(id string, stage Stage, force bool, prompt string) error {
	return e.RunWith(id, stage, RunOptions{Force: force, Prompt: prompt})
}

// RunWith is Run with all the options.
func (e *Engine) RunWith(id string, stage Stage, opt RunOptions) error {
	force, prompt := opt.Force, opt.Prompt
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var (
		kind  JobKind
		check func(p *Project) error
		run   func(j *jobCtx) (string, error)
	)
	switch stage {
	case StagePlan:
		kind, run = JobPlan, e.runPlan
	case StageRefs:
		kind = JobRefs
		check = func(p *Project) error {
			if len(p.Characters) == 0 && len(p.Locations) == 0 {
				return errors.New("Немає персонажів і локацій — спершу створи план")
			}
			return nil
		}
		run = func(j *jobCtx) (string, error) { return e.runRefs(j, force, 0, 100), nil }
	case StageFrames:
		kind = JobFrames
		check = func(p *Project) error {
			if len(p.Scenes) == 0 {
				return errors.New("Немає сцен — спершу створи план")
			}
			return nil
		}
		run = func(j *jobCtx) (string, error) { return e.runFrames(j, force), nil }
	case StageRender, StageVideos, StageVoices:
		kind = JobKind(stage)
		if stage != StageVoices {
			check = func(p *Project) error {
				if p.Input.CodeAnim() { // drawn by code: no frames needed
					if len(p.Scenes) == 0 {
						return errors.New("Немає сцен — спершу створи план")
					}
					return nil
				}
				for _, s := range p.Scenes {
					if s.Frame.Path != "" {
						return nil
					}
				}
				return errors.New("Спершу згенеруй кадри (Розкадровка)")
			}
		}
		run = func(j *jobCtx) (string, error) {
			return e.runRender(j, stage != StageVoices, stage != StageVideos, force), nil
		}
	case StageMontage:
		kind = JobMontage
		dir := st.dir
		check = func(p *Project) error {
			// Never a silent still or a dropped scene: ask first (gaps.go).
			return errMissing(MontageGaps(p, dir), opt.Missing)
		}
		run = func(j *jobCtx) (string, error) { return e.runMontage(j) }
	case StageCover:
		kind = JobCover
		run = func(j *jobCtx) (string, error) { return e.runCover(j, prompt) }
	case StageAutopilot:
		kind = JobAutopilot
		check = func(p *Project) error {
			// Own voice-over: the plan transcribes the recording first.
			voice := p.Input.Mode == ModeVoice && p.Input.VoiceFile != ""
			if (force || len(p.Scenes) == 0) && strings.TrimSpace(p.Input.Text) == "" && !voice {
				return errors.New("Спершу напиши історію або сценарій")
			}
			return nil
		}
		run = func(j *jobCtx) (string, error) { return e.runAutopilot(j, force, prompt) }
	default:
		return fmt.Errorf("невідомий етап «%s»", stage)
	}

	st.mu.Lock()
	if st.deleted.Load() {
		st.mu.Unlock()
		return ErrNotFound
	}
	if e.closing.Load() {
		st.mu.Unlock()
		return ErrClosing
	}
	if j := st.p.Job; st.job != nil || (j != nil && j.Status == JobRunning) {
		k := kind
		if j != nil {
			k = j.Kind
		}
		st.mu.Unlock()
		return &busyError{k}
	}
	if check != nil {
		if err := check(st.p); err != nil {
			st.mu.Unlock()
			return err
		}
	}
	jc := &jobCtx{e: e, st: st, work: st.ctx, missing: opt.Missing}
	jc.ctx, jc.stop = context.WithCancel(st.ctx)
	st.job = jc
	st.p.Job = &Job{Kind: kind, Status: JobRunning, Message: "Запуск…", StartedAt: nowMs()}
	list := e.commitLocked(st)
	st.bg.Add(1)
	st.mu.Unlock()
	e.emit(st, list)

	go e.runJob(st, jc, kind, run)
	return nil
}

func (e *Engine) runJob(st *proj, jc *jobCtx, kind JobKind, run func(*jobCtx) (string, error)) {
	defer st.bg.Done()
	msg, err := func() (msg string, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[studio:%s] %s panic: %v\n%s", st.id, kind, r, debug.Stack())
				err = fmt.Errorf("внутрішня помилка: %v", r)
			}
		}()
		return run(jc)
	}()
	jc.stop()
	cancelled := jc.flag.Load()

	// The job-end notification (Telegram) is registered before the final
	// state becomes visible and gets the finished job after the save.
	notice := e.jobNotice()
	var final *Job
	st.mu.Lock()
	if job := st.p.Job; job != nil && job.Status == JobRunning {
		switch {
		case err != nil && !(cancelled && isCancel(err)):
			job.Status = JobError
			job.Error = e.redact(errText(err))
			job.Message = job.Error
			if _, ok := IsMissingScenes(err); ok {
				job.Blocked = BlockedGaps
			}
			log.Printf("[studio:%s] %s failed: %s", st.id, kind, job.Error)
		case cancelled:
			job.Status = JobCancelled
			job.Message = "Зупинено"
		default:
			job.Status = JobDone
			job.Progress = 100
			job.Failures = jobFailures(st.p, kind)
			if msg == "" {
				msg = "Готово"
			}
			job.Message = msg
		}
		job.FinishedAt = nowMs()
		if !st.deleted.Load() {
			done := *job
			final = &done
		}
	}
	if st.job == jc {
		st.job = nil
	}
	list := false
	if !st.deleted.Load() {
		list = e.commitLocked(st)
	}
	st.mu.Unlock()
	if err := e.writeNow(st); err != nil {
		log.Printf("[studio:%s] %v", st.id, err)
	}
	e.emit(st, list)
	notice(st, final) // returns at once: sending runs in the background
}

// Cancel asks the running job to stop: no new items start, text-model calls
// and ffmpeg stop at once, generations already submitted finish.
func (e *Engine) Cancel(id string) {
	if st := e.state(id); st != nil {
		e.cancelJob(st)
	}
}

// cancelJob is Cancel for one project state; false when no job runs there.
func (e *Engine) cancelJob(st *proj) bool {
	st.mu.Lock()
	jc := st.job
	if jc == nil || st.deleted.Load() {
		st.mu.Unlock()
		return false
	}
	jc.flag.Store(true)
	jc.stop()
	if st.p.Job != nil {
		st.p.Job.Message = "Зупиняємо… (поточні задачі доробляться)"
	}
	list := e.commitLocked(st)
	st.mu.Unlock()
	e.emit(st, list)
	return true
}

// mapLimit runs task for every item with bounded concurrency; no new item
// starts once cancelled() reports true or a task failed. Returns the first
// error (a panicking task counts as an error).
func mapLimit[T any](items []T, limit int, cancelled func() bool, task func(item T, i int) error) error {
	if len(items) == 0 {
		return nil
	}
	n := max(1, min(limit, len(items)))
	var (
		mu       sync.Mutex
		next     int
		firstErr error
		wg       sync.WaitGroup
	)
	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if next >= len(items) || firstErr != nil || cancelled() {
					mu.Unlock()
					return
				}
				i := next
				next++
				mu.Unlock()
				err := func() (err error) {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[studio] task panic: %v\n%s", r, debug.Stack())
							err = fmt.Errorf("внутрішня помилка: %v", r)
						}
					}()
					return task(items[i], i)
				}()
				if err != nil {
					setErr(err)
				}
			}
		}()
	}
	wg.Wait()
	return firstErr
}

// counter returns a tick that reports "label · n/total" progress in [from, from+span].
func counter(j *jobCtx, total int, label string, from, span float64) func() {
	var mu sync.Mutex
	n := 0
	return func() {
		mu.Lock()
		defer mu.Unlock()
		n++
		j.progress(from+span*float64(n)/float64(max(1, total)), fmt.Sprintf("%s · %d/%d", label, n, total))
	}
}
