package core

import (
	"os"
	"path/filepath"
	"testing"
)

// webRoot is the repo folder of the web studio (…/YT-CARTELL-AI-MAKER).
const webRoot = "../../../.."

func TestDemoEngineAndShot(t *testing.T) {
	dir := t.TempDir()
	e, err := DemoEngine(filepath.Join(dir, "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := NewHeadless(e)
	defer c.Close()
	if _, err := os.Stat(filepath.Join(webRoot, "data", "studio")); err == nil && len(c.List()) == 0 {
		t.Fatal("demo engine imported no projects")
	}
	cfg := e.Config().Get()
	if cfg.TTS.APIKey != "demo-tts-key-0000" || len(cfg.Google.Keys) != 0 {
		t.Fatal("secrets not replaced")
	}
	out := filepath.Join(dir, "p.png")
	if err := Shot(c, h, 800, 500, 1, out, func(gtx C) D { return Placeholder(gtx, c, "Тест") }); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatal("no screenshot")
	}
	for _, s := range c.List() {
		if pv := c.Project(s.ID); pv == nil || pv.P.ID != s.ID {
			t.Fatalf("project %s: no snapshot", s.ID)
		}
	}
}
