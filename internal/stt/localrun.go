package stt

// Running whisper.cpp: the recording becomes a 16 kHz mono WAV (FFmpeg), then
//
//	whisper-cli -m <model> -f audio.wav -l <lang|auto> -t <n> -oj -of out -ml 1 -sow -pp [-sns]
//
// runs in a temp folder. -oj writes out.json:
//
//	{"result": {"language": "uk"},
//	 "transcription": [{"timestamps": {"from": "00:00:00,000", "to": "00:00:00,620"},
//	                    "offsets": {"from": 0, "to": 620}, "text": "Привіт,"}, …]}
//
// -ml 1 -sow (max segment length 1, split on word) makes every item one word
// with its punctuation and millisecond offsets (whisper.cpp splits before
// each token that starts with a space, so multi-byte letters stay whole);
// -pp prints "progress = N%" lines to stderr; -sns suppresses non-speech
// tokens like "[Music]" (the OpenAI API does that by default). Optional
// flags are passed only when `--help` lists them.
//
// Paths: whisper.cpp opens files with the ANSI code page on Windows, so a
// Cyrillic user name in a path would break it. The audio and the output are
// given relative to the working folder, and a model path with non-ASCII
// characters is hard-linked into it (or shortened to its 8.3 form).
//
// The program exits with 0 even after "error: unknown argument" (it prints the
// usage), so success is the JSON file, not the exit code.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
	"ytcartell/internal/sysx"
	"ytcartell/internal/tts"
)

// Tunables and seams (package vars so tests can shorten or replace them).
var (
	// locateFFmpeg finds ffmpeg the way the app does ("" = none).
	locateFFmpeg = func(cfg config.Config) string {
		ff, _ := media.Locate(cfg)
		return ff.FFmpeg
	}
	probeTimeout = 20 * time.Second
	// localSlot lets one whisper.cpp run at a time: each one takes every CPU
	// core and up to a few GB of memory.
	localSlot = make(chan struct{}, 1)
)

// transcribeLocal recognises the recording at path with whisper.cpp.
func (c *Client) transcribeLocal(ctx context.Context, path, lang string, progress func(int, string)) (Transcript, error) {
	cfg := c.get()
	bin, model, ok, hint := LocalStatus(cfg)
	if !ok {
		return Transcript{}, errors.New(hint)
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return Transcript{}, fmt.Errorf("Файл запису не знайдено: %s", filepath.Base(path))
	}
	if info.Size() == 0 {
		return Transcript{}, errors.New("Файл запису порожній")
	}
	rep := newReporter(progress)
	select {
	case localSlot <- struct{}{}:
	default:
		rep.force(0, "Чекаємо, поки завершиться інше розпізнавання…")
		select {
		case localSlot <- struct{}{}:
		case <-ctx.Done():
			return Transcript{}, ctx.Err()
		}
	}
	defer func() { <-localSlot }()

	wi, err := probeWhisper(ctx, bin, false)
	if err != nil {
		return Transcript{}, err
	}
	rep.force(0, "Готуємо запис для whisper.cpp…")
	work, err := os.MkdirTemp("", "ytc-whisper-")
	if err != nil {
		return Transcript{}, fmt.Errorf("не вдалося створити тимчасову папку: %w", err)
	}
	defer os.RemoveAll(work)
	audio, dur, err := prepareAudio(ctx, cfg, path, work)
	if err != nil {
		return Transcript{}, err
	}

	wlang := whisperLang(lang)
	marg := modelArg(model, work)
	args := []string{"-m", marg, "-f", audio, "-l", wlang,
		"-t", strconv.Itoa(whisperThreads()), "-oj", "-of", "out"}
	if wi.words {
		args = append(args, "-ml", "1", "-sow")
	}
	if wi.progress {
		args = append(args, "-pp")
	}
	if wi.suppressNST {
		args = append(args, "-sns")
	}
	rep.force(1, "Розпізнаємо мову на цьому комп'ютері…")
	stderr := &whisperLog{tail: tailBuf{max: 8 << 10}, onPct: func(p int) {
		rep.report(min(99, max(1, p)), fmt.Sprintf("Розпізнаємо мову на цьому комп'ютері · %d%%", p))
	}}
	stdout := &tailBuf{max: 4 << 10}
	cmd := whisperCmd(ctx, bin, args...)
	cmd.Dir = work
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = 3 * time.Second
	runErr := sysx.Run(cmd)
	if ctx.Err() != nil {
		return Transcript{}, ctx.Err()
	}
	out, readErr := os.ReadFile(filepath.Join(work, "out.json"))
	if runErr != nil || readErr != nil || len(bytes.TrimSpace(out)) == 0 {
		err := whisperErr(runErr, stderr.text()+"\n"+stdout.text())
		if errors.Is(err, errModelLoad) && runtime.GOOS == "windows" && !isASCII(marg) {
			// Neither a hard link nor an 8.3 name made the path ASCII.
			return Transcript{}, fmt.Errorf("whisper.cpp не відкриває модель, бо в шляху до неї є не латинські літери (%s) — перенеси файл моделі в папку з латинською назвою (наприклад, C:\\whisper) і вкажи його в Налаштуваннях", model)
		}
		return Transcript{}, err
	}
	if len(out) > maxReply {
		return Transcript{}, errors.New("whisper.cpp повернув завеликий результат")
	}
	tr, err := parseWhisper(out, wi.words)
	if err != nil {
		return Transcript{}, err
	}
	if tr.Language == "" {
		tr.Language = stderr.detected()
	}
	if tr.Language == "" && wlang != "auto" {
		tr.Language = wlang
	}
	tr.Language = fromWhisperLang(tr.Language)
	if dur > 0 {
		tr.Duration = dur
	}
	tr.finish()
	rep.force(100, fmt.Sprintf("Мову розпізнано · %d %s", len(tr.Words), wordsWord(len(tr.Words))))
	return tr, nil
}

