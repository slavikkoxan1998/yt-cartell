package providers

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

const uniKey = "uk_live_0123456789abcdefSECRET"

// uniClient is a client whose image and video presets are "universal".
func uniClient(base, model, imageSpec, videoSpec string) *Client {
	return customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{
			ImagePreset: PresetUniversal, ImageBaseURL: base, ImageKey: uniKey, ImageModel: model, ImageSpec: imageSpec,
			VideoPreset: PresetUniversal, VideoBaseURL: base, VideoKey: uniKey, VideoModel: model, VideoSpec: videoSpec,
		}
		c.Video.Length = 8
	})
}

func twoRefs() []gen.RefImage {
	return []gen.RefImage{{Data: jpegBytes, MIME: "image/jpeg", Name: "taras.jpg"}, {Data: pngBytes, MIME: "image/png", Name: "fox.png"}}
}

// ---- templates ---------------------------------------------------------------------------

func TestSpecTemplatesValid(t *testing.T) {
	for _, video := range []bool{false, true} {
		ids := map[string]bool{}
		for _, tp := range SpecTemplates(video) {
			if ids[tp.ID] || tp.Label == "" || tp.Hint == "" {
				t.Errorf("template %q: duplicate or missing label/hint", tp.ID)
			}
			ids[tp.ID] = true
			sum, warns, err := CheckSpec(tp.Spec, video)
			if err != nil || sum == "" {
				t.Errorf("template %s (video %v): %v", tp.ID, video, err)
			}
			t.Logf("%s/%v: %s %v", tp.ID, video, sum, warns)
			if got, ok := FindSpecTemplate(video, tp.ID); !ok || got.Spec != tp.Spec {
				t.Errorf("FindSpecTemplate(%s)", tp.ID)
			}
		}
	}
	if c := SpecCaps(tplRoyalVideo).Video; fmt.Sprint(c.Lengths) != "[4 6 8 10]" || c.MaxRefs != 7 || !c.Components || !c.StartFrame {
		t.Errorf("royal video caps %+v", c)
	}
	if c := SpecCaps(tplOpenAIImages).Image; c.MaxRefs != 0 || fmt.Sprint(c.Aspects) != "[16:9 9:16 1:1]" {
		t.Errorf("openai caps %+v", c)
	}
}

