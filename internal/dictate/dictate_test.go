package dictate

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"ytcartell/internal/config"
)

func TestParseDShow(t *testing.T) {
	got := parseDShow(dshowList)
	want := []Device{
		{Name: "Microphone (High Definition Audio Device)", ID: `@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{83C49780}`},
		{Name: "CABLE Output (VB-Audio Virtual Cable)", ID: `@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{E58CECA0}`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("new format:\n%+v", got)
	}
	// FFmpeg ≤ 4.4: section headers, no kind after the name.
	old := `[dshow @ 0x1] DirectShow video devices (some may be both video and audio devices)
[dshow @ 0x1]  "Integrated Camera"
[dshow @ 0x1]     Alternative name "@device_pnp_\\?\usb#vid"
[dshow @ 0x1] DirectShow audio devices
[dshow @ 0x1]  "Мікрофон (Realtek(R) Audio)"
[dshow @ 0x1]     Alternative name "@device_cm_{33D9A762}\wave_{AAA}"
dummy: Immediate exit requested`
	got = parseDShow(old)
	if len(got) != 1 || got[0].Name != "Мікрофон (Realtek(R) Audio)" || got[0].ID != `@device_cm_{33D9A762}\wave_{AAA}` {
		t.Fatalf("old format: %+v", got)
	}
	// A device that is both: counts.
	if got := parseDShow(`[dshow @ 1] "USB Cam" (audio, video)`); len(got) != 1 {
		t.Fatalf("audio, video: %+v", got)
	}
}

func TestParseAVFoundationAndPulse(t *testing.T) {
	out := `[AVFoundation indev @ 0x7f8] AVFoundation video devices:
[AVFoundation indev @ 0x7f8] [0] FaceTime HD Camera
[AVFoundation indev @ 0x7f8] [1] Capture screen 0
[AVFoundation indev @ 0x7f8] AVFoundation audio devices:
[AVFoundation indev @ 0x7f8] [0] MacBook Pro Microphone
[AVFoundation indev @ 0x7f8] [1] Rode NT-USB  [uid:AppleUSBAudioEngine:RODE:1]
: Input/output error`
	got := parseAVFoundation(out)
	if len(got) != 2 || got[0] != (Device{Name: "MacBook Pro Microphone", ID: "0"}) || got[1].ID != "1" || got[1].Name != "Rode NT-USB" {
		t.Fatalf("avfoundation: %+v", got)
	}
	pulse := `Auto-detected sources for pulse:
* alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
  alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio] (none)
  bluez_input.00_11 [Headset]`
	got = parsePulse(pulse)
	if len(got) != 2 || !got[0].Default || got[0].Name != "Built-in Audio Analog Stereo" || got[1].ID != "bluez_input.00_11" {
		t.Fatalf("pulse: %+v", got)
	}
}

func TestPickDeviceAndArgs(t *testing.T) {
	devs := parseDShow(dshowList)
	if d, _ := pickDevice(devs, ""); d.Name != devs[0].Name {
		t.Fatalf("default = %+v", d)
	}
	if d, _ := pickDevice(devs, "cable output (vb-audio virtual cable)"); d.Name != devs[1].Name {
		t.Fatalf("by name = %+v", d)
	}
	if d, _ := pickDevice(devs, "Gone mic"); d.Name != devs[0].Name {
		t.Fatalf("missing → first, got %+v", d)
	}
	if _, ok := pickDevice(nil, ""); ok {
		t.Fatal("no devices")
	}
	old := goos
	defer func() { goos = old }()
	goos = "windows"
	a := strings.Join(inputArgs(devs[1]), " ")
	if !strings.Contains(a, "-f dshow -audio_buffer_size 50 -i audio=@device_cm_") {
		t.Fatalf("windows args %q", a)
	}
	goos = "darwin"
	if a := strings.Join(inputArgs(Device{}), " "); !strings.HasSuffix(a, "-f avfoundation -i :default") {
		t.Fatalf("mac args %q", a)
	}
	if a := strings.Join(inputArgs(Device{ID: "1"}), " "); !strings.HasSuffix(a, "-i :1") {
		t.Fatalf("mac index %q", a)
	}
	goos = "linux"
	if a := strings.Join(inputArgs(Device{}), " "); !strings.HasSuffix(a, "-f pulse -fragment_size 1024 -i default") {
		t.Fatalf("linux args %q", a)
	}
	if o := strings.Join(outputArgs, " "); !strings.Contains(o, "-ar 16000") || !strings.Contains(o, "-f s16le") || !strings.Contains(o, "-flush_packets 1 pipe:1") {
		t.Fatalf("output args %q", o)
	}
}

func TestAudioHelpers(t *testing.T) {
	if db := frameDB(tone(0.02)); db < -20 || db > -10 {
		t.Fatalf("tone dB %.1f", db)
	}
	if db := frameDB(hush(0.02)); db > -55 {
		t.Fatalf("hush dB %.1f", db)
	}
	if frameDB(make([]int16, FrameSize)) != -100 || !allZero(make([]int16, 3)) || allZero(hush(0.01)) {
		t.Fatal("digital silence")
	}
	if meterLevel(-80) != 0 || meterLevel(0) != 1 {
		t.Fatal("meter range")
	}
	// The detector: quiet → speech → hangover → quiet.
	var v vad
	feed := func(pcm []int16) (speech int) {
		for i := 0; i+FrameSize <= len(pcm); i += FrameSize {
			if v.push(frameDB(pcm[i : i+FrameSize])) {
				speech++
			}
		}
		return
	}
	if n := feed(hush(1)); n != 0 {
		t.Fatalf("noise counted as speech: %d frames", n)
	}
	if n := feed(tone(1)); n < 49 {
		t.Fatalf("tone: %d of 50 frames", n)
	}
	if n := feed(hush(1)); n != vadHangFrames {
		t.Fatalf("hangover %d frames", n)
	}
	// WAV header.
	w := wav(make([]int16, 100))
	if len(w) != 244 || string(w[:4]) != "RIFF" || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" {
		t.Fatalf("wav %q", w[:44])
	}
	// 16 → 24 kHz: 3 out for 2 in, continuous over frames, exact on a ramp.
	var u upsampler
	in := make([]int16, 640)
	for i := range in {
		in[i] = int16(i * 3)
	}
	out := append(u.push(in[:320]), u.push(in[320:])...)
	if len(out) != 959 { // the first sample, then 3 per 2
		t.Fatalf("upsampled %d samples", len(out))
	}
	for k, s := range out {
		if want := int16(2 * k); s != want { // position 2k/3 × 3
			t.Fatalf("out[%d] = %d, want %d", k, s, want)
		}
	}
}