// ---- the recording ----------------------------------------------------------------------------

// whisperReads are the formats whisper-cli decodes itself (miniaudio):
// used as is only when FFmpeg is missing.
var whisperReads = map[string]bool{".wav": true, ".mp3": true, ".flac": true, ".ogg": true}

// prepareAudio puts the recording into work as a 16 kHz mono 16-bit WAV
// (FFmpeg) and returns its file name and duration. Without FFmpeg a WAV,
// MP3, FLAC or OGG file is linked or copied in as it is.
func prepareAudio(ctx context.Context, cfg config.Config, src, work string) (string, float64, error) {
	if ff := locateFFmpeg(cfg); ff != "" {
		out := filepath.Join(work, "audio.wav")
		err := media.FF{FFmpeg: ff}.Run(ctx, "", "-i", src, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", out)
		if err != nil {
			if ctx.Err() != nil {
				return "", 0, ctx.Err()
			}
			return "", 0, fmt.Errorf("FFmpeg не зміг підготувати запис для розпізнавання: %s", clip(err.Error(), 300))
		}
		return "audio.wav", wavDuration(out), nil
	}
	ext := strings.ToLower(filepath.Ext(src))
	if !whisperReads[ext] {
		return "", 0, errors.New("Для розпізнавання на цьому комп'ютері потрібен FFmpeg — встанови його або вкажи шлях у Налаштуваннях (або завантаж запис у WAV чи MP3)")
	}
	name := "audio" + ext
	if err := linkOrCopy(src, filepath.Join(work, name)); err != nil {
		return "", 0, fmt.Errorf("не вдалося підготувати запис: %w", err)
	}
	dur := 0.0
	if ext == ".wav" {
		dur = wavDuration(filepath.Join(work, name))
	}
	return name, dur, nil
}

func linkOrCopy(src, dst string) error {
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	return cerr
}

// wavDuration reads the length of a PCM WAV file from its header (0 when
// unknown).
func wavDuration(path string) float64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return 0
	}
	var rate, channels, bits uint32
	pos := int64(12)
	for {
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			return 0
		}
		id, size := string(ch[0:4]), int64(binary.LittleEndian.Uint32(ch[4:8]))
		pos += 8
		switch id {
		case "fmt ":
			var b [16]byte
			if size < 16 {
				return 0
			}
			if _, err := io.ReadFull(f, b[:]); err != nil {
				return 0
			}
			channels = uint32(binary.LittleEndian.Uint16(b[2:4]))
			rate = binary.LittleEndian.Uint32(b[4:8])
			bits = uint32(binary.LittleEndian.Uint16(b[14:16]))
			size -= 16
			pos += 16
		case "data":
			if size == 0 || size == 0xFFFFFFFF || pos+size > st.Size() {
				size = st.Size() - pos // streamed header: the rest of the file
			}
			per := float64(rate) * float64(channels) * float64(bits) / 8
			if per <= 0 || size <= 0 {
				return 0
			}
			return float64(size) / per
		}
		skip := size + size%2 // chunks are word-aligned
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return 0
		}
		pos += skip
	}
}

