package studio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
)

// profileMock answers the profile requests with canned replies (one per
// call, the last one repeats) and records what the engine sent.
type profileMock struct {
	mu       sync.Mutex
	replies  []string
	requests []string
}

func (m *profileMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if r.Header.Get("Authorization") != "Bearer txt-test" || json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 1 {
		w.WriteHeader(400)
		return
	}
	m.mu.Lock()
	m.requests = append(m.requests, req.Messages[0].Content)
	reply := "OK"
	if n := len(m.replies); n > 0 {
		reply = m.replies[min(len(m.requests), n)-1]
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}},
	})
}

func (m *profileMock) calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.requests...)
}

// newProfileEnv is the harness engine with the text model pointed at a profileMock.
func newProfileEnv(t *testing.T, replies ...string) (*testEnv, *profileMock) {
	t.Helper()
	env := newEnv(t, nil)
	pm := &profileMock{replies: replies}
	srv := httptest.NewServer(pm)
	allow(srv)
	t.Cleanup(srv.Close)
	_ = env.store.Update(func(c *config.Config) { c.Text.BaseURL = srv.URL + "/v1" }) // in-memory store
	return env, pm
}

const shortsDescription = "Роблю короткі вертикальні шортси про космос англійською для підлітків — динамічно, з великими субтитрами, трохи моторошно."

// A complete, valid answer of the model.
const shortsReply = "```json\n" + `{
  "name": "Космос · Shorts EN",
  "targetSeconds": 45,
  "aspect": "9:16",
  "style": "space-drama",
  "customStyle": "",
  "genre": "space",
  "tempo": "fast",
  "camera": "dynamic",
  "imageModel": "GEM_PIX_2",
  "voiceover": true,
  "language": "en",
  "tone": "eerie",
  "notes": "Підлітки 13–17 років. Гачок у першій секунді, жодної нудної теорії — лише загадки космосу.",
  "storyboardMode": "grid3x3",
  "chainFrames": false,
  "transition": "zoomin",
  "transitionDuration": 0.3,
  "filter": "cool",
  "effect": "pushin",
  "subtitles": "burn",
  "subtitlePreset": "shorts",
  "musicVolume": 0.22,
  "ambientVolume": 0.15,
  "ducking": true,
  "reasons": [
    "Вертикальний формат 9:16 і 45 секунд — саме для Shorts.",
    "Стиль «Космічна драма» і жанр «Космос» відповідають темі.",
    "Англійська мова й моторошний тон — як ти просив.",
    "Великі субтитри по центру тримають увагу без звуку."
  ]
}` + "\n```"

func TestSuggestProfileFromDescription(t *testing.T) {
	t.Parallel()
	env, pm := newProfileEnv(t, shortsReply)
	e := env.e
	before := len(e.Profiles())

	p, reasons, err := e.SuggestProfileWithReasons(context.Background(), "  "+shortsDescription+"  ")
	must(t, err)
	want := config.Profile{
		Name: "Космос · Shorts EN", TargetSeconds: 45, Aspect: "9:16", Style: "space-drama", Genre: "space", Tempo: "fast",
		Camera: "dynamic", ImageModel: "GEM_PIX_2", Voiceover: true, Language: "en", Tone: "eerie",
		Notes:          "Підлітки 13–17 років. Гачок у першій секунді, жодної нудної теорії — лише загадки космосу.",
		StoryboardMode: "grid3x3", Transition: "zoomin", TransitionDuration: 0.3, Filter: "cool", Effect: "pushin",
		SubtitlePreset: "shorts", Subtitles: "burn", MusicVolume: 0.22, AmbientVolume: 0.15, Ducking: true,
	}
	if p != want {
		t.Fatalf("profile\n got %+v\nwant %+v", p, want)
	}
	if len(reasons) != 4 || !strings.HasPrefix(reasons[0], "Вертикальний формат 9:16") {
		t.Fatalf("reasons %q", reasons)
	}
	if len(e.Profiles()) != before {
		t.Fatal("a suggestion must not be saved")
	}
	t.Logf("suggested: %+v", p)
	t.Logf("reasons: %q", reasons)

	// One request, carrying the catalogues and the user's words.
	calls := pm.calls()
	if len(calls) != 1 {
		t.Fatalf("%d requests", len(calls))
	}
	t.Logf("request size: %d bytes", len(calls[0]))
	mustContain(t, "profile request", calls[0], profileMarker, `"petrykivka" — Петриківський розпис`, `"fairy tale" — Казка`,
		`"drone" — Аерозйомка з дрона`, `"eerie" — Моторошний`, `"uk" Ukrainian`, `"GEM_PIX_2" — Nano Banana Pro`, `"grid3x3"`,
		`"dissolve" — Розчинення`, `"pushin" — Наїзд`, `"golden" — Золота година`, `"youtube" — Жовті YouTube`,
		`"reasons": 3–6 short sentences in UKRAINIAN`, "### INPUT\nWhat the user says about their videos:\n\n"+shortsDescription)

	// The plain variant gives the same profile; saving it keeps every choice.
	q, err := e.SuggestProfile(context.Background(), shortsDescription)
	must(t, err)
	if q != want {
		t.Fatalf("SuggestProfile %+v", q)
	}
	saved, err := e.SaveProfile(q)
	must(t, err)
	saved.ID, saved.CreatedAt, saved.UpdatedAt = "", 0, 0
	if saved != want {
		t.Fatalf("saved %+v", saved)
	}
}

