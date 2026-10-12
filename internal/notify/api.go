package notify

// The Bot API surface the remote control (package tgbot) needs on top of the
// notifications: updates (long polling), messages with keyboards, edits,
// callback answers, commands, chat actions and file downloads. Uploads with
// progress live in upload.go. Checked against https://core.telegram.org/bots/api
// (Bot API 10.3, 2026-08-24): text 1-4096 and captions 0-1024 characters after
// entity parsing, callback_data 1-64 bytes, answerCallbackQuery text 0-200
// characters, media groups of 2-10 items, getFile downloads up to 20 MB,
// uploads up to 50 MB (2000 MB through a local Bot API server), messages can be
// deleted for 48 h.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Telegram's limits.
const (
	// MaxCallbackData is the most bytes a callback button may carry.
	MaxCallbackData = 64
	// MaxAnswer is the longest answerCallbackQuery notification.
	MaxAnswer = 200
	// MaxUpload is what bots may upload through api.telegram.org; a local
	// Bot API server takes MaxUploadLocal.
	MaxUpload      = 50 << 20
	MaxUploadLocal = 2000 << 20
	// MaxDownload is the biggest file getFile serves from api.telegram.org.
	MaxDownload = 20 << 20
	// MaxAlbum is the most items of a media group (at least 2).
	MaxAlbum = 10
)

// User is a Telegram account (a person or a bot).
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Name is the person's name with the @username ("Іван (@ivan)").
func (u User) Name() string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	switch {
	case name != "" && u.Username != "":
		return name + " (@" + u.Username + ")"
	case u.Username != "":
		return "@" + u.Username
	case name != "":
		return name
	}
	return "id " + strconv.FormatInt(u.ID, 10)
}

// MsgChat is the chat of a message.
type MsgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // private | group | supergroup | channel
	Title     string `json:"title,omitempty"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// Name is the group title or the person's name.
func (c MsgChat) Name() string {
	ch := tgChat{ID: c.ID, Type: c.Type, Title: c.Title, Username: c.Username, FirstName: c.FirstName, LastName: c.LastName}
	return ch.title()
}

// File is a document, voice note or audio file of a message, or getFile's
// answer (FilePath).
type File struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	FilePath     string `json:"file_path,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	MimeType     string `json:"mime_type,omitempty"`
	Duration     int    `json:"duration,omitempty"`
}

// Message is an incoming or sent message (the fields the studio reads).
type Message struct {
	MessageID int      `json:"message_id"`
	From      *User    `json:"from,omitempty"`
	Chat      MsgChat  `json:"chat"`
	Date      int64    `json:"date"` // unix seconds; 0 = an inaccessible message
	Text      string   `json:"text,omitempty"`
	Caption   string   `json:"caption,omitempty"`
	Document  *File    `json:"document,omitempty"`
	Voice     *File    `json:"voice,omitempty"`
	Audio     *File    `json:"audio,omitempty"`
	ReplyTo   *Message `json:"reply_to_message,omitempty"`
}

// CallbackQuery is a press of an inline button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update is one incoming update of getUpdates.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	EditedMessage *Message       `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// ---- keyboards ------------------------------------------------------------------------

// Button is one inline button: Data (callback_data, ≤ 64 bytes) or URL.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
	// Style: "danger" (red), "success" (green), "primary" (blue); "" = the app's.
	Style string `json:"style,omitempty"`
}

// InlineKeyboard is the reply_markup of inline buttons, row by row.
type InlineKeyboard [][]Button

// MarshalJSON writes {"inline_keyboard": …}.
func (k InlineKeyboard) MarshalJSON() ([]byte, error) {
	rows := [][]Button(k)
	if rows == nil {
		rows = [][]Button{}
	}
	return json.Marshal(struct {
		Rows [][]Button `json:"inline_keyboard"`
	}{rows})
}

// Check reports a button Telegram would refuse (empty text, callback data
// over 64 bytes, neither data nor a link).
func (k InlineKeyboard) Check() error {
	for _, row := range k {
		for _, b := range row {
			switch {
			case strings.TrimSpace(b.Text) == "":
				return errors.New("inline button without text")
			case b.URL == "" && (b.Data == "" || len(b.Data) > MaxCallbackData):
				return fmt.Errorf("inline button %q: callback data must be 1-%d bytes, got %d", b.Text, MaxCallbackData, len(b.Data))
			}
		}
	}
	return nil
}

