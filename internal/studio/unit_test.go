package studio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStyles(t *testing.T) {
	if len(Styles) < 60 {
		t.Fatalf("styles: %d, want the 23 web styles + the extended catalogue (60+)", len(Styles))
	}
	seen := map[string]bool{}
	for _, s := range Styles {
		if s.ID == "" || s.Label == "" || s.Prompt == "" || seen[s.ID] {
			t.Fatalf("bad style %+v", s)
		}
		seen[s.ID] = true
	}
	if got := StylePrompt("noir", ""); !strings.HasPrefix(got, "Film noir:") {
		t.Fatalf("noir prompt %q", got)
	}
	if got := StylePrompt("noir", "  my own style "); got != "my own style" {
		t.Fatalf("custom style %q", got)
	}
	if StylePrompt("nope", "") != "" {
		t.Fatal("unknown style must be empty")
	}
	if Styles[0].ID != "cinematic" || Styles[22].ID != "stickman" || Styles[15].Label != "Rubber Hose" {
		t.Fatal("style order/labels differ from styles.ts")
	}
}

// TestStylesMatchWeb compares the first styles with services/styles.ts
// (read-only) when the web studio sources are present: same ids, labels,
// prompts and order.
func TestStylesMatchWeb(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "apps", "api", "src", "services", "styles.ts"))
	if err != nil {
		t.Skip("web sources not found")
	}
	re := regexp.MustCompile(`\{ id: "([^"]+)", label: "([^"]+)", prompt: "([^"]+)" \}`)
	var web, ours [][3]string
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		web = append(web, [3]string{m[1], m[2], m[3]})
	}
	for _, s := range Styles[:min(len(web), len(Styles))] {
		ours = append(ours, [3]string{s.ID, s.Label, s.Prompt})
	}
	if len(web) != 23 || !reflect.DeepEqual(web, ours) {
		t.Fatalf("styles differ from styles.ts:\nweb %v\ngo  %v", web, ours)
	}
}

