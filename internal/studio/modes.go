package studio

// Sound and content modes (Input.AudioMode, Input.Content) — helpers shared
// by the planner, production, montage, translation and the UI.
//
//	AudioVoiceover  a narrator / the characters voiced by TTS (or the user's
//	                own recording) over the clips — the classic film
//	AudioInVideo    the characters speak ON CAMERA: every scene carries
//	                Lines (speaker + words + delivery) that the clip prompt
//	                hands to the video model (Veo 3.1 / Omni Flash make
//	                synchronized speech with their own voices); no voice step,
//	                the montage keeps the clips' sound at full level, the
//	                subtitles come from the lines, the music ducks under them
//	AudioSilent     no voice at all: music and the clips' ambience
//
//	ContentCharacters  a cast of people/creatures (the default)
//	ContentNoPeople    places, objects, nature, products — nobody in frame;
//	                   the "cast" holds recurring objects (Character.Kind
//	                   "object") whose references are object shots
//	ContentMotion      motion graphics / infographics / kinetic type: motifs
//	                   and backdrops instead of a cast, on-screen text per
//	                   scene drawn by the studio (Scene.OnScreen)
//	ContentMixed       people where the story needs them, b-roll elsewhere

import (
	"math"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
)

const (
	// talkWPS is the on-camera speaking rate the dialogue budget assumes:
	// the owner's Ukrainian lines ran at ~2.0 words/s (median; the slowest
	// tenth ~1.7) — 2.5 promised clips more words than they had time for.
	// The syllable check of the clip length (dialogueNeed, storyboard.go)
	// covers the slow tail.
	talkWPS = 2.0
	// lineGap is the pause between two lines of a scene (s).
	lineGap = 0.3
	// talkLead / talkTail: the video model starts speaking a moment into the
	// clip and needs a beat after the last word.
	talkLead, talkTail = 0.5, 0.5
	// maxLines is the most lines a scene keeps.
	maxLines = 8
	// narratorID is the speaker of a narration line (an off-screen voice).
	narratorID = "narrator"
)

// lineSeconds estimates how long one line takes on camera.
func lineSeconds(text string) float64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	if lang := unspacedLanguage(text); lang != "" {
		return math.Max(0.6, spokenSeconds(text, lang)*0.95)
	}
	return math.Max(0.6, float64(countWords(text))/talkWPS)
}

// DialogueSeconds estimates how long a scene's lines take on camera, the
// pauses between them included (0 = nothing is said).
func DialogueSeconds(lines []Line) float64 {
	t, n := 0.0, 0
	for _, l := range lines {
		if s := lineSeconds(l.Text); s > 0 {
			if n > 0 {
				t += lineGap
			}
			t += s
			n++
		}
	}
	return t
}

// DialogueWords is the word budget of a scene's lines for a clip of clip
// seconds (the model needs a beat before the first and after the last word).
func DialogueWords(clip int) int {
	return max(4, int(math.Floor((float64(clip)-talkLead-talkTail-0.5)*talkWPS)))
}

// LinesText is the words of the lines, one after another.
func LinesText(lines []Line) string {
	var parts []string
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// spokenText is what a scene says aloud: its narration, else its lines.
func spokenText(s *Scene) string {
	if t := strings.TrimSpace(s.Narration); t != "" {
		return t
	}
	return LinesText(s.Lines)
}

// cleanLines drops blank lines and keeps at most maxLines.
func cleanLines(lines []Line) []Line {
	var out []Line
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		if len(out) == maxLines {
			break
		}
		out = append(out, l)
	}
	return out
}

// lineTiming places a scene's lines inside its clip (seconds from the clip
// start): one after another from talkLead, each as long as it takes to say,
// squeezed when the clip is shorter than the dialogue. It is an estimate —
// the video model times the speech itself — used for the subtitles.
func lineTiming(lines []Line, clip float64) (start, end []float64) {
	n := len(lines)
	start, end = make([]float64, n), make([]float64, n)
	need := DialogueSeconds(lines)
	if need <= 0 {
		return start, end
	}
	avail := math.Max(0.5, clip-talkLead-0.3)
	scale := 1.0
	if need > avail {
		scale = avail / need
	}
	at, first := talkLead, true
	for i, l := range lines {
		d := lineSeconds(l.Text)
		if d <= 0 {
			start[i], end[i] = at, at
			continue
		}
		if !first {
			at += lineGap * scale
		}
		first = false
		start[i], end[i] = at, at+d*scale
		at = end[i]
	}
	return start, end
}

