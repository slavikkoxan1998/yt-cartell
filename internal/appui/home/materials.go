package home

// «Матеріали» on the story card: the user's own pictures for the next film
// — character photos, places, objects, a style sample — picked before the
// project exists and handed to Engine.CreateProject, so the plan, the
// references and the autopilot work from them from the very first step.

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

const (
	maxMaterials  = 12       // pictures the card takes (the project takes more)
	maxMaterialSz = 40 << 20 // bytes (the engine's limit)
	matNameLen    = 60       // runes
	matNoteLen    = 400      // runes
)

// material is a picture picked for the next project, with its widget state.
type material struct {
	in   studio.MaterialInput
	key  int // stable per card (anchor, editor)
	open gui.Clickable
	del  gui.Clickable
	img  gui.ImageView
	hov  gui.Anim
}

// matKinds are the material kinds as the editor offers them.
var matKinds = []gui.SegOption{
	{Value: studio.MaterialCharacter, Label: "Персонаж"},
	{Value: studio.MaterialLocation, Label: "Локація"},
	{Value: studio.MaterialObject, Label: "Предмет"},
	{Value: studio.MaterialStyle, Label: "Стиль"},
}

func kindLabel(kind string) string {
	for _, k := range matKinds {
		if k.Value == kind {
			return k.Label
		}
	}
	return "Персонаж"
}

func kindIcon(kind string) *gui.Icon {
	switch kind {
	case studio.MaterialLocation:
		return gui.IconMapPin
	case studio.MaterialObject:
		return gui.IconShapes
	case studio.MaterialStyle:
		return gui.IconPalette
	}
	return gui.IconUserRound
}

func matAnchor(key int) string { return fmt.Sprintf("home.mat.%d", key) }

// AddMaterial puts a picture on the story card, as «Додати фото героїв»
// does after the file picker (headless screenshots seed the row with it).
// An empty kind is a character; an empty name is guessed from the file.
func (p *Page) AddMaterial(m studio.MaterialInput) { p.hero.add(m) }

// EditMaterial opens the editor of the i-th material (screenshots).
func (p *Page) EditMaterial(i int) {
	if i >= 0 && i < len(p.hero.mats) {
		p.hero.edit.open(p.hero.mats[i])
	}
}

func (h *hero) add(m studio.MaterialInput) *material {
	if m.Kind == "" {
		m.Kind = studio.MaterialCharacter
	}
	if strings.TrimSpace(m.Name) == "" {
		m.Name = nameFromFile(m.Path)
	}
	h.matSeq++
	x := &material{in: m, key: h.matSeq}
	h.mats = append(h.mats, x)
	return x
}

func (h *hero) remove(x *material) {
	for i, y := range h.mats {
		if y == x {
			h.mats = append(h.mats[:i:i], h.mats[i+1:]...)
			break
		}
	}
	if h.edit.x == x {
		h.edit.close()
	}
}

// inputs are the materials for Engine.CreateProject.
func (h *hero) inputs() []studio.MaterialInput {
	if len(h.mats) == 0 {
		return nil
	}
	out := make([]studio.MaterialInput, len(h.mats))
	for i, x := range h.mats {
		out[i] = x.in
		out[i].Name, out[i].Note = strings.TrimSpace(x.in.Name), strings.TrimSpace(x.in.Note)
	}
	return out
}

// pickMaterial asks for a picture and adds it, opening its editor so it can
// be named right away.
func (p *Page) pickMaterial(c *core.Ctx) {
	h := &p.hero
	if len(h.mats) >= maxMaterials {
		c.Toast(gui.ToneInfo, fmt.Sprintf("Тут поміститься %d фото — решту додаси в самому проєкті.", maxMaterials))
		return
	}
	c.PickFile("Вибери фото героя, місця чи стилю", studio.MaterialExts(), func(path string) {
		for _, x := range h.mats {
			if strings.EqualFold(filepath.Clean(x.in.Path), filepath.Clean(path)) {
				c.Toast(gui.ToneInfo, "Це фото вже додано")
				h.edit.open(x)
				return
			}
		}
		if err := checkPicture(path); err != nil {
			c.Error(err)
			return
		}
		h.edit.open(h.add(studio.MaterialInput{Path: path}))
	})
}