func TestSuggestProfileRepairsSloppyAnswers(t *testing.T) {
	t.Parallel()
	// Labels instead of ids, words instead of numbers, unknown ids, comments,
	// Python literals, a trailing comma and a reply cut off before its end.
	sloppy := `Ось профіль для тебе:
{
  // the user wants calm bedtime stories
  "name": "Казки на ніч",
  "targetSeconds": "4 хв",
  "aspect": "горизонтальне",
  "style": "Петриківський розпис",
  "genre": "Казка на ніч",
  "tempo": "Повільний",
  "camera": "helicopter",
  "imageModel": "Nano Banana 2 Lite",
  "voiceover": "так",
  "language": "Ukrainian",
  "tone": "Лагідний для дітей",
  "storyboardMode": "3x3 grid",
  "chainFrames": True,
  "transition": "Розчинення",
  "transitionDuration": "1,5",
  "filter": "none",
  "effect": "warp-speed",
  "subtitles": "без субтитрів",
  "subtitlePreset": "Неонові",
  "musicVolume": "15%",
  "ambientVolume": 20,
  "ducking": None,
  "reasons": "1. Казки на ніч люблять повільний темп.\n2. Петриківка — український колорит,`
	env, pm := newProfileEnv(t, sloppy)
	p, reasons, err := env.e.SuggestProfileWithReasons(context.Background(), "Українські казки на ніч для малечі, спокійні, 4 хвилини")
	must(t, err)
	if len(pm.calls()) != 1 {
		t.Fatalf("a repairable answer needs no second request (%d)", len(pm.calls()))
	}
	want := config.Profile{
		Name: "Казки на ніч", TargetSeconds: 240, Aspect: "16:9", Style: "petrykivka", Genre: "bedtime", Tempo: "slow",
		Camera:     "cinematic", // unknown → default
		ImageModel: "GEM_PIX", Voiceover: true, Language: "uk", Tone: "kids",
		Notes:          "Українські казки на ніч для малечі, спокійні, 4 хвилини", // no notes → the user's words
		StoryboardMode: "grid3x3", ChainFrames: true, Transition: "dissolve", TransitionDuration: 1.5,
		Filter: "", Effect: "", SubtitlePreset: "", Subtitles: "none", MusicVolume: 0.15, AmbientVolume: 0.2,
		Ducking: true, // null → default
	}
	if p != want {
		t.Fatalf("profile\n got %+v\nwant %+v", p, want)
	}
	if len(reasons) != 2 || reasons[0] != "Казки на ніч люблять повільний темп." || reasons[1] != "Петриківка — український колорит," {
		t.Fatalf("reasons %q", reasons)
	}
}

func TestSuggestProfileRetriesOnce(t *testing.T) {
	t.Parallel()
	t.Run("second answer is JSON", func(t *testing.T) {
		t.Parallel()
		env, pm := newProfileEnv(t, "Вибач, я не впевнений, що зрозумів.", `{"name":"Друга спроба","aspect":"9:16"}`)
		p, reasons, err := env.e.SuggestProfileWithReasons(context.Background(), "шортси про котиків")
		must(t, err)
		calls := pm.calls()
		if len(calls) != 2 || strings.Contains(calls[0], "could not be read as JSON") || !strings.Contains(calls[1], "could not be read as JSON") {
			t.Fatalf("retry requests %d", len(calls))
		}
		// Everything the model left out: defaults for a vertical channel.
		if p.Name != "Друга спроба" || p.Aspect != "9:16" || p.TargetSeconds != 48 || p.Tempo != "fast" || p.Camera != "dynamic" ||
			p.Subtitles != "burn" || p.SubtitlePreset != "shorts" || p.Transition != "none" || p.Style != "cinematic" ||
			p.ImageModel != "NARWHAL" || !p.Voiceover || !p.Ducking || p.Notes != "шортси про котиків" || p.Language != "" {
			t.Fatalf("defaults %+v", p)
		}
		// No reasons from the model: a short summary of the choices instead.
		if len(reasons) < 3 || !strings.HasPrefix(reasons[0], "Вертикальний формат 9:16 — для Shorts") || !strings.Contains(reasons[1], "Стиль «Кіно»") {
			t.Fatalf("summary %q", reasons)
		}
		t.Logf("fallback reasons: %q", reasons)
	})
	t.Run("never JSON", func(t *testing.T) {
		t.Parallel()
		env, pm := newProfileEnv(t, "Не можу.")
		_, _, err := env.e.SuggestProfileWithReasons(context.Background(), "документалки про історію")
		if !errors.Is(err, errNoProfileJSON) || len(pm.calls()) != 2 {
			t.Fatalf("err %v after %d requests", err, len(pm.calls()))
		}
	})
}

