package stt

// Local speech recognition (cfg.STT.Provider "local"): whisper.cpp's
// command-line program runs on this computer — no key, no upload, no internet
// after the one-time download the user starts in Settings (InstallLocal).
//
//	program  whisper-cli (whisper.cpp ≥ 1.7.4; "main" in older builds — a
//	         deprecation stub since), found by LocalStatus: the Settings path
//	         (STT.LocalBin) → next to the app → LocalDir → PATH → Homebrew /
//	         MacPorts / Linuxbrew folders (apps started from Finder or
//	         Explorer do not inherit a login shell's PATH)
//	model    ggml-<id>.bin from the official Hugging Face repository
//	         https://huggingface.co/ggerganov/whisper.cpp (STT.LocalModel, or
//	         the best ggml model in LocalDir()/models)
//	run      FFmpeg → 16 kHz mono 16-bit WAV, then (localrun.go)
//	         whisper-cli -m <model> -f audio.wav -l <lang|auto> -t <n> -oj -of out -ml 1 -sow -pp [-sns]
//	         CLI docs: https://github.com/ggml-org/whisper.cpp/blob/master/examples/cli/README.md
//
// The installer (localinstall.go) downloads only from the official sources:
// on Windows the CPU build of the whisper.cpp GitHub releases
// (whisper-bin-x64.zip / whisper-bin-Win32.zip / whisper-bin-win-cpu-arm64.zip)
// and the model from Hugging Face. There is no official macOS (or Linux)
// command-line build — only an XCFramework for apps — so there the program
// comes from Homebrew (`brew install whisper-cpp`, formula «whisper.cpp»,
// built with Metal) and the installer downloads just the model.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/sysx"
)

// ProviderLocal is cfg.STT.Provider for whisper.cpp on this computer.
const ProviderLocal = "local"

// DefaultLocalModel is the model InstallLocal downloads when none is chosen:
// "small" is the smallest model that transcribes Ukrainian decently (tiny and
// base make many mistakes in it), is a ~465 MB download and runs faster than
// real time on any CPU — the Windows build is CPU-only. large-v3-turbo is the
// most accurate choice for Ukrainian on a strong computer or a Mac (Metal).
const DefaultLocalModel = "small"

// ModelInfo describes a whisper.cpp model the installer can download.
type ModelInfo struct {
	ID     string // "small" → ggml-small.bin
	Label  string // short Ukrainian label for the UI
	SizeMB int    // download size in MB (MiB, like Explorer and Finder show it)
	Note   string // quality / speed note (Ukrainian)
}

// localModel is a catalogue model with its exact size and SHA-256 (the LFS
// metadata of the Hugging Face repository, commit 5359861c, 2024-10-29).
type localModel struct {
	ModelInfo
	size   int64
	sha256 string
}

// localModels is the model catalogue, smallest first (a var: tests add a
// tiny fake model).
var localModels = []localModel{
	{ModelInfo{ID: "tiny", Label: "Tiny — найшвидша",
		Note: "Дуже швидка, але в українській багато помилок — хіба щоб перевірити, що все працює."},
		77691713, "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21"},
	{ModelInfo{ID: "base", Label: "Base — швидка",
		Note: "Швидка, проте в українській помітні помилки — краще для англійської."},
		147951465, "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe"},
	{ModelInfo{ID: "small", Label: "Small — рекомендована",
		Note: "Добрий баланс: пристойно розпізнає українську й працює швидше за запис на будь-якому комп'ютері."},
		487601967, "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b"},
	{ModelInfo{ID: "medium", Label: "Medium — точна",
		Note: "Точніша за Small, але в кілька разів повільніша й потребує близько 2 ГБ пам'яті."},
		1533763059, "6c14d5adee5f86394037b4e4e8b59f1673b6cee10e3cf0b11bbdbee79c156208"},
	{ModelInfo{ID: "large-v3-turbo", Label: "Large v3 Turbo — найточніша",
		Note: "Найкраща якість для української. Швидка на Mac і потужному комп'ютері, на слабкому — повільна; потребує близько 2 ГБ пам'яті."},
		1624555275, "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69"},
}

// LocalModels lists the models InstallLocal can download, smallest first.
func LocalModels() []ModelInfo {
	out := make([]ModelInfo, len(localModels))
	for i, m := range localModels {
		out[i] = m.ModelInfo
		out[i].SizeMB = int((m.size + 1<<19) >> 20)
	}
	return out
}

