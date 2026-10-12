package nle

// FCPXML 1.10 — imported by DaVinci Resolve 18 and newer (File → Import →
// Timeline; Resolve 19.1.1 reads up to 1.13, Resolve 21 up to 1.14) and by
// Final Cut Pro 10.5 and newer (File → Import → XML; FCP 11 writes 1.12/1.13,
// FCP 12 writes 1.14, every version imports the older ones). 1.10 is the
// newest version the oldest supported Resolve (18) still reads.
//
// Layout: the scenes are the primary storyline (asset-clip for movies,
// video for stills; transitions between them, a gap where nothing plays);
// texts, voice lines and music are connected clips anchored to the
// storyline element under their first frame (lane 1… above, −1… below).
// Times are exact rationals: the storyline in timeline frames, a clip's
// start (its source in) in its media's own frames. Positions in
// adjust-transform are percent of the frame height from the centre, Y up;
// rotation is counter-clockwise. Media are referenced by absolute file://
// URLs (Final Cut Pro and Resolve do not resolve relative ones).

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// FCPXMLVersion is the version written.
const FCPXMLVersion = "1.10"

// Effect uids of Final Cut Pro's built-in transitions (Resolve maps them to
// its Cross Dissolve / Dip To Color Dissolve).
const (
	fcpCrossDissolveUID = "FxPlug:4731E73A-8DAC-4113-9A30-AE85B1761265"
	fcpFadeToColorUID   = ".../Transitions.localized/Dissolves.localized/Fade To Color.localized/Fade To Color.motn"
	fcpAudioCrossfade   = "FFAudioTransition"
)

// FCPXMLOptions name the containers Final Cut Pro shows and pick the
// flavour of the file.
type FCPXMLOptions struct {
	Event   string // event name ("" = "YT Cartell Studio")
	Project string // project name ("" = the timeline's name)
	// StaticStills writes no keyframes on still pictures: DaVinci Resolve
	// imports only their first value (a text fading in would stay at
	// opacity 0, a Ken Burns move would freeze).
	StaticStills bool
}

// rational time helpers -------------------------------------------------------------------

// secs is n frames of rate r as an exact rational number of seconds.
func secs(n int64, r Rate) *big.Rat {
	if !r.OK() {
		r = Rate24
	}
	return new(big.Rat).SetFrac(big.NewInt(n*r.Den), big.NewInt(r.Num))
}

// fcpTime formats a rational time: "0s", "5s", "1001/24000s".
func fcpTime(t *big.Rat) string {
	if t.Sign() == 0 {
		return "0s"
	}
	if t.IsInt() {
		return t.Num().String() + "s"
	}
	return t.Num().String() + "/" + t.Denom().String() + "s"
}

func ratAdd(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }

// mediaStart is a clip's source in (timeline frames) on its media's own
// frame grid — what FCPXML's start attribute holds.
func mediaStart(c Clip, t *Timeline) *big.Rat {
	if c.Media.Kind != Movie {
		return secs(c.In, t.Rate)
	}
	mr := c.Media.rate(t.Rate)
	if mr == t.Rate {
		return secs(c.In, t.Rate)
	}
	return secs(mr.Frames(t.Rate.Seconds(c.In)), mr)
}

// fcpFormatName is Final Cut Pro's name for the standard formats ("" = none:
// width, height and frameDuration describe it).
func fcpFormatName(w, h int, r Rate) string {
	var size string
	switch {
	case w == 1920 && h == 1080:
		size = "1080p"
	case w == 1280 && h == 720:
		size = "720p"
	case w == 3840 && h == 2160:
		size = "3840x2160p"
	default:
		return ""
	}
	rate := map[Rate]string{{24000, 1001}: "2398", {24, 1}: "24", {25, 1}: "25", {30000, 1001}: "2997", {30, 1}: "30",
		{50, 1}: "50", {60000, 1001}: "5994", {60, 1}: "60"}[r]
	if rate == "" {
		return ""
	}
	return "FFVideoFormat" + size + rate
}

