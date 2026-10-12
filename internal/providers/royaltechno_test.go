package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

const royalKey = "rt_live_0123456789abcdef0123456789abcdef"

var reUUID4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func royalClient(base, imageModel, videoModel string) *Client {
	return customClient(func(c *config.Config) {
		c.Custom = config.CustomAPI{
			ImagePreset: PresetRoyal, ImageBaseURL: base, ImageKey: royalKey, ImageModel: imageModel,
			VideoPreset: PresetRoyal, VideoBaseURL: base, VideoKey: royalKey, VideoModel: videoModel,
		}
		c.Video.Resolution = "720p"
	})
}

// royalJobs scripts one job: queued (with an ETag) → 304 → succeeded.
func royalJobs(t *testing.T, m *mockAPI, prefix, id, result string) *atomic.Int32 {
	t.Helper()
	m.on("POST "+prefix+"/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 201, fmt.Sprintf(`{"id":%q,"object":"job","status":"queued"}`, id))
	})
	var polls atomic.Int32
	m.on("GET "+prefix+"/jobs/"+id, func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1:
			w.Header().Set("ETag", `"e1"`)
			writeJSON(w, 200, fmt.Sprintf(`{"id":%q,"status":"queued","queue_position":3}`, id))
		case 2:
			if got := r.Header.Get("If-None-Match"); got != `"e1"` {
				t.Errorf("If-None-Match = %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			writeJSON(w, 200, fmt.Sprintf(`{"id":%q,"status":"succeeded","output":{"type":"x","url":"%s%s"}}`, id, m.srv.URL, result))
		}
	})
	return &polls
}

func TestRoyalImageWithReferences(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	royalJobs(t, m, "/v1", "job_img", "/cdn/img.png")
	m.on("GET /cdn/img.png", serveBytes("image/png", pngBytes))
	c := royalClient(m.srv.URL, "nano-banana-pro", "") // base without /v1
	refs := []gen.RefImage{{Data: jpegBytes, MIME: "image/jpeg", Name: "taras.jpg"}, {Data: pngBytes, MIME: "image/png", Name: "fox.png"}}
	data, mt, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "@taras meets @fox", Aspect: "3:4", Refs: refs})
	if err != nil || string(data) != string(pngBytes) || mt != "image/png" {
		t.Fatalf("image: %v %q", err, mt)
	}
	post := m.requests("POST /v1/jobs")
	if len(post) != 1 {
		t.Fatalf("posts: %d", len(post))
	}
	if got := post[0].header.Get("Authorization"); got != "Bearer "+royalKey {
		t.Fatalf("auth %q", got)
	}
	if k := post[0].header.Get("Idempotency-Key"); !reUUID4.MatchString(k) {
		t.Fatalf("Idempotency-Key %q", k)
	}
	body := post[0].json(t)
	in := body["input"].(map[string]any)
	if body["model"] != "nano-banana-pro" || in["aspect_ratio"] != "portrait" {
		t.Fatalf("body %v", body)
	}
	urls, _ := in["reference_image_urls"].([]any)
	if len(urls) != 2 || !strings.HasPrefix(urls[0].(string), "data:image/jpeg;base64,") || in["start_image_url"] != nil {
		t.Fatalf("refs %v", in)
	}
	if p := in["prompt"].(string); !strings.Contains(p, "image 1 = @taras") || !strings.Contains(p, "image 2 = @fox") {
		t.Fatalf("legend missing: %q", p)
	}
	noKeyLeak(t, m, royalKey, "Authorization")
}

func TestRoyalImageSingleSourceModel(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	royalJobs(t, m, "/v1", "job_g", "/cdn/g.jpg")
	m.on("GET /cdn/g.jpg", serveBytes("image/jpeg", jpegBytes))
	c := royalClient(m.srv.URL+"/v1", "gpt-image-2", "")
	refs := []gen.RefImage{{Data: jpegBytes, MIME: "image/jpeg"}, {Data: pngBytes, MIME: "image/png"}}
	if _, _, err := c.Image(context.Background(), gen.ImageRequest{Prompt: "a crown", Aspect: "16:9", Refs: refs}); err != nil {
		t.Fatal(err)
	}
	in := m.requests("POST /v1/jobs")[0].json(t)["input"].(map[string]any)
	if in["reference_image_urls"] != nil || !strings.HasPrefix(fmt.Sprint(in["start_image_url"]), "data:image/jpeg") || in["aspect_ratio"] != "landscape" {
		t.Fatalf("input %v", in)
	}
}