func TestSuggestProfileInputErrors(t *testing.T) {
	t.Parallel()
	env, pm := newProfileEnv(t, shortsReply)
	if _, _, err := env.e.SuggestProfileWithReasons(context.Background(), "   "); err == nil || !strings.Contains(err.Error(), "Опиши, які відео ти робиш") {
		t.Fatalf("empty description: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := env.e.SuggestProfile(ctx, shortsDescription); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if n := len(pm.calls()); n != 0 {
		t.Fatalf("%d requests for invalid input", n)
	}
	// A vertical channel is recognised from the words when the model gives no aspect.
	env2, _ := newProfileEnv(t, `{"name":"TikTok"}`)
	if p, err := env2.e.SuggestProfile(context.Background(), "Роблю TikTok-ролики про футбол"); err != nil || p.Aspect != "9:16" {
		t.Fatalf("vertical from description: %+v %v", p.Aspect, err)
	}
}

func TestRepairJSON(t *testing.T) {
	cases := map[string]string{
		`{"a": 1,}`:                           `{"a": 1}`,
		`{"a": [1, 2, ], }`:                   `{"a": [1, 2]}`,
		"{\"a\": 1 // note\n}":                "{\"a\": 1 \n}",
		`{"a": /* x */ True, "b": None}`:      `{"a":  true, "b": null}`,
		`{"url": "http://x//y", "t": "True"}`: `{"url": "http://x//y", "t": "True"}`,
		`{"a": "cut off`:                      `{"a": "cut off"}`,
		`{"a": [1, {"b": 2`:                   `{"a": [1, {"b": 2}]}`,
		`{"Trueish": "a, }"}`:                 `{"Trueish": "a, }"}`,
	}
	for in, want := range cases {
		if got := repairJSON(in); got != want {
			t.Errorf("repairJSON(%q) = %q, want %q", in, got, want)
		}
	}
	// A wrapper object and an array around the profile are unwrapped.
	if o := parseProfileReply(`{"profile":{"name":"X"},"reasons":["a"]}`); o == nil || jsString(get(o, "name")) != "X" || len(asArray(get(o, "reasons"))) != 1 {
		t.Fatal("wrapper")
	}
	if o := parseProfileReply(`[{"name":"Y"}]`); o == nil || jsString(get(o, "name")) != "Y" {
		t.Fatal("array")
	}
	if parseProfileReply("no json here") != nil || parseProfileReply(`"just a string"`) != nil {
		t.Fatal("non-objects must be rejected")
	}
}

func TestSuggestValueParsers(t *testing.T) {
	for in, want := range map[any]float64{"3 хв": 180, "2 min": 120, "45 s": 45, "1 год": 3600, 90.0: 90, "1.5h": 5400} {
		if got, ok := parseSeconds(in); !ok || got != want {
			t.Errorf("parseSeconds(%v) = %v %v, want %v", in, got, ok, want)
		}
	}
	if _, ok := parseSeconds("довго"); ok {
		t.Error("no number")
	}
	for in, want := range map[any]float64{0.3: 0.3, "25%": 0.25, 40.0: 0.4, 250.0: 1, "0,2": 0.2} {
		if got, ok := parseVolume(in); !ok || got != want {
			t.Errorf("parseVolume(%v) = %v %v, want %v", in, got, ok, want)
		}
	}
	for in, want := range map[string]string{"en": "en", "ENG": "en", "English": "en", "Англійська": "en", "Deutsch": "de", "auto": "", "klingon": "", "": ""} {
		if got := matchLanguage(in); got != want {
			t.Errorf("matchLanguage(%q) = %q, want %q", in, got, want)
		}
	}
	if got := parseReasons([]any{"- Перша причина.", "2) Друга.", "3 хвилини — оптимально для YouTube.", "  ", 7.0}); strings.Join(got, "|") !=
		"Перша причина.|Друга.|3 хвилини — оптимально для YouTube.|7" {
		t.Errorf("parseReasons %q", got)
	}
	if id, ok := matchChoice(Genres, "Трукрайм"); !ok || id != "true crime" {
		t.Errorf("genre by label: %q", id)
	}
	if _, ok := matchChoice(Genres, "space opera"); ok {
		t.Error("unknown genre accepted")
	}
}
