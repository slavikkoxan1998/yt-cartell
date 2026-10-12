package settings

import (
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"
	"gioui.org/io/transfer"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gemini"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
)

// Synthetic Google AI Studio keys (AIza + 35, never real ones).
func gKey(c string) string { return "AIza" + strings.Repeat(c, 35) }

// googleField focuses the add field of «Google AI Studio».
func googleField(t *testing.T, d *driver) image.Rectangle {
	t.Helper()
	ph := "Встав ключ AQ.… або AIza…"
	if len(d.labels(ph)) == 0 {
		ph = "Додати ще один ключ AQ.… або AIza…"
	}
	r := d.mustLabel(ph)
	d.clickRect(r)
	d.settle(2)
	if !d.p.google.add.Focused(layoutCtx(d)) {
		t.Fatal("the add field did not take the focus")
	}
	return r
}

// paste delivers text the way Ctrl+V does (the clipboard answers the editor).
func paste(d *driver, text string) {
	d.r.Queue(key.Event{Name: "V", Modifiers: key.ModShortcut, State: key.Press})
	d.frame(nil)
	d.r.Queue(transfer.DataEvent{Type: "application/text", Open: func() io.ReadCloser { return io.NopCloser(strings.NewReader(text)) }})
	d.settle(2)
}

func enter(d *driver) {
	d.r.Queue(key.Event{Name: key.NameReturn, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Release})
	d.settle(3)
}

// A user's report: Google keys could not be added. Every way in — typed,
// pasted, several at once (new lines, spaces, commas), with spaces and quotes
// around — through «Додати» and through Enter, then the bulk area.
func TestGoogleKeysAdd(t *testing.T) {
	c, h := demo(t)
	_ = c.E.Config().Update(func(cf *config.Config) { cf.Google.Keys = nil })
	d := &driver{t: t, c: c, h: h, p: New(), section: "google"}
	d.settle(4)
	keys := func() []string { return c.E.Config().Get().Google.Keys }

	// Typed, then «Додати».
	field := googleField(t, d)
	d.r.Queue(key.EditEvent{Text: gKey("a")})
	d.settle(2)
	add, ok := near(d.labels("Додати"), field, 20)
	if !ok {
		t.Fatal("no «Додати» next to the field")
	}
	d.clickRect(add)
	d.settle(3)
	if got := keys(); len(got) != 1 || got[0] != gKey("a") {
		t.Fatalf("typed + «Додати»: %v", got)
	}
	if d.p.google.add.Editor.Len() != 0 {
		t.Fatal("the key stays in the field")
	}

	// Pasted (Windows line end), then Enter.
	googleField(t, d)
	paste(d, gKey("b")+"\r\n")
	enter(d)
	if got := keys(); len(got) != 2 || got[1] != gKey("b") {
		t.Fatalf("pasted + Enter: %v", got)
	}

	// Several in one paste: new lines, spaces, commas, quotes and spaces around.
	field = googleField(t, d)
	paste(d, "  \""+gKey("c")+"\"\n"+gKey("d")+" "+gKey("e")+", '"+gKey("f")+"' ")
	add, _ = near(d.labels("Додати"), field, 20)
	d.clickRect(add)
	d.settle(3)
	if got := keys(); strings.Join(got, ",") != strings.Join([]string{gKey("a"), gKey("b"), gKey("c"), gKey("d"), gKey("e"), gKey("f")}, ",") {
		t.Fatalf("several: %v", got)
	}
	if !strings.HasPrefix(d.p.google.pasted.msg, "Додано 4 ключі") {
		t.Fatalf("note %+v", d.p.google.pasted)
	}

	// Something that is not a key stays in the field with the reason.
	googleField(t, d)
	paste(d, "AIzaSyTooShort")
	enter(d)
	if len(keys()) != 6 || d.p.google.add.Editor.Text() != "AIzaSyTooShort" || !strings.Contains(d.p.google.pasted.msg, "починається з AQ.") {
		t.Fatalf("junk: %d keys, field %q, note %+v", len(keys()), d.p.google.add.Editor.Text(), d.p.google.pasted)
	}
	d.p.google.add.SetText("")

	// «Додати кілька»: a list as people keep it.
	d.settle(2)
	more, ok := near(d.labels("Додати кілька"), field, 90)
	if !ok {
		t.Fatal("no «Додати кілька» under the field")
	}
	d.clickRect(more)
	d.settle(3)
	if !d.p.google.bulk {
		t.Fatal("no bulk area")
	}
	list := "Мої ключі Google:\nkey1: " + gKey("g") + "\nGEMINI_API_KEY=" + gKey("h") + "\n" + gKey("a") + "\nsk_" + strings.Repeat("0a1b", 12)
	d.frame(func(gtx C) { d.p.google.add.Focus(gtx) })
	paste(d, list)
	d.clickRect(d.mustLabel("Додати й перевірити"))
	d.settle(3)
	if got := keys(); len(got) != 8 || got[6] != gKey("g") || got[7] != gKey("h") {
		t.Fatalf("bulk: %v", got)
	}
	if n := d.p.google.pasted.msg; n != "Додано 2 ключі · 1 вже був · 1 — ключ ElevenLabs, не Google AI Studio" {
		t.Fatalf("bulk note %q", n)
	}
	if d.p.google.bulk {
		t.Fatal("the bulk area stays open")
	}
	// The check after an add runs (offline here: it says so, never the network).
	if !d.p.google.check.done || !strings.Contains(d.p.google.check.msg, "демо") {
		t.Fatalf("check %+v", d.p.google.check)
	}
}

