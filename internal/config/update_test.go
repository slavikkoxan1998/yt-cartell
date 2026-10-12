package config

import (
	"encoding/json"
	"testing"
)

// Settings files written before the updater existed keep the automatic
// check on; an explicit "autoUpdate": false stays off.
func TestAutoUpdateDefault(t *testing.T) {
	if !Default().UI.AutoUpdate {
		t.Fatal("AutoUpdate is off by default")
	}
	for _, c := range []struct {
		doc  string
		want bool
	}{
		{`{"ui":{"theme":"dark"}}`, true},
		{`{"ui":{"theme":"dark","autoUpdate":false}}`, false},
	} {
		cfg := Default()
		if err := json.Unmarshal([]byte(c.doc), &cfg); err != nil {
			t.Fatal(err)
		}
		cfg.Normalize()
		if cfg.UI.AutoUpdate != c.want {
			t.Errorf("%s → AutoUpdate %v", c.doc, cfg.UI.AutoUpdate)
		}
	}
}
