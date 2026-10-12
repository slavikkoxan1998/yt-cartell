package studio

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/media"
)

// matMock is a text model that can see: it describes pictures (OpenAI
// image_url parts), plans with the user's materials (binding a character by
// its material id under another name) and writes shots.
type matMock struct {
	mu        sync.Mutex
	noVision  bool     // vision requests fail (a text-only model)
	looks     []string // vision prompts
	pictures  [][]byte // pictures that came with them
	bibles    []string
	shots     []string
	reqPerKey map[string]int
}

var reMatID = regexp.MustCompile(`(Character|Location) "([^"]+)" \(material "([^"]+)"\)`)

func (m *matMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if r.Header.Get("Authorization") != "Bearer txt-test" || json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Messages) != 1 {
		w.WriteHeader(400)
		return
	}
	var text string
	var pics [][]byte
	if json.Unmarshal(req.Messages[0].Content, &text) != nil {
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		_ = json.Unmarshal(req.Messages[0].Content, &parts)
		for _, p := range parts {
			switch p.Type {
			case "text":
				text += p.Text
			case "image_url":
				_, b64, _ := strings.Cut(p.ImageURL.URL, ";base64,")
				b, _ := base64.StdEncoding.DecodeString(b64)
				pics = append(pics, b)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var reply string
	switch {
	case strings.Contains(text, "You describe pictures for an AI image model"):
		if m.noVision || len(pics) == 0 {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"image input is not supported by this model"}}`)
			return
		}
		m.looks = append(m.looks, text)
		m.pictures = append(m.pictures, pics...)
		switch {
		case strings.Contains(text, "shows a place"):
			reply = "LOOK place: a misty birch grove at dawn."
		case strings.Contains(text, "an object (a prop)"):
			reply = "LOOK prop: a small red tin lantern with a brass handle."
		case strings.Contains(text, "visual style"):
			reply = "LOOK style: thick gouache strokes, warm ochre palette."
		default:
			reply = "LOOK character: a 7-year-old boy, red curly hair, green wool coat, yellow boots."
		}
	case strings.Contains(text, "award-winning film director"):
		m.bibles = append(m.bibles, text)
		ids := map[string]string{}
		for _, mm := range reMatID.FindAllStringSubmatch(text, -1) {
			ids[mm[2]] = mm[3]
		}
		n := 3
		if mm := reExactly.FindStringSubmatch(text); mm != nil {
			fmt.Sscan(mm[1], &n)
		}
		scenes := make([]any, n)
		for i := range scenes {
			scenes[i] = map[string]any{"title": fmt.Sprintf("Сцена %d", i+1), "narration": fmt.Sprintf("Рядок %d.", i+1),
				"speaker": "narrator", "characters": []any{"Taras the Brave", "Лисиця"}, "location": "Гай"}
		}
		b, _ := json.Marshal(map[string]any{
			"title": "Ліхтарик", "logline": "Хлопчик і лисиця.", "styleGuide": "Soft light.",
			"characters": []any{
				// The model renamed the user's character but kept the material id.
				map[string]any{"name": "Taras the Brave", "material": ids["Тарас"], "role": "hero", "gender": "male", "description": "MODEL DESCRIPTION"},
				map[string]any{"name": "Лисиця", "material": "", "role": "guide", "gender": "female", "description": "A small orange fox."},
			},
			"locations": []any{map[string]any{"name": "Гай", "material": ids["Березовий гай"], "description": "MODEL PLACE"}},
			"scenes":    scenes,
		})
		reply = string(b)
	case strings.Contains(text, "storyboard artist"):
		m.shots = append(m.shots, text)
		reply = shotsReply(text)
	default:
		reply = "OK"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": reply}}}})
}

func (m *matMock) count() (looks, bibles int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.looks), len(m.bibles)
}

func matEnv(t *testing.T, mut func(c *config.Config)) (*testEnv, *matMock) {
	t.Helper()
	mm := &matMock{}
	srv := httptest.NewServer(mm)
	allow(srv)
	t.Cleanup(srv.Close)
	env := newEnv(t, func(c *config.Config) {
		c.Text.BaseURL = srv.URL + "/v1"
		if mut != nil {
			mut(c)
		}
	})
	return env, mm
}

// picture writes a w×h JPEG of colour c and returns its path.
func picture(t *testing.T, name string, w, h int, c color.RGBA) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	must(t, os.WriteFile(p, jpegOf(t, w, h, c), 0o644))
	return p
}

func waitLook(t *testing.T, e *Engine, id, materialID string) Material {
	t.Helper()
	p := waitFor(t, e, id, "material look", func(p Project) bool {
		m := findMaterial(&p, materialID)
		return m != nil && m.Look != ""
	})
	return *findMaterial(&p, materialID)
}

func TestMaterialsAPI(t *testing.T) {
	t.Parallel()
	env, mm := matEnv(t, nil)
	e := env.e
	p, _ := e.Create("Матеріали")
	tmp := t.TempDir()

	// Refused inputs.
	txt := filepath.Join(tmp, "notes.txt")
	must(t, os.WriteFile(txt, []byte("not a picture"), 0o644))
	big := filepath.Join(tmp, "big.png")
	f, _ := os.Create(big)
	f.Truncate(maxImportImage + 1)
	f.Close()
	for _, tc := range []struct {
		in   MaterialInput
		want string
	}{
		{MaterialInput{Kind: "dragon", Path: txt}, "Невідомий вид матеріалу"},
		{MaterialInput{Path: filepath.Join(tmp, "none.jpg")}, "файл не знайдено"},
		{MaterialInput{Path: txt}, "Це не зображення"},
		{MaterialInput{Path: big}, "Файл завеликий"},
	} {
		if _, err := e.AddMaterial(p.ID, tc.in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v", tc.in, err)
		}
	}
	if _, err := e.AddMaterial("missing", MaterialInput{Path: txt}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	// A character photo: copied into materials/, described in the background.
	big1 := picture(t, "Тарас фото.jpg", 2400, 1600, color.RGBA{200, 80, 40, 255})
	m, err := e.AddMaterial(p.ID, MaterialInput{Name: "  Тарас ", Note: "хлопчик у зеленому пальті", Path: big1})
	must(t, err)
	if m.Kind != MaterialCharacter || m.Name != "Тарас" || m.ID == "" || !strings.HasPrefix(m.Path, "materials/character-taras-") || !strings.HasSuffix(m.Path, ".jpg") {
		t.Fatalf("material %+v", m)
	}
	fileExists(t, e.Abs(p.ID, m.Path))
	got := waitLook(t, e, p.ID, m.ID)
	if got.Look != "LOOK character: a 7-year-old boy, red curly hair, green wool coat, yellow boots." {
		t.Fatalf("look %q", got.Look)
	}
	mm.mu.Lock()
	prompt, pic := mm.looks[0], mm.pictures[0]
	mm.mu.Unlock()
	mustContain(t, "vision prompt", prompt, "shows a character of a film", "Its name in the film: Тарас.", "The author's note (any language): хлопчик у зеленому пальті")
	// A big photo goes to the model scaled down (≤ 1024 px JPEG).
	if img, format, err := decodeAny(pic); err != nil || format != "jpeg" || img.Dx() > lookSide || img.Dy() > lookSide {
		t.Fatalf("sent picture %v %s %v", img, format, err)
	}

	// Update: the note changes, the picture and look stay; a new kind is described again.
	must(t, e.UpdateMaterial(p.ID, m.ID, func(x *Material) { x.Note = "новий опис"; x.Path = "../evil.jpg"; x.Look = "hack" }))
	q, _ := e.Get(p.ID)
	if x := findMaterial(&q, m.ID); x.Note != "новий опис" || x.Path != m.Path || x.Look != got.Look {
		t.Fatalf("updated %+v", x)
	}
	must(t, e.UpdateMaterial(p.ID, m.ID, func(x *Material) { x.Kind = MaterialLocation }))
	if l := waitFor(t, e, p.ID, "new look", func(p Project) bool {
		x := findMaterial(&p, m.ID)
		return x.Kind == MaterialLocation && strings.HasPrefix(x.Look, "LOOK place")
	}); len(l.Input.Materials) != 1 {
		t.Fatal("materials")
	}
	if err := e.UpdateMaterial(p.ID, m.ID, func(x *Material) { x.Kind = "nope" }); err == nil {
		t.Fatal("bad kind accepted")
	}
	if err := e.UpdateMaterial(p.ID, "zzz", func(*Material) {}); !errors.Is(err, errNoMaterial) {
		t.Fatal(err)
	}

	// Edit cannot change materials (engine-owned).
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Materials[0].Path = "../evil.jpg"
		p.Input.Materials = append(p.Input.Materials, Material{ID: "x", Path: "evil.jpg"})
	}))
	q, _ = e.Get(p.ID)
	if len(q.Input.Materials) != 1 || q.Input.Materials[0].Path != m.Path {
		t.Fatalf("edit changed materials %+v", q.Input.Materials)
	}

	// Without vision the note is the look.
	mm.mu.Lock()
	mm.noVision = true
	mm.mu.Unlock()
	o, err := e.AddMaterial(p.ID, MaterialInput{Kind: MaterialObject, Name: "Ліхтар", Note: "червоний ліхтарик", Path: picture(t, "l.jpg", 64, 64, color.RGBA{220, 0, 0, 255})})
	must(t, err)
	waitIdle(t, e)
	waitNoLooks(t, e, p.ID)
	q, _ = e.Get(p.ID)
	if x := findMaterial(&q, o.ID); x.Look != "" || materialLook(x) != "червоний ліхтарик" {
		t.Fatalf("no vision %+v", x)
	}

	// Remove: gone with its file.
	must(t, e.RemoveMaterial(p.ID, m.ID))
	if _, err := os.Stat(e.Abs(p.ID, m.Path)); !os.IsNotExist(err) {
		t.Fatal("material file kept")
	}
	if err := e.RemoveMaterial(p.ID, m.ID); !errors.Is(err, errNoMaterial) {
		t.Fatal(err)
	}
	q, _ = e.Get(p.ID)
	if len(q.Input.Materials) != 1 || q.Input.Materials[0].ID != o.ID {
		t.Fatalf("after remove %+v", q.Input.Materials)
	}
	if strings.Join(MaterialExts(), ",") != "png,jpg,jpeg,webp" {
		t.Fatal("exts")
	}
}

// decodeAny decodes a picture's size and format.
func decodeAny(b []byte) (image.Rectangle, string, error) {
	img, format, err := media.DecodeImage(b)
	if err != nil {
		return image.Rectangle{}, "", err
	}
	return img.Bounds(), format, nil
}

// waitNoLooks waits until no description of the project runs.
func waitNoLooks(t *testing.T, e *Engine, id string) {
	t.Helper()
	st := e.state(id)
	p, _ := e.Get(id)
	for _, m := range p.Input.Materials {
		if v, ok := lookJobs.Load(lookKey(st, m.ID)); ok {
			<-v.(chan struct{})
		}
	}
}

// TestMaterialsInPlan: characters and locations from materials are in the
// plan under the user's names, with their looks and pictures; a prop steers
// the shots, a style sample the style guide; re-planning keeps the pictures.
func TestMaterialsInPlan(t *testing.T) {
	t.Parallel()
	env, mm := matEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Text = "Хлопчик Тарас іде з ліхтариком через гай."
	}))
	add := func(kind, name, note string, c color.RGBA) Material {
		m, err := e.AddMaterial(p.ID, MaterialInput{Kind: kind, Name: name, Note: note, Path: picture(t, name+".jpg", 96, 128, c)})
		must(t, err)
		return m
	}
	taras := add(MaterialCharacter, "Тарас", "", color.RGBA{200, 80, 40, 255})
	maria := add(MaterialCharacter, "Марічка", "дівчинка з косою", color.RGBA{20, 80, 200, 255})
	grove := add(MaterialLocation, "Березовий гай", "", color.RGBA{20, 160, 60, 255})
	add(MaterialObject, "Ліхтар", "", color.RGBA{220, 0, 0, 255})
	add(MaterialStyle, "Стиль", "", color.RGBA{230, 180, 60, 255})

	// The plan waits for the descriptions still being written.
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)
	if looks, _ := mm.count(); looks != 5 {
		t.Fatalf("%d descriptions", looks)
	}
	mm.mu.Lock()
	bible, shots := mm.bibles[0], mm.shots[0]
	mm.mu.Unlock()
	mustContain(t, "bible", bible,
		"The user's own materials — use every one of them:",
		fmt.Sprintf(`- Character "Тарас" (material %q): LOOK character: a 7-year-old boy`, taras.ID),
		fmt.Sprintf(`- Location "Березовий гай" (material %q): LOOK place`, grove.ID),
		`- Prop "Ліхтар": LOOK prop: a small red tin lantern with a brass handle. Show it in the scenes where it fits the story.`,
		`- Visual style sample: LOOK style: thick gouache strokes, warm ochre palette. The whole film follows this style.`,
		`"material": the id of the user's material it is, or ""`)
	mustContain(t, "shots", shots, "Props the user supplied", "- Ліхтар: LOOK prop: a small red tin lantern")

	// Cast: Тарас (bound by id although the model renamed him), Лисиця (the
	// model's own), Марічка (left out by the model → added). Place: the grove.
	if len(p.Characters) != 3 || len(p.Locations) != 1 {
		t.Fatalf("cast %d / %d", len(p.Characters), len(p.Locations))
	}
	byName := map[string]Character{}
	for _, c := range p.Characters {
		byName[c.Name] = c
	}
	tc, fox, mc, loc := byName["Тарас"], byName["Лисиця"], byName["Марічка"], p.Locations[0]
	if tc.MaterialID != taras.ID || tc.Tag != "taras" || !strings.HasPrefix(tc.Description, "LOOK character") || tc.Role != "hero" || tc.Gender != "male" {
		t.Fatalf("Тарас %+v", tc)
	}
	if fox.MaterialID != "" || fox.Image.Status == StatusDone || mc.MaterialID != maria.ID || mc.Description != "LOOK character: a 7-year-old boy, red curly hair, green wool coat, yellow boots." {
		t.Fatalf("Лисиця %+v / Марічка %+v", fox, mc)
	}
	if loc.Name != "Березовий гай" || loc.MaterialID != grove.ID || loc.Description != "LOOK place: a misty birch grove at dawn." {
		t.Fatalf("location %+v", loc)
	}
	// Their pictures are the reference images: done, no generation.
	for _, a := range []struct {
		img Asset
		mat Material
	}{{tc.Image, taras}, {mc.Image, maria}, {loc.Image, grove}} {
		if a.img.Status != StatusDone || !strings.HasPrefix(a.img.Path, "images/") {
			t.Fatalf("reference %+v", a.img)
		}
		x, _ := os.ReadFile(e.Abs(p.ID, a.img.Path))
		y, _ := os.ReadFile(e.Abs(p.ID, a.mat.Path))
		if !bytes.Equal(x, y) {
			t.Fatal("reference is not the material picture")
		}
	}
	// Scenes use them (found under the model's name too).
	for i, s := range p.Scenes {
		if len(s.CharacterIDs) != 2 || s.CharacterIDs[0] != tc.ID || s.CharacterIDs[1] != fox.ID || s.LocationID != loc.ID {
			t.Fatalf("scene %d cast %v at %q", i, s.CharacterIDs, s.LocationID)
		}
	}
	if !strings.Contains(p.StyleGuide, "Style reference: LOOK style: thick gouache strokes, warm ochre palette.") {
		t.Fatalf("style guide %q", p.StyleGuide)
	}

	// A new plan keeps the bindings and the very same reference files.
	must(t, e.Run(p.ID, StagePlan, true, ""))
	q := waitJob(t, e, p.ID)
	mustJobDone(t, q)
	for _, c := range q.Characters {
		if c.MaterialID == taras.ID && c.Image.Path != tc.Image.Path || c.MaterialID == maria.ID && c.Image.Path != mc.Image.Path {
			t.Fatalf("re-plan changed the reference %+v", c)
		}
	}
	if q.Locations[0].Image.Path != loc.Image.Path {
		t.Fatal("re-plan changed the location reference")
	}
	// Edit keeps the binding; the UI cannot bind a new character itself.
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Characters[0].MaterialID = "other"
		p.Characters = append(p.Characters, Character{Name: "Новий", MaterialID: maria.ID})
	}))
	r, _ := e.Get(p.ID)
	if r.Characters[0].MaterialID != q.Characters[0].MaterialID || r.Characters[len(r.Characters)-1].MaterialID != "" {
		t.Fatalf("edit bindings %+v", r.Characters)
	}
	// Removing a material unbinds its character (the picture stays).
	must(t, e.RemoveMaterial(p.ID, maria.ID))
	r, _ = e.Get(p.ID)
	for _, c := range r.Characters {
		if c.Name == "Марічка" && (c.MaterialID != "" || c.Image.Status != StatusDone) {
			t.Fatalf("unbound %+v", c)
		}
	}
}

