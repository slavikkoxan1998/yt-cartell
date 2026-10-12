package studio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/glabs"
)

// rejectProxy stands in front of the storyboard G-Labs mock and refuses the
// video requests rule picks (with the task body G-Labs reports), passing the
// rest on.
type rejectProxy struct {
	mu    sync.Mutex
	next  http.Handler
	rule  func(prompt, mode string, names []string) string // failed-task JSON, "" = pass
	seen  []string                                         // "mode refs → outcome" per video request
	tasks map[string]string                                // rejected task id → status body
}

func (x *rejectProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/video/generate" {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		var body struct {
			Prompt string            `json:"prompt"`
			Mode   string            `json:"mode"`
			Refs   []json.RawMessage `json:"reference_images"`
		}
		_ = json.Unmarshal(b, &body)
		var names []string
		for _, raw := range body.Refs {
			var o struct{ Name string }
			if json.Unmarshal(raw, &o) == nil {
				names = append(names, o.Name)
			} else {
				names = append(names, "")
			}
		}
		x.mu.Lock()
		fail := x.rule(body.Prompt, body.Mode, names)
		scene, _, _ := strings.Cut(body.Prompt, ":")
		entry := fmt.Sprintf("%s %s %s", scene, body.Mode, strings.Join(names, ","))
		if fail != "" {
			id := fmt.Sprintf("rej%d", len(x.tasks)+1)
			x.tasks[id] = fail
			x.seen = append(x.seen, entry+" → rejected")
			x.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"task_id":%q}`, id)
			return
		}
		x.seen = append(x.seen, entry+" → ok")
		x.mu.Unlock()
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/api/status/rej"); ok {
		x.mu.Lock()
		body := x.tasks["rej"+id]
		x.mu.Unlock()
		_, _ = w.Write([]byte(body))
		return
	}
	x.next.ServeHTTP(w, r)
}

// TestOmniRejectionsAndSaferRetry: on G-Labs Omni Flash a components clip
// refused for its content — INVALID_ARGUMENT or a safety filter
// (REPUTATIONAL) — is tried ONCE more as the frame alone (start_image, the
// plain motion prompt): on the owner's G-Labs every refused clip carried the
// 3×3 storyboard, frame-only clips went through. When that is refused too,
// both exact errors stay on the clip. A scene in another location gets no
// previous shot.
func TestOmniRejectionsAndSaferRetry(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, gl := newSBEnv(t, nil)
	e := env.e
	p := castProject(t, e, func(in *Input) {
		in.StoryboardMode = StoryboardGrid
		in.ChainFrames = true
	}, []int{0}, []int{0}, []int{0, 1})
	id := p.ID
	img := filepath.Join(t.TempDir(), "cave.jpg")
	must(t, os.WriteFile(img, testImage, 0o644))
	must(t, e.Edit(id, func(p *Project) {
		p.Locations = append(p.Locations, Location{ID: "loc-cave", Name: "Печера", Description: "dark cave"})
		p.Scenes[2].LocationID = "loc-cave" // a cut to another place
	}))
	must(t, e.ImportImage(id, RegenLocation, "loc-cave", img))
	must(t, e.Run(id, StageFrames, false, ""))
	mustJobDone(t, waitJob(t, e, id))

	const invalid = `{"status":"failed","error_code":400,"error":"INVALID_ARGUMENT (flag=[3])","error_detail":"400: INVALID_ARGUMENT (flag=[3])"}`
	px := &rejectProxy{next: gl, tasks: map[string]string{}, rule: func(prompt, mode string, names []string) string {
		switch {
		case strings.HasPrefix(prompt, "Motion S1") && mode == "components": // refused with the storyboard
			return invalid
		case strings.HasPrefix(prompt, "Motion S2") && strings.Contains(strings.Join(names, ","), "previous_shot"):
			return invalid
		case strings.HasPrefix(prompt, "Motion S3"):
			return `{"status":"failed","error_code":0,"error":"REPUTATIONAL","error_detail":"REPUTATIONAL"}`
		}
		return ""
	}}
	srv := httptest.NewServer(px)
	allow(srv)
	t.Cleanup(srv.Close)
	_ = env.store.Update(func(c *config.Config) { c.Glabs.BaseURL = srv.URL }) // in-memory store

	must(t, e.Run(id, StageVideos, false, ""))
	p = waitJob(t, e, id)
	px.mu.Lock()
	seen := append([]string(nil), px.seen...)
	px.mu.Unlock()
	want := []string{
		"Motion S1 components storyboard.jpg,taras.jpg,lis.jpg → rejected",
		"Motion S1 start_image  → ok", // the frame alone
		"Motion S2 components storyboard.jpg,previous_shot.jpg,taras.jpg,lis.jpg → rejected",
		"Motion S2 start_image  → ok",                                                       // the frame alone, not the board again
		"Motion S3 components storyboard.jpg,taras.jpg,lysytsia.jpg,pechera.jpg → rejected", // another place: no previous shot
		"Motion S3 start_image  → rejected",                                                 // a safety refusal gets the safer try too
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("video requests:\n%s\nwant:\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
	vids := gl.requests("video")
	if len(vids) != 2 || strings.Contains(vids[1].prompt, "@previous_shot") || strings.Contains(vids[1].prompt, "@storyboard") ||
		strings.Contains(vids[1].prompt, "Shot:") || !strings.HasPrefix(vids[1].prompt, "Motion S2") {
		t.Fatalf("safer prompt:\n%s", vids[len(vids)-1].prompt)
	}
	if p.Scenes[0].Video.Status != StatusDone || p.Scenes[1].Video.Status != StatusDone {
		t.Fatalf("safer retries did not finish the clips: %+v %+v", p.Scenes[0].Video, p.Scenes[1].Video)
	}
	v := p.Scenes[2].Video
	if v.Status != StatusError || !glabs.Rejected(v.Error) || !strings.Contains(v.Error, "фільтр безпеки Google (REPUTATIONAL)") || !rejected(v) ||
		!strings.Contains(v.Error, "Спершу з розкадровкою й референсами: G-Labs відхилив запит") || !strings.Contains(v.Error, "потім лише кадр — теж не вийшло") {
		t.Fatalf("scene 3: %+v", v)
	}
	if h := FailureHint(v.Error); !strings.Contains(h, "репутаційно чутливою") {
		t.Fatalf("hint %q", h)
	}
	if r := autopilotRejected(&p); r.clips != 1 || r.frames != 0 {
		t.Fatalf("rejected %+v", r)
	}
	// during the autopilot's retry rounds the runners leave it alone
	j := &jobCtx{}
	if j.skip(v) {
		t.Fatal("outside retry rounds a rejected clip is regenerated on request")
	}
	j.retrying.Store(true)
	if !j.skip(v) || j.skip(p.Scenes[0].Video) || j.skip(Asset{Status: StatusError, Error: "G-Labs [429]: ліміт"}) {
		t.Fatal("skip")
	}
}
