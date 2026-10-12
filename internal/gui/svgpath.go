package gui

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"gioui.org/f32"
)

// A small SVG geometry parser for the icon set: path data (M L H V C S Q T A
// Z in absolute and relative forms, with implicit command repetition and
// packed arc flags) and the basic shapes Lucide uses (<path>, <circle>,
// <ellipse>, <rect rx ry>, <line>, <polyline>, <polygon>). Arcs become cubic
// Béziers, so every shape is a list of move/line/quad/cube/close segments.

type segOp uint8

const (
	segMove segOp = iota
	segLine
	segQuad
	segCube
	segClose
)

// seg is one path segment. Move/Line use P[0]; Quad uses P[0] (control) and
// P[1] (end); Cube uses P[0], P[1] (controls) and P[2] (end).
type seg struct {
	Op segOp
	P  [3]f32.Point
}

// end returns the point the pen is at after s.
func (s seg) end() f32.Point {
	switch s.Op {
	case segQuad:
		return s.P[1]
	case segCube:
		return s.P[2]
	default:
		return s.P[0]
	}
}

// subpath is one contour: it starts with a segMove and may end with segClose.
type subpath struct {
	Segs []seg
	Fill bool // the element is filled too (fill="currentColor")
}

// closed reports whether the contour ends with Z.
func (sp subpath) closed() bool { return len(sp.Segs) > 0 && sp.Segs[len(sp.Segs)-1].Op == segClose }

// length approximates the contour length (control polygon for curves).
func (sp subpath) length() float32 {
	var l float32
	var cur, start f32.Point
	for _, s := range sp.Segs {
		switch s.Op {
		case segMove:
			cur, start = s.P[0], s.P[0]
		case segLine:
			l += dist(cur, s.P[0])
			cur = s.P[0]
		case segQuad:
			l += dist(cur, s.P[0]) + dist(s.P[0], s.P[1])
			cur = s.P[1]
		case segCube:
			l += dist(cur, s.P[0]) + dist(s.P[0], s.P[1]) + dist(s.P[1], s.P[2])
			cur = s.P[2]
		case segClose:
			l += dist(cur, start)
			cur = start
		}
	}
	return l
}

func dist(a, b f32.Point) float32 {
	return float32(math.Hypot(float64(a.X-b.X), float64(a.Y-b.Y)))
}

// ---- path data ---------------------------------------------------------------

type pathScanner struct {
	s string
	i int
}

func isSep(c byte) bool {
	return c == ' ' || c == ',' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func (p *pathScanner) skip() {
	for p.i < len(p.s) && isSep(p.s[p.i]) {
		p.i++
	}
}

func (p *pathScanner) eof() bool { p.skip(); return p.i >= len(p.s) }

// numberAhead reports whether a number starts at the cursor.
func (p *pathScanner) numberAhead() bool {
	p.skip()
	if p.i >= len(p.s) {
		return false
	}
	c := p.s[p.i]
	return c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9')
}

// number reads one SVG number: [sign] (digits [. digits] | . digits) [exponent].
// "1.5.5" reads as 1.5 then .5, "1-2" as 1 then -2.
func (p *pathScanner) number() (float32, error) {
	p.skip()
	start := p.i
	n := len(p.s)
	if p.i < n && (p.s[p.i] == '-' || p.s[p.i] == '+') {
		p.i++
	}
	digits := 0
	for p.i < n && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
		p.i++
		digits++
	}
	if p.i < n && p.s[p.i] == '.' {
		p.i++
		for p.i < n && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			digits++
		}
	}
	if digits == 0 {
		p.i = start
		return 0, fmt.Errorf("svg: number expected at %d in %q", start, p.s)
	}
	if p.i < n && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		j := p.i + 1
		if j < n && (p.s[j] == '-' || p.s[j] == '+') {
			j++
		}
		if j < n && p.s[j] >= '0' && p.s[j] <= '9' {
			for j < n && p.s[j] >= '0' && p.s[j] <= '9' {
				j++
			}
			p.i = j
		}
	}
	v, err := strconv.ParseFloat(p.s[start:p.i], 32)
	if err != nil {
		return 0, fmt.Errorf("svg: bad number %q: %w", p.s[start:p.i], err)
	}
	return float32(v), nil
}