// speakerName is a line speaker as the UI shows it.
func speakerName(p *Project, id string) string {
	if c := findCharacter(p, id); c != nil {
		return firstNonBlank(strings.TrimSpace(c.Name), "Персонаж")
	}
	return "Голос за кадром"
}

// SpeakerName is the display name of a line's speaker ("Голос за кадром" for
// the narrator or an unknown id).
func SpeakerName(p *Project, id string) string { return speakerName(p, id) }

// ---- the video model's sound -------------------------------------------------------------

// Speech is whether a video model generates sound with synchronized speech
// (what AudioInVideo needs).
type Speech int

const (
	SpeechNo    Speech = iota // silent clips: the lines would not be heard
	SpeechMaybe               // depends on the model version — try one scene first
	SpeechYes                 // Veo 3.x, Gemini Omni Flash, Sora 2
)

// VideoSpeech is the speech capability of the video model configured in c:
// G-Labs (Veo 3.1, Omni Flash) and Google Veo 3.x speak; the user's own API
// is judged by the model name (veo-3.1 / omni-flash yes, seedance, grok,
// kling, wan maybe, Runway Gen-4, Luma Ray, Hailuo no).
func VideoSpeech(c config.Config) Speech {
	switch c.Video.Provider {
	case providerGoogle:
		return modelSpeech(c.Google.VeoModel)
	case providerCustom:
		model := strings.TrimSpace(c.Custom.VideoModel)
		if model == "" {
			if p, ok := providers.FindVideoPreset(c.Custom.VideoPreset); ok {
				model = p.DefaultModel
			}
		}
		return modelSpeech(model)
	}
	return SpeechYes // G-Labs renders Veo 3.1 or Omni Flash, both with sound
}

// modelSpeech judges a video model by its name.
func modelSpeech(model string) Speech {
	m := strings.ToLower(strings.TrimSpace(model))
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(m, s) {
				return true
			}
		}
		return false
	}
	switch {
	case m == "":
		return SpeechMaybe
	case has("veo-2", "veo2", "veo_2", "veo-1", "seedance-1", "seedance-v1", "hailuo", "gen4", "gen-4", "ray-", "ray2", "ray3"):
		return SpeechNo
	case has("veo3", "veo-3", "veo_3", "omni", "sora-2", "sora2"):
		return SpeechYes
	}
	return SpeechMaybe
}

// ---- production pipeline («Режим виробництва», Input.Pipeline) ------------------------------

// Pipeline modes (Input.Pipeline).
const (
	PipelineFull         = ""              // references → frames → clips → montage
	PipelineNoRefs       = "norefs"        // no reference images: looks in the prompts' words
	PipelineStills       = "stills"        // no video: frames animated by the montage
	PipelineNoRefsStills = "norefs+stills" // both
	PipelineStock        = "stock"         // стокові відео (Pexels, Pixabay, Wikimedia, Coverr)
	// PipelineCode («Анімація кодом»): no video model — every scene is an
	// animation drawn by code (codeanim: charts, maps, text, diagrams),
	// written by the text model after the voice and timed to its words;
	// characters are pictures (sprites). See codeanim.go.
	PipelineCode       = "code"
	PipelineNoRefsCode = "norefs+code" // code animation without reference images
)

// Pipelines lists the pipeline modes in display order.
var Pipelines = []string{PipelineFull, PipelineStock, PipelineNoRefs, PipelineStills, PipelineNoRefsStills, PipelineCode, PipelineNoRefsCode}

// ValidPipeline reports a known Input.Pipeline value.
func ValidPipeline(v string) bool {
	for _, p := range Pipelines {
		if p == v {
			return true
		}
	}
	return false
}

// PipelineLabel is the Ukrainian name of a pipeline mode.
func PipelineLabel(v string) string {
	switch v {
	case PipelineStock:
		return "Стокові відео"
	case PipelineNoRefs:
		return "Без референсів"
	case PipelineStills:
		return "Тільки картинки"
	case PipelineNoRefsStills:
		return "Без референсів + тільки картинки"
	case PipelineCode:
		return "Анімація кодом"
	case PipelineNoRefsCode:
		return "Без референсів + анімація кодом"
	}
	return "Повний"
}

