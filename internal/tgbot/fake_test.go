package tgbot

// A fake Bot API for the tests: getUpdates serves queued updates (long poll),
// the send/edit methods keep every message's current text and keyboard, and
// Telegram's limits are enforced (callback data ≤ 64 bytes, text ≤ 4096 and
// captions ≤ 1024 visible characters, balanced HTML of the supported tags,
// answers ≤ 200 characters) — a violation fails the test.

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"ytcartell/internal/notify"
)

const testToken = "123456789:TESTtoken-abcdefghijkl"

// upFile is an uploaded file part.
type upFile struct {
	name, ctype string
	data        []byte
}

// call is one Bot API request the bot made.
type call struct {
	method string
	chat   string
	text   string // text, or caption
	msgID  int    // the message sent or edited
	form   url.Values
	files  map[string]upFile
	at     time.Time
}

// msg is a message as it stands in the fake chat.
type msg struct {
	id     int
	chat   string
	text   string
	media  bool // photo/video/document/audio: callbacks carry no text
	kb     notify.InlineKeyboard
	reply  bool // came with a reply keyboard
	silent bool
}

type fakeTG struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	calls    []call
	msgs     map[int]*msg
	nextMsg  int
	updates  []notify.Update
	nextUp   int64
	kick     chan struct{}
	offsets  []int64
	files    map[string][]byte // file_id → bytes (getFile + download)
	username string
	// fail returns an error reply for a method (nil = none); consulted per call.
	fail func(method string, form url.Values) (int, string)
}

