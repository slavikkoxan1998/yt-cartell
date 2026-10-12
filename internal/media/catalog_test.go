package media

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

func TestCatalogs(t *testing.T) {
	if len(Transitions) < 15 || len(Effects) < 12 || len(Filters) < 14 || len(SubtitlePresets) < 6 {
		t.Fatalf("catalog sizes: %d transitions, %d effects, %d filters, %d subtitle presets",
			len(Transitions), len(Effects), len(Filters), len(SubtitlePresets))
	}
	seen := map[string]bool{}
	check := func(kind, id, label, hint string) {
		t.Helper()
		key := kind + ":" + id
		if id == "" || seen[key] || !hasCyrillic(label) || !hasCyrillic(hint) {
			t.Fatalf("%s %q: duplicate or missing Ukrainian label/hint (%q, %q)", kind, id, label, hint)
		}
		seen[key] = true
	}
	for _, tr := range Transitions {
		check("transition", tr.ID, tr.Label, tr.Hint)
		switch tr.Kind {
		case KindXFade:
			if tr.XFade == "" || !tr.Overlaps() {
				t.Fatalf("xfade transition %q without pattern", tr.ID)
			}
		case KindDip:
			if tr.Color != "black" && tr.Color != "white" || tr.Overlaps() {
				t.Fatalf("dip %q colour %q", tr.ID, tr.Color)
			}
		case KindCut:
		default:
			t.Fatalf("transition %q kind %q", tr.ID, tr.Kind)
		}
	}
	for _, e := range Effects {
		check("effect", e.ID, e.Label, e.Hint)
		if _, ok := motions[e.ID]; ok == (e.ID == "none") {
			t.Fatalf("effect %q motion mismatch", e.ID)
		}
	}
	for _, f := range Filters {
		check("filter", f.ID, f.Label, f.Hint)
		if (f.chain == "") != (f.ID == "none") {
			t.Fatalf("filter %q chain %q", f.ID, f.chain)
		}
	}
	for _, p := range SubtitlePresets {
		check("subtitles", p.ID, p.Label, p.Hint)
		if p.Size <= 0 || p.SizeVertical <= 0 || (p.Position != "bottom" && p.Position != "middle" && p.Position != "top") {
			t.Fatalf("preset %+v", p)
		}
		if _, ok := parseHexColor(p.Color); !ok {
			t.Fatalf("preset %q colour %q", p.ID, p.Color)
		}
	}
	// The ids the product asked for.
	for _, id := range strings.Fields("none fade fadewhite dissolve wipeleft wiperight wipeup wipedown slideleft slideright slideup slidedown smoothleft smoothright circleopen circleclose radial pixelize zoomin hblur squeezeh squeezev distance") {
		if _, ok := TransitionByID(id); !ok {
			t.Fatalf("transition %q missing", id)
		}
	}
	for _, id := range strings.Fields("none zoomin zoomout panleft panright panup pandown diagonal pushin drift shake rotate") {
		if _, ok := EffectByID(id); !ok {
			t.Fatalf("effect %q missing", id)
		}
	}
	for _, id := range strings.Fields("none cinematic warm cool vintage noir sepia faded contrast vivid dreamy moonlight golden grain vignette") {
		if _, ok := FilterByID(id); !ok {
			t.Fatalf("filter %q missing", id)
		}
	}
	for _, id := range []string{DefaultTransition, "none"} {
		if _, ok := TransitionByID(id); !ok {
			t.Fatal(id)
		}
	}
	if _, ok := EffectByID(DefaultEffect); !ok {
		t.Fatal(DefaultEffect)
	}
	if _, ok := SubtitlePresetByID(DefaultSubtitlePreset); !ok {
		t.Fatal(DefaultSubtitlePreset)
	}
	if _, ok := TransitionByID(""); ok {
		t.Fatal("empty id must not resolve")
	}
}

// Every xfade pattern we use exists in this ffmpeg build.
func TestXFadeNamesKnownToFFmpeg(t *testing.T) {
	f := locate(t)
	out, err := exec.Command(f.FFmpeg, "-hide_banner", "-h", "filter=xfade").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, tr := range Transitions {
		if tr.Kind == KindXFade && !regexp.MustCompile(`(?m)^\s+`+tr.XFade+`\s+-?\d+\s`).Match(out) {
			t.Fatalf("xfade %q unknown to ffmpeg", tr.XFade)
		}
	}
}

