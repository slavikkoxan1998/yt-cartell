package studio

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/config"
)

func mapSpec(t *testing.T, s string) codeSpec {
	t.Helper()
	spec, err := parseCodeSpec([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func cueKinds(s codeSpec) []string {
	var out []string
	for _, c := range cuesOf(s) {
		out = append(out, jsString(c["kind"]))
	}
	return out
}

// A map scene always moves: an action cue besides the camera and a camera
// move — added when the model left them out, never failed.
func TestEnsureMapMotion(t *testing.T) {
	// labels only, a sprite: the sprite bounces, the camera pushes in
	s := mapSpec(t, `{"duration":6,"data":{"sprites":[{"id":"nap","place":"paris"}],"regions":[{"id":"fr","preset":"france_metropolitan"}]},
"cues":[{"at":0.2,"kind":"label","text":"Франція"}]}`)
	added := ensureMapMotion(s, 2.5, nil)
	c := cuesOf(s)
	if len(added) != 2 || len(c) != 3 || c[1]["kind"] != "sprite" || c[1]["sprite"] != "nap" || c[1]["action"] != "bounce" || c[1]["at"] != 2.5 {
		t.Fatalf("added %v cues %v", added, c)
	}
	cam := c[2]
	if cam["kind"] != "camera" || cam["at"] != 0.0 || cam["dur"] != 5.9 || cam["arc"] != false || cam["view"].(map[string]any)["zoomBy"] != mapPushIn {
		t.Fatalf("push-in %v", cam)
	}
	// regions only: the focus region (the one the cues name) glows; "at" stays inside the clip
	s = mapSpec(t, `{"duration":3,"data":{"regions":[{"id":"uk","preset":"uk_1801"},{"id":"fr","preset":"france_metropolitan"},{"id":"es","preset":"spain_europe"}]},
"cues":[{"at":0.2,"kind":"camera","view":{"lon":2,"lat":47,"zoom":9}},{"at":1,"kind":"hide","target":"uk"}]}`)
	ensureMapMotion(s, 9, nil)
	c = cuesOf(s)
	if len(c) != 3 || c[2]["kind"] != "glow" || c[2]["region"] != "uk" || c[2]["pulse"] != true || c[2]["at"] != 2.2 {
		t.Fatalf("glow %v", c)
	}
	// no cue names a region: the last one (drawn on top) that is shown
	s = mapSpec(t, `{"duration":5,"data":{"regions":[{"id":"a","country":"FRA"},{"id":"b","country":"ESP"},{"id":"c","country":"PRT","show":false}]},"cues":[{"at":0.2,"kind":"label","text":"x"}]}`)
	ensureMapMotion(s, 2, nil)
	if c = cuesOf(s); c[1]["region"] != "b" {
		t.Fatalf("focus region %v", c[1])
	}
	// a river only: it glows
	s = mapSpec(t, `{"duration":5,"data":{"rivers":[{"id":"miss","river":"mississippi"}]},"cues":[{"at":0.2,"kind":"label","text":"x"}]}`)
	ensureMapMotion(s, 2, nil)
	if c = cuesOf(s); c[1]["kind"] != "glow" || c[1]["target"] != "miss" {
		t.Fatalf("river glow %v", c[1])
	}
	// enough motion: nothing added
	s = mapSpec(t, `{"duration":5,"data":{"sprites":[{"id":"n"}]},"cues":[{"at":0.2,"kind":"fill","region":"x"},{"at":1,"kind":"cam","view":{"place":"paris"}}]}`)
	if added := ensureMapMotion(s, 2, nil); len(added) != 0 || len(cuesOf(s)) != 2 {
		t.Fatalf("added %v", added)
	}
	// every listed action kind (and alias) counts; glow alone does not
	for _, k := range []string{"fill", "recolor", "drawRoute", "river", "moveAlong", "pop", "icon", "sprite", "bubble"} {
		s = mapSpec(t, `{"duration":5,"data":{"sprites":[{"id":"n"}]},"cues":[{"at":0.2,"kind":"`+k+`"}]}`)
		n := len(cuesOf(s))
		ensureMapMotion(s, 2, nil)
		if slices.Contains(cueKinds(s)[n:], "sprite") {
			t.Fatalf("%s is an action cue: %v", k, cueKinds(s))
		}
	}
	s = mapSpec(t, `{"duration":5,"data":{"sprites":[{"id":"n"}]},"cues":[{"at":0.2,"kind":"glow","region":"x"}]}`)
	ensureMapMotion(s, 2, nil)
	if k := cueKinds(s); !slices.Equal(k, []string{"glow", "sprite", "camera"}) {
		t.Fatalf("glow alone: %v", k)
	}
	// only kinds the grammar draws are added
	s = mapSpec(t, `{"duration":5,"data":{"sprites":[{"id":"n"}]},"cues":[{"at":0.2,"kind":"label"}]}`)
	ensureMapMotion(s, 2, func(k string) bool { return k != "camera" })
	if k := cueKinds(s); !slices.Equal(k, []string{"label", "sprite"}) {
		t.Fatalf("allowed: %v", k)
	}
}

// Sprites are big: a scale under the floor is dropped (the grammar's
// default applies), bigger ones stay.
func TestFixMapSprites(t *testing.T) {
	s := mapSpec(t, `{"data":{"sprites":[{"id":"a","scale":0.22},{"id":"b","scale":0.34},{"id":"c","size":0.2}]},
"cues":[{"at":1,"kind":"sprite","sprite":"d","image":"d.png","scale":0.24},{"at":2,"kind":"bubble","size":0.2}]}`)
	fixMapSprites(s, true)
	sp := asArray(s["data"].(map[string]any)["sprites"])
	if _, ok := sp[0].(map[string]any)["scale"]; ok {
		t.Fatal("0.22 must be dropped")
	}
	if sp[1].(map[string]any)["scale"] != 0.34 {
		t.Fatal("0.34 stays")
	}
	if _, ok := sp[2].(map[string]any)["size"]; ok {
		t.Fatal("size 0.2 must be dropped")
	}
	c := cuesOf(s)
	if _, ok := c[0]["scale"]; ok || c[1]["size"] != 0.2 {
		t.Fatalf("cues %v", c)
	}
	s = mapSpec(t, `{"data":{"sprites":[{"id":"a","scale":0.27}]},"cues":[]}`)
	fixMapSprites(s, false)
	if asArray(s["data"].(map[string]any)["sprites"])[0].(map[string]any)["scale"] != 0.27 {
		t.Fatal("landscape keeps 0.27")
	}
}

// The spec writer is told the bigger sprite size and that every scene moves.
func TestMapRulesMotionAndSpriteSize(t *testing.T) {
	j := &codeJob{p: Project{Input: Input{Aspect: "9:16", Language: "uk"}}, cast: []castSprite{{tag: "nap", name: "Наполеон", main: "nap.png", alt: "nap_shock.png"}}}
	rules := strings.Join(mapRules(j, "Ukrainian"), "\n")
	for _, want := range []string{"at least one action cue", "fill, recolor, drawRoute, moveAlong, pop, sprite or bubble", `"zoomBy": 1.1`,
		`default "scale" is 0.37`, "≈ 20 % of the frame height", `(no "scale": the default size is right`} {
		if !strings.Contains(rules, want) {
			t.Fatalf("map rules lack %q:\n%s", want, rules)
		}
	}
	if strings.Contains(rules, "0.2–0.26") {
		t.Fatal("the old small sprite scale is still offered")
	}
	j.p.Input.Aspect = "16:9"
	if rules = strings.Join(mapRules(j, "Ukrainian"), "\n"); !strings.Contains(rules, `default "scale" is 0.32`) {
		t.Fatalf("landscape rule:\n%s", rules)
	}
}

// The previous map state follows a push-in on the spot (zoomBy).
func TestPrevMapStateZoomBy(t *testing.T) {
	st := prevMapState(json.RawMessage(`{"grammar":"map","data":{"view":{"lon":1,"lat":2,"zoom":10}},
"cues":[{"at":0,"kind":"camera","view":{"zoomBy":1.1},"dur":5}]}`))
	if !strings.Contains(st, `"zoom":11`) || !strings.Contains(st, `"lon":1`) || strings.Contains(st, "zoomBy") {
		t.Fatalf("state %s", st)
	}
	st = prevMapState(json.RawMessage(`{"grammar":"map","data":{"view":{"lon":1,"lat":2,"zoom":10}},
"cues":[{"at":3,"kind":"camera","view":{"lon":-90,"lat":30,"zoom":8}},{"at":0,"kind":"camera","view":{"zoomBy":2}}]}`))
	if !strings.Contains(st, `"lon":-90`) || !strings.Contains(st, `"zoom":8`) {
		t.Fatalf("the later flight wins: %s", st)
	}
}

func TestSpecConcurrency(t *testing.T) {
	c := config.Default()
	if n := SpecConcurrency(c); n != 3 {
		t.Fatalf("default %d", n)
	}
	c.Text.Provider = "ollama"
	if n := SpecConcurrency(c); n != 1 {
		t.Fatalf("ollama %d", n)
	}
	c.CodeWriters = 2
	if n := SpecConcurrency(c); n != 2 {
		t.Fatalf("setting %d", n)
	}
	c.CodeWriters = 9
	if n := SpecConcurrency(c); n != config.MaxCodeWriters {
		t.Fatalf("clamped %d", n)
	}
	c.Normalize()
	if c.CodeWriters != config.MaxCodeWriters {
		t.Fatalf("normalized %d", c.CodeWriters)
	}
}

// A map film's specs are written in parallel: the first scene alone (it
// sets the film's map), then the rest SpecConcurrency at a time, each
// continuing the nearest scene already written.
func TestCodeMapSpecsParallel(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, cm, _ := codeEnv(t, map[string]string{"Сцена 1": "map", "Сцена 2": "map", "Сцена 3": "map"})
	cm.delay = 400 * time.Millisecond
	e := env.e
	pr := quickProfile(t, e)
	pr.ID, pr.Name, pr.Pipeline, pr.CodeGrammar, pr.Language = "", "Тест · мапа паралельно", PipelineCode, "map", "uk"
	pr, err := e.SaveProfile(pr)
	must(t, err)
	p, err := e.CreateProject(NewProjectOptions{Text: apStory, ProfileID: pr.ID})
	must(t, err)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	must(t, e.Run(p.ID, StageFrames, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	cm.mu.Lock()
	events, maxIn := slices.Clone(cm.events), cm.maxInflight
	cm.mu.Unlock()
	if maxIn != 2 {
		t.Fatalf("specs at once: %d (want 2 — scenes 2 and 3 together); events %v", maxIn, events)
	}
	if len(events) < 6 || events[0] != "start:Сцена 1" || events[1] != "end:Сцена 1" {
		t.Fatalf("the first map scene must be written alone first: %v", events)
	}
	for i, s := range p.Scenes {
		if g, _ := specGrammar(s.Code.Spec); g != "map" || s.Code.Fallback {
			t.Fatalf("scene %d: %s %+v", i+1, g, s.Code)
		}
	}
	// scene 3 was written next to scene 2: it continues scene 1, the nearest written
	if pr3 := cm.prompt("Scene: Сцена 3"); !strings.Contains(pr3, "Scene 1 (the nearest earlier one already written") {
		t.Fatalf("scene 3 prompt:\n%s", pr3)
	}
	if pr2 := cm.prompt("Scene: Сцена 2"); !strings.Contains(pr2, "The previous scene was animated with map") {
		t.Fatalf("scene 2 prompt:\n%s", pr2)
	}
}