// Every template works end to end against a mock of its API shape.
func TestUniversalTemplatesRoundTrip(t *testing.T) {
	fast(t)
	for _, video := range []bool{false, true} {
		for _, tp := range SpecTemplates(video) {
			t.Run(fmt.Sprintf("%s-video-%v", tp.ID, video), func(t *testing.T) {
				m := newMockAPI(t)
				file, ctype := pngBytes, "image/png"
				if video {
					file, ctype = mp4Bytes, "video/mp4"
				}
				m.on("GET /cdn/file", serveBytes(ctype, file))
				fileURL := m.srv.URL + "/cdn/file"
				base := m.srv.URL
				authHeader, authValue := "Authorization", "Bearer "+uniKey
				var polls atomic.Int32
				switch tp.ID {
				case "royaltechno":
					m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"job_1","status":"queued"}`) })
					m.on("GET /v1/jobs/job_1", func(w http.ResponseWriter, r *http.Request) {
						if polls.Add(1) == 1 {
							writeJSON(w, 200, `{"id":"job_1","status":"processing"}`)
							return
						}
						writeJSON(w, 200, fmt.Sprintf(`{"id":"job_1","status":"succeeded","output":{"url":%q}}`, fileURL))
					})
				case "replicate":
					p := "POST /models/" + tp.Model + "/predictions"
					m.on(p, func(w http.ResponseWriter, r *http.Request) {
						if !video { // "Prefer: wait": done at once
							writeJSON(w, 201, fmt.Sprintf(`{"id":"p1","status":"succeeded","output":[%q]}`, fileURL))
							return
						}
						writeJSON(w, 201, `{"id":"p1","status":"starting"}`)
					})
					m.on("GET /predictions/p1", func(w http.ResponseWriter, r *http.Request) {
						if polls.Add(1) == 1 {
							writeJSON(w, 200, `{"id":"p1","status":"processing"}`)
							return
						}
						writeJSON(w, 200, fmt.Sprintf(`{"id":"p1","status":"succeeded","output":%q}`, fileURL))
					})
				case "fal":
					authValue = "Key " + uniKey
					q := base + "/fal-ai/x/requests/r1"
					m.on("POST /"+tp.Model, func(w http.ResponseWriter, r *http.Request) {
						writeJSON(w, 200, fmt.Sprintf(`{"request_id":"r1","status_url":%q,"response_url":%q,"cancel_url":%q}`, q+"/status", q, q+"/cancel"))
					})
					m.on("GET /fal-ai/x/requests/r1/status", func(w http.ResponseWriter, r *http.Request) {
						if polls.Add(1) == 1 {
							writeJSON(w, 202, `{"status":"IN_QUEUE","queue_position":2}`)
							return
						}
						writeJSON(w, 200, `{"status":"COMPLETED"}`)
					})
					m.on("GET /fal-ai/x/requests/r1", func(w http.ResponseWriter, r *http.Request) {
						if video {
							writeJSON(w, 200, fmt.Sprintf(`{"video":{"url":%q}}`, fileURL))
							return
						}
						writeJSON(w, 200, fmt.Sprintf(`{"images":[{"url":%q}]}`, fileURL))
					})
				case "openai-images":
					m.on("POST /images/generations", func(w http.ResponseWriter, r *http.Request) {
						writeJSON(w, 200, fmt.Sprintf(`{"created":1,"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(pngBytes)))
					})
				case "sync-url":
					m.on("POST /v1/generate", func(w http.ResponseWriter, r *http.Request) {
						writeJSON(w, 200, fmt.Sprintf(`{"url":%q}`, fileURL))
					})
				case "async-jobs":
					m.on("POST /v1/tasks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"t1","status":"pending"}`) })
					m.on("GET /v1/tasks/t1", func(w http.ResponseWriter, r *http.Request) {
						if polls.Add(1) == 1 {
							writeJSON(w, 200, `{"id":"t1","status":"processing","progress":0.5}`)
							return
						}
						writeJSON(w, 200, fmt.Sprintf(`{"id":"t1","status":"completed","result":{"url":%q}}`, fileURL))
					})
				default:
					t.Fatalf("no mock for template %s", tp.ID)
				}
				model := firstNonEmpty(tp.Model, "my-model")
				c := uniClient(base, model, tp.Spec, tp.Spec)
				var got []byte
				var err error
				if video {
					var last int
					got, err = c.Video(context.Background(), gen.VideoRequest{Prompt: "a fox runs", Start: startFrame(), Aspect: "16:9", Seconds: 8},
						func(p int, _ string) { last = p })
					if err == nil && last != 100 {
						t.Errorf("progress ended at %d", last)
					}
				} else {
					got, _, err = c.Image(context.Background(), gen.ImageRequest{Prompt: "@taras meets @fox", Aspect: "16:9", Refs: twoRefs()})
				}
				if err != nil || string(got) != string(file) {
					t.Fatalf("generate: %v (%d bytes)", err, len(got))
				}
				// The submit went with the key (the template's way), one Idempotency-Key, the prompt.
				var submit *seen
				for _, s := range m.all() {
					if s.method == "POST" && !strings.HasSuffix(s.path, "/cancel") {
						s := s
						submit = &s
						break
					}
				}
				if submit == nil {
					t.Fatal("no submit")
				}
				if got := submit.header.Get(authHeader); got != authValue {
					t.Errorf("auth %q", got)
				}
				if k := submit.header.Get("Idempotency-Key"); !reUUID4.MatchString(k) {
					t.Errorf("Idempotency-Key %q", k)
				}
				if !strings.Contains(string(submit.body), "a fox runs") && !strings.Contains(string(submit.body), "@taras meets @fox") {
					t.Errorf("prompt missing: %s", clip(string(submit.body), 300))
				}
				noKeyLeak(t, m, uniKey, authHeader)
			})
		}
	}
}

// The RoyalTechno template sends what royaltechno.go sends.
func TestUniversalRoyalTemplateBody(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 201, fmt.Sprintf(`{"id":"j","status":"succeeded","output":{"url":"%s/f"}}`, "http://"+r.Host))
	})
	m.on("GET /f", serveBytes("video/mp4", mp4Bytes))
	tp, _ := FindSpecTemplate(true, "royaltechno")
	c := uniClient(m.srv.URL, "omni-flash", "", tp.Spec)
	refs := make([]gen.RefImage, 9)
	for i := range refs {
		refs[i] = gen.RefImage{Data: jpegBytes, MIME: "image/jpeg", Name: fmt.Sprintf("r%d.jpg", i)}
	}
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Aspect: "9:16", Seconds: 9,
		Mode: gen.ModeComponents, Refs: refs}, nil); err != nil {
		t.Fatal(err)
	}
	body := m.requests("POST /v1/jobs")[0].json(t)
	in := body["input"].(map[string]any)
	urls, _ := in["reference_image_urls"].([]any)
	if body["model"] != "omni-flash" || len(urls) != 7 || in["start_image_url"] != nil || in["duration_sec"] != float64(10) ||
		in["aspect_ratio"] != "portrait" || !strings.Contains(in["prompt"].(string), "image 1 = @r0") {
		t.Fatalf("input %v", in)
	}
	// Start-frame mode: the frame, no references, no legend.
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Aspect: "16:9", Seconds: 4}, nil); err != nil {
		t.Fatal(err)
	}
	in = m.requests("POST /v1/jobs")[1].json(t)["input"].(map[string]any)
	if in["reference_image_urls"] != nil || !strings.HasPrefix(fmt.Sprint(in["start_image_url"]), "data:image/jpeg;base64,") ||
		in["duration_sec"] != float64(4) || in["aspect_ratio"] != "landscape" || in["prompt"] != "walk" {
		t.Fatalf("input %v", in)
	}
}

