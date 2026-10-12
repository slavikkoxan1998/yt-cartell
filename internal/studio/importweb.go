package studio

// Import from the Node/web studio: its settings (.env + data/runtime-keys.json)
// and every project of data/studio/*.json together with the files it
// references under uploads/. The web root is only read, never written.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"ytcartell/internal/config"
)

// ImportFromWeb imports the web studio found at root (the repo folder that
// holds apps/, data/, uploads/): settings go into the config store (only
// non-empty values; Google keys are added), projects are copied into the data
// folder with their files. Projects whose id already exists are skipped.
// Returns the number of imported projects; err collects what went wrong
// (other projects are still imported).
func (e *Engine) ImportFromWeb(root string) (projects int, err error) {
	if a, err := filepath.Abs(root); err == nil {
		root = a
	}
	studioDir := filepath.Join(root, "data", "studio")
	keysFile := filepath.Join(root, "data", "runtime-keys.json")
	envFile := filepath.Join(root, ".env")
	hasStudio := isDir(studioDir)
	if !hasStudio && !isFile(keysFile) && !isFile(envFile) {
		return 0, errors.New("У цій папці не знайдено веб-версію студії (немає data/studio, data/runtime-keys.json чи .env)")
	}

	var errs []error
	env := parseDotEnv(envFile)
	if err := e.importSettings(env, keysFile); err != nil {
		errs = append(errs, err)
	}

	uploads := strings.TrimSpace(env["UPLOADS_DIR"])
	if uploads == "" {
		uploads = "uploads"
	}
	if !filepath.IsAbs(uploads) {
		uploads = filepath.Join(root, uploads)
	}
	uploads = filepath.Clean(uploads)

	if hasStudio {
		entries, rerr := os.ReadDir(studioDir)
		if rerr != nil {
			errs = append(errs, fmt.Errorf("не вдалося прочитати %s: %w", studioDir, rerr))
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, en := range entries {
			name := en.Name()
			if en.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".json") {
				continue
			}
			id := name[:len(name)-len(".json")]
			if !reProjectID.MatchString(id) || e.exists(id) {
				continue
			}
			if err := e.importProject(filepath.Join(studioDir, name), id, uploads); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
				continue
			}
			projects++
		}
	}
	if projects > 0 {
		e.notify("")
	}
	return projects, errors.Join(errs...)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// exists: the id is taken by a loaded project or a folder in the data dir.
func (e *Engine) exists(id string) bool {
	if e.state(id) != nil {
		return true
	}
	e.mu.Lock()
	dir := filepath.Join(projectsRoot(e.dataDir), id)
	e.mu.Unlock()
	_, err := os.Stat(dir)
	return err == nil
}

// ---- settings ----------------------------------------------------------------------------

// parseDotEnv reads KEY=VALUE lines like dotenv (quotes, `export`, # comments).
func parseDotEnv(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	text := strings.TrimPrefix(string(b), string(rune(0xFEFF)))
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if len(v) >= 2 && strings.ContainsRune(`"'`+"`", rune(v[0])) {
			if end := strings.LastIndexByte(v, v[0]); end > 0 {
				q := v[0]
				v = v[1:end]
				if q == '"' {
					v = strings.ReplaceAll(v, `\n`, "\n")
				}
			}
		} else if i := strings.IndexByte(v, '#'); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[k] = v
	}
	return out
}

var reKeySplit = regexp.MustCompile(`[\s,]+`)

func splitKeys(s string) []string {
	var out []string
	for _, k := range reKeySplit.Split(s, -1) {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// anyString renders a JSON scalar as trimmed text ("" for null/objects).
func anyString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func anyInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x + 0.5), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return int(f + 0.5), true
		}
	}
	return 0, false
}

func section(m map[string]any, key string) map[string]any {
	if s, ok := m[key].(map[string]any); ok {
		return s
	}
	return map[string]any{}
}