// checkPicture refuses what the engine would refuse anyway, before the
// story is sent.
func checkPicture(path string) error {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	ok := false
	for _, e := range studio.MaterialExts() {
		ok = ok || strings.TrimPrefix(strings.ToLower(e), ".") == ext
	}
	if !ok {
		return errors.New("Підходять фото PNG, JPG або WEBP")
	}
	st, err := os.Stat(path)
	switch {
	case err != nil || st.IsDir():
		return errors.New("Не вдалося відкрити фото — спробуй інше")
	case st.Size() > maxMaterialSz:
		return errors.New("Фото завелике: до 40 МБ")
	}
	return nil
}

// nameFromFile guesses a name from a picture's file name ("Тарас_у_полі.jpg"
// → "Тарас у полі"); camera names like IMG_2041 give none.
func nameFromFile(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if path == "" || base == "." {
		return ""
	}
	base = strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == '.' || r == '(' || r == ')' {
			return ' '
		}
		return r
	}, base)
	skip := map[string]bool{"img": true, "dsc": true, "dscn": true, "pxl": true, "screenshot": true, "photo": true,
		"image": true, "picture": true, "scan": true, "whatsapp": true, "at": true, "copy": true, "фото": true,
		"знімок": true, "екрана": true, "зображення": true, "копія": true}
	var keep []string
	for _, w := range strings.Fields(base) {
		letters := false
		for _, r := range w {
			letters = letters || unicode.IsLetter(r)
		}
		if letters && !skip[strings.ToLower(w)] {
			keep = append(keep, w)
		}
	}
	name := clipRunes(strings.Join(keep, " "), 40)
	if name == "" {
		return ""
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ---- the row -----------------------------------------------------------------------------------------

// materialsRow is «Матеріали:» — the picked pictures as small name chips (a
// tap opens the editor, × removes one) and «Додати фото героїв»; with none
// yet, a line on what they are for.
func (p *Page) materialsRow(gtx C, c *core.Ctx) D {
	th := c.Th
	h := &p.hero
	if h.addMat.Clicked(gtx) {
		p.pickMaterial(c)
	}
	for _, x := range append([]*material(nil), h.mats...) {
		if x.del.Clicked(gtx) {
			h.remove(x)
			continue
		}
		if x.open.Clicked(gtx) {
			h.edit.open(x)
		}
	}
	title := gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Матеріали:")
	// Narrow cards put the title on a line of its own, so the chips get it all.
	stacked := gtx.Constraints.Max.X < gtx.Dp(480)
	var ws []layout.Widget
	if !stacked {
		ws = append(ws, title)
	}
	for _, x := range h.mats {
		x := x
		ws = append(ws, func(gtx C) D {
			return gui.Anchor(gtx, th, matAnchor(x.key), func(gtx C) D { return h.matChip(gtx, c, x) })
		})
	}
	label := "Додати фото героїв"
	if len(h.mats) > 0 {
		label = "Додати ще"
	}
	ws = append(ws, func(gtx C) D {
		return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconImagePlus, Text: label, Disabled: h.creating,
			Tip: "Фото героїв, місць, предметів чи зразок стилю — студія триматиметься їх від першого кадру"}.Layout(gtx, th, &h.addMat)
	})
	if len(h.mats) == 0 {
		ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 2}.Widget(th,
			"Свої фото героїв чи місць — і студія збереже їхній вигляд від першого кадру."))
	}
	if stacked {
		return gui.VStack(gtx, gui.Sp8, title, func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...) })
	}
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
}

