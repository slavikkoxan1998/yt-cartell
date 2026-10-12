package studio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode"

	"ytcartell/internal/config"
	"ytcartell/internal/tts"
)

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// Trademarked studio, product and franchise names must not appear in the
// catalogue (whole words: "allegory" is fine, "lego" is not).
var reTrademark = regexp.MustCompile(`\b(pixar|disney|ghibli|minecraft|kurzgesagt|soyuzmult\w*|marvel|dc comics|lego|dreamworks|` +
	`simpsons|roblox|fortnite|pok[eé]mon|nintendo|looney|hanna-barbera|unreal engine|octane|shinkai|miyazaki|attack on titan|` +
	`arcane|spider-verse|national geographic|nat geo|netflix|bbc|hbo)\b`)

func TestCatalogStyles(t *testing.T) {
	wantCats := []string{"animation", "comics", "cinema", "art", "explainer", "effects"}
	if len(StyleCategories) != len(wantCats) {
		t.Fatalf("categories %v", StyleCategories)
	}
	cats := map[string]int{}
	for i, c := range StyleCategories {
		if c.ID != wantCats[i] || !hasCyrillic(c.Label) {
			t.Fatalf("category %d: %+v", i, c)
		}
		cats[c.ID] = 0
	}
	if len(Styles) < 60 {
		t.Fatalf("%d styles, want 60+", len(Styles))
	}
	reID := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	reHex := regexp.MustCompile(`^#[0-9A-F]{6}$`)
	seen := map[string]bool{}
	for _, s := range Styles {
		if !reID.MatchString(s.ID) || seen[s.ID] {
			t.Fatalf("style id %q bad or duplicate", s.ID)
		}
		seen[s.ID] = true
		if strings.TrimSpace(s.Label) == "" || len(s.Prompt) < 60 || hasCyrillic(s.Prompt) {
			t.Errorf("%s: label %q, prompt must be English and specific: %q", s.ID, s.Label, s.Prompt)
		}
		if _, ok := cats[s.Category]; !ok {
			t.Errorf("%s: category %q", s.ID, s.Category)
		}
		cats[s.Category]++
		if !strings.HasPrefix(s.Suits, "підходить для ") || len([]rune(s.Suits)) > 90 {
			t.Errorf("%s: suits %q", s.ID, s.Suits)
		}
		for _, c := range s.Palette {
			if !reHex.MatchString(c) {
				t.Errorf("%s: palette %v", s.ID, s.Palette)
			}
		}
		if tm := reTrademark.FindString(strings.ToLower(s.ID + " " + s.Label + " " + s.Prompt)); tm != "" {
			t.Errorf("%s mentions %q", s.ID, tm)
		}
		if got, ok := StyleByID(s.ID); !ok || got.Prompt != s.Prompt {
			t.Errorf("StyleByID(%s)", s.ID)
		}
	}
	for id, n := range cats {
		if n < 3 {
			t.Errorf("category %s has only %d styles", id, n)
		}
	}
	for _, id := range []string{"science-explainer", "chalkboard", "whiteboard", "infographic", "archival-footage", "photo-1940s",
		"aged-realism", "faceless-cinematic", "golden-allegory", "wildlife-doc", "botanical-macro", "cosmic-glow", "space-drama",
		"grunge-novel", "fantasy-comic", "ink-manuscript", "ukiyo-e", "soviet-realism", "soviet-animation", "petrykivka",
		"family-3d", "voxel", "low-poly", "papercraft", "felt-stop-motion", "anime-glow", "anime-drama", "chibi", "marker-cartoon",
		"cyberpunk", "vaporwave", "art-deco", "linocut", "stained-glass", "charcoal", "sumi-e", "gouache-book", "crayon", "isometric"} {
		if !seen[id] {
			t.Errorf("missing style %s", id)
		}
	}
	if _, ok := StyleByID("nope"); ok {
		t.Fatal("unknown style found")
	}
	if StylePrompt("petrykivka", "") == "" || !strings.Contains(StylePrompt("faceless-cinematic", ""), "faces are never visible") {
		t.Fatal("StylePrompt of the extended catalogue")
	}
}