// ---- layout -------------------------------------------------------------------------------------

func frames(sec float64) int { return int(math.Round(sec * fps)) }

func TestLayoutMixed(t *testing.T) {
	o := MontageOptions{
		Transition:         "dissolve",
		TransitionDuration: 0.5,
		DefaultEffect:      "panleft",
		Segments: []Segment{
			{Video: "a.mp4", VideoDur: 8},                                                  // clip, no voice
			{Frame: "b.png", Voice: "b.mp3", VoiceDur: 3, Transition: "fade"},              // still + voice; dip after it
			{Video: "c.mp4", VideoDur: 2, Voice: "c.mp3", VoiceDur: 5, Transition: "nope"}, // voice longer than the clip
			{Narration: "no picture — skipped"},
			{Video: "d.mp4", VideoDur: 2, TrimIn: 10, TrimOut: 3, Effect: "zoomin", Transition: "none"}, // over-trimmed
			{Frame: "e.png", Effect: "rotate"},
		},
	}
	slots, total := Layout(o)
	if len(slots) != 5 {
		t.Fatalf("%d slots", len(slots))
	}
	idx := []int{0, 1, 2, 4, 5}
	for k, sl := range slots {
		if sl.Segment != idx[k] {
			t.Fatalf("slot %d → segment %d", k, sl.Segment)
		}
	}
	a, b, c, d, e := slots[0], slots[1], slots[2], slots[3], slots[4]
	x := 12 // 0.5 s at 24 fps

	// Transitions: per-scene override, unknown → project default, last = "".
	if a.Transition != "dissolve" || b.Transition != "fade" || c.Transition != "dissolve" || d.Transition != "none" || e.Transition != "" {
		t.Fatalf("transitions %q %q %q %q %q", a.Transition, b.Transition, c.Transition, d.Transition, e.Transition)
	}
	if a.out != x || b.in != x || b.out != 0 || c.in != 0 || c.out != x || d.in != x || d.out != 0 || e.in != 0 {
		t.Fatalf("overlaps a%d/%d b%d/%d c%d/%d d%d/%d e%d", a.in, a.out, b.in, b.out, c.in, c.out, d.in, d.out, e.in)
	}
	if a.Overlap != 0.5 || b.Overlap != 0 || c.Overlap != 0.5 {
		t.Fatalf("overlap seconds %v %v %v", a.Overlap, b.Overlap, c.Overlap)
	}
	// Dip to black around the "fade" cut; film opens/ends in black (default is not a cut).
	if b.fadeOut != 0.3 || c.fadeIn != 0.3 || a.fadeIn != 0.3 || e.fadeOut != 0.3 || a.fadeOut != 0 || d.fadeIn != 0 {
		t.Fatalf("dips a%v/%v b%v c%v d%v e%v", a.fadeIn, a.fadeOut, b.fadeOut, c.fadeIn, d.fadeIn, e.fadeOut)
	}
	// Lengths.
	if a.frames != 192 || a.ClipDuration != 8 || a.Duration != 8 {
		t.Fatalf("a %+v", a)
	}
	// b: still 8 s ≥ voice need.
	if b.frames != 192 || b.HasClip || b.Effect != "panleft" {
		t.Fatalf("b %+v", b)
	}
	// c: 2 s clip, 5 s voice: 0.25 + 5 + 0.35 + 0.5 overlap = 6.1 s, last frame held.
	if c.frames != frames(6.1) || c.ClipDuration != 2 || c.Effect != "none" {
		t.Fatalf("c frames %d clip %v effect %q", c.frames, c.ClipDuration, c.Effect)
	}
	// d: trims clamped so 0.5 s remain; clip effect chosen explicitly.
	if d.TrimIn != 1.5 || d.TrimOut != 0 || d.clipFrames != 12 || d.Effect != "zoomin" {
		t.Fatalf("d trims %v/%v clip %d effect %q", d.TrimIn, d.TrimOut, d.clipFrames, d.Effect)
	}
	// d is shorter than its overlap + body minimum.
	if d.frames != d.in+d.out+minBodyFrames {
		t.Fatalf("d frames %d", d.frames)
	}
	if e.Effect != "rotate" || e.frames != 192 {
		t.Fatalf("e %+v", e)
	}
	// Starts: every next scene starts at the previous end minus the overlap.
	for k := 1; k < len(slots); k++ {
		p := slots[k-1]
		if slots[k].start != p.start+p.frames-p.out {
			t.Fatalf("slot %d start %d", k, slots[k].start)
		}
		if !near(slots[k].Start, p.Start+p.Duration-p.Overlap, 1e-9) {
			t.Fatalf("slot %d Start %v", k, slots[k].Start)
		}
	}
	last := slots[len(slots)-1]
	if total != last.Start+last.Duration {
		t.Fatalf("total %v", total)
	}
	// Voices never touch an overlap.
	for k, sl := range slots {
		if sl.VoiceDuration == 0 {
			continue
		}
		if sl.VoiceStart < sl.Start+frameSec(sl.in)+voiceDelay-1e-9 {
			t.Fatalf("slot %d voice starts inside the incoming transition", k)
		}
		if sl.VoiceStart+sl.VoiceDuration > sl.Start+sl.Duration-sl.Overlap-0.3 {
			t.Fatalf("slot %d voice runs into the outgoing transition", k)
		}
	}
	// Voice start = start + incoming overlap + 0.25 s.
	if b.VoiceStart != frameSec(b.start+x+6) || c.VoiceStart != frameSec(c.start+6) {
		t.Fatalf("voice starts b %v c %v", b.VoiceStart, c.VoiceStart)
	}
}

