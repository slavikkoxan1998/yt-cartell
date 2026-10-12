package studio

// Start materials: the user's own references given up front (Home / step 1)
// — character photos, places, objects, a style sample.
//
//	add       AddMaterial copies the picture into <project>/materials/ and, in
//	          the background, asks the text model to describe it (vision:
//	          OpenAI-compatible image_url / Gemini inline data) → Material.Look.
//	          Without vision the user's Note is the look.
//	plan      characters and locations given as materials are in every plan
//	          under their names (Character/Location.MaterialID binds them),
//	          described by their looks; objects are props the prompts show,
//	          a style sample joins the style guide (planner.go)
//	refs      a bound character/location gets the material picture as its
//	          reference image (no generation); the others are generated
//	          (production.go)
//
// Input.Materials is engine-owned: Edit keeps it, the UI changes it through
// AddMaterial / UpdateMaterial / RemoveMaterial.

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	"ytcartell/internal/gen"
	"ytcartell/internal/llm"
	"ytcartell/internal/media"
)

// Material kinds.
const (
	MaterialCharacter = "character"
	MaterialLocation  = "location"
	MaterialObject    = "object"
	MaterialStyle     = "style"
)

const (
	maxMaterials = 24
	// lookSide: pictures are downscaled to this side before a vision request.
	lookSide = 1024
	// lookWait is how long a plan waits for descriptions still being written.
	lookWait = 90 * time.Second
)

// MaterialInput is a material to add: Path is the absolute path of the
// source picture (PNG, JPG or WEBP, ≤ 40 MB).
type MaterialInput struct {
	Kind string // MaterialCharacter (default) | MaterialLocation | MaterialObject | MaterialStyle
	Name string // e.g. "Тарас" (characters and locations keep it in the plan)
	Note string // the user's description (look, role, outfit…)
	Path string
}

// MaterialExts lists the picture extensions AddMaterial accepts (for the file picker).
func MaterialExts() []string { return []string{"png", "jpg", "jpeg", "webp"} }

var errNoMaterial = errors.New("Матеріал не знайдено")

func materialKind(k string) (string, error) {
	switch k = strings.ToLower(strings.TrimSpace(k)); k {
	case "":
		return MaterialCharacter, nil
	case MaterialCharacter, MaterialLocation, MaterialObject, MaterialStyle:
		return k, nil
	}
	return "", fmt.Errorf("Невідомий вид матеріалу «%s» (персонаж, локація, предмет або стиль)", k)
}

// readPicture loads a user picture: PNG, JPEG or WEBP up to 40 MB.
func readPicture(path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, "", fmt.Errorf("файл не знайдено: %s", path)
	}
	if info.Size() > maxImportImage {
		return nil, "", errors.New("Файл завеликий (максимум 40 МБ)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("не вдалося прочитати файл: %w", err)
	}
	mime := http.DetectContentType(data)
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		return data, mime, nil
	}
	return nil, "", errors.New("Це не зображення (потрібен PNG, JPG або WEBP)")
}

// findMaterial returns a material for reading (nil when gone). Changes go
// through setMaterial.
func findMaterial(p *Project, id string) *Material {
	if i := materialIndex(p, id); i >= 0 {
		return &p.Input.Materials[i]
	}
	return nil
}

func materialIndex(p *Project, id string) int {
	for i := range p.Input.Materials {
		if p.Input.Materials[i].ID == id {
			return i
		}
	}
	return -1
}

// setMaterial replaces material i in a fresh slice: snapshots (Get) share
// the old array, so the engine never writes into it.
func setMaterial(p *Project, i int, m Material) {
	ms := append([]Material(nil), p.Input.Materials...)
	ms[i] = m
	p.Input.Materials = ms
}

// materialLook is what the plan knows of a material's look: the picture's
// description, else the user's note.
func materialLook(m *Material) string {
	if l := strings.TrimSpace(m.Look); l != "" {
		return l
	}
	return strings.TrimSpace(m.Note)
}

