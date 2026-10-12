package core

import (
	"errors"
	"fmt"
	"image"
	"math"
	"sync"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/gui"
	"ytcartell/internal/player"
)

// ---- serial media queue -------------------------------------------------------------------

// Player control (spawning ffmpeg, opening the sound device) can block for a
// moment: it runs in order on one background goroutine, never on the UI.
var (
	mediaMu sync.Mutex
	mediaQ  []func()
	mediaOn bool
)

func runMedia(fn func()) {
	mediaMu.Lock()
	mediaQ = append(mediaQ, fn)
	if mediaOn {
		mediaMu.Unlock()
		return
	}
	mediaOn = true
	mediaMu.Unlock()
	go func() {
		for {
			mediaMu.Lock()
			if len(mediaQ) == 0 {
				mediaOn = false
				mediaMu.Unlock()
				return
			}
			f := mediaQ[0]
			mediaQ = mediaQ[1:]
			mediaMu.Unlock()
			f()
		}
	}()
}

// StopMedia stops every sound and closes every video player (route changes,
// window close).
func (c *Ctx) StopMedia() {
	for v := range c.videos {
		v.close()
		delete(c.videos, v)
	}
	a := c.Audio.(audioBackend)
	a.clearPending()
	runMedia(func() { player.Audio().Stop() })
}

// ---- audio ---------------------------------------------------------------------------------

// audioBackend adapts player.Audio() to gui.AudioBackend (AudioPill).
type audioBackend struct{ c *Ctx }

var (
	pendMu  sync.Mutex
	pending string // src whose playback was requested but has not started yet
)

func (a audioBackend) clearPending() {
	pendMu.Lock()
	pending = ""
	pendMu.Unlock()
}

// Play starts src (stopping whatever plays). Errors are toasted.
func (a audioBackend) Play(src string, onDone func()) error {
	return a.PlayRange(src, 0, 0, onDone)
}

// PlayRange plays the part of src from at (seconds) lasting dur seconds
// (dur ≤ 0 = to the end), e.g. a trimmed voice line. Errors are toasted.
func (a audioBackend) PlayRange(src string, at, dur float64, onDone func()) error {
	if a.c.Offline {
		return errors.New("відтворення вимкнене")
	}
	pendMu.Lock()
	pending = src
	pendMu.Unlock()
	c := a.c
	runMedia(func() {
		pendMu.Lock()
		still := pending == src
		pendMu.Unlock()
		if !still {
			return // stopped or replaced before it started
		}
		err := player.Audio().PlayRange(c.E.FF(), src, at, dur, func() {
			if onDone != nil {
				onDone()
			}
			c.Invalidate()
		})
		pendMu.Lock()
		if pending == src {
			pending = ""
		}
		pendMu.Unlock()
		if err != nil {
			c.Error(fmt.Errorf("не вдалося відтворити звук: %w", err))
		}
		c.Invalidate()
	})
	return nil
}

// Stop silences the current sound.
func (a audioBackend) Stop() {
	a.clearPending()
	runMedia(func() { player.Audio().Stop() })
}

// Playing reports whether src plays (or is about to).
func (a audioBackend) Playing(src string) bool {
	pendMu.Lock()
	p := pending
	pendMu.Unlock()
	if p != "" {
		return p == src
	}
	return player.Audio().Playing(src)
}

// PlayRange plays part of an audio file through the shared player (see
// audioBackend.PlayRange); c.Audio.Playing(src) reports it like Play.
func (c *Ctx) PlayRange(src string, at, dur float64, onDone func()) error {
	return audioBackend{c}.PlayRange(src, at, dur, onDone)
}

// ---- video -----------------------------------------------------------------------------------

// Video is the state of one inline video player (a scene clip, the final
// render). The ffmpeg-backed player opens on the first press of Play and
// closes when the card is not laid out for a frame (scrolled away, route
// changed), so dozens of cards cost nothing until one plays.
type Video struct {
	play gui.Clickable
	area gui.Clickable
	p    *player.Video
	path string
	ver  int64
	img  image.Image
	op   paint.ImageOp
	seek bool // pointer tag of the time bar
}

