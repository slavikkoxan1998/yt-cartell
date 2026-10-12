package studio

// Cover studio: the YouTube thumbnail text overlay.
//
//	Montage.Cover      the picture (the "cover" stage draws it)
//	Montage.CoverText  the design: template, title, subtitle, badge, font, colours…
//	Montage.CoverFinal the finished JPEG — 1280×720, or 1080×1920 for a Shorts
//	                   (9:16) project — written by ApplyCoverText
//
// PreviewCover renders the same design in memory while the user types; the
// decoded picture is cached, so a preview costs a few milliseconds.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"ytcartell/internal/cover"
	"ytcartell/internal/llm"
)

// ErrNoCover: there is no cover picture to put the text on yet.
var ErrNoCover = errors.New("Спершу згенеруй обкладинку")

// CoverTemplates is the catalog of ready-made cover designs (UI order).
func CoverTemplates() []cover.TemplateDef {
	return append([]cover.TemplateDef(nil), cover.Templates...)
}

// CoverFonts is the catalog of display fonts for the cover text.
func CoverFonts() []cover.FontDef { return append([]cover.FontDef(nil), cover.Fonts...) }

// CoverPositions are the text positions (id + Ukrainian label, UI order).
func CoverPositions() []Choice {
	out := make([]Choice, len(cover.Positions))
	for i, p := range cover.Positions {
		out[i] = Choice{ID: p.ID, Label: p.Label}
	}
	return out
}

// CoverSpec resolves a cover design for a project format: the template's
// defaults fill every empty field; without a template a Shorts (9:16) project
// gets the Shorts layout.
func CoverSpec(ct CoverText, aspect string) cover.Spec {
	t := cover.Text{
		Template: strings.TrimSpace(ct.Template),
		Title:    ct.Title,
		Subtitle: ct.Subtitle,
		Badge:    ct.Badge,
		Font:     ct.Font,
		Color:    ct.Color,
		Stroke:   ct.Stroke,
		Accent:   ct.Accent,
		Position: ct.Position,
		Size:     ct.Size,
	}
	if _, ok := cover.TemplateByID(t.Template); !ok {
		t.Template = cover.DefaultTemplate(aspect == "9:16")
	}
	return cover.ResolveSpec(t)
}

// CoverFinalOutdated reports that the finished cover is missing or older
// than the picture (a new cover was drawn after the text was applied).
// Text edits are not tracked here: the UI applies them itself.
func CoverFinalOutdated(p Project) bool {
	c, f := p.Montage.Cover, p.Montage.CoverFinal
	if c.Path == "" {
		return false
	}
	return f.Path == "" || f.Stale || c.UpdatedAt > f.UpdatedAt
}

// ---- apply --------------------------------------------------------------------------

// coverGens numbers ApplyCoverText calls per project: only the newest one
// may store its result (an older render finishing late is dropped).
var coverGens sync.Map // project folder → *atomic.Int64 (no reference to the project)

func coverGen(st *proj) *atomic.Int64 {
	v, _ := coverGens.LoadOrStore(st.dir, new(atomic.Int64))
	return v.(*atomic.Int64)
}

