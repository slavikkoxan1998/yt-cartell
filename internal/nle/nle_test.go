package nle

// Writers on a fixed synthetic timeline: golden files (go test
// ./internal/nle -run TestGolden -update rewrites them), well-formed XML,
// the timing math of each format, and — when FCPXML_DTD points to Apple's
// FCPXMLv1_10.dtd (not shipped: Apple's copyright) — a DTD check of both
// FCPXML flavours.

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixture is a 10 s film at 24 fps: two clips with a dissolve, a hold, a
// still with a Ken Burns move behind a dip to black, a text on V2, the
// clips' sound with a crossfade, a voice line and looped music with levels.
func fixture() *Timeline {
	movie := func(id, name string, dur float64) *Media {
		return &Media{ID: id, Name: name, Rel: "media/" + name, Abs: "C:/Export/Фільм/media/" + name, Kind: Movie,
			Duration: dur, Rate: Rate24, Width: 1920, Height: 1080, HasAudio: true, SampleRate: 48000, Channels: 2}
	}
	pic := func(id, name string, alpha bool) *Media {
		return &Media{ID: id, Name: name, Rel: "media/" + name, Abs: "C:/Export/Фільм/media/" + name, Kind: Picture,
			Width: 1920, Height: 1080, Alpha: alpha}
	}
	sound := func(id, name string, dur float64) *Media {
		return &Media{ID: id, Name: name, Rel: "media/" + name, Abs: "C:/Export/Фільм/media/" + name, Kind: Sound,
			Duration: dur, SampleRate: 44100, Channels: 1}
	}
	m1, m2 := movie("m001_scene", "001_scene.mp4", 6), movie("m002_scene", "002_scene.mp4", 5)
	hold, still, text := pic("m002_hold", "002_hold.jpg", false), pic("m003_frame", "003_frame.jpg", false), pic("m001_text", "001_text.png", true)
	voice, music := sound("m001_voice", "001_voice.mp3", 2), sound("mmusic", "music.mp3", 6)
	c1 := Clip{Media: m1, Name: "Сцена 1 · Початок", Start: 0, Len: 96, In: 12, Gain: -12, FadeIn: 7, Scene: 1, Role: RoleScene}
	c2 := Clip{Media: m2, Name: "Сцена 2", Start: 96, Len: 54, In: 6, Gain: -12, Scene: 2, Role: RoleScene}
	return &Timeline{
		Name: "Тест · фільм", Rate: Rate24, Width: 1920, Height: 1080, SampleRate: 48000, Length: 240,
		Video: []Track{
			{Name: "V1 · Сцени", Kind: VideoTrack, Clips: []Clip{
				c1, c2,
				{Media: hold, Name: "Сцена 2 · утримання кадру", Start: 150, Len: 20, Scene: 2, Role: RoleHold},
				{Media: still, Name: "Сцена 3 · кадр", Start: 170, Len: 70, FadeOut: 7, Scene: 3, Role: RoleStill,
					Motion: []Key{{At: 0, Scale: 1.12, X: 86.4, Y: 0}, {At: 69, Scale: 1.12, X: -86.4, Y: 0}}},
			}, Transitions: []Transition{
				{At: 96, Len: 12, Kind: Dissolve, Source: "dissolve"},
				{At: 170, Len: 14, Kind: DipBlack, Source: "fade"},
			}},
			{Name: "V2 · Текст", Kind: VideoTrack, Clips: []Clip{
				{Media: text, Name: "Сцена 1 · текст", Start: 20, Len: 60, FadeIn: 7, FadeOut: 7, Scene: 1, Role: RoleText},
			}},
		},
		Audio: []Track{
			{Name: "A1 · Звук кліпів", Kind: AudioTrack, SceneSound: true, Clips: []Clip{
				func() Clip { c := c1; c.Role = RoleSound; return c }(),
				func() Clip { c := c2; c.Role = RoleSound; return c }(),
			}, Transitions: []Transition{{At: 96, Len: 12, Kind: Dissolve, Source: "dissolve"}}},
			{Name: "A2 · Голос", Kind: AudioTrack, Clips: []Clip{
				{Media: voice, Name: "Сцена 1 · голос", Start: 24, Len: 48, Scene: 1, Role: RoleVoice},
			}},
			{Name: "Музика", Kind: AudioTrack, Clips: []Clip{
				{Media: music, Name: "Музика", Start: 0, Len: 144, Role: RoleMusic, Gain: -14,
					Levels: []Level{{0, -14}, {22, -14}, {24, -26}, {72, -26}, {83, -14}, {143, -14}}},
				{Media: music, Name: "Музика", Start: 144, Len: 96, Role: RoleMusic, Gain: -14,
					Levels: []Level{{0, -14}, {36, -14}, {95, -96}}},
			}},
		},
		Markers: []Marker{{0, "Сцена 1 · Початок"}, {96, "Сцена 2"}, {170, "Сцена 3"}},
	}
}

