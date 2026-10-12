package studio

// Multi-voice voice-over («Мультиголос», Input.MultiVoice): in the voice-over
// mode the plan splits every scene's voice-over into parts (Scene.Parts) —
// the narrator («Диктор»), up to two extra narrators («Диктор 2/3»,
// Input.ExtraNarrators) and the characters who speak (their own words,
// first person) — and every speaker gets a TTS voice of the current
// provider (assignSpeakerVoices: gender, age and language matched, each
// voice used once while there are enough). The voice step speaks each part
// with its speaker's voice and joins them into the scene's one voice line
// (short pauses, longer when the speaker changes) — or, on ElevenLabs with
// Eleven v3, voices the whole scene in one text-to-dialogue request — and
// stores where every part sits (Line.Start/End). Scene.Narration always
// holds the parts' words joined, so the clip lengths, the montage, the
// exports and older code read it as before; the subtitles show each part at
// its own time, optionally in its speaker's colour
// (SubtitleStyle.SpeakerColors). Projects without parts are unchanged.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"ytcartell/internal/media"
	"ytcartell/internal/tts"
)

const (
	narrator2ID = "narrator2"
	narrator3ID = "narrator3"
	// MaxExtraNarrators is how many narrators besides the main one a film may have.
	MaxExtraNarrators = 2
	// maxParts is the most parts a scene keeps.
	maxParts = 12
	// Pauses between the parts of a joined line (s): the same speaker goes
	// on, another one answers.
	partGapSame, partGapTurn = 0.18, 0.38
)

// MultiVoiceOn reports whether the film is voiced by several speakers: the
// «Мультиголос» switch in the voice-over mode (never the user's recording).
func (in Input) MultiVoiceOn() bool {
	return in.MultiVoice && in.Mode != ModeVoice && in.SoundMode() == AudioVoiceover
}

// extraNarrators is how many extra narrators the film has (≤ MaxExtraNarrators).
func extraNarrators(in Input) int { return min(len(in.ExtraNarrators), MaxExtraNarrators) }

// NarratorIDs are the narrators of a film: "narrator", then "narrator2",
// "narrator3" for each extra narrator.
func NarratorIDs(in Input) []string {
	out := []string{narratorID}
	for i := 0; i < extraNarrators(in); i++ {
		out = append(out, []string{narrator2ID, narrator3ID}[i])
	}
	return out
}

// IsNarrator reports a narrator speaker id (the main one or an extra one).
func IsNarrator(id string) bool { return id == narratorID || id == narrator2ID || id == narrator3ID }

// SpeakerLabel is how the UI names a part's speaker: «Диктор», «Диктор 2»,
// «Диктор 3» or the character's name.
func SpeakerLabel(p *Project, id string) string {
	switch id {
	case narrator2ID:
		return "Диктор 2"
	case narrator3ID:
		return "Диктор 3"
	}
	if c := findCharacter(p, id); c != nil && !c.IsObject() {
		return firstNonBlank(strings.TrimSpace(c.Name), "Персонаж")
	}
	return "Диктор"
}

// SpeakerVoiceID is the voice id a speaker is voiced with ("" = the voice
// from Settings): a character's own voice, an extra narrator's, else the
// main narrator's.
func SpeakerVoiceID(p *Project, id string) string {
	switch id {
	case narrator2ID, narrator3ID:
		k := 0
		if id == narrator3ID {
			k = 1
		}
		if k < len(p.Input.ExtraNarrators) && strings.TrimSpace(p.Input.ExtraNarrators[k]) != "" {
			return strings.TrimSpace(p.Input.ExtraNarrators[k])
		}
	default:
		if c := findCharacter(p, id); c != nil && c.VoiceID != "" {
			return c.VoiceID
		}
	}
	return p.Input.NarratorVoiceID
}

// speakerPalette are the subtitle colours of the speakers after the main
// narrator (bright, readable on video with the usual outline).
var speakerPalette = []string{"#FFD54F", "#4FC3F7", "#81C784", "#F48FB1", "#CE93D8", "#FFB74D", "#4DD0E1", "#DCE775"}

