package settings

// «Профілі»: a card per settings profile (style preview, name, summary
// chips, edit / duplicate / delete) and a «Новий профіль» card.

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type profileCard struct {
	id             string
	edit, dup, del gui.Clickable
	makeDefault    gui.Clickable
}

type profilesSection struct {
	cards   []*profileCard
	add     gui.Clickable
	addHead gui.Clickable
	stagger gui.Stagger
}

func (s *profilesSection) card(id string) *profileCard {
	for _, c := range s.cards {
		if c.id == id {
			return c
		}
	}
	c := &profileCard{id: id}
	s.cards = append(s.cards, c)
	return c
}

// newProfileButton is the action in the section heading.
func (p *Page) newProfileButton(th *gui.Theme) layout.Widget {
	return func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Icon: gui.IconPlus, Text: "Новий профіль"}.Layout(gtx, th, &p.profiles.addHead)
	}
}

func (p *Page) profilesGrid(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.profiles
	list := c.E.Profiles()
	def := p.now.Autopilot.DefaultProfileID
	if s.add.Clicked(gtx) || s.addHead.Clicked(gtx) {
		c.OpenSheet(core.Sheet{Kind: core.SheetProfile})
	}
	for _, pr := range list {
		pr := pr
		pc := s.card(pr.ID)
		if pc.edit.Clicked(gtx) {
			c.OpenSheet(core.Sheet{Kind: core.SheetProfile, ProfileID: pr.ID})
		}
		if pc.dup.Clicked(gtx) {
			cp := pr
			cp.ID = ""
			cp.Name = copyName(pr.Name)
			c.Do(func() error { _, err := c.E.SaveProfile(cp); return err },
				func() { c.Toast(gui.ToneSuccess, "Створено «"+cp.Name+"»") })
		}
		if pc.del.Clicked(gtx) {
			p.confirmDelete(c, pr)
		}
		if pc.makeDefault.Clicked(gtx) {
			id, name := pr.ID, pr.Name
			p.cfg.set(c, func(cf *config.Config) { cf.Autopilot.DefaultProfileID = id }, func() {
				c.Toast(gui.ToneSuccess, "Автопілот тепер бере профіль «"+name+"»")
			})
		}
	}
	minW := unit.Dp(300)
	if m.narrow {
		minW = 260
	}
	grid := gui.Grid{MinWidth: minW, Gap: gui.Sp24, Stretch: true}
	// The «Новий профіль» tile only fills a gap of the last row (the heading
	// button always creates one): no row holding a lone tile.
	n := len(list)
	if cols := grid.Columns(gtx, gtx.Constraints.Max.X); n == 0 || n%cols != 0 {
		n++
	}
	return grid.Layout(gtx, n, func(gtx C, i int) D {
		a := s.stagger.Progress(gtx, i)
		return gui.FadeUp(gtx, a, 8, func(gtx C) D {
			if i == len(list) {
				return p.newProfileCard(gtx, th)
			}
			pr := list[i]
			return p.profileCard(gtx, c, pr, s.card(pr.ID), pr.ID == def)
		})
	})
}

func copyName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, "(копія)") {
		return name + " 2"
	}
	return name + " (копія)"
}

func (p *Page) confirmDelete(c *core.Ctx, pr config.Profile) {
	isDefault := p.now.Autopilot.DefaultProfileID == pr.ID
	text := "Проєкти, створені з нього, збережуть свої налаштування."
	if isDefault {
		text += " Автопілот повернеться до типових налаштувань студії."
	}
	id, name := pr.ID, pr.Name
	c.Confirm(core.Confirm{
		Title: "Видалити профіль «" + name + "»?", Text: text,
		Icon: gui.IconTrash2, OK: "Видалити", Danger: true,
		OnOK: func() {
			c.Do(func() error { return c.E.DeleteProfile(id) }, func() {
				c.Toast(gui.ToneSuccess, "Профіль «"+name+"» видалено")
			})
			if isDefault {
				p.cfg.set(c, func(cf *config.Config) {
					if cf.Autopilot.DefaultProfileID == id {
						cf.Autopilot.DefaultProfileID = ""
					}
				}, nil)
			}
		},
	})
}

