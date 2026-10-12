package studio

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
)

func TestClipLengthRules(t *testing.T) {
	video := func(v config.Video) config.Config {
		c := config.Default()
		c.Video = v
		return c
	}
	veo := video(config.Video{Provider: "glabs", GlabsModel: "veo_31_fast", Length: 8})
	veoU := video(config.Video{Provider: "glabs", GlabsModel: "veo_31_fast", Length: 8, Ultra: true})
	omni := video(config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10})
	omniU := video(config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10, Ultra: true})
	g720 := video(config.Video{Provider: "google", Resolution: "720p", Length: 8})
	g1080 := video(config.Video{Provider: "google", Resolution: "1080p", Length: 8})
	for name, tc := range map[string]struct {
		c    config.Config
		want []int
	}{
		"veo": {veo, []int{8}}, "veo ultra": {veoU, []int{4, 6, 8}}, "omni": {omni, []int{4, 6, 8, 10}}, "omni ultra": {omniU, []int{4, 6, 8, 10}},
		// Google's default video model is Gemini Omni Flash: 4/6/8/10 s at any resolution
		"google 720p": {g720, []int{4, 6, 8, 10}}, "google 1080p": {g1080, []int{4, 6, 8, 10}},
	} {
		if got := clipLengths(tc.c); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}

	dyn := func(c config.Config) config.Config { c.Video.Dynamic = true; return c }
	voiced := func(sec float64) *Scene {
		return &Scene{Narration: "Раз два три.", Audio: Asset{Status: StatusDone, Path: "audio/v.mp3", Duration: sec}}
	}
	for _, tc := range []struct {
		name string
		c    config.Config
		s    *Scene
		want int
	}{
		{"off", veoU, voiced(1.5), 8},
		{"short line", dyn(veoU), voiced(1.5), 4},
		{"5.1 s → 6", dyn(veoU), voiced(5.1), 6},
		{"longer than the longest", dyn(veoU), voiced(7.6), 8},
		{"omni 9.2 s → 10", dyn(omniU), voiced(9.2), 10},
		{"no ultra: 8", dyn(veo), voiced(1.5), 8},
		{"no ultra omni: 6", dyn(omni), voiced(5), 6}, // Omni Flash 4/6 s need no Ultra
		{"stale voice → estimate", dyn(veoU), &Scene{Narration: strings.Repeat("слово ", 10), Audio: Asset{Status: StatusDone, Duration: 9, Stale: true}}, 6},
		{"no voice → estimate", dyn(veoU), &Scene{Narration: "Раз."}, 4},
		{"nothing to say → Settings", dyn(veoU), &Scene{}, 8},
		{"chosen length still covers", dyn(veoU), &Scene{ClipLen: 8, Audio: Asset{Status: StatusDone, Duration: 1.5}}, 8},
		{"chosen length too short", dyn(veoU), &Scene{ClipLen: 4, Audio: Asset{Status: StatusDone, Duration: 5.1}}, 6},
		{"own voice slice (dynamic off)", veoU, &Scene{AudioSource: AudioUpload, VoiceStart: 1, VoiceEnd: 4.2}, 4},
		{"own voice slice without ultra", veo, &Scene{AudioSource: AudioUpload, VoiceStart: 1, VoiceEnd: 4.2}, 8},
	} {
		if got := sceneClip(tc.c, tc.s); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}

	// The plan is written for the average expected length.
	for _, tc := range []struct {
		c              config.Config
		settings, want int
	}{{dyn(veoU), 8, 6}, {dyn(omniU), 10, 7}, {dyn(omni), 10, 7}, {dyn(veo), 8, 8}, {veoU, 8, 8}, {dyn(veoU), 6, 5}} {
		if got := planClip(tc.c, tc.settings); got != tc.want {
			t.Errorf("planClip(%+v, %d) = %d, want %d", tc.c.Video, tc.settings, got, tc.want)
		}
	}
	if clipTrim(8, 0, 5) != 2.4 || clipTrim(4, 0, 5) != 0 || clipTrim(8, 1, 5) != 1.4 {
		t.Fatal("clipTrim")
	}

	env := newEnv(t, func(c *config.Config) { c.Video.Ultra, c.Video.Dynamic = true, true })
	p := Project{Input: DefaultInput()}
	if n, clip, words := env.e.Budget(p); n != 10 || clip != 6 || words != 13 {
		t.Fatalf("dynamic budget %d × %d s, %d words", n, clip, words)
	}
}

// ttsTimed is a "custom" TTS server answering each text with a prepared
// recording and noting when it answered.
type ttsTimed struct {
	mu    sync.Mutex
	audio map[string][]byte
	at    map[string]time.Time
}

