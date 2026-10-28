package tts

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
)

// Piper voice definition
type piperVoiceDef struct {
	ID        string
	Name      string
	Gender    string
	Language  string
	ModelID   string
	SpeakerID int
	Note      string
}

var piperVoiceCatalog = []piperVoiceDef{
	{
		ID:        "tetiana",
		Name:      "Тетяна (Українська, High)",
		Gender:    "female",
		Language:  "uk",
		ModelID:   "uk_UA-tetiana-high",
		SpeakerID: 0,
		Note:      "Висока якість (High), природний жіночий голос",
	},
	{
		ID:        "mykyta",
		Name:      "Микита (Українська, High)",
		Gender:    "male",
		Language:  "uk",
		ModelID:   "uk_UA-mykyta-high",
		SpeakerID: 0,
		Note:      "Висока якість (High), природний чоловічий голос",
	},
	{
		ID:        "ukrainian_tts_tetiana",
		Name:      "Тетяна (ukrainian_tts, Medium)",
		Gender:    "female",
		Language:  "uk",
		ModelID:   "uk_UA-ukrainian_tts-medium",
		SpeakerID: 2,
		Note:      "Природний жіночий голос, середня якість",
	},
	{
		ID:        "ukrainian_tts_mykyta",
		Name:      "Микита (ukrainian_tts, Medium)",
		Gender:    "male",
		Language:  "uk",
		ModelID:   "uk_UA-ukrainian_tts-medium",
		SpeakerID: 1,
		Note:      "Природний чоловічий голос, середня якість",
	},
	{
		ID:        "lada",
		Name:      "Лада (Українська, легка)",
		Gender:    "female",
		Language:  "uk",
		ModelID:   "uk_UA-lada-x_low",
		SpeakerID: 0,
		Note:      "Швидка та легка модель (~20 МБ)",
	},
	{
		ID:        "lessac",
		Name:      "Lessac (English)",
		Gender:    "male",
		Language:  "en",
		ModelID:   "en_US-lessac-medium",
		SpeakerID: 0,
		Note:      "Natural US English voice",
	},
	{
		ID:        "amy",
		Name:      "Amy (English)",
		Gender:    "female",
		Language:  "en",
		ModelID:   "en_US-amy-medium",
		SpeakerID: 0,
		Note:      "Clear female US English voice",
	},
}

var modelURLs = map[string]string{
	"uk_UA-tetiana-high":         "https://huggingface.co/rhasspy/piper-voices/resolve/main/uk/uk_UA/tetiana/high/uk_UA-tetiana-high",
	"uk_UA-mykyta-high":          "https://huggingface.co/rhasspy/piper-voices/resolve/main/uk/uk_UA/mykyta/high/uk_UA-mykyta-high",
	"uk_UA-ukrainian_tts-medium": "https://huggingface.co/rhasspy/piper-voices/resolve/main/uk/uk_UA/ukrainian_tts/medium/uk_UA-ukrainian_tts-medium",
	"uk_UA-lada-x_low":           "https://huggingface.co/rhasspy/piper-voices/resolve/main/uk/uk_UA/lada/x_low/uk_UA-lada-x_low",
	"en_US-lessac-medium":        "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium",
	"en_US-amy-medium":           "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/amy/medium/en_US-amy-medium",
}

var piperDownloadMu sync.Mutex

