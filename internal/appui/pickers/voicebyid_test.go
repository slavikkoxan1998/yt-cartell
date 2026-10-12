package pickers

import (
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
)

const (
	byIDKey   = "sk_car_" + "Qx7Qx7Qx7Qx7Qx7Qx7Qx7Qx7" // synthetic
	byIDVoice = "694f9389-aac1-45b6-b726-9d9369183238"
	byIDOther = "b7d50908-b17c-442d-ad8d-810c63997ed9"
)

// cartesiaByID answers like Cartesia's GET /voices/{id}; every other request
// fails without a network call.
type cartesiaByID struct{ hits int }

func (m *cartesiaByID) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(code int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	if r.URL.Host != "api.cartesia.ai" || r.Header.Get("Authorization") != "Bearer "+byIDKey || r.Header.Get("Cartesia-Version") == "" {
		return nil, errors.New("test: no network (" + r.URL.Host + ")")
	}
	m.hits++
	switch r.URL.Path {
	case "/voices/" + byIDVoice:
		return reply(200, `{"id":"`+byIDVoice+`","is_owner":false,"status":"active","access":"public","name":"Олена — оповідачка",
			"description":"Тепла українська оповідь","gender":"feminine","language":"uk","accents":[{"accent":"ukrainian","locale":"uk-UA","is_native":true}]}`)
	}
	return reply(404, `{"error_code":"voice_not_found","message":"Voice not found"}`)
}

type byIDDriver struct {
	t     *testing.T
	c     *core.Ctx
	h     *core.HeadlessHost
	v     *Voice
	r     input.Router
	ops   op.Ops
	n     int
	val   string
	w, h2 int
}

func (d *byIDDriver) draw(gtx C) D {
	if v, ok := d.v.Changed(); ok {
		d.val = v
	}
	return layout.UniformInset(gui.Sp32).Layout(gtx, func(gtx C) D {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.Y = 0
		d.v.Flat = true
		return d.v.Layout(gtx, d.c, "Голос оповідача за замовчуванням", d.val)
	})
}

func (d *byIDDriver) frame(before func(gtx C)) {
	d.ops.Reset()
	gtx := layout.Context{Ops: &d.ops, Now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).Add(time.Duration(d.n) * 16 * time.Millisecond),
		Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(d.w, d.h2)), Source: d.r.Source()}
	d.n++
	core.Frame(d.c, d.h, func(gtx C) D {
		if before != nil {
			before(gtx)
		}
		return d.draw(gtx)
	})(gtx)
	d.r.Frame(&d.ops)
	d.h.Flush()
}

func (d *byIDDriver) frames(n int) {
	for i := 0; i < n; i++ {
		d.frame(nil)
	}
}

func (d *byIDDriver) label(text string) (image.Rectangle, bool) {
	for _, n := range d.r.AppendSemantics(nil) {
		if n.Desc.Label == text {
			return n.Desc.Bounds, true
		}
	}
	return image.Rectangle{}, false
}

func (d *byIDDriver) clickLabel(text string) {
	d.t.Helper()
	r, ok := d.label(text)
	if !ok {
		d.t.Fatalf("no %q", text)
	}
	pos := f32.Pt(float32(r.Min.X+6), float32(r.Min.Y+6))
	d.r.Queue(pointer.Event{Kind: pointer.Move, Position: pos, Source: pointer.Mouse},
		pointer.Event{Kind: pointer.Press, Position: pos, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary},
		pointer.Event{Kind: pointer.Release, Position: pos, Source: pointer.Mouse})
	d.frames(3)
}

func (d *byIDDriver) typeID(id string) {
	ed := &d.v.byID.field.Editor
	d.frame(func(gtx C) { gtx.Execute(key.FocusCmd{Tag: ed}) })
	d.r.Queue(key.EditEvent{Range: key.Range{Start: 0, End: ed.Len()}, Text: id})
	d.frames(2)
}

func newByID(t *testing.T) (*byIDDriver, *cartesiaByID) {
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Shutdown(0) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	_ = e.Config().Update(func(cf *config.Config) {
		cf.TTS.Switch("cartesia")
		cf.TTS.SetKeys([]string{byIDKey})
	})
	c.SetVoicesForTest(tts.VoiceList{Provider: "cartesia", CharsLeft: -1, Voices: []tts.Voice{
		{ID: byIDOther, Name: "Тарас", Gender: "male", Language: "uk-UA", Usable: true, Category: "premade"},
	}})
	mock := &cartesiaByID{}
	old := http.DefaultTransport
	http.DefaultTransport = mock
	t.Cleanup(func() { http.DefaultTransport = old })
	return &byIDDriver{t: t, c: c, h: h, v: &Voice{Default: "Не вибрано"}, w: 1000, h2: 800}, mock
}

