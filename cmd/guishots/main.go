// Command guishots renders every screen of the new UI (internal/appui) —
// the whole window with the rail, not just page bodies — headlessly
// (gioui.org/gpu/headless) to PNG files, light and dark, at 1920×1080,
// 1440×900 and 390×844:
//
//	go run ./cmd/guishots                       # → screenshots/new
//	go run ./cmd/guishots -out shots -only home,queue -sizes 1440x900 -themes light
//
// It works on throw-away data folders (core.DemoEngine): the demo projects
// of the web studio (../data/studio + ../uploads) are imported read-only when
// present, settings live in memory, keys are fake. Nothing reaches a real
// service: the text and G-Labs endpoints point at local mock servers, which
// also produce the "running" and "error" job states (-jobs=false skips them).
//
// Shots: home, projects, queue (page), drawer (queue drawer over Home),
// update (the «Оновлення» sheet: a new version, and update-downloading),
// settings-about (Про студію with a new version known), settings, project-<n>-step<k> for every demo project, new-project (the
// sheet), menu (a card's ⋯ menu), confirm (delete confirmation), tour-* (the
// first-run tour), the empty install (*-empty) and the job states
// (*-running, *-error).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"ytcartell/internal/appui"
	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/update"
)

// demoRelease is the release the update shots show (no network: the sheet
// state is pinned with Shell.ShowUpdate).
func demoRelease() update.Release {
	asset := func(name string, mb int64) update.Asset {
		return update.Asset{Name: name, URL: "https://example.invalid/" + name, Size: mb << 20}
	}
	return update.Release{
		Tag: "v2.1.0", Version: "2.1.0", Title: "YT Cartell Studio v2.1.0 — швидший монтаж",
		URL:       "https://github.com/gitkalenyuk/yt-cartell-studio/releases/tag/v2.1.0",
		Published: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
		Notes: `## Що нового

- **Оновлення в один клік**: студія сама підкаже, коли вийде нова версія
- Монтаж рендериться до 30 % швидше на слабких ноутбуках
- Своя озвучка: точніше ріже фрази на сцени
- Версія для macOS (Apple Silicon та Intel)

## Виправлення

- Черга більше не губить прогрес після перезапуску
- Обкладинки з довгими назвами не вилазять за безпечну зону

Дякуємо всім, хто писав у спільноту!`,
		Assets: []update.Asset{
			asset("YT-Cartell-Studio-2.1.0-windows-x64.exe", 41),
			asset("YT-Cartell-Studio-2.1.0-macos-arm64.zip", 39),
			asset("YT-Cartell-Studio-2.1.0-macos-x64.zip", 40),
			asset(update.SumsName, 0),
		},
	}
}

type size struct{ w, h int }

type shot struct {
	name  string
	e     *studio.Engine
	setup func(s *appui.Shell)
	// quick renders a fixed number of frames (endless animations: spinners).
	quick bool
}

type options struct {
	out    string
	sizes  []size
	themes []string
	only   []string
}

func main() {
	log.SetFlags(0)
	out := flag.String("out", "", "output folder (default: <module>/screenshots/new)")
	web := flag.String("web", "", "web studio root to import the demo projects from (default: auto-detect)")
	sizes := flag.String("sizes", "1920x1080,1440x900,390x844", "comma-separated WxH sizes")
	themes := flag.String("themes", "light,dark", "comma-separated themes: light, dark")
	only := flag.String("only", "", "comma-separated shot names or name prefixes (default: all)")
	jobs := flag.Bool("jobs", true, "also render the running / error job states (local mock services)")
	flag.Parse()

	mod := moduleRoot()
	root := *web
	if root == "" {
		root = webRoot(mod)
	}
	o := options{out: *out}
	if o.out == "" {
		o.out = filepath.Join(mod, "screenshots", "new")
	}
	o.out, _ = filepath.Abs(o.out)
	if protected(root, o.out) {
		log.Fatalf("guishots: refusing to write into the web studio's data (%s)", o.out)
	}
	for _, s := range strings.Split(*sizes, ",") {
		w, h, ok := strings.Cut(strings.TrimSpace(s), "x")
		wi, err1 := strconv.Atoi(w)
		hi, err2 := strconv.Atoi(h)
		if !ok || err1 != nil || err2 != nil {
			log.Fatalf("bad size %q", s)
		}
		o.sizes = append(o.sizes, size{wi, hi})
	}
	for _, t := range strings.Split(*themes, ",") {
		if t = strings.TrimSpace(t); t != "" {
			o.themes = append(o.themes, t)
		}
	}
	for _, x := range strings.Split(*only, ",") {
		if x = strings.TrimSpace(x); x != "" {
			o.only = append(o.only, x)
		}
	}
	start := time.Now()
	n, err := run(root, o, *jobs)
	fmt.Printf("%d images in %s → %s\n", n, time.Since(start).Round(time.Second), o.out)
	if err != nil {
		log.Fatal(err)
	}
}

