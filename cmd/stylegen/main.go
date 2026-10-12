// Command stylegen regenerates the style-gallery previews
// (internal/assets/styles/<id>.jpg) through the user's own G-Labs Studio:
// one 4:3 picture per built-in style with one fixed subject, so the gallery
// compares the styles on the same characters and place and shows what the
// app really renders (the style prompt goes in exactly as the frame prompts
// send it, see studio framePrompt).
//
// Dev tool, not shipped. Run from the module root (GO/):
//
//	stylegen check                # G-Labs health only, nothing generated
//	stylegen prompt -ids noir     # print the prompts, nothing generated
//	stylegen gen [-ids a,b] [-n N] # generate the missing raw pictures (resume-safe)
//	stylegen reroll -ids a,b      # one more picture for these ids (once per id)
//	stylegen process              # raw → internal/assets/styles/<id>.jpg (640×480)
//	stylegen sheet                # labelled contact sheets of the previews
//
// The G-Labs address and key come from the app's settings file, read only
// (never written); the key is never printed. Every submitted generation is
// appended to <work>/ledger.jsonl and the total over all runs is hard-capped
// (-cap), so a re-run can never spend more than planned.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
	"ytcartell/internal/glabs"
	"ytcartell/internal/studio"
)

// subject is the fixed scene of every preview: characters and a place, so
// each card shows how people, an animal and a setting look in the style.
const subject = "A young woman with a red scarf and a curious red fox at the edge of an autumn forest, " +
	"a small wooden cottage with warm glowing windows behind them, golden hour, medium-wide shot, " +
	"the woman and the fox near the centre of the frame."

// noText is the tail of the app's frame prompts (studio production.go), plus
// one line against stray lettering, which would spoil a gallery card.
const noText = "No text, no captions, no labels, no watermark, no speech bubbles. " +
	"No letters, signatures or logos anywhere in the image."

// previewPrompt is the prompt of one style: the scene, then "Art style:" and
// the style prompt exactly as the app sends it, then the no-text line.
func previewPrompt(s studio.StyleDef) string {
	return subject + "\nArt style: " + s.Prompt + "\n" + noText
}

