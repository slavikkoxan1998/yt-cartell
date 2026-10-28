package studio

// Step 2 "plan": text → characters, locations, scenes (port of planner.ts).
//
// Two passes keep long videos reliable:
//
//	A. "bible" — title, style guide, characters, locations, and one entry per
//	             scene with its voiceover line, speaker, cast and location.
//	B. "shots" — in batches: an image prompt + motion prompt per scene that
//	             references characters/locations ONLY by @tag, so their look
//	             always comes from the reference images (consistency).
//
// ModeVoice (ownvoice.go): the user's recording is transcribed and split into
// scenes timed to the speech first; pass A gets those parts as a fixed script
// (verbatim, never translated) and every scene keeps its slice of the audio.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"ytcartell/internal/llm"
	"ytcartell/internal/tts"
)

const (
	wps       = 2.3 // speaking rate used to fit narration into a clip (words per second)
	maxScenes = 150
	shotBatch = 8
)

func wordsPerScene(clip int) int { return max(6, int(math.Floor(float64(clip)*wps))) }

// Budget is how many scenes a project will get, the clip length and each
// scene's word budget (script mode counts the split script parts). With
// dynamic clip lengths the clip length is the average expected one.
func (e *Engine) Budget(p Project) (scenes, clipSeconds, wordsPerScene int) {
	clip := e.planClipFor(p.Input)
	n, w, _ := sceneBudget(p.Input, clip)
	return n, clip, w
}

// planClip is the scene length plans are written for (see planClip in
// storyboard.go): the Settings length, or the average dynamic one.
func (e *Engine) planClip() int { return planClip(e.cfg.Get(), e.ClipSeconds()) }

func sceneBudget(in Input, clip int) (count, words int, chunks []string) {
	words = wordsFor(in, clip)
	sound := in.SoundMode()
	if sound == AudioInVideo {
		words = DialogueWords(clip) // said on camera, within the shot
	}
	if in.Mode == ModeScript && (sound == AudioVoiceover || sound == AudioInVideo) {
		chunks = splitScript(in.Text, words)
		if len(chunks) > maxScenes {
			chunks = chunks[:maxScenes]
		}
		if chunks == nil {
			chunks = []string{}
		}
		return len(chunks), words, chunks
	}
	count = int(math.Floor(float64(in.TargetSeconds)/float64(clip) + 0.5))
	return max(1, min(maxScenes, count)), words, nil
}

// ---- tags ------------------------------------------------------------------------------

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "h", 'ґ': "g", 'д': "d", 'е': "e", 'є': "ie", 'ж': "zh", 'з': "z", 'и': "y", 'і': "i", 'ї': "i",
	'й': "i", 'к': "k", 'л': "l", 'м': "m", 'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u", 'ф': "f", 'х': "kh",
	'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch", 'ь': "", 'ю': "iu", 'я': "ia", 'ё': "e", 'ы': "y", 'э': "e", 'ъ': "",
}

func isWordByte(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_'
}

// slugTag turns a name into a latin handle: transliterated, accents dropped,
// [a-z0-9_], at most 24 chars, "item" when nothing is left.
func slugTag(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if t, ok := translit[r]; ok {
			b.WriteString(t)
		} else {
			b.WriteRune(r)
		}
	}
	// normalize("NFKD").replace(/[^\w\s-]/g, "")
	var kept strings.Builder
	for _, r := range norm.NFKD.String(b.String()) {
		if isWordByte(r) || unicode.IsSpace(r) || r == '-' {
			kept.WriteRune(r)
		}
	}
	// .trim().replace(/[\s-]+/g, "_").replace(/_+/g, "_")
	var out strings.Builder
	sep := false
	for _, r := range strings.TrimSpace(kept.String()) {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			sep = true
			continue
		}
		if sep {
			out.WriteByte('_')
			sep = false
		}
		out.WriteRune(r)
	}
	if sep {
		out.WriteByte('_')
	}
	s := out.String()
	if len(s) > 24 {
		s = s[:24]
	}
	if s == "" {
		return "item"
	}
	return s
}

func tagConflict(tag string, used []string) bool {
	for _, u := range used {
		if u == tag || strings.Contains(u, tag) || strings.Contains(tag, u) {
			return true
		}
	}
	return false
}

// uniqueTags gives every name a tag that is unique and neither a substring nor
// a superstring of another tag (G-Labs matches @tag by substring). Like the TS
// version it tries base, base2, base3…; when the base itself contains a taken
// tag (e.g. "tarasyk" vs "taras") suffixes can never help, so it breaks the
// clash with an underscore, and as a last resort uses a random handle.
func uniqueTags(names []string, taken []string) []string {
	used := make([]string, 0, len(taken)+len(names))
	for _, t := range taken {
		if t != "" {
			used = append(used, t)
		}
	}
	out := make([]string, len(names))
	for i, n := range names {
		base := slugTag(n)
		tag := base
		for k := 2; tagConflict(tag, used) && k <= 99; k++ {
			tag = base + strconv.Itoa(k)
		}
		if tagConflict(tag, used) {
			tag = breakTag(base, used)
		}
		used = append(used, tag)
		out[i] = tag
	}
	return out
}

func breakTag(base string, used []string) string {
	for i := 1; i < len(base); i++ {
		if base[i-1] == '_' || base[i] == '_' {
			continue
		}
		if t := base[:i] + "_" + base[i:]; !tagConflict(t, used) {
			return t
		}
	}
	const letters = "bcdfghjkmnpqrstvwxz"
	for tries := 0; ; tries++ {
		raw := newID(8)
		b := make([]byte, len(raw))
		for i := range raw {
			b[i] = letters[int(raw[i])%len(letters)]
		}
		if t := string(b); !tagConflict(t, used) || tries > 1000 {
			return t
		}
	}
}

// ---- script splitting ---------------------------------------------------------------------

const sentenceEnds = ".!?…»\""

// splitSentences is text.split(/(?<=[.!?…»"])\s+|\n{2,}/), trimmed, blanks dropped.
func splitSentences(text string) []string {
	s := strings.ReplaceAll(text, "\r", "")
	var parts []string
	start, i := 0, 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) && i > 0 {
			prev, _ := utf8.DecodeLastRuneInString(s[:i])
			if strings.ContainsRune(sentenceEnds, prev) {
				j := i
				for j < len(s) {
					r2, n2 := utf8.DecodeRuneInString(s[j:])
					if !unicode.IsSpace(r2) {
						break
					}
					j += n2
				}
				parts = append(parts, s[start:i])
				start, i = j, j
				continue
			}
		}
		if r == '\n' && i+1 < len(s) && s[i+1] == '\n' {
			j := i
			for j < len(s) && s[j] == '\n' {
				j++
			}
			parts = append(parts, s[start:i])
			start, i = j, j
			continue
		}
		i += size
	}
	parts = append(parts, s[start:])
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitScript splits a finished script into scene-sized chunks without
// breaking sentences; a single very long sentence is hard-split on words.
func splitScript(text string, maxWords int) []string {
	var chunks []string
	cur := ""
	for _, s := range splitSentences(text) {
		next := s
		if cur != "" {
			next = cur + " " + s
		}
		if cur != "" && countWords(next) > maxWords {
			chunks = append(chunks, cur)
			cur = s
		} else {
			cur = next
		}
	}
	if cur != "" {
		chunks = append(chunks, cur)
	}
	var out []string
	for _, c := range chunks {
		w := strings.Fields(c)
		if float64(len(w)) <= float64(maxWords)*1.4 {
			out = append(out, c)
			continue
		}
		for i := 0; i < len(w); i += maxWords {
			out = append(out, strings.Join(w[i:min(len(w), i+maxWords)], " "))
		}
	}
	return out
}