// AddMaterial copies a picture into the project (materials/) and adds it to
// Input.Materials; its look is described in the background (Material.Look
// fills in a little later — subscribers are notified).
func (e *Engine) AddMaterial(id string, m MaterialInput) (Material, error) {
	st := e.state(id)
	if st == nil {
		return Material{}, ErrNotFound
	}
	kind, err := materialKind(m.Kind)
	if err != nil {
		return Material{}, err
	}
	data, mime, err := readPicture(m.Path)
	if err != nil {
		return Material{}, err
	}
	mat := Material{
		ID:   newID(8),
		Kind: kind,
		Name: clipRunes(strings.TrimSpace(m.Name), 80),
		Note: clipRunes(strings.TrimSpace(m.Note), 2000),
	}
	stem := slugTag(firstNonBlank(mat.Name, kind))
	rel, err := e.writeAsset(st, "materials", fmt.Sprintf("%s-%s-%s.%s", kind, stem, newID(6), imageExt(mime)), data)
	if err != nil {
		return Material{}, err
	}
	mat.Path = rel
	full := false
	if !e.update(st, func(p *Project) {
		if full = len(p.Input.Materials) >= maxMaterials; !full {
			p.Input.Materials = append(p.Input.Materials[:len(p.Input.Materials):len(p.Input.Materials)], mat) // a fresh array (see setMaterial)
		}
	}) {
		return Material{}, ErrNotFound
	}
	if full {
		e.removeFiles(st, []string{rel})
		return Material{}, fmt.Errorf("Забагато матеріалів (максимум %d)", maxMaterials)
	}
	e.describeAsync(st, mat.ID)
	return mat, nil
}

// UpdateMaterial changes a material's kind, name or note: fn gets a copy of
// the material (its id, picture and look are kept). fn runs under the
// project lock: it must not call engine methods. A new kind is described
// again.
func (e *Engine) UpdateMaterial(id, materialID string, fn func(m *Material)) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	st.mu.Lock()
	if st.deleted.Load() {
		st.mu.Unlock()
		return ErrNotFound
	}
	i := materialIndex(st.p, materialID)
	if i < 0 {
		st.mu.Unlock()
		return errNoMaterial
	}
	m := st.p.Input.Materials[i]
	c := m
	fn(&c)
	kind, err := materialKind(c.Kind)
	if err != nil {
		st.mu.Unlock()
		return err
	}
	c.ID, c.Path, c.Look = m.ID, m.Path, m.Look
	c.Kind, c.Name, c.Note = kind, clipRunes(strings.TrimSpace(c.Name), 80), clipRunes(strings.TrimSpace(c.Note), 2000)
	again := c.Kind != m.Kind
	if again {
		c.Look = ""
	}
	setMaterial(st.p, i, c)
	list := e.commitLocked(st)
	st.mu.Unlock()
	e.emit(st, list)
	if again {
		e.describeAsync(st, materialID)
	}
	return nil
}

// RemoveMaterial removes a material and its picture. Characters and
// locations planned from it keep their reference images.
func (e *Engine) RemoveMaterial(id, materialID string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var rel string
	found := false
	if !e.update(st, func(p *Project) {
		for i, m := range p.Input.Materials {
			if m.ID != materialID {
				continue
			}
			found, rel = true, m.Path
			p.Input.Materials = append(p.Input.Materials[:i:i], p.Input.Materials[i+1:]...)
			for k := range p.Characters {
				if p.Characters[k].MaterialID == materialID {
					p.Characters[k].MaterialID = ""
				}
			}
			for k := range p.Locations {
				if p.Locations[k].MaterialID == materialID {
					p.Locations[k].MaterialID = ""
				}
			}
			return
		}
	}) {
		return ErrNotFound
	}
	if !found {
		return errNoMaterial
	}
	e.removeFiles(st, []string{rel})
	return nil
}

// ---- looks (vision) -------------------------------------------------------------------------

// lookJobs tracks descriptions being written: project folder + material id →
// a channel closed when done.
var lookJobs sync.Map

func lookKey(st *proj, materialID string) string { return st.dir + "\x00" + materialID }

// describeAsync describes a material's picture in the background.
func (e *Engine) describeAsync(st *proj, materialID string) {
	if e.closing.Load() {
		return
	}
	done := make(chan struct{})
	key := lookKey(st, materialID)
	lookJobs.Store(key, done)
	st.bg.Add(1)
	go func() {
		defer st.bg.Done()
		defer func() {
			lookJobs.CompareAndDelete(key, done)
			close(done)
			if r := recover(); r != nil {
				log.Printf("[studio:%s] material look panic: %v", st.id, r)
			}
		}()
		e.describeMaterial(st.ctx, st, materialID)
	}()
}