func TestCatalogChoices(t *testing.T) {
	check := func(name string, list []Choice, min int, firstIDs ...string) {
		t.Helper()
		if len(list) < min {
			t.Fatalf("%s: %d entries, want %d+", name, len(list), min)
		}
		seen := map[string]bool{}
		for i, c := range list {
			if c.ID == "" || seen[c.ID] || !hasCyrillic(c.Label) || len(c.Hint) < 15 || hasCyrillic(c.Hint) {
				t.Fatalf("%s: bad entry %+v", name, c)
			}
			seen[c.ID] = true
			if i < len(firstIDs) && c.ID != firstIDs[i] {
				t.Fatalf("%s[%d] = %s, want %s", name, i, c.ID, firstIDs[i])
			}
			if got, ok := ChoiceByID(list, c.ID); !ok || got != c {
				t.Fatalf("%s: ChoiceByID(%s)", name, c.ID)
			}
		}
	}
	check("genres", Genres, 30, "fairy tale", "adventure", "drama", "comedy", "horror", "thriller", "detective", "action",
		"romance", "sci-fi", "epic", "documentary")
	check("cameras", Cameras, 9, "cinematic", "medium", "wide", "dynamic", "handheld", "drone", "tripod", "pov", "macro")
	check("tempos", Tempos, 3, "slow", "balanced", "fast")
	check("tones", Tones, 9, "storyteller", "documentary", "trailer", "youtuber", "calm", "humor", "eerie", "kids", "motivational")
	for _, id := range []string{"true crime", "history", "mythology", "parables", "psychology", "money", "pov", "survival", "space",
		"science", "geopolitics", "empire", "medieval fantasy", "mystery", "folk horror", "animals", "bedtime", "motivation",
		"book summary", "biography"} {
		if _, ok := ChoiceByID(Genres, id); !ok {
			t.Errorf("missing genre %q", id)
		}
	}
	// The existing web texts stay word for word (projects and tests rely on them).
	if hintOf(Tempos, "slow") != "Pacing: slow and contemplative, lingering shots." ||
		hintOf(Cameras, "dynamic") != "Camera language: dynamic — whip pans, fast push-ins, handheld energy." {
		t.Fatal("web hints changed")
	}
	if len(Tempos) != 4 || !knownCamera("drone") || knownCamera("warp") {
		t.Fatal("tempo/camera lookups")
	}
}

// elevenV3 is the Eleven v3 language list of the ElevenLabs model docs.
var elevenV3 = strings.Fields("af ar hy as az be bn bs bg ca ceb ny hr cs da nl en et fil fi fr gl ka de el gu ha he hi hu is id ga it " +
	"ja jv kn kk ky ko lv ln lt lb mk ms ml zh mr ne no ps fa pl pt pa ro ru sr sd sk sl so es sw sv ta te th tr uk ur vi cy")

func TestCatalogLanguages(t *testing.T) {
	if len(Languages) < 75 {
		t.Fatalf("%d languages, want 75+", len(Languages))
	}
	if Languages[0].Code != "uk" || Languages[0].Label != "Українська" || Languages[1].Code != "en" {
		t.Fatalf("Ukrainian (then English) comes first: %+v %+v", Languages[0], Languages[1])
	}
	reCode := regexp.MustCompile(`^[a-z]{2,3}$`)
	seen := map[string]bool{}
	for _, l := range Languages {
		if !reCode.MatchString(l.Code) || seen[l.Code] || l.English == "" || l.Native == "" || !hasCyrillic(l.Label) {
			t.Fatalf("bad language %+v", l)
		}
		seen[l.Code] = true
		if got := tts.NormalizeLanguage(l.Code); got != l.Code {
			t.Errorf("tts does not know %s (%s): %q", l.Code, l.English, got)
		}
		if got := tts.NormalizeLanguage(l.English); got != l.Code {
			t.Errorf("tts cannot map the name %q: %q", l.English, got)
		}
	}
	if len(elevenV3) != 74 {
		t.Fatalf("v3 list %d", len(elevenV3))
	}
	for _, c := range elevenV3 {
		if !seen[c] {
			t.Errorf("Eleven v3 language %s missing", c)
		}
	}
	if l, ok := LanguageByCode(" UK "); !ok || l.English != "Ukrainian" {
		t.Fatal("LanguageByCode")
	}
	if languageName("en") != "English" || languageName("xx-custom") != "xx-custom" || languageName("") != "" {
		t.Fatal("languageName")
	}
}

