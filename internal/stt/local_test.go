package stt

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// ---- the fake whisper-cli ----------------------------------------------------------------------

// The test binary doubles as whisper-cli: started with YTC_FAKE_WHISPER set
// it behaves like the program (fakeWhisper) instead of running the tests.
// init runs before TestMain, so the flags whisper gets never reach the
// testing package.
const (
	fakeEnv    = "YTC_FAKE_WHISPER"
	fakeLogEnv = "YTC_FAKE_LOG"
)

func init() {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fakeWhisper(mode, os.Args[1:]))
	}
}

// fakeCall is one run of the fake as it logs it.
type fakeCall struct {
	Dir  string   `json:"dir"`
	Args []string `json:"args"`
	Wav  string   `json:"wav"` // "rate/channels/bits" of the -f file when it is a WAV
}

func fakeWhisper(mode string, args []string) int {
	has := func(flag string) bool {
		for _, a := range args {
			if a == flag {
				return true
			}
		}
		return false
	}
	val := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if p := os.Getenv(fakeLogEnv); p != "" {
		wd, _ := os.Getwd()
		call := fakeCall{Dir: wd, Args: args}
		if f := val("-f"); strings.HasSuffix(f, ".wav") {
			call.Wav = wavInfo(f)
		}
		b, _ := json.Marshal(call)
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			f.Write(append(b, '\n'))
			f.Close()
		}
	}
	switch {
	case mode == "deprecated":
		fmt.Println("\nWARNING: The binary 'main.exe' is deprecated.\n Please use 'whisper-cli.exe' instead.")
		return 1
	case has("--help") || has("-h"):
		fmt.Fprint(os.Stderr, fakeHelp(mode))
		return 0
	case has("--version"):
		fmt.Fprintln(os.Stderr, "whisper.cpp version 1.9.4 (b5130)")
		return 0
	}
	switch mode {
	case "hang":
		fmt.Fprintln(os.Stderr, "whisper_print_progress_callback: progress =   5%")
		time.Sleep(time.Minute)
		return 0
	case "unknownarg":
		fmt.Fprintln(os.Stderr, "error: unknown argument: -sns")
		fmt.Fprint(os.Stderr, fakeHelp("old"))
		return 0 // whisper-cli exits with 0 here
	case "badmodel":
		fmt.Fprintln(os.Stderr, "whisper_model_load: invalid model data (bad magic)")
		fmt.Fprintln(os.Stderr, "error: failed to initialize whisper context")
		return 3
	}
	model, file, out := val("-m"), val("-f"), val("-of")
	if _, err := os.Stat(model); err != nil {
		fmt.Fprintf(os.Stderr, "whisper_init_from_file_with_params_no_state: failed to open '%s'\nerror: failed to initialize whisper context\n", model)
		return 3
	}
	if _, err := os.Stat(file); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to read audio file '%s'\n", file)
		return 2
	}
	fmt.Fprintf(os.Stderr, "whisper_init_from_file_with_params_no_state: loading model from '%s'\n", model)
	if val("-l") == "auto" {
		fmt.Fprintln(os.Stderr, "whisper_full_with_state: auto-detected language: uk (p = 0.987654)")
	}
	if has("-pp") {
		for p := 0; p <= 100; p += 25 {
			fmt.Fprintf(os.Stderr, "whisper_print_progress_callback: progress = %3d%%\n", p)
		}
	}
	lang := val("-l")
	if lang == "auto" {
		lang = "uk"
	}
	if err := os.WriteFile(out+".json", fakeJSON(has("-sow") && val("-ml") == "1", lang), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	return 0
}