func lookupModel(id string) (localModel, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, m := range localModels {
		if m.ID == id {
			return m, true
		}
	}
	return localModel{}, false
}

// modelFile is the file name of a catalogue model.
func modelFile(id string) string { return "ggml-" + id + ".bin" }

// LocalDir is where InstallLocal puts whisper.cpp by default:
// <settings dir>/whisper (%AppData%\YT Cartell Studio\whisper on Windows,
// ~/Library/Application Support/YT Cartell Studio/whisper on macOS) with
// bin/ (the program and its DLLs) and models/.
func LocalDir() string { return filepath.Join(config.Dir(), "whisper") }

// Seams for tests: where the app lives, the default install folder, PATH
// lookups and the package-manager folders.
var (
	exeDirFn   = sysx.ExeDir
	localDirFn = LocalDir
	lookPath   = exec.LookPath
	systemDirs = defaultSystemDirs
)

// LocalStatus finds the whisper.cpp program and model for cfg (it only looks
// for files, it runs nothing). ok means both are there; hint is a short
// Ukrainian status line for Settings: what is ready or what to do next.
func LocalStatus(cfg config.Config) (bin, model string, ok bool, hint string) {
	bin, berr := findBin(cfg.STT.LocalBin)
	model, merr := findModel(cfg.STT.LocalModel, bin)
	switch {
	case berr != nil:
		return bin, model, false, berr.Error()
	case merr != nil:
		return bin, model, false, merr.Error()
	}
	return bin, model, true, "Готово: whisper.cpp розпізнає мову на цьому комп'ютері · модель " + modelLabel(model)
}

// ---- the program ------------------------------------------------------------------------------

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// binNames are the program's names, newest first: whisper-cli since 1.7.4;
// Homebrew installed it as whisper-cpp before.
func binNames() []string {
	if runtime.GOOS == "windows" {
		return []string{exe("whisper-cli")}
	}
	return []string{"whisper-cli", "whisper-cpp"}
}

// binIn looks for the program in dir and its usual sub-folders (bin/,
// Release/ — the folder inside the official zip). legacy also accepts the
// old "main", only ever inside folders that are whisper's own.
func binIn(dir string, legacy bool) string {
	if dir == "" {
		return ""
	}
	names := binNames()
	if legacy {
		names = append(names, exe("main"))
	}
	for _, sub := range []string{"", "bin", "Release"} {
		for _, n := range names {
			if p := filepath.Join(dir, sub, n); isFile(p) {
				return absPath(p)
			}
		}
	}
	return ""
}

// findBin: the Settings path (a file, or a folder holding the program) wins
// and is never second-guessed; else next to the app, LocalDir, PATH and the
// package-manager folders.
func findBin(configured string) (string, error) {
	if c := cleanPath(configured); c != "" {
		st, err := os.Stat(c)
		switch {
		case err == nil && !st.IsDir():
			return absPath(c), nil
		case err == nil:
			if p := binIn(c, true); p != "" {
				return p, nil
			}
		}
		return "", fmt.Errorf("Програму whisper.cpp не знайдено за шляхом %s — вкажи правильний файл (whisper-cli) або очисти поле й натисни «Встановити»", c)
	}
	if d := exeDirFn(); d != "" {
		if p := binIn(d, false); p != "" {
			return p, nil
		}
		if p := binIn(filepath.Join(d, "whisper"), true); p != "" {
			return p, nil
		}
	}
	if p := binIn(localDirFn(), true); p != "" {
		return p, nil
	}
	for _, n := range binNames() {
		if p, err := lookPath(n); err == nil && p != "" {
			return absPath(p), nil
		}
	}
	for _, d := range systemDirs() {
		for _, n := range binNames() {
			if p := filepath.Join(d, n); isFile(p) {
				return p, nil
			}
		}
	}
	return "", errNoProgram()
}

