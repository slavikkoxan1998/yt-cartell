package studio

// «Анімація кодом» (Input.Pipeline PipelineCode / PipelineNoRefsCode): no
// video model — every scene is an animation drawn by code (internal/codeanim,
// the embedded huashu-art-motion engine: charts, maps, kinetic type,
// diagrams, keynote cards, whiteboard …). The order is fixed by the voice:
//
//	plan      the usual bible + shots; "motionPrompt" is the animation brief,
//	          the plan may suggest a grammar per scene (Code.Suggested)
//	voice     every scene's line (TTS, multi-voice parts or the own recording)
//	spec      the text model writes the scene's codeanim spec for the chosen
//	          grammar (Input.CodeGrammar, per scene Code.Grammar; "" = auto):
//	          duration = the voice + the montage's breath around it (exact),
//	          the project's format, 30 fps, cues timed to the narration's words
//	          (the recording's transcript, multi-voice part timings, else the
//	          words spread over the voice). The JSON is checked; ONE repair
//	          round with the problems; then the scene falls back to its frame
//	          («Тільки картинки» for that scene) with the exact error kept in
//	          Code.Error — never a silent placeholder
//	pictures  what a spec shows as images (character sprites, icons, photos)
//	          is drawn by the configured image service: sprites on a flat
//	          chroma green, keyed to transparency here; the cast's reference
//	          images keep them consistent; identical prompts are drawn once
//	render    codeanim.Render → videos/code-<scene>-<ms>.mp4 = Scene.Video,
//	          1–2 at a time, cancellable, progress on the clip; the montage
//	          plays it like any clip (no pan/zoom on it)
//
// StageFrames of such a film = voices + specs + pictures (+ frames of the
// scenes that fell back); StageRender = the clips (and whatever is missing
// before them). An edited spec is kept (Code.Edited) and only re-timed when
// the voice changes; a changed spec / grammar / voice re-renders the clip.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"ytcartell/internal/gen"
	"ytcartell/internal/llm"
	"ytcartell/internal/media"
	"ytcartell/internal/stt"
)

// ---- the renderer -------------------------------------------------------------------------

// CodeGrammar is one animation grammar of the renderer (what the grammar
// picker shows and what the spec writer gets).
type CodeGrammar struct {
	ID, Label, Description string
	// Schema is what the grammar reads — spec-level data, cue kinds and
	// their fields — in words for the text model.
	Schema string
	// Example is a complete spec that renders well (the upstream example).
	Example json.RawMessage
	// Kinds are the cue kinds the grammar draws (none = not checked).
	Kinds []string
	// Sprites: the grammar places cut-out pictures (characters, icons) —
	// the spec writer is told to declare them as sprites.
	Sprites bool
	// Reference is the grammar's full spec reference (markdown) — the spec
	// writer gets it instead of the short Schema; Examples are complete
	// films that render well (their picture paths are placeholders).
	Reference string
	Examples  []json.RawMessage
}

// CodeRenderOptions are the options of a render or a still.
type CodeRenderOptions struct {
	// AssetsDir resolves the spec's relative picture paths (the project folder).
	AssetsDir string
	// Progress reports rendered frames.
	Progress func(done, total int)
	// FFmpeg is the encoder the studio located ("" = the renderer's own search).
	FFmpeg string
}

// CodeRenderer draws code animations (internal/codeanim; tests use a fake).
type CodeRenderer interface {
	Grammars() []CodeGrammar
	// Available names the browser the renderer drives, or why it cannot run.
	Available(ctx context.Context) (browser string, err error)
	Render(ctx context.Context, spec []byte, out string, opt CodeRenderOptions) error
	Still(ctx context.Context, spec []byte, at float64, outPNG string, opt CodeRenderOptions) error
}

// newCodeRenderer builds the renderer of a new engine (codeanim_engine.go
// wires internal/codeanim).
var newCodeRenderer = func() CodeRenderer { return noCodeRenderer{} }

// noCodeRenderer stands in when the studio was built without a renderer.
type noCodeRenderer struct{}

var errNoCodeRenderer = errors.New("рушій анімації кодом не вбудовано в цю збірку студії")

func (noCodeRenderer) Grammars() []CodeGrammar                   { return nil }
func (noCodeRenderer) Available(context.Context) (string, error) { return "", errNoCodeRenderer }
func (noCodeRenderer) Render(context.Context, []byte, string, CodeRenderOptions) error {
	return errNoCodeRenderer
}
func (noCodeRenderer) Still(context.Context, []byte, float64, string, CodeRenderOptions) error {
	return errNoCodeRenderer
}

// coder is the engine's renderer (SetCodeRenderer, else the built-in one).
func (e *Engine) coder() CodeRenderer {
	e.codeMu.Lock()
	defer e.codeMu.Unlock()
	if e.code == nil {
		e.code = newCodeRenderer()
	}
	return e.code
}

// SetCodeRenderer replaces the code-animation renderer (tests, headless
// screens: a fake that never starts a browser).
func (e *Engine) SetCodeRenderer(r CodeRenderer) {
	e.codeMu.Lock()
	e.code = r
	e.codeMu.Unlock()
}

// CodeGrammars lists the animation grammars («Граматика анімації»).
func (e *Engine) CodeGrammars() []CodeGrammar { return e.coder().Grammars() }

// CodeGrammarByID finds a grammar of the engine's renderer.
func (e *Engine) CodeGrammarByID(id string) (CodeGrammar, bool) {
	for _, g := range e.CodeGrammars() {
		if g.ID == id {
			return g, true
		}
	}
	return CodeGrammar{}, false
}

// CodeAvailable reports whether code animations can render here (the
// browser the renderer drives), with the exact reason when not.
func (e *Engine) CodeAvailable(ctx context.Context) (string, error) { return e.coder().Available(ctx) }

// CodeConcurrency is how many code clips render at once: each drives a
// browser that uses a few cores — 2 on machines with 8+ cores, else 1.
func CodeConcurrency() int {
	if runtime.NumCPU() >= 8 {
		return 2
	}
	return 1
}

// estCodeSec is the expected time of one scene (spec + pictures + render),
// only for the estimate.
const estCodeSec = 45

// ---- the project model -------------------------------------------------------------------

// CodeClip is a scene's code-drawn animation (Scene.Code).
type CodeClip struct {
	// Grammar is the user's choice for this scene ("" = Input.CodeGrammar).
	Grammar string `json:"grammar,omitempty"`
	// Suggested is the grammar the plan proposed for this scene (used in
	// auto mode).
	Suggested string `json:"suggested,omitempty"`
	// Spec is the codeanim spec (JSON); pictures are named as in Images.
	Spec json.RawMessage `json:"spec,omitempty"`
	// Duration (s) the spec was timed to (codeTiming).
	Duration float64 `json:"duration,omitempty"`
	// Estimated: written before the voice existed (timed by an estimate);
	// written again once the voice is made unless Edited.
	Estimated bool `json:"estimated,omitempty"`
	// Words is the spoken text the spec was written for; Choice the grammar
	// choice it was written under ("" auto).
	Words  string `json:"words,omitempty"`
	Choice string `json:"choice,omitempty"`
	// Edited: the user changed the spec — it is kept and only re-timed.
	Edited bool `json:"edited,omitempty"`
	// Images are the pictures the spec shows, drawn by the image service.
	Images []CodeImage `json:"images,omitempty"`
	// Fallback: there is no animation — the scene is its frame with a camera
	// move (like «Тільки картинки»); Error says exactly why.
	Fallback bool   `json:"fallback,omitempty"`
	Error    string `json:"error,omitempty"`
	// Rendered is the signature of the spec the clip (Scene.Video) shows.
	Rendered string `json:"rendered,omitempty"`
	// Preview is a still of the spec at PreviewAt s («Попередній перегляд кадру»).
	Preview   Asset   `json:"preview,omitzero"`
	PreviewAt float64 `json:"previewAt,omitempty"`
}

// CodeImage is one picture of a code animation.
type CodeImage struct {
	Name      string `json:"name"`                // what the spec calls it ("taras.png")
	Kind      string `json:"kind,omitempty"`      // CodeSprite | CodePicture
	Prompt    string `json:"prompt"`              // English, for the image model
	Character string `json:"character,omitempty"` // "@tag" of the cast member it shows
	// Base names the picture of the same spec this one is a variant of (the
	// cast sprite's "shocked" frame): it is drawn from that picture.
	Base  string `json:"base,omitempty"`
	Image Asset  `json:"image"`
}

// Picture kinds (CodeImage.Kind).
const (
	CodeSprite  = "sprite"  // cut out on transparency (characters, icons, objects)
	CodePicture = "picture" // a full picture (a photo, a screenshot, a scene)
)

// codeFPS is the frame rate of every code clip.
const codeFPS = 30

// codeDir is where a project keeps the pictures of its code animations.
const codeDir = "images/code"

// CodeGrammarOf is the grammar choice of scene s in p: its own, else the
// project's ("" = auto).
func CodeGrammarOf(p *Project, s *Scene) string {
	return firstNonBlank(strings.TrimSpace(s.Code.Grammar), strings.TrimSpace(p.Input.CodeGrammar))
}

// codeSize is the frame size of a project's clips.
func codeSize(in Input) (w, h int) {
	if in.Aspect == "9:16" {
		return 1080, 1920
	}
	return 1920, 1080
}

// ---- timing -----------------------------------------------------------------------------

// codeTiming is how long a scene's clip lasts and where its voice starts in
// it: the montage's breath before the line (0.25 s; one frame with the rapid
// tempo), the incoming transition overlap, the line, the tail (0.35 s / 0.08
// s) and the outgoing overlap — exactly what media.Layout gives the scene, so
// the clip neither freezes nor gets cut. voice = 0: no voice yet (estimate =
// true, the narration at speaking pace).
func codeTiming(p *Project, s *Scene) (dur, offset, voice float64, estimate bool) {
	lead, tail := 0.25, 0.35
	if p.Input.TightLines() {
		lead, tail = 1.0/24, 0.08
	}
	left, right := codeSidePads(p, s.ID)
	switch {
	case s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart:
		// The slice of the own recording keeps its own breath.
		voice, lead, tail = s.VoiceEnd-s.VoiceStart, 0, 0
	case p.Input.Voiceover && s.Audio.Status == StatusDone && !s.Audio.Stale && s.Audio.Duration > 0:
		voice = s.Audio.Duration
	case p.Input.SoundMode() == AudioVoiceover && strings.TrimSpace(s.Narration) != "":
		voice, estimate = spokenSeconds(s.Narration, ""), true
	default: // nothing said: as long as a still of it would last
		return frames(math.Max(3, stillLength(s))), 0, 0, false
	}
	return frames(math.Max(1, left+lead+voice+tail+right)), left + lead, voice, estimate
}

// frames rounds seconds up to whole frames of a code clip.
func frames(sec float64) float64 { return math.Ceil(sec*codeFPS-1e-6) / codeFPS }

// codeSidePads are the overlaps of a scene with its neighbours (crossfades)
// and the extra breath of long dips — montagePad split into the time before
// the line (left) and after it (right).
func codeSidePads(p *Project, sid string) (left, right float64) {
	idx := slices.IndexFunc(p.Scenes, func(s Scene) bool { return s.ID == sid })
	if idx < 0 {
		return 0, 0
	}
	def := firstNonBlank(p.Montage.Transition, media.DefaultTransition)
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
	side := func(k media.TransitionKind, breath float64) float64 {
		switch k {
		case media.KindXFade:
			return math.Round(d*24) / 24
		case media.KindDip:
			return math.Max(0, 0.6*d/2-breath)
		}
		return 0
	}
	film := kind(def) != media.KindCut
	switch {
	case idx > 0:
		left = side(kind(p.Scenes[idx-1].Transition), 0.25)
	case film:
		left = side(media.KindDip, 0.25)
	}
	switch {
	case idx < len(p.Scenes)-1:
		right = side(kind(p.Scenes[idx].Transition), 0.35)
	case film:
		right = side(media.KindDip, 0.35)
	}
	return left, right
}

