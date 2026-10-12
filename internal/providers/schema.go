package providers

// Model input schemas. fal.ai and Replicate publish every model's input as an
// OpenAPI schema, and field names differ wildly between models (image_url vs
// start_image_url vs first_frame_image, duration "5" vs 5 vs "8s", …). The
// client reads the live schema (cached) and maps the studio's request onto
// it; when the schema cannot be read, a built-in one for the model family
// (models.go) is used. The same built-in schemas give the capabilities.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// prop is one input field, flattened from its OpenAPI schema.
type prop struct {
	kind     string // string | integer | number | boolean | array | object | "" (unknown)
	enum     []any  // allowed values (string or float64); nil = any
	min, max *float64
	maxItems int // arrays: 0 = unknown
	maxLen   int // strings: 0 = unknown
	def      any
}

// inputSchema is a model's input fields and which are required.
type inputSchema struct {
	props    map[string]prop
	required []string
	live     bool // read from the provider (false = built-in)
	// builtin (live schemas) is the model family's built-in schema: its
	// reference limit counts when the live one has no maxItems.
	builtin *inputSchema
}

// withBuiltin returns a copy of a live schema (cached ones are shared)
// that knows the built-in schema of its model.
func (s *inputSchema) withBuiltin(b *inputSchema) *inputSchema {
	if s == nil || !s.live {
		return s
	}
	c := *s
	c.builtin = b
	return &c
}

func (s *inputSchema) has(name string) bool {
	_, ok := s.props[name]
	return ok
}

// first returns the first of names the schema has.
func (s *inputSchema) first(names ...string) (string, prop, bool) {
	for _, n := range names {
		if p, ok := s.props[n]; ok {
			return n, p, true
		}
	}
	return "", prop{}, false
}

// ---- OpenAPI parsing ----------------------------------------------------------------