func TestRates(t *testing.T) {
	cases := []struct {
		fps  float64
		want Rate
	}{{24, Rate24}, {23.976023976, Rate{24000, 1001}}, {23.98, Rate{24, 1}}, {25, Rate25}, {29.97002997, Rate{30000, 1001}},
		{30, Rate30}, {59.94005994, Rate{60000, 1001}}, {12.5, Rate{13, 1}}, {0, Rate24}, {math.NaN(), Rate24}, {1000, Rate24}}
	for _, c := range cases {
		if got := RateOf(c.fps); got != c.want {
			t.Errorf("RateOf(%v) = %v, want %v", c.fps, got, c.want)
		}
	}
	ntsc := Rate{30000, 1001}
	if ntsc.Timebase() != 30 || !ntsc.NTSC() || ntsc.String() != "29.97" || Rate24.String() != "24" {
		t.Fatalf("ntsc %v %v %q", ntsc.Timebase(), ntsc.NTSC(), ntsc.String())
	}
	if ntsc.Frames(10) != 300 || math.Abs(ntsc.Seconds(300)-10.01) > 1e-9 {
		t.Fatalf("frames %d seconds %v", ntsc.Frames(10), ntsc.Seconds(300))
	}
	for _, c := range []struct {
		n    int64
		r    Rate
		want string
	}{{0, Rate24, "0s"}, {48, Rate24, "2s"}, {12, Rate24, "1/2s"}, {7, Rate24, "7/24s"}, {1, ntsc, "1001/30000s"}, {30, ntsc, "1001/1000s"}} {
		if got := fcpTime(secs(c.n, c.r)); got != c.want {
			t.Errorf("fcpTime(%d @ %v) = %q, want %q", c.n, c.r, got, c.want)
		}
	}
	if tc := timecode(24*3661+5, Rate24); tc != "01:01:01:05" {
		t.Fatalf("timecode %s", tc)
	}
	// A backslash separates folders only on Windows.
	if runtime.GOOS == "windows" {
		if u := FileURL(`C:\Users\Іван\Відео\a b.mp4`); u != "file:///C:/Users/%D0%86%D0%B2%D0%B0%D0%BD/%D0%92%D1%96%D0%B4%D0%B5%D0%BE/a%20b.mp4" {
			t.Fatalf("file url %s", u)
		}
	} else if u := FileURL(`/Users/Іван/Відео/a b.mp4`); u != "file:///Users/%D0%86%D0%B2%D0%B0%D0%BD/%D0%92%D1%96%D0%B4%D0%B5%D0%BE/a%20b.mp4" {
		t.Fatalf("file url %s", u)
	}
	if u := fileURLLocalhost(`C:/Export/a b.mp4`); u != "file://localhost/C%3a/Export/a%20b.mp4" {
		t.Fatalf("xmeml url %s", u)
	}
}

