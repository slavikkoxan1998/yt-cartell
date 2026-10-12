package media

import (
	"math"
	"strings"
	"testing"
)

func TestPhraseCues(t *testing.T) {
	got := phraseCues("The US sent two diplomats to France to buy just one city, but ended up buying everything. Yeah, slavery.", 4)
	want := []string{"The US sent two", "diplomats to France to", "buy just one city,", "but ended up", "buying everything.", "Yeah,", "slavery."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("phrases %q", got)
	}
	for _, c := range got {
		if n := len(strings.Fields(c)); n < 1 || n > 4 {
			t.Fatalf("%q has %d words", c, n)
		}
	}
	// 5 words split 3 + 2, never 4 + 1; quotes after a mark still end a phrase.
	if got := phraseCues("один два три чотири пʼять", 4); strings.Join(got, "|") != "один два три|чотири пʼять" {
		t.Fatalf("even split %q", got)
	}
	if got := phraseCues("«Так.» А далі", 4); strings.Join(got, "|") != "«Так.»|А далі" {
		t.Fatalf("quotes %q", got)
	}
}

// The phrase preset cuts the burnt subtitles AND the .srt into 1–4-word cues
// spread over the line; the other presets keep the usual cues.
func TestPhrasePresetCues(t *testing.T) {
	p, ok := SubtitlePresetByID("phrase")
	if !ok || p.MaxWords != 4 || p.Position != "bottom" {
		t.Fatalf("preset %+v", p)
	}
	segs := []srtSeg{{narration: "Napoleon offered the whole territory for fifteen million dollars.", start: 2, span: 3.2}}
	srt := buildSRTFor(segs, p)
	if n := strings.Count(srt, " --> "); n != 3 {
		t.Fatalf("%d cues:\n%s", n, srt)
	}
	cs := timedCuesWith(segs, false, 4)
	if cs[0].start != 2 || math.Abs(cs[len(cs)-1].end-5.2) > 1e-9 {
		t.Fatalf("cues span %v … %v", cs[0].start, cs[len(cs)-1].end)
	}
	ass := buildASS(segs, p, 1080, 1920)
	if strings.Count(ass, "Dialogue:") != 3 {
		t.Fatalf("ass:\n%s", ass)
	}
	// Vertical: above the Shorts caption zone (bottom edge at 70 % of the height).
	if !strings.Contains(ass, ",2,65,65,576,") {
		t.Fatalf("position:\n%s", ass)
	}
	if c, _ := SubtitlePresetByID("classic"); strings.Count(buildSRTFor(segs, c), " --> ") != 1 {
		t.Fatal("classic keeps one cue")
	}
	o := MontageOptions{Segments: []Segment{{Frame: "a.jpg", Voice: "a.wav", VoiceDur: 3, Narration: segs[0].narration, StillDur: 3}},
		SubtitleStyle: SubtitleStyle{Preset: "phrase"}}
	if n := len(SubtitleCues(o)); n != 3 {
		t.Fatalf("export cues %d", n)
	}
}

// TightLines: consecutive voice-driven stills nearly touch (0.04 s breath +
// 0.08 s tail instead of 0.25 + 0.35).
func TestTightLines(t *testing.T) {
	segs := []Segment{
		{Frame: "a.jpg", Voice: "a.wav", VoiceDur: 2.5, StillDur: 2.5},
		{Frame: "b.jpg", Voice: "b.wav", VoiceDur: 2.5, StillDur: 2.5},
	}
	_, loose := Layout(MontageOptions{Segments: segs})
	slots, tight := Layout(MontageOptions{Segments: segs, TightLines: true})
	if loose < 6.1 || tight > 5.3 || tight < 5 {
		t.Fatalf("loose %.2f s, tight %.2f s", loose, tight)
	}
	gap := slots[1].VoiceStart - (slots[0].VoiceStart + slots[0].VoiceDuration)
	if gap > 0.2 || gap < 0 {
		t.Fatalf("pause between lines %.3f s", gap)
	}
}
