package codeanim

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
	"ytcartell/internal/sysx"
)

// Spec is an upstream clip spec:
// {grammar, duration, fps=30, width=1920, height=1080, safe?, theme?, alpha?, data?, cues:[{at, kind, text?, sub?, data?, image?, dur?}]}.
type Spec = json.RawMessage

// Options of Render and Still.
type Options struct {
	Alpha     bool                  // transparent background: ProRes 4444 .mov (Render) / RGBA PNG (Still)
	AssetsDir string                // base folder for relative image paths in the spec
	Progress  func(done, total int) // frames written so far (may be nil; any goroutine)
}

// FFmpegPath, when set, is the ffmpeg executable to use; otherwise
// media.Locate finds one (the studio's own copy, PATH…).
var FFmpegPath string

// Encoder settings for the H.264 output (package vars for tests/tuning).
var (
	x264Preset = "medium"
	x264CRF    = "16"
)

// clip is a parsed, validated spec ready for the page.
type clip struct {
	raw      map[string]any
	grammar  string
	duration float64
	fps      float64
	w, h     int
	frames   int
	files    []string // local images referenced (served under __file__/n)
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case float64:
		return x, true
	}
	return 0, false
}

// parseSpec validates spec and rewrites its image paths to served URLs.
func parseSpec(spec []byte, opt Options) (*clip, error) {
	dec := json.NewDecoder(bytes.NewReader(spec))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return nil, fmt.Errorf("Опис анімації пошкоджений (не JSON-об’єкт): %v", err)
	}
	c := &clip{raw: raw, fps: 30, w: 1920, h: 1080}
	c.grammar, _ = raw["grammar"].(string)
	if c.grammar == "" {
		return nil, errors.New("В описі анімації немає grammar")
	}
	if !knownGrammar(c.grammar) {
		return nil, fmt.Errorf("Невідома граматика анімації «%s»", c.grammar)
	}
	d, ok := num(raw["duration"])
	if !ok || !(d > 0) || d > 3600 {
		return nil, errors.New("duration анімації має бути більше 0 (секунди)")
	}
	c.duration = d
	if v, ok := num(raw["fps"]); ok && v != 0 {
		if !(v > 0 && v <= 240) {
			return nil, errors.New("fps анімації має бути від 1 до 240")
		}
		c.fps = v
	}
	for _, k := range []struct {
		key string
		dst *int
	}{{"width", &c.w}, {"height", &c.h}} {
		if v, ok := num(raw[k.key]); ok && v != 0 {
			if v != math.Trunc(v) || v < 16 || v > 8192 {
				return nil, fmt.Errorf("%s анімації має бути цілим від 16 до 8192", k.key)
			}
			*k.dst = int(v)
		}
	}
	// The page reads the same numbers (clip.js: spec.width || 1920 …).
	raw["width"], raw["height"], raw["fps"] = c.w, c.h, c.fps
	c.frames = int(math.RoundToEven(c.duration * c.fps)) // Python's round(), as upstream
	if c.frames < 1 {
		c.frames = 1
	}
	if opt.Alpha {
		raw["alpha"] = true
	}
	// Images (see rewriteImages for the fields).
	seen := map[string]int{}
	fix := func(v any) (any, error) {
		s, ok := v.(string)
		if !ok || s == "" {
			return v, nil
		}
		low := strings.ToLower(s)
		switch {
		case strings.HasPrefix(low, "data:"):
			return s, nil
		case strings.HasPrefix(low, "http:"), strings.HasPrefix(low, "https:"):
			return nil, fmt.Errorf("Зображення з інтернету в анімації не підтримуються — завантаж файл і вкажи шлях: %s", s)
		}
		p := s
		if strings.HasPrefix(low, "file:") {
			p = strings.TrimPrefix(strings.TrimPrefix(s[5:], "//"), "/")
			if filepath.VolumeName(p) == "" {
				p = "/" + p
			}
		}
		p = filepath.FromSlash(p)
		if !filepath.IsAbs(p) {
			base := opt.AssetsDir
			if base == "" {
				return nil, fmt.Errorf("Відносний шлях до зображення «%s», але тека ресурсів не задана", s)
			}
			p = filepath.Join(base, p)
		}
		p = filepath.Clean(p)
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("Зображення для анімації не знайдено: %s", p)
		}
		n, ok := seen[p]
		if !ok {
			n = len(c.files)
			seen[p] = n
			c.files = append(c.files, p)
		}
		return "__file__/" + strconv.Itoa(n) + strings.ToLower(filepath.Ext(p)), nil
	}
	if _, ok := raw["cues"].([]any); !ok && raw["cues"] != nil {
		return nil, errors.New("cues анімації мають бути масивом")
	}
	// Every image field the grammars read, anywhere in data and the cues:
	// "image" (cues[].image, cues[].data.image, data.image, the map's
	// data.sprites[].image, data.icons[].image, inline icon/sprite objects,
	// bubble images) and "alt" when it names a picture (the map's sprite
	// frames). Each becomes a served URL; a missing file is an exact error.
	for _, key := range []string{"cues", "data"} {
		if v, ok := raw[key]; ok {
			if err := rewriteImages(v, key, fix); err != nil {
				return nil, err
			}
		}
	}
	return c, nil
}

