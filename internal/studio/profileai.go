package studio

// AI profile setup: the user describes in free words what videos they make
// ("короткі шортси про космос англійською для підлітків…"); the text model
// fills a whole settings profile from the catalogues. The profile is NOT
// saved — the UI shows it for review and calls SaveProfile on confirmation.
//
// Robustness like the planner: the reply may come in fences or prose, with
// comments, trailing commas or cut off at the end (repaired locally); one
// more request is made when it still is not JSON. Every value is checked
// against the catalogues (ids, or the Ukrainian labels the model may copy
// instead); anything unknown falls back to a sensible default.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ytcartell/internal/config"
	"ytcartell/internal/llm"
	"ytcartell/internal/media"
	"ytcartell/internal/tts"
)

const (
	profileMarker     = "You configure a video-production profile"
	maxProfileReasons = 8
	suggestTimeout    = 120 * time.Second
)

// errNoProfileJSON: the model's reply could not be read as a profile.
var errNoProfileJSON = errors.New("Модель не змогла запропонувати профіль (відповідь не схожа на JSON) — спробуй ще раз або опиши відео інакше")

// imageModels are the image models a profile can pick (ids as in Input.ImageModel).
var imageModels = []Choice{
	{"NARWHAL", "Nano Banana 2", "balanced quality and speed — the default"},
	{"GEM_PIX_2", "Nano Banana Pro", "best quality, slower and more expensive"},
	{"GEM_PIX", "Nano Banana 2 Lite", "fastest and cheapest, simpler images"},
}

// SuggestProfile asks the text model to configure a profile from a free
// description of the user's videos (any language). Every field is filled
// with catalogue ids; invalid answers fall back to sensible defaults. The
// profile is not saved (no id, no timestamps).
func (e *Engine) SuggestProfile(ctx context.Context, description string) (config.Profile, error) {
	p, _, err := e.SuggestProfileWithReasons(ctx, description)
	return p, err
}

// SuggestProfileWithReasons is SuggestProfile plus short Ukrainian reasons
// for the main choices (the "Чому так" list of the review dialog). Reasons
// come from the same model reply; when the model gives none, a short summary
// of the choices is returned instead.
func (e *Engine) SuggestProfileWithReasons(ctx context.Context, description string) (config.Profile, []string, error) {
	desc := strings.TrimSpace(clipRunes(description, 4000))
	if desc == "" {
		return config.Profile{}, nil, errors.New("Опиши, які відео ти робиш: тема, формат, тривалість, мова, настрій — і модель налаштує профіль")
	}
	system, prompt := profilePrompt(desc)
	var reply *jobj
	for attempt := 0; attempt < 2 && reply == nil; attempt++ {
		sys := system
		if attempt > 0 { // retry once with a stricter reminder
			sys += "\n\nIMPORTANT: your previous reply could not be read as JSON. Reply again with ONE valid JSON object only — no prose, no code fences, no comments."
		}
		raw, err := e.text.Text(ctx, llm.Request{System: sys, Prompt: prompt, Temperature: 0.4, Timeout: suggestTimeout})
		if err != nil {
			return config.Profile{}, nil, err
		}
		reply = parseProfileReply(raw)
	}
	if reply == nil {
		return config.Profile{}, nil, errNoProfileJSON
	}
	p, reasons := profileFromReply(reply, desc)
	return p, reasons, nil
}

// ---- prompt ----------------------------------------------------------------------------

