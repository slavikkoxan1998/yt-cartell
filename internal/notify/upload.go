package notify

// File uploads with progress: the multipart body is streamed from disk (a
// 50 MB video never sits in memory) with an exact Content-Length; a
// watchdog stops an upload that has not moved for a minute.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// uploadStall stops an upload whose bytes stopped moving (a var for tests).
var uploadStall = 60 * time.Second

const (
	// uploadAnswer bounds the wait for Telegram's answer after the last byte
	// (it processes a big video before answering).
	uploadAnswer = 3 * time.Minute
	// thumbMaxSide / thumbMaxBytes: Telegram's thumbnail limits (JPEG,
	// ≤ 320 px each side, < 200 kB).
	thumbMaxSide  = 320
	thumbMaxBytes = 200 << 10
)

var (
	errStall      = errors.New("upload stalled")
	errSlowAnswer = errors.New("no answer after the upload")
)

// Upload is one file sent with sendVideo, sendDocument, sendAudio or sendPhoto.
type Upload struct {
	Method string // "sendVideo", "sendDocument", "sendAudio", "sendPhoto"
	Field  string // the file's field: "video", "document", "audio", "photo"
	ChatID string
	Path   string // the file on disk
	Name   string // file name shown in Telegram ("" = the base of Path)
	MIME   string // "" = by extension
	// Caption is HTML, shortened to 1024 characters.
	Caption string
	// Params are extra fields: duration, width, height, supports_streaming …
	Params map[string]string
	// Thumb is the "thumbnail" (JPEG < 200 kB, ≤ 320 px — see ThumbJPEG).
	Thumb []byte
	// Cover is sendVideo's "cover" picture (JPEG).
	Cover []byte
	Opt   *SendOptions
	// Progress gets the file bytes sent so far (any goroutine; called often).
	Progress func(sent, total int64)
}

// UploadLimit is the biggest file this client may upload: 50 MB on
// api.telegram.org, 2000 MB through an own (local) Bot API server.
func (c *Client) UploadLimit() int64 {
	if c.base == DefaultBaseURL {
		return MaxUpload
	}
	return MaxUploadLocal
}

// Local reports an own Bot API server (not api.telegram.org).
func (c *Client) Local() bool { return c.base != DefaultBaseURL }

// Upload sends a file from disk and returns the sent message.
func (c *Client) Upload(ctx context.Context, u Upload) (Message, error) {
	chat := strings.TrimSpace(u.ChatID)
	if err := checkChat(chat); err != nil {
		return Message{}, err
	}
	if err := c.checkToken(); err != nil {
		return Message{}, err
	}
	f, err := os.Open(u.Path)
	if err != nil {
		return Message{}, &Error{msg: "Не вдалося прочитати файл для Telegram", err: err}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return Message{}, &Error{msg: "Не вдалося прочитати файл для Telegram", err: err}
	}
	size := st.Size()
	if lim := c.UploadLimit(); size > lim {
		return Message{}, &Error{Code: http.StatusRequestEntityTooLarge,
			msg: fmt.Sprintf("Файл завеликий для Telegram: %s, а бот може надіслати до %s", FormatSize(size), FormatSize(lim))}
	}
	name := u.Name
	if name == "" {
		name = filepath.Base(u.Path)
	}
	ctype := u.MIME
	if ctype == "" {
		ctype = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}

	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	var ferr error
	field := func(k, v string) {
		if ferr == nil {
			ferr = mw.WriteField(k, v)
		}
	}
	field("chat_id", chat)
	if u.Caption != "" {
		field("caption", ClipHTML(u.Caption, MaxCaption))
		field("parse_mode", "HTML")
	}
	keys := make([]string, 0, len(u.Params))
	for k := range u.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		field(k, u.Params[k])
	}
	if u.Opt != nil {
		if err := setMarkup(u.Opt.Markup, field); err != nil {
			return Message{}, err
		}
		if u.Opt.Silent {
			field("disable_notification", "true")
		}
		if u.Opt.ReplyTo > 0 {
			field("reply_parameters", fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, u.Opt.ReplyTo))
		}
	}
	part := func(name, filename, ctype string, data []byte) {
		if ferr != nil {
			return
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, name, filename))
		h.Set("Content-Type", ctype)
		w, err := mw.CreatePart(h)
		if err == nil && data != nil {
			_, err = w.Write(data)
		}
		ferr = err
	}
	if len(u.Thumb) > 0 {
		part("thumbnail", "thumb.jpg", "image/jpeg", u.Thumb)
	}
	if len(u.Cover) > 0 {
		field("cover", "attach://cover_image")
		part("cover_image", "cover.jpg", "image/jpeg", u.Cover)
	}
	part(u.Field, asciiName(name), ctype, nil) // the header only: the bytes stream from disk
	if ferr != nil {
		return Message{}, &Error{msg: "Не вдалося підготувати файл для Telegram", err: ferr}
	}
	tail := "\r\n--" + mw.Boundary() + "--\r\n"
	headLen := int64(head.Len())
	total := headLen + size + int64(len(tail))

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pr := &progressReader{r: io.MultiReader(&head, f, strings.NewReader(tail))}
	pr.last.Store(time.Now().UnixNano())
	if u.Progress != nil {
		pr.report = func(n int64) { u.Progress(min(size, max(0, n-headLen)), size) }
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case now := <-t.C:
				idle := now.Sub(time.Unix(0, pr.last.Load()))
				if pr.n.Load() < total && idle > uploadStall {
					cancel(errStall)
					return
				}
				if pr.n.Load() >= total && idle > uploadAnswer {
					cancel(errSlowAnswer)
					return
				}
			}
		}
	}()
	var m Message
	err = c.do(ctx, 0, http.MethodPost, u.Method, mw.FormDataContentType(), pr, total, &m)
	switch cause := context.Cause(ctx); {
	case err == nil:
	case errors.Is(cause, errStall):
		return Message{}, &Error{msg: "Завантаження в Telegram зупинилося — перевір інтернет і спробуй ще раз", err: cause}
	case errors.Is(cause, errSlowAnswer):
		return Message{}, &Error{msg: "Telegram не відповів після завантаження — перевір чат і спробуй ще раз", err: cause}
	}
	return m, err
}

