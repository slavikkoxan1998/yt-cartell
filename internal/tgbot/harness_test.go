package tgbot

// Test harness: settings and data in temp folders (never the user's), a
// transport guard that refuses every non-loopback host (no real Telegram,
// no real services), the fake Bot API and a demo engine with seeded projects.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
	"ytcartell/internal/notify"
	"ytcartell/internal/studio"
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ytc-tgbot-test-")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Setenv("AppData", filepath.Join(tmp, "appdata"))
	os.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localappdata"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	if runtime.GOOS == "darwin" {
		os.Setenv("HOME", filepath.Join(tmp, "home"))
	}
	if !strings.HasPrefix(config.Path(), tmp) {
		fmt.Println("refusing to run: settings path is not redirected:", config.Path())
		os.Exit(1)
	}
	http.DefaultTransport = &guard{base: http.DefaultTransport}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// guard lets requests reach this test binary's own mock servers only — not
// even other loopback ports: the owner's local services (a text gateway on
// 127.0.0.1:8317, G-Labs on :8765) may be running on this machine.
type guard struct{ base http.RoundTripper }

var mocks sync.Map // host:port → true

// allow registers a mock server with the guard.
func allow(rawURL string) {
	if u, err := url.Parse(rawURL); err == nil {
		mocks.Store(u.Host, true)
	}
}

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if _, ok := mocks.Load(r.URL.Host); !ok {
		return nil, errors.New("test guard: no network to " + r.URL.Host)
	}
	return g.base.RoundTrip(r)
}

// deadURL is where the services of a test's settings point (discard port,
// refused by the guard anyway).
const deadURL = "http://127.0.0.1:9"

var (
	owner    = notify.User{ID: 4242, FirstName: "Іван", Username: "ivan"}
	stranger = notify.User{ID: 666, FirstName: "Чужий", Username: "intruder"}
	member   = notify.User{ID: 777, FirstName: "Петро", Username: "petro"}
)

const ownerChat = "4242"

type harness struct {
	t     *testing.T
	dir   string
	fake  *fakeTG
	e     *studio.Engine
	store *config.Store
	bot   *Bot
}

// seed is a project written to disk before the engine loads it.
type seed struct {
	id, title string
	video     string // a file to use as the final video ("" = none)
	scenes    int    // with frames and voices
	cover     bool
	srt       bool
	job       *studio.Job
	mut       func(p *studio.Project) // last changes before the save
}

type hopts struct {
	tg     func(t *config.Telegram)
	cfg    func(c *config.Config)
	seeds  []seed
	opt    Options
	noWait bool
}

func newHarness(t *testing.T, o hopts) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), fake: newFake(t)}
	data := filepath.Join(h.dir, "data")
	for _, s := range o.seeds {
		writeSeed(t, data, s)
	}
	cfg := config.Default()
	cfg.DataDir = data
	cfg.Text.APIKey = "test-text-key"
	cfg.Text.BaseURL = deadURL + "/v1" // never the owner's gateway
	cfg.Glabs.BaseURL = deadURL
	cfg.Telegram = config.Telegram{Enabled: true, BotToken: testToken, ChatID: ownerChat, OwnerID: owner.ID, OwnerName: owner.Name(),
		Remote: true, Live: true, OnDone: true, OnError: true, OnFinal: true, APIServer: h.fake.URL()}
	if o.tg != nil {
		o.tg(&cfg.Telegram)
	}
	if o.cfg != nil {
		o.cfg(&cfg)
	}
	h.store = config.NewStoreWith(cfg)
	e, err := studio.NewEngine(h.store)
	if e == nil {
		t.Fatal(err)
	}
	h.e = e
	opt := o.opt
	opt.StateDir = h.dir
	if opt.PollTimeout == 0 {
		opt.PollTimeout = 1
	}
	if opt.Retry == 0 {
		opt.Retry = 20 * time.Millisecond
	}
	h.bot = New(e, opt)
	h.bot.pace.fast = true // Telegram's pacing has its own test (TestPacer)
	h.bot.Start()
	t.Cleanup(func() {
		h.bot.Stop(2 * time.Second)
		e.Shutdown(2 * time.Second)
	})
	if !o.noWait && cfg.Telegram.RemoteOn() {
		h.fake.wait("the bot to listen", func() bool { return h.bot.State().Status == StatusOn })
	}
	return h
}