// SpeakerColor is the subtitle colour of a speaker: "" for the main narrator
// (the subtitle style's own colour), else a palette colour fixed by the
// speaker's place (extra narrators, then the cast in order).
func SpeakerColor(p *Project, id string) string {
	if id == "" || id == narratorID {
		return ""
	}
	order := []string{narrator2ID, narrator3ID}
	for _, c := range p.Characters {
		if !c.IsObject() {
			order = append(order, c.ID)
		}
	}
	for i, x := range order {
		if x == id {
			return speakerPalette[i%len(speakerPalette)]
		}
	}
	return ""
}

// VoiceSpeakers are the speakers whose voices a multi-voice film needs: its
// narrators, then every character who speaks a part (in cast order).
func VoiceSpeakers(p *Project) []string {
	out := NarratorIDs(p.Input)
	speaks := partSpeakers(p)
	for _, c := range p.Characters {
		if !c.IsObject() && speaks[c.ID] {
			out = append(out, c.ID)
		}
	}
	return out
}

// partSpeakers is the set of speakers of every scene's parts.
func partSpeakers(p *Project) map[string]bool {
	m := map[string]bool{}
	for _, s := range p.Scenes {
		for _, l := range s.Parts {
			if strings.TrimSpace(l.Text) != "" {
				m[l.Speaker] = true
			}
		}
	}
	return m
}

// cleanParts drops blank parts and keeps at most maxParts.
func cleanParts(parts []Line) []Line {
	var out []Line
	for _, l := range parts {
		if strings.TrimSpace(l.Text) == "" {
			continue
		}
		if len(out) == maxParts {
			break
		}
		out = append(out, l)
	}
	return out
}

// hasParts reports a scene voiced in parts.
func hasParts(s *Scene) bool { return len(cleanParts(s.Parts)) > 0 }

// syncParts makes the parts the source of the scene's voice-over: blank
// parts go, Narration = their words, Speaker = the one character when a
// single character says everything (else the narrator).
func syncParts(s *Scene) {
	parts := cleanParts(s.Parts)
	if len(parts) == 0 {
		s.Parts = nil
		return
	}
	for i := range parts {
		parts[i].Text = strings.TrimSpace(parts[i].Text)
	}
	s.Parts = parts
	s.Narration = clipRunes(LinesText(parts), 4000)
	s.Speaker = narratorID
	one := parts[0].Speaker
	for _, l := range parts[1:] {
		if l.Speaker != one {
			return
		}
	}
	if !IsNarrator(one) {
		s.Speaker = one
	}
}

// sameParts compares what two scenes' parts say and who says it.
func sameParts(a, b []Line) bool {
	a, b = cleanParts(a), cleanParts(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Speaker != b[i].Speaker || strings.TrimSpace(a[i].Text) != strings.TrimSpace(b[i].Text) ||
			strings.TrimSpace(a[i].Delivery) != strings.TrimSpace(b[i].Delivery) {
			return false
		}
	}
	return true
}

// reconcileParts validates an edited scene's parts (speakers of removed
// characters go to the narrator, the timings stay engine-owned) and reports
// whether what they say changed. Narration edited as a whole goes into the
// single part, or replaces several parts by one narrator part.
func reconcileParts(s, old *Scene, existed bool, cids map[string]bool) (changed bool) {
	if len(s.Parts) == 0 {
		s.Parts = nil
		return existed && hasParts(old)
	}
	if existed && sameParts(old.Parts, s.Parts) && strings.TrimSpace(s.Narration) != strings.TrimSpace(old.Narration) {
		// The narration was edited (a field that shows the whole line).
		parts := cleanParts(s.Parts)
		switch {
		case strings.TrimSpace(s.Narration) == "":
			s.Parts = nil
		case len(parts) == 1:
			parts[0].Text = s.Narration
			s.Parts = parts
		default:
			s.Parts = []Line{{Speaker: narratorID, Text: s.Narration}}
		}
	}
	if len(s.Parts) > maxParts {
		s.Parts = s.Parts[:maxParts]
	}
	for i := range s.Parts {
		l := &s.Parts[i]
		l.Speaker = clipRunes(strings.TrimSpace(l.Speaker), 40)
		if !IsNarrator(l.Speaker) && !cids[l.Speaker] {
			l.Speaker = narratorID
		}
		l.Text = clipRunes(l.Text, 600)
		l.Delivery = clipRunes(strings.TrimSpace(l.Delivery), 120)
		// Timings belong to the voice step: kept while the part says the same.
		l.Start, l.End = 0, 0
		if existed && i < len(old.Parts) {
			if o := old.Parts[i]; o.Speaker == l.Speaker && strings.TrimSpace(o.Text) == strings.TrimSpace(l.Text) {
				l.Start, l.End = o.Start, o.End
			}
		}
	}
	syncParts(s)
	return !existed || !sameParts(old.Parts, s.Parts)
}

