package project

import (
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// materialKind describes one kind of start material.
type materialKind struct {
	id, label  string
	icon       *gui.Icon
	name, note string // placeholders
}

// materialKinds are the kinds in the order of the select.
var materialKinds = []materialKind{
	{studio.MaterialCharacter, "Персонаж", gui.IconUserRound, "Ім'я героя", "Вигляд, одяг, роль в історії…"},
	{studio.MaterialLocation, "Локація", gui.IconMapPin, "Назва місця", "Що тут важливо: час доби, настрій…"},
	{studio.MaterialObject, "Предмет", gui.IconShapes, "Що це за предмет", "Яку роль він грає в історії…"},
	{studio.MaterialStyle, "Стиль", gui.IconPalette, "Назва (необов'язково)", "Що взяти з цього зразка…"},
}

func kindOf(id string) materialKind {
	for _, k := range materialKinds {
		if k.id == id {
			return k
		}
	}
	return materialKinds[0]
}

// materialsCard is step 1's «Матеріали»: the user's own pictures given up
// front — character photos, places, objects, a style sample. The plan keeps
// the characters and places under these names and their photos become the
// references.
type materialsCard struct {
	add    gui.Clickable
	adding bool
	tiles  map[string]*materialUI
	focus  string // a material whose name field takes focus once it shows
}

// materialUI is the widget state of one material tile.
type materialUI struct {
	kind       gui.Select
	name, note bound
	del        gui.Clickable
	img        gui.ImageView
}

func (s *materialsCard) tile(id string) *materialUI {
	if s.tiles == nil {
		s.tiles = map[string]*materialUI{}
	}
	u := s.tiles[id]
	if u == nil {
		u = new(materialUI)
		s.tiles[id] = u
	}
	return u
}

// addPhoto picks a picture and adds it as a material (off the UI goroutine).
func (s *materialsCard) addPhoto(c *core.Ctx, id string) {
	if s.adding {
		return
	}
	c.PickFile("Вибери фото героя, місця, предмета чи зразок стилю", studio.MaterialExts(), func(path string) {
		s.adding = true
		var m studio.Material
		c.Do(func() error {
			var err error
			m, err = c.E.AddMaterial(id, studio.MaterialInput{Kind: studio.MaterialCharacter, Path: path})
			if err != nil {
				c.Post(func() { s.adding = false }) // c.Do shows the error
			}
			return err
		}, func() {
			s.adding = false
			s.focus = m.ID
			c.Toast(gui.ToneSuccess, "Фото додано — напиши, хто це або що це")
		})
	})
}

func (s *materialsCard) layout(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState) D {
	th := c.Th
	id := pv.ID
	if s.add.Clicked(gtx) && len(pv.P.Input.Materials) < maxMaterials {
		if c.Offline {
			c.Toast(gui.ToneError, offlineText)
		} else {
			s.addPhoto(c, id)
		}
	}
	ms := pv.P.Input.Materials
	full := len(ms) >= maxMaterials
	fullTip := ""
	if full {
		fullTip = sprintf("Уже %d фото — це максимум для одного проєкту", maxMaterials)
	}
	addBtn := func(kind gui.ButtonKind) layout.Widget {
		return func(gtx C) D {
			txt := "Додати фото"
			if s.adding {
				txt = "Додаємо…"
			}
			return gui.Button{Kind: kind, Size: gui.Sm, Icon: gui.IconImagePlus, Text: txt, Busy: s.adding, Disabled: full,
				Tip: fullTip}.Layout(gtx, th, &s.add)
		}
	}
	// Narrow cards put «Додати фото» under the grid, full width.
	narrow := gtx.Constraints.Max.X < gtx.Dp(560)
	var trail layout.Widget
	if len(ms) > 0 && !narrow {
		trail = addBtn(gui.Secondary)
	}
	note := "Твої фото героїв, місць, предметів і зразок стилю — особливо для автопілота"
	if len(ms) > 0 {
		note = core.Count(len(ms), "фото", "фото", "фото") + " · " + materialSummary(ms)
	}
	body := func(gtx C) D {
		if len(ms) == 0 {
			return s.empty(gtx, th, addBtn(gui.Secondary), narrow)
		}
		grid := func(gtx C) D {
			return gui.Grid{MinWidth: 340, Gap: gui.Sp12, Stretch: true}.Layout(gtx, len(ms), func(gtx C, i int) D {
				return s.materialTile(gtx, c, pv, ps, ms[i])
			})
		}
		if !narrow {
			return grid(gtx)
		}
		return gui.VStack(gtx, gui.Sp12, grid, func(gtx C) D {
			txt := "Додати ще фото"
			if s.adding {
				txt = "Додаємо…"
			}
			return gui.Button{Kind: gui.Secondary, Icon: gui.IconImagePlus, Text: txt, Busy: s.adding, Full: true, Disabled: full,
				Tip: fullTip}.Layout(gtx, th, &s.add)
		})
	}
	rows := []layout.Widget{cardTitle(th, gui.IconImages, "Матеріали", note, trail)}
	if len(ms) > 0 { // the empty well says it itself
		rows = append(rows, func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 2}.Layout(gtx, gui.IconInfo.Widget(16, th.Pal.Text2)) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, materialsWhy)),
			)
		})
	}
	rows = append(rows, body)
	return gui.Card{}.Layout(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, rows...) })
}

