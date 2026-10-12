package media

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Characters speaking in the clip: the clip plays at its own level instead
// of the ambient one, keys the music ducking, and its captions become the
// subtitles (moved with the scene, trimmed parts dropped).
func TestClipSpeech(t *testing.T) {
	k := newKit(t)
	a := k.clip("a.mp4", "color=c=black", 320, 180, 3, 440)
	b := k.clip("b.mp4", "color=c=gray", 320, 180, 3, 440)
	music := k.make("music.mp3", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=44100:duration=8", "-c:a", "libmp3lame", "-b:a", "96k")
	res, err := k.f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{
			{Video: b, TrimIn: 0.5}, // ambient only
			{Video: a, TrimIn: 0.5, ClipVolume: 1, ClipSpeech: true, Captions: []Caption{
				{Text: "Мамо, дивись!", Start: 0.6, End: 1.6},
				{Text: "Це вирізано", Start: 0.0, End: 0.4}, // inside the trim
				{Text: "Бачу, синку.", Start: 1.9, End: 2.8},
			}},
		},
		Aspect: "16:9", Transition: "none", Subtitles: "srt",
		MusicPath: music, MusicVolume: 0.5, AmbientVolume: 0.1, Ducking: true,
		OutDir: filepath.Join(k.dir, "out"),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFilm(t, k.f, res)
	// Scene 1 = 0…2.5 s (ambient 10 %), scene 2 = 2.5…5 s (speech at 100 %).
	quiet := bandRMS(t, k.f, res.Output, 440, 60, 0.4, 2.2)
	loud := bandRMS(t, k.f, res.Output, 440, 60, 2.9, 4.6)
	if loud < quiet*5 {
		t.Errorf("clip speech not at full level: %.0f vs ambient %.0f", loud, quiet)
	}
	free := bandRMS(t, k.f, res.Output, 220, 40, 0.4, 2.2)
	ducked := bandRMS(t, k.f, res.Output, 220, 40, 3.0, 4.6)
	if ducked > free*0.6 {
		t.Errorf("music not ducked under the clip speech: %.0f vs %.0f", ducked, free)
	}
	srt, err := os.ReadFile(res.SRT)
	if err != nil {
		t.Fatal(err)
	}
	s := string(srt)
	if strings.Contains(s, "вирізано") || !strings.Contains(s, "Мамо, дивись!") || !strings.Contains(s, "Бачу, синку.") {
		t.Fatalf("subtitles:\n%s", s)
	}
	// 0.6 s into the clip, 0.5 s trimmed, scene starts at 2.5 s → 2.6 s.
	if !strings.Contains(s, "00:00:02,600 --> ") {
		t.Errorf("first line not at 2.6 s:\n%s", s)
	}
}

// On-screen text frames are composited over the scene from its first frame:
// a white square shows from 1 s on, not before.
func TestOverlayComposited(t *testing.T) {
	k := newKit(t)
	clip := k.clip("c.mp4", "color=c=black", 320, 180, 3, 440)
	write := func(path string, white bool) {
		img := image.NewRGBA(image.Rect(0, 0, 80, 60))
		if white { // not fully opaque: Go writes opaque pictures as RGB, the frames stay RGBA
			for i := range img.Pix {
				img.Pix[i] = 250
			}
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	res, err := k.f.Montage(context.Background(), MontageOptions{
		Segments: []Segment{{Video: clip}},
		Aspect:   "16:9", Transition: "none", Subtitles: "none", AmbientVolume: 0.2,
		OutDir: filepath.Join(k.dir, "out"),
		Overlay: func(ctx context.Context, seg int, span OverlaySpan, size image.Point, dir string) (Overlay, bool, error) {
			calls++
			if seg != 0 || size != image.Pt(320, 180) || !near(span.Duration, 3, 0.05) {
				t.Errorf("overlay asked for seg %d size %v span %+v", seg, size, span)
			}
			blank, white := filepath.Join(dir, "blank.png"), filepath.Join(dir, "white.png")
			write(blank, false)
			write(white, true)
			list := filepath.Join(dir, "list.txt")
			body := fmt.Sprintf("ffconcat version 1.0\nfile '%s'\nduration 1\nfile '%s'\nduration 2\nfile '%s'\n",
				filepath.ToSlash(blank), filepath.ToSlash(white), filepath.ToSlash(white))
			if err := os.WriteFile(list, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			return Overlay{List: list, X: 40, Y: 30}, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("overlay called %d times", calls)
	}
	checkFilm(t, k.f, res)
	box := func(sec float64) float64 {
		fr := frameAt(t, k.f, res.Output, sec, 320, 180, "gray")
		s, n := 0.0, 0.0
		for y := 35; y < 85; y++ {
			for x := 45; x < 115; x++ {
				s, n = s+float64(fr[y*320+x]), n+1
			}
		}
		return s / n
	}
	if v := box(0.5); v > 40 {
		t.Errorf("overlay visible before its time: %.0f", v)
	}
	if v := box(2.0); v < 200 {
		t.Errorf("overlay missing at 2 s: %.0f", v)
	}
	if entries, _ := os.ReadDir(filepath.Join(k.dir, "out")); len(entries) > 0 {
		for _, e := range entries {
			if e.IsDir() {
				t.Errorf("overlay folder %s left behind", e.Name())
			}
		}
	}
}
