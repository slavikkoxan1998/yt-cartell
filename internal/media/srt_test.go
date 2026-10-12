package media

import (
	"reflect"
	"strings"
	"testing"
)

func TestSrtTime(t *testing.T) {
	for in, want := range map[float64]string{
		0:        "00:00:00,000",
		0.25:     "00:00:00,250",
		61.5:     "00:01:01,500",
		3725.004: "01:02:05,004",
		1.9996:   "00:00:02,000", // rounding carries into the seconds
		-1:       "00:00:00,000",
	} {
		if got := srtTime(in); got != want {
			t.Errorf("srtTime(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitAfterPunct(t *testing.T) {
	got := splitAfterPunct("Раз, два.  Три! Чотири… п'ять;шість: сім")
	want := []string{"Раз,", "два.", "Три!", "Чотири…", "п'ять;шість:", "сім"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := splitAfterPunct("без розділових знаків"); len(got) != 1 {
		t.Fatalf("got %q", got)
	}
}

func TestCues(t *testing.T) {
	// Short phrases merge up to 84 chars.
	short := "Він прийшов. Вона чекала. Ніхто не говорив."
	if got := cues(short, 84); len(got) != 1 || got[0] != short {
		t.Fatalf("merge: %q", got)
	}
	// Sentences are split at punctuation once the cue would exceed 84.
	a := strings.Repeat("слово ", 10) + "кінець." // 67 runes
	b := strings.Repeat("інше ", 6) + "фініш."    // 36 runes
	got := cues(a+" "+b, 84)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("split: %q", got)
	}
	// A long run without punctuation is word-wrapped at ≤84.
	long := strings.TrimSpace(strings.Repeat("дуже ", 40))
	got = cues(long, 84)
	if len(got) < 2 {
		t.Fatalf("long: %q", got)
	}
	for _, c := range got {
		if runeLen(c) > 84 {
			t.Fatalf("cue too long (%d): %q", runeLen(c), c)
		}
	}
	if strings.Join(got, " ") != long {
		t.Fatal("words lost")
	}
}

func TestWrap2(t *testing.T) {
	if got := wrap2("коротко", 42); got != "коротко" {
		t.Fatal(got)
	}
	s := "Привіт, світе. Це перша сцена, і тут звучить голос оповідача."
	got := wrap2(s, 42)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || strings.Join(lines, " ") != s {
		t.Fatalf("%q", got)
	}
	// Break at the last space at or before ceil(n/2)+6.
	r := []rune(s)
	from := (len(r)+1)/2 + 6
	if runeLen(lines[0]) > from {
		t.Fatalf("first line too long: %q", lines[0])
	}
	if got := wrap2(strings.Repeat("ж", 60), 42); strings.Contains(got, "\n") {
		t.Fatal("no space → unchanged")
	}
}

func TestBuildSRT(t *testing.T) {
	out := buildSRT([]srtSeg{
		{narration: "Перша. Друга.", start: 0.25, span: 2.4},
		{narration: "   ", start: 3.25, span: 7.4},
		{narration: "Третя", start: 11.25, span: 7.4},
	}, false)
	want := "1\n00:00:00,250 --> 00:00:02,650\nПерша. Друга.\n" +
		"\n2\n00:00:11,250 --> 00:00:18,650\nТретя\n"
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
	if buildSRT([]srtSeg{{narration: ""}}, false) != "" {
		t.Fatal("empty expected")
	}
	// Uppercase applies to the file too (Cyrillic included).
	if got := buildSRT([]srtSeg{{narration: "Привіт, їжаку", start: 1, span: 2}}, true); !strings.Contains(got, "\nПРИВІТ, ЇЖАКУ\n") {
		t.Fatalf("upper: %q", got)
	}
}
