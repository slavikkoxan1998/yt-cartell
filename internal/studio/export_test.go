package studio

// Editor export (export.go) on a project built from generated media
// (ffmpeg lavfi: testsrc2 clips, sine voices/music) in a temp data folder.
// EXPORT_KEEP=<dir> writes the packages there instead (they outlive the
// test's temp project: for a look or an import into an editor).

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"ytcartell/internal/media"
	"ytcartell/internal/nle"
)

// exportProject makes a 5-scene project that uses every timeline feature:
// trims, a hold (voice longer than the clip), a still with a motion
// effect, a dissolve, a wipe (approximated), a dip to black with media
// past the cut (a transition) and a dip to white without (fades), a moved
// voice line, on-screen text, music with ducking, a cover.
func exportProject(t *testing.T, e *Engine) (Project, string) {
	t.Helper()
	p, err := e.Create("Експорт · тест")
	must(t, err)
	dir := e.Dir(p.ID)
	ctx := context.Background()
	gen := func(rel string, args ...string) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(path), 0o755))
		must(t, testFF.Run(ctx, "", append(args, path)...))
		return rel
	}
	clip := func(rel string, sec float64, hz int) string {
		return gen(rel, "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:sample_rate=48000", hz),
			"-t", strconv.FormatFloat(sec, 'f', 3, 64), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	}
	voice := func(rel string, sec float64) string {
		return gen(rel, "-f", "lavfi", "-i", "sine=frequency=300:sample_rate=44100", "-t", strconv.FormatFloat(sec, 'f', 3, 64), "-c:a", "libmp3lame", "-b:a", "64k")
	}
	c1, c2, c4, c5 := clip("videos/s1.mp4", 3, 440), clip("videos/s2.mp4", 2, 550), clip("videos/s4.mp4", 2.5, 660), clip("videos/s5.mp4", 3, 770)
	f3 := gen("images/s3.jpg", "-f", "lavfi", "-i", "color=c=0x3366aa:s=400x200", "-frames:v", "1")
	v1, v2, v3, v4 := voice("audio/v1.mp3", 1.2), voice("audio/v2.mp3", 3.4), voice("audio/v3.mp3", 1.0), voice("audio/v4.mp3", 1.5)
	music := gen("user/music.mp3", "-f", "lavfi", "-i", "sine=frequency=200:sample_rate=44100", "-t", "4", "-c:a", "libmp3lame", "-b:a", "64k")
	cov := gen("images/cover-final-1.jpg", "-f", "lavfi", "-i", "color=c=0xaa3366:s=320x180", "-frames:v", "1")
	e.update(e.state(p.ID), func(p *Project) {
		p.Input.Voiceover = true
		p.Input.Overlays = "on"
		m := &p.Montage
		m.Transition, m.TransitionDuration, m.Effect = "fade", 0.5, "zoomin"
		m.MusicPath, m.MusicVolume, m.Ducking, m.AmbientVolume = music, 0.2, true, 0.25
		m.Subtitles = "srt"
		m.CoverFinal = doneAsset(cov, 0)
		p.Scenes = []Scene{
			{ID: "s1", Title: "Початок", Narration: "Перша сцена.", Video: doneAsset(c1, 3), Audio: doneAsset(v1, 1.2), TrimIn: 0.5, Transition: "dissolve",
				OnScreen: OnScreen{Title: "Рік", Value: "1914"}},
			{ID: "s2", Title: "Довгий голос", Narration: "Голос довший за кліп, тож кадр утримується.", Video: doneAsset(c2, 2), Audio: doneAsset(v2, 3.4),
				Transition: "wipeleft"},
			{ID: "s3", Title: "Кадр", Narration: "Сцена без відео.", Frame: doneAsset(f3, 0), Audio: doneAsset(v3, 1), Effect: "panright"},
			{ID: "s4", Title: "Зсунутий голос", Narration: "Четверта.", Video: doneAsset(c4, 2.5), Audio: doneAsset(v4, 1.5), VoiceShift: 0.3,
				TrimIn: 0.4, TrimOut: 0.25, Transition: "fadewhite"},
			{ID: "s5", Title: "Кінець", Narration: "", Video: doneAsset(c5, 3)},
		}
	})
	p, _ = e.Get(p.ID)
	return p, dir
}