func run(root string, o options, jobs bool) (int, error) {
	tmp, err := os.MkdirTemp("", "guishots-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	mocks := startMocks()
	defer mocks.close()

	demoDir := filepath.Join(tmp, "demo")
	demo, err := core.DemoEngine(demoDir, root)
	if err != nil {
		return 0, fmt.Errorf("demo engine: %w", err)
	}
	// The imported projects all finished today; spread their last jobs over
	// the past days (in this temp copy only) so the history shows its day
	// groups, then reopen the folder.
	if err := demo.Shutdown(2 * time.Second); err != nil {
		log.Printf("demo flush: %v", err)
	}
	if err := ageDemo(demoDir); err != nil {
		log.Printf("age demo history (skipped): %v", err)
	}
	if demo, err = core.DemoEngine(demoDir, ""); err != nil {
		return 0, fmt.Errorf("demo engine: %w", err)
	}
	defer demo.Shutdown(2 * time.Second)
	empty, err := core.DemoEngine(filepath.Join(tmp, "empty"), "")
	if err != nil {
		return 0, fmt.Errorf("empty engine: %w", err)
	}
	defer empty.Shutdown(2 * time.Second)
	for _, e := range []*studio.Engine{demo, empty} {
		if err := e.Config().Update(mocks.apply); err != nil {
			return 0, err
		}
	}
	list := demo.List()
	log.Printf("demo projects: %d (web root %q)", len(list), root)

	nav := func(r core.Route) func(s *appui.Shell) { return func(s *appui.Shell) { s.Navigate(r) } }
	shots := []shot{
		{name: "home", e: demo},
		{name: "projects", e: demo, setup: nav(core.Route{Page: core.PageProjects})},
		{name: "queue", e: demo, setup: nav(core.Route{Page: core.PageQueue})},
		{name: "drawer", e: demo, setup: func(s *appui.Shell) { s.OpenQueue() }},
		{name: "settings", e: demo, setup: nav(core.Route{Page: core.PageSettings})},
		{name: "new-project", e: demo, setup: func(s *appui.Shell) { s.OpenSheet(core.Sheet{Kind: core.SheetNewProject}) }},
		{name: "tour-story", e: demo, setup: func(s *appui.Shell) { s.ShowTour(1) }},
		{name: "tour-queue", e: demo, setup: func(s *appui.Shell) { s.ShowTour(4) }},
		{name: "chooser-lang", e: demo, setup: func(s *appui.Shell) { s.Home().OpenChooser(s.Ctx(), "lang") }},
		{name: "chooser-dur", e: demo, setup: func(s *appui.Shell) { s.Home().OpenChooser(s.Ctx(), "dur") }},
		{name: "chooser-sound", e: demo, setup: func(s *appui.Shell) { s.Home().OpenChooser(s.Ctx(), "sound") }},
		{name: "chooser-content", e: demo, setup: func(s *appui.Shell) { s.Home().OpenChooser(s.Ctx(), "content") }},
		{name: "update", e: demo, setup: func(s *appui.Shell) { s.ShowUpdate(demoRelease(), -1) }},
		{name: "update-downloading", e: demo, quick: true, setup: func(s *appui.Shell) { s.ShowUpdate(demoRelease(), 0.42) }},
		{name: "settings-about", e: demo, setup: func(s *appui.Shell) {
			s.ShowUpdate(demoRelease(), -1)
			s.Navigate(core.Route{Page: core.PageSettings, Section: "about"})
		}},
		{name: "home-empty", e: empty},
		{name: "projects-empty", e: empty, setup: nav(core.Route{Page: core.PageProjects})},
		{name: "queue-empty", e: empty, setup: nav(core.Route{Page: core.PageQueue})},
	}
	// The story card with three of the demo's own pictures as «Матеріали»,
	// and with the editor of the first one open.
	if mats := demoMaterials(demo); len(mats) > 0 {
		add := func(s *appui.Shell) {
			for _, m := range mats {
				s.Home().AddMaterial(m)
			}
		}
		shots = append(shots,
			shot{name: "home-materials", e: demo, setup: add},
			shot{name: "home-material-edit", e: demo, setup: func(s *appui.Shell) { add(s); s.Home().EditMaterial(0) }},
		)
	}
	if len(list) > 0 {
		id, title := list[0].ID, list[0].Title
		shots = append(shots,
			shot{name: "menu", e: demo, setup: func(s *appui.Shell) { s.Home().OpenMenu(id) }},
			shot{name: "confirm", e: demo, setup: func(s *appui.Shell) {
				s.Confirm(core.Confirm{Title: "Видалити проєкт?", Icon: gui.IconTrash2, OK: "Видалити", Danger: true,
					Text: "«" + title + "» і всі його файли — кадри, відео, озвучку й монтаж — буде видалено з папки даних. Скасувати це не можна."})
			}},
		)
	}
	for i, p := range list {
		for step := 1; step <= 5; step++ {
			shots = append(shots, shot{name: fmt.Sprintf("project-%d-step%d", i+1, step), e: demo,
				setup: nav(core.Route{Page: core.PageProject, ProjectID: p.ID, Step: step})})
		}
	}
	n := 0
	var errs []error
	render := func(sh shot) {
		if !o.want(sh.name) {
			return
		}
		for _, theme := range o.themes {
			for _, sz := range o.sizes {
				if err := renderShot(sh, theme == "dark", sz, o.out); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", sh.name, err))
					continue
				}
				n++
			}
		}
	}
	for _, sh := range shots {
		render(sh)
	}
	if jobs && o.wantAny("home-running", "queue-running", "drawer-running", "home-error", "queue-error", "drawer-error") {
		errs = append(errs, jobShots(demo, mocks, render)...)
	}
	return n, errors.Join(errs...)
}