// ---- the plan -------------------------------------------------------------------------------

// multiVoiceRules are the bible rules of the "parts" of a multi-voice scene.
func multiVoiceRules(p *Project, words, clip int, sameLang string, script bool, lang string) []string {
	n := extraNarrators(p.Input)
	who := `"speaker" is "narrator" for the narration`
	switch n {
	case 1:
		who += `, "narrator2" for a SECOND narrator — a contrasting co-host voice: give it whole passages (quotes, documents, ` +
			`facts, the other side of an argument) so both narrators are heard through the film`
	case 2:
		who += `, "narrator2" and "narrator3" for a second and a third narrator — contrasting co-host voices: give each whole ` +
			`passages (quotes, documents, facts, other points of view) so every narrator is heard through the film`
	}
	if !p.Input.NoPeople() {
		who += `, or the exact name of a character from "characters" for that character's OWN words spoken aloud ` +
			`(dialogue, a quote, a cry, a thought in the first person)`
	}
	say := fmt.Sprintf(`at most %d words in total (the scene is a ~%d-second shot).`, words, clip)
	if script {
		say = `the scene's given script part, copied VERBATIM and split between its speakers — never add, drop or change a word.`
		if lang != "" {
			say = `the scene's given script part, faithfully translated into ` + lang + ` and split between its speakers — nothing added or dropped.`
		}
	}
	out := []string{
		`- "parts": the voice-over of this scene, ` + sameLang + `, split between its speakers in the order they speak — ` + say,
		`  ` + who + `. Everything else stays with "narrator".`,
		`  Each part is a whole phrase or sentence; no names, quotes marks around the whole part or stage directions inside "text".`,
		`  "delivery": ENGLISH, 2–5 words on how the part is said (e.g. "whispers, scared", "warm, calm"), or "".`,
	}
	if !p.Input.NoPeople() && !script {
		out = append(out, `  Let the characters speak where the story has dialogue — a film where only the narrator talks wastes the cast's voices.`)
	}
	return out
}

// partSpeaker resolves a speaker written by the model: a narrator id, a
// character (by name, alias or tag — never an object), else the narrator.
func partSpeaker(in Input, raw string, byName func(n any) *Character) string {
	k := strings.ToLower(strings.TrimSpace(raw))
	k = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(k)
	switch k {
	case "", "narrator", "narrator1", "диктор", "диктор1", "оповідач", "голосзакадром", "voiceover":
		return narratorID
	case "narrator2", "диктор2", "оповідач2":
		if extraNarrators(in) >= 1 {
			return narrator2ID
		}
		return narratorID
	case "narrator3", "диктор3", "оповідач3":
		if extraNarrators(in) >= 2 {
			return narrator3ID
		}
		return narratorID
	}
	if c := byName(raw); c != nil && !c.IsObject() {
		return c.ID
	}
	return narratorID
}

// planParts reads a bible scene's "parts". script: the user's own words for
// the scene (verbatim, "" = none) — parts that do not say exactly those words
// are replaced by one narrator part saying them.
func planParts(in Input, scene any, script string, byName func(n any) *Character) []Line {
	var parts []Line
	for _, l := range asArray(get(scene, "parts")) {
		text := strings.TrimSpace(jsString(get(l, "text")))
		if text == "" {
			continue
		}
		parts = append(parts, Line{
			Speaker:  partSpeaker(in, jsString(get(l, "speaker")), byName),
			Text:     clipRunes(text, 600),
			Delivery: clipRunes(strings.TrimSpace(jsString(get(l, "delivery"))), 120),
		})
	}
	parts = cleanParts(parts)
	if script = strings.TrimSpace(script); script != "" && (len(parts) == 0 || letters(LinesText(parts)) != letters(script)) {
		return []Line{{Speaker: narratorID, Text: script}}
	}
	if len(parts) == 0 {
		if n := strings.TrimSpace(jsString(get(scene, "narration"))); n != "" {
			return []Line{{Speaker: narratorID, Text: clipRunes(n, 600)}}
		}
	}
	return parts
}

