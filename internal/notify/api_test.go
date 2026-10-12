package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyboardsAndSend(t *testing.T) {
	m, c := newMock(t, func(r tgReq) (int, string) {
		return 200, `{"ok":true,"result":{"message_id":77,"chat":{"id":5,"type":"private"},"date":1,"text":"x"}}`
	})
	kb := InlineKeyboard{{{Text: "Так", Data: "x:1", Style: "danger"}, {Text: "Сайт", URL: "https://example.com"}}}
	msg, err := c.Send(context.Background(), "5", "<b>Привіт</b>", &SendOptions{Markup: kb, Silent: true, ReplyTo: 3})
	if err != nil || msg.MessageID != 77 || msg.Chat.ID != 5 {
		t.Fatalf("send: %+v %v", msg, err)
	}
	r := one(t, m)
	var got struct {
		Rows [][]map[string]string `json:"inline_keyboard"`
	}
	if err := json.Unmarshal([]byte(r.form.Get("reply_markup")), &got); err != nil || got.Rows[0][0]["callback_data"] != "x:1" ||
		got.Rows[0][0]["style"] != "danger" || got.Rows[0][1]["url"] != "https://example.com" {
		t.Fatalf("markup %q", r.form.Get("reply_markup"))
	}
	if r.form.Get("disable_notification") != "true" || !strings.Contains(r.form.Get("reply_parameters"), `"message_id":3`) {
		t.Fatalf("options %v", r.form)
	}
	// A reply keyboard and its removal.
	if _, err := c.Send(context.Background(), "5", "x", &SendOptions{Markup: ReplyKeyboard{Rows: [][]string{{"🎬 Новий"}}, Persistent: true, Resize: true}}); err != nil {
		t.Fatal(err)
	}
	if mk := m.all()[1].form.Get("reply_markup"); mk != `{"keyboard":[[{"text":"🎬 Новий"}]],"is_persistent":true,"resize_keyboard":true}` {
		t.Fatalf("reply keyboard %s", mk)
	}
	// Too long callback data is refused before Telegram sees it.
	long := InlineKeyboard{{{Text: "x", Data: strings.Repeat("a", 65)}}}
	if _, err := c.Send(context.Background(), "5", "x", &SendOptions{Markup: long}); err == nil || len(m.all()) != 2 {
		t.Fatalf("65-byte callback data: %v", err)
	}
	if err := (InlineKeyboard{{{Text: "ok", Data: strings.Repeat("я", 32)}}}).Check(); err != nil {
		t.Fatalf("64 bytes are fine: %v", err)
	}
}