// MaterialDescribing reports that the text model is still describing a
// material's picture (Look fills in when it is done; "" afterwards means the
// model could not see it and the note is used).
func (e *Engine) MaterialDescribing(id, materialID string) bool {
	st := e.state(id)
	if st == nil {
		return false
	}
	_, busy := lookJobs.Load(lookKey(st, materialID))
	return busy
}

// waitLooks waits (a while, until the job stops) for material descriptions
// that are still being written, so the plan sees them.
func (e *Engine) waitLooks(j *jobCtx) {
	snap := j.snapshot()
	var pending []chan struct{}
	for _, m := range snap.Input.Materials {
		if v, ok := lookJobs.Load(lookKey(j.st, m.ID)); ok {
			pending = append(pending, v.(chan struct{}))
		}
	}
	if len(pending) == 0 {
		return
	}
	j.note("Описуємо твої матеріали…")
	timeout := time.NewTimer(lookWait)
	defer timeout.Stop()
	for _, ch := range pending {
		select {
		case <-ch:
		case <-timeout.C:
			return
		case <-j.ctx.Done():
			return
		}
	}
}

// describeMaterial asks the text model to describe the material's picture
// and stores the answer as its Look. Failures (no vision, no service) are
// only logged: the user's note stays the look.
func (e *Engine) describeMaterial(ctx context.Context, st *proj, materialID string) {
	var m Material
	if !e.read(st, func(p *Project) bool {
		if f := findMaterial(p, materialID); f != nil {
			m = *f
			return true
		}
		return false
	}) {
		return
	}
	data, err := os.ReadFile(filepath.Join(st.dir, filepath.FromSlash(m.Path)))
	if err != nil {
		return
	}
	pic, mime := lookPicture(data)
	system, prompt := lookPrompt(&m)
	reply, err := e.text.Text(ctx, llm.Request{System: system, Prompt: prompt, Temperature: 0.2, Timeout: 90 * time.Second,
		Images: []llm.Image{{MIME: mime, Data: pic}}})
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("[studio:%s] material %s not described (the note is used): %v", st.id, materialID, err)
		}
		return
	}
	look := clipRunes(strings.Join(strings.Fields(strings.Trim(strings.TrimSpace(reply), "\"«»")), " "), 1200)
	if look == "" {
		return
	}
	e.update(st, func(p *Project) {
		if i := materialIndex(p, materialID); i >= 0 && p.Input.Materials[i].Path == m.Path && p.Input.Materials[i].Kind == m.Kind {
			f := p.Input.Materials[i]
			f.Look = look
			setMaterial(p, i, f)
		}
	})
}

// lookPicture prepares a picture for a vision request: big ones are scaled
// down to lookSide as JPEG.
func lookPicture(data []byte) ([]byte, string) {
	mime := http.DetectContentType(data)
	img, _, err := media.DecodeImage(data)
	if err != nil {
		return data, mime
	}
	b := img.Bounds()
	if b.Dx() <= lookSide && b.Dy() <= lookSide && len(data) <= 4<<20 {
		return data, mime
	}
	scale := float64(lookSide) / float64(max(b.Dx(), b.Dy()))
	w, h := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	out, err := media.EncodeJPEG(dst)
	if err != nil {
		return data, mime
	}
	return out, "image/jpeg"
}

