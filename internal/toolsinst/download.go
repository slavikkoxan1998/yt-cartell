package toolsinst

// Downloads: "<dst>.part" first, resumed with HTTP Range after a broken
// connection, a stall, a cancel or the next run; renamed into place only once
// the size and the SHA-256 match. A part that fails the check is deleted.
// Mirrors serve the very same bytes (same size and digest), so a part
// started on one continues from another.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Asset is one file to download: its official URL, optional mirrors that
// serve the same bytes, and what it must be. SHA256 is required — Download
// refuses a file it cannot verify.
type Asset struct {
	URL     string
	Mirrors []string
	Size    int64  // bytes (0 = unknown; then only the digest is checked)
	SHA256  string // hex
	Name    string // file name in the download folder ("" = the URL's last segment)
}

// FileName is the name the asset is stored under.
func (a Asset) FileName() string {
	if a.Name != "" {
		return safeName(a.Name)
	}
	u := a.URL
	if p, err := url.Parse(u); err == nil {
		u = p.Path
	}
	return safeName(path.Base(u))
}

// Tunables (package vars so tests can shorten them).
var (
	httpClient      = &http.Client{} // nil Transport = http.DefaultTransport
	downloadStall   = 60 * time.Second
	downloadRetries = 3 // extra attempts per URL for transient errors
	retryPause      = 2 * time.Second
	checksumTimeout = 30 * time.Second
)

const userAgent = "YT-Cartell-Studio"

var errStalled = errors.New("download stalled")

// permanent marks an error another attempt at the same URL cannot fix.
type permanent struct{ error }

func (p permanent) Unwrap() error { return p.error }

// UserError is a message ready for the user (Ukrainian, says what to do).
type UserError struct{ Err error }

func (u UserError) Error() string { return u.Err.Error() }
func (u UserError) Unwrap() error { return u.Err }

// errChecksum: the downloaded bytes are not the published file.
var errChecksum = errors.New("Завантажений файл пошкоджений (контрольна сума не збігається) — спробуй ще раз")

