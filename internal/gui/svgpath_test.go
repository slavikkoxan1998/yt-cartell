package gui

import (
	"math"
	"strings"
	"testing"

	"gioui.org/f32"
)

func near(a, b f32.Point) bool {
	return math.Abs(float64(a.X-b.X)) < 1e-3 && math.Abs(float64(a.Y-b.Y)) < 1e-3
}

func mustPath(t *testing.T, d string) []subpath {
	t.Helper()
	p, err := parsePathData(d)
	if err != nil {
		t.Fatalf("parse %q: %v", d, err)
	}
	return p
}

// ends returns the end point of every segment of sp.
func ends(sp subpath) []f32.Point {
	var out []f32.Point
	for _, s := range sp.Segs {
		out = append(out, s.end())
	}
	return out
}

func TestPathNumbersAndImplicitLineTo(t *testing.T) {
	// "1.5.5" is 1.5 then .5; "-2e1" is −20; after M, pairs are implicit L.
	p := mustPath(t, "M1.5.5-2e1,3 4-5")
	if len(p) != 1 {
		t.Fatalf("subpaths = %d, want 1", len(p))
	}
	got := ends(p[0])
	want := []f32.Point{{X: 1.5, Y: 0.5}, {X: -20, Y: 3}, {X: 4, Y: -5}}
	if len(got) != len(want) {
		t.Fatalf("points %v, want %v", got, want)
	}
	for i := range want {
		if !near(got[i], want[i]) {
			t.Errorf("point %d = %v, want %v", i, got[i], want[i])
		}
	}
	if p[0].Segs[1].Op != segLine || p[0].Segs[2].Op != segLine {
		t.Errorf("implicit segments are not lines: %+v", p[0].Segs)
	}
}