// importSettings maps the web settings onto the config: .env first, then
// data/runtime-keys.json on top (as the Node server merges them).
func (e *Engine) importSettings(env map[string]string, keysFile string) error {
	var rk map[string]any
	var perr error
	if b, err := os.ReadFile(keysFile); err == nil {
		if err := json.Unmarshal(b, &rk); err != nil {
			perr = fmt.Errorf("runtime-keys.json пошкоджено: %w", err)
			rk = nil
		}
	}
	if len(env) == 0 && rk == nil {
		return perr
	}
	set := func(dst *string, v string) {
		if v = strings.TrimSpace(v); v != "" {
			*dst = v
		}
	}
	setInt := func(dst *int, v any) {
		if n, ok := anyInt(v); ok && n > 0 {
			*dst = n
		}
	}
	err := e.cfg.Update(func(c *config.Config) {
		// Never switch a service to Google without a Google key: the studio
		// would stop at «ключів Google не задано» (an OpenAI-compatible text
		// endpoint, G-Labs frames stay what they were).
		prev := *c
		defer func() {
			if len(c.Google.Keys) > 0 {
				return
			}
			if c.Text.Provider == "google" && prev.Text.Provider != "google" {
				c.Text.Provider = prev.Text.Provider
			}
			if c.Image.Provider == "google" && prev.Image.Provider != "google" {
				c.Image.Provider = prev.Image.Provider
			}
			if c.Video.Provider == "google" && prev.Video.Provider != "google" {
				c.Video.Provider = prev.Video.Provider
			}
		}()
		// .env
		set(&c.Text.Provider, env["TEXT_PROVIDER"])
		set(&c.Text.BaseURL, env["TEXT_BASE_URL"])
		set(&c.Text.APIKey, env["TEXT_API_KEY"])
		set(&c.Text.Model, env["TEXT_MODEL"])
		set(&c.Image.Provider, env["IMAGE_PROVIDER"])
		set(&c.Video.Provider, env["VIDEO_PROVIDER"])
		set(&c.Video.GlabsModel, env["GLABS_VIDEO_MODEL"])
		set(&c.Video.Resolution, env["GLABS_VIDEO_RESOLUTION"])
		if v := env["VIDEO_LENGTH"]; v != "" {
			setInt(&c.Video.Length, v)
		}
		set(&c.Glabs.BaseURL, env["GLABS_BASE_URL"])
		set(&c.Glabs.APIKey, env["GLABS_API_KEY"])
		if v := env["PIPELINE_CONCURRENCY"]; v != "" {
			setInt(&c.Concurrency, v)
		}
		set(&c.Google.StoryModel, env["GEMINI_STORY_MODEL"])
		set(&c.Google.ImageModel, env["GEMINI_IMAGE_MODEL"])
		set(&c.Google.VeoModel, env["VEO_MODEL"])
		set(&c.TTS.Provider, env["TTS_PROVIDER"])
		set(&c.TTS.APIKey, env["ELEVENLABS_API_KEY"])
		set(&c.TTS.APIKey, env["TTS_API_KEY"])
		set(&c.TTS.BaseURL, env["TTS_BASE_URL"])
		set(&c.TTS.Voice, env["TTS_VOICE"])
		set(&c.TTS.Model, env["TTS_MODEL"])

		var keys []string
		if rk != nil {
			if list, ok := rk["googleKeys"].([]any); ok {
				for _, k := range list {
					if s := anyString(k); s != "" {
						keys = append(keys, s)
					}
				}
			}
		}
		keys = append(keys, splitKeys(env["GOOGLE_API_KEYS"]+" "+env["GEMINI_API_KEYS"])...)
		for i := 1; i <= 9; i++ {
			keys = append(keys, splitKeys(env[fmt.Sprintf("GEMINI_API_KEY_%d", i)])...)
		}
		if k := firstNonBlank(env["GEMINI_API_KEY"], env["VEO_API_KEY"]); k != "" {
			keys = append(keys, strings.TrimSpace(k))
		}
		c.Google.Keys = append(c.Google.Keys, keys...) // Normalize drops duplicates

		if rk == nil {
			return
		}
		// data/runtime-keys.json (what the web Settings screen saved)
		t := section(rk, "tts")
		set(&c.TTS.Provider, anyString(t["provider"]))
		set(&c.TTS.APIKey, anyString(rk["ttsKey"])) // older files
		set(&c.TTS.APIKey, anyString(t["apiKey"]))
		set(&c.TTS.BaseURL, strings.TrimRight(anyString(t["baseUrl"]), "/"))
		set(&c.TTS.Voice, anyString(t["voice"]))
		set(&c.TTS.Model, anyString(t["model"]))

		pr := section(rk, "providers")
		tx := section(pr, "text")
		set(&c.Text.Provider, anyString(tx["provider"]))
		set(&c.Text.BaseURL, anyString(tx["baseUrl"]))
		set(&c.Text.APIKey, anyString(tx["apiKey"]))
		set(&c.Text.Model, anyString(tx["model"]))
		set(&c.Image.Provider, anyString(section(pr, "image")["provider"]))
		vd := section(pr, "video")
		set(&c.Video.Provider, anyString(vd["provider"]))
		set(&c.Video.GlabsModel, anyString(vd["glabsModel"]))
		set(&c.Video.Resolution, anyString(vd["resolution"]))
		setInt(&c.Video.Length, vd["length"])
		gl := section(pr, "glabs")
		set(&c.Glabs.BaseURL, anyString(gl["baseUrl"]))
		set(&c.Glabs.APIKey, anyString(gl["apiKey"]))
		setInt(&c.Concurrency, pr["concurrency"])
	})
	if err != nil {
		err = fmt.Errorf("налаштування імпортовано, але не збережено: %w", err)
	}
	return errors.Join(perr, err)
}

