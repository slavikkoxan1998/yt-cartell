package settings

import (
	"image"
	"testing"

	"ytcartell/internal/config"
)

// pickOption presses option in the selector labelled label and lets the save
// finish.
func pickOption(t *testing.T, d *driver, label, option string) {
	t.Helper()
	head := d.mustLabel(label)
	r, ok := near(d.labels(option), head, 70)
	if !ok {
		t.Fatalf("no %q under %q (%v)", option, label, d.labels(option))
	}
	d.clickRect(r)
	d.settle(4)
}

// Every option of «Кадри» and «Відео», in turn: the saved settings AND what
// each selector shows after the save — and the other card's selector never
// moves (they used to share one card; the frame of a change showed the old
// value).
func TestMediaSelectorsSaveAndShow(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New(), section: "frames"}
	d.settle(4)
	names := map[string]string{"glabs": "G-Labs", "google": "Google", "custom": "Своє API"}
	steps := []struct {
		video bool
		value string
	}{
		{false, "google"}, {true, "custom"}, {false, "custom"}, {true, "google"}, {false, "glabs"},
		{true, "glabs"}, {false, "google"}, {true, "custom"}, {true, "glabs"}, {false, "glabs"},
	}
	for i, st := range steps {
		label, section := "Хто малює кадри", "frames"
		if st.video {
			label, section = "Хто знімає відео", "video"
		}
		d.section = section
		d.settle(3)
		before := c.E.Config().Get()
		pickOption(t, d, label, names[st.value])
		got := c.E.Config().Get()
		img, vid := got.Image.Provider, got.Video.Provider
		want, other, wantOther := img, vid, before.Video.Provider
		shown, otherShown := d.p.frames.shown, d.p.video.shown
		if st.video {
			want, other, wantOther = vid, img, before.Image.Provider
			shown, otherShown = d.p.video.shown, d.p.frames.shown
		}
		if want != st.value || other != wantOther {
			t.Fatalf("step %d (%s → %s): saved image %q video %q", i, label, st.value, img, vid)
		}
		if shown != st.value || otherShown != wantOther {
			t.Fatalf("step %d: the selectors show %q / %q, saved %q / %q", i, shown, otherShown, want, other)
		}
		if d.p.now.Image.Provider != img || d.p.now.Video.Provider != vid || d.p.cfg.pending != 0 {
			t.Fatalf("step %d: page %q/%q pending %d", i, d.p.now.Image.Provider, d.p.now.Video.Provider, d.p.cfg.pending)
		}
	}
	// «Текст»: the same for «Хто пише текст».
	d.section = "text"
	d.settle(3)
	for _, v := range []struct{ value, label string }{{"google", "Google Gemini"}, {"openai", "OpenAI-сумісний"}} {
		pickOption(t, d, "Хто пише текст", v.label)
		if got := c.E.Config().Get().Text.Provider; got != v.value || d.p.text.shown != v.value {
			t.Fatalf("text: saved %q, shown %q, want %q", got, d.p.text.shown, v.value)
		}
	}
}

// The selector shows the new value in the very frame of the change (the page's
// settings follow the change at once).
func TestSelectorShowsChangeAtOnce(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New(), section: "frames"}
	d.settle(3)
	r, _ := near(d.labels("Google"), d.mustLabel("Хто малює кадри"), 70)
	d.clickRect(r)
	d.frame(nil) // the click arrives; the save is still on its way
	if d.p.frames.shown != "google" {
		t.Fatalf("frame of the change shows %q", d.p.frames.shown)
	}
	h.Flush()
	if c.E.Config().Get().Image.Provider != "google" {
		t.Fatal("not saved")
	}
}

// A double click on a dropdown row: the second press lands where the list was
// — on a provider selector. The click guard swallows it; a later press works.
func TestDropdownDoubleClickDoesNotSwitch(t *testing.T) {
	c, h := demo(t)
	_ = c.E.Config().Update(func(cf *config.Config) { cf.Video.Provider, cf.Video.GlabsModel = "glabs", "veo_31_fast" })
	d := &driver{t: t, c: c, h: h, p: New(), section: "video"}
	d.settle(3)
	google, _ := near(d.labels("Google"), d.mustLabel("Хто знімає відео"), 70)
	// Open «Модель відео» and pick Omni Flash.
	d.clickRect(d.mustLabel("Veo 3.1 Fast"))
	d.settle(4)
	if !d.p.video.model.IsOpen() {
		t.Fatal("the model list did not open")
	}
	omni := d.mustLabel("Omni Flash")
	d.clickRect(omni)
	d.frame(nil)
	if d.p.video.model.IsOpen() {
		t.Fatal("the list stays open after a pick")
	}
	// The second click of the double click, 50 ms later, on «Google».
	d.frame(nil)
	d.frame(nil)
	d.clickRect(google)
	d.settle(3)
	got := c.E.Config().Get().Video
	if got.GlabsModel != "omni_flash" {
		t.Fatalf("model %q", got.GlabsModel)
	}
	if got.Provider != "glabs" {
		t.Fatalf("the second click switched the provider to %q", got.Provider)
	}
	// Half a second later a press is a press again.
	d.settle(30)
	google, _ = near(d.labels("Google"), d.mustLabel("Хто знімає відео"), 70)
	d.clickRect(google)
	d.settle(3)
	if got := c.E.Config().Get().Video.Provider; got != "google" {
		t.Fatalf("after the guard: %q", got)
	}
	_ = image.Point{}
}

// Google chosen without Google keys: the cards say so and bring the previous
// setup back in one press.
func TestGoogleWithoutKeysWarns(t *testing.T) {
	c, h := demo(t)
	_ = c.E.Config().Update(func(cf *config.Config) {
		cf.Google.Keys = nil
		cf.Text = config.Text{Provider: "google", BaseURL: "http://127.0.0.1:8317/v1", APIKey: "cg-demo-not-real-0000", Model: "gemini-3.8-flash-high"}
		cf.Image.Provider = "google"
	})
	d := &driver{t: t, c: c, h: h, p: New(), section: "text"}
	d.settle(3)
	if len(d.labels("Ключів Google немає")) == 0 {
		t.Fatal("no warning in «Текст»")
	}
	d.clickRect(d.mustLabel("Повернути OpenAI-сумісний"))
	d.settle(3)
	if got := c.E.Config().Get().Text; got.Provider != "openai" || got.APIKey != "cg-demo-not-real-0000" || got.Model != "gemini-3.8-flash-high" {
		t.Fatalf("text after «Повернути»: %+v", got)
	}
	d.section = "frames"
	d.settle(3)
	if len(d.labels("Ключів Google ще немає")) == 0 {
		t.Fatal("no warning in «Кадри»")
	}
	d.clickRect(d.mustLabel("Повернути G-Labs"))
	d.settle(3)
	if got := c.E.Config().Get().Image.Provider; got != "glabs" {
		t.Fatalf("frames after «Повернути G-Labs»: %q", got)
	}
}
