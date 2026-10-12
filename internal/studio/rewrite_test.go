package studio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/glabs"
	"ytcartell/internal/providers"
)

// ---- content refusals → prompt rewrites ------------------------------------------------------

// TestContentRefusal: content refusals of every provider are rewritten;
// transient failures (rate limits, server trouble, timeouts) and
// cancellations are not.
func TestContentRefusal(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&glabs.Error{Msg: glabs.RejectedPrefix + " — фільтр безпеки Google (PROMINENT_PERSON)", Reject: glabs.RejectSafety}, true},
		{&glabs.Error{Msg: glabs.RejectedPrefix + " · G-Labs [400]: INVALID_ARGUMENT", Reject: glabs.RejectInvalid}, true},
		{fmt.Errorf("кадр: %w", &glabs.Error{Msg: "x", Reject: glabs.RejectSafety}), true},
		{&providers.Error{Msg: "RoyalTechno: запит заблоковано фільтром безпеки — зміни промт або референси"}, true},
		{&providers.Error{Msg: "OpenAI: сервіс відхилив запит (400) — Your request was rejected by the safety system (content_policy_violation)"}, true},
		{errors.New("fal: content_policy: flagged as NSFW"), true},
		{errors.New("Replicate: E005 sensitive content"), true},
		{errors.New("Google: blockReason PROHIBITED_CONTENT"), true},
		{errors.New("Veo відхилив відео фільтром безпеки: REPUTATIONAL"), true},
		{errors.New("G-Labs: DANGEROUS"), true},
		{&providers.Error{Msg: "OpenAI: забагато запитів (429) — зачекай", Retry: true}, false},
		{&providers.Error{Msg: "fal: сервер тимчасово недоступний (503) — safety system busy", Retry: true}, false},
		{errors.New("Google: 503 UNAVAILABLE"), false},
		{errors.New("G-Labs: RESOURCE_EXHAUSTED quota"), false},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{errors.New("не вдалося прочитати референс @taras"), false},
	}
	for _, tc := range cases {
		if got := contentRefusal(tc.err); got != tc.want {
			t.Errorf("contentRefusal(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// fakeRewriter replaces from with to (recording every call).
type fakeRewriter struct {
	mu       sync.Mutex
	from, to string
	calls    []string
	fail     error
}

func (f *fakeRewriter) rewrite(_ context.Context, kind, prompt string, refusal error) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, kind+": "+prompt)
	if f.fail != nil {
		return "", f.fail
	}
	return strings.ReplaceAll(prompt, f.from, f.to) + " (softened)", nil
}

func (f *fakeRewriter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// TestWithRewrites: the loop rewrites after a content refusal at most twice
// and stops with the exact last error; transient failures and a failed
// rewrite never rewrite; a stored rewrite is reused for the same original.
func TestWithRewrites(t *testing.T) {
	refused := &glabs.Error{Msg: glabs.RejectedPrefix + " — фільтр безпеки Google (REPUTATIONAL)", Reject: glabs.RejectSafety}
	st := &proj{id: "p"}
	ctx := context.Background()
	run := func(rw *fakeRewriter, prev *PromptRewrite, outcome func(n int, prompt string) error) (*PromptRewrite, error, []string) {
		e := &Engine{rewriter: rw.rewrite}
		var sent []string
		rec, err := e.withRewrites(ctx, st, "image", "Taylor Swift on @stage", prev, maxRewrites, func(p string) error {
			sent = append(sent, p)
			return outcome(len(sent), p)
		})
		return rec, err, sent
	}

	// Refused once → rewritten once → through.
	rw := &fakeRewriter{from: "Taylor Swift", to: "a fictional pop singer"}
	rec, err, sent := run(rw, nil, func(n int, p string) error {
		if strings.Contains(p, "Taylor Swift") {
			return refused
		}
		return nil
	})
	if err != nil || rec == nil || rec.Count != 1 || rec.Original != "Taylor Swift on @stage" ||
		rec.Prompt != "a fictional pop singer on @stage (softened)" || !strings.Contains(rec.Reason, "REPUTATIONAL") || len(sent) != 2 {
		t.Fatalf("one rewrite: %+v %v %q", rec, err, sent)
	}

	// The stored rewrite is used right away (no refusal, no rewrite).
	rw2 := &fakeRewriter{from: "x", to: "y"}
	rec2, err, sent := run(rw2, rec, func(int, string) error { return nil })
	if err != nil || rec2 != rec || len(sent) != 1 || sent[0] != rec.Prompt || rw2.count() != 0 {
		t.Fatalf("reuse: %+v %v %q", rec2, err, sent)
	}
	// …but not for another original.
	other := &PromptRewrite{Original: "something else", Prompt: "zzz"}
	_, _, sent = run(rw2, other, func(int, string) error { return nil })
	if sent[0] != "Taylor Swift on @stage" {
		t.Fatalf("stale rewrite reused: %q", sent)
	}

	// Always refused → 2 rewrites, then the exact last error.
	rw3 := &fakeRewriter{from: "zzz", to: "zzz"}
	_, err, sent = run(rw3, nil, func(int, string) error { return refused })
	var re *rewriteError
	if !errors.As(err, &re) || re.tries != 2 || len(sent) != 3 || rw3.count() != 2 ||
		!strings.HasPrefix(err.Error(), refused.Msg) || !strings.Contains(err.Error(), "переписано 2 раз") || glabs.Rejection(err) != glabs.RejectSafety {
		t.Fatalf("give up: %v (%d sent, %d rewrites)", err, len(sent), rw3.count())
	}
	if !refusedText(err.Error()) {
		t.Fatal("the stored error must keep the autopilot from resubmitting it")
	}

	// A transient failure is never rewritten.
	rw4 := &fakeRewriter{}
	busy := &providers.Error{Msg: "fal: забагато запитів (429)", Retry: true}
	_, err, sent = run(rw4, nil, func(int, string) error { return busy })
	if !errors.Is(err, busy) || len(sent) != 1 || rw4.count() != 0 {
		t.Fatalf("transient: %v %q", err, sent)
	}

	// The text model cannot rewrite → the original refusal, unchanged.
	rw5 := &fakeRewriter{fail: errors.New("no text model")}
	_, err, sent = run(rw5, nil, func(int, string) error { return refused })
	if err != error(refused) || len(sent) != 1 {
		t.Fatalf("failed rewrite: %v %q", err, sent)
	}
}

func TestKeepTags(t *testing.T) {
	got := keepTags("@taras walks with @lysytsia in @lis", "a boy walks with a fox in a forest @lis")
	if !strings.HasSuffix(got, "\nReferences: @taras, @lysytsia — exactly as in their reference images.") {
		t.Fatalf("keepTags: %q", got)
	}
	if got := keepTags("@a and @b", "@b then @a"); got != "@b then @a" {
		t.Fatalf("keepTags kept: %q", got)
	}
}

// TestRewriteFrameAndClip: a frame and a clip refused for their content
// are rewritten and go through; the rewrite is stored on the asset, reused
// by the next regeneration and can be reverted. A prompt the filter keeps
// refusing ends after two rewrites with the exact error.
func TestRewriteFrameAndClip(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	rw := &fakeRewriter{from: "Taylor Swift", to: "a fictional pop singer"}
	e.rewriter = rw.rewrite
	p := seedScenes(t, e, 2)
	s1, s2 := p.Scenes[0].ID, p.Scenes[1].ID
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Scenes[0].ImagePrompt = "Taylor Swift sings on a stage"
		p.Scenes[0].MotionPrompt = "Taylor Swift waves, slow push-in"
		p.Scenes[1].ImagePrompt = "FAILME forever"
	}))
	env.gl.mu.Lock()
	env.gl.failOn = "Taylor Swift"
	env.gl.mu.Unlock()

	// ---- frame
	must(t, e.Regen(p.ID, RegenFrame, s1, ""))
	p = waitFor(t, e, p.ID, "frame", func(p Project) bool {
		return p.Scenes[0].Frame.Status == StatusDone || p.Scenes[0].Frame.Status == StatusError
	})
	f := p.Scenes[0].Frame
	if f.Status != StatusDone || f.Rewrite == nil || f.Rewrite.Count != 1 || !strings.Contains(f.Rewrite.Original, "Taylor Swift sings") ||
		!strings.Contains(f.Rewrite.Prompt, "a fictional pop singer sings") || !strings.Contains(f.Rewrite.Reason, "G-Labs") {
		t.Fatalf("frame %+v rewrite %+v", f, f.Rewrite)
	}
	if n := len(env.gl.requests("image")); n != 2 || rw.count() != 1 {
		t.Fatalf("image requests %d, rewrites %d", n, rw.count())
	}
	// Regenerated: the stored rewrite goes first — no refusal, no new rewrite.
	must(t, e.Regen(p.ID, RegenFrame, s1, ""))
	waitIdle(t, e)
	p, _ = e.Get(p.ID)
	if n := len(env.gl.requests("image")); n != 3 || rw.count() != 1 || p.Scenes[0].Frame.Rewrite == nil {
		t.Fatalf("reuse: image requests %d, rewrites %d", n, rw.count())
	}
	// Reverted: the studio's own prompt again (the frame is marked outdated).
	must(t, e.RevertRewrite(p.ID, RegenFrame, s1))
	p, _ = e.Get(p.ID)
	if p.Scenes[0].Frame.Rewrite != nil || !p.Scenes[0].Frame.Stale {
		t.Fatalf("revert: %+v", p.Scenes[0].Frame)
	}
	if err := e.RevertRewrite(p.ID, RegenFrame, s1); err == nil {
		t.Fatal("a second revert must say there is nothing to revert")
	}

	// ---- clip: the plain prompt is rewritten
	must(t, e.Regen(p.ID, RegenVideo, s1, ""))
	p = waitFor(t, e, p.ID, "clip", func(p Project) bool {
		v := p.Scenes[0].Video
		return v.Status == StatusDone || v.Status == StatusError
	})
	v := p.Scenes[0].Video
	if v.Status != StatusDone || v.Rewrite == nil || !strings.Contains(v.Rewrite.Prompt, "a fictional pop singer waves") {
		t.Fatalf("clip %+v rewrite %+v", v, v.Rewrite)
	}
	if n := len(env.gl.requests("video")); n != 2 {
		t.Fatalf("video requests %d, want 2", n)
	}

	// ---- refused for good: two rewrites, then the exact error
	env.gl.mu.Lock()
	env.gl.failOn = "FAILME"
	env.gl.mu.Unlock()
	before, calls := len(env.gl.requests("image")), rw.count()
	must(t, e.Regen(p.ID, RegenFrame, s2, ""))
	p = waitFor(t, e, p.ID, "frame error", func(p Project) bool { return p.Scenes[1].Frame.Status == StatusError })
	if got := len(env.gl.requests("image")) - before; got != 3 || rw.count()-calls != 2 {
		t.Fatalf("give up: %d requests, %d rewrites", got, rw.count()-calls)
	}
	if msg := p.Scenes[1].Frame.Error; !strings.Contains(msg, "G-Labs [400]: prompt rejected") || !strings.Contains(msg, "Промт автоматично переписано 2 раз") {
		t.Fatalf("frame error %q", msg)
	}
}

