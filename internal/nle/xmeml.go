package nle

// Final Cut Pro 7 XML ("xmeml", version 4) — Premiere Pro's interchange
// format (File → Import; Premiere reads FCP 7 XML and writes it with a few
// Adobe tags), also read by DaVinci Resolve (File → Import → Timeline).
//
// One sequence at the timeline rate; video tracks V1 (scenes) and V2
// (texts), audio tracks in the timeline's order. Times are frames of the
// sequence: start/in count from 0, end/out are exclusive. A clip next to a
// transition has start or end −1 (the transitionitem says where it is),
// its in/out stay on the cut, so the handles come from the media. Levels
// are linear gains of the "Audio Levels" filter (1 = 0 dB), keyframes in
// media frames; Basic Motion scale is percent, its centre is relative to
// the frame (±0.5 = the edges … measured in half frame sizes ±1 in FCP 7;
// see centre()). Paths are absolute file://localhost URLs (Premiere and
// Resolve offer to relink when the folder moved).

import (
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"
)

// XMEMLOptions for the Premiere file.
type XMEMLOptions struct{}

// fileURLLocalhost is the xmeml form of a path: file://localhost/C%3a/….
func fileURLLocalhost(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := (&url.URL{Scheme: "file", Host: "localhost", Path: p}).String()
	// Premiere writes the drive colon escaped ("C%3a"); both forms import.
	if len(p) > 2 && p[2] == ':' {
		u = strings.Replace(u, p[:3], p[:2]+"%3a", 1)
	}
	return u
}

func xRate(n *node, r Rate) {
	rt := n.child("rate")
	rt.leafInt("timebase", r.Timebase())
	if r.NTSC() {
		rt.leaf("ntsc", "TRUE")
	} else {
		rt.leaf("ntsc", "FALSE")
	}
}

func xTimecode(n *node, r Rate) {
	tc := n.child("timecode")
	xRate(tc, r)
	tc.leaf("string", "00:00:00:00")
	tc.leafInt("frame", 0)
	tc.leaf("displayformat", "NDF")
}

type xmemlWriter struct {
	t       *Timeline
	files   map[*Media]string
	written map[*Media]bool
	uses    map[*Media]int64 // furthest frame of a picture used
	clipID  int
	ids     map[*Clip]string
}

// XMEML renders t as an xmeml v4 document.
func XMEML(t *Timeline, _ XMEMLOptions) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	w := &xmemlWriter{t: t, files: map[*Media]string{}, written: map[*Media]bool{}, uses: map[*Media]int64{}, ids: map[*Clip]string{}}
	for i, m := range t.Media() {
		w.files[m] = fmt.Sprintf("file-%d", i+1)
	}
	for _, tr := range t.Tracks() {
		for _, c := range tr.Clips {
			if c.Media.Kind == Picture {
				w.uses[c.Media] = max(w.uses[c.Media], c.In+c.Len)
			}
		}
	}
	// Clip ids first, so links can point forward.
	for ti := range t.Video {
		for i := range t.Video[ti].Clips {
			w.idOf(&t.Video[ti].Clips[i])
		}
	}
	for ti := range t.Audio {
		for i := range t.Audio[ti].Clips {
			w.idOf(&t.Audio[ti].Clips[i])
		}
	}

	seq := el("sequence", "id", "sequence-1")
	seq.leaf("uuid", uuidFrom("seq:"+t.Name))
	seq.leafInt("duration", t.Length)
	xRate(seq, t.Rate)
	seq.leaf("name", t.Name)
	media := seq.child("media")
	video := media.child("video")
	f := video.child("format").child("samplecharacteristics")
	xRate(f, t.Rate)
	f.leafInt("width", int64(t.Width)).leafInt("height", int64(t.Height))
	f.leaf("anamorphic", "FALSE").leaf("pixelaspectratio", "square").leaf("fielddominance", "none")
	for ti, tr := range t.Video {
		video.add(w.track(tr, ti, true))
	}
	audio := media.child("audio")
	audio.leafInt("numOutputChannels", 2)
	af := audio.child("format").child("samplecharacteristics")
	af.leafInt("depth", 16).leafInt("samplerate", int64(t.sampleRate()))
	for ti, tr := range t.Audio {
		audio.add(w.track(tr, ti, false))
	}
	xTimecode(seq, t.Rate)
	for _, m := range t.Markers {
		mk := seq.child("marker")
		mk.leaf("name", m.Name).leaf("comment", "").leafInt("in", m.At).leafInt("out", -1)
	}
	root := el("xmeml", "version", "4")
	root.add(seq)
	return document(root, "<!DOCTYPE xmeml>"), nil
}