func profilePrompt(desc string) (system, prompt string) {
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line(profileMarker + ` for a creator of AI-generated YouTube videos (faceless channels: an AI narrator, generated images and clips).`)
	line(`The user describes in their own words (any language) what videos they make. Choose EVERY setting so that new projects match the description;`)
	line(`when the description says nothing about a setting, choose what suits such a channel best.`)
	line(`Use ONLY ids from the lists below and copy them exactly.`)
	line(``)
	line(`Return ONLY a JSON object:`)
	line(`{`)
	line(`  "name": short profile name in the user's language (2–5 words),`)
	line(`  "targetSeconds": typical video length in seconds (Shorts/Reels/TikTok 15–60, YouTube 180–1800),`)
	line(`  "aspect": "16:9" (horizontal YouTube) or "9:16" (vertical Shorts, Reels, TikTok),`)
	line(`  "style": style id,`)
	line(`  "customStyle": "" — or, only when no listed style fits, an ENGLISH image-style prompt of 1–2 sentences,`)
	line(`  "genre": genre id, or "" when the videos have no single genre,`)
	line(`  "tempo": tempo id,`)
	line(`  "camera": camera id,`)
	line(`  "imageModel": image model id,`)
	line(`  "voiceover": true when an AI narrator voices the videos, false for music-only videos,`)
	line(`  "language": code of the language the videos are narrated in — set it ONLY when the description names that language`)
	line(`              or a foreign audience; "" = the language of each story,`)
	line(`  "tone": tone id, or "" for a neutral storyteller,`)
	line(`  "notes": a short director's brief in the user's language (1–3 sentences): audience, mood, what to always or never do,`)
	line(`  "storyboardMode": "" or "grid3x3",`)
	line(`  "chainFrames": true or false,`)
	line(`  "transition": transition id,`)
	line(`  "transitionDuration": seconds, %.1f–%.1f,`, media.MinTransitionDuration, media.MaxTransitionDuration)
	line(`  "filter": colour look id,`)
	line(`  "effect": motion effect id,`)
	line(`  "subtitles": "none" | "srt" (separate subtitle file) | "burn" (burnt into the video),`)
	line(`  "subtitlePreset": subtitle look id (matters for "burn"),`)
	line(`  "musicVolume": 0–1, background music level (0.1–0.25 under a narrator),`)
	line(`  "ambientVolume": 0–1, level of the clips' own sound,`)
	line(`  "ducking": true to lower the music while the narrator speaks,`)
	line(`  "reasons": 3–6 short sentences in UKRAINIAN explaining the main choices`)
	line(`}`)

	line(``)
	line(`Styles by group ("id" — name — suits):`)
	for _, c := range StyleCategories {
		line(`%s:`, c.Label)
		for _, s := range Styles {
			if s.Category == c.ID {
				line(`  "%s" — %s — %s`, s.ID, s.Label, strings.TrimPrefix(s.Suits, "підходить для "))
			}
		}
	}
	choices := func(title string, list []Choice) {
		line(``)
		line(`%s ("id" — name):`, title)
		for _, c := range list {
			line(`  "%s" — %s`, c.ID, c.Label)
		}
	}
	choices("Genres", Genres)
	choices("Tempos", Tempos)
	choices("Cameras", Cameras)
	choices("Tones of the narration", Tones)
	line(``)
	line(`Image models ("id" — what it is):`)
	for _, m := range imageModels {
		line(`  "%s" — %s: %s`, m.ID, m.Label, m.Hint)
	}
	line(``)
	line(`Languages ("code" English name):`)
	langs := make([]string, len(Languages))
	for i, l := range Languages {
		langs[i] = fmt.Sprintf(`"%s" %s`, l.Code, l.English)
	}
	line(`  %s`, strings.Join(langs, ", "))
	line(``)
	line(`Storyboard modes: "" — one first frame per scene (default, cheapest); "grid3x3" — a 3×3 storyboard sheet per scene that guides`)
	line(`  the Omni Flash video model (richer, planned motion; costs more).`)
	line(`chainFrames: true — every clip continues from the last frame of the previous one (smooth continuity for one continuous story,`)
	line(`  scenes render one after another); false — independent scenes (faster).`)
	media4 := func(title string, n int, at func(i int) (id, label, hint string)) {
		line(``)
		line(`%s ("id" — name — what it does):`, title)
		for i := 0; i < n; i++ {
			id, label, hint := at(i)
			line(`  "%s" — %s — %s`, id, label, hint)
		}
	}
	media4("Transitions between scenes", len(media.Transitions), func(i int) (string, string, string) {
		t := media.Transitions[i]
		return t.ID, t.Label, t.Hint
	})
	media4("Motion effects for still frames", len(media.Effects), func(i int) (string, string, string) {
		x := media.Effects[i]
		return x.ID, x.Label, x.Hint
	})
	media4("Colour looks", len(media.Filters), func(i int) (string, string, string) {
		f := media.Filters[i]
		return f.ID, f.Label, f.Hint
	})
	media4("Subtitle looks", len(media.SubtitlePresets), func(i int) (string, string, string) {
		s := media.SubtitlePresets[i]
		return s.ID, s.Label, s.Hint
	})
	line(``)
	line(`Rules of thumb: vertical short videos → "9:16", 15–60 s, "fast" tempo, burnt-in subtitles with a big look ("shorts"), quick or no transitions.`)
	line(`Long calm videos (sleep, meditation, bedtime) → "slow" tempo, soft transitions, low music. Explainers → clear styles and "srt" subtitles.`)
	line(`Kids → gentle tone, bright friendly style, nothing scary.`)
	return strings.TrimRight(b.String(), "\n"), "What the user says about their videos:\n\n" + desc
}