// codeWord is a spoken word on the clip's clock.
type codeWord struct {
	Text string
	At   float64
}

// codeWords times the scene's spoken words on its clip's clock (offset =
// where the voice starts): the own recording's transcript, else the
// multi-voice parts' places, else the words spread over the voice by their
// syllables (a pause after sentence ends).
func codeWords(p *Project, s *Scene, tr *stt.Transcript, voice, offset float64) []codeWord {
	if s.AudioSource == AudioUpload && tr != nil && len(tr.Words) > 0 {
		var out []codeWord
		for _, w := range tr.Words {
			if w.Start >= s.VoiceStart-0.05 && w.End <= s.VoiceEnd+0.05 {
				out = append(out, codeWord{strings.TrimSpace(w.Text), round2(offset + math.Max(0, w.Start-s.VoiceStart))})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if voice <= 0 {
		return nil
	}
	parts := cleanParts(s.Parts)
	placedParts := len(parts) > 0
	for _, pt := range parts {
		placedParts = placedParts && pt.End > pt.Start
	}
	if placedParts {
		var out []codeWord
		for _, pt := range parts {
			out = append(out, spreadWords(pt.Text, offset+pt.Start, pt.End-pt.Start)...)
		}
		return out
	}
	return spreadWords(spokenText(s), offset, voice)
}

// spreadWords places the words of text over span seconds from start,
// each as long as its syllables (at least one), a short pause after
// punctuation.
func spreadWords(text string, start, span float64) []codeWord {
	words := strings.Fields(text)
	if len(words) == 0 || span <= 0 {
		return nil
	}
	weights := make([]float64, len(words))
	total := 0.0
	for i, w := range words {
		wt := math.Max(1, float64(syllables(w)))
		if strings.ContainsAny(w, ".!?…;:") {
			wt += 1.2
		} else if strings.ContainsAny(w, ",—–") {
			wt += 0.5
		}
		weights[i] = wt
		total += wt
	}
	out := make([]codeWord, len(words))
	at := start
	for i, w := range words {
		out[i] = codeWord{w, round2(at)}
		at += span * weights[i] / total
	}
	return out
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

// ---- the spec ------------------------------------------------------------------------------

// codeSpec is a spec while the studio works on it (map form; numbers are
// float64).
type codeSpec = map[string]any

func parseCodeSpec(raw []byte) (codeSpec, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("це не JSON: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("опис анімації має бути JSON-об'єктом {…}")
	}
	return m, nil
}

// marshalSpec is the stored (indented) form of a spec.
func marshalSpec(s codeSpec) json.RawMessage {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil
	}
	return b
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok && !math.IsNaN(f) && !math.IsInf(f, 0)
}

func cuesOf(s codeSpec) []map[string]any {
	var out []map[string]any
	for _, c := range asArray(s["cues"]) {
		if m, ok := c.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// codeFrame is what the studio fixes in every spec: the grammar, the exact
// duration, the frame and — with burnt-in subtitles — the band they need.
type codeFrame struct {
	grammar       string
	duration      float64
	width, height int
	safeBottom    int    // 0 = keep the spec's
	language      string // the film's language code (the map's labels)
}

func frameFor(p *Project, grammar string, dur float64) codeFrame {
	w, h := codeSize(p.Input)
	f := codeFrame{grammar: grammar, duration: dur, width: w, height: h, language: strings.ToLower(strings.TrimSpace(p.Input.Language))}
	if p.Montage.Subtitles == "burn" {
		f.safeBottom = 180
		if h > w {
			f.safeBottom = 520 // the subtitle band of a vertical film sits at ~1400–1520 px
		}
	}
	return f
}

// fixSpec writes the frame into s.
func fixSpec(s codeSpec, f codeFrame) {
	if f.grammar != "" {
		s["grammar"] = f.grammar
	}
	s["duration"] = f.duration
	s["fps"] = codeFPS
	s["width"], s["height"] = f.width, f.height
	delete(s, "alpha")
	if f.grammar == "map" {
		// The map names countries and presets in the film's language.
		d, _ := s["data"].(map[string]any)
		if d == nil {
			d = map[string]any{}
			s["data"] = d
		}
		d["language"] = mapLanguage(f.language)
	}
	if f.safeBottom > 0 {
		safe, _ := s["safe"].(map[string]any)
		if safe == nil {
			safe = map[string]any{}
		}
		if b, ok := num(safe["bottom"]); !ok || b < float64(f.safeBottom)*0.8 {
			safe["bottom"] = f.safeBottom
		}
		s["safe"] = safe
	}
}

// mapLanguages are the label languages of the map grammar.
var mapLanguages = []string{"en", "uk", "ru", "de", "fr", "es", "pl", "it", "pt"}

// mapLanguage is the map's label language for a film language code
// ("uk", "uk-UA", "pt-BR" …); English when the map has none.
func mapLanguage(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if i := strings.IndexAny(code, "-_"); i > 0 {
		code = code[:i]
	}
	if slices.Contains(mapLanguages, code) {
		return code
	}
	return "en"
}

// retimeSpec stretches a spec written for another length to dur: every
// cue's at / dur (and y4's absolute data.hit) scaled alike.
func retimeSpec(s codeSpec, dur float64) {
	old, ok := num(s["duration"])
	if !ok || old <= 0 || math.Abs(old-dur) < 0.5/codeFPS {
		s["duration"] = dur
		return
	}
	k := dur / old
	for _, c := range cuesOf(s) {
		for _, key := range []string{"at", "dur"} {
			if v, ok := num(c[key]); ok {
				c[key] = round2(v * k)
			}
		}
		if d, ok := c["data"].(map[string]any); ok {
			if v, ok := num(d["hit"]); ok {
				d["hit"] = round2(v * k)
			}
		}
	}
	s["duration"] = dur
}

var imageNameRe = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9_.-]{0,60}\.(png|jpg|jpeg|webp)$`)

// imageRefs are the picture file names a spec mentions (any string value
// that names an image file; URLs and data: URIs are not files).
func imageRefs(v any, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for _, vv := range x {
			imageRefs(vv, out)
		}
	case []any:
		for _, vv := range x {
			imageRefs(vv, out)
		}
	case string:
		l := strings.ToLower(strings.TrimSpace(x))
		if strings.HasPrefix(l, "data:") || strings.HasPrefix(l, "http:") || strings.HasPrefix(l, "https:") {
			return
		}
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
			if strings.HasSuffix(l, ext) {
				out[strings.TrimSpace(x)] = true
			}
		}
	}
}

// replaceStrings returns v with every string equal to a key of m replaced.
func replaceStrings(v any, m map[string]string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = replaceStrings(vv, m)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = replaceStrings(vv, m)
		}
		return out
	case string:
		if r, ok := m[strings.TrimSpace(x)]; ok {
			return r
		}
	}
	return v
}

// checkSpec lists what is wrong with a spec (none = it can render): the
// grammar, the cues (time inside the clip, a kind the grammar draws), and
// pictures that are neither declared nor files of the project.
func checkSpec(s codeSpec, grammars []CodeGrammar, images []CodeImage, dir string) []string {
	var probs []string
	gid, _ := s["grammar"].(string)
	var g *CodeGrammar
	for i := range grammars {
		if grammars[i].ID == gid {
			g = &grammars[i]
		}
	}
	if g == nil && len(grammars) > 0 {
		ids := make([]string, len(grammars))
		for i, x := range grammars {
			ids[i] = x.ID
		}
		probs = append(probs, fmt.Sprintf(`"grammar" %q is unknown (one of: %s)`, gid, strings.Join(ids, ", ")))
	}
	dur, _ := num(s["duration"])
	raw, ok := s["cues"]
	if !ok {
		probs = append(probs, `"cues" is missing`)
	} else if _, isArr := raw.([]any); !isArr {
		probs = append(probs, `"cues" must be an array`)
	}
	cues := cuesOf(s)
	if ok && len(cues) == 0 {
		probs = append(probs, `"cues" is empty: the clip would show nothing`)
	}
	for i, c := range cues {
		kind, _ := c["kind"].(string)
		at, okAt := num(c["at"])
		switch {
		case strings.TrimSpace(kind) == "":
			probs = append(probs, fmt.Sprintf(`cue %d: "kind" is missing`, i+1))
		case g != nil && len(g.Kinds) > 0 && !slices.Contains(g.Kinds, kind):
			probs = append(probs, fmt.Sprintf(`cue %d: kind %q is not drawn by %s (use: %s)`, i+1, kind, g.ID, strings.Join(g.Kinds, ", ")))
		}
		switch {
		case !okAt:
			probs = append(probs, fmt.Sprintf(`cue %d: "at" must be a number of seconds`, i+1))
		case at < 0 || (dur > 0 && at >= dur):
			probs = append(probs, fmt.Sprintf(`cue %d: "at" %.2f is outside the clip (0 … %.2f s)`, i+1, at, dur))
		}
	}
	declared := map[string]bool{}
	for _, im := range images {
		declared[im.Name] = true
	}
	refs := map[string]bool{}
	imageRefs(s["cues"], refs)
	imageRefs(s["data"], refs)
	var names []string
	for r := range refs {
		names = append(names, r)
	}
	sort.Strings(names)
	for _, r := range names {
		if declared[r] {
			continue
		}
		if dir != "" && within(dir, filepath.Join(dir, filepath.FromSlash(r))) && isFile(filepath.Join(dir, filepath.FromSlash(r))) {
			continue
		}
		probs = append(probs, fmt.Sprintf(`picture %q is used but not declared in "images"`, r))
	}
	return probs
}

// ValidateCodeSpec checks a spec the user typed for scene sid of p (the
// editor's live check): "" = it can render, else the problems in one text.
func (e *Engine) ValidateCodeSpec(p *Project, sid string, raw []byte) string {
	s := findScene(p, sid)
	if s == nil {
		return "сцену не знайдено"
	}
	spec, err := parseCodeSpec(raw)
	if err != nil {
		return err.Error()
	}
	if probs := checkSpec(spec, e.CodeGrammars(), s.Code.Images, e.Dir(p.ID)); len(probs) > 0 {
		return strings.Join(probs, "; ")
	}
	return ""
}

// specSig is the signature of what a clip renders.
func specSig(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// ---- the spec writer (text model) ------------------------------------------------------------

// codeAlternates: a project grammar that gives way, scene by scene, to
// these when the scene does not fit it — the map yields to kinetic text or
// a storytime card only where no place on Earth is involved. The plan
// suggests (Code.Suggested), the spec writer decides; a grammar the user
// picked for the scene itself is never overridden.
var codeAlternates = map[string][]string{"map": {"y5_kinetic_type", "y4_storytime"}}

// codeShotRules is what the shot writer adds in a code film: the motion
// prompt becomes the animation brief, the grammar suggestion.
func codeShotRules(p *Project) []string {
	out := []string{`- NOTE: this film is NOT shot by a video model: every scene becomes an animation drawn by code (charts, maps, kinetic ` +
		`text, diagrams, keynote cards, a whiteboard), written later from the voice-over. In "motionPrompt" write the animation brief ` +
		`(20–45 words): what the screen shows — the numbers of a chart, the points of a list, the map region with what moves on it, ` +
		`the diagram — and which element appears on which words. "imagePrompt" is only a still used if the animation cannot be made.`}
	if strings.TrimSpace(p.Input.CodeGrammar) == "" {
		if gs := newCodeRenderer().Grammars(); len(gs) > 0 {
			var ids []string
			for _, g := range gs {
				ids = append(ids, g.ID+" ("+clipRunes(g.Description, 90)+")")
			}
			out = append(out, `- Also return "grammar" for every scene: the animation grammar that shows it best, one of: `+strings.Join(ids, "; ")+`.`)
		}
	} else if alts := codeAlternates[cleanGrammarID(p.Input.CodeGrammar)]; len(alts) > 0 {
		g := cleanGrammarID(p.Input.CodeGrammar)
		out = append(out, fmt.Sprintf(`- Also return "grammar" for every scene: %q (the film's grammar) whenever the scene involves ANY place, country, `+
			`territory, border, route, voyage or position on Earth — almost every scene of this film. Only a scene with no geography at all may use `+
			`%s instead.`, g, strings.Join(alternateDescriptions(alts), " or ")))
		if g == "map" {
			out = append(out, `- In "motionPrompt" of a map scene name the region in view, which territories are filled in which colour, `+
				`the routes/rivers drawn, the icons (price tags, coins, ships, flags, year) and which cast figures stand where and react how.`)
		}
	}
	return out
}

// alternateDescriptions describes alternate grammars for the model.
func alternateDescriptions(ids []string) []string {
	var out []string
	gs := newCodeRenderer().Grammars()
	for _, id := range ids {
		d := id
		for _, g := range gs {
			if g.ID == id {
				d = fmt.Sprintf("%q (%s)", id, clipRunes(g.Description, 90))
			}
		}
		out = append(out, d)
	}
	return out
}

// codeReply is what the spec writer returns.
type codeReply struct {
	Grammar string          `json:"grammar"`
	Spec    json.RawMessage `json:"spec"`
	Images  []struct {
		Name      string `json:"name"`
		Kind      string `json:"kind"`
		Prompt    string `json:"prompt"`
		Character string `json:"character"`
	} `json:"images"`
}

// codeJob is everything the spec writer needs about one scene (a copy).
type codeJob struct {
	p        Project
	s        Scene
	choice   string        // the grammar choice ("" auto)
	grammars []CodeGrammar // offered to the model
	// primary: the project's grammar when its alternates are offered too
	// (codeAlternates) — the spec writer keeps it unless the scene has no
	// geography.
	primary  string
	cast     []castSprite // the cast's sprites a map scene may place
	dur      float64
	offset   float64
	voice    float64
	estimate bool
	words    []codeWord
}

// codeSystem is the spec writer's instructions.
func codeSystem(j *codeJob) string {
	w, h := codeSize(j.p.Input)
	format := fmt.Sprintf("%d×%d (horizontal 16:9)", w, h)
	if h > w {
		format = fmt.Sprintf("%d×%d (vertical 9:16)", w, h)
	}
	lang := firstNonBlank(languageName(j.p.Input.Language), "the language of the narration")
	f := frameFor(&j.p, "", j.dur)
	lines := []string{
		`You are a motion designer. You write ONE scene of an explainer film as a clip spec for a code-drawn animation engine ` +
			`(huashu-art-motion "clip" specs): no video model — every element is drawn by code at exact times.`,
		`Return ONLY a JSON object: {"grammar": "<grammar id>", "spec": { the clip spec }, "images": [ the pictures the spec shows ]}.`,
		`The spec: {"grammar", "duration", "fps", "width", "height", "data": { grammar-level fields }, "cues": [ {"at", "kind", "text", "sub", "data", "image", "dur"} ]}.`,
		``,
		`Rules:`,
		fmt.Sprintf(`- "duration" is EXACTLY %.2f, "fps" %d, "width" %d, "height" %d — %s.`, j.dur, codeFPS, w, h, format),
		`- "at" of a cue is the second its element first becomes visible: put it ON the words of the narration timeline below that it shows ` +
			`(a number cue's "at" is when the number lands — the moment its value is said). Never earlier than the words, at most 0.4 s later.`,
		fmt.Sprintf(`- Every "at" is between 0 and %.2f. 2–5 cues for 3–8 s, about one per sentence; nothing in the last 0.4 s. The first cue is at the voice start (%.2f) at the latest — the screen is never empty after the cut.`, math.Max(0.1, j.dur-0.4), j.offset),
		`- Every word on screen is in ` + lang + `, short (a label ≤ 6 words), taken from the narration; numbers and dates exactly as said.`,
		`- Use only the cue kinds and fields of the chosen grammar (an unknown kind draws nothing). Honest data: never invent precise ` +
			`statistics the narration does not give — then show the idea (a list, a diagram) instead of a chart.`,
		`- Pictures: when a cue or the grammar data shows an image (a character, an icon, a photo, a screenshot-like card), declare it in "images" as ` +
			`{"name": "<short-latin-name>.png", "kind": "sprite" (one character or object cut out on transparency) | "picture" (a full rectangular picture), ` +
			`"prompt": "<English description for an image model; no text, letters or numbers in it>", "character": "@tag" when it shows a cast member} ` +
			`and use exactly that name in the spec. Never write other file paths. At most 3 pictures; none when the grammar draws everything itself.`,
	}
	if f.safeBottom > 0 {
		lines = append(lines, fmt.Sprintf(`- Burnt-in subtitles cover the bottom of the frame: add "safe": {"bottom": %d}.`, f.safeBottom))
	}
	if g := strings.TrimSpace(j.p.StyleGuide); g != "" {
		lines = append(lines, `- The film's look (colours, mood; use matching accent colours where a grammar takes them): `+clipRunes(g, 600))
	}
	if slices.ContainsFunc(j.grammars, func(g CodeGrammar) bool { return g.ID == "map" }) {
		lines = append(lines, mapRules(j, lang)...)
	}
	lines = append(lines, ``)
	switch {
	case len(j.grammars) == 1:
		lines = append(lines, `Grammar (use exactly this one):`)
	case j.primary != "":
		lines = append(lines, fmt.Sprintf(`Grammars: use %q — the film's grammar — for every scene that involves any place, country, territory, `+
			`route or position on Earth. Only a scene with NO geography at all may use one of the others below.`, j.primary))
	default:
		lines = append(lines, `Grammars (pick the ONE that shows this scene best):`)
	}
	for _, g := range j.grammars {
		lines = append(lines, ``, "### "+g.ID+" — "+g.Label, g.Description)
		if ref := strings.TrimSpace(g.Reference); ref != "" {
			lines = append(lines, "Full spec reference of "+g.ID+":", ref)
		} else if s := strings.TrimSpace(g.Schema); s != "" {
			lines = append(lines, "Fields: "+s)
		}
		if len(g.Kinds) > 0 {
			lines = append(lines, "Cue kinds: "+strings.Join(g.Kinds, ", "))
		}
		if g.Sprites && len(j.cast) == 0 {
			lines = append(lines, "This grammar places cut-out pictures: declare them as sprites.")
		}
		examples := g.Examples
		if len(examples) == 0 && len(g.Example) > 0 {
			examples = []json.RawMessage{g.Example}
		}
		for k, ex := range examples {
			var buf bytes.Buffer
			if json.Compact(&buf, ex) != nil {
				continue
			}
			label := "Example spec: "
			if len(examples) > 1 {
				label = fmt.Sprintf("Example film %d of %d (other duration/size/language than yours; its picture paths are placeholders): ", k+1, len(examples))
			}
			lines = append(lines, label+buf.String())
		}
	}
	return strings.Join(lines, "\n")
}

// mapRules are the spec writer's rules for the map grammar.
func mapRules(j *codeJob, lang string) []string {
	out := []string{
		``,
		`Map scenes (grammar "map"):`,
		fmt.Sprintf(`- "data.language" is set to %q by the studio (country and preset names come in it). Every label, tag, sale sign and `+
			`note text you write yourself is in %s, at most 3 words; numbers and dates exactly as said.`, mapLanguage(j.p.Input.Language), lang),
		`- Real geography: put "data.view" on the region the narration is about (its zoom from the reference), use presets / ISO countries for ` +
			`territories, real [lon, lat] for every point. Keep each country's colour from the previous scenes. 1–3 "camera" cues when the story ` +
			`moves between places (the flight lands on the words that name the place).`,
		`- Readable on a phone: at most ~6 labels and 3 icons on screen at once; hide ("hide" cue) what the story has left.`,
		`- Every map scene MOVES. Besides "camera" cues it has at least one action cue — fill, recolor, drawRoute, moveAlong, pop, sprite ` +
			`or bubble — on the words it shows (a map that only sits there with labels is not allowed). And it has a gentle camera move: ` +
			`a "camera" flight when the story changes place, else a slow push-in over the whole clip: ` +
			`{"at": 0, "kind": "camera", "view": {"zoomBy": 1.1}, "dur": <the clip's duration>, "ease": "sine", "arc": false}. ` +
			`(A spec without them gets a default glow / sprite bounce and push-in added by the studio.)`,
		mapSpriteRule(j),
	}
	if len(j.cast) > 0 {
		out = append(out, `- The cast figures are READY transparent sprites (chibi peg-doll cut-outs) — do NOT declare them in "images". `+
			`Put each figure the scene is about in "data.sprites" as {"id", "lon", "lat" (or "place"), "image": "<its main file>", `+
			`"alt": "<its shocked file>"} (no "scale": the default size is right — see the sprite size rule), standing at the geographic point the narration ties the person to (their country, capital, `+
			`the place where they act). Bring them in with a "sprite" cue action "enter" on the words that name them; on a surprise / shock / `+
			`big number use action "swap" with "frame": "alt" (and "dur": 1.5–2.5 to come back) plus "shake" or "bounce". At most 3 figures per scene; `+
			`none when the scene is only about land, money or routes.`)
	}
	return out
}

// mapSpriteRule is the sprite size rule of the map (the grammar's default:
// the figure's own height, transparent margins trimmed).
func mapSpriteRule(j *codeJob) string {
	w, h := codeSize(j.p.Input)
	if h > w {
		return `- Sprites are BIG, like the reference: the default "scale" is 0.37 of the short side — the figure is ≈ 20 % of the frame ` +
			`height. Leave "scale" out for the default; 0.3–0.42 is the range, never smaller (a smaller value is dropped). Stand each figure ` +
			`on its geographic point but, when you can, a little beside the points of the region / river labels it would cover (labels the ` +
			`figure still covers glide aside by themselves).`
	}
	return `- Sprites are BIG, like the reference: the default "scale" is 0.32 of the short side (≈ 32 % of the frame height). Leave ` +
		`"scale" out for the default; 0.26–0.38 is the range, never smaller (a smaller value is dropped). Stand each figure on its ` +
		`geographic point but, when you can, a little beside the points of the labels it would cover (labels it still covers glide aside).`
}

// codePrompt is the scene the spec writer animates.
func codePrompt(j *codeJob) string {
	p, s := &j.p, &j.s
	var b strings.Builder
	fmt.Fprintf(&b, "Film: %s\n", firstNonBlank(p.Title, "(untitled)"))
	if t := strings.TrimSpace(p.Logline); t != "" {
		fmt.Fprintf(&b, "Logline: %s\n", t)
	}
	fmt.Fprintf(&b, "Scene: %s\n", firstNonBlank(s.Title, s.ID))
	if t := spokenText(s); t != "" {
		fmt.Fprintf(&b, "Narration: %s\n", t)
	}
	if t := strings.TrimSpace(s.MotionPrompt); t != "" {
		fmt.Fprintf(&b, "Animation brief from the plan: %s\n", t)
	}
	if !s.OnScreen.Empty() {
		fmt.Fprintf(&b, "Key on-screen facts: %s\n", onScreenSummary(s.OnScreen))
	}
	if len(j.words) > 0 {
		var parts []string
		for _, w := range j.words {
			parts = append(parts, fmt.Sprintf("%.2f %s", w.At, w.Text))
		}
		note := "measured from the voice"
		if j.estimate {
			note = "estimated — the voice is not recorded yet"
		}
		fmt.Fprintf(&b, "Narration timeline (seconds from the clip start, %s; the voice starts at %.2f s and ends at %.2f s):\n%s\n",
			note, j.offset, j.offset+j.voice, strings.Join(parts, " | "))
	} else {
		b.WriteString("Nothing is said in this scene: pace the cues evenly.\n")
	}
	var cast []string
	for _, id := range s.CharacterIDs {
		if c := findCharacter(p, id); c != nil {
			cast = append(cast, fmt.Sprintf("@%s — %s: %s", c.Tag, c.Name, clipRunes(strings.TrimSpace(c.Description), 220)))
		}
	}
	if len(j.cast) > 0 {
		var lines []string
		for _, c := range j.cast {
			where := "in the film"
			if c.inScene {
				where = "IN THIS SCENE"
			}
			lines = append(lines, fmt.Sprintf("@%s — %s (%s): image %q, alt (shocked) %q — %s", c.tag, c.name, where, c.main, c.alt,
				clipRunes(c.desc, 160)))
		}
		fmt.Fprintf(&b, "Cast figures for map scenes (ready sprites; place the ones the narration is about):\n%s\n", strings.Join(lines, "\n"))
	} else if len(cast) > 0 {
		fmt.Fprintf(&b, "Cast in this scene (a picture of one of them is a sprite with its \"character\"):\n%s\n", strings.Join(cast, "\n"))
	}
	if l := findLocation(p, s.LocationID); l != nil {
		fmt.Fprintf(&b, "Place: %s — %s\n", l.Name, clipRunes(strings.TrimSpace(l.Description), 200))
	}
	if i := slices.IndexFunc(p.Scenes, func(x Scene) bool { return x.ID == s.ID }); i > 0 {
		// Specs are written 2–3 at a time (SpecConcurrency): the scene just
		// before may still be in the works — continue the nearest one written.
		k := i - 1
		for k > 0 && len(p.Scenes[k].Code.Spec) == 0 {
			k--
		}
		prev := p.Scenes[k]
		if g, _ := specGrammar(prev.Code.Spec); g != "" {
			which, mapWhich := "The previous scene was", "The previous map scene ended"
			if k < i-1 {
				which = fmt.Sprintf("Scene %d (the nearest earlier one already written; the scenes between are written at the same time) was", k+1)
				mapWhich = fmt.Sprintf("Map scene %d ended", k+1)
			}
			fmt.Fprintf(&b, "%s animated with %s (keep the film coherent; change the grammar only when the content asks for it).\n", which, g)
			if g == "map" {
				if st := prevMapState(prev.Code.Spec); st != "" {
					fmt.Fprintf(&b, "%s like this (start near its last view, keep the countries' colours): %s\n", mapWhich, st)
				}
			}
		}
	}
	if s.Code.Suggested != "" && (j.choice == "" || (j.primary != "" && slices.ContainsFunc(j.grammars, func(g CodeGrammar) bool { return g.ID == s.Code.Suggested }))) {
		fmt.Fprintf(&b, "The plan suggests the grammar %s for this scene.\n", s.Code.Suggested)
	}
	return b.String()
}

// prevMapState is the end state of a map spec, compact: its last camera
// view and its regions' colours.
func prevMapState(raw json.RawMessage) string {
	spec, err := parseCodeSpec(raw)
	if err != nil {
		return ""
	}
	d, _ := spec["data"].(map[string]any)
	state := map[string]any{}
	if d != nil && d["view"] != nil {
		state["view"] = d["view"]
	}
	var cams []map[string]any
	for _, c := range cuesOf(spec) {
		if k := jsString(c["kind"]); (k == "camera" || k == "view" || k == "cam") && c["view"] != nil {
			if _, ok := num(c["at"]); ok {
				cams = append(cams, c)
			}
		}
	}
	sort.SliceStable(cams, func(a, b int) bool { x, _ := num(cams[a]["at"]); y, _ := num(cams[b]["at"]); return x < y })
	for _, c := range cams {
		v, _ := c["view"].(map[string]any)
		by, rel := num(v["zoomBy"])
		if !rel || v["lon"] != nil || v["bbox"] != nil || v["place"] != nil || v["zoom"] != nil {
			state["view"] = c["view"]
			continue
		}
		// a push-in on the spot: the view so far, zoomed
		if cur, ok := state["view"].(map[string]any); ok {
			if z, ok := num(cur["zoom"]); ok {
				nv := maps.Clone(cur)
				nv["zoom"] = round2(z * by)
				state["view"] = nv
			}
		}
	}
	var regions []any
	if d != nil {
		for _, r := range asArray(d["regions"]) {
			m, ok := r.(map[string]any)
			if !ok {
				continue
			}
			keep := map[string]any{}
			for _, k := range []string{"id", "preset", "country", "countries", "states", "color"} {
				if m[k] != nil {
					keep[k] = m[k]
				}
			}
			regions = append(regions, keep)
		}
	}
	for _, c := range cuesOf(spec) { // recolours change the final colour
		if jsString(c["kind"]) == "recolor" && c["region"] != nil && c["color"] != nil {
			regions = append(regions, map[string]any{"recolor": c["region"], "color": c["color"]})
		}
	}
	if len(regions) > 0 {
		state["regions"] = regions
	}
	if len(state) == 0 {
		return ""
	}
	b, err := json.Marshal(state)
	if err != nil {
		return ""
	}
	return clipRunes(string(b), 1500)
}

// specGrammar is the grammar a stored spec names.
func specGrammar(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	s, err := parseCodeSpec(raw)
	if err != nil {
		return "", false
	}
	g, _ := s["grammar"].(string)
	return g, g != ""
}

// codeJobFor gathers what writing scene sid's spec needs.
func (e *Engine) codeJobFor(st *proj, sid string) (*codeJob, error) {
	var j *codeJob
	if !e.read(st, func(p *Project) bool {
		s := findScene(p, sid)
		if s == nil {
			return false
		}
		j = &codeJob{p: cloneProject(p), choice: CodeGrammarOf(p, s)}
		j.s = *findScene(&j.p, sid)
		return true
	}) {
		return nil, errors.New("сцену не знайдено")
	}
	all := e.CodeGrammars()
	if len(all) == 0 {
		return nil, errNoCodeRenderer
	}
	if j.choice != "" {
		for _, g := range all {
			if g.ID == j.choice {
				j.grammars = []CodeGrammar{g}
			}
		}
		if len(j.grammars) == 0 {
			return nil, fmt.Errorf("граматики анімації «%s» немає в цій версії студії — обери іншу", j.choice)
		}
		if strings.TrimSpace(j.s.Code.Grammar) == "" { // the project's grammar: its alternates may take a scene
			for _, id := range codeAlternates[j.choice] {
				if g, ok := grammarIn(all, id); ok {
					j.grammars = append(j.grammars, g)
				}
			}
			if len(j.grammars) > 1 {
				j.primary = j.choice
			}
		}
	} else {
		j.grammars = all
	}
	if slices.ContainsFunc(j.grammars, func(g CodeGrammar) bool { return g.ID == "map" }) {
		j.cast = castSpritesOf(&j.p, &j.s)
	}
	j.dur, j.offset, j.voice, j.estimate = codeTiming(&j.p, &j.s)
	var tr *stt.Transcript
	if j.s.AudioSource == AudioUpload {
		if t, ok := e.VoiceTranscript(st.id); ok {
			tr = &t
		}
	}
	j.words = codeWords(&j.p, &j.s, tr, j.voice, j.offset)
	return j, nil
}

func grammarIn(gs []CodeGrammar, id string) (CodeGrammar, bool) {
	for _, g := range gs {
		if g.ID == id {
			return g, true
		}
	}
	return CodeGrammar{}, false
}

// writeSpec asks the text model for the scene's spec: checked, ONE repair
// round with the problems, else the error (the exact problems).
func (e *Engine) writeSpec(ctx context.Context, st *proj, j *codeJob) (codeSpec, []CodeImage, error) {
	system, prompt := codeSystem(j), codePrompt(j)
	var lastRaw string
	var probs []string
	for attempt := 0; attempt < 2; attempt++ {
		ask := prompt
		if attempt > 0 {
			ask += "\n\nYOUR PREVIOUS ANSWER:\n" + clipRunes(lastRaw, 12000) + "\n\nIt cannot render:\n- " + strings.Join(probs, "\n- ") +
				"\nReturn the corrected JSON object only."
		}
		var raw json.RawMessage
		if err := e.text.JSON(ctx, llm.Request{System: system, Prompt: ask, Temperature: 0.5}, &raw); err != nil {
			if !isCancel(err) && strings.Contains(err.Error(), "JSON") {
				if attempt == 0 {
					lastRaw, probs = err.Error(), []string{"the answer was not a JSON object"}
					continue
				}
				return nil, nil, &specError{"текстова модель двічі відповіла не JSON-об'єктом: " + err.Error()}
			}
			return nil, nil, err
		}
		lastRaw = string(raw)
		spec, images, p := e.acceptSpec(st, j, raw)
		if len(p) == 0 {
			return spec, images, nil
		}
		probs = p
	}
	return nil, nil, &specError{fmt.Sprintf("текстова модель двічі написала анімацію з помилками: %s", strings.Join(probs, "; "))}
}

// specError: the text model answered, but its animation cannot render even
// after the repair round — the scene falls back to its frame. Any other
// failure (the service is down, not configured, out of quota) is an error
// of the scene that a retry can fix: no fallback.
type specError struct{ msg string }

func (e *specError) Error() string { return e.msg }

// acceptSpec checks a reply and fixes the frame (grammar, duration, size);
// problems = what the model must repair.
func (e *Engine) acceptSpec(st *proj, j *codeJob, raw []byte) (codeSpec, []CodeImage, []string) {
	var r codeReply
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Spec) == 0 {
		// A bare spec is fine too.
		if s, err2 := parseCodeSpec(raw); err2 == nil && s["cues"] != nil {
			r.Spec, r.Grammar = raw, jsString(s["grammar"])
		} else {
			return nil, nil, []string{`the answer must be {"grammar", "spec", "images"}`}
		}
	}
	spec, err := parseCodeSpec(r.Spec)
	if err != nil {
		return nil, nil, []string{`"spec": ` + err.Error()}
	}
	grammar := firstNonBlank(j.choice, strings.TrimSpace(r.Grammar), jsString(spec["grammar"]))
	switch {
	case len(j.grammars) == 1:
		grammar = j.grammars[0].ID
	case j.primary != "": // an alternate only when the model chose one
		grammar = j.primary
		if want := firstNonBlank(strings.TrimSpace(r.Grammar), jsString(spec["grammar"])); want != "" {
			if _, ok := grammarIn(j.grammars, want); ok {
				grammar = want
			}
		}
	}
	castNames := map[string]bool{}
	for _, c := range j.cast {
		castNames[c.main], castNames[c.alt] = true, true
	}
	var probs []string
	var images []CodeImage
	seen := map[string]bool{}
	for _, im := range r.Images {
		name := strings.TrimSpace(im.Name)
		switch {
		case !imageNameRe.MatchString(name):
			probs = append(probs, fmt.Sprintf(`image name %q must be a short latin file name like "hero.png"`, name))
			continue
		case strings.TrimSpace(im.Prompt) == "":
			probs = append(probs, fmt.Sprintf(`image %q has no "prompt"`, name))
			continue
		case seen[name] || castNames[name]: // the cast's sprites are the studio's
			continue
		}
		seen[name] = true
		kind := CodePicture
		if strings.EqualFold(strings.TrimSpace(im.Kind), CodeSprite) {
			kind = CodeSprite
		}
		images = append(images, CodeImage{Name: name, Kind: kind, Prompt: clipRunes(strings.TrimSpace(im.Prompt), 1200),
			Character: strings.TrimSpace(im.Character), Image: EmptyAsset()})
	}
	if len(images) > 6 {
		probs = append(probs, fmt.Sprintf("too many pictures (%d): at most 3", len(images)))
	}
	fixSpec(spec, frameFor(&j.p, grammar, j.dur))
	if grammar != "map" { // the map is on screen from frame 0; other grammars start empty
		liftFirstCue(spec, j.offset)
	} else {
		w, h := codeSize(j.p.Input)
		fixMapSprites(spec, h > w)
		at := j.offset + 0.45*j.voice
		if j.voice <= 0 {
			at = j.dur * 0.4
		}
		g, _ := grammarIn(j.grammars, "map")
		allowed := func(k string) bool { return len(g.Kinds) == 0 || slices.Contains(g.Kinds, k) }
		if added := ensureMapMotion(spec, at, allowed); len(added) > 0 {
			log.Printf("[studio:%s] map spec of %s had too little motion — added: %s", st.id, j.s.ID, strings.Join(added, ", "))
		}
	}
	images = append(images, castImages(spec, j.cast)...)
	probs = append(probs, checkSpec(spec, j.grammars, images, st.dir)...)
	return spec, images, probs
}

