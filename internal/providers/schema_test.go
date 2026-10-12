package providers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
)

// A trimmed copy of a real fal.ai platform answer (fal-ai/veo3.1/fast/image-to-video).
const falVeoDoc = `{"models":[{"endpoint_id":"fal-ai/veo3.1/fast/image-to-video","openapi":{
 "paths":{
  "/fal-ai/veo3.1/fast/image-to-video/requests/{request_id}/status":{"get":{}},
  "/fal-ai/veo3.1/fast/image-to-video":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Veo31FastImageToVideoInput"}}}}}}},
 "components":{"schemas":{"Veo31FastImageToVideoInput":{"type":"object","required":["prompt","image_url"],"properties":{
  "prompt":{"type":"string","maxLength":20000},
  "image_url":{"type":"string"},
  "duration":{"type":"string","enum":["4s","6s","8s"],"default":"8s"},
  "aspect_ratio":{"type":"string","enum":["auto","16:9","9:16"],"default":"auto"},
  "resolution":{"type":"string","enum":["720p","1080p","4k"],"default":"720p"},
  "negative_prompt":{"anyOf":[{"type":"string"},{"type":"null"}]},
  "generate_audio":{"type":"boolean","default":true}}}}}}}]}`

// A Replicate model answer: enums behind allOf/$ref, integer enums.
const replicateDoc = `{"latest_version":{"id":"v1","openapi_schema":{"components":{"schemas":{
 "Input":{"type":"object","required":["prompt","start_image"],"properties":{
  "prompt":{"type":"string"},
  "start_image":{"type":"string","format":"uri"},
  "duration":{"allOf":[{"$ref":"#/components/schemas/duration"}],"default":5},
  "aspect_ratio":{"allOf":[{"$ref":"#/components/schemas/aspect_ratio"}],"default":"16:9"},
  "reference_images":{"type":"array","items":{"type":"string","format":"uri"},"maxItems":4}}},
 "duration":{"type":"integer","enum":[5,10]},
 "aspect_ratio":{"type":"string","enum":["16:9","9:16","1:1"]}}}}}}`