// ---- projects ----------------------------------------------------------------------------

// webAsset / webProject capture the URLs of a web project (the Go model
// stores relative paths instead); slices are index-aligned with Project.
type webAsset struct {
	URL string `json:"url"`
}

type webProject struct {
	Characters []struct {
		Image webAsset `json:"image"`
	} `json:"characters"`
	Locations []struct {
		Image webAsset `json:"image"`
	} `json:"locations"`
	Scenes []struct {
		Frame webAsset `json:"frame"`
		Video webAsset `json:"video"`
		Audio webAsset `json:"audio"`
	} `json:"scenes"`
	Montage struct {
		Output   webAsset `json:"output"`
		Cover    webAsset `json:"cover"`
		MusicURL string   `json:"musicUrl"`
		SRTURL   string   `json:"srtUrl"`
	} `json:"montage"`
}

var reUploadURL = regexp.MustCompile(`^(?:https?://[^/]+)?/uploads/([^?#]+)`)

// fileCopier copies web uploads into a project folder, once per source file.
type fileCopier struct {
	uploads, dir string
	done         map[string]string // source → relative path
	names        map[string]bool   // relative paths in use
}

// copy maps a web URL to a local uploads file and copies it into <dir>/<sub>.
// "" when the URL is not one of ours or the file is missing.
func (fc *fileCopier) copy(u, sub string) (string, error) {
	m := reUploadURL.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return "", nil
	}
	relURL := m[1]
	if dec, err := unescapePath(relURL); err == nil {
		relURL = dec
	}
	src := filepath.Join(fc.uploads, filepath.FromSlash(relURL))
	if !within(fc.uploads, src) || !isFile(src) {
		return "", nil
	}
	if rel, ok := fc.done[src]; ok {
		return rel, nil
	}
	base := safeFileName(filepath.Base(src))
	rel := sub + "/" + base
	for k := 2; fc.names[rel]; k++ {
		ext := filepath.Ext(base)
		rel = fmt.Sprintf("%s/%s-%d%s", sub, strings.TrimSuffix(base, ext), k, ext)
	}
	dst := filepath.Join(fc.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := copyFile(src, dst); err != nil {
		return "", err
	}
	fc.done[src] = rel
	fc.names[rel] = true
	return rel, nil
}

