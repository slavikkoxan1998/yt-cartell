package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/version"
)

// DefaultAPI is the GitHub REST API root.
const DefaultAPI = "https://api.github.com"

// Timeout bounds one API request (the release check).
const Timeout = 15 * time.Second

// CacheName is the ETag cache file inside the folder New gets.
const CacheName = "update-cache.json"

// Kind classifies an Error so the UI can decide what to say (and the
// start-up check what to keep quiet about).
type Kind uint8

const (
	KindOther       Kind = iota
	KindOffline          // no connection, DNS, timeout
	KindRateLimited      // GitHub's 60 anonymous requests per hour are used up
	KindNoRelease        // nothing published yet
	KindServer           // GitHub answered 5xx or nonsense
	KindNoAsset          // the release has no build for this computer
	KindChecksum         // SHA256SUMS.txt missing, without our file, or a mismatch
	KindManual           // this install cannot be updated in place: use the release page
	KindPermission       // the app's folder is not writable
	KindIncomplete       // the download stopped early (it resumes next time)
)

// Error is a friendly Ukrainian error of the updater.
type Error struct {
	Kind  Kind
	Msg   string
	Reset time.Time // KindRateLimited: when GitHub accepts requests again (zero = unknown)
	Err   error     // the cause, for logs
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// KindOf is the Kind of err (KindOther when it is not an *Error).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindOther
}

// Client talks to GitHub. The zero value works (no ETag file, the system's
// temp folder for downloads); New is what the app uses.
type Client struct {
	// HTTP sends every request (nil = a plain client; Latest adds Timeout).
	HTTP *http.Client
	// API is the REST root ("" = DefaultAPI); downloads use the asset URLs.
	API string
	// Owner and Repo ("" = the package's Owner / Repo).
	Owner, Repo string
	// UserAgent ("" = "YT-Cartell-Studio/<version> (<os>; <arch>)").
	UserAgent string
	// CacheFile keeps the last answer and its ETag between runs, so an
	// unchanged release is a small 304 (anonymous 304s still count against
	// the hourly limit; checks are rare anyway) ("" = in memory only).
	CacheFile string
	// Dir receives downloads ("" = DefaultDir()).
	Dir string

	mu     sync.Mutex
	loaded bool
	cache  cacheEntry
}

// cacheEntry is the ETag cache (memory and CacheFile).
type cacheEntry struct {
	URL  string          `json:"url"`
	ETag string          `json:"etag"`
	Body json.RawMessage `json:"body"`
}

// New is the app's client: the ETag cache lives in dir (the settings folder).
func New(dir string) *Client {
	c := &Client{}
	if dir != "" {
		c.CacheFile = filepath.Join(dir, CacheName)
	}
	return c
}

// DefaultDir is where downloads wait until they are installed.
func DefaultDir() string { return filepath.Join(os.TempDir(), "yt-cartell-studio-update") }

func (c *Client) dir() string {
	if c.Dir != "" {
		return c.Dir
	}
	return DefaultDir()
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{}
}

func (c *Client) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return "YT-Cartell-Studio/" + version.String() + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}

func (c *Client) latestURL() string {
	api := strings.TrimRight(c.API, "/")
	if api == "" {
		api = DefaultAPI
	}
	owner, repo := c.Owner, c.Repo
	if owner == "" {
		owner = Owner
	}
	if repo == "" {
		repo = Repo
	}
	return api + "/repos/" + owner + "/" + repo + "/releases/latest"
}

// Latest returns the newest published release (GitHub's /releases/latest
// already skips drafts and pre-releases; Release.Newer checks again). A
// cached ETag makes an unchanged answer a 304.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	url := c.latestURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, &Error{Msg: "Неправильна адреса репозиторію оновлень.", Err: err}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.userAgent())
	cached := c.cached(url)
	if cached.ETag != "" && len(cached.Body) > 0 {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return Release{}, netError(ctx, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotModified && len(cached.Body) > 0:
		return parseRelease(cached.Body)
	case resp.StatusCode == http.StatusOK:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return Release{}, netError(ctx, err)
		}
		rel, err := parseRelease(body)
		if err != nil {
			return Release{}, err
		}
		if et := resp.Header.Get("ETag"); et != "" {
			c.store(cacheEntry{URL: url, ETag: et, Body: body})
		}
		return rel, nil
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, &Error{Kind: KindNoRelease, Msg: "Опублікованих версій ще немає — спробуй пізніше."}
	}
	return Release{}, statusError(resp, "перевірити оновлення")
}

