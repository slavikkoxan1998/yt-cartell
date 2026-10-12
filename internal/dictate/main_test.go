package dictate

// Test harness: no real network (loopback mocks only), no microphone. The
// test binary doubles as FFmpeg (YTC_FAKE_FFMPEG: device lists, a PCM file
// streamed to stdout, open failures) and as whisper-server
// (YTC_FAKE_WHISPER_SERVER: /health and /inference on --port).

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	fakeFFmpegEnv = "YTC_FAKE_FFMPEG"         // list | stream | fail
	fakeListEnv   = "YTC_FAKE_LIST"           // the listing to print
	fakePCMEnv    = "YTC_FAKE_PCM"            // PCM file to stream
	fakeHoldEnv   = "YTC_FAKE_HOLD"           // keep streaming silence after the file
	fakeFailEnv   = "YTC_FAKE_FAIL"           // stderr text of a failed open
	fakeArgsEnv   = "YTC_FAKE_ARGS"           // file to write the arguments to
	fakeServerEnv = "YTC_FAKE_WHISPER_SERVER" // reply text of the fake whisper-server
	fakeCLIEnv    = "YTC_FAKE_WHISPER_CLI"    // reply text of the fake whisper-cli
)

func init() {
	if mode := os.Getenv(fakeFFmpegEnv); mode != "" {
		os.Exit(fakeFFmpeg(mode))
	}
	if reply := os.Getenv(fakeServerEnv); reply != "" {
		os.Exit(fakeWhisperServer(reply))
	}
	if reply := os.Getenv(fakeCLIEnv); reply != "" {
		os.Exit(fakeWhisperCLI(reply))
	}
}

func fakeFFmpeg(mode string) int {
	if p := os.Getenv(fakeArgsEnv); p != "" {
		_ = os.WriteFile(p, []byte(strings.Join(os.Args[1:], "\n")), 0o600)
	}
	args := strings.Join(os.Args[1:], " ")
	if strings.Contains(args, "-list_devices") || strings.Contains(args, "-sources") {
		fmt.Fprint(os.Stderr, os.Getenv(fakeListEnv))
		fmt.Fprintln(os.Stderr, "Error opening input file dummy.")
		return 1
	}
	if msg := os.Getenv(fakeFailEnv); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		return 1
	}
	b, err := os.ReadFile(os.Getenv(fakePCMEnv))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	frame := FrameSize * 2
	for i := 0; i < len(b); i += frame {
		end := min(i+frame, len(b))
		if _, err := os.Stdout.Write(b[i:end]); err != nil {
			return 0
		}
		time.Sleep(200 * time.Microsecond)
	}
	if os.Getenv(fakeHoldEnv) != "" {
		quiet := pcmBytes(make([]int16, FrameSize))
		for {
			if _, err := os.Stdout.Write(quiet); err != nil {
				return 0
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	return 0
}

func fakeWhisperServer(reply string) int {
	port := ""
	for i, a := range os.Args {
		if a == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	mux.HandleFunc("/inference", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, `{"error":"bad form"}`, 400)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":"no 'file' field in the request"}`, 400)
			return
		}
		f.Close()
		lang := r.FormValue("language")
		if r.FormValue("response_format") == "verbose_json" {
			fmt.Fprintf(w, `{"task":"transcribe","language":"ukrainian","text":%q}`, reply+" "+lang)
			return
		}
		fmt.Fprintf(w, `{"text":%q}`, reply+" "+lang)
	})
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bind:", err)
		return 1
	}
	_ = http.Serve(l, mux)
	return 0
}

// guard lets requests reach loopback mock servers only.
type guard struct{ base http.RoundTripper }

func (g guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if ip := net.ParseIP(r.URL.Hostname()); ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("test guard: blocked request to %s", r.URL.Host)
	}
	return g.base.RoundTrip(r)
}