func piperDir() string {
	return filepath.Join(config.Dir(), "piper")
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// FindPiper searches for the Piper TTS binary across system PATH, standard directories,
// user local bin, virtual environments, and the app's local piper directory.
func FindPiper(customPath string) (string, error) {
	if strings.TrimSpace(customPath) != "" {
		p := strings.Trim(strings.TrimSpace(customPath), `"`)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			ensurePiperSharedLibs(filepath.Dir(p))
			return p, nil
		}
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			cand := filepath.Join(p, exeName("piper"))
			if _, err := os.Stat(cand); err == nil {
				ensurePiperSharedLibs(filepath.Dir(cand))
				return cand, nil
			}
		}
	}

	for _, cand := range []string{"piper", "piper-tts"} {
		if path, err := exec.LookPath(cand); err == nil {
			ensurePiperSharedLibs(filepath.Dir(path))
			return path, nil
		}
	}

	pDir := piperDir()
	appCands := []string{
		filepath.Join(pDir, "piper", exeName("piper")),
		filepath.Join(pDir, exeName("piper")),
		filepath.Join(pDir, "bin", exeName("piper")),
	}
	for _, c := range appCands {
		if _, err := os.Stat(c); err == nil {
			ensurePiperSharedLibs(filepath.Dir(c))
			return c, nil
		}
	}

	var userCands []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		userCands = append(userCands,
			filepath.Join(home, ".local", "bin", exeName("piper")),
			filepath.Join(home, ".local", "bin", exeName("piper-tts")),
			filepath.Join(home, "piper", exeName("piper")),
			filepath.Join(home, ".cargo", "bin", exeName("piper")),
			filepath.Join(home, "piper_models", exeName("piper")),
		)
	}

	systemCands := []string{
		"/usr/local/bin/piper",
		"/usr/local/bin/piper-tts",
		"/usr/bin/piper",
		"/usr/bin/piper-tts",
		"/bin/piper",
		"/opt/piper/piper",
		"/snap/bin/piper",
		"/root/piper_models/piper",
	}

	for _, c := range append(userCands, systemCands...) {
		if _, err := os.Stat(c); err == nil {
			ensurePiperSharedLibs(filepath.Dir(c))
			return c, nil
		}
	}

	return "", fmt.Errorf("Програму Piper TTS не знайдено на комп'ютері. Натисніть «Завантажити Piper» у Налаштуваннях.")
}

func findPiperBin(customPath string) (string, error) {
	return FindPiper(customPath)
}

// PiperStatus reports whether Piper TTS is ready and returns its location.
func PiperStatus(customPath string) (binPath string, ok bool, hint string) {
	bin, err := FindPiper(customPath)
	if err != nil {
		return "", false, "Програму Piper TTS не знайдено на комп'ютері. Натисніть «Завантажити Piper» для автоматичного встановлення."
	}
	return bin, true, fmt.Sprintf("Piper знайдено: %s", bin)
}

// findLocalModel checks if the ONNX model and its JSON config exist in local directories.
func findLocalModel(modelID string) (modelPath, configPath string, found bool) {
	var searchDirs []string
	pDir := piperDir()
	searchDirs = append(searchDirs,
		pDir,
		filepath.Join(pDir, "models"),
		filepath.Join(pDir, "piper"),
	)
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		searchDirs = append(searchDirs,
			filepath.Join(home, "piper_models"),
			filepath.Join(home, ".local", "share", "piper", "models"),
		)
	}
	searchDirs = append(searchDirs,
		"/root/piper_models",
		"./piper_models",
		"/usr/share/piper/models",
	)

	for _, d := range searchDirs {
		mPath := filepath.Join(d, modelID+".onnx")
		cPath := filepath.Join(d, modelID+".onnx.json")
		if _, err1 := os.Stat(mPath); err1 == nil {
			if _, err2 := os.Stat(cPath); err2 == nil {
				return mPath, cPath, true
			}
		}
	}
	return "", "", false
}