// ---- prompts ------------------------------------------------------------------------------

// genreHint is the genre direction: the catalogue's hint for a known id, the
// free text of an unknown one ("" = none).
func genreHint(g string) string {
	g = strings.TrimSpace(g)
	if g == "" {
		return ""
	}
	if c, ok := ChoiceByID(Genres, g); ok {
		return "Genre — " + c.Hint + " Match its mood, lighting and pacing."
	}
	return "Genre: " + g + " — match its mood, lighting and pacing."
}

// toneHint is the voice-over direction of a tone ("" = neutral storyteller).
func toneHint(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	if h := hintOf(Tones, t); h != "" {
		return h
	}
	return "Voice-over tone: " + t + "."
}

// unspaced are languages written without spaces between words (a word budget
// means little there).
var unspaced = map[string]bool{"zh": true, "yue": true, "ja": true, "th": true, "lo": true, "my": true}

// languageRules are the audience-language directives of the bible pass (nil
// when the film keeps the language of the user's text). voice: the narration
// is the user's own recording, only the titles are written.
func languageRules(lang string, voice bool) []string {
	if lang == "" {
		return nil
	}
	first := `Audience language: ` + lang + `. Write EVERY audience-facing text — "title", "logline", every scene "title" and "narration", all dialogue — in ` + lang + `, even when the user's text is written in another language.`
	if voice {
		first = `Audience language: ` + lang + `. Write "title", "logline" and every scene "title" in ` + lang + `.`
	}
	return []string{
		first,
		`Adapt character and place names naturally for ` + lang + ` speakers (the established form of a well-known name, else a natural transliteration); never translate a name word by word, and keep each name identical in every scene.`,
		`Keep "styleGuide" and every "description" in ENGLISH — they drive the image model.`,
	}
}

