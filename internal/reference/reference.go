// Package reference studies a YouTube video the user wants to remake
// («Перезняти за прикладом»): a video model watches AND listens to it and
// returns a precise, structured breakdown — scenes with timestamps, what is on
// screen, who says what, sound, style, pacing — that the studio
// (studio/remake.go) turns into a new project. Nothing is downloaded: the
// model reads the video from YouTube itself.
//
//	probe.go      a cheap look without any key: oEmbed (title, channel) and the
//	              watch page (length, unlisted, live, playability) → early,
//	              friendly errors and the ground truth to verify a model by
//	watch.go      the watchers: Google (Gemini API generateContent with the
//	              YouTube URL as fileData, the studio's Google AI Studio keys
//	              rotated by internal/gemini; clipping with videoMetadata) and
//	              Text (an OpenAI-compatible gateway such as CarteLink — only
//	              when it really can watch YouTube: several content-part shapes
//	              are tried and the answer is checked against the probe)
//	breakdown.go  the prompts, the JSON breakdown (lenient parsing), long videos
//	              in 10-minute parts merged into one, the Ukrainian text the
//	              user reads and edits in step 1
//
// Public videos only (Gemini cannot open private or unlisted ones). Errors are
// friendly Ukrainian (*Error with a Kind the UI can act on).
package reference

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kind classifies a failure.
type Kind string

const (
	KindBadURL      Kind = "badurl"      // not a YouTube video address
	KindPrivate     Kind = "private"     // private video
	KindUnlisted    Kind = "unlisted"    // unlisted: Gemini cannot open it
	KindAge         Kind = "age"         // age-restricted (needs a sign-in)
	KindUnavailable Kind = "unavailable" // removed, terminated, does not exist, blocked
	KindLive        Kind = "live"        // a live stream / premiere that has not ended
	KindTooLong     Kind = "toolong"     // longer than MaxDuration
	KindNoService   Kind = "noservice"   // no Google key and no text service that can watch YouTube
	KindQuota       Kind = "quota"       // the keys' quota (or the daily YouTube allowance) is spent
	KindNotSeen     Kind = "notseen"     // the model could not watch the video
	KindNetwork     Kind = "network"     // no connection
	KindBadReply    Kind = "badreply"    // the model's answer is not a breakdown
	KindOther       Kind = "other"
)

// Error is a failure explained for the user.
type Error struct {
	Kind   Kind
	Msg    string // Ukrainian, says what to do
	Detail string // the service's own message (logs)
}

func (e *Error) Error() string { return e.Msg }

// KindOf is the Kind of err ("" when it is not an *Error).
func KindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

// messages are the Ukrainian texts of the kinds.
var messages = map[Kind]string{
	KindBadURL:      "Це не схоже на посилання на відео YouTube — встав адресу на кшталт youtube.com/watch?v=… або youtu.be/…",
	KindPrivate:     "Це приватне відео — його бачить лише автор. Встав посилання на публічне відео.",
	KindUnlisted:    "Це відео доступне лише за посиланням (не в пошуку) — Gemini такі не відкриває. Вибери публічне відео.",
	KindAge:         "YouTube показує це відео лише після входу (вікове обмеження) — Gemini його не відкриє. Вибери інше відео.",
	KindUnavailable: "Відео недоступне — його видалили, закрили або воно заблоковане. Перевір посилання.",
	KindLive:        "Це пряма трансляція або прем'єра, яка ще не закінчилась — дочекайся запису.",
	KindNoService:   "Щоб студія подивилась відео, додай ключ Google AI Studio в Налаштуваннях → Підключення — це все, що потрібно.",
	KindQuota:       "Ліміт Google на перегляд відео вичерпано (на безкоштовному ключі — 8 годин YouTube на день). Спробуй пізніше або додай інший ключ Google AI Studio.",
	KindNotSeen:     "Модель не змогла переглянути це відео. Перевір, що воно публічне, або спробуй пізніше.",
	KindNetwork:     "Немає з'єднання — перевір інтернет і спробуй ще раз.",
	KindBadReply:    "Модель повернула незрозумілий розбір — спробуй ще раз.",
}

