package settings

// Headless screenshots of the settings page (skipped unless GUISHOTS=<dir>):
//
//	GUISHOTS=../../../screenshots/new/settings go test ./internal/appui/settings -run TestShots
//
// The engine is core.DemoEngine on a temp folder (fake keys, in-memory
// settings and profiles); nothing touches the network or the user's data.

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/tts"
)

const webRoot = "../../../.."

type shotSize struct{ w, h int }

var shotSizes = []shotSize{{1920, 1080}, {1440, 900}, {390, 844}}

func demo(t *testing.T) (*core.Ctx, *core.HeadlessHost) {
	t.Helper()
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Shutdown(0) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	c.SetVoicesForTest(tts.VoiceList{Provider: "elevenlabs", Tier: "creator", CharsLeft: 81234, Voices: []tts.Voice{
		{ID: "v-olena", Name: "Олена", Gender: "female", Accent: "українська", Usable: true, Category: "premade"},
		{ID: "v-taras", Name: "Тарас", Gender: "male", Accent: "глибокий", Usable: true, Category: "premade"},
		{ID: "v-mira", Name: "Міра", Gender: "female", Usable: true, Category: "cloned"},
	}})
	return c, h
}

// shell draws a stand-in for the app shell: the 92 dp rail (a bottom bar
// under 900 dp) and the page in the remaining area.
func shell(c *core.Ctx, page func(gtx C) D) func(gtx C) D {
	return func(gtx C) D {
		th := c.Th
		size := gtx.Constraints.Max
		wide := size.X >= gtx.Dp(900)
		area := image.Rectangle{Max: size}
		if wide {
			rw := gtx.Dp(92)
			gui.FillRect(gtx, image.Rect(0, 0, rw, size.Y), th.Pal.Card)
			gui.FillRect(gtx, image.Rect(rw-1, 0, rw, size.Y), th.Pal.Border)
			for i, ic := range []*gui.Icon{gui.IconHome, gui.IconFolder, gui.IconListVideo, gui.IconSettings} {
				y := gtx.Dp(120) + i*gtx.Dp(68)
				if i == 3 {
					gui.FillRRect(gtx, image.Rect((rw-gtx.Dp(56))/2, y-gtx.Dp(4), (rw+gtx.Dp(56))/2, y+gtx.Dp(28)), gui.RadiusPill, th.Pal.ActionSoft)
				}
				o := op.Offset(image.Pt((rw-gtx.Dp(22))/2, y)).Push(gtx.Ops)
				col := th.Pal.Text2
				if i == 3 {
					col = th.Pal.Action
				}
				ic.Layout(gtx, 22, col)
				o.Pop()
			}
			area.Min.X = rw
		} else {
			bh := gtx.Dp(64)
			gui.FillRect(gtx, image.Rect(0, size.Y-bh, size.X, size.Y), th.Pal.Card)
			gui.FillRect(gtx, image.Rect(0, size.Y-bh, size.X, size.Y-bh+1), th.Pal.Border)
			area.Max.Y -= bh
		}
		o := op.Offset(area.Min).Push(gtx.Ops)
		pg := gtx
		pg.Constraints = layout.Exact(area.Size())
		page(pg)
		o.Pop()
		return D{Size: size}
	}
}

type shotState struct {
	name    string
	section string
	setup   func(c *core.Ctx, p *Page) // runs on the second frame (after the visit reset)
	config  func(cf *config.Config)
}

var fakeKeys = []string{"AIzaSyDEMO-not-a-real-key-0001", "AIzaSyDEMO-not-a-real-key-0002"}

func withChecks(c *core.Ctx, p *Page) {
	p.text.check = check{done: true, ok: true, msg: "Відповідь за 812 мс (gemini-3.8-flash @ http://127.0.0.1:8317/v1): OK"}
	p.glabs.check = check{done: true, ok: false, msg: "G-Labs не відповідає: з'єднання з 127.0.0.1:8765 відхилено."}
	p.google.check = check{done: true, ok: true, msg: "Ключ Google працює"}
	p.ff.check = check{done: true, ok: true, msg: "FFmpeg 8.0-full_build-www.gyan.dev"}
	p.stt.check = check{busy: true}
	p.telegram.check = check{done: true, ok: true, msg: "Тестове повідомлення надіслано — бот @ytcartell_demo_bot"}
	p.text.saved.want = true
}