// ---- arguments --------------------------------------------------------------------------------

// whisperLangCodes are the languages whisper.cpp knows (g_lang in
// src/whisper.cpp); any other code would stop it with "unknown language".
var whisperLangCodes = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range strings.Fields(`en zh de es ru ko fr ja pt tr pl ca nl ar sv it id hi fi vi he uk el ms cs ro da hu ta
		no th ur hr bg lt la mi ml cy sk te fa lv bn sr az sl kn et mk br eu is hy ne mn bs kk sq sw gl mr pa si km sn
		yo so af oc ka be tg sd gu am yi lo uz fo ht ps tk nn mt sa lb my bo tl mg as tt haw ln ha ba jw su yue`) {
		m[c] = true
	}
	return m
}()

// whisperLang maps a catalogue language code to whisper.cpp's ("auto" when
// it is empty or unknown to whisper).
func whisperLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return "auto"
	}
	if n := tts.NormalizeLanguage(c); n != "" {
		c = n
	}
	switch c {
	case "jv":
		c = "jw"
	case "fil":
		c = "tl"
	}
	if whisperLangCodes[c] {
		return c
	}
	return "auto"
}

// fromWhisperLang maps whisper's own codes back to the catalogue's.
func fromWhisperLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch c {
	case "auto":
		return ""
	case "jw":
		return "jv"
	}
	return c
}

// whisperThreads leaves one core to the app, at most 8 (whisper.cpp gains
// little beyond that on a CPU).
func whisperThreads() int { return max(1, min(8, runtime.NumCPU()-1)) }

// modelArg is the model path as whisper.cpp can open it (see the file comment).
func modelArg(model, work string) string {
	if runtime.GOOS != "windows" || isASCII(model) {
		return model
	}
	if os.Link(model, filepath.Join(work, "model.bin")) == nil {
		return "model.bin"
	}
	if sp := shortPath(model); sp != "" && isASCII(sp) {
		return sp
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

// ---- the program's abilities ------------------------------------------------------------------

// whisperInfo is what `whisper-cli --help` says about the program.
type whisperInfo struct {
	words       bool   // -ml and -sow: one item per word
	progress    bool   // -pp
	suppressNST bool   // -sns
	version     string // "1.9.4" when --version is there
}

var (
	probeMu    sync.Mutex
	probeCache = map[string]whisperInfo{} // key: path|size|mtime
)

// probeWhisper runs `bin --help` (and `--version` when listed) — never a
// transcription. fresh skips the cache (the service check).
func probeWhisper(ctx context.Context, bin string, fresh bool) (whisperInfo, error) {
	st, err := os.Stat(bin)
	if err != nil || st.IsDir() {
		return whisperInfo{}, fmt.Errorf("Програму whisper.cpp не знайдено: %s", bin)
	}
	key := fmt.Sprintf("%s|%d|%d", bin, st.Size(), st.ModTime().UnixNano())
	if !fresh {
		probeMu.Lock()
		wi, ok := probeCache[key]
		probeMu.Unlock()
		if ok {
			return wi, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := runQuick(ctx, bin, "--help")
	text := string(out)
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "deprecated"):
		return whisperInfo{}, fmt.Errorf("%s — застаріла програма whisper.cpp, яка нічого не розпізнає: потрібна whisper-cli (вона лежить поруч або натисни «Встановити»)", filepath.Base(bin))
	case ctx.Err() != nil && !strings.Contains(low, "usage"):
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return whisperInfo{}, errors.New("whisper.cpp не відповідає — перевір програму у Налаштуваннях або встанови її ще раз")
		}
		return whisperInfo{}, ctx.Err()
	case !strings.Contains(low, "usage"):
		if err != nil {
			return whisperInfo{}, whisperErr(err, text)
		}
		return whisperInfo{}, fmt.Errorf("%s не схожа на whisper.cpp (немає довідки whisper-cli) — вкажи програму whisper-cli", filepath.Base(bin))
	case !strings.Contains(text, "--output-json"):
		return whisperInfo{}, errors.New("Ця версія whisper.cpp застаріла — онови її («Встановити» або brew upgrade whisper-cpp)")
	}
	wi := whisperInfo{
		words:       strings.Contains(text, "--max-len") && strings.Contains(text, "--split-on-word"),
		progress:    strings.Contains(text, "--print-progress"),
		suppressNST: strings.Contains(text, "--suppress-nst"),
	}
	if strings.Contains(text, "--version") {
		if v, err := runQuick(ctx, bin, "--version"); err == nil {
			wi.version = versionRe.FindString(string(v))
		}
	}
	probeMu.Lock()
	probeCache[key] = wi
	probeMu.Unlock()
	return wi, nil
}

var versionRe = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// whisperCmd creates a command configured with LD_LIBRARY_PATH so dynamic shared
// libraries in the binary's directory are always loaded on Linux.
func whisperCmd(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := sysx.Command(ctx, bin, args...)
	if runtime.GOOS == "linux" {
		binDir := filepath.Dir(bin)
		ldPath := os.Getenv("LD_LIBRARY_PATH")
		if ldPath != "" {
			ldPath = binDir + ":" + ldPath
		} else {
			ldPath = binDir
		}
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+ldPath)
	}
	return cmd
}

