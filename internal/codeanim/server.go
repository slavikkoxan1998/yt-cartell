package codeanim

// The in-process HTTP server the browser loads the engine from:
// 127.0.0.1 only, every path under a random per-job token
// (/<token>/clip.html, /<token>/lib/…, /<token>/clips/<id>.js), plus
// /<token>/__file__/<n> for the images the job's spec names (nothing else
// from disk) and POST /<token>/__frame__/<slot> where the page hands over
// the raw RGBA of a frame.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type job struct {
	token string
	files []string // absolute paths the spec references (index = n in __file__/n)
	frame int      // bytes of one RGBA frame

	mu    sync.Mutex
	slots map[int][]byte // frames handed over, by page slot
}

func (j *job) take(slot int) []byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	b := j.slots[slot]
	delete(j.slots, slot)
	return b
}

var srv struct {
	once sync.Once
	err  error
	addr string
	mu   sync.Mutex
	jobs map[string]*job
}

// serverAddr starts the server once and returns host:port.
func serverAddr() (string, error) {
	srv.once.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			srv.err = err
			return
		}
		srv.addr = ln.Addr().String()
		s := &http.Server{Handler: http.HandlerFunc(serve), ReadHeaderTimeout: 10 * time.Second}
		go func() { _ = s.Serve(ln) }()
	})
	return srv.addr, srv.err
}

func newJob(files []string, frameBytes int) *job {
	_, _ = serverAddr()
	var b [16]byte
	_, _ = rand.Read(b[:])
	j := &job{token: hex.EncodeToString(b[:]), files: files, frame: frameBytes, slots: map[int][]byte{}}
	srv.mu.Lock()
	if srv.jobs == nil {
		srv.jobs = map[string]*job{}
	}
	srv.jobs[j.token] = j
	srv.mu.Unlock()
	return j
}

func dropJob(j *job) {
	srv.mu.Lock()
	delete(srv.jobs, j.token)
	srv.mu.Unlock()
}

var framePool sync.Map // frame size → *sync.Pool

func getFrameBuf(n int) []byte {
	p, _ := framePool.LoadOrStore(n, &sync.Pool{New: func() any { return make([]byte, n) }})
	return p.(*sync.Pool).Get().([]byte)
}

func putFrameBuf(b []byte) {
	if p, ok := framePool.Load(len(b)); ok {
		p.(*sync.Pool).Put(b) //nolint:staticcheck // slices are fine here
	}
}

func serve(w http.ResponseWriter, r *http.Request) {
	tok, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	srv.mu.Lock()
	j := srv.jobs[tok]
	srv.mu.Unlock()
	if j == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case strings.HasPrefix(rest, "__frame__/"):
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		slot, err := strconv.Atoi(strings.TrimPrefix(rest, "__frame__/"))
		if err != nil || r.ContentLength != int64(j.frame) {
			http.Error(w, "bad frame", http.StatusBadRequest)
			return
		}
		buf := getFrameBuf(j.frame)
		if _, err := io.ReadFull(r.Body, buf); err != nil {
			putFrameBuf(buf)
			http.Error(w, "short frame", http.StatusBadRequest)
			return
		}
		j.mu.Lock()
		if old := j.slots[slot]; old != nil {
			putFrameBuf(old)
		}
		j.slots[slot] = buf
		j.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	case strings.HasPrefix(rest, "__file__/"):
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rest, "__file__/"), path.Ext(rest)))
		if err != nil || n < 0 || n >= len(j.files) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, j.files[n])
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if id, ok := strings.CutPrefix(rest, "clips/"); ok && strings.HasSuffix(id, ".js") {
		js, ok := grammarJS(strings.TrimSuffix(id, ".js"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(js)
		return
	}
	data, err := readEngine(rest)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "read", http.StatusInternalServerError)
		}
		return
	}
	ct := mime.TypeByExtension(path.Ext(rest))
	switch path.Ext(rest) {
	case ".js":
		ct = "text/javascript; charset=utf-8"
	case ".html":
		ct = "text/html; charset=utf-8"
	case ".woff":
		ct = "font/woff"
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	_, _ = w.Write(data)
}