// liftFirstCue: the screen is never empty after a cut — the first cue(s)
// land at the voice start (offset) at the latest, even when the model put
// them on a later word.
func liftFirstCue(spec codeSpec, offset float64) {
	first := math.Inf(1)
	for _, c := range cuesOf(spec) {
		if at, ok := num(c["at"]); ok && at < first {
			first = at
		}
	}
	to := round2(math.Max(0, offset))
	if math.IsInf(first, 1) || first <= to+0.15 {
		return
	}
	for _, c := range cuesOf(spec) {
		if at, ok := num(c["at"]); ok && at == first {
			c["at"] = to
		}
	}
}

// ---- the cast's sprites (map) -----------------------------------------------------------------

// castSprite is a cast member as a map figure: a chibi peg-doll cut-out
// (main) and the same figure shocked (alt), drawn once per project.
type castSprite struct {
	tag, name, desc string
	main, alt       string // file names the spec uses
	inScene         bool
}

// maxCastSprites bounds the figures offered to one scene.
const maxCastSprites = 5

// castSpritesOf are the cast figures a map scene may place: the scene's
// cast first, then the film's other people (not objects).
func castSpritesOf(p *Project, s *Scene) []castSprite {
	var out []castSprite
	seen := map[string]bool{}
	add := func(c *Character, in bool) {
		if c == nil || c.IsObject() || seen[c.ID] || len(out) >= maxCastSprites {
			return
		}
		stem := strings.ToLower(safeName(strings.TrimPrefix(strings.TrimSpace(c.Tag), "@")))
		stem = strings.Trim(stem, "_-.")
		if stem == "" || !imageNameRe.MatchString(stem+"_shock.png") {
			return
		}
		seen[c.ID] = true
		out = append(out, castSprite{tag: c.Tag, name: c.Name, desc: strings.TrimSpace(c.Description),
			main: stem + ".png", alt: stem + "_shock.png", inScene: in})
	}
	for _, id := range s.CharacterIDs {
		add(findCharacter(p, id), true)
	}
	for i := range p.Characters {
		add(&p.Characters[i], false)
	}
	return out
}

