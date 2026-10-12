package produce

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"sync"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"ytcartell/internal/gui"
	"ytcartell/internal/media"
)

// LightboxItem is one picture of a Lightbox.
type LightboxItem struct {
	// Key identifies the item (a scene or character id): refreshed items
	// keep the current position by it.
	Key     string
	Path    string // absolute image file
	Ver     int64  // version (Asset.UpdatedAt): a new one reloads the picture
	Title   string
	Caption string
	// Grid > 1 marks a Grid×Grid storyboard sheet: its panels get numbers
	// and a click on a panel zooms into it.
	Grid int
}

// Lightbox shows pictures over the whole window: a dark scrim, the picture
// fitted (contain), a title bar, prev/next buttons and a filmstrip, Esc to
// close, ←/→ to move, zoom with the wheel, a click, +/−/0 (drag to pan), and
// numbered panels on storyboard sheets. It draws in a Theme layer, above
// every other overlay laid out before it.
//
//	lb.Open(items, i)               // on a click
//	lb.Layout(gtx, th, cache)       // every frame (draws nothing while closed)
type Lightbox struct {
	// Background runs slow work (finding the panels of a sheet) off the UI
	// goroutine; nil starts a goroutine. Invalidate asks for a frame when
	// that work is done (nil: the next frame picks it up).
	Background func(fn func())
	Invalidate func()

	items []LightboxItem
	index int
	open  bool

	show     gui.Anim // open / close
	swap     gui.Anim // fade-in after moving to another picture
	shownKey string
	since    time.Time // when the current picture was first asked for

	// The view: zoom (1 = fitted) and pan (px from the centre), easing
	// towards their targets.
	zoom, zoomT float32
	pan, panT   f32.Point
	last        time.Time

	tagScrim, tagKeys int
	focused           bool

	pressed  bool
	pressAt  f32.Point
	pressPan f32.Point
	dragged  bool
	ptr      f32.Point

	prev, next, close, zin, zout, reset glassBtn
	thumbs                              []widget.Clickable

	mu      sync.Mutex
	panels  map[string][]frac
	finding map[string]bool

	// Geometry of the last frame, window px.
	view image.Rectangle // picture area
	fit  f32.Point       // picture size at zoom 1
}

// frac is a rectangle in fractions of a picture.
type frac struct{ x0, y0, x1, y1 float32 }

const (
	lbMaxZoom   = 8
	lbClickZoom = 2.5
)

// Open shows items starting at index.
func (l *Lightbox) Open(items []LightboxItem, index int) {
	if len(items) == 0 {
		return
	}
	l.items = append(l.items[:0], items...)
	l.index = max(0, min(index, len(items)-1))
	if !l.open {
		l.focused = false
	}
	l.open = true
	l.resetView()
}

// Close hides the lightbox (animated).
func (l *Lightbox) Close() { l.open, l.pressed = false, false }

// IsOpen reports whether the lightbox is shown.
func (l *Lightbox) IsOpen() bool { return l.open }

// Index is the position of the current picture.
func (l *Lightbox) Index() int { return l.index }

// Current is the key of the picture shown ("" when closed or empty).
func (l *Lightbox) Current() string {
	if !l.open || l.index >= len(l.items) {
		return ""
	}
	return l.items[l.index].Key
}

// Update replaces the items while open (a picture was redrawn, a scene
// removed), staying on the current key when it is still there.
func (l *Lightbox) Update(items []LightboxItem) {
	if !l.open {
		return
	}
	if len(items) == 0 {
		l.Close()
		return
	}
	cur := l.Current()
	l.items = append(l.items[:0], items...)
	for i, it := range l.items {
		if it.Key == cur {
			l.index = i
			return
		}
	}
	l.index = min(l.index, len(l.items)-1)
	l.resetView()
}

// Go moves to picture i.
func (l *Lightbox) Go(i int) {
	if i < 0 || i >= len(l.items) || i == l.index {
		return
	}
	l.index = i
	l.resetView()
}