// keepDir points the export at EXPORT_KEEP when set.
func keepDir(opt *ExportOptions) bool {
	if keep := os.Getenv("EXPORT_KEEP"); keep != "" {
		opt.Dir = keep
		return true
	}
	return false
}

func keepExport(t *testing.T, res ExportResult) {
	if os.Getenv("EXPORT_KEEP") != "" {
		t.Logf("kept %s", res.Dir)
	}
}

// xmlOK parses data fully (well-formed) and counts elements by name.
func xmlOK(t *testing.T, name string, data []byte) map[string]int {
	t.Helper()
	counts := map[string]int{}
	d := xml.NewDecoder(strings.NewReader(string(data)))
	d.Strict = true
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("%s is not well-formed XML: %v", name, err)
		}
		if se, ok := tok.(xml.StartElement); ok {
			counts[se.Name.Local]++
		}
	}
	return counts
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return b
}

var reRat = regexp.MustCompile(`^(\d+)(?:/(\d+))?s$`)

// ratSec parses an FCPXML time.
func ratSec(t *testing.T, s string) float64 {
	t.Helper()
	m := reRat.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("bad FCPXML time %q", s)
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	d := 1.0
	if m[2] != "" {
		d, _ = strconv.ParseFloat(m[2], 64)
	}
	return n / d
}

func TestExportReadiness(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, dir := exportProject(t, e)
	r := exportReadiness(&p, dir)
	if r.Scenes != 5 || r.Clips != 4 || r.Stills != 1 || r.Skipped != 0 || r.Voices != 4 || r.MissingVoices != 0 {
		t.Fatalf("readiness %+v", r)
	}
	if !r.Music || !r.Subtitles || !r.Overlays || !r.Cover || r.Blocker != "" {
		t.Fatalf("readiness %+v", r)
	}
	if r.Complete() || !strings.Contains(r.Missing(), "1 сцена без відео") {
		t.Fatalf("missing %q", r.Missing())
	}
	// Without Partial an incomplete project is refused.
	if _, err := e.ExportTimeline(p.ID, DefaultExportOptions(ExportResolve)); !errors.Is(err, ErrExportIncomplete) {
		t.Fatalf("incomplete export: %v", err)
	}
	// Nothing to export at all.
	q, _ := e.Create("Порожній")
	r2, _ := e.ExportCheck(q.ID)
	if r2.Blocker == "" {
		t.Fatal("an empty project must be blocked")
	}
	if _, err := e.ExportTimeline(q.ID, DefaultExportOptions(ExportOTIO)); err == nil {
		t.Fatal("exported an empty project")
	}
}

