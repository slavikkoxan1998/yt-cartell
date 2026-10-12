package dialogs

// The profiles manager: every profile with edit / duplicate / delete and
// «для автопілота»; «Новий профіль». The editor opens on top of it.

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

type profileRow struct {
	edit, dup, del, def gui.Clickable
}

type profilesSheet struct {
	drawer  gui.Drawer
	sh      core.Sheet
	rows    map[string]*profileRow
	add     gui.Clickable
	empty   gui.Clickable
	stagger gui.Stagger
}

func (s *profilesSheet) kind() core.SheetKind { return core.SheetProfiles }
func (s *profilesSheet) isOpen() bool         { return s.drawer.IsOpen() }
func (s *profilesSheet) shut()                { s.drawer.Close() }

func (s *profilesSheet) open(c *core.Ctx, sh core.Sheet) {
	s.sh = sh
	s.stagger.Restart()
	s.drawer.Open()
}

func (s *profilesSheet) row(id string) *profileRow {
	if s.rows == nil {
		s.rows = map[string]*profileRow{}
	}
	r := s.rows[id]
	if r == nil {
		r = &profileRow{}
		s.rows[id] = r
	}
	return r
}

func (s *profilesSheet) layout(gtx C, c *core.Ctx, host *Sheets) {
	th := c.Th
	list := c.E.Profiles()
	def := c.E.Config().Get().Autopilot.DefaultProfileID
	openEditor := func(id string) {
		host.Open(c, core.Sheet{Kind: core.SheetProfile, ProfileID: id, Done: s.sh.Done})
	}
	if s.add.Clicked(gtx) || s.empty.Clicked(gtx) {
		openEditor("")
	}
	for _, pr := range list {
		pr := pr
		r := s.row(pr.ID)
		if r.edit.Clicked(gtx) {
			openEditor(pr.ID)
		}
		if r.dup.Clicked(gtx) {
			cp := pr
			cp.ID = ""
			cp.Name = copyName(pr.Name)
			c.Do(func() error { _, err := c.E.SaveProfile(cp); return err },
				func() { c.Toast(gui.ToneSuccess, "Створено «"+cp.Name+"»") })
		}
		if r.del.Clicked(gtx) {
			deleteProfile(c, pr, pr.ID == def)
		}
		if r.def.Clicked(gtx) {
			id, name := pr.ID, pr.Name
			c.Background(func() {
				err := c.E.Config().Update(func(cf *config.Config) { cf.Autopilot.DefaultProfileID = id })
				c.Post(func() {
					if err != nil {
						c.Error(err)
						return
					}
					c.Toast(gui.ToneSuccess, "Автопілот тепер бере профіль «"+name+"»")
				})
			})
		}
	}
	body := func(gtx C) D {
		if len(list) == 0 {
			return layout.Inset{Top: gui.Sp40}.Layout(gtx, func(gtx C) D {
				return gui.EmptyState{Icon: gui.IconLayers, Title: "Профілів ще немає",
					Text: "Профіль — це набір налаштувань: стиль, формат, тривалість, голос і монтаж. Створи перший, щоб стартувати в один клік."}.Layout(gtx, th, nil)
			})
		}
		var ws []layout.Widget
		for i, pr := range list {
			i, pr := i, pr
			ws = append(ws, func(gtx C) D {
				a := s.stagger.Progress(gtx, i)
				return gui.FadeUp(gtx, a, 8, func(gtx C) D { return s.profileRow(gtx, c, pr, s.row(pr.ID), pr.ID == def) })
			})
		}
		return gui.VStack(gtx, gui.Sp12, ws...)
	}
	foot := func(gtx C) D {
		count := func(gtx C) D {
			return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, core.Count(len(list), "профіль", "профілі", "профілів"))
		}
		return sheetFoot(gtx, th, nil, nil, count, func(gtx C, full bool) D {
			return gui.Button{Kind: gui.Primary, Icon: gui.IconPlus, Text: "Новий профіль", Full: full}.Layout(gtx, th, &s.add)
		})
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Профілі",
		Subtitle: "Готові набори налаштувань для швидкого старту.",
		Width:    580,
		Body:     body,
		Footer:   foot,
	})
}

func (s *profilesSheet) profileRow(gtx C, c *core.Ctx, pr config.Profile, r *profileRow, isDefault bool) D {
	th := c.Th
	return flatTile(gtx, th, gui.Sp16, false, func(gtx C) D {
		top := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return styleThumb(gtx, c, pr.Style, pr.CustomStyle, 52) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp12 + 2}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), MaxLines: 2}.Widget(th, pr.Name)),
						layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, styleName(pr)+" · "+summary(pr))),
					)
				}),
			)
		}
		actions := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconPencil, Text: "Редагувати"}.Layout(gtx, th, &r.edit)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconCopy, Tip: "Дублювати"}.Layout(gtx, th, &r.dup)
				}),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2, Tip: "Видалити"}.Layout(gtx, th, &r.del)
				}),
				layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
				layout.Rigid(func(gtx C) D {
					if isDefault {
						return defaultPill(gtx, th)
					}
					b := gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRocket, Text: "Для автопілота",
						Tip: "Автопілот братиме цей профіль, коли на головній не вибрано інший"}
					if gtx.Constraints.Max.X < gtx.Dp(150) {
						b.Text = "" // phones: the rocket, with the tooltip
					}
					return b.Layout(gtx, th, &r.def)
				}),
			)
		}
		return gui.VStack(gtx, gui.Sp12, top, actions)
	})
}

// defaultPill marks the autopilot's profile.
func defaultPill(gtx C, th *gui.Theme) D {
	h := gtx.Dp(28)
	padL, padR, gap, is := gtx.Dp(10), gtx.Dp(12), gtx.Dp(6), gtx.Dp(14)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(max(0, gtx.Constraints.Max.X-padL-padR-is-gap), h)}
	ld, lc := record(lg, gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Профіль автопілота"))
	w := padL + is + gap + ld.Size.X + padR
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.Surface)
	o := op.Offset(image.Pt(padL, (h-is)/2)).Push(gtx.Ops)
	gui.IconRocket.Layout(gtx, 14, th.Pal.Text2)
	o.Pop()
	o = op.Offset(image.Pt(padL+is+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

func copyName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, "(копія)") {
		return name + " 2"
	}
	return name + " (копія)"
}

// deleteProfile confirms and deletes a profile (and forgets it as the
// autopilot's default).
func deleteProfile(c *core.Ctx, pr config.Profile, isDefault bool) {
	text := "Проєкти, створені з нього, збережуть свої налаштування."
	if isDefault {
		text += " Автопілот повернеться до типових налаштувань студії."
	}
	id, name := pr.ID, pr.Name
	c.Confirm(core.Confirm{
		Title: "Видалити профіль «" + name + "»?", Text: text,
		Icon: gui.IconTrash2, OK: "Видалити", Danger: true,
		OnOK: func() {
			c.Background(func() {
				err := c.E.DeleteProfile(id)
				if err == nil && isDefault {
					_ = c.E.Config().Update(func(cf *config.Config) {
						if cf.Autopilot.DefaultProfileID == id {
							cf.Autopilot.DefaultProfileID = ""
						}
					})
				}
				c.Post(func() {
					if err != nil {
						c.Error(err)
						return
					}
					c.Toast(gui.ToneSuccess, "Профіль «"+name+"» видалено")
				})
			})
		},
	})
}