// ReplyKeyboard is a custom keyboard under the input field (buttons send
// their text).
type ReplyKeyboard struct {
	Rows        [][]string
	Persistent  bool   // is_persistent: shown even when the letter keyboard is hidden
	Resize      bool   // resize_keyboard
	Placeholder string // input_field_placeholder, 1-64 characters
}

// MarshalJSON writes Telegram's ReplyKeyboardMarkup.
func (k ReplyKeyboard) MarshalJSON() ([]byte, error) {
	type btn struct {
		Text string `json:"text"`
	}
	rows := make([][]btn, 0, len(k.Rows))
	for _, r := range k.Rows {
		row := make([]btn, 0, len(r))
		for _, t := range r {
			row = append(row, btn{t})
		}
		rows = append(rows, row)
	}
	return json.Marshal(struct {
		Keyboard    [][]btn `json:"keyboard"`
		Persistent  bool    `json:"is_persistent,omitempty"`
		Resize      bool    `json:"resize_keyboard,omitempty"`
		Placeholder string  `json:"input_field_placeholder,omitempty"`
	}{rows, k.Persistent, k.Resize, clipRunes(k.Placeholder, 64)})
}

// RemoveKeyboard hides a custom keyboard.
type RemoveKeyboard struct{}

// MarshalJSON writes {"remove_keyboard":true}.
func (RemoveKeyboard) MarshalJSON() ([]byte, error) { return []byte(`{"remove_keyboard":true}`), nil }

// SendOptions are the optional parts of a sent message.
type SendOptions struct {
	// Markup: InlineKeyboard, ReplyKeyboard, RemoveKeyboard or nil.
	Markup any
	// Silent sends without a sound (disable_notification).
	Silent bool
	// ReplyTo quotes that message (sent anyway when it is gone).
	ReplyTo int
	// Preview allows link previews (off by default).
	Preview bool
}

// form adds the options to a request.
func (o *SendOptions) form(set func(k, v string)) error {
	if o == nil {
		set("link_preview_options", `{"is_disabled":true}`)
		return nil
	}
	if !o.Preview {
		set("link_preview_options", `{"is_disabled":true}`)
	}
	if o.Silent {
		set("disable_notification", "true")
	}
	if o.ReplyTo > 0 {
		set("reply_parameters", fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, o.ReplyTo))
	}
	return setMarkup(o.Markup, set)
}