// parseSchema flattens the object schema node (whose $refs point into
// components) into an inputSchema. nil when it has no properties.
func parseSchema(node, components map[string]any) *inputSchema {
	obj := resolveRef(node, components)
	props, _ := obj["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	s := &inputSchema{props: map[string]prop{}, live: true}
	for name, raw := range props {
		if n, ok := raw.(map[string]any); ok {
			s.props[name] = flatten(n, components)
		}
	}
	if req, ok := obj["required"].([]any); ok {
		for _, r := range req {
			if name, ok := r.(string); ok {
				s.required = append(s.required, name)
			}
		}
	}
	return s
}

// resolveRef follows "$ref": "#/components/schemas/Name" links.
func resolveRef(n, components map[string]any) map[string]any {
	for i := 0; i < 8 && n != nil; i++ {
		ref, ok := n["$ref"].(string)
		if !ok {
			return n
		}
		next, ok := components[ref[strings.LastIndexByte(ref, '/')+1:]].(map[string]any)
		if !ok {
			return n
		}
		n = next
	}
	return n
}

// flatten merges a property node with its allOf/anyOf/oneOf branches (null
// branches skipped): first type, enum, bounds and default win. JSON-schema
// "const" values count as an enum (oneOf: [{const: "5"}, {const: "10"}] →
// enum ["5", "10"]) when no branch has a real one.
func flatten(n, components map[string]any) prop {
	var p prop
	var consts []any
	var walk func(n map[string]any, depth int)
	walk = func(n map[string]any, depth int) {
		if n == nil || depth > 6 {
			return
		}
		if d, ok := n["default"]; ok && p.def == nil {
			p.def = d
		}
		n = resolveRef(n, components)
		if d, ok := n["default"]; ok && p.def == nil {
			p.def = d
		}
		switch t := n["type"].(type) {
		case string:
			if t != "null" && p.kind == "" {
				p.kind = t
			}
		case []any:
			for _, x := range t {
				if s, ok := x.(string); ok && s != "null" && p.kind == "" {
					p.kind = s
				}
			}
		}
		if e, ok := n["enum"].([]any); ok && p.enum == nil {
			for _, v := range e {
				if v != nil {
					p.enum = append(p.enum, v)
				}
			}
		}
		switch c := n["const"].(type) { // scalars only (objects are not comparable)
		case string, float64, bool:
			if !slices.Contains(consts, any(c)) {
				consts = append(consts, c)
			}
		}
		if v, ok := n["minimum"].(float64); ok && p.min == nil {
			p.min = &v
		}
		if v, ok := n["maximum"].(float64); ok && p.max == nil {
			p.max = &v
		}
		if v, ok := n["maxItems"].(float64); ok && p.maxItems == 0 {
			p.maxItems = int(v)
		}
		if v, ok := n["maxLength"].(float64); ok && p.maxLen == 0 {
			p.maxLen = int(v)
		}
		if _, ok := n["items"]; ok && p.kind == "" {
			p.kind = "array"
		}
		for _, k := range []string{"allOf", "anyOf", "oneOf"} {
			if list, ok := n[k].([]any); ok {
				for _, b := range list {
					if bm, ok := b.(map[string]any); ok {
						walk(bm, depth+1)
					}
				}
			}
		}
	}
	walk(n, 0)
	if p.enum == nil && len(consts) > 0 {
		p.enum = consts
		if p.kind == "" {
			switch consts[0].(type) {
			case string:
				p.kind = "string"
			case float64:
				p.kind = "number"
			case bool:
				p.kind = "boolean"
			}
		}
	}
	return p
}

// ---- built-in schemas (a tiny DSL for models.go) ------------------------------------

type field struct {
	name     string
	p        prop
	required bool
}

func schemaOf(fields ...field) *inputSchema {
	s := &inputSchema{props: map[string]prop{}}
	for _, f := range fields {
		s.props[f.name] = f.p
		if f.required {
			s.required = append(s.required, f.name)
		}
	}
	return s
}

func fStr(name string) field { return field{name: name, p: prop{kind: "string"}} }
func fReq(f field) field     { f.required = true; return f }
func fArr(name string, max int) field {
	return field{name: name, p: prop{kind: "array", maxItems: max}}
}
func fInt(name string, lo, hi float64) field {
	return field{name: name, p: prop{kind: "integer", min: &lo, max: &hi}}
}
func fDef(f field, def any) field { f.p.def = def; return f }
func fEnumS(name string, vals ...string) field {
	e := make([]any, len(vals))
	for i, v := range vals {
		e[i] = v
	}
	return field{name: name, p: prop{kind: "string", enum: e}}
}
func fEnumI(name string, vals ...int) field {
	e := make([]any, len(vals))
	for i, v := range vals {
		e[i] = float64(v)
	}
	return field{name: name, p: prop{kind: "integer", enum: e}}
}

// secs is "lo".."hi" as strings (duration enums like Kling's "3"…"15").
func secs(lo, hi int, extra ...string) []string {
	out := append([]string(nil), extra...)
	for i := lo; i <= hi; i++ {
		out = append(out, strconv.Itoa(i))
	}
	return out
}

// ---- request → input ----------------------------------------------------------------

// Field names, most specific first.
var (
	promptFields     = []string{"prompt", "text", "prompt_text", "caption"}
	startFields      = []string{"start_image_url", "image_url", "first_frame_image", "first_frame_url", "first_frame", "start_image", "image", "input_image", "init_image"}
	imageRefArrays   = []string{"image_urls", "image_input", "input_images", "images", "reference_images", "reference_image_urls", "input_image_urls"}
	imageRefSingles  = []string{"image_url", "input_image", "image", "image_prompt"}
	videoRefArrays   = []string{"reference_images", "reference_image_urls", "image_urls", "images", "input_images", "image_input"}
	durationFields   = []string{"duration", "duration_seconds", "seconds", "video_length", "video_duration", "length"}
	aspectFields     = []string{"aspect_ratio", "ratio", "aspect"}
	resolutionFields = []string{"resolution"}
	countFields      = []string{"num_images", "number_of_images", "num_outputs"}
)

// inputs is what the studio asks for, in provider-neutral form.
type inputs struct {
	image      bool     // a still (else a clip)
	prompt     string   //
	start      string   // video start frame as URL / data URI ("" = none)
	refs       []string // references (URLs / data URIs) in priority order
	components bool     // video: refs are components (reference-to-video)
	seconds    int      // video length
	aspect     string   // "16:9", "9:16", …
	resolution string   // video: "720p" | "1080p" | "4K" | ""
}

// build maps in onto the schema. missing lists required fields it could not
// fill (the model needs something the studio does not have).
func (s *inputSchema) build(in inputs) (out map[string]any, usedRefs int, missing []string) {
	out = map[string]any{}
	name, p, ok := s.first(promptFields...)
	if !ok {
		name = "prompt"
	}
	out[name] = in.prompt
	if p.maxLen > 0 {
		out[name] = clipKeep(in.prompt, p.maxLen)
	}

	if in.image {
		usedRefs = s.putRefs(out, in.refs, imageRefArrays, imageRefSingles)
	} else {
		if in.components && len(in.refs) > 0 {
			usedRefs = s.putRefs(out, in.refs, videoRefArrays, nil)
		}
		if usedRefs == 0 && in.start != "" {
			if n, p, ok := s.first(startFields...); ok {
				if p.kind == "array" {
					out[n] = []string{in.start}
				} else {
					out[n] = in.start
				}
			} else if n, _, ok := s.first(videoRefArrays...); ok {
				out[n] = []string{in.start}
			}
		}
		if n, p, ok := s.first(durationFields...); ok && in.seconds > 0 {
			if v, ok := pickDuration(p, in.seconds); ok {
				out[n] = v
			}
		}
		if n, p, ok := s.first(resolutionFields...); ok && in.resolution != "" {
			if v, ok := pickResolution(p, in.resolution); ok {
				out[n] = v
			}
		}
	}
	if n, p, ok := s.first(aspectFields...); ok && in.aspect != "" {
		if v, ok := pickAspectValue(p, in.aspect); ok {
			out[n] = v
		}
	} else if p, ok := s.props["image_size"]; ok && in.image && in.aspect != "" {
		if v, ok := pickImageSize(p, in.aspect); ok {
			out["image_size"] = v
		}
	}
	if n, p, ok := s.first(countFields...); ok && (p.min == nil || *p.min <= 1) {
		out[n] = 1
	}
	for _, r := range s.required {
		if _, ok := out[r]; !ok && s.props[r].def == nil {
			missing = append(missing, r)
		}
	}
	return out, usedRefs, missing
}

// clipKeep cuts s to at most n runes (no ellipsis: the API counts them).
func clipKeep(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// putRefs writes refs into the first array field (capped by maxItems), else
// the first one into a single-image field. Returns how many were used.
func (s *inputSchema) putRefs(out map[string]any, refs, arrays, singles []string) int {
	if len(refs) == 0 {
		return 0
	}
	if n, p, ok := s.first(arrays...); ok && (p.kind == "array" || p.kind == "") {
		if p.maxItems > 0 && len(refs) > p.maxItems {
			refs = refs[:p.maxItems]
		}
		out[n] = refs
		return len(refs)
	}
	if n, _, ok := s.first(singles...); ok {
		out[n] = refs[0]
		return 1
	}
	return 0
}

// enumSeconds parses a duration enum value: "5", "8s", 6 → seconds; "auto"
// and other words → ok false.
func enumSeconds(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, x > 0
	case string:
		f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(x)), "s"), 64)
		return f, err == nil && f > 0
	}
	return 0, false
}