// flag reads an arc flag: a single '0' or '1', which may be packed against
// the next number ("a1 1 0 011 1").
func (p *pathScanner) flag() (bool, error) {
	p.skip()
	if p.i < len(p.s) {
		switch p.s[p.i] {
		case '0':
			p.i++
			return false, nil
		case '1':
			p.i++
			return true, nil
		}
	}
	return false, fmt.Errorf("svg: arc flag expected at %d in %q", p.i, p.s)
}

func (p *pathScanner) point() (f32.Point, error) {
	x, err := p.number()
	if err != nil {
		return f32.Point{}, err
	}
	y, err := p.number()
	if err != nil {
		return f32.Point{}, err
	}
	return f32.Pt(x, y), nil
}

// pathBuilder accumulates subpaths.
type pathBuilder struct {
	paths     []subpath
	cur       f32.Point
	start     f32.Point
	open      bool // a subpath is in progress
	afterZ    bool // the last command closed the subpath
	lastCtrl  f32.Point
	lastCmd   byte // upper-case letter of the previous segment command
	fillShape bool
}

func (b *pathBuilder) moveTo(p f32.Point) {
	b.paths = append(b.paths, subpath{Segs: []seg{{Op: segMove, P: [3]f32.Point{p}}}, Fill: b.fillShape})
	b.cur, b.start, b.open, b.afterZ = p, p, true, false
}

// ensure starts a subpath at the current point when drawing continues after
// Z (or without any M), as the SVG spec requires.
func (b *pathBuilder) ensure() {
	if !b.open || b.afterZ {
		b.moveTo(b.cur)
	}
}

func (b *pathBuilder) add(s seg) {
	b.ensure()
	last := &b.paths[len(b.paths)-1]
	last.Segs = append(last.Segs, s)
	b.cur = s.end()
}

func (b *pathBuilder) lineTo(p f32.Point) { b.add(seg{Op: segLine, P: [3]f32.Point{p}}) }

func (b *pathBuilder) quadTo(c, p f32.Point) {
	b.add(seg{Op: segQuad, P: [3]f32.Point{c, p}})
	b.lastCtrl = c
}

func (b *pathBuilder) cubeTo(c1, c2, p f32.Point) {
	b.add(seg{Op: segCube, P: [3]f32.Point{c1, c2, p}})
	b.lastCtrl = c2
}

func (b *pathBuilder) close() {
	if !b.open || b.afterZ {
		return
	}
	last := &b.paths[len(b.paths)-1]
	last.Segs = append(last.Segs, seg{Op: segClose, P: [3]f32.Point{b.start}})
	b.cur = b.start
	b.afterZ = true
}

// parsePathData parses an SVG path "d" attribute into subpaths.
func parsePathData(d string) ([]subpath, error) {
	var b pathBuilder
	if err := b.path(d); err != nil {
		return nil, err
	}
	return b.paths, nil
}

func (b *pathBuilder) path(d string) error {
	p := &pathScanner{s: d}
	var cmd byte
	for !p.eof() {
		c := p.s[p.i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			cmd = c
			p.i++
			if cmd == 'Z' || cmd == 'z' {
				b.close()
				b.lastCmd = 'Z'
				continue
			}
		} else {
			switch cmd {
			case 0:
				return fmt.Errorf("svg: path data must start with a command: %q", d)
			case 'Z', 'z':
				return fmt.Errorf("svg: numbers after Z in %q", d)
			case 'M':
				cmd = 'L' // implicit lineto after the first moveto pair
			case 'm':
				cmd = 'l'
			}
		}
		if err := b.segment(p, cmd); err != nil {
			return err
		}
	}
	return nil
}