// letters is text reduced to its lower-case letters and digits (a verbatim
// check that ignores spacing, punctuation and quote marks).
func letters(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---- voices -----------------------------------------------------------------------------------

// voiceAge is a catalogue voice's age group ("" = unknown).
func voiceAge(age string) string {
	switch strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(strings.TrimSpace(age))) {
	case "child", "kid", "children":
		return AgeChild
	case "young", "teen", "young adult":
		return AgeYoung
	case "middle aged", "adult", "middle":
		return AgeAdult
	case "old", "senior", "elderly":
		return AgeOld
	}
	return ""
}

// normAge is a character's age group as the plan wrote it ("" = unknown).
func normAge(age string) string {
	a := strings.ToLower(strings.TrimSpace(age))
	switch {
	case a == "":
		return ""
	case strings.Contains(a, "child") || strings.Contains(a, "kid") || strings.Contains(a, "boy") || strings.Contains(a, "girl"):
		return AgeChild
	case strings.Contains(a, "teen") || strings.Contains(a, "young"):
		return AgeYoung
	case strings.Contains(a, "old") || strings.Contains(a, "elder") || strings.Contains(a, "senior"):
		return AgeOld
	case strings.Contains(a, "adult") || strings.Contains(a, "middle"):
		return AgeAdult
	}
	return voiceAge(a)
}

var ageRank = map[string]int{AgeChild: 0, AgeYoung: 1, AgeAdult: 2, AgeOld: 3}

// normGender is "male" | "female" | "" (anything else).
func normGender(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "male", "man", "m":
		return "male"
	case "female", "woman", "f":
		return "female"
	}
	return ""
}

// voiceLang is the language of a catalogue voice ("" = any language).
func voiceLang(v tts.Voice) string {
	base, _, _ := strings.Cut(strings.ReplaceAll(strings.TrimSpace(v.Language), "_", "-"), "-")
	return tts.NormalizeLanguage(base)
}

// voiceScore rates a catalogue voice for a speaker: the gender first, the
// language of the film, the age, and a voice nobody uses yet.
func voiceScore(v tts.Voice, gender, age, lang string, used bool) int {
	s := 0
	if vg := normGender(v.Gender); gender != "" && vg != "" {
		if vg == gender {
			s += 40
		} else {
			s -= 40
		}
	}
	if lang != "" {
		switch vl := voiceLang(v); {
		case vl == "":
			s += 4 // multilingual
		case vl == lang:
			s += 16
		default:
			s -= 25
		}
	}
	if va := voiceAge(v.Age); age != "" && va != "" {
		switch d := ageRank[age] - ageRank[va]; {
		case d == 0:
			s += 10
		case d == 1 || d == -1:
			s += 3
		default:
			s -= 6
		}
	}
	if used {
		s -= 30
	}
	return s
}

// pickVoice is the best usable voice for a speaker (stable: the first of
// equals, in catalogue order).
func pickVoice(voices []tts.Voice, gender, age, lang string, used map[string]bool) string {
	best, score := "", math.MinInt
	for _, v := range voices {
		if sc := voiceScore(v, gender, age, lang, used[v.ID]); sc > score {
			best, score = v.ID, sc
		}
	}
	return best
}

// narratorFor picks the narrator among usable voices: without a narrator
// kind — Settings' voice when listed, else the best for the language; with
// one (NarratorStyles, e.g. an energetic male) — Settings'
// voice when it is of that gender, else the kind's fitting voice of this
// provider when listed, else Settings' voice when its gender is unknown,
// else the best voice of that gender and age for the language.
func narratorFor(kind string, usable []tts.Voice, pref, lang string, known func(string) bool) string {
	ns, ok := NarratorStyleByID(kind)
	if !ok {
		if known(pref) {
			return pref
		}
		return pickVoice(usable, "", AgeAdult, lang, nil)
	}
	prefGender := "-" // not listed
	for _, v := range usable {
		if v.ID == pref {
			prefGender = normGender(v.Gender)
		}
	}
	if prefGender == ns.Gender {
		return pref
	}
	for _, id := range ns.Voices {
		if known(id) {
			return id
		}
	}
	if prefGender == "" { // listed, gender unknown: the user's choice stands
		return pref
	}
	return pickVoice(usable, ns.Gender, ns.Age, lang, nil)
}