func uidOf(s string) string {
	h := md5.Sum([]byte(s))
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

// FCPXML renders t as an FCPXML 1.10 document.
func FCPXML(t *Timeline, o FCPXMLOptions) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	if o.Event == "" {
		o.Event = "YT Cartell Studio"
	}
	if o.Project == "" {
		o.Project = t.Name
	}
	w := &fcpWriter{t: t, o: o, formats: map[string]string{}, assets: map[*Media]string{}}
	res := el("resources")
	w.res = res
	seqFmt := w.format(t.Width, t.Height, t.Rate, false)

	// Effects used by the transitions.
	if len(t.Video) > 0 {
		for _, x := range t.Video[0].Transitions {
			switch x.Kind {
			case Dissolve:
				w.effect(&w.cross, "Cross Dissolve", fcpCrossDissolveUID)
			default:
				w.effect(&w.fade, "Fade To Color", fcpFadeToColorUID)
			}
			w.effect(&w.xfade, "Audio Crossfade", fcpAudioCrossfade)
		}
	}
	for _, m := range t.Media() {
		w.asset(m)
	}

	spine := w.spine()
	seq := el("sequence", "format", seqFmt, "duration", fcpTime(secs(t.Length, t.Rate)),
		"tcStart", "0s", "tcFormat", "NDF", "audioLayout", "stereo", "audioRate", audioHz(t.sampleRate()))
	seq.add(spine)
	root := el("fcpxml", "version", FCPXMLVersion)
	root.add(res)
	lib := root.child("library")
	ev := lib.child("event", "name", o.Event)
	pr := ev.child("project", "name", o.Project)
	pr.add(seq)
	return document(root, "<!DOCTYPE fcpxml>"), nil
}

func audioHz(rate int) string {
	switch rate {
	case 32000:
		return "32k"
	case 44100:
		return "44.1k"
	case 88200:
		return "88.2k"
	case 96000:
		return "96k"
	}
	return "48k"
}

type fcpWriter struct {
	t       *Timeline
	o       FCPXMLOptions
	res     *node
	next    int
	formats map[string]string
	assets  map[*Media]string
	cross   string
	fade    string
	xfade   string
}

func (w *fcpWriter) id() string {
	w.next++
	return fmt.Sprintf("r%d", w.next)
}

// format declares (once) a format resource and returns its id.
func (w *fcpWriter) format(width, height int, r Rate, still bool) string {
	key := fmt.Sprintf("%dx%d@%d/%d:%v", width, height, r.Num, r.Den, still)
	if id, ok := w.formats[key]; ok {
		return id
	}
	id := w.id()
	f := el("format", "id", id)
	if still {
		f.attr("name", "FFVideoFormatRateUndefined")
	} else {
		f.attr("name", fcpFormatName(width, height, r))
		f.attr("frameDuration", fcpTime(secs(1, r)))
	}
	if width > 0 && height > 0 {
		f.attr("width", fmt.Sprint(width)).attr("height", fmt.Sprint(height))
	}
	f.attr("colorSpace", "1-1-1 (Rec. 709)")
	w.res.add(f)
	w.formats[key] = id
	return id
}

func (w *fcpWriter) effect(dst *string, name, uid string) {
	if *dst != "" {
		return
	}
	*dst = w.id()
	w.res.add(el("effect", "id", *dst, "name", name, "uid", uid))
}