func (o options) want(name string) bool {
	if len(o.only) == 0 {
		return true
	}
	for _, x := range o.only {
		if name == x || strings.HasPrefix(name, x) {
			return true
		}
	}
	return false
}

func (o options) wantAny(names ...string) bool {
	for _, n := range names {
		if o.want(n) {
			return true
		}
	}
	return false
}

func renderShot(sh shot, dark bool, sz size, out string) error {
	s := appui.Headless(sh.e)
	defer s.Close()
	s.SetDark(dark)
	if sh.setup != nil {
		sh.setup(s)
	}
	var opts []gui.SnapOption
	if sh.quick {
		opts = []gui.SnapOption{gui.Frames(40)}
	}
	img, err := s.Snapshot(sz.w, sz.h, opts...)
	if err != nil {
		return err
	}
	theme := "light"
	if dark {
		theme = "dark"
	}
	path := filepath.Join(out, fmt.Sprintf("%s-%s-%dx%d.png", sh.name, theme, sz.w, sz.h))
	if err := gui.SavePNG(img, path); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// jobShots re-shoots the clips of a demo project against the G-Labs mock:
// the first clip finishes and the rest never do (a running job with real
// progress), then the mock refuses everything (a job that ends with failed
// clips) and a new project's plan is refused by the text mock (a failed job).
func jobShots(e *studio.Engine, mocks *mockSet, render func(shot)) []error {
	id := ""
	for _, s := range e.List() {
		p, ok := e.Get(s.ID)
		if !ok || len(p.Scenes) < 2 {
			continue
		}
		framed := true
		for _, sc := range p.Scenes {
			framed = framed && sc.Frame.Status == studio.StatusDone
		}
		if framed {
			id = s.ID
			break
		}
	}
	if id == "" {
		return []error{errors.New("job shots: no demo project with frames")}
	}
	// Shoot the clips from scratch (this temp copy only), so the running job
	// shows every state at once: a finished clip, clips rendering, clips
	// waiting their turn.
	if err := freshClips(e, id); err != nil {
		return []error{fmt.Errorf("reset clips: %w", err)}
	}
	mocks.mode.Store(modeHang)
	if err := e.Run(id, studio.StageVideos, false, ""); err != nil {
		return []error{fmt.Errorf("run videos: %w", err)}
	}
	moving := func(p studio.Project) bool {
		return p.Job != nil && p.Job.Status == studio.JobRunning && p.Job.Progress > 0
	}
	if !waitProject(e, id, 45*time.Second, moving) {
		e.Cancel(id)
		return []error{errors.New("run videos: the job made no progress")}
	}
	render(shot{name: "home-running", e: e, quick: true})
	render(shot{name: "queue-running", e: e, quick: true, setup: func(s *appui.Shell) { s.Navigate(core.Route{Page: core.PageQueue}) }})
	render(shot{name: "drawer-running", e: e, quick: true, setup: func(s *appui.Shell) { s.OpenQueue() }})

	mocks.mode.Store(modeFail)
	var errs []error
	if p, err := e.Create("Лист до майбутнього"); err != nil {
		errs = append(errs, err)
	} else {
		_ = e.Edit(p.ID, func(q *studio.Project) {
			q.Input.Text = "Дівчинка пише лист собі дорослій і ховає його під старою грушею. Через двадцять років лист знаходить її донька."
		})
		if err := e.Run(p.ID, studio.StagePlan, false, ""); err != nil {
			errs = append(errs, fmt.Errorf("run plan: %w", err))
		} else {
			failed := func(p studio.Project) bool { return p.Job != nil && p.Job.Status != studio.JobRunning }
			if !waitProject(e, p.ID, time.Minute, failed) {
				errs = append(errs, errors.New("run plan: the job did not end"))
			}
		}
	}
	ended := func(p studio.Project) bool { return p.Job == nil || p.Job.Status != studio.JobRunning }
	if !waitProject(e, id, 2*time.Minute, ended) {
		e.Cancel(id)
		return append(errs, errors.New("run videos: the job did not end after the mock started refusing"))
	}
	render(shot{name: "home-error", e: e})
	render(shot{name: "queue-error", e: e, setup: func(s *appui.Shell) { s.Navigate(core.Route{Page: core.PageQueue}) }})
	render(shot{name: "drawer-error", e: e, setup: func(s *appui.Shell) { s.OpenQueue() }})
	render(shot{name: "projects-error", e: e, setup: func(s *appui.Shell) { s.Navigate(core.Route{Page: core.PageProjects}) }})
	return errs
}

// freshClips starts a demo project's clips and voices over: its scenes get
// new ids (the engine resets their assets) and their frames are imported
// back from a copy.
func freshClips(e *studio.Engine, id string) error {
	p, ok := e.Get(id)
	if !ok {
		return studio.ErrNotFound
	}
	tmp, err := os.MkdirTemp("", "guishots-frames-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	saved := make([]string, len(p.Scenes))
	for i, s := range p.Scenes {
		if s.Frame.Path == "" {
			continue
		}
		src := e.Abs(id, s.Frame.Path)
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		saved[i] = filepath.Join(tmp, fmt.Sprintf("frame-%02d%s", i+1, filepath.Ext(src)))
		if err := os.WriteFile(saved[i], b, 0o644); err != nil {
			return err
		}
	}
	if err := e.Edit(id, func(q *studio.Project) {
		for i := range q.Scenes {
			q.Scenes[i].ID = ""
		}
	}); err != nil {
		return err
	}
	p, _ = e.Get(id)
	for i, s := range p.Scenes {
		if i < len(saved) && saved[i] != "" {
			if err := e.ImportImage(id, studio.RegenFrame, s.ID, saved[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func waitProject(e *studio.Engine, id string, timeout time.Duration, cond func(studio.Project) bool) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if p, ok := e.Get(id); ok && cond(p) {
			return true
		}
	}
	return false
}

// ---- folders -----------------------------------------------------------------------------------

// moduleRoot walks up from the working directory to the folder with go.mod.
func moduleRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for d := wd; ; d = filepath.Dir(d) {
		if b, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil && strings.Contains(string(b), "module ytcartell") {
			return d
		}
		if filepath.Dir(d) == d {
			return wd
		}
	}
}

// webRoot is the web studio folder next to the module (it holds data/studio).
func webRoot(mod string) string {
	for d := mod; ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, "data", "studio")); err == nil && st.IsDir() {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// protected reports whether dir lies inside the web studio's own folders.
func protected(root, dir string) bool {
	if root == "" {
		return false
	}
	for _, sub := range []string{"data", "uploads", "apps"} {
		base, err := filepath.Abs(filepath.Join(root, sub))
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(base, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ---- mock services -------------------------------------------------------------------------------

// G-Labs mock behaviour for the job shots.
const (
	modeHang int32 = iota // tasks are accepted; the first finishes, the rest never do
	modeFail              // submissions and polls are refused (a policy block)
)

type mockSet struct {
	text, glabs *httptest.Server
	mode        atomic.Int32
}

func startMocks() *mockSet {
	m := &mockSet{}
	text := http.NewServeMux()
	text.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gemini-3.8-flash"}]}`)
	})
	text.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if m.mode.Load() == modeFail {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"error":{"message":"invalid api key","type":"invalid_request_error"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`)
	})
	glabs := http.NewServeMux()
	glabs.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"ok","uptime":42}`)
	})
	glabs.HandleFunc("/api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"tasks":[]}`)
	})
	var seq atomic.Int64
	glabs.HandleFunc("/api/video/generate", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if m.mode.Load() == modeFail {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"Prompt blocked","error_code":400}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, `{"task_id":"shot-%d"}`, seq.Add(1))
	})
	glabs.HandleFunc("/api/status/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if m.mode.Load() == modeFail {
			fmt.Fprint(w, `{"status":"failed","error":"Prompt blocked","error_code":400}`)
			return
		}
		// The first clip finishes (the job shows real progress), the rest render forever.
		if strings.HasSuffix(r.URL.Path, "/shot-1") {
			fmt.Fprint(w, `{"status":"completed","results":["/files/shot-1.mp4"]}`)
			return
		}
		fmt.Fprint(w, `{"status":"running"}`)
	})
	glabs.HandleFunc("/files/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom guishots mock clip"))
	})
	m.text, m.glabs = httptest.NewServer(text), httptest.NewServer(glabs)
	return m
}

