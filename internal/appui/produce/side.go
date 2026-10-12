package produce

import (
	"fmt"
	"image"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// sideRow is one line of a status card in the context column: a coloured
// dot, what, how many; a click jumps to the first scene in that state.
type sideRow struct {
	Tone   gui.Tone
	Label  string
	Count  int
	Scene  string // first scene in this state ("" = not clickable)
	Always bool   // shown even at zero
}

// sideStack lays the context cards out: stacked in the 360 dp column, side
// by side when the workspace puts them under the main column.
func sideStack(gtx C, ws ...layout.Widget) D {
	if gtx.Constraints.Max.X < gtx.Dp(680) {
		return vstack(gtx, gui.Sp24, ws...)
	}
	return gui.Grid{MinWidth: 320, Gap: gui.Sp24, MaxCols: len(ws)}.Layout(gtx, len(ws), func(gtx C, i int) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return ws[i](gtx)
	})
}

// sideState keeps the context-column widgets of one project.
type sideState struct {
	rows  [8]widget.Clickable
	hover [8]gui.Anim
	tip   gui.Clickable
	tip2  gui.Clickable
}

// clicked returns the scene of a clicked row ("" = none). Call before card.
func (s *sideState) clicked(gtx C, rows []sideRow) string {
	for i, r := range rows {
		if i < len(s.rows) && s.rows[i].Clicked(gtx) && r.Scene != "" {
			return r.Scene
		}
	}
	return ""
}

// card draws the status card.
func (s *sideState) card(gtx C, th *gui.Theme, title, badge string, rows []sideRow) D {
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ws := []layout.Widget{func(gtx C) D {
			return layout.Inset{Bottom: gui.Sp4}.Layout(gtx, func(gtx C) D {
				return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
					layout.Flexed(1, label(th, th.H3, th.Pal.Text, 1, title)),
					layout.Rigid(func(gtx C) D {
						if badge == "" {
							return D{}
						}
						return label(th, th.Label, th.Pal.Text2, 1, badge)(gtx)
					}),
				)
			})
		}}
		for i, r := range rows {
			if (r.Count == 0 && !r.Always) || i >= len(s.rows) {
				continue
			}
			ws = append(ws, func(gtx C) D { return s.row(gtx, th, i, r) })
		}
		return vstack(gtx, 2, ws...)
	})
}

func (s *sideState) row(gtx C, th *gui.Theme, i int, r sideRow) D {
	W := gtx.Constraints.Max.X
	h := gtx.Dp(40)
	click := r.Scene != "" && r.Count > 0
	strong, _, _ := th.Pal.ToneColors(r.Tone)
	if r.Tone == gui.ToneNeutral {
		strong = th.Pal.BorderStrong
	}
	if click {
		s.rows[i].Layout(gtx, func(gtx C) D {
			defer clip.Rect(rectWH(W, h)).Push(gtx.Ops).Pop()
			pointer.CursorPointer.Add(gtx.Ops)
			return D{Size: pt(W, h)}
		})
	}
	s.hover[i].Duration = gui.DurHover
	hv := s.hover[i].Animate(gtx, b2f(click && s.rows[i].Hovered()))
	if hv > 0.01 {
		x := gtx.Dp(8)
		gui.FillRRect(gtx, image.Rect(-x, 0, W+x, h), gui.RadiusControl, gui.WithAlpha(th.Pal.Surface, hv))
	}
	cg := gtx
	cg.Constraints = layout.Constraints{Min: pt(W, 0), Max: pt(W, h)}
	rd, rc := record(cg, func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return dot(gtx, 10, strong) }),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Flexed(1, label(th, th.Body.Sized(14), th.Pal.Text2, 1, r.Label)),
			layout.Rigid(label(th, th.BodyStrong, th.Pal.Text, 1, fmt.Sprint(r.Count))),
			layout.Rigid(func(gtx C) D {
				if !click {
					return D{Size: pt(gtx.Dp(20), 0)}
				}
				return layout.Inset{Left: gui.Sp4}.Layout(gtx, gui.IconChevronRight.Widget(16, gui.LerpColor(th.Pal.Text3, th.Pal.Text, hv)))
			}),
		)
	})
	drawAt(gtx, 0, (h-rd.Size.Y)/2, rc)
	return D{Size: pt(W, h)}
}

// ---- description editor (references) ----------------------------------------------------

// descEditor is the "change the description and redraw" dialog of a
// character or location reference.
type descEditor struct {
	dlg                     gui.Dialog
	field                   gui.TextField
	cancel, save, saveRegen gui.Clickable
	proj, id, name          string
	kind                    studio.RegenKind
	focus                   bool
}

// Open shows the dialog for one reference.
func (d *descEditor) Open(pid string, kind studio.RegenKind, id, name, desc string) {
	d.proj, d.kind, d.id, d.name = pid, kind, id, name
	d.field.SetText(desc)
	d.focus = true
	d.dlg.Open()
}

func (d *descEditor) commit(c *core.Ctx, redraw bool) {
	text := d.field.Editor.Text()
	kind, id := d.kind, d.id
	ok := c.Edit(d.proj, func(p *studio.Project) {
		if kind == studio.RegenCharacter {
			for i := range p.Characters {
				if p.Characters[i].ID == id {
					p.Characters[i].Description = text
				}
			}
			return
		}
		for i := range p.Locations {
			if p.Locations[i].ID == id {
				p.Locations[i].Description = text
			}
		}
	})
	if !ok {
		return
	}
	d.dlg.Close()
	if redraw {
		c.Regen(d.proj, kind, id, "")
	} else {
		c.Toast(gui.ToneSuccess, "Опис збережено")
	}
}

// Layout queues the dialog (nothing while closed).
func (d *descEditor) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) {
	th := c.Th
	if pv != nil && d.proj != pv.ID && d.dlg.IsOpen() {
		d.dlg.Close()
	}
	if d.cancel.Clicked(gtx) {
		d.dlg.Close()
	}
	if d.save.Clicked(gtx) {
		d.commit(c, false)
	}
	if d.saveRegen.Clicked(gtx) {
		d.commit(c, true)
	}
	title := "Опис персонажа"
	hint := "Англійською: вік, зовнішність, зачіска, одяг. З цього опису студія малює референс, а з референсу — героя в кожному кадрі."
	if d.kind == studio.RegenLocation {
		title = "Опис локації"
		hint = "Англійською: що це за місце, час доби, погода, настрій. Людей на референсі локації не буде."
	}
	if d.name != "" {
		title += " «" + d.name + "»"
	}
	running := pv != nil && pv.Running()
	d.dlg.Layout(gtx, th, gui.DialogStyle{
		Title: title,
		Text:  hint,
		Icon:  gui.IconPenLine,
		Width: 600,
		Body: func(gtx C) D {
			if d.focus {
				d.focus = false
				d.field.Focus(gtx)
			}
			return d.field.Layout(gtx, th, gui.FieldStyle{MinLines: 6, MaxLines: 12, MaxLen: 2000,
				Placeholder: "Наприклад: a 10-year-old boy with messy red hair, green knitted sweater…"})
		},
		Actions: []layout.Widget{
			func(gtx C) D {
				return gui.Button{Kind: gui.Ghost, Text: "Скасувати"}.Layout(gtx, th, &d.cancel)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Text: "Зберегти"}.Layout(gtx, th, &d.save)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Primary, Text: "Зберегти й перемалювати", Icon: gui.IconRefreshCw, Disabled: running,
					Tip: "Референс буде намальовано заново з цим описом"}.Layout(gtx, th, &d.saveRegen)
			},
		},
	})
}