func TestLayoutDefaults(t *testing.T) {
	// "" project transition = hard cuts, no film fades; stills default to zoom-in.
	slots, total := Layout(MontageOptions{Segments: []Segment{{Frame: "a.png"}, {Video: "b.mp4"}}})
	if len(slots) != 2 || slots[0].Transition != "none" || slots[0].fadeIn != 0 || slots[1].fadeOut != 0 {
		t.Fatalf("%+v", slots)
	}
	if slots[0].Effect != DefaultEffect || slots[1].Effect != "none" || total != 16 {
		t.Fatalf("effects %q %q total %v", slots[0].Effect, slots[1].Effect, total)
	}
	// Transition duration is clamped to 0.2…2 s.
	for d, want := range map[float64]int{5: 48, 0.05: 5, 0: 12, 1: 24} {
		s, _ := Layout(MontageOptions{Transition: "wipeleft", TransitionDuration: d, Segments: []Segment{{Frame: "a"}, {Frame: "b"}}})
		if s[0].out != want || s[1].in != want {
			t.Fatalf("duration %v → %d frames", d, s[0].out)
		}
	}
	// Dips scale with the duration: 0.6 × 1 s.
	s, _ := Layout(MontageOptions{Transition: "fadewhite", TransitionDuration: 1, Segments: []Segment{{Frame: "a"}, {Frame: "b"}}})
	if s[0].fadeOut != 0.6 || s[0].fadeOutCol != "white" || s[1].fadeIn != 0.6 || s[0].fadeIn != 0.6 || s[0].fadeInCol != "black" {
		t.Fatalf("white dip %+v", s[0])
	}
	if s, _ := Layout(MontageOptions{}); s != nil {
		t.Fatal("empty")
	}
	// Long dips: the line starts once the picture is half faded in and keeps
	// half the fade-out as its tail (defaults stay at 0.25 s / 0.35 s).
	s, _ = Layout(MontageOptions{Transition: "fade", TransitionDuration: 2, Segments: []Segment{
		{Video: "a", VideoDur: 1, Voice: "v", VoiceDur: 2}, {Video: "b", VideoDur: 1, Voice: "v", VoiceDur: 2}}})
	if s[0].voiceAt != 14 || s[1].voiceAt != 14 || s[0].frames != 14+frames(2+0.6) {
		t.Fatalf("long dip: voice at %d/%d, %d frames", s[0].voiceAt, s[1].voiceAt, s[0].frames)
	}
}