func TestPathRelativeCommands(t *testing.T) {
	// Lucide "arrow-right" head: m12 5 7 7-7 7 (relative moveto, then implicit l).
	got := ends(mustPath(t, "m12 5 7 7-7 7")[0])
	want := []f32.Point{{X: 12, Y: 5}, {X: 19, Y: 12}, {X: 12, Y: 19}}
	for i := range want {
		if !near(got[i], want[i]) {
			t.Errorf("point %d = %v, want %v", i, got[i], want[i])
		}
	}
	got = ends(mustPath(t, "M5 12h14v2H3V1")[0])
	want = []f32.Point{{X: 5, Y: 12}, {X: 19, Y: 12}, {X: 19, Y: 14}, {X: 3, Y: 14}, {X: 3, Y: 1}}
	for i := range want {
		if !near(got[i], want[i]) {
			t.Errorf("H/V point %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestPathArcPackedFlags(t *testing.T) {
	// Flags packed against the next number: large=0, sweep=1, then x=1 y=1.
	p := mustPath(t, "M0 0a1 1 0 011 1")
	last := p[0].Segs[len(p[0].Segs)-1]
	if last.Op != segCube {
		t.Fatalf("arc became %v, want cubic", last.Op)
	}
	if !near(last.end(), f32.Pt(1, 1)) {
		t.Errorf("arc ends at %v, want (1,1)", last.end())
	}
	// The same with separators must give the same geometry.
	q := mustPath(t, "M0 0 a1 1 0 0 1 1 1")
	if len(q[0].Segs) != len(p[0].Segs) {
		t.Fatalf("packed and spaced arcs differ: %d vs %d segments", len(p[0].Segs), len(q[0].Segs))
	}
	for i := range p[0].Segs {
		for k := 0; k < 3; k++ {
			if !near(p[0].Segs[i].P[k], q[0].Segs[i].P[k]) {
				t.Errorf("segment %d point %d: %v vs %v", i, k, p[0].Segs[i].P[k], q[0].Segs[i].P[k])
			}
		}
	}
}

func TestPathArcGeometry(t *testing.T) {
	// A half circle of radius 5 from (0,0) to (10,0) with sweep=1 (clockwise
	// on screen) passes through the top point (5,-5) and splits into two
	// quarter-arc cubics.
	sp := mustPath(t, "M0 0A5 5 0 0 1 10 0")[0]
	if len(sp.Segs) != 3 {
		t.Fatalf("segments = %d, want move + 2 cubics", len(sp.Segs))
	}
	if !near(sp.Segs[1].end(), f32.Pt(5, -5)) {
		t.Errorf("quarter point = %v, want (5,-5)", sp.Segs[1].end())
	}
	if !near(sp.Segs[2].end(), f32.Pt(10, 0)) {
		t.Errorf("end = %v, want (10,0)", sp.Segs[2].end())
	}
	// sweep=0 goes the other way, through (5,5).
	sp = mustPath(t, "M0 0A5 5 0 0 0 10 0")[0]
	if !near(sp.Segs[1].end(), f32.Pt(5, 5)) {
		t.Errorf("sweep=0 quarter point = %v, want (5,5)", sp.Segs[1].end())
	}
	// Radii too small to span the endpoints are scaled up (same half circle).
	sp = mustPath(t, "M0 0A1 1 0 0 1 10 0")[0]
	if !near(sp.Segs[1].end(), f32.Pt(5, -5)) {
		t.Errorf("scaled arc quarter point = %v, want (5,-5)", sp.Segs[1].end())
	}
	// Cubic midpoint of a quarter arc stays on the circle (error < 0.1 %).
	s := sp.Segs[1]
	p0 := sp.Segs[0].end()
	mid := cubicAt(p0, s.P[0], s.P[1], s.P[2], 0.5)
	r := dist(mid, f32.Pt(5, 0))
	if math.Abs(float64(r-5)) > 0.005 {
		t.Errorf("arc midpoint radius = %v, want 5", r)
	}
	// A large-arc flag picks the long way round: 270° → 3 cubics.
	sp = mustPath(t, "M5 0A5 5 0 1 1 0 5")[0]
	if len(sp.Segs) != 1+3 {
		t.Errorf("large arc segments = %d, want 1+3", len(sp.Segs))
	}
	// Zero radii degrade to a line; zero-length arcs vanish.
	if sp := mustPath(t, "M0 0A0 3 0 0 1 4 4")[0]; sp.Segs[1].Op != segLine {
		t.Errorf("zero-radius arc = %v, want line", sp.Segs[1].Op)
	}
	if sp := mustPath(t, "M2 2A3 3 0 0 1 2 2")[0]; len(sp.Segs) != 1 {
		t.Errorf("zero-length arc kept %d segments", len(sp.Segs)-1)
	}
}

func cubicAt(p0, p1, p2, p3 f32.Point, t float32) f32.Point {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return f32.Pt(a*p0.X+b*p1.X+c*p2.X+d*p3.X, a*p0.Y+b*p1.Y+c*p2.Y+d*p3.Y)
}

func TestPathSmoothCurves(t *testing.T) {
	// S reflects the previous cubic's second control point about the pen.
	sp := mustPath(t, "M0 0C0 10 10 10 10 0S20 -10 20 0")[0]
	s := sp.Segs[2]
	if s.Op != segCube || !near(s.P[0], f32.Pt(10, -10)) {
		t.Errorf("S first control = %v, want (10,-10)", s.P[0])
	}
	// S without a previous cubic uses the pen as the first control point.
	sp = mustPath(t, "M3 4S5 6 7 8")[0]
	if !near(sp.Segs[1].P[0], f32.Pt(3, 4)) {
		t.Errorf("lone S control = %v, want (3,4)", sp.Segs[1].P[0])
	}
	// T reflects the previous quadratic control point.
	sp = mustPath(t, "M0 0Q5 10 10 0T20 0")[0]
	if !near(sp.Segs[2].P[0], f32.Pt(15, -10)) {
		t.Errorf("T control = %v, want (15,-10)", sp.Segs[2].P[0])
	}
	// Relative forms.
	sp = mustPath(t, "M0 0c0 10 10 10 10 0s10 -10 10 0")[0]
	if !near(sp.Segs[2].P[0], f32.Pt(10, -10)) || !near(sp.Segs[2].end(), f32.Pt(20, 0)) {
		t.Errorf("relative s = %+v", sp.Segs[2])
	}
}

func TestPathClose(t *testing.T) {
	p := mustPath(t, "M0 0L10 0L10 10ZL5 5M20 20l1 1z")
	if len(p) != 3 {
		t.Fatalf("subpaths = %d, want 3", len(p))
	}
	if !p[0].closed() || p[1].closed() || !p[2].closed() {
		t.Errorf("closed flags = %v %v %v", p[0].closed(), p[1].closed(), p[2].closed())
	}
	// Drawing after Z starts a new subpath at the closed one's start point.
	if !near(p[1].Segs[0].P[0], f32.Pt(0, 0)) || !near(p[1].Segs[1].end(), f32.Pt(5, 5)) {
		t.Errorf("subpath after Z = %+v", p[1].Segs)
	}
	// Relative commands after Z are relative to the subpath start.
	p = mustPath(t, "M4 4h4z m1 1h2")
	if !near(p[1].Segs[0].P[0], f32.Pt(5, 5)) {
		t.Errorf("m after z moved to %v, want (5,5)", p[1].Segs[0].P[0])
	}
}

func TestPathErrors(t *testing.T) {
	for _, d := range []string{
		"M1",                 // missing y
		"X1 2",               // unknown command
		"12 3",               // no command
		"M1 2Z 3 4",          // numbers after Z
		"M0 0a1 1 0 2 1 1 1", // bad arc flag
		"M0 0L1 -",           // dangling sign
	} {
		if _, err := parsePathData(d); err == nil {
			t.Errorf("parse %q: no error", d)
		}
	}
}

func TestSVGElements(t *testing.T) {
	sps, err := parseSVG(`<circle cx="12" cy="12" r="10" /><rect x="3" y="4" width="18" height="12" rx="2" />` +
		`<line x1="1" y1="2" x2="3" y2="4" /><polyline points="1 1, 5 5 9 1" /><polygon points="0,0 4,0 2,3" />` +
		`<ellipse cx="5" cy="5" rx="4" ry="2"/><circle cx="1" cy="1" r=".5" fill="currentColor" />`)
	if err != nil {
		t.Fatal(err)
	}
	if len(sps) != 7 {
		t.Fatalf("subpaths = %d, want 7", len(sps))
	}
	circle := sps[0]
	if !circle.closed() || len(circle.Segs) != 1+4+1 {
		t.Errorf("circle: closed=%v segs=%d", circle.closed(), len(circle.Segs))
	}
	for _, s := range circle.Segs[1:5] {
		if d := dist(s.end(), f32.Pt(12, 12)); math.Abs(float64(d-10)) > 1e-3 {
			t.Errorf("circle point %v off radius (%v)", s.end(), d)
		}
	}
	rect := sps[1]
	if !rect.closed() || !near(rect.Segs[0].P[0], f32.Pt(5, 4)) {
		t.Errorf("rect starts at %v, want (5,4) (rx inset)", rect.Segs[0].P[0])
	}
	if got := ends(sps[2]); !near(got[0], f32.Pt(1, 2)) || !near(got[1], f32.Pt(3, 4)) {
		t.Errorf("line = %v", got)
	}
	if sps[3].closed() || len(sps[3].Segs) != 3 {
		t.Errorf("polyline: closed=%v segs=%d", sps[3].closed(), len(sps[3].Segs))
	}
	if !sps[4].closed() {
		t.Error("polygon is not closed")
	}
	if !sps[5].closed() || !near(sps[5].Segs[0].P[0], f32.Pt(9, 5)) {
		t.Errorf("ellipse starts at %v, want (9,5)", sps[5].Segs[0].P[0])
	}
	if sps[4].Fill || !sps[6].Fill {
		t.Errorf("fill flags: polygon %v, dot %v", sps[4].Fill, sps[6].Fill)
	}
	// Only rx given: ry follows it; rx is clamped to half the size.
	sps, _ = parseSVG(`<rect width="4" height="10" rx="9" />`)
	if !near(sps[0].Segs[0].P[0], f32.Pt(2, 0)) {
		t.Errorf("clamped rx start = %v, want (2,0)", sps[0].Segs[0].P[0])
	}
	if _, err := parseSVG(`<circle cx="a" r="1" />`); err == nil {
		t.Error("bad number attribute: no error")
	}
}

func TestAllIconsParse(t *testing.T) {
	if len(AllIcons) < 150 {
		t.Fatalf("only %d icons", len(AllIcons))
	}
	seen := map[string]bool{}
	for _, ic := range AllIcons {
		if seen[ic.Name] {
			t.Errorf("duplicate icon %q", ic.Name)
		}
		seen[ic.Name] = true
		paths, err := parseSVG(ic.src)
		if err != nil {
			t.Errorf("%s: %v", ic.Name, err)
			continue
		}
		if len(paths) == 0 {
			t.Errorf("%s: no geometry", ic.Name)
		}
		for _, sp := range paths {
			if sp.Segs[0].Op != segMove {
				t.Errorf("%s: subpath does not start with a move", ic.Name)
			}
			for _, s := range sp.Segs {
				for k := 0; k < 3; k++ {
					p := s.P[k]
					if p.X < -1 || p.X > 25 || p.Y < -1 || p.Y > 25 {
						if s.Op == segCube || k == 0 {
							t.Errorf("%s: point %v outside the 24×24 box", ic.Name, p)
						}
					}
				}
			}
		}
	}
	for _, want := range []string{"home", "check-circle", "x-circle", "alert-triangle", "help-circle", "more-horizontal", "trash-2", "wand-sparkles"} {
		if !seen[want] {
			t.Errorf("icon %q missing", want)
		}
	}
	if !strings.Contains(IconHome.src, "<path") {
		t.Error("IconHome has no path data")
	}
}
