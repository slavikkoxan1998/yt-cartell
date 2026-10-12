package dictate

// whisper.cpp on this computer (offline, free). The program and the model are
// the ones Settings → Розпізнавання мови installs or finds (stt.LocalStatus).
//
// whisper-server — in the official Windows zip next to whisper-cli and in
// Homebrew's whisper.cpp (verified 2026-10-02 against
// https://github.com/ggml-org/whisper.cpp/blob/master/examples/server/README.md
// and server.cpp) — keeps the model loaded, so a phrase takes only its
// decoding time:
//
//	whisper-server -m <model> --host 127.0.0.1 --port <free> -t <n> -l auto -nt
//	GET  /health    → 200 {"status":"ok"} (the port opens after the model loaded)
//	POST /inference multipart: file (16 kHz WAV, decoded in memory) ·
//	     response_format=json|verbose_json · language (per request) · prompt ·
//	     temperature · no_timestamps · suppress_nst
//	   ← {"text":"…"} · verbose_json adds "language":"ukrainian"
//
// One server serves every dictation of an app run and exits two minutes
// after the last one (the next dictation starts warm); it runs in the app's
// kill-on-close job. Without whisper-server each phrase runs whisper-cli
// (-nt -np -otxt; the model loads every time) and there is no interim text.
// Paths: whisper.cpp opens files in the ANSI code page on Windows, so a model
// under a Cyrillic user folder is hard-linked into the work folder and named
// relative to it (as package stt does).

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/sysx"
)

type localT struct {
	p    Plan
	cli  bool
	srv  *whisperServer
	work string // CLI mode
}

func newLocalRec(p Plan) recognizer {
	t := &localT{p: p}
	mode := chunkLocal
	if serverBin(p.Bin) == "" {
		t.cli, mode = true, chunkLocalCLI
	}
	return newChunkRec(t, mode, p.Language)
}

// serverBin is whisper-server next to whisper-cli ("" = none).
func serverBin(cli string) string {
	if cli == "" {
		return ""
	}
	name := "whisper-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(cli), name)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return ""
}

func (t *localT) start(ctx context.Context) error {
	if t.cli {
		w, err := os.MkdirTemp("", "ytc-dictate-")
		if err != nil {
			return newErr(KindOther, err, "Не вдалося створити тимчасову папку для whisper.cpp")
		}
		t.work = w
		return nil
	}
	s, err := acquireServer(ctx, serverBin(t.p.Bin), t.p.ModelPath)
	if err != nil {
		return err
	}
	t.srv = s
	return nil
}

func (t *localT) close() {
	if t.srv != nil {
		t.srv.release()
		t.srv = nil
	}
	if t.work != "" {
		os.RemoveAll(t.work)
		t.work = ""
	}
}

func (t *localT) transcribe(ctx context.Context, pcm []int16, lang, prompt string) (string, string, error) {
	if t.cli {
		text, err := t.runCLI(ctx, pcm, lang, prompt)
		return text, "", err
	}
	return t.srv.inference(ctx, pcm, whisperLang(lang), prompt, lang == "")
}

// ---- the server -------------------------------------------------------------------------------

type whisperServer struct {
	key    string
	base   string
	cmd    *exec.Cmd
	work   string
	log    *tailBuf
	exited chan struct{}
	ready  chan struct{}
	err    error // after ready closed
	users  int
	idle   *time.Timer
}

var (
	srvMu      sync.Mutex
	server     *whisperServer
	serverIdle = 2 * time.Minute
	readyLimit = 90 * time.Second // a large model on a slow disk
)

// acquireServer returns the running server for bin+model or starts one, and
// waits until it answers.
func acquireServer(ctx context.Context, bin, model string) (*whisperServer, error) {
	key := bin + "|" + model
	srvMu.Lock()
	s := server
	if s != nil && (s.key != key || s.dead()) {
		if s.users == 0 {
			s.kill()
		}
		server, s = nil, nil
	}
	if s == nil {
		var err error
		s, err = launchServer(bin, model)
		if err != nil {
			srvMu.Unlock()
			return nil, err
		}
		server = s
	}
	s.users++
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	srvMu.Unlock()
	select {
	case <-s.ready:
	case <-ctx.Done():
		s.release()
		return nil, ctx.Err()
	}
	if s.err != nil {
		s.release()
		return nil, s.err
	}
	return s, nil
}

