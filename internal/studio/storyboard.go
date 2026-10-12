package studio

// Storyboard mode and continuity chaining.
//
//	grid3x3 — (Input.StoryboardMode) the frames stage and a frame regeneration
//	          draw a 3×3 storyboard SHEET per scene (Scene.Board): nine equal
//	          panels in reading order showing the shot's action over the clip,
//	          with the same @tag references as a single frame. Panel 1 is
//	          cropped into Scene.Frame, so thumbnails, the step screens and the
//	          montage still-fallback keep working.
//	videos  — on G-Labs Omni Flash a scene with a board renders in "components"
//	          mode: the board, the previous shot's last frame (chaining) and the
//	          cast/location references as named ingredients. Other models and
//	          scenes without a board animate Scene.Frame as before.
//	chain   — (Input.ChainFrames, Omni Flash) clips render one after another in
//	          scene order; each finished clip's last frame is kept as
//	          images/last-<sceneId>.jpg and handed to the next scene. Other
//	          models cannot use it and keep rendering in parallel.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
	"ytcartell/internal/glabs"
	"ytcartell/internal/media"
	"ytcartell/internal/providers"
)

// StoryboardGrid is the Input.StoryboardMode value of the 3×3 storyboard mode.
const StoryboardGrid = "grid3x3"

const (
	gridRows, gridCols = 3, 3
	// Names of the Omni Flash components references (bound as @name).
	refStoryboard = "storyboard"
	refPrevious   = "previous_shot"
	refFirst      = "first_frame"
	defaultMotion = "Subtle cinematic camera push-in, natural movement."
)

// LastFrameRel is where the last frame of a scene's clip is kept for the next
// scene when chaining (relative to the project folder). The file exists only
// after a chained render.
func LastFrameRel(sceneID string) string { return "images/last-" + safeName(sceneID) + ".jpg" }

// boardRef locates Scene.Board (the asset bookkeeping works on it like on
// assetRef).
type boardRef struct{ id string }

func (r boardRef) in(p *Project) *Asset {
	if s := findScene(p, r.id); s != nil {
		return &s.Board
	}
	return nil
}

// omniFlash: the configured video model is Gemini Omni Flash — on G-Labs,
// Google (Gemini Omni through the Interactions API: up to 10 reference
// images) or through the user's own API (RoyalTechno) — the only one that
// gets storyboards and the previous shot (components mode). ChainFrames
// changes nothing for other models (Google Veo, other own APIs), so they keep
// rendering in parallel.
func omniFlash(c config.Config) bool {
	switch c.Video.Provider {
	case providerGoogle:
		return config.GoogleRendersOmni(c.Google.VeoModel)
	case providerCustom: // Gemini Omni Flash through the user's own API (RoyalTechno)
		return providers.OmniFlash(c.Custom.VideoPreset, c.Custom.VideoModel)
	}
	return c.Video.GlabsModel == glabs.OmniFlash
}

// videoSeconds is the clip length the configured video model renders (Omni
// Flash can do 10 s, Veo 4/6/8; the user's own API: the nearest length its
// preset/model allows — e.g. Kling 5/10 s).
func videoSeconds(c config.Config) int {
	if c.Video.Provider == providerCustom {
		caps := videoCaps(c)
		return caps.Snap(float64(c.Video.Length), caps.Snap(8, 8))
	}
	model := c.Video.GlabsModel
	if c.Video.Provider == providerGoogle {
		model = "" // Veo 4/6/8 s
		if config.GoogleRendersOmni(c.Google.VeoModel) {
			model = glabs.OmniFlash // Gemini Omni: 4/6/8/10 s like G-Labs Omni Flash
		}
	}
	return config.SnapVideoLength(float64(c.Video.Length), model, 8)
}

// ---- dynamic clip lengths ----------------------------------------------------------------
//
// With Settings → video → dynamic length (config.Video.Dynamic) every scene's
// clip is the shortest length the video model renders that covers the
// scene's voice line plus the montage's breath around it: 5.7 s of speech →
// a 6 s clip instead of 10 s. A scene cut to the user's own recording always
// fits its slice. The choice is stored in Scene.ClipLen (and sent as the
// request's Seconds); the plan is written for the average expected length.

// clipPad is the air the montage gives a voice line (0.25 s before, 0.35 s
// after): a clip that covers its line lasts the line plus this.
const clipPad = 0.6

// clipLengths are the clip lengths (s, ascending) the configured video model
// renders — VideoCapsFor (providers.go) is the single source.
func clipLengths(c config.Config) []int {
	if ls := VideoCapsFor(c).Lengths; len(ls) > 0 {
		return ls
	}
	return []int{videoSeconds(c)}
}

// ClipLengths are the clip lengths (s, ascending) the configured video model
// renders — the choices of a scene's ClipLen and of dynamic lengths.
func (e *Engine) ClipLengths() []int { return clipLengths(e.cfg.Get()) }