// ---- planner directives --------------------------------------------------------------------

func planProject(mut func(in *Input)) *Project {
	p := &Project{Input: DefaultInput()}
	p.Input.Text = "Жила-була лисичка."
	if mut != nil {
		mut(&p.Input)
	}
	return p
}

func mustContain(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, s := range parts {
		if !strings.Contains(text, s) {
			t.Fatalf("%s lacks %q:\n%s", what, s, text)
		}
	}
}

func mustLack(t *testing.T, what, text string, parts ...string) {
	t.Helper()
	for _, s := range parts {
		if strings.Contains(text, s) {
			t.Fatalf("%s must not contain %q:\n%s", what, s, text)
		}
	}
}

func TestBiblePromptLanguageAndTone(t *testing.T) {
	// Auto: the language of the user's text (the web behaviour, word for word).
	sys, prompt := biblePrompt(planProject(nil), 3, 18, nil, 8)
	mustContain(t, "auto", sys,
		`"title": short catchy title in the language of the user's text,`,
		`"logline": one-sentence summary in the language of the user's text,`,
		`"narration": the voice-over line for this scene in the SAME LANGUAGE as the user's text, at most 18 words (it must be speakable in about 8 seconds).`)
	mustLack(t, "auto", sys, "Audience language", "Voice-over tone", "sourceLanguage", "Genre")
	if prompt != "Жила-була лисичка." {
		t.Fatalf("prompt %q", prompt)
	}
	if strings.Contains(sys, "\n\n\n") || strings.HasPrefix(sys, "\n") {
		t.Fatalf("blank directive lines:\n%s", sys)
	}

	// English film from a Ukrainian story, eerie tone, catalogue genre.
	sys, _ = biblePrompt(planProject(func(in *Input) { in.Language, in.Tone, in.Genre = "en", "eerie", "true crime" }), 3, 18, nil, 8)
	mustContain(t, "english", sys,
		`Audience language: English. Write EVERY audience-facing text — "title", "logline", every scene "title" and "narration", all dialogue — in English, even when the user's text is written in another language.`,
		`Adapt character and place names naturally for English speakers`,
		`never translate a name word by word`,
		`Keep "styleGuide" and every "description" in ENGLISH`,
		`"title": short catchy title in English,`,
		`"logline": one-sentence summary in English,`,
		`"narration": the voice-over line for this scene in English, at most 18 words`,
		hintOf(Tones, "eerie"),
		"Genre — True crime: a real-feeling case told step by step")
	mustLack(t, "english", sys, "in the language of the user's text", "SAME LANGUAGE", "sourceLanguage")

	// Unknown tone/genre/language ids pass through as free text.
	sys, _ = biblePrompt(planProject(func(in *Input) { in.Language, in.Tone, in.Genre = "tlh", "whispering", "cyber-noir" }), 2, 18, nil, 8)
	mustContain(t, "free text", sys, "Audience language: tlh.", "Voice-over tone: whispering.", "Genre: cyber-noir — match its mood, lighting and pacing.")

	// Languages written without spaces get a time budget too.
	sys, _ = biblePrompt(planProject(func(in *Input) { in.Language = "ja" }), 2, 18, nil, 8)
	mustContain(t, "japanese", sys, "in Japanese, at most 18 words", "Japanese is written without spaces: keep each line to what a narrator says in about 8 seconds.")

	// Captions only (no voice-over) are written in the film's language too.
	sys, _ = biblePrompt(planProject(func(in *Input) { in.Language, in.Voiceover = "de", false }), 2, 18, nil, 8)
	mustContain(t, "captions", sys, `"narration": one short sentence describing the moment (used only as a subtitle/caption), in German.`)
}

