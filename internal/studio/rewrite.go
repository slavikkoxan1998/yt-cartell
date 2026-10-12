package studio

// Automatic prompt rewrites. A generation refused for its CONTENT (a safety
// filter, a real person, a brand, G-Labs/Google INVALID_ARGUMENT,
// REPUTATIONAL, PROMINENT_PERSON, DANGEROUS; OpenAI / fal / Replicate /
// RoyalTechno content_policy, "safety", "moderation" …) is refused again
// when resubmitted unchanged. So after the first refusal the studio asks the
// text model to REWRITE the prompt so it passes the filter while keeping the
// scene: fictional people instead of real ones, softened violence / drugs /
// weapons / tragedies, no brands, every @tag and the continuity kept — and
// tries again with it. At most maxRewrites rewrites; then the exact error
// stays on the asset (never a loop). Transient failures (429, 5xx,
// timeouts) are not rewritten: the providers' own backoff handles them.
//
// A rewrite that went through is kept on the asset (Asset.Rewrite): the
// scene card shows «Промт переписано автоматично» with the original and the
// new prompt and can revert it (RevertRewrite). While the studio still
// composes the same original prompt, the next generation of that asset
// starts from the rewritten one right away.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	"ytcartell/internal/gen"
	"ytcartell/internal/glabs"
	"ytcartell/internal/llm"
	"ytcartell/internal/providers"
)

// maxRewrites is how many times one generation may rewrite its prompt.
const maxRewrites = 2

// rewriteTimeout bounds one rewrite call of the text model.
const rewriteTimeout = 90 * time.Second

// PromptRewrite is a prompt the studio rewrote after a content refusal.
type PromptRewrite struct {
	// Original is the prompt the studio composed (what was refused).
	Original string `json:"original"`
	// Prompt is the rewritten prompt that went through.
	Prompt string `json:"prompt"`
	// Reason is the refusal that started it (the service's exact error).
	Reason string `json:"reason,omitempty"`
	// Count is how many rewrites it took (1…maxRewrites).
	Count int   `json:"count,omitempty"`
	At    int64 `json:"at,omitempty"`
}

var (
	// reRefusal: what content refusals of every provider say (the G-Labs
	// ones are recognised by glabs.Rejection first).
	reRefusal = regexp.MustCompile(`(?i)PROMINENT_PERSON|REPUTATIONAL|DANGEROUS|UNSAFE_GENERATION|IMAGE_SAFETY|PROHIBITED_CONTENT|` +
		`blockReason|finishReason (SAFETY|IMAGE_SAFETY|PROHIBITED|BLOCKLIST|RECITATION)|raiMediaFiltered|responsible ?ai|` +
		`content[_ ]policy|content[_ ]filter|policy violation|safety (system|filter|check)|moderation|flagged|nsfw|` +
		`фільтр\S* безпеки|E005|sensitive content|not allowed content|inappropriate`)
	// reTransient: never rewritten — waiting helps, a new prompt does not.
	reTransient = regexp.MustCompile(`(?i)\b(429|500|502|503|504)\b|RESOURCE_EXHAUSTED|quota|rate limit|timeout|timed out|UNAVAILABLE|не відповів вчасно|тимчасов`)
	reTag       = regexp.MustCompile(`@[A-Za-z0-9_]+`)
)

// contentRefusal reports a failure the same prompt would get again: the
// service refused the request for its content. Transient failures (rate
// limits, server trouble, timeouts) and cancellations are not refusals.
func contentRefusal(err error) bool {
	if err == nil || isCancel(err) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if glabs.Rejection(err) != "" {
		return true
	}
	var pe *providers.Error
	if errors.As(err, &pe) && pe.Retry {
		return false
	}
	msg := err.Error()
	if glabs.Rejected(msg) {
		return true
	}
	return reRefusal.MatchString(msg) && !reTransient.MatchString(msg)
}

// rewriteError: the prompt was rewritten (tries times) and still refused.
// It reads as the last exact error with a note and unwraps to it.
type rewriteError struct {
	last  error
	tries int
}

func (e *rewriteError) Error() string {
	return e.last.Error() + fmt.Sprintf("\n(Промт автоматично переписано %d раз(и) — фільтр усе одно відхилив. Зміни опис сцени вручну.)", e.tries)
}

func (e *rewriteError) Unwrap() error { return e.last }