// pickDuration encodes sec for a duration field: the nearest enum value
// (ties → longer), a clamped number, or a string in the default's style
// ("8s" → "6s").
func pickDuration(p prop, sec int) (any, bool) {
	if len(p.enum) > 0 {
		var best any
		bestSec := 0.0
		for _, v := range p.enum {
			s, ok := enumSeconds(v)
			if !ok {
				continue
			}
			d, bd := math.Abs(s-float64(sec)), math.Abs(bestSec-float64(sec))
			if best == nil || d < bd || d == bd && s > bestSec {
				best, bestSec = v, s
			}
		}
		return best, best != nil
	}
	v := float64(sec)
	if p.min != nil && v < *p.min {
		v = *p.min
	}
	if p.max != nil && v > *p.max {
		v = *p.max
	}
	switch p.kind {
	case "integer":
		return int(math.Round(v)), true
	case "number":
		return v, true
	}
	if d, ok := p.def.(string); ok && strings.HasSuffix(strings.TrimSpace(d), "s") {
		return strconv.Itoa(int(v)) + "s", true
	}
	return strconv.Itoa(int(v)), true
}

// ratioOf parses "16:9" → 1.777…; false for words.
func ratioOf(s string) (float64, bool) {
	w, h, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, false
	}
	fw, err1 := strconv.ParseFloat(w, 64)
	fh, err2 := strconv.ParseFloat(h, 64)
	if err1 != nil || err2 != nil || fw <= 0 || fh <= 0 {
		return 0, false
	}
	return fw / fh, true
}