// ---- rendering ------------------------------------------------------------------------------

func TestUniversalBodyRendering(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /gen", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"out":[{"b64":"`+base64.StdEncoding.EncodeToString(mp4Bytes)+`"}]}`)
	})
	spec := `{
	  "submit": {"path": "/gen?model={{model}}", "headers": {"X-Model": "{{model}}", "X-Res": "{{resolution}}"},
	    "body": {
	      "prompt": "Scene: {{prompt}} ({{aspect}}, {{orientation}})",
	      "secs": "{{seconds}}", "secs_text": "{{seconds|text}}", "label": "{{seconds}} s",
	      "w": "{{width}}", "h": "{{height}}", "size": "{{size}}",
	      "images": ["{{start_image}}", "{{refs}}"],
	      "raw": "{{start_image_b64}}",
	      "gone": "{{refs}}", "gone_too": "frame: {{refs_b64}}",
	      "keep": {"n": 1, "flag": true, "nothing": null}
	    }},
	  "result": "out.b64",
	  "caps": {"lengths": [3, 6], "aspects": ["16:9", "9:16"], "size": 1024}
	}`
	c := uniClient(m.srv.URL, "acme/vid 2", "", spec)
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "fox", Start: startFrame(), Aspect: "9:16", Seconds: 5, Resolution: "1080p"}, nil)
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("video: %v", err)
	}
	s := m.requests("POST /gen")[0]
	if s.query != "model=acme%2Fvid+2" || s.header.Get("X-Model") != "acme/vid 2" || s.header.Get("X-Res") != "1080p" {
		t.Fatalf("query %q headers %v", s.query, s.header)
	}
	b := s.json(t)
	imgs, _ := b["images"].([]any)
	want := map[string]any{"prompt": "Scene: fox (9:16, portrait)", "secs": float64(6), "secs_text": "6", "label": "6 s", "w": float64(576), "h": float64(1024), "size": "576x1024"}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s = %#v, want %#v", k, b[k], v)
		}
	}
	if len(imgs) != 1 || !strings.HasPrefix(imgs[0].(string), "data:image/jpeg;base64,") || b["raw"] != base64.StdEncoding.EncodeToString(jpegBytes) {
		t.Errorf("images %v raw %v", imgs, b["raw"])
	}
	if _, ok := b["gone"]; ok {
		t.Error("a placeholder without a value must remove its key")
	}
	if _, ok := b["gone_too"]; ok {
		t.Error("an embedded placeholder without a value must remove its key")
	}
	keep, _ := b["keep"].(map[string]any)
	if keep["n"] != float64(1) || keep["flag"] != true {
		t.Errorf("keep %v", keep)
	}
}

func TestJSONPaths(t *testing.T) {
	var v any = map[string]any{
		"data":   []any{map[string]any{"url": "u0"}, map[string]any{"url": "u1"}},
		"output": map[string]any{"images": []any{map[string]any{"url": "img"}}},
		"n":      nil,
	}
	for path, want := range map[string]any{
		"data.0.url": "u0", "data[1].url": "u1", "$.data.-1.url": "u1", "data.url": "u0",
		"output.images.0.url": "img", `output["images"][0].url`: "img",
	} {
		if got, ok := lookupPath(v, path); !ok || got != want {
			t.Errorf("%s = %v %v, want %v", path, got, ok, want)
		}
	}
	for _, path := range []string{"n", "data.5.url", "missing", "output.images.0.nope", "data..url"} {
		if got, ok := lookupPath(v, path); ok {
			t.Errorf("%s = %v, want missing", path, got)
		}
	}
}

// ---- validation -------------------------------------------------------------------------------