// materialsWhy says what the materials do, in one line.
const materialsWhy = "Персонажі з фото потраплять у план під цими іменами, а фото стане їхнім референсом."

// materialSummary counts the materials by kind ("2 персонажі · 1 локація").
func materialSummary(ms []studio.Material) string {
	n := map[string]int{}
	for _, m := range ms {
		n[m.Kind]++
	}
	var parts []string
	if k := n[studio.MaterialCharacter]; k > 0 {
		parts = append(parts, core.Count(k, "персонаж", "персонажі", "персонажів"))
	}
	if k := n[studio.MaterialLocation]; k > 0 {
		parts = append(parts, core.Count(k, "локація", "локації", "локацій"))
	}
	if k := n[studio.MaterialObject]; k > 0 {
		parts = append(parts, core.Count(k, "предмет", "предмети", "предметів"))
	}
	if k := n[studio.MaterialStyle]; k > 0 {
		parts = append(parts, core.Count(k, "зразок стилю", "зразки стилю", "зразків стилю"))
	}
	return strings.Join(parts, " · ")
}

// empty invites to add the first picture (an inset well inside the card).
func (s *materialsCard) empty(gtx C, th *gui.Theme, add layout.Widget, narrow bool) D {
	return inset(gtx, th, gui.Sp20, func(gtx C) D {
		icon := func(gtx C) D {
			sz := gtx.Dp(48)
			gui.FillCircle(gtx, 48, th.Pal.Card)
			is := gtx.Dp(22)
			o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
			gui.IconImagePlus.Layout(gtx, 22, th.Pal.ActionText)
			o.Pop()
			return D{Size: image.Pt(sz, sz)}
		}
		texts := func(gtx C) D {
			return gui.VStack(gtx, 2,
				gui.Text{Style: th.BodyStrong}.Widget(th, "Є фото героїв чи місць?"),
				gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
					materialsWhy),
			)
		}
		if narrow {
			return gui.VStack(gtx, gui.Sp12,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(icon), layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout), layout.Flexed(1, texts))
				},
				add,
			)
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(icon),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, texts),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(add),
		)
	})
}

