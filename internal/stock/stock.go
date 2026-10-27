package stock

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Config holds keys and options for stock video search.
type Config struct {
	Provider    string // "pexels", "pixabay", "auto"
	PexelsKey   string
	PixabayKey  string
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
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; YTCartellStudio/2.1)")

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

// Search queries the configured stock providers for video clips.
func Search(ctx context.Context, cfg Config, opts SearchOptions) ([]VideoItem, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" || provider == "auto" {
		if cfg.PexelsKey != "" {
			provider = "pexels"
		} else if cfg.PixabayKey != "" {
			provider = "pixabay"
		} else {
			return nil, fmt.Errorf("no stock API key provided (configure Pexels or Pixabay key in settings)")
		}
	}

	switch provider {
	case "pexels":
		items, err := SearchPexels(ctx, cfg.PexelsKey, opts)
		if err == nil && len(items) > 0 {
			return items, nil
		}
		if cfg.PixabayKey != "" {
			// Fallback to Pixabay
			return SearchPixabay(ctx, cfg.PixabayKey, opts)
		}
		return items, err
	case "pixabay":
		items, err := SearchPixabay(ctx, cfg.PixabayKey, opts)
		if err == nil && len(items) > 0 {
			return items, nil
		}
		if cfg.PexelsKey != "" {
			// Fallback to Pexels
			return SearchPexels(ctx, cfg.PexelsKey, opts)
		}
		return items, err
	default:
		return nil, fmt.Errorf("unsupported stock provider: %s", provider)
	}
}

// SearchAndDownload searches for a matching clip and downloads the MP4 bytes.
func SearchAndDownload(ctx context.Context, cfg Config, rawPrompt string, orientation string) ([]byte, *VideoItem, error) {
	keywords := ExtractKeywords(rawPrompt)
	opts := SearchOptions{
		Query:       keywords,
		Orientation: orientation,
		PerPage:     10,
	}

	items, err := Search(ctx, cfg, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(items) == 0 {
		// Fallback search with single main keyword
		words := strings.Fields(keywords)
		if len(words) > 1 {
			opts.Query = words[0]
			items, err = Search(ctx, cfg, opts)
		}
	}
	if len(items) == 0 {
		return nil, nil, fmt.Errorf("no stock videos found for query: %q", keywords)
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

	return nil, nil, fmt.Errorf("failed to download video stream from search results")
}