// SkipRefs: the film draws no character/location reference images (the
// user's own material pictures are still used — they cost nothing).
func (in Input) SkipRefs() bool {
	return in.Pipeline == PipelineStock || in.Pipeline == PipelineNoRefs || in.Pipeline == PipelineNoRefsStills || in.Pipeline == PipelineNoRefsCode
}

// CodeAnim: «Анімація кодом» — the scenes are animations drawn by code
// (codeanim.go), no video model; a scene whose animation could not be
// written falls back to its frame (Scene.Code.Fallback).
func (in Input) CodeAnim() bool {
	return in.Pipeline == PipelineCode || in.Pipeline == PipelineNoRefsCode
}

// StillsOnly: the film has no video generation — every scene is its frame
// with a motion effect, as long as its voice line.
func (in Input) StillsOnly() bool {
	return in.Pipeline == PipelineStills || in.Pipeline == PipelineNoRefsStills
}

// textLooks: an image prompt must describe this reference's look in words —
// the film draws no references and this one has no picture.
func textLooks(p *Project, img Asset) bool { return p.Input.SkipRefs() && img.Path == "" }

// stillLength is how long the picture of a «Тільки картинки» scene lasts
// before its voice line lengthens it (media.Segment.StillDur): just long
// enough for its line — the voice, else the lines said or the narration at
// speaking pace — and 4 s for a scene with nothing to say.
func stillLength(s *Scene) float64 {
	switch {
	case s.Audio.Duration > 0 && s.Audio.Path != "":
		return math.Max(1.5, s.Audio.Duration)
	case DialogueSeconds(s.Lines) > 0:
		return math.Max(2, DialogueClipSeconds(s.Lines))
	case strings.TrimSpace(s.Narration) != "":
		return math.Max(2, spokenSeconds(s.Narration, "")+clipPad)
	}
	return 4
}

// stillCycle varies the motion of scenes whose shot names no camera move.
var stillCycle = []string{"zoomin", "panright", "zoomout", "panleft", "drift", "diagonal", "pushin", "panup"}

// AutoStillEffect is the motion effect a «Тільки картинки» scene gets when
// nobody chose one: the camera move its motion prompt describes (push-in,
// pull back, pan, tilt, handheld, drift/parallax, orbit), else a varied
// cycle by its position in the film so consecutive scenes never move alike.
func AutoStillEffect(s *Scene, index int) string {
	m := strings.ToLower(s.MotionPrompt + " " + s.Shot)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(m, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("handheld", "shake", "shaky", "earthquake", "explosion", "running"):
		return "shake"
	case has("pull back", "pulls back", "pull-back", "zoom out", "zooms out", "dolly out", "reveal"):
		return "zoomout"
	case has("crash zoom", "fast push", "rapid push", "snap zoom"):
		return "pushin"
	case has("push-in", "push in", "pushes in", "dolly in", "zoom in", "zooms in", "moves closer", "approach"):
		return "zoomin"
	case has("pan left", "pans left", "track left", "tracks left", "moves left"):
		return "panleft"
	case has("pan right", "pans right", "track right", "tracks right", "moves right", "tracking"):
		return "panright"
	case has("tilt up", "tilts up", "crane up", "rises", "rising", "ascend"):
		return "panup"
	case has("tilt down", "tilts down", "crane down", "descend", "falls", "falling"):
		return "pandown"
	case has("orbit", "arc shot", "rotat", "spin"):
		return "rotate"
	case has("drift", "parallax", "float", "slow glide", "glide"):
		return "drift"
	}
	return stillCycle[((index%len(stillCycle))+len(stillCycle))%len(stillCycle)]
}

// pipelineRules are what a lighter production pipeline adds to the shot
// writer's rules: without references the looks are written out by the
// studio; with stills the motion prompt only picks the montage's camera move.
func pipelineRules(p *Project) []string {
	var out []string
	if p.Input.SkipRefs() {
		out = append(out, `- NOTE: this film draws NO reference images. The studio appends every visible character's and place's written look to `+
			`the image prompt, so keep using the @tags and never contradict those looks (outfits, colours, age).`)
	}
	if p.Input.StillsOnly() {
		out = append(out, `- NOTE: this film is animated from STILL frames (no video model). In "motionPrompt" name ONE simple camera move first `+
			`(slow push-in, pull back, pan left, pan right, tilt up, tilt down, slow drift, handheld shake) — it chooses the montage's motion; `+
			`make the still frame itself tell the moment.`)
	}
	if p.Input.CodeAnim() {
		out = append(out, codeShotRules(p)...) // codeanim.go
	}
	return out
}

