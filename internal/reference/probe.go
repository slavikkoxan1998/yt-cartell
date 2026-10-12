package reference

// A cheap look at a YouTube video before a model watches it — no key, no
// quota, best effort (any failure just leaves the fields unknown):
//
//	oEmbed      GET https://www.youtube.com/oembed?url=…&format=json →
//	            title, channel; 404/400 = the video does not exist; a portrait
//	            player = a Short
//	watch page  GET https://www.youtube.com/watch?v=… → lengthSeconds,
//	            isUnlisted, isLive/isUpcoming, playabilityStatus {status,
//	            reason} (private, age-restricted, removed …)
//
// What is known early is refused early with a friendly error; the duration
// and title are also the ground truth a text gateway's answer is checked
// against (watch.go).

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Endpoints (package vars so tests point them at a mock).
var (
	oembedURL    = "https://www.youtube.com/oembed"
	watchPageURL = "https://www.youtube.com/watch"
	thumbBase    = "https://i.ytimg.com/vi/"
	probeTimeout = 15 * time.Second
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

// Meta is what YouTube tells about a video without a key.
type Meta struct {
	ID       string  `json:"id"`
	URL      string  `json:"url"`
	Title    string  `json:"title,omitempty"`
	Channel  string  `json:"channel,omitempty"`
	Duration float64 `json:"duration,omitempty"` // 0 = unknown
	Vertical bool    `json:"vertical,omitempty"` // a Short (portrait player)
	Unlisted bool    `json:"unlisted,omitempty"`
	Status   string  `json:"status,omitempty"` // playabilityStatus.status ("OK", "LOGIN_REQUIRED" …)
	Reason   string  `json:"reason,omitempty"`
}

// Probe looks the video id up. The error is an *Error only for what surely
// blocks a model from watching (private, unlisted, removed, live, too long);
// everything else returns the Meta it could fill.
func Probe(ctx context.Context, hc *http.Client, id string) (Meta, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	m := Meta{ID: id, URL: WatchURL(id)}
	gone := false
	if o, status, err := oembed(ctx, hc, m.URL); err == nil {
		m.Title, m.Channel = o.Title, o.Author
		m.Vertical = o.Width > 0 && o.Height > o.Width
	} else if status == http.StatusNotFound || status == http.StatusBadRequest {
		gone = true
	}
	if ctx.Err() != nil {
		return m, ctx.Err()
	}
	if page, err := fetchText(ctx, hc, watchPageURL+"?v="+url.QueryEscape(id)+"&hl=en&has_verified=1", 6<<20); err == nil {
		readWatchPage(page, &m)
	}
	if ctx.Err() != nil {
		return m, ctx.Err()
	}
	if err := m.blocker(); err != nil {
		return m, err
	}
	if gone && m.Duration == 0 {
		return m, newError(KindUnavailable, "oembed 404")
	}
	return m, nil
}

// blocker is the error for a video a model cannot watch (nil = none known).
func (m Meta) blocker() error {
	r := strings.ToLower(m.Reason)
	switch {
	case m.Status == "LIVE_STREAM_OFFLINE" || m.Status == "LIVE_STREAM":
		return newError(KindLive, m.Reason)
	case strings.Contains(r, "private"):
		return newError(KindPrivate, m.Reason)
	case strings.Contains(r, "confirm your age") || strings.Contains(r, "age-restricted") || strings.Contains(r, "inappropriate"):
		return newError(KindAge, m.Reason)
	case m.Status == "ERROR" || m.Status == "UNPLAYABLE":
		e := newError(KindUnavailable, m.Reason)
		if m.Reason != "" {
			e.Msg += " (YouTube: «" + clip(m.Reason, 120) + "»)"
		}
		return e
	case m.Unlisted:
		return newError(KindUnlisted, "unlisted")
	case m.Duration > MaxDuration:
		return tooLong(m.Duration)
	}
	return nil
}

type oembedReply struct {
	Title  string `json:"title"`
	Author string `json:"author_name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func oembed(ctx context.Context, hc *http.Client, watch string) (oembedReply, int, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	u := oembedURL + "?format=json&url=" + url.QueryEscape(watch)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return oembedReply{}, 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	res, err := hc.Do(req)
	if err != nil {
		return oembedReply{}, 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return oembedReply{}, res.StatusCode, errors.New(res.Status)
	}
	var o oembedReply
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&o); err != nil {
		return oembedReply{}, res.StatusCode, err
	}
	return o, res.StatusCode, nil
}

func fetchText(ctx context.Context, hc *http.Client, u string, limit int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", "en-US,en;q=0.8")
	// Skip the EU consent page.
	req.Header.Set("Cookie", "SOCS=CAI; CONSENT=YES+cb")
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", errors.New(res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit))
	return string(b), err
}

var (
	reLength   = regexp.MustCompile(`"lengthSeconds"\s*:\s*"(\d+)"`)
	reUnlisted = regexp.MustCompile(`"isUnlisted"\s*:\s*true`)
	reLive     = regexp.MustCompile(`"isLive"\s*:\s*true|"isUpcoming"\s*:\s*true`)
	reStatus   = regexp.MustCompile(`"status"\s*:\s*"([A-Z_]+)"`)
	reReason   = regexp.MustCompile(`"reason"\s*:\s*(?:"((?:[^"\\]|\\.)*)"|\{"simpleText"\s*:\s*"((?:[^"\\]|\\.)*)")`)
	reMetaName = regexp.MustCompile(`<meta name="title" content="([^"]*)"`)
	reAuthor   = regexp.MustCompile(`"author"\s*:\s*"((?:[^"\\]|\\.)*)"`)
)