// apply points every service at the mocks (keys stay the demo's fakes).
func (m *mockSet) apply(c *config.Config) {
	c.Text.Provider = "openai"
	c.Text.BaseURL = m.text.URL + "/v1"
	c.Glabs.BaseURL = m.glabs.URL
	c.Video.Provider, c.Image.Provider = "glabs", "glabs"
	c.Google.Keys = nil
	c.UI.TourDone = true
}

func (m *mockSet) close() {
	m.text.Close()
	m.glabs.Close()
}

// demoMaterials picks pictures of the demo projects (in the temp copy) for
// the story card: two cast members and a place.
func demoMaterials(e *studio.Engine) []studio.MaterialInput {
	var cast, places []studio.MaterialInput
	for _, s := range e.List() {
		p, ok := e.Get(s.ID)
		if !ok {
			continue
		}
		for _, c := range p.Characters {
			if c.Image.Status == studio.StatusDone && c.Image.Path != "" {
				cast = append(cast, studio.MaterialInput{Kind: studio.MaterialCharacter, Name: c.Name, Path: e.Abs(s.ID, c.Image.Path)})
			}
		}
		for _, l := range p.Locations {
			if l.Image.Status == studio.StatusDone && l.Image.Path != "" {
				places = append(places, studio.MaterialInput{Kind: studio.MaterialLocation, Name: l.Name, Path: e.Abs(s.ID, l.Image.Path)})
			}
		}
	}
	out := cast[:min(2, len(cast))]
	if len(places) > 0 {
		out = append(out, places[0])
	}
	return out
}