// asciiName keeps a file name safe for the multipart header (Telegram shows
// it as is; quotes and line breaks would break the header).
func asciiName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n':
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "file"
	}
	return s
}

// progressReader counts what the transport has read.
type progressReader struct {
	r      io.Reader
	n      atomic.Int64
	last   atomic.Int64 // unix ns of the last read that moved
	report func(n int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		v := p.n.Add(int64(n))
		p.last.Store(time.Now().UnixNano())
		if p.report != nil {
			p.report(v)
		}
	}
	return n, err
}

// SendPhotoWith is SendPhoto with keyboards and options; it returns the
// sent message.
func (c *Client) SendPhotoWith(ctx context.Context, chatID, path, caption string, opt *SendOptions) (Message, error) {
	chatID = strings.TrimSpace(chatID)
	if err := checkChat(chatID); err != nil {
		return Message{}, err
	}
	if err := c.checkToken(); err != nil {
		return Message{}, err
	}
	photo, err := preparePhoto(path)
	if err != nil {
		return Message{}, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", chatID)
	if caption != "" {
		_ = mw.WriteField("caption", ClipHTML(caption, MaxCaption))
		_ = mw.WriteField("parse_mode", "HTML")
	}
	if opt != nil {
		var ferr error
		set := func(k, v string) {
			if ferr == nil {
				ferr = mw.WriteField(k, v)
			}
		}
		if err := setMarkup(opt.Markup, set); err != nil {
			return Message{}, err
		}
		if opt.Silent {
			set("disable_notification", "true")
		}
		if ferr != nil {
			return Message{}, &Error{msg: "Не вдалося підготувати фото для Telegram", err: ferr}
		}
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="photo"; filename=%q`, photo.name))
	h.Set("Content-Type", photo.mime)
	pw, err := mw.CreatePart(h)
	if err == nil {
		_, err = pw.Write(photo.data)
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		return Message{}, &Error{msg: "Не вдалося підготувати фото для Telegram", err: err}
	}
	var m Message
	err = c.call(ctx, http.MethodPost, "sendPhoto", mw.FormDataContentType(), &body, &m)
	return m, err
}

// SendAlbum sends up to 10 pictures as one album (the caption goes under the
// first). Pictures that cannot be prepared are skipped; one picture left is
// sent as a photo. Errors match ErrPhoto when none could be prepared.
func (c *Client) SendAlbum(ctx context.Context, chatID string, paths []string, caption string) error {
	chatID = strings.TrimSpace(chatID)
	if err := checkChat(chatID); err != nil {
		return err
	}
	if err := c.checkToken(); err != nil {
		return err
	}
	var photos []photoFile
	var okPaths []string
	var firstErr error
	for _, p := range paths {
		if len(photos) == MaxAlbum {
			break
		}
		ph, err := preparePhoto(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		photos, okPaths = append(photos, ph), append(okPaths, p)
	}
	switch len(photos) {
	case 0:
		if firstErr == nil {
			firstErr = &Error{msg: "Немає зображень для альбому", err: ErrPhoto}
		}
		return firstErr
	case 1:
		_, err := c.SendPhotoWith(ctx, chatID, okPaths[0], caption, nil)
		return err
	}
	type media struct {
		Type      string `json:"type"`
		Media     string `json:"media"`
		Caption   string `json:"caption,omitempty"`
		ParseMode string `json:"parse_mode,omitempty"`
	}
	items := make([]media, len(photos))
	for i := range photos {
		items[i] = media{Type: "photo", Media: fmt.Sprintf("attach://p%d", i)}
	}
	if caption != "" {
		items[0].Caption, items[0].ParseMode = ClipHTML(caption, MaxCaption), "HTML"
	}
	list, _ := json.Marshal(items)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", chatID)
	_ = mw.WriteField("media", string(list))
	var err error
	for i, ph := range photos {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="p%d"; filename=%q`, i, ph.name))
		h.Set("Content-Type", ph.mime)
		var pw io.Writer
		if pw, err = mw.CreatePart(h); err == nil {
			_, err = pw.Write(ph.data)
		}
		if err != nil {
			break
		}
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		return &Error{msg: "Не вдалося підготувати альбом для Telegram", err: err}
	}
	// Up to ten pictures: give the upload more time than one message.
	return c.do(ctx, 4*c.timeout, http.MethodPost, "sendMediaGroup", mw.FormDataContentType(), &body, -1, nil)
}

// ThumbJPEG makes a video thumbnail Telegram accepts from a picture: a JPEG
// of at most 320 px a side and under 200 kB.
func ThumbJPEG(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	small := fitPhoto(img, thumbMaxSide)
	for _, q := range []int{85, 70, 55, 40} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if buf.Len() < thumbMaxBytes {
			return buf.Bytes(), nil
		}
	}
	return nil, errors.New("the thumbnail stays over 200 kB")
}

// PhotoJPEG prepares a picture as a JPEG of at most 1280 px (a video
// cover); a small JPEG is kept as it is.
func PhotoJPEG(path string) ([]byte, error) {
	ph, err := preparePhoto(path)
	if err != nil {
		return nil, err
	}
	if ph.mime == "image/jpeg" {
		return ph.data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(ph.data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, fitPhoto(img, photoMaxSide), &jpeg.Options{Quality: photoQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