// ZoomTo sets the zoom (1 = fitted) around the centre (screenshots, tests).
func (l *Lightbox) ZoomTo(z float32) {
	l.zoomT = max(1, min(lbMaxZoom, z))
	l.panT = f32.Point{}
}

func (l *Lightbox) resetView() {
	l.zoomT, l.panT = 1, f32.Point{}
	l.zoom, l.pan = 1, f32.Point{}
	l.pressed = false
}

// Layout queues the lightbox above the window content.
func (l *Lightbox) Layout(gtx C, th *gui.Theme, cache *gui.ImageCache) {
	l.show.Duration, l.show.Ease = gui.DurDialog, gui.EaseOutCubic
	if !l.open {
		l.show.Ease = gui.EaseInCubic
	}
	a := l.show.Animate(gtx, b2f(l.open))
	if a <= 0.001 && !l.open {
		return
	}
	th.Layer(gtx, func(gtx C) { l.draw(gtx, th, cache, a) })
}

// lbBucket is the decode size for a window: the next of 1280/1920/2560/3840
// px above its longest side, so the picture stays sharp when zoomed a bit.
func lbBucket(win image.Point) int {
	m := max(win.X, win.Y)
	for _, b := range []int{1280, 1920, 2560, 3840} {
		if m <= b {
			return b
		}
	}
	return 3840
}

// Fixed "night" colours: the lightbox is dark in both themes.
var (
	lbScrim = color.NRGBA{R: 0x12, G: 0x0E, B: 0x0B, A: 0xFA}
	lbText  = gui.DarkPalette.Text
	lbText2 = gui.DarkPalette.Text2
)