// statusError explains a failed GitHub answer (rate limit, 5xx …).
func statusError(resp *http.Response, what string) error {
	code := resp.StatusCode
	if code == http.StatusTooManyRequests || code == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != "") {
		reset := rateReset(resp.Header)
		msg := "GitHub тимчасово обмежив перевірки (60 на годину з однієї адреси)."
		if !reset.IsZero() {
			msg += " Спробуй після " + reset.Local().Format("15:04") + "."
		} else {
			msg += " Спробуй трохи пізніше."
		}
		return &Error{Kind: KindRateLimited, Msg: msg, Reset: reset, Err: fmt.Errorf("github: HTTP %d", code)}
	}
	if code >= 500 {
		return &Error{Kind: KindServer, Msg: fmt.Sprintf("GitHub зараз не відповідає (код %d) — спробуй трохи пізніше.", code), Err: fmt.Errorf("github: HTTP %d", code)}
	}
	return &Error{Kind: KindServer, Msg: fmt.Sprintf("Не вдалося %s: GitHub відповів кодом %d.", what, code), Err: fmt.Errorf("github: HTTP %d", code)}
}

// rateReset reads when the rate limit resets: Retry-After first (secondary
// limits), then X-RateLimit-Reset.
func rateReset(h http.Header) time.Time {
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Now().Add(time.Duration(n) * time.Second)
		}
	}
	if s := h.Get("X-RateLimit-Reset"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			return time.Unix(n, 0)
		}
	}
	return time.Time{}
}

// netError explains a request that got no answer.
func netError(ctx context.Context, err error) error {
	if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return &Error{Kind: KindOffline, Msg: "GitHub не відповів вчасно — перевір інтернет і спробуй ще раз.", Err: err}
	}
	return &Error{Kind: KindOffline, Msg: "Немає з'єднання з GitHub — перевір інтернет і спробуй ще раз.", Err: err}
}

// ghRelease is the part of GitHub's release JSON the app reads.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name  string `json:"name"`
		URL   string `json:"browser_download_url"`
		Size  int64  `json:"size"`
		State string `json:"state"`
	} `json:"assets"`
}

func parseRelease(b []byte) (Release, error) {
	var g ghRelease
	if err := json.Unmarshal(b, &g); err != nil || strings.TrimSpace(g.TagName) == "" {
		if err == nil {
			err = errors.New("no tag_name")
		}
		return Release{}, &Error{Kind: KindServer, Msg: "GitHub повернув незрозумілу відповідь — спробуй пізніше.", Err: err}
	}
	r := Release{
		Tag:        strings.TrimSpace(g.TagName),
		Title:      strings.TrimSpace(g.Name),
		Notes:      g.Body,
		URL:        g.HTMLURL,
		Published:  g.PublishedAt,
		Draft:      g.Draft,
		Prerelease: g.Prerelease,
	}
	r.Version = strings.TrimPrefix(strings.TrimPrefix(r.Tag, "v"), "V")
	for _, a := range g.Assets {
		if a.State != "" && a.State != "uploaded" {
			continue // still uploading
		}
		r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return r, nil
}

// cached returns the cache entry for url (loads CacheFile once).
func (c *Client) cached(url string) cacheEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		c.loaded = true
		if c.CacheFile != "" {
			if b, err := os.ReadFile(c.CacheFile); err == nil {
				var e cacheEntry
				if json.Unmarshal(b, &e) == nil {
					c.cache = e
				}
			}
		}
	}
	if c.cache.URL != url {
		return cacheEntry{}
	}
	return c.cache
}

// store remembers an answer (best effort: a cache that cannot be written
// only costs a full request next time).
func (c *Client) store(e cacheEntry) {
	c.mu.Lock()
	c.cache, c.loaded = e, true
	path := c.CacheFile
	c.mu.Unlock()
	if path == "" {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// Apply installs a verified download (see the package Apply).
func (c *Client) Apply(staged string) (Restart, error) { return Apply(staged) }