// ---- parsing ----------------------------------------------------------------------------

// parseProfileReply reads the model's JSON object (nil when there is none).
// A {"profile":{…},"reasons":[…]} wrapper is flattened.
func parseProfileReply(raw string) *jobj {
	for _, cand := range []string{raw, repairJSON(raw)} {
		b, err := llm.ExtractJSON(cand)
		if err != nil {
			continue
		}
		v, err := parseJSON(b)
		if err != nil {
			continue
		}
		if a, ok := v.([]any); ok && len(a) > 0 {
			v = a[0]
		}
		o, ok := v.(*jobj)
		if !ok {
			continue
		}
		if inner, ok := get(o, "profile").(*jobj); ok {
			if r := get(o, "reasons"); r != nil && get(inner, "reasons") == nil {
				inner.set("reasons", r)
			}
			o = inner
		}
		return o
	}
	return nil
}

// repairJSON fixes the usual slips of a model writing JSON by hand —
// comments, trailing commas, Python literals, a reply cut off before its
// closing quote/brackets — without touching the inside of strings.
func repairJSON(s string) string {
	var out strings.Builder
	var closers []byte
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			out.WriteByte(c)
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
		switch {
		case c == '"':
			inStr = true
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				i = len(s)
			} else {
				i += end + 3
			}
			continue
		case c == '{':
			closers = append(closers, '}')
		case c == '[':
			closers = append(closers, ']')
		case c == '}' || c == ']':
			trimComma(&out)
			if len(closers) > 0 {
				closers = closers[:len(closers)-1]
			}
		default:
			if lit, rep := pyLiteral(s, i); lit != "" {
				out.WriteString(rep)
				i += len(lit) - 1
				continue
			}
		}
		out.WriteByte(c)
	}
	if inStr {
		out.WriteByte('"')
	}
	trimComma(&out)
	for k := len(closers) - 1; k >= 0; k-- {
		out.WriteByte(closers[k])
	}
	return out.String()
}

// trimComma drops a trailing comma (and the blanks after it) from b.
func trimComma(b *strings.Builder) {
	s := strings.TrimRight(b.String(), " \t\r\n")
	if strings.HasSuffix(s, ",") {
		s = s[:len(s)-1]
		b.Reset()
		b.WriteString(s)
	}
}

// pyLiteral recognises True/False/None at s[i] (a whole word).
func pyLiteral(s string, i int) (lit, rep string) {
	for _, l := range [...][2]string{{"True", "true"}, {"False", "false"}, {"None", "null"}} {
		if strings.HasPrefix(s[i:], l[0]) {
			before := i == 0 || !isWordByte(rune(s[i-1]))
			after := i+len(l[0]) >= len(s) || !isWordByte(rune(s[i+len(l[0])]))
			if before && after {
				return l[0], l[1]
			}
		}
	}
	return "", ""
}

// ---- reply → profile ----------------------------------------------------------------------

var reVertical = regexp.MustCompile(`(?i)shorts|reels|tik ?tok|тік ?ток|шортс|рілс|вертикал|9\s*[:x×]\s*16`)