// rewriteFunc rewrites prompt after refusal; the engine's default asks the
// text model (rewriteWithLLM). Tests replace it.
type rewriteFunc func(ctx context.Context, kind, prompt string, refusal error) (string, error)

// withRewrites runs try with the prompt, rewriting it after a content
// refusal (at most maxRewrites times). original is the prompt the studio
// composed; prev the rewrite stored on the asset — reused while the
// original is still the same. It returns the rewrite that went through (nil
// when the original did) or the error (a rewriteError when rewrites were
// tried). limit (≤ maxRewrites) caps the rewrites of this call.
func (e *Engine) withRewrites(ctx context.Context, st *proj, kind, original string, prev *PromptRewrite, limit int, try func(prompt string) error) (*PromptRewrite, error) {
	prompt, rec := original, (*PromptRewrite)(nil)
	if prev != nil && prev.Original == original && strings.TrimSpace(prev.Prompt) != "" {
		prompt, rec = prev.Prompt, prev
	}
	err := try(prompt)
	var first error
	tries := 0
	for err != nil && tries < min(limit, maxRewrites) && contentRefusal(err) && ctx.Err() == nil {
		next, rerr := e.rewrite(ctx, kind, prompt, err)
		if rerr != nil {
			log.Printf("[studio:%s] %s prompt rewrite failed: %v", st.id, kind, rerr)
			break
		}
		if first == nil {
			first = err
		}
		tries++
		log.Printf("[studio:%s] %s refused for its content (%v); retrying with rewrite %d", st.id, kind, err, tries)
		prompt = next
		err = try(prompt)
		rec = &PromptRewrite{Original: original, Prompt: prompt, Reason: clipRunes(e.failureText(first), 600), Count: tries, At: nowMs()}
	}
	if err != nil {
		if tries > 0 {
			return nil, &rewriteError{last: err, tries: tries}
		}
		return nil, err
	}
	return rec, nil
}

// rewrite asks the configured rewriter (the text model by default).
func (e *Engine) rewrite(ctx context.Context, kind, prompt string, refusal error) (string, error) {
	if e.rewriter != nil {
		return e.rewriter(ctx, kind, prompt, refusal)
	}
	return e.rewriteWithLLM(ctx, kind, prompt, refusal)
}

// rewriteWithLLM asks the text model for a version of prompt that passes
// the filter, keeping the scene. The reply must be {"prompt": "…"}; tags the
// model dropped are named again at the end (they bind the references).
func (e *Engine) rewriteWithLLM(ctx context.Context, kind, prompt string, refusal error) (string, error) {
	system := "You are a careful prompt editor for AI " + kind + " generation. A safety / content filter refused the prompt below. " +
		"Rewrite it so it passes the filter while keeping the scene's meaning, composition, camera, mood and art style."
	rules := strings.Join([]string{
		"Rules:",
		"- Real, famous or historical people and any real person's name → a fictional character described generically (age, build, hair, outfit); never name or resemble a real person.",
		"- Soften violence, weapons, blood, injuries, drugs, alcohol abuse, self-harm, disasters and real tragedies: imply them (tension, shadows, aftermath, reactions) instead of showing them.",
		"- Remove brand names, logos, trademarks, real political symbols and real dates of tragedies.",
		"- Children only in safe, wholesome situations; nothing sexual or suggestive.",
		"- Keep EVERY @tag (like @hero) exactly as written — they bind reference images — and keep the same characters, outfits, places, lighting and continuity.",
		"- Keep every layout, format and \"no text\" instruction (grids, panels, aspect ratio) word for word.",
		"- English, about the same length. No explanations.",
	}, "\n")
	why := clipRunes(e.redact(errText(refusal)), 600)
	user := rules + "\n\nThe filter said: " + why + "\n\nPrompt:\n<<<\n" + prompt + "\n>>>\n\nReply with JSON only: {\"prompt\": \"<the rewritten prompt>\"}"
	var out struct {
		Prompt string `json:"prompt"`
	}
	if err := e.text.JSON(ctx, llm.Request{System: system, Prompt: user, Temperature: 0.4, Timeout: rewriteTimeout}, &out); err != nil {
		return "", err
	}
	next := strings.TrimSpace(out.Prompt)
	switch {
	case next == "":
		return "", errors.New("порожня відповідь текстової моделі")
	case next == strings.TrimSpace(prompt):
		return "", errors.New("текстова модель повернула той самий промт")
	}
	if r := []rune(next); len(r) > 4*len([]rune(prompt))+400 {
		next = string(r[:4*len([]rune(prompt))+400])
	}
	return keepTags(prompt, next), nil
}