func TestParseSpecErrors(t *testing.T) {
	cases := []struct{ spec, want string }{
		{"", "порожній"},
		{`{"submit": {"path": "/x", "body": {"prompt": "{{prompt}}"}},}`, "рядку 1"},
		{"{\n  \"submit\": {\"path\": \"/x\"\n}", "рядку 3"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "resut": "url"}`, `невідоме поле "resut"`},
		{`{"submit": {"path": "/x", "body": {"p": "hi"}}}`, "немає {{prompt}}"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}", "k": "{{key}}"}}}`, "ключ не вписують"},
		{`{"submit": {"path": "/x", "headers": {"Authorization": "Bearer x"}, "body": {"p": "{{prompt}}"}}}`, `ключ додає поле "auth"`},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "id": "id", "result": "url"}`, "poll: потрібен"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "id": "id", "poll": {"path": "/x/1"}, "result": "url"}`, "poll.path: додай {id}"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "id": "id", "poll": {"path": "/x/{id}"}}`, "state: вкажи"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "state": "status", "result": "url"}`, "done: перелічи"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "poll": {"path": "/x/{id}"}, "result": "url"}`, `poll: задано, але немає "id"`},
		{`{"auth": "cookie", "submit": {"path": "/x", "body": {"p": "{{prompt}}"}}}`, "auth: невідомий спосіб"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}", "q": "{{fps}}"}}}`, "невідома підстановка {{fps}}"},
		{`{"submit": {"path": "/x/{id}", "body": {"p": "{{prompt}}"}}}`, "submit: {id} ще немає"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "caps": {"lengths": [0], "aspects": ["wide"]}}`, "caps.lengths"},
		{`{"submit": {"path": "/x", "every": "10", "body": {"p": "{{prompt}}"}}}`, "submit.every: неправильний тип"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}, "result": 5}`, "result: потрібен шлях"},
		{`{"submit": {"path": "/x", "body": {"p": "{{seconds|upper}} {{prompt}}"}}}`, "невідомий модифікатор"},
		{`{"submit": {"path": "/x", "encoding": "xml", "body": {"p": "{{prompt}}"}}}`, "submit.encoding"},
		{`{"submit": {"path": "/x", "body": {"p": "{{prompt}}"}}} trailing`, "зайвий текст"},
	}
	for _, c := range cases {
		_, err := ParseSpec(c.spec)
		var se *SpecError
		if err == nil || !errors.As(err, &se) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  → %v\n  want %q", clip(c.spec, 120), err, c.want)
		}
	}
	// a fine minimal spec, absolute submit path
	s, err := ParseSpec(`{"submit": {"path": "https://api.example.com/v1/img", "body": {"prompt": "{{prompt}}"}}, "result": "url"}`)
	if err != nil || s.origin() != "https://api.example.com" {
		t.Fatalf("minimal: %v", err)
	}
	if _, warns, err := CheckSpec(tplSyncImage, false); err != nil || len(warns) != 0 {
		t.Errorf("sync image template: %v %v", warns, err)
	}
	if _, warns, _ := CheckSpec(`{"submit":{"path":"/v","body":{"p":"{{prompt}}"}},"result":"url"}`, true); len(warns) != 2 {
		t.Errorf("video warnings: %v", warns)
	}
}

func TestSpecCaps(t *testing.T) {
	if c := SpecCaps(""); fmt.Sprint(c.Video.Lengths) != "[5 10]" || c.Video.Components || c.Image.MaxRefs != 0 {
		t.Errorf("defaults %+v", c)
	}
	if c := SpecCaps("{broken"); fmt.Sprint(c.Video.Lengths) != "[5 10]" {
		t.Errorf("broken → defaults %+v", c)
	}
	if c := VideoCapsFor(PresetUniversal, "x"); fmt.Sprint(c.Lengths) != "[5 10]" || !c.StartFrame {
		t.Errorf("VideoCapsFor universal %+v", c)
	}
	refs := `{"submit":{"path":"/x","body":{"p":"{{prompt}}","r":"{{refs}}"}},"result":"u","caps":{"lengths":[10,4,4,6],"maxRefs":3}}`
	if c := SpecCaps(refs); fmt.Sprint(c.Video.Lengths) != "[4 6 10]" || c.Video.MaxRefs != 3 || !c.Video.Components || c.Image.MaxRefs != 3 || !c.Video.StartFrame {
		t.Errorf("refs %+v", c)
	}
	start := `{"submit":{"path":"/x","body":{"p":"{{prompt}}","i":"{{start_image_b64}}"}},"result":"u","caps":{"maxRefs":9}}`
	if c := SpecCaps(start); c.Video.MaxRefs != 0 || c.Video.Components || !c.Video.StartFrame || c.Image.MaxRefs != 1 {
		t.Errorf("start only %+v", c)
	}
}

// ---- protocol behaviour ---------------------------------------------------------------------

// A synchronous API answering with the raw image to a multipart form
// (Stability-style), auth in a custom header.
func TestUniversalMultipartRawBytes(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	var form *multipart.Form
	m.on("POST /v2/generate", func(w http.ResponseWriter, r *http.Request) {
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		f, err := multipart.NewReader(r.Body, params["boundary"]).ReadForm(1 << 20)
		if err != nil {
			t.Errorf("multipart: %v", err)
		}
		form = f
		w.Header().Set("Content-Type", "image/webp")
		w.Write(append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 32)...))
	})
	spec := `{"auth": "header:X-Api-Key", "submit": {"path": "/v2/generate", "encoding": "multipart", "headers": {"Accept": "image/*"},
	  "body": {"prompt": "{{prompt}}", "aspect_ratio": "{{aspect}}", "image": "{{start_image}}", "output_format": "webp"}}}`
	c := uniClient(m.srv.URL, "", spec, "")
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "castle", Aspect: "1:1", Refs: twoRefs()})
	if err != nil || mt != "image/webp" || len(data) == 0 {
		t.Fatalf("image: %v %q", err, mt)
	}
	s := m.requests("POST /v2/generate")[0]
	if s.header.Get("X-Api-Key") != uniKey || s.header.Get("Authorization") != "" || s.header.Get("Accept") != "image/*" {
		t.Fatalf("headers %v", s.header)
	}
	if form == nil || form.Value["prompt"][0] != "castle" || form.Value["aspect_ratio"][0] != "1:1" || len(form.File["image"]) != 1 {
		t.Fatalf("form %+v", form)
	}
	if fh := form.File["image"][0]; fh.Header.Get("Content-Type") != "image/jpeg" || fh.Size != int64(len(jpegBytes)) {
		t.Fatalf("file part %v %d", fh.Header, fh.Size)
	}
	noKeyLeak(t, m, uniKey, "X-Api-Key")
}

