package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// stallTimeout cancels a download that received nothing for this long.
const stallTimeout = 60 * time.Second

// maxSums caps SHA256SUMS.txt.
const maxSums = 1 << 20

// Download fetches the build of rel for this computer into the download
// folder and returns its path once its SHA-256 matches the release's
// SHA256SUMS.txt. A missing checksum file or line refuses the download (no
// unverified install, ever). An interrupted download leaves a ".part" file
// that the next call resumes with an HTTP Range request; a finished and
// verified file is reused.
func (c *Client) Download(ctx context.Context, rel Release, progress func(done, total int64)) (string, error) {
	a, ok := rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", &Error{Kind: KindNoAsset, Msg: "У версії " + rel.Version + " немає збірки для цього комп'ютера — відкрий сторінку релізу."}
	}
	return c.download(ctx, rel, a, progress)
}

func (c *Client) download(ctx context.Context, rel Release, a Asset, progress func(done, total int64)) (string, error) {
	if progress == nil {
		progress = func(int64, int64) {}
	}
	want, err := c.checksum(ctx, rel, a.Name)
	if err != nil {
		return "", err
	}
	dir := c.dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &Error{Kind: KindPermission, Msg: "Не вдалося створити тимчасову папку для оновлення: " + err.Error(), Err: err}
	}
	name := filepath.Base(filepath.FromSlash(a.Name))
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return "", &Error{Kind: KindServer, Msg: "Неправильна назва файлу оновлення.", Err: fmt.Errorf("asset %q", a.Name)}
	}
	final := filepath.Join(dir, name)
	if sum, size, err := fileSHA256(final); err == nil && sum == want {
		progress(size, size)
		return final, nil
	}
	// The part file carries the expected checksum: a re-published asset
	// never resumes into an older half.
	part := final + "." + want[:12] + ".part"
	removeOtherParts(final, part)

	for attempt := 0; ; attempt++ {
		path, retry, err := c.fetch(ctx, a, final, part, want, progress)
		if err == nil || !retry || attempt > 0 {
			return path, err
		}
	}
}

// fetch downloads (or resumes) a into part, verifies it and moves it to
// final. retry asks for one fresh attempt (the server refused the range).
func (c *Client) fetch(ctx context.Context, a Asset, final, part, want string, progress func(done, total int64)) (path string, retry bool, err error) {
	h := sha256.New()
	var have int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 && (a.Size <= 0 || st.Size() <= a.Size) {
		if n, err := hashFile(part, h); err == nil {
			have = n
		} else {
			h.Reset()
		}
	}
	if have == 0 {
		_ = os.Remove(part)
	}
	total := a.Size
	if have > 0 && have == total {
		// Complete already (the last run stopped after the copy).
		return finish(h, part, final, want, have, total, progress)
	}

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", false, &Error{Kind: KindServer, Msg: "Неправильне посилання на файл оновлення.", Err: err}
	}
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Accept", "application/octet-stream")
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	// A stalled connection is cancelled; every chunk re-arms the timer.
	stall := time.AfterFunc(stallTimeout, cancel)
	defer stall.Stop()
	resp, err := c.client().Do(req)
	if err != nil {
		if parent.Err() != nil {
			return "", false, context.Canceled
		}
		return "", false, netError(parent, err)
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if have == 0 || !rangeStartsAt(resp.Header.Get("Content-Range"), have) {
			_ = os.Remove(part)
			return "", true, &Error{Kind: KindServer, Msg: "GitHub повернув не ту частину файлу — спробуй ще раз."}
		}
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0
		h.Reset()
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return "", true, &Error{Kind: KindServer, Msg: "Не вдалося докачати файл — спробуй ще раз."}
	case http.StatusNotFound:
		return "", false, &Error{Kind: KindServer, Msg: "Файл оновлення не знайдено на GitHub — можливо, реліз саме оновлюють. Спробуй за кілька хвилин."}
	default:
		return "", false, statusError(resp, "завантажити оновлення")
	}
	if total <= 0 && resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", false, &Error{Kind: KindPermission, Msg: "Не вдалося записати файл оновлення: " + err.Error(), Err: err}
	}
	progress(have, total)
	buf := make([]byte, 64<<10)
	var werr error
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(stallTimeout)
			if _, werr = f.Write(buf[:n]); werr != nil {
				break
			}
			h.Write(buf[:n])
			have += int64(n)
			progress(have, total)
			if total > 0 && have > total {
				werr = errors.New("the file is larger than announced")
				break
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			werr = rerr
			break
		}
	}
	if err := f.Sync(); err != nil && werr == nil {
		werr = err
	}
	if err := f.Close(); err != nil && werr == nil {
		werr = err
	}
	if werr != nil {
		// The part stays: the next call resumes it.
		switch {
		case parent.Err() != nil:
			return "", false, context.Canceled // the user's «Скасувати»
		case ctx.Err() != nil:
			return "", false, &Error{Kind: KindIncomplete, Msg: "GitHub перестав надсилати файл — натисни ще раз, щоб докачати.", Err: werr}
		}
		var pe *os.PathError
		if errors.As(werr, &pe) {
			return "", false, &Error{Kind: KindPermission, Msg: "Не вдалося записати файл оновлення: " + pe.Err.Error(), Err: werr}
		}
		return "", false, &Error{Kind: KindIncomplete, Msg: "Завантаження обірвалося — натисни ще раз, щоб докачати.", Err: werr}
	}
	if total > 0 && have < total {
		return "", false, &Error{Kind: KindIncomplete, Msg: "Завантаження обірвалося — натисни ще раз, щоб докачати.", Err: fmt.Errorf("got %d of %d bytes", have, total)}
	}
	return finish(h, part, final, want, have, total, progress)
}