type opts struct {
	work, assets, ids, model string
	conc, cap, max           int
	timeout                  time.Duration
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: stylegen check|prompt|gen|reroll|process|sheet [flags] (-h for flags)")
}

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var o opts
	fs.StringVar(&o.work, "work", filepath.Join(os.TempDir(), "ytcartell-stylegen"), "work folder: raw pictures, ledger, contact sheets")
	fs.StringVar(&o.assets, "assets", filepath.Join("internal", "assets", "styles"), "embedded previews folder")
	fs.StringVar(&o.ids, "ids", "", "comma-separated style ids (default: all)")
	fs.IntVar(&o.conc, "c", 2, "concurrent generations")
	fs.IntVar(&o.cap, "cap", 90, "hard cap of generations recorded in the ledger, over all runs")
	fs.IntVar(&o.max, "n", 0, "at most this many generations in this run (0 = up to the cap)")
	fs.DurationVar(&o.timeout, "timeout", 12*time.Minute, "per-picture timeout")
	fs.StringVar(&o.model, "model", "NARWHAL", "UI image model (NARWHAL = Nano Banana 2, the app default)")
	_ = fs.Parse(os.Args[2:])
	o.conc = max(1, o.conc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch cmd {
	case "check":
		var c *glabs.Client
		if c, err = client(); err == nil {
			err = health(ctx, c)
		}
	case "prompt":
		err = runPrompt(o)
	case "gen":
		err = runGen(ctx, o, false)
	case "reroll":
		err = runGen(ctx, o, true)
	case "process":
		err = runProcess(o)
	case "sheet":
		err = runSheet(o)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

// selectStyles returns the built-in styles named in ids (all when empty).
func selectStyles(ids string) ([]studio.StyleDef, error) {
	if strings.TrimSpace(ids) == "" {
		return studio.Styles, nil
	}
	var out []studio.StyleDef
	for _, id := range strings.Split(ids, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		s, ok := studio.StyleByID(id)
		if !ok {
			return nil, fmt.Errorf("unknown style id %q", id)
		}
		out = append(out, s)
	}
	return out, nil
}

func runPrompt(o opts) error {
	styles, err := selectStyles(o.ids)
	if err != nil {
		return err
	}
	for _, s := range styles {
		fmt.Printf("== %s\n%s\n\n", s.ID, previewPrompt(s))
	}
	return nil
}

// ---- G-Labs -----------------------------------------------------------------------------------

// client reads the app's settings (read only) and returns a G-Labs client.
func client() (*glabs.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("read settings %s: %w", config.Path(), err)
	}
	if strings.TrimSpace(cfg.Glabs.APIKey) == "" {
		return nil, fmt.Errorf("no G-Labs API key in %s", config.Path())
	}
	log.Printf("G-Labs %s (API key from %s: set)", cfg.Glabs.BaseURL, config.Path())
	return glabs.New(func() config.Config { return cfg }), nil
}

// health checks that G-Labs answers and accepts the key.
func health(ctx context.Context, c *glabs.Client) error {
	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ok, msg := c.Health(hctx)
	log.Printf("health: %s", msg)
	if !ok {
		return errors.New("G-Labs is not reachable or not ready: nothing generated")
	}
	return nil
}

// stopsRun: failures after which more submissions only burn time or credits
// (bad key, account session, exhausted credits).
func stopsRun(err error) bool {
	var ge *glabs.Error
	if errors.As(err, &ge) {
		switch ge.Code {
		case 401, 403, 429:
			return true
		}
	}
	return false
}

// ---- ledger -------------------------------------------------------------------------------------

// entry is one submitted generation.
type entry struct {
	Time   time.Time `json:"time"`
	ID     string    `json:"id"`
	Reroll bool      `json:"reroll,omitempty"`
	OK     bool      `json:"ok"`
	File   string    `json:"file,omitempty"`
	MIME   string    `json:"mime,omitempty"`
	Size   string    `json:"size,omitempty"`
	Bytes  int       `json:"bytes,omitempty"`
	Secs   float64   `json:"secs"`
	Err    string    `json:"err,omitempty"`
}

// ledger is the append-only record of every submission (all runs).
type ledger struct {
	path     string
	mu       sync.Mutex
	all      []entry
	reserved int
}

func openLedger(path string) (*ledger, error) {
	l := &ledger{path: path}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("ledger %s: %w", path, err)
		}
		l.all = append(l.all, e)
	}
	return l, sc.Err()
}

// used is the number of generations submitted so far (and in flight).
func (l *ledger) used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.all) + l.reserved
}

// pictures counts the successful generations of a style id.
func (l *ledger) pictures(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.all {
		if e.ID == id && e.OK {
			n++
		}
	}
	return n
}

// reserve takes one generation slot unless the cap is reached.
func (l *ledger) reserve(limit int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.all)+l.reserved >= limit {
		return false
	}
	l.reserved++
	return true
}

// add records a submission made on a reserved slot.
func (l *ledger) add(e entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reserved--
	l.all = append(l.all, e)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ---- generation -----------------------------------------------------------------------------------

var rawExts = []string{".png", ".jpg", ".jpeg", ".webp"}

// rawFile is the current raw picture of a style id ("" when there is none).
func rawFile(dir, id string) string {
	for _, ext := range rawExts {
		p := filepath.Join(dir, id+ext)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
			return p
		}
	}
	return ""
}

func extFor(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	}
	return ".png"
}

