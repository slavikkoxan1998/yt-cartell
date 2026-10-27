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

const WikimediaAPIBase = "https://commons.wikimedia.org/w/api.php"

type wikimediaResponse struct {
	Query struct {
		Pages map[string]wikimediaPage `json:"pages"`
	} `json:"query"`
}

type wikimediaPage struct {
	PageID    int                  `json:"pageid"`
	Title     string               `json:"title"`
	ImageInfo []wikimediaImageInfo `json:"imageinfo"`
}

type wikimediaImageInfo struct {
	URL            string `json:"url"`
	DescriptionURL string `json:"descriptionurl"`
	Size           int    `json:"size"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	Mime           string `json:"mime"`
}

// SearchWikimedia queries Wikimedia Commons for public domain/CC video files.
func SearchWikimedia(ctx context.Context, opts SearchOptions) ([]VideoItem, error) {
	if opts.Query == "" {
		return nil, fmt.Errorf("wikimedia: empty query")
	}

	u, err := url.Parse(WikimediaAPIBase)
	if err != nil {
		return nil, err
	}

	limit := opts.PerPage
	if limit <= 0 || limit > 30 {
		limit = 10
	}

	q := u.Query()
	q.Set("action", "query")
	q.Set("generator", "search")
	q.Set("gsrnamespace", "6") // File: namespace
	q.Set("gsrsearch", opts.Query+" filetype:video")
	q.Set("gsrlimit", strconv.Itoa(limit))
	q.Set("prop", "imageinfo")
	q.Set("iiprop", "url|size|mime")
	q.Set("format", "json")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "YTCartellStudio/2.1 (contact: synapse-office)")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikimedia do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("wikimedia api error (status %d): %s", resp.StatusCode, string(body))
	}

	var res wikimediaResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("wikimedia decode json: %w", err)
	}

	items := make([]VideoItem, 0, len(res.Query.Pages))
	for _, p := range res.Query.Pages {
		if len(p.ImageInfo) == 0 {
			continue
		}
		info := p.ImageInfo[0]
		if !strings.HasPrefix(info.Mime, "video/") && !strings.HasSuffix(info.URL, ".webm") && !strings.HasSuffix(info.URL, ".mp4") {
			continue
		}

		item := VideoItem{
			ID:       strconv.Itoa(p.PageID),
			Provider: "wikimedia",
			URL:      info.DescriptionURL,
			Width:    info.Width,
			Height:   info.Height,
			Files: []VideoFile{
				{
					Quality:  "original",
					Width:    info.Width,
					Height:   info.Height,
					Link:     info.URL,
					FileType: info.Mime,
				},
			},
		}
		items = append(items, item)
	}
	return items, nil
}