// sceneNeed is how long a scene's clip must last (0 = nothing to cover): its
// slice of the user's recording, else its voice, else the estimated speaking
// time of its narration — plus clipPad and pad (montagePad: what the
// montage's transitions add around a generated line).
func sceneNeed(s *Scene, pad float64) float64 {
	switch {
	case s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart:
		return s.VoiceEnd - s.VoiceStart + clipPad
	case s.Audio.Status == StatusDone && !s.Audio.Stale && s.Audio.Duration > 0:
		return s.Audio.Duration + clipPad + pad
	case DialogueSeconds(s.Lines) > 0: // said on camera (AudioInVideo)
		return dialogueNeed(s.Lines) + talkLead + talkTail
	case strings.TrimSpace(s.Narration) != "":
		return spokenSeconds(s.Narration, "") + clipPad + pad
	}
	return 0
}

// slowSyllables is a slow speaking rate (syllables per second) — about the
// slowest fifth of the owner's Ukrainian voice lines (median 5.1/s; words
// ran at 1.7–2.5/s, median 2.0 — the rate of the dialogue budget, talkWPS).
const slowSyllables = 4.4

// dialogueNeed is how long the lines of a scene may take on camera when the
// video model speaks them slowly: per line the longer of DialogueSeconds'
// estimate and its syllables at slowSyllables. A clip chosen by the word
// estimate alone cut long Ukrainian lines off at its end ("Радіоактивний
// пончик кусає" is 6 words but 14 syllables).
func dialogueNeed(lines []Line) float64 {
	t, n := 0.0, 0
	for _, l := range lines {
		sec := lineSeconds(l.Text)
		if sec <= 0 {
			continue
		}
		if syl := syllables(l.Text); syl > 0 {
			sec = math.Max(sec, float64(syl)/slowSyllables)
		}
		if n > 0 {
			t += lineGap
		}
		t += sec
		n++
	}
	return t
}

// DialogueClipSeconds is how long a clip must last for lines said on camera
// (AudioInVideo) — what dynamic clip lengths cover (sceneNeed): the lines
// spoken slowly (dialogueNeed) and the model's beat before and after. 0 when
// nothing is said. The screens' "too long for the clip" checks use it too.
func DialogueClipSeconds(lines []Line) float64 {
	need := dialogueNeed(lines)
	if need <= 0 {
		return 0
	}
	return need + talkLead + talkTail
}

// syllables counts the vowels of Latin and Cyrillic text (≈ syllables).
func syllables(text string) int {
	n := 0
	for _, r := range strings.ToLower(text) {
		if strings.ContainsRune("aeiouyàáâãäåæèéêëìíîïòóôõöøùúûüýÿąęőűаеєиіїоуюяыэё", r) {
			n++
		}
	}
	return n
}