func TestExportResolve(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, dir := exportProject(t, e)
	opt := DefaultExportOptions(ExportResolve)
	opt.Partial, opt.Zip, opt.Stems = true, true, true
	kept := keepDir(&opt)
	var last int
	opt.Progress = func(pct int, msg string) {
		if pct < last {
			t.Errorf("progress went back: %d → %d (%s)", last, pct, msg)
		}
		last = pct
	}
	res, err := e.ExportTimelineContext(context.Background(), p.ID, opt)
	must(t, err)
	keepExport(t, res)
	if last != 100 || !kept && !strings.HasPrefix(res.Dir, filepath.Join(dir, "export", "resolve-")) {
		t.Fatalf("result %+v, progress %d", res, last)
	}
	for _, f := range []string{"timeline.xml", "timeline.fcpxml", "timeline.edl", "subtitles.srt", "README.txt", "edit-list.csv", "cover.jpg",
		"media/001_scene.mp4", "media/002_scene.mp4", "media/002_hold.jpg", "media/003_frame.jpg", "media/004_scene.mp4",
		"media/001_voice.mp3", "media/002_voice.mp3", "media/music.mp3", "media/001_text.png", "stems/voice.wav", "stems/music.wav"} {
		if !isFile(filepath.Join(res.Dir, filepath.FromSlash(f))) {
			t.Errorf("missing %s", f)
		}
	}
	if !isFile(res.Zip) {
		t.Fatal("no zip")
	}

	// The film's length everywhere: the montage plan.
	opts := montageOptions(&p, dir)
	for i := range opts.Segments {
		s := &opts.Segments[i]
		if info, err := testFF.Probe(context.Background(), s.Video); s.Video != "" && err == nil {
			s.VideoDur = info.Duration
		}
		if info, err := testFF.Probe(context.Background(), s.Voice); s.Voice != "" && err == nil {
			s.VoiceDur = info.Duration
		}
	}
	slots, total := media.Layout(opts)
	if len(slots) != 5 {
		t.Fatalf("%d slots", len(slots))
	}
	frames := int64(math.Round(total * 24))

	// FCPXML: well-formed, the sequence as long as the film, the storyline contiguous.
	raw := readFile(t, filepath.Join(res.Dir, "timeline.fcpxml"))
	counts := xmlOK(t, "timeline.fcpxml", raw)
	// The dissolve, the wipe (as a dissolve) and the dip with media past
	// the cut are transitions; the other dip is two fades (adjust-blend).
	if counts["transition"] != 3 || counts["asset-clip"] < 4 || counts["video"] < 2 || counts["adjust-blend"] < 2 {
		t.Fatalf("fcpxml elements %v", counts)
	}
	var doc struct {
		Version   string `xml:"version,attr"`
		Resources struct {
			Assets []struct {
				Name string `xml:"name,attr"`
				Rep  struct {
					Src string `xml:"src,attr"`
				} `xml:"media-rep"`
			} `xml:"asset"`
		} `xml:"resources"`
		Seq struct {
			Duration string `xml:"duration,attr"`
			Spine    struct {
				Items []struct {
					XMLName  xml.Name
					Offset   string `xml:"offset,attr"`
					Duration string `xml:"duration,attr"`
					Ref      string `xml:"ref,attr"`
					Anchored []struct {
						XMLName xml.Name
						Lane    string `xml:"lane,attr"`
					} `xml:",any"`
				} `xml:",any"`
			} `xml:"spine"`
		} `xml:"library>event>project>sequence"`
	}
	must(t, xml.Unmarshal(raw, &doc))
	if doc.Version != "1.10" {
		t.Fatalf("version %q", doc.Version)
	}
	if got := ratSec(t, doc.Seq.Duration); math.Abs(got-float64(frames)/24) > 1e-9 {
		t.Fatalf("sequence %v s, film %v s", got, float64(frames)/24)
	}
	at := 0.0
	lanes := map[string]int{}
	for _, it := range doc.Seq.Spine.Items {
		if it.XMLName.Local == "transition" {
			continue
		}
		if off := ratSec(t, it.Offset); math.Abs(off-at) > 1e-9 {
			t.Fatalf("%s %s at %v, expected %v", it.XMLName.Local, it.Ref, off, at)
		}
		at += ratSec(t, it.Duration)
		for _, a := range it.Anchored {
			if a.Lane != "" {
				lanes[a.Lane]++
			}
		}
	}
	if math.Abs(at-float64(frames)/24) > 1e-9 {
		t.Fatalf("storyline ends at %v, film %v", at, float64(frames)/24)
	}
	if lanes["1"] != 1 || lanes["-1"] != 4 || lanes["-2"] < 1 {
		t.Fatalf("connected lanes %v (text 1, voice 4 on −1, music on −2)", lanes)
	}
	for _, a := range doc.Resources.Assets {
		if !strings.HasPrefix(a.Rep.Src, "file:///") || !strings.Contains(a.Rep.Src, "/media/") {
			t.Fatalf("asset %s src %q", a.Name, a.Rep.Src)
		}
	}

	// EDL: the scenes and the three transitions on V; the title in Latin letters.
	edl := string(readFile(t, filepath.Join(res.Dir, "timeline.edl")))
	if !strings.HasPrefix(edl, "TITLE: Eksport - test\n") || strings.Count(edl, " D    ") != 3 || !strings.Contains(edl, "* FROM CLIP NAME: 002_hold.jpg") {
		t.Fatalf("edl:\n%s", edl)
	}

	// Subtitles: exactly the montage's cues.
	want := media.SRT(media.SubtitleCues(opts))
	if got := string(readFile(t, filepath.Join(res.Dir, "subtitles.srt"))); got != want || !strings.Contains(got, "Перша сцена.") {
		t.Fatalf("srt:\n%s\nwant:\n%s", got, want)
	}

	// Stems: as long as the film, 48 kHz stereo.
	for _, f := range []string{"voice.wav", "music.wav"} {
		info, err := testFF.ProbeStreams(context.Background(), filepath.Join(res.Dir, "stems", f))
		must(t, err)
		if math.Abs(info.Duration-total) > 0.03 || info.SampleRate != 48000 || info.Channels != 2 {
			t.Fatalf("%s: %+v, film %.3f s", f, info, total)
		}
	}
	// Hard links where the volume allows: same content either way.
	if a, b := readFile(t, filepath.Join(dir, "videos", "s1.mp4")), readFile(t, filepath.Join(res.Dir, "media", "001_scene.mp4")); string(a) != string(b) {
		t.Fatal("media copy differs")
	}

	readme := string(readFile(t, filepath.Join(res.Dir, "README.txt")))
	for _, s := range []string{"DaVinci Resolve", "File → Import → Timeline", "timeline.xml", "timeline.fcpxml", "Шторка вліво",
		"Увага: не все було готово", "24 к/с, кадр 320×180"} {
		if !strings.Contains(readme, s) {
			t.Errorf("README lacks %q", s)
		}
	}
	csv := string(readFile(t, filepath.Join(res.Dir, "edit-list.csv")))
	if !strings.HasPrefix(csv, "\uFEFFДоріжка;") || strings.Count(csv, "\r\n") < 10 {
		t.Fatalf("csv:\n%s", csv)
	}

	// The zip holds the folder.
	zr, err := zip.OpenReader(res.Zip)
	must(t, err)
	defer zr.Close()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	base := filepath.Base(res.Dir)
	if !names[base+"/timeline.xml"] || !names[base+"/media/001_scene.mp4"] || len(zr.File) != res.Files {
		t.Fatalf("zip: %d files (result %d): %v", len(zr.File), res.Files, names)
	}
}