// segment consumes one argument group of cmd.
func (b *pathBuilder) segment(p *pathScanner, cmd byte) error {
	rel := cmd >= 'a' && cmd <= 'z'
	up := cmd &^ 0x20
	base := f32.Point{}
	if rel {
		base = b.cur
	}
	abs := func(q f32.Point) f32.Point { return q.Add(base) }
	switch up {
	case 'M':
		q, err := p.point()
		if err != nil {
			return err
		}
		b.moveTo(abs(q))
	case 'L':
		q, err := p.point()
		if err != nil {
			return err
		}
		b.lineTo(abs(q))
	case 'H':
		x, err := p.number()
		if err != nil {
			return err
		}
		if rel {
			x += b.cur.X
		}
		b.lineTo(f32.Pt(x, b.cur.Y))
	case 'V':
		y, err := p.number()
		if err != nil {
			return err
		}
		if rel {
			y += b.cur.Y
		}
		b.lineTo(f32.Pt(b.cur.X, y))
	case 'C':
		var q [3]f32.Point
		for i := range q {
			v, err := p.point()
			if err != nil {
				return err
			}
			q[i] = abs(v)
		}
		b.cubeTo(q[0], q[1], q[2])
	case 'S':
		c2, err := p.point()
		if err != nil {
			return err
		}
		e, err := p.point()
		if err != nil {
			return err
		}
		c1 := b.cur
		if b.lastCmd == 'C' || b.lastCmd == 'S' {
			c1 = b.cur.Mul(2).Sub(b.lastCtrl)
		}
		b.cubeTo(c1, abs(c2), abs(e))
	case 'Q':
		c, err := p.point()
		if err != nil {
			return err
		}
		e, err := p.point()
		if err != nil {
			return err
		}
		b.quadTo(abs(c), abs(e))
	case 'T':
		e, err := p.point()
		if err != nil {
			return err
		}
		c := b.cur
		if b.lastCmd == 'Q' || b.lastCmd == 'T' {
			c = b.cur.Mul(2).Sub(b.lastCtrl)
		}
		b.quadTo(c, abs(e))
	case 'A':
		rx, err := p.number()
		if err != nil {
			return err
		}
		ry, err := p.number()
		if err != nil {
			return err
		}
		rot, err := p.number()
		if err != nil {
			return err
		}
		large, err := p.flag()
		if err != nil {
			return err
		}
		sweep, err := p.flag()
		if err != nil {
			return err
		}
		e, err := p.point()
		if err != nil {
			return err
		}
		b.arcTo(rx, ry, rot, large, sweep, abs(e))
	default:
		return fmt.Errorf("svg: unknown path command %q", cmd)
	}
	b.lastCmd = up
	return nil
}

// arcTo appends an elliptical arc from the pen to e as cubic Béziers
// (endpoint → centre parameterisation, SVG 1.1 F.6.5; ≤ 90° per cubic).
func (b *pathBuilder) arcTo(rx, ry, rotDeg float32, large, sweep bool, e f32.Point) {
	p0 := b.cur
	if p0 == e {
		return // the spec omits zero-length arcs
	}
	rx, ry = abs32(rx), abs32(ry)
	if rx == 0 || ry == 0 {
		b.lineTo(e)
		return
	}
	for _, c := range arcCubics(p0, rx, ry, rotDeg, large, sweep, e) {
		b.cubeTo(c[0], c[1], c[2])
	}
}

// arcCubics converts an SVG arc into cubic segments (control, control, end).
func arcCubics(p0 f32.Point, rxf, ryf, rotDeg float32, large, sweep bool, p1 f32.Point) [][3]f32.Point {
	x0, y0, x1, y1 := float64(p0.X), float64(p0.Y), float64(p1.X), float64(p1.Y)
	rx, ry := float64(rxf), float64(ryf)
	phi := float64(rotDeg) * math.Pi / 180
	cosP, sinP := math.Cos(phi), math.Sin(phi)
	dx2, dy2 := (x0-x1)/2, (y0-y1)/2
	x1p := cosP*dx2 + sinP*dy2
	y1p := -sinP*dx2 + cosP*dy2
	// Scale radii up when they cannot span the endpoints.
	if lambda := x1p*x1p/(rx*rx) + y1p*y1p/(ry*ry); lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1p*y1p - ry*ry*x1p*x1p
	den := rx*rx*y1p*y1p + ry*ry*x1p*x1p
	coef := 0.0
	if den > 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1p / ry
	cyp := -coef * ry * x1p / rx
	cx := cosP*cxp - sinP*cyp + (x0+x1)/2
	cy := sinP*cxp + cosP*cyp + (y0+y1)/2
	ux, uy := (x1p-cxp)/rx, (y1p-cyp)/ry
	vx, vy := (-x1p-cxp)/rx, (-y1p-cyp)/ry
	theta1 := math.Atan2(uy, ux)
	dtheta := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	if !sweep && dtheta > 0 {
		dtheta -= 2 * math.Pi
	} else if sweep && dtheta < 0 {
		dtheta += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dtheta)/(math.Pi/2) - 1e-9))
	n = max(1, n)
	delta := dtheta / float64(n)
	t := 4.0 / 3.0 * math.Tan(delta/4)
	point := func(a float64) (float64, float64) {
		ca, sa := math.Cos(a), math.Sin(a)
		return cx + rx*ca*cosP - ry*sa*sinP, cy + rx*ca*sinP + ry*sa*cosP
	}
	deriv := func(a float64) (float64, float64) {
		ca, sa := math.Cos(a), math.Sin(a)
		return -rx*sa*cosP - ry*ca*sinP, -rx*sa*sinP + ry*ca*cosP
	}
	out := make([][3]f32.Point, 0, n)
	a1 := theta1
	for i := 0; i < n; i++ {
		a2 := a1 + delta
		px1, py1 := point(a1)
		px2, py2 := point(a2)
		dx1, dy1 := deriv(a1)
		dx2, dy2 := deriv(a2)
		end := f32.Pt(float32(px2), float32(py2))
		if i == n-1 {
			end = p1 // land exactly on the endpoint
		}
		out = append(out, [3]f32.Point{
			f32.Pt(float32(px1+t*dx1), float32(py1+t*dy1)),
			f32.Pt(float32(px2-t*dx2), float32(py2-t*dy2)),
			end,
		})
		a1 = a2
	}
	return out
}