// imageKeys are the fields that hold a picture path.
var imageKeys = map[string]bool{"image": true, "alt": true}

// looksLikeImage: "alt" is only a picture when it names one (not alt text).
func looksLikeImage(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if strings.HasPrefix(l, "data:image/") || strings.HasPrefix(l, "file:") {
		return true
	}
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg", ".bmp"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return false
}

// rewriteImages walks v (maps and arrays, in place) and replaces the
// picture paths of its image fields with fix's result; where names the
// field in errors.
func rewriteImages(v any, where string, fix func(any) (any, error)) error {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic __file__ numbering
		for _, k := range keys {
			vv := x[k]
			if s, ok := vv.(string); ok && imageKeys[k] {
				if k == "alt" && !looksLikeImage(s) {
					continue
				}
				nv, err := fix(s)
				if err != nil {
					return fmt.Errorf("%s (%s.%s)", err.Error(), where, k)
				}
				x[k] = nv
				continue
			}
			if err := rewriteImages(vv, where+"."+k, fix); err != nil {
				return err
			}
		}
	case []any:
		for i, vv := range x {
			if err := rewriteImages(vv, fmt.Sprintf("%s[%d]", where, i), fix); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---- a booted page -------------------------------------------------------------------------

type session struct {
	p    *page
	j    *job
	slot int
}

// initScript runs before any page script: the spec, and 2D contexts that
// prefer CPU readback (getImageData every frame).
func initScript(specJSON []byte) string {
	return `(()=>{const g=HTMLCanvasElement.prototype.getContext;HTMLCanvasElement.prototype.getContext=function(t,o){if(t==='2d')o=Object.assign({willReadFrequently:true},o||{});return g.call(this,t,o);};})();
window.CLIP_SPEC = ` + string(specJSON) + `;`
}

// bootPage opens clip.html for the job and waits until the clip is ready.
func bootPage(ctx context.Context, b *browser, j *job, c *clip, specJSON []byte, slot int) (*session, error) {
	p, err := newPage(ctx, b, c.w, c.h)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*session, error) {
		p.close()
		return nil, err
	}
	if err := b.cdp.call(ctx, p.sid, "Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": initScript(specJSON)}, nil); err != nil {
		return fail(err)
	}
	addr, err := serverAddr()
	if err != nil {
		return fail(err)
	}
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := b.cdp.call(ctx, p.sid, "Page.navigate", map[string]any{"url": "http://" + addr + "/" + j.token + "/clip.html?render=1"}, &nav); err != nil {
		return fail(err)
	}
	if nav.ErrorText != "" {
		return fail(fmt.Errorf("Сторінка анімації не відкрилася: %s", nav.ErrorText))
	}
	deadline := time.Now().Add(bootTimeout)
	for {
		var st struct {
			Ready  bool   `json:"ready"`
			Booted bool   `json:"booted"`
			Failed string `json:"failed"`
		}
		err := b.cdp.eval(ctx, p.sid, `(() => { const here = document.readyState === 'complete' && location.pathname.endsWith('/clip.html');
return {ready: here && window.__ready === true, booted: here && typeof window.renderFrame === 'function', failed: (location.pathname.endsWith('/clip.html') && window.__bootFailed) || ''}; })()`, &st)
		if err != nil && ctx.Err() != nil {
			return fail(ctx.Err())
		}
		var cerr *cdpError
		if err != nil && !errors.As(err, &cerr) { // a context swap during navigation is retried
			return fail(err)
		}
		if st.Failed != "" {
			return fail(&PageError{Messages: []string{"Анімація не завантажилась: " + st.Failed}})
		}
		if st.Ready || st.Booted { // booted: prepare + __ready are awaited below
			break
		}
		if time.Now().After(deadline) {
			if perr := p.pageErr(); perr != nil {
				return fail(perr)
			}
			return fail(fmt.Errorf("Анімація не завантажилась за %d с (сторінка кліпу не дійшла до window.__ready)", int(bootTimeout.Seconds())))
		}
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(30 * time.Millisecond):
		}
	}
	// window.prepare (when the clip defines one: async loading of its own
	// pictures, fonts, data) runs to the end before frame 0, then __ready
	// must still hold.
	if err := runPrepare(ctx, p); err != nil {
		return fail(err)
	}
	if perr := p.pageErr(); perr != nil {
		return fail(perr)
	}
	return &session{p: p, j: j, slot: slot}, nil
}