// assignSpeakerVoices gives every speaker of p without a voice one from
// voices (usable ones of the current provider): the narrator (Settings'
// voice pref when listed, else the best for the film's language), the extra
// narrators (a contrasting gender first) and every character who speaks —
// a part, or a whole scene. all: pick again for every speaker (keeps nothing).
func assignSpeakerVoices(p *Project, voices []tts.Voice, pref, lang string, all bool) {
	var usable []tts.Voice
	for _, v := range voices {
		if v.Usable {
			usable = append(usable, v)
		}
	}
	if len(usable) == 0 {
		return
	}
	lang = tts.NormalizeLanguage(lang)
	known := func(id string) bool {
		for _, v := range usable {
			if v.ID == id {
				return true
			}
		}
		return false
	}
	if all || p.Input.NarratorVoiceID == "" {
		p.Input.NarratorVoiceID = narratorFor(p.Input.NarratorStyle, usable, pref, lang, known)
	}
	used := map[string]bool{p.Input.NarratorVoiceID: true}
	keep := func(id string) {
		if id != "" {
			used[id] = true
		}
	}
	if !all {
		for _, v := range p.Input.ExtraNarrators {
			keep(v)
		}
		for _, c := range p.Characters {
			keep(c.VoiceID)
		}
	}
	// The cast first (gender and age narrow their choice), then the extra
	// narrators: a contrasting gender for «Диктор 2», the narrator's for «Диктор 3».
	speaks := partSpeakers(p)
	for _, s := range p.Scenes {
		if !IsNarrator(s.Speaker) {
			speaks[s.Speaker] = true
		}
	}
	for i := range p.Characters {
		c := &p.Characters[i]
		if c.IsObject() || !speaks[c.ID] || (!all && c.VoiceID != "") {
			continue
		}
		c.VoiceID = pickVoice(usable, normGender(c.Gender), normAge(c.Age), lang, used)
		keep(c.VoiceID)
	}
	main := ""
	for _, v := range usable {
		if v.ID == p.Input.NarratorVoiceID {
			main = normGender(v.Gender)
		}
	}
	for i := 0; i < extraNarrators(p.Input); i++ {
		if !all && strings.TrimSpace(p.Input.ExtraNarrators[i]) != "" {
			continue
		}
		g := ""
		switch {
		case i == 0 && main == "male":
			g = "female"
		case i == 0 && main == "female":
			g = "male"
		case i == 1:
			g = main
		}
		id := pickVoice(usable, g, AgeAdult, lang, used)
		p.Input.ExtraNarrators[i] = id
		keep(id)
	}
}

// AssignVoices picks voices of the current TTS provider for the project's
// speakers: those without a voice, or (all) every one again. It reads the
// voice list (cached for a while) and returns its error.
func (e *Engine) AssignVoices(ctx context.Context, id string, all bool) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	list, err := e.speech.Voices(ctx, false)
	if err != nil {
		return err
	}
	pref := e.cfg.Get().TTS.Voice
	e.update(st, func(p *Project) {
		before := cloneProject(p)
		assignSpeakerVoices(p, list.Voices, pref, e.ttsOptions(p).LanguageCode, all)
		staleChangedVoices(&before, p)
	})
	return nil
}

// staleChangedVoices flags the voiced lines whose speakers' voices changed.
func staleChangedVoices(before, p *Project) {
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if s.Audio.Status != StatusDone || s.AudioSource == AudioUpload {
			continue
		}
		speakers := []string{s.Speaker}
		for _, l := range s.Parts {
			speakers = append(speakers, l.Speaker)
		}
		for _, sp := range speakers {
			if SpeakerVoiceID(before, sp) != SpeakerVoiceID(p, sp) {
				s.Audio.Stale = true
				break
			}
		}
	}
}

