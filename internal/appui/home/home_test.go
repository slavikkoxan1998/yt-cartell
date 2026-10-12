package home

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// testCtx is an offline context on a fresh demo engine whose services point
// at a closed local port (nothing can reach a real or the user's service).
func testCtx(t *testing.T) (*core.Ctx, *core.HeadlessHost, *studio.Engine) {
	t.Helper()
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), "")
	if err != nil {
		t.Fatal(err)
	}
	_ = e.Config().Update(func(c *config.Config) {
		c.Text.BaseURL = "http://127.0.0.1:1/v1"
		c.Glabs.BaseURL = "http://127.0.0.1:1"
		c.TTS.APIKey = ""
		c.Google.Keys = nil
	})
	t.Cleanup(func() { _ = e.Shutdown(2 * time.Second) })
	c, h := core.NewHeadless(e)
	t.Cleanup(c.Close)
	return c, h, e
}

// draw lays fn out n times without a GPU, the way the shell does.
func draw(c *core.Ctx, h *core.HeadlessHost, w, hgt, n int, fn func(gtx C) D) {
	var router input.Router
	var ops op.Ops
	for i := 0; i < n; i++ {
		ops.Reset()
		gtx := layout.Context{
			Ops:         &ops,
			Now:         gui.SnapshotEpoch.Add(time.Duration(i) * 16 * time.Millisecond),
			Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
			Constraints: layout.Exact(image.Pt(w, hgt)),
			Source:      router.Source(),
		}
		core.Frame(c, h, fn)(gtx)
		router.Frame(&ops)
	}
}