func TestExportEveryTarget(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := exportProject(t, e)
	for _, tgt := range ExportTargets() {
		if tgt.ID == ExportResolve || tgt.ID == ExportCapCut {
			continue // own tests
		}
		opt := DefaultExportOptions(tgt.ID)
		opt.Partial = true
		keepDir(&opt)
		res, err := e.ExportTimelineContext(context.Background(), p.ID, opt)
		if err != nil {
			t.Fatalf("%s: %v", tgt.ID, err)
		}
		keepExport(t, res)
		switch tgt.ID {
		case ExportPremiere:
			c := xmlOK(t, "timeline.xml", readFile(t, filepath.Join(res.Dir, "timeline.xml")))
			// Two dissolves, a dip and the clips' sound crossfading under the first.
			if c["sequence"] != 1 || c["transitionitem"] != 4 || c["clipitem"] < 10 || c["file"] < 10 {
				t.Fatalf("xmeml %v", c)
			}
		case ExportFinalCut:
			a := readFile(t, filepath.Join(res.Dir, "timeline.fcpxml"))
			b := readFile(t, filepath.Join(res.Dir, "timeline.fcpxmld", "Info.fcpxml"))
			if string(a) != string(b) {
				t.Fatal("bundle and file differ")
			}
			xmlOK(t, "Info.fcpxml", b)
		case ExportKdenlive, ExportOTIO:
			var doc map[string]any
			must(t, json.Unmarshal(readFile(t, filepath.Join(res.Dir, "timeline.otio")), &doc))
			if doc["OTIO_SCHEMA"] != "Timeline.1" {
				t.Fatalf("otio %v", doc["OTIO_SCHEMA"])
			}
			if tgt.ID == ExportKdenlive {
				c := xmlOK(t, "timeline.mlt", readFile(t, filepath.Join(res.Dir, "timeline.mlt")))
				if c["mlt"] != 1 || c["playlist"] < 5 || c["tractor"] < 2 {
					t.Fatalf("mlt %v", c)
				}
			}
		case ExportFiles:
			if res.Timeline != "" || !isFile(filepath.Join(res.Dir, "stems", "voice.wav")) || !isFile(filepath.Join(res.Dir, "edit-list.csv")) {
				t.Fatalf("files package %+v", res)
			}
		}
		if !isFile(filepath.Join(res.Dir, "README.txt")) {
			t.Fatalf("%s: no README", tgt.ID)
		}
	}
}

