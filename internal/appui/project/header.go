package project

import (
	"image"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// header is the project header card: progress ring, inline title, logline,
// fact pills, the profile chip (menu), Автопілот / Зупинити and the ⋯ menu.
type header struct {
	profBtn gui.Clickable
	prof    pickers.Menu
	moreBtn gui.Clickable
	more    pickers.Menu
	autoBtn gui.Clickable
	stopBtn gui.Clickable
}

// layout draws the header. compact (narrow windows) is one slim row: ring,
// title, Автопілот as an icon, the "Огляд" drawer toggle and ⋯.
func (h *header) layout(gtx C, c *core.Ctx, pv *core.ProjectView, ps *projState, compact bool, drawer *gui.Clickable) D {
	th := c.Th
	id := pv.ID
	h.actions(gtx, c, pv)
	ps.title.update(gtx, &ps.bind, c.Frame(), pv.P.Title, func(v string) {
		c.Edit(id, func(p *studio.Project) { p.Title = strings.TrimSpace(v) })
	})
	step := min(5, ps.f.ndone+1)
	ps.ring.Duration = gui.DurValue
	if compact {
		return gui.Card{Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.ProgressRing{Value: float32(ps.f.ndone) / 5, Size: 44, Stroke: 4,
						Label: strconv.Itoa(step) + "/5", Anim: &ps.ring}.Layout(gtx, th)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12 + 2}.Layout),
				layout.Flexed(1, func(gtx C) D { return ps.title.layout(gtx, th, th.H3, "Назва проєкту", 2) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
				layout.Rigid(func(gtx C) D {
					if pv.Running() {
						return gui.Button{Kind: gui.Secondary, Icon: gui.IconSquare, Round: true, Tip: "Зупинити"}.Layout(gtx, th, &h.stopBtn)
					}
					return gui.Button{Icon: gui.IconRocket, Round: true, Tip: "Автопілот"}.Layout(gtx, th, &h.autoBtn)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconPanelRight, Round: true, Tip: "Огляд: наступний крок і сцени"}.Layout(gtx, th, drawer)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D { return h.moreMenu(gtx, c, true) }),
			)
		})
	}

	ring := func(gtx C) D {
		return gui.ProgressRing{Value: float32(ps.f.ndone) / 5, Size: 72, Stroke: 7,
			Label: strconv.Itoa(step) + "/5", Caption: "крок", Anim: &ps.ring}.Layout(gtx, th)
	}
	titleW := func(gtx C) D { return ps.title.layout(gtx, th, th.H2, "Назва проєкту", 1) }
	logline := func(gtx C) D {
		txt := strings.TrimSpace(pv.P.Logline)
		if txt == "" {
			if len(pv.P.Scenes) > 0 {
				return D{}
			}
			return gui.Text{Style: th.Body, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, "Коротко про фільм з'явиться після плану.")
		}
		return gui.Text{Style: th.Body, Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, txt)
	}
	pills := func(gtx C) D { return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, h.facts(c, pv, ps)...) }
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(ring),
			layout.Rigid(layout.Spacer{Width: gui.Sp20}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D { return gui.VStack(gtx, 0, titleW, logline) },
					pills,
				)
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp24}.Layout),
			layout.Rigid(func(gtx C) D { return h.buttons(gtx, c, pv) }),
		)
	})
}

// facts are the header's fact pills.
func (h *header) facts(c *core.Ctx, pv *core.ProjectView, ps *projState) []layout.Widget {
	th := c.Th
	p := &pv.P
	var out []layout.Widget
	if n := len(p.Scenes); n > 0 {
		out = append(out, statPill(th, gui.IconClapperboard, scenesN(n)),
			statPill(th, gui.IconTimer, "≈ "+core.Clock(ps.f.total)))
	} else {
		dur := core.Duration(p.Input.TargetSeconds)
		if p.Input.Mode == studio.ModeVoice {
			dur = "за записом"
		}
		out = append(out, statPill(th, gui.IconTimer, dur))
	}
	out = append(out, func(gtx C) D { return aspectPill(gtx, th, p.Input.Aspect) })
	lang := pickers.LanguageLabel(p.Input.Language)
	if p.Input.Language == "" {
		lang = "мова як у тексті"
	}
	out = append(out, statPill(th, gui.IconLanguages, lang))
	style := pickers.StyleLabel(p.Input.Style)
	if strings.TrimSpace(p.Input.CustomStyle) != "" {
		style = "свій стиль"
	}
	out = append(out, statPill(th, gui.IconPalette, style))
	switch {
	case p.Input.Mode == studio.ModeVoice:
		out = append(out, statPill(th, gui.IconAudioLines, "своя озвучка"))
	case p.Input.SoundMode() == studio.AudioInVideo:
		out = append(out, statPill(th, gui.IconMessageCircle, "голос у відео"))
	case !p.Input.Voiceover:
		out = append(out, statPill(th, gui.IconVolumeX, "без голосу"))
	}
	if cm := p.Input.ContentMode(); cm != studio.ContentCharacters {
		m := pickers.ModeByValue(pickers.ContentModes, cm)
		out = append(out, statPill(th, m.Icon, strings.ToLower(m.Label)))
	}
	out = append(out, func(gtx C) D { return saveState(gtx, th, pv, ps) })
	return out
}