func launchServer(bin, model string) (*whisperServer, error) {
	work, err := os.MkdirTemp("", "ytc-whisper-server-")
	if err != nil {
		return nil, newErr(KindOther, err, "Не вдалося створити тимчасову папку для whisper.cpp")
	}
	port, err := freePort()
	if err != nil {
		os.RemoveAll(work)
		return nil, newErr(KindOther, err, "Не вдалося знайти вільний порт для whisper.cpp")
	}
	args := []string{"-m", modelArg(model, work), "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-t", strconv.Itoa(whisperThreads()), "-l", "auto", "-nt"}
	cmd := sysx.Command(context.Background(), bin, args...)
	cmd.Dir = work
	lg := &tailBuf{max: 16 << 10}
	cmd.Stdout, cmd.Stderr = lg, lg
	if err := sysx.Start(cmd); err != nil {
		os.RemoveAll(work)
		return nil, newErr(KindNotInstalled, err, "Не вдалося запустити whisper-server: %s", clip(err.Error(), 140))
	}
	s := &whisperServer{key: bin + "|" + model, base: "http://127.0.0.1:" + strconv.Itoa(port), cmd: cmd, work: work,
		log: lg, exited: make(chan struct{}), ready: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.exited)
		os.RemoveAll(work)
	}()
	go s.waitReady()
	return s, nil
}

func (s *whisperServer) waitReady() {
	defer close(s.ready)
	deadline := time.Now().Add(readyLimit)
	hc := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-s.exited:
			s.err = localErr(s.log.text())
			return
		default:
		}
		if res, err := hc.Get(s.base + "/health"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	s.kill()
	s.err = newErr(KindService, nil, "whisper.cpp не запустився за %d с — можливо, модель завелика для цього комп'ютера (вибери меншу в Налаштуваннях)", int(readyLimit/time.Second))
}

func (s *whisperServer) dead() bool {
	select {
	case <-s.exited:
		return true
	default:
		return false
	}
}

func (s *whisperServer) kill() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// release ends one use; the last one leaves the server warm for a while.
func (s *whisperServer) release() {
	srvMu.Lock()
	defer srvMu.Unlock()
	s.users--
	if s.users > 0 {
		return
	}
	s.idle = time.AfterFunc(serverIdle, func() {
		srvMu.Lock()
		defer srvMu.Unlock()
		if s.users == 0 {
			s.kill()
			if server == s {
				server = nil
			}
		}
	})
}

