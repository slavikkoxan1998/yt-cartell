package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math/rand"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// Never a real token: every request goes to httptest servers.
const testToken = "123456789:AAtest-Secret_token0123456789"

type tgReq struct {
	verb, path, method string
	query              url.Values
	form               url.Values
	contentType        string
	photo              []byte
	photoName          string
	photoType          string
}

// tgMock records requests; reply decides the answer (default: ok).
type tgMock struct {
	mu    sync.Mutex
	reqs  []tgReq
	reply func(r tgReq) (status int, body string)
}

func (m *tgMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rq := tgReq{verb: r.Method, path: r.URL.Path, query: r.URL.Query(), contentType: r.Header.Get("Content-Type")}
	if i := strings.LastIndex(r.URL.Path, "/"); i >= 0 {
		rq.method = r.URL.Path[i+1:]
	}
	mt, _, _ := mime.ParseMediaType(rq.contentType)
	switch mt {
	case "multipart/form-data":
		if err := r.ParseMultipartForm(64 << 20); err == nil {
			rq.form = url.Values(r.MultipartForm.Value)
			if fhs := r.MultipartForm.File["photo"]; len(fhs) == 1 {
				f, _ := fhs[0].Open()
				rq.photo, _ = io.ReadAll(f)
				f.Close()
				rq.photoName, rq.photoType = fhs[0].Filename, fhs[0].Header.Get("Content-Type")
			}
		}
	case "application/x-www-form-urlencoded":
		_ = r.ParseForm()
		rq.form = r.PostForm
	}
	m.mu.Lock()
	m.reqs = append(m.reqs, rq)
	reply := m.reply
	m.mu.Unlock()
	status, body := 200, `{"ok":true,"result":{"message_id":1}}`
	if reply != nil {
		status, body = reply(rq)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func (m *tgMock) all() []tgReq {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]tgReq(nil), m.reqs...)
}

func newMock(t *testing.T, reply func(r tgReq) (int, string)) (*tgMock, *Client) {
	t.Helper()
	m := &tgMock{reply: reply}
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return m, New(testToken, srv.URL+"/")
}

func one(t *testing.T, m *tgMock) tgReq {
	t.Helper()
	reqs := m.all()
	if len(reqs) != 1 {
		t.Fatalf("want 1 request, got %d", len(reqs))
	}
	return reqs[0]
}

func TestSendMessageRequest(t *testing.T) {
	m, c := newMock(t, nil)
	html := "✅ <b>«" + Escape(`Том & "Джеррі" <3`) + "»</b> — готово"
	if err := c.SendMessage(context.Background(), " 777 ", html); err != nil {
		t.Fatal(err)
	}
	r := one(t, m)
	if r.verb != http.MethodPost || r.path != "/bot"+testToken+"/sendMessage" || !strings.HasPrefix(r.contentType, "application/x-www-form-urlencoded") {
		t.Fatalf("request %s %s %s", r.verb, r.path, r.contentType)
	}
	want := "✅ <b>«Том &amp; &quot;Джеррі&quot; &lt;3»</b> — готово"
	if r.form.Get("chat_id") != "777" || r.form.Get("text") != want || r.form.Get("parse_mode") != "HTML" {
		t.Fatalf("form %v", r.form)
	}
	var lp struct {
		IsDisabled bool `json:"is_disabled"`
	}
	if err := json.Unmarshal([]byte(r.form.Get("link_preview_options")), &lp); err != nil || !lp.IsDisabled {
		t.Fatalf("link previews must be disabled: %q", r.form.Get("link_preview_options"))
	}
	// Too long texts are shortened to Telegram's limit.
	long := "<b>" + strings.Repeat("я", 5000) + "</b>"
	if err := c.SendMessage(context.Background(), "@my_channel", long); err != nil {
		t.Fatal(err)
	}
	r = m.all()[1]
	if got := r.form.Get("text"); visibleLen(got) != MaxMessage || !strings.HasSuffix(got, "…</b>") || r.form.Get("chat_id") != "@my_channel" {
		t.Fatalf("long text: %d visible, tail %q", visibleLen(got), got[len(got)-12:])
	}
}