func newFake(t *testing.T) *fakeTG {
	f := &fakeTG{t: t, msgs: map[int]*msg{}, nextMsg: 100, nextUp: 1, kick: make(chan struct{}, 1),
		files: map[string][]byte{}, username: "studio_test_bot"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	allow(f.srv.URL)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTG) URL() string { return f.srv.URL }

var reTag = regexp.MustCompile(`</?([a-zA-Z]+)[^>]*>`)

// checkHTML: Telegram's HTML subset, balanced; returns the visible length.
func checkHTML(s string) (int, error) {
	var stack []string
	for _, m := range reTag.FindAllStringSubmatch(s, -1) {
		name := strings.ToLower(m[1])
		switch name {
		case "b", "i", "u", "s", "code", "pre", "a", "tg-spoiler", "blockquote":
		default:
			return 0, fmt.Errorf("unsupported tag <%s>", name)
		}
		if strings.HasPrefix(m[0], "</") {
			if len(stack) == 0 || stack[len(stack)-1] != name {
				return 0, fmt.Errorf("unbalanced </%s>", name)
			}
			stack = stack[:len(stack)-1]
		} else {
			stack = append(stack, name)
		}
	}
	if len(stack) > 0 {
		return 0, fmt.Errorf("unclosed <%s>", stack[len(stack)-1])
	}
	plain := reTag.ReplaceAllString(s, "")
	if strings.ContainsAny(plain, "<>") {
		return 0, fmt.Errorf("unescaped < or > in %q", plain)
	}
	for _, e := range []string{"&lt;", "&gt;", "&amp;", "&quot;"} {
		plain = strings.ReplaceAll(plain, e, "x")
	}
	if strings.Contains(plain, "&") {
		return 0, fmt.Errorf("unescaped & in %q", plain)
	}
	n := 0
	for _, r := range plain {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n, nil
}

func (f *fakeTG) bad(w http.ResponseWriter, format string, args ...any) {
	f.t.Errorf("fake Telegram: "+format, args...)
	w.WriteHeader(400)
	io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: test violation"}`)
}

func (f *fakeTG) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/file/bot"+testToken+"/") {
		id := strings.TrimPrefix(r.URL.Path, "/file/bot"+testToken+"/")
		f.mu.Lock()
		b, ok := f.files[id]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
		return
	}
	prefix := "/bot" + testToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(401)
		io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	c := call{method: method, files: map[string]upFile{}, at: time.Now()}
	mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		c.form = url.Values{}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				c.files[p.FormName()] = upFile{name: p.FileName(), ctype: p.Header.Get("Content-Type"), data: b}
			} else {
				c.form.Add(p.FormName(), string(b))
			}
		}
	} else {
		_ = r.ParseForm()
		c.form = r.Form
	}
	c.chat = c.form.Get("chat_id")
	c.text = c.form.Get("text")
	if c.text == "" {
		c.text = c.form.Get("caption")
	}
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail != nil {
		if code, body := fail(method, c.form); code != 0 {
			f.mu.Lock()
			f.calls = append(f.calls, c)
			f.mu.Unlock()
			w.WriteHeader(code)
			io.WriteString(w, body)
			return
		}
	}
	// Telegram's limits.
	if t := c.form.Get("text"); t != "" || method == "sendMessage" || method == "editMessageText" {
		n, err := checkHTML(t)
		switch {
		case err != nil:
			f.bad(w, "%s: %v in %q", method, err, t)
			return
		case n == 0 || n > notify.MaxMessage:
			f.bad(w, "%s: text of %d characters", method, n)
			return
		}
	}
	if cp := c.form.Get("caption"); cp != "" {
		if n, err := checkHTML(cp); err != nil || n > notify.MaxCaption {
			f.bad(w, "%s: caption %v (%d characters)", method, err, n)
			return
		}
	}
	var kb notify.InlineKeyboard
	reply := false
	if mk := c.form.Get("reply_markup"); mk != "" {
		var raw struct {
			Inline [][]notify.Button `json:"inline_keyboard"`
			Reply  [][]struct {
				Text string `json:"text"`
			} `json:"keyboard"`
		}
		if err := json.Unmarshal([]byte(mk), &raw); err != nil {
			f.bad(w, "%s: reply_markup %q: %v", method, mk, err)
			return
		}
		for _, row := range raw.Inline {
			for _, b := range row {
				if b.Text == "" || (b.URL == "" && (len(b.Data) == 0 || len(b.Data) > notify.MaxCallbackData)) {
					f.bad(w, "%s: button %+v", method, b)
					return
				}
			}
		}
		kb, reply = raw.Inline, raw.Reply != nil
	}
	if method == "answerCallbackQuery" && utf8.RuneCountInString(c.form.Get("text")) > notify.MaxAnswer {
		f.bad(w, "answer text over 200 characters")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	switch method {
	case "getMe":
		io.WriteString(w, `{"ok":true,"result":{"id":987,"is_bot":true,"first_name":"Студія","username":"`+f.username+`"}}`)
	case "getUpdates":
		off, _ := strconv.ParseInt(c.form.Get("offset"), 10, 64)
		timeout, _ := strconv.Atoi(c.form.Get("timeout"))
		f.offsets = append(f.offsets, off)
		f.calls = append(f.calls, c)
		deadline := time.Now().Add(time.Duration(timeout) * time.Second)
		for {
			var out []notify.Update
			for _, u := range f.updates {
				if u.UpdateID >= off {
					out = append(out, u)
				}
			}
			if len(out) > 0 || time.Now().After(deadline) {
				b, _ := json.Marshal(out)
				if out == nil {
					b = []byte("[]")
				}
				fmt.Fprintf(w, `{"ok":true,"result":%s}`, b)
				return
			}
			f.mu.Unlock()
			select {
			case <-f.kick:
			case <-time.After(50 * time.Millisecond):
			case <-r.Context().Done():
				f.mu.Lock()
				return
			}
			f.mu.Lock()
		}
	case "sendMessage", "sendPhoto", "sendVideo", "sendDocument", "sendAudio":
		f.nextMsg++
		c.msgID = f.nextMsg
		f.msgs[c.msgID] = &msg{id: c.msgID, chat: c.chat, text: c.text, media: method != "sendMessage", kb: kb, reply: reply,
			silent: c.form.Get("disable_notification") == "true"}
		f.calls = append(f.calls, c)
		fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"chat":{"id":%s,"type":"private"},"date":%d}}`, c.msgID, c.chat, time.Now().Unix())
	case "sendMediaGroup":
		var media []map[string]any
		_ = json.Unmarshal([]byte(c.form.Get("media")), &media)
		if len(media) < 2 || len(media) > notify.MaxAlbum || len(c.files) != len(media) {
			f.t.Errorf("album of %d items, %d files", len(media), len(c.files))
		}
		f.nextMsg++
		c.msgID = f.nextMsg
		if len(media) > 0 {
			c.text, _ = media[0]["caption"].(string)
		}
		f.msgs[c.msgID] = &msg{id: c.msgID, chat: c.chat, text: c.text, media: true}
		f.calls = append(f.calls, c)
		fmt.Fprintf(w, `{"ok":true,"result":[{"message_id":%d,"chat":{"id":%s},"date":1}]}`, c.msgID, c.chat)
	case "editMessageText", "editMessageReplyMarkup":
		id, _ := strconv.Atoi(c.form.Get("message_id"))
		c.msgID = id
		f.calls = append(f.calls, c)
		m := f.msgs[id]
		if m == nil || m.chat != c.chat {
			w.WriteHeader(400)
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`)
			return
		}
		if method == "editMessageText" {
			if m.media {
				w.WriteHeader(400)
				io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: there is no text in the message to edit"}`)
				return
			}
			m.text = c.text
		}
		m.kb = kb
		io.WriteString(w, `{"ok":true,"result":true}`)
	case "deleteMessage":
		id, _ := strconv.Atoi(c.form.Get("message_id"))
		c.msgID = id
		f.calls = append(f.calls, c)
		if f.msgs[id] == nil {
			w.WriteHeader(400)
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`)
			return
		}
		delete(f.msgs, id)
		io.WriteString(w, `{"ok":true,"result":true}`)
	case "getFile":
		f.calls = append(f.calls, c)
		id := c.form.Get("file_id")
		b, ok := f.files[id]
		if !ok {
			w.WriteHeader(400)
			io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: invalid file_id"}`)
			return
		}
		fmt.Fprintf(w, `{"ok":true,"result":{"file_id":%q,"file_size":%d,"file_path":%q}}`, id, len(b), id)
	default: // answerCallbackQuery, setMyCommands, sendChatAction …
		f.calls = append(f.calls, c)
		io.WriteString(w, `{"ok":true,"result":true}`)
	}
}