func (w *fcpWriter) asset(m *Media) {
	id := w.id()
	w.assets[m] = id
	a := el("asset", "id", id, "name", strings.TrimSuffix(m.Name, extOf(m.Name)), "uid", uidOf(m.Abs), "start", "0s")
	switch m.Kind {
	case Movie:
		mr := m.rate(w.t.Rate)
		a.attr("duration", fcpTime(secs(mr.Frames(m.Duration), mr)))
		a.attr("hasVideo", "1").attr("format", w.format(m.Width, m.Height, mr, false)).attr("videoSources", "1")
		if m.HasAudio {
			a.attr("hasAudio", "1").attr("audioSources", "1").attr("audioChannels", fmt.Sprint(m.channels())).attr("audioRate", fmt.Sprint(m.sampleRate()))
		}
	case Sound:
		sr := int64(m.sampleRate())
		a.attr("duration", fcpTime(new(big.Rat).SetFrac64(int64(m.Duration*float64(sr)+0.5), sr)))
		a.attr("hasAudio", "1").attr("audioSources", "1").attr("audioChannels", fmt.Sprint(m.channels())).attr("audioRate", fmt.Sprint(m.sampleRate()))
	case Picture:
		a.attr("duration", "0s").attr("hasVideo", "1").attr("format", w.format(m.Width, m.Height, Rate{}, true)).attr("videoSources", "1")
	}
	a.child("media-rep", "kind", "original-media", "src", m.FileURL())
	w.res.add(a)
}

func extOf(name string) string {
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}

// spineItem is an element of the primary storyline with where it sits.
type spineItem struct {
	n          *node
	start, end int64    // timeline frames
	local      *big.Rat // the element's start (its local time at its first frame)
	clip       *Clip
	anchored   []*node
	markers    []*node
}

// spine builds the primary storyline and anchors every other clip to it.
func (w *fcpWriter) spine() *node {
	t := w.t
	var items []*spineItem
	var v1 Track
	if len(t.Video) > 0 {
		v1 = t.Video[0]
	}
	at := int64(0)
	gap := func(from, to int64) {
		items = append(items, &spineItem{
			n:     el("gap", "name", "Gap", "offset", fcpTime(secs(from, t.Rate)), "start", "0s", "duration", fcpTime(secs(to-from, t.Rate))),
			start: from, end: to, local: new(big.Rat),
		})
	}
	for i := range v1.Clips {
		c := &v1.Clips[i]
		if c.Start > at {
			gap(at, c.Start)
		}
		items = append(items, w.primary(c))
		at = c.End()
	}
	if at < t.Length {
		gap(at, t.Length)
	}
	// V2… above the scenes (lane +k), the sound tracks below (lane −k).
	type laneTrack struct {
		tr   *Track
		lane int
	}
	var others []laneTrack
	for vi := 1; vi < len(t.Video); vi++ {
		others = append(others, laneTrack{&t.Video[vi], vi})
	}
	lane := 0
	for ai := range t.Audio {
		if t.Audio[ai].SceneSound {
			continue // the scene clips carry their own sound
		}
		lane--
		others = append(others, laneTrack{&t.Audio[ai], lane})
	}
	for _, o := range others {
		if len(o.tr.Clips) == 0 {
			continue
		}
		for i := range o.tr.Clips {
			w.anchor(items, &o.tr.Clips[i], o.lane)
		}
	}
	// Scene markers sit on the storyline element under them.
	for _, m := range t.Markers {
		if it := itemAt(items, m.At); it != nil {
			local := ratAdd(it.local, secs(m.At-it.start, t.Rate))
			it.markers = append(it.markers, el("marker", "start", fcpTime(local), "duration", fcpTime(secs(1, t.Rate)), "value", m.Name))
		}
	}

	sp := el("spine")
	trans := map[int64]Transition{}
	for _, x := range v1.Transitions {
		trans[x.At] = x
	}
	for k, it := range items {
		if k > 0 {
			if x, ok := trans[it.start]; ok && items[k-1].end == it.start && it.clip != nil && items[k-1].clip != nil {
				sp.add(w.transition(x))
			}
		}
		// Order inside a clip: adjustments (already added), anchored clips, markers, filters.
		it.n.add(it.anchored...)
		it.n.add(it.markers...)
		sp.add(it.n)
	}
	return sp
}

func itemAt(items []*spineItem, f int64) *spineItem {
	k := sort.Search(len(items), func(i int) bool { return items[i].end > f })
	if k < len(items) && items[k].start <= f {
		return items[k]
	}
	if len(items) > 0 && f >= items[len(items)-1].end {
		return items[len(items)-1]
	}
	return nil
}

