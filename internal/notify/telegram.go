// Package notify is a small Telegram Bot API client: the studio reports
// finished jobs to the user's own bot (Settings → «Сповіщення Telegram»).
//
// Every call has its own timeout. Errors are friendly Ukrainian texts
// (*Error) and never contain the bot token: request URLs are redacted before
// anything is wrapped.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Telegram Bot API endpoint.
const DefaultBaseURL = "https://api.telegram.org"

const (
	// Timeout of one Bot API request (connect, upload and answer).
	Timeout = 15 * time.Second
	// MaxMessage and MaxCaption are Telegram's limits (characters after the
	// HTML markup is parsed).
	MaxMessage = 4096
	MaxCaption = 1024

	maxReply = 1 << 20
)

var (
	// A bot token is "<bot id>:<secret>" as @BotFather prints it.
	reToken = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{10,}$`)
	// A chat is a numeric id (negative for groups) or a public @username.
	reChat = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z][A-Za-z0-9_]{3,})$`)
	// reBotPath finds a token inside a request URL ("…/bot123:AbC…/getMe").
	reBotPath = regexp.MustCompile(`bot[0-9]+:[A-Za-z0-9_-]+`)
)

// ErrNoChat: the bot has not received anything yet (FindChat).
var ErrNoChat = errors.New("Бот ще не отримав жодного повідомлення — напиши йому /start і натисни «Знайти мій чат» ще раз")

// Error is a failed Bot API call. Error() is a plain Ukrainian explanation
// for the user; the fields tell what Telegram answered.
type Error struct {
	// Code is Telegram's error_code (the HTTP status); 0 when there was no
	// answer (network, timeout) or the request was not sent.
	Code int
	// Description is Telegram's own text, e.g. "Bad Request: chat not found".
	Description string
	// RetryAfter (seconds) comes with 429 Too Many Requests.
	RetryAfter int

	msg string
	err error // cause without the token
}

func (e *Error) Error() string { return e.msg }
func (e *Error) Unwrap() error { return e.err }

// Client talks to the Bot API with one bot token. Safe for concurrent use.
type Client struct {
	token   string
	base    string
	timeout time.Duration
	hc      *http.Client
}

// New returns a client for token. baseURL "" is DefaultBaseURL (tests and
// self-hosted Bot API servers pass their own).
func New(token, baseURL string) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	// No client-level timeout: every call bounds itself with its context, and
	// the default transport is used (proxy settings apply).
	return &Client{token: strings.TrimSpace(token), base: base, timeout: Timeout, hc: &http.Client{}}
}

// GetMe checks the token and returns the bot's username (without "@").
func (c *Client) GetMe(ctx context.Context) (string, error) {
	var me struct {
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
	}
	if err := c.call(ctx, http.MethodGet, "getMe", "", nil, &me); err != nil {
		return "", err
	}
	if me.Username == "" {
		return me.FirstName, nil
	}
	return me.Username, nil
}

// SendMessage sends an HTML message (parse mode HTML — build it with Escape
// for every dynamic part) without link previews. Longer texts are shortened
// to Telegram's limit.
func (c *Client) SendMessage(ctx context.Context, chatID, html string) error {
	chatID = strings.TrimSpace(chatID)
	if err := checkChat(chatID); err != nil {
		return err
	}
	form := url.Values{
		"chat_id":              {chatID},
		"text":                 {ClipHTML(html, MaxMessage)},
		"parse_mode":           {"HTML"},
		"link_preview_options": {`{"is_disabled":true}`},
	}
	return c.call(ctx, http.MethodPost, "sendMessage", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil)
}

// SendPhoto uploads the image at path with an HTML caption (shortened to
// 1024 characters). A large or unusual image is re-encoded to a JPEG of at
// most 1280 px first. A picture that cannot be read or prepared fails with
// an error matching ErrPhoto (the caller may send the text alone).
func (c *Client) SendPhoto(ctx context.Context, chatID, path, caption string) error {
	_, err := c.SendPhotoWith(ctx, chatID, path, caption, nil)
	return err
}

// Chat is a chat the bot has seen.
type Chat struct {
	ID    string // numeric id as text — what the settings store
	Title string // group/channel title, or the person's name (+ @username)
	Type  string // private | group | supergroup | channel
}

type tgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type withChat struct {
	Chat *tgChat `json:"chat"`
}

type update struct {
	UpdateID          int64     `json:"update_id"`
	Message           *withChat `json:"message"`
	EditedMessage     *withChat `json:"edited_message"`
	ChannelPost       *withChat `json:"channel_post"`
	EditedChannelPost *withChat `json:"edited_channel_post"`
	MyChatMember      *withChat `json:"my_chat_member"`
	ChatMember        *withChat `json:"chat_member"`
	CallbackQuery     *struct {
		Message *withChat `json:"message"`
	} `json:"callback_query"`
}

func (u *update) chat() *tgChat {
	holders := []*withChat{u.Message, u.EditedMessage, u.ChannelPost, u.EditedChannelPost, u.MyChatMember, u.ChatMember}
	if u.CallbackQuery != nil {
		holders = append(holders, u.CallbackQuery.Message)
	}
	for _, h := range holders {
		if h != nil && h.Chat != nil && h.Chat.ID != 0 {
			return h.Chat
		}
	}
	return nil
}

func (ch *tgChat) title() string {
	if t := strings.TrimSpace(ch.Title); t != "" {
		return t
	}
	name := strings.TrimSpace(ch.FirstName + " " + ch.LastName)
	switch {
	case ch.Username != "" && name != "":
		return name + " (@" + ch.Username + ")"
	case ch.Username != "":
		return "@" + ch.Username
	case name != "":
		return name
	}
	return "чат " + strconv.FormatInt(ch.ID, 10)
}

// FindChat returns the chat of the newest update the bot received
// (getUpdates): the user writes /start to the bot, then presses «Знайти мій
// чат». Updates are not confirmed, so nothing is taken away from other
// readers of the bot. ErrNoChat when the bot has seen nothing yet.
func (c *Client) FindChat(ctx context.Context) (Chat, error) {
	var ups []update
	q := url.Values{"limit": {"100"}, "timeout": {"0"}}
	if err := c.call(ctx, http.MethodGet, "getUpdates?"+q.Encode(), "", nil, &ups); err != nil {
		return Chat{}, err
	}
	var best *tgChat
	var bestID int64
	for i := range ups {
		if ch := ups[i].chat(); ch != nil && (best == nil || ups[i].UpdateID > bestID) {
			best, bestID = ch, ups[i].UpdateID
		}
	}
	if best == nil {
		return Chat{}, ErrNoChat
	}
	return Chat{ID: strconv.FormatInt(best.ID, 10), Title: best.title(), Type: best.Type}, nil
}

// ---- plumbing --------------------------------------------------------------------------