func (l *Lightbox) draw(gtx C, th *gui.Theme, cache *gui.ImageCache, a float32) {
	win := gtx.Constraints.Max
	n := len(l.items)
	if n == 0 {
		return
	}
	l.index = max(0, min(l.index, n-1))
	it := l.items[l.index]

	// Geometry.
	pad := gtx.Dp(16)
	topH := gtx.Dp(68)
	stripH := gtx.Dp(16)
	if n > 1 {
		stripH = gtx.Dp(92)
	}
	side := gtx.Dp(24)
	narrow := win.X < gtx.Dp(640)
	if n > 1 && !narrow {
		side = gtx.Dp(84)
	}
	if narrow {
		side = gtx.Dp(8)
	}
	l.view = image.Rect(side, topH, max(side+1, win.X-side), max(topH+1, win.Y-stripH))

	// Input (from the last frame).
	l.input(gtx, it)
	if l.close.Clicked(gtx) {
		l.Close()
	}
	if l.prev.Clicked(gtx) {
		l.Go(l.index - 1)
	}
	if l.next.Clicked(gtx) {
		l.Go(l.index + 1)
	}
	c := layout.FPt(l.view.Min).Add(layout.FPt(l.view.Size()).Mul(0.5))
	if l.zin.Clicked(gtx) {
		l.zoomAt(c, 1.5)
	}
	if l.zout.Clicked(gtx) {
		l.zoomAt(c, 1/1.5)
	}
	if l.reset.Clicked(gtx) {
		l.zoomT, l.panT = 1, f32.Point{}
	}
	if len(l.thumbs) < n {
		l.thumbs = append(l.thumbs, make([]widget.Clickable, n-len(l.thumbs))...)
	}
	for i := 0; i < n; i++ {
		if l.thumbs[i].Clicked(gtx) {
			l.Go(i)
		}
	}
	if !l.open {
		l.pressed = false
	}
	it = l.items[l.index]
	l.ease(gtx)

	// Scrim: blocks the window and takes the pointer and the keys.
	func() {
		st := clip.Rect(image.Rectangle{Max: win}).Push(gtx.Ops)
		defer st.Pop()
		paint.ColorOp{Color: gui.WithAlpha(lbScrim, a)}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		event.Op(gtx.Ops, &l.tagScrim)
		event.Op(gtx.Ops, &l.tagKeys)
		switch {
		case l.zoom > 1.01 && l.pressed:
			pointer.CursorGrabbing.Add(gtx.Ops)
		case l.zoom > 1.01:
			pointer.CursorGrab.Add(gtx.Ops)
		}
	}()
	if l.open && !l.focused {
		l.focused = true
		gtx.Execute(key.FocusCmd{Tag: &l.tagKeys})
	}

	defer paint.PushOpacity(gtx.Ops, a).Pop()

	// The picture.
	if it.Key != l.shownKey {
		l.shownKey, l.since = it.Key, gtx.Now
		l.swap.Set(0)
	}
	l.swap.Duration, l.swap.Ease = 180*time.Millisecond, gui.EaseOutCubic
	img, isz, ok := cache.Get(it.Path, it.Ver, lbBucket(win))
	vw, vh := float32(l.view.Dx()), float32(l.view.Dy())
	if ok && isz.X > 0 && isz.Y > 0 {
		s0 := min(vw/float32(isz.X), vh/float32(isz.Y))
		l.fit = f32.Pt(float32(isz.X)*s0, float32(isz.Y)*s0)
		fade := l.swap.Animate(gtx, 1)
		func() {
			band := clip.Rect(image.Rect(0, topH, win.X, win.Y-stripH)).Push(gtx.Ops)
			defer band.Pop()
			// Entry: the picture grows from 96 % as the scrim fades in.
			s := 0.96 + 0.04*a
			defer op.Affine(f32.AffineId().Scale(c, f32.Pt(s, s))).Push(gtx.Ops).Pop()
			defer paint.PushOpacity(gtx.Ops, fade).Pop()
			r := l.picRect()
			drawPicture(gtx, img, r)
			if it.Grid > 1 {
				l.drawPanels(gtx, th, it, r)
			}
			// Hand cursor over the picture while it can be zoomed by a click.
			if l.zoom <= 1.01 {
				pr := image.Rect(int(r.Min.X), int(r.Min.Y), int(r.Max.X), int(r.Max.Y)).Intersect(l.view)
				cl := clip.Rect(pr).Push(gtx.Ops)
				pointer.CursorPointer.Add(gtx.Ops)
				cl.Pop()
			}
		}()
	} else {
		l.fit = f32.Point{}
		sp := gtx.Dp(28)
		o := op.Offset(pt(int(c.X)-sp/2, int(c.Y)-sp/2)).Push(gtx.Ops)
		gui.Spinner{Size: 28, Color: lbText2}.Layout(gtx, th)
		o.Pop()
		if gtx.Now.Sub(l.since) > 2500*time.Millisecond {
			lg := gtx
			lg.Constraints = layout.Constraints{Max: pt(l.view.Dx(), gtx.Dp(60))}
			d, call := record(lg, label(th, th.Small, lbText2, 2, "Зображення не відкривається — можливо, файл перемістили або видалили."))
			drawAt(gtx, int(c.X)-d.Size.X/2, int(c.Y)+sp, call)
		} else {
			gtx.Execute(op.InvalidateCmd{At: l.since.Add(2600 * time.Millisecond)})
		}
	}

	// Title bar.
	l.drawBar(gtx, th, it, win, pad, topH)

	// Previous / next.
	if n > 1 {
		b := gtx.Dp(52)
		y := l.view.Min.Y + (l.view.Dy()-b)/2
		xl, xr := (side-b)/2, win.X-side+(side-b)/2
		if narrow {
			xl, xr = l.view.Min.X+gtx.Dp(8), l.view.Max.X-gtx.Dp(8)-b
		}
		o := op.Offset(pt(xl, y)).Push(gtx.Ops)
		l.prev.Layout(gtx, th, gui.IconChevronLeft, 52, "Попереднє (←)", l.index == 0)
		o.Pop()
		o = op.Offset(pt(xr, y)).Push(gtx.Ops)
		l.next.Layout(gtx, th, gui.IconChevronRight, 52, "Наступне (→)", l.index >= n-1)
		o.Pop()
		l.drawStrip(gtx, th, cache, win, stripH)
	}
}