// runQuick runs bin with args and returns stdout and stderr together.
func runQuick(ctx context.Context, bin string, args ...string) ([]byte, error) {
	buf := &tailBuf{max: 64 << 10}
	cmd := whisperCmd(ctx, bin, args...)
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.WaitDelay = 2 * time.Second
	err := sysx.Run(cmd)
	return []byte(buf.text()), err
}

// ---- output -----------------------------------------------------------------------------------

type whisperSpan struct {
	From *float64 `json:"from"`
	To   *float64 `json:"to"`
}

// whisperJSON is whisper-cli's -oj file (output_json in examples/cli/cli.cpp).
type whisperJSON struct {
	Params struct {
		Language string `json:"language"`
	} `json:"params"`
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Timestamps struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"timestamps"`
		Offsets whisperSpan `json:"offsets"` // milliseconds
		Text    string      `json:"text"`
	} `json:"transcription"`
}

// parseWhisper reads the -oj file: words (one item per word with -ml 1 -sow)
// or phrase segments.
func parseWhisper(b []byte, words bool) (Transcript, error) {
	// whisper.cpp escapes only quotes and backslashes: raw control characters
	// would make the JSON invalid (as whitespace they change nothing).
	b = bytes.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, b)
	var r whisperJSON
	if err := json.Unmarshal(b, &r); err != nil {
		return Transcript{}, fmt.Errorf("whisper.cpp повернув незрозумілий результат: %s", clip(string(b), 200))
	}
	tr := Transcript{Language: r.Result.Language}
	if tr.Language == "" && !strings.EqualFold(r.Params.Language, "auto") {
		tr.Language = r.Params.Language
	}
	for _, it := range r.Transcription {
		text := strings.TrimSpace(it.Text)
		if text == "" {
			continue
		}
		from, to := msOr(it.Offsets.From, it.Timestamps.From), msOr(it.Offsets.To, it.Timestamps.To)
		if !words {
			if !isAnnotation(text) {
				tr.Segments = append(tr.Segments, Segment{Text: text, Start: from, End: to})
			}
			continue
		}
		// One word per item; a script without spaces may still give a phrase.
		if f := strings.Fields(text); len(f) > 1 {
			tr.Words = append(tr.Words, wordsFromSegments([]Segment{{Text: text, Start: from, End: to}})...)
			continue
		}
		tr.Words = append(tr.Words, Word{Text: text, Start: from, End: to})
	}
	tr.Words = dropAnnotations(tr.Words)
	return tr, nil
}

// msOr is a millisecond offset in seconds, else the "HH:MM:SS,mmm" stamp
// (NaN when neither is usable: finish fills it in).
func msOr(ms *float64, stamp string) float64 {
	if ms != nil && finite(*ms) && *ms >= 0 {
		return *ms / 1000
	}
	if m := stampRe.FindStringSubmatch(stamp); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		s, _ := strconv.Atoi(m[3])
		frac, _ := strconv.Atoi((m[4] + "000")[:3])
		return float64(h*3600+mi*60+s) + float64(frac)/1000
	}
	return math.NaN()
}

var stampRe = regexp.MustCompile(`^(\d+):(\d{2}):(\d{2})[,.](\d{1,3})`)

// isAnnotation: a caption of a sound, not speech — "[BLANK_AUDIO]",
// "[Музика]", "(сміх)", "*оплески*", "♪".
func isAnnotation(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	r := []rune(t)
	first, last := r[0], r[len(r)-1]
	if len(r) > 1 && (first == '[' && last == ']' || first == '(' && last == ')' || first == '*' && last == '*') {
		return true
	}
	return strings.Trim(t, "♪♫♬♩ ") == ""
}

// dropAnnotations removes sound captions from a word list: whole words and
// bracketed runs ("[", "Музика", "]") — whisper builds without -sns write them.
func dropAnnotations(words []Word) []Word {
	out := words[:0]
	closer := rune(0)
	for _, w := range words {
		t := strings.TrimSpace(w.Text)
		if closer != 0 {
			if strings.ContainsRune(t, closer) {
				closer = 0
			}
			continue
		}
		if t == "" || isAnnotation(t) {
			continue
		}
		switch r := []rune(t); {
		case r[0] == '[' && !strings.ContainsRune(t, ']'):
			closer = ']'
			continue
		case r[0] == '(' && !strings.ContainsRune(t, ')'):
			closer = ')'
			continue
		}
		out = append(out, w)
	}
	return out
}

// ---- stderr -----------------------------------------------------------------------------------

// whisperLog watches whisper-cli's stderr: progress lines, the detected
// language, and the tail for error messages.
type whisperLog struct {
	mu    sync.Mutex
	line  []byte
	tail  tailBuf
	lang  string
	onPct func(int)
}

var (
	progressRe = regexp.MustCompile(`progress\s*=\s*(\d+)\s*%`)
	detectedRe = regexp.MustCompile(`auto-detected language:\s*([a-z]{2,3})\b`)
)

func (w *whisperLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tail.Write(p)
	for _, c := range p {
		if c == '\n' || c == '\r' {
			w.flush()
			continue
		}
		if len(w.line) < 4096 {
			w.line = append(w.line, c)
		}
	}
	return len(p), nil
}

func (w *whisperLog) flush() {
	line := string(w.line)
	w.line = w.line[:0]
	if m := progressRe.FindStringSubmatch(line); m != nil && w.onPct != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			w.onPct(min(100, n))
		}
		return
	}
	if m := detectedRe.FindStringSubmatch(line); m != nil {
		w.lang = m[1]
	}
}

func (w *whisperLog) detected() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lang
}

func (w *whisperLog) text() string { return w.tail.text() }

// tailBuf keeps the last max bytes written to it.
type tailBuf struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	if t.max <= 0 {
		t.max = 8 << 10
	}
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	t.mu.Unlock()
	return len(p), nil
}

func (t *tailBuf) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.b), "")
}

// ---- errors -----------------------------------------------------------------------------------

// Windows NTSTATUS codes of a program that cannot start.
const (
	statusDLLNotFound   = 0xC0000135
	statusEntryNotFound = 0xC0000139
	statusBadImage      = 0xC000007B
)

// errModelLoad: whisper.cpp could not load the model file.
var errModelLoad = errors.New("Модель whisper.cpp не відкривається — завантаж її ще раз (Налаштування → Розпізнавання мови → «Встановити»)")

// whisperErr turns a failed run into a plain Ukrainian message (log is
// whisper's stderr/stdout tail).
func whisperErr(err error, log string) error {
	low := strings.ToLower(log)
	var ee *exec.ExitError
	code := 0
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	switch uint32(code) {
	case statusDLLNotFound, statusEntryNotFound, statusBadImage:
		return errors.New("whisper.cpp не запускається: у Windows бракує бібліотек Microsoft Visual C++. Встанови «Visual C++ Redistributable» з сайту Microsoft (aka.ms/vs/17/release/vc_redist.x64.exe) і спробуй ще раз")
	}
	switch {
	case strings.Contains(low, "unknown argument"):
		return errors.New("Ця версія whisper.cpp застаріла — онови її («Встановити» або brew upgrade whisper-cpp)")
	case strings.Contains(low, "unknown language"):
		return errors.New("whisper.cpp не знає мови запису — вибери іншу мову проєкту або «як у тексті»")
	case strings.Contains(low, "failed to read audio") || strings.Contains(low, "failed to open") && strings.Contains(low, "audio") ||
		strings.Contains(low, "input file not found"):
		return errors.New("whisper.cpp не зміг прочитати запис — перевір файл озвучки")
	case strings.Contains(low, "bad magic") || strings.Contains(low, "invalid model") || strings.Contains(low, "failed to load model") ||
		strings.Contains(low, "failed to initialize whisper context"):
		return errModelLoad
	case strings.Contains(low, "bad_alloc") || strings.Contains(low, "out of memory") || strings.Contains(low, "failed to allocate"):
		return errors.New("Не вистачило пам'яті для моделі розпізнавання — вибери меншу модель (наприклад, small)")
	}
	tail := lastLines(log, 3)
	if err == nil {
		return fmt.Errorf("whisper.cpp не створив результат: %s", clip(tail, 300))
	}
	var pe *os.PathError
	if errors.As(err, &pe) || errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("whisper.cpp не запускається: %s", clip(err.Error(), 200))
	}
	return fmt.Errorf("whisper.cpp завершився з помилкою (%s): %s", exitText(code, err), clip(tail, 300))
}

func exitText(code int, err error) string {
	if code != 0 {
		if code < 0 || code > 0xFFFF {
			return fmt.Sprintf("код 0x%08X", uint32(code))
		}
		return "код " + strconv.Itoa(code)
	}
	return clip(err.Error(), 80)
}

// lastLines is the last n non-empty lines of a log.
func lastLines(s string, n int) string {
	lines := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
	var keep []string
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		if l := strings.TrimFunc(lines[i], unicode.IsSpace); l != "" {
			keep = append([]string{l}, keep...)
		}
	}
	return strings.Join(keep, " · ")
}

// ---- service check ----------------------------------------------------------------------------

// testLocal checks the program (--help / --version, never a transcription)
// and the model file (present, complete, a ggml file).
func (c *Client) testLocal(ctx context.Context) (bool, string) {
	bin, model, ok, hint := LocalStatus(c.get())
	if bin == "" {
		return false, hint
	}
	wi, err := probeWhisper(ctx, bin, true)
	if err != nil {
		return false, err.Error()
	}
	if !ok {
		return false, hint
	}
	if err := checkModel(model); err != nil {
		return false, err.Error()
	}
	name := "whisper.cpp"
	if wi.version != "" {
		name += " " + wi.version
	}
	msg := name + " працює · модель " + modelLabel(model)
	if !wi.words {
		msg += " · стара версія без часу кожного слова — краще онови її"
	}
	return true, msg
}

// ggmlMagic starts every whisper.cpp model: GGML_FILE_MAGIC 0x67676d6c, little-endian.
var ggmlMagic = []byte("lmgg")

// checkModel: the file is a ggml model and, for a catalogue model, complete.
func checkModel(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("Модель не відкривається: %s", filepath.Base(path))
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, ggmlMagic) {
		return fmt.Errorf("%s — не модель whisper.cpp (ggml) — завантаж модель ще раз («Встановити»)", filepath.Base(path))
	}
	if st, err := f.Stat(); err == nil {
		for _, m := range localModels {
			if strings.EqualFold(filepath.Base(path), modelFile(m.ID)) && st.Size() < m.size {
				return fmt.Errorf("Модель %s завантажена не повністю — натисни «Встановити», щоб докачати її", m.ID)
			}
		}
	}
	return nil
}

// ---- progress ---------------------------------------------------------------------------------

// reporter forwards progress at most ~8 times a second (the UI posts every
// call to its frame loop); force sends a phase change at once.
type reporter struct {
	mu   sync.Mutex
	fn   func(int, string)
	last time.Time
	pct  int
	msg  string
}

func newReporter(fn func(int, string)) *reporter { return &reporter{fn: fn, pct: -1} }

func (r *reporter) report(pct int, msg string) { r.send(pct, msg, false) }

func (r *reporter) force(pct int, msg string) { r.send(pct, msg, true) }

func (r *reporter) send(pct int, msg string, force bool) {
	if r == nil || r.fn == nil {
		return
	}
	pct = max(0, min(100, pct))
	r.mu.Lock()
	now := time.Now()
	if !force && (pct == r.pct && msg == r.msg || now.Sub(r.last) < 120*time.Millisecond) {
		r.mu.Unlock()
		return
	}
	r.last, r.pct, r.msg = now, pct, msg
	r.mu.Unlock()
	r.fn(pct, msg)
}

// wordsWord is "слово" in the right Ukrainian plural for n.
func wordsWord(n int) string {
	n %= 100
	switch {
	case n >= 11 && n <= 14:
		return "слів"
	case n%10 == 1:
		return "слово"
	case n%10 >= 2 && n%10 <= 4:
		return "слова"
	}
	return "слів"
}