func falDocSchema(t *testing.T) *inputSchema {
	t.Helper()
	var doc struct {
		Models []struct {
			OpenAPI map[string]any `json:"openapi"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(falVeoDoc), &doc); err != nil {
		t.Fatal(err)
	}
	s := openAPIInput(doc.Models[0].OpenAPI, "/fal-ai/veo3.1/fast/image-to-video")
	if s == nil {
		t.Fatal("no schema")
	}
	return s
}

func TestParseFalOpenAPI(t *testing.T) {
	s := falDocSchema(t)
	if !s.live || !slices.Equal(s.required, []string{"prompt", "image_url"}) {
		t.Errorf("required = %v", s.required)
	}
	if p := s.props["negative_prompt"]; p.kind != "string" {
		t.Errorf("anyOf null branch: %+v", p)
	}
	in, used, missing := s.build(inputs{prompt: "go", start: "https://x/f.jpg", seconds: 5, aspect: "9:16", resolution: "1080p"})
	want := map[string]any{"prompt": "go", "image_url": "https://x/f.jpg", "duration": "6s", "aspect_ratio": "9:16", "resolution": "1080p"}
	if !reflect.DeepEqual(in, want) || used != 0 || len(missing) != 0 {
		t.Errorf("build = %v used %d missing %v", in, used, missing)
	}
	caps := s.videoCaps()
	if !slices.Equal(caps.Lengths, []int{4, 6, 8}) || !caps.StartFrame || caps.Components || !slices.Equal(caps.Aspects, []string{"16:9", "9:16"}) {
		t.Errorf("caps = %+v", caps)
	}
}

func TestParseReplicateSchema(t *testing.T) {
	var doc struct {
		Latest struct {
			Schema map[string]any `json:"openapi_schema"`
		} `json:"latest_version"`
	}
	if err := json.Unmarshal([]byte(replicateDoc), &doc); err != nil {
		t.Fatal(err)
	}
	comps := doc.Latest.Schema["components"].(map[string]any)["schemas"].(map[string]any)
	s := parseSchema(comps["Input"].(map[string]any), comps)
	if s == nil {
		t.Fatal("nil schema")
	}
	if p := s.props["duration"]; p.kind != "integer" || len(p.enum) != 2 || p.def != float64(5) {
		t.Errorf("duration = %+v", p)
	}
	in, _, missing := s.build(inputs{prompt: "p", start: "data:image/jpeg;base64,AA", seconds: 8, aspect: "4:3"})
	if in["duration"] != float64(10) || in["start_image"] != "data:image/jpeg;base64,AA" || in["aspect_ratio"] != "16:9" || len(missing) != 0 {
		t.Errorf("build = %v missing %v", in, missing)
	}
	// components: refs capped at maxItems, the start frame is not sent
	refs := []string{"a", "b", "c", "d", "e"}
	in, used, missing := s.build(inputs{prompt: "p", start: "s", refs: refs, components: true, seconds: 5})
	if !reflect.DeepEqual(in["reference_images"], refs[:4]) || used != 4 || in["start_image"] != nil {
		t.Errorf("components = %v used %d", in, used)
	}
	if !slices.Equal(missing, []string{"start_image"}) {
		t.Errorf("missing = %v", missing)
	}
	caps := s.videoCaps()
	if !slices.Equal(caps.Lengths, []int{5, 10}) || !caps.Components || caps.MaxRefs != 4 {
		t.Errorf("caps = %+v", caps)
	}
}

func TestBuildFieldVariants(t *testing.T) {
	// Kling v3: start_image_url, duration "3"…"15"
	in, _, _ := falVideoSchema("fal-ai/kling-video/v3/pro/image-to-video").build(inputs{prompt: "p", start: "u", seconds: 7})
	if in["start_image_url"] != "u" || in["duration"] != "7" || in["image_url"] != nil {
		t.Errorf("kling v3 = %v", in)
	}
	// Wan 2.7: integer enum
	in, _, _ = falVideoSchema("fal-ai/wan/v2.7/image-to-video").build(inputs{prompt: "p", start: "u", seconds: 9, resolution: "4K"})
	if in["duration"] != float64(9) || in["resolution"] != "1080p" {
		t.Errorf("wan 2.7 = %v", in)
	}
	// MiniMax H3 on fal: integer range, P-suffixed resolutions
	in, _, _ = falVideoSchema("minimax/h3/image-to-video").build(inputs{prompt: "p", start: "u", seconds: 3, resolution: "720p"})
	if in["duration"] != 5 || in["resolution"] != "768P" {
		t.Errorf("h3 = %v", in)
	}
	// reference-to-video picked for a plain start frame: the frame becomes the only reference
	in, _, missing := falVideoSchema("fal-ai/veo3.1/reference-to-video").build(inputs{prompt: "p", start: "u", seconds: 6})
	if !reflect.DeepEqual(in["image_urls"], []string{"u"}) || in["duration"] != "6s" || len(missing) != 0 {
		t.Errorf("veo reference = %v %v", in, missing)
	}
	// Seedream edit: image_size presets instead of aspect_ratio
	in, used, _ := falImageSchema("fal-ai/bytedance/seedream/v4.5/edit").build(inputs{image: true, prompt: "p", refs: []string{"a", "b"}, aspect: "9:16"})
	if in["image_size"] != "portrait_16_9" || used != 2 || in["num_images"] != 1 {
		t.Errorf("seedream = %v", in)
	}
	// FLUX Kontext: one image only
	in, used, _ = falImageSchema("fal-ai/flux-pro/kontext").build(inputs{image: true, prompt: "p", refs: []string{"a", "b"}, aspect: "16:9"})
	if in["image_url"] != "a" || used != 1 || in["aspect_ratio"] != "16:9" {
		t.Errorf("kontext = %v", in)
	}
	// gpt-image-1 on Replicate needs the user's OpenAI key — reported, not guessed
	_, _, missing = replicateImageSchema("openai/gpt-image-1").build(inputs{image: true, prompt: "p"})
	if !slices.Equal(missing, []string{"openai_api_key"}) {
		t.Errorf("missing = %v", missing)
	}
}

func TestPickers(t *testing.T) {
	res := prop{kind: "string", enum: []any{"512P", "768P"}}
	if v, _ := pickResolution(res, "720p"); v != "768P" {
		t.Errorf("720p → %v", v)
	}
	if v, _ := pickResolution(prop{kind: "string", enum: []any{"768P", "2K"}}, "1080p"); v != "768P" {
		t.Errorf("1080p → %v", v)
	}
	if v, _ := pickResolution(prop{kind: "string", enum: []any{"720p", "1080p", "4k"}}, "4K"); v != "4k" {
		t.Errorf("4K → %v", v)
	}
	asp := prop{kind: "string", enum: []any{"auto", "21:9", "16:9", "1:1"}}
	for in, want := range map[string]string{"16:9": "16:9", "3:2": "16:9", "9:16": "auto", "1:1": "1:1"} {
		if v, _ := pickAspectValue(asp, in); v != want {
			t.Errorf("aspect %s → %v, want %s", in, v, want)
		}
	}
	if v, _ := pickDuration(prop{kind: "string", def: "8s"}, 6); v != "6s" {
		t.Errorf("default style: %v", v)
	}
	if v, _ := pickDuration(prop{kind: "string", enum: []any{"auto", "4", "5", "6"}}, 10); v != "6" {
		t.Errorf("enum clamp: %v", v)
	}
	if v, _ := pickDuration(prop{kind: "string", enum: []any{"5", "10"}}, 7); v != "5" {
		t.Errorf("nearest: %v", v)
	}
	if v, _ := pickDuration(prop{kind: "string", enum: []any{"6", "10"}}, 8); v != "10" {
		t.Errorf("tie → longer: %v", v)
	}
	if got := durationLengths(prop{kind: "integer", min: ptr(2.0), max: ptr(5.0)}); !slices.Equal(got, []int{2, 3, 4, 5}) {
		t.Errorf("range lengths = %v", got)
	}
}

func ptr(f float64) *float64 { return &f }

func TestSchemaCache(t *testing.T) {
	t.Cleanup(resetSchemas)
	resetSchemas()
	calls := 0
	load := func() (*inputSchema, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("down")
		}
		return schemaOf(fStr("prompt")), nil
	}
	if cachedSchema(context.Background(), "k", load) != nil || cachedSchema(context.Background(), "k", load) != nil || calls != 1 {
		t.Fatalf("a failure is remembered briefly: calls %d", calls)
	}
	old := schemaMissTTL
	schemaMissTTL = 0
	defer func() { schemaMissTTL = old }()
	time.Sleep(time.Millisecond)
	if cachedSchema(context.Background(), "k", load) == nil || cachedSchema(context.Background(), "k", load) == nil || calls != 2 {
		t.Fatalf("hit after retry: calls %d", calls)
	}
}

func resetSchemas() {
	schemaMu.Lock()
	schemaCache = map[string]schemaEntry{}
	schemaMu.Unlock()
}