var bootTimeout = 120 * time.Second

// prepareTimeout bounds window.prepare.
var prepareTimeout = 90 * time.Second

// runPrepare awaits window.prepare() when the clip defines it, then checks
// window.__ready; both bounded by prepareTimeout, errors are exact.
func runPrepare(ctx context.Context, p *page) error {
	pctx, cancel := context.WithTimeout(ctx, prepareTimeout)
	defer cancel()
	var st struct {
		Has   bool `json:"has"`
		Ready bool `json:"ready"`
	}
	err := p.b.cdp.eval(pctx, p.sid, `(async () => { const f = window.prepare; if (typeof f !== 'function') return {has: false, ready: window.__ready === true};
await f(); return {has: true, ready: window.__ready === true}; })()`, &st)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if pctx.Err() != nil {
			return fmt.Errorf("Анімація не підготувалась за %d с: window.prepare() не завершився", int(prepareTimeout.Seconds()))
		}
		var je *jsError
		if errors.As(err, &je) {
			return &PageError{Messages: []string{"window.prepare() впав: " + je.msg}}
		}
		return err
	}
	if !st.Ready {
		// prepare finished, __ready may follow a moment later.
		deadline := time.Now().Add(prepareTimeout)
		for !st.Ready {
			if time.Now().After(deadline) {
				what := "window.__ready не став true (картинки чи шрифти кліпу не завантажились)"
				if st.Has {
					what = "після window.prepare() " + what
				}
				return fmt.Errorf("Анімація не підготувалась за %d с: %s", int(prepareTimeout.Seconds()), what)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Millisecond):
			}
			has := st.Has
			if err := p.b.cdp.eval(ctx, p.sid, `({ready: window.__ready === true})`, &st); err != nil {
				return err
			}
			st.Has = has
			if perr := p.pageErr(); perr != nil {
				return perr
			}
		}
	}
	return nil
}

// frame renders time t and returns its RGBA pixels (unpremultiplied,
// row-major, w×h×4) — a buffer from the frame pool.
func (s *session) frame(ctx context.Context, t float64) ([]byte, error) {
	expr := `(async () => { window.renderFrame(` + strconv.FormatFloat(t, 'g', -1, 64) + `);
const cv = window.__canvas, d = cv.getContext('2d').getImageData(0, 0, cv.width, cv.height).data;
const r = await fetch('__frame__/` + strconv.Itoa(s.slot) + `', {method: 'POST', body: d});
if (!r.ok) throw new Error('frame upload: HTTP ' + r.status); return 1; })()`
	if err := s.p.b.cdp.eval(ctx, s.p.sid, expr, nil); err != nil {
		var je *jsError
		if errors.As(err, &je) {
			return nil, &PageError{Messages: []string{je.msg}}
		}
		return nil, err
	}
	if perr := s.p.pageErr(); perr != nil {
		return nil, perr
	}
	buf := s.j.take(s.slot)
	if buf == nil {
		return nil, errors.New("Кадр анімації не надійшов")
	}
	return buf, nil
}