// lookPrompt asks for the English description the image model needs.
func lookPrompt(m *Material) (system, prompt string) {
	var what string
	switch m.Kind {
	case MaterialLocation:
		what = "a place in a film. Describe it in ENGLISH, 30–60 words: setting, architecture or nature, time of day, light, palette, key props. No people."
	case MaterialObject:
		what = "an object (a prop) of a film. Describe it in ENGLISH, 20–40 words: what it is, shape, size, material, colours and distinctive details."
	case MaterialStyle:
		what = "a sample of the film's visual style. Describe ONLY the style in ENGLISH, 1–2 sentences: medium or technique, palette, lighting, " +
			"texture, line work and mood — not what is shown."
	default:
		what = "a character of a film. Describe in ENGLISH, 40–70 words, everything an image model must keep: apparent age, gender, " +
			"ethnicity/skin tone, face, hair (colour, length, style), body type, the outfit with exact colours and materials, distinctive accessories. " +
			"A drawing, toy or animal is described as what it is. Not the background."
	}
	system = "You describe pictures for an AI image model. The picture shows " + what +
		" Reply with the description only: plain text, no lists, no preamble."
	prompt = "Describe the picture."
	if m.Name != "" {
		prompt += " Its name in the film: " + m.Name + "."
	}
	if m.Note != "" {
		prompt += " The author's note (any language): " + m.Note
	}
	return system, prompt
}

// ---- reference pictures from materials ----------------------------------------------------

// materialImage puts a material's picture in place as the reference image
// of a bound character/location (images/<prefix>-<tag>-<id>.<ext>, a hard
// link when possible) and returns the finished asset.
func (e *Engine) materialImage(st *proj, m *Material, prefix, tag string) (Asset, error) {
	if m.Path == "" || filepath.IsAbs(m.Path) {
		return Asset{}, errNoMaterial
	}
	src := filepath.Join(st.dir, filepath.FromSlash(m.Path))
	if !isFile(src) {
		return Asset{}, fmt.Errorf("картинку матеріалу «%s» не знайдено", firstNonBlank(m.Name, m.Kind))
	}
	ext := strings.ToLower(filepath.Ext(m.Path))
	rel := "images/" + prefix + "-" + safeName(tag) + "-" + newID(12) + ext
	st.fmu.RLock()
	defer st.fmu.RUnlock()
	if st.deleted.Load() {
		return Asset{}, ErrNotFound
	}
	dst := filepath.Join(st.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Asset{}, err
	}
	// Both files are written once and never rewritten: a link shares the bytes.
	if os.Link(src, dst) != nil {
		if err := copyFile(src, dst); err != nil {
			return Asset{}, fmt.Errorf("не вдалося скопіювати картинку матеріалу: %w", err)
		}
	}
	return Asset{Path: rel, Status: StatusDone, Progress: 100, UpdatedAt: nowMs()}, nil
}

// ---- the plan -----------------------------------------------------------------------------

// sentence ends s with a full stop (for prompt lines).
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.ContainsRune(".!?…", []rune(s)[len([]rune(s))-1]) {
		s += "."
	}
	return s
}

// hasCastMaterials reports character or location materials (the plan binds
// them) — and object materials in films whose cast holds objects.
func hasCastMaterials(p *Project) bool {
	objects := p.Input.ContentMode() != ContentCharacters
	for _, m := range p.Input.Materials {
		if m.Kind == MaterialCharacter || m.Kind == MaterialLocation || objects && m.Kind == MaterialObject {
			return true
		}
	}
	return false
}

// objectMaterials are the materials the plan binds to its objects: the
// object pictures — and, in a film without people, the character pictures
// too (a pet, a mascot, a product someone photographed as a "character").
// Copies with the object kind; their ids are the materials' own.
func objectMaterials(mats []Material, noPeople bool) []Material {
	var out []Material
	for _, m := range mats {
		if m.Kind == MaterialObject || noPeople && m.Kind == MaterialCharacter {
			m.Kind = MaterialObject
			out = append(out, m)
		}
	}
	return out
}

// boundMaterial reports a material some cast entry is bound to.
func boundMaterial(p *Project, id string) bool {
	for _, c := range p.Characters {
		if c.MaterialID == id {
			return true
		}
	}
	return false
}

