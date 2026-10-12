package catalog_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"ytcartell/internal/providers"
	"ytcartell/internal/tts"
	"ytcartell/internal/tts/catalog"
)

func known(id string) bool { _, ok := tts.ProviderByID(id); return ok }

// TestEmbedded: every shipped entry is complete, and its provider and model
// are the studio's.
func TestEmbedded(t *testing.T) {
	d := catalog.Embedded()
	for _, p := range catalog.Check(d, known) {
		t.Error(p)
	}
	uk, ok := 0, 0
	for _, e := range d.Entries {
		if e.Ukrainian {
			uk++
		}
		if e.Supported(known) {
			ok++
		}
	}
	t.Logf("tts %d entries · %d speak Ukrainian · %d work in the studio today", len(d.Entries), uk, ok)
}

func TestLoad(t *testing.T) {
	fsys := fstest.MapFS{
		"data/a.json": {Data: []byte(`{"list":"tts","checked":"2026-10-05","entries":[
			{"id":"openai:tts","kind":"model","label":"A","vendor":"OpenAI","provider":"openai","protocol":"openai","model":"gpt-4o-mini-tts","source":"https://x.example/a","popular":1},
			{"id":"x:rest","kind":"model","label":"B","vendor":"X","provider":"","protocol":"rest","baseUrl":"https://api.x.example","source":"https://x.example/b","popular":3,"ukrainian":true}]}`)},
		"data/b.json": {Data: []byte(`{"list":"tts","entries":[{"id":"z","nope":true}]}`)},
	}
	d := catalog.Load(fsys)
	if len(d.Entries) != 2 || d.Entries[0].ID != "x:rest" {
		t.Fatalf("entries = %+v", d.Entries)
	}
	if len(d.Problems) != 1 || !strings.Contains(d.Problems[0], "b.json") {
		t.Errorf("problems = %q", d.Problems)
	}
	if p := catalog.Check(d, known); len(p) != 1 {
		t.Errorf("check = %q", p)
	}
	if d.Entries[0].Supported(known) || !d.Entries[1].Supported(known) {
		t.Error("Supported: rest is not, openai is")
	}
	d.Entries[1].Provider = "nobody"
	d.Entries[0].Kind = "voice"
	if p := catalog.Check(d, known); len(p) != 3 {
		t.Errorf("check = %q (unknown provider, voice without id)", p)
	}
}

// TestSpecs: every spec parses as a voice-over spec of the universal adapter.
func TestSpecs(t *testing.T) {
	n := 0
	for _, e := range catalog.Embedded().Entries {
		text := e.SpecText()
		if text == "" {
			continue
		}
		n++
		if _, err := providers.ParseSpecFor(text, providers.KindTTS); err != nil {
			t.Errorf("%s: spec: %v", e.ID, err)
			continue
		}
		if _, warns, err := providers.CheckSpecFor(text, providers.KindTTS); err != nil {
			t.Errorf("%s: spec check: %v", e.ID, err)
		} else {
			for _, w := range warns {
				t.Logf("%s: %s", e.ID, w)
			}
		}
	}
	t.Logf("%d entries run through the universal adapter", n)
}