// resolvePiperVoiceAndModel chooses the best voice definition and model paths,
// taking advantage of already available local models.
func resolvePiperVoiceAndModel(ctx context.Context, s settings, voiceID string) (chosen piperVoiceDef, modelPath, configPath string, err error) {
	targetVoice := strings.ToLower(strings.TrimSpace(voiceID))
	if targetVoice == "" {
		targetVoice = strings.ToLower(strings.TrimSpace(s.voice))
	}
	if targetVoice == "" {
		targetVoice = "tetiana"
	}

	for i := range piperVoiceCatalog {
		if strings.EqualFold(piperVoiceCatalog[i].ID, targetVoice) {
			chosen = piperVoiceCatalog[i]
			break
		}
	}
	if chosen.ID == "" {
		chosen = piperVoiceCatalog[0]
	}

	modelID := chosen.ModelID
	if strings.TrimSpace(s.model) != "" && strings.TrimSpace(s.model) != "Автоматично" {
		modelID = strings.TrimSpace(s.model)
	}

	// Smart fallback: if requested voice is tetiana and uk_UA-tetiana-high is not local
	// but uk_UA-ukrainian_tts-medium is local (or vice versa), pick the local one!
	if strings.TrimSpace(s.model) == "" || strings.TrimSpace(s.model) == "Автоматично" {
		if targetVoice == "tetiana" {
			if mp, cp, ok := findLocalModel("uk_UA-tetiana-high"); ok {
				return piperVoiceDef{ID: "tetiana", Name: "Тетяна", ModelID: "uk_UA-tetiana-high", SpeakerID: 0}, mp, cp, nil
			}
			if mp, cp, ok := findLocalModel("uk_UA-ukrainian_tts-medium"); ok {
				return piperVoiceDef{ID: "tetiana", Name: "Тетяна", ModelID: "uk_UA-ukrainian_tts-medium", SpeakerID: 2}, mp, cp, nil
			}
		} else if targetVoice == "mykyta" {
			if mp, cp, ok := findLocalModel("uk_UA-mykyta-high"); ok {
				return piperVoiceDef{ID: "mykyta", Name: "Микита", ModelID: "uk_UA-mykyta-high", SpeakerID: 0}, mp, cp, nil
			}
			if mp, cp, ok := findLocalModel("uk_UA-ukrainian_tts-medium"); ok {
				return piperVoiceDef{ID: "mykyta", Name: "Микита", ModelID: "uk_UA-ukrainian_tts-medium", SpeakerID: 1}, mp, cp, nil
			}
		}
	}

	// Check if the chosen model exists locally
	if mp, cp, ok := findLocalModel(modelID); ok {
		return chosen, mp, cp, nil
	}

	// Otherwise, ensure model is downloaded into piperDir()
	mp, cp, err := ensurePiperModel(ctx, modelID)
	return chosen, mp, cp, err
}

func ensurePiperModel(ctx context.Context, modelID string) (modelPath, configPath string, err error) {
	// 1. Check local models first
	if mp, cp, ok := findLocalModel(modelID); ok {
		return mp, cp, nil
	}

	dir := piperDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("не вдалося створити каталог для моделей piper: %w", err)
	}

	modelPath = filepath.Join(dir, modelID+".onnx")
	configPath = filepath.Join(dir, modelID+".onnx.json")

	// If already present in dir, return
	if _, err1 := os.Stat(modelPath); err1 == nil {
		if _, err2 := os.Stat(configPath); err2 == nil {
			return modelPath, configPath, nil
		}
	}

	piperDownloadMu.Lock()
	defer piperDownloadMu.Unlock()

	// Recheck after lock
	if _, err1 := os.Stat(modelPath); err1 == nil {
		if _, err2 := os.Stat(configPath); err2 == nil {
			return modelPath, configPath, nil
		}
	}

	baseURL, ok := modelURLs[modelID]
	if !ok {
		return "", "", fmt.Errorf("невідома модель piper: %s", modelID)
	}

	client := &http.Client{Timeout: 180 * time.Second}

	// Download config first
	if _, err := os.Stat(configPath); err != nil {
		if err := downloadFile(ctx, client, baseURL+".onnx.json", configPath); err != nil {
			return "", "", fmt.Errorf("не вдалося завантажити конфіг моделі: %w", err)
		}
	}

	// Download model weights
	if _, err := os.Stat(modelPath); err != nil {
		if err := downloadFile(ctx, client, baseURL+".onnx", modelPath); err != nil {
			return "", "", fmt.Errorf("не вдалося завантажити ваги моделі: %w", err)
		}
	}

	return modelPath, configPath, nil
}