func TestSplice(t *testing.T) {
	apply := func(text string, edits []Edit) string {
		rs := []rune(text)
		for _, e := range edits {
			rs = append(append(append([]rune(nil), rs[:e.Start]...), []rune(e.Text)...), rs[e.End:]...)
		}
		return string(rs)
	}
	// An empty box: the first words get a capital, finals then interim.
	var s Splice
	text := ""
	text = apply(text, s.Begin(text, 0, 0))
	text = apply(text, s.Update(text, 0, nil, "привіт як"))
	if text != "Привіт як" {
		t.Fatalf("interim %q", text)
	}
	if a, b := s.Interim(); a != 0 || b != 9 {
		t.Fatalf("interim range %d-%d", a, b)
	}
	text = apply(text, s.Update(text, 9, []string{"Привіт, як справи?"}, "добре"))
	if text != "Привіт, як справи? Добре" {
		t.Fatalf("final + interim %q", text)
	}
	if a, b := s.Interim(); string([]rune(text)[a:b]) != " Добре" {
		t.Fatalf("interim range %d-%d", a, b)
	}
	// Nothing new: no edit.
	if e := s.Update(text, len([]rune(text)), nil, "добре"); len(e) != 0 {
		t.Fatalf("no-op edit %+v", e)
	}
	text = apply(text, s.End(text, false))
	if text != "Привіт, як справи?" {
		t.Fatalf("end without interim %q", text)
	}

	// In the middle of a sentence, before a word: spaces on both sides,
	// no capital; a selection is replaced.
	text = "Жив собі ЦЕ ВИДАЛИТИ кіт."
	start := len([]rune("Жив собі "))
	end := start + len([]rune("ЦЕ ВИДАЛИТИ "))
	text = apply(text, s.Begin(text, start, end))
	if text != "Жив собі кіт." {
		t.Fatalf("selection %q", text)
	}
	text = apply(text, s.Update(text, start, []string{"рудий"}, ""))
	if text != "Жив собі рудий кіт." {
		t.Fatalf("mid-sentence %q", text)
	}
	text = apply(text, s.Update(text, start+5, nil, "пухнастий"))
	if text != "Жив собі рудий пухнастий кіт." {
		t.Fatalf("interim mid-sentence %q", text)
	}
	// The owner clicks elsewhere: the interim goes, dictation continues there.
	text = apply(text, s.Update(text, 0, nil, "пухнастий"))
	if text != "Пухнастий Жив собі рудий кіт." {
		t.Fatalf("moved caret %q", text)
	}
	text = apply(text, s.End(text, true))
	if text != "Пухнастий Жив собі рудий кіт." {
		t.Fatalf("end keep %q", text)
	}

	// Punctuation joins without a space; Chinese without spaces.
	if p := joinPiece('о', false, ", і далі"); p != ", і далі" {
		t.Fatalf("comma %q", p)
	}
	if p := joinPiece('好', false, "世界"); p != "世界" {
		t.Fatalf("unspaced %q", p)
	}
	if p := joinPiece('«', false, "так"); p != "так" {
		t.Fatalf("after quote %q", p)
	}
	if !sentenceStart([]rune("Кінець. "), 8) || sentenceStart([]rune("слово "), 6) || !sentenceStart([]rune("a\n"), 2) {
		t.Fatal("sentence starts")
	}
}

func TestResolveKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Normalize()
	cfg.TTS.Provider, cfg.TTS.APIKey = "elevenlabs", "sk_tts"
	cfg.Dictation.Provider = ElevenLabs
	p := Resolve(cfg)
	if !p.Ready || p.Key != "sk_tts" || p.KeyFrom == "" || p.Model != ElevenModel {
		t.Fatalf("ElevenLabs from TTS: %+v", p)
	}
	cfg.STT.APIKey = "sk_stt"
	if p = Resolve(cfg); p.Key != "sk_stt" {
		t.Fatalf("ElevenLabs from STT: %q", p.Key)
	}
	cfg.Dictation.ElevenKey = "sk_own"
	if p = Resolve(cfg); p.Key != "sk_own" || p.KeyFrom != "" {
		t.Fatalf("own key: %+v", p)
	}

	// OpenAI Realtime: only keys of the official API.
	cfg = config.Default()
	cfg.Dictation.Provider = OpenAI
	cfg.Text.APIKey = "gateway-key" // http://127.0.0.1:8317/v1 — a gateway
	if p = Resolve(cfg); p.Ready {
		t.Fatalf("a gateway key was sent to OpenAI: %+v", p)
	}
	cfg.Text.BaseURL = "https://api.openai.com/v1"
	if p = Resolve(cfg); !p.Ready || p.Key != "gateway-key" || p.Model != OpenAIRealtimeDef {
		t.Fatalf("official Text key: %+v", p)
	}

	// Chunks: the Text endpoint and key; Groq picks its Whisper.
	cfg = config.Default()
	cfg.Dictation.Provider = OpenAIChunks
	cfg.Text.APIKey = "tk"
	if p = Resolve(cfg); !p.Ready || p.BaseURL != "http://127.0.0.1:8317/v1" || p.Key != "tk" || p.Model != WhisperCompatDef {
		t.Fatalf("chunks from Text: %+v", p)
	}
	cfg.Dictation.ChunksURL, cfg.Dictation.ChunksKey = "https://api.groq.com/openai/v1/", "gk"
	cfg.Normalize()
	if p = Resolve(cfg); p.BaseURL != "https://api.groq.com/openai/v1" || p.Key != "gk" || p.Model != GroqDef {
		t.Fatalf("groq: %+v", p)
	}

	// Gemini: every Google key in rotation.
	cfg = config.Default()
	cfg.Dictation.Provider = Gemini
	cfg.Google.Keys = []string{"g1", "g2"}
	if p = Resolve(cfg); !p.Ready || len(p.Keys) != 2 || p.Model != GeminiDef {
		t.Fatalf("gemini: %+v", p)
	}

	// Deepgram needs its own key.
	cfg.Dictation.Provider = Deepgram
	if p = Resolve(cfg); p.Ready || !strings.Contains(p.Problem, "Deepgram") {
		t.Fatalf("deepgram without key: %+v", p)
	}
	if err := planErr(p); KindOf(err) != KindNoKey || !NeedsSettings(err) {
		t.Fatalf("plan error %v", err)
	}
}

func TestResolveAuto(t *testing.T) {
	old := localStatus
	defer func() { localStatus = old }()
	installed := false
	localStatus = func(config.Config) (string, string, bool, string) {
		if installed {
			return "C:/w/whisper-cli.exe", "C:/w/ggml-small.bin", true, "ok"
		}
		return "", "", false, "whisper.cpp ще не встановлено"
	}
	cfg := config.Default()
	cfg.TTS.Provider, cfg.TTS.APIKey = "openai", "" // nothing usable
	p := Resolve(cfg)
	if p.Ready || p.Provider != "" || !p.Auto || KindOf(planErr(p)) != KindNoProvider {
		t.Fatalf("nothing ready: %+v", p)
	}
	installed = true
	if p = Resolve(cfg); !p.Ready || p.Provider != Local || !p.Auto || p.Bin == "" {
		t.Fatalf("local installed: %+v", p)
	}
	// The STT choice comes first when it is ready.
	cfg.STT.Provider, cfg.STT.APIKey = "elevenlabs", "sk_x"
	if p = Resolve(cfg); p.Provider != ElevenLabs {
		t.Fatalf("auto → %q, want elevenlabs", p.Provider)
	}
	cfg.STT.Provider = "local"
	if p = Resolve(cfg); p.Provider != Local {
		t.Fatalf("auto (STT local) → %q", p.Provider)
	}
	// Silence setting.
	cfg.Dictation.SilenceSec = -1
	if Resolve(cfg).Silence != 0 {
		t.Fatal("never stop on silence")
	}
	cfg.Dictation.SilenceSec = 0
	if Resolve(cfg).Silence != config.DefaultSilenceSec {
		t.Fatal("default silence")
	}
}