func layoutCtx(d *driver) C { return C{Source: d.r.Source()} }

// The key list: 52 keys fold into one line; unfolded it searches and filters
// in a list of fixed height (the page does not grow by 52 rows); the rotation
// states (resting until, rejected) show; «Видалити всі з помилкою» drops the
// rejected keys.
func TestGoogleKeyListFoldsAndFilters(t *testing.T) {
	c, h := demo(t)
	var keys []string
	for i := 0; i < 52; i++ {
		keys = append(keys, "AIza"+strings.Repeat(string(rune('a'+i%26)), 33)+string(rune('A'+i/26))+"x")
	}
	_ = c.E.Config().Update(func(cf *config.Config) { cf.Google.Keys = keys })
	d := &driver{t: t, c: c, h: h, p: New(), section: "google"}
	rest := time.Now().Add(2 * time.Hour)
	d.p.google.status = func() []gemini.KeyStatus {
		st := make([]gemini.KeyStatus, len(keys))
		for i, k := range keys {
			st[i] = gemini.KeyStatus{Index: i, Mask: config.Mask(k), State: gemini.KeyOK, Requests: 3}
		}
		st[5].State, st[5].Until = gemini.KeyResting, rest
		st[9].State, st[9].LastError = gemini.KeyError, "ключ недійсний — перевір, чи скопійовано його повністю"
		st[30].State, st[30].LastError = gemini.KeyError, "ключ прострочено — створи новий в AI Studio"
		return st
	}
	d.settle(4)
	sum := "52 ключі · 49 працюють · 2 з помилкою · 1 відпочиває"
	if len(d.labels(sum)) == 0 {
		t.Fatalf("no summary %q", sum)
	}
	if n := len(d.labels(config.Mask(keys[0]))); n != 0 {
		t.Fatal("folded, yet rows are drawn")
	}
	folded := d.p.contentH
	d.clickRect(d.mustLabel("Показати всі"))
	d.settle(3)
	if !d.p.google.list.open {
		t.Fatal("did not unfold")
	}
	if grow := d.p.contentH - folded; grow <= 0 || grow > 560 {
		t.Fatalf("unfolded list grows the page by %d px", grow)
	}
	// Filter: «Помилка» → the two rejected keys.
	d.clickRect(d.mustLabel("Помилка · 2"))
	d.settle(2)
	if len(d.labels(config.Mask(keys[9]))) == 0 || len(d.labels(config.Mask(keys[30]))) == 0 || len(d.labels(config.Mask(keys[0]))) != 0 {
		t.Fatal("the error filter shows the wrong keys")
	}
	if len(d.labels("ключ прострочено — створи новий в AI Studio")) == 0 {
		t.Fatal("no reason under a rejected key")
	}
	// «Вичерпані» → the resting key, with its time.
	d.clickRect(d.mustLabel("Вичерпані · 1"))
	d.settle(2)
	if len(d.labels("відпочиває до "+untilText(rest))) == 0 {
		t.Fatal("no «відпочиває до …»")
	}
	// Search by mask.
	d.clickRect(d.mustLabel("Усі · 52"))
	d.settle(2)
	d.frame(func(gtx C) { d.p.google.list.search.Focus(gtx) })
	d.r.Queue(key.EditEvent{Text: string([]rune(config.Mask(keys[30]))[6:])})
	d.settle(2)
	if q := d.p.google.list.search.Query(); q == "" || len(d.labels(config.Mask(keys[30]))) == 0 {
		t.Fatalf("search %q found nothing", q)
	}
	// «Видалити всі з помилкою (2)» → confirm → both gone.
	d.p.google.dropBad.Click()
	d.settle(2)
	if n := len(h.Confirms); n == 0 || !strings.Contains(h.Confirms[n-1].Title, "2 ключі з помилкою") {
		t.Fatalf("confirm %+v", h.Confirms)
	}
	h.Confirms[len(h.Confirms)-1].OnOK()
	d.settle(3)
	if got := c.E.Config().Get().Google.Keys; len(got) != 50 {
		t.Fatalf("after dropping: %d keys", len(got))
	}
	// «Згорнути».
	d.clickRect(d.mustLabel("Згорнути"))
	d.settle(2)
	if d.p.google.list.open {
		t.Fatal("did not fold")
	}
}

