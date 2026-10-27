package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const PixabayAPIBase = "https://pixabay.com/api/videos/"

type pixabayResponse struct {
	Total     int            `json:"total"`
	TotalHits int            `json:"totalHits"`
	Hits      []pixabayVideo `json:"hits"`
}

type pixabayStream struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Size   int    `json:"size"`
}

type pixabayVideo struct {
	ID        int    `json:"id"`
	PageURL   string `json:"pageURL"`
	Duration  int    `json:"duration"`
	Tags      string `json:"tags"`
	Videos    struct {
		Large  pixabayStream `json:"large"`
		Medium pixabayStream `json:"medium"`
		Small  pixabayStream `json:"small"`
		Tiny   pixabayStream `json:"tiny"`
	} `json:"videos"`
}

// SearchPixabay queries Pixabay API for videos.
func SearchPixabay(ctx context.Context, apiKey string, opts SearchOptions) ([]VideoItem, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("pixabay: missing API key")
	}
	if opts.Query == "" {
		return nil, fmt.Errorf("pixabay: empty query")
	}

	u, err := url.Parse(PixabayAPIBase)
	if err != nil {
		return nil, err
	}

	q := u.Query()
	q.Set("key", apiKey)
	q.Set("q", opts.Query)
	q.Set("video_type", "all")

	perPage := opts.PerPage
	if perPage <= 0 || perPage > 50 {
		perPage = 15
	}
	q.Set("per_page", strconv.Itoa(perPage))

	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "YTCartellStudio/2.1")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pixabay do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("pixabay api error (status %d): %s", resp.StatusCode, string(body))
	}

	var res pixabayResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("pixabay decode json: %w", err)
	}

	items := make([]VideoItem, 0, len(res.Hits))
	for _, h := range res.Hits {
		tags := strings.Split(h.Tags, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}

		item := VideoItem{
			ID:       strconv.Itoa(h.ID),
			Provider: "pixabay",
			URL:      h.PageURL,
			Duration: h.Duration,
			Tags:     tags,
			Files:    make([]VideoFile, 0, 4),
		}

		// Add streams by priority: Large -> Medium -> Small -> Tiny
		addStream := func(s pixabayStream, quality string) {
			if s.URL != "" {
				item.Files = append(item.Files, VideoFile{
					Quality:  quality,
					Width:    s.Width,
					Height:   s.Height,
					Link:     s.URL,
					FileType: "video/mp4",
				})
				if item.Width == 0 && s.Width > 0 {
					item.Width = s.Width
					item.Height = s.Height
				}
			}
		}

		addStream(h.Videos.Large, "large")
		addStream(h.Videos.Medium, "medium")
		addStream(h.Videos.Small, "small")
		addStream(h.Videos.Tiny, "tiny")

		// Filter by orientation if requested
		if opts.Orientation == "landscape" && item.Width < item.Height {
			continue
		}
		if opts.Orientation == "portrait" && item.Height < item.Width {
			continue
		}

		items = append(items, item)
	}
	return items, nil
}
