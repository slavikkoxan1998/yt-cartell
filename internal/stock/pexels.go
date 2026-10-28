package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const PexelsAPIBase = "https://api.pexels.com/videos/search"

type pexelsResponse struct {
	Page         int           `json:"page"`
	PerPage      int           `json:"per_page"`
	TotalResults int           `json:"total_results"`
	Videos       []pexelsVideo `json:"videos"`
}

type pexelsVideo struct {
	ID         int               `json:"id"`
	Width      int               `json:"width"`
	Height     int               `json:"height"`
	Duration   int               `json:"duration"`
	URL        string            `json:"url"`
	Image      string            `json:"image"`
	VideoFiles []pexelsVideoFile `json:"video_files"`
}

type pexelsVideoFile struct {
	ID       int     `json:"id"`
	Quality  string  `json:"quality"`
	FileType string  `json:"file_type"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Link     string  `json:"link"`
}

// SearchPexels queries Pexels Video Search API for clips.
func SearchPexels(ctx context.Context, apiKey string, opts SearchOptions) ([]VideoItem, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("pexels: missing API key")
	}
	if opts.Query == "" {
		return nil, fmt.Errorf("pexels: empty query")
	}

	u, err := url.Parse(PexelsAPIBase)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("query", opts.Query)
	perPage := opts.PerPage
	if perPage <= 0 || perPage > 50 {
		perPage = 15
	}
	q.Set("per_page", strconv.Itoa(perPage))

	if opts.Orientation == "landscape" || opts.Orientation == "portrait" || opts.Orientation == "square" {
		q.Set("orientation", opts.Orientation)
	}
	if opts.MinDuration > 0 {
		q.Set("min_duration", strconv.Itoa(opts.MinDuration))
	}
	if opts.MaxDuration > 0 {
		q.Set("max_duration", strconv.Itoa(opts.MaxDuration))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", apiKey)
	req.Header.Set("User-Agent", "YTCartellStudio/2.1")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pexels do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("pexels api error (status %d): %s", resp.StatusCode, string(body))
	}

	var res pexelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("pexels decode json: %w", err)
	}

	items := make([]VideoItem, 0, len(res.Videos))
	for _, v := range res.Videos {
		item := VideoItem{
			ID:        strconv.Itoa(v.ID),
			Provider:  "pexels",
			URL:       v.URL,
			Duration:  v.Duration,
			Width:     v.Width,
			Height:    v.Height,
			Thumbnail: v.Image,
			Files:     make([]VideoFile, 0, len(v.VideoFiles)),
		}
		for _, f := range v.VideoFiles {
			if f.FileType == "video/mp4" || f.FileType == "" {
				item.Files = append(item.Files, VideoFile{
					ID:       f.ID,
					Quality:  f.Quality,
					Width:    f.Width,
					Height:   f.Height,
					Link:     f.Link,
					FileType: f.FileType,
					FPS:      f.FPS,
				})
			}
		}
		items = append(items, item)
	}
	return items, nil
}