func (p *Page) profileCard(gtx C, c *core.Ctx, pr config.Profile, pc *profileCard, isDefault bool) D {
	th := c.Th
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		top := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return styleThumb(gtx, c, pr.Style, pr.CustomStyle, 56) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.H3, MaxLines: 2}.Widget(th, pr.Name)),
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, styleLabel(pr.Style, pr.CustomStyle)))
						}),
					)
				}),
			)
		}
		chips := func(gtx C) D {
			return gui.Flow{Gap: 6}.Layout(gtx, profileChips(th, pr)...)
		}
		foot := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconPencil, Text: "Редагувати"}.Layout(gtx, th, &pc.edit)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconCopy, Tip: "Дублювати"}.Layout(gtx, th, &pc.dup)
				}),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2, Tip: "Видалити"}.Layout(gtx, th, &pc.del)
				}),
				layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
				layout.Rigid(func(gtx C) D {
					if isDefault {
						return pill(gtx, th, gui.IconRocket, "Для автопілота")
					}
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRocket, Tip: "Зробити профілем автопілота"}.Layout(gtx, th, &pc.makeDefault)
				}),
			)
		}
		// The footer sits at the bottom of stretched cards (the grid gives
		// shorter cards the row height as their minimum).
		free := gtx
		free.Constraints.Min.Y = 0
		d := gui.VStack(free, gui.Sp16, top, chips)
		fd, fc := record(free, foot)
		h := max(gtx.Constraints.Min.Y, d.Size.Y+gtx.Dp(gui.Sp16)+fd.Size.Y)
		o := op.Offset(image.Pt(0, h-fd.Size.Y)).Push(gtx.Ops)
		fc.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(gtx.Constraints.Max.X, h)}
	})
}

func (p *Page) newProfileCard(gtx C, th *gui.Theme) D {
	return gui.Card{Tint: true, Click: &p.profiles.add, Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		body := func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					s := gtx.Dp(48)
					fillCircle(gtx, s, th.Pal.Card)
					o := op.Offset(image.Pt((s-gtx.Dp(22))/2, (s-gtx.Dp(22))/2)).Push(gtx.Ops)
					gui.IconPlus.Layout(gtx, 22, th.Pal.ActionText)
					o.Pop()
					return D{Size: image.Pt(s, s)}
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
				layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold)}.Widget(th, "Новий профіль")),
				layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(240))
					return gui.Text{Style: th.Small, Color: th.Pal.Text2, Align: text.Middle}.Layout(gtx, th, "Збережи свій набір: стиль, формат, голос і монтаж")
				}),
			)
		}
		free := gtx
		free.Constraints.Min = image.Point{}
		d, call := record(free, body)
		h := max(gtx.Constraints.Min.Y, d.Size.Y)
		o := op.Offset(image.Pt((gtx.Constraints.Max.X-d.Size.X)/2, (h-d.Size.Y)/2)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(gtx.Constraints.Max.X, h)}
	})
}

// profileChips are the summary chips of a profile: style, genre, language,
// format, duration.
func profileChips(th *gui.Theme, pr config.Profile) []layout.Widget {
	var ws []layout.Widget
	if pr.Genre != "" {
		ws = append(ws, staticChip(th, gui.IconDrama, choiceLabel(studio.Genres, pr.Genre)))
	}
	ws = append(ws, staticChip(th, gui.IconLanguages, languageLabel(pr.Language)))
	ws = append(ws, staticChip(th, gui.IconRatio, pr.Aspect))
	if pr.TargetSeconds > 0 {
		ws = append(ws, staticChip(th, gui.IconTimer, core.Duration(pr.TargetSeconds)))
	}
	if !pr.Voiceover {
		ws = append(ws, staticChip(th, gui.IconVolumeX, "Без озвучки"))
	}
	if pr.StoryboardMode == "grid3x3" {
		ws = append(ws, staticChip(th, gui.IconGrid3x3, "Розкадровка 3×3"))
	}
	return ws
}

// styleLabel is the name of a profile's look ("Свій стиль" for a custom one).
func styleLabel(id, custom string) string {
	if strings.TrimSpace(custom) != "" {
		return "Свій стиль"
	}
	if id == "" {
		return "Стиль не вибрано"
	}
	return pickers.StyleLabel(id)
}

// languageLabel is the name of an audience language ("" = as in the text).
func languageLabel(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Мова як у тексті"
	}
	return pickers.LanguageLabel(code)
}

func choiceLabel(list []studio.Choice, id string) string {
	if c, ok := studio.ChoiceByID(list, id); ok {
		return c.Label
	}
	return id
}

// styleThumb is a rounded square preview of a style (the shared picker
// previews: picture or palette poster), or a brush for a custom style.
func styleThumb(gtx C, c *core.Ctx, id, custom string, size unit.Dp) D {
	th := c.Th
	px := gtx.Dp(size)
	g := gtx
	g.Constraints = layout.Exact(image.Pt(px, px))
	if strings.TrimSpace(custom) == "" {
		if img, ok := pickers.StylePreview(c, id); ok {
			return gui.Thumb{Image: img, Radius: gui.RadiusControl}.Layout(g, th)
		}
	}
	return gui.Thumb{Radius: gui.RadiusControl, Placeholder: gui.IconPaintbrush}.Layout(g, th)
}
