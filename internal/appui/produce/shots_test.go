package produce

// Headless screenshots of steps 3 and 4 (and the lightbox) for self-review:
//
//	GUISHOTS=<dir> go test ./internal/appui/produce -run TestShots
//
// The engine is core.DemoEngine on a temp folder, seeded with the web
// studio's projects (real frames, clips and voices); states that need a
// running or failed generation are faked on a copy of the project view, so
// nothing ever reaches a real service.

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

const webRoot = "../../../.."

// fakeView copies a project view deeply enough to change it for one shot.
func fakeView(pv *core.ProjectView, fn func(p *studio.Project)) *core.ProjectView {
	cp := *pv
	cp.P.Characters = append([]studio.Character(nil), pv.P.Characters...)
	cp.P.Locations = append([]studio.Location(nil), pv.P.Locations...)
	cp.P.Scenes = append([]studio.Scene(nil), pv.P.Scenes...)
	if pv.P.Job != nil {
		j := *pv.P.Job
		cp.P.Job = &j
	}
	if fn != nil {
		fn(&cp.P)
	}
	return &cp
}

// shotFrame lays a step out like the workspace does: the main column of
// mainW px and, when side is set, the 360 px context column with the
// step's next-step card and side cards.
func shotFrame(c *core.Ctx, v core.StepView, pv *core.ProjectView, mainW int, side bool) func(gtx C) D {
	var next gui.Clickable
	return func(gtx C) D {
		win := gtx.Constraints.Max
		padX := gtx.Dp(32)
		if win.X < gtx.Dp(500) {
			padX = gtx.Dp(15)
		}
		top := gtx.Dp(24)
		// Like the workspace: a core.Bleeder gets the column widened by its
		// bleed on both sides.
		b := 0
		if bl, ok := v.(core.Bleeder); ok {
			b = gtx.Dp(bl.Bleed())
		}
		mg := gtx
		mg.Constraints = layout.Exact(image.Pt(mainW+2*b, win.Y-top))
		o := op.Offset(image.Pt(padX-b, top)).Push(gtx.Ops)
		v.Layout(mg, c, pv)
		o.Pop()
		if side {
			sw := gtx.Dp(360)
			sg := gtx
			sg.Constraints = layout.Constraints{Min: image.Pt(sw, 0), Max: image.Pt(sw, win.Y-top)}
			o := op.Offset(image.Pt(padX+mainW+gtx.Dp(24), top+gtx.Dp(8))).Push(gtx.Ops)
			n := v.Next(c, pv)
			vstack(sg, gui.Sp24,
				func(gtx C) D {
					if n.Title == "" {
						return D{}
					}
					return gui.NextStepCard{Icon: n.Icon, Title: n.Title, Text: n.Text, Busy: n.Busy}.Layout(gtx, c.Th, &next)
				},
				func(gtx C) D { return v.Side(gtx, c, pv) },
			)
			o.Pop()
		}
		return D{Size: win}
	}
}

type shotEnv struct {
	t   *testing.T
	c   *core.Ctx
	h   *core.HeadlessHost
	out string
}

func (s *shotEnv) shot(name string, w, h int, dark bool, draw func(gtx C) D, opts ...gui.SnapOption) {
	s.t.Helper()
	s.c.Th.SetPaletteNow(dark)
	path := filepath.Join(s.out, name+".png")
	if err := core.Shot(s.c, s.h, w, h, 1, path, draw, opts...); err != nil {
		s.t.Fatalf("%s: %v", name, err)
	}
	s.c.Th.SetPaletteNow(false)
}

// wide renders a step at the 1920×1080 main-column width with the side column.
func (s *shotEnv) wide(name string, v core.StepView, pv *core.ProjectView, dark bool) {
	s.shot(name, 32+1400+24+360+32, 1080, dark, shotFrame(s.c, v, pv, 1400, true))
}