func TestTextCleanup(t *testing.T) {
	cases := map[string]string{
		"  [BLANK_AUDIO] ":        "",
		"Дякую за перегляд!":      "",
		"(музика) Привіт, друзі.": "Привіт, друзі.",
		"...":       "",
		"♪ la la ♪": "la la",
		"*кашель* Отже, почнімо":          "Отже, почнімо",
		"Дякую за перегляд мого відео, а": "Дякую за перегляд мого відео, а",
	}
	for in, want := range cases {
		if got := cleanText(in, 1); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanText("Thank you.", 4); got != "Thank you." {
		t.Errorf("long real speech kept: %q", got)
	}
	if got := dropOverlap("Він пішов до старого лісу", "старого лісу, де жила сова."); got != "де жила сова." {
		t.Errorf("overlap %q", got)
	}
	if got := dropOverlap("раз два", "три чотири"); got != "три чотири" {
		t.Errorf("no overlap %q", got)
	}
	if got := tail("один два три чотири", 9); got != "чотири" {
		t.Errorf("tail %q", got)
	}
}

func TestCaptureFake(t *testing.T) {
	ff, argsFile := fakeMic(t, cat(tone(0.2), hush(0.1)), false)
	devs, err := ListDevices(bg, ff)
	if err != nil || len(devs) != 2 {
		t.Fatalf("list: %v %+v", err, devs)
	}
	c, err := startCapture(bg, ff, devs[0])
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	err = c.read(func(f []int16) bool { n++; return len(f) == FrameSize })
	if !errors.Is(err, io.EOF) || n != 15 {
		t.Fatalf("read: %v after %d frames", err, n)
	}
	a, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(a), "audio="+devs[0].ID) || !strings.Contains(string(a), "pipe:1") {
		t.Fatalf("ffmpeg args:\n%s", a)
	}

	// Open failures are explained.
	for msg, kind := range map[string]Kind{
		`[dshow @ 01] Could not find audio only device with name [X] among source devices of type audio.`:     KindNoMic,
		`[dshow @ 01] Could not run graph (sometimes caused by a device already in use by other application)`: KindNoMic,
		`[AVFoundation indev @ 0x1] Failed to create AV capture input device: not authorized`:                 KindPermission,
	} {
		t.Setenv(fakeFailEnv, msg)
		c, err := startCapture(bg, ff, devs[0])
		if err != nil {
			t.Fatal(err)
		}
		err = c.read(func([]int16) bool { return true })
		if KindOf(err) != kind {
			t.Errorf("%q → %v (kind %d)", msg, err, KindOf(err))
		}
	}
	if _, err := ListDevices(bg, ""); KindOf(err) != KindNoFFmpeg {
		t.Fatalf("no ffmpeg: %v", err)
	}
}

func TestConfigNormalize(t *testing.T) {
	c := config.Config{Dictation: config.Dictation{Provider: " Deepgram ", Language: "AUTO", SilenceSec: 1, ChunksURL: "https://x/v1/ "}}
	c.Normalize()
	d := c.Dictation
	if d.Provider != Deepgram || d.Language != "" || d.SilenceSec != 3 || d.ChunksURL != "https://x/v1" {
		t.Fatalf("normalized %+v", d)
	}
	c.Dictation.Provider, c.Dictation.SilenceSec = "nope", 600
	c.Normalize()
	if c.Dictation.Provider != "" || c.Dictation.SilenceSec != 60 {
		t.Fatalf("clamped %+v", c.Dictation)
	}
}

// TestRealDeviceList lists this computer's microphones through the real
// FFmpeg on PATH (opt-in: YTC_REAL_MIC_LIST=1). It only lists — nothing is
// recorded.
func TestRealDeviceList(t *testing.T) {
	if os.Getenv("YTC_REAL_MIC_LIST") == "" {
		t.Skip("YTC_REAL_MIC_LIST=1 lists the real microphones")
	}
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	devs, err := ListDevices(bg, ff)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := pickDevice(devs, "")
	t.Logf("%d microphone(s): %+v; default → %q; capture args: %q", len(devs), devs, d.Name, inputArgs(d))
}