// Async jobs whose file comes from one more call answering with the bytes
// (OpenAI videos-style "content" endpoint); progress is reported; query auth
// goes only to the API's own origin.
func TestUniversalFetchRawAndQueryAuth(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	cdn := newMockAPI(t)
	cdn.on("GET /f.mp4", serveBytes("video/mp4", mp4Bytes))
	m.on("POST /v1/videos", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"vid_1","status":"queued"}`) })
	var polls atomic.Int32
	m.on("GET /v1/videos/vid_1", func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1:
			writeJSON(w, 200, `{"id":"vid_1","status":"in_progress","progress":40}`)
		default:
			writeJSON(w, 200, `{"id":"vid_1","status":"completed","progress":100}`)
		}
	})
	m.on("GET /v1/videos/vid_1/content", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cdn.srv.URL+"/f.mp4", http.StatusFound)
	})
	spec := `{"auth": "query:api_key", "submit": {"path": "/v1/videos", "body": {"prompt": "{{prompt}}", "seconds": "{{seconds|text}}", "size": "{{size}}"}},
	  "id": "id", "poll": {"path": "/v1/videos/{id}", "every": 1}, "state": "status", "done": ["completed"], "failed": ["failed"],
	  "queued": ["queued"], "progress": "progress", "fetch": {"path": "/v1/videos/{id}/content"},
	  "caps": {"lengths": [4, 8, 12], "aspects": ["16:9", "9:16"]}}`
	c := uniClient(m.srv.URL, "", "", spec)
	var seenPct []int
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "rain", Aspect: "16:9", Seconds: 10}, func(p int, _ string) { seenPct = append(seenPct, p) })
	if err != nil || string(clip) != string(mp4Bytes) {
		t.Fatalf("video: %v", err)
	}
	body := m.requests("POST /v1/videos")[0].json(t)
	if body["seconds"] != "12" || body["size"] != "1280x720" {
		t.Fatalf("body %v", body)
	}
	for _, s := range m.all() {
		if !strings.Contains(s.query, "api_key="+uniKey) {
			t.Errorf("%s %s: no query key", s.method, s.path)
		}
	}
	for _, s := range cdn.all() {
		if strings.Contains(s.query, uniKey) || s.header.Get("Authorization") != "" {
			t.Errorf("the key went to another origin: %s %q", s.path, s.query)
		}
	}
	if !containsInt(seenPct, 20+28) { // 40 % of the render band
		t.Errorf("progress %v", seenPct)
	}
}

// A status URL on another host gets no key (header auth), a result on
// another host neither.
func TestUniversalKeyStaysOnOrigin(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	other := newMockAPI(t)
	other.on("GET /status/1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"state":"ok","file":"%s/img.png"}`, other.srv.URL))
	})
	other.on("GET /img.png", serveBytes("image/png", pngBytes))
	m.on("POST /go", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"job":{"id":1,"poll":"%s/status/1"}}`, other.srv.URL))
	})
	spec := `{"submit": {"path": "/go", "body": {"prompt": "{{prompt}}"}}, "id": "job.id",
	  "poll": {"path": "{{submit.job.poll}}"}, "state": "state", "done": ["ok"], "result": "file"}`
	c := uniClient(m.srv.URL, "", spec, "")
	if _, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if got := m.requests("POST /go")[0].header.Get("Authorization"); got != "Bearer "+uniKey {
		t.Fatalf("own origin auth %q", got)
	}
	for _, s := range other.all() {
		if s.header.Get("Authorization") != "" {
			t.Errorf("key sent to another origin: %s", s.path)
		}
	}
}

func TestUniversalSubmitRetries(t *testing.T) {
	fast(t)
	spec := `{"submit": {"path": "/img", "body": {"prompt": "{{prompt}}"}}, "result": "url"}`
	slow := `{"submit": {"path": "/img", "timeout": 20, "body": {"prompt": "{{prompt}}"}}, "result": "url"}` // 20 ms under fast()
	// 429 with Retry-After → the same request again, same Idempotency-Key.
	m := newMockAPI(t)
	var n atomic.Int32
	m.on("POST /img", func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, `{"error":{"message":"slow down"}}`)
			return
		}
		writeJSON(w, 200, fmt.Sprintf(`{"url":"%s/f"}`, "http://"+r.Host))
	})
	m.on("GET /f", serveBytes("image/png", pngBytes))
	if _, _, err := uniClient(m.srv.URL, "", spec, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	ps := m.requests("POST /img")
	if len(ps) != 2 || ps[0].header.Get("Idempotency-Key") == "" || ps[0].header.Get("Idempotency-Key") != ps[1].header.Get("Idempotency-Key") {
		t.Fatalf("retry: %d posts", len(ps))
	}
	// Refusals are final: one request each.
	for _, st := range []int{400, 401, 402, 403} {
		m := newMockAPI(t)
		m.on("POST /img", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, st, `{"error":{"message":"no"}}`) })
		_, _, err := uniClient(m.srv.URL, "", spec, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
		if err == nil || len(m.requests("POST /img")) != 1 {
			t.Errorf("%d: %v, %d posts", st, err, len(m.requests("POST /img")))
		}
	}
	// A connection lost before any answer is repeated.
	m = newMockAPI(t)
	n.Store(0)
	m.on("POST /img", func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		writeJSON(w, 200, fmt.Sprintf(`{"url":"%s/f"}`, "http://"+r.Host))
	})
	m.on("GET /f", serveBytes("image/png", pngBytes))
	if _, _, err := uniClient(m.srv.URL, "", spec, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"}); err != nil || n.Load() != 2 {
		t.Fatalf("lost connection: %v (%d posts)", err, n.Load())
	}
	// A timeout is never repeated (the job may be running and billed).
	m = newMockAPI(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	m.on("POST /img", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(2 * time.Second):
		}
	})
	_, _, err := uniClient(m.srv.URL, "", slow, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "не відповів вчасно") || len(m.requests("POST /img")) != 1 {
		t.Fatalf("timeout: %v, %d posts", err, len(m.requests("POST /img")))
	}
}

func TestUniversalFailedAndCancelled(t *testing.T) {
	fast(t)
	spec := `{"submit": {"path": "/jobs", "body": {"prompt": "{{prompt}}"}}, "id": "id", "poll": {"path": "/jobs/{id}"},
	  "state": "status", "done": ["done"], "failed": ["failed", "canceled"], "result": "url", "error": "error.message",
	  "cancel": {"method": "DELETE", "path": "/jobs/{id}"}}`
	m := newMockAPI(t)
	m.on("POST /jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"j9"}`) })
	m.on("GET /jobs/j9", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"status":"failed","error":{"message":"Prompt flagged by the safety system"}}`)
	})
	_, err := uniClient(m.srv.URL, "", "", spec).Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: startFrame()}, nil)
	var pe *Error
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") || !errors.As(err, &pe) || pe.Retry {
		t.Fatalf("failed: %v", err)
	}
	if len(m.requests("DELETE")) != 0 {
		t.Fatal("a failed job is not cancelled")
	}
	// The caller gives up: the job is cancelled.
	m = newMockAPI(t)
	m.on("POST /jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"j7"}`) })
	m.on("GET /jobs/j7", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"status":"running"}`) })
	cancelled := make(chan string, 1)
	m.on("DELETE /jobs/j7", func(w http.ResponseWriter, r *http.Request) {
		cancelled <- r.Header.Get("Authorization")
		w.WriteHeader(204)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := uniClient(m.srv.URL, "", "", spec).Video(ctx, gen.VideoRequest{Prompt: "p", Start: startFrame()}, nil); err == nil {
		t.Fatal("want an error")
	}
	select {
	case auth := <-cancelled:
		if auth != "Bearer "+uniKey {
			t.Errorf("cancel auth %q", auth)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the job was not cancelled")
	}
}

func TestUniversalResultForms(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("GET /rel/file.png", serveBytes("image/png", pngBytes))
	m.on("GET /page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html><body>Your image is ready</body></html>")
	})
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	for name, tc := range map[string]struct {
		reply, result, want string
	}{
		"data uri":      {`{"img":"data:image/png;base64,` + b64 + `"}`, `"img"`, ""},
		"raw base64":    {`{"b":"` + b64 + `"}`, `"b"`, ""},
		"relative url":  {`{"u":"/rel/file.png"}`, `"u"`, ""},
		"array → first": {`{"out":["", "/rel/file.png"]}`, `"out"`, ""},
		"object → url":  {`{"out":{"url":"/rel/file.png"}}`, `"out"`, ""},
		"second path":   {`{"b":{"url":"/rel/file.png"}}`, `["a.url", "b.url"]`, ""},
		"not a file":    {`{"u":"/page"}`, `"u"`, "не файл"},
		"gibberish":     {`{"u":"hello world"}`, `"u"`, "не посилання і не файл"},
		"missing":       {`{"other":1}`, `"u"`, "у відповіді немає файлу"},
		"no result":     {`{"u":"x"}`, `[]`, `вкажи в описі "result"`},
	} {
		reply := tc.reply
		m.on("POST /"+strings.ReplaceAll(name, " ", "-"), func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, reply) })
		spec := fmt.Sprintf(`{"submit": {"path": "/%s", "body": {"prompt": "{{prompt}}"}}, "result": %s}`, strings.ReplaceAll(name, " ", "-"), tc.result)
		data, mt, err := uniClient(m.srv.URL, "", spec, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
		switch {
		case tc.want == "" && (err != nil || string(data) != string(pngBytes) || mt != "image/png"):
			t.Errorf("%s: %v %q", name, err, mt)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

func TestUniversalEndpointAndTest(t *testing.T) {
	fast(t)
	sync := `{"submit": {"path": "/img", "body": {"prompt": "{{prompt}}"}}, "result": "url"}`
	cases := []struct {
		custom config.CustomAPI
		want   string
	}{
		{config.CustomAPI{ImagePreset: PresetUniversal}, "опис API порожній"},
		{config.CustomAPI{ImagePreset: PresetUniversal, ImageSpec: `{"submit":{}}`}, "submit.path"},
		{config.CustomAPI{ImagePreset: PresetUniversal, ImageSpec: sync}, "вкажи адресу API"},
		{config.CustomAPI{ImagePreset: PresetUniversal, ImageSpec: sync, ImageBaseURL: "ftp://x"}, "некоректна адреса"},
		{config.CustomAPI{ImagePreset: PresetUniversal, ImageBaseURL: "https://a.example", ImageSpec: strings.Replace(sync, `{"submit"`, `{"auth":"bearer","submit"`, 1)}, "не задано API-ключ"},
	}
	for _, c := range cases {
		cl := customClient(func(cfg *config.Config) { cfg.Custom = c.custom })
		_, _, err := cl.Image(context.Background(), gen.ImageRequest{Prompt: "p"})
		ok, msg := cl.TestImage(context.Background())
		if err == nil || !strings.Contains(err.Error(), c.want) || ok || !strings.Contains(msg, c.want) {
			t.Errorf("%+v: %v / %q, want %q", c.custom, err, msg, c.want)
		}
	}
	// No key and the default auth: no Authorization header; an absolute
	// submit path gives the base URL; the check needs no network.
	m := newMockAPI(t)
	m.on("POST /img", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"url":"%s/f"}`, "http://"+r.Host))
	})
	m.on("GET /f", serveBytes("image/png", pngBytes))
	abs := strings.Replace(sync, `"/img"`, `"`+m.srv.URL+`/img"`, 1)
	cl := customClient(func(cfg *config.Config) { cfg.Custom = config.CustomAPI{ImagePreset: PresetUniversal, ImageSpec: abs} })
	if _, _, err := cl.Image(context.Background(), gen.ImageRequest{Prompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if h := m.requests("POST /img")[0].header.Get("Authorization"); h != "" {
		t.Fatalf("no key, but Authorization %q", h)
	}
	before := len(m.all())
	if ok, msg := cl.TestImage(context.Background()); !ok || !strings.Contains(msg, "опис API коректний") || len(m.all()) != before {
		t.Fatalf("untested check: %v %q", ok, msg)
	}
	// With "test": an authenticated GET, nothing generated.
	m.on("GET /v1/account", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+uniKey {
			writeJSON(w, 401, `{"error":"bad key"}`)
			return
		}
		writeJSON(w, 200, `{"ok":true}`)
	})
	withTest := strings.Replace(sync, `"result"`, `"test": {"path": "/v1/account"}, "result"`, 1)
	ok, msg := uniClient(m.srv.URL, "m-1", withTest, "").TestImage(context.Background())
	if !ok || !strings.Contains(msg, "ключ працює") || !strings.Contains(msg, "модель m-1") {
		t.Fatalf("test: %v %q", ok, msg)
	}
	bad := customClient(func(cfg *config.Config) {
		cfg.Custom = config.CustomAPI{ImagePreset: PresetUniversal, ImageBaseURL: m.srv.URL, ImageKey: "wrong-key-123456", ImageSpec: withTest}
	})
	if ok, msg := bad.TestImage(context.Background()); ok || !strings.Contains(msg, "ключ не прийнято") || strings.Contains(msg, "wrong-key-123456") {
		t.Fatalf("bad key: %v %q", ok, msg)
	}
	if n := len(m.requests("POST")); n != 1 {
		t.Fatalf("checks generated something: %d posts", n)
	}
}