// VideoStyle configures a Video for one frame.
type VideoStyle struct {
	Path      string  // absolute mp4 path ("" = poster only, no play button)
	Ver       int64   // Asset.UpdatedAt: a new version reopens the player
	Poster    string  // absolute image shown until playback (the scene frame)
	PosterVer int64   //
	Ratio     float32 // width/height of the box (16/9, 9/16 …); 0 = 16/9
	Radius    unit.Dp // 0 = gui.RadiusInner; negative = square corners
	Compact   bool    // small play button, no time bar (dense grids)
	// Overlay (optional) is drawn over the picture (status pills, badges).
	Overlay layout.Widget
}

func (v *Video) close() {
	if v.p != nil {
		p := v.p
		v.p, v.img, v.op = nil, nil, paint.ImageOp{}
		runMedia(p.Close)
	}
}

// Playing reports whether this card plays right now.
func (v *Video) Playing() bool { return v.p != nil && v.p.Playing() }

// Layout draws the player box: the poster (or the current frame), a round
// play/pause button and, unless compact, a time bar you can click to seek.
func (v *Video) Layout(gtx C, c *Ctx, st VideoStyle) D {
	th := c.Th
	if st.Ratio <= 0 {
		st.Ratio = 16.0 / 9
	}
	if v.p != nil && (st.Path != v.path || st.Ver != v.ver) {
		v.close()
	}
	v.path, v.ver = st.Path, st.Ver
	if v.p != nil {
		c.videos[v] = c.frame
	}

	if v.play.Clicked(gtx) || v.area.Clicked(gtx) {
		v.toggle(c, gtx)
	}

	// Picture: the decoded frame while a player is open, else the poster.
	var pic paint.ImageOp
	if v.p != nil {
		if f := v.p.Frame(); f != nil {
			if f != v.img {
				v.img, v.op = f, paint.NewImageOp(f)
			}
			pic = v.op
		}
	}
	thumb := gui.Thumb{Ratio: st.Ratio, Radius: st.Radius, Fit: gui.FitContain, Placeholder: gui.IconFilm}
	if pic.Size().X == 0 && st.Poster != "" && c.Img != nil {
		longest := gtx.Constraints.Max.X
		if st.Ratio < 1 {
			longest = int(float32(longest) / st.Ratio)
		}
		if img, _, ok := c.Img.Get(st.Poster, st.PosterVer, gui.ImgBucket(longest)); ok {
			pic = img
		} else {
			thumb.Loading = true
		}
		thumb.Fit = gui.FitCover
	}
	thumb.Image = pic
	thumb.Overlay = func(gtx C) D {
		size := gtx.Constraints.Max
		if st.Path != "" {
			// The whole picture toggles playback.
			v.area.Layout(gtx, func(gtx C) D { return D{Size: size} })
			v.drawControls(gtx, c, st, size)
		}
		if st.Overlay != nil {
			st.Overlay(gtx)
		}
		return D{Size: size}
	}
	return thumb.Layout(gtx, th)
}

func (v *Video) toggle(c *Ctx, gtx C) {
	if c.Offline || v.path == "" {
		return
	}
	if v.p == nil {
		// Stop the other players first (one picture moving at a time).
		for o := range c.videos {
			if o != v {
				o.close()
				delete(c.videos, o)
			}
		}
		maxW := min(1280, max(320, gtx.Constraints.Max.X))
		v.p = player.NewVideo(c.E.FF(), v.path, maxW, c.Invalidate)
		c.videos[v] = c.frame
		p := v.p
		runMedia(p.Play)
		return
	}
	p := v.p
	runMedia(p.Toggle)
}