// inset is a flat inset well inside a card (Surface fill, hairline, no
// shadow — cards never nest).
func inset(gtx C, th *gui.Theme, pad unit.Dp, w layout.Widget) D {
	p := gtx.Dp(pad)
	cg := gtx
	cg.Constraints.Max.X = max(0, gtx.Constraints.Max.X-2*p)
	cg.Constraints.Min.X = cg.Constraints.Max.X
	cg.Constraints.Min.Y = max(0, gtx.Constraints.Min.Y-2*p)
	cg.Constraints.Max.Y = max(cg.Constraints.Min.Y, gtx.Constraints.Max.Y-2*p)
	m := op.Record(gtx.Ops)
	d := w(cg)
	call := m.Stop()
	size := image.Pt(gtx.Constraints.Max.X, max(d.Size.Y+2*p, gtx.Constraints.Min.Y))
	gui.Box(gtx, image.Rectangle{Max: size}, gui.RadiusInner, th.Pal.Surface, th.Pal.Border)
	o := op.Offset(image.Pt(p, p)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// materialTile is one material: the picture, its kind, the name and note
// the user gives, and the engine's description of the picture.
func (s *materialsCard) materialTile(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, m studio.Material) D {
	th := c.Th
	id, mid := pv.ID, m.ID
	u := s.tile(mid)
	k := kindOf(m.Kind)
	update := func(fn func(m *studio.Material)) { c.Error(c.E.UpdateMaterial(id, mid, fn)) }
	fr := c.Frame()
	u.name.update(gtx, &ps.bind, fr, m.Name, func(v string) { update(func(m *studio.Material) { m.Name = strings.TrimSpace(v) }) })
	u.note.update(gtx, &ps.bind, fr, m.Note, func(v string) { update(func(m *studio.Material) { m.Note = v }) })
	if v, ok := u.kind.Changed(gtx); ok && v != m.Kind {
		update(func(m *studio.Material) { m.Kind = v })
	}
	if u.del.Clicked(gtx) {
		name := firstText(m.Name, strings.ToLower(k.label))
		c.Confirm(core.Confirm{
			Title: "Прибрати «" + name + "» з матеріалів?", Text: "Фото зникне з проєкту; уже створений план не зміниться.",
			Icon: gui.IconTrash2, OK: "Прибрати", Danger: true,
			OnOK: func() { c.Do(func() error { return c.E.RemoveMaterial(id, mid) }, nil) },
		})
	}
	if s.focus == mid {
		s.focus = ""
		u.name.Focus(gtx)
	}
	opts := make([]gui.SelectOption, len(materialKinds))
	for i, mk := range materialKinds {
		opts[i] = gui.SelectOption{Value: mk.id, Label: mk.label, Icon: mk.icon}
	}
	thumbDp := unit.Dp(104)
	if gtx.Constraints.Max.X < gtx.Dp(360) {
		thumbDp = 88
	}
	thumb := func(gtx C) D {
		sz := gtx.Dp(thumbDp)
		gtx.Constraints = layout.Exact(image.Pt(sz, sz))
		t := gui.Thumb{Ratio: 1, Radius: 12, Placeholder: k.icon, Overlay: func(gtx C) D {
			// Remove: a small × on a veil in the photo's corner.
			return layout.NE.Layout(gtx, func(gtx C) D {
				return layout.UniformInset(6).Layout(gtx, func(gtx C) D {
					gui.FillCircle(gtx, 28, th.Pal.Overlay)
					return gui.IconButton{Icon: gui.IconX, Size: 28, IconSize: 16, Color: th.Pal.OverlayInk,
						Tip: "Прибрати з матеріалів"}.Layout(gtx, th, &u.del)
				})
			})
		}}
		if m.Path == "" {
			return t.Layout(gtx, th)
		}
		return u.img.Layout(gtx, th, c.Img, pv.Abs(m.Path), 0, t)
	}
	fields := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D {
				return u.kind.Layout(gtx, th, gui.SelectStyle{Options: opts, Value: k.id})
			},
			func(gtx C) D {
				return u.name.Layout(gtx, th, gui.FieldStyle{Placeholder: k.name, Style: th.BodyStrong})
			},
		)
	}
	look := func(gtx C) D {
		line := func(icon *gui.Icon, txt string) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 2}.Layout(gtx, icon.Widget(14, th.Pal.Text3)) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, txt)),
			)
		}
		note := strings.TrimSpace(u.note.text())
		switch {
		case strings.TrimSpace(m.Look) != "":
			return line(gui.IconEye, m.Look)
		case materialDescribing(c, id, mid):
			// The text model is looking at the picture (Look fills in later).
			return gui.VStack(gtx, 6,
				gui.Text{Style: th.Caption, Color: th.Pal.Text3}.Widget(th, "Описуємо фото…"),
				func(gtx C) D { return shimmerLines(gtx, th, 2) },
			)
		case note != "":
			// The model could not see the picture: the note is its look.
			return line(gui.IconEye, note)
		}
		return line(gui.IconInfo, "Фото не вдалося роздивитися — опиши в нотатці, що на ньому.")
	}
	return inset(gtx, th, gui.Sp12, func(gtx C) D {
		return fillStack(gtx, gui.Sp12,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(thumb),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Flexed(1, fields),
				)
			},
			func(gtx C) D {
				return u.note.Layout(gtx, th, gui.FieldStyle{Placeholder: k.note, MinLines: 2, MaxLines: 4})
			},
			look,
		)
	})
}