// pickAspectValue: the exact ratio when allowed, else the closest one (by
// log ratio), else a word like "auto"/"adaptive"; free strings take a as is.
func pickAspectValue(p prop, a string) (any, bool) {
	if len(p.enum) == 0 {
		if p.kind == "string" || p.kind == "" {
			return a, true
		}
		return nil, false
	}
	want, ok := ratioOf(a)
	var best string
	bestD := math.Inf(1)
	var word string
	for _, v := range p.enum {
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		if s == a {
			return s, true
		}
		r, isRatio := ratioOf(s)
		if !isRatio {
			if l := strings.ToLower(s); word == "" && (l == "auto" || l == "adaptive" || l == "match_input_image") {
				word = s
			}
			continue
		}
		if ok {
			if d := math.Abs(math.Log(r / want)); d < bestD {
				best, bestD = s, d
			}
		}
	}
	switch {
	case best != "" && bestD < 0.25:
		return best, true
	case word != "":
		return word, true
	case best != "":
		return best, true
	}
	return nil, false
}

// pickImageSize maps an aspect ratio to fal's image_size presets
// (landscape_16_9, portrait_16_9, landscape_4_3, portrait_4_3, square_hd).
func pickImageSize(p prop, a string) (any, bool) {
	want := map[string][]string{
		"16:9": {"landscape_16_9"}, "9:16": {"portrait_16_9"},
		"4:3": {"landscape_4_3"}, "3:4": {"portrait_4_3"},
		"3:2": {"landscape_4_3"}, "2:3": {"portrait_4_3"},
		"1:1": {"square_hd", "square"},
	}[a]
	for _, w := range append(want, "auto") {
		for _, v := range p.enum {
			if s, ok := v.(string); ok && s == w {
				return s, true
			}
		}
	}
	return nil, false
}

// resolutionHeight parses "720p", "768P", "2K", "4k", "1080p" → lines.
func resolutionHeight(s string) (float64, bool) {
	l := strings.ToLower(strings.TrimSpace(s))
	switch l {
	case "4k", "2160p":
		return 2160, true
	case "2k", "1440p":
		return 1440, true
	case "1k":
		return 1024, true
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(l, "p"), 64)
	return f, err == nil && f > 0
}

// pickResolution: the enum value nearest to the requested height (ties →
// lower, the cheaper one); free strings take it as is.
func pickResolution(p prop, want string) (any, bool) {
	wh, ok := resolutionHeight(want)
	if !ok {
		return nil, false
	}
	if len(p.enum) == 0 {
		if p.kind == "string" {
			return want, true
		}
		return nil, false
	}
	var best string
	bestD := math.Inf(1)
	bestH := 0.0
	for _, v := range p.enum {
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		h, ok := resolutionHeight(s)
		if !ok {
			continue
		}
		if d := math.Abs(h - wh); d < bestD || d == bestD && h < bestH {
			best, bestD, bestH = s, d, h
		}
	}
	return best, best != ""
}

// ---- capabilities from a schema -----------------------------------------------------

var commonAspects = []string{"16:9", "9:16", "1:1", "4:3", "3:4"}

