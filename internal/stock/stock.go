package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Config holds keys and options for stock video search across the 4 providers.
type Config struct {
	Provider    string // "pexels", "pixabay", "coverr", "wikimedia", "auto"
	PexelsKey   string
	PixabayKey  string
	CoverrKey   string
	Orientation string // "landscape", "portrait", "all"
}

// DownloadFile downloads media stream from URL into memory.
func DownloadFile(ctx context.Context, downloadURL string) ([]byte, error) {
	if downloadURL == "" {
		return nil, fmt.Errorf("empty download url")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; YTCartellStudio/2.4)")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download video stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download video failed (status %d)", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read video data: %w", err)
	}
	return data, nil
}

// PickBestFile selects the most suitable MP4 stream (prefers 1080p, then 720p).
func PickBestFile(item VideoItem) *VideoFile {
	if len(item.Files) == 0 {
		return nil
	}
	var best *VideoFile
	for i := range item.Files {
		f := &item.Files[i]
		if best == nil {
			best = f
			continue
		}
		// Prefer around 1920x1080 (HD / 1080p)
		if f.Width >= 1280 && f.Width <= 1920 && best.Width < 1280 {
			best = f
		} else if f.Width > best.Width && best.Width < 1280 {
			best = f
		}
	}
	return best
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9\sа-яА-ЯіІїЇєЄґҐ]`)
var fluffWords = map[string]bool{
	"cinematic": true, "photorealistic": true, "hyperrealistic": true,
	"4k": true, "8k": true, "hd": true, "uhd": true, "shot": true,
	"close": true, "up": true, "closeup": true, "wide": true, "angle": true,
	"scene": true, "frame": true, "clip": true, "camera": true, "lens": true,
	"masterpiece": true, "trending": true, "award": true, "winning": true,
	"detailed": true, "high": true, "quality": true, "slow": true, "motion": true,
	"pan": true, "zoom": true, "tilt": true, "tracking": true, "push": true, "pull": true,
	"view": true, "style": true, "aesthetic": true, "render": true, "octane": true,
	"unreal": true, "engine": true,
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if (r >= 0x0400 && r <= 0x04FF) || (r >= 0x0500 && r <= 0x052F) {
			return true
		}
	}
	return false
}

func translateCyrillic(ctx context.Context, text string) string {
	if !hasCyrillic(text) {
		return text
	}
	tCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	u := "https://api.mymemory.translated.net/get?q=" + url.QueryEscape(text) + "&langpair=uk|en"
	req, err := http.NewRequestWithContext(tCtx, http.MethodGet, u, nil)
	if err != nil {
		return text
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; YTCartellStudio/2.4)")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return text
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		var res struct {
			ResponseData struct {
				TranslatedText string `json:"translatedText"`
			} `json:"responseData"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && strings.TrimSpace(res.ResponseData.TranslatedText) != "" {
			return res.ResponseData.TranslatedText
		}
	}
	return text
}

// ExtractKeywords simplifies complex AI prompts into 2-5 essential stock search keywords.
func ExtractKeywords(prompt string) string {
	cleaned := nonAlphanumeric.ReplaceAllString(prompt, " ")
	words := strings.Fields(strings.ToLower(cleaned))
	var result []string
	for _, w := range words {
		if len(w) < 3 || fluffWords[w] {
			continue
		}
		result = append(result, w)
		if len(result) >= 4 {
			break
		}
	}
	if len(result) == 0 {
		return "nature landscape"
	}
	return strings.Join(result, " ")
}

// Search queries the configured stock providers (Pexels, Pixabay, Coverr, Wikimedia).
func Search(ctx context.Context, cfg Config, opts SearchOptions) ([]VideoItem, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "auto"
	}

	var tryOrder []string
	switch provider {
	case "pexels":
		tryOrder = []string{"pexels"}
	case "pixabay":
		tryOrder = []string{"pixabay"}
	case "coverr":
		tryOrder = []string{"coverr"}
	case "wikimedia":
		tryOrder = []string{"wikimedia"}
	default: // "auto"
		tryOrder = make([]string, 0, 4)
		if strings.TrimSpace(cfg.PexelsKey) != "" {
			tryOrder = append(tryOrder, "pexels")
		}
		if strings.TrimSpace(cfg.PixabayKey) != "" {
			tryOrder = append(tryOrder, "pixabay")
		}
		if strings.TrimSpace(cfg.CoverrKey) != "" {
			tryOrder = append(tryOrder, "coverr")
		}
		tryOrder = append(tryOrder, "wikimedia")
	}

	var lastErr error
	for _, p := range tryOrder {
		var items []VideoItem
		var err error

		switch p {
		case "pexels":
			if strings.TrimSpace(cfg.PexelsKey) != "" {
				items, err = SearchPexels(ctx, strings.TrimSpace(cfg.PexelsKey), opts)
			} else if provider == "pexels" {
				err = fmt.Errorf("pexels: потрібен API ключ у налаштуваннях")
			}
		case "pixabay":
			if strings.TrimSpace(cfg.PixabayKey) != "" {
				items, err = SearchPixabay(ctx, strings.TrimSpace(cfg.PixabayKey), opts)
			} else if provider == "pixabay" {
				err = fmt.Errorf("pixabay: потрібен API ключ у налаштуваннях")
			}
		case "coverr":
			if strings.TrimSpace(cfg.CoverrKey) != "" {
				items, err = SearchCoverr(ctx, strings.TrimSpace(cfg.CoverrKey), opts)
			} else if provider == "coverr" {
				err = fmt.Errorf("coverr: потрібен API ключ у налаштуваннях")
			}
		case "wikimedia":
			items, err = SearchWikimedia(ctx, opts)
		}

		if err == nil && len(items) > 0 {
			return items, nil
		}
		if err != nil {
			lastErr = err
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("не знайдено стокових відео за запитом %q", opts.Query)
}

// SearchAndDownload searches for a matching clip across stock providers and downloads the video bytes.
func SearchAndDownload(ctx context.Context, cfg Config, rawPrompt string, orientation string) ([]byte, *VideoItem, error) {
	cleanPrompt := strings.ReplaceAll(rawPrompt, "@", "")
	translated := translateCyrillic(ctx, cleanPrompt)

	keywords := ExtractKeywords(translated)
	opts := SearchOptions{
		Query:       keywords,
		Orientation: orientation,
		PerPage:     10,
	}

	items, err := Search(ctx, cfg, opts)
	if (err != nil || len(items) == 0) && orientation != "" {
		opts.Orientation = ""
		items, err = Search(ctx, cfg, opts)
	}
	if len(items) == 0 {
		words := strings.Fields(keywords)
		if len(words) > 1 {
			opts.Query = words[0]
			opts.Orientation = ""
			items, err = Search(ctx, cfg, opts)
		}
	}
	if len(items) == 0 {
		opts.Query = "nature landscape"
		opts.Orientation = ""
		items, err = Search(ctx, cfg, opts)
	}
	if len(items) == 0 {
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("стокове відео не знайдено за запитом %q", keywords)
	}

	// Pick first item that has playable files
	for i := range items {
		item := &items[i]
		bestFile := PickBestFile(*item)
		if bestFile != nil && bestFile.Link != "" {
			data, err := DownloadFile(ctx, bestFile.Link)
			if err == nil && len(data) > 0 {
				return data, item, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("не вдалося завантажити потік відео зі знайдених результатів")
}