func downloadFile(ctx context.Context, client *http.Client, url, dest string) error {
	part := dest + ".part"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP помилка %d при завантаженні %s", resp.StatusCode, url)
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(part)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(part)
		return closeErr
	}
	return os.Rename(part, dest)
}

// piperReleaseAsset returns the official GitHub release URL for rhasspy/piper.
func piperReleaseAsset() (url string, archType string, err error) {
	const base = "https://github.com/rhasspy/piper/releases/download/2023.11.14-2/"
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return base + "piper_linux_x86_64.tar.gz", "tar.gz", nil
		case "arm64":
			return base + "piper_linux_aarch64.tar.gz", "tar.gz", nil
		case "arm":
			return base + "piper_linux_armv7l.tar.gz", "tar.gz", nil
		default:
			return "", "", fmt.Errorf("непідтримувана архітектура Linux: %s", runtime.GOARCH)
		}
	case "windows":
		switch runtime.GOARCH {
		case "amd64":
			return base + "piper_windows_amd64.zip", "zip", nil
		default:
			return "", "", fmt.Errorf("непідтримувана архітектура Windows: %s", runtime.GOARCH)
		}
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			return base + "piper_macos_aarch64.tar.gz", "tar.gz", nil
		case "amd64":
			return base + "piper_macos_x64.tar.gz", "tar.gz", nil
		default:
			return "", "", fmt.Errorf("непідтримувана архітектура macOS: %s", runtime.GOARCH)
		}
	default:
		return "", "", fmt.Errorf("непідтримувана ОС: %s", runtime.GOOS)
	}
}

// InstallPiper downloads and extracts the official Piper TTS binary bundle for the current OS.
func InstallPiper(ctx context.Context, progress func(pct int, msg string)) (string, error) {
	piperDownloadMu.Lock()
	defer piperDownloadMu.Unlock()

	rep := func(pct int, msg string) {
		if progress != nil {
			progress(pct, msg)
		}
	}

	rep(5, "Визначаємо сумісну версію Piper…")
	assetURL, archType, err := piperReleaseAsset()
	if err != nil {
		return "", err
	}

	dir := piperDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("не вдалося створити каталог %s: %w", dir, err)
	}

	dlDir := filepath.Join(dir, ".download")
	_ = os.MkdirAll(dlDir, 0o755)
	archivePath := filepath.Join(dlDir, "piper-release."+archType)
	defer os.RemoveAll(dlDir)

	rep(10, "Завантажуємо Piper з GitHub…")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("не вдалося з'єднатися з сервером завантаження: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("сервер повернув помилку %d при завантаженні Piper", resp.StatusCode)
	}

	partPath := archivePath + ".part"
	outFile, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}

	totalSize := resp.ContentLength
	buf := make([]byte, 64*1024)
	var downloaded int64
	lastRep := time.Now()

	for {
		if ctx.Err() != nil {
			outFile.Close()
			_ = os.Remove(partPath)
			return "", ctx.Err()
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := outFile.Write(buf[:n]); werr != nil {
				outFile.Close()
				_ = os.Remove(partPath)
				return "", werr
			}
			downloaded += int64(n)
			if totalSize > 0 && time.Since(lastRep) > 100*time.Millisecond {
				pct := int(10 + float64(downloaded)/float64(totalSize)*60)
				rep(pct, fmt.Sprintf("Завантажуємо Piper: %d%% (%s з %s)…", pct, fmtSizeMB(downloaded), fmtSizeMB(totalSize)))
				lastRep = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			outFile.Close()
			_ = os.Remove(partPath)
			return "", rerr
		}
	}
	outFile.Close()

	if err := os.Rename(partPath, archivePath); err != nil {
		return "", err
	}

	rep(75, "Розпаковуємо Piper…")
	if archType == "tar.gz" {
		if err := extractTarGzPiper(archivePath, dir); err != nil {
			return "", fmt.Errorf("помилка розпакування tar.gz: %w", err)
		}
	} else if archType == "zip" {
		if err := extractZipPiper(archivePath, dir); err != nil {
			return "", fmt.Errorf("помилка розпакування zip: %w", err)
		}
	}

	rep(85, "Перевіряємо запуск програми…")
	binPath, err := FindPiper("")
	if err != nil {
		return "", fmt.Errorf("розпаковано, але виконуваний файл Piper не знайдено: %w", err)
	}

	// Ensure executable permission
	_ = os.Chmod(binPath, 0o755)
	ensurePiperSharedLibs(filepath.Dir(binPath))

	// Quick execution probe
	probeCmd := piperCmd(ctx, binPath, "--help")
	var probeErrBuf strings.Builder
	probeCmd.Stderr = &probeErrBuf
	if err := probeCmd.Run(); err != nil {
		return "", fmt.Errorf("помилка перевірки запуску Piper (%s): %w (%s)", binPath, err, strings.TrimSpace(probeErrBuf.String()))
	}

	rep(92, "Перевіряємо модель голосу…")
	// Ensure at least one Ukrainian model is ready
	_, _, _, err = resolvePiperVoiceAndModel(ctx, settings{}, "tetiana")
	if err != nil {
		// Log but don't fail: piper binary itself is already working
		rep(98, "Piper готовий (модель буде завантажено автоматично)")
	}

	rep(100, "Готово: Piper встановлено та готовий до роботи")
	return binPath, nil
}