// PipelineCost is how much a project's pipeline generates — what the mode
// picker and the step tracker show as its cost and time.
type PipelineCost struct {
	Refs, Frames, Clips, Voices int
	// Code is how many scenes become code-drawn animations («Анімація
	// кодом»: rendered on this computer, no video model).
	Code int
	// Minutes is a rough wall-clock estimate of the generation (references,
	// frames, clips — at the configured concurrency; voices run alongside).
	Minutes int
}

// Expected time of one generation (only for the estimate).
const (
	estImageSec = 40
	estClipSec  = 150
)

// PipelineCostOf estimates what a project generates in its pipeline mode
// (pipeline = the mode to estimate, e.g. a choice not applied yet): the
// planned cast and scenes, else the budget of a plan still to come.
func (e *Engine) PipelineCostOf(p Project, pipeline string) PipelineCost {
	in := p.Input
	in.Pipeline = pipeline
	scenes := len(p.Scenes)
	if scenes == 0 {
		scenes, _, _ = e.Budget(p)
	}
	refs := len(p.Characters) + len(p.Locations)
	if len(p.Scenes) == 0 && refs == 0 {
		refs = 3 // a typical plan: two characters and a place
	}
	cost := PipelineCost{Frames: scenes, Clips: scenes, Refs: refs}
	if in.Pipeline == PipelineStock {
		cost.Refs = 0
		cost.Frames = 0
		cost.Clips = scenes
	}
	if in.SkipRefs() {
		cost.Refs = 0 // the user's own material pictures are bound, never drawn
	}
	if in.StillsOnly() {
		cost.Clips = 0
	}
	if in.CodeAnim() { // frames only for scenes whose animation fails
		cost.Clips, cost.Frames, cost.Code = 0, 0, scenes
	}
	if in.SoundMode() == AudioVoiceover {
		cost.Voices = scenes
	}
	c := e.cfg.Get()
	waves := func(n, par int) int { return (n + max(1, par) - 1) / max(1, par) }
	sec := waves(cost.Refs, ImageConcurrency(c))*estImageSec + waves(cost.Frames, ImageConcurrency(c))*estImageSec +
		waves(cost.Clips, ClipConcurrency(c))*estClipSec
	if cost.Clips == 0 && cost.Voices > 0 {
		sec += waves(cost.Voices, VoiceConcurrency(c)) * 5
	}
	if cost.Code > 0 { // the spec (text model) + the local render, per scene
		sec += waves(cost.Code, CodeConcurrency()) * estCodeSec
	}
	cost.Minutes = max(1, (sec+59)/60)
	return cost
}

// ---- the «Дуже швидкий» tempo (Shorts pacing) ------------------------------------------------

// TempoRapid is the Shorts tempo: a new picture every 2–3 s. In a
// «Тільки картинки» film the plan is written for rapidScene-second scenes
// (no video model's shortest clip limits a still), the lines are written at
// a brisk narrator's pace and the montage joins them almost without pauses.
const TempoRapid = "rapid"

const (
	rapidScene = 3   // seconds per scene of a rapid stills film
	rapidWPS   = 2.9 // words per second of a brisk, energetic narrator
	// rapidCodeScene: a rapid «Анімація кодом» film — each scene is a whole
	// animated beat (a camera flight, a fill, a figure's reaction), cut
	// shorter it never settles; still about two picture changes per scene.
	rapidCodeScene = 6
)

// sceneSeconds is the scene length a project's own pacing sets (0 = the
// video clip decides): rapid stills.
func sceneSeconds(in Input) int {
	switch {
	case in.Tempo == TempoRapid && in.StillsOnly():
		return rapidScene
	case in.Tempo == TempoRapid && in.CodeAnim():
		return rapidCodeScene
	}
	return 0
}

// planClipFor is the scene length the plan of a project is written for.
func (e *Engine) planClipFor(in Input) int {
	if s := sceneSeconds(in); s > 0 {
		return s
	}
	return e.planClip()
}

// wordsFor is the word budget of one scene's line in a project.
func wordsFor(in Input, clip int) int {
	if in.Tempo == TempoRapid {
		return max(5, int(math.Floor(float64(clip)*rapidWPS)))
	}
	return wordsPerScene(clip)
}

// TightLines: the montage joins the voice lines almost without pauses (the
// rapid tempo — a breathless Shorts narrator).
func (in Input) TightLines() bool { return in.Tempo == TempoRapid }