// primary is a storyline element for a V1 clip.
func (w *fcpWriter) primary(c *Clip) *spineItem {
	t := w.t
	start := mediaStart(*c, t)
	it := &spineItem{start: c.Start, end: c.End(), local: start, clip: c}
	common := []string{"offset", fcpTime(secs(c.Start, t.Rate)), "name", c.Name, "start", fcpTime(start), "duration", fcpTime(secs(c.Len, t.Rate))}
	switch c.Media.Kind {
	case Picture:
		it.n = el("video", append([]string{"ref", w.assets[c.Media]}, common...)...)
		w.pictureAdjust(it.n, c, start)
	default:
		it.n = el("asset-clip", append([]string{"ref", w.assets[c.Media]}, common...)...)
		it.n.attr("format", w.format(c.Media.Width, c.Media.Height, c.Media.rate(t.Rate), false)).attr("tcFormat", "NDF")
		sound := c.Media.HasAudio && !c.Mute
		if sound {
			it.n.attr("audioRole", "effects")
		} else if c.Media.HasAudio {
			it.n.attr("srcEnable", "video")
		}
		w.pictureAdjust(it.n, c, start)
		if sound {
			w.volume(it.n, c, start)
		}
	}
	return it
}

// anchor attaches a clip to the storyline element under its first frame.
func (w *fcpWriter) anchor(items []*spineItem, c *Clip, lane int) {
	parent := itemAt(items, c.Start)
	if parent == nil {
		return
	}
	offset := ratAdd(parent.local, secs(c.Start-parent.start, w.t.Rate))
	parent.anchored = append(parent.anchored, w.clipNode(c, fmt.Sprint(lane), offset))
}

// clipNode is a connected clip on lane at offset in its parent's local time.
func (w *fcpWriter) clipNode(c *Clip, lane string, offset *big.Rat) *node {
	t := w.t
	start := mediaStart(*c, t)
	attrs := []string{"ref", w.assets[c.Media]}
	if lane != "" {
		attrs = append(attrs, "lane", lane)
	}
	attrs = append(attrs, "offset", fcpTime(offset), "name", c.Name, "start", fcpTime(start), "duration", fcpTime(secs(c.Len, t.Rate)))
	var n *node
	switch c.Media.Kind {
	case Picture:
		n = el("video", attrs...)
		w.pictureAdjust(n, c, start)
	case Sound:
		n = el("asset-clip", attrs...)
		role := "dialogue"
		if c.Role == RoleMusic {
			role = "music"
		}
		n.attr("audioRole", role)
		w.volume(n, c, start)
	default:
		n = el("asset-clip", attrs...)
		n.attr("format", w.format(c.Media.Width, c.Media.Height, c.Media.rate(t.Rate), false)).attr("tcFormat", "NDF")
		if !c.Media.HasAudio || c.Mute {
			if c.Media.HasAudio {
				n.attr("srcEnable", "video")
			}
		} else {
			n.attr("audioRole", "effects")
		}
		w.pictureAdjust(n, c, start)
		if c.Media.HasAudio && !c.Mute {
			w.volume(n, c, start)
		}
	}
	return n
}