// picRect is where the picture is drawn at the current zoom and pan.
func (l *Lightbox) picRect() fRect {
	c := layout.FPt(l.view.Min).Add(layout.FPt(l.view.Size()).Mul(0.5)).Add(l.pan)
	w, h := l.fit.X*l.zoom, l.fit.Y*l.zoom
	return fRect{Min: f32.Pt(c.X-w/2, c.Y-h/2), Max: f32.Pt(c.X+w/2, c.Y+h/2)}
}

type fRect struct{ Min, Max f32.Point }

func (r fRect) Dx() float32 { return r.Max.X - r.Min.X }
func (r fRect) Dy() float32 { return r.Max.Y - r.Min.Y }

func (r fRect) contains(p f32.Point) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

// drawPicture paints img scaled into r.
func drawPicture(gtx C, img paint.ImageOp, r fRect) {
	sz := img.Size()
	if sz.X == 0 || sz.Y == 0 || r.Dx() <= 0 || r.Dy() <= 0 {
		return
	}
	tr := f32.AffineId().Scale(f32.Point{}, f32.Pt(r.Dx()/float32(sz.X), r.Dy()/float32(sz.Y))).Offset(r.Min)
	defer op.Affine(tr).Push(gtx.Ops).Pop()
	defer clip.Rect(image.Rectangle{Max: sz}).Push(gtx.Ops).Pop()
	img.Filter = paint.FilterLinear
	img.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// ease moves zoom and pan towards their targets (critically damped feel).
func (l *Lightbox) ease(gtx C) {
	dt := gtx.Now.Sub(l.last).Seconds()
	l.last = gtx.Now
	if dt <= 0 || dt > 0.1 {
		dt = 1.0 / 60
	}
	l.panT = l.clampPan(l.panT, l.zoomT)
	if gui.ReducedMotion() {
		l.zoom, l.pan = l.zoomT, l.panT
		return
	}
	k := float32(1 - math.Exp(-dt/0.075))
	l.zoom += (l.zoomT - l.zoom) * k
	l.pan = l.pan.Add(l.panT.Sub(l.pan).Mul(k))
	if abs32(l.zoomT-l.zoom) < 0.002 && abs32(l.panT.X-l.pan.X) < 0.5 && abs32(l.panT.Y-l.pan.Y) < 0.5 {
		l.zoom, l.pan = l.zoomT, l.panT
		return
	}
	gtx.Execute(op.InvalidateCmd{})
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// clampPan keeps the picture covering the view along the axes where it is
// larger than the view, and centred along the others.
func (l *Lightbox) clampPan(p f32.Point, z float32) f32.Point {
	ex := max(0, (l.fit.X*z-float32(l.view.Dx()))/2)
	ey := max(0, (l.fit.Y*z-float32(l.view.Dy()))/2)
	return f32.Pt(max(-ex, min(ex, p.X)), max(-ey, min(ey, p.Y)))
}

// zoomAt multiplies the zoom by factor keeping the picture point under p
// (window px) in place.
func (l *Lightbox) zoomAt(p f32.Point, factor float32) {
	z1 := max(1, min(lbMaxZoom, l.zoomT*factor))
	if z1 == l.zoomT {
		return
	}
	c := layout.FPt(l.view.Min).Add(layout.FPt(l.view.Size()).Mul(0.5))
	q := p.Sub(c).Sub(l.panT).Mul(1 / l.zoomT)
	l.panT = l.clampPan(p.Sub(c).Sub(q.Mul(z1)), z1)
	l.zoomT = z1
}

// clickAt handles a click on the picture: back to fitted when zoomed, a
// panel of a sheet fills the view, else zoom in where clicked.
func (l *Lightbox) clickAt(it LightboxItem, p f32.Point) {
	if l.zoomT > 1.05 {
		l.zoomT, l.panT = 1, f32.Point{}
		return
	}
	if it.Grid > 1 && l.fit.X > 0 {
		r := l.picRect()
		for _, f := range l.panelsFor(it) {
			pr := fRect{Min: f32.Pt(r.Min.X+f.x0*r.Dx(), r.Min.Y+f.y0*r.Dy()), Max: f32.Pt(r.Min.X+f.x1*r.Dx(), r.Min.Y+f.y1*r.Dy())}
			if !pr.contains(p) {
				continue
			}
			pw, ph := (f.x1-f.x0)*l.fit.X, (f.y1-f.y0)*l.fit.Y
			z := min(float32(l.view.Dx())/pw, float32(l.view.Dy())/ph) * 0.94
			z = max(1, min(lbMaxZoom, z))
			d := f32.Pt(((f.x0+f.x1)/2-0.5)*l.fit.X, ((f.y0+f.y1)/2-0.5)*l.fit.Y)
			l.zoomT = z
			l.panT = l.clampPan(d.Mul(-z), z)
			return
		}
	}
	l.zoomAt(p, lbClickZoom/l.zoomT)
}

// input handles keys and the pointer on the scrim.
func (l *Lightbox) input(gtx C, it LightboxItem) {
	for {
		e, ok := gtx.Event(
			key.FocusFilter{Target: &l.tagKeys},
			key.Filter{Focus: &l.tagKeys, Name: key.NameEscape},
			key.Filter{Focus: &l.tagKeys, Name: key.NameLeftArrow},
			key.Filter{Focus: &l.tagKeys, Name: key.NameRightArrow},
			key.Filter{Focus: &l.tagKeys, Name: key.NameHome},
			key.Filter{Focus: &l.tagKeys, Name: key.NameEnd},
			key.Filter{Focus: &l.tagKeys, Name: "+", Optional: key.ModShift},
			key.Filter{Focus: &l.tagKeys, Name: "=", Optional: key.ModShift},
			key.Filter{Focus: &l.tagKeys, Name: "-", Optional: key.ModShift},
			key.Filter{Focus: &l.tagKeys, Name: "0"},
		)
		if !ok {
			break
		}
		ke, ok := e.(key.Event)
		if !ok || ke.State != key.Press || !l.open {
			continue
		}
		c := layout.FPt(l.view.Min).Add(layout.FPt(l.view.Size()).Mul(0.5))
		switch ke.Name {
		case key.NameEscape:
			if l.zoomT > 1.05 {
				l.zoomT, l.panT = 1, f32.Point{}
			} else {
				l.Close()
			}
		case key.NameLeftArrow:
			l.Go(l.index - 1)
		case key.NameRightArrow:
			l.Go(l.index + 1)
		case key.NameHome:
			l.Go(0)
		case key.NameEnd:
			l.Go(len(l.items) - 1)
		case "+", "=":
			l.zoomAt(c, 1.5)
		case "-":
			l.zoomAt(c, 1/1.5)
		case "0":
			l.zoomT, l.panT = 1, f32.Point{}
		}
		gtx.Execute(op.InvalidateCmd{})
	}
	for {
		e, ok := gtx.Event(pointer.Filter{
			Target:  &l.tagScrim,
			Kinds:   pointer.Press | pointer.Release | pointer.Drag | pointer.Move | pointer.Scroll | pointer.Cancel,
			ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
			ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20},
		})
		if !ok {
			break
		}
		pe, ok := e.(pointer.Event)
		if !ok || !l.open {
			continue
		}
		l.ptr = pe.Position
		switch pe.Kind {
		case pointer.Press:
			if pe.Buttons != 0 && pe.Buttons&pointer.ButtonPrimary == 0 {
				continue
			}
			l.pressed, l.dragged = true, false
			l.pressAt, l.pressPan = pe.Position, l.panT
		case pointer.Drag:
			if !l.pressed {
				continue
			}
			d := pe.Position.Sub(l.pressAt)
			if abs32(d.X)+abs32(d.Y) > float32(gtx.Dp(5)) {
				l.dragged = true
			}
			if l.dragged && l.zoomT > 1.01 {
				l.panT = l.clampPan(l.pressPan.Add(d), l.zoomT)
				l.pan = l.panT // follow the hand without lag
			}
		case pointer.Release:
			if l.pressed && !l.dragged {
				switch {
				case l.fit.X > 0 && l.picRect().contains(pe.Position) && image.Pt(int(pe.Position.X), int(pe.Position.Y)).In(l.view):
					l.clickAt(it, pe.Position)
				case image.Pt(int(pe.Position.X), int(pe.Position.Y)).In(l.view):
					l.Close() // a click beside the picture
				}
			}
			l.pressed = false
		case pointer.Cancel:
			l.pressed = false
		case pointer.Scroll:
			if l.fit.X == 0 {
				continue
			}
			dy := pe.Scroll.Y
			if dy == 0 {
				continue
			}
			f := float32(math.Exp(float64(-dy) * math.Log(1.25) / 120))
			l.zoomAt(pe.Position, max(0.5, min(2, f)))
		}
		gtx.Execute(op.InvalidateCmd{})
	}
}

// drawBar draws the title, the caption, the counter and the zoom/close
// buttons along the top.
func (l *Lightbox) drawBar(gtx C, th *gui.Theme, it LightboxItem, win image.Point, pad, topH int) {
	b := gtx.Dp(40)
	gap := gtx.Dp(8)
	// Right cluster.
	rg := gtx
	rg.Constraints = layout.Constraints{Max: pt(win.X, topH)}
	rd, rc := record(rg, func(gtx C) D {
		zoomTxt := fmt.Sprintf("%d%%", int(math.Round(float64(l.zoomT*100))))
		ws := []layout.Widget{}
		if len(l.items) > 1 {
			ws = append(ws, label(th, th.Label, lbText2, 1, fmt.Sprintf("%d / %d", l.index+1, len(l.items))),
				func(gtx C) D { return D{Size: pt(gtx.Dp(8), 1)} })
		}
		if win.X >= gtx.Dp(560) {
			ws = append(ws,
				func(gtx C) D {
					return l.zout.Layout(gtx, th, gui.IconMinus, 40, "Віддалити (−)", l.zoomT <= 1.001)
				},
				func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Dp(52)
					return layout.Center.Layout(gtx, label(th, th.Label, lbText, 1, zoomTxt))
				},
				func(gtx C) D {
					return l.zin.Layout(gtx, th, gui.IconPlus, 40, "Наблизити (+)", l.zoomT >= lbMaxZoom-0.01)
				},
				func(gtx C) D {
					return l.reset.Layout(gtx, th, gui.IconMinimize2, 40, "Показати цілком (0)", l.zoomT <= 1.001)
				},
				func(gtx C) D { return D{Size: pt(gtx.Dp(8), 1)} },
			)
		}
		ws = append(ws, func(gtx C) D { return l.close.Layout(gtx, th, gui.IconX, 40, "Закрити (Esc)", false) })
		return hrow(gtx, unit.Dp(float32(gap)/gtx.Metric.PxPerDp), ws...)
	})
	drawAt(gtx, win.X-pad-rd.Size.X, (topH-b)/2, rc)
	// Title and caption.
	lg := gtx
	lg.Constraints = layout.Constraints{Max: pt(max(0, win.X-3*pad-rd.Size.X), topH)}
	ld, lc := record(lg, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(label(th, th.H3, lbText, 1, it.Title)),
			layout.Rigid(func(gtx C) D {
				if it.Caption == "" {
					return D{}
				}
				return label(th, th.Small, lbText2, 1, it.Caption)(gtx)
			}),
		)
	})
	drawAt(gtx, pad+gtx.Dp(8), (topH-ld.Size.Y)/2, lc)
}

