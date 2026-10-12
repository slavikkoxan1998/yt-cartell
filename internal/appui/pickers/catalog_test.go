package pickers

import "testing"

func TestCatalogMatch(t *testing.T) {
	it := CatalogItem{ID: "fal:kling", Title: "Kling 3.0 Pro", Sub: "fal.ai · Kuaishou",
		Tags: []string{"host=fal", "mode=i2v", "mode=t2v", "speech", "audio"}, Search: "kling 3.0 pro fal-ai/kling-video/v3"}
	for _, tc := range []struct {
		name  string
		sel   map[string]string
		on    map[string]bool
		words []string
		skip  string
		want  bool
	}{
		{"nothing", nil, nil, nil, "", true},
		{"host", map[string]string{"host": "fal"}, nil, nil, "", true},
		{"other host", map[string]string{"host": "replicate"}, nil, nil, "", false},
		{"other host, its group skipped (counts)", map[string]string{"host": "replicate"}, nil, nil, "host", true},
		{"all hosts", map[string]string{"host": ""}, nil, nil, "", true},
		{"mode + toggle", map[string]string{"mode": "t2v"}, map[string]bool{"speech": true}, nil, "", true},
		{"missing toggle", nil, map[string]bool{"refs": true}, nil, "", false},
		{"toggle off", nil, map[string]bool{"refs": false}, nil, "", true},
		{"words in title and sub", nil, nil, []string{"kling", "kuaishou"}, "", true},
		{"word in the model id", nil, nil, []string{"v3"}, "", true},
		{"missing word", nil, nil, []string{"kling", "veo"}, "", false},
	} {
		if got := CatalogMatch(it, tc.sel, tc.on, tc.words, tc.skip); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCatalogState(t *testing.T) {
	var s Catalog
	if _, ok := s.Changed(); ok {
		t.Fatal("a pick before any")
	}
	s.SetFilter("host", "fal")
	s.SetToggle("speech", true)
	if s.sel["host"] != "fal" || !s.on["speech"] {
		t.Errorf("filters: %v %v", s.sel, s.on)
	}
	s.Open()
	if !s.IsOpen() || s.limit != catalogPage {
		t.Errorf("open: %v, limit %d", s.IsOpen(), s.limit)
	}
	s.picked = catalogPick{id: "x", set: true}
	if id, ok := s.Changed(); !ok || id != "x" {
		t.Errorf("Changed = %q, %v", id, ok)
	}
	if _, ok := s.Changed(); ok {
		t.Error("Changed reported twice")
	}
}
