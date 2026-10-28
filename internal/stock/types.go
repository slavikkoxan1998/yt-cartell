package stock

// VideoFile represents a single video stream/resolution of a stock video item.
type VideoFile struct {
	ID       int    `json:"id"`
	Quality  string `json:"quality"` // "hd", "uhd", "sd"
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Link     string `json:"link"`
	FileType string `json:"file_type"`
	FPS      float64 `json:"fps"`
}

// VideoItem represents a discovered stock video from a provider.
type VideoItem struct {
	ID        string      `json:"id"`
	Provider  string      `json:"provider"` // "pexels" | "pixabay"
	URL       string      `json:"url"`      // page URL on stock site
	Duration  int         `json:"duration"` // in seconds
	Width     int         `json:"width"`
	Height    int         `json:"height"`
	Thumbnail string      `json:"thumbnail"`
	Files     []VideoFile `json:"files"`
	Tags      []string    `json:"tags"`
}

// SearchOptions holds criteria for finding stock videos.
type SearchOptions struct {
	Query          string   // search terms or scene prompt
	Orientation    string   // "landscape", "portrait", "square", "all"
	MinDuration    int      // in seconds
	MaxDuration    int      // in seconds
	TargetDuration float64  // desired target duration in seconds
	ExcludeIDs     []string // IDs or filenames of videos to avoid reusing
	PerPage        int      // count of results to return
}