// drawStrip draws the filmstrip: square thumbs around the current picture.
func (l *Lightbox) drawStrip(gtx C, th *gui.Theme, cache *gui.ImageCache, win image.Point, stripH int) {
	n := len(l.items)
	ts := gtx.Dp(60)
	gap := gtx.Dp(10)
	fit := max(1, (win.X-2*gtx.Dp(24)+gap)/(ts+gap))
	count := min(n, fit)
	first := max(0, min(l.index-count/2, n-count))
	w := count*ts + (count-1)*gap
	x0 := (win.X - w) / 2
	y := win.Y - stripH + (stripH-ts)/2
	for k := 0; k < count; k++ {
		i := first + k
		it := l.items[i]
		o := op.Offset(pt(x0+k*(ts+gap), y)).Push(gtx.Ops)
		cur := i == l.index
		l.thumbs[i].Layout(gtx, func(gtx C) D {
			defer clip.Rect(rectWH(ts, ts)).Push(gtx.Ops).Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			return D{Size: pt(ts, ts)}
		})
		alpha := float32(0.5)
		if cur {
			alpha = 1
		} else if l.thumbs[i].Hovered() {
			alpha = 0.85
		}
		func() {
			defer paint.PushOpacity(gtx.Ops, alpha).Pop()
			img, _, ok := cache.Get(it.Path, it.Ver, gui.ImgBucket(ts))
			tg := gtx
			tg.Constraints = layout.Exact(pt(ts, ts))
			t := gui.Thumb{Radius: 10, Fit: gui.FitCover}
			if ok {
				t.Image = img
			} else {
				t.Loading = true
			}
			t.Layout(tg, th)
		}()
		if cur {
			r := rectWH(ts, ts)
			gui.Border(gtx, r.Inset(-gtx.Dp(3)), 13, 2, th.Pal.Action)
		}
		o.Pop()
	}
}