// aspectPill is the format pill with a tiny frame of the format's shape.
func aspectPill(gtx C, th *gui.Theme, aspect string) D {
	h := gtx.Dp(30)
	padL, padR, gap := gtx.Dp(10), gtx.Dp(12), gtx.Dp(6)
	is := gtx.Dp(15)
	m := op.Record(gtx.Ops)
	ld := gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, aspect)
	call := m.Stop()
	w := padL + is + gap + ld.Size.X + padR
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.Surface)
	pickers.FrameIcon(gtx, th, image.Rect(padL, (h-is)/2, padL+is, (h-is)/2+is), pickers.AspectRatio(aspect), 14)
	o := op.Offset(image.Pt(padL+is+gap, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// buttons are the header actions: profile chip, Автопілот/Зупинити, ⋯.
func (h *header) buttons(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	prof := "Без профілю"
	if pr, ok := c.E.Profile(pv.P.ProfileID); ok && pv.P.ProfileID != "" {
		prof = pr.Name
	}
	profW := func(gtx C) D {
		return h.prof.Layout(gtx, th, &h.profBtn, profileItems(c, pv), func(gtx C) D {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(260))
			return gui.Chip{Text: prof, Icon: gui.IconBookmark, Tip: "Профіль налаштувань"}.Layout(gtx, th, &h.profBtn)
		})
	}
	main := func(gtx C) D {
		if pv.Running() {
			return gui.Button{Kind: gui.Secondary, Icon: gui.IconSquare, Text: "Зупинити",
				Tip: "Нові задачі не стартують; уже запущені доробляться"}.Layout(gtx, th, &h.stopBtn)
		}
		return gui.Button{Icon: gui.IconRocket, Text: "Автопілот", Tip: "Студія сама пройде всі кроки до готового відео"}.Layout(gtx, th, &h.autoBtn)
	}
	return gui.HStack(gtx, gui.Sp8, profW, main, func(gtx C) D { return h.moreMenu(gtx, c, false) })
}

// moreMenu is the ⋯ button with its menu (compact adds the profile actions).
func (h *header) moreMenu(gtx C, c *core.Ctx, compact bool) D {
	th := c.Th
	items := moreItems()
	if compact {
		items = append([]pickers.MenuItem{
			{Value: "save", Label: "Зберегти як профіль", Icon: gui.IconSave},
			{Value: "manage", Label: "Профілі налаштувань", Icon: gui.IconSettings2},
		}, append([]pickers.MenuItem{{Divider: true, Value: "translate", Label: "Перекласти", Icon: gui.IconLanguages}}, items[1:]...)...)
	}
	return h.more.Layout(gtx, th, &h.moreBtn, items, func(gtx C) D {
		return gui.Button{Kind: gui.Tinted, Icon: gui.IconMoreHorizontal, Round: compact, Tip: "Ще дії"}.Layout(gtx, th, &h.moreBtn)
	})
}

// profileItems lists the settings profiles to apply, then save/manage.
func profileItems(c *core.Ctx, pv *core.ProjectView) []pickers.MenuItem {
	var items []pickers.MenuItem
	for _, pr := range c.E.Profiles() {
		items = append(items, pickers.MenuItem{Value: "apply:" + pr.ID, Label: pr.Name, Icon: gui.IconBookmark, Checked: pr.ID == pv.P.ProfileID})
	}
	items = append(items,
		pickers.MenuItem{Value: "save", Label: "Зберегти як профіль", Icon: gui.IconSave, Divider: len(items) > 0},
		pickers.MenuItem{Value: "manage", Label: "Керувати профілями", Icon: gui.IconSettings2},
	)
	return items
}

func moreItems() []pickers.MenuItem {
	return []pickers.MenuItem{
		{Value: "translate", Label: "Перекласти", Icon: gui.IconLanguages},
		{Value: "duplicate", Label: "Дублювати", Icon: gui.IconCopy},
		{Value: "export", Label: "Експортувати в редактор…", Icon: gui.IconExternalLink},
		{Value: "reveal", Label: "Показати в папці", Icon: gui.IconFolderOpen},
		{Value: "delete", Label: "Видалити проєкт", Icon: gui.IconTrash2, Danger: true, Divider: true},
	}
}

// actions handles the header's buttons and menus.
func (h *header) actions(gtx C, c *core.Ctx, pv *core.ProjectView) {
	id, title := pv.ID, pv.P.Title
	if h.autoBtn.Clicked(gtx) && !pv.Running() {
		startAutopilot(c, pv)
	}
	if h.stopBtn.Clicked(gtx) {
		c.E.Cancel(id)
	}
	if v, ok := h.prof.Picked(); ok {
		profileAction(c, pv, v)
	}
	if v, ok := h.more.Picked(); ok {
		switch v {
		case "translate":
			c.OpenSheet(core.Sheet{Kind: core.SheetTranslate, ProjectID: id, Done: func(newID string) {
				if newID != "" {
					c.Navigate(core.Route{Page: core.PageProject, ProjectID: newID})
				}
			}})
		case "duplicate":
			var copyID string
			c.Do(func() error {
				p, err := c.E.Duplicate(id)
				copyID = p.ID
				return err
			}, func() {
				c.Toast(gui.ToneSuccess, "Створено копію «"+title+"»")
				if copyID != "" {
					c.Navigate(core.Route{Page: core.PageProject, ProjectID: copyID})
				}
			})
		case "export":
			// Step 5 opens its «Експортувати в…» drawer.
			c.UI(id).Export = true
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 5})
		case "reveal":
			c.OpenPath(pv.Dir)
		case "save", "manage":
			profileAction(c, pv, v)
		case "delete":
			c.Confirm(core.Confirm{
				Title:  "Видалити проєкт «" + title + "»?",
				Text:   "Разом із ним зникнуть кадри, відео й озвучка. Цю дію не можна скасувати.",
				Icon:   gui.IconTrash2,
				OK:     "Видалити",
				Danger: true,
				OnOK: func() {
					c.Navigate(core.Route{Page: core.PageHome})
					c.Do(func() error { return c.E.Delete(id) }, func() {
						c.Toast(gui.ToneSuccess, "Проєкт «"+title+"» видалено")
					})
				},
			})
		}
	}
}