// castImages are the pictures of the cast sprites spec uses (the main
// figure also when only its shocked frame is used: that one is drawn from it).
func castImages(spec codeSpec, cast []castSprite) []CodeImage {
	refs := map[string]bool{}
	imageRefs(spec["cues"], refs)
	imageRefs(spec["data"], refs)
	var out []CodeImage
	for _, c := range cast {
		if !refs[c.main] && !refs[c.alt] {
			continue
		}
		out = append(out, CodeImage{Name: c.main, Kind: CodeSprite, Character: "@" + c.tag, Image: EmptyAsset(),
			Prompt: fmt.Sprintf("Cute chibi peg-doll figure of %s: %s. Full body, standing upright like a game pawn, facing the viewer "+
				"in a slight three-quarter view, confident neutral expression, the whole figure from the top of the hat to the feet.", c.name, c.desc)})
		if refs[c.alt] {
			out = append(out, CodeImage{Name: c.alt, Kind: CodeSprite, Character: "@" + c.tag, Base: c.main, Image: EmptyAsset(),
				Prompt: fmt.Sprintf("The SAME chibi peg-doll figure of %s as the @base reference image — identical head, face, hair, hat, "+
					"costume, colours and proportions — now SHOCKED: eyes wide, mouth open in a round O, eyebrows raised, both little hands "+
					"raised by the cheeks. Full body, standing, the whole figure visible.", c.name)})
		}
	}
	return out
}