// ApplyCoverText renders Montage.CoverText onto Montage.Cover and stores the
// result as Montage.CoverFinal (images/cover-final-<n>.jpg). With an empty
// title the final cover is the plain picture (as a YouTube-ready JPEG). The
// asset goes running → done/error like any other (subscribers are
// notified); a newer call supersedes one still rendering. Synchronous (a
// fraction of a second) — call it from a goroutine.
func (e *Engine) ApplyCoverText(id string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	gen := coverGen(st)
	var (
		my     int64
		coverP string
		ct     CoverText
		aspect string
		err    error
		list   bool
	)
	st.mu.Lock()
	m := &st.p.Montage
	switch {
	case st.deleted.Load():
		err = ErrNotFound
	case e.closing.Load():
		err = ErrClosing
	case m.Cover.Path == "":
		err = ErrNoCover
	default:
		my = gen.Add(1)
		coverP, ct, aspect = m.Cover.Path, m.CoverText, st.p.Input.Aspect
		m.CoverFinal.Status, m.CoverFinal.Error, m.CoverFinal.Progress = StatusRunning, "", 0
		list = e.commitLocked(st)
		st.bg.Add(1)
	}
	st.mu.Unlock()
	if err != nil {
		return err
	}
	defer st.bg.Done()
	e.emit(st, list)

	ref := coverFinalRef{}
	rel, err := func() (rel string, err error) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[studio:%s] cover text panic: %v\n%s", id, r, debug.Stack())
				err = fmt.Errorf("внутрішня помилка: %v", r)
			}
		}()
		data, err := renderCoverJPEG(filepath.Join(st.dir, filepath.FromSlash(coverP)), ct, aspect)
		if err != nil {
			return "", err
		}
		if gen.Load() != my {
			return "", nil // superseded: the newer call stores its own result
		}
		return e.writeAsset(st, "images", coverFinalName(st.dir, my), data)
	}()
	if err != nil {
		if gen.Load() == my {
			e.fail(st, ref, err)
		}
		return err
	}
	if rel == "" {
		return nil
	}
	var drop []string
	stored := false
	e.update(st, func(p *Project) {
		a := &p.Montage.CoverFinal
		if gen.Load() != my {
			drop = append(drop, rel)
			return
		}
		if a.Path != "" && a.Path != rel {
			drop = append(drop, a.Path)
		}
		a.Path, a.Status, a.Error, a.Stale, a.Progress = rel, StatusDone, "", false, 100
		a.UpdatedAt = nowMs()
		stored = true
	})
	// Old finals are cheap renders of what the project still has: delete
	// them rather than filling the trash.
	e.removeFiles(st, drop)
	if !stored && st.deleted.Load() {
		return ErrNotFound
	}
	if err := e.writeNow(st); err != nil {
		log.Printf("[studio:%s] %v", id, err)
	}
	return nil
}

// coverFinalRef locates Montage.CoverFinal for the asset helpers (fail).
type coverFinalRef struct{}

func (coverFinalRef) in(p *Project) *Asset { return &p.Montage.CoverFinal }

// coverFinalName is a new file name under images/, unique per call (gen):
// a superseded call deletes its own file, never the winner's.
func coverFinalName(dir string, gen int64) string {
	n := nowMs()*1000 + gen%1000
	for {
		name := fmt.Sprintf("cover-final-%d.jpg", n)
		if _, err := os.Lstat(filepath.Join(dir, "images", name)); err != nil {
			return name
		}
		n += 1000
	}
}