// ---- 3×3 storyboards: the layout is checked -----------------------------------------------

// gridPNG draws a w×h sheet of rows×cols equal coloured panels with white
// gutters of gutter px.
func gridPNG(t *testing.T, w, h, rows, cols, gutter int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	pw, ph := (w-(cols-1)*gutter)/cols, (h-(rows-1)*gutter)/rows
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			col := sheetColors[(r*cols+c)%len(sheetColors)]
			x0, y0 := c*(pw+gutter), r*(ph+gutter)
			for y := y0; y < y0+ph; y++ {
				for x := x0; x < x0+pw; x++ {
					img.SetRGBA(x, y, col)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestStoryboardLayoutChecked: a 9:16 sheet is requested as a 9:16 grid of
// nine 9:16 panels; a clean 3×3 gives a 9:16 panel-1 frame; a sheet the
// model drew as 2 columns × 5 rows is drawn once more with the stricter
// prompt, and when that one is off too the scene gets a single frame (no
// crop through the wrong grid) and the board says why.
func TestStoryboardLayoutChecked(t *testing.T) {
	needMedia(t)
	t.Parallel()

	t.Run("clean 9:16", func(t *testing.T) {
		t.Parallel()
		env, gl := newSBEnv(t, nil)
		gl.sheet = gridPNG(t, 540, 960, 3, 3, 6)
		p := castProject(t, env.e, func(in *Input) { in.StoryboardMode, in.Aspect = StoryboardGrid, "9:16" }, []int{0})
		must(t, env.e.Run(p.ID, StageFrames, false, ""))
		p = waitJob(t, env.e, p.ID)
		mustJobDone(t, p)
		boards := gl.requests("image")
		if len(boards) != 1 || boards[0].aspect != "9:16" ||
			!strings.Contains(boards[0].prompt, "exactly 3 columns and 3 rows of nine equal-sized vertical 9:16 panels") ||
			!strings.Contains(boards[0].prompt, "on a vertical 9:16 canvas") {
			t.Fatalf("board requests %d: %+v", len(boards), boards)
		}
		s := p.Scenes[0]
		if s.Board.Status != StatusDone || s.Frame.Status != StatusDone {
			t.Fatalf("board %+v frame %+v", s.Board, s.Frame)
		}
		data, err := os.ReadFile(env.e.Abs(p.ID, s.Frame.Path))
		must(t, err)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		must(t, err)
		if r := float64(cfg.Width) / float64(cfg.Height); r < 0.55 || r > 0.575 || cfg.Width < 160 {
			t.Fatalf("panel 1 is %dx%d (aspect %.3f), want a 9:16 crop", cfg.Width, cfg.Height, r)
		}
		if c := avgRGB(t, data); !nearRGB(c, 220, 40, 40, 12) {
			t.Fatalf("frame is not panel 1: %v", c)
		}
	})

	t.Run("2x5 then clean", func(t *testing.T) {
		t.Parallel()
		env, gl := newSBEnv(t, nil)
		gl.sheets = [][]byte{gridPNG(t, 540, 960, 5, 2, 6), gridPNG(t, 540, 960, 3, 3, 6)}
		p := castProject(t, env.e, func(in *Input) { in.StoryboardMode, in.Aspect = StoryboardGrid, "9:16" }, []int{0})
		must(t, env.e.Run(p.ID, StageFrames, false, ""))
		p = waitJob(t, env.e, p.ID)
		mustJobDone(t, p)
		boards := gl.requests("image")
		if len(boards) != 2 || strings.Contains(boards[0].prompt, "LAYOUT IS MANDATORY") || !strings.Contains(boards[1].prompt, "LAYOUT IS MANDATORY") {
			t.Fatalf("board requests %d", len(boards))
		}
		if s := p.Scenes[0]; s.Board.Status != StatusDone || s.Frame.Status != StatusDone {
			t.Fatalf("board %+v", s.Board)
		}
	})

	t.Run("2x5 twice", func(t *testing.T) {
		t.Parallel()
		env, gl := newSBEnv(t, nil)
		gl.sheet = gridPNG(t, 540, 960, 5, 2, 6)
		p := castProject(t, env.e, func(in *Input) { in.StoryboardMode, in.Aspect = StoryboardGrid, "9:16" }, []int{0})
		must(t, env.e.Run(p.ID, StageFrames, false, ""))
		p = waitJob(t, env.e, p.ID)
		mustJobDone(t, p)
		imgs := gl.requests("image")
		if len(imgs) != 3 || strings.Contains(imgs[2].prompt, "3×3") {
			t.Fatalf("requests %d (2 sheets + 1 single frame)", len(imgs))
		}
		s := p.Scenes[0]
		if s.Frame.Status != StatusDone || s.Board.Path != "" || s.Board.Status != StatusError ||
			!strings.Contains(s.Board.Error, "не сіткою 3×3 (сітка 2×5 замість 3×3)") {
			t.Fatalf("board %+v frame %+v", s.Board, s.Frame)
		}
	})
}

// ---- production modes («Режим виробництва») -------------------------------------------------

func TestPipelineModes(t *testing.T) {
	for _, v := range []string{PipelineFull, PipelineNoRefs, PipelineStills, PipelineNoRefsStills} {
		in := Input{Pipeline: v}
		if !ValidPipeline(v) || PipelineLabel(v) == "" {
			t.Fatalf("pipeline %q", v)
		}
		wantRefs := v == PipelineNoRefs || v == PipelineNoRefsStills
		wantStills := v == PipelineStills || v == PipelineNoRefsStills
		if in.SkipRefs() != wantRefs || in.StillsOnly() != wantStills {
			t.Fatalf("pipeline %q: skipRefs %v stills %v", v, in.SkipRefs(), in.StillsOnly())
		}
	}
	if ValidPipeline("nope") {
		t.Fatal("unknown pipeline accepted")
	}
	effects := []struct{ motion, want string }{
		{"Slow push-in on her face", "zoomin"},
		{"The camera pulls back to reveal the city", "zoomout"},
		{"Pan left across the market", "panleft"},
		{"Handheld, shaky chase", "shake"},
		{"Tilt up to the sky", "panup"},
		{"Gentle parallax drift over the hills", "drift"},
	}
	for _, tc := range effects {
		if got := AutoStillEffect(&Scene{MotionPrompt: tc.motion}, 0); got != tc.want {
			t.Errorf("AutoStillEffect(%q) = %q, want %q", tc.motion, got, tc.want)
		}
	}
	if a, b := AutoStillEffect(&Scene{}, 0), AutoStillEffect(&Scene{}, 1); a == b {
		t.Fatalf("neighbouring scenes without a camera move both get %q", a)
	}
}

// TestNoRefsPrompts: without references the frame and board prompts carry
// the looks in words (and keep the reference line for a picture the user
// gave).
func TestNoRefsPrompts(t *testing.T) {
	p := &Project{StyleGuide: "Watercolor.", Input: Input{Pipeline: PipelineNoRefs}}
	p.Characters = []Character{
		{ID: "a", Name: "Тарас", Tag: "taras", Description: "A 7-year-old boy in a red jacket."},
		{ID: "b", Name: "Лисиця", Tag: "lysytsia", Description: "A small orange fox.", Image: Asset{Path: "images/fox.jpg", Status: StatusDone}},
	}
	p.Locations = []Location{{ID: "l", Name: "Ліс", Tag: "lis", Description: "Autumn forest with golden leaves."}}
	s := &Scene{ImagePrompt: "@taras meets @lysytsia in @lis", CharacterIDs: []string{"a", "b"}, LocationID: "l"}
	for name, prompt := range map[string]string{"frame": framePrompt(p, s), "board": boardPrompt(p, s, 8)} {
		if !strings.Contains(prompt, "@taras is Тарас: A 7-year-old boy in a red jacket.") ||
			!strings.Contains(prompt, "@lysytsia is Лисиця — identical face") ||
			!strings.Contains(prompt, "Location: Ліс — Autumn forest with golden leaves.") {
			t.Fatalf("%s prompt:\n%s", name, prompt)
		}
	}
	p.Input.Pipeline = PipelineFull
	if prompt := framePrompt(p, s); !strings.Contains(prompt, "@taras is Тарас — identical face") || strings.Contains(prompt, "red jacket") {
		t.Fatalf("full pipeline prompt:\n%s", prompt)
	}
}

// TestStillsPipeline: «Без референсів + тільки картинки» from story to film:
// no reference images, no clips, every scene its frame with a motion effect
// as long as its line; the montage has no gaps; clips cannot be shot.
func TestStillsPipeline(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	pr := quickProfile(t, e)
	pr.ID, pr.Name, pr.Pipeline = "", "Тест · картинки", PipelineNoRefsStills
	pr, err := e.SaveProfile(pr)
	must(t, err)
	if pr.Pipeline != PipelineNoRefsStills {
		t.Fatalf("profile pipeline %q", pr.Pipeline)
	}
	p, err := e.CreateProject(NewProjectOptions{Text: apStory, ProfileID: pr.ID, Autopilot: true})
	must(t, err)
	if p.Input.Pipeline != PipelineNoRefsStills {
		t.Fatalf("project pipeline %q", p.Input.Pipeline)
	}
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	for _, r := range env.gl.requests("image") {
		if strings.Contains(r.prompt, "reference image of") {
			t.Fatalf("a reference was drawn: %q", r.prompt)
		}
	}
	if n := len(env.gl.requests("video")); n != 0 {
		t.Fatalf("%d clips shot in «Тільки картинки»", n)
	}
	var frames []glabsReq
	for _, r := range env.gl.requests("image") {
		if !strings.Contains(r.prompt, "YouTube thumbnail") { // the cover
			frames = append(frames, r)
		}
	}
	if len(frames) != len(p.Scenes) || !strings.Contains(frames[0].prompt, "(no reference image — keep this look)") {
		t.Fatalf("%d frame requests: %+v", len(frames), frames)
	}
	if p.Montage.Output.Status != StatusDone || len(p.Montage.Gaps) != 0 {
		t.Fatalf("montage %+v gaps %+v (%s)", p.Montage.Output, p.Montage.Gaps, p.Job.Message)
	}
	tl := e.Timeline(p)
	if len(tl) != len(p.Scenes) {
		t.Fatalf("timeline %d items", len(tl))
	}
	for i, it := range tl {
		s := p.Scenes[i]
		if it.HasClip || it.Effect == "" || it.Effect == "none" {
			t.Fatalf("scene %d: clip %v effect %q", i+1, it.HasClip, it.Effect)
		}
		// As long as its line (breath + voice + tail + fades), never the 8 s of a still.
		if it.Duration > s.Audio.Duration+1.6 || it.Duration < s.Audio.Duration {
			t.Fatalf("scene %d lasts %.2f s for a %.2f s line", i+1, it.Duration, s.Audio.Duration)
		}
	}
	if err := e.Regen(p.ID, RegenVideo, p.Scenes[0].ID, ""); err == nil || !strings.Contains(err.Error(), "Тільки картинки") {
		t.Fatalf("regen video in stills mode: %v", err)
	}
	if c := e.PipelineCostOf(p, PipelineNoRefsStills); c.Clips != 0 || c.Refs != 0 || c.Frames != len(p.Scenes) {
		t.Fatalf("cost %+v", c)
	}
	if c := e.PipelineCostOf(p, PipelineFull); c.Clips != len(p.Scenes) || c.Refs != len(p.Characters)+len(p.Locations) || c.Minutes <= e.PipelineCostOf(p, PipelineNoRefsStills).Minutes {
		t.Fatalf("full cost %+v", c)
	}
}