// ---- pictures -------------------------------------------------------------------------------

// codeImagePrompt is the image request of a picture: the description, the
// cast member's reference (when it has a picture), the film's look; a sprite
// is drawn on a flat chroma green that is keyed out afterwards.
func codeImagePrompt(p *Project, im *CodeImage, basePath string) (gen.ImageRequest, []namedRef) {
	var refs []namedRef
	lines := []string{strings.TrimSpace(im.Prompt)}
	if basePath != "" {
		// A variant (the shocked frame) is drawn from its main figure.
		refs = append(refs, namedRef{"base", basePath})
	} else if tag := strings.TrimPrefix(strings.TrimSpace(im.Character), "@"); tag != "" {
		for i := range p.Characters {
			c := &p.Characters[i]
			if !strings.EqualFold(c.Tag, tag) {
				continue
			}
			if c.Image.Path != "" && !textLooks(p, c.Image) {
				refs = append(refs, namedRef{c.Tag, c.Image.Path})
				lines = append(lines, fmt.Sprintf("@%s is %s — identical face, hair, body and outfit to the @%s reference image.", c.Tag, c.Name, c.Tag))
			} else {
				lines = append(lines, fmt.Sprintf("@%s is %s: %s (keep exactly this look).", c.Tag, c.Name, strings.TrimSpace(c.Description)))
			}
		}
	}
	aspect := p.Input.Aspect
	if im.Kind == CodeSprite {
		aspect = "3:4"
		lines = append(lines, "ONE cut-out sprite: the single subject whole and centred, isolated on a perfectly flat pure green (#00FF00) "+
			"chroma-key background that fills the whole image — no shadow, no floor, no gradient, no green anywhere on the subject.")
	}
	style := firstNonBlank(strings.TrimSpace(p.StyleGuide), "clean flat illustration")
	if im.Kind == CodeSprite && im.Character != "" {
		// A cast figure stands on the animated map: only the figure, never
		// the map, parchment or icons of the film's look.
		lines = append(lines, "Draw ONLY the figure: no map, no parchment, no ground, no icons or props around it. "+
			"Character look from the film's art style (ignore its backgrounds and maps): "+clipRunes(style, 900), noText)
	} else {
		lines = append(lines, "Art style: "+style, noText)
	}
	return gen.ImageRequest{Prompt: strings.Join(lines, "\n"), UIModel: p.Input.ImageModel, Aspect: aspect}, refs
}

// codeImageFile is the file a picture is stored in: the same request (kind,
// prompt, reference) is the same file — drawn once per project.
func codeImageFile(im *CodeImage, req gen.ImageRequest, refs []namedRef) string {
	h := sha256.New()
	h.Write([]byte(im.Kind + "\x00" + req.Prompt + "\x00" + req.Aspect))
	for _, r := range refs {
		h.Write([]byte("\x00" + r.path))
	}
	stem := strings.TrimSuffix(strings.ToLower(im.Name), filepath.Ext(im.Name))
	return fmt.Sprintf("%s/%s-%s.png", codeDir, safeName(stem), hex.EncodeToString(h.Sum(nil)[:5]))
}

// drawCodeImages draws the scene's pictures that have no file yet (ctx: the
// paid work context). The first failure is returned with the picture's name.
func (e *Engine) drawCodeImages(ctx context.Context, st *proj, sid string) error {
	var todo []CodeImage
	var p Project
	if !e.read(st, func(live *Project) bool {
		s := findScene(live, sid)
		if s == nil {
			return false
		}
		p = cloneProject(live)
		for _, im := range s.Code.Images {
			if im.Image.Path == "" || !isFile(filepath.Join(st.dir, filepath.FromSlash(im.Image.Path))) {
				todo = append(todo, im)
			}
		}
		return true
	}) {
		return errors.New("сцену не знайдено")
	}
	set := func(name string, fn func(a *Asset)) {
		e.update(st, func(live *Project) {
			if s := findScene(live, sid); s != nil {
				for i := range s.Code.Images {
					if s.Code.Images[i].Name == name {
						fn(&s.Code.Images[i].Image)
					}
				}
			}
		})
	}
	// Variants after the pictures they are drawn from.
	sort.SliceStable(todo, func(a, b int) bool { return todo[a].Base == "" && todo[b].Base != "" })
	for _, im := range todo {
		basePath := ""
		if im.Base != "" {
			e.read(st, func(live *Project) bool {
				if s := findScene(live, sid); s != nil {
					for _, o := range s.Code.Images {
						if o.Name == im.Base && o.Image.Status == StatusDone {
							basePath = o.Image.Path
						}
					}
				}
				return true
			})
			if basePath == "" || !isFile(filepath.Join(st.dir, filepath.FromSlash(basePath))) {
				err := fmt.Errorf("картинка «%s» малюється з «%s», а тієї немає", im.Name, im.Base)
				set(im.Name, func(a *Asset) { a.Status, a.Error = StatusError, err.Error() })
				return err
			}
		}
		req, refs := codeImagePrompt(&p, &im, basePath)
		rel := codeImageFile(&im, req, refs)
		abs := filepath.Join(st.dir, filepath.FromSlash(rel))
		unlock := lockCodeImage(abs) // scenes drawn in parallel share the cast's sprites
		if !isFile(abs) {            // another scene may have drawn the same picture
			set(im.Name, func(a *Asset) { a.Status, a.Error = StatusRunning, "" })
			data, err := e.drawCodeImage(ctx, st, im, req, refs)
			if err == nil {
				_, err = e.writeAsset(st, codeDir, filepath.Base(rel), data)
			}
			if err != nil {
				unlock()
				msg := e.failureText(err)
				set(im.Name, func(a *Asset) { a.Status, a.Error = StatusError, msg })
				return fmt.Errorf("картинка «%s»: %w", im.Name, err)
			}
		}
		unlock()
		set(im.Name, func(a *Asset) {
			a.Path, a.Status, a.Error, a.UpdatedAt = rel, StatusDone, "", nowMs()
		})
	}
	return nil
}

var codeImageLocks sync.Map // abs path → *sync.Mutex