func (s *session) close() { s.p.close() }

// ---- Render -------------------------------------------------------------------------------

func locateFFmpeg() (string, error) {
	if p := strings.TrimSpace(FFmpegPath); p != "" {
		return p, nil
	}
	ff, err := media.Locate(config.Config{})
	if ff.FFmpeg == "" {
		if err == nil {
			err = media.ErrNotFound
		}
		return "", err
	}
	return ff.FFmpeg, nil
}

// Render renders spec into out: H.264 (yuv420p, a keyframe every second,
// BT.709) in .mp4, or ProRes 4444 with alpha in a QuickTime .mov when
// opt.Alpha. Exactly round(duration×fps) frames; frame i shows t = i/fps.
// Page errors (exceptions, console.error) fail the render with the JS
// message; nothing is left at out on failure.
func Render(ctx context.Context, spec []byte, out string, opt Options) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c, err := parseSpec(spec, opt)
	if err != nil {
		return err
	}
	if !opt.Alpha && (c.w%2 != 0 || c.h%2 != 0) {
		return fmt.Errorf("Для MP4 розмір анімації має бути парним (зараз %d×%d)", c.w, c.h)
	}
	ffmpeg, err := locateFFmpeg()
	if err != nil {
		return err
	}
	specJSON, err := json.Marshal(c.raw)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	b, release, err := acquireBrowser(ctx)
	if err != nil {
		return err
	}
	defer release()
	slots := slotChan()
	// One page slot (waiting), then as many free ones as useful.
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	nPages := 1
	want := min(cap(slots), max(1, c.frames/24))
	for nPages < want {
		select {
		case slots <- struct{}{}:
			nPages++
			continue
		default:
		}
		break
	}
	defer func() {
		for i := 0; i < nPages; i++ {
			<-slots
		}
	}()

	j := newJob(c.files, c.w*c.h*4)
	defer dropJob(j)

	// Boot the pages in parallel.
	sess := make([]*session, nPages)
	errs := make([]error, nPages)
	var wg sync.WaitGroup
	for i := range nPages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess[i], errs[i] = bootPage(ctx, b, j, c, specJSON, i)
		}()
	}
	wg.Wait()
	defer func() {
		for _, s := range sess {
			if s != nil {
				s.close()
			}
		}
	}()
	if err := errors.Join(errs...); err != nil {
		for _, e := range errs { // the first real error, not the cancellations it caused
			if e != nil {
				return e
			}
		}
	}

	// ffmpeg reads raw RGBA from stdin.
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".part"
	fps := strconv.FormatFloat(c.fps, 'f', -1, 64)
	args := []string{"-hide_banner", "-nostdin", "-y", "-v", "error",
		"-f", "rawvideo", "-pix_fmt", "rgba", "-s", fmt.Sprintf("%dx%d", c.w, c.h), "-framerate", fps, "-i", "pipe:0"}
	if opt.Alpha {
		args = append(args, "-c:v", "prores_ks", "-profile:v", "4444", "-pix_fmt", "yuva444p10le", "-alpha_bits", "16",
			"-vendor", "apl0", "-f", "mov", tmp)
	} else {
		g := strconv.Itoa(int(math.Max(1, math.Round(c.fps))))
		args = append(args, "-vf", "scale=out_color_matrix=bt709:out_range=tv,format=yuv420p",
			"-c:v", "libx264", "-preset", x264Preset, "-crf", x264CRF, "-pix_fmt", "yuv420p",
			"-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv",
			"-g", g, "-keyint_min", g, "-sc_threshold", "0", "-movflags", "+faststart", "-f", "mp4", tmp)
	}
	cmd := sysx.Command(ctx, ffmpeg, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limitWriter{w: &stderr, n: 16 << 10}
	if err := sysx.Start(cmd); err != nil {
		return fmt.Errorf("FFmpeg не запускається: %v", err)
	}
	ffDone := make(chan error, 1)
	go func() { ffDone <- cmd.Wait() }()
	ok := false
	defer func() {
		if !ok {
			cancel()
			_ = stdin.Close()
			<-ffDone
			_ = os.Remove(tmp)
		}
	}()

	// Frames are dealt round-robin to the pages (frame i → page i % n);
	// each page keeps at most 2 frames ahead, the writer takes them in order.
	type res struct {
		buf []byte
		err error
	}
	chans := make([]chan res, nPages)
	for i := range nPages {
		chans[i] = make(chan res, 2)
		go func(i int) {
			defer close(chans[i])
			for f := i; f < c.frames; f += nPages {
				buf, err := sess[i].frame(ctx, float64(f)/c.fps)
				select {
				case chans[i] <- res{buf, err}:
				case <-ctx.Done():
					if buf != nil {
						putFrameBuf(buf)
					}
					return
				}
				if err != nil {
					return
				}
			}
		}(i)
	}
	for f := 0; f < c.frames; f++ {
		r, more := <-chans[f%nPages]
		if !more {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("Рендер анімації перервався")
		}
		if r.err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return r.err
		}
		_, werr := stdin.Write(r.buf)
		putFrameBuf(r.buf)
		if werr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case e := <-ffDone:
				ffDone <- e
			case <-time.After(2 * time.Second):
			}
			return ffmpegErr(stderr.String(), werr)
		}
		if opt.Progress != nil {
			opt.Progress(f+1, c.frames)
		}
	}
	_ = stdin.Close()
	if err := <-ffDone; err != nil {
		ffDone <- err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ffmpegErr(stderr.String(), err)
	}
	ffDone <- nil
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(out)
		if err := os.Rename(tmp, out); err != nil {
			return err
		}
	}
	ok = true
	return nil
}