// videoCaps derives what a video model accepts from its input schema.
func (s *inputSchema) videoCaps() VideoCaps {
	var c VideoCaps
	if _, p, ok := s.first(durationFields...); ok {
		c.Lengths = durationLengths(p)
	}
	_, _, c.StartFrame = s.first(startFields...)
	if _, p, ok := s.first(videoRefArrays...); ok && (p.kind == "array" || p.kind == "") {
		c.Components = true
		c.MaxRefs = p.maxItems
		if c.MaxRefs == 0 && s.builtin != nil { // the live schema has no maxItems: the model's known limit (Kling O1: 7)
			c.MaxRefs = s.builtin.videoCaps().MaxRefs
		}
		if c.MaxRefs == 0 {
			c.MaxRefs = 3
		}
	}
	if _, p, ok := s.first(aspectFields...); ok && len(p.enum) > 0 {
		for _, v := range p.enum {
			if a, ok := v.(string); ok {
				if _, isRatio := ratioOf(a); isRatio {
					c.Aspects = append(c.Aspects, a)
				}
			}
		}
	}
	if len(c.Aspects) == 0 { // the clip follows the start frame
		c.Aspects = slices.Clone(commonAspects)
	}
	return c
}

// durationLengths lists the whole-second lengths a duration field allows.
func durationLengths(p prop) []int {
	var out []int
	if len(p.enum) > 0 {
		for _, v := range p.enum {
			if s, ok := enumSeconds(v); ok && s == math.Trunc(s) {
				out = append(out, int(s))
			}
		}
	} else if p.min != nil && p.max != nil && *p.max-*p.min <= 60 {
		for i := int(math.Ceil(max(1, *p.min))); i <= int(*p.max); i++ {
			out = append(out, i)
		}
	} else if s, ok := enumSeconds(p.def); ok {
		out = append(out, int(s))
	}
	sort.Ints(out)
	return slices.Compact(out)
}

// imageCaps derives what an image model accepts from its input schema.
func (s *inputSchema) imageCaps() ImageCaps {
	var c ImageCaps
	if _, p, ok := s.first(imageRefArrays...); ok && (p.kind == "array" || p.kind == "") {
		c.MaxRefs = p.maxItems
		if c.MaxRefs == 0 && s.builtin != nil {
			c.MaxRefs = s.builtin.imageCaps().MaxRefs
		}
		if c.MaxRefs == 0 {
			c.MaxRefs = 10
		}
	} else if _, _, ok := s.first(imageRefSingles...); ok {
		c.MaxRefs = 1
	}
	if _, p, ok := s.first(aspectFields...); ok && len(p.enum) > 0 {
		for _, v := range p.enum {
			if a, ok := v.(string); ok {
				if _, isRatio := ratioOf(a); isRatio {
					c.Aspects = append(c.Aspects, a)
				}
			}
		}
	}
	if len(c.Aspects) == 0 {
		c.Aspects = slices.Clone(commonAspects)
	}
	return c
}

// ---- schema cache -------------------------------------------------------------------

var (
	schemaTTL     = 6 * time.Hour
	schemaMissTTL = 3 * time.Minute

	schemaMu    sync.Mutex
	schemaCache = map[string]schemaEntry{}
)

type schemaEntry struct {
	s  *inputSchema // nil = could not be read
	at time.Time
}

// cachedSchema returns the cached schema for key, calling load on a miss
// (failures are remembered briefly so a down catalogue does not slow every
// generation; a failure because ctx ended is not remembered).
func cachedSchema(ctx context.Context, key string, load func() (*inputSchema, error)) *inputSchema {
	schemaMu.Lock()
	e, ok := schemaCache[key]
	schemaMu.Unlock()
	if ok && (e.s != nil && time.Since(e.at) < schemaTTL || e.s == nil && time.Since(e.at) < schemaMissTTL) {
		return e.s
	}
	s, err := load()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		s = nil
	}
	schemaMu.Lock()
	schemaCache[key] = schemaEntry{s: s, at: time.Now()}
	schemaMu.Unlock()
	return s
}

// decodeJSONMap unmarshals b into a generic map.
func decodeJSONMap(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, fmt.Errorf("empty JSON")
	}
	return m, nil
}