func (m *ttsTimed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	time.Sleep(80 * time.Millisecond)
	m.mu.Lock()
	data := m.audio[body.Text]
	m.at[body.Text] = time.Now()
	m.mu.Unlock()
	if data == nil {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Write(data)
}

// TestDynamicRender: with dynamic lengths the voices go first and every clip
// is the shortest length covering its voice; the 3×3 sheet spans the
// expected length already.
func TestDynamicRender(t *testing.T) {
	needMedia(t)
	t.Parallel()
	lines := []string{"Раз.", "Раз два три чотири п'ять шість сім вісім.", strings.TrimSpace(strings.Repeat("ще одне слово ", 5))}
	durations := []float64{1.2, 3.5, 6.8} // → clips 4, 6, 8 (+0.6 s of air)
	tm := &ttsTimed{audio: map[string][]byte{}, at: map[string]time.Time{}}
	for i, l := range lines {
		out := filepath.Join(t.TempDir(), fmt.Sprintf("v%d.mp3", i))
		must(t, testFF.Run(t.Context(), "", "-f", "lavfi", "-i", "sine=frequency=330:sample_rate=44100", "-t", fmt.Sprint(durations[i]),
			"-c:a", "libmp3lame", "-b:a", "64k", out))
		b, err := os.ReadFile(out)
		must(t, err)
		tm.audio[l] = b
	}
	ts := httptest.NewServer(tm)
	allow(ts)
	t.Cleanup(ts.Close)
	env, gl := newSBEnv(t, func(c *config.Config) {
		c.Video.Ultra, c.Video.Dynamic = true, true
		c.TTS = config.TTS{Provider: "custom", BaseURL: ts.URL + "/tts"}
	})
	e := env.e
	p := castProject(t, e, func(in *Input) { in.StoryboardMode = StoryboardGrid }, []int{0}, []int{1}, []int{0, 1})
	must(t, e.Edit(p.ID, func(p *Project) {
		for i := range p.Scenes {
			p.Scenes[i].Narration = lines[i]
		}
	}))

	// Frames first (3×3 sheets): no voice yet, the sheet spans the estimate.
	must(t, e.Run(p.ID, StageFrames, false, ""))
	mustJobDone(t, waitJob(t, e, p.ID))
	estimate := map[string]string{"S1": "over its 4 seconds", "S2": "over its 6 seconds", "S3": "over its 8 seconds"}
	for _, r := range gl.requests("image") {
		if !strings.Contains(r.prompt, "3×3 storyboard sheet") {
			continue
		}
		for scene, want := range estimate {
			if strings.Contains(r.prompt, "Motion "+scene+":") && !strings.Contains(r.prompt, want) {
				t.Errorf("%s sheet: want %q in\n%s", scene, want, r.prompt)
			}
		}
	}

	must(t, e.Run(p.ID, StageRender, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	want := []int{4, 6, 8}
	for i, s := range p.Scenes {
		if s.Audio.Status != StatusDone || math.Abs(s.Audio.Duration-durations[i]) > 0.1 || s.Video.Status != StatusDone || s.ClipLen != want[i] {
			t.Fatalf("scene %d: voice %+v clip %+v ClipLen %d", i+1, s.Audio, s.Video, s.ClipLen)
		}
		var reqs []sbReq
		for _, r := range gl.requests("video") {
			if strings.Contains(r.prompt, fmt.Sprintf("Motion S%d:", i+1)) {
				reqs = append(reqs, r)
			}
		}
		tm.mu.Lock()
		voiced := tm.at[lines[i]]
		tm.mu.Unlock()
		if len(reqs) != 1 || reqs[0].length != want[i] || reqs[0].at.Before(voiced) {
			t.Fatalf("scene %d: clip requests %+v (voice at %s)", i+1, reqs, voiced.Format(time.StampMilli))
		}
	}

	// A new, shorter line: its clip length is chosen again at the next render.
	must(t, e.Edit(p.ID, func(p *Project) { p.Scenes[2].Narration = "Коротко." }))
	tm.mu.Lock()
	tm.audio["Коротко."] = tm.audio[lines[0]]
	tm.mu.Unlock()
	must(t, e.Regen(p.ID, RegenVoice, p.Scenes[2].ID, ""))
	q := waitFor(t, e, p.ID, "new voice", func(p Project) bool { return p.Scenes[2].Audio.Status == StatusDone && !p.Scenes[2].Audio.Stale })
	if q.Scenes[2].ClipLen != 0 {
		t.Fatalf("ClipLen after a new voice %d, want 0 (chosen again)", q.Scenes[2].ClipLen)
	}
	before := len(gl.requests("video"))
	must(t, e.Regen(p.ID, RegenVideo, q.Scenes[2].ID, ""))
	q = waitFor(t, e, p.ID, "new clip", func(p Project) bool {
		return len(gl.requests("video")) > before && p.Scenes[2].Video.Status == StatusDone
	})
	if r := gl.requests("video")[before]; q.Scenes[2].ClipLen != 4 || r.length != 4 {
		t.Fatalf("re-render: ClipLen %d, asked %d s", q.Scenes[2].ClipLen, r.length)
	}
}