var shotStates = []shotState{
	{name: "top"},
	{name: "checks", setup: withChecks, config: func(cf *config.Config) {
		cf.Google.Keys = fakeKeys
		cf.Telegram.Enabled = true
		cf.Telegram.BotToken = "123456789:DEMO-token-not-real"
		cf.Telegram.ChatID = "123456789"
	}},
	{name: "google-text", config: func(cf *config.Config) {
		cf.Text.Provider = "google"
		cf.Image.Provider = "google"
		cf.Video.Provider = "glabs"
		cf.Video.GlabsModel = "omni_flash"
		cf.Video.Length = 10
		cf.TTS.Provider = "openai"
		cf.STT.Provider = "openai"
	}},
	// «Озвучка» (tts_test.go): ElevenLabs keys checked against a mock — a free
	// and a paid account, a rejected key; Alice is a default voice.
	{name: "voice", section: "tts", setup: ttsShotChecked, config: ttsShotElevenLabs},
	// Cartesia right after switching: its own (empty) keys and voices.
	{name: "voice-cartesia", section: "tts", config: func(cf *config.Config) {
		ttsShotElevenLabs(cf)
		cf.TTS.Switch("cartesia")
	}},
	// A pasted list: the bulk area, then what it added and the check.
	{name: "voice-paste", section: "tts", setup: ttsShotPaste, config: ttsShotOneKey},
	{name: "voice-bulk", section: "tts", setup: ttsShotBulk, config: ttsShotOneKey},
	{name: "voice-shared", section: "tts", config: func(cf *config.Config) {
		// Google without a TTS key: the Google AI Studio keys are borrowed.
		cf.TTS.Switch("google")
		cf.Google.Keys = fakeKeys
	}},
	{name: "custom", section: "frames", config: func(cf *config.Config) {
		cf.Image.Provider, cf.Video.Provider = "custom", "custom"
		cf.Custom.ImagePreset, cf.Custom.VideoPreset = "fal", "replicate"
		cf.Custom.ImageKey = "demo-fal-key-not-real-0000"
	}},
	{name: "lengths", section: "video", config: func(cf *config.Config) {
		cf.Video.Provider, cf.Video.GlabsModel = "glabs", "omni_flash"
		cf.Video.Ultra, cf.Video.Dynamic, cf.Video.Length = true, true, 6
	}},
	{name: "stt-local", section: "stt", config: func(cf *config.Config) { cf.STT.Provider = "local" }},
	{name: "stt", section: "stt"},
	{name: "profiles", section: "profiles"},
	{name: "autopilot", section: "autopilot"},
	{name: "telegram", section: "telegram", setup: func(c *core.Ctx, p *Page) {
		p.telegram.check = check{done: true, ok: false, msg: "Chat not found (400)"}
	}, config: func(cf *config.Config) {
		cf.Telegram.Enabled = true
		cf.Telegram.BotToken = "123456789:DEMO-token-not-real"
	}},
	// «Помічник підключення»: a checked plan (no network: the plan is set).
	{name: "assistant", section: "assistant", setup: assistShot},
	// «Перевірити» of the text card: the exact request after a failure.
	{name: "text-error", section: "text", setup: func(c *core.Ctx, p *Page) {
		p.text.check = check{done: true, ok: false, msg: textErrorShot}
	}, config: func(cf *config.Config) {
		cf.Text = config.Text{Provider: "openai", BaseURL: "https://gateway.example.com", APIKey: "sk-demo-not-a-real-key-0001", Model: "gemini-3.8-flash"}
	}},
	{name: "appearance", section: "appearance"},
	{name: "data", section: "data", setup: func(c *core.Ctx, p *Page) {
		p.data.importRes = check{done: true, ok: true, msg: "Імпортовано 3 проєкти і налаштування."}
	}},
}

func TestShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("GUISHOTS=<dir> renders the settings screenshots")
	}
	only := os.Getenv("GUISHOTS_ONLY")
	for _, st := range shotStates {
		if only != "" && !strings.Contains(","+only+",", ","+st.name+",") {
			continue
		}
		for _, dark := range []bool{false, true} {
			for _, sz := range shotSizes {
				st, dark, sz := st, dark, sz
				theme := "light"
				if dark {
					theme = "dark"
				}
				name := fmt.Sprintf("settings-%s-%s-%dx%d.png", st.name, theme, sz.w, sz.h)
				t.Run(name, func(t *testing.T) {
					c, h := demo(t)
					if st.config != nil {
						if err := c.E.Config().Update(st.config); err != nil {
							t.Fatal(err)
						}
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

// TestSectionTabs checks the tab mapping of route sections and cards.
func TestSectionTabs(t *testing.T) {
	for id, want := range map[string]int{"connections": 0, "stt": 0, "google": 0, "profiles": 1, "autopilot": 2, "telegram": 3, "appearance": 4, "data": 5, "about": 5, "nope": -1} {
		if got := tabOf(id); got != want {
			t.Errorf("tabOf(%q) = %d, want %d", id, got, want)
		}
	}
}

// TestCommitOnSave checks that a change goes through the store and that
// checks never run offline.
func TestCommitOnSave(t *testing.T) {
	c, h := demo(t)
	p := New()
	p.cfg.set(c, func(cf *config.Config) { cf.Concurrency = 5 }, nil)
	if p.cfg.get(c).Concurrency != 5 {
		t.Fatal("optimistic value not applied")
	}
	h.Flush()
	if got := c.E.Config().Get().Concurrency; got != 5 {
		t.Fatalf("store Concurrency = %d, want 5", got)
	}
	if p.cfg.pending != 0 {
		t.Fatalf("pending = %d after flush", p.cfg.pending)
	}
	k := &check{}
	called := false
	p.runCheck(c, k, nil)
	h.Flush()
	if called || k.busy || !k.done || k.ok {
		t.Fatalf("offline check ran or has the wrong state: %+v", *k)
	}
}

// TestEngineLengthsAndKeys: the clip lengths come from the engine
// (studio.VideoCapsFor) with the Ultra-only ones as dimmed hints, a length
// snaps to the nearest allowed one, and key states pair by their place in
// [APIKey, Keys...].
func TestEngineLengthsAndKeys(t *testing.T) {
	var cf config.Config
	cf.Video.Provider, cf.Video.GlabsModel = "glabs", "veo_31_fast"
	allowed, all := clipLengths(cf)
	if fmt.Sprint(allowed, all) != "[8] [4 6 8]" {
		t.Fatalf("G-Labs Veo: %v %v", allowed, all)
	}
	cf.Video.Length = 6
	fitLength(&cf)
	if cf.Video.Length != 8 {
		t.Fatalf("6 s without Ultra → %d, want 8", cf.Video.Length)
	}
	cf.Video.GlabsModel = "omni_flash" // 4/6 s need no Ultra on Omni Flash
	if allowed, all = clipLengths(cf); fmt.Sprint(allowed, all) != "[4 6 8 10] [4 6 8 10]" {
		t.Fatalf("Omni Flash: %v %v", allowed, all)
	}
	cf.Video.Ultra = true
	if allowed, all = clipLengths(cf); fmt.Sprint(allowed, all) != "[4 6 8 10] [4 6 8 10]" {
		t.Fatalf("Omni Flash + Ultra: %v %v", allowed, all)
	}
	// What G-Labs renders only on Ultra accounts is flagged without it.
	if m, r := glabsUltraHints(config.Video{GlabsModel: "veo_31_lite_relaxed", Resolution: "4K"}); m == "" || r == "" {
		t.Fatalf("no Ultra warnings: %q %q", m, r)
	}
	if m, r := glabsUltraHints(config.Video{GlabsModel: "veo_31_lite_relaxed", Resolution: "4K", Ultra: true}); m != "" || r != "" {
		t.Fatalf("Ultra warnings with Ultra: %q %q", m, r)
	}
	if m, r := glabsUltraHints(config.Video{GlabsModel: "omni_flash", Resolution: "1080p"}); m != "" || r != "" {
		t.Fatalf("Omni Flash 1080p needs no Ultra: %q %q", m, r)
	}
	cf.Video.Provider = "google" // VeoModel "": the default Gemini Omni Flash
	if allowed, all = clipLengths(cf); fmt.Sprint(allowed, all) != "[4 6 8 10] [4 6 8 10]" {
		t.Fatalf("Google Omni: %v %v", allowed, all)
	}
	cf.Google.VeoModel = "veo-3.1-generate-001" // a Veo id without a shutdown date (the 3.1 previews retire 2026-10-22)
	if allowed, all = clipLengths(cf); fmt.Sprint(allowed, all) != "[4 6 8] [4 6 8]" {
		t.Fatalf("Google Veo: %v %v", allowed, all)
	}
	cf.Video.Length = 10
	fitLength(&cf)
	if cf.Video.Length != 8 {
		t.Fatalf("10 s on Google Veo → %d, want 8", cf.Video.Length)
	}
	if opts := googleVideoOptions("gemini-omni-flash-preview"); len(opts) == 0 || opts[0].Value != config.GoogleOmniModel ||
		opts[len(opts)-1].Value != "gemini-omni-flash-preview" {
		t.Fatalf("google video options: %+v", opts)
	}
	if got := fmt.Sprint(ttsKeyIndex(config.TTS{APIKey: " ", Keys: []string{"a", "", "b"}})); got != "[1 3]" {
		t.Fatalf("key places = %s", got)
	}
}

// TestStackSkipsEmptyCards: a card that draws nothing takes no room — no
// gap, no mark.
func TestStackSkipsEmptyCards(t *testing.T) {
	p := New()
	gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1},
		Constraints: layout.Constraints{Max: image.Pt(600, 4000)}}
	box := func(h int) layout.Widget {
		return func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Max.X, h)} }
	}
	p.marks["empty"] = 7 // left from an earlier frame
	d := p.stack(gtx, 10, 600, []block{{"appearance", box(100)}, {"empty", box(0)}, {"about", box(50)}})
	gap := gtx.Dp(gui.Sp24)
	if d.Size.Y != 150+gap || p.marks["about"] != 10+100+gap {
		t.Fatalf("a hole: height %d, «Про студію» at %d", d.Size.Y, p.marks["about"])
	}
	if _, ok := p.marks["empty"]; ok {
		t.Fatal("the empty card keeps its mark")
	}
}

const textErrorShot = "Жодна форма адреси не підійшла (пробували з /v1, /api/v1, /openai/v1): Текстовий ендпоінт відповів помилкою 404: {\"error\":\"not_found\"}\n" +
	"Запит: POST https://gateway.example.com/openai/v1/chat/completions → 404 Not Found\nВідповідь: {\"error\":\"not_found\"}\n" +
	"Підказка: сервер не знає цього методу за цією адресою — найчастіше бракує /v1 (напр. https://сервер/v1) або /api/v1\n" +
	"Спробувано:\n• GET https://gateway.example.com/models → 404 Not Found\n• GET https://gateway.example.com/v1/models → 404 Not Found\n• POST https://gateway.example.com/chat/completions → 404 Not Found"

// assistShot fills the assistant with a pasted note and a checked plan.
func assistShot(c *core.Ctx, p *Page) {
	p.assist.input.SetText("ось мій ендпоінт https://gateway.example.com ключ sk-demo…XyZ1 модель gemini-3.8-flash\n" +
		"озвучка ElevenLabs sk_…4567, а ще Lumean https://lumean.app/developers/docs\nAIza…0001")
	p.assist.plan = &studio.AssistPlan{
		Items: []studio.AssistItem{
			{Kind: "url", Display: "https://gateway.example.com"},
			{Kind: "key", Display: "sk-…XyZ1"},
			{Kind: "model", Display: "gemini-3.8-flash"},
			{Kind: "key", Display: "sk_…4567", Service: "ElevenLabs"},
			{Kind: "docs", Display: "https://lumean.app/developers/docs", Service: "Lumean"},
			{Kind: "key", Display: "AIz…0001", Service: "Google AI Studio"},
		},
		Steps: []*studio.AssistStep{
			{Role: studio.RoleText, Target: "gateway.example.com · gemini-3.8-flash", Status: "ok", On: true,
				Lines: []string{"Протокол: OpenAI-сумісний", "Адреса: https://gateway.example.com/v1", "Ключ: sk-…XyZ1", "Модель: gemini-3.8-flash"},
				Msg:   "Відповідь за 640 мс (gemini-3.8-flash @ https://gateway.example.com/v1): OK · моделей: 42"},
			{Role: studio.RoleImage, Target: "Google AI Studio · Nano Banana", Status: "ok", On: false,
				Lines: []string{"Ключ Google: AIz…0001 (додається до списку)"}, Msg: "Ключ Google працює"},
			{Role: studio.RoleVideo, Target: "Google AI Studio · Gemini Omni", Status: "ok", On: true,
				Lines: []string{"Ключ Google: AIz…0001 (додається до списку)"}, Msg: "Ключ Google працює"},
			{Role: studio.RoleTTS, Target: "ElevenLabs", Status: "fail", On: false,
				Lines: []string{"Сервіс: ElevenLabs", "Ключ: sk_…4567 (додається до ключів сервісу)"},
				Msg: "ElevenLabs: невірний API-ключ\nЗапит: GET https://api.elevenlabs.io/v1/user/subscription → 401 Unauthorized\n" +
					"Відповідь: {\"detail\":{\"status\":\"invalid_api_key\",\"message\":\"Invalid API key\"}}\nПідказка: ключ не прийнято — перевір ключ"},
			{Role: studio.RoleSTT, Target: "ElevenLabs Scribe", Status: "ok", On: true,
				Lines: []string{"Сервіс: ElevenLabs", "Ключ: sk_…4567"}, Msg: "ElevenLabs Scribe (scribe_v2): ключ працює · тариф creator"},
		},
		Notes: []string{"Текст: є й інший варіант — Google AI Studio", "Lumean: озвучка — окремий сервіс «Lumean» в картці «Озвучка», встав його ключ"},
		AI:    "ШІ підказав призначення 1 адреси",
	}
	p.assist.toggles = make([]gui.Toggle, len(p.assist.plan.Steps))
}