// lockCodeImage serialises the drawing of one picture file.
func lockCodeImage(abs string) (unlock func()) {
	m, _ := codeImageLocks.LoadOrStore(strings.ToLower(abs), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// drawCodeImage draws one picture; a sprite comes back keyed (transparent PNG).
func (e *Engine) drawCodeImage(ctx context.Context, st *proj, im CodeImage, req gen.ImageRequest, refs []namedRef) ([]byte, error) {
	imgs, err := readRefs(st.dir, refs)
	if err != nil {
		return nil, err
	}
	req.Refs = imgs
	data, _, _, err := e.imageRewriting(ctx, st, req, nil, maxRewrites)
	if err != nil {
		return nil, err
	}
	src, _, err := media.DecodeImage(data)
	if err != nil {
		return nil, fmt.Errorf("сервіс картинок повернув не зображення: %w", err)
	}
	var out image.Image = src
	if im.Kind == CodeSprite {
		out = keyGreen(src)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// keyGreen cuts a sprite out of its flat green background: pixels whose
// green clearly dominates become transparent, the edge gets partial alpha
// and loses its green spill.
func keyGreen(src image.Image) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			r, g, bl := float64(c.R), float64(c.G), float64(c.B)
			spill := g - math.Max(r, bl) // how much greener than the rest
			alpha := 1.0
			switch {
			case spill > 90 && g > 110:
				alpha = 0
			case spill > 35:
				alpha = 1 - (spill-35)/55
			}
			if spill > 0 { // despill: green no brighter than the other channels
				g = math.Max(r, bl)
			}
			out.SetNRGBA(x-b.Min.X, y-b.Min.Y, color.NRGBA{R: c.R, G: uint8(g), B: c.B, A: uint8(math.Round(float64(c.A) * alpha))})
		}
	}
	return out
}

// resolvedSpec is the spec as the renderer gets it: the frame fixed, the
// picture names replaced by their files (relative to the project folder).
func resolvedSpec(code *CodeClip) ([]byte, error) {
	spec, err := parseCodeSpec(code.Spec)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, im := range code.Images {
		if im.Image.Path != "" {
			m[im.Name] = im.Image.Path
		}
	}
	out := replaceStrings(spec, m)
	return json.Marshal(out)
}

// ---- the scene pipeline --------------------------------------------------------------------

// codeNeedsSpec says why scene s needs a (new) spec, "" = its spec stands.
func codeNeedsSpec(p *Project, s *Scene) string {
	c := &s.Code
	switch {
	case len(c.Spec) == 0:
		return "немає анімації"
	case c.Edited:
		return "" // the user's spec is kept (only re-timed)
	case c.Choice != CodeGrammarOf(p, s):
		return "змінено граматику"
	case strings.TrimSpace(c.Words) != strings.TrimSpace(spokenText(s)):
		return "змінено текст сцени"
	case c.Estimated && codeVoiced(p, s):
		return "озвучка готова — точні таймінги"
	}
	return ""
}

// codeVoiced: the scene's voice is made (or nothing needs one).
func codeVoiced(p *Project, s *Scene) bool {
	if s.AudioSource == AudioUpload {
		return s.VoiceEnd > s.VoiceStart
	}
	if !needsVoice(p, s) {
		return true
	}
	return s.Audio.Status == StatusDone && !s.Audio.Stale && s.Audio.Duration > 0
}

// prepareCode writes (or re-times) the scene's spec and draws its pictures.
// A spec that cannot be written, or a picture that cannot be drawn, makes
// the scene fall back to its frame (Code.Fallback, the exact error). ok =
// the scene has a usable spec.
func (e *Engine) prepareCode(ctx, work context.Context, st *proj, sid string, rewrite bool) (ok bool, err error) {
	var need string
	if !e.read(st, func(live *Project) bool {
		s := findScene(live, sid)
		if s == nil {
			return false
		}
		need = codeNeedsSpec(live, s)
		return true
	}) {
		return false, errors.New("сцену не знайдено")
	}
	if rewrite {
		need = "переписати"
	}
	j, jerr := e.codeJobFor(st, sid)
	if jerr != nil {
		return false, jerr
	}
	if need != "" {
		spec, images, werr := e.writeSpec(ctx, st, j)
		if werr != nil {
			if isCancel(werr) {
				return false, werr
			}
			var bad *specError
			if !errors.As(werr, &bad) { // the service failed: the scene is an error, a retry may fix it
				return false, fmt.Errorf("анімацію не вдалося написати: %w", werr)
			}
			e.codeFallback(work, st, sid, "анімацію не вдалося написати: "+e.failureText(werr))
			return false, nil
		}
		raw := marshalSpec(spec)
		words, choice := strings.TrimSpace(spokenText(&j.s)), j.choice
		e.update(st, func(live *Project) {
			s := findScene(live, sid)
			if s == nil {
				return
			}
			c := &s.Code
			// Pictures of the old spec with the same name and prompt are kept.
			old := map[string]CodeImage{}
			for _, im := range c.Images {
				old[im.Name] = im
			}
			for i := range images {
				if o, ok := old[images[i].Name]; ok && o.Prompt == images[i].Prompt && o.Kind == images[i].Kind {
					images[i].Image = o.Image
				}
			}
			c.Spec, c.Images, c.Duration, c.Estimated = raw, images, j.dur, j.estimate
			c.Words, c.Choice, c.Edited = words, choice, false
			c.Fallback, c.Error = false, ""
			c.Preview.Stale = c.Preview.Status == StatusDone
		})
	} else {
		// The spec stands: only the voice's length may have changed.
		e.update(st, func(live *Project) {
			s := findScene(live, sid)
			if s == nil || len(s.Code.Spec) == 0 {
				return
			}
			c := &s.Code
			if math.Abs(c.Duration-j.dur) < 0.5/codeFPS && !c.Estimated {
				return
			}
			spec, perr := parseCodeSpec(c.Spec)
			if perr != nil {
				return
			}
			retimeSpec(spec, j.dur)
			fixSpec(spec, frameFor(live, jsString(spec["grammar"]), j.dur))
			c.Spec, c.Duration = marshalSpec(spec), j.dur
			c.Estimated = j.estimate
			c.Preview.Stale = c.Preview.Status == StatusDone
		})
	}
	if err := e.drawCodeImages(work, st, sid); err != nil {
		if isCancel(err) {
			return false, err
		}
		if !refusedText(e.failureText(err)) { // not a refusal: the image service failed, retry later
			return false, err
		}
		e.codeFallback(work, st, sid, e.failureText(err))
		return false, nil
	}
	return true, nil
}

// codeFallback makes the scene its frame (drawn now when missing) and keeps
// why; the clip of an older animation is dropped from the film.
func (e *Engine) codeFallback(work context.Context, st *proj, sid, why string) {
	log.Printf("[studio:%s] code animation of %s falls back to its frame: %s", st.id, sid, why)
	needFrame := false
	e.update(st, func(p *Project) {
		s := findScene(p, sid)
		if s == nil {
			return
		}
		s.Code.Fallback, s.Code.Error = true, clipRunes(why, maxAssetError)
		if s.Video.Status == StatusRunning || s.Video.Status == StatusQueued || s.Video.Status == StatusError {
			s.Video.Status, s.Video.Error, s.Video.Progress = StatusEmpty, "", 0
			if s.Video.Path != "" {
				s.Video.Status = StatusDone
			}
		}
		needFrame = s.Frame.Path == "" && s.Frame.Status != StatusRunning
	})
	if needFrame {
		e.genFrame(work, st, sid)
	}
}

// renderCode renders the scene's clip from its spec (ctx: cancellable).
// Skipped when the clip already shows this spec (unless force).
func (e *Engine) renderCode(ctx context.Context, st *proj, sid string, force bool) bool {
	var spec []byte
	var dur float64
	var sig string
	skip, gone := false, true
	e.read(st, func(p *Project) bool {
		s := findScene(p, sid)
		if s == nil || s.Code.Fallback || len(s.Code.Spec) == 0 {
			return false
		}
		gone = false
		var err error
		if spec, err = resolvedSpec(&s.Code); err != nil {
			spec = nil
			return false
		}
		dur, sig = s.Code.Duration, specSig(spec)
		skip = !force && s.Video.Status == StatusDone && !s.Video.Stale && s.Code.Rendered == sig && s.Video.Path != "" &&
			isFile(filepath.Join(st.dir, filepath.FromSlash(s.Video.Path)))
		return true
	})
	ref := assetRef{slotVideo, sid}
	switch {
	case gone:
		return false
	case spec == nil:
		e.fail(st, ref, errors.New("опис анімації пошкоджений — напиши його заново"))
		return false
	case skip:
		return true
	}
	if !e.begin(st, ref) {
		return false
	}
	name := fmt.Sprintf("code-%s-%d.mp4", safeName(sid), nowMs())
	dir := filepath.Join(st.dir, "videos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.fail(st, ref, err)
		return false
	}
	part := filepath.Join(dir, "."+name+".part.mp4")
	var last atomic.Int64
	opt := CodeRenderOptions{AssetsDir: st.dir, FFmpeg: e.ffNow().FFmpeg, Progress: func(done, total int) {
		if total <= 0 {
			return
		}
		pct := int64(5 + 94*done/total)
		if pct == last.Load() {
			return
		}
		last.Store(pct)
		e.update(st, func(p *Project) {
			if a := ref.in(p); a != nil && a.Status == StatusRunning {
				a.Progress = int(pct)
			}
		})
	}}
	err := e.coder().Render(ctx, spec, part, opt)
	if err == nil && !isFile(part) {
		err = errors.New("рушій анімації не створив файл")
	}
	if err != nil {
		_ = os.Remove(part)
		if isCancel(err) || ctx.Err() != nil {
			e.update(st, func(p *Project) {
				if a := ref.in(p); a != nil && a.Status == StatusRunning {
					a.Status, a.Progress = StatusEmpty, 0
					if a.Path != "" {
						a.Status = StatusDone
					}
				}
			})
			return false
		}
		e.fail(st, ref, fmt.Errorf("рендер анімації: %w", err))
		return false
	}
	rel := "videos/" + name
	st.fmu.RLock()
	if st.deleted.Load() {
		st.fmu.RUnlock()
		_ = os.Remove(part)
		return false
	}
	err = os.Rename(part, filepath.Join(dir, name))
	st.fmu.RUnlock()
	if err != nil {
		_ = os.Remove(part)
		e.fail(st, ref, err)
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
		a := &s.Video
		if a.Path != "" && a.Path != rel {
			drop = append(drop, a.Path)
		}
		a.Path, a.Status, a.Error, a.Stale, a.Progress = rel, StatusDone, "", false, 100
		a.Duration, a.UpdatedAt, a.Rewrite = dur, nowMs(), nil
		s.Code.Rendered, s.Code.Fallback, s.Code.Error = sig, false, ""
		s.TrimIn, s.TrimOut = 0, 0 // the clip is exactly the scene
		ok = true
	})
	e.discardFiles(st, drop)
	return ok
}