// laptop is the 1440×900 window: the rail leaves 1348 px, the main column
// gets 900 px next to the 360 px context column.
func (s *shotEnv) laptop(name string, v core.StepView, pv *core.ProjectView) {
	s.shot(name, 1440-92, 900, false, shotFrame(s.c, v, pv, 900, true))
}

func (s *shotEnv) mid(name string, v core.StepView, pv *core.ProjectView) {
	s.shot(name, 32+1000+32, 900, false, shotFrame(s.c, v, pv, 1000, false))
}

func (s *shotEnv) narrow(name string, v core.StepView, pv *core.ProjectView) {
	s.shot(name, 390, 844, false, shotFrame(s.c, v, pv, 360, false))
}

// makeBoard composes a 3×3 storyboard sheet from the project's frames
// (white gutters, like the image model draws them) and returns its
// project-relative path.
func makeBoard(t *testing.T, pv *core.ProjectView, sceneIdx int) string {
	t.Helper()
	pw, ph := 400, 225
	if pv.P.Input.Aspect == "9:16" {
		pw, ph = 225, 400
	}
	return makeGrid(t, pv, sceneIdx, 3, 3, pw, ph, fmt.Sprintf("images/test-board-%d.jpg", sceneIdx))
}

// makeGrid composes a rows×cols sheet of pw×ph panels (white gutters) from
// the project's frames into rel.
func makeGrid(t *testing.T, pv *core.ProjectView, sceneIdx, rows, cols, pw, ph int, rel string) string {
	t.Helper()
	var frames []image.Image
	for i := range pv.P.Scenes {
		f := pv.P.Scenes[(sceneIdx+i)%len(pv.P.Scenes)].Frame
		data, err := os.ReadFile(pv.Abs(f.Path))
		if err != nil {
			continue
		}
		img, _, err := media.DecodeImage(data)
		if err == nil {
			frames = append(frames, img)
		}
	}
	if len(frames) == 0 {
		t.Fatal("no frames to build a board from")
	}
	g := 10
	sheet := image.NewRGBA(image.Rect(0, 0, cols*pw+(cols+1)*g, rows*ph+(rows+1)*g))
	draw.Draw(sheet, sheet.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	for k := 0; k < rows*cols; k++ {
		src := frames[k%len(frames)]
		r := image.Rect(g+(k%cols)*(pw+g), g+(k/cols)*(ph+g), g+(k%cols)*(pw+g)+pw, g+(k/cols)*(ph+g)+ph)
		// Nearest-neighbour scale, shifted a little per panel so they differ.
		b := src.Bounds()
		shift := float64(k%cols) * 0.04
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				fx := (float64(x-r.Min.X)/float64(pw))*(0.88) + shift
				fy := (float64(y-r.Min.Y)/float64(ph))*(0.88) + float64((k/cols)%3)*0.04
				sheet.Set(x, y, src.At(b.Min.X+int(fx*float64(b.Dx())), b.Min.Y+int(fy*float64(b.Dy()))))
			}
		}
	}
	f, err := os.Create(pv.Abs(rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, sheet, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return rel
}

func TestShots(t *testing.T) {
	out := os.Getenv("GUISHOTS")
	if out == "" {
		t.Skip("set GUISHOTS=<dir> to render the screenshots")
	}
	if !filepath.IsAbs(out) {
		t.Fatalf("GUISHOTS must be an absolute path, got %q", out)
	}
	e, err := core.DemoEngine(filepath.Join(t.TempDir(), "data"), webRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Shutdown(0)
	c, h := core.NewHeadless(e)
	defer c.Close()
	var wideID, vertID, emptyID string
	for _, s := range c.List() {
		switch {
		case s.Scenes == 0:
			emptyID = s.ID
		case s.Aspect == "9:16":
			vertID = s.ID
		default:
			wideID = s.ID
		}
	}
	if wideID == "" || vertID == "" || emptyID == "" {
		t.Skip("the web studio demo projects are not there")
	}
	env := &shotEnv{t: t, c: c, h: h, out: out}
	only := os.Getenv("GUISHOTS_ONLY")
	want := func(name string) bool { return only == "" || strings.Contains(name, only) }

	pvW := c.Project(wideID)
	pvV := c.Project(vertID)
	pvE := c.Project(emptyID)
	now := time.Now().UnixMilli()

	// ---- step 3 ----
	if want("s3-empty") {
		env.wide("s3-empty-light-1920", NewStoryboard(), pvE, false)
	}
	if want("s3-done") {
		env.wide("s3-done-light-1920", NewStoryboard(), pvW, false)
		env.wide("s3-done-dark-1920", NewStoryboard(), pvW, true)
		env.mid("s3-done-light-1000", NewStoryboard(), pvW)
		env.laptop("s3-done-light-1440", NewStoryboard(), pvW)
		env.laptop("s3-vertical-light-1440", NewStoryboard(), pvV)
		env.narrow("s3-done-light-360", NewStoryboard(), pvW)
	}
	if want("s3-vertical") {
		env.wide("s3-vertical-light-1920", NewStoryboard(), pvV, false)
		env.mid("s3-vertical-light-1000", NewStoryboard(), pvV)
	}
	if want("s3-states") {
		pv := fakeView(pvW, func(p *studio.Project) {
			p.Job = &studio.Job{Kind: studio.JobRefs, Status: studio.JobRunning, Message: "Референси персонажів і локацій · 2/6", StartedAt: now}
			p.Characters[0].Image.Status, p.Characters[0].Image.Progress = studio.StatusRunning, 45
			p.Characters[1].Image.Status = studio.StatusQueued
			p.Locations[1].Image = studio.Asset{Status: studio.StatusError, Error: "G-Labs: ліміт генерацій вичерпано — спробуй за годину"}
			p.Scenes[0].Frame.Stale = true
			p.Scenes[1].Frame.Status = studio.StatusError
			p.Scenes[1].Frame.Error = "Модель відмовилась малювати кадр: у промті забагато деталей. Спрости опис сцени."
			p.Scenes[2].Frame.Status, p.Scenes[2].Frame.Progress = studio.StatusRunning, 60
		})
		env.wide("s3-states-light-1920", NewStoryboard(), pv, false)
		env.wide("s3-states-dark-1920", NewStoryboard(), pv, true)
		env.narrow("s3-states-light-360", NewStoryboard(), pv)
		c.UI(wideID).ScrollTo = pv.P.Scenes[0].ID
		env.wide("s3-states-frames-light-1920", NewStoryboard(), pv, false)
		c.UI(wideID).ScrollTo = pv.P.Scenes[1].ID
		env.narrow("s3-states-frames-light-360", NewStoryboard(), pv)
		pv2 := fakeView(pvW, func(p *studio.Project) {
			p.Job = &studio.Job{Kind: studio.JobFrames, Status: studio.JobError, Error: "Кадрів: 2/3. Перегенеруй сцени з помилками.", FinishedAt: now}
			p.Scenes[2].Frame = studio.Asset{Status: studio.StatusEmpty}
		})
		env.mid("s3-joberror-light-1000", NewStoryboard(), pv2)
	}
	if want("s3-board") || want("s3-lightbox") {
		if err := e.Edit(vertID, func(p *studio.Project) { p.Input.StoryboardMode = studio.StoryboardGrid }); err != nil {
			t.Fatal(err)
		}
		h.Flush()
		c.Refresh()
		time.Sleep(300 * time.Millisecond) // engine notifications are coalesced
		c.Refresh()
		base := c.Project(vertID)
		boards := map[int]string{}
		for i := 0; i < len(base.P.Scenes)-1; i++ {
			boards[i] = makeBoard(t, base, i)
		}
		pv := fakeView(base, func(p *studio.Project) {
			for i, rel := range boards {
				p.Scenes[i].Board = studio.Asset{Path: rel, Status: studio.StatusDone, UpdatedAt: now}
			}
		})
		if want("s3-board") {
			c.UI(vertID).ScrollTo = pv.P.Scenes[0].ID
			env.wide("s3-board-light-1920", NewStoryboard(), pv, false)
			c.UI(vertID).ScrollTo = pv.P.Scenes[0].ID
			env.shot("s3-board-dark-1000", 32+1000+32, 900, true, shotFrame(c, NewStoryboard(), pv, 1000, false))
			c.UI(vertID).ScrollTo = pv.P.Scenes[7].ID
			env.narrow("s3-board-light-360", NewStoryboard(), pv)
		}
		if want("s3-lightbox") {
			v := newStoryboard()
			v.openFrames(pv, pv.P.Scenes[1].ID)
			env.wide("s3-lightbox-board-1920", v, pv, false)
			v2 := newStoryboard()
			v2.openFrames(pvW, pvW.P.Scenes[0].ID)
			env.wide("s3-lightbox-frame-1920", v2, pvW, true)
			v3 := newStoryboard()
			v3.openRefs(pvW, pvW.P.Characters[1].ID)
			env.narrow("s3-lightbox-refs-360", v3, pvW)
			v4 := newStoryboard()
			v4.openFrames(pv, pv.P.Scenes[0].ID)
			v4.lb.ZoomTo(2.2)
			env.mid("s3-lightbox-zoom-1000", v4, pv)
		}
		if err := e.Edit(vertID, func(p *studio.Project) { p.Input.StoryboardMode = "" }); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		c.Refresh()
	}
	if want("s3-list") {
		c.UI(wideID).List = true
		env.wide("s3-list-light-1920", NewStoryboard(), pvW, false)
		c.UI(wideID).List = false
	}
	if want("s3-edit") {
		v := newStoryboard()
		pv := c.Project(wideID)
		st := v.state(pv.ID)
		fc := st.frame(pv.P.Scenes[0].ID)
		fc.editing = true
		fc.field.SetText(pv.P.Scenes[0].ImagePrompt)
		c.UI(wideID).ScrollTo = pv.P.Scenes[0].ID
		env.wide("s3-edit-light-1920", v, pv, false)
		v2 := newStoryboard()
		ch := pv.P.Characters[0]
		v2.desc.Open(pv.ID, studio.RegenCharacter, ch.ID, ch.Name, ch.Description)
		env.mid("s3-desc-dialog-1000", v2, pv)
	}
	if want("s3-scrollto") {
		v := newStoryboard()
		pv := c.Project(vertID)
		c.UI(vertID).ScrollTo = pv.P.Scenes[6].ID
		env.wide("s3-scrollto-light-1920", v, pv, false)
	}

	// ---- 9:16 storyboards: panels as the sheet really has them ----
	if want("s3-grid916") {
		if err := e.Edit(vertID, func(p *studio.Project) { p.Input.StoryboardMode = studio.StoryboardGrid }); err != nil {
			t.Fatal(err)
		}
		h.Flush()
		c.Refresh()
		time.Sleep(300 * time.Millisecond)
		c.Refresh()
		base := c.Project(vertID)
		good := makeBoard(t, base, 0)
		bad := makeGrid(t, base, 1, 5, 2, 225, 160, "images/test-board-2x5.jpg")
		pv := fakeView(base, func(p *studio.Project) {
			p.Scenes[0].Board = studio.Asset{Path: good, Status: studio.StatusDone, UpdatedAt: now}
			p.Scenes[1].Board = studio.Asset{Path: bad, Status: studio.StatusDone, UpdatedAt: now}
			p.Scenes[2].Board = studio.Asset{Status: studio.StatusError, UpdatedAt: now,
				Error: "Модель двічі намалювала розкадровку не сіткою 3×3 (сітка 2×5 замість 3×3) — намальовано один кадр, кліп зніметься з нього."}
		})
		v := newStoryboard()
		v.openFrames(pv, pv.P.Scenes[0].ID)
		env.wide("s3-grid916-lightbox-1920", v, pv, false)
		v2 := newStoryboard()
		v2.openFrames(pv, pv.P.Scenes[1].ID)
		env.wide("s3-grid916-lightbox-2x5-1920", v2, pv, true)
		c.UI(vertID).ScrollTo = pv.P.Scenes[0].ID
		env.wide("s3-grid916-cards-1920", NewStoryboard(), pv, false)
		if err := e.Edit(vertID, func(p *studio.Project) { p.Input.StoryboardMode = "" }); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		c.Refresh()
	}
	// ---- automatic prompt rewrites ----
	rewrite := &studio.PromptRewrite{
		Original: "Medium shot of Taylor Swift holding a rifle on a burning street, @taras hiding behind her, Nike logo on her jacket.",
		Prompt:   "Medium shot of a fictional young pop singer holding a flashlight on a smoky street at dusk, @taras hiding behind her, plain jacket.",
		Reason:   "G-Labs відхилив запит: змінити промпт або референси — фільтр безпеки Google (PROMINENT_PERSON)",
		Count:    1, At: now,
	}
	if want("s3-rewrite") {
		rw2 := *rewrite
		rw2.Count = 2
		pv := fakeView(pvW, func(p *studio.Project) {
			p.Scenes[0].Frame.Rewrite = rewrite
			p.Scenes[1].Frame.Rewrite = &rw2
		})
		c.UI(wideID).ScrollTo = pv.P.Scenes[0].ID
		env.wide("s3-rewrite-light-1920", NewStoryboard(), pv, false)
		v := newStoryboard()
		v.state(pv.ID).frame(pv.P.Scenes[0].ID).rw.open = true
		c.UI(wideID).ScrollTo = pv.P.Scenes[0].ID
		env.mid("s3-rewrite-open-1000", v, pv)
		c.UI(wideID).ScrollTo = pv.P.Scenes[0].ID
		env.shot("s3-rewrite-dark-360", 390, 844, true, shotFrame(c, NewStoryboard(), pv, 360, false))
	}
	// ---- production modes ----
	if want("s3-norefs") {
		pv := fakeView(pvW, func(p *studio.Project) { p.Input.Pipeline = studio.PipelineNoRefs })
		env.wide("s3-norefs-light-1920", NewStoryboard(), pv, false)
	}
	if want("s4-stills") {
		pv := fakeView(pvV, func(p *studio.Project) {
			p.Input.Pipeline = studio.PipelineNoRefsStills
			for i := range p.Scenes {
				p.Scenes[i].Video = studio.Asset{Status: studio.StatusEmpty}
			}
		})
		env.wide("s4-stills-light-1920", NewRender(), pv, false)
		pv2 := fakeView(pvV, func(p *studio.Project) { p.Scenes[0].Video.Rewrite = rewrite })
		c.UI(vertID).ScrollTo = pv2.P.Scenes[0].ID
		env.wide("s4-rewrite-light-1920", NewRender(), pv2, false)
	}

	// ---- step 4 ----
	if want("s4-done") {
		env.wide("s4-done-light-1920", NewRender(), pvW, false)
		env.wide("s4-done-dark-1920", NewRender(), pvW, true)
		env.mid("s4-done-light-1000", NewRender(), pvW)
		env.laptop("s4-done-light-1440", NewRender(), pvW)
		env.laptop("s4-vertical-light-1440", NewRender(), pvV)
		env.narrow("s4-done-light-360", NewRender(), pvW)
	}
	if want("s4-vertical") {
		env.wide("s4-vertical-light-1920", NewRender(), pvV, false)
		env.mid("s4-vertical-light-1000", NewRender(), pvV)
	}
	if want("s4-states") {
		pv := fakeView(pvV, func(p *studio.Project) {
			p.Job = &studio.Job{Kind: studio.JobRender, Status: studio.JobRunning, Progress: 35, Message: "Відео й озвучка · 3/8", StartedAt: now}
			p.Scenes[0].Video.Status, p.Scenes[0].Video.Progress = studio.StatusRunning, 62
			p.Scenes[1].Audio.Status = studio.StatusError
			p.Scenes[1].Audio.Error = "ElevenLabs: закінчились символи на акаунті. Поповни баланс або обери інший голос."
			p.Scenes[2].Video.Stale = true
			p.Scenes[3].Audio.Stale = true
			p.Scenes[4].Video = studio.Asset{Status: studio.StatusError, Error: "G-Labs відхилив запит: змінити промпт або референси — фільтр безпеки Google (PROMINENT_PERSON)"}
			p.Scenes[5].Video = studio.Asset{Status: studio.StatusEmpty}
			p.Scenes[6].Frame = studio.Asset{Status: studio.StatusEmpty}
			p.Scenes[6].Video = studio.Asset{Status: studio.StatusEmpty}
		})
		env.wide("s4-states-light-1920", NewRender(), pv, false)
		env.wide("s4-states-dark-1920", NewRender(), pv, true)
		env.narrow("s4-states-light-360", NewRender(), pv)
		c.UI(vertID).ScrollTo = pv.P.Scenes[0].ID
		env.wide("s4-states-cards-light-1920", NewRender(), pv, false)
		c.UI(vertID).ScrollTo = pv.P.Scenes[4].ID
		env.wide("s4-states-cards2-light-1920", NewRender(), pv, false)
		c.UI(vertID).ScrollTo = pv.P.Scenes[1].ID
		env.narrow("s4-states-cards-light-360", NewRender(), pv)
	}
	if want("s4-upload") {
		orig := e.Config().Get().Video
		_ = e.Config().Update(func(cfg *config.Config) {
			cfg.Video.Provider, cfg.Video.GlabsModel, cfg.Video.Length = "glabs", "omni_flash", 10
		})
		pv := fakeView(pvW, func(p *studio.Project) {
			p.Input.ChainFrames = true
			p.Input.Mode = studio.ModeVoice
			at := 0.0
			for i := range p.Scenes {
				s := &p.Scenes[i]
				s.AudioSource = studio.AudioUpload
				s.VoiceStart, s.VoiceEnd = at, at+s.Audio.Duration+0.4
				at = s.VoiceEnd + 0.3
			}
			p.Scenes[1].Audio.Duration = 9.4
		})
		env.wide("s4-upload-chain-light-1920", NewRender(), pv, false)
		_ = e.Config().Update(func(cfg *config.Config) { cfg.Video = orig })
	}
	if want("s4-list") {
		c.UI(vertID).List = true
		env.wide("s4-list-light-1920", NewRender(), pvV, false)
		c.UI(vertID).List = false
	}
	if want("s4-empty") {
		env.mid("s4-empty-light-1000", NewRender(), pvE)
	}
	if want("side-wide") {
		// Under 1440 dp the workspace puts the context cards under the main
		// column: they flow into a row.
		pv := fakeView(pvW, func(p *studio.Project) {
			p.Scenes[0].Frame.Stale = true
			p.Scenes[1].Video = studio.Asset{Status: studio.StatusError, Error: "x"}
			p.Input.ChainFrames = true
		})
		for _, v := range []struct {
			name string
			v    core.StepView
		}{{"s3", NewStoryboard()}, {"s4", NewRender()}} {
			sv := v.v
			env.shot(v.name+"-side-wide-1000", 1064, 420, false, func(gtx C) D {
				gtx.Constraints = layout.Exact(image.Pt(1000, 380))
				o := op.Offset(image.Pt(32, 24)).Push(gtx.Ops)
				sv.Side(gtx, c, pv)
				o.Pop()
				return D{Size: image.Pt(1064, 420)}
			})
		}
	}
}