func TestBiblePromptScriptTranslation(t *testing.T) {
	chunks := []string{"Перша частина.", "Друга частина."}
	// Same language: verbatim, as before.
	sys, prompt := biblePrompt(planProject(func(in *Input) { in.Mode = ModeScript }), 2, 18, chunks, 8)
	mustContain(t, "script", sys, `copy the given script part for this scene VERBATIM (it is already split for you)`)
	mustContain(t, "script prompt", prompt, "already split into 2 scene parts:\n\n[1] Перша частина.\n[2] Друга частина.")
	mustLack(t, "script", sys, "TRANSLATED", "sourceLanguage")

	sys, prompt = biblePrompt(planProject(func(in *Input) { in.Mode, in.Language = ModeScript, "en" }), 2, 18, chunks, 8)
	mustContain(t, "translated script", sys,
		`"narration": the given script part for this scene faithfully TRANSLATED into English — keep the meaning, tone, names and every detail; do not shorten, summarise or add anything. If the part is already in English, copy it VERBATIM.`,
		`"sourceLanguage": ISO 639-1 code of the language the user's script is written in,`)
	mustContain(t, "translated prompt", prompt, "Translate each part into English as the narration of its scene", "[2] Друга частина.")
}

func TestShotsPromptLanguage(t *testing.T) {
	p := planProject(func(in *Input) { in.Language, in.Camera = "en", "drone" })
	p.StyleGuide = "Watercolor."
	p.Scenes = []Scene{{ID: "s1", Title: "The Fox", Narration: "Once upon a time.", CharacterIDs: []string{}}}
	sys, prompt := shotsPrompt(p, []int{0}, "більше туману")
	mustContain(t, "shots", sys,
		`The titles and voice-over lines are in English; write "shot", "imagePrompt" and "motionPrompt" in ENGLISH regardless.`,
		hintOf(Cameras, "drone"), "Film style guide: Watercolor.")
	mustContain(t, "shots prompt", prompt,
		"The user's wish may be written in any language — follow it, but keep every prompt in English.")
	if !strings.HasSuffix(prompt, "\nExtra direction from the user for this rewrite: більше туману") {
		t.Fatalf("the wish must end the prompt:\n%s", prompt)
	}
	sys, prompt = shotsPrompt(planProject(nil), nil, "")
	mustLack(t, "auto shots", sys+prompt, "voice-over lines are in", "wish")
}

// ---- script translation through the plan job -------------------------------------------

// langMock answers the bible pass with an English translation of every script
// part and a configurable "sourceLanguage"; shots come from the harness mock.
type langMock struct {
	mu      sync.Mutex
	source  string
	prompts []string
}

func (m *langMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if r.Header.Get("Authorization") != "Bearer txt-test" || json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 1 {
		w.WriteHeader(400)
		return
	}
	content := req.Messages[0].Content
	reply := "OK"
	switch {
	case strings.Contains(content, "award-winning film director"):
		m.mu.Lock()
		m.prompts = append(m.prompts, content)
		source := m.source
		m.mu.Unlock()
		n := 2
		if mm := reExactly.FindStringSubmatch(content); mm != nil {
			fmt.Sscan(mm[1], &n)
		}
		scenes := make([]any, n)
		for i := range scenes {
			scenes[i] = map[string]any{"title": fmt.Sprintf("Scene %d", i+1), "narration": fmt.Sprintf("English line %d.", i+1),
				"speaker": "narrator", "characters": []any{"Taras"}, "location": "Forest"}
		}
		bible := map[string]any{"title": "The Way Home", "logline": "A boy finds his way.", "styleGuide": "Soft light.",
			"characters": []any{map[string]any{"name": "Taras", "role": "boy", "gender": "male", "description": "A boy."}},
			"locations":  []any{map[string]any{"name": "Forest", "description": "A forest."}},
			"scenes":     scenes}
		if source != "" {
			bible["sourceLanguage"] = source
		}
		b, _ := json.Marshal(bible)
		reply = string(b)
	case strings.Contains(content, "storyboard artist"):
		reply = shotsReply(content)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}},
	})
}