func writePNG(t *testing.T, img image.Image) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSendPhotoSmallAsIs(t *testing.T) {
	m, c := newMock(t, nil)
	img := image.NewRGBA(image.Rect(0, 0, 64, 36))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	path := writePNG(t, img)
	orig, _ := os.ReadFile(path)
	caption := "🎬 Відео готове: <b>«" + Escape("A&B") + "»</b> · 1:02 · 16:9\n<code>" + Escape(`C:\x\final.mp4`) + "</code>"
	if err := c.SendPhoto(context.Background(), "-100123", path, caption); err != nil {
		t.Fatal(err)
	}
	r := one(t, m)
	if r.path != "/bot"+testToken+"/sendPhoto" || !strings.HasPrefix(r.contentType, "multipart/form-data") {
		t.Fatalf("request %s %s", r.path, r.contentType)
	}
	if r.form.Get("chat_id") != "-100123" || r.form.Get("caption") != caption || r.form.Get("parse_mode") != "HTML" {
		t.Fatalf("fields %v", r.form)
	}
	if !bytes.Equal(r.photo, orig) || r.photoName != "photo.png" || r.photoType != "image/png" {
		t.Fatalf("photo %d bytes %q %q (want the file as is)", len(r.photo), r.photoName, r.photoType)
	}
}

func TestSendPhotoLargeReencoded(t *testing.T) {
	m, c := newMock(t, nil)
	// Noise does not compress: well over the 1 MB threshold.
	img := image.NewNRGBA(image.Rect(0, 0, 2000, 1125))
	rng := rand.New(rand.NewSource(1))
	rng.Read(img.Pix)
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	path := writePNG(t, img)
	if st, _ := os.Stat(path); st.Size() <= photoKeep {
		t.Fatalf("test image too small: %d", st.Size())
	}
	caption := "<b>" + strings.Repeat("ш", 3000) + "</b> &amp; хвіст"
	if err := c.SendPhoto(context.Background(), "777", path, caption); err != nil {
		t.Fatal(err)
	}
	r := one(t, m)
	if r.photoType != "image/jpeg" || r.photoName != "photo.jpg" {
		t.Fatalf("re-encoded photo %q %q", r.photoName, r.photoType)
	}
	got, err := jpeg.Decode(bytes.NewReader(r.photo))
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Bounds(); b.Dx() != 1280 || b.Dy() != 720 {
		t.Fatalf("re-encoded size %v", b)
	}
	cap := r.form.Get("caption")
	if n := visibleLen(cap); n != MaxCaption || !strings.HasSuffix(cap, "…</b>") || strings.Contains(cap, "хвіст") {
		t.Fatalf("caption %d visible chars, tail %q", n, cap[len(cap)-10:])
	}
}