// fromPhoto reports whether a character or location of the plan is bound
// to one of the user's materials (Character/Location.MaterialID): its
// picture is the reference.
func fromPhoto(p *studio.Project, materialID string) bool {
	if materialID == "" {
		return false
	}
	for _, m := range p.Input.Materials {
		if m.ID == materialID {
			return true
		}
	}
	return false
}

// materialDescribing reports that the engine is still describing a
// material's picture (a variable so the screenshots can show the state).
var materialDescribing = func(c *core.Ctx, id, materialID string) bool {
	return c.E.MaterialDescribing(id, materialID)
}

// maxMaterials is the engine's limit of materials per project.
const maxMaterials = 24

// shimmerLines are text-line placeholders for a Surface well (the kit's
// skeleton is Surface-coloured itself and would vanish there): Border bars
// with the shared shimmer band.
func shimmerLines(gtx C, th *gui.Theme, n int) D {
	base, hi := th.Pal.Border, th.Pal.Shimmer
	if lum(hi) <= lum(base)+8 { // the evening palette: a lighter band
		hi = th.Pal.BorderStrong
	}
	w := gtx.Constraints.Max.X
	lh, gap := gtx.Dp(12), gtx.Dp(12)
	h := n*lh + max(0, n-1)*gap
	moving := !gui.ReducedMotion()
	var x, band float32
	if moving {
		ph := float32(gtx.Now.UnixNano()%int64(gui.DurShimmer)) / float32(gui.DurShimmer)
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 30)})
		band = float32(max(gtx.Dp(160), w/3))
		x = -band + (float32(w)+2*band)*gui.EaseInOutCubic(ph)
	}
	lo := gui.WithAlpha(hi, 0)
	grad := func(y, lh int, x0, x1 float32, c0, c1 color.NRGBA) {
		st := clip.Rect(image.Rect(int(x0), y, int(math.Ceil(float64(x1))), y+lh)).Push(gtx.Ops)
		paint.LinearGradientOp{Stop1: f32.Pt(x0, 0), Color1: c0, Stop2: f32.Pt(x1, 0), Color2: c1}.Add(gtx.Ops)
		paint.PaintOp{}.Add(gtx.Ops)
		st.Pop()
	}
	for i := 0; i < n; i++ {
		lw := w
		if i == n-1 && n > 1 {
			lw = w * 3 / 5
		}
		y := i * (lh + gap)
		st := clip.UniformRRect(image.Rect(0, y, lw, y+lh), lh/2).Push(gtx.Ops)
		paint.Fill(gtx.Ops, base)
		if moving {
			grad(y, lh, x, x+band/2, lo, hi)
			grad(y, lh, x+band/2, x+band, hi, lo)
		}
		st.Pop()
	}
	return D{Size: image.Pt(w, h)}
}

// lum is a colour's rough brightness (0…765).
func lum(c color.NRGBA) int { return int(c.R) + int(c.G) + int(c.B) }