// finish checks the hash and moves the part into place.
func finish(h hash.Hash, part, final, want string, have, total int64, progress func(done, total int64)) (string, bool, error) {
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		_ = os.Remove(part)
		return "", false, &Error{Kind: KindChecksum, Msg: "Завантажений файл не пройшов перевірку SHA-256 (пошкоджений або підмінений) — його видалено. Спробуй ще раз.",
			Err: fmt.Errorf("sha256 %s, want %s", got, want)}
	}
	_ = os.Remove(final)
	if err := os.Rename(part, final); err != nil {
		return "", false, &Error{Kind: KindPermission, Msg: "Не вдалося зберегти файл оновлення: " + err.Error(), Err: err}
	}
	if total <= 0 {
		total = have
	}
	progress(total, total)
	return final, false, nil
}

// checksum downloads SHA256SUMS.txt and returns the line for name.
func (c *Client) checksum(ctx context.Context, rel Release, name string) (string, error) {
	sa, ok := rel.Checksums()
	if !ok {
		return "", &Error{Kind: KindChecksum, Msg: "У релізі немає файлу контрольних сум " + SumsName + " — без перевірки студія не встановлює оновлення. Завантаж нову версію вручну зі сторінки релізу."}
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sa.URL, nil)
	if err != nil {
		return "", &Error{Kind: KindServer, Msg: "Неправильне посилання на " + SumsName + ".", Err: err}
	}
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.client().Do(req)
	if err != nil {
		return "", netError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", statusError(resp, "завантажити контрольні суми")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSums))
	if err != nil {
		return "", netError(ctx, err)
	}
	sums := ParseSums(b)
	want, ok := sums[name]
	if !ok {
		for n, s := range sums {
			if strings.EqualFold(n, name) {
				want, ok = s, true
				break
			}
		}
	}
	if !ok {
		return "", &Error{Kind: KindChecksum, Msg: "У " + SumsName + " немає рядка для " + name + " — без перевірки студія не встановлює оновлення."}
	}
	return want, nil
}

// ParseSums reads sha256sum output: "<64 hex>  <file>" or "<64 hex> *<file>"
// per line (comments and junk are skipped). Keys are base names; hashes lowercase.
func ParseSums(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, rest, ok := strings.Cut(line, " ")
		if !ok || len(sum) != 64 {
			continue
		}
		if _, err := hex.DecodeString(sum); err != nil {
			continue
		}
		name := strings.TrimLeft(strings.TrimSpace(rest), "*")
		name = strings.TrimPrefix(name, "./")
		if i := strings.LastIndexAny(name, `/\`); i >= 0 {
			name = name[i+1:]
		}
		if name != "" {
			out[name] = strings.ToLower(sum)
		}
	}
	return out
}

// rangeStartsAt checks "bytes <from>-<to>/<size>".
func rangeStartsAt(cr string, from int64) bool {
	cr = strings.TrimSpace(cr)
	rest, ok := strings.CutPrefix(cr, "bytes ")
	if !ok {
		return false
	}
	start, _, ok := strings.Cut(rest, "-")
	n, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	return ok && err == nil && n == from
}

// hashFile feeds path into h and returns its size.
func hashFile(path string, h hash.Hash) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(h, f)
}

// fileSHA256 is the hex SHA-256 and size of a file.
func fileSHA256(path string) (string, int64, error) {
	h := sha256.New()
	n, err := hashFile(path, h)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// removeOtherParts deletes stale halves of final (another checksum).
func removeOtherParts(final, keep string) {
	matches, _ := filepath.Glob(globEscape(final) + ".*.part")
	for _, m := range matches {
		if m != keep {
			_ = os.Remove(m)
		}
	}
}

// globEscape quotes the glob meta characters of a literal path.
func globEscape(s string) string {
	if runtime.GOOS == "windows" {
		// "\" is the separator on Windows, not an escape: bracket the metas.
		r := strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]")
		return r.Replace(s)
	}
	r := strings.NewReplacer(`\`, `\\`, "[", `\[`, "*", `\*`, "?", `\?`)
	return r.Replace(s)
}

// Throttle wraps a progress callback so it runs at most every d (and always
// for the final value). Safe from one goroutine at a time.
func Throttle(d time.Duration, fn func(done, total int64)) func(done, total int64) {
	var mu sync.Mutex
	var last time.Time
	return func(done, total int64) {
		mu.Lock()
		now := time.Now()
		fire := now.Sub(last) >= d || (total > 0 && done >= total)
		if fire {
			last = now
		}
		mu.Unlock()
		if fire {
			fn(done, total)
		}
	}
}
