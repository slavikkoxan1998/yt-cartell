package dialogs

import (
	"strings"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/studio"
)

// The remake sheet's screenshot states join TestShots (shots_test.go).
func init() {
	withKeys := func(c *core.Ctx) {
		_ = c.E.Config().Update(func(cf *config.Config) { cf.Google.Keys = []string{"demo-google-key"} })
	}
	shotStates = append(shotStates,
		shotState{name: "remake-open", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetRemake} },
			setup: func(c *core.Ctx, s *Sheets) { withKeys(c) }},
		shotState{name: "remake-filled", sheet: func(*core.Ctx) core.Sheet {
			return core.Sheet{Kind: core.SheetRemake, Text: "Та сама подача, але про мою кав'ярню; героїня — бариста років 30, українською."}
		}, setup: func(c *core.Ctx, s *Sheets) {
			withKeys(c)
			s.remake.link.SetText("https://youtube.com/shorts/dQw4w9WgXcQ")
			s.remake.fidelity = studio.FidelityExact
			s.remake.keep = false
			s.remake.seconds = 60
		}},
		shotState{name: "remake-map", sheet: func(*core.Ctx) core.Sheet {
			return core.Sheet{Kind: core.SheetRemake, Text: "Та сама історія, українською."}
		}, setup: func(c *core.Ctx, s *Sheets) {
			withKeys(c)
			s.remake.link.SetText("https://youtube.com/shorts/dQw4w9WgXcQ")
			s.remake.fidelity = studio.FidelityExact
			s.remake.profileID = mapProfile(c)
		}, scroll: 700},
		shotState{name: "remake-nokey", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetRemake} },
			setup: func(c *core.Ctx, s *Sheets) {
				_ = c.E.Config().Update(func(cf *config.Config) { cf.Google.Keys = nil; cf.Remake.Analysis = config.RemakeGoogle })
				s.remake.link.SetText("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
			}, scroll: 5000},
		shotState{name: "remake-error", sheet: func(*core.Ctx) core.Sheet { return core.Sheet{Kind: core.SheetRemake} },
			setup: func(c *core.Ctx, s *Sheets) {
				withKeys(c)
				s.remake.link.SetText("https://youtu.be/dQw4w9WgXcQ")
				s.remake.err = "Це приватне відео — його бачить лише автор. Встав посилання на публічне відео."
			}, scroll: 5000},
	)
}

// TestRemakeSheet checks the link handling and that a demo context never
// starts a remake.
func TestRemakeSheet(t *testing.T) {
	c, h := demo(t)
	s := New()
	s.Open(c, core.Sheet{Kind: core.SheetRemake, Text: "  про каву  "})
	r := &s.remake
	if !r.isOpen() || r.fidelity != studio.FidelityClose || !r.keep || r.wish.Editor.Text() != "про каву" {
		t.Fatalf("opened: fidelity %q keep %v wish %q", r.fidelity, r.keep, r.wish.Editor.Text())
	}
	r.start(c, false)
	if r.linkErr == "" || r.busy != "" {
		t.Fatal("an empty link must be refused")
	}
	r.link.SetText("https://vimeo.com/123")
	r.start(c, false)
	if !strings.Contains(r.linkErr, "YouTube") {
		t.Fatalf("a non-YouTube link: %q", r.linkErr)
	}
	r.link.SetText("youtu.be/dQw4w9WgXcQ?si=abc")
	if r.videoID() != "dQw4w9WgXcQ" {
		t.Fatalf("id = %q", r.videoID())
	}
	before := len(c.List())
	r.start(c, true)
	h.Flush()
	if r.err != offlineText || len(c.List()) != before {
		t.Fatalf("demo context: err %q, projects %d → %d", r.err, before, len(c.List()))
	}
	// The new-project sheet hands its story over as the wish.
	s.Open(c, core.Sheet{Kind: core.SheetNewProject, Text: "Історія про каву"})
	if s.remake.isOpen() || !s.newProject.isOpen() {
		t.Fatal("sheets must replace each other")
	}
}