// Slices of the user's own recording play back to back (no added breath or
// tail); under an overlapping transition only their built-in padding lies.
func TestLayoutOwnVoice(t *testing.T) {
	own := func(tr string) []Segment {
		return []Segment{
			{Video: "a", VideoDur: 2, Voice: "v1", VoiceDur: 2.4, OwnVoice: true, VoiceLead: 0.15, VoiceTail: 0.25, Transition: tr},
			{Video: "b", VideoDur: 2, Voice: "v2", VoiceDur: 2.4, OwnVoice: true, VoiceLead: 0.15, VoiceTail: 0.25},
		}
	}
	s, _ := Layout(MontageOptions{Transition: "fade", Segments: own("")})
	if s[0].voiceAt != 0 || s[0].frames != 58 || s[1].voiceAt != 0 || s[1].Start != s[0].Duration {
		t.Fatalf("dips: %+v", s)
	}
	if !near(s[0].cueStart, 0.15, 1e-9) || !near(s[0].cueSpan, 2.0, 1e-9) || !near(s[1].cueStart, s[1].VoiceStart+0.15, 1e-9) {
		t.Fatalf("cues %v/%v %v", s[0].cueStart, s[0].cueSpan, s[1].cueStart)
	}
	s, _ = Layout(MontageOptions{Transition: "dissolve", TransitionDuration: 0.5, Segments: own("")})
	speech1End := s[0].VoiceStart + 2.4 - 0.25
	speech2Start := s[1].VoiceStart + 0.15
	overlapFrom, overlapTo := s[1].Start, s[0].Start+s[0].Duration
	if speech1End > overlapFrom+1e-9 || speech2Start < overlapTo-1e-9 {
		t.Fatalf("speech under the transition: %.3f/%.3f vs overlap %.3f–%.3f", speech1End, speech2Start, overlapFrom, overlapTo)
	}
	if gap := speech2Start - speech1End; gap > 0.6 {
		t.Fatalf("speech gap %.3f", gap)
	}
	// A generated voice in the same place keeps its breath and tail.
	g := own("")
	g[0].OwnVoice, g[1].OwnVoice = false, false
	if s, _ := Layout(MontageOptions{Transition: "fade", Segments: g}); s[0].voiceAt != 6 || s[0].frames != frames(2.4+0.6) {
		t.Fatalf("tts: %+v", s[0])
	}
}