// matChip is one picked picture: its round thumbnail with the kind's badge,
// its name (or kind) and ×.
func (h *hero) matChip(gtx C, c *core.Ctx, x *material) D {
	th := c.Th
	x.hov.Duration = gui.DurHover
	hv := x.hov.Animate(gtx, b2f(x.open.Hovered()))
	if x.open.Hovered() {
		th.Tooltip(gtx, &x.open, x.open.Pressed(), "Змінити ім'я, тип чи опис")
	}
	if x.del.Hovered() {
		th.Tooltip(gtx, &x.del, x.del.Pressed(), "Прибрати фото")
	}
	hgt, pic, del := gtx.Dp(40), gtx.Dp(32), gtx.Dp(28)
	in := (hgt - pic) / 2
	name, ink := strings.TrimSpace(x.in.Name), th.Pal.Text
	if name == "" {
		name, ink = kindLabel(x.in.Kind), th.Pal.Text2
	}
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(gtx.Dp(150), hgt)}
	ld, lc := record(lg, gui.Text{Style: th.Label, Color: ink, MaxLines: 1}.Widget(th, name))
	tx := in + pic + gtx.Dp(8)
	openW := tx + ld.Size.X + gtx.Dp(2)
	w := openW + del + gtx.Dp(4)
	r := image.Rect(0, 0, w, hgt)
	gui.FillRRect(gtx, r, gui.RadiusPill, gui.LerpColor(th.Pal.Surface, th.Pal.SurfaceHover, hv))
	x.open.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		return D{Size: image.Pt(openW, hgt)}
	})
	if gtx.Focused(&x.open.Clickable) {
		gui.FocusRing(gtx, th, r, gui.RadiusPill, 1)
	}
	func() {
		defer op.Offset(image.Pt(in, in)).Push(gtx.Ops).Pop()
		pg := gtx
		pg.Constraints = layout.Exact(image.Pt(pic, pic))
		x.img.Layout(pg, th, c.Img, x.in.Path, 0, gui.Thumb{Radius: 16, Placeholder: kindIcon(x.in.Kind)})
		b := gtx.Dp(16)
		defer op.Offset(image.Pt(pic-b+gtx.Dp(4), pic-b+gtx.Dp(4))).Push(gtx.Ops).Pop()
		kindBadge(gtx, th, x.in.Kind, 16)
	}()
	func() {
		defer op.Offset(image.Pt(tx, (hgt-ld.Size.Y)/2)).Push(gtx.Ops).Pop()
		lc.Add(gtx.Ops)
	}()
	func() {
		defer op.Offset(image.Pt(openW, (hgt-del)/2)).Push(gtx.Ops).Pop()
		x.del.Layout(gtx, func(gtx C) D {
			pointer.CursorPointer.Add(gtx.Ops)
			dh := b2f(x.del.Hovered())
			if dh > 0 {
				gui.FillCircle(gtx, 28, th.Pal.Card)
			}
			if gtx.Focused(&x.del.Clickable) {
				gui.FocusRing(gtx, th, image.Rect(0, 0, del, del), 14, 1)
			}
			is := gtx.Dp(14)
			o := op.Offset(image.Pt((del-is)/2, (del-is)/2)).Push(gtx.Ops)
			gui.IconX.LayoutStroke(gtx, 14, 2, gui.LerpColor(th.Pal.Text2, th.Pal.ErrorInk, dh))
			o.Pop()
			return D{Size: image.Pt(del, del)}
		})
	}()
	return D{Size: r.Size()}
}

// kindBadge is a small card-coloured disc with the kind's icon.
func kindBadge(gtx C, th *gui.Theme, kind string, size unit.Dp) {
	px := gtx.Dp(size)
	gui.FillCircle(gtx, size, th.Pal.Card)
	is := size * 5 / 8
	ip := gtx.Dp(is)
	defer op.Offset(image.Pt((px-ip)/2, (px-ip)/2)).Push(gtx.Ops).Pop()
	kindIcon(kind).LayoutStroke(gtx, is, 2.25, th.Pal.Text)
}

// ---- the editor ------------------------------------------------------------------------------------