func TestValidate(t *testing.T) {
	if err := fixture().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(t *Timeline){
		"overlap":          func(t *Timeline) { t.Video[0].Clips[1].Start = 90 },
		"outside":          func(t *Timeline) { t.Video[0].Clips[3].Len = 80 },
		"past the media":   func(t *Timeline) { t.Video[0].Clips[0].In = 60 },
		"not on a cut":     func(t *Timeline) { t.Video[0].Transitions[0].At = 100 },
		"no handle before": func(t *Timeline) { t.Video[0].Clips[1].In = 2 },
		"sound on video":   func(t *Timeline) { t.Video[1].Clips[0].Media = t.Audio[1].Clips[0].Media },
		"no rate":          func(t *Timeline) { t.Rate = Rate{} },
	}
	for name, mut := range bad {
		tl := fixture()
		mut(tl)
		if tl.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A dip needs no handles: each side shows only on its own half.
	tl := fixture()
	tl.Video[0].Transitions[1] = Transition{At: 96, Len: 14, Kind: DipBlack}
	tl.Video[0].Transitions = tl.Video[0].Transitions[1:]
	tl.Video[0].Clips[1].In = 0
	if err := tl.Validate(); err != nil {
		t.Fatalf("dip without handles: %v", err)
	}
}

// golden compares data with testdata/name (rewritten with -update).
func golden(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != string(data) {
		t.Fatalf("%s differs from the golden file (run with -update after checking the change)\n--- got:\n%s", name, data)
	}
}

func wellFormed(t *testing.T, name string, data []byte) {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(string(data)))
	d.Strict = true
	for {
		_, err := d.Token()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestGolden(t *testing.T) {
	tl := fixture()
	outs := map[string]func() ([]byte, error){
		"timeline.fcpxml":         func() ([]byte, error) { return FCPXML(tl, FCPXMLOptions{}) },
		"timeline-resolve.fcpxml": func() ([]byte, error) { return FCPXML(tl, FCPXMLOptions{StaticStills: true}) },
		"timeline.xml":            func() ([]byte, error) { return XMEML(tl, XMEMLOptions{}) },
		"timeline.otio":           func() ([]byte, error) { return OTIO(tl, OTIOOptions{}) },
		"timeline.edl":            func() ([]byte, error) { return EDL(tl) },
		"timeline.mlt":            func() ([]byte, error) { return MLT(tl, MLTOptions{}) },
	}
	for name, fn := range outs {
		data, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, _ := fn()
		if string(again) != string(data) {
			t.Fatalf("%s is not deterministic", name)
		}
		if strings.HasSuffix(name, "xml") || strings.HasSuffix(name, ".mlt") {
			wellFormed(t, name, data)
		}
		golden(t, name, data)
	}
}

var reRatio = regexp.MustCompile(`^(\d+)(?:/(\d+))?s$`)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	m := reRatio.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("bad time %q", s)
	}
	r, _ := new(big.Rat).SetString(m[1])
	if m[2] != "" {
		d, _ := new(big.Rat).SetString(m[2])
		r.Quo(r, d)
	}
	return r
}