func newError(k Kind, detail string) *Error {
	msg := messages[k]
	if msg == "" {
		msg = "Не вдалося розібрати відео"
		if d := strings.TrimSpace(detail); d != "" {
			msg += ": " + clip(d, 200)
		}
	}
	return &Error{Kind: k, Msg: msg, Detail: detail}
}

// MaxDuration is the longest video the studio studies (seconds).
const MaxDuration = 90 * 60

func tooLong(d float64) *Error {
	return &Error{Kind: KindTooLong, Msg: fmt.Sprintf("Приклад задовгий (%s) — візьми відео до %d хвилин або його частину.", Clock(d), MaxDuration/60)}
}

// ---- addresses ------------------------------------------------------------------------------

var reVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// YouTubeID is the video id of a YouTube address ("" for anything else):
// watch?v=, youtu.be/, /shorts/, /embed/, /live/, music.youtube.com,
// m.youtube.com, youtube-nocookie.com; a bare 11-character id too.
func YouTubeID(raw string) string {
	s := strings.Trim(strings.TrimSpace(raw), "<>\"'«» ")
	if reVideoID.MatchString(s) {
		return s
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range []string{"www.", "m.", "music."} {
		host = strings.TrimPrefix(host, p)
	}
	switch host {
	case "youtu.be":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		return validID(parts[0])
	case "youtube.com", "youtube-nocookie.com":
		if v := u.Query().Get("v"); v != "" {
			return validID(v)
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			switch parts[0] {
			case "shorts", "embed", "live", "v", "e":
				return validID(parts[1])
			}
		}
	}
	return ""
}

func validID(s string) string {
	if reVideoID.MatchString(s) {
		return s
	}
	return ""
}

// WatchURL is the canonical address of a video id.
func WatchURL(id string) string { return "https://www.youtube.com/watch?v=" + id }

// NormalizeURL turns what the user pasted into the canonical watch address
// (Shorts and youtu.be links included); anything else is a KindBadURL error.
func NormalizeURL(raw string) (string, error) {
	id := YouTubeID(raw)
	if id == "" {
		return "", newError(KindBadURL, raw)
	}
	return WatchURL(id), nil
}

// ---- small helpers --------------------------------------------------------------------------

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// Clock is "m:ss" (or "h:mm:ss") for t seconds.
func Clock(t float64) string {
	if t < 0 || math.IsNaN(t) {
		t = 0
	}
	s := int(t + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

var reClock = regexp.MustCompile(`^(?:(\d+):)?(\d{1,2}):(\d{1,2}(?:\.\d+)?)$`)

// seconds reads a time the model wrote: a number, "75", "75s", "1:15",
// "01:01:15", "1m15s" (-1 when unreadable).
func seconds(v any) float64 {
	switch x := v.(type) {
	case float64:
		if x >= 0 && !math.IsInf(x, 0) && !math.IsNaN(x) {
			return x
		}
		return -1
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		s = strings.TrimSuffix(s, "s")
		if f, err := strconv.ParseFloat(s, 64); err == nil && f >= 0 {
			return f
		}
		if m := reClock.FindStringSubmatch(s); m != nil {
			h, _ := strconv.Atoi(m[1])
			mm, _ := strconv.Atoi(m[2])
			ss, _ := strconv.ParseFloat(m[3], 64)
			return float64(h*3600+mm*60) + ss
		}
		if m := reHMS.FindStringSubmatch(s); m != nil && m[0] != "" {
			h, _ := strconv.Atoi(m[1])
			mm, _ := strconv.Atoi(m[2])
			ss, _ := strconv.ParseFloat(m[3], 64)
			return float64(h*3600+mm*60) + ss
		}
	}
	return -1
}

var reHMS = regexp.MustCompile(`^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?))?$`)