// drawPanels numbers the panels of a storyboard sheet drawn in r and rings
// the panel under the pointer (a click zooms into it).
func (l *Lightbox) drawPanels(gtx C, th *gui.Theme, it LightboxItem, r fRect) {
	panels := l.panelsFor(it)
	bs := gtx.Dp(26)
	if r.Dx() < float32(gtx.Dp(420)) {
		bs = gtx.Dp(20)
	}
	for i, f := range panels {
		pr := fRect{Min: f32.Pt(r.Min.X+f.x0*r.Dx(), r.Min.Y+f.y0*r.Dy()), Max: f32.Pt(r.Min.X+f.x1*r.Dx(), r.Min.Y+f.y1*r.Dy())}
		if l.zoom <= 1.01 && pr.contains(l.ptr) && !l.pressed {
			ir := image.Rect(int(pr.Min.X), int(pr.Min.Y), int(pr.Max.X), int(pr.Max.Y))
			gui.Border(gtx, ir, 4, 2, gui.WithAlpha(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 0.85))
		}
		m := float32(gtx.Dp(6))
		o := op.Offset(pt(int(pr.Min.X+m), int(pr.Min.Y+m))).Push(gtx.Ops)
		gui.Shadow(gtx, th, rectWH(bs, bs), gui.RadiusPill, gui.ElevControl)
		gui.FillCircle(gtx, pxDp(gtx, bs), color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xEE})
		lg := gtx
		lg.Constraints = layout.Constraints{Max: pt(bs, bs)}
		st := th.Caption.Weighted(gui.FontSemiBold)
		if bs >= gtx.Dp(26) {
			st = th.Small.Weighted(gui.FontSemiBold)
		}
		d, call := record(lg, label(th, st, gui.LightPalette.Text, 1, fmt.Sprint(i+1)))
		drawAt(gtx, (bs-d.Size.X)/2, (bs-d.Size.Y)/2, call)
		o.Pop()
	}
}