// Client.Video snaps the length and aspect to the spec's caps and drops
// components mode when the spec takes no references.
func TestUniversalVideoSnapsToSpec(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"url":"%s/f"}`, "http://"+r.Host))
	})
	m.on("GET /f", serveBytes("video/mp4", mp4Bytes))
	spec := `{"submit": {"path": "/v", "body": {"prompt": "{{prompt}}", "d": "{{seconds}}", "a": "{{aspect}}", "img": "{{start_image}}"}},
	  "result": "url", "caps": {"lengths": [4, 6, 8, 10], "aspects": ["16:9", "9:16"]}}`
	c := uniClient(m.srv.URL, "", "", spec)
	refs := []gen.RefImage{{Data: pngBytes, Name: "storyboard.png"}}
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: startFrame(), Seconds: 7, Aspect: "3:4", Mode: gen.ModeComponents, Refs: refs}, nil); err != nil {
		t.Fatal(err)
	}
	b := m.requests("POST /v")[0].json(t)
	if b["d"] != float64(8) || b["a"] != "9:16" || !strings.HasPrefix(fmt.Sprint(b["img"]), "data:image/jpeg") || strings.Contains(fmt.Sprint(b["prompt"]), "Reference images") {
		t.Fatalf("body %v", b)
	}
	// Synchronous video: the progress estimate moves while the request is open.
	slow := newMockAPI(t)
	slow.on("POST /v", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		writeJSON(w, 200, fmt.Sprintf(`{"url":"%s/f"}`, "http://"+r.Host))
	})
	slow.on("GET /f", serveBytes("video/mp4", mp4Bytes))
	var mid atomic.Int32
	_, err := uniClient(slow.srv.URL, "", "", spec).Video(context.Background(), gen.VideoRequest{Prompt: "p", Start: startFrame()}, func(p int, _ string) {
		if p > 5 && p < 100 {
			mid.Store(int32(p))
		}
	})
	if err != nil || mid.Load() < 20 {
		t.Fatalf("sync progress: %v %d", err, mid.Load())
	}
}

// The API's own error text reaches the user, the key never does.
func TestUniversalErrorsRedacted(t *testing.T) {
	fast(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 400, fmt.Sprintf(`{"error":{"message":"bad request for key %s"}}`, uniKey))
	}))
	defer srv.Close()
	spec := `{"submit": {"path": "/x", "body": {"prompt": "{{prompt}}"}}, "result": "u"}`
	_, _, err := uniClient(srv.URL, "", spec, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
	if err == nil || strings.Contains(err.Error(), uniKey) || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("error %v", err)
	}
}

// Status checks: a 429 is waited out (not a failure); server trouble in a
// row cancels the job, and the error invites a retry only when the cancel
// was accepted (or there is no way to cancel: never).
func TestUniversalPollFailuresCancel(t *testing.T) {
	fast(t)
	spec := `{"submit": {"path": "/jobs", "body": {"prompt": "{{prompt}}"}}, "id": "id", "poll": {"path": "/jobs/{id}"},
	  "state": "status", "done": ["done"], "result": "url", "cancel": {"path": "/jobs/{id}/cancel"}}`
	m := newMockAPI(t)
	m.on("POST /jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"j1"}`) })
	var polls atomic.Int32
	m.on("GET /jobs/j1", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) <= int32(maxBlips)+2 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, `{"error":"slow down"}`)
			return
		}
		writeJSON(w, 200, fmt.Sprintf(`{"status":"done","url":"%s/f"}`, "http://"+r.Host))
	})
	m.on("GET /f", serveBytes("image/png", pngBytes))
	if _, _, err := uniClient(m.srv.URL, "", spec, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"}); err != nil {
		t.Fatalf("429s: %v", err)
	}
	for _, tc := range []struct {
		cancel    string // "" = the spec has no cancel
		status    int
		wantRetry bool
	}{{"/jobs/{id}/cancel", 200, true}, {"/jobs/{id}/cancel", 409, false}, {"", 0, false}} {
		m := newMockAPI(t)
		m.on("POST /jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, `{"id":"j2"}`) })
		m.on("GET /jobs/j2", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 503, `{"error":"busy"}`) })
		m.on("POST /jobs/j2/cancel", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.status, `{}`) })
		s := spec
		if tc.cancel == "" {
			s = strings.Replace(spec, `, "cancel": {"path": "/jobs/{id}/cancel"}`, "", 1)
		}
		_, _, err := uniClient(m.srv.URL, "", s, "").Image(context.Background(), gen.ImageRequest{Prompt: "p"})
		var pe *Error
		if err == nil || !errors.As(err, &pe) || pe.Retry != tc.wantRetry {
			t.Fatalf("cancel %q %d: %v", tc.cancel, tc.status, err)
		}
		if n := len(m.requests("GET /jobs/j2")); n != maxBlips+1 {
			t.Fatalf("%d polls", n)
		}
		wantCancels := 1
		if tc.cancel == "" {
			wantCancels = 0
		}
		if n := len(m.requests("POST /jobs/j2/cancel")); n != wantCancels {
			t.Fatalf("%d cancels", n)
		}
	}
}