// Download fetches a into dst. onBytes (may be nil) gets the bytes on disk
// so far and the expected total (0 = unknown). A cancelled ctx returns
// ctx.Err() and keeps the part for the next run. The URL and then each
// mirror are tried; errors are friendly Ukrainian messages.
func Download(ctx context.Context, a Asset, dst string, onBytes func(done, total int64)) error {
	want := strings.ToLower(strings.TrimSpace(a.SHA256))
	if !isHex64(want) {
		return errors.New("Для цього файлу немає контрольної суми — завантаження зупинено з міркувань безпеки")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return writeErr(err)
	}
	// Downloaded by an earlier run that stopped later (unpacking…): keep it.
	if have := fileSize(dst); have > 0 && (a.Size <= 0 || have == a.Size) {
		if sum, err := FileSHA256(dst); err == nil && sum == want {
			if onBytes != nil {
				onBytes(have, have)
			}
			return nil
		}
		_ = os.Remove(dst)
	}
	part := dst + ".part"
	ds := &dlState{h: sha256.New()}
	urls := append([]string{a.URL}, a.Mirrors...)
	var last error
	for _, u := range urls {
		if strings.TrimSpace(u) == "" {
			continue
		}
		err := fetchURL(ctx, u, a.Size, part, ds, onBytes)
		if err == nil {
			if verr := ds.verify(part, a.Size, want); verr != nil {
				_ = os.Remove(part)
				ds.reset()
				last = verr
				continue // a mirror may serve the right file
			}
			if err := os.Rename(part, dst); err != nil {
				return writeErr(err)
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		last = dlErr(hostOf(u), err)
	}
	if last == nil {
		last = errors.New("Немає адреси для завантаження")
	}
	return last
}

// fetchURL downloads from one URL with retries for transient errors.
func fetchURL(ctx context.Context, u string, size int64, part string, ds *dlState, onBytes func(int64, int64)) error {
	for attempt := 0; ; attempt++ {
		err := fetchOnce(ctx, u, size, part, ds, onBytes)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var p permanent
		var ue UserError
		if errors.As(err, &p) || errors.As(err, &ue) || attempt >= downloadRetries {
			return err
		}
		if serr := sleep(ctx, retryPause*time.Duration(attempt+1)); serr != nil {
			return serr
		}
	}
}

// dlState is one file's download across attempts: the SHA-256 of the part so far.
type dlState struct {
	h      hash.Hash
	hashed int64
}

func (d *dlState) reset() {
	d.h.Reset()
	d.hashed = 0
}

// catchUp makes the hash cover exactly the first have bytes of the part.
func (d *dlState) catchUp(part string, have int64) error {
	if d.hashed == have {
		return nil
	}
	d.reset()
	if have <= 0 {
		return nil
	}
	f, err := os.Open(part)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(d.h, io.LimitReader(f, have))
	d.hashed = n
	if err != nil || n != have {
		d.reset()
		_ = os.Remove(part)
		return fmt.Errorf("не вдалося прочитати недокачаний файл: %v", err)
	}
	return nil
}

// verify: the part has the expected size and digest.
func (d *dlState) verify(part string, size int64, sha string) error {
	got := fileSize(part)
	if size > 0 && got != size {
		return fmt.Errorf("Завантажений файл має неправильний розмір (%s замість %s) — спробуй ще раз", FormatSize(got), FormatSize(size))
	}
	if err := d.catchUp(part, got); err != nil {
		return errors.New("Не вдалося перевірити завантажений файл — спробуй ще раз")
	}
	if hex.EncodeToString(d.h.Sum(nil)) != sha {
		return errChecksum
	}
	return nil
}

// fetchOnce makes one request, resuming the part when it has bytes.
func fetchOnce(ctx context.Context, u string, size int64, part string, ds *dlState, onBytes func(int64, int64)) error {
	have := fileSize(part)
	if size > 0 && have > size {
		_ = os.Remove(part)
		have = 0
	}
	if err := ds.catchUp(part, have); err != nil {
		return err
	}
	if size > 0 && have == size {
		return nil // complete from an earlier run
	}
	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(downloadStall, func() { cancel(errStalled) })
	defer stall.Stop()
	req, err := http.NewRequestWithContext(actx, http.MethodGet, u, nil)
	if err != nil {
		return permanent{err}
	}
	req.Header.Set("User-Agent", userAgent)
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return stalledOr(actx, err)
	}
	defer res.Body.Close()
	switch st := res.StatusCode; {
	case st == http.StatusPartialContent && have > 0 && rangeFrom(res.Header.Get("Content-Range")) == have:
	case st == http.StatusOK:
		have = 0 // the whole file comes
		ds.reset()
	case st == http.StatusPartialContent || st == http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part) // the part does not fit this file: start over
		ds.reset()
		return fmt.Errorf("сервер не продовжив завантаження (%d)", st)
	case st == http.StatusNotFound || st == http.StatusGone:
		return permanent{fmt.Errorf("файл не знайдено на сервері (%d)", st)}
	case st == http.StatusUnauthorized || st == http.StatusForbidden:
		return permanent{fmt.Errorf("сервер не дає завантажити файл (%d)", st)}
	default:
		return fmt.Errorf("сервер відповів %d", st)
	}
	total := size
	if total <= 0 && res.ContentLength > 0 {
		total = have + res.ContentLength
	}
	flag := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if have > 0 {
		flag = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(part, flag, 0o644)
	if err != nil {
		return writeErr(err)
	}
	room := int64(math.MaxInt64)
	if size > 0 {
		room = size - have
	}
	buf := make([]byte, 256<<10)
	done := have
	if onBytes != nil {
		onBytes(done, total)
	}
	for {
		n, rerr := res.Body.Read(buf)
		if n > 0 {
			stall.Reset(downloadStall)
			if int64(n) > room {
				f.Close()
				_ = os.Remove(part)
				ds.reset()
				return permanent{errors.New("файл на сервері більший, ніж має бути")}
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return writeErr(werr)
			}
			ds.h.Write(buf[:n])
			ds.hashed += int64(n)
			room -= int64(n)
			done += int64(n)
			if onBytes != nil {
				onBytes(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return stalledOr(actx, rerr)
		}
	}
	if err := f.Close(); err != nil {
		return writeErr(err)
	}
	if size > 0 && done < size {
		return io.ErrUnexpectedEOF // the next attempt resumes
	}
	return nil
}

// FetchChecksums reads a checksum list ("<sha256>  <name>" per line, like
// SHA256SUMS or a single "<file>.sha256") and returns name → lower-case
// digest. A line without a name maps to "".
func FetchChecksums(ctx context.Context, u string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, checksumTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, dlErr(hostOf(u), err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Не вдалося отримати контрольні суми з %s (%d)", hostOf(u), res.StatusCode)
	}
	return ParseChecksums(io.LimitReader(res.Body, 1<<20)), nil
}

// ParseChecksums parses "<hex>  <name>" / "<hex> *<name>" lines.
func ParseChecksums(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		sum := strings.ToLower(f[0])
		if !isHex64(sum) {
			continue
		}
		name := ""
		if len(f) > 1 {
			name = strings.TrimPrefix(f[len(f)-1], "*")
		}
		out[name] = sum
	}
	return out
}

func stalledOr(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errStalled) {
		return errStalled
	}
	return err
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// rangeFrom is the first byte of "Content-Range: bytes 100-199/200" (-1 when unreadable).
func rangeFrom(v string) int64 {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "bytes"))
	from, _, ok := strings.Cut(v, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(from), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// FileSHA256 is the lower-case hex SHA-256 of a file.
func FileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return 0
	}
	return st.Size()
}