// ---- elements -------------------------------------------------------------------

// parseSVG parses a sequence of basic SVG elements (as stored in the icon
// data: `<path d="…" /><circle cx=".." … />`) into subpaths.
func parseSVG(src string) ([]subpath, error) {
	var out []subpath
	rest := src
	for {
		i := strings.IndexByte(rest, '<')
		if i < 0 {
			break
		}
		rest = rest[i+1:]
		j := strings.IndexByte(rest, '>')
		if j < 0 {
			return nil, errors.New("svg: unterminated element")
		}
		tag := strings.TrimSuffix(strings.TrimSpace(rest[:j]), "/")
		rest = rest[j+1:]
		if strings.HasPrefix(tag, "!") || strings.HasPrefix(tag, "?") || strings.HasPrefix(tag, "/") {
			continue // comments, declarations, closing tags
		}
		name, attrText, _ := strings.Cut(tag, " ")
		attrs, err := parseAttrs(attrText)
		if err != nil {
			return nil, fmt.Errorf("svg: <%s>: %w", name, err)
		}
		paths, err := elementPaths(name, attrs)
		if err != nil {
			return nil, fmt.Errorf("svg: <%s>: %w", name, err)
		}
		out = append(out, paths...)
	}
	return out, nil
}

// parseAttrs reads name="value" pairs.
func parseAttrs(s string) (map[string]string, error) {
	m := map[string]string{}
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return m, nil
		}
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			return nil, fmt.Errorf("attribute without value in %q", s)
		}
		key := strings.TrimSpace(s[:eq])
		s = strings.TrimLeft(s[eq+1:], " ")
		if s == "" || (s[0] != '"' && s[0] != '\'') {
			return nil, fmt.Errorf("unquoted attribute %q", key)
		}
		q := s[0]
		end := strings.IndexByte(s[1:], q)
		if end < 0 {
			return nil, fmt.Errorf("unterminated attribute %q", key)
		}
		m[key] = s[1 : 1+end]
		s = s[2+end:]
	}
}

func attrNum(a map[string]string, key string, def float32) (float32, error) {
	v, ok := a[key]
	if !ok || strings.TrimSpace(v) == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
	if err != nil {
		return 0, fmt.Errorf("attribute %s=%q: %w", key, v, err)
	}
	return float32(f), nil
}

