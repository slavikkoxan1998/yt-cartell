package dialogs

// «Матеріали»: the user's own pictures given up front — characters,
// places, objects, a style sample — with a kind, a name and a short note.
// The engine copies them into the new project; the plan keeps the
// characters and locations under these names and uses the pictures as the
// reference images (studio.NewProjectOptions.Materials).

import (
	"image"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// maxMaterials keeps the list (and the request) reasonable.
const maxMaterials = 16

type materialKind struct {
	id, label, name, note string
	icon                  *gui.Icon
}

var materialKinds = []materialKind{
	{studio.MaterialCharacter, "Персонаж", "Ім'я героя", "Вік, характер, одяг — що важливо", gui.IconUserRound},
	{studio.MaterialLocation, "Локація", "Назва місця", "Час доби, настрій, деталі", gui.IconMapPin},
	{studio.MaterialObject, "Предмет", "Що це", "Чим він важливий для історії", gui.IconShapes},
	{studio.MaterialStyle, "Стиль", "Назва зразка (необов'язково)", "Що взяти з нього: кольори, техніку, світло", gui.IconPalette},
}

func kindOf(id string) materialKind {
	for _, k := range materialKinds {
		if k.id == id {
			return k
		}
	}
	return materialKinds[0]
}

// materialDraft is one picture the user added (not saved anywhere yet).
type materialDraft struct {
	kind   string
	path   string
	kindIn gui.Select
	name   gui.TextField
	note   gui.TextField
	remove gui.Clickable
	thumb  gui.ImageView
	appear gui.Anim
}

// materialsEditor is the «Матеріали» section of a sheet.
type materialsEditor struct {
	items []*materialDraft
	add   gui.Clickable
}

func (m *materialsEditor) reset() { m.items = nil }

// addFile takes a picked picture (the name starts as the file's name).
func (m *materialsEditor) addFile(path string) {
	if len(m.items) >= maxMaterials {
		return
	}
	d := &materialDraft{kind: studio.MaterialCharacter, path: path}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(stem))
	if len([]rune(stem)) > 40 {
		stem = string([]rune(stem)[:40])
	}
	d.name.SetText(stem)
	m.items = append(m.items, d)
}

// inputs are the materials for studio.NewProjectOptions.
func (m *materialsEditor) inputs() []studio.MaterialInput {
	out := make([]studio.MaterialInput, 0, len(m.items))
	for _, d := range m.items {
		out = append(out, studio.MaterialInput{
			Kind: d.kind,
			Name: strings.TrimSpace(d.name.Editor.Text()),
			Note: strings.TrimSpace(d.note.Editor.Text()),
			Path: d.path,
		})
	}
	return out
}

// update handles the clicks (before the layout of the sheet).
func (m *materialsEditor) update(gtx C, c *core.Ctx) {
	if m.add.Clicked(gtx) {
		c.PickFile("Фото героя, місця, предмета або зразок стилю", studio.MaterialExts(), m.addFile)
	}
	kept := m.items[:0]
	for _, d := range m.items {
		if d.remove.Clicked(gtx) {
			continue
		}
		if v, ok := d.kindIn.Changed(gtx); ok {
			d.kind = v
		}
		kept = append(kept, d)
	}
	m.items = kept
}

// layout draws the section: a heading with the add button, a line on what
// it is for, and a flat tile per picture.
func (m *materialsEditor) layout(gtx C, c *core.Ctx, hint string) D {
	th := c.Th
	full := len(m.items) >= maxMaterials
	head := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				title := "Матеріали"
				if n := len(m.items); n > 0 {
					title += " · " + core.Count(n, "фото", "фото", "фото")
				}
				return gui.Text{Style: th.Label}.Layout(gtx, th, title)
			}),
			layout.Rigid(func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconImagePlus, Text: "Додати фото", Disabled: full}.Layout(gtx, th, &m.add)
			}),
		)
	}
	ws := []layout.Widget{head,
		gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, hint),
	}
	for _, d := range m.items {
		d := d
		ws = append(ws, func(gtx C) D {
			d.appear.Duration = gui.DurDialog
			a := d.appear.Animate(gtx, 1)
			return gui.FadeUp(gtx, a, 6, func(gtx C) D { return d.layout(gtx, c) })
		})
	}
	return gui.VStack(gtx, gui.Sp12, ws...)
}

func (d *materialDraft) layout(gtx C, c *core.Ctx) D {
	th := c.Th
	k := kindOf(d.kind)
	opts := make([]gui.SelectOption, len(materialKinds))
	for i, mk := range materialKinds {
		opts[i] = gui.SelectOption{Value: mk.id, Label: mk.label, Icon: mk.icon}
	}
	// Length limits without counters (they would only add noise here).
	d.name.Editor.MaxLen, d.note.Editor.MaxLen = 60, 300
	d.name.Editor.SingleLine, d.note.Editor.SingleLine = true, true
	return flatTile(gtx, th, gui.Sp12, false, func(gtx C) D {
		W := gtx.Constraints.Max.X
		narrowTile := W < gtx.Dp(420)
		thumbW := unit.Dp(72)
		if narrowTile {
			thumbW = 52
		}
		thumb := func(gtx C) D {
			s := gtx.Dp(thumbW)
			g := gtx
			g.Constraints = layout.Exact(image.Pt(s, s))
			return d.thumb.Layout(g, th, c.Img, d.path, 0, gui.Thumb{Radius: gui.RadiusControl, Placeholder: gui.IconImage})
		}
		kindSel := func(gtx C) D {
			return d.kindIn.Layout(gtx, th, gui.SelectStyle{Options: opts, Value: d.kind})
		}
		name := func(gtx C) D {
			return d.name.Layout(gtx, th, gui.FieldStyle{Placeholder: k.name})
		}
		note := func(gtx C) D {
			return d.note.Layout(gtx, th, gui.FieldStyle{Placeholder: k.note})
		}
		removeBtn := func(gtx C) D {
			return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Прибрати"}.Layout(gtx, th, &d.remove)
		}
		if narrowTile {
			return gui.VStack(gtx, gui.Sp8,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(thumb),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Flexed(1, kindSel),
						layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
						layout.Rigid(removeBtn),
					)
				},
				name, note,
			)
		}
		fields := func(gtx C) D {
			top := func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(170))
						gtx.Constraints.Min.X = gtx.Constraints.Max.X
						return kindSel(gtx)
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Flexed(1, name),
				)
			}
			return gui.VStack(gtx, gui.Sp8, top, note)
		}
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(thumb),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, fields),
			layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
			layout.Rigid(removeBtn),
		)
	})
}