func TestTitleFrom(t *testing.T) {
	cases := map[string]string{
		"":    "",
		"   ": "",
		"жила-була руда лисиця. Одного разу…": "Жила-була руда лисиця",
		"Лисиця і місяць":                     "Лисиця і місяць",
		"Перший рядок\nдругий рядок":          "Перший рядок",
		"«Стежка додому» — казка про осінь, лисицю, хлопчика і дорогу назад": "«Стежка додому» — казка про осінь",
		"Що буде, якщо Місяць зникне?":                                       "Що буде, якщо Місяць зникне",
		strings.Repeat("дуже", 30): "Дуже" + strings.Repeat("дуже", 11),
	}
	for in, want := range cases {
		if got := TitleFrom(in); got != want {
			t.Errorf("TitleFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeText(t *testing.T) {
	if got := decodeText([]byte("\xEF\xBB\xBFПривіт\r\nсвіт")); got != "Привіт\nсвіт" {
		t.Errorf("utf-8 bom: %q", got)
	}
	u16 := []byte{0xFF, 0xFE, 0x1F, 0x04, 0x40, 0x04, 0x38, 0x04} // "При" UTF-16LE
	if got := decodeText(u16); got != "При" {
		t.Errorf("utf-16: %q", got)
	}
	cp := []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xB3, 0xF2} // "Привіт" in Windows-1251
	if got := decodeText(cp); got != "Привіт" {
		t.Errorf("cp1251: %q", got)
	}
}

func TestAutopilotNeedsAStory(t *testing.T) {
	c, _, e := testCtx(t)
	p := New()
	before := len(e.List())
	p.autopilot(c)
	if len(e.List()) != before {
		t.Fatal("a project was created without a story")
	}
	if p.hero.err == "" || !p.hero.focus {
		t.Fatalf("no friendly hint: err=%q focus=%v", p.hero.err, p.hero.focus)
	}
}

// needCreate skips a test while Engine.CreateProject is still the stub of
// its in-progress implementation.
func needCreate(t *testing.T, e *studio.Engine) {
	t.Helper()
	if _, err := e.CreateProject(studio.NewProjectOptions{Title: "Перевірка"}); err != nil && strings.Contains(err.Error(), "в роботі") {
		t.Skip("Engine.CreateProject is not implemented yet: " + err.Error())
	}
}

// writePNG writes a small real picture and returns its path.
func writePNG(t *testing.T, name string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStepByStepCreatesProject(t *testing.T) {
	c, h, e := testCtx(t)
	needCreate(t, e)
	p := New()
	p.hero.story.SetText("Руда лисиця щоночі говорила з місяцем. Одного разу він зник.")
	p.hero.langCode, p.hero.langSet = "en", true
	p.hero.secs, p.hero.secsSet = 90, true
	p.AddMaterial(studio.MaterialInput{Path: writePNG(t, "fox.png"), Name: "Лисиця", Note: "Руда, з білим кінчиком хвоста"})
	p.stepByStep(c)
	h.Flush()
	r := h.Current()
	if r.Page != core.PageProject || r.Step != 1 || r.ProjectID == "" {
		t.Fatalf("route = %+v", r)
	}
	pr, ok := e.Get(r.ProjectID)
	if !ok {
		t.Fatal("project not created")
	}
	if pr.Title != "Руда лисиця щоночі говорила з місяцем" || pr.Input.Language != "en" || pr.Input.TargetSeconds != 90 ||
		!strings.HasPrefix(pr.Input.Text, "Руда лисиця") {
		t.Fatalf("project = %q %q %d %q", pr.Title, pr.Input.Language, pr.Input.TargetSeconds, pr.Input.Text)
	}
	if len(pr.Input.Materials) != 1 || pr.Input.Materials[0].Name != "Лисиця" || pr.Input.Materials[0].Kind != studio.MaterialCharacter {
		t.Fatalf("materials = %+v", pr.Input.Materials)
	}
	if p.hero.story.Text() != "" || len(p.hero.mats) != 0 || p.hero.creating {
		t.Fatal("the card was not cleared")
	}
}

func TestProjectOptions(t *testing.T) {
	c, _, e := testCtx(t)
	p := New()
	h := &p.hero
	p.AddMaterial(studio.MaterialInput{Path: "C:/фото/Тарас_у_полі.jpg"})
	p.AddMaterial(studio.MaterialInput{Path: "C:/фото/IMG_2041.png", Kind: studio.MaterialLocation, Name: "  Хата  ", Note: " Біла "})
	h.picked = true // «Типові налаштування»
	h.langCode, h.langSet = "de", true
	h.secs, h.secsSet = 45, true
	o := h.project(c, "Історія", true)
	if o.Title != "Історія" || o.Text != "Історія" || o.ProfileID != "" || !o.NoProfile || o.Language != "de" || !o.LanguageSet ||
		o.TargetSeconds != 45 || o.Mode != studio.ModeStory || !o.Autopilot {
		t.Fatalf("options = %+v", o)
	}
	if len(o.Materials) != 2 || o.Materials[0].Name != "Тарас у полі" || o.Materials[0].Kind != studio.MaterialCharacter ||
		o.Materials[1].Name != "Хата" || o.Materials[1].Note != "Біла" || o.Materials[1].Kind != studio.MaterialLocation {
		t.Fatalf("materials = %+v", o.Materials)
	}
	// «Мова: авто» over a profile that has a language.
	pr, err := e.SaveProfile(config.Profile{Name: "English", Language: "en", TargetSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	h.profileID, h.langCode = pr.ID, ""
	o = h.project(c, "Історія", false)
	if o.ProfileID != pr.ID || o.NoProfile || o.Language != "" || !o.LanguageSet || o.Autopilot {
		t.Fatalf("auto language over a profile = %+v", o)
	}
	// Nothing picked: the profile's own language and length stay.
	h.langSet, h.secsSet = false, false
	if o = h.project(c, "Історія", false); o.LanguageSet || o.TargetSeconds != 0 {
		t.Fatalf("unpicked language and length = %+v", o)
	}
}

func TestCreateFromTheCard(t *testing.T) {
	c, _, e := testCtx(t)
	needCreate(t, e)
	pr, err := e.SaveProfile(config.Profile{Name: "English", Language: "en", TargetSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	h := &p.hero
	// «Мова: авто» over the profile's English.
	h.profileID, h.picked = pr.ID, true
	h.langCode, h.langSet = "", true
	h.secs, h.secsSet = 45, true
	got, err := e.CreateProject(h.project(c, "Історія про кота, який шукав дім.", false))
	if err != nil {
		t.Fatal(err)
	}
	if got.Input.Language != "" || got.Input.TargetSeconds != 45 || got.Input.Mode != studio.ModeStory {
		t.Fatalf("auto language: %+v", got.Input)
	}
	// «Типові налаштування» picked although the autopilot default is English.
	_ = e.Config().Update(func(cfg *config.Config) { cfg.Autopilot.DefaultProfileID = pr.ID })
	h.profileID, h.picked = "", true
	h.langSet, h.secsSet = false, false
	got, err = e.CreateProject(h.project(c, "Історія про кота, який шукав дім.", true))
	if got.ID == "" {
		t.Fatalf("no project: %v", err)
	}
	e.Cancel(got.ID)
	if got.Input.Language == "en" || got.ProfileID == pr.ID {
		t.Fatalf("the default profile was applied: %q %q", got.Input.Language, got.ProfileID)
	}
}

func TestClipRange(t *testing.T) {
	cases := []struct {
		ls             []int
		settings       int
		wantLo, wantHi int
	}{
		{[]int{4, 6, 8}, 8, 4, 8},
		{[]int{4, 6, 8}, 6, 4, 6},
		{[]int{8}, 8, 8, 8},
		{[]int{5, 10}, 3, 5, 5},
		{nil, 8, 8, 8},
	}
	for _, tc := range cases {
		if lo, hi := clipRange(tc.ls, tc.settings); lo != tc.wantLo || hi != tc.wantHi {
			t.Errorf("clipRange(%v, %d) = %d–%d, want %d–%d", tc.ls, tc.settings, lo, hi, tc.wantLo, tc.wantHi)
		}
	}
}

func TestMaterialHelpers(t *testing.T) {
	names := map[string]string{
		"C:/a/Тарас_у_полі.jpg":                        "Тарас у полі",
		"C:/a/taras.png":                               "Taras",
		"C:/a/IMG_2041.jpg":                            "",
		"C:/a/WhatsApp Image 2024-05-01 at 10.32.jpeg": "",
		"C:/a/Знімок екрана 2025-01-02.png":            "",
		"": "",
	}
	for in, want := range names {
		if got := nameFromFile(in); got != want {
			t.Errorf("nameFromFile(%q) = %q, want %q", in, got, want)
		}
	}
	if err := checkPicture(writePNG(t, "ok.png")); err != nil {
		t.Fatalf("a png is refused: %v", err)
	}
	if err := checkPicture(filepath.Join(t.TempDir(), "missing.jpg")); err == nil {
		t.Fatal("a missing file passes")
	}
	txt := filepath.Join(t.TempDir(), "story.txt")
	_ = os.WriteFile(txt, []byte("hi"), 0o644)
	if err := checkPicture(txt); err == nil {
		t.Fatal("a text file passes")
	}
}

func TestMaterialsRowAndEditor(t *testing.T) {
	c, h, _ := testCtx(t)
	p := New()
	for _, n := range []string{"Тарас", "Ліс"} {
		p.AddMaterial(studio.MaterialInput{Path: writePNG(t, n+".png"), Name: n})
	}
	home := func(gtx C) D { return p.Home(gtx, c) }
	for _, sz := range [][2]int{{1920, 1080}, {390, 844}} {
		draw(c, h, sz[0], sz[1], 2, home)
	}
	// The editor opens on a chip and closes with «Готово».
	p.EditMaterial(1)
	draw(c, h, 1440, 900, 3, home)
	if p.hero.edit.x != p.hero.mats[1] {
		t.Fatal("the editor is not open on the material")
	}
	p.hero.edit.done.Click()
	draw(c, h, 1440, 900, 1, home)
	if p.hero.edit.x != nil {
		t.Fatal("«Готово» did not close the editor")
	}
	// × removes a picture; «Прибрати» in the editor too.
	first := p.hero.mats[0]
	first.del.Click()
	draw(c, h, 1440, 900, 1, home)
	if len(p.hero.mats) != 1 || p.hero.mats[0].in.Name != "Ліс" {
		t.Fatalf("× left %d materials", len(p.hero.mats))
	}
	p.EditMaterial(0)
	draw(c, h, 1440, 900, 3, home)
	p.hero.edit.remove.Click()
	draw(c, h, 1440, 900, 1, home)
	if len(p.hero.mats) != 0 || p.hero.edit.x != nil {
		t.Fatalf("«Прибрати» left %d materials (editor open: %v)", len(p.hero.mats), p.hero.edit.x != nil)
	}
}

func TestCardInfoAndFilters(t *testing.T) {
	c, _, e := testCtx(t)
	p, err := e.Create("Чернетка")
	if err != nil {
		t.Fatal(err)
	}
	var s studio.Summary
	for _, x := range c.List() {
		if x.ID == p.ID {
			s = x
		}
	}
	in := info(c, s)
	if in.status != stDraft || in.pill.Text != "Чернетка" || in.title != "Чернетка" {
		t.Fatalf("info = %+v", in)
	}
	if !matches(filterAll, stDraft) || !matches(filterWork, stDraft) || matches(filterDone, stDraft) || matches(filterErr, stDraft) {
		t.Fatal("filters")
	}
	if got := search([]studio.Summary{{Title: "Лисиця і місяць"}, {Title: "Кава"}}, "ЛИС"); len(got) != 1 {
		t.Fatalf("search: %v", got)
	}
}

func TestPagesLayOut(t *testing.T) {
	c, h, e := testCtx(t)
	for i := 0; i < 3; i++ {
		if _, err := e.Create("Проєкт"); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	for _, sz := range [][2]int{{1920, 1080}, {1100, 700}, {390, 844}} {
		draw(c, h, sz[0], sz[1], 3, func(gtx C) D { return p.Home(gtx, c) })
		draw(c, h, sz[0], sz[1], 3, func(gtx C) D { return p.Projects(gtx, c) })
	}
	// The ⋯ menu opens and a pick runs its action (here: open the project).
	id := c.List()[0].ID
	p.OpenMenu(id)
	p.pop.pickID, p.pop.pickVal, p.pop.picked = "menu:"+id, "open", true
	draw(c, h, 1440, 900, 1, func(gtx C) D { return p.Home(gtx, c) })
	if r := h.Current(); r.Page != core.PageProject || r.ProjectID != id {
		t.Fatalf("menu open: route %+v", r)
	}
}