func (w *xmemlWriter) idOf(c *Clip) string {
	if id, ok := w.ids[c]; ok {
		return id
	}
	w.clipID++
	id := fmt.Sprintf("clipitem-%d", w.clipID)
	w.ids[c] = id
	return id
}

func (w *xmemlWriter) track(tr Track, index int, video bool) *node {
	n := el("track")
	trans := map[int64]Transition{}
	for _, x := range tr.Transitions {
		trans[x.At] = x
	}
	for i := range tr.Clips {
		c := &tr.Clips[i]
		if x, ok := trans[c.Start]; ok && i > 0 && tr.Clips[i-1].End() == c.Start {
			n.add(w.transition(x, video))
		}
		var before, after *Transition
		if x, ok := trans[c.Start]; ok && i > 0 && tr.Clips[i-1].End() == c.Start {
			before = &x
		}
		if x, ok := trans[c.End()]; ok && i+1 < len(tr.Clips) && tr.Clips[i+1].Start == c.End() {
			after = &x
		}
		n.add(w.clipitem(c, before, after, index, video))
	}
	n.leaf("enabled", "TRUE").leaf("locked", "FALSE")
	return n
}

// clipitem writes a clip. Next to a transition (before/after) its start or
// end is −1 and its in/out reach under the transition (FCP 7's rule, which
// Resolve and Premiere follow): the clip spans from the transition's first
// frame / to its last.
func (w *xmemlWriter) clipitem(c *Clip, before, after *Transition, trackIndex int, video bool) *node {
	t := w.t
	n := el("clipitem", "id", w.ids[c])
	n.leaf("masterclipid", "masterclip-"+strings.TrimPrefix(w.files[c.Media], "file-"))
	n.leaf("name", c.Name)
	n.leaf("enabled", "TRUE")
	n.leafInt("duration", w.mediaFrames(c.Media))
	xRate(n, t.Rate)
	start, end := c.Start, c.End()
	in, out := c.In+w.base(c), c.In+w.base(c)+c.Len
	if before != nil {
		start = -1
		in -= before.At - before.From()
	}
	if after != nil {
		end = -1
		out += after.To() - after.At
	}
	n.leafInt("start", start).leafInt("end", end)
	n.leafInt("in", in).leafInt("out", out)
	if c.Media.Kind == Picture {
		n.leaf("stillframe", "TRUE")
	}
	n.add(w.file(c.Media))
	if !video {
		st := n.child("sourcetrack")
		st.leaf("mediatype", "audio").leafInt("trackindex", 1)
	}
	if video {
		if len(c.Motion) > 0 {
			n.add(w.motion(c))
		}
		if c.FadeIn > 0 || c.FadeOut > 0 {
			n.add(w.opacity(c))
		}
	} else if c.Gain != 0 || len(c.Levels) > 0 || c.FadeIn > 0 || c.FadeOut > 0 {
		n.add(w.levels(c))
	}
	// Link a scene's picture with its own sound (A1) so they move together.
	if partner := w.partner(c, video); partner != nil {
		for _, pair := range []struct {
			c     *Clip
			video bool
		}{{c, video}, {partner, !video}} {
			l := n.child("link")
			l.leaf("linkclipref", w.ids[pair.c])
			if pair.video {
				l.leaf("mediatype", "video").leafInt("trackindex", 1)
			} else {
				l.leaf("mediatype", "audio").leafInt("trackindex", 1)
			}
			l.leafInt("clipindex", w.clipIndex(pair.c, pair.video))
		}
	}
	_ = trackIndex
	return n
}

// partner is the A1 clip of a V1 scene clip (or the V1 clip of an A1 one).
func (w *xmemlWriter) partner(c *Clip, video bool) *Clip {
	t := w.t
	if len(t.Video) == 0 || len(t.Audio) == 0 || c.Media.Kind != Movie {
		return nil
	}
	from, to := &t.Audio[0], &t.Video[0]
	if video {
		from, to = &t.Video[0], &t.Audio[0]
	}
	mine := false
	for i := range from.Clips {
		mine = mine || &from.Clips[i] == c
	}
	if !mine {
		return nil
	}
	for i := range to.Clips {
		o := &to.Clips[i]
		if o.Media == c.Media && o.Start == c.Start && o.Len == c.Len {
			return o
		}
	}
	return nil
}