// genCode is one scene from its voice to its clip (Regen of the clip, the
// render stage): the spec when needed, its pictures, the render.
func (e *Engine) genCode(ctx, work context.Context, st *proj, sid string, force bool) bool {
	ready := false
	e.read(st, func(p *Project) bool {
		if s := findScene(p, sid); s != nil {
			ready = codeVoiced(p, s)
		}
		return true
	})
	if !ready {
		e.fail(st, assetRef{slotVideo, sid}, errors.New("Спершу потрібна озвучка сцени: анімація триває рівно як її голос"))
		return false
	}
	e.update(st, func(p *Project) { // a new try of a scene that fell back
		if s := findScene(p, sid); s != nil && s.Code.Fallback && force {
			s.Code.Fallback, s.Code.Error = false, ""
			if len(s.Code.Spec) > 0 && !s.Code.Edited {
				s.Code.Spec = nil
			}
		}
	})
	ok, err := e.prepareCode(ctx, work, st, sid, false)
	if err != nil {
		if !isCancel(err) {
			e.fail(st, assetRef{slotVideo, sid}, err)
		}
		return false
	}
	if !ok {
		return true // the scene is its frame now (Code.Error says why)
	}
	return e.renderCode(ctx, st, sid, force)
}

// ---- stages ---------------------------------------------------------------------------------