// ageDemo moves the saved times of the demo projects in dataDir back by 0,
// 1, 3, 5 … days (newest first), so their last jobs land on different days.
func ageDemo(dataDir string) error {
	paths, err := filepath.Glob(filepath.Join(dataDir, "projects", "*", "project.json"))
	if err != nil {
		return err
	}
	type doc struct {
		path string
		m    map[string]any
		upd  int64
	}
	var docs []doc
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return err
		}
		upd, _ := m["updatedAt"].(json.Number).Int64()
		docs = append(docs, doc{path, m, upd})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].upd > docs[j].upd })
	shift := func(m map[string]any, key string, by int64) {
		if n, ok := m[key].(json.Number); ok {
			if v, err := n.Int64(); err == nil && v > 0 {
				m[key] = json.Number(strconv.FormatInt(v-by, 10))
			}
		}
	}
	for i, d := range docs {
		days := []int64{0, 1, 3, 5}[min(i, 3)]
		if days == 0 {
			continue
		}
		by := days * 24 * int64(time.Hour/time.Millisecond)
		shift(d.m, "updatedAt", by)
		shift(d.m, "createdAt", by)
		if job, ok := d.m["job"].(map[string]any); ok {
			shift(job, "startedAt", by)
			shift(job, "finishedAt", by)
		}
		b, err := json.MarshalIndent(d.m, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(d.path, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}