// biblePrompt is pass A. In ModeVoice (chunks = the transcript parts of the
// user's recording) the narration is fixed: the model only plans the cast,
// places and scenes for it and gives each scene the "index" of its part.
// The sound mode decides what a scene says (a voice-over line, lines spoken
// on camera, or a caption) and the content mode what is cast (characters,
// objects and motifs, backdrops); with text overlays every scene may carry
// its on-screen text.
func biblePrompt(p *Project, count, words int, chunks []string, clip int) (system, prompt string) {
	style := StylePrompt(p.Input.Style, p.Input.CustomStyle)
	if style == "" {
		style = "cinematic"
	}
	voice := p.Input.Mode == ModeVoice && chunks != nil
	sound := p.Input.SoundMode()
	if voice {
		sound = AudioVoiceover
	}
	content := p.Input.ContentMode()
	noPeople := p.Input.NoPeople()
	people := !noPeople // the cast has characters
	objects := content != ContentCharacters
	talk := sound == AudioInVideo
	multi := !voice && p.Input.MultiVoiceOn() // the voice-over split between speakers (multivoice.go)
	overlays := p.Input.TextOverlays()
	lang := languageName(p.Input.Language)
	inLang, sameLang := "in the language of the user's text", "in the SAME LANGUAGE as the user's text"
	if voice {
		inLang = "in the language of the voice-over"
	}
	if lang != "" {
		inLang, sameLang = "in "+lang, "in "+lang
	}
	var narrationRule string
	switch {
	case voice: // not asked for: the voice-over is fixed
	case talk: // nobody narrates: the scenes speak through their "lines"
	case multi: // the voice-over is written as "parts" (multiVoiceRules)
	case sound == AudioSilent:
		narrationRule = `"narration": one short sentence describing the moment (used only as a subtitle/caption), ` + inLang + `.`
	case chunks != nil && lang != "":
		narrationRule = fmt.Sprintf(`"narration": the given script part for this scene faithfully TRANSLATED into %s — keep the meaning, tone, `+
			`names and every detail; do not shorten, summarise or add anything. If the part is already in %s, copy it VERBATIM.`, lang, lang)
	case chunks != nil:
		narrationRule = `"narration": copy the given script part for this scene VERBATIM (it is already split for you).`
	default:
		narrationRule = fmt.Sprintf(`"narration": the voice-over line for this scene %s, `+
			`at most %d words (it must be speakable in about %d seconds). Together the lines must tell the whole story.`, sameLang, words, clip)
		if code := strings.ToLower(strings.TrimSpace(p.Input.Language)); unspaced[code] {
			narrationRule += fmt.Sprintf(` %s is written without spaces: keep each line to what a narrator says in about %d seconds.`, lang, clip)
		}
	}
	notes := ""
	if p.Input.Notes != "" {
		notes = "Director's notes from the user: " + p.Input.Notes
	}
	voiceNote := ""
	if voice {
		voiceNote = `The film is cut to the user's own recorded voice-over: its words and timing are final — plan the pictures for them.`
	}
	direction := append(dropEmpty(
		`You are an award-winning film director and screenwriter preparing a production plan for an AI-generated video.`,
		voiceNote,
		contentDirection(content),
		soundDirection(sound, noPeople),
		`Visual style of the film: `+style,
		styleDirection(p.Input),
		genreHint(p.Input.Genre),
		hintOf(Tempos, p.Input.Tempo),
		toneHint(p.Input.Tone),
		notes,
	), languageRules(lang, voice)...)
	direction = append(direction, materialsBrief(p)...) // the user's own characters, places, props, style
	schema := []string{
		`Return ONLY a JSON object:`,
		`{`,
		`  "title": short catchy title ` + inLang + `,`,
		`  "logline": one-sentence summary ` + inLang + `,`,
	}
	if chunks != nil && lang != "" && !voice {
		schema = append(schema, `  "sourceLanguage": ISO 639-1 code of the language the user's script is written in,`)
	}
	sceneKeys := []string{`"title"`, `"narration"`, `"speaker"`}
	switch {
	case voice:
		sceneKeys = []string{`"index"`, `"title"`, `"speaker"`}
	case talk:
		sceneKeys = []string{`"title"`, `"lines": [ { "speaker", "text", "delivery" } ]`}
	case multi:
		sceneKeys = []string{`"title"`, `"parts": [ { "speaker", "text", "delivery" } ]`}
	case noPeople:
		sceneKeys = []string{`"title"`, `"narration"`}
	}
	if people {
		sceneKeys = append(sceneKeys, `"characters": [names]`)
	}
	if objects {
		sceneKeys = append(sceneKeys, `"objects": [names]`)
	}
	sceneKeys = append(sceneKeys, `"location": name`)
	if overlays {
		sceneKeys = append(sceneKeys, `"onScreen": { "title", "value", "caption", "anim" }`)
	}
	scenesShape := `  "scenes": [ { ` + strings.Join(sceneKeys, ", ") + ` } ]`
	scenesRule := fmt.Sprintf(`- scenes: EXACTLY %d scenes in story order. Each scene is one continuous ~%d-second shot.`, count, clip)
	if voice {
		scenesRule = fmt.Sprintf(`- scenes: EXACTLY %d scenes, one per numbered part of the voice-over, in the same order ("index" = the part number). `+
			`Each scene is one continuous shot that lasts while its part is spoken; "title" names what we see.`, count)
	}
	mat := ""
	if hasCastMaterials(p) {
		mat = `, "material": the id of the user's material it is, or ""`
	}
	charKeys := `"name", "role", "gender": "male|female|other", "description"`
	if talk {
		charKeys += `, "voice"`
	}
	if multi {
		charKeys += `, "age": "child|young|adult|old"`
	}
	schema = append(schema, `  "styleGuide": ENGLISH, 1–2 sentences: rendering style, palette, lighting and mood shared by every shot,`)
	if talk {
		schema = append(schema, `  "narratorVoice": ENGLISH, how the off-screen voice sounds (8–20 words), or "" when nobody speaks off screen,`)
	}
	if people {
		schema = append(schema, `  "characters": [ { `+charKeys+mat+` } ],`)
	}
	if objects {
		schema = append(schema, `  "objects": [ { "name", "role", "description"`+mat+` } ],`)
	}
	schema = append(schema,
		`  "locations":  [ { "name", "description"`+mat+` } ],`,
		scenesShape,
		`}`,
	)
	rules := []string{`Rules:`}
	if people {
		rules = append(rules,
			`- characters: every recurring character (people, animals, creatures) — max 8. "description" is ENGLISH, 40–70 words:`,
			`  age, ethnicity/skin tone, face, hair, body type, ONE fixed outfit with exact colors, distinctive accessories.`,
			`  The outfit stays the same in every scene. This description is the ONLY source of the character's look.`)
		if talk {
			rules = append(rules, `  "voice": ENGLISH, 8–20 words — how the character sounds: age, gender, timbre, pitch, accent, pace `+
				`(e.g. "a boy of about 8, soft high voice, a little shy, quick pace"). It stays the same in every scene.`)
		}
	}
	if objects {
		rules = append(rules, objectsRule(content)...)
	}
	if content == ContentMotion {
		rules = append(rules,
			`- locations: the designed backdrops the film returns to (a canvas, a stylised set, a map base) — max 4. "description" is ENGLISH, 20–50 words:`,
			`  background colour or gradient, texture, grid or pattern, depth, lighting. No people.`)
	} else {
		rules = append(rules,
			`- locations: every distinct place — max 10. "description" is ENGLISH, 30–60 words: setting, architecture/nature,`,
			`  time of day, palette, key props. No people.`)
	}
	rules = append(rules, scenesRule)
	if narrationRule != "" {
		rules = append(rules, `- `+narrationRule)
	}
	if talk {
		rules = append(rules, linesRules(p, words, clip, sameLang, chunks != nil, noPeople)...)
	} else if multi {
		rules = append(rules, multiVoiceRules(p, words, clip, sameLang, chunks != nil, lang)...)
	} else if !noPeople {
		rules = append(rules, `- "speaker": "narrator" for narration, or the exact character name when the line is that character speaking aloud.`)
	}
	switch {
	case people && objects:
		rules = append(rules, `- "characters": names (exactly as in "characters") visible in the shot — [] for a shot without people. "objects": names (exactly as in "objects") visible in it. "location": a name from "locations".`)
	case objects:
		rules = append(rules, `- "objects": names (exactly as in "objects") visible in the shot. "location": a name from "locations".`)
	default:
		rules = append(rules, `- "characters": names (exactly as in "characters") visible in the shot. "location": a name from "locations".`)
	}
	if overlays {
		rules = append(rules, onScreenRule(content, inLang))
	}
	if noPeople {
		rules = append(rules, `- Keep continuity: the same objects, places and palette across scenes. Nobody appears in any scene.`)
	} else {
		rules = append(rules, `- Keep continuity: the same people, outfits and places across scenes.`)
	}
	system = strings.Join(direction, "\n") + "\n\n" + strings.Join(schema, "\n") + "\n\n" + strings.Join(rules, "\n")

	if chunks != nil {
		lines := make([]string, len(chunks))
		for i, c := range chunks {
			lines[i] = fmt.Sprintf("[%d] %s", i+1, c)
		}
		head := fmt.Sprintf("The finished script, already split into %d scene parts:\n\n", len(chunks))
		switch {
		case voice:
			head = fmt.Sprintf("The voice-over — the transcript of the user's own recording, already split into %d scene parts "+
				"(one scene per part, in this order; the words are final):\n\n", len(chunks))
		case multi:
			head = fmt.Sprintf("The finished script, already split into %d scene parts. Split each part between its speakers as its "+
				"scene's \"parts\" (quoted speech to the character who says it, the rest to the narrator):\n\n", len(chunks))
		case talk:
			head = fmt.Sprintf("The finished script, already split into %d scene parts. Turn each part into what is said ON CAMERA "+
				"in its scene (its \"lines\"): quoted speech stays word for word; narration becomes a line of the character it is about "+
				"or of the off-screen voice:\n\n", len(chunks))
		case lang != "":
			head = fmt.Sprintf("The finished script, already split into %d scene parts. Translate each part into %s as the narration "+
				"of its scene (copy it verbatim if it is already in %s):\n\n", len(chunks), lang, lang)
		}
		prompt = head + strings.Join(lines, "\n")
	} else {
		prompt = p.Input.Text
	}
	return system, prompt
}

// contentDirection is the bible directive of a content mode ("" for a cast
// of characters).
func contentDirection(content string) string {
	switch content {
	case ContentNoPeople:
		return `Content: NOBODY appears in this film — no people, no faces, no hands, no silhouettes, no crowds. ` +
			`Tell it through places, objects, nature, details, light and motion: b-roll, landscapes, still life, product and macro shots, ` +
			`animals only when the subject is about them.`
	case ContentMotion:
		return `Content: a MOTION-GRAPHICS film — animated 2D/3D design: icons, shapes, charts, diagrams, maps, timelines, ` +
			`isometric scenes and kinetic typography on clean backdrops. No live-action footage and no realistic people ` +
			`(at most simple stylised icon figures). Every scene is one designed frame that comes alive.`
	case ContentMixed:
		return `Content: mix scenes with characters and scenes without people — b-roll of places, objects and details, ` +
			`and motion-graphics inserts where a fact needs explaining. Use people only where the story needs them.`
	}
	return ""
}

// soundDirection is the bible directive of a sound mode ("" for a voice-over).
func soundDirection(sound string, noPeople bool) string {
	switch sound {
	case AudioInVideo:
		if noPeople {
			return `Sound: there is NO separate voice-over. The video model speaks the words itself as an off-screen voice ` +
				`inside each clip, with natural ambient sound — so every scene says at most a short line.`
		}
		return `Sound: there is NO voice-over. The characters speak ON CAMERA — the video model generates their speech ` +
			`with lip-sync and natural ambient sound inside each clip — so the story is told through short lines of dialogue.`
	case AudioSilent:
		return `Sound: no voice at all — music and ambient sound only; the pictures tell the story.`
	}
	return ""
}