func fmtSizeMB(bytes int64) string {
	mb := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%.1f МБ", mb)
}

func extractTarGzPiper(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		target := filepath.Join(dst, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			link := strings.Trim(strings.ReplaceAll(header.Linkname, `\`, "/"), "/")
			cleanLink := filepath.Clean(filepath.FromSlash(link))
			if !strings.HasPrefix(cleanLink, "/") && !strings.HasPrefix(cleanLink, `\`) {
				_ = os.Remove(target)
				_ = os.Symlink(header.Linkname, target)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode)
			if mode&0o111 != 0 {
				mode = 0o755
			} else {
				mode = 0o644
			}
			w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			w.Close()
		}
	}
	return nil
}

func extractZipPiper(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := filepath.Clean(filepath.FromSlash(f.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		target := filepath.Join(dst, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(w, rc)
		rc.Close()
		w.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func ensurePiperSharedLibs(dir string) {
	if runtime.GOOS != "linux" {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	fileNames := make([]string, 0, len(ents))
	for _, e := range ents {
		fileNames = append(fileNames, e.Name())
	}

	targets := []struct {
		prefix string
		links  []string
	}{
		{"libespeak-ng.so.", []string{"libespeak-ng.so", "libespeak-ng.so.1"}},
		{"libpiper_phonemize.so.", []string{"libpiper_phonemize.so", "libpiper_phonemize.so.1"}},
		{"libonnxruntime.so.", []string{"libonnxruntime.so", "libonnxruntime.so.1.14.1"}},
	}

	for _, t := range targets {
		var realFile string
		for _, name := range fileNames {
			if strings.HasPrefix(name, t.prefix) && !strings.HasSuffix(name, ".so") && !strings.HasSuffix(name, ".1") {
				realFile = name
				break
			}
		}
		if realFile == "" {
			for _, name := range fileNames {
				if strings.HasPrefix(name, t.prefix) {
					realFile = name
					break
				}
			}
		}
		if realFile == "" {
			continue
		}
		for _, linkName := range t.links {
			linkPath := filepath.Join(dir, linkName)
			if _, err := os.Stat(linkPath); err != nil {
				_ = os.Remove(linkPath)
				if err := os.Symlink(realFile, linkPath); err != nil {
					_ = copyPiperFile(filepath.Join(dir, realFile), linkPath)
				}
			}
		}
	}
}

func copyPiperFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func piperCmd(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	if runtime.GOOS == "linux" {
		binDir := filepath.Dir(bin)
		ensurePiperSharedLibs(binDir)
		cmd.Env = os.Environ()
		var newLd string
		if curLd := os.Getenv("LD_LIBRARY_PATH"); curLd != "" {
			newLd = binDir + ":" + curLd
		} else {
			newLd = binDir
		}
		cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+newLd)
		espeakData := filepath.Join(binDir, "espeak-ng-data")
		if _, err := os.Stat(espeakData); err == nil {
			cmd.Env = append(cmd.Env, "ESPEAK_DATA_PATH="+espeakData)
		}
	}
	return cmd
}

func (c *Client) piperVoices(ctx context.Context, s settings) (VoiceList, error) {
	voices := make([]Voice, 0, len(piperVoiceCatalog))
	for _, v := range piperVoiceCatalog {
		voices = append(voices, Voice{
			ID:       v.ID,
			Name:     v.Name,
			Category: "local",
			Gender:   v.Gender,
			Language: v.Language,
			Usable:   true,
		})
	}
	return VoiceList{
		Provider:  "piper",
		CharsLeft: -1,
		Voices:    voices,
	}, nil
}

func (c *Client) piper(ctx context.Context, s settings, text, voiceID string, opt SynthOptions) ([]byte, error) {
	piperBin, err := FindPiper(s.base)
	if err != nil {
		return nil, err
	}

	chosen, modelPath, configPath, err := resolvePiperVoiceAndModel(ctx, s, voiceID)
	if err != nil {
		return nil, fmt.Errorf("помилка моделі piper: %w", err)
	}

	// Temporary output files
	tmpDir, err := os.MkdirTemp("", "ytc-piper-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	wavPath := filepath.Join(tmpDir, "out.wav")
	mp3Path := filepath.Join(tmpDir, "out.mp3")

	args := []string{
		"-m", modelPath,
		"-c", configPath,
		"-f", wavPath,
	}

	if chosen.SpeakerID > 0 {
		args = append(args, "-s", strconv.Itoa(chosen.SpeakerID))
	}

	if opt.Speed > 0 && opt.Speed != 1.0 {
		lengthScale := 1.0 / opt.Speed
		args = append(args, "--length-scale", fmt.Sprintf("%.2f", lengthScale))
	}

	cmd := piperCmd(ctx, piperBin, args...)
	cmd.Stdin = strings.NewReader(text)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("помилка синтезу мови piper (%w): %s", err, errBuf.String())
	}

	// Convert WAV to MP3 using ffmpeg
	ffCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-v", "error", "-i", wavPath, "-c:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", mp3Path)
	if err := ffCmd.Run(); err != nil {
		// If ffmpeg fails, try reading raw wav
		data, rerr := os.ReadFile(wavPath)
		if rerr == nil && len(data) > 0 {
			return data, nil
		}
		return nil, fmt.Errorf("помилка конвертації аудіо у mp3: %w", err)
	}

	data, err := os.ReadFile(mp3Path)
	if err != nil {
		return nil, fmt.Errorf("не вдалося прочитати згенерований аудіофайл: %w", err)
	}
	return data, nil
}

func (c *Client) piperProbe(ctx context.Context, s settings) probeResult {
	bin, err := FindPiper(s.base)
	if err != nil {
		return probeResult{used: -1, err: &apiErr{kind: failTransient, msg: err.Error()}}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return probeResult{used: -1, err: &apiErr{kind: failTransient, msg: "FFmpeg не знайдено в системі (потрібен для конвертації аудіо)"}}
	}
	return probeResult{used: 0, limit: -1, info: fmt.Sprintf("Готово: Piper знайдено (%s) · локальна озвучка готова", bin)}
}