// inference sends one phrase. verbose asks for the detected language too.
func (s *whisperServer) inference(ctx context.Context, pcm []int16, lang, prompt string, verbose bool) (string, string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	format := "json"
	if verbose {
		format = "verbose_json"
	}
	_ = mw.WriteField("response_format", format)
	_ = mw.WriteField("temperature", "0.0")
	_ = mw.WriteField("language", lang)
	_ = mw.WriteField("no_timestamps", "true")
	_ = mw.WriteField("suppress_nst", "true")
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="audio.wav"`)
	h.Set("Content-Type", "audio/wav")
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(wav(padTo(pcm, SampleRate)))
	_ = mw.Close()
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, s.base+"/inference", &body)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	status, b, err := do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		if s.dead() {
			return "", "", localErr(s.log.text())
		}
		return "", "", newErr(KindService, err, "whisper.cpp не відповів — спробуй ще раз")
	}
	var r struct {
		Text     string `json:"text"`
		Language string `json:"language"`
		Error    string `json:"error"`
	}
	_ = json.Unmarshal(b, &r)
	if status/100 != 2 || r.Error != "" {
		return "", "", newErr(KindService, nil, "whisper.cpp не зміг розпізнати фразу: %s", clip(firstNonEmpty(r.Error, string(b)), 140))
	}
	return strings.TrimSpace(r.Text), whisperCode(r.Language), nil
}

// ---- whisper-cli per phrase ------------------------------------------------------------------

func (t *localT) runCLI(ctx context.Context, pcm []int16, lang, prompt string) (string, error) {
	in := filepath.Join(t.work, "phrase.wav")
	if err := os.WriteFile(in, wav(padTo(pcm, SampleRate)), 0o600); err != nil {
		return "", newErr(KindOther, err, "Не вдалося записати фразу для whisper.cpp")
	}
	defer os.Remove(in)
	out := filepath.Join(t.work, "out.txt")
	os.Remove(out)
	args := []string{"-m", modelArg(t.p.ModelPath, t.work), "-f", "phrase.wav", "-l", whisperLang(lang),
		"-t", strconv.Itoa(whisperThreads()), "-nt", "-np", "-otxt", "-of", "out"}
	if prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	lg := &tailBuf{max: 8 << 10}
	cmd := sysx.Command(ctx, t.p.Bin, args...)
	cmd.Dir = t.work
	cmd.Stdout, cmd.Stderr = lg, lg
	cmd.WaitDelay = 2 * time.Second
	runErr := sysx.Run(cmd)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	b, err := os.ReadFile(out)
	if err != nil {
		if runErr == nil {
			runErr = err
		}
		return "", localErr(lg.text())
	}
	return strings.TrimSpace(string(b)), nil
}

// localErr explains a whisper.cpp failure from its log.
func localErr(log string) error {
	low := strings.ToLower(log)
	switch {
	case strings.Contains(low, "failed to load model") || strings.Contains(low, "failed to open") || strings.Contains(low, "invalid model"):
		return newErr(KindNotInstalled, nil, "whisper.cpp не відкриває модель — завантаж її ще раз (Налаштування → Розпізнавання мови → «Встановити»)")
	case strings.Contains(low, "address already in use") || strings.Contains(low, "bind"):
		return newErr(KindService, nil, "whisper.cpp не зміг відкрити локальний порт — спробуй ще раз")
	}
	return newErr(KindService, nil, "whisper.cpp зупинився: %s", clip(lastLine(log), 160))
}

// ---- helpers ----------------------------------------------------------------------------------

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func whisperThreads() int { return max(1, min(8, runtime.NumCPU()-1)) }

// modelArg is the model path as whisper.cpp can open it.
func modelArg(model, work string) string {
	if runtime.GOOS != "windows" || isASCII(model) {
		return model
	}
	if os.Link(model, filepath.Join(work, "model.bin")) == nil {
		return "model.bin"
	}
	return model
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// whisperLangs maps whisper.cpp's language codes to its names (g_lang in
// src/whisper.cpp) — the names verbose_json reports.
var whisperLangs = map[string]string{
	"en": "english", "zh": "chinese", "de": "german", "es": "spanish", "ru": "russian", "ko": "korean", "fr": "french",
	"ja": "japanese", "pt": "portuguese", "tr": "turkish", "pl": "polish", "ca": "catalan", "nl": "dutch", "ar": "arabic",
	"sv": "swedish", "it": "italian", "id": "indonesian", "hi": "hindi", "fi": "finnish", "vi": "vietnamese", "he": "hebrew",
	"uk": "ukrainian", "el": "greek", "ms": "malay", "cs": "czech", "ro": "romanian", "da": "danish", "hu": "hungarian",
	"ta": "tamil", "no": "norwegian", "th": "thai", "ur": "urdu", "hr": "croatian", "bg": "bulgarian", "lt": "lithuanian",
	"la": "latin", "mi": "maori", "ml": "malayalam", "cy": "welsh", "sk": "slovak", "te": "telugu", "fa": "persian",
	"lv": "latvian", "bn": "bengali", "sr": "serbian", "az": "azerbaijani", "sl": "slovenian", "kn": "kannada", "et": "estonian",
	"mk": "macedonian", "br": "breton", "eu": "basque", "is": "icelandic", "hy": "armenian", "ne": "nepali", "mn": "mongolian",
	"bs": "bosnian", "kk": "kazakh", "sq": "albanian", "sw": "swahili", "gl": "galician", "mr": "marathi", "pa": "punjabi",
	"si": "sinhala", "km": "khmer", "sn": "shona", "yo": "yoruba", "so": "somali", "af": "afrikaans", "oc": "occitan",
	"ka": "georgian", "be": "belarusian", "tg": "tajik", "sd": "sindhi", "gu": "gujarati", "am": "amharic", "yi": "yiddish",
	"lo": "lao", "uz": "uzbek", "fo": "faroese", "ht": "haitian creole", "ps": "pashto", "tk": "turkmen", "nn": "nynorsk",
	"mt": "maltese", "sa": "sanskrit", "lb": "luxembourgish", "my": "myanmar", "bo": "tibetan", "tl": "tagalog",
	"mg": "malagasy", "as": "assamese", "tt": "tatar", "haw": "hawaiian", "ln": "lingala", "ha": "hausa", "ba": "bashkir",
	"jw": "javanese", "su": "sundanese", "yue": "cantonese",
}

// whisperLang maps a catalogue code to whisper.cpp's ("auto" when empty or
// unknown to whisper).
func whisperLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch c {
	case "":
		return "auto"
	case "jv":
		c = "jw"
	case "fil":
		c = "tl"
	}
	if _, ok := whisperLangs[c]; ok {
		return c
	}
	return "auto"
}

// whisperCode maps a language whisper.cpp reports (a name or a code) back to
// the catalogue code ("" = unknown).
func whisperCode(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || v == "auto" {
		return ""
	}
	for code, name := range whisperLangs {
		if v == code || v == name {
			switch code {
			case "jw":
				return "jv"
			case "tl":
				return "fil"
			}
			return code
		}
	}
	return ""
}

// Shutdown stops the warm whisper-server (the app is closing; outside
// Windows no kill-on-close job would end it). A running capture ends by
// itself: FFmpeg dies on its next write into the closed pipe.
func Shutdown() {
	srvMu.Lock()
	defer srvMu.Unlock()
	if server != nil {
		server.kill()
		server = nil
	}
}