func TestSendPhotoFileProblems(t *testing.T) {
	m, c := newMock(t, nil)
	err := c.SendPhoto(context.Background(), "777", filepath.Join(t.TempDir(), "missing.jpg"), "x")
	if !errors.Is(err, ErrPhoto) || !strings.Contains(err.Error(), "Не вдалося прочитати зображення") {
		t.Fatalf("missing file: %v", err)
	}
	junk := filepath.Join(t.TempDir(), "junk.jpg")
	if err := os.WriteFile(junk, bytes.Repeat([]byte("not an image "), 200_000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.SendPhoto(context.Background(), "777", junk, ""); !errors.Is(err, ErrPhoto) {
		t.Fatalf("junk file: %v", err)
	}
	if n := len(m.all()); n != 0 {
		t.Fatalf("nothing may be uploaded, got %d requests", n)
	}
}

func TestGetMeAndFindChat(t *testing.T) {
	updates := `[]`
	m, c := newMock(t, func(r tgReq) (int, string) {
		switch r.method {
		case "getMe":
			return 200, `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Студія","username":"cartell_bot"}}`
		case "getUpdates":
			return 200, `{"ok":true,"result":` + updates + `}`
		}
		return 404, `{"ok":false,"error_code":404,"description":"Not Found"}`
	})
	name, err := c.GetMe(context.Background())
	if err != nil || name != "cartell_bot" {
		t.Fatalf("getMe %q %v", name, err)
	}
	if r := m.all()[0]; r.verb != http.MethodGet || r.path != "/bot"+testToken+"/getMe" {
		t.Fatalf("getMe request %s %s", r.verb, r.path)
	}

	if _, err := c.FindChat(context.Background()); !errors.Is(err, ErrNoChat) {
		t.Fatalf("no updates: %v", err)
	}
	updates = `[
		{"update_id":10,"message":{"message_id":1,"chat":{"id":111,"type":"private","first_name":"Старий"},"text":"/start"}},
		{"update_id":12,"my_chat_member":{"chat":{"id":-100500,"type":"supergroup","title":"Моя група"}}},
		{"update_id":13,"message":{"message_id":2,"chat":{"id":222,"type":"private","first_name":"Іван","last_name":"К","username":"ivan_k"},"text":"/start"}},
		{"update_id":11,"channel_post":{"chat":{"id":-100777,"type":"channel","title":"Канал"}}}
	]`
	ch, err := c.FindChat(context.Background())
	if err != nil || ch.ID != "222" || ch.Title != "Іван К (@ivan_k)" || ch.Type != "private" {
		t.Fatalf("chat %+v %v", ch, err)
	}
	r := m.all()[len(m.all())-1]
	if r.path != "/bot"+testToken+"/getUpdates" || r.query.Get("offset") != "" || r.query.Get("timeout") != "0" {
		t.Fatalf("getUpdates must not confirm updates: %s %v", r.path, r.query)
	}
	updates = `[{"update_id":5,"message":{"chat":{"id":-100500,"type":"group","title":"Монтажна"}}}]`
	if ch, err = c.FindChat(context.Background()); err != nil || ch.ID != "-100500" || ch.Title != "Монтажна" {
		t.Fatalf("group chat %+v %v", ch, err)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
		code   int
	}{
		{401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, "Токен бота не підходить", 401},
		{404, `{"ok":false,"error_code":404,"description":"Not Found"}`, "Токен бота не підходить", 404},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, "Бот ще не бачив цей чат — напиши йому /start", 400},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1001234}}`, "новий ID чату: -1001234", 400},
		{400, `{"ok":false,"error_code":400,"description":"Bad Request: can't parse entities"}`, "Telegram відхилив запит: Bad Request: can't parse entities", 400},
		{403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`, "Бота заблоковано", 403},
		{403, `{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the channel chat"}`, "Бот не може писати в цей чат", 403},
		{409, `{"ok":false,"error_code":409,"description":"Conflict: can't use getUpdates method while webhook is active"}`, "вебхук", 409},
		{429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`, "зачекати 7 с", 429},
		{502, `<html>Bad Gateway</html>`, "Telegram тимчасово недоступний", 502},
		{200, `not json`, "незрозумілу відповідь", 200},
	}
	for _, tc := range cases {
		_, c := newMock(t, func(tgReq) (int, string) { return tc.status, tc.body })
		err := c.SendMessage(context.Background(), "777", "hi")
		var te *Error
		if !errors.As(err, &te) || te.Code != tc.code || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d %s → %v (code %v)", tc.status, tc.body, err, te)
		}
		if err != nil && strings.Contains(err.Error(), testToken) {
			t.Errorf("token leaked: %v", err)
		}
	}
	var te *Error
	_, c := newMock(t, func(tgReq) (int, string) {
		return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":7}}`
	})
	if err := c.SendMessage(context.Background(), "777", "hi"); !errors.As(err, &te) || te.RetryAfter != 7 {
		t.Fatalf("retry_after: %v", err)
	}
}

func TestValidationBeforeRequest(t *testing.T) {
	m, c := newMock(t, nil)
	srvBase := c.base
	cases := []struct {
		c    *Client
		chat string
		want string
	}{
		{New("", srvBase), "777", "Вкажи токен бота"},
		{New("not-a-token", srvBase), "777", "Токен бота має вигляд"},
		{New("123:abc/../../x", srvBase), "777", "Токен бота має вигляд"},
		{c, "", "Вкажи ID чату"},
		{c, "мій чат", "ID чату — це число"},
		{c, "12 34", "ID чату — це число"},
	}
	for _, tc := range cases {
		if err := tc.c.SendMessage(context.Background(), tc.chat, "x"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("chat %q: %v", tc.chat, err)
		}
	}
	if _, err := New("bad token", srvBase).GetMe(context.Background()); err == nil {
		t.Error("getMe with a malformed token")
	}
	if n := len(m.all()); n != 0 {
		t.Fatalf("invalid settings must not reach Telegram (%d requests)", n)
	}
}