// runGen generates the missing raw pictures, or (reroll) one more picture
// for each named id; the old one moves to <work>/rejected.
func runGen(ctx context.Context, o opts, reroll bool) error {
	if reroll && strings.TrimSpace(o.ids) == "" {
		return errors.New("reroll needs -ids")
	}
	styles, err := selectStyles(o.ids)
	if err != nil {
		return err
	}
	rawDir := filepath.Join(o.work, "raw")
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}
	led, err := openLedger(filepath.Join(o.work, "ledger.jsonl"))
	if err != nil {
		return err
	}
	var jobs []studio.StyleDef
	for _, s := range styles {
		switch {
		case reroll && led.pictures(s.ID) >= 2:
			log.Printf("%s: already re-rolled once, skipped", s.ID)
		case reroll:
			jobs = append(jobs, s)
		case rawFile(rawDir, s.ID) == "": // resume-safe: done ids are skipped
			jobs = append(jobs, s)
		}
	}
	left := o.cap - led.used()
	log.Printf("ledger: %d generations so far, cap %d; %d to generate now", led.used(), o.cap, len(jobs))
	if len(jobs) == 0 {
		return nil
	}
	if left <= 0 {
		return fmt.Errorf("generation cap %d reached: nothing generated", o.cap)
	}
	if len(jobs) > left {
		log.Printf("only %d generations left under the cap: generating the first %d", left, left)
		jobs = jobs[:left]
	}
	if o.max > 0 && len(jobs) > o.max {
		jobs = jobs[:o.max]
	}

	c, err := client()
	if err != nil {
		return err
	}
	if err := health(ctx, c); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu         sync.Mutex
		done, fail int
		streak     int // consecutive failures
	)
	queue := make(chan studio.StyleDef)
	var wg sync.WaitGroup
	for range min(o.conc, len(jobs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range queue {
				if ctx.Err() != nil {
					continue
				}
				if !led.reserve(o.cap) {
					log.Printf("%s: generation cap reached, skipped", s.ID)
					continue
				}
				e, err := generate(ctx, c, o, rawDir, s, reroll)
				if lerr := led.add(e); lerr != nil {
					log.Printf("ledger: %v", lerr)
				}
				mu.Lock()
				done++
				if err != nil {
					fail++
					streak++
					log.Printf("[%d/%d] %s: FAILED after %.0fs: %v", done, len(jobs), s.ID, e.Secs, err)
					if stopsRun(err) || streak >= 3 {
						log.Printf("stopping the run (%v)", err)
						cancel()
					}
				} else {
					streak = 0
					log.Printf("[%d/%d] %s: ok %.0fs %s %s %d KB", done, len(jobs), s.ID, e.Secs, e.Size, e.MIME, e.Bytes>>10)
				}
				mu.Unlock()
			}
		}()
	}
	for _, s := range jobs {
		select {
		case queue <- s:
		case <-ctx.Done():
		}
	}
	close(queue)
	wg.Wait()
	log.Printf("run finished: %d submitted, %d failed; ledger total %d/%d", done, fail, led.used(), o.cap)
	if fail > 0 || ctx.Err() != nil {
		return fmt.Errorf("%d of %d generations failed or were not run", len(jobs)-(done-fail), len(jobs))
	}
	return nil
}

// generate renders one style and saves the raw picture as <raw>/<id>.<ext>;
// the returned entry describes the submission for the ledger.
func generate(ctx context.Context, c *glabs.Client, o opts, rawDir string, s studio.StyleDef, reroll bool) (entry, error) {
	e := entry{Time: time.Now().UTC(), ID: s.ID, Reroll: reroll}
	start := time.Now()
	ictx, cancel := context.WithTimeout(ctx, o.timeout)
	data, mime, err := c.Image(ictx, gen.ImageRequest{Prompt: previewPrompt(s), UIModel: o.model, Aspect: "4:3"})
	cancel()
	e.Secs = time.Since(start).Round(100 * time.Millisecond).Seconds()
	if err != nil {
		e.Err = err.Error()
		return e, err
	}
	e.MIME, e.Bytes = mime, len(data)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		bad := filepath.Join(o.work, "bad")
		_ = os.MkdirAll(bad, 0o755)
		_ = os.WriteFile(filepath.Join(bad, fmt.Sprintf("%s-%d%s", s.ID, time.Now().Unix(), extFor(mime))), data, 0o644)
		e.Err = "undecodable picture: " + err.Error()
		return e, errors.New(e.Err)
	}
	e.Size = fmt.Sprintf("%dx%d", cfg.Width, cfg.Height)
	// The new picture replaces the old one only once it is safely on disk.
	if old := rawFile(rawDir, s.ID); old != "" {
		rej := filepath.Join(o.work, "rejected")
		if err := os.MkdirAll(rej, 0o755); err != nil {
			e.Err = err.Error()
			return e, err
		}
		dst := filepath.Join(rej, fmt.Sprintf("%s-%d%s", s.ID, time.Now().Unix(), filepath.Ext(old)))
		if err := os.Rename(old, dst); err != nil {
			e.Err = err.Error()
			return e, err
		}
	}
	path := filepath.Join(rawDir, s.ID+extFor(mime))
	if err := writeAtomic(path, data); err != nil {
		e.Err = err.Error()
		return e, err
	}
	e.OK, e.File = true, filepath.Base(path)
	return e, nil
}

// writeAtomic writes via a temp file + rename, so a crash never leaves a
// half-written picture that a resumed run would take as done.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