// panelsFor returns the panels of a sheet in reading order as the sheet
// really has them (media.DetectGrid in the background, cached per file
// version) — never an assumed 3×3 over a different layout; none until found
// and none for a picture without a grid.
func (l *Lightbox) panelsFor(it LightboxItem) []frac {
	n := max(1, it.Grid)
	key := fmt.Sprintf("%s@%d#%d", it.Path, it.Ver, n)
	l.mu.Lock()
	if l.panels == nil {
		l.panels, l.finding = map[string][]frac{}, map[string]bool{}
	}
	if p, ok := l.panels[key]; ok {
		l.mu.Unlock()
		return p
	}
	start := !l.finding[key]
	l.finding[key] = true
	l.mu.Unlock()
	if start {
		path := it.Path
		work := func() {
			p := findPanels(path)
			l.mu.Lock()
			l.panels[key] = p
			delete(l.finding, key)
			l.mu.Unlock()
			if l.Invalidate != nil {
				l.Invalidate()
			}
		}
		if l.Background != nil {
			l.Background(work)
		} else {
			go work()
		}
	}
	return nil
}

// findPanels detects the panels of the sheet at path (media.DetectGrid: the
// rows and columns it really has); nil when it has no grid or cannot be read.
func findPanels(path string) []frac {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	img, _, err := media.DecodeImage(data)
	if err != nil {
		return nil
	}
	g := media.DetectGrid(img)
	if g.Rows*g.Cols <= 1 {
		return nil
	}
	rects := g.Rects
	b := img.Bounds()
	w, h := float32(b.Dx()), float32(b.Dy())
	out := make([]frac, len(rects))
	for i, r := range rects {
		r = r.Sub(b.Min)
		out[i] = frac{float32(r.Min.X) / w, float32(r.Min.Y) / h, float32(r.Max.X) / w, float32(r.Max.Y) / h}
	}
	return out
}