// push queues an update for getUpdates.
func (f *fakeTG) push(u notify.Update) {
	f.mu.Lock()
	u.UpdateID = f.nextUp
	f.nextUp++
	f.updates = append(f.updates, u)
	f.mu.Unlock()
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// say sends a text message from user in chat.
func (f *fakeTG) say(chat int64, user notify.User, text string) {
	typ := "private"
	if chat < 0 {
		typ = "supergroup"
	}
	f.push(notify.Update{Message: &notify.Message{MessageID: int(time.Now().UnixNano() % 1e6), From: &user,
		Chat: notify.MsgChat{ID: chat, Type: typ}, Date: time.Now().Unix(), Text: text}})
}

// sendMsg sends a whole message (voice, document) from user.
func (f *fakeTG) sendMsg(m notify.Message) { f.push(notify.Update{Message: &m}) }

// pressData presses a button with data under message id (no lookup).
func (f *fakeTG) pressData(chat int64, user notify.User, id int, data string) string {
	f.mu.Lock()
	text := ""
	if m := f.msgs[id]; m != nil && !m.media {
		text = m.text
	}
	f.mu.Unlock()
	qid := fmt.Sprintf("q%d", time.Now().UnixNano())
	typ := "private"
	if chat < 0 {
		typ = "supergroup"
	}
	f.push(notify.Update{CallbackQuery: &notify.CallbackQuery{ID: qid, From: user, Data: data,
		Message: &notify.Message{MessageID: id, Chat: notify.MsgChat{ID: chat, Type: typ}, Date: time.Now().Unix(), Text: text}}})
	return qid
}

// button finds the newest message in chat with a button starting with label.
func (f *fakeTG) button(chat, label string) (int, notify.Button, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	best := 0
	var found notify.Button
	for id, m := range f.msgs {
		if m.chat != chat || id < best {
			continue
		}
		for _, row := range m.kb {
			for _, b := range row {
				if strings.HasPrefix(b.Text, label) {
					best, found = id, b
				}
			}
		}
	}
	return best, found, best > 0
}

// press presses the newest button starting with label in chat and waits for
// the answer of the press.
func (f *fakeTG) press(chat int64, user notify.User, label string) int {
	f.t.Helper()
	var id int
	var b notify.Button
	f.wait("a button "+strconv.Quote(label), func() bool {
		var ok bool
		id, b, ok = f.button(notify.ChatID(chat), label)
		return ok
	})
	qid := f.pressData(chat, user, id, b.Data)
	f.wait("the answer to "+label, func() bool {
		for _, c := range f.all() {
			if c.method == "answerCallbackQuery" && c.form.Get("callback_query_id") == qid {
				return true
			}
		}
		return false
	})
	f.settle()
	return id
}

// settle waits until the bot stops calling for a moment (the edits after
// an answered press).
func (f *fakeTG) settle() {
	n, quiet := -1, 0
	for i := 0; i < 300 && quiet < 8; i++ {
		m := 0
		for _, c := range f.all() {
			if c.method != "getUpdates" {
				m++
			}
		}
		if m == n {
			quiet++
		} else {
			n, quiet = m, 0
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fakeTG) all() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

// of lists the calls of method (in chat when chat != "").
func (f *fakeTG) of(method, chat string) []call {
	var out []call
	for _, c := range f.all() {
		if c.method == method && (chat == "" || c.chat == chat) {
			out = append(out, c)
		}
	}
	return out
}

// text is the current text of a message.
func (f *fakeTG) text(id int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.msgs[id]; m != nil {
		return m.text
	}
	return ""
}

func (f *fakeTG) exists(id int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.msgs[id] != nil
}

// last is the newest message in chat.
func (f *fakeTG) last(chat string) *msg {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *msg
	for _, m := range f.msgs {
		if m.chat == chat && (best == nil || m.id > best.id) {
			best = m
		}
	}
	if best == nil {
		return &msg{}
	}
	cp := *best
	return &cp
}

// waitText waits until a message in chat contains s and returns it.
func (f *fakeTG) waitText(chat, s string) *msg {
	f.t.Helper()
	var got *msg
	f.wait("a message with "+strconv.Quote(s), func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, m := range f.msgs {
			if m.chat == chat && strings.Contains(m.text, s) && (got == nil || m.id > got.id) {
				cp := *m
				got = &cp
			}
		}
		return got != nil
	})
	return got
}

func (f *fakeTG) wait(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s; chat so far:\n%s", what, f.dump(""))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// dump lists the messages (for failures).
func (f *fakeTG) dump(chat string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sb strings.Builder
	for _, c := range f.calls {
		if chat != "" && c.chat != chat || c.method == "getUpdates" {
			continue
		}
		fmt.Fprintf(&sb, "  %s chat=%s id=%d %q\n", c.method, c.chat, c.msgID, c.text)
	}
	return sb.String()
}

// waitIn waits until message id says s (edits are paced and come after the
// answer of a press).
func (f *fakeTG) waitIn(id int, s string) string {
	f.t.Helper()
	var txt string
	f.wait(fmt.Sprintf("message %d to say %q", id, s), func() bool {
		txt = f.text(id)
		return strings.Contains(txt, s)
	})
	return txt
}