// readWatchPage fills m from the watch page's embedded player response.
func readWatchPage(page string, m *Meta) {
	if x := reLength.FindStringSubmatch(page); x != nil {
		if n, err := strconv.Atoi(x[1]); err == nil && n > 0 {
			m.Duration = float64(n)
		}
	}
	m.Unlisted = reUnlisted.MatchString(page)
	if i := strings.Index(page, `"playabilityStatus"`); i >= 0 {
		part := page[i:min(len(page), i+1500)]
		if x := reStatus.FindStringSubmatch(part); x != nil {
			m.Status = x[1]
		}
		if x := reReason.FindStringSubmatch(part); x != nil {
			m.Reason = unquote(x[1] + x[2])
		}
	}
	// Live flags of THIS video only (suggested videos carry their own).
	if i := strings.Index(page, `"videoDetails"`); i >= 0 {
		if reLive.MatchString(page[i:min(len(page), i+12000)]) && (m.Status == "OK" || m.Status == "") {
			m.Status = "LIVE_STREAM"
		}
	}
	if m.Title == "" {
		if x := reMetaName.FindStringSubmatch(page); x != nil {
			m.Title = htmlUnescape(x[1])
		}
	}
	if m.Channel == "" {
		if x := reAuthor.FindStringSubmatch(page); x != nil {
			m.Channel = unquote(x[1])
		}
	}
}

func unquote(s string) string {
	if u, err := strconv.Unquote(`"` + s + `"`); err == nil {
		return u
	}
	return s
}

func htmlUnescape(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&quot;", `"`, "&#39;", "'", "&lt;", "<", "&gt;", ">")
	return r.Replace(s)
}

// FetchThumb saves the video's YouTube thumbnail as dst (JPEG).
func FetchThumb(ctx context.Context, hc *http.Client, id, dst string) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	var last error
	for _, name := range []string{"maxresdefault.jpg", "hqdefault.jpg"} {
		ctx2, cancel := context.WithTimeout(ctx, probeTimeout)
		req, err := http.NewRequestWithContext(ctx2, http.MethodGet, thumbBase+url.PathEscape(id)+"/"+name, nil)
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("User-Agent", browserUA)
		res, err := hc.Do(req)
		if err != nil {
			cancel()
			last = err
			continue
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		cancel()
		if err != nil || res.StatusCode != http.StatusOK || len(b) < 1000 || !strings.HasPrefix(http.DetectContentType(b), "image/") {
			last = errors.New("no thumbnail " + name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	}
	return last
}