// keepTags names again (at the end) every @tag of prompt that next lost.
func keepTags(prompt, next string) string {
	var lost []string
	seen := map[string]bool{}
	for _, t := range reTag.FindAllString(prompt, -1) {
		if seen[t] {
			continue
		}
		seen[t] = true
		if !strings.Contains(next, t) {
			lost = append(lost, t)
		}
	}
	if len(lost) == 0 {
		return next
	}
	return next + "\nReferences: " + strings.Join(lost, ", ") + " — exactly as in their reference images."
}

// rewriteOf is the rewrite stored on an asset (nil when none).
func rewriteOf(a *Asset) *PromptRewrite {
	if a == nil {
		return nil
	}
	return a.Rewrite
}

// RevertRewrite drops the automatic rewrite of an asset (a reference, a
// frame or board, a clip): the next generation uses the studio's own prompt
// again. The asset is marked outdated so the card offers to regenerate it.
func (e *Engine) RevertRewrite(id string, kind RegenKind, itemID string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var ref assetRef
	switch kind {
	case RegenCharacter:
		ref = assetRef{slotCharacter, itemID}
	case RegenLocation:
		ref = assetRef{slotLocation, itemID}
	case RegenFrame:
		ref = assetRef{slotFrame, itemID}
	case RegenVideo:
		ref = assetRef{slotVideo, itemID}
	default:
		return fmt.Errorf("невідомий тип елемента «%s»", kind)
	}
	found := false
	if !e.update(st, func(p *Project) {
		a := ref.in(p)
		if a == nil || a.Rewrite == nil {
			return
		}
		found = true
		a.Rewrite = nil
		if a.Path != "" {
			a.Stale = true
		}
		if kind == RegenFrame { // board and frame share one prompt history
			if s := findScene(p, itemID); s != nil {
				s.Board.Rewrite = nil
			}
		}
	}) {
		return ErrNotFound
	}
	if !found {
		return errors.New("автоматичного переписування промту тут немає")
	}
	return nil
}

// imageRewriting generates req, rewriting its prompt after content refusals
// (withRewrites; at most limit rewrites).
func (e *Engine) imageRewriting(ctx context.Context, st *proj, req gen.ImageRequest, prev *PromptRewrite, limit int) (data []byte, mime string, rw *PromptRewrite, err error) {
	rw, err = e.withRewrites(ctx, st, "image", req.Prompt, prev, limit, func(prompt string) error {
		r := req
		r.Prompt = prompt
		var ierr error
		data, mime, ierr = e.image(ctx, r)
		return ierr
	})
	if err != nil {
		return nil, "", nil, err
	}
	return data, mime, rw, nil
}

// clipRewriting renders a clip (renderClip: the request, and one safer form
// after a G-Labs refusal); after a content refusal the PLAIN prompt (the
// frame animated alone — what passes most often) is rewritten and sent
// without the components references. The stored rewrite of the clip is
// reused while the plain prompt is the same.
func (e *Engine) clipRewriting(ctx context.Context, st *proj, in *shotInputs, req gen.VideoRequest, progress gen.Progress) (data []byte, rw *PromptRewrite, err error) {
	plain := in.plain
	if plain.Start == nil {
		plain = req
	}
	first := true
	rw, err = e.withRewrites(ctx, st, "video", plain.Prompt, in.scene.Video.Rewrite, maxRewrites, func(prompt string) error {
		var verr error
		if first && prompt == plain.Prompt {
			first = false
			data, verr = e.renderClip(ctx, st, in, req, progress)
			return verr
		}
		first = false
		r := plain
		r.Prompt = prompt
		data, verr = e.video(ctx, r, progress)
		return verr
	})
	if err != nil {
		return nil, nil, err
	}
	return data, rw, nil
}

// refusedText reports a stored failure (an asset's Error) that is a content
// refusal of any provider, or one the rewrites could not get through: the
// autopilot's retry rounds never resubmit it (the same prompt fails again).
func refusedText(msg string) bool {
	if strings.Contains(msg, "Промт автоматично переписано") {
		return true
	}
	return reRefusal.MatchString(msg) && !reTransient.MatchString(msg)
}
