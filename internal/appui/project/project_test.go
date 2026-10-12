package project

import (
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

func TestDecodeText(t *testing.T) {
	want := "Жила-була лисиця\nі місяць"
	cp1251, _ := charmap.Windows1251.NewEncoder().String("Жила-була лисиця\r\nі місяць")
	utf16le := []byte{0xFF, 0xFE}
	for _, r := range "Жила-була лисиця\r\nі місяць" {
		utf16le = append(utf16le, byte(r), byte(r>>8))
	}
	cases := map[string][]byte{
		"utf8":    []byte("Жила-була лисиця\r\nі місяць"),
		"bom":     append([]byte{0xEF, 0xBB, 0xBF}, []byte("Жила-була лисиця\nі місяць")...),
		"cp1251":  []byte(cp1251),
		"utf16le": utf16le,
	}
	for name, in := range cases {
		if got := decodeText(in); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestCountWords(t *testing.T) {
	for in, n := range map[string]int{"": 0, "  ": 0, "один": 1, "раз, два\nтри": 3, " a  b\t c ": 3} {
		if got := countWords(in); got != n {
			t.Errorf("countWords(%q) = %d, want %d", in, got, n)
		}
	}
	if words(1) != "1 слово" || words(3) != "3 слова" || words(12) != "12 слів" {
		t.Fatal("plural")
	}
}

// TestBinderFlushesHiddenFields: an edit pending in a field that stops
// being drawn (the user switched steps) is saved, and only once.
func TestBinderFlushesHiddenFields(t *testing.T) {
	var b binder
	var saved []string
	f := &bound{}
	f.dirty, f.val, f.at, f.drawn = true, "нова назва", time.Now(), 5
	f.commit = func(v string) { saved = append(saved, v) }
	b.add(f)
	b.flushStale(5) // drawn this frame: not yet
	if len(saved) != 0 {
		t.Fatal("flushed a field that is still drawn")
	}
	b.flushStale(6)
	if len(saved) != 1 || saved[0] != "нова назва" || f.dirty {
		t.Fatalf("saved %v", saved)
	}
	b.flushAll()
	if len(saved) != 1 {
		t.Fatal("saved twice")
	}
	if !f.holding || f.hold != "нова назва" {
		t.Fatal("the committed text must be held until the snapshot catches up")
	}
}

func newTestCtx(t *testing.T) (*studio.Engine, *core.Ctx, *core.HeadlessHost) {
	t.Helper()
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Shutdown(0) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	return e, c, h
}

// TestSceneOperations: insert, move and the cast of a new character go
// through the engine's merge rules.
func TestSceneOperations(t *testing.T) {
	e, c, _ := newTestCtx(t)
	p, err := e.Create("Сцени")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Edit(p.ID, func(p *studio.Project) {
		for _, id := range []string{"sc-1", "sc-2", "sc-3"} {
			p.Scenes = append(p.Scenes, studio.Scene{ID: id, Title: id, Speaker: "narrator", CharacterIDs: []string{},
				Frame: studio.EmptyAsset(), Board: studio.EmptyAsset(), Video: studio.EmptyAsset(), Audio: studio.EmptyAsset()})
		}
	}); err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace()
	ps := ws.state(p.ID)
	ps.s2.insertAfter(c, p.ID, "sc-1")
	ps.s2.move(c, p.ID, "sc-3", -1)
	got, _ := e.Get(p.ID)
	if len(got.Scenes) != 4 {
		t.Fatalf("scenes = %d", len(got.Scenes))
	}
	if got.Scenes[0].ID != "sc-1" || got.Scenes[1].Title != "Нова сцена" || got.Scenes[2].ID != "sc-3" || got.Scenes[3].ID != "sc-2" {
		t.Fatalf("order: %s %s %s %s", got.Scenes[0].ID, got.Scenes[1].Title, got.Scenes[2].ID, got.Scenes[3].ID)
	}
	if c.UI(p.ID).Scene != "sc-3" {
		t.Fatal("a moved scene stays selected")
	}
}

// TestWorkspaceRendersEverything renders every demo project at every step,
// a fresh project and a missing one: no panics, the jump request is taken.
func TestWorkspaceRendersEverything(t *testing.T) {
	e, c, h := newTestCtx(t)
	fresh, err := e.Create("Новий")
	if err != nil {
		t.Fatal(err)
	}
	ws := NewWorkspace()
	ids := []string{fresh.ID, "missing"}
	for _, s := range c.List() {
		ids = append(ids, s.ID)
	}
	for _, id := range ids {
		for step := 0; step <= 5; step++ {
			id, step := id, step
			if pv := c.Project(id); pv != nil && len(pv.P.Scenes) > 1 && step == 2 {
				c.UI(id).ScrollTo = pv.P.Scenes[1].ID
			}
			for _, w := range []int{1440, 390} {
				_, err := gui.Snapshot(w, 900, 1, core.Frame(c, h, func(gtx C) D { return ws.Layout(gtx, c, id, step) }), gui.Frames(20))
				if err != nil {
					t.Fatal(err)
				}
			}
			if step == 2 && c.UI(id).ScrollTo != "" {
				t.Fatalf("%s: the jump request was not taken", id)
			}
		}
	}
}

// TestBinderTimerCommitsPausedEdits: the timer commits an edit that paused
// for the debounce even when nothing draws the field any more.
func TestBinderTimerCommitsPausedEdits(t *testing.T) {
	var b binder
	var saved []string
	f := &bound{}
	f.dirty, f.val, f.atWall = true, "текст", time.Now()
	f.commit = func(v string) { saved = append(saved, v) }
	b.add(f)
	b.flushDue(time.Now()) // still typing
	if len(saved) != 0 {
		t.Fatal("committed before the pause")
	}
	b.flushDue(time.Now().Add(debounce + time.Millisecond))
	if len(saved) != 1 || saved[0] != "текст" {
		t.Fatalf("saved %v", saved)
	}
}

// TestMaterialHelpers: the step 2 badge follows the engine's binding, the
// summary counts the kinds, an attached transcript is named by its format.
func TestMaterialHelpers(t *testing.T) {
	p := &studio.Project{Input: studio.Input{Materials: []studio.Material{
		{ID: "m1", Kind: studio.MaterialCharacter, Name: " Тарас "},
		{ID: "m2", Kind: studio.MaterialCharacter, Name: "Бабуся"},
		{ID: "m3", Kind: studio.MaterialLocation, Name: "Рідна хата"},
		{ID: "m4", Kind: studio.MaterialStyle},
	}}}
	switch {
	case !fromPhoto(p, "m1"), !fromPhoto(p, "m3"):
		t.Fatal("a character or place bound to a material comes from a photo")
	case fromPhoto(p, ""):
		t.Fatal("planned by the model alone")
	case fromPhoto(p, "gone"):
		t.Fatal("a removed material binds nothing")
	}
	if got := materialSummary(p.Input.Materials); got != "2 персонажі · 1 локація · 1 зразок стилю" {
		t.Fatalf("summary = %q", got)
	}
	for in, want := range map[string]string{"audio/transcript-ab12.srt": "SRT", "audio/x.vtt": "VTT", "a/b.json": "JSON", "": "файл"} {
		if got := trFormat(in); got != want {
			t.Fatalf("trFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClipLengths: a scene's line has to fit the clip chosen for it, else
// the longest dynamic clip, else the clip setting.
func TestClipLengths(t *testing.T) {
	f := facts{clip: 8}
	if got := clipOf(studio.Scene{}, f); got != 8 {
		t.Fatalf("fixed clip = %v", got)
	}
	f.dynamic = true
	if got := clipOf(studio.Scene{}, f); got != maxDynamicClip {
		t.Fatalf("dynamic clip = %v", got)
	}
	if got := clipOf(studio.Scene{ClipLen: 6}, f); got != 6 {
		t.Fatalf("chosen clip = %v", got)
	}
	if got := sceneSeconds(studio.Scene{ClipLen: 6}, 8); got != 6 {
		t.Fatalf("scene length = %v", got)
	}
}

// TestMaterialsAndTranscriptRender draws step 1 with materials (one still
// being described, one with a missing picture), step 2 with the photo
// badges and the own-voice mode with an attached transcript: no panics.
func TestMaterialsAndTranscriptRender(t *testing.T) {
	e, c, h := newTestCtx(t)
	ws := NewWorkspace()
	describing := materialDescribing // one picture is being described
	materialDescribing = func(c *core.Ctx, id, materialID string) bool { return materialID == "mat-2" }
	defer func() { materialDescribing = describing }()
	for _, s := range c.List() {
		id := s.ID
		pv := c.Project(id)
		if pv == nil || len(pv.P.Characters) == 0 {
			continue
		}
		fake := func() {
			pv := c.Project(id)
			pv.P.Input.Materials = []studio.Material{
				{ID: "mat-1", Kind: studio.MaterialCharacter, Name: pv.P.Characters[0].Name, Path: "materials/missing.jpg", Look: "A boy in a yellow sweater."},
				{ID: "mat-2", Kind: studio.MaterialStyle, Path: "materials/style.jpg"},                     // being described
				{ID: "mat-4", Kind: studio.MaterialObject, Path: "materials/cup.jpg"},                      // no look, no note
				{ID: "mat-3", Kind: studio.MaterialObject, Path: "materials/hat.jpg", Note: "Стара шапка"}, // the note is the look
			}
			pv.P.Characters[0].MaterialID = "mat-1"
		}
		for _, step := range []int{1, 2} {
			for _, w := range []int{1440, 390} {
				fake()
				if _, err := gui.Snapshot(w, 900, 1, core.Frame(c, h, func(gtx C) D { return ws.Layout(gtx, c, id, step) }), gui.Frames(20)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	p, err := e.Create("Своя озвучка")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Edit(p.ID, func(p *studio.Project) { p.Input.Mode = studio.ModeVoice }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the engine's notification
	h.Flush()
	c.Refresh()
	for _, w := range []int{1440, 390} {
		pv := c.Project(p.ID)
		pv.P.Input.VoiceFile, pv.P.Input.TranscriptFile = "audio/voice.mp3", "audio/transcript.srt"
		if _, err := gui.Snapshot(w, 900, 1, core.Frame(c, h, func(gtx C) D { return ws.Layout(gtx, c, p.ID, 1) }), gui.Frames(20)); err != nil {
			t.Fatal(err)
		}
	}
}