// chainText is every error text in err's chain.
func chainText(err error) string {
	var b strings.Builder
	for e := err; e != nil; e = errors.Unwrap(e) {
		fmt.Fprintf(&b, "%v | %+v | %#v\n", e, e, e.Error())
	}
	return b.String()
}

func TestNetworkErrorsHideToken(t *testing.T) {
	secret := testToken[strings.IndexByte(testToken, ':')+1:]
	// Nothing listens there any more.
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	c := New(testToken, base)
	err := c.SendMessage(context.Background(), "777", "hi")
	var te *Error
	if !errors.As(err, &te) || te.Code != 0 || !strings.Contains(err.Error(), "Немає зв'язку з Telegram") {
		t.Fatalf("offline: %v", err)
	}
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("cause lost: %s", chainText(err))
	}
	if all := chainText(err) + ue.URL; strings.Contains(all, secret) || !strings.Contains(all, "bot•••") {
		t.Fatalf("token leaked or not redacted:\n%s", all)
	}

	// A server that does not answer in time.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	c = New(testToken, slow.URL)
	c.timeout = 80 * time.Millisecond
	t0 := time.Now()
	err = c.SendMessage(context.Background(), "777", "hi")
	if !strings.Contains(fmt.Sprint(err), "Telegram не відповів вчасно") || time.Since(t0) > 5*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(t0))
	}
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(chainText(err), secret) {
		t.Fatalf("timeout chain:\n%s", chainText(err))
	}
	if s := Redact("GET https://api.telegram.org/bot"+testToken+"/getMe failed; token "+testToken, testToken); strings.Contains(s, secret) {
		t.Fatalf("Redact: %s", s)
	}
	if s := Redact("bot987:AbcdefGhijkl_-x/sendMessage", ""); s != "bot•••/sendMessage" {
		t.Fatalf("Redact without the token: %s", s)
	}
}

func TestEscapeAndClip(t *testing.T) {
	if got := Escape(`<b>Tom & "Jerry"</b>`); got != "&lt;b&gt;Tom &amp; &quot;Jerry&quot;&lt;/b&gt;" {
		t.Fatalf("escape %q", got)
	}
	short := "<b>Привіт</b> &amp; <code>x</code>"
	if ClipHTML(short, 100) != short || visibleLen(short) != 10 || ClipHTML(short, 10) != short {
		t.Fatalf("short text changed (%d visible)", visibleLen(short))
	}
	// Entities count as one character and are never split; tags close.
	got := ClipHTML("<b>ab&amp;cd</b><i>ef</i>", 5)
	if got != "<b>ab&amp;c…</b>" || visibleLen(got) != 5 {
		t.Fatalf("clip %q", got)
	}
	got = ClipHTML(`<a href="https://x.y/?a=1&amp;b=2">посилання</a> кінець`, 4)
	if got != `<a href="https://x.y/?a=1&amp;b=2">пос…</a>` {
		t.Fatalf("clip link %q", got)
	}
	// Emoji take two characters, like Telegram counts them.
	if got = ClipHTML("🎬🎬🎬", 4); got != "🎬…" || !utf8.ValidString(got) {
		t.Fatalf("clip emoji %q", got)
	}
	if got = ClipHTML("1 < 2 & 3", 100); got != "1 < 2 & 3" {
		t.Fatalf("loose characters %q", got)
	}
}

func TestFitPhoto(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 900, 3000)) // transparent, tall
	got := fitPhoto(src, photoMaxSide)
	if b := got.Bounds(); b.Dx() != 384 || b.Dy() != 1280 {
		t.Fatalf("bounds %v", b)
	}
	if r, g, b, a := got.At(10, 10).RGBA(); a != 0xffff || r != 0xffff || g != 0xffff || b != 0xffff {
		t.Fatalf("transparent pixels must become white: %v", color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)})
	}
}