func setMarkup(m any, set func(k, v string)) error {
	switch k := m.(type) {
	case nil:
		return nil
	case InlineKeyboard:
		if k == nil {
			return nil
		}
		if err := k.Check(); err != nil {
			return &Error{msg: "Кнопка бота не пройшла перевірку", err: err}
		}
	case *InlineKeyboard:
		if k == nil {
			return nil
		}
		if err := k.Check(); err != nil {
			return &Error{msg: "Кнопка бота не пройшла перевірку", err: err}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return &Error{msg: "Кнопки бота не вдалося підготувати", err: err}
	}
	set("reply_markup", string(b))
	return nil
}

// ChatID is a numeric chat id as the text the methods take.
func ChatID(id int64) string { return strconv.FormatInt(id, 10) }

// ---- methods ----------------------------------------------------------------------------

// Me returns the bot's own account (getMe).
func (c *Client) Me(ctx context.Context) (User, error) {
	var me User
	err := c.call(ctx, http.MethodGet, "getMe", "", nil, &me)
	return me, err
}

// Send sends an HTML message (shortened to 4096 characters) and returns it.
func (c *Client) Send(ctx context.Context, chatID, html string, opt *SendOptions) (Message, error) {
	chatID = strings.TrimSpace(chatID)
	if err := checkChat(chatID); err != nil {
		return Message{}, err
	}
	form := url.Values{"chat_id": {chatID}, "text": {ClipHTML(html, MaxMessage)}, "parse_mode": {"HTML"}}
	if err := opt.form(form.Set); err != nil {
		return Message{}, err
	}
	var m Message
	err := c.post(ctx, "sendMessage", form, &m)
	return m, err
}

// Edit replaces the text (and the inline keyboard: nil removes it) of a
// message the bot sent. Telegram's "message is not modified" counts as
// success.
func (c *Client) Edit(ctx context.Context, chatID string, messageID int, html string, kb InlineKeyboard) error {
	form := url.Values{"chat_id": {strings.TrimSpace(chatID)}, "message_id": {strconv.Itoa(messageID)},
		"text": {ClipHTML(html, MaxMessage)}, "parse_mode": {"HTML"}, "link_preview_options": {`{"is_disabled":true}`}}
	if kb != nil {
		if err := setMarkup(kb, form.Set); err != nil {
			return err
		}
	}
	err := c.post(ctx, "editMessageText", form, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// EditMarkup replaces only the inline keyboard of a message (nil removes it).
func (c *Client) EditMarkup(ctx context.Context, chatID string, messageID int, kb InlineKeyboard) error {
	form := url.Values{"chat_id": {strings.TrimSpace(chatID)}, "message_id": {strconv.Itoa(messageID)}}
	if kb == nil {
		kb = InlineKeyboard{}
	}
	if err := setMarkup(kb, form.Set); err != nil {
		return err
	}
	err := c.post(ctx, "editMessageReplyMarkup", form, nil)
	if IsNotModified(err) {
		return nil
	}
	return err
}

// Delete removes a message (Telegram allows it for 48 h).
func (c *Client) Delete(ctx context.Context, chatID string, messageID int) error {
	form := url.Values{"chat_id": {strings.TrimSpace(chatID)}, "message_id": {strconv.Itoa(messageID)}}
	return c.post(ctx, "deleteMessage", form, nil)
}

// Answer answers a button press (always do: the button spins until then).
// text (≤ 200 characters, "" = nothing) shows as a toast, or as a dialog
// with alert.
func (c *Client) Answer(ctx context.Context, queryID, text string, alert bool) error {
	form := url.Values{"callback_query_id": {queryID}}
	if text = strings.TrimSpace(text); text != "" {
		form.Set("text", clipRunes(text, MaxAnswer))
	}
	if alert {
		form.Set("show_alert", "true")
	}
	return c.post(ctx, "answerCallbackQuery", form, nil)
}

// GetUpdates long-polls for updates from offset (timeout in seconds, 0 =
// answer at once). allowed limits the update kinds (nil = as before).
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int, allowed []string) ([]Update, error) {
	form := url.Values{"timeout": {strconv.Itoa(max(0, timeout))}, "limit": {"100"}}
	if offset != 0 {
		form.Set("offset", strconv.FormatInt(offset, 10))
	}
	if allowed != nil {
		b, _ := json.Marshal(allowed)
		form.Set("allowed_updates", string(b))
	}
	var ups []Update
	// The answer may take the whole long-poll timeout.
	err := c.do(ctx, c.timeout+time.Duration(max(0, timeout))*time.Second, http.MethodPost, "getUpdates",
		"application/x-www-form-urlencoded", strings.NewReader(form.Encode()), -1, &ups)
	return ups, err
}

// Command is one entry of the bot's command menu.
type Command struct {
	Command     string `json:"command"`     // 1-32 characters: a-z, 0-9, _
	Description string `json:"description"` // 1-256 characters
}

// SetCommands sets the command menu, for one chat only (chatID "" = every
// chat).
func (c *Client) SetCommands(ctx context.Context, cmds []Command, chatID string) error {
	b, err := json.Marshal(cmds)
	if err != nil {
		return err
	}
	form := url.Values{"commands": {string(b)}}
	if chatID = strings.TrimSpace(chatID); chatID != "" {
		form.Set("scope", `{"type":"chat","chat_id":`+strconv.Quote(chatID)+`}`)
		if _, err := strconv.ParseInt(chatID, 10, 64); err == nil {
			form.Set("scope", `{"type":"chat","chat_id":`+chatID+`}`)
		}
	}
	return c.post(ctx, "setMyCommands", form, nil)
}

// Action shows "typing…", "upload_video" … in the chat for ~5 s.
func (c *Client) Action(ctx context.Context, chatID, action string) error {
	return c.post(ctx, "sendChatAction", url.Values{"chat_id": {strings.TrimSpace(chatID)}, "action": {action}}, nil)
}

// GetFile prepares a file of a message for Download.
func (c *Client) GetFile(ctx context.Context, fileID string) (File, error) {
	var f File
	err := c.post(ctx, "getFile", url.Values{"file_id": {fileID}}, &f)
	return f, err
}

// Download saves a file returned by GetFile to dst (at most max bytes). A
// local Bot API server answers with an absolute path on its own disk: when
// that file is readable here it is copied directly.
func (c *Client) Download(ctx context.Context, f File, dst string, max int64) error {
	if f.FileSize > 0 && max > 0 && f.FileSize > max {
		return &Error{Code: http.StatusRequestEntityTooLarge, msg: fmt.Sprintf("Файл завеликий (%s, можна до %s)", FormatSize(f.FileSize), FormatSize(max))}
	}
	if f.FilePath == "" {
		return &Error{msg: "Telegram не дав адресу файлу — надішли його ще раз"}
	}
	if filepath.IsAbs(f.FilePath) || path.IsAbs(f.FilePath) {
		if src, err := os.Open(f.FilePath); err == nil {
			defer src.Close()
			return saveLimited(src, dst, max)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	u := c.base + "/file/bot" + c.token + "/" + strings.TrimLeft(f.FilePath, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &Error{msg: "Неправильна адреса файлу Telegram", err: c.clean(err)}
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return c.netError(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return apiError(res.StatusCode, "", 0, 0)
	}
	if err := saveLimited(res.Body, dst, max); err != nil {
		if ctx.Err() != nil {
			return c.netError(ctx.Err())
		}
		return err
	}
	return nil
}

func saveLimited(r io.Reader, dst string, max int64) error {
	out, err := os.Create(dst)
	if err != nil {
		return &Error{msg: "Не вдалося зберегти файл", err: err}
	}
	src := r
	if max > 0 {
		src = io.LimitReader(r, max+1)
	}
	n, err := io.Copy(out, src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && max > 0 && n > max {
		err = &Error{Code: http.StatusRequestEntityTooLarge, msg: "Файл завеликий"}
	}
	if err != nil {
		_ = os.Remove(dst)
		var te *Error
		if errors.As(err, &te) {
			return te
		}
		return &Error{msg: "Не вдалося завантажити файл із Telegram", err: err}
	}
	return nil
}

// post runs a method with a form body.
func (c *Client) post(ctx context.Context, method string, form url.Values, out any) error {
	return c.call(ctx, http.MethodPost, method, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), out)
}

// ---- errors -----------------------------------------------------------------------------

func apiErr(err error) *Error {
	var te *Error
	if errors.As(err, &te) {
		return te
	}
	return nil
}

// RetryAfter is how long Telegram asked to wait (429), 0 otherwise.
func RetryAfter(err error) time.Duration {
	if te := apiErr(err); te != nil && te.Code == http.StatusTooManyRequests {
		return time.Duration(max(1, te.RetryAfter)) * time.Second
	}
	return 0
}

// IsNotModified: an edit that changes nothing.
func IsNotModified(err error) bool {
	te := apiErr(err)
	return te != nil && te.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(te.Description), "not modified")
}

// IsGone: the message to edit or delete no longer exists or cannot be
// changed any more (too old, deleted by the user).
func IsGone(err error) bool {
	te := apiErr(err)
	if te == nil || te.Code != http.StatusBadRequest {
		return false
	}
	d := strings.ToLower(te.Description)
	for _, s := range []string{"message to edit not found", "message to delete not found", "message can't be edited",
		"message can't be deleted", "message_id_invalid", "message not found"} {
		if strings.Contains(d, s) {
			return true
		}
	}
	return false
}

// Conflict reports a 409: another getUpdates reader (webhook false) or a
// webhook set on the bot (webhook true).
func Conflict(err error) (conflict, webhook bool) {
	te := apiErr(err)
	if te == nil || te.Code != http.StatusConflict {
		return false, false
	}
	return true, strings.Contains(strings.ToLower(te.Description), "webhook")
}

// Unauthorized: the token is wrong or revoked (401, or 404 for an unknown bot).
func Unauthorized(err error) bool {
	te := apiErr(err)
	return te != nil && (te.Code == http.StatusUnauthorized || te.Code == http.StatusNotFound)
}

// Temporary: no answer at all, 429 or a server error — worth retrying.
func Temporary(err error) bool {
	te := apiErr(err)
	return te == nil || te.Code == 0 || te.Code == http.StatusTooManyRequests || te.Code >= 500
}

// FormatSize is a file size in Ukrainian units ("48 МБ", "820 КБ").
func FormatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + " ГБ"
	case n >= 10<<20:
		return strconv.FormatInt((n+(1<<19))>>20, 10) + " МБ"
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " МБ"
	case n >= 1<<10:
		return strconv.FormatInt((n+512)>>10, 10) + " КБ"
	}
	return strconv.FormatInt(n, 10) + " Б"
}

// clipRunes shortens s to n runes (with "…").
func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:max(0, n-1)]) + "…"
}