// errNoProgram tells how to get the program on this OS.
func errNoProgram() error {
	switch runtime.GOOS {
	case "windows":
		return errors.New("whisper.cpp ще не встановлено — вибери модель і натисни «Встановити»: програма (≈ 9 МБ) і модель завантажаться з офіційних сторінок")
	case "darwin":
		return errors.New("На Mac програму whisper.cpp ставить Homebrew: відкрий Термінал і виконай brew install whisper-cpp (Homebrew — brew.sh), потім натисни «Встановити», щоб завантажити модель")
	}
	return errors.New("Встанови whisper.cpp (програма whisper-cli) через менеджер пакетів або Homebrew — brew install whisper-cpp, потім натисни «Встановити», щоб завантажити модель")
}

// defaultSystemDirs are package-manager folders that may be missing from
// the PATH of an app started from Finder or Explorer.
func defaultSystemDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		var ds []string
		if home != "" {
			ds = append(ds, filepath.Join(home, "scoop", "shims"))
		}
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			ds = append(ds, filepath.Join(v, "Microsoft", "WinGet", "Links"))
		}
		return ds
	case "darwin":
		return []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}
	}
	ds := []string{"/home/linuxbrew/.linuxbrew/bin", "/usr/local/bin", "/usr/bin"}
	if home != "" {
		ds = append(ds, filepath.Join(home, ".linuxbrew", "bin"), filepath.Join(home, ".local", "bin"))
	}
	return ds
}

// ---- the model --------------------------------------------------------------------------------

// findModel: the Settings path (a file, or a folder of models) wins; else the
// best model in LocalDir()/models, next to the app or next to the program.
func findModel(configured, bin string) (string, error) {
	if c := cleanPath(configured); c != "" {
		st, err := os.Stat(c)
		switch {
		case err == nil && !st.IsDir():
			return absPath(c), nil
		case err == nil:
			if p := modelIn(c); p != "" {
				return p, nil
			}
		}
		return "", fmt.Errorf("Модель не знайдено за шляхом %s — вкажи файл ggml-….bin або очисти поле й натисни «Встановити»", c)
	}
	dirs := []string{filepath.Join(localDirFn(), "models"), localDirFn()}
	if d := exeDirFn(); d != "" {
		dirs = append(dirs, filepath.Join(d, "whisper", "models"), filepath.Join(d, "models"))
	}
	if bin != "" {
		b := filepath.Dir(bin)
		dirs = append(dirs, filepath.Join(b, "models"), filepath.Join(filepath.Dir(b), "models"))
	}
	for _, d := range dirs {
		if p := modelIn(d); p != "" {
			return p, nil
		}
	}
	return "", errors.New("Модель розпізнавання ще не завантажена — вибери модель і натисни «Встановити»")
}

// modelIn picks the best whisper model in dir: catalogue models from the
// most accurate down, then other multilingual ggml-*.bin files, then the
// English-only ones (*.en.bin). VAD models (ggml-silero-*) and unfinished
// downloads are not whisper models.
func modelIn(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	have := map[string]bool{}
	var other, english []string
	for _, e := range ents {
		n := e.Name()
		ln := strings.ToLower(n)
		if e.IsDir() || !strings.HasPrefix(ln, "ggml-") || !strings.HasSuffix(ln, ".bin") ||
			strings.Contains(ln, "silero") || strings.Contains(ln, "-vad") {
			continue
		}
		have[ln] = true
		switch {
		case isCatalogueFile(ln):
		case strings.HasSuffix(ln, ".en.bin"):
			english = append(english, n)
		default:
			other = append(other, n)
		}
	}
	for i := len(localModels) - 1; i >= 0; i-- {
		if f := modelFile(localModels[i].ID); have[f] {
			if p := filepath.Join(dir, f); isFile(p) {
				return absPath(p)
			}
		}
	}
	sort.Strings(other)
	sort.Strings(english)
	for _, n := range append(other, english...) {
		if p := filepath.Join(dir, n); isFile(p) {
			return absPath(p)
		}
	}
	return ""
}

func isCatalogueFile(lowerName string) bool {
	for _, m := range localModels {
		if lowerName == modelFile(m.ID) {
			return true
		}
	}
	return false
}

// modelLabel names a model file for messages: the catalogue id ("small") or
// the file name.
func modelLabel(path string) string {
	base := filepath.Base(path)
	lb := strings.ToLower(base)
	for _, m := range localModels {
		if lb == modelFile(m.ID) {
			return m.ID
		}
	}
	return base
}

// ---- helpers ----------------------------------------------------------------------------------

func cleanPath(p string) string { return strings.Trim(strings.TrimSpace(p), `"`) }

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
