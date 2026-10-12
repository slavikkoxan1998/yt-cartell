package home

// Dictation in the story box. Screenshots (skipped unless GUISHOTS=<dir>):
//
//	GUISHOTS=../../../screenshots/new/home go test ./internal/appui/home -run TestMicShots

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/dictate"
	"ytcartell/internal/gui"
)

// TestMicApply: the splice edits land in the story box at the caret, the
// interim words replaced as they change.
func TestMicApply(t *testing.T) {
	var f gui.TextField
	f.SetText("Початок. Кінець.")
	f.Editor.SetCaret(9, 9) // after "Початок. "
	var m mic
	m.apply(&f, m.splice.Begin(f.Text(), 9, 9))
	m.apply(&f, m.splice.Update(f.Text(), caret(&f), nil, "середина"))
	if got := f.Text(); got != "Початок. Середина Кінець." {
		t.Fatalf("interim: %q", got)
	}
	m.apply(&f, m.splice.Update(f.Text(), caret(&f), []string{"Середина історії."}, ""))
	if got := f.Text(); got != "Початок. Середина історії. Кінець." {
		t.Fatalf("final: %q", got)
	}
	if c := caret(&f); c != len([]rune("Початок. Середина історії.")) {
		t.Fatalf("caret %d", c)
	}
	m.apply(&f, m.splice.End(f.Text(), true))
	if m.splice.Active() {
		t.Fatal("still active")
	}
}

// TestMicOfflineAndNotice: demo renders never start the microphone; an
// error stays under the box with the way to Settings → Диктування.
func TestMicOfflineAndNotice(t *testing.T) {
	c, h, _ := testCtx(t)
	p := New()
	p.micStart(c)
	if p.hero.mic.sess != nil || p.hero.mic.resolving {
		t.Fatal("a dictation started offline")
	}
	p.hero.mic.err = &dictate.Error{Kind: dictate.KindNoKey, Msg: "Немає ключа Deepgram"}
	draw(c, h, 1440, 900, 3, func(gtx C) D { return p.Home(gtx, c) })
	if !dictate.NeedsSettings(p.hero.mic.err) {
		t.Fatal("the notice lost its error")
	}
	// The link goes to the dictation settings.
	p.hero.mic.fix.Clickable.Click()
	draw(c, h, 1440, 900, 1, func(gtx C) D { return p.Home(gtx, c) })
	if r := h.Current(); r.Page != core.PageSettings || r.Section != SectionDictate {
		t.Fatalf("route %+v", r)
	}
	if p.hero.mic.err != nil {
		t.Fatal("the notice stays after the link")
	}
	// A recording look lays out at every width.
	p.hero.mic.demo = &dictate.State{Phase: dictate.Listening, Level: 0.5, Elapsed: 3 * time.Second}
	for _, w := range []int{1920, 900, 390} {
		draw(c, h, w, 844, 2, func(gtx C) D { return p.Home(gtx, c) })
	}
}

type micShot struct {
	name  string
	setup func(p *Page)
}

const shotStory = "Жив собі рудий кіт на ім'я Бублик. Щоранку він виходив на дах і дивився, як над містом встає сонце."

var micShots = []micShot{
	{"idle", func(p *Page) { p.hero.story.SetText(shotStory) }},
	{"recording", func(p *Page) {
		text := shotStory + " Одного дня він помітив"
		interim := " маленьку пташку з поламаним крилом"
		p.hero.story.SetText(text + interim)
		n, k := len([]rune(text)), len([]rune(interim))
		p.hero.mic.pending = [2]int{n + 1, n + k} // the words, not the space before them
		p.hero.story.Editor.SetCaret(n+k, n+k)
		p.hero.mic.demo = &dictate.State{Phase: dictate.Listening, Level: 0.62, Speaking: true, Heard: true,
			Elapsed: 47 * time.Second, Device: "Microphone (Realtek(R) Audio)", Label: "ElevenLabs Scribe v2 Realtime"}
	}},
	{"start", func(p *Page) {
		p.hero.mic.demo = &dictate.State{Phase: dictate.Listening, Level: 0.3,
			Device: "Microphone (Realtek(R) Audio)", Label: "whisper.cpp на цьому комп'ютері"}
	}},
	{"error", func(p *Page) {
		p.hero.story.SetText(shotStory)
		p.hero.mic.err = &dictate.Error{Kind: dictate.KindAuth, Msg: "ElevenLabs не прийняв ключ — перевір його в Налаштуваннях → Диктування (потрібен дозвіл Speech to Text)"}
	}},
	{"sheet", func(p *Page) {
		p.hero.mic.sheetText = "Диктування ще не налаштоване: вибери сервіс у Налаштуваннях → Диктування (whisper.cpp працює без ключа, якщо встановлений)."
		p.hero.mic.sheet.Open()
	}},
}

func TestMicShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the dictation screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, st := range micShots {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range [][2]int{{1440, 900}, {390, 844}} {
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("home-mic-%s-%s-%dx%d.png", st.name, theme, sz[0], sz[1])
				st, dark, sz := st, dark, sz
				t.Run(name, func(t *testing.T) {
					c, h, _ := testCtx(t)
					c.Th.SetPaletteNow(dark)
					p := New()
					st.setup(p)
					if err := core.Shot(c, h, sz[0], sz[1], 1, filepath.Join(out, name), func(gtx C) D { return p.Home(gtx, c) },
						gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// TestMicSessionEnds: a session that fails (here: no key) ends in the next
// frame and leaves its error under the box; the story keeps its text.
func TestMicSessionEnds(t *testing.T) {
	c, h, _ := testCtx(t)
	p := New()
	p.hero.story.SetText("Було собі")
	m := &p.hero.mic
	m.sess = dictate.Start(dictate.Options{Plan: dictate.Plan{Provider: dictate.Deepgram, Problem: "Немає ключа Deepgram"}})
	m.splice.Begin(p.hero.story.Text(), 9, 9)
	select {
	case <-m.sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end")
	}
	draw(c, h, 1440, 900, 2, func(gtx C) D { return p.Home(gtx, c) })
	if m.sess != nil || dictate.KindOf(m.err) != dictate.KindNoKey || m.splice.Active() {
		t.Fatalf("after the end: sess %v err %v", m.sess, m.err)
	}
	if p.hero.story.Text() != "Було собі" {
		t.Fatalf("text %q", p.hero.story.Text())
	}
}