// The same list folds TTS keys: 52 Cartesia keys are one line until unfolded.
func TestTTSKeyListFolds(t *testing.T) {
	c, h := demo(t)
	var keys []string
	for i := 0; i < 52; i++ {
		keys = append(keys, "sk_car_"+strings.Repeat(string(rune('a'+i%26)), 20)+itoa(i))
	}
	_ = c.E.Config().Update(func(cf *config.Config) {
		cf.TTS.Switch("cartesia")
		cf.TTS.SetKeys(keys)
	})
	c.SetVoicesForTest(tts.VoiceList{Provider: "cartesia", CharsLeft: -1})
	d := &driver{t: t, c: c, h: h, p: New(), section: "tts"}
	d.settle(4)
	if len(d.labels("52 ключі · 0 працюють · 0 з помилкою · 52 ще не перевірені")) == 0 {
		t.Fatal("no folded summary for 52 Cartesia keys")
	}
	if len(d.labels(config.Mask(keys[3]))) != 0 {
		t.Fatal("rows drawn while folded")
	}
	// Three keys or fewer: listed as they are.
	_ = c.E.Config().Update(func(cf *config.Config) { cf.TTS.SetKeys(keys[:3]) })
	d.settle(3)
	if len(d.labels(config.Mask(keys[2]))) == 0 || d.p.tts.list.open {
		t.Fatal("a short list is not listed")
	}
}

// A change whose save panics (a settings subscriber) no longer stops every
// later save: the page shows what the store has, and the next change is saved.
func TestSaveSurvivesPanic(t *testing.T) {
	c, h := demo(t)
	c.E.Config().Subscribe(func(cf config.Config) {
		if cf.Concurrency == 5 {
			panic("subscriber")
		}
	})
	p := New()
	p.cfg.set(c, func(cf *config.Config) { cf.Concurrency = 5 }, nil)
	h.Flush()
	p.cfg.set(c, func(cf *config.Config) { cf.Concurrency = 6 }, nil)
	h.Flush()
	if got := c.E.Config().Get().Concurrency; got != 6 || p.cfg.pending != 0 {
		t.Fatalf("store %d, pending %d", got, p.cfg.pending)
	}
}

// ---- screenshots (GUISHOTS=<dir>) ------------------------------------------------------

// demoGoogleStatus: a rotation with a resting and a rejected key.
func demoGoogleStatus(keys []string) func() []gemini.KeyStatus {
	return func() []gemini.KeyStatus {
		st := make([]gemini.KeyStatus, len(keys))
		for i, k := range keys {
			st[i] = gemini.KeyStatus{Index: i, Mask: config.Mask(k), State: gemini.KeyOK, Requests: 4 + i%5}
		}
		if len(st) > 3 {
			st[1].State, st[1].Until, st[1].LastError = gemini.KeyResting, time.Now().Add(47*time.Minute), "денну квоту проєкту Google вичерпано — вона оновиться опівночі за тихоокеанським часом"
			st[1].Requests = 0
			st[3].State, st[3].LastError = gemini.KeyError, "ключ недійсний — перевір, чи скопійовано його повністю"
			st[3].Requests = 0
		}
		return st
	}
}

