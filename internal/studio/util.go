package studio

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ytcartell/internal/media"
)

// ---- ids / time ---------------------------------------------------------------------

// nanoid's URL-safe alphabet (64 symbols → one byte masks to one symbol).
const idAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLF_GQZbfghjklqvwyzrict"

func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = idAlphabet[b[i]&63]
	}
	return string(b)
}

var (
	reClientID  = regexp.MustCompile(`^[\w-]{4,24}$`)
	reProjectID = regexp.MustCompile(`^[\w-]{1,64}$`)
)

// clientID keeps an id the caller generated for a new item (so references to
// it survive the save) unless it is malformed or already taken.
func clientID(v string, taken map[string]bool) string {
	if reClientID.MatchString(v) && !taken[v] && v != "narrator" {
		return v
	}
	for {
		id := newID(8)
		if !taken[id] {
			return id
		}
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }

// ---- strings ----------------------------------------------------------------------

// clipRunes is the TS str(v, max): at most max characters.
func clipRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max])
}

func clamp01(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Max(0, math.Min(1, x))
}

// safeName keeps a file-name fragment to [A-Za-z0-9_-].
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "item"
	}
	return b.String()
}

// countWords counts whitespace-separated words (TS /\S+/g).
func countWords(t string) int { return len(strings.Fields(t)) }

// SpeechSeconds is the estimated speaking time of a line (words / 2.3).
func SpeechSeconds(text string) float64 { return float64(countWords(text)) / wps }

// ---- loosely typed JSON (model replies) ----------------------------------------------

// jobj is a JSON object that keeps its key order: JS objects do, and asArray
// picks the FIRST array member.
type jobj struct {
	keys []string
	vals map[string]any
}

func (o *jobj) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// parseJSON decodes like JSON.parse: objects → *jobj, arrays → []any,
// numbers → float64.
func parseJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	return readJSON(dec)
}

func readJSON(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := &jobj{vals: map[string]any{}}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, _ := kt.(string)
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}
			o.set(k, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return o, nil
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, errors.New("unexpected JSON delimiter")
}

// get is v?.[key].
func get(v any, key string) any {
	if o, ok := v.(*jobj); ok {
		return o.vals[key]
	}
	return nil
}

// asArray is the TS helper: an array as is, or the first array member of an
// object ({"scenes":[…]}), else empty.
func asArray(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case *jobj:
		for _, k := range x.keys {
			if a, ok := x.vals[k].([]any); ok {
				return a
			}
		}
	}
	return nil
}

// truthy is JS truthiness.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	case bool:
		return x
	}
	return true
}

// jsString is String(v ?? "").
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if math.IsNaN(x) {
			return "NaN"
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = jsString(e)
		}
		return strings.Join(parts, ",")
	case *jobj:
		return "[object Object]"
	}
	return ""
}

// ---- files --------------------------------------------------------------------------

func isFile(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

var mimeByExt = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp",
	".mp4": "video/mp4", ".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4",
}

// imageExt is the file extension for an image MIME type (jpeg → jpg).
func imageExt(mime string) string {
	_, sub, _ := strings.Cut(mime, "/")
	sub, _, _ = strings.Cut(sub, ";")
	sub = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(sub)), "jpeg", "jpg")
	sub = safeName(sub)
	if sub == "item" || sub == "" {
		return "png"
	}
	return sub
}

// within reports whether path is inside dir (or dir itself is excluded).
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// writeFileAtomic writes via a temp file + rename, retrying the rename a few
// times (Windows: antivirus/indexer may briefly hold the target).
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	var err error
	for i := 0; i < 6; i++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 40 * time.Millisecond)
	}
	_ = os.Remove(tmp)
	return err
}

// removeAllRetry removes a folder, retrying briefly for locked files.
func removeAllRetry(dir string) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
		time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
	}
	return err
}

// isCancel reports a cancellation error (context or ffmpeg montage).
func isCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, media.ErrCancelled)
}

// errText is the user-facing text of an error.
func errText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled), errors.Is(err, media.ErrCancelled):
		return "Зупинено"
	case errors.Is(err, context.DeadlineExceeded):
		return "Сервіс не відповів вчасно — спробуй ще раз"
	}
	return err.Error()
}

// ---- state helpers used by the UI --------------------------------------------------

// AssetState is the visual state of an asset: empty|running|done|error|stale
// (web lib.js assetState).
func AssetState(a Asset) string {
	switch {
	case a.Status == StatusRunning || a.Status == StatusQueued:
		return "running"
	case a.Status == StatusError:
		return "error"
	case a.Status == StatusDone && a.Stale:
		return "stale"
	case a.Status == StatusDone:
		return "done"
	}
	return "empty"
}

// SceneState is a scene's strip colour on the stripboard:
// planned|framed|shot|stale|running|error (web lib.js sceneState).
func SceneState(s Scene) string {
	parts := [3]Asset{s.Frame, s.Video, s.Audio}
	for _, a := range parts {
		if a.Status == StatusRunning || a.Status == StatusQueued {
			return "running"
		}
	}
	for _, a := range parts {
		if a.Status == StatusError {
			return "error"
		}
	}
	for _, a := range parts {
		if a.Status == StatusDone && a.Stale {
			return "stale"
		}
	}
	switch {
	case s.Video.Status == StatusDone:
		return "shot"
	case s.Frame.Status == StatusDone:
		return "framed"
	}
	return "planned"
}

