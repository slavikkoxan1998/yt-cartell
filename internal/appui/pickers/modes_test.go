package pickers

import (
	"path/filepath"
	"testing"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

func TestModeLists(t *testing.T) {
	want := []string{studio.AudioVoiceover, studio.AudioInVideo, studio.AudioSilent}
	for i, m := range SoundModes {
		if m.Value != want[i] || m.Label == "" || m.Caption == "" || m.Icon == nil {
			t.Fatalf("sound %d: %+v", i, m)
		}
	}
	wantC := []string{studio.ContentCharacters, studio.ContentNoPeople, studio.ContentMotion, studio.ContentMixed}
	for i, m := range ContentModes {
		if m.Value != wantC[i] || m.Label == "" || m.Caption == "" || m.Icon == nil {
			t.Fatalf("content %d: %+v", i, m)
		}
	}
	if ModeByValue(ContentModes, "").Value != studio.ContentCharacters || ModeByValue(SoundModes, "nope").Value != studio.AudioVoiceover {
		t.Fatal("ModeByValue fallback")
	}
	if !(ContentValue{Content: studio.ContentMotion}).TextOn() || (ContentValue{Content: studio.ContentMotion, Overlays: "off"}).TextOn() ||
		(ContentValue{Content: studio.ContentNoPeople}).TextOn() || !(ContentValue{Content: studio.ContentNoPeople, Overlays: "on"}).TextOn() {
		t.Fatal("TextOn")
	}
}

// A row of an open panel picks its mode; a new content resets the text
// switch to its default, the same content keeps it.
func TestModePicks(t *testing.T) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var (
		snd  Sound
		cnt  Content
		sv   = studio.AudioVoiceover
		cv   = ContentValue{Content: studio.ContentMotion, Overlays: "off"}
		todo func()
	)
	draw := func(gtx C) D {
		if todo != nil {
			todo()
			todo = nil
		}
		d := gui.VStack(gtx, gui.Sp8,
			func(gtx C) D { return snd.Layout(gtx, c, "Звук", sv) },
			func(gtx C) D { return cnt.Layout(gtx, c, "Що в кадрі", cv) },
		)
		if v, ok := snd.Changed(); ok {
			sv = v
		}
		if v, ok := cnt.Changed(); ok {
			cv = v
		}
		return d
	}
	run := func(fn func()) {
		t.Helper()
		todo = fn
		if _, err := gui.Snapshot(900, 1200, 1, core.Frame(c, h, draw), gui.Frames(3)); err != nil {
			t.Fatal(err)
		}
	}
	snd.Open()
	run(func() { snd.rows[1].Clickable.Click() })
	if sv != studio.AudioInVideo || snd.pop.open {
		t.Fatalf("sound pick %q (open %v)", sv, snd.pop.open)
	}
	cnt.Open()
	run(func() { cnt.rows[1].Clickable.Click() })
	if cv != (ContentValue{Content: studio.ContentNoPeople}) {
		t.Fatalf("content pick %+v", cv)
	}
	cv.Overlays = "on"
	run(func() { cnt.rows[1].Clickable.Click() })
	if cv != (ContentValue{Content: studio.ContentNoPeople, Overlays: "on"}) {
		t.Fatalf("same content lost the switch: %+v", cv)
	}
}