func TestRoyalOmniFlashComponents(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	royalJobs(t, m, "/v1", "job_v", "/cdn/v.mp4")
	m.on("GET /cdn/v.mp4", serveBytes("video/mp4", mp4Bytes))
	c := royalClient(m.srv.URL+"/v1", "", "omni-flash")
	refs := make([]gen.RefImage, 9)
	for i := range refs {
		refs[i] = gen.RefImage{Data: jpegBytes, MIME: "image/jpeg", Name: fmt.Sprintf("r%d.jpg", i)}
	}
	var last int
	clip, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "walk", Start: startFrame(), Aspect: "16:9", Seconds: 10,
		Mode: gen.ModeComponents, Refs: refs}, func(p int, _ string) { last = p })
	if err != nil || string(clip) != string(mp4Bytes) || last != 100 {
		t.Fatalf("video: %v (progress %d)", err, last)
	}
	in := m.requests("POST /v1/jobs")[0].json(t)["input"].(map[string]any)
	urls, _ := in["reference_image_urls"].([]any)
	if len(urls) != 7 || in["start_image_url"] != nil || in["duration_sec"] != float64(10) || in["aspect_ratio"] != "landscape" || in["resolution"] != nil {
		t.Fatalf("input %v", in)
	}
}

func TestRoyalVeoStartFrame1080(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	royalJobs(t, m, "/v1", "job_veo", "/cdn/veo.mp4")
	m.on("GET /cdn/veo.mp4", serveBytes("video/mp4", mp4Bytes))
	c := royalClient(m.srv.URL, "", "veo-3.1")
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "dolly", Start: startFrame(), Aspect: "9:16", Seconds: 6, Resolution: "1080p"}, nil); err != nil {
		t.Fatal(err)
	}
	in := m.requests("POST /v1/jobs")[0].json(t)["input"].(map[string]any)
	if in["duration_sec"] != nil || in["resolution"] != "1080p" || in["aspect_ratio"] != "portrait" || !strings.HasPrefix(fmt.Sprint(in["start_image_url"]), "data:image/jpeg") {
		t.Fatalf("input %v", in)
	}
}

func TestRoyalSubmitRetriesWithSameKey(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	var posts atomic.Int32
	m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if posts.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, `{"error":{"type":"rate_limit","code":"too_many_requests","message":"slow down"}}`)
			return
		}
		writeJSON(w, 201, `{"id":"job_r","status":"processing"}`)
	})
	m.on("GET /v1/jobs/job_r", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, fmt.Sprintf(`{"id":"job_r","status":"succeeded","output":{"url":"%s/cdn/r.mp4"}}`, m.srv.URL))
	})
	m.on("GET /cdn/r.mp4", serveBytes("video/mp4", mp4Bytes))
	c := royalClient(m.srv.URL, "", "omni-flash")
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "go", Start: startFrame(), Seconds: 8}, nil); err != nil {
		t.Fatal(err)
	}
	ps := m.requests("POST /v1/jobs")
	if len(ps) != 2 || ps[0].header.Get("Idempotency-Key") != ps[1].header.Get("Idempotency-Key") {
		t.Fatalf("retries must reuse the Idempotency-Key: %d posts", len(ps))
	}
}

func TestRoyalRefusalsAreFinal(t *testing.T) {
	fast(t)
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{402, `{"error":{"type":"quota","code":"daily_limit_reached","message":"Daily limit of 100 reached"}}`, "денний ліміт"},
		{429, `{"error":{"type":"quota","code":"daily_limit_reached","message":"Daily limit of 100 reached"}}`, "денний ліміт"},
		{429, `{"error":{"type":"rate_limit","code":"queue_full","message":"Too many active jobs"}}`, "черга RoyalTechno заповнена"},
		{503, `{"error":{"type":"upstream","code":"model_unavailable","message":"veo is down"}}`, "модель тимчасово недоступна"},
		{400, `{"error":{"type":"invalid_request","code":"content_policy","message":"Rejected"}}`, "фільтром безпеки"},
		{402, `{"error":{"type":"billing","code":"insufficient_credits","message":"Top up"}}`, "закінчились кошти"},
		{401, `{"error":{"type":"auth","code":"invalid_credentials","message":"bad key"}}`, "ключ не прийнято"},
		{400, `{"error":{"type":"invalid_request","code":"unsupported_combination","message":"end_image_url needs veo"}}`, "end_image_url needs veo"},
	} {
		m := newMockAPI(t)
		m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.status, tc.body) })
		c := royalClient(m.srv.URL, "", "omni-flash")
		_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "go", Start: startFrame()}, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: %v", tc.status, err)
		}
		if n := len(m.requests("POST /v1/jobs")); n != 1 {
			t.Errorf("%d: %d posts, want 1", tc.status, n)
		}
	}
}