// ---- the voice step -------------------------------------------------------------------------

// voicedPart is a part ready to be voiced.
type voicedPart struct {
	speaker, text, delivery, voice string
}

// voicedParts are a multi-voice scene's parts with their voices (nil when
// the scene is not voiced in parts).
func voicedParts(p *Project, s *Scene, fallback string) []voicedPart {
	if !p.Input.MultiVoiceOn() || s.AudioSource == AudioUpload {
		return nil
	}
	var out []voicedPart
	for _, l := range cleanParts(s.Parts) {
		out = append(out, voicedPart{speaker: l.Speaker, text: strings.TrimSpace(l.Text), delivery: strings.TrimSpace(l.Delivery),
			voice: firstNonBlank(SpeakerVoiceID(p, l.Speaker), fallback)})
	}
	return out
}

// genVoiceParts voices a multi-voice scene into its one voice line and stores
// where every part sits in it.
func (e *Engine) genVoiceParts(ctx context.Context, st *proj, sid string, parts []voicedPart) bool {
	return e.produce(st, assetRef{slotAudio, sid}, func() (product, error) {
		data, start, end, err := e.speakParts(ctx, parts, e.voiceOptions(st))
		if err != nil {
			return product{}, err
		}
		return product{
			data: data, sub: "audio", name: fmt.Sprintf("voice-%s-%d.mp3", safeName(sid), nowMs()), probe: true,
			after: func(p *Project) []string {
				staleShortClip(p, sid) // a clip shorter than the new line is outdated
				if s := findScene(p, sid); s != nil {
					s.ClipLen = 0 // a new line: the next clip gets its length chosen again
					stampParts(s, parts, start, end)
				}
				return nil
			},
		}, nil
	})
}

// stampParts stores the parts' places in the new voice line (start/end nil:
// spread over the line by their length) — only while the scene still has
// these parts.
func stampParts(s *Scene, parts []voicedPart, start, end []float64) {
	cur := cleanParts(s.Parts)
	if len(cur) != len(parts) {
		return
	}
	for i, l := range cur {
		if l.Speaker != parts[i].speaker || strings.TrimSpace(l.Text) != parts[i].text {
			return
		}
	}
	if len(start) != len(parts) || len(end) != len(parts) {
		start, end = spreadParts(parts, s.Audio.Duration)
	}
	for i := range cur {
		cur[i].Start, cur[i].End = math.Round(start[i]*1000)/1000, math.Round(end[i]*1000)/1000
	}
	s.Parts = cur
}

// spreadParts places parts over a line of dur seconds by their length.
func spreadParts(parts []voicedPart, dur float64) (start, end []float64) {
	start, end = make([]float64, len(parts)), make([]float64, len(parts))
	total := 0
	for _, p := range parts {
		total += max(1, len([]rune(p.text)))
	}
	at := 0.0
	for i, p := range parts {
		d := dur * float64(max(1, len([]rune(p.text)))) / float64(max(1, total))
		start[i], end[i] = at, at+d
		at += d
	}
	return start, end
}

// speakParts voices the parts: one request when one voice says everything,
// one ElevenLabs text-to-dialogue request when the service can, else every
// part on its own, joined with short pauses (FFmpeg). start/end = nil: the
// parts are spread over the line by their length.
func (e *Engine) speakParts(ctx context.Context, parts []voicedPart, opt tts.SynthOptions) (data []byte, start, end []float64, err error) {
	voices := map[string]bool{}
	chars := 0
	for _, p := range parts {
		voices[p.voice] = true
		chars += len([]rune(p.text))
	}
	if len(voices) == 1 {
		texts := make([]string, len(parts))
		for i, p := range parts {
			texts[i] = p.text
		}
		data, err = e.speech.SynthesizeWith(ctx, strings.Join(texts, " "), parts[0].voice, opt)
		return data, nil, nil, err
	}
	if len(voices) <= tts.DialogueMaxVoices && chars <= tts.DialogueMaxChars && e.speech.CanDialogue(ctx, opt.LanguageCode) {
		lines := make([]tts.DialogueLine, len(parts))
		for i, p := range parts {
			text := p.text
			if p.delivery != "" { // Eleven v3 audio tags
				text = "[" + strings.Trim(p.delivery, "[] ") + "] " + text
			}
			lines[i] = tts.DialogueLine{Text: text, Voice: p.voice}
		}
		d, derr := e.speech.SynthesizeDialogue(ctx, lines, opt)
		if derr == nil {
			if placed(d.Start, d.End) {
				return d.Audio, d.Start, d.End, nil
			}
			return d.Audio, nil, nil, nil
		}
		if isCancel(derr) || ctx.Err() != nil {
			return nil, nil, nil, derr
		}
		log.Printf("[studio] dialogue in one request failed, voicing the parts one by one: %v", derr)
	}
	return e.joinParts(ctx, parts, opt)
}