func elementPaths(name string, a map[string]string) ([]subpath, error) {
	fill := false
	if f := strings.TrimSpace(a["fill"]); f != "" && f != "none" {
		fill = true
	}
	var b pathBuilder
	b.fillShape = fill
	// Validate the numeric attributes once; num then cannot fail.
	for _, k := range []string{"cx", "cy", "r", "rx", "ry", "x", "y", "width", "height", "x1", "y1", "x2", "y2"} {
		if _, err := attrNum(a, k, 0); err != nil {
			return nil, err
		}
	}
	num := func(key string, def float32) float32 {
		v, _ := attrNum(a, key, def)
		return v
	}
	switch name {
	case "path":
		if err := b.path(a["d"]); err != nil {
			return nil, err
		}
	case "circle":
		r := num("r", 0)
		if r <= 0 {
			return nil, nil
		}
		ellipse(&b, num("cx", 0), num("cy", 0), r, r)
	case "ellipse":
		rx, ry := num("rx", 0), num("ry", 0)
		if rx <= 0 || ry <= 0 {
			return nil, nil
		}
		ellipse(&b, num("cx", 0), num("cy", 0), rx, ry)
	case "rect":
		x, y, w, h := num("x", 0), num("y", 0), num("width", 0), num("height", 0)
		if w <= 0 || h <= 0 {
			return nil, nil
		}
		_, hasRX := a["rx"]
		_, hasRY := a["ry"]
		rx, ry := num("rx", 0), num("ry", 0)
		switch {
		case hasRX && !hasRY:
			ry = rx
		case hasRY && !hasRX:
			rx = ry
		}
		rx, ry = min(max(rx, 0), w/2), min(max(ry, 0), h/2)
		roundRect(&b, x, y, w, h, rx, ry)
	case "line":
		b.moveTo(f32.Pt(num("x1", 0), num("y1", 0)))
		b.lineTo(f32.Pt(num("x2", 0), num("y2", 0)))
	case "polyline", "polygon":
		p := &pathScanner{s: a["points"]}
		first := true
		for p.numberAhead() {
			q, err := p.point()
			if err != nil {
				return nil, err
			}
			if first {
				b.moveTo(q)
				first = false
			} else {
				b.lineTo(q)
			}
		}
		if name == "polygon" && !first {
			b.close()
		}
	default:
		return nil, nil // not geometry (e.g. <title>)
	}
	return b.paths, nil
}

// ellipse appends a closed ellipse as four quarter-arc cubics.
func ellipse(b *pathBuilder, cx, cy, rx, ry float32) {
	const k = 0.5522847498
	b.moveTo(f32.Pt(cx+rx, cy))
	b.cubeTo(f32.Pt(cx+rx, cy+ry*k), f32.Pt(cx+rx*k, cy+ry), f32.Pt(cx, cy+ry))
	b.cubeTo(f32.Pt(cx-rx*k, cy+ry), f32.Pt(cx-rx, cy+ry*k), f32.Pt(cx-rx, cy))
	b.cubeTo(f32.Pt(cx-rx, cy-ry*k), f32.Pt(cx-rx*k, cy-ry), f32.Pt(cx, cy-ry))
	b.cubeTo(f32.Pt(cx+rx*k, cy-ry), f32.Pt(cx+rx, cy-ry*k), f32.Pt(cx+rx, cy))
	b.close()
}

// roundRect appends a closed rectangle with elliptical corners.
func roundRect(b *pathBuilder, x, y, w, h, rx, ry float32) {
	const k = 0.5522847498
	if rx <= 0 || ry <= 0 {
		b.moveTo(f32.Pt(x, y))
		b.lineTo(f32.Pt(x+w, y))
		b.lineTo(f32.Pt(x+w, y+h))
		b.lineTo(f32.Pt(x, y+h))
		b.close()
		return
	}
	x1, y1 := x+w, y+h
	b.moveTo(f32.Pt(x+rx, y))
	b.lineTo(f32.Pt(x1-rx, y))
	b.cubeTo(f32.Pt(x1-rx+rx*k, y), f32.Pt(x1, y+ry-ry*k), f32.Pt(x1, y+ry))
	b.lineTo(f32.Pt(x1, y1-ry))
	b.cubeTo(f32.Pt(x1, y1-ry+ry*k), f32.Pt(x1-rx+rx*k, y1), f32.Pt(x1-rx, y1))
	b.lineTo(f32.Pt(x+rx, y1))
	b.cubeTo(f32.Pt(x+rx-rx*k, y1), f32.Pt(x, y1-ry+ry*k), f32.Pt(x, y1-ry))
	b.lineTo(f32.Pt(x, y+ry))
	b.cubeTo(f32.Pt(x, y+ry-ry*k), f32.Pt(x+rx-rx*k, y), f32.Pt(x+rx, y))
	b.close()
}