// codeScenes are the scenes of p that get an animation (not the ones that
// fell back, unless force).
func codeScenes(p *Project, force bool) []string {
	var ids []string
	for _, s := range p.Scenes {
		if !s.Code.Fallback || force {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// runVoicesFirst voices the scenes that need it (a code film is timed to its
// voice), counted by tick; returns the failures.
func (e *Engine) runVoicesFirst(j *jobCtx, force bool, tick func()) int {
	snap := j.snapshot()
	var ids []string
	if snap.Input.Voiceover {
		for _, s := range snap.Scenes {
			spoken := strings.TrimSpace(s.Narration) != "" || s.AudioSource == AudioUpload
			if spoken && (force || s.Audio.Status != StatusDone || s.Audio.Stale) {
				ids = append(ids, s.ID)
			}
		}
	}
	var failed atomic.Int64
	_ = mapLimit(ids, VoiceConcurrency(e.cfg.Get()), j.cancelled, func(id string, _ int) error {
		if !e.genVoice(j.work, j.st, id) {
			failed.Add(1)
		}
		tick()
		return nil
	})
	return int(failed.Load())
}

func countVoicesToDo(p *Project, force bool) int {
	if !p.Input.Voiceover {
		return 0
	}
	n := 0
	for _, s := range p.Scenes {
		spoken := strings.TrimSpace(s.Narration) != "" || s.AudioSource == AudioUpload
		if spoken && (force || s.Audio.Status != StatusDone || s.Audio.Stale) {
			n++
		}
	}
	return n
}

// runCodePrep is StageFrames of a code film: the voices, then every scene's
// spec and pictures (frames only for the scenes that fall back).
func (e *Engine) runCodePrep(j *jobCtx, force bool) string {
	snap := j.snapshot()
	ids := codeScenes(&snap, force)
	total := countVoicesToDo(&snap, false) + len(ids)
	if total == 0 {
		return "Немає сцен для анімації"
	}
	tick := counter(j, total, "Озвучка й анімації", 0, 100)
	vfail := e.runVoicesFirst(j, false, tick)
	if j.cancelled() {
		return "Зупинено"
	}
	var written, fell, failed atomic.Int64
	// Specs are written SpecConcurrency at a time (each is a ~1 min request).
	// A map film continues the map from scene to scene (the last view, the
	// countries' colours): its first scene is written alone, then each spec
	// continues the nearest earlier one already written (codePrompt).
	writers := SpecConcurrency(e.cfg.Get())
	prep := func(id string, _ int) error {
		defer tick()
		ok, err := e.prepareCode(j.ctx, j.work, j.st, id, false)
		switch {
		case err != nil && !isCancel(err):
			failed.Add(1)
			e.fail(j.st, assetRef{slotVideo, id}, err) // exact error on the scene; «Повторити» retries it
		case ok:
			written.Add(1)
		case err == nil:
			fell.Add(1)
		}
		return nil
	}
	rest := ids
	if cleanGrammarID(snap.Input.CodeGrammar) == "map" && writers > 1 && len(ids) > 1 && needsSpecFirst(&snap, ids[0]) {
		_ = prep(ids[0], 0)
		rest = ids[1:]
	}
	if !j.cancelled() {
		_ = mapLimit(rest, writers, j.cancelled, prep)
	}
	e.update(j.st, func(p *Project) { p.Step = max(p.Step, 3) })
	if j.cancelled() {
		return "Зупинено"
	}
	msg := fmt.Sprintf("Анімації написано: %d", written.Load())
	if n := fell.Load(); n > 0 {
		msg += fmt.Sprintf(" · з кадру замість анімації: %d (причина — на картці сцени)", n)
	}
	if n := failed.Load(); n > 0 {
		msg += fmt.Sprintf(" · помилок: %d (причина — на картці сцени)", n)
	}
	if vfail > 0 {
		msg += fmt.Sprintf(" · озвучок з помилкою: %d", vfail)
	}
	return msg
}

// needsSpecFirst: scene sid of p will be written (it has no usable spec).
func needsSpecFirst(p *Project, sid string) bool {
	s := findScene(p, sid)
	return s != nil && codeNeedsSpec(p, s) != ""
}

// runCodeRender is StageRender of a code film: missing voices, then each
// scene's clip (its spec and pictures first when missing) as soon as its
// voice is ready — 1–2 renders at a time.
func (e *Engine) runCodeRender(j *jobCtx, videos, voices, force bool) string {
	snap := j.snapshot()
	var ids []string
	if videos {
		ids = codeScenes(&snap, false)
	}
	nv := 0
	if voices {
		nv = countVoicesToDo(&snap, force && !videos)
	}
	total := nv + len(ids)
	if total == 0 {
		return "Усе вже згенеровано"
	}
	if len(ids) > 0 {
		if _, err := e.coder().Available(j.ctx); err != nil {
			msg := "Анімація кодом недоступна: " + e.failureText(err)
			for _, id := range ids {
				e.fail(j.st, assetRef{slotVideo, id}, errors.New(msg))
			}
			return msg
		}
	}
	tick := counter(j, total, "Озвучка й анімація", 0, 100)
	vfail := 0
	if voices {
		vfail = e.runVoicesFirst(j, force && !videos, tick)
	}
	if j.cancelled() {
		return "Зупинено"
	}
	number := map[string]int{}
	for i, s := range snap.Scenes {
		number[s.ID] = i + 1
	}
	var failed, fell atomic.Int64
	_ = mapLimit(ids, CodeConcurrency(), j.cancelled, func(id string, _ int) error {
		defer tick()
		e.setJobNote(j, fmt.Sprintf("Анімація сцени %d…", number[id]))
		if !e.genCode(j.ctx, j.work, j.st, id, force) {
			if !j.cancelled() {
				failed.Add(1)
			}
			return nil
		}
		fell.Add(int64(boolInt(e.sceneFellBack(j.st, id))))
		return nil
	})
	e.update(j.st, func(p *Project) { p.Step = max(p.Step, 4) })
	if j.cancelled() {
		return "Зупинено"
	}
	msg := fmt.Sprintf("Анімації готові (%d)", len(ids)-int(failed.Load())-int(fell.Load()))
	if f := failed.Load(); f > 0 {
		msg = fmt.Sprintf("Анімацій: %d/%d, помилок: %d", len(ids)-int(f)-int(fell.Load()), len(ids), f)
	}
	if n := fell.Load(); n > 0 {
		msg += fmt.Sprintf(" · з кадру замість анімації: %d", n)
	}
	if vfail > 0 {
		msg += fmt.Sprintf(" · озвучок з помилкою: %d", vfail)
	}
	return msg
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (e *Engine) sceneFellBack(st *proj, sid string) bool {
	fell := false
	e.read(st, func(p *Project) bool {
		if s := findScene(p, sid); s != nil {
			fell = s.Code.Fallback
		}
		return true
	})
	return fell
}

// ---- single actions (step 3 «Анімація») ------------------------------------------------------

// codeAction runs fn for scene sid on its own goroutine (outside the job),
// with the clip marked queued while it waits.
func (e *Engine) codeAction(id, sid string, mark func(s *Scene) error, fn func(st *proj)) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	st.mu.Lock()
	var err error
	s := findScene(st.p, sid)
	switch {
	case st.deleted.Load():
		err = ErrNotFound
	case e.closing.Load():
		err = ErrClosing
	case s == nil:
		err = errors.New("сцену не знайдено")
	case !st.p.Input.CodeAnim():
		err = errors.New("Це не фільм у режимі «Анімація кодом»")
	default:
		err = mark(s)
	}
	list := false
	if err == nil {
		list = e.commitLocked(st)
		st.bg.Add(1)
	}
	st.mu.Unlock()
	if err != nil {
		return err
	}
	e.emit(st, list)
	go func() {
		defer st.bg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[studio:%s] code action panic: %v\n%s", id, r, debug.Stack())
			}
		}()
		fn(st)
		if err := e.writeNow(st); err != nil {
			log.Printf("[studio:%s] %v", id, err)
		}
	}()
	return nil
}

// RewriteCode writes the scene's animation again with the text model (the
// voice's timing when it exists, else an estimate), draws its pictures and
// renders the preview still. The user's edits of the spec are replaced.
func (e *Engine) RewriteCode(id, sid string) error {
	return e.codeAction(id, sid, func(s *Scene) error {
		if busyAsset(s.Video) || busyAsset(s.Code.Preview) {
			return errors.New("Ця сцена вже генерується — зачекай")
		}
		s.Code.Preview.Status, s.Code.Preview.Error = StatusQueued, ""
		return nil
	}, func(st *proj) {
		e.update(st, func(p *Project) {
			if s := findScene(p, sid); s != nil {
				s.Code.Preview.Status = StatusRunning
			}
		})
		ok, err := e.prepareCode(st.ctx, st.ctx, st, sid, true)
		if err != nil || !ok {
			msg := "анімацію не написано"
			if err != nil {
				msg = e.failureText(err)
			}
			e.update(st, func(p *Project) {
				if s := findScene(p, sid); s != nil {
					s.Code.Preview.Status, s.Code.Preview.Error = StatusError, firstNonBlank(s.Code.Error, msg)
					if s.Code.Preview.Path != "" {
						s.Code.Preview.Status = StatusDone
					}
				}
			})
			return
		}
		e.update(st, func(p *Project) {
			if s := findScene(p, sid); s != nil && s.Video.Status == StatusDone {
				s.Video.Stale = true
			}
		})
		e.stillCode(st, sid, -1)
	})
}

// CodePreview renders a still of the scene's spec at second at (< 0 = the
// moment after its last cue lands) — «Попередній перегляд кадру». Missing
// pictures are drawn first.
func (e *Engine) CodePreview(id, sid string, at float64) error {
	return e.codeAction(id, sid, func(s *Scene) error {
		if len(s.Code.Spec) == 0 {
			return errors.New("У сцени ще немає анімації — спершу напиши її")
		}
		if busyAsset(s.Code.Preview) {
			return errors.New("Кадр уже малюється — зачекай")
		}
		s.Code.Preview.Status, s.Code.Preview.Error = StatusQueued, ""
		return nil
	}, func(st *proj) { e.stillCode(st, sid, at) })
}

func busyAsset(a Asset) bool { return a.Status == StatusRunning || a.Status == StatusQueued }

// stillCode renders the preview still of a scene.
func (e *Engine) stillCode(st *proj, sid string, at float64) {
	setErr := func(err error) {
		msg := e.failureText(err)
		e.update(st, func(p *Project) {
			if s := findScene(p, sid); s != nil {
				a := &s.Code.Preview
				a.Status, a.Error = StatusError, msg
			}
		})
	}
	e.update(st, func(p *Project) {
		if s := findScene(p, sid); s != nil {
			s.Code.Preview.Status = StatusRunning
		}
	})
	if err := e.drawCodeImages(st.ctx, st, sid); err != nil {
		setErr(err)
		return
	}
	var spec []byte
	var err error
	dur := 0.0
	e.read(st, func(p *Project) bool {
		if s := findScene(p, sid); s != nil {
			spec, err = resolvedSpec(&s.Code)
			dur = s.Code.Duration
			if parsed, perr := parseCodeSpec(s.Code.Spec); perr == nil {
				if d, ok := num(parsed["duration"]); ok && d > 0 {
					dur = d // what the spec says (an edited spec may not be timed yet)
				}
				if at < 0 {
					at = previewMoment(parsed, dur)
				}
			}
		}
		return true
	})
	if err != nil || spec == nil {
		setErr(firstErr(err, errors.New("опис анімації порожній")))
		return
	}
	at = math.Max(0, math.Min(at, math.Max(0, dur-1.0/codeFPS)))
	name := fmt.Sprintf("code-preview-%s-%d.png", safeName(sid), nowMs())
	dir := filepath.Join(st.dir, codeDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		setErr(err)
		return
	}
	if err := e.coder().Still(st.ctx, spec, at, filepath.Join(dir, name), CodeRenderOptions{AssetsDir: st.dir}); err != nil {
		_ = os.Remove(filepath.Join(dir, name))
		setErr(fmt.Errorf("кадр анімації: %w", err))
		return
	}
	rel := codeDir + "/" + name
	var drop []string
	e.update(st, func(p *Project) {
		s := findScene(p, sid)
		if s == nil {
			drop = append(drop, rel)
			return
		}
		a := &s.Code.Preview
		if a.Path != "" && a.Path != rel {
			drop = append(drop, a.Path)
		}
		a.Path, a.Status, a.Error, a.Stale, a.UpdatedAt = rel, StatusDone, "", false, nowMs()
		s.Code.PreviewAt = round2(at)
	})
	e.removeFiles(st, drop)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// previewMoment is a telling moment of a spec: just after its last cue
// lands (everything is on screen), inside the clip.
func previewMoment(s codeSpec, dur float64) float64 {
	last := 0.0
	for _, c := range cuesOf(s) {
		if at, ok := num(c["at"]); ok && at > last {
			last = at
		}
	}
	at := last + 1.2
	if dur > 0 {
		at = math.Min(at, dur-0.1)
	}
	return math.Max(0, at)
}

// ---- grammar previews («Граматика анімації» picker) ---------------------------------------------

var grammarPreviewMu sync.Mutex

// GrammarPreview is a still of a grammar's own example in the given format
// ("16:9" | "9:16"), rendered once and kept in the data folder
// (<data>/.cache/codeanim/<grammar>-<w>x<h>.png); the path is returned.
func (e *Engine) GrammarPreview(ctx context.Context, grammar, aspect string) (string, error) {
	g, ok := e.CodeGrammarByID(grammar)
	if !ok {
		return "", fmt.Errorf("граматики «%s» немає", grammar)
	}
	w, h := codeSize(Input{Aspect: aspect})
	e.mu.Lock()
	root := e.dataDir
	e.mu.Unlock()
	dir := filepath.Join(root, ".cache", "codeanim")
	out := filepath.Join(dir, fmt.Sprintf("%s-%dx%d.png", safeName(g.ID), w, h))
	grammarPreviewMu.Lock()
	defer grammarPreviewMu.Unlock()
	if isFile(out) {
		return out, nil
	}
	if len(g.Example) == 0 {
		return "", errors.New("у граматики немає прикладу")
	}
	spec, err := parseCodeSpec(g.Example)
	if err != nil {
		return "", err
	}
	dur, ok := num(spec["duration"])
	if !ok || dur <= 0 {
		dur = 6
	}
	spec["width"], spec["height"] = w, h
	b, _ := json.Marshal(spec)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := out + ".part.png"
	if err := e.coder().Still(ctx, b, previewMoment(spec, dur), tmp, CodeRenderOptions{AssetsDir: dir}); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

// ---- montage, gaps, edits ---------------------------------------------------------------------

// codeGaps are the scenes of a code film shown as their frame instead of
// the animation (Montage.Gaps, with the exact reason).
func codeGaps(p *Project) []MontageGap {
	if !p.Input.CodeAnim() {
		return nil
	}
	var out []MontageGap
	for i, s := range p.Scenes {
		if s.Code.Fallback && s.Frame.Path != "" {
			out = append(out, MontageGap{SceneID: s.ID, Number: i + 1, NoClip: true, Action: GapStill,
				Reason: "анімація кодом: " + firstNonBlank(s.Code.Error, "не вдалась")})
		}
	}
	return out
}

// reconcileCode applies the edit rules of the code animation: the studio
// owns the pictures, preview, timing and bookkeeping; the user owns the
// grammar choices and the spec. A changed spec is marked Edited and makes
// the clip stale; a spec that is not JSON is refused (the old one stays); a
// changed grammar choice makes the next render write a new spec.
func reconcileCode(before, p *Project) {
	in := &p.Input
	in.CodeGrammar = cleanGrammarID(in.CodeGrammar)
	old := make(map[string]*Scene, len(before.Scenes))
	for i := range before.Scenes {
		old[before.Scenes[i].ID] = &before.Scenes[i]
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		c := &s.Code
		c.Grammar = cleanGrammarID(c.Grammar)
		o := old[s.ID]
		if o == nil {
			*c = CodeClip{Grammar: c.Grammar}
			continue
		}
		oc := o.Code
		c.Suggested, c.Duration, c.Estimated, c.Words, c.Choice = oc.Suggested, oc.Duration, oc.Estimated, oc.Words, oc.Choice
		c.Images, c.Fallback, c.Error, c.Rendered, c.Preview = oc.Images, oc.Fallback, oc.Error, oc.Rendered, oc.Preview
		c.PreviewAt = math.Max(0, c.PreviewAt)
		if math.IsNaN(c.PreviewAt) || math.IsInf(c.PreviewAt, 0) {
			c.PreviewAt = oc.PreviewAt
		}
		switch {
		case bytes.Equal(bytes.TrimSpace(c.Spec), bytes.TrimSpace(oc.Spec)):
			c.Spec, c.Edited = oc.Spec, oc.Edited
		case len(bytes.TrimSpace(c.Spec)) == 0:
			c.Spec, c.Edited = nil, false // cleared: written again
		default:
			spec, err := parseCodeSpec(c.Spec)
			if err != nil {
				c.Spec, c.Edited = oc.Spec, oc.Edited
				break
			}
			c.Spec, c.Edited = marshalSpec(spec), true
			if s.Video.Status == StatusDone {
				s.Video.Stale = true
			}
			if c.Preview.Status == StatusDone {
				c.Preview.Stale = true
			}
		}
		if CodeGrammarOf(p, s) != CodeGrammarOf(before, o) && p.Input.CodeAnim() {
			// Another grammar: the next render writes a new spec.
			c.Edited = false
			if s.Video.Status == StatusDone {
				s.Video.Stale = true
			}
		}
	}
}

var grammarIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// cleanGrammarID keeps a well-formed grammar id ("" otherwise = auto).
func cleanGrammarID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if !grammarIDRe.MatchString(id) {
		return ""
	}
	return id
}

// codeAssets are the files of a scene's code animation (pictures, preview).
func codeAssets(s *Scene) []string {
	var out []string
	for _, im := range s.Code.Images {
		if im.Image.Path != "" {
			out = append(out, im.Image.Path)
		}
	}
	if s.Code.Preview.Path != "" {
		out = append(out, s.Code.Preview.Path)
	}
	return out
}

// cloneCode deep-copies a scene's code animation.
func cloneCode(c CodeClip) CodeClip {
	if c.Spec != nil {
		c.Spec = append(json.RawMessage(nil), c.Spec...)
	}
	if c.Images != nil {
		c.Images = append(make([]CodeImage, 0, len(c.Images)), c.Images...)
	}
	return c
}

// normalizeCode resets what cannot survive a restart (running pictures and
// previews).
func normalizeCode(p *Project, reset func(a *Asset)) {
	for i := range p.Scenes {
		c := &p.Scenes[i].Code
		for k := range c.Images {
			reset(&c.Images[k].Image)
		}
		if c.Preview.Status != "" || c.Preview.Path != "" {
			reset(&c.Preview)
		}
	}
}

// CodeSceneState is how far a scene of a code film is, for the screens:
// "fallback" (its frame instead), "ready" (the clip), "spec" (written, not
// rendered), "running", "error", "" (nothing yet).
func CodeSceneState(s *Scene) string {
	switch {
	case s.Code.Fallback:
		return "fallback"
	case busyAsset(s.Video):
		return "running"
	case s.Video.Status == StatusError:
		return "error"
	case s.Video.Status == StatusDone && !s.Video.Stale && s.Code.Rendered != "":
		return "ready"
	case len(s.Code.Spec) > 0:
		return "spec"
	}
	return ""
}

// CodeSpecCues lists a spec's cues for the friendly editor: time, kind and
// the texts (nil when the spec is not a valid object).
type CodeCue struct {
	At        float64
	Kind      string
	Text, Sub string
	Image     string
}

// CodeSpecDuration is the duration a spec states (0 = none / unreadable).
func CodeSpecDuration(raw json.RawMessage) float64 {
	s, err := parseCodeSpec(raw)
	if err != nil {
		return 0
	}
	d, _ := num(s["duration"])
	return math.Max(0, d)
}

// CodeCues reads the cues of a spec (the editor's fields).
func CodeCues(raw json.RawMessage) ([]CodeCue, map[string]any, bool) {
	s, err := parseCodeSpec(raw)
	if err != nil {
		return nil, nil, false
	}
	var out []CodeCue
	for _, c := range cuesOf(s) {
		at, _ := num(c["at"])
		out = append(out, CodeCue{At: at, Kind: jsString(c["kind"]), Text: jsString(c["text"]), Sub: jsString(c["sub"]), Image: jsString(c["image"])})
	}
	data, _ := s["data"].(map[string]any)
	return out, data, true
}

// SetCodeCue changes cue k of a spec (the editor's fields): text, sub and
// the time; the rest of the spec is kept. ok = the spec was readable.
func SetCodeCue(raw json.RawMessage, k int, at float64, text, sub string) (json.RawMessage, bool) {
	s, err := parseCodeSpec(raw)
	if err != nil {
		return raw, false
	}
	cues := cuesOf(s)
	if k < 0 || k >= len(cues) {
		return raw, false
	}
	c := cues[k]
	if !math.IsNaN(at) && !math.IsInf(at, 0) {
		c["at"] = round2(math.Max(0, at))
	}
	setText := func(key, v string) {
		if strings.TrimSpace(v) == "" {
			delete(c, key)
			return
		}
		c[key] = v
	}
	setText("text", text)
	setText("sub", sub)
	return marshalSpec(s), true
}

// ParseCueTime reads "1.5", "1,5" or "0:01.5" as seconds.
func ParseCueTime(v string) (float64, bool) {
	v = strings.TrimSpace(strings.ReplaceAll(v, ",", "."))
	v = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(v, "с")), "s")
	if m, sec, ok := strings.Cut(v, ":"); ok {
		mm, err1 := strconv.Atoi(strings.TrimSpace(m))
		ss, err2 := strconv.ParseFloat(strings.TrimSpace(sec), 64)
		if err1 != nil || err2 != nil || ss < 0 {
			return 0, false
		}
		return float64(mm)*60 + ss, true
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	return f, err == nil && f >= 0 && !math.IsInf(f, 0)
}