// placed reports dialogue timings that place every line.
func placed(start, end []float64) bool {
	for i := range start {
		if !(end[i] > start[i]) {
			return false
		}
	}
	return len(start) > 0
}

// joinParts voices every part with its voice and joins them into one line.
func (e *Engine) joinParts(ctx context.Context, parts []voicedPart, opt tts.SynthOptions) ([]byte, []float64, []float64, error) {
	ff := e.ffNow()
	if ff.FFmpeg == "" || ff.FFprobe == "" {
		return nil, nil, nil, errors.New("Для мультиголосу потрібен FFmpeg — він зшиває репліки в одну доріжку. " + media.ErrNotFound.Error())
	}
	dir, err := os.MkdirTemp("", "ytc-parts-*")
	if err != nil {
		return nil, nil, nil, err
	}
	defer os.RemoveAll(dir)
	files := make([]string, len(parts))
	gaps := make([]float64, len(parts))
	for i, p := range parts {
		data, err := e.speech.SynthesizeWith(ctx, p.text, p.voice, opt)
		if err != nil {
			if len(parts) > 1 && !isCancel(err) {
				err = fmt.Errorf("репліка %d (%s): %w", i+1, clipRunes(p.text, 40), err)
			}
			return nil, nil, nil, err
		}
		files[i] = filepath.Join(dir, fmt.Sprintf("part-%02d.mp3", i))
		if err := os.WriteFile(files[i], data, 0o644); err != nil {
			return nil, nil, nil, err
		}
		if i > 0 {
			gaps[i] = partGapSame
			if p.speaker != parts[i-1].speaker {
				gaps[i] = partGapTurn
			}
		}
	}
	out := filepath.Join(dir, "line.mp3")
	start, end, err := ff.JoinVoices(ctx, files, gaps, out)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, nil, ctx.Err()
		}
		return nil, nil, nil, fmt.Errorf("не вдалося зшити репліки: %w", err)
	}
	data, err := os.ReadFile(out)
	return data, start, end, err
}

// voiceParts are a scene's timed parts for the montage subtitles (nil
// unless every part is placed in the current voice line); colours when the
// subtitle style asks for speaker colours.
func voiceParts(p *Project, s *Scene) []media.VoicePart {
	if s.AudioSource == AudioUpload || s.Audio.Stale {
		return nil
	}
	parts := cleanParts(s.Parts)
	if len(parts) == 0 {
		return nil
	}
	colors := p.Montage.SubtitleStyle.SpeakerColors
	out := make([]media.VoicePart, 0, len(parts))
	for _, l := range parts {
		if !(l.End > l.Start) {
			return nil
		}
		vp := media.VoicePart{Text: strings.TrimSpace(l.Text), Start: l.Start, End: l.End}
		if colors {
			vp.Color = SpeakerColor(p, l.Speaker)
		}
		out = append(out, vp)
	}
	return out
}

// applyParts puts a translation's parts into a multi-voice scene: the same
// speakers say the new words (the timings wait for the new voice line).
func applyParts(s *Scene, r lineResult) {
	parts := cleanParts(s.Parts)
	if len(parts) == 0 || len(r.parts) != len(parts) {
		return
	}
	for i := range parts {
		if t := strings.TrimSpace(r.parts[i]); t != "" {
			parts[i].Text = clipRunes(t, 600)
		}
		parts[i].Start, parts[i].End = 0, 0
	}
	s.Parts = parts
	syncParts(s)
}