func ffmpegErr(stderr string, err error) error {
	s := strings.TrimSpace(stderr)
	if s == "" {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("FFmpeg завершився з кодом %d", ee.ExitCode())
		}
		return fmt.Errorf("FFmpeg: %v", err)
	}
	if len(s) > 800 {
		s = s[len(s)-800:]
	}
	return fmt.Errorf("FFmpeg: %s", s)
}

type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}

// ---- Still --------------------------------------------------------------------------------

// Still renders the frame at second at (snapped to the frame grid like
// Render: t = round(at×fps)/fps, clamped to the clip) into a PNG — RGBA with
// transparency when opt.Alpha.
func Still(ctx context.Context, spec []byte, at float64, outPNG string, opt Options) error {
	img, err := stillImage(ctx, spec, at, opt)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPNG), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return err
	}
	tmp := outPNG + ".part"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, outPNG); err != nil {
		_ = os.Remove(outPNG)
		if err := os.Rename(tmp, outPNG); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	return nil
}

func stillImage(ctx context.Context, spec []byte, at float64, opt Options) (*image.NRGBA, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c, err := parseSpec(spec, opt)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(at) || at < 0 {
		at = 0
	}
	i := int(math.Round(at * c.fps))
	i = min(i, c.frames-1)
	specJSON, err := json.Marshal(c.raw)
	if err != nil {
		return nil, err
	}
	b, release, err := acquireBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	slots := slotChan()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-slots }()
	j := newJob(c.files, c.w*c.h*4)
	defer dropJob(j)
	s, err := bootPage(ctx, b, j, c, specJSON, 0)
	if err != nil {
		return nil, err
	}
	defer s.close()
	buf, err := s.frame(ctx, float64(i)/c.fps)
	if err != nil {
		return nil, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, c.w, c.h))
	copy(img.Pix, buf)
	putFrameBuf(buf)
	if !opt.Alpha {
		for k := 3; k < len(img.Pix); k += 4 {
			img.Pix[k] = 255
		}
	}
	if opt.Progress != nil {
		opt.Progress(1, 1)
	}
	return img, nil
}