func TestEditDeleteAnswer(t *testing.T) {
	var answer atomic.Value
	answer.Store(`{"ok":true,"result":true}`)
	m, c := newMock(t, func(r tgReq) (int, string) {
		if r.method == "editMessageText" && r.form.Get("text") == "same" {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified: specified new message content and reply markup are exactly the same"}`
		}
		if r.method == "deleteMessage" {
			return 400, `{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`
		}
		return 200, answer.Load().(string)
	})
	ctx := context.Background()
	if err := c.Edit(ctx, "5", 9, "same", nil); err != nil {
		t.Fatalf("not modified is not an error: %v", err)
	}
	if err := c.Edit(ctx, "5", 9, "new", InlineKeyboard{{{Text: "a", Data: "b"}}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, "5", 9); !IsGone(err) {
		t.Fatalf("delete of a gone message: %v", err)
	}
	if err := c.EditMarkup(ctx, "5", 9, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Answer(ctx, "q1", strings.Repeat("я", 300), true); err != nil {
		t.Fatal(err)
	}
	reqs := m.all()
	if reqs[1].form.Get("message_id") != "9" || !strings.Contains(reqs[1].form.Get("reply_markup"), `"callback_data":"b"`) {
		t.Fatalf("edit %v", reqs[1].form)
	}
	if reqs[3].method != "editMessageReplyMarkup" || reqs[3].form.Get("reply_markup") != `{"inline_keyboard":[]}` {
		t.Fatalf("markup removal %v", reqs[3].form)
	}
	a := reqs[4].form
	if a.Get("callback_query_id") != "q1" || a.Get("show_alert") != "true" || len([]rune(a.Get("text"))) != MaxAnswer {
		t.Fatalf("answer %v", a)
	}
}

func TestGetUpdatesAndErrors(t *testing.T) {
	var mode atomic.Int32
	m, c := newMock(t, func(r tgReq) (int, string) {
		switch mode.Load() {
		case 1:
			return 409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request; make sure that only one bot instance is running"}`
		case 2:
			return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`
		}
		return 200, `{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"from":{"id":7,"first_name":"Іван","username":"ivan"},"chat":{"id":7,"type":"private"},"date":5,"text":"/start 123456"}},
			{"update_id":11,"callback_query":{"id":"cb","from":{"id":7,"first_name":"Іван"},"message":{"message_id":2,"chat":{"id":7,"type":"private"},"date":5,"text":"Меню"},"data":"p:abc"}},
			{"update_id":12,"message":{"message_id":3,"from":{"id":7},"chat":{"id":7,"type":"private"},"date":6,"voice":{"file_id":"F1","file_unique_id":"u","duration":3,"mime_type":"audio/ogg","file_size":1234}}}]}`
	})
	ups, err := c.GetUpdates(context.Background(), 10, 0, []string{"message", "callback_query"})
	if err != nil || len(ups) != 3 {
		t.Fatalf("updates %v %v", ups, err)
	}
	if ups[0].Message.From.Name() != "Іван (@ivan)" || ups[1].CallbackQuery.Data != "p:abc" || ups[1].CallbackQuery.Message.Text != "Меню" ||
		ups[2].Message.Voice == nil || ups[2].Message.Voice.FileSize != 1234 {
		t.Fatalf("parsed %+v", ups)
	}
	r := one(t, m)
	if r.form.Get("offset") != "10" || r.form.Get("allowed_updates") != `["message","callback_query"]` {
		t.Fatalf("request %v", r.form)
	}
	mode.Store(1)
	_, err = c.GetUpdates(context.Background(), 0, 0, nil)
	if conflict, webhook := Conflict(err); !conflict || webhook || Temporary(err) {
		t.Fatalf("409: %v", err)
	}
	mode.Store(2)
	_, err = c.Send(context.Background(), "7", "x", nil)
	if RetryAfter(err) != 7*time.Second || !Temporary(err) {
		t.Fatalf("429: %v", err)
	}
	if !Unauthorized(apiError(401, "", 0, 0)) || Unauthorized(err) {
		t.Fatal("Unauthorized")
	}
}

// uploadMock parses streamed multipart uploads.
type upload struct {
	method string
	fields map[string]string
	files  map[string][]byte
	names  map[string]string
	length int64
}

func uploadServer(t *testing.T, delay time.Duration) (*httptest.Server, func() []upload) {
	t.Helper()
	var mu sync.Mutex
	var got []upload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delay > 0 {
			time.Sleep(delay) // never reads: the client's upload stalls
			return
		}
		u := upload{method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], fields: map[string]string{},
			files: map[string][]byte{}, names: map[string]string{}, length: r.ContentLength}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				u.files[p.FormName()], u.names[p.FormName()] = b, p.FileName()
			} else {
				u.fields[p.FormName()] = string(b)
			}
		}
		mu.Lock()
		got = append(got, u)
		mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":3,"chat":{"id":5,"type":"private"},"date":1}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []upload {
		mu.Lock()
		defer mu.Unlock()
		return append([]upload(nil), got...)
	}
}

func TestUploadStreams(t *testing.T) {
	srv, got := uploadServer(t, 0)
	c := New(testToken, srv.URL)
	if c.UploadLimit() != MaxUploadLocal || New(testToken, "").UploadLimit() != MaxUpload || New(testToken, "").Local() {
		t.Fatal("upload limits")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "film.mp4")
	data := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MB
	if err := os.WriteFile(video, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cover := writePNG(t, image.NewRGBA(image.Rect(0, 0, 1600, 900)))
	thumb, err := ThumbJPEG(cover)
	if err != nil {
		t.Fatal(err)
	}
	if img, _, err := image.Decode(bytes.NewReader(thumb)); err != nil || img.Bounds().Dx() > 320 || len(thumb) >= 200<<10 {
		t.Fatalf("thumbnail %v %v (%d bytes)", img.Bounds(), err, len(thumb))
	}
	coverJPG, err := PhotoJPEG(cover)
	if err != nil || http.DetectContentType(coverJPG) != "image/jpeg" {
		t.Fatalf("cover %v %s", err, http.DetectContentType(coverJPG))
	}
	var last, total atomic.Int64
	m, err := c.Upload(context.Background(), Upload{Method: "sendVideo", Field: "video", ChatID: "5", Path: video,
		Name: `Мій "фільм".mp4`, Caption: "🎬 <b>Фільм</b>", Thumb: thumb, Cover: coverJPG,
		Params:   map[string]string{"supports_streaming": "true", "duration": "62", "width": "1920", "height": "1080"},
		Opt:      &SendOptions{Markup: InlineKeyboard{{{Text: "▶", Data: "p:1"}}}},
		Progress: func(sent, tot int64) { last.Store(sent); total.Store(tot) }})
	if err != nil || m.MessageID != 3 {
		t.Fatalf("upload: %v", err)
	}
	u := got()[0]
	if u.method != "sendVideo" || !bytes.Equal(u.files["video"], data) || u.names["video"] != "Мій _фільм_.mp4" ||
		u.fields["supports_streaming"] != "true" || u.fields["duration"] != "62" || u.fields["caption"] != "🎬 <b>Фільм</b>" ||
		u.fields["parse_mode"] != "HTML" || u.fields["cover"] != "attach://cover_image" || !bytes.Equal(u.files["cover_image"], coverJPG) ||
		!bytes.Equal(u.files["thumbnail"], thumb) || !strings.Contains(u.fields["reply_markup"], "p:1") {
		t.Fatalf("upload fields %v names %v", u.fields, u.names)
	}
	if u.length <= int64(len(data)) || last.Load() != int64(len(data)) || total.Load() != int64(len(data)) {
		t.Fatalf("length %d, progress %d/%d", u.length, last.Load(), total.Load())
	}
	// Too big for the client's limit: refused before any byte goes out.
	big := New(testToken, "")
	big.base = "https://api.telegram.org"
	f, _ := os.Create(filepath.Join(dir, "big.mp4"))
	f.Truncate(MaxUpload + 1)
	f.Close()
	_, err = big.Upload(context.Background(), Upload{Method: "sendVideo", Field: "video", ChatID: "5", Path: filepath.Join(dir, "big.mp4")})
	var te *Error
	if !errors.As(err, &te) || te.Code != 413 || !strings.Contains(err.Error(), "50 МБ") {
		t.Fatalf("over the limit: %v", err)
	}
}

func TestUploadStall(t *testing.T) {
	old := uploadStall
	uploadStall = 300 * time.Millisecond
	t.Cleanup(func() { uploadStall = old })
	srv, _ := uploadServer(t, 3*time.Second)
	c := New(testToken, srv.URL)
	path := filepath.Join(t.TempDir(), "big.mp4")
	f, _ := os.Create(path)
	f.Truncate(64 << 20) // more than the socket buffers hold
	f.Close()
	start := time.Now()
	_, err := c.Upload(context.Background(), Upload{Method: "sendVideo", Field: "video", ChatID: "5", Path: path})
	if err == nil || !strings.Contains(err.Error(), "зупинилося") || time.Since(start) > 2500*time.Millisecond {
		t.Fatalf("stalled upload: %v after %s", err, time.Since(start))
	}
}

func TestAlbumAndDownload(t *testing.T) {
	srv, got := uploadServer(t, 0)
	c := New(testToken, srv.URL)
	var paths []string
	for i := 0; i < 12; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 64, 36))
		img.Set(1, 1, color.RGBA{uint8(i), 0, 0, 255})
		paths = append(paths, writePNG(t, img))
	}
	if err := c.SendAlbum(context.Background(), "5", append([]string{"/nope.png"}, paths...), "🖼 <b>Кадри</b>"); err != nil {
		t.Fatal(err)
	}
	u := got()[0]
	var media []map[string]string
	if err := json.Unmarshal([]byte(u.fields["media"]), &media); err != nil || len(media) != MaxAlbum || len(u.files) != MaxAlbum ||
		media[0]["caption"] != "🖼 <b>Кадри</b>" || media[9]["media"] != "attach://p9" || media[1]["caption"] != "" {
		t.Fatalf("album %s (%d files)", u.fields["media"], len(u.files))
	}
	// One picture: a plain photo.
	if err := c.SendAlbum(context.Background(), "5", paths[:1], "x"); err != nil || got()[1].method != "sendPhoto" {
		t.Fatalf("single: %v", err)
	}

	// Downloads: through /file/bot<token>/<path>, or a local server's own path.
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file/bot"+testToken+"/voice/file_1.oga" {
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, "OggS-voice")
	}))
	t.Cleanup(files.Close)
	dc := New(testToken, files.URL)
	dst := filepath.Join(t.TempDir(), "v.ogg")
	if err := dc.Download(context.Background(), File{FilePath: "voice/file_1.oga", FileSize: 10}, dst, 100); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "OggS-voice" {
		t.Fatalf("downloaded %q", b)
	}
	if err := dc.Download(context.Background(), File{FilePath: "voice/file_1.oga", FileSize: 500}, dst, 100); err == nil {
		t.Fatal("a file over the limit was downloaded")
	}
	if err := dc.Download(context.Background(), File{FilePath: "voice/file_1.oga"}, dst, 5); err == nil {
		t.Fatal("an unknown-size file over the limit was kept")
	}
	local := filepath.Join(t.TempDir(), "local.txt")
	os.WriteFile(local, []byte("історія"), 0o644)
	dst2 := filepath.Join(t.TempDir(), "copy.txt")
	if err := dc.Download(context.Background(), File{FilePath: local}, dst2, 100); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst2); string(b) != "історія" {
		t.Fatalf("local copy %q", b)
	}
	if FormatSize(128<<20) != "128 МБ" || FormatSize(1536<<10) != "1.5 МБ" || FormatSize(2<<30) != "2.0 ГБ" {
		t.Fatalf("sizes %s %s %s", FormatSize(128<<20), FormatSize(1536<<10), FormatSize(2<<30))
	}
	_ = jpeg.DefaultQuality
}