func (w *xmemlWriter) clipIndex(c *Clip, video bool) int64 {
	tr := w.t.Audio[0]
	if video {
		tr = w.t.Video[0]
	}
	for i := range tr.Clips {
		if &tr.Clips[i] == c {
			return int64(i + 1)
		}
	}
	return 0
}

// stillBase is where a still's frames start (10 s in): a transition next
// to it reaches under it from both sides without negative in points.
func (w *xmemlWriter) stillBase() int64 { return w.t.Rate.Frames(10) }

// base is the offset of a clip's source frames in the file (stills only).
func (w *xmemlWriter) base(c *Clip) int64 {
	if c.Media.Kind == Picture {
		return w.stillBase()
	}
	return 0
}

// mediaFrames is the file's length in sequence frames.
func (w *xmemlWriter) mediaFrames(m *Media) int64 {
	if m.Kind == Picture {
		return max(1, w.uses[m]) + 2*w.stillBase()
	}
	return max(1, w.t.Rate.Frames(m.Duration))
}

func (w *xmemlWriter) file(m *Media) *node {
	id := w.files[m]
	if w.written[m] {
		return el("file", "id", id)
	}
	w.written[m] = true
	t := w.t
	f := el("file", "id", id)
	f.leaf("name", m.Name)
	f.leaf("pathurl", fileURLLocalhost(m.Abs))
	xRate(f, m.rate(t.Rate))
	if m.Kind == Picture {
		f.leafInt("duration", w.mediaFrames(m))
	} else {
		f.leafInt("duration", m.rate(t.Rate).Frames(m.Duration))
	}
	xTimecode(f, m.rate(t.Rate))
	md := f.child("media")
	if m.HasVideo() {
		sc := md.child("video").child("samplecharacteristics")
		xRate(sc, m.rate(t.Rate))
		sc.leafInt("width", int64(m.Width)).leafInt("height", int64(m.Height))
		sc.leaf("anamorphic", "FALSE").leaf("pixelaspectratio", "square").leaf("fielddominance", "none")
	}
	if m.Kind == Sound || m.HasAudio {
		a := md.child("audio")
		sc := a.child("samplecharacteristics")
		sc.leafInt("depth", 16).leafInt("samplerate", int64(m.sampleRate()))
		a.leafInt("channelcount", int64(m.channels()))
	}
	return f
}

func xEffect(n *node, name, id, category, typ, media string) *node {
	e := n.child("effect")
	e.leaf("name", name).leaf("effectid", id).leaf("effectcategory", category).leaf("effecttype", typ).leaf("mediatype", media)
	return e
}

func (w *xmemlWriter) transition(x Transition, video bool) *node {
	t := w.t
	n := el("transitionitem")
	xRate(n, t.Rate)
	n.leafInt("start", x.From()).leafInt("end", x.To())
	n.leaf("alignment", "center")
	// Premiere's cut point inside the transition (254016000000 ticks per second).
	n.leafInt("cutPointTicks", int64(math.Round(t.Rate.Seconds(x.At-x.From())*254016000000)))
	if !video {
		xEffect(n, "Cross Fade (+3dB)", "KGAudioTransCrossFade3dB", "", "transition", "audio")
		return n
	}
	switch x.Kind {
	case Dissolve:
		xEffect(n, "Cross Dissolve", "Cross Dissolve", "Dissolve", "transition", "video")
	default:
		e := xEffect(n, "Dip to Color Dissolve", "Dip to Color Dissolve", "Dissolve", "transition", "video")
		p := e.child("parameter")
		p.leaf("parameterid", "color").leaf("name", "Color")
		v := p.child("value")
		c := int64(0)
		if x.Kind == DipWhite {
			c = 255
		}
		v.leafInt("alpha", 255).leafInt("red", c).leafInt("green", c).leafInt("blue", c)
	}
	return n
}