// startAutopilot confirms and starts the autopilot on a project.
func startAutopilot(c *core.Ctx, pv *core.ProjectView) {
	id := pv.ID
	if len(pv.P.Scenes) == 0 {
		if ok, why := ready(pv); !ok {
			c.Toast(gui.ToneInfo, why)
			return
		}
	}
	text := "Студія сама пройде всі кроки: план, героїв і кадри, відео з озвучкою та монтаж. Це витрачає кредити генерації. Зупинити можна будь-коли."
	if len(pv.P.Scenes) > 0 {
		text = "Студія продовжить з того, що вже зроблено: домалює кадри, зніме відео, озвучить і змонтує фільм. Це витрачає кредити генерації. Зупинити можна будь-коли."
	}
	c.Confirm(core.Confirm{
		Title: "Запустити автопілот?",
		Text:  text,
		Icon:  gui.IconRocket,
		OK:    "Запустити",
		OnOK: func() {
			if c.Run(id, studio.StageAutopilot, false, "") {
				c.Toast(gui.ToneInfo, "Автопілот почав роботу — прогрес видно тут і в черзі.")
			}
		},
	})
}

// profileAction applies a pick of the profile menu.
func profileAction(c *core.Ctx, pv *core.ProjectView, v string) {
	id := pv.ID
	switch {
	case v == "save":
		c.OpenSheet(core.Sheet{Kind: core.SheetProfile, FromProject: id})
	case v == "manage":
		c.OpenSheet(core.Sheet{Kind: core.SheetProfiles})
	case strings.HasPrefix(v, "apply:"):
		pid := strings.TrimPrefix(v, "apply:")
		pr, ok := c.E.Profile(pid)
		if !ok {
			return
		}
		c.Confirm(core.Confirm{
			Title: "Застосувати профіль «" + pr.Name + "»?",
			Text:  "Тривалість, формат, стиль, голос, мова, тон і налаштування монтажу стануть як у профілі. Текст історії, план і вже згенеровані файли не зміняться.",
			Icon:  gui.IconBookmark,
			OK:    "Застосувати",
			OnOK: func() {
				if err := c.E.ApplyProfile(id, pid); err != nil {
					c.Error(err)
					return
				}
				c.Toast(gui.ToneSuccess, "Профіль «"+pr.Name+"» застосовано")
			},
		})
	}
}

// savingFor is how long "Зберігаємо…" shows after a change (the engine
// saves 400 ms after the last edit).
const savingFor = 700 * time.Millisecond

// saveState is the autosave pill: "Зберігаємо…" right after an edit (or
// while typing), then "Збережено".
func saveState(gtx C, th *gui.Theme, pv *core.ProjectView, ps *projState) D {
	pending := false
	for f := range ps.bind.pending {
		pending = pending || f.dirty
	}
	since := time.Since(time.UnixMilli(pv.P.UpdatedAt))
	saving := pending || (since >= 0 && since < savingFor)
	if saving && !pending {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(savingFor - since)})
	}
	txt, ic, col := "Збережено", gui.IconCheck, th.Pal.Text3
	if saving {
		txt, ic, col = "Зберігаємо…", gui.IconRefreshCw, th.Pal.Text2
	}
	h := gtx.Dp(30)
	is := gtx.Dp(14)
	gap := gtx.Dp(6)
	m := op.Record(gtx.Ops)
	d := gui.Text{Style: th.Small, Color: col, MaxLines: 1}.Layout(gtx, th, txt)
	call := m.Stop()
	x := gtx.Dp(4)
	o := op.Offset(image.Pt(x, (h-is)/2)).Push(gtx.Ops)
	ic.Layout(gtx, 14, col)
	o.Pop()
	o = op.Offset(image.Pt(x+is+gap, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(x+is+gap+d.Size.X, h)}
}