func TestRoyalFailedJob(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"job_f","status":"queued"}`) })
	m.on("GET /v1/jobs/job_f", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":"job_f","status":"failed","error":{"type":"upstream_error","code":"content_policy","message":"Blocked by the safety filter","retryable":false}}`)
	})
	c := royalClient(m.srv.URL, "", "omni-flash")
	_, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "go", Start: startFrame()}, nil)
	var pe *Error
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") || !errorsAs(err, &pe) || pe.Retry {
		t.Fatalf("failed job: %v", err)
	}
}

func TestRoyalCancelWhenCallerGivesUp(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"job_c","status":"queued"}`) })
	m.on("GET /v1/jobs/job_c", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":"job_c","status":"processing"}`)
	})
	cancelled := make(chan struct{}, 1)
	m.on("POST /v1/jobs/job_c/cancel", func(w http.ResponseWriter, r *http.Request) {
		cancelled <- struct{}{}
		writeJSON(w, 200, `{"id":"job_c","status":"canceled"}`)
	})
	c := royalClient(m.srv.URL, "", "omni-flash")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	if _, err := c.Video(ctx, gen.VideoRequest{Prompt: "go", Start: startFrame()}, nil); err == nil {
		t.Fatal("want an error")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the job was not cancelled")
	}
}

func TestRoyalTest(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("GET /v1/account", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"balance_usd_cents":125,"balance_usd":"$1.25","plans":[{"plan_id":"p","name":"Veo Unlimited"}],
			"unlimited":[{"kind":"veo","label":"Veo"}],"daily":[{"kind":"nano_banana","label":"Nano Banana","limit":100,"used_today":4,"remaining_today":96,"reset_at":"x"}],"allowances":[],"limits":{}}`)
	})
	m.on("GET /v1/pricing", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"object":"list","data":[{"model":"veo-3.1"},{"model":"omni-flash"},{"model":"nano-banana-pro"}]}`)
	})
	ok, msg := royalClient(m.srv.URL, "", "omni-flash").TestVideo(context.Background())
	if !ok || !strings.Contains(msg, "тариф Veo Unlimited") || !strings.Contains(msg, "без ліміту: Veo") ||
		!strings.Contains(msg, "Nano Banana: сьогодні лишилось 96 з 100") || !strings.Contains(msg, "$1.25") {
		t.Fatalf("test: %v %q", ok, msg)
	}
	ok, msg = royalClient(m.srv.URL+"/v1", "", "kling-9").TestVideo(context.Background())
	if ok || !strings.Contains(msg, "kling-9") || !strings.Contains(msg, "omni-flash, veo-3.1") {
		t.Fatalf("unknown model: %v %q", ok, msg)
	}
	if len(m.requests("POST")) != 0 {
		t.Fatal("the check must not generate anything")
	}
	noKeyLeak(t, m, royalKey, "Authorization", msg)
}

func TestRoyalCapsAndOmniFlash(t *testing.T) {
	if c := VideoCapsFor(PresetRoyal, "omni-flash"); c.MaxRefs != 7 || !c.Components || fmt.Sprint(c.Lengths) != "[4 6 8 10]" {
		t.Fatalf("omni caps %+v", c)
	}
	if c := VideoCapsFor(PresetRoyal, "veo-3.1"); c.MaxRefs != 3 || fmt.Sprint(c.Lengths) != "[8]" {
		t.Fatalf("veo caps %+v", c)
	}
	if c := ImageCapsFor(PresetRoyal, "nano-banana-2"); c.MaxRefs != 10 || c.Aspect("1:1") != "16:9" || c.Aspect("3:4") != "9:16" {
		t.Fatalf("image caps %+v", c)
	}
	if !OmniFlash(PresetRoyal, "") || !OmniFlash(PresetRoyal, "omni-flash") || OmniFlash(PresetRoyal, "veo-3.1") || OmniFlash(PresetFal, "omni-flash") {
		t.Fatal("OmniFlash")
	}
	if _, ok := FindImagePreset(PresetRoyal); !ok {
		t.Fatal("image preset")
	}
	if _, ok := FindVideoPreset(PresetRoyal); !ok {
		t.Fatal("video preset")
	}
}

func errorsAs(err error, target **Error) bool { return errors.As(err, target) }