// CapCut gets every scene cut to exactly its part of the film: laid one
// after another they are as long as the film, and the stems match it.
func TestExportCapCut(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, _ := exportProject(t, e)
	opt := DefaultExportOptions(ExportCapCut)
	opt.Partial = true
	keepDir(&opt)
	res, err := e.ExportTimelineContext(context.Background(), p.ID, opt)
	must(t, err)
	keepExport(t, res)
	if res.Timeline != "" {
		t.Fatalf("CapCut got a timeline file: %s", res.Timeline)
	}
	sum := 0.0
	for i := 1; i <= 5; i++ {
		info, err := testFF.ProbeStreams(context.Background(), filepath.Join(res.Dir, "media", fmt.Sprintf("%03d_scene.mp4", i)))
		must(t, err)
		if info.Width != 320 || info.Height != 180 || info.FPS() != 24 {
			t.Fatalf("scene %d: %+v", i, info)
		}
		n := info.Frames
		if n == 0 {
			n = int(math.Round(info.Duration * 24))
		}
		sum += float64(n) / 24
	}
	voice, err := testFF.ProbeStreams(context.Background(), filepath.Join(res.Dir, "stems", "voice.wav"))
	must(t, err)
	if math.Abs(sum-voice.Duration) > 0.05 {
		t.Fatalf("cut scenes %.3f s, voice stem %.3f s", sum, voice.Duration)
	}
	readme := string(readFile(t, filepath.Join(res.Dir, "README.txt")))
	if !strings.Contains(readme, "CapCut не імпортує монтажні файли") || !strings.Contains(readme, "001_text.png — з ") {
		t.Fatalf("README:\n%s", readme)
	}
}

// A cancelled export leaves nothing behind.
func TestExportCancel(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, dir := exportProject(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	opt := DefaultExportOptions(ExportCapCut)
	opt.Partial = true
	opt.Progress = func(pct int, _ string) {
		if pct >= 20 {
			cancel()
		}
	}
	if _, err := e.ExportTimelineContext(ctx, p.ID, opt); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "export"))
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

// The motion of a still becomes keyframes that start and end where the
// montage's perspective window does.
func TestExportMotionKeys(t *testing.T) {
	x := &exporter{rate: nle.Rate24, W: 1280, H: 720}
	sl := media.Slot{HasClip: false, Effect: "panright"}
	c := nle.Clip{Len: 48}
	x.motion(&c, sl, 0, 48, 48)
	if len(c.Motion) != 2 {
		t.Fatalf("keys %+v", c.Motion)
	}
	k0, k1 := c.Motion[0], c.Motion[1]
	// panright: zoom 1.12, the window travels from x −0.9 to +0.9 of its free room.
	if math.Abs(k0.Scale-1.12) > 1e-9 || math.Abs(k0.X-0.9*640*0.12) > 1e-6 || math.Abs(k1.X+0.9*640*0.12) > 1e-6 || k1.At != 47 {
		t.Fatalf("keys %+v", c.Motion)
	}
	c2 := nle.Clip{Len: 48}
	x.motion(&c2, media.Slot{Effect: "pushin"}, 0, 48, 48)
	if len(c2.Motion) != 9 {
		t.Fatalf("eased move: %d keys", len(c2.Motion))
	}
}
