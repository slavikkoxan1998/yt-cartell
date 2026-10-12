package glabs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

func uri(mime, data string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString([]byte(data))
}

func clientWith(base string, v config.Video) *Client {
	cfg := config.Config{Glabs: config.Glabs{BaseURL: base, APIKey: "k1"}, Video: v}
	return New(func() config.Config { return cfg })
}

func videoMock(t *testing.T) *mock {
	m := newMock(t, `{"status":"completed","results":["/f/x.mp4"]}`)
	m.files["/f/x.mp4"] = "v"
	return m
}

// Components mode: named refs in the caller's order, capped at 7 for Omni
// Flash, 10 s allowed; the start frame is not sent.
func TestVideoComponentsOmniFlashPayload(t *testing.T) {
	fast(t)
	m := videoMock(t)
	c := clientWith(m.srv.URL, config.Video{GlabsModel: "veo_31_fast", Resolution: "720p", Length: 8})
	refs := []gen.RefImage{
		{Data: []byte("board"), MIME: "image/jpeg", Name: "storyboard.jpg"},
		{Data: []byte("prev"), MIME: "image/jpeg", Name: "previous_shot.jpg"},
		{Data: nil, Name: "missing.png"}, // no data: skipped, does not count
	}
	for i := 1; i <= 6; i++ {
		refs = append(refs, gen.RefImage{Data: []byte(fmt.Sprintf("c%d", i)), MIME: "image/png", Name: fmt.Sprintf("hero%d.png", i)})
	}
	_, err := c.Video(context.Background(), gen.VideoRequest{
		Prompt: "@storyboard shot", Model: OmniFlash, Mode: gen.ModeComponents, Refs: refs, Seconds: 10,
		Aspect: "9:16", Start: &gen.RefImage{Data: []byte("frame"), MIME: "image/jpeg"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, r := range []struct{ mime, data, name string }{
		{"image/jpeg", "board", "storyboard.jpg"}, {"image/jpeg", "prev", "previous_shot.jpg"},
		{"image/png", "c1", "hero1.png"}, {"image/png", "c2", "hero2.png"}, {"image/png", "c3", "hero3.png"},
		{"image/png", "c4", "hero4.png"}, {"image/png", "c5", "hero5.png"},
	} {
		b, _ := json.Marshal(map[string]string{"data": uri(r.mime, r.data), "name": r.name})
		want = append(want, string(b))
	}
	s := m.submits[0]
	exp := map[string]string{
		"prompt":           `"@storyboard shot"`,
		"model":            `"omni_flash"`,
		"aspect_ratio":     `"9:16"`,
		"mode":             `"components"`,
		"reference_images": "[" + strings.Join(want, ",") + "]",
		"resolution":       `["720p"]`,
		"video_length":     `10`,
	}
	for k, v := range exp {
		if string(s[k]) != v {
			t.Errorf("%s = %s\nwant %s", k, s[k], v)
		}
	}
	if len(s) != len(exp) {
		t.Errorf("unexpected fields in components body: %v", keys(s))
	}
}

// Veo models take 3 components refs and at most 8 s.
func TestVideoComponentsVeoCapAndLength(t *testing.T) {
	fast(t)
	m := videoMock(t)
	c := clientWith(m.srv.URL, config.Video{GlabsModel: "veo_31_fast", Resolution: "1080p", Length: 10})
	var refs []gen.RefImage
	for i := 1; i <= 5; i++ {
		refs = append(refs, gen.RefImage{Data: []byte{byte(i)}, MIME: "image/png", Name: fmt.Sprintf("r%d.png", i)})
	}
	if _, err := c.Video(context.Background(), gen.VideoRequest{Prompt: "p", Mode: gen.ModeComponents, Refs: refs}, nil); err != nil {
		t.Fatal(err)
	}
	var got []struct{ Data, Name string }
	if err := json.Unmarshal(m.submits[0]["reference_images"], &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "r1.png" || got[2].Name != "r3.png" || got[1].Data != uri("image/png", "\x02") {
		t.Fatalf("veo components refs = %+v", got)
	}
	if string(m.submits[0]["mode"]) != `"components"` || string(m.submits[0]["model"]) != `"veo_31_fast"` ||
		string(m.submits[0]["video_length"]) != `8` {
		t.Fatalf("body = mode %s model %s length %s", m.submits[0]["mode"], m.submits[0]["model"], m.submits[0]["video_length"])
	}
}

// Settings decide the length when the request has none: 10 s only on Omni Flash.
func TestVideoLengthPerModel(t *testing.T) {
	fast(t)
	cases := []struct {
		settings config.Video
		req      gen.VideoRequest
		want     string
	}{
		{config.Video{GlabsModel: OmniFlash, Length: 10}, gen.VideoRequest{Prompt: "p"}, `10`},
		{config.Video{GlabsModel: OmniFlash, Length: 10}, gen.VideoRequest{Prompt: "p", Model: "veo_31_lite"}, `8`},
		{config.Video{GlabsModel: "veo_31_fast", Length: 4}, gen.VideoRequest{Prompt: "p", Model: OmniFlash, Seconds: 9}, `10`},
		{config.Video{GlabsModel: OmniFlash, Length: 6}, gen.VideoRequest{Prompt: "p"}, `6`},
		{config.Video{GlabsModel: OmniFlash}, gen.VideoRequest{Prompt: "p"}, `8`},
	}
	for i, tc := range cases {
		m := videoMock(t)
		if _, err := clientWith(m.srv.URL, tc.settings).Video(context.Background(), tc.req, nil); err != nil {
			t.Fatal(err)
		}
		if got := string(m.submits[0]["video_length"]); got != tc.want {
			t.Errorf("case %d: video_length %s, want %s", i, got, tc.want)
		}
	}
}

// Components without usable refs fall back to the start frame; unknown modes
// (start_end_image included) behave like auto — Omni Flash never gets
// start_end_image.
func TestVideoModeFallbacks(t *testing.T) {
	fast(t)
	start := &gen.RefImage{Data: []byte{1, 2, 3}, MIME: "image/webp"}
	cases := []struct {
		req        gen.VideoRequest
		mode, refs string
	}{
		{gen.VideoRequest{Prompt: "p", Model: OmniFlash, Mode: gen.ModeComponents, Refs: []gen.RefImage{{Name: "x.png"}}, Start: start},
			`"start_image"`, `["data:image/webp;base64,AQID"]`},
		{gen.VideoRequest{Prompt: "p", Model: OmniFlash, Mode: gen.ModeComponents}, `"text_to_video"`, `[]`},
		{gen.VideoRequest{Prompt: "p", Model: OmniFlash, Mode: "start_end_image", Start: start, Refs: []gen.RefImage{{Data: []byte{9}}}},
			`"start_image"`, `["data:image/webp;base64,AQID"]`},
		{gen.VideoRequest{Prompt: "p", Model: OmniFlash, Mode: "bogus"}, `"text_to_video"`, `[]`},
	}
	for i, tc := range cases {
		m := videoMock(t)
		if _, err := clientWith(m.srv.URL, config.Video{}).Video(context.Background(), tc.req, nil); err != nil {
			t.Fatal(err)
		}
		if got := string(m.submits[0]["mode"]); got != tc.mode {
			t.Errorf("case %d: mode %s, want %s", i, got, tc.mode)
		}
		if got := string(m.submits[0]["reference_images"]); got != tc.refs {
			t.Errorf("case %d: refs %s, want %s", i, got, tc.refs)
		}
	}
	if MaxVideoRefs(OmniFlash) != 7 || MaxVideoRefs("veo_31_quality") != 3 || MaxVideoRefs("") != 3 {
		t.Fatal("MaxVideoRefs")
	}
}