func TestScriptModeTranslation(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	lm := &langMock{}
	srv := httptest.NewServer(lm)
	allow(srv)
	t.Cleanup(srv.Close)
	_ = env.store.Update(func(c *config.Config) { c.Text.BaseURL = srv.URL + "/v1" }) // in-memory store: nothing is written

	script := "Перше речення нашої історії про хлопчика Тараса, який заблукав у темному осінньому лісі. " +
		"Друге речення розповідає, як лисиця вивела його додому."
	plan := func(lang, source string) Project {
		t.Helper()
		lm.mu.Lock()
		lm.source = source
		lm.mu.Unlock()
		p, err := e.Create("Сценарій")
		must(t, err)
		must(t, e.Edit(p.ID, func(p *Project) {
			p.Input.Mode, p.Input.Text, p.Input.Language = ModeScript, script, lang
		}))
		must(t, e.Run(p.ID, StagePlan, false, ""))
		p = waitJob(t, e, p.ID)
		mustJobDone(t, p)
		return p
	}
	chunks := splitScript(script, wordsPerScene(e.ClipSeconds()))
	if len(chunks) != 2 {
		t.Fatalf("chunks %q", chunks)
	}
	// Ukrainian script, English film: the model's translation is voiced.
	p := plan("en", "uk")
	for i, s := range p.Scenes {
		if s.Narration != fmt.Sprintf("English line %d.", i+1) {
			t.Fatalf("scene %d narration %q, want the translation", i, s.Narration)
		}
	}
	// Source not reported: the model was told to translate (or copy): trust it.
	p = plan("en", "")
	if p.Scenes[0].Narration != "English line 1." {
		t.Fatalf("unreported source: %q", p.Scenes[0].Narration)
	}
	// The script is already in the film's language: verbatim, never the model's rewording.
	p = plan("uk", "ukr")
	for i, s := range p.Scenes {
		if s.Narration != chunks[i] {
			t.Fatalf("same language must stay verbatim: %q", s.Narration)
		}
	}
	// Auto language: verbatim as always.
	p = plan("", "uk")
	if p.Scenes[1].Narration != chunks[1] {
		t.Fatalf("auto: %q", p.Scenes[1].Narration)
	}
	lm.mu.Lock()
	first := lm.prompts[0]
	lm.mu.Unlock()
	mustContain(t, "bible request", first, "TRANSLATED into English", "Translate each part into English")

	if !translating("en", "") || translating("", "uk") || translating("uk", "UKR") || !translating("en", "uk") || translating("xx", "XX") {
		t.Fatal("translating rules")
	}
}

// ---- voice-over options -----------------------------------------------------------------

func TestTTSOptions(t *testing.T) {
	e := &Engine{}
	p := &Project{Input: DefaultInput()}
	p.Input.Language, p.Input.Tone = "en", "calm"
	opt := e.ttsOptions(p)
	if opt.LanguageCode != "en" || opt.Stability != 0.7 || opt.Speed != 0.93 || opt.Model != "" {
		t.Fatalf("options %+v", opt)
	}
	// Auto language: an unambiguous script picks the language (multilingual v2 cannot voice Georgian).
	p.Input.Language, p.Input.Tone = "", ""
	p.Scenes = []Scene{{Narration: "ერთხელ, ძველ ქალაქში, ცხოვრობდა პატარა ბიჭი."}, {Narration: "Tbilisi, 1921."}}
	if opt := e.ttsOptions(p); opt.LanguageCode != "ka" || opt.Stability != 0 || opt.Speed != 0 {
		t.Fatalf("georgian %+v", opt)
	}
	p.Scenes = nil
	p.Input.Text = "Жила-була лисичка в осінньому лісі."
	if opt := e.ttsOptions(p); opt.LanguageCode != "" {
		t.Fatalf("cyrillic stays auto: %+v", opt)
	}
	for text, want := range map[string]string{
		"שלום עולם, זה סיפור": "he", "Բարեւ աշխարհ": "hy", "สวัสดีชาวโลก": "th", "નમસ્તે દુનિયા": "gu", "ሰላም ልዑል": "am",
		"Hello world": "", "": "", "Hello ერთხელ world and more english words here": "", "123 !!!": "",
	} {
		if got := scriptLanguage(text); got != want {
			t.Errorf("scriptLanguage(%q) = %q, want %q", text, got, want)
		}
	}
}