// montagePad is how much longer than clipPad a clip must be to cover its
// voice line in this project's montage (media.Layout): a crossfade on either
// side of the scene takes its overlap, a long dip more breath before/after
// the line. 0 for hard cuts and short dips.
func montagePad(p *Project, sid string) float64 {
	idx := -1
	for i := range p.Scenes {
		if p.Scenes[i].ID == sid {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0
	}
	def := p.Montage.Transition
	if def == "" {
		def = media.DefaultTransition
	}
	d := media.ClampTransitionDuration(p.Montage.TransitionDuration)
	kind := func(id string) media.TransitionKind {
		if t, ok := media.TransitionByID(id); ok {
			return t.Kind
		}
		if t, ok := media.TransitionByID(def); ok {
			return t.Kind
		}
		return media.KindCut
	}
	film := kind(def) != media.KindCut // the film fades in and out
	n := len(p.Scenes)
	side := func(k media.TransitionKind, breath float64) float64 {
		switch k {
		case media.KindXFade:
			return math.Round(d*24) / 24
		case media.KindDip:
			return math.Max(0, 0.6*d/2-breath)
		}
		return 0
	}
	pad := 0.0
	switch {
	case idx > 0:
		pad += side(kind(p.Scenes[idx-1].Transition), 0.25)
	case film:
		pad += side(media.KindDip, 0.25)
	}
	switch {
	case idx < n-1:
		pad += side(kind(p.Scenes[idx].Transition), 0.35)
	case film:
		pad += side(media.KindDip, 0.35)
	}
	return pad
}

// sceneClip is the clip length of scene s: the Settings length, or — with
// dynamic lengths, and always for a slice of the user's recording — the
// shortest allowed length that covers the scene. A length already chosen
// for it (ClipLen) stays while it still covers the scene.
func sceneClip(c config.Config, s *Scene) int { return sceneClipPad(c, s, 0) }

// sceneClipPad is sceneClip with the montage's pad around the line
// (montagePad): with crossfades a clip that only covered the line plus
// clipPad froze on its last frame for the overlaps.
func sceneClipPad(c config.Config, s *Scene, pad float64) int {
	fixed := videoSeconds(c)
	upload := s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart
	if !c.Video.Dynamic && !upload {
		return fixed
	}
	need := sceneNeed(s, pad)
	if need <= 0 {
		return fixed
	}
	ls := clipLengths(c)
	if slices.Contains(ls, s.ClipLen) && float64(s.ClipLen) >= need-0.05 {
		return s.ClipLen
	}
	return providers.VideoCaps{Lengths: ls}.Cover(need-0.05, fixed)
}

// VoiceOutgrowsClip compares a scene's finished voice line with its finished
// clip the way the montage plays them: the line plus the air around it
// (clipPad) and what the project's transitions take (montagePad) — need —
// against what the clip plays (its length minus the trims) — clip. short:
// the line runs longer, so the montage would hold the clip's last frame; a
// new clip of the right length is needed. A slice of the user's own
// recording is never short (its clip is trimmed to the slice).
func VoiceOutgrowsClip(p *Project, s *Scene) (need, clip float64, short bool) {
	if p == nil || s == nil || !p.Input.Voiceover || s.AudioSource == AudioUpload ||
		s.Audio.Status != StatusDone || s.Audio.Stale || s.Audio.Duration <= 0 ||
		s.Video.Status != StatusDone || s.Video.Path == "" {
		return 0, 0, false
	}
	switch {
	case s.Video.Duration > 0:
		clip = s.Video.Duration - math.Max(0, s.TrimIn) - math.Max(0, s.TrimOut)
	case s.ClipLen > 0:
		clip = float64(s.ClipLen)
	default:
		return 0, 0, false // the clip's length is not known
	}
	need = s.Audio.Duration + clipPad + montagePad(p, s.ID)
	return need, clip, need-0.05 > clip
}

// staleShortClip flags the finished clip of scene sid as outdated when its
// new voice line outgrows it (VoiceOutgrowsClip): the scene card asks to
// shoot it again («озвучка довша за кліп — перезняти?»). Runs inside the
// voice's commit, after the new Duration is stored.
func staleShortClip(p *Project, sid string) {
	if s := findScene(p, sid); s != nil {
		if _, _, short := VoiceOutgrowsClip(p, s); short {
			s.Video.Stale = true
		}
	}
}

// planClip is the scene length the plan is written for: the Settings length,
// or — with dynamic lengths — the average of the lengths the model renders
// up to it (each clip then follows its own voice line).
func planClip(c config.Config, settings int) int {
	if !c.Video.Dynamic {
		return settings
	}
	ls := clipLengths(c)
	sum, n := 0, 0
	for _, l := range ls {
		if l <= max(settings, ls[0]) {
			sum, n = sum+l, n+1
		}
	}
	if n == 0 {
		return settings
	}
	return int(math.Round(float64(sum) / float64(n)))
}

// clipTrim is the TrimOut that ends a clip together with its scene's slice
// of the user's recording (plus the breath), so the voice-over runs on
// without gaps; 0 when the clip is not longer than that.
func clipTrim(clip, trimIn, slice float64) float64 {
	return math.Max(0, math.Round((clip-trimIn-slice-clipPad)*100)/100)
}

// ---- storyboard sheet --------------------------------------------------------------------

func boardPrompt(p *Project, s *Scene, seconds int) string {
	return boardPromptFor(p, s, seconds, false)
}

// boardPromptFor is the storyboard sheet prompt. The layout comes FIRST and
// in numbers — "ONE image … a clean 3×3 grid … exactly 3 columns and 3 rows
// of nine equal-sized 9:16 panels" (how Nano Banana follows a grid best) —
// and the sheet has the project's own aspect (the request's aspect param),
// so each equal panel is itself a full frame of that format. strict is the
// one retry after a sheet that did not follow the grid (DetectGrid): the
// layout is repeated as a hard rule at the start and the end.
func boardPromptFor(p *Project, s *Scene, seconds int, strict bool) string {
	shot := firstNonBlank(s.ImagePrompt, s.Title)
	aspect, orient := "16:9", "horizontal"
	if p.Input.Aspect == "9:16" {
		aspect, orient = "9:16", "vertical"
	}
	var lines []string
	if strict {
		lines = append(lines, "IMPORTANT — LAYOUT IS MANDATORY: the previous sheet did not follow the grid. Draw EXACTLY 3 columns and EXACTLY 3 rows; "+
			"never 2 or 4 columns, never 4 or 5 rows, no merged, split, overlapping or differently sized panels.")
	}
	lines = append(lines,
		fmt.Sprintf("Create ONE image: a clean 3×3 storyboard sheet on a %s %s canvas — exactly 3 columns and 3 rows of nine equal-sized %s %s panels "+
			"(every panel the same width and the same height, aligned in a perfect grid).", orient, aspect, orient, aspect),
		"The panels are separated by thin, straight, pure white gutters of equal width (about 1% of the sheet) with a thin white outer margin; nothing else on the sheet.",
		fmt.Sprintf("The nine panels show ONE continuous shot of a film, read left to right, top to bottom, the action progressing moment by moment over its %d seconds "+
			"— panel 1 (top left) is the opening frame, panel 9 (bottom right) the final frame.", seconds),
		"Shot: "+shot,
	)
	if m := strings.TrimSpace(s.MotionPrompt); m != "" {
		lines = append(lines, "Action and camera: "+m)
	}
	lines = append(lines, castLinesFor(p, sceneCast(p, s), " in every panel")...)
	if l := findLocation(p, s.LocationID); l != nil {
		if textLooks(p, l.Image) {
			lines = append(lines, fmt.Sprintf("Location: %s — %s; the same place in every panel.", l.Name, strings.TrimSpace(l.Description)))
		} else {
			lines = append(lines, fmt.Sprintf("Location: @%s (%s) — the same place in every panel, matching its reference image.", l.Tag, l.Name))
		}
	}
	lines = append(lines, contentLine(p, s))
	lines = append(lines,
		fmt.Sprintf("Every panel is a full %s frame of the same take: same camera setup, characters, outfits, place, lighting and colour palette; "+
			"consecutive panels differ only by the natural progress of the motion.", aspect),
		"Art style: "+p.StyleGuide,
	)
	if strict {
		lines = append(lines, fmt.Sprintf("Layout check before finishing: 3 columns × 3 rows = 9 equal %s panels, straight white gutters.", aspect))
	}
	lines = append(lines, "No text, no numbers, no panel labels, no captions, no speech bubbles, no watermark.")
	out := lines[:0]
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// genBoard draws the scene's 3×3 storyboard sheet and derives the frame from
// its first panel. Board and frame share one bookkeeping: both run, fail and
// finish together; the new frame invalidates the old clip (like genFrame).
//
// The sheet is never trusted blindly: DetectGrid finds the layout the model
// really drew. A sheet that is not a clean 3×3 of equal panels is drawn once
// more with the stricter prompt; when that one is off too, the scene gets a
// plain single frame instead (no crop through a wrong grid) and the board
// says why. A sheet refused for its content is rewritten once (rewrite.go),
// then the single frame is drawn (with its own rewrites).
func (e *Engine) genBoard(ctx context.Context, st *proj, sid string) bool {
	c := e.cfg.Get()
	var refs []namedRef
	var req, single gen.ImageRequest
	var strict string
	var prev *PromptRewrite
	aspect := "16:9"
	if !e.read(st, func(p *Project) bool {
		s := findScene(p, sid)
		if s == nil {
			return false
		}
		refs = frameRefs(p, s)
		single = gen.ImageRequest{Prompt: framePrompt(p, s), UIModel: p.Input.ImageModel, Aspect: p.Input.Aspect}
		// The sheet spans the clip this scene will get (its voice, when there is one yet).
		secs := sceneClipPad(c, s, montagePad(p, sid))
		req = gen.ImageRequest{Prompt: boardPrompt(p, s, secs), UIModel: p.Input.ImageModel, Aspect: p.Input.Aspect}
		strict = boardPromptFor(p, s, secs, true)
		prev, aspect = s.Board.Rewrite, p.Input.Aspect
		return true
	}) {
		return false
	}
	frame, board := assetRef{slotFrame, sid}, boardRef{sid}
	if !e.begin(st, frame, board) {
		return false
	}
	boardRel, frameRel, rw, err := e.drawBoard(ctx, st, sid, req, refs, aspect, prev)
	var bad *gridError
	if errors.As(err, &bad) && ctx.Err() == nil {
		log.Printf("[studio:%s] the storyboard of %s is not a clean 3×3 (%v); drawing it once more with the stricter prompt", st.id, sid, err)
		req.Prompt = strict
		boardRel, frameRel, rw, err = e.drawBoard(ctx, st, sid, req, refs, aspect, nil)
		if errors.As(err, &bad) && ctx.Err() == nil {
			log.Printf("[studio:%s] the storyboard of %s is still not a clean 3×3 (%v); drawing the single frame", st.id, sid, err)
			return e.boardFallback(ctx, st, sid, single, refs, err,
				"Модель двічі намалювала розкадровку не сіткою 3×3 ("+err.Error()+") — намальовано один кадр, кліп зніметься з нього.")
		}
	}
	if err != nil && contentRefusal(err) && ctx.Err() == nil {
		// The sheet was refused for its content: one try of the single
		// frame (the scene then animates its frame; the board shows why).
		log.Printf("[studio:%s] the 3×3 storyboard of %s was refused (%v); drawing the single frame", st.id, sid, err)
		return e.boardFallback(ctx, st, sid, single, refs, err,
			"Розкадровку 3×3 відхилено — намальовано один кадр, кліп зніметься з нього. "+e.failureText(err))
	}
	if err != nil {
		e.fail(st, frame, err, board)
		return false
	}
	var drop []string
	ok := false
	e.update(st, func(p *Project) {
		s := findScene(p, sid)
		if s == nil { // deleted meanwhile
			drop = append(drop, boardRel, frameRel)
			return
		}
		now := nowMs()
		for _, it := range []struct {
			a   *Asset
			rel string
		}{{&s.Board, boardRel}, {&s.Frame, frameRel}} {
			if it.a.Path != "" && it.a.Path != it.rel {
				drop = append(drop, it.a.Path)
			}
			it.a.Path, it.a.Status, it.a.Error, it.a.Stale, it.a.Progress = it.rel, StatusDone, "", false, 100
			it.a.Rewrite = rw
			it.a.UpdatedAt = now
		}
		if s.Video.Status == StatusDone { // a new frame invalidates the old clip
			drop = append(drop, s.Video.Path)
			s.Video = EmptyAsset()
		}
		ok = true
	})
	e.discardFiles(st, drop)
	return ok
}

// boardFallback draws the single frame of a scene whose storyboard sheet
// failed (refused for its content, or not a clean 3×3 twice — refused is
// that error, note what the board then says): the frame is done, the board
// keeps the note (error, no file) so the step shows that this scene has no
// sheet and why. Both errors stay when the frame fails too.
func (e *Engine) boardFallback(ctx context.Context, st *proj, sid string, req gen.ImageRequest, refs []namedRef, refused error, note string) bool {
	frame, board := assetRef{slotFrame, sid}, boardRef{sid}
	imgs, err := readRefs(st.dir, refs)
	var data []byte
	var rw *PromptRewrite
	mime := ""
	if err == nil {
		req.Refs = imgs
		data, mime, rw, err = e.imageRewriting(ctx, st, req, nil, maxRewrites)
	}
	var rel string
	if err == nil {
		rel, err = e.writeAsset(st, "images", fmt.Sprintf("frame-%s-%s.%s", safeName(sid), newID(12), imageExt(mime)), data)
	}
	if err != nil {
		e.fail(st, frame, &boardError{board: refused, frame: err}, board)
		return false
	}
	var drop []string
	ok := false
	e.update(st, func(p *Project) {
		s := findScene(p, sid)
		if s == nil {
			drop = append(drop, rel)
			return
		}
		if s.Frame.Path != "" && s.Frame.Path != rel {
			drop = append(drop, s.Frame.Path)
		}
		s.Frame.Path, s.Frame.Status, s.Frame.Error, s.Frame.Stale, s.Frame.Progress = rel, StatusDone, "", false, 100
		s.Frame.Rewrite = rw
		s.Frame.UpdatedAt = nowMs()
		if s.Board.Path != "" { // drawn for the old frame
			drop = append(drop, s.Board.Path)
		}
		s.Board = Asset{Status: StatusError, Error: clipRunes(note, maxAssetError), UpdatedAt: nowMs()}
		if s.Video.Status == StatusDone { // a new frame invalidates the old clip
			drop = append(drop, s.Video.Path)
			s.Video = EmptyAsset()
		}
		ok = true
	})
	e.discardFiles(st, drop)
	return ok
}

// boardError: the storyboard sheet and then the single frame failed. It
// reads as the frame's error with the sheet's after it and unwraps to the
// frame's (glabs.Rejected keeps working on it).
type boardError struct{ board, frame error }

func (e *boardError) Error() string {
	return e.frame.Error() + "\n(Спершу розкадровка 3×3: " + e.board.Error() + "; потім один кадр — теж не вийшло.)"
}

func (e *boardError) Unwrap() error { return e.frame }

// gridError: the sheet the model drew is not a clean 3×3 of equal panels.
type gridError struct {
	rows, cols int
	even       bool
}

func (e *gridError) Error() string {
	switch {
	case e.rows*e.cols <= 1:
		return "на аркуші не видно сітки панелей"
	case e.rows == gridRows && e.cols == gridCols:
		return "панелі різного розміру"
	case !e.even:
		return fmt.Sprintf("сітка %d×%d з панелями різного розміру замість 3×3", e.cols, e.rows)
	}
	return fmt.Sprintf("сітка %d×%d замість 3×3", e.cols, e.rows)
}

// drawBoard generates the sheet (stored as JPEG), checks its layout
// (DetectGrid: a *gridError when it is not a clean 3×3) and crops panel 1,
// trimmed to the project's aspect (never stretched), into a frame file;
// returns both relative paths and the prompt rewrite that went through (one
// rewrite at most: a refused sheet falls back to the single frame).
func (e *Engine) drawBoard(ctx context.Context, st *proj, sid string, req gen.ImageRequest, refs []namedRef, aspect string, prev *PromptRewrite) (boardRel, frameRel string, rw *PromptRewrite, err error) {
	if req.Refs, err = readRefs(st.dir, refs); err != nil {
		return "", "", nil, err
	}
	data, _, rw, err := e.imageRewriting(ctx, st, req, prev, 1)
	if err != nil {
		return "", "", nil, err
	}
	img, format, err := media.DecodeImage(data)
	if err != nil {
		return "", "", nil, fmt.Errorf("розкадровка: %w", err)
	}
	g := media.DetectGrid(img)
	if !g.Clean(gridRows, gridCols) {
		return "", "", nil, &gridError{rows: g.Rows, cols: g.Cols, even: g.Even}
	}
	sheet := data
	if format != "jpeg" {
		if sheet, err = media.EncodeJPEG(img); err != nil {
			return "", "", nil, err
		}
	}
	aw, ah := media.AspectOf(aspect)
	first, err := media.EncodeJPEG(media.SubImage(img, media.FitAspect(g.Rects[0], aw, ah)))
	if err != nil {
		return "", "", nil, err
	}
	if boardRel, err = e.writeAsset(st, "images", fmt.Sprintf("board-%s-%d.jpg", safeName(sid), nowMs()), sheet); err != nil {
		return "", "", nil, err
	}
	if frameRel, err = e.writeAsset(st, "images", fmt.Sprintf("frame-%s-%s.jpg", safeName(sid), newID(12)), first); err != nil {
		e.removeFiles(st, []string{boardRel})
		return "", "", nil, err
	}
	return boardRel, frameRel, rw, nil
}

// ---- clips ---------------------------------------------------------------------------------

// shotInputs is what a clip needs, copied under the project lock.
type shotInputs struct {
	scene  Scene
	prev   *Scene // previous scene in the film, nil for the first
	cast   []Character
	loc    *Location
	style  string
	aspect string
	chain  bool
	// pad is the montage's room around the voice line (montagePad).
	pad float64
	// The project's modes (modes.go): the sound and content mode and the
	// scene's spoken lines as the video model gets them ("" = none).
	sound, content, talk string
	// Filled by videoRequest for saferRequest: the plain request animating
	// the frame, and the components references actually sent.
	plain gen.VideoRequest
	sent  []videoRef
}

func shotInputsOf(p *Project, sid string) (shotInputs, bool) {
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if s.ID != sid {
			continue
		}
		in := shotInputs{scene: *s, style: p.StyleGuide, aspect: p.Input.Aspect, chain: p.Input.ChainFrames,
			sound: p.Input.SoundMode(), content: p.Input.ContentMode(), pad: montagePad(p, sid)}
		if in.sound == AudioInVideo {
			in.talk = dialogueBlock(p, s)
		}
		if i > 0 {
			prev := p.Scenes[i-1]
			in.prev = &prev
		}
		for _, c := range sceneCast(p, s) {
			in.cast = append(in.cast, *c)
		}
		if l := findLocation(p, s.LocationID); l != nil {
			loc := *l
			in.loc = &loc
		}
		return in, true
	}
	return shotInputs{}, false
}

// Reference roles in a components request.
const (
	roleCast     = "cast"
	roleLocation = "location"
)

// videoRef is one components reference with what the prompt says about it.
type videoRef struct {
	img  gen.RefImage
	role string // refStoryboard | refPrevious | refFirst | roleCast | roleLocation
	tag  string // cast/location @tag
	name string // cast/location display name
	obj  bool   // the cast entry is an object (Character.Kind)
}

// videoRequest builds the clip request. Default: animate the scene frame
// (start_image). On Omni Flash (G-Labs or RoyalTechno), components mode when there is
// something to add — the scene's storyboard (lead "storyboard") and/or, with
// ChainFrames, the previous clip's last frame ("previous_shot"; without a
// board the frame leads as "first_frame") — followed by the cast and location
// references, in that priority, capped at the model's limit.
func (e *Engine) videoRequest(ctx context.Context, st *proj, c config.Config, in *shotInputs) (gen.VideoRequest, error) {
	s := &in.scene
	start, err := readImage(st.dir, s.Frame.Path, "")
	if err != nil {
		return gen.VideoRequest{}, fmt.Errorf("не вдалося прочитати кадр сцени: %w", err)
	}
	req := gen.VideoRequest{Prompt: clipPrompt(in), Start: &start, Aspect: in.aspect, Seconds: sceneClipPad(c, s, in.pad), Resolution: c.Video.Resolution}
	if c.Video.Provider != providerGoogle && c.Video.Provider != providerCustom { // G-Labs: its model id (the others choose their own)
		req.Model = c.Video.GlabsModel
	}
	in.plain, in.sent = req, nil
	if !omniFlash(c) {
		return req, nil
	}

	var lead, prev *videoRef
	if s.Board.Path != "" {
		if img, err := readImage(st.dir, s.Board.Path, refStoryboard); err == nil {
			lead = &videoRef{img: img, role: refStoryboard}
		} else {
			log.Printf("[studio:%s] storyboard of %s unreadable, animating the frame: %v", st.id, s.ID, err)
		}
	}
	if in.chain && in.prev != nil && in.prev.Video.Status == StatusDone && in.prev.Video.Path != "" && sameSetting(in.prev, s) {
		img, err := e.previousShot(ctx, st, in.prev)
		switch {
		case err == nil:
			prev = &videoRef{img: img, role: refPrevious}
		case ctx.Err() != nil:
			return gen.VideoRequest{}, ctx.Err()
		default:
			log.Printf("[studio:%s] no last frame of scene %s for chaining: %v", st.id, in.prev.ID, err)
		}
	}
	if lead == nil {
		if prev == nil {
			return req, nil // nothing to add: plain start_image
		}
		first := start
		first.Name = refFirst + "." + imageExt(start.MIME)
		lead = &videoRef{img: first, role: refFirst}
	}

	refs := []videoRef{*lead}
	if prev != nil {
		refs = append(refs, *prev)
	}
	for _, ch := range in.cast {
		if ch.Image.Path == "" {
			continue
		}
		img, err := readImage(st.dir, ch.Image.Path, ch.Tag)
		if err != nil {
			log.Printf("[studio:%s] reference @%s skipped: %v", st.id, ch.Tag, err)
			continue
		}
		refs = append(refs, videoRef{img: img, role: roleCast, tag: ch.Tag, name: ch.Name, obj: ch.IsObject()})
	}
	if l := in.loc; l != nil && l.Image.Path != "" {
		if img, err := readImage(st.dir, l.Image.Path, l.Tag); err == nil {
			refs = append(refs, videoRef{img: img, role: roleLocation, tag: l.Tag, name: l.Name})
		} else {
			log.Printf("[studio:%s] reference @%s skipped: %v", st.id, l.Tag, err)
		}
	}
	if limit := videoCaps(c).MaxRefs; limit > 0 && len(refs) > limit {
		refs = refs[:limit]
	}
	return componentsRequest(in, req, bindSafe(refs)), nil
}

// componentsRequest is base in components mode with refs (already ordered
// by bindSafe); in.sent remembers them.
func componentsRequest(in *shotInputs, base gen.VideoRequest, refs []videoRef) gen.VideoRequest {
	req := base
	req.Mode = gen.ModeComponents
	req.Refs = make([]gen.RefImage, len(refs))
	for i, r := range refs {
		req.Refs[i] = r.img
	}
	req.Prompt = componentsPrompt(in, refs)
	in.sent = refs
	return req
}

// sameSetting: the previous scene plays in the same place, so the clip can
// continue its last frame. At a cut to another location the previous shot
// only contradicts the storyboard and the location reference ("continue
// seamlessly — same characters, positions" from another room with other
// people); on the owner's G-Labs every INVALID_ARGUMENT of Omni Flash came
// from such requests, so the previous shot is not sent across a location
// change.
func sameSetting(prev, s *Scene) bool { return prev.LocationID == s.LocationID }

// saferRequest is the ONE automatic retry of an Omni Flash components clip
// that the service refused for its content: the frame animated alone with
// its plain motion prompt (start_image — what every other model gets),
// without the storyboard, the previous shot, the references and the shot
// description. On the owner's G-Labs (Sept 29 – Oct 2) every refused video
// was a components request with the 3×3 storyboard (12× INVALID_ARGUMENT,
// 8× REPUTATIONAL, 5× PROMINENT_PERSON, 2× DANGEROUS of 157), while 30 of
// 31 frame-only clips went through; the previous shot made no difference.
// ok is false for a request that is not components.
func saferRequest(in *shotInputs, req gen.VideoRequest) (safer gen.VideoRequest, what string, ok bool) {
	if req.Mode != gen.ModeComponents || in.plain.Start == nil {
		return req, "", false
	}
	return in.plain, "animating the frame alone", true
}

// renderClip renders a clip request; an Omni Flash components request that
// G-Labs refused for its content (INVALID_ARGUMENT or a Google safety
// filter) is tried once more in the safer form (saferRequest). When that
// fails too, both exact errors are kept (saferError).
func (e *Engine) renderClip(ctx context.Context, st *proj, in *shotInputs, req gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	data, err := e.video(ctx, req, progress)
	if err == nil || glabs.Rejection(err) == "" || ctx.Err() != nil {
		return data, err
	}
	safer, what, ok := saferRequest(in, req)
	if !ok {
		return nil, err
	}
	log.Printf("[studio:%s] G-Labs refused the components clip of %s (%v); one more try %s", st.id, in.scene.ID, err, what)
	data, err2 := e.video(ctx, safer, progress)
	if err2 != nil {
		return nil, &saferError{first: err, last: err2}
	}
	return data, nil
}

// saferError: the components clip and its safer retry (the frame alone) both
// failed. It reads as the last error with the first one after it and
// unwraps to the last (glabs.Rejection, cancellation).
type saferError struct{ first, last error }

func (e *saferError) Error() string {
	return e.last.Error() + "\n(Спершу з розкадровкою й референсами: " + e.first.Error() + "; потім лише кадр — теж не вийшло.)"
}

func (e *saferError) Unwrap() error { return e.last }

// componentsPrompt is the Omni Flash components prompt: the motion, the shot,
// who/where the references are and how to use the storyboard / previous shot.
// It only names references that are actually sent.
func componentsPrompt(in *shotInputs, refs []videoRef) string {
	s := &in.scene
	var cast, things []string
	loc := ""
	has := map[string]bool{}
	for _, r := range refs {
		switch r.role {
		case roleCast:
			if r.obj {
				things = append(things, fmt.Sprintf("@%s is %s — identical shape, materials and colours to the @%s reference image", r.tag, r.name, r.tag))
				continue
			}
			cast = append(cast, fmt.Sprintf("@%s is %s — identical face, hair, body and outfit to the @%s reference image", r.tag, r.name, r.tag))
		case roleLocation:
			loc = fmt.Sprintf("Location: @%s (%s) — match the reference image of this place.", r.tag, r.name)
		default:
			has[r.role] = true
		}
	}
	var lines []string
	if in.talk != "" {
		lines = append(lines, in.talk) // dialogue first: the fewest burnt-in subtitles
	}
	lines = append(lines, firstNonBlank(strings.TrimSpace(s.MotionPrompt), defaultMotion))
	if shot := strings.TrimSpace(firstNonBlank(s.ImagePrompt, s.Title)); shot != "" {
		lines = append(lines, "Shot: "+shot)
	}
	if len(cast) > 0 {
		lines = append(lines, "Characters: "+strings.Join(cast, "; ")+".")
	}
	if len(things) > 0 {
		lines = append(lines, "Objects: "+strings.Join(things, "; ")+".")
	}
	if loc != "" {
		lines = append(lines, loc)
	}
	if has[refStoryboard] {
		lines = append(lines, "The reference image @storyboard is a 3×3 storyboard of THIS shot: follow its nine panels in reading order "+
			"(left to right, top to bottom) as the progression of the action over the clip. It is only a plan — never show the grid, "+
			"panel borders, gutters or any text from it; render one continuous full-frame shot.")
	}
	if has[refFirst] {
		lines = append(lines, "The reference image @first_frame is the opening frame of this shot: start from exactly this composition, characters and lighting.")
	}
	if has[refPrevious] {
		lines = append(lines, "The reference image @previous_shot is the final frame of the previous shot: continue seamlessly from it "+
			"— same characters, outfits, lighting and positions.")
	}
	if style := strings.TrimSpace(in.style); style != "" {
		lines = append(lines, "Art style: "+style)
	}
	if in.content == ContentNoPeople || in.content == ContentMotion {
		lines = append(lines, keepLine(in))
	}
	lines = append(lines, soundLine(in))
	return strings.Join(lines, "\n")
}

// refStem is the part of a ref name G-Labs matches @keywords against.
func refStem(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
}

// bindSafe orders the references so G-Labs binds every @keyword to the
// intended image. G-Labs matches "@keyword" as a case-insensitive substring of
// the file name and the FIRST image in order wins, so a ref whose name is
// contained in an earlier ref's name moves right in front of it (a character
// tagged @tor would otherwise bind to storyboard.jpg). Otherwise the priority
// order is kept.
func bindSafe(refs []videoRef) []videoRef {
	out := make([]videoRef, 0, len(refs))
	for _, r := range refs {
		at := len(out)
		if k := refStem(r.img.Name); k != "" {
			for j, o := range out {
				if ok := refStem(o.img.Name); ok != k && strings.Contains(ok, k) {
					at = j
					break
				}
			}
		}
		out = slices.Insert(out, at, r)
	}
	return out
}

// ---- chaining ------------------------------------------------------------------------------

// previousShot is the last frame of the previous scene's current clip as the
// "previous_shot" reference.
func (e *Engine) previousShot(ctx context.Context, st *proj, prev *Scene) (gen.RefImage, error) {
	rel, err := e.lastFrame(ctx, st, prev.ID, prev.Video.Path)
	if err != nil {
		return gen.RefImage{}, err
	}
	return readImage(st.dir, rel, refPrevious)
}

// lastFrame makes sure LastFrameRel(sceneID) holds the last frame of the
// scene's clip clipRel (extracted again when missing or older than the clip)
// and returns its relative path.
func (e *Engine) lastFrame(ctx context.Context, st *proj, sceneID, clipRel string) (string, error) {
	rel := LastFrameRel(sceneID)
	abs := filepath.Join(st.dir, filepath.FromSlash(rel))
	clip := filepath.Join(st.dir, filepath.FromSlash(clipRel))
	ci, err := os.Stat(clip)
	if err != nil || ci.IsDir() {
		return "", fmt.Errorf("кліп сцени не знайдено: %s", clipRel)
	}
	if li, err := os.Stat(abs); err == nil && li.Size() > 0 && !li.ModTime().Before(ci.ModTime()) {
		return rel, nil
	}
	st.fmu.RLock() // never resurrect a deleted project folder
	defer st.fmu.RUnlock()
	if st.deleted.Load() {
		return "", ErrNotFound
	}
	if err := e.ffNow().LastFrame(ctx, clip, abs); err != nil {
		return "", err
	}
	return rel, nil
}

// keepLastFrame runs after a chained clip finished: its last frame is saved
// for the next scene right away (previousShot re-extracts it when missing).
func (e *Engine) keepLastFrame(ctx context.Context, st *proj, sid string) {
	var clip string
	e.read(st, func(p *Project) bool {
		if s := findScene(p, sid); s != nil && s.Video.Status == StatusDone {
			clip = s.Video.Path
		}
		return true
	})
	if clip == "" {
		return
	}
	if _, err := e.lastFrame(ctx, st, sid, clip); err != nil && !isCancel(err) {
		log.Printf("[studio:%s] last frame of %s: %v", st.id, sid, err)
	}
}

// markNextClipStale flags the finished clip of the scene after sid as
// outdated: with chaining it continued the previous version of sid's clip.
// Runs in the commit of the new clip.
func markNextClipStale(p *Project, sid string) {
	for i := range p.Scenes {
		if p.Scenes[i].ID == sid {
			if i+1 < len(p.Scenes) {
				if n := &p.Scenes[i+1].Video; n.Status == StatusDone {
					n.Stale = true
				}
			}
			return
		}
	}
}

// setJobNote replaces the running job's message (keeps its progress).
func (e *Engine) setJobNote(j *jobCtx, msg string) {
	e.update(j.st, func(p *Project) {
		if p.Job != nil && p.Job.Status == JobRunning && !j.flag.Load() {
			p.Job.Message = msg
		}
	})
}