type reply struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Parameters  struct {
		RetryAfter      int   `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

// call runs one Bot API method (method may carry a query string) and decodes
// its result into out (nil = ignore it).
func (c *Client) call(ctx context.Context, verb, method, contentType string, body io.Reader, out any) error {
	return c.do(ctx, c.timeout, verb, method, contentType, body, -1, out)
}

// do is call with its own timeout (0 = none: the caller bounds the request)
// and, for streamed bodies, the body size (-1 = unknown or set by the body).
func (c *Client) do(ctx context.Context, timeout time.Duration, verb, method, contentType string, body io.Reader, size int64, out any) error {
	if err := c.checkToken(); err != nil {
		return err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, verb, c.base+"/bot"+c.token+"/"+method, body)
	if err != nil {
		return &Error{msg: "Неправильна адреса Telegram API", err: c.clean(err)}
	}
	if size >= 0 {
		req.ContentLength = size
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return c.netError(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
	if err != nil {
		return c.netError(err)
	}
	var r reply
	if err := json.Unmarshal(raw, &r); err != nil {
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			return &Error{Code: res.StatusCode, msg: "Telegram повернув незрозумілу відповідь"}
		}
		return apiError(res.StatusCode, "", 0, 0) // not a Bot API answer (proxy page…)
	}
	if !r.OK {
		code := r.ErrorCode
		if code == 0 {
			code = res.StatusCode
		}
		return apiError(code, r.Description, r.Parameters.RetryAfter, r.Parameters.MigrateToChatID)
	}
	if out != nil && len(r.Result) > 0 {
		if err := json.Unmarshal(r.Result, out); err != nil {
			return &Error{Code: res.StatusCode, msg: "Telegram повернув незрозумілу відповідь", err: err}
		}
	}
	return nil
}

func (c *Client) checkToken() error {
	switch {
	case c.token == "":
		return &Error{msg: "Вкажи токен бота — його видає @BotFather"}
	case !reToken.MatchString(c.token):
		return &Error{msg: "Токен бота має вигляд 123456789:AA… — скопіюй його з @BotFather повністю"}
	}
	return nil
}

func checkChat(chatID string) error {
	switch {
	case chatID == "":
		return &Error{msg: "Вкажи ID чату або натисни «Знайти мій чат»"}
	case !reChat.MatchString(chatID):
		return &Error{msg: "ID чату — це число (наприклад 123456789) або @назва_каналу"}
	}
	return nil
}

// apiError turns a Bot API failure into a plain explanation.
func apiError(code int, desc string, retryAfter int, migrateTo int64) *Error {
	d := strings.ToLower(desc)
	var msg string
	switch {
	case code == http.StatusUnauthorized || code == http.StatusNotFound:
		// Telegram answers 404 for a token it does not know at all.
		msg = "Токен бота не підходить — перевір його в @BotFather"
	case code == http.StatusBadRequest && strings.Contains(d, "chat not found"):
		msg = "Бот ще не бачив цей чат — напиши йому /start"
	case code == http.StatusBadRequest && migrateTo != 0:
		msg = fmt.Sprintf("Групу перетворено на супергрупу — новий ID чату: %d", migrateTo)
	case code == http.StatusForbidden && strings.Contains(d, "blocked"):
		msg = "Бота заблоковано в цьому чаті — розблокуй його й напиши йому /start"
	case code == http.StatusForbidden:
		msg = "Бот не може писати в цей чат — напиши йому /start або додай його в групу"
	case code == http.StatusConflict && strings.Contains(d, "webhook"):
		msg = "У бота ввімкнено вебхук, тож знайти чат автоматично не вийде — впиши ID чату вручну"
	case code == http.StatusConflict:
		msg = "Цього бота зараз читає інша програма — закрий її й спробуй ще раз"
	case code == http.StatusRequestEntityTooLarge:
		msg = "Файл завеликий для Telegram"
	case code == http.StatusTooManyRequests && retryAfter > 0:
		msg = fmt.Sprintf("Telegram просить зачекати %d с — забагато повідомлень", retryAfter)
	case code == http.StatusTooManyRequests:
		msg = "Telegram просить зачекати — забагато повідомлень, спробуй за хвилину"
	case code >= 500:
		msg = "Telegram тимчасово недоступний — спробуй пізніше"
	case desc != "":
		msg = "Telegram відхилив запит: " + desc
	default:
		msg = fmt.Sprintf("Telegram відповів помилкою %d", code)
	}
	return &Error{Code: code, Description: desc, RetryAfter: retryAfter, msg: msg}
}

// netError explains a request that got no answer. The request URL (which
// carries the token) is redacted inside the cause.
func (c *Client) netError(err error) error {
	msg := "Немає зв'язку з Telegram — перевір інтернет"
	var ne net.Error
	switch {
	case errors.Is(err, context.Canceled):
		msg = "Запит до Telegram скасовано"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		msg = "Telegram не відповів вчасно — перевір інтернет"
	}
	return &Error{msg: msg, err: c.clean(err)}
}

// clean removes the token from err (url.Error keeps the request URL).
func (c *Client) clean(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		ue.URL = Redact(ue.URL, c.token)
	}
	return redacted{err: err, token: c.token}
}

// redacted hides the token even in texts this package did not build;
// errors.Is/As still see the original chain.
type redacted struct {
	err   error
	token string
}

func (r redacted) Error() string { return Redact(r.err.Error(), r.token) }
func (r redacted) Unwrap() error { return r.err }

// Redact replaces token (and anything that looks like "bot<id>:<secret>")
// in s — use it before logging text that might contain a request URL.
func Redact(s, token string) string {
	// A too short "token" would garble the text and cannot be a real one.
	if token = strings.TrimSpace(token); len(token) >= 8 {
		s = strings.ReplaceAll(s, token, "•••")
	}
	return reBotPath.ReplaceAllString(s, "bot•••")
}