// suggestionDefaults is the profile used for every field the model leaves
// out or gets wrong.
func suggestionDefaults(vertical bool) config.Profile {
	p := config.Profile{
		Name: "Мій профіль", TargetSeconds: 300, Aspect: "16:9", Style: "cinematic", Tempo: "balanced", Camera: "cinematic",
		ImageModel: "NARWHAL", Voiceover: true, Transition: media.DefaultTransition, Subtitles: "srt",
		MusicVolume: 0.18, AmbientVolume: 0.25, Ducking: true,
	}
	if vertical {
		p.TargetSeconds, p.Aspect, p.Tempo, p.Camera = 48, "9:16", "fast", "dynamic"
		p.Subtitles, p.SubtitlePreset, p.Transition = "burn", "shorts", "none"
	}
	return p
}

// profileFromReply maps the model's answer onto a valid profile.
func profileFromReply(o *jobj, desc string) (config.Profile, []string) {
	str := func(key string) string { return strings.TrimSpace(jsString(get(o, key))) }

	aspect, aspectOK := parseAspect(str("aspect"))
	vertical := aspect == "9:16"
	if !aspectOK {
		vertical = reVertical.MatchString(desc)
	}
	p := suggestionDefaults(vertical)

	if n := clipRunes(str("name"), 80); n != "" {
		p.Name = n
	} else {
		p.Name = nameFrom(desc)
	}
	if sec, ok := parseSeconds(get(o, "targetSeconds")); ok {
		p.TargetSeconds = int(math.Round(max(8, min(3600, sec))))
	}
	if id, ok := matchID(str("style"), len(Styles), func(i int) (string, string) { return Styles[i].ID, Styles[i].Label }); ok {
		p.Style = id
	}
	p.CustomStyle = clipRunes(str("customStyle"), 1000)
	if id, ok := matchChoice(Genres, str("genre")); ok {
		p.Genre = id
	}
	if id, ok := matchChoice(Tempos, str("tempo")); ok {
		p.Tempo = id
	}
	if id, ok := matchChoice(Cameras, str("camera")); ok {
		p.Camera = id
	}
	if id, ok := matchImageModel(str("imageModel")); ok {
		p.ImageModel = id
	}
	if v, ok := parseBool(get(o, "voiceover")); ok {
		p.Voiceover = v
	}
	p.Language = matchLanguage(str("language"))
	if id, ok := matchChoice(Tones, str("tone")); ok {
		p.Tone = id
	}
	if n := clipRunes(str("notes"), 2000); n != "" {
		p.Notes = n
	} else {
		p.Notes = clipRunes(desc, 600) // the user's own words are the best brief we have
	}
	p.StoryboardMode = matchStoryboard(str("storyboardMode"))
	if v, ok := parseBool(get(o, "chainFrames")); ok {
		p.ChainFrames = v
	}
	if id, ok := matchID(str("transition"), len(media.Transitions), func(i int) (string, string) {
		return media.Transitions[i].ID, media.Transitions[i].Label
	}); ok {
		p.Transition = id
	}
	if d, ok := parseNumber(get(o, "transitionDuration")); ok && d > 0 {
		p.TransitionDuration = math.Round(max(media.MinTransitionDuration, min(media.MaxTransitionDuration, d))*10) / 10
	}
	if id, ok := matchID(str("filter"), len(media.Filters), func(i int) (string, string) {
		return media.Filters[i].ID, media.Filters[i].Label
	}); ok && id != "none" { // "" = no colour look
		p.Filter = id
	}
	if id, ok := matchID(str("effect"), len(media.Effects), func(i int) (string, string) {
		return media.Effects[i].ID, media.Effects[i].Label
	}); ok {
		p.Effect = id
	}
	if s, ok := matchSubtitles(str("subtitles")); ok {
		p.Subtitles = s
	}
	if id, ok := matchID(str("subtitlePreset"), len(media.SubtitlePresets), func(i int) (string, string) {
		return media.SubtitlePresets[i].ID, media.SubtitlePresets[i].Label
	}); ok {
		p.SubtitlePreset = id
	}
	if v, ok := parseVolume(get(o, "musicVolume")); ok {
		p.MusicVolume = v
	}
	if v, ok := parseVolume(get(o, "ambientVolume")); ok {
		p.AmbientVolume = v
	}
	if v, ok := parseBool(get(o, "ducking")); ok {
		p.Ducking = v
	}
	p.Normalize()

	reasons := parseReasons(get(o, "reasons"))
	if len(reasons) == 0 {
		reasons = summarizeProfile(p)
	}
	return p, reasons
}