// The editor's voice controls: trims shorten the line (and a voice-driven
// scene with it); moves place the line elsewhere, clamped into the film,
// without touching the picture; subtitles follow.
func TestLayoutVoiceEdit(t *testing.T) {
	base := func() []Segment {
		return []Segment{
			{Video: "a", VideoDur: 2, Voice: "v1", VoiceDur: 3, Narration: "один"},
			{Video: "b", VideoDur: 2, Voice: "v2", VoiceDur: 3, Narration: "два"},
			{Video: "c", VideoDur: 4, Voice: "v3", VoiceDur: 1, Narration: "три"},
		}
	}
	plain, total := Layout(MontageOptions{Transition: "dissolve", Segments: base()})

	// Moves: +1.5 s, −0.5 s, and far past both ends of the film.
	segs := base()
	segs[0].VoiceShift, segs[1].VoiceShift, segs[2].VoiceShift = -10, 1.5, 60
	moved, total2 := Layout(MontageOptions{Transition: "dissolve", Segments: segs})
	if total2 != total {
		t.Fatalf("a move changed the film: %v → %v", total, total2)
	}
	for k := range plain {
		p, m := plain[k], moved[k]
		if p.Start != m.Start || p.Duration != m.Duration || p.Overlap != m.Overlap || p.ClipDuration != m.ClipDuration || p.VoiceDefaultStart != m.VoiceDefaultStart {
			t.Fatalf("a move changed the picture of scene %d: %+v vs %+v", k+1, p, m)
		}
		if m.VoiceDuration != p.VoiceDuration || m.VoiceEnd != m.VoiceStart+m.VoiceDuration || m.cueStart != m.VoiceStart {
			t.Fatalf("scene %d line %+v", k+1, m)
		}
	}
	if moved[0].VoiceStart != 0 || moved[0].VoiceShift != -moved[0].VoiceDefaultStart { // clamped at 0
		t.Fatalf("early clamp %+v", moved[0])
	}
	if !near(moved[1].VoiceStart, plain[1].VoiceStart+1.5, 1.0/sampleRate) || !near(moved[1].VoiceShift, 1.5, 1.0/sampleRate) {
		t.Fatalf("+1.5 s: %v vs %v", moved[1].VoiceStart, plain[1].VoiceStart)
	}
	if !near(moved[2].VoiceEnd, total, 1.0/sampleRate) || moved[2].VoiceShift <= 0 || moved[2].VoiceShift >= 60 { // clamped at the end
		t.Fatalf("late clamp %+v (film %v)", moved[2], total)
	}
	if moved[1].voiceAt0 != int(math.Round(moved[1].VoiceStart*sampleRate)) || moved[1].voiceLen != 3*sampleRate {
		t.Fatalf("samples %d %d", moved[1].voiceAt0, moved[1].voiceLen)
	}

	// Trims: 0.5 s off the head, 1 s off the tail of line 2 → the scene holds
	// 1.5 s less; the line starts at the default place.
	segs = base()
	segs[1].VoiceTrimIn, segs[1].VoiceTrimOut = 0.5, 1
	trimmed, _ := Layout(MontageOptions{Transition: "dissolve", Segments: segs})
	tr := trimmed[1]
	if tr.VoiceRawDuration != 3 || tr.VoiceDuration != 1.5 || tr.VoiceTrimIn != 0.5 || tr.VoiceTrimOut != 1 ||
		tr.VoiceStart != tr.VoiceDefaultStart || tr.voiceSkip != sampleRate/2 || tr.cueSpan != 1.5 {
		t.Fatalf("trimmed line %+v", tr)
	}
	if d := plain[1].Duration - tr.Duration; !near(d, 1.5, 1.0/fps+1e-9) {
		t.Fatalf("the voice-driven scene should shrink by 1.5 s, shrank %.3f", d)
	}
	// Trims never leave less than 0.1 s.
	segs[1].VoiceTrimIn, segs[1].VoiceTrimOut = 5, 5
	if s, _ := Layout(MontageOptions{Segments: segs}); !near(s[1].VoiceDuration, minVoiceLeft, 1e-9) || s[1].VoiceTrimIn != 2.9 {
		t.Fatalf("over-trim %+v", s[1])
	}

	// Own voice: a head trim eats into the built-in lead; subtitles start on the word.
	own := []Segment{{Video: "a", VideoDur: 4, Voice: "v", VoiceDur: 2.4, OwnVoice: true, VoiceLead: 0.15, VoiceTail: 0.25, VoiceTrimIn: 0.1, VoiceShift: 0.4}}
	s, _ := Layout(MontageOptions{Transition: "fade", Segments: own})
	if !near(s[0].cueStart, s[0].VoiceStart+0.05, 1e-6) || !near(s[0].cueSpan, 2.3-0.05-0.25, 1e-9) || !near(s[0].VoiceShift, 0.4, 1e-6) {
		t.Fatalf("own voice cue %v/%v shift %v", s[0].cueStart-s[0].VoiceStart, s[0].cueSpan, s[0].VoiceShift)
	}
}

// Rounding is diffused over the film: many voice-driven scenes whose
// nominal lengths are not whole frames still add up to the nominal total.
func TestLayoutErrorDiffusion(t *testing.T) {
	var segs []Segment
	nominal := 0.0
	for i := 0; i < 150; i++ {
		v := 1.5 + float64(i%7)*0.137
		segs = append(segs, Segment{Video: "c.mp4", VideoDur: 1, Voice: "v.mp3", VoiceDur: v})
		nominal += v + voiceDelay + voiceTail
	}
	slots, total := Layout(MontageOptions{Transition: "fade", Segments: segs})
	if math.Abs(total-nominal) > 0.5/fps+1e-9 {
		t.Fatalf("total %.4f, nominal %.4f", total, nominal)
	}
	for k, sl := range slots {
		need := frameSec(sl.voiceAt) + sl.VoiceDuration + voiceTail
		if sl.Duration < need-1.0/fps-1e-9 || sl.Duration > need+1.0/fps+1e-9 {
			t.Fatalf("slot %d: %.4f for a %.4f line", k, sl.Duration, need)
		}
	}
	// Clip-driven scenes keep their exact frames.
	slots, _ = Layout(MontageOptions{Transition: "fade", Segments: []Segment{
		{Video: "a", VideoDur: 2.01, Voice: "v", VoiceDur: 1.3}, {Video: "b", VideoDur: 8}, {Video: "c", VideoDur: 3.02},
	}})
	if slots[1].frames != 192 || slots[2].frames != frames(3.02) {
		t.Fatalf("clip frames %d %d", slots[1].frames, slots[2].frames)
	}
}