// ---- deep copy --------------------------------------------------------------------

// cloneProject returns a deep copy (no shared slices or pointers).
func cloneProject(p *Project) Project {
	c := *p
	if p.Input.Materials != nil {
		c.Input.Materials = append(make([]Material, 0, len(p.Input.Materials)), p.Input.Materials...)
	}
	if p.Input.ExtraNarrators != nil {
		c.Input.ExtraNarrators = append(make([]string, 0, len(p.Input.ExtraNarrators)), p.Input.ExtraNarrators...)
	}
	if p.Characters != nil {
		c.Characters = append(make([]Character, 0, len(p.Characters)), p.Characters...)
	}
	if p.Locations != nil {
		c.Locations = append(make([]Location, 0, len(p.Locations)), p.Locations...)
	}
	if p.Scenes != nil {
		c.Scenes = make([]Scene, len(p.Scenes))
		for i, s := range p.Scenes {
			if s.CharacterIDs != nil {
				s.CharacterIDs = append(make([]string, 0, len(s.CharacterIDs)), s.CharacterIDs...)
			}
			if s.Lines != nil {
				s.Lines = append(make([]Line, 0, len(s.Lines)), s.Lines...)
			}
			if s.Parts != nil {
				s.Parts = append(make([]Line, 0, len(s.Parts)), s.Parts...)
			}
			s.Code = cloneCode(s.Code)
			c.Scenes[i] = s
		}
	}
	if p.Job != nil {
		j := *p.Job
		c.Job = &j
	}
	if p.Montage.Gaps != nil {
		c.Montage.Gaps = append(make([]MontageGap, 0, len(p.Montage.Gaps)), p.Montage.Gaps...)
	}
	return c
}

// assetPaths lists every file the project references (relative paths).
func assetPaths(p *Project) map[string]bool {
	m := map[string]bool{}
	add := func(rel string) {
		if rel != "" {
			m[rel] = true
		}
	}
	for _, c := range p.Characters {
		add(c.Image.Path)
	}
	for _, l := range p.Locations {
		add(l.Image.Path)
	}
	for _, s := range p.Scenes {
		add(s.Frame.Path)
		add(s.Board.Path)
		add(s.Video.Path)
		add(s.Audio.Path)
		for _, rel := range codeAssets(&s) { // «Анімація кодом» pictures and preview
			add(rel)
		}
	}
	add(p.Montage.Output.Path)
	add(p.Montage.Cover.Path)
	add(p.Montage.CoverFinal.Path)
	add(p.Montage.MusicPath)
	add(p.Montage.SRTPath)
	add(p.Input.VoiceFile)
	add(p.Input.TranscriptFile)
	for _, mt := range p.Input.Materials {
		add(mt.Path)
	}
	return m
}

// orphans are files referenced by before but no longer by after.
func orphans(before, after map[string]bool) []string {
	var out []string
	for rel := range before {
		if !after[rel] {
			out = append(out, rel)
		}
	}
	return out
}

func findCharacter(p *Project, id string) *Character {
	for i := range p.Characters {
		if p.Characters[i].ID == id {
			return &p.Characters[i]
		}
	}
	return nil
}

func findLocation(p *Project, id string) *Location {
	for i := range p.Locations {
		if p.Locations[i].ID == id {
			return &p.Locations[i]
		}
	}
	return nil
}

func findScene(p *Project, id string) *Scene {
	for i := range p.Scenes {
		if p.Scenes[i].ID == id {
			return &p.Scenes[i]
		}
	}
	return nil
}

// slot names one asset of a project; assetRef locates it by item id, so a
// background task survives the lists being re-ordered or replaced by edits.
type slot int

const (
	slotCharacter slot = iota // Character.Image
	slotLocation              // Location.Image
	slotFrame                 // Scene.Frame
	slotVideo                 // Scene.Video
	slotAudio                 // Scene.Audio
	slotCover                 // Montage.Cover
	slotOutput                // Montage.Output
)

type assetRef struct {
	slot slot
	id   string
}

// in returns the live asset (valid only under the project lock), nil when gone.
func (r assetRef) in(p *Project) *Asset {
	switch r.slot {
	case slotCharacter:
		if c := findCharacter(p, r.id); c != nil {
			return &c.Image
		}
	case slotLocation:
		if l := findLocation(p, r.id); l != nil {
			return &l.Image
		}
	case slotFrame, slotVideo, slotAudio:
		if s := findScene(p, r.id); s != nil {
			switch r.slot {
			case slotFrame:
				return &s.Frame
			case slotVideo:
				return &s.Video
			default:
				return &s.Audio
			}
		}
	case slotCover:
		return &p.Montage.Cover
	case slotOutput:
		return &p.Montage.Output
	}
	return nil
}