func TestMain(m *testing.M) {
	http.DefaultTransport = guard{base: http.DefaultTransport}
	httpClient = &http.Client{Transport: http.DefaultTransport}
	wsTransport = guard{base: newWSTransport()}
	// Never the real services, even if a test forgets to point a plan at a mock.
	elevenWS, openaiBase, deepgramWS, geminiBase = "ws://127.0.0.1:9/eleven", "http://127.0.0.1:9/v1", "ws://127.0.0.1:9/dg", "http://127.0.0.1:9/gemini"
	assemblyWS, assemblyWSEU = "ws://127.0.0.1:9/aai", "ws://127.0.0.1:9/aai-eu"
	os.Exit(m.Run())
}

// ---- synthetic audio --------------------------------------------------------------------------

// tone is speech-like audio: a 220 Hz tone at about −18 dBFS in 240 ms
// "syllables" with 60 ms dips to the room noise between them.
func tone(sec float64) []int16 {
	n := int(sec * SampleRate)
	out := make([]int16, n)
	for i := range out {
		amp := 6000.0
		if i%(SampleRate*3/10) >= SampleRate*24/100 {
			amp = 30
		}
		out[i] = int16(amp * math.Sin(2*math.Pi*220*float64(i)/SampleRate))
	}
	return out
}

// hush is room noise at about −60 dBFS (never digital zeros).
func hush(sec float64) []int16 {
	n := int(sec * SampleRate)
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(30 * math.Sin(float64(i)*1.7))
	}
	return out
}

func cat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func writePCM(t *testing.T, pcm []int16) string {
	t.Helper()
	b := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	p := filepath.Join(t.TempDir(), "mic.pcm")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const dshowList = `[dshow @ 000001] "OBS Virtual Camera" (none)
[dshow @ 000001]   Alternative name "@device_sw_{860BB310-5D01-11D0-BD3B-00A0C911CE86}\{A3FCE0F5}"
[dshow @ 000001] "Microphone (High Definition Audio Device)" (audio)
[dshow @ 000001]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{83C49780}"
[dshow @ 000001] "CABLE Output (VB-Audio Virtual Cable)" (audio)
[dshow @ 000001]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{E58CECA0}"
`

// fakeMic makes the test binary the FFmpeg of a session: it lists dshow
// devices and streams pcm. It returns the "ffmpeg" path and the file its
// arguments are written to.
func fakeMic(t *testing.T, pcm []int16, hold bool) (string, string) {
	t.Helper()
	args := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv(fakeFFmpegEnv, "stream")
	t.Setenv(fakeListEnv, dshowList)
	t.Setenv(fakePCMEnv, writePCM(t, pcm))
	t.Setenv(fakeArgsEnv, args)
	if hold {
		t.Setenv(fakeHoldEnv, "1")
	}
	old := goos
	goos = "windows"
	t.Cleanup(func() { goos = old })
	return os.Args[0], args
}

// waitDone waits for a session to end.
func waitDone(t *testing.T, s *Session, d time.Duration) State {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(d):
		s.Cancel()
		t.Fatalf("the session did not end: %+v", s.State())
	}
	return s.State()
}

// takeAll drains a session's text.
func takeAll(s *Session) (finals []string, interim string) {
	f, i, _ := s.Take()
	return f, i
}

var bg = context.Background()

// fakeWhisperCLI answers like `whisper-cli … -f phrase.wav -l <lang> -otxt -of out`:
// out.txt in the working folder.
func fakeWhisperCLI(reply string) int {
	out, lang, in := "", "", ""
	for i, a := range os.Args {
		if i+1 >= len(os.Args) {
			break
		}
		switch a {
		case "-of":
			out = os.Args[i+1]
		case "-l":
			lang = os.Args[i+1]
		case "-f":
			in = os.Args[i+1]
		}
	}
	if b, err := os.ReadFile(in); err != nil || string(b[:4]) != "RIFF" {
		fmt.Fprintln(os.Stderr, "error: failed to read audio")
		return 1
	}
	if err := os.WriteFile(out+".txt", []byte(" "+reply+" "+lang+"\n"), 0o600); err != nil {
		return 1
	}
	return 0
}