// matchID finds a catalogue id by id or by its (Ukrainian) label, ignoring case.
func matchID(v string, n int, at func(i int) (id, label string)) (string, bool) {
	v = strings.Trim(strings.TrimSpace(v), `"'«»`)
	if v == "" {
		return "", false
	}
	for i := 0; i < n; i++ {
		if id, _ := at(i); strings.EqualFold(id, v) {
			return id, true
		}
	}
	for i := 0; i < n; i++ {
		if id, label := at(i); strings.EqualFold(label, v) {
			return id, true
		}
	}
	return "", false
}

func matchChoice(list []Choice, v string) (string, bool) {
	return matchID(v, len(list), func(i int) (string, string) { return list[i].ID, list[i].Label })
}

func matchImageModel(v string) (string, bool) {
	if id, ok := matchChoice(imageModels, v); ok {
		return id, true
	}
	switch l := strings.ToLower(v); {
	case strings.Contains(l, "pro"):
		return "GEM_PIX_2", true
	case strings.Contains(l, "lite"):
		return "GEM_PIX", true
	}
	return "", false
}

// matchLanguage is a catalogue language code ("" = auto, also for unknown).
func matchLanguage(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "auto") {
		return ""
	}
	if l, ok := LanguageByCode(v); ok {
		return l.Code
	}
	if code := tts.NormalizeLanguage(v); code != "" { // "eng", "en-US", "English"
		if l, ok := LanguageByCode(code); ok {
			return l.Code
		}
	}
	for _, l := range Languages { // a native or Ukrainian name
		if strings.EqualFold(l.Native, v) || strings.EqualFold(l.Label, v) {
			return l.Code
		}
	}
	return ""
}

func matchStoryboard(v string) string {
	switch l := strings.ToLower(strings.TrimSpace(v)); {
	case l == StoryboardGrid, strings.Contains(l, "grid"), strings.Contains(l, "3x3"), strings.Contains(l, "3×3"):
		return StoryboardGrid
	}
	return ""
}

func matchSubtitles(v string) (string, bool) {
	switch l := strings.ToLower(strings.TrimSpace(v)); {
	case l == "none", l == "no", l == "off", l == "false", l == "без субтитрів", l == "ні":
		return "none", true
	case l == "srt", strings.Contains(l, "file"), strings.Contains(l, "файл"):
		return "srt", true
	case strings.HasPrefix(l, "burn"), strings.Contains(l, "hard"), strings.Contains(l, "впал"), strings.Contains(l, "вшит"):
		return "burn", true
	}
	return "", false
}

func parseAspect(v string) (string, bool) {
	switch l := strings.ToLower(strings.ReplaceAll(v, " ", "")); {
	case l == "16:9", l == "16x9", l == "16×9", l == "horizontal", l == "landscape", strings.HasPrefix(l, "горизонт"):
		return "16:9", true
	case l == "9:16", l == "9x16", l == "9×16", l == "vertical", l == "portrait", strings.HasPrefix(l, "вертикал"):
		return "9:16", true
	}
	return "", false
}

var reNumber = regexp.MustCompile(`[-+]?\d+(?:[.,]\d+)?`)

// parseNumber reads a JSON number or a number inside a string ("0,5").
func parseNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case string:
		m := reNumber.FindString(x)
		if m == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", "."), 64)
		return f, err == nil
	}
	return 0, false
}

// parseSeconds reads a duration: a number of seconds, or a string with a
// unit ("3 хв", "2 min", "45 s", "1 год").
func parseSeconds(v any) (float64, bool) {
	f, ok := parseNumber(v)
	if !ok || f <= 0 {
		return 0, false
	}
	if s, isStr := v.(string); isStr {
		switch l := strings.ToLower(s); {
		case strings.Contains(l, "год"), strings.Contains(l, "hour"), strings.HasSuffix(strings.TrimSpace(l), "h"):
			f *= 3600
		case strings.Contains(l, "хв"), strings.Contains(l, "min"), strings.HasSuffix(strings.TrimSpace(l), "m"):
			f *= 60
		}
	}
	return f, true
}

