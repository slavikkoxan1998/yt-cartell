package codeanim

// The "map" grammar (grammars/map): an antique parchment world map for
// history shorts — flat region fills that spread and recolour, glowing
// rivers, dashed routes with travelling ships, pop-in icons, PNG character
// sprites at geographic points, thought bubbles with a mini-map and a rewind
// beat. Geography is Natural Earth 1:50m (public domain), simplified TopoJSON
// in world.js; historical regions and the gazetteer are in presets.js. See
// grammars/map/README.md for the spec reference.
//
// The file is named grammars_map.go (not grammar_map.go) on purpose: Go runs
// a package's init functions in file-name order, and the registry maps are
// created by the init in grammars.go, which must run first.

import (
	"embed"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
)

var (
	//go:embed grammars/map/world.js
	mapWorldJS []byte
	//go:embed grammars/map/presets.js
	mapPresetsJS []byte
	//go:embed grammars/map/map.js
	mapGrammarJS []byte
	//go:embed grammars/map/meta.json
	mapMetaJSON []byte
	//go:embed grammars/map/README.md
	mapReadme string
	//go:embed grammars/map/examples/*.json
	mapExamples embed.FS
)

// mapReference is the spec part of the README (from "## Spec" up to the
// images section, which is about hosts, not about writing a spec).
func mapReference() string {
	s := mapReadme
	if i := strings.Index(s, "## Spec"); i >= 0 {
		s = s[i:]
	}
	if i := strings.Index(s, "### Images"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// mapExampleSpecs are the README's example films, by file name.
func mapExampleSpecs() []json.RawMessage {
	names, _ := fs.Glob(mapExamples, "grammars/map/examples/*.json")
	sort.Strings(names)
	var out []json.RawMessage
	for _, n := range names {
		b, err := mapExamples.ReadFile(n)
		if err != nil || !json.Valid(b) {
			panic("codeanim: " + n + ": not JSON")
		}
		out = append(out, b)
	}
	return out
}

func init() {
	var meta Grammar
	if err := json.Unmarshal(mapMetaJSON, &meta); err != nil {
		panic("codeanim: grammars/map/meta.json: " + err.Error())
	}
	meta.Reference, meta.Examples = mapReference(), mapExampleSpecs()
	// One clip file: the data globals first, then CLIPS.map.
	js := make([]byte, 0, len(mapWorldJS)+len(mapPresetsJS)+len(mapGrammarJS)+2)
	js = append(js, mapWorldJS...)
	js = append(js, '\n')
	js = append(js, mapPresetsJS...)
	js = append(js, '\n')
	js = append(js, mapGrammarJS...)
	RegisterGrammar("map", js, meta)
}