// objectsRule describes the "objects" list of a content mode.
func objectsRule(content string) []string {
	if content == ContentMotion {
		return []string{
			`- objects: the recurring visual motifs of the design — a mascot shape, a key icon, a product, a chart style — max 6 (none is fine).`,
			`  "description" is ENGLISH, 20–50 words: shape, colours, flat or 3D rendering, details. It stays identical in every scene.`,
		}
	}
	return []string{
		`- objects: the recurring key objects, products, vehicles, animals or motifs the film keeps showing — max 8 (none if nothing recurs).`,
		`  "description" is ENGLISH, 30–60 words: shape, size, materials, colours, textures, distinctive details. It stays identical in`,
		`  every scene and is the ONLY source of its look.`,
	}
}

// linesRules are the rules of the spoken lines of AudioInVideo.
func linesRules(p *Project, words, clip int, sameLang string, script, noPeople bool) []string {
	who := `"speaker" is the exact name of a character VISIBLE in the scene (list them in "characters"), or "narrator" for an ` +
		`off-screen voice — use that rarely, only where nobody in frame could say it.`
	if noPeople {
		who = `Nobody is in frame: "speaker" is always "narrator" — an off-screen voice.`
	}
	say := fmt.Sprintf(`0–3 short lines per scene, at most %d words in total (it must fit a ~%d-second shot: about %s words per second `+
		`with a beat before and after). A scene may have no lines ([]) when the moment speaks for itself; together the lines tell the story.`,
		words, clip, strconv.FormatFloat(talkWPS, 'f', -1, 64))
	if script {
		say = fmt.Sprintf(`The words of the scene's script part, spoken in about %d seconds (at most %d words — tighten a longer part, keep its meaning).`, clip, words)
	}
	out := []string{
		`- "lines": what is said ON CAMERA in this scene, ` + sameLang + ` — the video model speaks it with the speakers' own voices. ` + say,
		`  ` + who,
		`  "delivery": ENGLISH, 2–6 words on how it is said (e.g. "whispers, scared", "shouts joyfully", "calm, warm").`,
		`  No stage directions or names inside "text" — only the spoken words.`,
	}
	if code := strings.ToLower(strings.TrimSpace(p.Input.Language)); unspaced[code] {
		out = append(out, fmt.Sprintf(`  %s is written without spaces: keep each scene's lines to what is said in about %d seconds.`, languageName(code), clip))
	}
	return out
}

// onScreenRule asks for the animated on-screen text of a scene.
func onScreenRule(content, inLang string) string {
	when := `Only scenes with a key fact, number, date, place or term get it — leave the others empty ({}).`
	if content == ContentMotion {
		when = `EVERY scene gets it — it is the heart of a motion-graphics film.`
	}
	return `- "onScreen": the animated text the studio draws over the scene (the image model never draws text), ` + inLang + `: ` +
		`"title" ≤ 6 words (a headline or key phrase), "value" — ONE key number or short fact when the scene has one ` +
		`(e.g. "73%", "1 200 km", "1914"), else "", "caption" ≤ 10 words or "", "anim": "count" for a number that should count up, ` +
		`"type" for a phrase revealed word by word, else "pop", "slide" or "fade". Short and punchy — never a whole sentence of narration. ` + when
}

