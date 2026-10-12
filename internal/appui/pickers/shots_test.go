package pickers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/tts"
)

// webRoot is the repo folder of the web studio (…/YT-CARTELL-AI-MAKER).
const webRoot = "../../../.."

// DemoVoices is a fake voice list for screenshots (no network).
var demoVoices = tts.VoiceList{Provider: "elevenlabs", Tier: "creator", CharsLeft: 90000, Voices: []tts.Voice{
	{ID: "v-olena", Name: "Олена", Gender: "female", Accent: "ukrainian", Category: "cloned", PreviewURL: "https://example.invalid/olena.mp3", Usable: true},
	{ID: "v-rachel", Name: "Rachel", Gender: "female", Accent: "american", Age: "young", Category: "premade", PreviewURL: "https://example.invalid/rachel.mp3", Usable: true},
	{ID: "v-adam", Name: "Adam", Gender: "male", Accent: "american", Age: "middle_aged", Category: "premade", PreviewURL: "https://example.invalid/adam.mp3", Usable: true},
	{ID: "v-george", Name: "George", Gender: "male", Accent: "british", Category: "premade", PreviewURL: "https://example.invalid/george.mp3", Usable: true},
	{ID: "v-taras", Name: "Тарас", Gender: "male", Accent: "ukrainian", Category: "professional", PreviewURL: "https://example.invalid/taras.mp3", Usable: false},
	{ID: "v-mira", Name: "Міра", Gender: "female", Category: "generated", Usable: true},
}}

type fields struct {
	flat   bool // inside a card: flat inset fields
	style  Style
	lang   Language
	voice  Voice
	genre  Choice
	tone   Choice
	camera Choice
	dur    Duration
	aspect Aspect
	model  ImageModel
	sound  Sound
	cont   Content
}

func (f *fields) layout(gtx C, c *core.Ctx) D {
	th := c.Th
	_ = th
	f.genre.None, f.tone.None = "Авто — за історією", "Нейтральний оповідач"
	f.genre.Icon, f.tone.Icon, f.camera.Icon = gui.IconDrama, gui.IconMic, gui.IconCamera
	f.dur.Note = "≈ 8 сцен по 8 с"
	items := []func(gtx C) D{
		func(gtx C) D { return f.dur.Layout(gtx, c, "Тривалість", 60) },
		func(gtx C) D { return f.aspect.Layout(gtx, c, "Формат", "16:9") },
		func(gtx C) D { return f.lang.Layout(gtx, c, "Мова фільму", "en") },
		func(gtx C) D { return f.tone.Layout(gtx, c, "Тон оповіді", studio.Tones, "storyteller") },
		func(gtx C) D { return f.voice.Layout(gtx, c, "Голос оповідача", "v-rachel") },
		func(gtx C) D { return f.style.Layout(gtx, c, "Стиль", "watercolor") },
		func(gtx C) D { return f.genre.Layout(gtx, c, "Жанр або ніша", studio.Genres, "fairy tale") },
		func(gtx C) D { return f.camera.Layout(gtx, c, "Камера", studio.Cameras, "cinematic") },
		func(gtx C) D { return f.model.Layout(gtx, c, "Модель кадрів", "NARWHAL") },
		func(gtx C) D { return f.sound.Layout(gtx, c, "Звук", studio.AudioInVideo) },
		func(gtx C) D {
			return f.cont.Layout(gtx, c, "Що в кадрі", ContentValue{Content: studio.ContentMotion})
		},
	}
	for _, l := range []*Look{&f.style.Look, &f.lang.Look, &f.voice.Look, &f.genre.Look, &f.tone.Look, &f.camera.Look, &f.dur.Look, &f.aspect.Look, &f.model.Look, &f.sound.Look, &f.cont.Look} {
		l.Flat = f.flat
	}
	grid := func(gtx C) D {
		return gui.Grid{MinWidth: 300, Gap: gui.Sp16, Stretch: true}.Layout(gtx, len(items), func(gtx C, i int) D {
			return items[i](gtx)
		})
	}
	return layout.UniformInset(gui.Sp32).Layout(gtx, func(gtx C) D {
		if f.flat {
			return gui.Card{}.Layout(gtx, th, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp16, gui.Text{Style: th.H3}.Widget(th, "Профіль · налаштування"), grid)
			})
		}
		return grid(gtx)
	})
}

func TestPickerShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the picker screenshots")
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	c.SetVoicesForTest(demoVoices)
	shots := []struct {
		name string
		open func(f *fields)
	}{
		{"fields", func(*fields) {}},
		{"flat", func(f *fields) { f.flat = true }},
		{"flat-voice", func(f *fields) { f.flat = true; f.voice.Open() }},
		{"style", func(f *fields) { f.style.Open() }},
		{"language", func(f *fields) { f.lang.Open() }},
		{"voice", func(f *fields) { f.voice.Open() }},
		{"genre", func(f *fields) { f.genre.Open() }},
		{"duration", func(f *fields) { f.dur.Open() }},
		{"aspect", func(f *fields) { f.aspect.Open() }},
		{"model", func(f *fields) { f.model.Open() }},
		{"sound", func(f *fields) { f.sound.Open() }},
		{"content", func(f *fields) { f.cont.Open() }},
	}
	only := os.Getenv("GUISHOTS_ONLY") // substring filter for quick iterations
	sizes := []struct{ w, h int }{{1440, 900}, {1920, 1080}, {390, 844}}
	for _, dark := range []bool{false, true} {
		mode := "light"
		if dark {
			mode = "dark"
		}
		c.Th.SetPaletteNow(dark)
		for _, sz := range sizes {
			for _, s := range shots {
				if only != "" && !strings.Contains(s.name, only) {
					continue
				}
				if sz.w == 390 && s.name != "fields" && s.name != "flat" && s.name != "language" && s.name != "voice" {
					continue
				}
				f := &fields{}
				s.open(f)
				path := filepath.Join(out, "pickers", s.name+"-"+mode+"-"+itoa(sz.w)+"x"+itoa(sz.h)+".png")
				if err := core.Shot(c, h, sz.w, sz.h, 1, path, func(gtx C) D { return f.layout(gtx, c) }); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