// parseVolume reads 0–1, also a percentage (20 → 0.2).
func parseVolume(v any) (float64, bool) {
	f, ok := parseNumber(v)
	if !ok || f < 0 {
		return 0, false
	}
	if s, isStr := v.(string); (isStr && strings.Contains(s, "%")) || (f > 1 && f <= 100) {
		f /= 100
	}
	return max(0, min(1, f)), true
}

func parseBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case float64:
		return x != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "yes", "on", "1", "так", "да":
			return true, true
		case "false", "no", "off", "0", "ні", "нет":
			return false, true
		}
	}
	return false, false
}

// reListMarker is a list bullet or number at the start of a reason ("- ", "2. ", "3) ").
var reListMarker = regexp.MustCompile(`^\s*(?:[-–•*]|\d{1,2}[.)])\s+`)

// parseReasons accepts an array of strings or one text (split into lines).
func parseReasons(v any) []string {
	var raw []string
	switch x := v.(type) {
	case []any:
		for _, r := range x {
			raw = append(raw, jsString(r))
		}
	case string:
		raw = strings.Split(x, "\n")
	}
	var out []string
	for _, r := range raw {
		r = strings.TrimSpace(reListMarker.ReplaceAllString(r, ""))
		if r == "" {
			continue
		}
		out = append(out, clipRunes(r, 240))
		if len(out) == maxProfileReasons {
			break
		}
	}
	return out
}

// nameFrom makes a profile name from the first words of the description.
func nameFrom(desc string) string {
	words := strings.Fields(desc)
	if len(words) > 4 {
		words = words[:4]
	}
	name := strings.TrimRight(strings.Join(words, " "), ",.;:!?")
	if utf8.RuneCountInString(name) > 40 {
		name = clipRunes(name, 40)
	}
	if name == "" {
		return "Мій профіль"
	}
	return name
}

// summarizeProfile explains a profile in a few Ukrainian lines (when the
// model gave no reasons of its own).
func summarizeProfile(p config.Profile) []string {
	format := "Горизонтальний формат 16:9 — для YouTube"
	if p.Aspect == "9:16" {
		format = "Вертикальний формат 9:16 — для Shorts, Reels і TikTok"
	}
	out := []string{fmt.Sprintf("%s, приблизно %s на відео.", format, humanSeconds(p.TargetSeconds))}
	if s, ok := StyleByID(p.Style); ok && strings.TrimSpace(p.CustomStyle) == "" {
		out = append(out, fmt.Sprintf("Стиль «%s» — %s.", s.Label, s.Suits))
	} else if p.CustomStyle != "" {
		out = append(out, "Свій стиль зображення — жоден із готових не пасує до опису.")
	}
	voice := "Без закадрового голосу."
	if p.Voiceover {
		voice = "Озвучує диктор"
		if l, ok := LanguageByCode(p.Language); ok {
			voice += " мовою: " + strings.ToLower(l.Label)
		}
		if t, ok := ChoiceByID(Tones, p.Tone); ok {
			voice += ", тон — " + strings.ToLower(t.Label)
		}
		voice += "."
	}
	out = append(out, voice)
	if t, ok := media.TransitionByID(p.Transition); ok {
		subs := map[string]string{"none": "без субтитрів", "srt": "субтитри окремим файлом", "burn": "субтитри просто на відео"}[p.Subtitles]
		out = append(out, fmt.Sprintf("Монтаж: перехід «%s», %s.", t.Label, subs))
	}
	return out
}

func humanSeconds(sec int) string {
	if sec < 60 {
		return fmt.Sprintf("%d с", sec)
	}
	if sec%60 == 0 {
		return fmt.Sprintf("%d хв", sec/60)
	}
	return fmt.Sprintf("%d:%02d хв", sec/60, sec%60)
}
