package assets_test

import (
	"bytes"
	"image/jpeg"
	"strings"
	"testing"

	"ytcartell/internal/assets"
	"ytcartell/internal/studio"
)

// Every built-in style ships its gallery picture: a 640×480 (4:3) JPEG.
func TestEveryStyleHasPreview(t *testing.T) {
	for _, s := range studio.Styles {
		b := assets.StylePreview(s.ID)
		if len(b) == 0 {
			t.Errorf("%s: no embedded preview (styles/%s.jpg)", s.ID, s.ID)
			continue
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Errorf("%s: preview is not a JPEG: %v", s.ID, err)
			continue
		}
		if cfg.Width != 640 || cfg.Height != 480 {
			t.Errorf("%s: preview is %d×%d, want 640×480", s.ID, cfg.Width, cfg.Height)
		}
	}
	if assets.StylePreview("no-such-style") != nil {
		t.Error("preview for an unknown id")
	}
}

// No preview outlives its style (the gallery would never show it).
func TestNoStalePreviews(t *testing.T) {
	entries, err := assets.FS.ReadDir("styles")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".jpg")
		if _, ok := studio.StyleByID(id); !ok {
			t.Errorf("styles/%s: no built-in style %q", e.Name(), id)
		}
	}
	if len(entries) != len(studio.Styles) {
		t.Errorf("%d previews for %d styles", len(entries), len(studio.Styles))
	}
}