// A job failed with the content_policy code is a safety refusal even when
// its message says nothing about safety.
func TestRoyalContentPolicyCode(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"job_p","status":"queued"}`) })
	m.on("GET /v1/jobs/job_p", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"id":"job_p","status":"failed","error":{"code":"content_policy","message":"Rejected","retryable":true}}`)
	})
	_, err := royalClient(m.srv.URL, "", "omni-flash").Video(context.Background(), gen.VideoRequest{Prompt: "go", Start: startFrame()}, nil)
	var pe *Error
	if err == nil || !strings.Contains(err.Error(), "фільтром безпеки") || !errors.As(err, &pe) || pe.Retry {
		t.Fatalf("policy: %v", err)
	}
	if len(m.requests("POST /v1/jobs/job_p/cancel")) != 0 {
		t.Fatal("a failed job is not cancelled")
	}
}

// A 429 while polling waits its Retry-After and is not a failure: more of
// them than maxBlips still end in the clip.
func TestRoyalPoll429IsNotABlip(t *testing.T) {
	fast(t)
	m := newMockAPI(t)
	m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"job_l","status":"queued"}`) })
	var polls atomic.Int32
	m.on("GET /v1/jobs/job_l", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) <= int32(maxBlips)+3 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 429, `{"error":{"code":"too_many_requests","message":"slow down"}}`)
			return
		}
		writeJSON(w, 200, fmt.Sprintf(`{"id":"job_l","status":"succeeded","output":{"url":"%s/cdn/l.mp4"}}`, m.srv.URL))
	})
	m.on("GET /cdn/l.mp4", serveBytes("video/mp4", mp4Bytes))
	if _, err := royalClient(m.srv.URL, "", "omni-flash").Video(context.Background(), gen.VideoRequest{Prompt: "go", Start: startFrame()}, nil); err != nil {
		t.Fatal(err)
	}
	if len(m.requests("POST /v1/jobs/job_l/cancel")) != 0 || len(m.requests("POST /v1/jobs")) != 1 {
		t.Fatal("rate limits must neither cancel nor resubmit")
	}
}

// Server trouble while polling: after maxBlips the job is cancelled; the
// error invites a retry only when the cancel was accepted.
func TestRoyalPollBlipsCancel(t *testing.T) {
	fast(t)
	for _, cancelOK := range []bool{true, false} {
		m := newMockAPI(t)
		m.on("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 201, `{"id":"job_b","status":"queued"}`) })
		m.on("GET /v1/jobs/job_b", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 502, `{"error":{"message":"bad gateway"}}`) })
		m.on("POST /v1/jobs/job_b/cancel", func(w http.ResponseWriter, r *http.Request) {
			if cancelOK {
				writeJSON(w, 200, `{"id":"job_b","status":"canceled"}`)
				return
			}
			writeJSON(w, 409, `{"error":{"code":"not_cancelable","message":"already rendering"}}`)
		})
		_, err := royalClient(m.srv.URL, "", "omni-flash").Video(context.Background(), gen.VideoRequest{Prompt: "go", Start: startFrame()}, nil)
		var pe *Error
		if err == nil || !errors.As(err, &pe) {
			t.Fatalf("cancel ok %v: %v", cancelOK, err)
		}
		if n := len(m.requests("POST /v1/jobs/job_b/cancel")); n != 1 {
			t.Fatalf("cancel ok %v: %d cancels", cancelOK, n)
		}
		if pe.Retry != cancelOK || (!cancelOK && !strings.Contains(pe.Msg, "можливо, ще працює")) {
			t.Fatalf("cancel ok %v: retry %v %q", cancelOK, pe.Retry, pe.Msg)
		}
		if n := len(m.requests("GET /v1/jobs/job_b")); n != maxBlips+1 {
			t.Fatalf("%d polls, want %d", n, maxBlips+1)
		}
		if n := len(m.requests("POST /v1/jobs")) - len(m.requests("POST /v1/jobs/")); n != 1 {
			t.Fatalf("%d submits: a job that may run is never submitted again", n)
		}
	}
}

func TestRoyalBackoffAndTimeouts(t *testing.T) {
	for n, base := range []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60} {
		for i := 0; i < 20; i++ {
			d := royalBackoff(n)
			lo, hi := base*time.Second*8/10, base*time.Second*12/10
			if d < lo || d > hi {
				t.Fatalf("backoff(%d) = %s, want %s…%s", n, d, lo, hi)
			}
		}
	}
	if royalImagePollTimeout < 30*time.Minute {
		t.Fatalf("image poll timeout %s", royalImagePollTimeout)
	}
	if got := withLegend(strings.Repeat("я", 5000), " legend", 4000); len([]rune(got)) != 4000 {
		t.Fatalf("legend prompt: %d runes, want exactly the cap", len([]rune(got)))
	}
}