// matEditor is the small popover of a material: what the picture shows, its
// name and a note. Edits apply as you type; «Готово», Enter in the name,
// Escape or a click outside close it; «Прибрати» removes the picture.
type matEditor struct {
	x       *material
	anchor  string
	kind    gui.Segmented
	kinds   [4]gui.Clickable // the narrow version: a chip per kind
	name    gui.TextField
	note    gui.TextField
	done    gui.Clickable
	remove  gui.Clickable
	anim    gui.Anim
	scrim   int
	keys    int
	focused bool
	missing int
}

func (e *matEditor) open(x *material) {
	e.x, e.anchor = x, matAnchor(x.key)
	e.name.Editor.MaxLen, e.note.Editor.MaxLen = matNameLen, matNoteLen
	e.name.SetText(x.in.Name)
	e.note.SetText(x.in.Note)
	e.focused, e.missing = false, 0
	e.anim.Set(0)
}

func (e *matEditor) close() { e.x = nil }

// Layout queues the popover above the page (after the chips were laid out).
func (e *matEditor) Layout(gtx C, c *core.Ctx, h *hero) {
	if e.x == nil {
		return
	}
	c.Th.WantAnchors()
	c.Th.Layer(gtx, func(gtx C) { e.draw(gtx, c, h) })
}

func (e *matEditor) events(gtx C) {
	for {
		ev, ok := gtx.Event(
			pointer.Filter{Target: &e.scrim, Kinds: pointer.Press},
			key.FocusFilter{Target: &e.keys},
			key.Filter{Focus: &e.keys, Name: key.NameEscape},
			key.Filter{Focus: &e.name.Editor, Name: key.NameEscape},
			key.Filter{Focus: &e.note.Editor, Name: key.NameEscape},
		)
		if !ok {
			return
		}
		switch ev := ev.(type) {
		case pointer.Event:
			if ev.Kind == pointer.Press {
				e.close()
			}
		case key.Event:
			if ev.State == key.Press && ev.Name == key.NameEscape {
				e.close()
			}
		}
		gtx.Execute(op.InvalidateCmd{})
	}
}