// TestMaterialsRefs: the refs stage generates only the characters/locations
// without a material (even when forced); regenerating a bound one draws it
// after the user's picture.
func TestMaterialsRefs(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, _ := matEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	must(t, e.Edit(p.ID, func(p *Project) { p.Input.Text = "Історія." }))
	taras, err := e.AddMaterial(p.ID, MaterialInput{Name: "Тарас", Path: picture(t, "t.jpg", 96, 128, color.RGBA{200, 80, 40, 255})})
	must(t, err)
	grove, err := e.AddMaterial(p.ID, MaterialInput{Kind: MaterialLocation, Name: "Березовий гай", Path: picture(t, "g.jpg", 128, 72, color.RGBA{20, 160, 60, 255})})
	must(t, err)
	must(t, e.Run(p.ID, StagePlan, false, ""))
	p = waitJob(t, e, p.ID)
	mustJobDone(t, p)

	for _, force := range []bool{false, true} {
		before := len(env.gl.requests("image"))
		must(t, e.Run(p.ID, StageRefs, force, ""))
		q := waitJob(t, e, p.ID)
		mustJobDone(t, q)
		reqs := env.gl.requests("image")[before:]
		if want := map[bool]int{false: 1, true: 1}[force]; len(reqs) != want || !strings.Contains(reqs[0].prompt, "Лисиця") {
			t.Fatalf("force=%v: %d image requests %+v", force, len(reqs), reqs)
		}
		for _, c := range q.Characters {
			if c.Image.Status != StatusDone {
				t.Fatalf("reference %+v", c)
			}
		}
	}
	// A lost bound picture comes back from the material, still no generation.
	var tc Character
	q, _ := e.Get(p.ID)
	for _, c := range q.Characters {
		if c.MaterialID == taras.ID {
			tc = c
		}
	}
	must(t, os.Remove(e.Abs(p.ID, tc.Image.Path)))
	e.update(e.state(p.ID), func(p *Project) { findCharacter(p, tc.ID).Image = EmptyAsset() })
	before := len(env.gl.requests("image"))
	must(t, e.Run(p.ID, StageRefs, false, ""))
	q = waitJob(t, e, p.ID)
	mustJobDone(t, q)
	if c := findCharacter(&q, tc.ID); len(env.gl.requests("image")) != before || c.Image.Status != StatusDone {
		t.Fatalf("restored %+v (%d requests)", c.Image, len(env.gl.requests("image"))-before)
	}
	fileExists(t, e.Abs(p.ID, findCharacter(&q, tc.ID).Image.Path))

	// Regenerating a bound character on purpose: drawn after the user's picture.
	must(t, e.Regen(p.ID, RegenCharacter, tc.ID, ""))
	waitFor(t, e, p.ID, "regen", func(p Project) bool {
		a := findCharacter(&p, tc.ID).Image
		return a.Status == StatusDone && a.Path != findCharacter(&q, tc.ID).Image.Path
	})
	reqs := env.gl.requests("image")
	last := reqs[len(reqs)-1]
	if strings.Join(last.refNames, ",") != "taras.jpg" || !strings.Contains(last.prompt, "The reference image @taras shows this character") {
		t.Fatalf("regen request %+v", last)
	}
	_ = grove
}

