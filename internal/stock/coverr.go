package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const CoverrAPIBase = "https://api.coverr.co/videos"

type coverrResponse struct {
	Hits []coverrHit `json:"hits"`
}

type coverrHit struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Duration  int    `json:"duration"`
	Thumbnail string `json:"thumbnail"`
	URLs      struct {
		MP4         string `json:"mp4"`
		MP4Download string `json:"mp4_download"`
		MP4Preview  string `json:"mp4_preview"`
	} `json:"urls"`
}

// SearchCoverr queries Coverr API for cinematic stock videos.
func SearchCoverr(ctx context.Context, apiKey string, opts SearchOptions) ([]VideoItem, error) {
	if opts.Query == "" {
		return nil, fmt.Errorf("coverr: empty query")
	}

	u, err := url.Parse(CoverrAPIBase)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("query", opts.Query)
	q.Set("urls", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	req.Header.Set("User-Agent", "YTCartellStudio/2.1")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("coverr do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("coverr api error (status %d): %s", resp.StatusCode, string(body))
	}

	var res coverrResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("coverr decode json: %w", err)
	}

	items := make([]VideoItem, 0, len(res.Hits))
	for _, h := range res.Hits {
		downloadURL := h.URLs.MP4Download
		if downloadURL == "" {
			downloadURL = h.URLs.MP4
		}
		if downloadURL == "" {
			downloadURL = h.URLs.MP4Preview
		}
		if downloadURL == "" {
			continue
		}

		item := VideoItem{
			ID:        h.ID,
			Provider:  "coverr",
			URL:       fmt.Sprintf("https://coverr.co/videos/%s", h.ID),
			Duration:  h.Duration,
			Thumbnail: h.Thumbnail,
			Files: []VideoFile{
				{
					Quality:  "hd",
					Width:    1920,
					Height:   1080,
					Link:     downloadURL,
					FileType: "video/mp4",
				},
			},
		}
		items = append(items, item)
	}
	return items, nil
}