func (e *matEditor) draw(gtx C, c *core.Ctx, h *hero) {
	th := c.Th
	e.events(gtx)
	x := e.x
	if x == nil {
		return
	}
	if v, ok := e.kind.Changed(gtx); ok {
		x.in.Kind = v
	}
	for i := range e.kinds {
		if e.kinds[i].Clicked(gtx) && i < len(matKinds) {
			x.in.Kind = matKinds[i].Value
		}
	}
	if v, ok := e.name.Changed(gtx); ok {
		x.in.Name = clipRunes(v, matNameLen)
	}
	if v, ok := e.note.Changed(gtx); ok {
		x.in.Note = clipRunes(v, matNoteLen)
	}
	if e.done.Clicked(gtx) || e.name.Submitted(gtx) {
		e.close()
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	if e.remove.Clicked(gtx) {
		h.remove(x)
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	ar, ok := th.AnchorRect(e.anchor)
	win := gtx.Constraints.Max
	if !ok {
		// Placed once the anchor is known (next frame).
		if e.missing++; e.missing > 3 {
			e.close()
			return
		}
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	e.missing = 0
	margin, gap, pad := gtx.Dp(popMargin), gtx.Dp(popGap), gtx.Dp(gui.Sp16)
	w := min(gtx.Dp(400), win.X-2*margin)
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(w-2*pad, 0), Max: image.Pt(w-2*pad, win.Y)}
	cd, cc := record(cg, func(gtx C) D { return e.content(gtx, c, x) })
	hgt := cd.Size.Y + 2*pad
	below := win.Y - margin - ar.Max.Y - gap
	up := hgt > below && ar.Min.Y-gap-margin > below
	px := max(margin, min(ar.Min.X, win.X-margin-w))
	py := ar.Max.Y + gap
	if up {
		py = ar.Min.Y - gap - hgt
	}
	py = max(margin, min(py, win.Y-margin-hgt))
	e.anim.Duration, e.anim.Ease = gui.DurHover*3/2, gui.EaseOutCubic
	a := e.anim.Animate(gtx, 1)

	// Scrim: a press anywhere else closes the editor.
	sc := clip.Rect(image.Rectangle{Max: win}).Push(gtx.Ops)
	event.Op(gtx.Ops, &e.scrim)
	sc.Pop()
	rise := float32(gtx.Dp(6)) * (1 - a)
	if up {
		rise = -rise
	}
	defer op.Affine(f32.AffineId().Offset(f32.Pt(float32(px), float32(py)+rise))).Push(gtx.Ops).Pop()
	gui.Fade(gtx, a, func(gtx C) D {
		r := image.Rectangle{Max: image.Pt(w, hgt)}
		gui.Shadow(gtx, th, r, gui.RadiusInner, gui.ElevPop)
		gui.Box(gtx, r, gui.RadiusInner, th.Pal.Card, gui.WithAlpha(th.Pal.Border, 0.8))
		cl := clip.UniformRRect(r, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops)
		event.Op(gtx.Ops, &e.keys) // presses inside are not outside presses
		o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
		cc.Add(gtx.Ops)
		o.Pop()
		cl.Pop()
		return D{Size: r.Size()}
	})
	if !e.focused {
		e.focused = true
		e.name.Focus(gtx)
	}
}

// content is the editor's body.
func (e *matEditor) content(gtx C, c *core.Ctx, x *material) D {
	th := c.Th
	namePH, notePH, nameLabel := "Напр. Тарас", "Вік, характер, одяг — що студії варто знати", "Ім'я"
	switch x.in.Kind {
	case studio.MaterialLocation:
		namePH, notePH, nameLabel = "Напр. Стара хата", "Пора року, настрій, важливі деталі", "Назва"
	case studio.MaterialObject:
		namePH, notePH, nameLabel = "Напр. Бабусин кулон", "Чим він важливий для історії", "Назва"
	case studio.MaterialStyle:
		namePH, notePH, nameLabel = "Напр. Акварель", "Що взяти зі стилю: кольори, техніку, світло", "Назва"
	}
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					sz := gtx.Dp(56)
					pg := gtx
					pg.Constraints = layout.Exact(image.Pt(sz, sz))
					return x.img.Layout(pg, th, c.Img, x.in.Path, 0, gui.Thumb{Radius: 12, Placeholder: kindIcon(x.in.Kind)})
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return gui.VStack(gtx, 2,
						gui.Text{Style: th.BodyStrong, MaxLines: 1}.Widget(th, "Що на фото?"),
						gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, "Студія збереже цей вигляд у кадрах — без генерації заново."),
					)
				}),
			)
		},
		func(gtx C) D {
			if gtx.Constraints.Max.X >= gtx.Dp(360) {
				e.kind.Full = true
				return e.kind.Layout(gtx, th, x.in.Kind, matKinds)
			}
			// Narrow: chips that wrap instead of squeezed labels.
			ws := make([]layout.Widget, len(matKinds))
			for i, k := range matKinds {
				i, k := i, k
				ws[i] = func(gtx C) D {
					return gui.Chip{Text: k.Label, Icon: kindIcon(k.Value), Selected: x.in.Kind == k.Value, Small: true}.Layout(gtx, th, &e.kinds[i])
				}
			}
			return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
		},
		func(gtx C) D {
			return e.name.Layout(gtx, th, gui.FieldStyle{Label: nameLabel, Placeholder: namePH, Submit: true})
		},
		func(gtx C) D {
			return e.note.Layout(gtx, th, gui.FieldStyle{Label: "Опис (необов'язково)", Placeholder: notePH, MinLines: 2, MaxLines: 4})
		},
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2, Text: "Прибрати"}.Layout(gtx, th, &e.remove)
				}),
				layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Size: gui.Sm, Text: "Готово"}.Layout(gtx, th, &e.done)
				}),
			)
		},
	)
}