// materialsBrief are the bible-pass lines about the user's materials (nil
// when there are none).
func materialsBrief(p *Project) []string {
	var lines []string
	objects, noPeople := p.Input.ContentMode() != ContentCharacters, p.Input.NoPeople()
	for i := range p.Input.Materials {
		m := &p.Input.Materials[i]
		look := sentence(materialLook(m))
		if look == "" {
			look = "(no description given)"
		}
		name := strings.TrimSpace(m.Name)
		kind := m.Kind
		if kind == MaterialCharacter && noPeople {
			kind = MaterialObject // nobody in frame: the picture is an object or a motif
		}
		if kind == MaterialObject && objects {
			if name != "" {
				lines = append(lines, fmt.Sprintf(`- Object %q (material %q): %s Include it in "objects" with exactly this name and "material": %q; `+
					`its "description" follows this look (the same shape, materials and colours in every scene).`, name, m.ID, look, m.ID))
			} else {
				lines = append(lines, fmt.Sprintf(`- Object without a name (material %q): %s Include it in "objects" with a fitting name and "material": %q; `+
					`its "description" follows this look (the same shape, materials and colours in every scene).`, m.ID, look, m.ID))
			}
			continue
		}
		switch kind {
		case MaterialCharacter, MaterialLocation:
			what := "Character"
			if m.Kind == MaterialLocation {
				what = "Location"
			}
			if name != "" {
				lines = append(lines, fmt.Sprintf(`- %s %q (material %q): %s Include it with exactly this name and "material": %q; `+
					`its "description" follows this look (the same %s in every scene).`, what, name, m.ID, look, m.ID, keepWhat(m.Kind)))
			} else {
				lines = append(lines, fmt.Sprintf(`- %s without a name (material %q): %s Include it with a fitting name and "material": %q; `+
					`its "description" follows this look (the same %s in every scene).`, what, m.ID, look, m.ID, keepWhat(m.Kind)))
			}
		case MaterialObject:
			lines = append(lines, fmt.Sprintf(`- Prop %q: %s Show it in the scenes where it fits the story.`, firstNonBlank(name, "prop"), look))
		case MaterialStyle:
			lines = append(lines, `- Visual style sample: `+look+` The whole film follows this style.`)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]string{`The user's own materials — use every one of them:`}, lines...)
}

func keepWhat(kind string) string {
	if kind == MaterialLocation {
		return "place, architecture and palette"
	}
	return "face, hair, body and outfit"
}

// propsBrief lists the object materials for the shot prompts (nil = none).
func propsBrief(p *Project) []string {
	var lines []string
	for i := range p.Input.Materials {
		// A bound object has its own reference image and @tag (not a prop).
		if m := &p.Input.Materials[i]; m.Kind == MaterialObject && !boundMaterial(p, m.ID) {
			lines = append(lines, fmt.Sprintf("- %s: %s", firstNonBlank(strings.TrimSpace(m.Name), "prop"), firstNonBlank(sentence(materialLook(m)), "(no description given)")))
		}
	}
	return lines
}