// renderCoverJPEG draws the design onto the picture at the full cover size.
func renderCoverJPEG(abs string, ct CoverText, aspect string) ([]byte, error) {
	size := cover.SizeFor(aspect)
	spec := CoverSpec(ct, aspect)
	if spec.Title == "" {
		if data, ok := plainCoverJPEG(abs, size); ok {
			return data, nil
		}
	}
	base, err := coverBase(abs, size)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Файл обкладинки не знайдено — згенеруй її ще раз")
	}
	if err != nil {
		return nil, err
	}
	canvas := cloneRGBA(base)
	if spec.Title != "" {
		if err := cover.Draw(canvas, spec); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := cover.EncodeJPEG(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// plainCoverJPEG is the picture's own bytes when it already is a JPEG of
// exactly the cover size and within YouTube's limit.
func plainCoverJPEG(abs string, size image.Point) ([]byte, bool) {
	data, err := os.ReadFile(abs)
	if err != nil || len(data) > cover.MaxBytes {
		return nil, false
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != size.X || cfg.Height != size.Y {
		return nil, false
	}
	return data, true
}

// ---- preview ------------------------------------------------------------------------

// PreviewCover renders ct over the project's cover picture in memory, maxW
// pixels wide (≤ 0 → 640), for the editor while the user types. Nothing is
// written. Without a cover picture the first scene frame (or a dark
// background) stands in.
func (e *Engine) PreviewCover(id string, ct CoverText, maxW int) (image.Image, error) {
	st := e.state(id)
	if st == nil {
		return nil, ErrNotFound
	}
	var rel, aspect string
	if !e.read(st, func(p *Project) bool {
		aspect = p.Input.Aspect
		rel = p.Montage.Cover.Path
		if rel == "" {
			for _, s := range p.Scenes {
				if s.Frame.Path != "" {
					rel = s.Frame.Path
					break
				}
			}
		}
		return true
	}) {
		return nil, ErrNotFound
	}
	full := cover.SizeFor(aspect)
	if maxW <= 0 {
		maxW = 640
	}
	w := min(maxW, full.X)
	w = max(w, 64)
	size := image.Pt(w, max(1, (full.Y*w+full.X/2)/full.X))
	var base *image.RGBA
	if rel != "" {
		b, err := coverBase(filepath.Join(st.dir, filepath.FromSlash(rel)), size)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		base = b
	}
	if base == nil {
		base = cover.CoverFit(nil, size)
	}
	canvas := cloneRGBA(base)
	if spec := CoverSpec(ct, aspect); spec.Title != "" {
		if err := cover.Draw(canvas, spec); err != nil {
			return nil, err
		}
	}
	return canvas, nil
}

// ---- picture cache ------------------------------------------------------------------

// coverCache keeps decoded pictures and their cover-fitted copies (keyed by
// file identity and size) so previews only draw the text. Bounded by bytes;
// the newest entry always stays.
var coverCache = &imgCache{srcBudget: 40 << 20, fitBudget: 32 << 20}

type imgKey struct {
	path string
	mod  int64
	size int64
	w, h int // 0,0 = the decoded source
}

type imgEntry struct {
	key   imgKey
	img   image.Image
	bytes int
}

type imgCache struct {
	mu                   sync.Mutex
	srcBudget, fitBudget int
	src, fit             []imgEntry // most recently used last
}

func (c *imgCache) get(k imgKey) image.Image {
	c.mu.Lock()
	defer c.mu.Unlock()
	list := &c.fit
	if k.w == 0 {
		list = &c.src
	}
	for i, en := range *list {
		if en.key == k {
			l := *list
			copy(l[i:], l[i+1:])
			l[len(l)-1] = en
			return en.img
		}
	}
	return nil
}

func (c *imgCache) put(k imgKey, img image.Image) {
	c.mu.Lock()
	defer c.mu.Unlock()
	list, budget := &c.fit, c.fitBudget
	if k.w == 0 {
		list, budget = &c.src, c.srcBudget
	}
	out := (*list)[:0]
	for _, en := range *list {
		// Drop the same entry and other versions of the file (replaced).
		same := en.key.path == k.path && en.key.w == k.w && en.key.h == k.h
		stale := en.key.path == k.path && (en.key.mod != k.mod || en.key.size != k.size)
		if !same && !stale {
			out = append(out, en)
		}
	}
	out = append(out, imgEntry{key: k, img: img, bytes: imgBytes(img)})
	total := 0
	for _, en := range out {
		total += en.bytes
	}
	for len(out) > 1 && total > budget {
		total -= out[0].bytes
		out = out[1:]
	}
	*list = append([]imgEntry(nil), out...)
}

// imgBytes is the memory an image holds.
func imgBytes(img image.Image) int {
	switch m := img.(type) {
	case *image.RGBA:
		return len(m.Pix)
	case *image.NRGBA:
		return len(m.Pix)
	case *image.YCbCr:
		return len(m.Y) + len(m.Cb) + len(m.Cr)
	case *image.Gray:
		return len(m.Pix)
	}
	b := img.Bounds()
	return b.Dx() * b.Dy() * 4
}

// coverBase is the picture at abs cover-fitted to size (cached; the caller
// must not modify it).
func coverBase(abs string, size image.Point) (*image.RGBA, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	key := imgKey{path: abs, mod: fi.ModTime().UnixNano(), size: fi.Size()}
	fk := key
	fk.w, fk.h = size.X, size.Y
	if img, ok := coverCache.get(fk).(*image.RGBA); ok {
		return img, nil
	}
	src := coverCache.get(key)
	if src == nil {
		if src, err = cover.LoadImage(abs); err != nil {
			return nil, err
		}
		coverCache.put(key, src)
	}
	fit := cover.CoverFit(src, size)
	coverCache.put(fk, fit)
	return fit, nil
}

func cloneRGBA(src *image.RGBA) *image.RGBA {
	return &image.RGBA{Pix: append([]uint8(nil), src.Pix...), Stride: src.Stride, Rect: src.Rect}
}

// ---- title suggestion ---------------------------------------------------------------------

// SuggestCoverTitle asks the text model for a short, punchy thumbnail title
// (2–6 words) in the project's audience language (Input.Language; "" = the
// language of the story). The most important word may come wrapped in
// *asterisks* — the cover draws it in the highlight colour. Nothing is saved.
func (e *Engine) SuggestCoverTitle(ctx context.Context, id string) (string, error) {
	p, ok := e.Get(id)
	if !ok {
		return "", ErrNotFound
	}
	system, prompt := coverTitlePrompt(&p)
	var out struct {
		Title string `json:"title"`
	}
	if err := e.text.JSON(ctx, llm.Request{System: system, Prompt: prompt, Temperature: 0.9, Timeout: 90 * time.Second}, &out); err != nil {
		return "", err
	}
	title := cleanCoverTitle(out.Title)
	if title == "" {
		return "", errors.New("Модель не запропонувала заголовок — спробуй ще раз")
	}
	return title, nil
}

func coverTitlePrompt(p *Project) (system, prompt string) {
	lang := languageName(p.Input.Language)
	inLang := "in the SAME language as the video title and summary below"
	if lang != "" {
		inLang = "in " + lang + " (translate the idea if the material is in another language)"
	}
	system = strings.Join(dropEmpty(
		`You write the big text on YouTube thumbnails — the words that make people click.`,
		`Write ONE thumbnail text `+inLang+`:`,
		`- 2 to 6 words, as short as possible; punchy, emotional, creates curiosity; the viewer must understand it in half a second.`,
		`- It complements the video title instead of repeating it word for word; no spoilers of the ending.`,
		`- Wrap the single most important word in *asterisks* (it is drawn in an accent colour).`,
		`- No quotes, hashtags, emoji or period at the end; "!" or "?" are fine.`,
		genreHint(p.Input.Genre),
		toneHint(p.Input.Tone),
		`Return ONLY a JSON object: {"title": "..."}`,
	), "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "Video title: %s\n", strings.TrimSpace(p.Title))
	if l := strings.TrimSpace(p.Logline); l != "" {
		fmt.Fprintf(&b, "Summary: %s\n", l)
	}
	var lines []string
	for _, s := range p.Scenes {
		if n := strings.TrimSpace(s.Narration); n != "" {
			lines = append(lines, n)
		}
		if len(lines) == 4 {
			break
		}
	}
	if len(lines) > 0 {
		fmt.Fprintf(&b, "Opening of the voice-over: %s\n", clipRunes(strings.Join(lines, " "), 900))
	} else if t := strings.TrimSpace(p.Input.Text); t != "" {
		fmt.Fprintf(&b, "Story: %s\n", clipRunes(t, 1500))
	}
	return system, b.String()
}

// cleanCoverTitle strips what a model tends to add (quotes, emoji, hashtags,
// a final period), keeps at most one *highlight* and at most six words.
func cleanCoverTitle(s string) string {
	var kept []string
	for _, w := range strings.Fields(s) {
		if !strings.HasPrefix(w, "#") { // hashtags go as a whole
			kept = append(kept, w)
		}
	}
	s = strings.Join(kept, " ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case strings.ContainsRune("\"«»“”„‘`#", r):
		case r == '\'' || r == '’' || r == 'ʼ':
			b.WriteRune(r) // apostrophe inside Ukrainian words
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.IsSpace(r), unicode.IsMark(r):
			b.WriteRune(r)
		case strings.ContainsRune("*!?,:;.-–—…%№$€₴&+", r):
			b.WriteRune(r)
		}
	}
	words := strings.Fields(b.String())
	// Words are counted without the markup and bare punctuation.
	count := 0
	for i, w := range words {
		if strings.IndexFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			count++
		}
		if count > 6 {
			words = words[:i]
			break
		}
	}
	s = strings.Join(words, " ")
	s = strings.Trim(s, " '’ʼ,;:-–—")
	for strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "...") {
		s = strings.TrimSuffix(s, ".")
	}
	// One highlight at most, and no unpaired marker.
	if n := strings.Count(s, "*"); n != 2 {
		first := strings.Index(s, "*")
		if n > 2 && first >= 0 {
			if second := strings.Index(s[first+1:], "*"); second >= 0 {
				end := first + 1 + second
				s = s[:end+1] + strings.ReplaceAll(s[end+1:], "*", "")
			}
		} else {
			s = strings.ReplaceAll(s, "*", "")
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > cover.MaxTitle {
		s = clipRunes(s, cover.MaxTitle)
	}
	return strings.TrimSpace(s)
}