// dropEmpty keeps the directive lines that are set (genre, tempo, tone and
// notes are optional).
func dropEmpty(lines ...string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func shotsPrompt(p *Project, batch []int, wish string) (system, prompt string) {
	content := p.Input.ContentMode()
	noPeople := p.Input.NoPeople()
	talk := p.Input.SoundMode() == AudioInVideo
	overlays := p.Input.TextOverlays()
	var cast, things []string
	for _, c := range p.Characters {
		if c.IsObject() {
			things = append(things, fmt.Sprintf("@%s — %s (%s)", c.Tag, c.Name, firstNonBlank(c.Role, "object")))
		} else {
			cast = append(cast, fmt.Sprintf("@%s — %s (%s)", c.Tag, c.Name, c.Role))
		}
	}
	places := make([]string, len(p.Locations))
	for i, l := range p.Locations {
		places[i] = fmt.Sprintf("@%s — %s: %s", l.Tag, l.Name, l.Description)
	}
	castText := strings.Join(cast, "\n")
	if castText == "" {
		castText = "(no recurring characters)"
	}
	placesText := strings.Join(places, "\n")
	if placesText == "" {
		placesText = "(none)"
	}
	example := "hero"
	for _, c := range p.Characters {
		if !c.IsObject() {
			example = c.Tag
			break
		}
	}
	format := "horizontal 16:9"
	if p.Input.Aspect == "9:16" {
		format = "vertical 9:16"
	}
	head := dropEmpty(
		`You are a storyboard artist and cinematographer. Write shot prompts for an image model and a video model.`,
		contentDirection(content),
		`Film style guide: `+p.StyleGuide,
		styleDirection(p.Input),
		hintOf(Cameras, p.Input.Camera),
		hintOf(Tempos, p.Input.Tempo),
		`Frame format: `+format+`.`,
	)
	if lang := languageName(p.Input.Language); lang != "" {
		head = append(head, `The titles and voice-over lines are in `+lang+`; write "shot", "imagePrompt" and "motionPrompt" in ENGLISH regardless.`)
	}
	var props []string
	if lines := propsBrief(p); len(lines) > 0 {
		props = append([]string{``, `Props the user supplied (when a scene shows one, describe it exactly like this):`}, lines...)
	}
	out := head
	if !noPeople {
		out = append(out, ``, `Cast (reference images exist for each; refer to them ONLY by their @tag):`, castText)
	}
	if len(things) > 0 || noPeople {
		label := `Recurring objects (reference images exist for each; refer to them ONLY by their @tag):`
		if content == ContentMotion {
			label = `Recurring design motifs (reference images exist for each; refer to them ONLY by their @tag):`
		}
		list := strings.Join(things, "\n")
		if list == "" {
			list = "(none)"
		}
		out = append(out, ``, label, list)
	}
	locLabel := `Locations (reference images exist; refer to them by @tag):`
	if content == ContentMotion {
		locLabel = `Backdrops (reference images exist; refer to them by @tag):`
	}
	out = append(out, ``, locLabel, placesText)
	out = append(out, props...)

	image := []string{
		`- "imagePrompt": ENGLISH, 45–90 words, the STILL first frame. Start with the shot type and angle.`,
		`  Mention each visible character ONLY as @tag (e.g. "@` + example + ` kneels by the fire")`,
		`  and describe their action, pose, expression and position in the frame. NEVER describe their face, hair,`,
		`  body or clothes — the reference image defines them. Place the scene in its location using the location @tag`,
		`  plus scene-specific details, lighting and mood. No text, captions, speech bubbles or logos in the image.`,
	}
	objectsLine := `  Mention each visible object ONLY as @tag and describe where it is, its angle and what happens to it — NEVER its shape, ` +
		`colours or materials (the reference image defines them).`
	motion := []string{
		`- "motionPrompt": ENGLISH, 20–50 words: ONE continuous shot starting from that frame — camera movement,`,
		`  what moves and how, natural ambient sound. No dialogue, no on-screen text, no scene cuts.`,
	}
	switch content {
	case ContentNoPeople:
		image = []string{
			`- "imagePrompt": ENGLISH, 45–90 words, the STILL first frame. Start with the shot type and angle.`,
			objectsLine,
			`  Place it in its location (@tag) with scene-specific details, light and mood. NO people anywhere in the frame: no humans,`,
			`  faces, hands, body parts or silhouettes. No text, captions, labels or logos in the image.`,
		}
		motion = []string{
			`- "motionPrompt": ENGLISH, 20–50 words: ONE continuous shot starting from that frame — camera movement (slow dolly, orbit,`,
			`  slider, drone, macro focus pull) and what moves in nature or on the objects, natural ambient sound. No people appear,`,
			`  no dialogue, no on-screen text, no scene cuts.`,
		}
	case ContentMotion:
		image = []string{
			`- "imagePrompt": ENGLISH, 45–90 words, the STILL first frame as a designed motion-graphics composition: layout, shapes,`,
			`  icons, charts, diagrams, maps or isometric elements, colours, depth and lighting on its backdrop (@tag).`,
			`  Mention a motif ONLY as @tag. Leave clean empty space where the on-screen text will go (centre or lower third) — the`,
			`  studio adds every word and number itself: NEVER letters, words, numbers, labels or logos in the image. No realistic people.`,
		}
		motion = []string{
			`- "motionPrompt": ENGLISH, 20–50 words: how the design comes alive — elements slide, scale or pop in with smooth easing,`,
			`  lines draw on, charts and bars grow, icons bounce, gentle parallax or a slow camera drift. No live action, no text,`,
			`  no scene cuts.`,
		}
	case ContentMixed:
		image = append(image, objectsLine,
			`  Scenes without characters are b-roll or design inserts: then NO people anywhere in the frame.`)
	default:
		if len(things) > 0 {
			image = append(image, objectsLine)
		}
	}
	if talk {
		who := `the speaking character faces the camera or the listener and talks naturally (lips moving)`
		if noPeople {
			who = `the off-screen voice speaks over it; nobody in frame talks`
		}
		motion = []string{
			`- "motionPrompt": ENGLISH, 20–50 words: ONE continuous shot starting from that frame — camera movement and the visible action;`,
			`  ` + who + `. The spoken words are added separately — never write them, never describe subtitles or text. End with`,
			`  "Ambient sound:" and the natural sounds of the place. No background music, no scene cuts.`,
		}
	}
	image = letteringRule(p, image)
	rules := append([]string{
		``,
		`For every scene return { "index", "shot", "imagePrompt", "motionPrompt" }. Return ONLY a JSON array.`,
		`- "shot": shot type, e.g. "wide establishing", "medium two-shot", "close-up".`,
	}, image...)
	rules = append(rules, motion...)
	if overlays {
		rules = append(rules, `- A scene with on-screen text: keep calm, uncluttered space for it in the frame (the studio animates the text over it).`)
	}
	rules = append(rules, `- Keep continuity with neighbouring scenes (same outfits, time of day, props).`)
	rules = append(rules, pipelineRules(p)...)
	system = strings.Join(append(out, rules...), "\n")

	lines := make([]string, 0, len(batch))
	for _, index := range batch {
		s := &p.Scenes[index]
		var who, what []string
		for _, id := range s.CharacterIDs {
			if c := findCharacter(p, id); c != nil {
				if c.IsObject() {
					what = append(what, "@"+c.Tag)
				} else {
					who = append(who, "@"+c.Tag)
				}
			}
		}
		whoText := strings.Join(who, ", ")
		if whoText == "" {
			whoText = "none"
		}
		loc := "unspecified"
		if l := findLocation(p, s.LocationID); l != nil {
			loc = "@" + l.Tag
		}
		prev := "this is the opening scene"
		if index > 0 {
			prev = "previous scene: " + p.Scenes[index-1].Title
		}
		fields := []string{fmt.Sprintf("index: %d", index+1), "title: " + s.Title}
		switch {
		case talk:
			fields = append(fields, "dialogue: "+firstNonBlank(dialogueSummary(p, s), "(nobody speaks)"))
		default:
			fields = append(fields, "voiceover: "+s.Narration)
		}
		if !noPeople {
			fields = append(fields, "visible characters: "+whoText)
		}
		if len(what) > 0 {
			fields = append(fields, "visible objects: "+strings.Join(what, ", "))
		}
		fields = append(fields, "location: "+loc)
		if overlays && !s.OnScreen.Empty() {
			fields = append(fields, "on-screen text (added by the studio): "+onScreenSummary(s.OnScreen))
		}
		fields = append(fields, prev)
		lines = append(lines, strings.Join(fields, "\n"))
	}
	extra := ""
	if wish != "" {
		// The wish is often written in Ukrainian: it steers the shot, the
		// prompts stay English (the next line must end with the wish itself).
		extra = "\n\nThe user's wish may be written in any language — follow it, but keep every prompt in English." +
			"\nExtra direction from the user for this rewrite: " + wish
	}
	return system, "Scenes:\n\n" + strings.Join(lines, "\n\n") + extra
}

// dialogueSummary is a scene's lines for the shot writer: who says what.
func dialogueSummary(p *Project, s *Scene) string {
	var parts []string
	for _, l := range cleanLines(s.Lines) {
		who := "off-screen voice"
		if c := findCharacter(p, l.Speaker); c != nil {
			who = "@" + c.Tag
		}
		d := ""
		if t := strings.TrimSpace(l.Delivery); t != "" {
			d = " (" + t + ")"
		}
		parts = append(parts, fmt.Sprintf("%s%s: «%s»", who, d, strings.TrimSpace(l.Text)))
	}
	return strings.Join(parts, " / ")
}

// onScreenSummary is the on-screen text in one line.
func onScreenSummary(o OnScreen) string {
	var parts []string
	for _, v := range []string{o.Title, o.Value, o.Caption} {
		if v = strings.TrimSpace(v); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " | ")
}

// ---- voices ---------------------------------------------------------------------------------

// assignVoices picks a voice for every speaker without one from the usable
// voices of the current TTS provider: the narrator (Settings' voice when
// listed), the extra narrators of a multi-voice film and every speaking
// character — gender, age and the film's language matched, each voice used
// once while there are enough (multivoice.go).
func (e *Engine) assignVoices(ctx context.Context, st *proj) {
	list, err := e.speech.Voices(ctx, false)
	if err != nil {
		return // no TTS configured — voices can be picked later
	}
	pref := e.cfg.Get().TTS.Voice
	e.update(st, func(p *Project) {
		assignSpeakerVoices(p, list.Voices, pref, e.ttsOptions(p).LanguageCode, false)
	})
}

// ---- pass B -------------------------------------------------------------------------------

type shotUpdate struct {
	id                  string
	shot, image, motion *string
	grammar             string // «Анімація кодом»: the grammar the shot writer suggests
}

// writeShots is pass B for the scenes of snap at the given indexes (also used
// to rewrite a single scene). Results are applied to the live project by
// scene id; onBatch runs after each batch.
func (e *Engine) writeShots(ctx context.Context, st *proj, snap *Project, indexes []int, cancelled func() bool, onBatch func(), wish string) error {
	var batches [][]int
	for i := 0; i < len(indexes); i += shotBatch {
		batches = append(batches, indexes[i:min(len(indexes), i+shotBatch)])
	}
	return mapLimit(batches, 3, cancelled, func(idxs []int, _ int) error {
		system, prompt := shotsPrompt(snap, idxs, wish)
		var raw json.RawMessage
		if err := e.text.JSON(ctx, llm.Request{System: system, Prompt: prompt, Temperature: 0.8}, &raw); err != nil {
			return err
		}
		reply, _ := parseJSON(raw)
		var ups []shotUpdate
		for k, r := range asArray(reply) {
			target := -1
			if n, ok := get(r, "index").(float64); ok {
				for _, i := range idxs {
					if float64(i) == n-1 {
						target = i
						break
					}
				}
			}
			if target < 0 && k < len(idxs) {
				target = idxs[k]
			}
			if target < 0 || target >= len(snap.Scenes) {
				continue
			}
			u := shotUpdate{id: snap.Scenes[target].ID}
			if v := get(r, "shot"); truthy(v) {
				s := jsString(v)
				u.shot = &s
			}
			if v := get(r, "imagePrompt"); truthy(v) {
				s := jsString(v)
				u.image = &s
			}
			if v := get(r, "motionPrompt"); truthy(v) {
				s := jsString(v)
				u.motion = &s
			}
			u.grammar = cleanGrammarID(jsString(get(r, "grammar")))
			ups = append(ups, u)
		}
		e.update(st, func(p *Project) {
			for _, u := range ups {
				s := findScene(p, u.id)
				if s == nil {
					continue
				}
				if u.shot != nil {
					s.Shot = *u.shot
				}
				if u.image != nil {
					s.ImagePrompt = *u.image
				}
				if u.motion != nil {
					s.MotionPrompt = *u.motion
				}
				if u.grammar != "" {
					s.Code.Suggested = u.grammar
				}
			}
		})
		if onBatch != nil {
			onBatch()
		}
		return nil
	})
}

// ---- the plan job ----------------------------------------------------------------------------

// translating reports whether a script must be voiced in translation: a
// target language is set and the model did not report the script as already
// being in it (an unreported source language trusts the model's narration,
// which it was told to copy verbatim when no translation is needed).
func translating(target, source string) bool {
	if strings.TrimSpace(target) == "" {
		return false
	}
	t, s := tts.NormalizeLanguage(target), tts.NormalizeLanguage(source)
	if t == "" || s == "" {
		return !strings.EqualFold(strings.TrimSpace(target), strings.TrimSpace(source))
	}
	return t != s
}

func (e *Engine) runPlan(j *jobCtx) (string, error) {
	e.waitLooks(j) // the descriptions of the user's materials, when still being written
	snap := j.snapshot()
	clip := e.planClipFor(snap.Input)
	var (
		count, words int
		chunks       []string
		speech       []speechChunk // ModeVoice: each scene's slice of the recording
		spoken       float64       // ModeVoice: seconds of the recording
		base         float64       // progress spent on the transcription
	)
	prompted := snap // what the bible pass sees
	if snap.Input.Mode == ModeVoice {
		// The user's own voice-over: transcribe (once), then split the words
		// into scenes timed to the speech; the narration is fixed.
		tr, err := e.transcriptFor(j, &snap, 0, 20)
		if err != nil {
			return "", err
		}
		if j.cancelled() {
			return "Зупинено", nil
		}
		lim := voiceLimits(e.cfg.Get())
		speech = chunkSpeech(tr, lim)
		for len(speech) > maxScenes && lim.target < lim.max { // a long recording: longer scenes
			lim.target = math.Min(lim.max, lim.target+0.5)
			speech = chunkSpeech(tr, lim)
		}
		switch {
		case len(speech) == 0:
			return "", errors.New("У записі не знайдено мовлення — перевір файл озвучки")
		case len(speech) > maxScenes:
			return "", fmt.Errorf("Запис задовгий: він не вміщається у %d сцен (кліпи до %.0f с). Скороти запис або розбий його на кілька проєктів", maxScenes, lim.max)
		}
		chunks = make([]string, len(speech))
		for i, c := range speech {
			chunks[i] = c.Text
		}
		count, words, spoken, base = len(chunks), wordsPerScene(clip), tr.Duration, 20
		snap = j.snapshot() // Input.Text now holds the transcript
		prompted = snap
		if prompted.Input.Language == "" { // titles in the language that is spoken
			prompted.Input.Language = tr.Language
		}
	} else {
		if strings.TrimSpace(snap.Input.Text) == "" {
			return "", errors.New("Спершу напиши історію або сценарій")
		}
		count, words, chunks = sceneBudget(snap.Input, clip)
	}
	at := func(pct float64) float64 { return base + pct*(100-base)/100 }

	j.progress(at(5), fmt.Sprintf("Режисер читає історію · %d сцен", count))
	system, prompt := biblePrompt(&prompted, count, words, chunks, clip)
	var raw json.RawMessage
	var bible any
	if err := e.text.JSON(j.ctx, llm.Request{System: system, Prompt: prompt, Temperature: 0.85, Timeout: 300 * time.Second}, &raw); err != nil {
		log.Printf("[studio:%s] plan: LLM text model unavailable (%v), constructing local plan from text", j.st.id, err)
		bible = buildFallbackBible(&prompted, count, chunks)
	} else {
		if j.cancelled() {
			return "Зупинено", nil
		}
		bible, _ = parseJSON(raw)
	}

	named := func(list []any, limit int) []any {
		var out []any
		for _, v := range list {
			if truthy(get(v, "name")) {
				out = append(out, v)
			}
		}
		if len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	// The user's character/location materials are always in the cast.
	mats := snap.Input.Materials
	in := snap.Input
	talk := in.SoundMode() == AudioInVideo && speech == nil
	multi := in.MultiVoiceOn() && speech == nil // the voice-over in speaker parts (multivoice.go)
	verbatim := false                           // the scenes say the user's script parts word for word
	var castC []castEntry
	if !in.NoPeople() {
		castC = bindMaterials(named(asArray(get(bible, "characters")), 64), mats, MaterialCharacter, 8)
	}
	objFrom := len(castC) // castC[objFrom:] are objects / motifs (Character.Kind "object")
	if in.ContentMode() != ContentCharacters {
		raw := named(asArray(get(bible, "objects")), 64)
		if len(raw) == 0 && in.NoPeople() {
			raw = named(asArray(get(bible, "characters")), 64) // the model kept the usual key
		}
		castC = append(castC, bindMaterials(raw, objectMaterials(mats, in.NoPeople()), MaterialObject, 8)...)
	}
	castL := bindMaterials(named(asArray(get(bible, "locations")), 64), mats, MaterialLocation, 10)
	rawScenes := asArray(get(bible, "scenes"))
	if len(rawScenes) == 0 {
		return "", errors.New("Модель не повернула сцен — спробуй ще раз")
	}
	if chunks != nil {
		// Script mode: narration is the user's text, verbatim, one chunk per
		// scene — or, for a film in another language, the model's faithful
		// translation of that chunk. ModeVoice: always the transcript,
		// verbatim; the model's scenes are matched by their "index".
		translate := speech == nil && translating(snap.Input.Language, jsString(get(bible, "sourceLanguage")))
		verbatim = speech == nil && !translate
		sceneFor := func(i int) any {
			if i < len(rawScenes) {
				return rawScenes[i]
			}
			return nil
		}
		if speech != nil {
			byIndex := map[int]any{}
			for _, s := range rawScenes {
				if n, ok := get(s, "index").(float64); ok && n >= 1 && n <= float64(len(chunks)) && n == math.Trunc(n) {
					if _, dup := byIndex[int(n)-1]; !dup {
						byIndex[int(n)-1] = s
					}
				}
			}
			if len(byIndex) > 0 {
				sceneFor = func(i int) any { return byIndex[i] }
			}
		}
		out := make([]any, len(chunks))
		for i, c := range chunks {
			o := &jobj{vals: map[string]any{}}
			narration := c
			if rs := sceneFor(i); rs != nil {
				if src, ok := rs.(*jobj); ok {
					for _, k := range src.keys {
						o.set(k, src.vals[k])
					}
				}
				if t := strings.TrimSpace(jsString(get(rs, "narration"))); translate && t != "" {
					narration = t
				}
			}
			o.set("narration", narration)
			out[i] = o
		}
		rawScenes = out
	}

	// A material keeps the user's name (the model may have used another one:
	// scenes find it by that alias too) and its look is the description.
	castName := func(c castEntry, n int, fallback string) string {
		if c.mat != nil && strings.TrimSpace(c.mat.Name) != "" {
			return strings.TrimSpace(c.mat.Name)
		}
		return firstNonBlank(c.alias, fmt.Sprintf("%s %d", fallback, n+1))
	}
	castDesc := func(c castEntry) string {
		if c.mat != nil {
			return firstNonBlank(strings.TrimSpace(c.mat.Look), jsString(get(c.raw, "description")), c.mat.Note)
		}
		return jsString(get(c.raw, "description"))
	}
	charNames := make([]string, len(castC))
	for i, c := range castC {
		if i >= objFrom {
			charNames[i] = castName(c, i-objFrom, "Об'єкт")
		} else {
			charNames[i] = castName(c, i, "Персонаж")
		}
	}
	locNames := make([]string, len(castL))
	for i, l := range castL {
		locNames[i] = castName(l, i, "Локація")
	}
	charTags := uniqueTags(charNames, nil)
	locTags := uniqueTags(locNames, charTags)

	chars := make([]Character, len(castC))
	for i, c := range castC {
		chars[i] = Character{
			ID:          newID(8),
			Name:        charNames[i],
			Tag:         charTags[i],
			Role:        jsString(get(c.raw, "role")),
			Gender:      strings.ToLower(jsString(get(c.raw, "gender"))),
			Description: castDesc(c),
			Image:       EmptyAsset(),
		}
		if c.mat != nil {
			chars[i].MaterialID = c.mat.ID
		}
		if i >= objFrom {
			chars[i].Kind, chars[i].Gender = KindObject, ""
		} else if talk {
			chars[i].Voice = clipRunes(strings.TrimSpace(jsString(get(c.raw, "voice"))), 400)
		} else if multi {
			chars[i].Age = normAge(jsString(get(c.raw, "age")))
		}
	}
	locs := make([]Location, len(castL))
	for i, l := range castL {
		locs[i] = Location{
			ID:          newID(8),
			Name:        locNames[i],
			Tag:         locTags[i],
			Description: castDesc(l),
			Image:       EmptyAsset(),
		}
		if l.mat != nil {
			locs[i].MaterialID = l.mat.ID
		}
	}
	byName := func(n any) *Character {
		k := strings.ToLower(strings.TrimSpace(jsString(n)))
		slug := slugTag(k)
		for i := range chars {
			if strings.ToLower(chars[i].Name) == k || chars[i].Tag == slug || k != "" && strings.ToLower(castC[i].alias) == k {
				return &chars[i]
			}
		}
		return nil
	}
	locByName := func(n any) *Location {
		k := strings.ToLower(strings.TrimSpace(jsString(n)))
		slug := slugTag(k)
		for i := range locs {
			if strings.ToLower(locs[i].Name) == k || locs[i].Tag == slug || k != "" && strings.ToLower(castL[i].alias) == k {
				return &locs[i]
			}
		}
		return nil
	}
	// Bound characters/locations: the material picture is the reference
	// image (the one of the previous plan when it is still there).
	e.bindMaterialImages(j.st, &snap, chars, locs)

	if len(rawScenes) > maxScenes {
		rawScenes = rawScenes[:maxScenes]
	}
	scenes := make([]Scene, len(rawScenes))
	for i, s := range rawScenes {
		sc := Scene{
			ID:           newID(8),
			Title:        jsString(get(s, "title")),
			Narration:    strings.TrimSpace(jsString(get(s, "narration"))),
			Speaker:      "narrator",
			CharacterIDs: []string{},
			Frame:        EmptyAsset(),
			Video:        EmptyAsset(),
			Audio:        EmptyAsset(),
		}
		sp := byName(get(s, "speaker"))
		if sp != nil && (talk || sp.IsObject()) {
			sp = nil // lines carry their own speakers; objects never narrate
		}
		if sp != nil {
			sc.Speaker = sp.ID
		}
		seen := map[string]bool{}
		for _, key := range []string{"characters", "objects"} {
			for _, n := range asArray(get(s, key)) {
				if c := byName(n); c != nil && !seen[c.ID] {
					seen[c.ID] = true
					sc.CharacterIDs = append(sc.CharacterIDs, c.ID)
				}
			}
		}
		if sp != nil && !seen[sp.ID] {
			sc.CharacterIDs = append(sc.CharacterIDs, sp.ID)
		}
		if talk {
			// Said on camera: the lines, no narration.
			sc.Narration = ""
			for _, l := range asArray(get(s, "lines")) {
				text := strings.TrimSpace(jsString(get(l, "text")))
				if text == "" {
					continue
				}
				line := Line{Speaker: narratorID, Text: clipRunes(text, 600), Delivery: clipRunes(strings.TrimSpace(jsString(get(l, "delivery"))), 120)}
				if c := byName(get(l, "speaker")); c != nil && !c.IsObject() {
					line.Speaker = c.ID
					if !seen[c.ID] { // a speaker is in the shot
						seen[c.ID] = true
						sc.CharacterIDs = append(sc.CharacterIDs, c.ID)
					}
				}
				sc.Lines = append(sc.Lines, line)
			}
			sc.Lines = cleanLines(sc.Lines)
			if len(sc.Lines) == 0 && chunks != nil && i < len(chunks) {
				sc.Lines = []Line{{Speaker: narratorID, Text: chunks[i]}} // the user's script part is said
			}
		}
		if multi {
			// The voice-over split between its speakers; a script part is
			// kept word for word (or said by the narrator when the split
			// changed its words).
			script := ""
			if verbatim && i < len(chunks) {
				script = chunks[i]
			}
			sc.Parts = planParts(in, s, script, byName)
			syncParts(&sc)
		}
		if in.TextOverlays() {
			if o := get(s, "onScreen"); o != nil {
				str := func(k string) string { return strings.TrimSpace(jsString(get(o, k))) }
				sc.OnScreen = OnScreen{Title: str("title"), Value: str("value"), Caption: str("caption"), Anim: str("anim")}
				reconcileOnScreen(&sc.OnScreen)
			}
		}
		if l := locByName(get(s, "location")); l != nil {
			sc.LocationID = l.ID
		} else if len(locs) > 0 {
			sc.LocationID = locs[0].ID
		}
		if i < len(speech) { // ModeVoice: the voice is this scene's slice of the recording
			sc.AudioSource, sc.VoiceStart, sc.VoiceEnd = AudioUpload, speech[i].Start, speech[i].End
		}
		scenes[i] = sc
	}

	style := StylePrompt(snap.Input.Style, snap.Input.CustomStyle)
	var dropped []string
	e.update(j.st, func(p *Project) {
		before := assetPaths(p)
		p.Characters, p.Locations, p.Scenes = chars, locs, scenes
		if t := get(bible, "title"); truthy(t) && (p.Title == "Новий проєкт" || strings.TrimSpace(p.Title) == "") {
			p.Title = clipRunes(jsString(t), 80)
		}
		p.Logline = jsString(get(bible, "logline"))
		if v := strings.TrimSpace(jsString(get(bible, "narratorVoice"))); talk && v != "" {
			p.NarratorVoice = clipRunes(v, 400)
		}
		guide := []string{}
		for _, g := range []string{style, jsString(get(bible, "styleGuide")), styleLooks(&snap)} {
			if g != "" {
				guide = append(guide, g)
			}
		}
		p.StyleGuide = strings.Join(guide, " ")
		dropped = orphans(before, assetPaths(p))
	})
	e.discardFiles(j.st, dropped)
	found := fmt.Sprintf("Знайдено %d персонажів, %d локацій, %d сцен", objFrom, len(locs), len(scenes))
	if objFrom < len(chars) {
		found = fmt.Sprintf("Знайдено %d персонажів, %d об'єктів, %d локацій, %d сцен", objFrom, len(chars)-objFrom, len(locs), len(scenes))
		if in.NoPeople() {
			found = fmt.Sprintf("Знайдено %d об'єктів, %d локацій, %d сцен", len(chars)-objFrom, len(locs), len(scenes))
		}
	}
	j.progress(at(35), found)

	if in.SoundMode() == AudioVoiceover { // TTS voices; the other modes need none
		e.assignVoices(j.ctx, j.st)
	}

	snap = j.snapshot()
	total := len(snap.Scenes)
	all := make([]int, total)
	for i := range all {
		all[i] = i
	}
	var mu sync.Mutex
	done := 0
	cfg := e.cfg.Get()
	stockMode := cfg.Stock.Enabled || cfg.Video.Provider == "stock" || snap.Input.Pipeline == "stock"
	if stockMode {
		e.update(j.st, func(p *Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ImagePrompt == "" {
					p.Scenes[i].ImagePrompt = p.Scenes[i].Narration
				}
				if p.Scenes[i].MotionPrompt == "" {
					p.Scenes[i].MotionPrompt = "stock footage"
				}
			}
		})
	} else {
		err := e.writeShots(j.ctx, j.st, &snap, all, j.cancelled, func() {
			mu.Lock()
			defer mu.Unlock()
			done = min(total, done+shotBatch)
			j.progress(at(35+60*float64(done)/float64(max(1, total))), fmt.Sprintf("Пишемо промти кадрів · %d/%d", done, total))
		}, "")
		if err != nil {
			log.Printf("[studio:%s] writeShots failed (%v), falling back to scene narration", j.st.id, err)
			e.update(j.st, func(p *Project) {
				for i := range p.Scenes {
					if p.Scenes[i].ImagePrompt == "" {
						p.Scenes[i].ImagePrompt = p.Scenes[i].Narration
					}
					if p.Scenes[i].MotionPrompt == "" {
						p.Scenes[i].MotionPrompt = "stock footage"
					}
				}
			})
		} else {
			// One retry for scenes the model skipped.
			snap = j.snapshot()
			var missing []int
			for i, s := range snap.Scenes {
				if s.ImagePrompt == "" {
					missing = append(missing, i)
				}
			}
			if len(missing) > 0 && !j.cancelled() {
				_ = e.writeShots(j.ctx, j.st, &snap, missing, j.cancelled, nil, "")
			}
		}
	}

	n := 0
	e.update(j.st, func(p *Project) {
		p.Step = max(p.Step, 2)
		n = len(p.Scenes)
	})
	secs := int(math.Floor(float64(n*clip) + 0.5))
	if speech != nil {
		secs = int(math.Round(spoken)) // the film lasts as long as the recording
	}
	return fmt.Sprintf("План готовий: %d сцен ≈ %d:%02d", n, secs/60, secs%60), nil
}

// Rewrite asks the model for new image/motion prompts of one scene, with an
// optional user wish. Synchronous (call it from a goroutine); the frame and
// clip are flagged stale.
func (e *Engine) Rewrite(ctx context.Context, id, sceneID, wish string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	st.mu.Lock()
	snap := cloneProject(st.p)
	st.mu.Unlock()
	idx := -1
	for i, s := range snap.Scenes {
		if s.ID == sceneID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errors.New("сцену не знайдено")
	}
	wish = strings.TrimSpace(clipRunes(wish, 1000))
	if err := e.writeShots(ctx, st, &snap, []int{idx}, func() bool { return ctx.Err() != nil }, nil, wish); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.update(st, func(p *Project) {
		if s := findScene(p, sceneID); s != nil {
			if s.Frame.Status == StatusDone {
				s.Frame.Stale = true
			}
			if s.Video.Status == StatusDone {
				s.Video.Stale = true
			}
		}
	})
	return nil
}

func buildFallbackBible(p *Project, count int, chunks []string) any {
	root := &jobj{vals: map[string]any{}}
	text := strings.TrimSpace(p.Input.Text)
	title := "Новий проєкт"
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			if len([]rune(l)) > 60 {
				title = string([]rune(l)[:60])
			} else {
				title = l
			}
			break
		}
	}
	root.set("title", title)
	root.set("logline", title)

	if len(chunks) == 0 {
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				chunks = append(chunks, l)
			}
		}
	}
	if len(chunks) == 0 && text != "" {
		chunks = []string{text}
	}
	if len(chunks) == 0 {
		chunks = []string{"Сцена 1"}
	}

	rawScenes := make([]any, len(chunks))
	for i, c := range chunks {
		sc := &jobj{vals: map[string]any{}}
		sc.set("index", float64(i+1))
		sc.set("title", fmt.Sprintf("Сцена %d", i+1))
		sc.set("narration", c)
		rawScenes[i] = sc
	}
	root.set("scenes", rawScenes)
	root.set("characters", []any{})
	root.set("objects", []any{})
	root.set("locations", []any{})
	return root
}
