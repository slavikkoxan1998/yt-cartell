package produce

// Headless screenshots of a «Анімація кодом» film (steps 3 and 4):
//
//	GUISHOTS=<dir> go test ./internal/appui/produce -run TestCodeShots
//
// A copy of the demo project is switched to the code pipeline and given
// hand-written specs; the previews and one clip are rendered by the real
// renderer when a browser exists (else the step shows its placeholders).
// Nothing reaches a network service.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/studio"
)

var codeShotSpecs = []string{
	`{"grammar":"t3_finance_chart","duration":7,"data":{"title":"Скільки коштувала Луїзіана","unit":"мільйони доларів","source":"Джерело: Архів США","chart":"bar",
	  "series":[{"label":"1803","value":15},{"label":"Аляска 1867","value":7.2},{"label":"Флорида 1819","value":5}]},
	  "cues":[{"at":0,"kind":"title"},{"at":1.2,"kind":"bar"},{"at":4,"kind":"highlight","data":{"index":0},"text":"Найбільша угода"}]}`,
	`{"grammar":"y5_kinetic_type","duration":6,"cues":[{"at":0,"kind":"title","text":"Наполеон продає"},
	  {"at":1.8,"kind":"point","text":"15 млн","sub":"за два мільйони км²","data":{"key":"км²"}},{"at":3.6,"kind":"point","text":"4 центи","sub":"за акр землі"}]}`,
}

func TestCodeShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var srcID string
	best := 0
	for _, s := range c.List() {
		if s.Scenes > best && s.Aspect != "9:16" {
			srcID, best = s.ID, s.Scenes
		}
	}
	if best < 2 {
		srcID = ""
	}
	if srcID == "" {
		t.Skip("the web studio demo projects are not there")
	}
	cp, err := e.Duplicate(srcID)
	if err != nil {
		t.Fatal(err)
	}
	id := cp.ID
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.Edit(id, func(p *studio.Project) {
		p.Input.Pipeline = studio.PipelineCode
		p.Scenes[1].Code.Grammar = "y5_kinetic_type"
		p.Scenes[0].Code.Suggested = "t3_finance_chart"
	}))
	must(e.Edit(id, func(p *studio.Project) {
		for i, raw := range codeShotSpecs {
			var v any
			must(json.Unmarshal([]byte(raw), &v))
			b, _ := json.MarshalIndent(v, "", "  ")
			p.Scenes[i].Code.Spec = b
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	real := false
	if _, err := e.CodeAvailable(ctx); err == nil {
		real = true
		must(e.CodePreview(id, cp.Scenes[0].ID, 4.8))
		must(e.CodePreview(id, cp.Scenes[1].ID, 4.2))
		must(e.Regen(id, studio.RegenVideo, cp.Scenes[0].ID, ""))
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			p, _ := e.Get(id)
			idle := true
			for _, s := range p.Scenes[:2] {
				idle = idle && s.Code.Preview.Status != studio.StatusRunning && s.Code.Preview.Status != studio.StatusQueued
			}
			idle = idle && p.Scenes[0].Video.Status != studio.StatusRunning && p.Scenes[0].Video.Status != studio.StatusQueued
			if idle {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		p, _ := e.Get(id)
		t.Logf("previews: %+v / %+v; clip: %+v", p.Scenes[0].Code.Preview, p.Scenes[1].Code.Preview, p.Scenes[0].Video)
		for _, s := range p.Scenes[:2] {
			if s.Code.Preview.Status != studio.StatusDone {
				t.Errorf("preview of %s: %+v", s.ID, s.Code.Preview)
			}
			if s.Code.Preview.Path != "" {
				_ = os.MkdirAll(filepath.Join(out, "produce"), 0o755)
				data, _ := os.ReadFile(e.Abs(id, s.Code.Preview.Path))
				_ = os.WriteFile(filepath.Join(out, "produce", "code-preview-"+s.ID+".png"), data, 0o644)
			}
		}
	}
	c.Refresh()
	pv := c.Project(id)
	// The view state: scene 3 fell back, scene 4 has no spec yet.
	pv = fakeView(pv, func(p *studio.Project) {
		s := &p.Scenes[len(p.Scenes)-1]
		s.Code = studio.CodeClip{Fallback: true, Error: `анімацію не вдалося написати: текстова модель двічі написала анімацію з помилками: cue 3: kind "explosion" is not drawn by t3_finance_chart (use: title, bar, line, highlight, number)`}
	})
	env := &shotEnv{t: t, c: c, h: h, out: filepath.Join(out, "produce")}
	only := os.Getenv("GUISHOTS_ONLY")
	want := func(name string) bool { return only == "" || strings.Contains(name, only) }
	sb := NewStoryboard()
	if want("code-s3") {
		env.wide("code-s3-light-1920", sb, pv, false)
		env.wide("code-s3-dark-1920", sb, pv, true)
		env.laptop("code-s3-light-1440", sb, pv)
		env.narrow("code-s3-light-360", sb, pv)
		// The raw JSON editor of the first scene.
		cc := sb.(*storyboard).code.state(id).card(pv.P.Scenes[0].ID)
		cc.lastMode = "json"
		env.wide("code-s3-json-light-1920", sb, pv, false)
		cc.lastMode = "fields"
		c.UI(id).ScrollTo = pv.P.Scenes[len(pv.P.Scenes)-1].ID
		env.wide("code-s3-fallback-light-1920", sb, pv, false)
	}
	if want("code-s4") {
		env.wide("code-s4-light-1920", NewRender(), pv, false)
		env.laptop("code-s4-light-1440", NewRender(), pv)
	}
	if !real {
		t.Log("no browser: previews and the clip were not rendered")
	}
}