// The FCPXML storyline: contiguous scenes, transitions centred on the cuts,
// connected clips at their film time, keyframes in the clips' local time.
func TestFCPXMLTiming(t *testing.T) {
	tl := fixture()
	data, err := FCPXML(tl, FCPXMLOptions{})
	if err != nil {
		t.Fatal(err)
	}
	type anchored struct {
		XMLName xml.Name
		Lane    string `xml:"lane,attr"`
		Offset  string `xml:"offset,attr"`
		Name    string `xml:"name,attr"`
	}
	type spineItem struct {
		XMLName  xml.Name
		Offset   string     `xml:"offset,attr"`
		Start    string     `xml:"start,attr"`
		Duration string     `xml:"duration,attr"`
		Name     string     `xml:"name,attr"`
		Anchored []anchored `xml:",any"`
	}
	var doc struct {
		Seq struct {
			Duration string `xml:"duration,attr"`
			Spine    struct {
				Items []spineItem `xml:",any"`
			} `xml:"spine"`
		} `xml:"library>event>project>sequence"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if got := rat(t, doc.Seq.Duration); got.Cmp(big.NewRat(10, 1)) != 0 {
		t.Fatalf("sequence %v", got)
	}
	f := func(n int64) *big.Rat { return big.NewRat(n, 24) }
	var kinds []string
	at := new(big.Rat)
	film := map[string]*big.Rat{} // connected clip name → film time
	for _, it := range doc.Seq.Spine.Items {
		kinds = append(kinds, it.XMLName.Local)
		off := rat(t, it.Offset)
		if it.XMLName.Local == "transition" {
			continue
		}
		if off.Cmp(at) != 0 {
			t.Fatalf("%s at %v, expected %v", it.Name, off, at)
		}
		for _, a := range it.Anchored {
			if a.Lane == "" {
				continue
			}
			// film time = parent offset + (anchor offset − parent start)
			ft := new(big.Rat).Add(off, new(big.Rat).Sub(rat(t, a.Offset), rat(t, it.Start)))
			film[a.Name] = ft
		}
		at.Add(at, rat(t, it.Duration))
	}
	want := []string{"asset-clip", "transition", "asset-clip", "video", "transition", "video"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("storyline %v", kinds)
	}
	if doc.Seq.Spine.Items[1].Offset != "15/4s" || doc.Seq.Spine.Items[1].Duration != "1/2s" {
		t.Fatalf("dissolve at %s for %s (centred on 4 s)", doc.Seq.Spine.Items[1].Offset, doc.Seq.Spine.Items[1].Duration)
	}
	if doc.Seq.Spine.Items[0].Start != "1/2s" || doc.Seq.Spine.Items[2].Start != "1/4s" {
		t.Fatalf("source ins %s %s", doc.Seq.Spine.Items[0].Start, doc.Seq.Spine.Items[2].Start)
	}
	for name, frame := range map[string]int64{"Сцена 1 · текст": 20, "Сцена 1 · голос": 24} {
		if got := film[name]; got == nil || got.Cmp(f(frame)) != 0 {
			t.Fatalf("%s at %v, expected frame %d", name, got, frame)
		}
	}
	// Music: the first piece at film 0 = 1/2 s in scene 1's local time (its in point).
	if !strings.Contains(string(data), `lane="-2" offset="1/2s" name="Музика" start="0s" duration="6s"`) {
		t.Fatal("first music piece")
	}
	// Resolve flavour: stills without keyframes, the move's middle framing.
	res, _ := FCPXML(tl, FCPXMLOptions{StaticStills: true})
	if strings.Count(string(res), "<keyframeAnimation>") != strings.Count(string(data), "<keyframeAnimation>")-4 {
		t.Fatalf("static stills: %d vs %d keyframe animations", strings.Count(string(res), "<keyframeAnimation>"), strings.Count(string(data), "<keyframeAnimation>"))
	}
	if !strings.Contains(string(res), `<adjust-transform position="0 0" scale="1.12 1.12" rotation="0"/>`) {
		t.Fatal("static still framing")
	}
}

// xmeml: clips next to a transition have start/end −1 and in/out reaching
// under it; levels are linear gains with keyframes in media frames.
func TestXMEMLTiming(t *testing.T) {
	data, err := XMEML(fixture(), XMEMLOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Video []struct {
			Items []struct {
				XMLName xml.Name
				Name    string `xml:"name"`
				Start   int64  `xml:"start"`
				End     int64  `xml:"end"`
				In      int64  `xml:"in"`
				Out     int64  `xml:"out"`
			} `xml:",any"`
		} `xml:"sequence>media>video>track"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	v1 := doc.Video[0].Items
	type row struct {
		kind                string
		start, end, in, out int64
	}
	var got []row
	for _, it := range v1 {
		if it.XMLName.Local == "clipitem" || it.XMLName.Local == "transitionitem" {
			got = append(got, row{it.XMLName.Local, it.Start, it.End, it.In, it.Out})
		}
	}
	base := Rate24.Frames(10) // stills start 10 s into their "file"
	want := []row{
		{"clipitem", 0, -1, 12, 114},               // ends in the dissolve: out reaches 6 frames past the cut
		{"transitionitem", 90, 102, 0, 0},          //
		{"clipitem", -1, 150, 0, 60},               // starts in it: in reaches 6 frames before
		{"clipitem", 150, -1, base, base + 27},     // the hold, into the dip
		{"transitionitem", 163, 177, 0, 0},         //
		{"clipitem", -1, 240, base - 7, base + 70}, // the still, from the dip
	}
	if len(got) != len(want) {
		t.Fatalf("V1 %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("V1 item %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	s := string(data)
	for _, frag := range []string{
		"<pathurl>file://localhost/C%3a/Export/%D0%A4%D1%96%D0%BB%D1%8C%D0%BC/media/001_scene.mp4</pathurl>",
		"<effectid>Dip to Color Dissolve</effectid>", "<effectid>KGAudioTransCrossFade3dB</effectid>",
		"<value>0.251189</value>", // −12 dB
		"<when>240</when>",        // the still's Ken Burns starts at its in point (10 s base)
		"<stillframe>TRUE</stillframe>",
	} {
		if !strings.Contains(s, frag) {
			t.Errorf("xmeml lacks %s", frag)
		}
	}
}

// OTIO: every track as long as its last clip, transitions around the cut,
// doubles for RationalTime values.
func TestOTIOTiming(t *testing.T) {
	data, err := OTIO(fixture(), OTIOOptions{Relative: true})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	tracks := doc["tracks"].(map[string]any)["children"].([]any)
	if len(tracks) != 5 {
		t.Fatalf("%d tracks", len(tracks))
	}
	ends := []float64{240, 80, 150, 72, 240}
	for i, tr := range tracks {
		sum := 0.0
		for _, ch := range tr.(map[string]any)["children"].([]any) {
			item := ch.(map[string]any)
			switch item["OTIO_SCHEMA"] {
			case "Clip.2", "Gap.1":
				sum += item["source_range"].(map[string]any)["duration"].(map[string]any)["value"].(float64)
				if item["OTIO_SCHEMA"] == "Clip.2" {
					ref := item["media_references"].(map[string]any)["DEFAULT_MEDIA"].(map[string]any)
					if !strings.HasPrefix(ref["target_url"].(string), "media/") {
						t.Fatalf("relative target %v", ref["target_url"])
					}
				}
			case "Transition.1":
				in := item["in_offset"].(map[string]any)["value"].(float64)
				out := item["out_offset"].(map[string]any)["value"].(float64)
				if in+out <= 0 {
					t.Fatalf("transition %v", item)
				}
			}
		}
		if sum != ends[i] {
			t.Fatalf("track %d ends at %v, want %v", i, sum, ends[i])
		}
	}
	if !strings.Contains(string(data), `"rate": 24.0,`) || !strings.Contains(string(data), `"OTIO_SCHEMA": "Marker.2"`) {
		t.Fatal("RationalTime must be written as doubles; markers on the stack")
	}
	old, _ := OTIO(fixture(), OTIOOptions{Clip1: true})
	if !strings.Contains(string(old), `"OTIO_SCHEMA": "Clip.1"`) || !strings.Contains(string(old), `"media_reference": {`) {
		t.Fatal("Clip.1 flavour")
	}
}

// EDL: record times contiguous; a dissolve is the outgoing clip at zero
// length followed by the incoming one with D and the length.
func TestEDLEvents(t *testing.T) {
	data, err := EDL(fixture())
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^(\d{3})  AX       V     (C   |D   ) (\d{3}|   ) (\S+) (\S+) (\S+) (\S+)$`)
	ms := re.FindAllStringSubmatch(string(data), -1)
	if len(ms) != 6 {
		t.Fatalf("events:\n%s", data)
	}
	frames := func(tc string) int64 {
		n := int64(0)
		for i, p := range strings.Split(tc, ":") {
			v, _ := strconv.ParseInt(p, 10, 64)
			if i < 3 {
				n = n*60 + v
			} else {
				n = n*24 + v
			}
		}
		return n
	}
	at := int64(0)
	for _, m := range ms {
		recIn, recOut := frames(m[6]), frames(m[7])
		if recIn != at {
			t.Fatalf("event %s records from %d, expected %d:\n%s", m[1], recIn, at, data)
		}
		at = recOut
		if m[2] == "D   " && m[3] == "012" && recIn != 90 {
			t.Fatalf("the dissolve starts at %d", recIn)
		}
	}
	if at != 240 || !strings.HasPrefix(string(data), "TITLE: Test - film\n") {
		t.Fatalf("ends at %d:\n%s", at, data)
	}
}

// MLT: every playlist (blanks + entries, out inclusive) fits the film; a
// dissolve is a transition tractor in the playlist.
func TestMLTPlaylists(t *testing.T) {
	data, err := MLT(fixture(), MLTOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Playlists []struct {
			ID    string `xml:"id,attr"`
			Items []struct {
				XMLName  xml.Name
				Producer string `xml:"producer,attr"`
				In       int64  `xml:"in,attr"`
				Out      int64  `xml:"out,attr"`
				Length   int64  `xml:"length,attr"`
			} `xml:",any"`
		} `xml:"playlist"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	lengths := map[string]int64{}
	for _, p := range doc.Playlists {
		for _, it := range p.Items {
			switch it.XMLName.Local {
			case "entry":
				lengths[p.ID] += it.Out - it.In + 1
			case "blank":
				lengths[p.ID] += it.Length
			}
		}
	}
	if lengths["playlist0"] != 240 || lengths["background"] != 240 || lengths["playlist1"] != 80 || lengths["playlist4"] != 240 {
		t.Fatalf("playlist lengths %v", lengths)
	}
	s := string(data)
	if !strings.Contains(s, `<property name="mlt_service">luma</property>`) || !strings.Contains(s, `<property name="resource">media/001_scene.mp4</property>`) ||
		!strings.Contains(s, `<property name="level">0=-14;22=-14;24=-26;72=-26;83=-14;143=-14</property>`) {
		t.Fatal("dissolve tractor, relative resources, volume keyframes")
	}
}
