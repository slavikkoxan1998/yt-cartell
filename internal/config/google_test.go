package config

import (
	"encoding/json"
	"testing"
	"time"
)

func pinNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

var (
	beforeShutdown = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	afterShutdown  = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC) // 01:00 PDT on the 22nd
)

// loadJSON mimics Load: defaults, the stored JSON over them, Normalize.
func loadJSON(t *testing.T, raw string) Config {
	t.Helper()
	c := Default()
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	c.Normalize()
	return c
}

func TestGoogleVideoDefaultAndMigration(t *testing.T) {
	pinNow(t, beforeShutdown)

	d := Default()
	d.Normalize()
	if d.Google.VeoModel != GoogleOmniModel || !d.Google.OmniMigrated {
		t.Fatalf("fresh install: %+v", d.Google)
	}

	// A config saved by an older version names the old default: moved to Omni once.
	c := loadJSON(t, `{"google":{"keys":["k"],"veoModel":"veo-3.1-generate-preview"},"video":{"provider":"google","length":8}}`)
	if c.Google.VeoModel != GoogleOmniModel || !c.Google.OmniMigrated {
		t.Fatalf("legacy config: %+v", c.Google)
	}
	// …after which the user may pick Veo again until the shutdown.
	c.Google.VeoModel = "veo-3.1-fast-generate-preview"
	c.Normalize()
	if c.Google.VeoModel != "veo-3.1-fast-generate-preview" {
		t.Fatalf("chosen Veo must stay before the shutdown: %q", c.Google.VeoModel)
	}
	b, _ := json.Marshal(c)
	if again := loadJSON(t, string(b)); again.Google.VeoModel != "veo-3.1-fast-generate-preview" {
		t.Fatalf("round trip: %q", again.Google.VeoModel)
	}

	// Shut-down models always move to Omni; other ids stay.
	for model, want := range map[string]string{
		"veo-3.0-generate-001": GoogleOmniModel, "veo-2.0-generate-001": GoogleOmniModel, "": GoogleOmniModel,
		"gemini-omni-flash-preview": "gemini-omni-flash-preview", "veo-3.1-generate-001": "veo-3.1-generate-001",
	} {
		c := Default()
		c.Google.OmniMigrated, c.Google.VeoModel = true, model
		c.Normalize()
		if c.Google.VeoModel != want {
			t.Errorf("%q → %q, want %q", model, c.Google.VeoModel, want)
		}
	}

	// From the shutdown on, the Veo 3.1 previews migrate too.
	pinNow(t, afterShutdown)
	c.Google.OmniMigrated, c.Google.VeoModel = true, "veo-3.1-lite-generate-preview"
	c.Normalize()
	if c.Google.VeoModel != GoogleOmniModel {
		t.Fatalf("after the shutdown: %q", c.Google.VeoModel)
	}
}

// Google Omni keeps 10 s clips; Google Veo snaps them to 8 s.
func TestGoogleVideoLengths(t *testing.T) {
	pinNow(t, beforeShutdown)
	c := Default()
	c.Video.Provider, c.Video.Length = "google", 10
	c.Normalize()
	if c.Video.Length != 10 {
		t.Fatalf("omni length = %d", c.Video.Length)
	}
	c.Google.VeoModel = "veo-3.1-generate-preview"
	c.Normalize()
	if c.Video.Length != 8 {
		t.Fatalf("veo length = %d", c.Video.Length)
	}
}

func TestGoogleVideoModels(t *testing.T) {
	if n := len(GoogleVideoModels(beforeShutdown)); n != 4 {
		t.Fatalf("before the shutdown: %d models", n)
	}
	after := GoogleVideoModels(afterShutdown)
	if len(after) != 1 || after[0].ID != GoogleOmniModel {
		t.Fatalf("after the shutdown: %+v", after)
	}
	// The shutdown is midnight Pacific: 06:59 UTC on the 22nd is still the 21st there.
	if VeoRetired("veo-3.1-generate-preview", time.Date(2026, 10, 22, 6, 59, 0, 0, time.UTC)) ||
		!VeoRetired("veo-3.1-generate-preview", time.Date(2026, 10, 22, 7, 0, 0, 0, time.UTC)) {
		t.Fatal("shutdown instant")
	}
	for model, want := range map[string]bool{"gemini-omni-1.1-flash": true, "models/gemini-omni-flash-preview": true, "veo-3.1-generate-preview": false, "": false} {
		if GoogleOmni(model) != want {
			t.Errorf("GoogleOmni(%q) != %v", model, want)
		}
	}
	pinNow(t, beforeShutdown)
	if !GoogleRendersOmni("") || !GoogleRendersOmni("veo-3.0-generate-001") || GoogleRendersOmni("veo-3.1-generate-preview") {
		t.Fatal("GoogleRendersOmni")
	}
}