// glassBtn is a round translucent button for dark overlays (the lightbox
// chrome). Read Clicked before Layout.
type glassBtn struct {
	widget.Clickable
	hover gui.Anim
}

func (b *glassBtn) Layout(gtx C, th *gui.Theme, ic *gui.Icon, size unit.Dp, tip string, disabled bool) D {
	px := gtx.Dp(size)
	if disabled {
		gtx = gtx.Disabled()
	}
	b.Clickable.Layout(gtx, func(gtx C) D {
		defer clip.Ellipse(rectWH(px, px)).Push(gtx.Ops).Pop()
		if !disabled {
			pointer.CursorPointer.Add(gtx.Ops)
		}
		return D{Size: pt(px, px)}
	})
	hov := b.Clickable.Hovered() && !disabled
	if hov && tip != "" {
		th.Tooltip(gtx, b, b.Clickable.Pressed(), tip)
	}
	b.hover.Duration = gui.DurHover
	h := b.hover.Animate(gtx, b2f(hov))
	bg := float32(0.10) + 0.10*h
	if b.Clickable.Pressed() && !disabled {
		bg += 0.08
	}
	// A dark base keeps the light icon readable over a light picture; the
	// white wash shapes the button on the dark scrim.
	gui.FillCircle(gtx, size, gui.WithAlpha(lbScrim, 0.45))
	gui.FillCircle(gtx, size, gui.WithAlpha(color.NRGBA{R: 255, G: 255, B: 255, A: 255}, bg))
	fg := lbText
	if disabled {
		fg = gui.WithAlpha(fg, 0.35)
	}
	is := unit.Dp(float32(size) * 0.5)
	ip := gtx.Dp(is)
	o := op.Offset(pt((px-ip)/2, (px-ip)/2)).Push(gtx.Ops)
	ic.Layout(gtx, is, fg)
	o.Pop()
	return D{Size: pt(px, px)}
}