// motion is the Basic Motion filter with scale/centre/rotation keyframes.
func (w *xmemlWriter) motion(c *Clip) *node {
	f := el("filter")
	e := xEffect(f, "Basic Motion", "basic", "motion", "motion", "video")
	when := func(k Key) int64 { return c.In + w.base(c) + k.At }
	k0 := c.Motion[0]
	sc := e.child("parameter", "authoringApp", "PremierePro")
	sc.leaf("parameterid", "scale").leaf("name", "Scale").leafInt("valuemin", 0).leafInt("valuemax", 1000).leaf("value", ftoa(k0.Scale*100, 3))
	rot := e.child("parameter", "authoringApp", "PremierePro")
	rot.leaf("parameterid", "rotation").leaf("name", "Rotation").leafInt("valuemin", -8640).leafInt("valuemax", 8640).leaf("value", ftoa(k0.Rotation, 3))
	ce := e.child("parameter", "authoringApp", "PremierePro")
	ce.leaf("parameterid", "center").leaf("name", "Center")
	h, v := w.centre(k0)
	cv := ce.child("value")
	cv.leaf("horiz", h).leaf("vert", v)
	if len(c.Motion) > 1 {
		for _, k := range c.Motion {
			sc.child("keyframe").leafInt("when", when(k)).leaf("value", ftoa(k.Scale*100, 3))
			rot.child("keyframe").leafInt("when", when(k)).leaf("value", ftoa(k.Rotation, 3))
			h, v := w.centre(k)
			kv := ce.child("keyframe")
			kv.leafInt("when", when(k))
			kv.child("value").leaf("horiz", h).leaf("vert", v)
		}
	}
	return f
}

// centre is Basic Motion's centre: the offset as a share of the frame
// (−0.5 … 0.5 = edge to edge, +vert down), Premiere's normalised form.
func (w *xmemlWriter) centre(k Key) (string, string) {
	return ftoa(k.X/float64(w.t.Width), 5), ftoa(k.Y/float64(w.t.Height), 5)
}

// opacity is the Opacity filter with the fade keyframes.
func (w *xmemlWriter) opacity(c *Clip) *node {
	f := el("filter")
	e := xEffect(f, "Opacity", "opacity", "motion", "motion", "video")
	p := e.child("parameter", "authoringApp", "PremierePro")
	p.leaf("parameterid", "opacity").leaf("name", "opacity").leafInt("valuemin", 0).leafInt("valuemax", 100).leafInt("value", 100)
	key := func(at int64, v int64) {
		k := p.child("keyframe")
		k.leafInt("when", c.In+w.base(c)+at).leafInt("value", v)
	}
	if c.FadeIn > 0 {
		key(0, 0)
		key(min(c.FadeIn, c.Len), 100)
	}
	if c.FadeOut > 0 {
		key(max(0, c.Len-c.FadeOut), 100)
		key(c.Len, 0)
	}
	return f
}

// levels is the Audio Levels filter: the gain, keyframes, fades.
func (w *xmemlWriter) levels(c *Clip) *node {
	f := el("filter")
	e := xEffect(f, "Audio Levels", "audiolevels", "audiolevels", "audiolevels", "audio")
	p := e.child("parameter", "authoringApp", "PremierePro")
	p.leaf("parameterid", "level").leaf("name", "Level").leafInt("valuemin", 0).leaf("valuemax", "3.98109")
	p.leaf("value", ftoa(gainOf(c.levelAt(0)), 6))
	type kf struct {
		at int64
		g  float64
	}
	var keys []kf
	for _, l := range c.Levels {
		keys = append(keys, kf{l.At, gainOf(l.DB)})
	}
	if c.FadeIn > 0 {
		keys = append([]kf{{0, 0}, {min(c.FadeIn, c.Len), gainOf(c.levelAt(min(c.FadeIn, c.Len)))}}, keys...)
	}
	if c.FadeOut > 0 {
		keys = append(keys, kf{max(0, c.Len-c.FadeOut), gainOf(c.levelAt(max(0, c.Len-c.FadeOut)))}, kf{c.Len, 0})
	}
	for _, k := range keys {
		kn := p.child("keyframe")
		kn.leafInt("when", c.In+k.at).leaf("value", ftoa(k.g, 6))
	}
	return f
}

// uuidFrom is a stable RFC 4122-shaped id derived from s.
func uuidFrom(s string) string {
	h := strings.ToLower(uidOf(s))
	return h[0:8] + "-" + h[8:12] + "-4" + h[13:16] + "-a" + h[17:20] + "-" + h[20:32]
}
