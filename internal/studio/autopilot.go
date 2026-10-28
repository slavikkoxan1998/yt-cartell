package studio

// Autopilot: story text → finished mp4 as ONE unattended job.
//
//	plan      skipped when the project already has one (unless forced)
//	refs      character and location references
//	frames    scene frames (the frames runner also handles storyboard mode)
//	render    clips + voices
//	retries   failed/empty items only, up to cfg.Autopilot.Retries rounds
//	montage   scenes that are not ready (gaps.go) follow the choice of this
//	          run (RunOptions.Missing), else Settings (cfg.Autopilot.OnFailed);
//	          without one the autopilot stops before the montage and asks
//	          (Job.Blocked = BlockedGaps) — never a silent still or a dropped scene
//	cover     when cfg.Autopilot.MakeCover (an existing cover is kept unless forced)
//
// On a project that is already (partly) produced the autopilot continues
// where it stopped: every runner skips finished items.
//
// Every stage runner reports its own 0–100 progress; jobCtx.within maps it
// into the stage's slice of the autopilot job and prefixes the messages
// ("Автопілот · Кадри сцен · 3/8"). Cancel stops between and inside stages
// exactly like the single stages.

import (
	"errors"
	"fmt"
	"strings"

	"ytcartell/internal/glabs"
)

const apPrefix = "Автопілот · "

// Progress slices of the autopilot job (percent).
const (
	apPlanFrom, apPlanSpan     = 0, 15
	apRefsFrom, apRefsSpan     = 15, 10
	apFramesFrom, apFramesSpan = 25, 20
	apRenderFrom, apRenderSpan = 45, 35
	apRetryFrom, apRetrySpan   = 80, 8
	apMontageFrom              = 88
	apCoverFrom, apCoverSpan   = 97, 3
	apMontageSpanWithCover     = apCoverFrom - apMontageFrom
	apMontageSpanWithoutCover  = 100 - apMontageFrom
)

// apFailures counts what is still missing after a production stage.
type apFailures struct{ refs, frames, clips, voices int }

func autopilotFailures(p *Project) apFailures {
	var f apFailures
	skipRefs := p.Input.SkipRefs() // «Без референсів»: missing references are no failures
	for _, c := range p.Characters {
		if c.Image.Status != StatusDone && !skipRefs {
			f.refs++
		}
	}
	for _, l := range p.Locations {
		if l.Image.Status != StatusDone && !skipRefs {
			f.refs++
		}
	}
	code := p.Input.CodeAnim() // «Анімація кодом»: frames only for scenes that fell back
	for _, s := range p.Scenes {
		if s.Frame.Status != StatusDone && (!code || s.Code.Fallback) {
			f.frames++
		}
		if s.Video.Status != StatusDone && !p.Input.StillsOnly() && !(code && s.Code.Fallback) { // «Тільки картинки» shoots no clips
			f.clips++
		}
		spoken := strings.TrimSpace(s.Narration) != "" || s.AudioSource == AudioUpload // a slice of the own recording needs no text
		if p.Input.Voiceover && spoken && s.Audio.Status != StatusDone {
			f.voices++
		}
	}
	return f
}

func (f apFailures) none() bool { return f == apFailures{} }

// rejected: the asset's last attempt was refused for its content — by G-Labs
// (400 / INVALID_ARGUMENT / REPUTATIONAL …) or any provider's filter, after the
// automatic prompt rewrites (rewrite.go) did not get it through. The
// same request is refused again, so the autopilot never resubmits it
// unchanged — the user changes the prompt or the references and regenerates.
// Quotas (429), server trouble (5xx, UPLOAD_ERROR) and timeouts stay
// retryable.
func rejected(a Asset) bool {
	return a.Status == StatusError && (glabs.Rejected(a.Error) || refusedText(a.Error))
}

// skip: a runner leaves a alone — during the autopilot's retry rounds, an
// item G-Labs refused for its content.
func (j *jobCtx) skip(a Asset) bool { return j.retrying.Load() && rejected(a) }

// autopilotRejected counts the missing items a retry round cannot redo:
// refused for their content, or a clip whose frame was.
func autopilotRejected(p *Project) apFailures {
	var f apFailures
	for _, c := range p.Characters {
		if rejected(c.Image) {
			f.refs++
		}
	}
	for _, l := range p.Locations {
		if rejected(l.Image) {
			f.refs++
		}
	}
	for _, s := range p.Scenes {
		if rejected(s.Frame) {
			f.frames++
		}
		if s.Video.Status != StatusDone && (rejected(s.Video) || rejected(s.Frame)) {
			f.clips++
		}
	}
	return f
}

// minus is f without g (never below zero).
func (f apFailures) minus(g apFailures) apFailures {
	return apFailures{refs: max(0, f.refs-g.refs), frames: max(0, f.frames-g.frames),
		clips: max(0, f.clips-g.clips), voices: max(0, f.voices-g.voices)}
}

// String lists the missing items in Ukrainian ("кадрів: 1, кліпів: 2").
func (f apFailures) String() string {
	var parts []string
	for _, x := range []struct {
		n    int
		what string
	}{{f.refs, "референсів"}, {f.frames, "кадрів"}, {f.clips, "кліпів"}, {f.voices, "озвучок"}} {
		if x.n > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", x.what, x.n))
		}
	}
	return strings.Join(parts, ", ")
}