func fakeHelp(mode string) string {
	lines := []string{
		"",
		"usage: whisper-cli [options] file0 file1 ...",
		"supported audio formats: flac, mp3, ogg, wav",
		"",
		"options:",
		"  -h,        --help                 [default] show this help message and exit",
		"             --version              show version information and exit",
		"  -t N,      --threads N            [4      ] number of threads to use during computation",
		"  -ml N,     --max-len N            [0      ] maximum segment length in characters",
		"  -sow,      --split-on-word        [false  ] split on word rather than on token",
		"  -oj,       --output-json          [false  ] output result in a JSON file",
		"  -of FNAME, --output-file FNAME    [       ] output file path (without file extension)",
		"  -pp,       --print-progress       [false  ] print progress",
		"  -l LANG,   --language LANG        [en     ] spoken language ('auto' for auto-detect)",
		"  -m FNAME,  --model FNAME          [models/ggml-base.en.bin] model path",
		"  -f FNAME,  --file FNAME           [       ] input audio file path",
		"  -sns,      --suppress-nst         [false  ] suppress non-speech tokens",
		"",
	}
	var out []string
	for _, l := range lines {
		if mode == "old" && (strings.Contains(l, "--version") || strings.Contains(l, "--split-on-word") ||
			strings.Contains(l, "--print-progress") || strings.Contains(l, "--suppress-nst")) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n") + "\n"
}

type fakeItem struct {
	text     string
	from, to int64 // ms
}

var (
	fakeWords = []fakeItem{
		{"Привіт,", 0, 620}, {"друзі!", 620, 1180}, {"Сьогодні", 1600, 2100}, {"—", 2100, 2200}, {"про", 2200, 2400},
		{"космос.", 2400, 3050}, {"[BLANK_AUDIO]", 3050, 3500}, {"А", 3600, 3700}, {"ти", 3700, 3850}, {"знав?", 3850, 4400},
	}
	fakeSegments = []fakeItem{
		{" Привіт, друзі!", 0, 1180}, {" Сьогодні — про космос.", 1600, 3050}, {" [BLANK_AUDIO]", 3050, 3500}, {" А ти знав?", 3600, 4400},
	}
)

// fakeJSON writes the -oj file the way output_json in examples/cli/cli.cpp
// does (tab indents, quotes and backslashes escaped, nothing else).
func fakeJSON(words bool, lang string) []byte {
	items := fakeSegments
	if words {
		items = fakeWords
	}
	stamp := func(ms int64) string {
		return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	}
	var b strings.Builder
	b.WriteString("{\n\t\"systeminfo\": \"WHISPER : COREML = 0 | OPENVINO = 0 | CPU : SSE3 = 1 | AVX2 = 1 | F16C = 1 | FMA = 1 | OPENMP = 1 | REPACK = 1 | \",\n")
	b.WriteString("\t\"model\": {\n\t\t\"type\": \"small\",\n\t\t\"multilingual\": true,\n\t\t\"vocab\": 51865,\n")
	b.WriteString("\t\t\"audio\": {\n\t\t\t\"ctx\": 1500,\n\t\t\t\"state\": 768,\n\t\t\t\"head\": 12,\n\t\t\t\"layer\": 12\n\t\t},\n")
	b.WriteString("\t\t\"text\": {\n\t\t\t\"ctx\": 448,\n\t\t\t\"state\": 768,\n\t\t\t\"head\": 12,\n\t\t\t\"layer\": 12\n\t\t},\n")
	b.WriteString("\t\t\"mels\": 80,\n\t\t\"ftype\": 1\n\t},\n")
	b.WriteString("\t\"params\": {\n\t\t\"model\": \"model.bin\",\n\t\t\"language\": \"auto\",\n\t\t\"translate\": false\n\t},\n")
	fmt.Fprintf(&b, "\t\"result\": {\n\t\t\"language\": %q\n\t},\n", lang)
	b.WriteString("\t\"transcription\": [\n")
	for i, it := range items {
		fmt.Fprintf(&b, "\t\t{\n\t\t\t\"timestamps\": {\n\t\t\t\t\"from\": %q,\n\t\t\t\t\"to\": %q\n\t\t\t},\n", stamp(it.from), stamp(it.to))
		fmt.Fprintf(&b, "\t\t\t\"offsets\": {\n\t\t\t\t\"from\": %d,\n\t\t\t\t\"to\": %d\n\t\t\t},\n", it.from, it.to)
		b.WriteString("\t\t\t\"text\": \"" + it.text + "\"\n\t\t}")
		if i < len(items)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("\t]\n}\n")
	return []byte(b.String())
}

func wavInfo(path string) string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 36 || string(b[0:4]) != "RIFF" {
		return "unreadable"
	}
	return fmt.Sprintf("%d/%d/%d", binary.LittleEndian.Uint32(b[24:28]), binary.LittleEndian.Uint16(b[22:24]), binary.LittleEndian.Uint16(b[34:36]))
}

// writeWav writes a PCM WAV of silence.
func writeWav(t *testing.T, path string, rate, channels int, seconds float64) {
	t.Helper()
	n := int(float64(rate)*seconds) * channels * 2
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+n))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))
	binary.Write(&b, binary.LittleEndian, uint16(channels))
	binary.Write(&b, binary.LittleEndian, uint32(rate))
	binary.Write(&b, binary.LittleEndian, uint32(rate*channels*2))
	binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(n))
	b.Write(make([]byte, n))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- fixtures ---------------------------------------------------------------------------------

// isolate points every seam of the local provider at temp folders (nothing
// of the real machine is found: no app folder, no PATH, no Homebrew, no
// FFmpeg) and restores them after the test.
func isolate(t *testing.T) (exeDir, localDir string) {
	t.Helper()
	exeDir, localDir = t.TempDir(), t.TempDir()
	saved := struct {
		exeDirFn, localDirFn func() string
		lookPath             func(string) (string, error)
		systemDirs           func() []string
		locateFFmpeg         func(config.Config) string
		models               []localModel
		releasesAPI, prefix  string
		modelBase, tag       string
		pinned               map[string]fileSpec
		goos, goarch         string
		stall, pause         time.Duration
	}{exeDirFn, localDirFn, lookPath, systemDirs, locateFFmpeg, localModels, releasesAPI, releasePrefix,
		modelBase, pinnedTag, pinnedAssets, installGOOS, installGOARCH, downloadStall, retryPause}
	exeDirFn = func() string { return exeDir }
	localDirFn = func() string { return localDir }
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	systemDirs = func() []string { return nil }
	locateFFmpeg = func(config.Config) string { return "" }
	releasesAPI, releasePrefix, modelBase = "http://127.0.0.1:9/api", "http://127.0.0.1:9/dl/", "http://127.0.0.1:9/hf/"
	retryPause = time.Millisecond
	resetProbes()
	t.Cleanup(func() {
		exeDirFn, localDirFn, lookPath, systemDirs, locateFFmpeg = saved.exeDirFn, saved.localDirFn, saved.lookPath, saved.systemDirs, saved.locateFFmpeg
		localModels, releasesAPI, releasePrefix, modelBase, pinnedTag = saved.models, saved.releasesAPI, saved.prefix, saved.modelBase, saved.tag
		pinnedAssets, installGOOS, installGOARCH, downloadStall, retryPause = saved.pinned, saved.goos, saved.goarch, saved.stall, saved.pause
		resetProbes()
	})
	return exeDir, localDir
}

func resetProbes() {
	probeMu.Lock()
	probeCache = map[string]whisperInfo{}
	probeMu.Unlock()
}

// fakeMode makes the test binary act as whisper-cli in mode for the
// children of this test; the returned func reads the calls it logged.
func fakeMode(t *testing.T, mode string) func() []fakeCall {
	t.Helper()
	resetProbes()
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv(fakeEnv, mode)
	t.Setenv(fakeLogEnv, logPath)
	return func() []fakeCall {
		b, _ := os.ReadFile(logPath)
		var out []fakeCall
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var c fakeCall
			if json.Unmarshal([]byte(l), &c) == nil {
				out = append(out, c)
			}
		}
		return out
	}
}