// ---- subtitle styles ------------------------------------------------------------------------------

func TestSubtitleStyle(t *testing.T) {
	p := SubtitleStyle{}.Resolve()
	if p.ID != "classic" || p.Uppercase || p.Box {
		t.Fatalf("default %+v", p)
	}
	p = SubtitleStyle{Preset: "minimal", Size: 90, Position: "TOP", Color: "#ff0000", Outline: true, Box: true, Uppercase: true}.Resolve()
	if p.Size != 90 || p.SizeVertical != 90 || p.Position != "top" || p.Color != "#ff0000" || p.Outline != 3 || !p.Box || !p.Uppercase {
		t.Fatalf("overrides %+v", p)
	}
	if p := (SubtitleStyle{Preset: "youtube", Color: "yellowish", Position: "left"}).Resolve(); p.Color != "#FFE000" || p.Position != "bottom" {
		t.Fatalf("invalid overrides must be ignored: %+v", p)
	}
	if c := assColor("#FFE000", "#000000", 0); c != "&H0000E0FF" {
		t.Fatal(c)
	}
	if c := assColor("nope", "#102030", 0.4); c != "&H66302010" {
		t.Fatal(c)
	}

	style := func(s SubtitleStyle, W, H int) map[string]string {
		names, values := s.Resolve().assStyle(W, H)
		if len(names) != 23 || len(values) != 23 {
			t.Fatalf("style has %d/%d fields", len(names), len(values))
		}
		m := map[string]string{}
		for i, n := range names {
			m[n] = values[i]
		}
		return m
	}
	expect := func(what string, m map[string]string, want map[string]string) {
		t.Helper()
		for k, v := range want {
			if m[k] != v {
				t.Fatalf("%s: %s = %q, want %q (%v)", what, k, m[k], v, m)
			}
		}
	}
	expect("classic 1080p", style(SubtitleStyle{}, 1920, 1080), map[string]string{
		"Fontname": "Arial", "Fontsize": "64", "Alignment": "2", "MarginV": "86", "MarginL": "115",
		"BorderStyle": "1", "Outline": "3.5", "Bold": "-1", "PrimaryColour": "&H00FFFFFF"})
	// Vertical 720×1280: the vertical size scaled by 720/1080; numpad 5 = middle.
	expect("shorts vertical", style(SubtitleStyle{Preset: "shorts"}, 720, 1280), map[string]string{
		"Fontsize": "61", "Alignment": "5", "MarginV": "0", "Outline": "4"})
	expect("top", style(SubtitleStyle{Position: "top"}, 1280, 720), map[string]string{"Alignment": "8", "MarginV": "43"})
	expect("plate", style(SubtitleStyle{Preset: "plate"}, 1280, 720), map[string]string{
		"BorderStyle": "3", "OutlineColour": "&H0DFFFFFF", "Shadow": "0", "PrimaryColour": "&H00141414", "Bold": "-1"})
	expect("vertical bottom", style(SubtitleStyle{Position: "bottom"}, 1080, 1920), map[string]string{"MarginV": "576", "Fontsize": "72"})

	// The burnt-in script: same cues as the .srt, escaped, in the style.
	ass := buildASS([]srtSeg{{narration: "Привіт, {світе} \\ зараз. " + strings.Repeat("далі ", 12), start: 1.006, span: 4}},
		SubtitleStyle{Preset: "shorts"}.Resolve(), 1080, 1920)
	for _, want := range []string{"PlayResX: 1080\n", "PlayResY: 1920\n", "\nStyle: Default,Arial,92,", "Dialogue: 0,0:00:01.01,", "ПРИВІТ, (СВІТЕ) / ЗАРАЗ.", `\N`} {
		if !strings.Contains(ass, want) {
			t.Fatalf("ass lacks %q:\n%s", want, ass)
		}
	}
	if buildASS(nil, SubtitleStyle{}.Resolve(), 640, 360) != "" || assTime(3725.004) != "1:02:05.00" {
		t.Fatal("ass edge cases")
	}
}