// runAutopilot is the StageAutopilot job. force re-plans a project that
// already has a plan; coverPrompt is the optional cover prompt.
func (e *Engine) runAutopilot(j *jobCtx, force bool, coverPrompt string) (string, error) {
	ap := e.cfg.Get().Autopilot
	defer j.within(0, 0, "")
	phase := func(from, span float64, prefix, label string) {
		j.within(from, span, prefix)
		j.progress(0, label)
	}
	const stopped = "Зупинено"

	// 1. plan
	if snap := j.snapshot(); force || len(snap.Scenes) == 0 {
		phase(apPlanFrom, apPlanSpan, apPrefix, "План")
		if _, err := e.runPlan(j); err != nil {
			return "", err
		}
		if j.cancelled() {
			return stopped, nil
		}
	}

	// 2–4. references, frames, clips + voices
	cfg := e.cfg.Get()
	stockMode := cfg.Stock.Enabled || cfg.Video.Provider == "stock" || j.snapshot().Input.Pipeline == "stock"
	if !stockMode {
		phase(apRefsFrom, apRefsSpan, apPrefix, "Референси")
		e.runRefs(j, false, 0, 100)
		if j.cancelled() {
			return stopped, nil
		}
		phase(apFramesFrom, apFramesSpan, apPrefix, "Кадри")
		e.runFrames(j, false)
		if j.cancelled() {
			return stopped, nil
		}
	}
	phase(apRenderFrom, apRenderSpan, apPrefix, "Відео й озвучка")
	e.runRender(j, true, true, false)
	if j.cancelled() {
		return stopped, nil
	}

	// 5. retries: the runners skip finished items, so each round re-runs only
	// what failed (a new frame first, then its clip) — never what G-Labs
	// refused for its content (rejected: the same request fails again).
	defer j.retrying.Store(false)
	for round := 1; round <= ap.Retries; round++ {
		snap := j.snapshot()
		f := autopilotFailures(&snap).minus(autopilotRejected(&snap))
		if f.none() {
			break
		}
		j.retrying.Store(true)
		span := float64(apRetrySpan) / float64(ap.Retries)
		from := apRetryFrom + span*float64(round-1)
		prefix := fmt.Sprintf("%sПовтор %d/%d · ", apPrefix, round, ap.Retries)
		phase(from, span, prefix, "повторюємо невдалі ("+f.String()+")")
		third := span / 3
		if f.refs > 0 && !stockMode {
			j.within(from, third, prefix)
			e.runRefs(j, false, 0, 100)
		}
		if f.frames > 0 && !stockMode && !j.cancelled() {
			j.within(from+third, third, prefix)
			e.runFrames(j, false)
		}
		if f.clips+f.voices > 0 && !j.cancelled() {
			j.within(from+2*third, third, prefix)
			e.runRender(j, true, true, false)
		}
		if j.cancelled() {
			return stopped, nil
		}
	}

	// 6. montage: what to do with scenes that are not ready is the user's
	// choice — this run's (the montage dialog), else Settings; without one
	// the autopilot stops here with every such scene and its exact error.
	snap := j.snapshot()
	if j.missing == MissingAsk {
		j.missing = ap.OnFailed
	}
	if err := errMissing(MontageGaps(&snap, j.st.dir), j.missing); err != nil {
		return "", fmt.Errorf("Автопілот зупинився перед монтажем. %w", err)
	}
	// A cover that already exists is kept unless forced.
	cover := ap.MakeCover && (force || snap.Montage.Cover.Status != StatusDone)
	span := float64(apMontageSpanWithoutCover)
	if cover {
		span = apMontageSpanWithCover
	}
	phase(apMontageFrom, span, apPrefix, "Монтаж")
	msg, err := e.runMontage(j)
	if err != nil {
		return "", err
	}

	// 7. cover: a failed cover does not undo the finished film (the error
	// stays on the cover asset).
	note := ""
	if cover && !j.cancelled() {
		phase(apCoverFrom, apCoverSpan, apPrefix, "Обкладинка")
		if _, err := e.runCover(j, coverPrompt); err != nil {
			if isCancel(err) {
				note = " · обкладинку зупинено"
			} else {
				note = " · обкладинка не вдалась: " + clipRunes(e.redact(errText(err)), 600)
			}
		}
	}
	return "Автопілот завершено · " + msg + note, nil
}

// StartAutopilot creates a project from a story and runs the autopilot on it.
// title "" lets the plan name the film; profileID "" applies the default
// autopilot profile from Settings (a deleted default falls back to the app
// defaults), an unknown explicit profile is an error. The id of the new
// project is returned even when starting the job failed after it was created.
func (e *Engine) StartAutopilot(title, text, profileID string) (projectID string, err error) {
	if e.closing.Load() {
		return "", ErrClosing
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("Спершу напиши історію або сценарій")
	}
	pid := strings.TrimSpace(profileID)
	if pid == "" {
		pid = strings.TrimSpace(e.cfg.Get().Autopilot.DefaultProfileID)
		if _, ok := e.profiles.Get(pid); !ok {
			pid = ""
		}
	}
	p, err := e.CreateWithProfile(title, pid)
	if err != nil {
		return p.ID, err
	}
	if err := e.Edit(p.ID, func(p *Project) {
		p.Input.Text = text
		p.Input.Mode = ModeStory
	}); err != nil {
		return p.ID, err
	}
	return p.ID, e.Run(p.ID, StageAutopilot, false, "")
}