// TestVoiceOptionsReachTTS runs the call production's voice runner makes —
// SynthesizeWith(ctx, text, voice, e.voiceOptions(st)) — against a TTS mock.
func TestVoiceOptionsReachTTS(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Unlock()
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write([]byte("ID3"))
	}))
	allow(srv)
	t.Cleanup(srv.Close)
	env := newEnv(t, func(c *config.Config) { c.TTS = config.TTS{Provider: "custom", BaseURL: srv.URL + "/tts"} })
	e := env.e
	p, err := e.Create("")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) { p.Input.Language, p.Input.Tone = "en", "youtuber" }))
	st := e.state(p.ID)
	if _, err := e.speech.SynthesizeWith(t.Context(), "Hello there", "v1", e.voiceOptions(st)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if body["language"] != "en" || body["speed"] != 1.05 || body["voice"] != "v1" || body["text"] != "Hello there" {
		t.Fatalf("request %v", body)
	}
	if opt := e.voiceOptions(&proj{p: &Project{}}); opt != (tts.SynthOptions{}) {
		t.Fatalf("empty project options %+v", opt)
	}
}

func TestNormalizeNewFields(t *testing.T) {
	raw := `{"id":"x","title":"T","input":{"text":"hi","language":" xx-custom ","tone":"whisper","storyboardMode":"grid3x3"},
		"profileId":"gone",
		"scenes":[{"id":"s1","frame":{"status":"done","path":"images/f.jpg"}},
		          {"id":"s2","board":{"status":"running","path":"images/b.jpg"}},
		          {"id":"s3","board":{"status":"queued"}}]}`
	p, err := decodeProject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	normalize(p)
	if p.Scenes[0].Board.Status != StatusEmpty || p.Scenes[1].Board.Status != StatusDone || p.Scenes[2].Board.Status != StatusError {
		t.Fatalf("boards %+v %+v %+v", p.Scenes[0].Board, p.Scenes[1].Board, p.Scenes[2].Board)
	}
	if p.Input.Language != "xx-custom" || p.Input.Tone != "whisper" || p.Input.StoryboardMode != "grid3x3" || p.ProfileID != "gone" {
		t.Fatalf("new fields must survive: %+v %q", p.Input, p.ProfileID)
	}
	// Old projects without the new fields get the defaults.
	p, _ = decodeProject([]byte(`{"id":"y","input":{"text":"hi"},"scenes":[{"id":"s"}]}`))
	normalize(p)
	if p.Input.Language != "" || p.Input.Tone != "" || p.Input.ChainFrames || p.Scenes[0].Board.Status != StatusEmpty ||
		p.Montage.CoverFinal.Status != StatusEmpty {
		t.Fatalf("defaults %+v %+v %+v", p.Input, p.Scenes[0].Board, p.Montage.CoverFinal)
	}
}

func TestEditAcceptsCatalogCameras(t *testing.T) {
	before := &Project{ID: "p", Title: "T", Step: 1, Input: DefaultInput(), Montage: DefaultMontage()}
	p := cloneProject(before)
	p.Input.Camera, p.Input.Language, p.Input.Tone = "drone", "  en ", strings.Repeat("т", 50)
	reconcile(before, &p)
	if p.Input.Camera != "drone" || p.Input.Language != "en" || len([]rune(p.Input.Tone)) != 40 {
		t.Fatalf("input %+v", p.Input)
	}
	p.Input.Camera = "warp"
	reconcile(before, &p)
	if p.Input.Camera != "cinematic" {
		t.Fatalf("unknown camera kept: %q", p.Input.Camera)
	}
}
