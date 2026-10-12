package studio

// What the studio guarantees in every map spec the text model writes
// (acceptSpec; a spec the user edited is left alone):
//
//	motion   at least one action cue besides the camera — fill / recolor /
//	         drawRoute / moveAlong / pop / sprite / bubble (and their
//	         aliases) — and a camera move. A spec without them is not
//	         failed: a tasteful default is added (a sprite bounce, else a
//	         pulsing glow on the focus region or line; a slow push-in
//	         {"zoomBy": 1.1} over the clip).
//	sprites  figures are big (the grammar's default 0.37 of the short side
//	         in portrait ≈ 20 % of the frame height, 0.32 landscape): a
//	         scale under mapSpriteFloor is dropped so the default applies.

import (
	"math"
	"slices"
)

// mapActionKinds are the map cue kinds (with the grammar's aliases) that
// move something on the map besides the camera.
var mapActionKinds = []string{"fill", "spread", "recolor", "drawRoute", "route", "river", "draw", "moveAlong", "move", "travel",
	"pop", "icon", "year", "badge", "sprite", "character", "char", "bubble", "think"}

// mapCameraKinds are the map's camera cue kinds.
var mapCameraKinds = []string{"camera", "view", "cam"}

// mapPushIn is the gentle camera move added to a map spec without one.
const mapPushIn = 1.1

// mapSpriteFloor is the smallest sprite scale the studio keeps (portrait,
// landscape); below it the grammar's default size is used.
func mapSpriteFloor(portrait bool) float64 {
	if portrait {
		return 0.3
	}
	return 0.26
}

// ensureMapMotion adds the default action and camera move a map spec lacks;
// at is when the action lands (the middle of the voice). It returns what was
// added (for the log). allowed says which cue kinds the grammar draws (nil =
// all).
func ensureMapMotion(s codeSpec, at float64, allowed func(kind string) bool) []string {
	if allowed == nil {
		allowed = func(string) bool { return true }
	}
	dur, _ := num(s["duration"])
	cues := cuesOf(s)
	hasAction, hasCamera, hasGlow := false, false, false
	refs := map[string]int{}
	for _, c := range cues {
		k := jsString(c["kind"])
		switch {
		case slices.Contains(mapActionKinds, k):
			hasAction = true
		case slices.Contains(mapCameraKinds, k):
			hasCamera = true
		case k == "glow" || k == "highlight":
			hasGlow = true
		}
		for _, f := range []string{"region", "target"} {
			if id, ok := c[f].(string); ok {
				refs[id]++
			}
		}
	}
	data, _ := s["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
		s["data"] = data
	}
	if dur > 0 {
		at = math.Max(0.3, math.Min(at, dur-0.8))
	}
	at = round2(math.Max(0, at))
	var added []string
	add := func(c map[string]any, what string) {
		if !allowed(jsString(c["kind"])) {
			return
		}
		s["cues"] = append(asArray(s["cues"]), c)
		added = append(added, what)
	}
	if !hasAction {
		if id := visibleID(data["sprites"], nil); id != "" {
			add(map[string]any{"at": at, "kind": "sprite", "sprite": id, "action": "bounce", "count": 2}, "sprite bounce ("+id+")")
		} else if !hasGlow {
			if id := visibleID(data["regions"], refs); id != "" {
				add(map[string]any{"at": at, "kind": "glow", "region": id, "pulse": true}, "glow ("+id+")")
			} else if id := visibleID(append(slices.Clone(asArray(data["rivers"])), asArray(data["routes"])...), refs); id != "" {
				add(map[string]any{"at": at, "kind": "glow", "target": id, "pulse": true}, "glow ("+id+")")
			}
		}
	}
	if !hasCamera && dur > 0.5 {
		add(map[string]any{"at": 0.0, "kind": "camera", "view": map[string]any{"zoomBy": mapPushIn}, "dur": round2(dur - 0.1),
			"ease": "sine", "arc": false}, "push-in")
	}
	return added
}

// visibleID is the id of the element of list the scene is about: the one
// the cues name most, else the last one (drawn on top) shown from the start.
func visibleID(list any, refs map[string]int) string {
	best, bestN := "", -1
	for _, v := range asArray(list) {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		id := jsString(m["id"])
		if id == "" || m["show"] == false {
			continue
		}
		if n := refs[id]; n >= bestN {
			best, bestN = id, n
		}
	}
	return best
}

// fixMapSprites drops sprite scales under the floor (the grammar's default
// then applies): figures are meant to be big.
func fixMapSprites(s codeSpec, portrait bool) {
	floor := mapSpriteFloor(portrait)
	small := func(m map[string]any) {
		for _, k := range []string{"scale", "size"} {
			if v, ok := num(m[k]); ok && v < floor {
				delete(m, k)
			}
		}
	}
	if data, ok := s["data"].(map[string]any); ok {
		for _, v := range asArray(data["sprites"]) {
			if m, ok := v.(map[string]any); ok {
				small(m)
			}
		}
	}
	for _, c := range cuesOf(s) {
		if k := jsString(c["kind"]); k == "sprite" || k == "character" || k == "char" {
			small(c)
		}
	}
}