func TestSlugTag(t *testing.T) {
	cases := map[string]string{
		"Тарас":          "taras",
		"Осінній ліс":    "osinnii_lis",
		"Лисиця":         "lysytsia",
		"Щука-Юля":       "shchuka_iulia",
		"José Ñúñez":     "jose_nunez",
		"  Old  -  Man ": "old_man",
		"!!!":            "item",
		"":               "item",
		"Дуже довге ім'я персонажа з казки": "duzhe_dovhe_imia_persona",
		"Mr_ _Bean": "mr_bean",
	}
	for in, want := range cases {
		if got := slugTag(in); got != want {
			t.Errorf("slugTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueTags(t *testing.T) {
	got := uniqueTags([]string{"Тарас", "Лисиця"}, nil)
	if !reflect.DeepEqual(got, []string{"taras", "lysytsia"}) {
		t.Fatalf("got %v", got)
	}
	// Duplicate names: "taras2" would contain "taras" (the TS loop never ends
	// here); the clash is broken inside the word instead.
	got = uniqueTags([]string{"Тарас", "Тарас"}, nil)
	if got[0] != "taras" || got[1] != "t_aras" {
		t.Fatalf("duplicates: %v", got)
	}
	// New name shorter than a taken tag → suffix resolves it.
	got = uniqueTags([]string{"Тарас"}, []string{"tarasyk"})
	if got[0] != "taras2" {
		t.Fatalf("got %v", got)
	}
	// Base contains a taken tag: TS would loop forever; we must terminate
	// with a tag that is neither a sub- nor superstring.
	got = uniqueTags([]string{"Тарасик", "Марія"}, []string{"taras", "", "a"})
	used := []string{"taras", "a"}
	for _, g := range got {
		if tagConflict(g, used) {
			t.Fatalf("tag %q conflicts with %v", g, used)
		}
		used = append(used, g)
	}
	// No tag may be a substring of another.
	all := uniqueTags([]string{"Ліс", "Лісник", "Ліс", "Ліс темний"}, nil)
	for i := range all {
		for j := range all {
			if i != j && strings.Contains(all[i], all[j]) {
				t.Fatalf("%q contains %q (%v)", all[i], all[j], all)
			}
		}
	}
}

func TestSplitSentences(t *testing.T) {
	got := splitSentences("Раз. Два!  Три?\nЧотири… «П'ять» і шість»  сім\r\n\r\nвісім\n\n\nдев'ять \"десять\" одинадцять")
	// Same result as JS: text.replace(/\r/g, "").split(/(?<=[.!?…»"])\s+|\n{2,}/)
	want := []string{"Раз.", "Два!", "Три?", "Чотири…", "«П'ять»", "і шість»", "сім", "вісім", "дев'ять \"десять\"", "одинадцять"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestSplitScript(t *testing.T) {
	text := "Один два три. Чотири п'ять шість. Сім вісім дев'ять десять.\n\nОдинадцять дванадцять."
	got := splitScript(text, 6)
	want := []string{"Один два три. Чотири п'ять шість.", "Сім вісім дев'ять десять. Одинадцять дванадцять."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	// One very long sentence is hard-split on words.
	long := strings.TrimSpace(strings.Repeat("слово ", 20))
	parts := splitScript(long, 6)
	if len(parts) != 4 || countWords(parts[0]) != 6 || countWords(parts[3]) != 2 {
		t.Fatalf("hard split: %q", parts)
	}
	// Slightly long (≤ 1.4×) stays whole.
	if p := splitScript("a b c d e f g h", 6); len(p) != 1 {
		t.Fatalf("1.4x rule: %q", p)
	}
	if p := splitScript("   \n\n  ", 6); len(p) != 0 {
		t.Fatalf("blank: %q", p)
	}
}

func TestSceneBudget(t *testing.T) {
	in := DefaultInput()
	in.TargetSeconds = 60
	if n, w, ch := sceneBudget(in, 8); n != 8 || w != 18 || ch != nil { // round(7.5) = 8
		t.Fatalf("story budget %d %d %v", n, w, ch)
	}
	in.TargetSeconds = 3600
	if n, _, _ := sceneBudget(in, 4); n != 150 {
		t.Fatalf("max scenes %d", n)
	}
	in.TargetSeconds = 1
	if n, w, _ := sceneBudget(in, 4); n != 1 || w != 9 {
		t.Fatalf("min scenes %d words %d", n, w)
	}
	in.Mode, in.Text = ModeScript, "Раз два. Три чотири."
	if n, _, ch := sceneBudget(in, 8); n != 1 || len(ch) != 1 {
		t.Fatalf("script budget %d %q", n, ch)
	}
	in.Voiceover = false // script without voiceover plans by duration
	if n, _, ch := sceneBudget(in, 8); n != 1 || ch != nil {
		t.Fatalf("script w/o voice %d %q", n, ch)
	}
	if SpeechSeconds("один два три") != 3/2.3 {
		t.Fatal("speech seconds")
	}
	if wordsPerScene(2) != 6 {
		t.Fatal("min words per scene")
	}
}

func TestStates(t *testing.T) {
	done := Asset{Status: StatusDone, Path: "a"}
	cases := []struct {
		a    Asset
		want string
	}{
		{Asset{Status: StatusQueued}, "running"},
		{Asset{Status: StatusRunning}, "running"},
		{Asset{Status: StatusError}, "error"},
		{Asset{Status: StatusDone, Stale: true}, "stale"},
		{done, "done"},
		{EmptyAsset(), "empty"},
		{Asset{}, "empty"},
	}
	for _, c := range cases {
		if got := AssetState(c.a); got != c.want {
			t.Errorf("AssetState(%+v) = %s want %s", c.a, got, c.want)
		}
	}
	s := Scene{Frame: EmptyAsset(), Video: EmptyAsset(), Audio: EmptyAsset()}
	if SceneState(s) != "planned" {
		t.Fatal("planned")
	}
	s.Frame = done
	if SceneState(s) != "framed" {
		t.Fatal("framed")
	}
	s.Video = done
	if SceneState(s) != "shot" {
		t.Fatal("shot")
	}
	s.Audio = Asset{Status: StatusDone, Stale: true}
	if SceneState(s) != "stale" {
		t.Fatal("stale")
	}
	s.Frame = Asset{Status: StatusError}
	if SceneState(s) != "error" {
		t.Fatal("error")
	}
	s.Video = Asset{Status: StatusRunning}
	if SceneState(s) != "running" {
		t.Fatal("running")
	}
}

func TestLooseJSON(t *testing.T) {
	v, err := parseJSON([]byte(`{"z":1,"b":[1],"a":[2],"s":"x","n":null,"o":{"k":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if a := asArray(v); len(a) != 1 || a[0] != 1.0 {
		t.Fatalf("first array member in document order: %v", a)
	}
	if asArray(get(v, "s")) != nil || asArray(nil) != nil {
		t.Fatal("non-arrays")
	}
	for in, want := range map[string]string{`"x"`: "x", `3`: "3", `2.5`: "2.5", `true`: "true", `null`: "", `[1,"a",null]`: "1,a,", `{"a":1}`: "[object Object]"} {
		v, _ := parseJSON([]byte(in))
		if got := jsString(v); got != want {
			t.Errorf("jsString(%s) = %q want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{`""`: false, `"a"`: true, `0`: false, `1`: true, `null`: false, `[]`: true, `{}`: true, `false`: false} {
		v, _ := parseJSON([]byte(in))
		if truthy(v) != want {
			t.Errorf("truthy(%s) != %v", in, want)
		}
	}
}

func TestCloneIsDeep(t *testing.T) {
	p := &Project{
		ID: "p", Characters: []Character{{ID: "c"}}, Locations: []Location{{ID: "l"}},
		Scenes: []Scene{{ID: "s", CharacterIDs: []string{"c"}}},
		Job:    &Job{Kind: JobPlan, Status: JobRunning},
	}
	c := cloneProject(p)
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(c)
	if string(a) != string(b) {
		t.Fatal("clone differs")
	}
	c.Characters[0].ID = "x"
	c.Scenes[0].CharacterIDs[0] = "x"
	c.Locations[0].ID = "x"
	c.Job.Status = JobDone
	if p.Characters[0].ID != "c" || p.Scenes[0].CharacterIDs[0] != "c" || p.Locations[0].ID != "l" || p.Job.Status != JobRunning {
		t.Fatal("clone shares memory")
	}
}

func TestNormalize(t *testing.T) {
	raw := `{"id":"x","title":"T","step":0,"input":{"text":"hi","voiceover":false},
		"characters":[{"id":"c1","name":"A","tag":"a","image":{"status":"running","path":"images/a.jpg"}}],
		"locations":[{"id":"l1","name":"L","tag":"l","image":{"status":"queued"}}],
		"scenes":[{"id":"s1","frame":{"status":"done"},"video":{"status":"running"},"audio":{}}],
		"montage":{"musicVolume":0.5},
		"job":{"kind":"frames","status":"running","progress":40,"message":"Кадри сцен · 2/5","startedAt":1}}`
	p, err := decodeProject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	normalize(p)
	if p.Input.Aspect != "16:9" || p.Input.Voiceover || p.Input.Style != "cinematic" || p.Input.Text != "hi" {
		t.Fatalf("input defaults: %+v", p.Input)
	}
	if p.Montage.Transition != "fade" || p.Montage.Subtitles != "srt" || p.Montage.MusicVolume != 0.5 || !p.Montage.Ducking || p.Montage.AmbientVolume != 0.25 {
		t.Fatalf("montage defaults: %+v", p.Montage)
	}
	if p.Job.Status != JobError || p.Job.Error == "" || p.Job.FinishedAt == 0 {
		t.Fatalf("job: %+v", p.Job)
	}
	if p.Characters[0].Image.Status != StatusDone {
		t.Fatal("running with file → done")
	}
	if l := p.Locations[0].Image; l.Status != StatusError || l.Error != "перервано перезапуском" {
		t.Fatalf("queued without file → error: %+v", l)
	}
	s := p.Scenes[0]
	if s.Frame.Status != StatusEmpty || s.Video.Status != StatusError || s.Audio.Status != StatusEmpty || s.Speaker != "narrator" || s.CharacterIDs == nil {
		t.Fatalf("scene: %+v", s)
	}
	if p.Step != 1 {
		t.Fatal("step clamp")
	}
}

func TestReconcileRules(t *testing.T) {
	done := func(path string) Asset { return Asset{Status: StatusDone, Path: path, UpdatedAt: 1} }
	before := &Project{
		ID: "p", Title: "T", Step: 3, Input: DefaultInput(), Montage: DefaultMontage(),
		Characters: []Character{{ID: "cA", Name: "Тарас", Tag: "taras", Image: done("images/a.jpg")}, {ID: "cB", Name: "Лисиця", Tag: "lysytsia"}},
		Locations:  []Location{{ID: "lA", Name: "Ліс", Tag: "lis", Image: done("images/l.jpg")}},
		Scenes: []Scene{
			{ID: "s1", Speaker: "cB", Narration: "n1", ImagePrompt: "i1", MotionPrompt: "m1", CharacterIDs: []string{"cA", "cB"}, LocationID: "lA",
				Frame: done("images/f1.jpg"), Video: done("videos/v1.mp4"), Audio: done("audio/a1.mp3")},
			{ID: "s2", Speaker: "narrator", Narration: "n2", ImagePrompt: "i2", MotionPrompt: "m2",
				Frame: done("images/f2.jpg"), Video: done("videos/v2.mp4"), Audio: done("audio/a2.mp3")},
		},
	}
	p := cloneProject(before)
	// The UI: delete Лисиця, rename Тарас, add a character and a scene, edit texts,
	// try to touch engine-owned things.
	p.Characters = []Character{p.Characters[0], {Name: "Тарасик", Image: done("images/hack.jpg"), Tag: "hack"}}
	p.Characters[0].Name = "Тарас Бульба"
	p.Characters[0].Image = Asset{}
	p.Locations = nil
	p.Scenes[0].Narration = "n1 edited"
	p.Scenes[1].ImagePrompt = "i2 edited"
	p.Scenes[1].MotionPrompt = "m2 edited"
	p.Scenes[1].Video = EmptyAsset()
	p.Scenes = append(p.Scenes, Scene{ID: "s2", Title: "dup id"}, Scene{ID: "new-scene-1", Frame: done("x")})
	p.Title = ""
	p.Step = 1
	p.Input.Aspect = "4:3"
	p.Input.TargetSeconds = 99999
	p.Input.Tempo = "warp"
	p.Montage.Output = done("montage/hack.mp4")
	p.Montage.Transition = "wipe"
	p.Montage.MusicVolume = 7
	p.Job = &Job{Status: JobRunning}
	p.ID = "other"

	reconcile(before, &p)

	if p.ID != "p" || p.Job != nil || p.Montage.Output.Status != StatusEmpty {
		t.Fatal("engine-owned fields must be restored")
	}
	if p.Title != "Без назви" || p.Step != 3 || p.Input.Aspect != "16:9" || p.Input.TargetSeconds != 3600 || p.Input.Tempo != "balanced" {
		t.Fatalf("validation: %q %d %+v", p.Title, p.Step, p.Input)
	}
	if p.Montage.Transition != "fade" || p.Montage.MusicVolume != 1 {
		t.Fatalf("montage validation: %+v", p.Montage)
	}
	c0, c1 := p.Characters[0], p.Characters[1]
	if c0.Tag != "taras" || c0.Image.Path != "images/a.jpg" || c0.Name != "Тарас Бульба" {
		t.Fatalf("existing character: %+v", c0)
	}
	if c1.ID == "" || c1.Image.Status != StatusEmpty || c1.Tag == "hack" || tagConflict(c1.Tag, []string{"taras"}) {
		t.Fatalf("new character: %+v", c1)
	}
	s1 := p.Scenes[0]
	if !reflect.DeepEqual(s1.CharacterIDs, []string{"cA"}) || s1.LocationID != "" || s1.Speaker != "narrator" {
		t.Fatalf("deleted references: %+v", s1)
	}
	if !s1.Audio.Stale || s1.Frame.Stale || s1.Video.Stale {
		t.Fatalf("s1 stale flags: %+v %+v %+v", s1.Frame, s1.Video, s1.Audio)
	}
	s2 := p.Scenes[1]
	if !s2.Frame.Stale || !s2.Video.Stale || s2.Audio.Stale || s2.Video.Path != "videos/v2.mp4" {
		t.Fatalf("s2 stale flags: %+v %+v %+v", s2.Frame, s2.Video, s2.Audio)
	}
	if dup := p.Scenes[2]; dup.ID == "s2" || dup.Frame.Status != StatusEmpty {
		t.Fatalf("duplicate id must become a new scene: %+v", dup)
	}
	if n := p.Scenes[3]; n.ID != "new-scene-1" || n.Frame.Status != StatusEmpty || n.Speaker != "narrator" {
		t.Fatalf("client id kept, assets emptied: %+v", n)
	}
	gone := orphans(assetPaths(before), assetPaths(&p))
	want := map[string]bool{"images/l.jpg": true}
	for _, g := range gone {
		if !want[g] {
			t.Fatalf("unexpected orphan %s (%v)", g, gone)
		}
	}
	if len(gone) != 1 {
		t.Fatalf("orphans %v", gone)
	}
}

func TestMapLimit(t *testing.T) {
	items := make([]int, 20)
	var mu = make(chan struct{}, 1)
	running, peak := 0, 0
	err := mapLimit(items, 3, func() bool { return false }, func(_ int, i int) error {
		mu <- struct{}{}
		running++
		peak = max(peak, running)
		<-mu
		for k := 0; k < 1000; k++ {
			_ = k * i
		}
		mu <- struct{}{}
		running--
		<-mu
		if i == 7 {
			panic("boom")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic must surface as error: %v", err)
	}
	if peak > 3 {
		t.Fatalf("concurrency %d > 3", peak)
	}
	var n atomic.Int32
	_ = mapLimit(items, 2, func() bool { return n.Load() >= 5 }, func(int, int) error { n.Add(1); return nil })
	if n.Load() < 5 || n.Load() > 7 {
		t.Fatalf("cancel stops new items: ran %d", n.Load())
	}
}