func touch(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeModelData is a tiny "model": the ggml magic and some bytes.
func fakeModelData(n int) []byte {
	b := make([]byte, n)
	copy(b, ggmlMagic)
	for i := 4; i < n; i++ {
		b[i] = byte(i * 7)
	}
	return b
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func localClient(cfg config.Config) *Client {
	cfg.STT.Provider = ProviderLocal
	return New(func() config.Config { return cfg })
}

func argOf(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// ---- catalogue, parsing, languages ------------------------------------------------------------

func TestLocalModelsCatalogue(t *testing.T) {
	ms := LocalModels()
	want := []string{"tiny", "base", "small", "medium", "large-v3-turbo"}
	if len(ms) != len(want) {
		t.Fatalf("models: %+v", ms)
	}
	for i, m := range ms {
		if m.ID != want[i] || m.Label == "" || m.Note == "" || m.SizeMB <= 0 {
			t.Errorf("model %d: %+v", i, m)
		}
		if i > 0 && m.SizeMB <= ms[i-1].SizeMB {
			t.Errorf("not smallest first: %+v", ms)
		}
		if lm, _ := lookupModel(m.ID); !isHex64(lm.sha256) || lm.size <= 0 {
			t.Errorf("model %s: bad size/hash", m.ID)
		}
	}
	if ms[2].SizeMB != 465 || ms[0].SizeMB != 74 {
		t.Errorf("sizes: tiny %d, small %d MB", ms[0].SizeMB, ms[2].SizeMB)
	}
	if _, ok := lookupModel(DefaultLocalModel); !ok {
		t.Fatal("the default model is not in the catalogue")
	}
	if len(whisperLangCodes) != 100 {
		t.Errorf("whisper knows 100 languages, table has %d", len(whisperLangCodes))
	}
}

func TestParseWhisperWords(t *testing.T) {
	raw := fakeJSON(true, "uk")
	// whisper.cpp does not escape control characters: a raw tab in a text.
	raw = bytes.Replace(raw, []byte(`"Сьогодні"`), []byte("\"Сьогодні\t\""), 1)
	tr, err := parseWhisper(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	tr.finish()
	var words []string
	for _, w := range tr.Words {
		words = append(words, w.Text)
	}
	if got := strings.Join(words, "|"); got != "Привіт,|друзі!|Сьогодні|—|про|космос.|А|ти|знав?" {
		t.Fatalf("words: %s", got)
	}
	if tr.Text != "Привіт, друзі! Сьогодні — про космос. А ти знав?" {
		t.Errorf("text: %q", tr.Text)
	}
	if len(tr.Segments) != 3 || tr.Segments[1].Text != "Сьогодні — про космос." || tr.Segments[1].Start != 1.6 || tr.Segments[1].End != 3.05 {
		t.Errorf("segments: %+v", tr.Segments)
	}
	if w := tr.Words[1]; w.Start != 0.62 || w.End != 1.18 {
		t.Errorf("times of «друзі!»: %+v", w)
	}
	if tr.Language != "uk" || tr.Duration != 4.4 {
		t.Errorf("language %q, duration %v", tr.Language, tr.Duration)
	}
}

func TestParseWhisperSegmentsAndAnnotations(t *testing.T) {
	tr, err := parseWhisper(fakeJSON(false, "en"), false)
	if err != nil {
		t.Fatal(err)
	}
	tr.finish()
	if len(tr.Segments) != 3 || tr.Segments[2].Text != "А ти знав?" {
		t.Fatalf("segments: %+v", tr.Segments)
	}
	if len(tr.Words) != 9 || tr.Words[0].Text != "Привіт," || math.Abs(tr.Words[8].End-4.4) > 1e-9 {
		t.Fatalf("words spread over the segments: %+v", tr.Words)
	}
	// Bracketed runs and captions written by builds without -sns.
	in := []Word{{Text: "Ну"}, {Text: "["}, {Text: "Музика"}, {Text: "]"}, {Text: "(сміх)"}, {Text: "♪"},
		{Text: "(оплески"}, {Text: "залу)"}, {Text: "*зітхає*"}, {Text: "добре."}}
	var got []string
	for _, w := range dropAnnotations(in) {
		got = append(got, w.Text)
	}
	if strings.Join(got, " ") != "Ну добре." {
		t.Fatalf("annotations kept: %v", got)
	}
	// Timestamps when offsets are missing; broken JSON is an error.
	if v := msOr(nil, "00:01:02,345"); math.Abs(v-62.345) > 1e-9 {
		t.Errorf("stamp: %v", v)
	}
	if _, err := parseWhisper([]byte("not json"), true); err == nil {
		t.Error("broken JSON accepted")
	}
}

func TestWhisperLanguages(t *testing.T) {
	for in, want := range map[string]string{"": "auto", "uk": "uk", "UK": "uk", "jv": "jw", "fil": "tl", "zh-CN": "zh",
		"yue": "yue", "ckb": "auto", "zu": "auto", "eng": "en"} {
		if got := whisperLang(in); got != want {
			t.Errorf("whisperLang(%q) = %q, want %q", in, got, want)
		}
	}
	if fromWhisperLang("jw") != "jv" || fromWhisperLang("auto") != "" || fromWhisperLang("UK") != "uk" {
		t.Error("fromWhisperLang")
	}
}

func TestWavDuration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.wav")
	writeWav(t, p, 16000, 1, 1.5)
	if d := wavDuration(p); math.Abs(d-1.5) > 1e-9 {
		t.Fatalf("duration %v", d)
	}
	writeWav(t, p, 44100, 2, 2)
	if d := wavDuration(p); math.Abs(d-2) > 1e-9 {
		t.Fatalf("stereo duration %v", d)
	}
	touch(t, p, []byte("RIFFxxxxWAVE"))
	if d := wavDuration(p); d != 0 {
		t.Fatalf("broken wav: %v", d)
	}
}

// ---- finding the program and the model ---------------------------------------------------------

func TestLocalStatusDetectionOrder(t *testing.T) {
	exeDir, localDir := isolate(t)
	pathDir, brewDir := t.TempDir(), t.TempDir()
	lookPath = exec.LookPath
	t.Setenv("PATH", pathDir)
	systemDirs = func() []string { return []string{brewDir} }
	cli := exe("whisper-cli")

	brew := touch(t, filepath.Join(brewDir, cli), nil)
	onPath := touch(t, filepath.Join(pathDir, cli), nil)
	legacy := touch(t, filepath.Join(localDir, "bin", exe("main")), nil)
	installed := touch(t, filepath.Join(localDir, "bin", cli), nil)
	nested := touch(t, filepath.Join(exeDir, "whisper", "Release", cli), nil)
	beside := touch(t, filepath.Join(exeDir, cli), nil)
	touch(t, filepath.Join(exeDir, exe("main")), nil) // a "main" next to the app is not whisper's

	order := []string{beside, nested, installed, legacy, onPath, brew}
	for i, want := range order {
		got, err := findBin("")
		if err != nil || !samePath(got, want) {
			t.Fatalf("step %d: got %q (%v), want %q", i, got, err, want)
		}
		if err := os.Remove(want); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := findBin(""); err == nil {
		t.Fatalf("found %q with nothing installed (the exe dir's main must not count)", got)
	}

	// The Settings path wins and is never second-guessed.
	own := touch(t, filepath.Join(t.TempDir(), "мій whisper", cli), nil)
	touch(t, filepath.Join(localDir, "bin", cli), nil)
	if got, _ := findBin(own); !samePath(got, own) {
		t.Fatalf("configured file: %q", got)
	}
	if got, _ := findBin(`"` + filepath.Dir(own) + `"`); !samePath(got, own) {
		t.Fatalf("configured folder: %q", got)
	}
	if got, err := findBin(filepath.Join(exeDir, "gone", cli)); got != "" || err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("missing configured path: %q, %v", got, err)
	}
}

func TestLocalStatusModels(t *testing.T) {
	_, localDir := isolate(t)
	cli := touch(t, filepath.Join(localDir, "bin", exe("whisper-cli")), nil)
	models := filepath.Join(localDir, "models")

	bin, model, ok, hint := LocalStatus(config.Config{})
	if ok || !samePath(bin, cli) || model != "" || !strings.Contains(hint, "Модель") {
		t.Fatalf("no model: %q %q %v %q", bin, model, ok, hint)
	}
	touch(t, filepath.Join(models, "ggml-silero-v5.1.2.bin"), nil) // a VAD model, not whisper
	touch(t, filepath.Join(models, "ggml-medium.bin.part"), nil)   // an unfinished download
	touch(t, filepath.Join(models, "ggml-small.en.bin"), nil)
	if _, model, _, _ = LocalStatus(config.Config{}); filepath.Base(model) != "ggml-small.en.bin" {
		t.Fatalf("only an English model: %q", model)
	}
	touch(t, filepath.Join(models, "ggml-tiny-q5_1.bin"), nil)
	if _, model, _, _ = LocalStatus(config.Config{}); filepath.Base(model) != "ggml-tiny-q5_1.bin" {
		t.Fatalf("multilingual before English-only: %q", model)
	}
	touch(t, filepath.Join(models, "ggml-small.bin"), nil)
	touch(t, filepath.Join(models, "ggml-large-v3-turbo.bin"), nil)
	bin, model, ok, hint = LocalStatus(config.Config{})
	if !ok || filepath.Base(model) != "ggml-large-v3-turbo.bin" || !strings.Contains(hint, "large-v3-turbo") {
		t.Fatalf("best catalogue model: %q %v %q", model, ok, hint)
	}
	// A configured model wins; a configured missing one is reported.
	cfg := config.Config{STT: config.STT{LocalModel: filepath.Join(models, "ggml-small.bin")}}
	if _, model, ok, _ = LocalStatus(cfg); !ok || filepath.Base(model) != "ggml-small.bin" {
		t.Fatalf("configured model: %q", model)
	}
	cfg.STT.LocalModel = filepath.Join(models, "ggml-nope.bin")
	if _, model, ok, hint = LocalStatus(cfg); ok || model != "" || !strings.Contains(hint, "ggml-nope.bin") {
		t.Fatalf("missing configured model: %q %v %q", model, ok, hint)
	}
	// Without the program the hint says how to get it on this OS.
	cfg = config.Config{STT: config.STT{LocalBin: filepath.Join(localDir, "nope", "whisper-cli")}}
	if _, _, ok, hint = LocalStatus(cfg); ok || !strings.Contains(hint, "nope") {
		t.Fatalf("missing configured program: %v %q", ok, hint)
	}
	if err := errNoProgramFor("darwin"); !strings.Contains(err.Error(), "brew install whisper-cpp") {
		t.Fatalf("macOS hint: %v", err)
	}
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = absPath(a), absPath(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ---- running whisper.cpp -----------------------------------------------------------------------

// runEnv: the fake program as STT.LocalBin, a model and a 5 s recording in
// folders with Cyrillic names (whisper.cpp must only ever see ASCII paths).
func runEnv(t *testing.T) (config.Config, string) {
	t.Helper()
	isolate(t)
	data := fakeModelData(4096)
	localModels = append(append([]localModel{}, localModels...), localModel{ModelInfo: ModelInfo{ID: "test", Label: "Test"},
		size: int64(len(data)), sha256: sha(data)})
	root := t.TempDir()
	model := touch(t, filepath.Join(root, "Моделі", "ggml-test.bin"), data)
	rec := filepath.Join(root, "Записи", "голос.wav")
	writeWav(t, rec, 16000, 1, 5)
	return config.Config{STT: config.STT{Provider: ProviderLocal, LocalBin: os.Args[0], LocalModel: model}}, rec
}

func TestLocalTranscribe(t *testing.T) {
	cfg, rec := runEnv(t)
	calls := fakeMode(t, "ok")
	c := localClient(cfg)
	if p, m := c.Model(); p != ProviderLocal || m != "test" {
		t.Fatalf("Model() = %q, %q", p, m)
	}
	var mu sync.Mutex
	var pcts []int
	var last string
	tr, err := c.TranscribeProgress(context.Background(), rec, "uk", func(pct int, msg string) {
		mu.Lock()
		pcts, last = append(pcts, pct), msg
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Words) != 9 || tr.Words[0].Text != "Привіт," || tr.Language != "uk" || len(tr.Segments) != 3 {
		t.Fatalf("transcript: %+v", tr)
	}
	if tr.Duration != 5 {
		t.Errorf("duration %v, want the recording's 5 s", tr.Duration)
	}
	got := calls()
	if len(got) != 3 { // --help, --version, the run
		t.Fatalf("calls: %+v", got)
	}
	run := got[2].Args
	for flag, want := range map[string]string{"-l": "uk", "-f": "audio.wav", "-of": "out", "-ml": "1"} {
		if v := argOf(run, flag); v != want {
			t.Errorf("%s = %q, want %q (%v)", flag, v, want, run)
		}
	}
	for _, f := range []string{"-oj", "-sow", "-pp", "-sns", "-t", "-m"} {
		if !hasArg(run, f) {
			t.Errorf("missing %s: %v", f, run)
		}
	}
	if m := argOf(run, "-m"); runtime.GOOS == "windows" && !isASCII(m) {
		t.Errorf("model path with non-ASCII characters reached whisper.cpp: %q", m)
	}
	if got[2].Wav != "16000/1/16" {
		t.Errorf("audio: %q", got[2].Wav)
	}
	if _, err := os.Stat(got[2].Dir); !os.IsNotExist(err) {
		t.Errorf("work folder %s left behind (%v)", got[2].Dir, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pcts) < 3 || pcts[len(pcts)-1] != 100 || !strings.Contains(last, "9 слів") {
		t.Errorf("progress: %v, last %q", pcts, last)
	}
	for i := 1; i < len(pcts); i++ {
		if pcts[i] < pcts[i-1] {
			t.Errorf("progress went back: %v", pcts)
		}
	}

	// Detection: "" asks whisper to detect, the detected language comes back.
	tr, err = c.Transcribe(context.Background(), rec, "")
	if err != nil || tr.Language != "uk" {
		t.Fatalf("auto: %v, %q", err, tr.Language)
	}
	if got := calls(); argOf(got[len(got)-1].Args, "-l") != "auto" {
		t.Errorf("lang: %v", got[len(got)-1].Args)
	}
}

func TestLocalTranscribeOldWhisper(t *testing.T) {
	cfg, rec := runEnv(t)
	calls := fakeMode(t, "old")
	tr, err := localClient(cfg).Transcribe(context.Background(), rec, "en")
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	run := got[len(got)-1].Args
	for _, f := range []string{"-sow", "-pp", "-sns", "-ml", "--version"} {
		if hasArg(run, f) {
			t.Errorf("flag %s passed to a version without it: %v", f, run)
		}
	}
	if len(got) != 2 {
		t.Errorf("--version asked although not listed: %+v", got)
	}
	if len(tr.Words) != 9 || len(tr.Segments) != 3 || tr.Language != "en" {
		t.Fatalf("segment timings: %+v", tr)
	}
}

func TestLocalTranscribeWithFFmpeg(t *testing.T) {
	cfg, _ := runEnv(t)
	ff, err := media.Locate(config.Default())
	if err != nil || ff.FFmpeg == "" {
		t.Skip("ffmpeg not available")
	}
	locateFFmpeg = func(config.Config) string { return ff.FFmpeg }
	rec := filepath.Join(t.TempDir(), "stereo 44k.wav")
	writeWav(t, rec, 44100, 2, 2)
	calls := fakeMode(t, "ok")
	tr, err := localClient(cfg).Transcribe(context.Background(), rec, "uk")
	if err != nil {
		t.Fatal(err)
	}
	got := calls()
	if w := got[len(got)-1].Wav; w != "16000/1/16" {
		t.Fatalf("FFmpeg did not make a 16 kHz mono WAV: %q", w)
	}
	if tr.Duration < 4.4 {
		t.Errorf("duration %v", tr.Duration)
	}
}

func TestLocalTranscribeErrors(t *testing.T) {
	cfg, rec := runEnv(t)
	ctx := context.Background()
	for mode, want := range map[string]string{
		"unknownarg": "застаріла",
		"badmodel":   "Модель whisper.cpp не відкривається",
		"deprecated": "застаріла програма",
	} {
		fakeMode(t, mode)
		if _, err := localClient(cfg).Transcribe(ctx, rec, "uk"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", mode, err)
		}
	}
	fakeMode(t, "ok")
	// No FFmpeg and a format whisper.cpp cannot read.
	m4a := touch(t, filepath.Join(t.TempDir(), "voice.m4a"), []byte("not really"))
	if _, err := localClient(cfg).Transcribe(ctx, m4a, "uk"); err == nil || !strings.Contains(err.Error(), "FFmpeg") {
		t.Errorf("m4a without FFmpeg: %v", err)
	}
	if _, err := localClient(cfg).Transcribe(ctx, filepath.Join(t.TempDir(), "none.wav"), "uk"); err == nil || !strings.Contains(err.Error(), "не знайдено") {
		t.Errorf("missing recording: %v", err)
	}
	bad := cfg
	bad.STT.LocalBin = filepath.Join(t.TempDir(), "nope", exe("whisper-cli"))
	if _, err := localClient(bad).Transcribe(ctx, rec, "uk"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("missing program: %v", err)
	}
	// The online provider's key is never needed.
	if _, err := localClient(cfg).Transcribe(ctx, rec, "uk"); err != nil {
		t.Errorf("no key needed: %v", err)
	}
}

func TestLocalTranscribeCancel(t *testing.T) {
	cfg, rec := runEnv(t)
	fakeMode(t, "hang")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	t0 := time.Now()
	go func() {
		_, err := localClient(cfg).TranscribeProgress(ctx, rec, "uk", func(pct int, msg string) {
			if pct >= 1 {
				once.Do(func() { close(started) })
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("whisper.cpp did not start")
	}
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancel did not stop whisper.cpp")
	}
	if d := time.Since(t0); d > 30*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestLocalServiceCheck(t *testing.T) {
	cfg, _ := runEnv(t)
	calls := fakeMode(t, "ok")
	ok, msg := localClient(cfg).Test(context.Background())
	if !ok || !strings.Contains(msg, "1.9.4") || !strings.Contains(msg, "модель test") {
		t.Fatalf("check: %v %q", ok, msg)
	}
	for _, c := range calls() {
		if hasArg(c.Args, "-f") || hasArg(c.Args, "-m") {
			t.Fatalf("the check transcribed: %v", c.Args)
		}
	}
	// A model file that is not a ggml model; a truncated catalogue model.
	notModel := cfg
	notModel.STT.LocalModel = touch(t, filepath.Join(t.TempDir(), "ggml-x.bin"), []byte("<html>404</html>"))
	if ok, msg := localClient(notModel).Test(context.Background()); ok || !strings.Contains(msg, "не модель") {
		t.Errorf("html as model: %v %q", ok, msg)
	}
	short := cfg
	short.STT.LocalModel = touch(t, filepath.Join(t.TempDir(), "ggml-small.bin"), fakeModelData(100))
	if ok, msg := localClient(short).Test(context.Background()); ok || !strings.Contains(msg, "не повністю") {
		t.Errorf("truncated model: %v %q", ok, msg)
	}
	fakeMode(t, "deprecated")
	if ok, msg := localClient(cfg).Test(context.Background()); ok || !strings.Contains(msg, "застаріла програма") {
		t.Errorf("deprecated main: %v %q", ok, msg)
	}
	noProg := cfg
	noProg.STT.LocalBin = filepath.Join(t.TempDir(), exe("whisper-cli"))
	if ok, msg := localClient(noProg).Test(context.Background()); ok || !strings.Contains(msg, "не знайдено") {
		t.Errorf("no program: %v %q", ok, msg)
	}
}

// ---- installing --------------------------------------------------------------------------------

// installMock serves the GitHub releases API, the release archive and the
// Hugging Face model (resolve → 302 → CDN, like the real thing).
type installMock struct {
	srv       *httptest.Server
	zip       []byte
	model     []byte
	served    []byte // what the CDN sends (default: model)
	linked    string // X-Linked-Etag on the redirect ("" = none)
	apiStatus int
	// cut makes the n-th model response stop after half the bytes:
	// "abort" drops the connection, "stall" goes silent until the client leaves.
	cut     map[int]string
	mu      sync.Mutex
	hits    map[string]int
	ranges  []string
	modelN  atomic.Int32
	onModel func()
}

func newInstallMock(t *testing.T) *installMock {
	t.Helper()
	m := &installMock{hits: map[string]int{}, cut: map[int]string{}, apiStatus: http.StatusOK}
	m.zip = releaseZip(t)
	m.model = fakeModelData(300 << 10)
	m.srv = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *installMock) count(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hits[path]
}

func (m *installMock) rangesSeen() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.ranges...)
}

func (m *installMock) total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, v := range m.hits {
		n += v
	}
	return n
}

func (m *installMock) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.hits[r.URL.Path]++
	if strings.HasPrefix(r.URL.Path, "/cdn/") {
		m.ranges = append(m.ranges, r.Header.Get("Range"))
	}
	m.mu.Unlock()
	switch {
	case r.URL.Path == "/api/releases":
		if m.apiStatus != http.StatusOK {
			http.Error(w, `{"message":"API rate limit exceeded"}`, m.apiStatus)
			return
		}
		rel := []map[string]any{
			{"tag_name": "v9.9.9", "draft": false, "prerelease": false, "assets": []any{}},
			{"tag_name": "b9999", "draft": false, "prerelease": true, "assets": []map[string]any{
				{"name": "whisper-bin-Win32.zip", "state": "uploaded", "size": 1, "digest": "sha256:" + strings.Repeat("0", 64),
					"browser_download_url": m.srv.URL + "/dl/b9999/whisper-bin-Win32.zip"},
				{"name": "whisper-bin-x64.zip", "state": "uploaded", "size": len(m.zip), "digest": "sha256:" + sha(m.zip),
					"browser_download_url": m.srv.URL + "/dl/b9999/whisper-bin-x64.zip"},
			}},
			{"tag_name": "b9000", "draft": false, "prerelease": true, "assets": []map[string]any{
				{"name": "whisper-bin-x64.zip", "state": "uploaded", "size": 5, "digest": "",
					"browser_download_url": "https://evil.example/whisper-bin-x64.zip"},
			}},
		}
		json.NewEncoder(w).Encode(rel)
	case strings.HasPrefix(r.URL.Path, "/dl/") && strings.HasSuffix(r.URL.Path, "/whisper-bin-x64.zip"):
		http.Redirect(w, r, "/objects"+r.URL.Path, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/objects/"):
		http.ServeContent(w, r, "whisper.zip", time.Time{}, bytes.NewReader(m.zip))
	case strings.HasPrefix(r.URL.Path, "/hf/"):
		if m.linked != "" {
			w.Header().Set("X-Linked-Etag", `"`+m.linked+`"`)
			w.Header().Set("X-Linked-Size", fmt.Sprint(len(m.model)))
		}
		http.Redirect(w, r, "/cdn/"+strings.TrimPrefix(r.URL.Path, "/hf/"), http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/cdn/"):
		n := int(m.modelN.Add(1))
		if m.onModel != nil {
			m.onModel()
		}
		data := m.served
		if data == nil {
			data = m.model
		}
		switch m.cut[n] {
		case "abort", "stall":
			if r.Header.Get("Range") != "" {
				break
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			w.Write(data[:len(data)/2])
			w.(http.Flusher).Flush()
			if m.cut[n] == "stall" {
				<-r.Context().Done()
			}
			panic(http.ErrAbortHandler)
		}
		http.ServeContent(w, r, "model.bin", time.Time{}, bytes.NewReader(data))
	default:
		http.NotFound(w, r)
	}
}

// releaseZip is a whisper-bin-x64.zip look-alike: Release/whisper-cli(.exe)
// (this test binary, so the installed program answers --help) and DLLs, one
// written with Windows PowerShell's backslashes.
func releaseZip(t *testing.T) []byte {
	t.Helper()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	add := func(name string, data []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if _, err := zw.Create("Release/"); err != nil {
		t.Fatal(err)
	}
	add("Release/"+exe("whisper-cli"), self)
	add(`Release\whisper.dll`, []byte("dll"))
	add("Release/ggml-cpu-haswell.dll", []byte("dll"))
	add("Release/"+exe("main"), []byte("stub"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// installEnv points the installer at the mock: Windows x64, a tiny catalogue
// model "test", nothing installed anywhere, the fake program in "ok" mode.
func installEnv(t *testing.T) (*installMock, string) {
	t.Helper()
	isolate(t)
	m := newInstallMock(t)
	releasesAPI, releasePrefix, modelBase = m.srv.URL+"/api/releases", m.srv.URL+"/dl/", m.srv.URL+"/hf/"
	installGOOS, installGOARCH = "windows", "amd64"
	localModels = append(append([]localModel{}, localModels...), localModel{ModelInfo: ModelInfo{ID: "test", Label: "Test"},
		size: int64(len(m.model)), sha256: sha(m.model)})
	fakeMode(t, "ok")
	return m, filepath.Join(t.TempDir(), "whisper")
}

func TestInstallLocalWindows(t *testing.T) {
	m, dir := installEnv(t)
	var pcts []int
	var msgs []string
	bin, model, err := InstallLocal(context.Background(), dir, "test", func(pct int, msg string) {
		pcts, msgs = append(pcts, pct), append(msgs, msg)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(bin, filepath.Join(dir, "bin", exe("whisper-cli"))) || !samePath(model, filepath.Join(dir, "models", "ggml-test.bin")) {
		t.Fatalf("paths: %q %q", bin, model)
	}
	for _, f := range []string{"whisper.dll", "ggml-cpu-haswell.dll", exe("main")} {
		if !isFile(filepath.Join(dir, "bin", f)) {
			t.Errorf("%s not unpacked next to the program", f)
		}
	}
	if b, _ := os.ReadFile(model); !bytes.Equal(b, m.model) {
		t.Error("model content")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	for _, l := range left {
		if b := filepath.Base(l); b != "bin" && b != "models" {
			t.Errorf("left behind: %s", l)
		}
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "models", "*.part")); len(parts) > 0 {
		t.Errorf("parts left: %v", parts)
	}
	if m.count("/api/releases") != 1 || m.count("/objects/dl/b9999/whisper-bin-x64.zip") != 1 || m.count("/cdn/ggml-test.bin") != 1 {
		t.Errorf("requests: %v", m.hits)
	}
	if len(pcts) < 3 || pcts[len(pcts)-1] != 100 || !strings.Contains(msgs[len(msgs)-1], "Готово") {
		t.Errorf("progress %v %q", pcts, msgs)
	}
	for i := 1; i < len(pcts); i++ {
		if pcts[i] < pcts[i-1] {
			t.Fatalf("progress went back: %v", pcts)
		}
	}
	// Ready, and a second run downloads nothing.
	cfg := config.Config{STT: config.STT{LocalBin: bin, LocalModel: model}}
	if _, _, ok, hint := LocalStatus(cfg); !ok {
		t.Fatalf("not ready: %s", hint)
	}
	before := m.total()
	if bin2, model2, err := InstallLocal(context.Background(), dir, "test", nil); err != nil || bin2 != bin || model2 != model {
		t.Fatalf("again: %q %q %v", bin2, model2, err)
	}
	if m.total() != before {
		t.Errorf("a ready install downloaded again: %v", m.hits)
	}
	// Another model: only the model is fetched.
	if p, err := InstallLocalModel(context.Background(), dir, "nope", nil); err == nil || p != "" {
		t.Errorf("unknown model accepted: %q", p)
	}
}

func TestInstallLocalResumesAfterDrop(t *testing.T) {
	m, dir := installEnv(t)
	m.cut[1] = "abort"
	m.linked = sha(m.model)
	if _, _, err := InstallLocal(context.Background(), dir, "test", nil); err != nil {
		t.Fatal(err)
	}
	if r := m.rangesSeen(); len(r) != 2 || r[1] != fmt.Sprintf("bytes=%d-", len(m.model)/2) {
		t.Fatalf("not resumed: %q", r)
	}
}

func TestInstallLocalStallReconnects(t *testing.T) {
	m, dir := installEnv(t)
	downloadStall = 300 * time.Millisecond
	m.cut[1] = "stall"
	if _, err := InstallLocalModel(context.Background(), dir, "test", nil); err != nil {
		t.Fatal(err)
	}
	if r := m.rangesSeen(); len(r) != 2 || !strings.HasPrefix(r[1], "bytes=") {
		t.Fatalf("no resume after a stall: %q", r)
	}
	if m.count("/api/releases") != 0 {
		t.Error("the model-only install looked for the program")
	}
}

func TestInstallLocalRejectsCorruptModel(t *testing.T) {
	m, dir := installEnv(t)
	bad := append([]byte{}, m.model...)
	bad[len(bad)-1] ^= 0xFF
	m.served = bad
	_, err := InstallLocalModel(context.Background(), dir, "test", nil)
	if err == nil || !strings.Contains(err.Error(), "пошкоджений") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "models", "*")); len(left) != 0 {
		t.Fatalf("a corrupt download was kept: %v", left)
	}
	// The SHA-256 Hugging Face publishes is accepted when the catalogue's is stale.
	m.served, m.linked = nil, sha(m.model)
	localModels[len(localModels)-1].sha256 = strings.Repeat("ab", 32)
	if _, err := InstallLocalModel(context.Background(), dir, "test", nil); err != nil {
		t.Fatalf("published digest: %v", err)
	}
}

func TestInstallLocalCancelKeepsPart(t *testing.T) {
	m, dir := installEnv(t)
	m.cut[1] = "stall"
	ctx, cancel := context.WithCancel(context.Background())
	_, err := InstallLocalModel(ctx, dir, "test", func(pct int, msg string) {
		if strings.Contains(msg, "Завантажуємо") {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	part := filepath.Join(dir, "models", "ggml-test.bin.part")
	if st, err := os.Stat(part); err != nil || st.Size() == 0 {
		t.Fatalf("part not kept for the next run: %v", err)
	}
	if _, err := InstallLocalModel(context.Background(), dir, "test", nil); err != nil {
		t.Fatal(err)
	}
	if r := m.rangesSeen(); len(r) < 2 || !strings.HasPrefix(r[len(r)-1], "bytes=") || r[len(r)-1] == "bytes=0-" {
		t.Fatalf("next run did not resume: %q", r)
	}
}

func TestInstallLocalPinnedFallback(t *testing.T) {
	m, dir := installEnv(t)
	m.apiStatus = http.StatusForbidden
	pinnedTag = "b1234"
	pinnedAssets = map[string]fileSpec{"whisper-bin-x64.zip": {Size: int64(len(m.zip)), SHA256: sha(m.zip)}}
	if _, _, err := InstallLocal(context.Background(), dir, "test", nil); err != nil {
		t.Fatal(err)
	}
	if m.count("/objects/dl/b1234/whisper-bin-x64.zip") != 1 {
		t.Fatalf("pinned build not used: %v", m.hits)
	}
	// A pinned digest that does not match is refused, nothing installed.
	dir2 := filepath.Join(t.TempDir(), "w")
	pinnedAssets = map[string]fileSpec{"whisper-bin-x64.zip": {Size: int64(len(m.zip)), SHA256: strings.Repeat("0", 64)}}
	if _, _, err := InstallLocal(context.Background(), dir2, "test", nil); err == nil || !strings.Contains(err.Error(), "пошкоджений") {
		t.Fatalf("bad digest: %v", err)
	}
	if isFile(filepath.Join(dir2, "bin", exe("whisper-cli"))) {
		t.Fatal("an unverified program was installed")
	}
}

func TestInstallLocalMacUsesHomebrew(t *testing.T) {
	m, dir := installEnv(t)
	installGOOS, installGOARCH = "darwin", "arm64"
	_, _, err := InstallLocal(context.Background(), dir, "test", nil)
	if err == nil || !strings.Contains(err.Error(), "brew install whisper-cpp") {
		t.Fatalf("err = %v", err)
	}
	if m.total() != 0 {
		t.Fatalf("downloaded without the program: %v", m.hits)
	}
	// With Homebrew's whisper-cli only the model is downloaded.
	brew := t.TempDir()
	self, _ := os.ReadFile(os.Args[0])
	cli := touch(t, filepath.Join(brew, exe("whisper-cli")), self)
	systemDirs = func() []string { return []string{brew} }
	bin, model, err := InstallLocal(context.Background(), dir, "test", nil)
	if err != nil || !samePath(bin, cli) || filepath.Base(model) != "ggml-test.bin" {
		t.Fatalf("with Homebrew: %q %q %v", bin, model, err)
	}
	if m.count("/api/releases") != 0 || m.count("/cdn/ggml-test.bin") != 1 {
		t.Fatalf("requests: %v", m.hits)
	}
}

func TestInstallLocalBusy(t *testing.T) {
	_, dir := installEnv(t)
	installMu.Lock()
	_, _, err := InstallLocal(context.Background(), dir, "test", nil)
	installMu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "вже триває") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractZipRefusesEscapes(t *testing.T) {
	for _, name := range []string{"../evil.txt", `..\evil.txt`, "Release/../../evil.txt"} {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
		zw.Close()
		root := t.TempDir()
		archive := touch(t, filepath.Join(root, "a.zip"), b.Bytes())
		dst := filepath.Join(root, "out")
		if err := extractZip(archive, dst); err == nil || !strings.Contains(err.Error(), "небезпечний") {
			t.Errorf("%s: %v", name, err)
		}
		if isFile(filepath.Join(root, "evil.txt")) {
			t.Fatalf("%s escaped", name)
		}
	}
}

func TestDownloadHelpers(t *testing.T) {
	if rangeFrom("bytes 100-199/200") != 100 || rangeFrom("garbage") != -1 {
		t.Error("rangeFrom")
	}
	for in, want := range map[int64]string{8573270: "8,2 МБ", 487601967: "465 МБ", 1624555275: "1,5 ГБ"} {
		if got := fmtSize(in); got != want {
			t.Errorf("fmtSize(%d) = %q, want %q", in, got, want)
		}
	}
	if safeName("../b5130") != "b5130" || safeName("..") != "download" {
		t.Error("safeName")
	}
	for goarch, want := range map[string]string{"amd64": "whisper-bin-x64.zip", "386": "whisper-bin-Win32.zip", "arm64": "whisper-bin-win-cpu-arm64.zip"} {
		if got := assetName("windows", goarch); got != want {
			t.Errorf("%s: %q", goarch, got)
		}
	}
	if assetName("darwin", "arm64") != "" || assetName("linux", "amd64") != "" {
		t.Error("no official command-line build outside Windows")
	}
}