// unescapePath is decodeURIComponent for a URL path.
func unescapePath(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			b.WriteByte(s[i])
			continue
		}
		if i+2 >= len(s) {
			return "", errors.New("bad escape")
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return "", err
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), nil
}

// safeFileName keeps a readable file name without path or odd characters.
func safeFileName(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	stem := safeName(strings.TrimSuffix(name, filepath.Ext(name)))
	if len(stem) > 80 {
		stem = stem[:80]
	}
	return stem + safeExt(ext)
}

func safeExt(ext string) string {
	if ext == "" {
		return ""
	}
	return "." + strings.ToLower(safeName(ext[1:]))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

func (e *Engine) importProject(file, id, uploads string) (err error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	p, err := decodeProject(b)
	if err != nil {
		return fmt.Errorf("не JSON-проєкт: %w", err)
	}
	var w webProject
	_ = json.Unmarshal(b, &w) // wrong types just leave URLs empty

	e.mu.Lock()
	dir := filepath.Join(projectsRoot(e.dataDir), id)
	e.mu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("не вдалося створити папку проєкту: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	fc := &fileCopier{uploads: uploads, dir: dir, done: map[string]string{}, names: map[string]bool{}}
	var cerr error
	mapAsset := func(a *Asset, u, sub string) {
		a.Path = ""
		if strings.TrimSpace(u) == "" {
			return // never generated (or failed): keep its status
		}
		rel, err := fc.copy(u, sub)
		if err != nil && cerr == nil {
			cerr = err
		}
		if rel == "" {
			*a = EmptyAsset() // file missing: generate again later
			return
		}
		a.Path = rel
	}
	for i := range p.Characters {
		u := ""
		if i < len(w.Characters) {
			u = w.Characters[i].Image.URL
		}
		mapAsset(&p.Characters[i].Image, u, "images")
	}
	for i := range p.Locations {
		u := ""
		if i < len(w.Locations) {
			u = w.Locations[i].Image.URL
		}
		mapAsset(&p.Locations[i].Image, u, "images")
	}
	for i := range p.Scenes {
		var fu, vu, au string
		if i < len(w.Scenes) {
			fu, vu, au = w.Scenes[i].Frame.URL, w.Scenes[i].Video.URL, w.Scenes[i].Audio.URL
		}
		s := &p.Scenes[i]
		mapAsset(&s.Frame, fu, "images")
		mapAsset(&s.Video, vu, "videos")
		mapAsset(&s.Audio, au, "audio")
	}
	mapAsset(&p.Montage.Output, w.Montage.Output.URL, "montage")
	mapAsset(&p.Montage.Cover, w.Montage.Cover.URL, "images")
	p.Montage.MusicPath, p.Montage.SRTPath = "", ""
	if w.Montage.MusicURL != "" {
		rel, err := fc.copy(w.Montage.MusicURL, "user")
		if err != nil && cerr == nil {
			cerr = err
		}
		p.Montage.MusicPath = rel
	}
	if p.Montage.MusicPath == "" {
		p.Montage.MusicName = ""
	}
	if w.Montage.SRTURL != "" {
		rel, err := fc.copy(w.Montage.SRTURL, "montage")
		if err != nil && cerr == nil {
			cerr = err
		}
		p.Montage.SRTPath = rel
	}
	if cerr != nil {
		return fmt.Errorf("не вдалося скопіювати файли: %w", cerr)
	}

	p.ID = id
	normalize(p)
	now := nowMs()
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = p.CreatedAt
	}
	if strings.TrimSpace(p.Title) == "" {
		p.Title = "Новий проєкт"
	}

	st := newProj(id, dir, p)
	st.dirty = true
	if err := e.writeNow(st); err != nil {
		return err
	}
	e.mu.Lock()
	e.projects[id] = st
	e.mu.Unlock()
	e.notify(id)
	return nil
}