// writeSeed writes a project folder the engine will load.
func writeSeed(t *testing.T, data string, s seed) {
	t.Helper()
	dir := filepath.Join(data, "projects", s.id)
	for _, sub := range []string{"images", "audio", "montage", "videos"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UnixMilli()
	p := studio.Project{ID: s.id, Title: s.title, CreatedAt: now, UpdatedAt: now, Step: 1, Input: studio.DefaultInput(),
		Characters: []studio.Character{}, Locations: []studio.Location{}, Scenes: []studio.Scene{}, Montage: studio.DefaultMontage(), Job: s.job}
	p.Input.Text = "Маленький хлопчик Тарас загубився в осінньому лісі й шукав дорогу додому."
	for i := 0; i < s.scenes; i++ {
		frame := fmt.Sprintf("images/frame-%d.jpg", i+1)
		writeJPEG(t, filepath.Join(dir, filepath.FromSlash(frame)), 320, 180, uint8(40*i))
		voice := fmt.Sprintf("audio/voice-%d.mp3", i+1)
		os.WriteFile(filepath.Join(dir, filepath.FromSlash(voice)), []byte("ID3fake-mp3"), 0o644)
		p.Scenes = append(p.Scenes, studio.Scene{ID: fmt.Sprintf("s%d", i+1), Title: fmt.Sprintf("Сцена %d", i+1),
			Speaker: "narrator", Narration: fmt.Sprintf("Тарас іде стежкою, сцена %d.", i+1), CharacterIDs: []string{},
			Frame: studio.Asset{Path: frame, Status: studio.StatusDone, UpdatedAt: now},
			Video: studio.Asset{Status: studio.StatusDone, Path: frame, UpdatedAt: now},
			Audio: studio.Asset{Path: voice, Status: studio.StatusDone, Duration: 2.5, UpdatedAt: now}})
		p.Step = 4
	}
	if s.video != "" {
		b, err := os.ReadFile(s.video)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "montage", "final.mp4"), b, 0o644)
		p.Montage.Output = studio.Asset{Path: "montage/final.mp4", Status: studio.StatusDone, Duration: 4, UpdatedAt: now}
		p.Step = 5
	}
	if s.cover {
		writeJPEG(t, filepath.Join(dir, "images", "cover.jpg"), 1280, 720, 200)
		p.Montage.Cover = studio.Asset{Path: "images/cover.jpg", Status: studio.StatusDone, UpdatedAt: now}
	}
	if s.srt {
		os.WriteFile(filepath.Join(dir, "montage", "final.srt"), []byte("1\n00:00:00,000 --> 00:00:02,000\nПривіт\n"), 0o644)
		p.Montage.SRTPath = "montage/final.srt"
	}
	if s.mut != nil {
		s.mut(&p)
	}
	b, _ := json.MarshalIndent(p, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "project.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeJPEG(t *testing.T, path string, w, h int, shade uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{shade, uint8(x), uint8(y), 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
}

// testVideo makes a small real mp4 with FFmpeg (keyframes every half
// second, so it can be split); skips the test without FFmpeg.
func testVideo(t *testing.T, seconds int, size string, bitrate string) string {
	t.Helper()
	ff, err := media.Locate(config.Default())
	if err != nil || !ff.OK() {
		t.Skip("FFmpeg not found")
	}
	out := filepath.Join(t.TempDir(), "film.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err = ff.Run(ctx, "", "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%s:rate=24", size),
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", fmt.Sprint(seconds), "-c:v", "libx264", "-preset", "ultrafast", "-b:v", bitrate, "-g", "12",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "96k", "-shortest", out)
	if err != nil {
		t.Skipf("FFmpeg cannot make a test video: %v", err)
	}
	return out
}

// chatCalls are the calls into one chat.
func (h *harness) chatCalls(chat string) []call {
	var out []call
	for _, c := range h.fake.all() {
		if c.chat == chat {
			out = append(out, c)
		}
	}
	return out
}