// «Вставити ID голосу» for Cartesia: the popover's button opens the dialog; a
// wrong shape is refused without a request; an unknown id says so; a found
// voice shows its name, language and gender, and «Використати голос» picks it
// and remembers it in «Мої голоси за ID» (listed in the picker from then on).
func TestVoiceByIDCartesia(t *testing.T) {
	d, mock := newByID(t)
	d.c.Offline = false // the lookup goes to the mock transport
	d.frames(2)
	d.v.Open()
	d.frames(4)
	d.clickLabel("Вставити ID голосу")
	if !d.v.byID.dlg.IsOpen() || d.v.pop.open {
		t.Fatal("the dialog did not open (or the list stays open)")
	}
	d.typeID("ElevenLabs-like-id-x")
	d.clickLabel("Знайти голос")
	if d.v.byID.err == "" || mock.hits != 0 {
		t.Fatalf("a wrong shape: err %q, requests %d", d.v.byID.err, mock.hits)
	}
	d.typeID(byIDOther[:35] + "0") // a UUID Cartesia does not have
	d.clickLabel("Знайти голос")
	d.frames(2)
	if !strings.HasPrefix(d.v.byID.err, "Голос не знайдено.") || mock.hits != 1 {
		t.Fatalf("unknown id: err %q, requests %d", d.v.byID.err, mock.hits)
	}
	d.typeID("https://play.cartesia.ai/voices/" + byIDVoice)
	d.r.Queue(key.Event{Name: key.NameReturn, State: key.Press}, key.Event{Name: key.NameReturn, State: key.Release})
	d.frames(3)
	if f := d.v.byID.found; f == nil || f.Name != "Олена — оповідачка" || f.Language != "uk-UA" || f.Gender != "female" {
		t.Fatalf("found %+v err %q", d.v.byID.found, d.v.byID.err)
	}
	if _, ok := d.label("Знайдено: Олена — оповідачка"); !ok {
		t.Fatal("the dialog does not show the voice")
	}
	d.clickLabel("Використати голос")
	d.frames(20)
	if d.val != byIDVoice {
		t.Fatalf("picked %q", d.val)
	}
	saved := d.c.E.Config().Get().TTS.VoicesByID("cartesia")
	if len(saved) != 1 || saved[0].ID != byIDVoice || saved[0].Name != "Олена — оповідачка" || saved[0].Language != "uk-UA" {
		t.Fatalf("saved %+v", saved)
	}
	if _, ok := d.label("Олена — оповідачка"); !ok || VoiceTitle(d.c, byIDVoice) != "Олена — оповідачка" {
		t.Fatal("the field does not name the voice")
	}
	// It is in the list now, as «за ID».
	d.v.Open()
	d.frames(4)
	found := false
	for _, o := range d.v.opts {
		found = found || (o.Value == byIDVoice && strings.Contains(o.Caption, "за ID"))
	}
	if !found {
		t.Fatalf("not listed: %+v", d.v.opts)
	}
}

// ElevenLabs offers it too; other providers do not.
func TestVoiceByIDProviders(t *testing.T) {
	if !tts.CanLookupVoice("elevenlabs") || !tts.CanLookupVoice("cartesia") || tts.CanLookupVoice("openai") {
		t.Fatal("providers")
	}
}

// Screenshots of the flow (GUISHOTS=<dir>): the popover button, the dialog with
// a found voice, an error, and the field after the pick.
func TestVoiceByIDShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	states := []struct {
		name  string
		setup func(d *byIDDriver)
	}{
		{"list", func(d *byIDDriver) { d.v.Open() }},
		{"found", func(d *byIDDriver) {
			d.v.byID.open("cartesia")
			d.v.byID.field.SetText(byIDVoice)
			v := tts.Voice{ID: byIDVoice, Name: "Олена — оповідачка", Gender: "female", Language: "uk-UA", Category: "premade", Usable: true}
			d.v.byID.found, d.v.byID.foundFor = &v, byIDVoice
		}},
		{"error", func(d *byIDDriver) {
			d.v.byID.open("cartesia")
			d.v.byID.field.SetText(byIDOther)
			d.v.byID.err = "Голос не знайдено. У Cartesia немає голосу з ID " + byIDOther + " — перевір, чи скопійовано весь ID"
		}},
		{"picked", func(d *byIDDriver) {
			_ = d.c.E.Config().Update(func(cf *config.Config) {
				cf.TTS.SaveVoiceByID("cartesia", config.VoiceRef{ID: byIDVoice, Name: "Олена — оповідачка", Language: "uk-UA", Gender: "female"})
			})
			d.val = byIDVoice
			d.v.Open()
		}},
	}
	for _, dark := range []bool{false, true} {
		for _, sz := range []struct{ w, h int }{{1440, 900}, {390, 844}} {
			for _, st := range states {
				d, _ := newByID(t)
				d.c.Th.SetPaletteNow(dark)
				d.w, d.h2 = sz.w, sz.h
				st.setup(d)
				mode := map[bool]string{false: "light", true: "dark"}[dark]
				path := filepath.Join(out, "pickers", "voice-byid-"+st.name+"-"+mode+"-"+itoa(sz.w)+"x"+itoa(sz.h)+".png")
				if err := core.Shot(d.c, d.h, sz.w, sz.h, 1, path, d.draw, gui.Frames(6), gui.Settle(120)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