// styleLooks are the style materials' looks for the style guide.
func styleLooks(p *Project) string {
	var parts []string
	for i := range p.Input.Materials {
		if m := &p.Input.Materials[i]; m.Kind == MaterialStyle {
			if l := sentence(materialLook(m)); l != "" {
				parts = append(parts, l)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "Style reference: " + strings.Join(parts, " ")
}

// castEntry is a character or location of a new plan: the model's entry
// (nil for a material it left out) and the material bound to it.
type castEntry struct {
	raw   any
	mat   *Material
	alias string // the name the model used (scenes refer to it)
}

// bindMaterials binds the model's characters (or locations) to the
// materials of kind: by the "material" id it gave, else by name; a material
// it left out is added. Bound entries always stay, the model's own ones fill
// up to limit.
func bindMaterials(raw []any, mats []Material, kind string, limit int) []castEntry {
	out := make([]castEntry, len(raw))
	for i, r := range raw {
		out[i] = castEntry{raw: r, alias: strings.TrimSpace(jsString(get(r, "name")))}
	}
	used := map[string]bool{}
	for i := range out {
		id := strings.TrimSpace(jsString(get(out[i].raw, "material")))
		for k := range mats {
			if id != "" && mats[k].ID == id && mats[k].Kind == kind && !used[id] {
				out[i].mat, used[id] = &mats[k], true
				break
			}
		}
	}
	for k := range mats {
		m := &mats[k]
		if m.Kind != kind || used[m.ID] || strings.TrimSpace(m.Name) == "" {
			continue
		}
		for i := range out {
			if out[i].mat == nil && strings.EqualFold(out[i].alias, strings.TrimSpace(m.Name)) {
				out[i].mat, used[m.ID] = m, true
				break
			}
		}
	}
	for k := range mats {
		if m := &mats[k]; m.Kind == kind && !used[m.ID] {
			out = append(out, castEntry{mat: m})
			used[m.ID] = true
		}
	}
	bound := 0
	for _, c := range out {
		if c.mat != nil {
			bound++
		}
	}
	free := max(0, limit-bound)
	kept := out[:0]
	for _, c := range out {
		if c.mat == nil {
			if free == 0 {
				continue
			}
			free--
		}
		kept = append(kept, c)
	}
	return kept
}

// bindMaterialImages gives the bound characters/locations of a new plan
// their reference images: the previous plan's image of the same material
// when it is still there (a regenerated one stays), else the material picture.
func (e *Engine) bindMaterialImages(st *proj, prev *Project, chars []Character, locs []Location) {
	reuse := map[string]Asset{}
	keep := func(kind, materialID string, a Asset) {
		if materialID != "" && a.Status == StatusDone && a.Path != "" && isFile(filepath.Join(st.dir, filepath.FromSlash(a.Path))) {
			reuse[kind+"\x00"+materialID] = a
		}
	}
	for _, c := range prev.Characters {
		keep("c", c.MaterialID, c.Image)
	}
	for _, l := range prev.Locations {
		keep("l", l.MaterialID, l.Image)
	}
	bind := func(kind, prefix, materialID, tag string, img *Asset) {
		m := findMaterial(prev, materialID)
		if materialID == "" || m == nil {
			return
		}
		if a, ok := reuse[kind+"\x00"+materialID]; ok {
			*img = a
			return
		}
		a, err := e.materialImage(st, m, prefix, tag)
		if err != nil {
			log.Printf("[studio:%s] material %s: %v (the reference will be generated)", st.id, materialID, err)
			return
		}
		*img = a
	}
	for i := range chars {
		bind("c", "char", chars[i].MaterialID, chars[i].Tag, &chars[i].Image)
	}
	for i := range locs {
		bind("l", "loc", locs[i].MaterialID, locs[i].Tag, &locs[i].Image)
	}
}

// useMaterialImage makes a material picture the finished reference image of
// a character (char) or location; false when the picture is gone.
func (e *Engine) useMaterialImage(st *proj, m *Material, itemID string, char bool) bool {
	ref, prefix := assetRef{slotLocation, itemID}, "loc"
	if char {
		ref, prefix = assetRef{slotCharacter, itemID}, "char"
	}
	var tag string
	if !e.read(st, func(p *Project) bool {
		if char {
			if c := findCharacter(p, itemID); c != nil {
				tag = c.Tag
			}
		} else if l := findLocation(p, itemID); l != nil {
			tag = l.Tag
		}
		return tag != ""
	}) {
		return false
	}
	a, err := e.materialImage(st, m, prefix, tag)
	if err != nil {
		return false
	}
	var drop []string
	ok := false
	e.update(st, func(p *Project) {
		cur := ref.in(p)
		if cur == nil {
			drop = append(drop, a.Path)
			return
		}
		if cur.Path != "" && cur.Path != a.Path {
			drop = append(drop, cur.Path)
		}
		*cur, ok = a, true
	})
	e.discardFiles(st, drop)
	return ok
}

// addPhoto adds the user's material picture (rel, "" = none) to an image
// request as the named reference @tag.
func addPhoto(dir, rel, tag string, req *gen.ImageRequest) error {
	if rel == "" {
		return nil
	}
	img, err := readImage(dir, rel, tag)
	if err != nil {
		return fmt.Errorf("не вдалося прочитати картинку матеріалу: %w", err)
	}
	req.Refs = append(req.Refs, img)
	return nil
}

// normalizeMaterials cleans the materials of a loaded project: entries
// without an id or picture go, an unknown kind becomes a prop.
func normalizeMaterials(p *Project) {
	var kept []Material
	for _, m := range p.Input.Materials {
		if m.ID == "" || m.Path == "" {
			continue
		}
		if k, err := materialKind(m.Kind); err == nil {
			m.Kind = k
		} else {
			m.Kind = MaterialObject
		}
		kept = append(kept, m)
	}
	p.Input.Materials = kept
}
