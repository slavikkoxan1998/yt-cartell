package nle

// MLT XML — Shotcut's project format (File → Open; Kdenlive reads the same
// framework but prefers its own .kdenlive files: use the .otio there).
//
// A Shotcut-style document: a profile at the timeline's size and rate, a
// black background track, one playlist per track (entries with in/out —
// MLT's out is inclusive — and blanks), a cross dissolve as a small tractor
// with a luma + mix transition in the playlist (Shotcut's own form), dips
// and the film's opening/closing as brightness fades, levels as the volume
// filter's dB keyframes, motion as the affine filter's rect keyframes.
// Every use of a file is its own producer (Shotcut does the same). Media
// paths are relative to the .mlt file (MLT resolves them from the
// document's folder when no root is set).

import (
	"fmt"
	"strconv"
	"strings"
)

// MLTOptions for the Shotcut file.
type MLTOptions struct {
	// Absolute writes absolute paths instead of paths relative to the file.
	Absolute bool
}

type mltWriter struct {
	t     *Timeline
	o     MLTOptions
	root  *node
	nprod int
	ntran int
	nfilt int
}

func (w *mltWriter) path(m *Media) string {
	if w.o.Absolute || m.Rel == "" {
		return strings.ReplaceAll(m.Abs, `\`, "/")
	}
	return m.Rel
}

func prop(n *node, name, value string) {
	p := n.child("property", "name", name)
	p.text, p.set = value, true
}

// MLT renders t as a Shotcut MLT XML document.
func MLT(t *Timeline, o MLTOptions) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	w := &mltWriter{t: t, o: o}
	g := gcd(int64(t.Width), int64(t.Height))
	w.root = el("mlt", "LC_NUMERIC", "C", "version", "7.24.0", "title", t.Name, "producer", "main_bin")
	w.root.child("profile", "description", fmt.Sprintf("%dx%d %s fps", t.Width, t.Height, t.Rate),
		"width", fmt.Sprint(t.Width), "height", fmt.Sprint(t.Height), "progressive", "1",
		"sample_aspect_num", "1", "sample_aspect_den", "1",
		"display_aspect_num", fmt.Sprint(int64(t.Width)/g), "display_aspect_den", fmt.Sprint(int64(t.Height)/g),
		"frame_rate_num", fmt.Sprint(t.Rate.Num), "frame_rate_den", fmt.Sprint(t.Rate.Den), "colorspace", "709")
	bin := w.root.child("playlist", "id", "main_bin")
	prop(bin, "xml_retain", "1")
	last := fmt.Sprint(t.Length - 1)
	black := w.root.child("producer", "id", "black", "in", "0", "out", last)
	prop(black, "length", fmt.Sprint(t.Length))
	prop(black, "eof", "pause")
	prop(black, "resource", "0")
	prop(black, "aspect_ratio", "1")
	prop(black, "mlt_service", "color")
	prop(black, "mlt_image_format", "rgba")
	prop(black, "set.test_audio", "0")
	bg := w.root.child("playlist", "id", "background")
	bg.child("entry", "producer", "black", "in", "0", "out", last)

	var playlists []*node
	for i, tr := range t.Video {
		playlists = append(playlists, w.playlist(tr, fmt.Sprintf("playlist%d", len(playlists)), fmt.Sprintf("V%d", i+1), true))
	}
	for i, tr := range t.Audio {
		playlists = append(playlists, w.playlist(tr, fmt.Sprintf("playlist%d", len(playlists)), fmt.Sprintf("A%d", i+1), false))
	}
	tractor := el("tractor", "id", "tractor0", "title", t.Name, "in", "0", "out", last)
	prop(tractor, "shotcut", "1")
	prop(tractor, "shotcut:projectAudioChannels", "2")
	prop(tractor, "shotcut:projectFolder", "0")
	tractor.child("track", "producer", "background")
	for i := range playlists {
		tk := tractor.child("track", "producer", fmt.Sprintf("playlist%d", i))
		if i >= len(t.Video) {
			tk.attr("hide", "video")
		}
	}
	for i := range playlists {
		b := strconv.Itoa(i + 1)
		mix := tractor.child("transition", "id", w.tid())
		prop(mix, "a_track", "0")
		prop(mix, "b_track", b)
		prop(mix, "mlt_service", "mix")
		prop(mix, "always_active", "1")
		prop(mix, "sum", "1")
		if i < len(t.Video) {
			bl := tractor.child("transition", "id", w.tid())
			prop(bl, "a_track", "0")
			prop(bl, "b_track", b)
			prop(bl, "version", "0.1")
			prop(bl, "mlt_service", "qtblend")
			prop(bl, "threads", "0")
			dis := "0"
			if i == 0 {
				dis = "1" // V1 is opaque: nothing to blend with the black background
			}
			prop(bl, "disable", dis)
		}
	}
	w.root.add(tractor)
	return document(w.root, ""), nil
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

func (w *mltWriter) tid() string {
	w.ntran++
	return fmt.Sprintf("transition%d", w.ntran)
}

// producer declares a producer for one use of a clip, from source frame
// in for n frames, with its filters, and returns its id.
func (w *mltWriter) producer(c Clip, in, n int64, video bool, fadeIn, fadeOut int64) string {
	t := w.t
	m := c.Media
	w.nprod++
	id := fmt.Sprintf("producer%d", w.nprod)
	length := in + n
	if m.Kind != Picture && m.Duration > 0 {
		length = max(length, t.Rate.Frames(m.Duration))
	}
	tag := "chain"
	if m.Kind == Picture {
		tag = "producer"
	}
	p := w.root.child(tag, "id", id, "in", "0", "out", fmt.Sprint(length-1))
	prop(p, "length", fmt.Sprint(length))
	prop(p, "eof", "pause")
	prop(p, "resource", w.path(m))
	if m.Kind == Picture {
		prop(p, "ttl", "1")
		prop(p, "aspect_ratio", "1")
		prop(p, "progressive", "1")
		prop(p, "seekable", "1")
		prop(p, "mlt_service", "qimage")
	} else {
		prop(p, "mlt_service", "avformat-novalidate")
		prop(p, "seekable", "1")
		switch {
		case m.Kind == Sound:
			prop(p, "video_index", "-1")
			prop(p, "audio_index", "0")
		case !video:
			prop(p, "video_index", "-1") // a movie's sound on an audio track
		default:
			prop(p, "audio_index", "-1") // pictures only: the sound tracks carry the sound
		}
	}
	prop(p, "shotcut:caption", c.Name)
	// Filters work in the producer's own frames: from in to in+n-1.
	fin, fout := fmt.Sprint(in), fmt.Sprint(in+n-1)
	if video && (fadeIn > 0 || fadeOut > 0) {
		f := w.filter(p, fin, fout)
		var keys []string
		if fadeIn > 0 {
			keys = append(keys, "0=0", fmt.Sprintf("%d=1", min(fadeIn, n)-1))
		}
		if fadeOut > 0 {
			keys = append(keys, fmt.Sprintf("%d=1", max(0, n-fadeOut)), fmt.Sprintf("%d=0", n-1))
		}
		prop(f, "level", strings.Join(keys, ";"))
		prop(f, "alpha", "1")
		prop(f, "mlt_service", "brightness")
		prop(f, "shotcut:filter", "fadeInBrightness")
	}
	if video && len(c.Motion) > 0 {
		f := w.filter(p, fin, fout)
		var rect, rot []string
		off := in - c.In // the producer's frames run from c.In
		for _, k := range c.Motion {
			wd, ht := float64(t.Width)*k.Scale, float64(t.Height)*k.Scale
			x := (float64(t.Width)-wd)/2 + k.X
			y := (float64(t.Height)-ht)/2 + k.Y
			at := k.At - off
			rect = append(rect, fmt.Sprintf("%d=%s %s %s %s 1", at, ftoa(x, 3), ftoa(y, 3), ftoa(wd, 3), ftoa(ht, 3)))
			rot = append(rot, fmt.Sprintf("%d=%s", at, ftoa(k.Rotation, 3)))
		}
		prop(f, "background", "color:#00000000")
		prop(f, "mlt_service", "affine")
		prop(f, "shotcut:filter", "affineSizePosition")
		prop(f, "transition.fill", "1")
		prop(f, "transition.distort", "0")
		prop(f, "transition.rect", strings.Join(rect, ";"))
		prop(f, "transition.fix_rotate_x", strings.Join(rot, ";"))
		prop(f, "transition.valign", "middle")
		prop(f, "transition.halign", "center")
		prop(f, "transition.threads", "0")
	}
	if !video && !c.Mute && (c.Gain != 0 || len(c.Levels) > 0 || c.FadeIn > 0 || c.FadeOut > 0) {
		f := w.filter(p, fin, fout)
		off := in - c.In
		var keys []string
		add := func(at int64, db float64) {
			if db <= -96 {
				db = -96
			}
			keys = append(keys, fmt.Sprintf("%d=%s", at, ftoa(db, 2)))
		}
		switch {
		case len(c.Levels) > 0:
			for _, l := range c.Levels {
				add(l.At-off, l.DB)
			}
		default:
			add(0, c.Gain)
		}
		prop(f, "window", "75")
		prop(f, "max_gain", "20dB")
		prop(f, "level", strings.Join(keys, ";"))
		prop(f, "mlt_service", "volume")
		prop(f, "shotcut:filter", "audioGain")
		if c.FadeIn > 0 {
			fi := w.filter(p, fin, fmt.Sprint(in+c.FadeIn-1))
			prop(fi, "gain", "0")
			prop(fi, "end", "1")
			prop(fi, "mlt_service", "volume")
			prop(fi, "shotcut:filter", "fadeInVolume")
		}
		if c.FadeOut > 0 {
			fo := w.filter(p, fmt.Sprint(in+n-c.FadeOut), fout)
			prop(fo, "gain", "1")
			prop(fo, "end", "0")
			prop(fo, "mlt_service", "volume")
			prop(fo, "shotcut:filter", "fadeOutVolume")
		}
	}
	return id
}

func (w *mltWriter) filter(p *node, in, out string) *node {
	w.nfilt++
	return p.child("filter", "id", fmt.Sprintf("filter%d", w.nfilt), "in", in, "out", out)
}

// playlist writes one track.
func (w *mltWriter) playlist(tr Track, id, name string, video bool) *node {
	pl := el("playlist", "id", id)
	if video {
		prop(pl, "shotcut:video", "1")
	} else {
		prop(pl, "shotcut:audio", "1")
	}
	prop(pl, "shotcut:name", name)
	trans := map[int64]Transition{}
	for _, x := range tr.Transitions {
		if x.Kind == Dissolve {
			trans[x.At] = x
		}
	}
	// Dips and the film's edges become fades on the clips.
	fadeIn := map[int]int64{}
	fadeOut := map[int]int64{}
	for i, c := range tr.Clips {
		fadeIn[i], fadeOut[i] = c.FadeIn, c.FadeOut
	}
	for _, x := range tr.Transitions {
		if x.Kind == Dissolve {
			continue
		}
		for i, c := range tr.Clips {
			if c.End() == x.At {
				fadeOut[i] = max(fadeOut[i], x.At-x.From())
			}
			if c.Start == x.At {
				fadeIn[i] = max(fadeIn[i], x.To()-x.At)
			}
		}
	}
	at := int64(0)
	var pending []*node // entries are added after the producers they use
	entry := func(prod string, in, n int64) {
		pending = append(pending, el("entry", "producer", prod, "in", fmt.Sprint(in), "out", fmt.Sprint(in+n-1)))
	}
	for i := 0; i < len(tr.Clips); i++ {
		c := tr.Clips[i]
		from, to := c.Start, c.End()
		if x, ok := trans[c.Start]; ok && i > 0 && tr.Clips[i-1].End() == c.Start {
			from = x.To() // the head is inside the transition tractor
		}
		next, hasNext := Transition{}, false
		if i+1 < len(tr.Clips) && tr.Clips[i+1].Start == c.End() {
			next, hasNext = trans[c.End()]
		}
		if hasNext {
			to = next.From()
		}
		if from > at {
			pending = append(pending, el("blank", "length", fmt.Sprint(from-at)))
		}
		if to > from {
			fi, fo := int64(0), int64(0)
			if from == c.Start {
				fi = fadeIn[i]
			}
			if to == c.End() {
				fo = fadeOut[i]
			}
			prod := w.producer(c, c.In+(from-c.Start), to-from, video, fi, fo)
			entry(prod, c.In+(from-c.Start), to-from)
		}
		at = to
		if hasNext {
			// The dissolve: a tractor of the two clips' overlapping parts.
			b := tr.Clips[i+1]
			n := next.Len
			aIn := c.In + (next.From() - c.Start)
			bIn := b.In - (next.At - next.From())
			pa := w.producer(c, aIn, n, video, 0, 0)
			pb := w.producer(b, bIn, n, video, 0, 0)
			w.nprod++
			tid := fmt.Sprintf("tractor%d", w.nprod)
			tt := w.root.child("tractor", "id", tid, "in", "0", "out", fmt.Sprint(n-1))
			prop(tt, "shotcut:transition", "lumaMix")
			tt.child("track", "producer", pa, "in", fmt.Sprint(aIn), "out", fmt.Sprint(aIn+n-1))
			tt.child("track", "producer", pb, "in", fmt.Sprint(bIn), "out", fmt.Sprint(bIn+n-1))
			if video {
				l := tt.child("transition", "id", w.tid(), "out", fmt.Sprint(n-1))
				prop(l, "a_track", "0")
				prop(l, "b_track", "1")
				prop(l, "factory", "loader")
				prop(l, "mlt_service", "luma")
				prop(l, "alpha_over", "1")
				prop(l, "fix_background_alpha", "1")
			}
			mx := tt.child("transition", "id", w.tid(), "out", fmt.Sprint(n-1))
			prop(mx, "a_track", "0")
			prop(mx, "b_track", "1")
			prop(mx, "start", "-1")
			prop(mx, "accepts_blanks", "1")
			prop(mx, "mlt_service", "mix")
			pending = append(pending, el("entry", "producer", tid, "in", "0", "out", fmt.Sprint(n-1)))
			at = next.To()
		}
	}
	pl.add(pending...)
	w.root.add(pl)
	return pl
}