// pictureAdjust adds the transform animation and the opacity fades
// (stills: none with StaticStills, the move's middle as a fixed framing).
func (w *fcpWriter) pictureAdjust(n *node, c *Clip, start *big.Rat) {
	t := w.t
	if c.Media.Kind == Picture && w.o.StaticStills {
		if len(c.Motion) > 0 {
			mid := c.Motion[len(c.Motion)/2]
			if len(c.Motion) == 2 {
				a, b := c.Motion[0], c.Motion[1]
				mid = Key{Scale: (a.Scale + b.Scale) / 2, X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2, Rotation: (a.Rotation + b.Rotation) / 2}
			}
			h := float64(t.Height)
			n.child("adjust-transform", "position", ftoa(mid.X/h*100, 4)+" "+ftoa(-mid.Y/h*100, 4),
				"scale", ftoa(mid.Scale, 5)+" "+ftoa(mid.Scale, 5), "rotation", ftoa(-mid.Rotation, 4))
		}
		return
	}
	h := float64(t.Height)
	at := func(f int64) string { return fcpTime(ratAdd(start, secs(f, t.Rate))) }
	if len(c.Motion) > 0 {
		k0 := c.Motion[0]
		pos := func(k Key) string { return ftoa(k.X/h*100, 4) + " " + ftoa(-k.Y/h*100, 4) }
		sc := func(k Key) string { return ftoa(k.Scale, 5) + " " + ftoa(k.Scale, 5) }
		tr := n.child("adjust-transform", "position", pos(k0), "scale", sc(k0), "rotation", ftoa(-k0.Rotation, 4))
		if len(c.Motion) > 1 {
			p := tr.child("param", "name", "position").child("keyframeAnimation")
			s := tr.child("param", "name", "scale").child("keyframeAnimation")
			rotates := false
			for _, k := range c.Motion {
				rotates = rotates || k.Rotation != k0.Rotation
			}
			var r *node
			if rotates {
				r = tr.child("param", "name", "rotation").child("keyframeAnimation")
			}
			for _, k := range c.Motion {
				p.child("keyframe", "time", at(k.At), "value", pos(k))
				s.child("keyframe", "time", at(k.At), "value", sc(k), "curve", "linear")
				if r != nil {
					r.child("keyframe", "time", at(k.At), "value", ftoa(-k.Rotation, 4), "curve", "linear")
				}
			}
		}
	}
	if c.FadeIn > 0 || c.FadeOut > 0 {
		b := n.child("adjust-blend", "amount", "1")
		ka := b.child("param", "name", "amount").child("keyframeAnimation")
		if c.FadeIn > 0 {
			ka.child("keyframe", "time", at(0), "value", "0", "curve", "linear")
			ka.child("keyframe", "time", at(min(c.FadeIn, c.Len)), "value", "1", "curve", "linear")
		}
		if c.FadeOut > 0 {
			ka.child("keyframe", "time", at(max(0, c.Len-c.FadeOut)), "value", "1", "curve", "linear")
			ka.child("keyframe", "time", at(c.Len), "value", "0", "curve", "linear")
		}
	}
}

// volume adds the clip's level: a constant gain, volume keyframes, fades.
func (w *fcpWriter) volume(n *node, c *Clip, start *big.Rat) {
	t := w.t
	db := func(v float64) string {
		if v <= -96 {
			return "-96dB"
		}
		return ftoa(v, 2) + "dB"
	}
	if len(c.Levels) == 0 && c.FadeIn == 0 && c.FadeOut == 0 {
		if c.Gain != 0 {
			n.child("adjust-volume", "amount", db(c.Gain))
		}
		return
	}
	v := n.child("adjust-volume", "amount", db(c.levelAt(0)))
	p := v.child("param", "name", "amount")
	if c.FadeIn > 0 {
		p.child("fadeIn", "type", "easeIn", "duration", fcpTime(secs(c.FadeIn, t.Rate)))
	}
	if c.FadeOut > 0 {
		p.child("fadeOut", "type", "easeOut", "duration", fcpTime(secs(c.FadeOut, t.Rate)))
	}
	if len(c.Levels) > 0 {
		ka := p.child("keyframeAnimation")
		for _, l := range c.Levels {
			ka.child("keyframe", "time", fcpTime(ratAdd(start, secs(l.At, t.Rate))), "value", db(l.DB))
		}
	}
}

// transition is a spine transition element.
func (w *fcpWriter) transition(x Transition) *node {
	t := w.t
	name, ref := "Cross Dissolve", w.cross
	if x.Kind != Dissolve {
		name, ref = "Fade To Color", w.fade
	}
	n := el("transition", "name", name, "offset", fcpTime(secs(x.From(), t.Rate)), "duration", fcpTime(secs(x.Len, t.Rate)))
	n.child("filter-video", "ref", ref, "name", name)
	n.child("filter-audio", "ref", w.xfade, "name", "Audio Crossfade")
	return n
}
