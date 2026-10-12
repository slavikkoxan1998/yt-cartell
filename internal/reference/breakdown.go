package reference

// The breakdown: what a model says about how the reference is built.
//
//	short video (≤ 12 min, or length unknown)
//	          one look: the overview (hook, structure, style, sound, pacing,
//	          people, sound mode) AND every scene with its timestamps
//	long video (≤ 90 min)
//	          an overview of the whole video at 0.2 fps and low resolution,
//	          then the scenes part by part (10 minutes each, clipped with
//	          videoMetadata, low resolution past 30 min) — merged in order
//	any look that leaves the scenes short of the end (an answer cut off at
//	its token limit) is continued with parts from where it stopped
//
// Answers are read leniently: fences and prose around the JSON, numbers as
// strings, "1:15" times, a reply cut off mid-way (closed and trimmed back to
// its last whole element). Timestamps of a clipped part that come back
// relative to the clip are shifted to the whole video.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Breakdown is the reference video as a model saw it. Descriptive fields are
// Ukrainian; quoted speech and on-screen text stay in the original language.
type Breakdown struct {
	Seen        bool     `json:"seen"`
	Title       string   `json:"title,omitempty"`
	Duration    float64  `json:"durationSeconds"`
	Language    string   `json:"language,omitempty"`
	Aspect      string   `json:"aspect,omitempty"`
	FirstWords  string   `json:"firstWords,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Hook        Hook     `json:"hook"`
	Structure   []Beat   `json:"structure,omitempty"`
	Scenes      []Scene  `json:"scenes"`
	CutsPerMin  float64  `json:"cutsPerMinute,omitempty"`
	AvgShot     float64  `json:"avgShotSeconds,omitempty"`
	People      string   `json:"people,omitempty"`    // characters | nopeople | motion | mixed
	AudioMode   string   `json:"audioMode,omitempty"` // voiceover | invideo | silent
	Narration   string   `json:"narrationStyle,omitempty"`
	Music       string   `json:"music,omitempty"`
	VisualStyle string   `json:"visualStyle,omitempty"`
	Palette     string   `json:"palette,omitempty"`
	Pacing      string   `json:"pacing,omitempty"`
	Why         []string `json:"whyItWorks,omitempty"`
	Via         string   `json:"via,omitempty"`   // the watcher's label
	Looks       int      `json:"looks,omitempty"` // requests it took
}

// Hook is the opening.
type Hook struct {
	End  float64 `json:"end"`
	What string  `json:"what,omitempty"`
	Why  string  `json:"why,omitempty"`
}

// Beat is a part of the structure.
type Beat struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Name    string  `json:"name"`
	Purpose string  `json:"purpose,omitempty"`
}

// Scene is one continuous moment of the reference.
type Scene struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Shot       string  `json:"shot,omitempty"`
	Visual     string  `json:"visual,omitempty"`
	Text       string  `json:"onScreenText,omitempty"`
	Transition string  `json:"transition,omitempty"`
	Speaker    string  `json:"speaker,omitempty"`
	Speech     string  `json:"speech,omitempty"`
	Sound      string  `json:"sound,omitempty"`
}

// Lengths of the looks (seconds).
const (
	singleMax = 12 * 60 // one look up to this length
	partLen   = 10 * 60 // the parts of a long video
	maxScenes = 400
)

// ---- prompts ---------------------------------------------------------------------------------

const systemPrompt = `You are a senior video editor and story analyst. You WATCH and LISTEN to the attached YouTube video carefully, from the first to the last second, and describe exactly how it is built, so that a production studio can recreate it scene by scene.
Rules:
- Describe only what you actually see and hear. Never guess or invent. If you cannot open or play the video, return {"seen": false} and nothing else.
- Timestamps are seconds from the start of the WHOLE video, as numbers (e.g. 83.5).
- Write every description in UKRAINIAN; quoted speech and on-screen text stay in the original language.
- Return ONLY one JSON object — no markdown, no comments.`

const sceneShape = `{
    "start": seconds, "end": seconds,
    "shot": shot size + camera angle + camera movement (e.g. "крупний план, знизу, повільний наїзд"; very fast cuts of one moment → "швидкі склейки: …"),
    "visual": who/what is in frame, how they look, what they do, and the setting (place, time of day, light) — specific, 15–40 words,
    "onScreenText": the exact on-screen text or captions ("" if none),
    "transition": the transition into the next scene (cut, fade, zoom, swipe, match cut…),
    "speaker": "voice-over" | "on-camera" | the speaker's role or name | "" (nobody speaks),
    "speech": what is said in this scene, verbatim in the original language (if longer than 40 words — its key sentences),
    "sound": music, sound effects and ambience in this scene
  }`

const sceneRules = `Scenes: one entry per scene — a continuous moment, usually 2–12 seconds; merge very fast cuts of the same moment into one scene, but start a new scene whenever the place, subject, action or speaker changes. Scenes follow each other in time without gaps or overlaps.`

func overviewFields(withScenes bool) string {
	scenes := `  "scenes": [ ` + sceneShape + ` ],` + "\n"
	if !withScenes {
		scenes = `  "scenes": [] (the scenes are requested separately, part by part),` + "\n"
	}
	return `{
  "seen": true,
  "title": the video's title if shown or known, else a short descriptive title,
  "durationSeconds": the exact length of the whole video in seconds,
  "language": ISO 639-1 code of the main spoken language ("" if nobody speaks),
  "aspect": "16:9" | "9:16" | "1:1" | "4:3" (the shape of the picture),
  "firstWords": the first spoken words, verbatim (max 12 words; "" if none),
  "summary": what the video is about, 1–2 sentences,
  "hook": {"end": seconds where the hook ends (usually 3–10), "what": what happens in the hook, "why": why it grabs attention},
  "structure": [ {"start": seconds, "end": seconds, "name": the beat's name, "purpose": what the beat does} ] (3–12 beats covering the whole video),
` + scenes + `  "cutsPerMinute": cuts per minute (count them),
  "avgShotSeconds": the average shot length in seconds,
  "people": "characters" (people or characters on screen) | "nopeople" (places, objects, nature, b-roll without people) | "motion" (motion graphics, animated text, infographics) | "mixed",
  "audioMode": "voiceover" (a narrator off screen) | "invideo" (people speak on camera) | "silent" (music and ambience only),
  "narrationStyle": who narrates, tone, energy, sentence length, speaking pace, direct address, catchphrases,
  "music": the music's genre, mood and tempo and where it changes; sound effects,
  "visualStyle": the medium (live action, stock footage, 3D, 2D animation, screen recording…), look, lighting, colour grading, typography and graphics,
  "palette": the main colours,
  "pacing": how the rhythm changes over the video,
  "whyItWorks": 3–6 short points
}`
}

// fullPrompt asks for everything in one look.
func fullPrompt() string {
	return "Return this JSON object:\n" + overviewFields(true) + "\n" + sceneRules + " The scenes must cover the whole video, from 0 to its end."
}

// overviewPrompt asks for everything but the scenes (a long video).
func overviewPrompt() string {
	return "This is a long video: describe the whole of it, but leave the scenes for later. Return this JSON object:\n" + overviewFields(false)
}

// partPrompt asks for the scenes of [from, to].
func partPrompt(from, to float64) string {
	return fmt.Sprintf(`Watch ONLY the part of the video from %s to %s (%.0f–%.0f s). Return this JSON object:
{
  "seen": true,
  "durationSeconds": the length of the WHOLE video if you know it, else 0,
  "scenes": [ %s ]
}
%s List only the scenes of this part, with timestamps counted from the start of the WHOLE video.`,
		Clock(from), Clock(to), from, to, sceneShape, sceneRules)
}

const retryNote = "\n\nIMPORTANT: your previous answer could not be read as the JSON object asked for. Answer again with ONE valid JSON object only — no prose, no code fences."

// ---- analysis ------------------------------------------------------------------------------------

// Options of Analyze.
type Options struct {
	URL  string
	Meta Meta // what Probe found (Duration 0 = unknown)
	// Progress (may be nil) gets 0–100 and a short Ukrainian message, also
	// while a look is under way (an estimate that creeps on).
	Progress func(pct float64, msg string)
}

// Analyze lets w watch the video and returns its breakdown.
func Analyze(ctx context.Context, w Watcher, o Options) (Breakdown, error) {
	progress := o.Progress
	if progress == nil {
		progress = func(float64, string) {}
	}
	dur := o.Meta.Duration
	if dur > MaxDuration {
		return Breakdown{}, tooLong(dur)
	}
	long := dur > singleMax
	first := Request{URL: o.URL, System: systemPrompt, Prompt: fullPrompt()}
	firstSpan := 92.0
	if long {
		first.Prompt, first.FPS, first.LowRes = overviewPrompt(), 0.2, true
		firstSpan = 25
	}
	looks := 1
	b, err := look(ctx, w, first, func(p float64) { progress(p*firstSpan/100, "Дивиться відео…") }, estimate(dur, first))
	if err != nil {
		return Breakdown{}, err
	}
	if !b.Seen {
		return Breakdown{}, newError(KindNotSeen, "seen=false")
	}
	total := dur
	if total <= 0 {
		total = b.Duration
	}
	if total > MaxDuration {
		return Breakdown{}, tooLong(total)
	}
	b.Duration = total
	b.Scenes = cleanScenes(b.Scenes, total)

	// The scenes the first look left out, part by part.
	covered := 0.0
	if n := len(b.Scenes); n > 0 {
		covered = b.Scenes[n-1].End
	}
	if total > 60 && covered < total*0.9 {
		start := math.Floor(covered)
		parts := int(math.Ceil((total - start) / partLen))
		done := firstSpan
		for k := 0; start < total-3 && k < 12; k++ {
			end := math.Min(total, start+partLen)
			r := Request{URL: o.URL, System: systemPrompt, Prompt: partPrompt(start, end), Start: start, End: end, LowRes: total > 30*60}
			span := (98 - done) / float64(max(1, parts-k))
			msg := fmt.Sprintf("Дивиться частину %s–%s…", Clock(start), Clock(end))
			from := done
			pb, err := look(ctx, w, r, func(p float64) { progress(from+p*span/100, msg) }, estimate(end-start, r))
			looks++
			if err != nil {
				return Breakdown{}, err
			}
			scenes := shiftRelative(pb.Scenes, start, end)
			var kept []Scene
			for _, s := range scenes {
				if s.End > start+0.5 && s.Start < end+2 {
					kept = append(kept, s)
				}
			}
			if len(kept) == 0 { // nothing for this part: do not loop on it
				start = end
			} else {
				b.Scenes = cleanScenes(append(b.Scenes, kept...), total)
				start = math.Max(end, b.Scenes[len(b.Scenes)-1].End)
				if start >= end-1 {
					start = end
				}
			}
			done += span
		}
	}
	if len(b.Scenes) == 0 {
		return Breakdown{}, &Error{Kind: KindBadReply, Msg: "Модель не описала сцени відео — спробуй ще раз."}
	}
	if b.CutsPerMin <= 0 && total > 0 {
		b.CutsPerMin = math.Round(float64(len(b.Scenes)-1)/(total/60)*10) / 10
	}
	if b.AvgShot <= 0 && len(b.Scenes) > 0 {
		b.AvgShot = math.Round(total/float64(len(b.Scenes))*10) / 10
	}
	if b.Aspect == "" && o.Meta.Vertical {
		b.Aspect = "9:16"
	}
	if b.Title == "" {
		b.Title = o.Meta.Title
	}
	b.Via, b.Looks = w.Label(), looks
	progress(100, "Розбір готовий")
	return b, nil
}

// estimate is how long a look may take (for the creeping progress).
func estimate(dur float64, r Request) time.Duration {
	if r.End > 0 {
		dur = r.End - r.Start
	}
	if dur <= 0 {
		dur = 300
	}
	return time.Duration(20+dur/8) * time.Second
}

// look makes one request (and one more when the answer is not JSON).
func look(ctx context.Context, w Watcher, r Request, progress func(pct float64), est time.Duration) (Breakdown, error) {
	stop := creep(ctx, progress, est)
	defer stop()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		rr := r
		if attempt > 0 {
			rr.Prompt += retryNote
		}
		raw, err := w.Watch(ctx, rr)
		if err != nil {
			return Breakdown{}, err
		}
		b, err := ParseBreakdown(raw)
		if err == nil {
			return b, nil
		}
		lastErr = err
	}
	return Breakdown{}, lastErr
}

// creep reports a progress that approaches 95 % of the slice over est.
func creep(ctx context.Context, progress func(float64), est time.Duration) (stop func()) {
	progress(1)
	done := make(chan struct{})
	exited := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(exited)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				x := time.Since(start).Seconds() / est.Seconds()
				progress(95 * (1 - math.Exp(-2*x)))
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-exited
		})
	}
}

// shiftRelative moves scenes a clipped look timed from the clip's start to
// the whole video's time.
func shiftRelative(scenes []Scene, from, to float64) []Scene {
	if from < 30 || len(scenes) == 0 {
		return scenes
	}
	minStart, maxEnd := math.Inf(1), 0.0
	for _, s := range scenes {
		minStart = math.Min(minStart, s.Start)
		maxEnd = math.Max(maxEnd, s.End)
	}
	if minStart >= from-5 || maxEnd > (to-from)+15 {
		return scenes // already absolute
	}
	out := make([]Scene, len(scenes))
	for i, s := range scenes {
		s.Start += from
		s.End += from
		out[i] = s
	}
	return out
}

// cleanScenes sorts the scenes, fixes missing or crossed ends, drops
// duplicates and empty ones and keeps them inside [0, total].
func cleanScenes(in []Scene, total float64) []Scene {
	var s []Scene
	for _, x := range in {
		if x.Start < 0 || strings.TrimSpace(x.Visual+x.Speech+x.Shot+x.Text) == "" {
			continue
		}
		s = append(s, x)
	}
	sort.SliceStable(s, func(i, j int) bool { return s[i].Start < s[j].Start })
	var out []Scene
	for i, x := range s {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if math.Abs(x.Start-last.Start) < 0.5 { // the same scene twice (overlapping parts)
				continue
			}
		}
		if x.End <= x.Start {
			x.End = x.Start + 3
			if i+1 < len(s) && s[i+1].Start > x.Start {
				x.End = s[i+1].Start
			}
		}
		if total > 0 {
			if x.Start >= total {
				continue
			}
			x.End = math.Min(x.End, total)
		}
		if len(out) > 0 && out[len(out)-1].End > x.Start {
			out[len(out)-1].End = x.Start
		}
		out = append(out, x)
		if len(out) >= maxScenes {
			break
		}
	}
	return out
}

// ---- parsing ---------------------------------------------------------------------------------

// ParseBreakdown reads a model's answer.
func ParseBreakdown(raw string) (Breakdown, error) {
	obj, err := parseObject(raw)
	if err != nil {
		if reRefusal.MatchString(raw) {
			return Breakdown{}, newError(KindNotSeen, clip(raw, 200))
		}
		return Breakdown{}, &Error{Kind: KindBadReply, Msg: messages[KindBadReply], Detail: clip(raw, 300)}
	}
	if inner, ok := obj["breakdown"].(map[string]any); ok {
		obj = inner
	}
	b := Breakdown{Seen: true}
	if v, ok := obj["seen"].(bool); ok {
		b.Seen = v
	}
	str := func(m map[string]any, k string) string { return strings.TrimSpace(asString(m[k])) }
	b.Title = str(obj, "title")
	b.Duration = math.Max(0, seconds(obj["durationSeconds"]))
	b.Language = strings.ToLower(str(obj, "language"))
	if i := strings.IndexAny(b.Language, "-_"); i > 0 {
		b.Language = b.Language[:i]
	}
	b.Aspect = normAspect(str(obj, "aspect"))
	b.FirstWords = str(obj, "firstWords")
	b.Summary = str(obj, "summary")
	if h, ok := obj["hook"].(map[string]any); ok {
		b.Hook = Hook{End: math.Max(0, seconds(h["end"])), What: str(h, "what"), Why: str(h, "why")}
	}
	for _, v := range asList(obj["structure"]) {
		if m, ok := v.(map[string]any); ok {
			bt := Beat{Start: math.Max(0, seconds(m["start"])), End: math.Max(0, seconds(m["end"])), Name: str(m, "name"), Purpose: str(m, "purpose")}
			if bt.Name != "" || bt.Purpose != "" {
				b.Structure = append(b.Structure, bt)
			}
		}
	}
	for _, v := range asList(obj["scenes"]) {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		b.Scenes = append(b.Scenes, Scene{
			Start: seconds(m["start"]), End: seconds(m["end"]),
			Shot: str(m, "shot"), Visual: str(m, "visual"), Text: str(m, "onScreenText"), Transition: str(m, "transition"),
			Speaker: str(m, "speaker"), Speech: str(m, "speech"), Sound: str(m, "sound"),
		})
	}
	b.CutsPerMin = math.Max(0, seconds(obj["cutsPerMinute"]))
	b.AvgShot = math.Max(0, seconds(obj["avgShotSeconds"]))
	b.People = oneOfOr(strings.ToLower(str(obj, "people")), "", "characters", "nopeople", "motion", "mixed")
	b.AudioMode = oneOfOr(strings.ToLower(str(obj, "audioMode")), "", "voiceover", "invideo", "silent")
	b.Narration = str(obj, "narrationStyle")
	b.Music = str(obj, "music")
	b.VisualStyle = str(obj, "visualStyle")
	b.Palette = str(obj, "palette")
	b.Pacing = str(obj, "pacing")
	for _, v := range asList(obj["whyItWorks"]) {
		if s := strings.TrimSpace(asString(v)); s != "" {
			b.Why = append(b.Why, s)
		}
	}
	if !b.Seen {
		return b, nil
	}
	if b.Duration == 0 && len(b.Scenes) == 0 && b.Summary == "" {
		return Breakdown{}, &Error{Kind: KindBadReply, Msg: messages[KindBadReply], Detail: clip(raw, 300)}
	}
	return b, nil
}

func oneOfOr(v, fallback string, allowed ...string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return fallback
}

func normAspect(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), "x", ":"), "×", ":")
	switch s {
	case "16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "4:5":
		return s
	}
	return ""
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprint(x)
	case []any:
		var parts []string
		for _, p := range x {
			if s := asString(p); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "; ")
	}
	return ""
}

func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any: // {"scenes": {...}} with one item, or keyed items
		for _, k := range []string{"items", "scenes", "list"} {
			if l, ok := x[k].([]any); ok {
				return l
			}
		}
	}
	return nil
}

// parseObject finds the JSON object in a model's answer: fences and prose
// around it are skipped, an answer cut off mid-way is closed and trimmed
// back to its last whole element.
func parseObject(raw string) (map[string]any, error) {
	s := raw
	if i := strings.Index(s, "{"); i >= 0 {
		s = s[i:]
	} else {
		return nil, errors.New("no JSON object")
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		var m map[string]any
		if json.Unmarshal([]byte(s[:j+1]), &m) == nil {
			return m, nil
		}
	}
	cand := s
	for tries := 0; tries < 400 && len(cand) > 1; tries++ {
		var m map[string]any
		if json.Unmarshal([]byte(closeJSON(cand)), &m) == nil {
			return m, nil
		}
		cut := strings.LastIndexAny(cand[:len(cand)-1], ",{[")
		if cut <= 0 {
			break
		}
		if cand[cut] == ',' {
			cand = cand[:cut]
		} else {
			cand = cand[:cut+1]
		}
	}
	return nil, errors.New("no JSON object")
}

// closeJSON closes the strings and brackets a cut-off JSON text left open
// and drops a trailing comma or colon.
func closeJSON(s string) string {
	var stack []byte
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	out := s
	if inStr {
		if esc {
			out = out[:len(out)-1]
		}
		out += `"`
	}
	out = strings.TrimRight(out, " \t\r\n")
	for strings.HasSuffix(out, ",") || strings.HasSuffix(out, ":") {
		if strings.HasSuffix(out, ":") {
			out += "null"
			break
		}
		out = strings.TrimRight(strings.TrimSuffix(out, ","), " \t\r\n")
	}
	for k := len(stack) - 1; k >= 0; k-- {
		out += string(stack[k])
	}
	return out
}
