package nle

// OpenTimelineIO (.otio, JSON) — the open interchange format of the Academy
// Software Foundation: Kdenlive 25.04+ (File → OpenTimelineIO Import),
// DaVinci Resolve 18.5+ (File → Import → Timeline), Nuke Studio, Hiero,
// RV and the otio converters read it.
//
// Schemas: Timeline.1 › Stack.1 › Track.1 (kind Video/Audio, bottom track
// first) › Clip.2 (media_references + active_media_reference_key, OTIO 0.15+;
// readers upgrade Clip.1 automatically, so Clip.1 is written when asked),
// Gap.1, Transition.1 (SMPTE_Dissolve; in_offset/out_offset around the cut),
// ExternalReference.1, Marker.2 on the stack. Times are RationalTime at the
// timeline rate. OTIO has no standard for levels, motion or fades: they are
// kept in the clip's metadata under "ytcartell" (the editor ignores them).

import (
	"strings"
)

// OTIOOptions for the .otio file.
type OTIOOptions struct {
	// Relative writes target_url as the media path relative to the .otio
	// file ("media/001_scene.mp4"); else an absolute file:// URL.
	Relative bool
	// Clip1 writes the legacy Clip.1 schema (one media_reference) for
	// readers older than OTIO 0.15.
	Clip1 bool
}

func (t *Timeline) rt(frames int64) obj {
	return obj{{"OTIO_SCHEMA", "RationalTime.1"}, {"rate", dbl(t.Rate.FPS())}, {"value", dbl(float64(frames))}}
}

func (t *Timeline) tr(start, dur int64) obj {
	return obj{{"OTIO_SCHEMA", "TimeRange.1"}, {"duration", t.rt(dur)}, {"start_time", t.rt(start)}}
}

// OTIO renders t as an OpenTimelineIO JSON document.
func OTIO(t *Timeline, o OTIOOptions) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	var tracks []any
	for i, tr := range t.Video {
		tracks = append(tracks, otioTrack(t, tr, "Video", i, o))
	}
	for i, tr := range t.Audio {
		tracks = append(tracks, otioTrack(t, tr, "Audio", i, o))
	}
	var markers []any
	for _, m := range t.Markers {
		markers = append(markers, obj{
			{"OTIO_SCHEMA", "Marker.2"}, {"metadata", obj{}}, {"name", m.Name}, {"color", "RED"}, {"comment", ""},
			{"marked_range", t.tr(m.At, 0)},
		})
	}
	stack := obj{
		{"OTIO_SCHEMA", "Stack.1"}, {"metadata", obj{}}, {"name", "tracks"}, {"source_range", nil},
		{"effects", []any{}}, {"markers", append([]any{}, markers...)}, {"enabled", true}, {"children", append([]any{}, tracks...)},
	}
	root := obj{
		{"OTIO_SCHEMA", "Timeline.1"},
		{"metadata", obj{{"ytcartell", obj{{"width", t.Width}, {"height", t.Height}, {"sample_rate", t.sampleRate()}}}}},
		{"name", t.Name},
		{"global_start_time", t.rt(0)},
		{"tracks", stack},
	}
	var b strings.Builder
	writeJSON(&b, root, 0)
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

func otioTrack(t *Timeline, tr Track, kind string, _ int, o OTIOOptions) obj {
	var kids []any
	trans := map[int64]Transition{}
	for _, x := range tr.Transitions {
		trans[x.At] = x
	}
	at := int64(0)
	for i, c := range tr.Clips {
		if c.Start > at {
			kids = append(kids, obj{
				{"OTIO_SCHEMA", "Gap.1"}, {"metadata", obj{}}, {"name", ""}, {"source_range", t.tr(0, c.Start-at)},
				{"effects", []any{}}, {"markers", []any{}}, {"enabled", true},
			})
		}
		if x, ok := trans[c.Start]; ok && i > 0 && tr.Clips[i-1].End() == c.Start {
			name := "Cross Dissolve"
			switch x.Kind {
			case DipBlack, DipWhite:
				name = "Dip To Color Dissolve"
			}
			kids = append(kids, obj{
				{"OTIO_SCHEMA", "Transition.1"},
				{"metadata", obj{{"ytcartell", obj{{"source", x.Source}, {"kind", transKindName(x.Kind)}}}}},
				{"name", name}, {"in_offset", t.rt(x.At - x.From())}, {"out_offset", t.rt(x.To() - x.At)},
				{"transition_type", "SMPTE_Dissolve"},
			})
		}
		kids = append(kids, otioClip(t, c, o))
		at = c.End()
	}
	return obj{
		{"OTIO_SCHEMA", "Track.1"}, {"metadata", obj{}}, {"name", tr.Name}, {"source_range", nil},
		{"effects", []any{}}, {"markers", []any{}}, {"enabled", true}, {"children", append([]any{}, kids...)}, {"kind", kind},
	}
}

func transKindName(k TransKind) string {
	switch k {
	case DipBlack:
		return "dip-black"
	case DipWhite:
		return "dip-white"
	}
	return "dissolve"
}

func otioClip(t *Timeline, c Clip, o OTIOOptions) obj {
	m := c.Media
	target := m.FileURL()
	if o.Relative && m.Rel != "" {
		target = m.Rel
	}
	var avail any
	if m.Kind != Picture && m.Duration > 0 {
		avail = t.tr(0, t.Rate.Frames(m.Duration))
	}
	ref := obj{
		{"OTIO_SCHEMA", "ExternalReference.1"}, {"metadata", obj{}}, {"name", m.Name},
		{"available_range", avail}, {"available_image_bounds", nil}, {"target_url", target},
	}
	meta := obj{{"role", c.Role}}
	if c.Scene > 0 {
		meta = append(meta, kv{"scene", c.Scene})
	}
	if c.Gain != 0 {
		meta = append(meta, kv{"gain_db", dbl(c.Gain)})
	}
	if c.Mute {
		meta = append(meta, kv{"mute", true})
	}
	if len(c.Levels) > 0 {
		var ls []any
		for _, l := range c.Levels {
			ls = append(ls, obj{{"frame", l.At}, {"db", dbl(l.DB)}})
		}
		meta = append(meta, kv{"levels", ls})
	}
	if c.FadeIn > 0 {
		meta = append(meta, kv{"fade_in_frames", c.FadeIn})
	}
	if c.FadeOut > 0 {
		meta = append(meta, kv{"fade_out_frames", c.FadeOut})
	}
	if len(c.Motion) > 0 {
		var ks []any
		for _, k := range c.Motion {
			ks = append(ks, obj{{"frame", k.At}, {"scale", dbl(k.Scale)}, {"x_px", dbl(k.X)}, {"y_px", dbl(k.Y)}, {"rotation_deg", dbl(k.Rotation)}})
		}
		meta = append(meta, kv{"motion", ks})
	}
	out := obj{
		{"OTIO_SCHEMA", "Clip.2"},
		{"metadata", obj{{"ytcartell", meta}}},
		{"name", c.Name},
		{"source_range", t.tr(c.In, c.Len)},
		{"effects", []any{}}, {"markers", []any{}}, {"enabled", !c.Mute || m.Kind != Sound},
	}
	if o.Clip1 {
		out[0].v = "Clip.1"
		out = append(out, kv{"media_reference", ref})
	} else {
		out = append(out, kv{"media_references", obj{{"DEFAULT_MEDIA", ref}}}, kv{"active_media_reference_key", "DEFAULT_MEDIA"})
	}
	return out
}
