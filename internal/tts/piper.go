package tts

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
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
		Name:      "Тетяна (Українська)",
		Gender:    "female",
		Language:  "uk",
		ModelID:   "uk_UA-ukrainian_tts-medium",
		SpeakerID: 2,
		Note:      "Природний жіночий голос, висока якість",
	},
	{
		ID:        "mykyta",
		Name:      "Микита (Українська)",
		Gender:    "male",
		Language:  "uk",
		ModelID:   "uk_UA-ukrainian_tts-medium",
		SpeakerID: 1,
		Note:      "Природний чоловічий голос, висока якість",
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
	"uk_UA-ukrainian_tts-medium": "https://huggingface.co/rhasspy/piper-voices/resolve/main/uk/uk_UA/ukrainian_tts/medium/uk_UA-ukrainian_tts-medium",
	"uk_UA-lada-x_low":           "https://huggingface.co/rhasspy/piper-voices/resolve/main/uk/uk_UA/lada/x_low/uk_UA-lada-x_low",
	"en_US-lessac-medium":        "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/lessac/medium/en_US-lessac-medium",
	"en_US-amy-medium":           "https://huggingface.co/rhasspy/piper-voices/resolve/main/en/en_US/amy/medium/en_US-amy-medium",
}

var piperDownloadMu sync.Mutex

func piperDir() string {
	return filepath.Join(config.Dir(), "piper")
}

func findPiperBin(customPath string) (string, error) {
	if strings.TrimSpace(customPath) != "" {
		if _, err := os.Stat(customPath); err == nil {
			return customPath, nil
		}
	}
	for _, cand := range []string{"piper", "piper-tts", "/usr/local/bin/piper", "/usr/bin/piper"} {
		if path, err := exec.LookPath(cand); err == nil {
			return path, nil
		}
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", fmt.Errorf("Програму Piper TTS не знайдено на комп'ютері. Встановіть її через pip: 'pip install piper-tts' або вкажіть шлях у Налаштуваннях.")
}

func ensurePiperModel(ctx context.Context, modelID string) (modelPath, configPath string, err error) {
	dir := piperDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("не вдалося створити каталог для моделей piper: %w", err)
	}

	modelPath = filepath.Join(dir, modelID+".onnx")
	configPath = filepath.Join(dir, modelID+".onnx.json")

	// If already present, return
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

	client := &http.Client{Timeout: 120 * time.Second}

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
	piperBin, err := findPiperBin(s.base)
	if err != nil {
		return nil, err
	}

	// Determine voice and model
	targetVoice := strings.ToLower(strings.TrimSpace(voiceID))
	if targetVoice == "" {
		targetVoice = strings.ToLower(strings.TrimSpace(s.voice))
	}
	if targetVoice == "" {
		targetVoice = "tetiana"
	}

	var chosen *piperVoiceDef
	for i := range piperVoiceCatalog {
		if strings.EqualFold(piperVoiceCatalog[i].ID, targetVoice) {
			chosen = &piperVoiceCatalog[i]
			break
		}
	}
	if chosen == nil {
		chosen = &piperVoiceCatalog[0] // tetiana
	}

	modelID := chosen.ModelID
	if strings.TrimSpace(s.model) != "" && strings.TrimSpace(s.model) != "Автоматично" {
		modelID = strings.TrimSpace(s.model)
	}

	modelPath, configPath, err := ensurePiperModel(ctx, modelID)
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
		// length_scale is inversely proportional to speed
		lengthScale := 1.0 / opt.Speed
		args = append(args, "--length-scale", fmt.Sprintf("%.2f", lengthScale))
	}

	cmd := exec.CommandContext(ctx, piperBin, args...)
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
	bin, err := findPiperBin(s.base)
	if err != nil {
		return probeResult{used: -1, err: &apiErr{kind: failTransient, msg: err.Error()}}
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return probeResult{used: -1, err: &apiErr{kind: failTransient, msg: "FFmpeg не знайдено в системі (потрібен для конвертації аудіо)"}}
	}
	return probeResult{used: 0, limit: -1, info: fmt.Sprintf("Готово: Piper знайдено (%s) · локальна озвучка готова", filepath.Base(bin))}
}

