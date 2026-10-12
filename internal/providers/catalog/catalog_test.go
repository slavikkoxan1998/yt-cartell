package catalog_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"ytcartell/internal/providers"
	"ytcartell/internal/providers/catalog"
)

// TestEmbedded: every shipped entry is complete and consistent.
func TestEmbedded(t *testing.T) {
	d := catalog.Embedded()
	for _, p := range catalog.Check(d) {
		t.Error(p)
	}
	t.Logf("video %d · image %d · text %d", len(d.Video), len(d.Image), len(d.Text))
}

// TestSpecs: every universal entry's spec parses and its caps say what the
// entry says.
func TestSpecs(t *testing.T) {
	d := catalog.Embedded()
	check := func(e catalog.Entry, video bool) providers.Caps {
		t.Helper()
		text := e.SpecText()
		if text == "" {
			return providers.Caps{}
		}
		if _, err := providers.ParseSpec(text); err != nil {
			t.Errorf("%s: spec: %v", e.ID, err)
			return providers.Caps{}
		}
		if _, warns, err := providers.CheckSpec(text, video); err != nil {
			t.Errorf("%s: spec check: %v", e.ID, err)
		} else {
			for _, w := range warns {
				t.Logf("%s: %s", e.ID, w)
			}
		}
		return providers.SpecCaps(text)
	}
	for _, e := range d.Video {
		c := check(e.Entry, true).Video
		if e.SpecText() == "" {
			continue
		}
		if len(e.Lengths) > 0 && !slices.Equal(c.Lengths, e.Lengths) {
			t.Errorf("%s: spec lengths %v, entry %v", e.ID, c.Lengths, e.Lengths)
		}
		if c.MaxRefs != e.MaxRefs && e.HasMode(catalog.ModeRef) {
			t.Errorf("%s: spec maxRefs %d, entry %d", e.ID, c.MaxRefs, e.MaxRefs)
		}
		if c.StartFrame != (e.HasMode(catalog.ModeI2V) || e.HasMode(catalog.ModeFirstLast)) {
			t.Errorf("%s: spec start frame %v, entry modes %v", e.ID, c.StartFrame, e.Modes)
		}
	}
	for _, e := range d.Image {
		c := check(e.Entry, false).Image
		if e.SpecText() != "" && c.MaxRefs != e.MaxRefs {
			t.Errorf("%s: spec maxRefs %d, entry %d", e.ID, c.MaxRefs, e.MaxRefs)
		}
	}
}

// TestPresetsExist: the presets the entries name are the studio's.
func TestPresetsExist(t *testing.T) {
	for _, id := range catalog.VideoPresets {
		if _, ok := providers.FindVideoPreset(id); !ok {
			t.Errorf("video preset %q is not a studio preset", id)
		}
	}
	for _, id := range catalog.ImagePresets {
		if _, ok := providers.FindImagePreset(id); !ok {
			t.Errorf("image preset %q is not a studio preset", id)
		}
	}
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"video/a.json": {Data: []byte(`{"list":"video","checked":"2026-10-05","entries":[
			{"id":"x:1","label":"One","vendor":"V","host":"x","hostLabel":"X","preset":"fal","model":"m1","modes":["t2v"],"lengths":[5,10],"aspects":["16:9"],"resolutions":[],"maxRefs":0,"audio":"none","promptMax":0,"source":"https://example.com/1","popular":0},
			{"id":"x:2","label":"Two","vendor":"V","host":"x","hostLabel":"X","preset":"fal","model":"m2","modes":["i2v"],"lengths":[],"aspects":[],"resolutions":[],"maxRefs":0,"audio":"speech","promptMax":0,"source":"https://example.com/2","popular":3,"checked":"2026-09-01"}]}`)},
		"video/b.json": {Data: []byte(`{"list":"video","entries":[{"id":"y","bogus":1}]}`)},
		"image/c.json": {Data: []byte("\xef\xbb\xbf" + `{"list":"image","checked":"2026-10-05","entries":[
			{"id":"i:1","label":"Img","vendor":"V","host":"x","hostLabel":"X","preset":"universal","baseUrl":"https://api.example.com","model":"m","spec":{"submit":{"path":"/gen","body":{"prompt":"{{prompt}}"}}},"modes":["t2i"],"maxRefs":0,"aspects":["1:1"],"sizes":[],"promptMax":0,"text":false,"source":"https://example.com/i","popular":1}]}`)},
		"text/t.json": {Data: []byte(`{"list":"video","entries":[]}`)},
	}
	d := catalog.Load(fsys)
	if len(d.Video) != 2 || d.Video[0].ID != "x:2" || d.Video[1].ID != "x:1" {
		t.Fatalf("video = %+v (want the popular one first)", d.Video)
	}
	if d.Video[1].Checked != "2026-10-05" || d.Video[0].Checked != "2026-09-01" {
		t.Errorf("checked = %q, %q", d.Video[1].Checked, d.Video[0].Checked)
	}
	if len(d.Image) != 1 || !strings.Contains(d.Image[0].SpecText(), "\n  \"submit\"") {
		t.Errorf("image = %+v / spec %q", d.Image, d.Image[0].SpecText())
	}
	if len(d.Problems) != 2 || !strings.Contains(d.Problems[0], "b.json") || !strings.Contains(d.Problems[1], "t.json") {
		t.Errorf("problems = %q (want the unknown field and the wrong list)", d.Problems)
	}
	if p := catalog.Check(d); len(p) != 2 {
		t.Errorf("check = %q (only the two file problems)", p)
	}
	d.Video[0].Modes = []string{"ref"}
	d.Video[0].Audio = "loud"
	if p := catalog.Check(d); len(p) != 4 {
		t.Errorf("check = %q (ref without maxRefs, bad audio)", p)
	}
}

func TestFind(t *testing.T) {
	for _, e := range catalog.Video() {
		got, ok := catalog.FindVideo(e.Preset, e.Model)
		if !ok || got.Preset != e.Preset || !strings.EqualFold(got.Model, e.Model) {
			t.Fatalf("FindVideo(%q, %q) = %v, %v", e.Preset, e.Model, got.ID, ok)
		}
	}
	for _, e := range catalog.Image() {
		if _, ok := catalog.FindImage(e.Preset, e.Model); !ok {
			t.Fatalf("FindImage(%q, %q) not found", e.Preset, e.Model)
		}
	}
	if _, ok := catalog.FindVideo("fal", "no/such-model"); ok {
		t.Error("an unknown model was found")
	}
}