func demoKeys(n int, f func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

var (
	shotGoogleKeys = demoKeys(12, func(i int) string { return "AIza" + strings.Repeat(string(rune('a'+i)), 33) + "Qx" })
	shotCarKeys    = demoKeys(52, func(i int) string { return "sk_car_" + strings.Repeat(string(rune('a'+i%26)), 18) + itoa(10+i) })
)

func TestKeyShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the screenshots")
	}
	type state struct {
		name, section string
		config        func(cf *config.Config)
		setup         func(c *core.Ctx, p *Page)
	}
	tts52 := func(cf *config.Config) { cf.TTS.Switch("cartesia"); cf.TTS.SetKeys(shotCarKeys) }
	google := func(cf *config.Config) { cf.Google.Keys = shotGoogleKeys; cf.Image.Provider = "google" }
	pin := func(c *core.Ctx, p *Page) {
		p.google.status = demoGoogleStatus(shotGoogleKeys)
		c.SetVoicesForTest(tts.VoiceList{Provider: "cartesia", CharsLeft: -1})
	}
	states := []state{
		{"tts-folded", "tts", tts52, pin},
		{"tts-open", "tts", tts52, func(c *core.Ctx, p *Page) { pin(c, p); p.tts.list.open = true }},
		{"google-folded", "google", google, pin},
		{"google-open", "google", google, func(c *core.Ctx, p *Page) { pin(c, p); p.google.list.open = true; p.google.list.filter = kErr }},
		{"google-bulk", "google", func(cf *config.Config) { cf.Google.Keys = shotGoogleKeys[:2] }, func(c *core.Ctx, p *Page) {
			pin(c, p)
			p.google.bulk = true
			p.google.add.Editor.SingleLine = false
			p.google.add.SetText("Мої ключі Google:\nkey1: " + shotGoogleKeys[4] + "\nGEMINI_API_KEY=\"" + shotGoogleKeys[5] + "\"\n" + shotGoogleKeys[0] + ", AIzaSyShort")
		}},
		{"google-added", "google", func(cf *config.Config) { cf.Google.Keys = shotGoogleKeys[:4] }, func(c *core.Ctx, p *Page) {
			pin(c, p)
			p.google.status = demoGoogleStatus(shotGoogleKeys[:4])
			p.google.pasted = pasteNote{ok: true, title: "Ключі додано — перевіряємо кожен", msg: "Додано 2 ключі · 1 вже був · 1 не схожий на ключ. Ключ Google AI Studio починається з AIza і має 39 символів."}
			p.google.check = check{done: true, ok: true, msg: "Google: працюють 2 з 4 ключів · 1 відпочиває до 13:47 · 1 з помилкою: ключ недійсний — перевір, чи скопійовано його повністю"}
		}},
		{"media", "frames", func(cf *config.Config) {
			cf.Google.Keys = shotGoogleKeys[:2]
			cf.Image.Provider, cf.Video.Provider, cf.Video.GlabsModel = "google", "glabs", "omni_flash"
		}, pin},
		{"media-nokeys", "text", func(cf *config.Config) {
			cf.Google.Keys = nil
			cf.Text = config.Text{Provider: "google", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "cg-demo-not-real-0000", Model: "gemini-3.8-flash-high"}
			cf.Image.Provider, cf.Video.Provider = "google", "google"
		}, pin},
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, st := range states {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range []shotSize{{1440, 1500}, {390, 2600}} {
				st, dark, sz := st, dark, sz
				theme := map[bool]string{false: "light", true: "dark"}[dark]
				name := "keys-" + st.name + "-" + theme + "-" + itoa(sz.w) + "x" + itoa(sz.h) + ".png"
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if err := c.E.Config().Update(st.config); err != nil {
						t.Fatal(err)
					}
					c.Th.SetPaletteNow(dark)
					p := New()
					frame := 0
					draw := shell(c, func(gtx C) D {
						frame++
						if frame == 2 && st.setup != nil {
							st.setup(c, p)
						}
						return p.Layout(gtx, c, st.section)
					})
					if err := core.Shot(c, h, sz.w, sz.h, 1, filepath.Join(out, name), draw, gui.Frames(6), gui.Settle(120)); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

// Google AI Studio's new "AQ." auth keys are accepted by the card.
func TestGoogleKeysAddNewFormat(t *testing.T) {
	c, h := demo(t)
	_ = c.E.Config().Update(func(cf *config.Config) { cf.Google.Keys = nil })
	d := &driver{t: t, c: c, h: h, p: New(), section: "google"}
	d.settle(4)
	googleField(t, d)
	aq := "AQ.Ab8RN6Lq" + strings.Repeat("x9Y_", 12)
	paste(d, aq)
	enter(d)
	if got := c.E.Config().Get().Google.Keys; len(got) != 1 || got[0] != aq {
		t.Fatalf("AQ key not added: %d keys, note %+v", len(got), d.p.google.pasted)
	}
}