func (v *Video) drawControls(gtx C, c *Ctx, st VideoStyle, size image.Point) {
	th := c.Th
	playing := v.p != nil && v.p.Playing()
	if v.p != nil {
		if err := v.p.Err(); err != nil {
			gui.Text{Style: th.Caption, Color: th.Pal.OverlayInk, MaxLines: 2}.Layout(gtx, th, "Не вдалося відтворити: "+ErrorText(err))
		}
	}
	// Round play/pause button in the centre (hidden while playing unless hovered).
	bs := gtx.Dp(56)
	if st.Compact {
		bs = gtx.Dp(40)
	}
	if !playing || v.area.Hovered() || v.play.Hovered() {
		off := op.Offset(image.Pt((size.X-bs)/2, (size.Y-bs)/2)).Push(gtx.Ops)
		bg := gtx
		bg.Constraints = layout.Exact(image.Pt(bs, bs))
		v.play.Layout(bg, func(gtx C) D {
			rr := clip.Ellipse(image.Rectangle{Max: image.Pt(bs, bs)}).Push(gtx.Ops)
			paint.Fill(gtx.Ops, th.Pal.Overlay)
			rr.Pop()
			ic := gui.IconPlay
			if playing {
				ic = gui.IconPause
			}
			is := unit.Dp(22)
			if st.Compact {
				is = 18
			}
			ip := gtx.Dp(is)
			o := op.Offset(image.Pt((bs-ip)/2, (bs-ip)/2)).Push(gtx.Ops)
			ic.Layout(gtx, is, th.Pal.OverlayInk)
			o.Pop()
			return D{Size: image.Pt(bs, bs)}
		})
		off.Pop()
	}
	if st.Compact || v.p == nil {
		return
	}
	// Time bar along the bottom edge: click/drag to seek.
	dur := v.p.Duration()
	pos := v.p.Position()
	if dur <= 0 {
		return
	}
	pad := gtx.Dp(12)
	barH := gtx.Dp(28)
	y := size.Y - barH - gtx.Dp(8)
	area := image.Rect(pad, y, size.X-pad, y+barH)
	st2 := clip.UniformRRect(area, barH/2).Push(gtx.Ops)
	paint.Fill(gtx.Ops, th.Pal.Overlay)
	st2.Pop()
	label := fmtClock(pos) + " / " + fmtClock(dur)
	lw := th.TextWidth(gtx, th.Caption, label) + gtx.Dp(16)
	track := image.Rect(area.Min.X+gtx.Dp(14), y+barH/2-gtx.Dp(2), area.Max.X-lw, y+barH/2+gtx.Dp(2))
	if track.Dx() > gtx.Dp(40) {
		tr := clip.UniformRRect(track, gtx.Dp(2)).Push(gtx.Ops)
		paint.Fill(gtx.Ops, th.Pal.Track)
		tr.Pop()
		frac := float32(math.Min(1, pos/dur))
		done := track
		done.Max.X = track.Min.X + int(float32(track.Dx())*frac)
		dr := clip.UniformRRect(done, gtx.Dp(2)).Push(gtx.Ops)
		paint.Fill(gtx.Ops, th.Pal.Action)
		dr.Pop()
		// Seek input on the track (taller hit area).
		hit := image.Rect(track.Min.X, y, track.Max.X, y+barH)
		v.seekInput(gtx, hit, dur)
	}
	lo := op.Offset(image.Pt(area.Max.X-lw+gtx.Dp(4), y+(barH-gtx.Sp(12)*4/3)/2)).Push(gtx.Ops)
	gui.Text{Style: th.Caption, Color: th.Pal.OverlayInk, MaxLines: 1}.Layout(gtx, th, label)
	lo.Pop()
	if playing {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(250 * time.Millisecond)})
	}
}

// seekInput turns presses and drags on the time bar into seeks: a press
// seeks at once, a drag moves the bar and seeks where it is released.
func (v *Video) seekInput(gtx C, hit image.Rectangle, dur float64) {
	tag := &v.seek
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok || v.p == nil {
			continue
		}
		frac := float64((e.Position.X - float32(hit.Min.X)) / float32(max(1, hit.Dx())))
		frac = math.Max(0, math.Min(1, frac))
		p, at := v.p, frac*dur
		switch e.Kind {
		case pointer.Press, pointer.Release:
			runMedia(func() { p.Seek(at) })
		}
	}
	defer clip.Rect(hit).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
	pointer.CursorPointer.Add(gtx.Ops)
}

// fmtClock formats seconds as m:ss.
func fmtClock(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	s := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