// FormatSize: "8,2 МБ", "465 МБ", "1,5 ГБ" (binary units, like the file managers).
func FormatSize(b int64) string {
	mb := float64(b) / (1 << 20)
	switch {
	case mb >= 1000:
		return strings.Replace(strconv.FormatFloat(mb/1024, 'f', 1, 64), ".", ",", 1) + " ГБ"
	case mb >= 100:
		return strconv.Itoa(int(mb+0.5)) + " МБ"
	}
	return strings.Replace(strconv.FormatFloat(mb, 'f', 1, 64), ".", ",", 1) + " МБ"
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return "сервера"
}

// dlErr is the message of a download that failed for good.
func dlErr(host string, err error) error {
	var ue UserError
	if errors.As(err, &ue) { // already a message for the user (a full disk…)
		return ue
	}
	var p permanent
	if errors.As(err, &p) {
		return fmt.Errorf("Не вдалося завантажити з %s: %v", host, p.error)
	}
	var ne net.Error
	switch {
	case errors.Is(err, errStalled) || errors.As(err, &ne) && ne.Timeout():
		return fmt.Errorf("Завантаження з %s зупинилось — перевір інтернет і спробуй ще раз: продовжимо з того ж місця", host)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("З'єднання з %s обривається — спробуй ще раз: продовжимо з того ж місця", host)
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return fmt.Errorf("Немає з'єднання з %s — перевір інтернет і спробуй ще раз (%s)", host, clip(uerr.Err.Error(), 120))
	}
	return fmt.Errorf("Не вдалося завантажити з %s: %s — спробуй ще раз", host, clip(err.Error(), 160))
}

// writeErr is a friendly message for a failed write (a full disk first of all).
func writeErr(err error) error {
	var en syscall.Errno
	if errors.As(err, &en) && (runtime.GOOS == "windows" && (en == 112 || en == 39) || runtime.GOOS != "windows" && en == syscall.ENOSPC) {
		return UserError{errors.New("Не вистачає місця на диску — звільни місце й спробуй ще раз")}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return UserError{fmt.Errorf("Не вдалося записати файл %s: %v", filepath.Base(pe.Path), pe.Err)}
	}
	return UserError{fmt.Errorf("Не вдалося записати файл: %v", err)}
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// safeName keeps a tag or asset name usable as a file name.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if out := strings.Trim(b.String(), "."); out != "" {
		return out
	}
	return "download"
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