// ---- audio mix -------------------------------------------------------------------------------

// writePCM writes n stereo samples of value v (raw s16le) and returns the path.
func writePCM(t *testing.T, dir, name string, n int, v int16) string {
	t.Helper()
	b := make([]byte, n*4)
	for i := 0; i < 2*n; i++ {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(v))
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// readWAV returns the left-channel samples of a mixParts WAV.
func readWAV(t *testing.T, path string, total int) []int16 {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 44+total*4 || string(b[:4]) != "RIFF" || binary.LittleEndian.Uint32(b[40:]) != uint32(total*4) {
		t.Fatalf("wav size %d for %d samples", len(b), total)
	}
	out := make([]int16, total)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[44+i*4:]))
	}
	return out
}

func TestMixParts(t *testing.T) {
	dir := t.TempDir()
	f := samplesPerFrame
	// Two scene parts overlapping by 2 frames, a third one short (padded).
	parts := []pcmPart{
		{writePCM(t, dir, "a", 10*f, 1000), 0, 10 * f},
		{writePCM(t, dir, "b", 8*f, 2000), 8 * f, 8 * f},
		{writePCM(t, dir, "c", 6*f-250, -32000), 16 * f, 6 * f},
	}
	out := filepath.Join(dir, "mix.wav")
	if err := mixParts(out, 22*f, parts, false); err != nil {
		t.Fatal(err)
	}
	s := readWAV(t, out, 22*f)
	for _, c := range []struct{ at, want int }{
		{0, 1000}, {8*f - 1, 1000}, {8 * f, 3000}, {10*f - 1, 3000}, {10 * f, 2000}, {16*f - 1, 2000},
		{16 * f, -32000}, {22*f - 251, -32000}, {22*f - 1, 0},
	} {
		if s[c.at] != int16(c.want) {
			t.Fatalf("sample %d = %d, want %d", c.at, s[c.at], c.want)
		}
	}
	// Without the limiter a sum beyond 16 bits saturates.
	sat := filepath.Join(dir, "sat.wav")
	if err := mixParts(sat, 1000, []pcmPart{{writePCM(t, dir, "x", 1000, 30000), 0, 1000}, {writePCM(t, dir, "y", 1000, 30000), 0, 1000}}, false); err != nil {
		t.Fatal(err)
	}
	if v := readWAV(t, sat, 1000)[500]; v != math.MaxInt16 {
		t.Fatalf("saturation %d", v)
	}
}

// Overlapping voice lines are summed but never louder than −1 dBFS; the
// limiter leaves everything else untouched and recovers to unity gain.
func TestMixLimiter(t *testing.T) {
	dir := t.TempDir()
	const total = 3 * sampleRate
	line1 := writePCM(t, dir, "l1", sampleRate, 25000)   // 0 … 1 s
	line2 := writePCM(t, dir, "l2", sampleRate/2, 25000) // moved over line 1: 0.75 … 1.25 s
	quiet := writePCM(t, dir, "q", total, 300)
	out := filepath.Join(dir, "lim.wav")
	err := mixParts(out, total, []pcmPart{{quiet, 0, total}, {line1, 0, sampleRate}, {line2, sampleRate * 3 / 4, sampleRate / 2}}, true)
	if err != nil {
		t.Fatal(err)
	}
	s := readWAV(t, out, total)
	peak := 0.0
	for _, v := range s {
		peak = math.Max(peak, math.Abs(float64(v)))
	}
	if peak > limCeil+1 {
		t.Fatalf("peak %.0f above −1 dBFS (%.0f)", peak, limCeil)
	}
	if s[1000] != 25300 || s[sampleRate/2] != 25300 { // before the overlap: untouched
		t.Fatalf("limited outside the overlap: %d %d", s[1000], s[sampleRate/2])
	}
	if v := s[sampleRate*7/8]; float64(v) < limCeil-200 { // in the overlap: held at the ceiling
		t.Fatalf("overlap %d", v)
	}
	if s[2*sampleRate] != 300 || s[total-1] != 300 { // well after: back to unity
		t.Fatalf("no recovery: %d %d", s[2*sampleRate], s[total-1])
	}
}