func TestNormalizeMaterials(t *testing.T) {
	p, err := decodeProject([]byte(`{"id":"m","input":{"materials":[{"id":"a","kind":"location","path":"materials/a.jpg"},
		{"id":"","path":"materials/b.jpg"},{"id":"c","kind":"","path":"materials/c.jpg"},{"id":"d","kind":"spaceship","path":"materials/d.jpg"},
		{"id":"e","kind":"style"}]},"characters":[{"id":"x","name":"X","materialId":"a"}]}`))
	must(t, err)
	normalize(p)
	var kinds []string
	for _, m := range p.Input.Materials {
		kinds = append(kinds, m.ID+":"+m.Kind)
	}
	if strings.Join(kinds, ",") != "a:location,c:character,d:object" || p.Characters[0].MaterialID != "a" {
		t.Fatalf("normalized %v %+v", kinds, p.Characters[0])
	}
}

func TestMaterialDescribing(t *testing.T) {
	t.Parallel()
	env, _ := matEnv(t, nil)
	e := env.e
	p, _ := e.Create("")
	m, err := e.AddMaterial(p.ID, MaterialInput{Name: "Тарас", Path: picture(t, "t.jpg", 32, 32, color.RGBA{1, 2, 3, 255})})
	must(t, err)
	waitLook(t, e, p.ID, m.ID)
	waitNoLooks(t, e, p.ID)
	if e.MaterialDescribing(p.ID, m.ID) || e.MaterialDescribing("missing", m.ID) {
		t.Fatal("still describing")
	}
}
