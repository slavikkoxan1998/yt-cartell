package project

import (
	"image"
	"math/rand/v2"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// step2 is План: the general card (title, logline, style guide), the cast
// and the locations as cards, and the scene cards (grid or list, virtualised).
type step2 struct {
	ps     *projState
	scroll gui.Scroll

	title, logline, guide bound

	addChar, addLoc gui.Clickable
	addObj          gui.Clickable // an object / motif (films without people, mixed)
	narrVoice       bound         // the off-screen voice of «Голос у відео»
	voices          voicesCard    // «Голоси озвучки» (voices.go)
	chars           map[string]*charUI
	locs            map[string]*locUI

	view      gui.Segmented
	replanBtn gui.Clickable
	backBtn   gui.Clickable
	scenes    map[string]*sceneUI

	jumpLong gui.Clickable
	flashID  string
	flashAt  time.Time
	skel     gui.Stagger
}

func newStep2(ps *projState) *step2 {
	return &step2{ps: ps, chars: map[string]*charUI{}, locs: map[string]*locUI{}, scenes: map[string]*sceneUI{}}
}

// Bleed lets the cards' shadows and the scrollbar live beside the column.
func (s *step2) Bleed() unit.Dp { return bleed }

// newUID is a client id for a new item (8 base-36 chars) so references to it
// survive the save.
func newUID() string {
	const abc = "0123456789abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 8)
	for i := range b {
		b[i] = abc[rand.IntN(len(abc))]
	}
	return string(b)
}

// Elements of the page before the first scene row.
const (
	elGeneral = iota
	elCastHead
	elCast
	elLocHead
	elLocs
	elScenesHead
	elFirstScene
)

// Layout draws the step.
func (s *step2) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	b := gtx.Dp(bleed)
	W := gtx.Constraints.Max.X - 2*b
	if len(pv.P.Scenes) == 0 {
		return s.noPlan(gtx, c, pv)
	}
	s.handle(gtx, c, pv)
	ui := c.UI(pv.ID)
	list := ui.List
	cols := 1
	if !list {
		cols = gui.Grid{MinWidth: 380, Gap: gui.Sp16}.Columns(gtx, W)
	}
	n := len(pv.P.Scenes)
	rows := (n + cols - 1) / cols
	if ui.ScrollTo != "" {
		if _, idx := pv.Scene(ui.ScrollTo); idx >= 0 {
			s.scroll.List.Position = layout.Position{First: elFirstScene + idx/cols}
			s.flashID, s.flashAt = ui.ScrollTo, gtx.Now
			ui.Scene = ui.ScrollTo
		}
		ui.ScrollTo = ""
	}
	total := elFirstScene + rows + 1
	defer topFade(gtx, th, &s.scroll)
	return s.scroll.Layout(gtx, th, total, func(gtx C, i int) D {
		full := gtx.Constraints.Max.X
		if i == total-1 {
			return D{Size: image.Pt(full, gtx.Dp(gui.Sp32))}
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Min: image.Pt(W, 0), Max: image.Pt(W, gtx.Constraints.Max.Y)}
		m := op.Record(gtx.Ops)
		var d D
		gapB := gtx.Dp(gui.Sp20)
		switch i {
		case elGeneral:
			d = s.general(cg, c, pv)
			gapB = gtx.Dp(gui.Sp32)
		case elCastHead:
			d = s.castHead(cg, c, pv)
			gapB = gtx.Dp(gui.Sp16)
		case elCast:
			d = s.cast(cg, c, pv)
			gapB = gtx.Dp(gui.Sp32)
		case elLocHead:
			d = s.locHead(cg, c, pv)
			gapB = gtx.Dp(gui.Sp16)
		case elLocs:
			d = s.locations(cg, c, pv)
			gapB = gtx.Dp(gui.Sp32)
		case elScenesHead:
			d = s.scenesHead(cg, c, pv)
			gapB = gtx.Dp(gui.Sp16)
		default:
			d = s.sceneRow(cg, c, pv, i-elFirstScene, cols)
			gapB = gtx.Dp(gui.Sp16)
		}
		call := m.Stop()
		o := op.Offset(image.Pt(b, gtx.Dp(2))).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: image.Pt(full, d.Size.Y+gtx.Dp(2)+gapB)}
	})
}

// handle applies the page-level clicks.
func (s *step2) handle(gtx C, c *core.Ctx, pv *core.ProjectView) {
	id := pv.ID
	if s.addChar.Clicked(gtx) {
		c.Edit(id, func(p *studio.Project) {
			p.Characters = append(p.Characters, studio.Character{ID: newUID(), Name: "Новий персонаж", Image: studio.EmptyAsset()})
		})
	}
	if s.addObj.Clicked(gtx) {
		c.Edit(id, func(p *studio.Project) {
			p.Characters = append(p.Characters, studio.Character{ID: newUID(), Name: "Новий об'єкт", Kind: studio.KindObject, Image: studio.EmptyAsset()})
		})
	}
	if s.addLoc.Clicked(gtx) {
		c.Edit(id, func(p *studio.Project) {
			p.Locations = append(p.Locations, studio.Location{ID: newUID(), Name: "Нова локація", Image: studio.EmptyAsset()})
		})
	}
	if v, ok := s.view.Changed(gtx); ok {
		c.UI(id).List = v == "list"
	}
	if s.replanBtn.Clicked(gtx) && !pv.Running() {
		replan(c, s.ps, id)
	}
}

// noPlan is the page before a plan exists: the plan being written (skeleton
// cards) or the way back to step 1.
func (s *step2) noPlan(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	if s.backBtn.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: pv.ID, Step: 1})
	}
	b := gtx.Dp(bleed)
	if !s.ps.f.planning {
		s.skel.Restart()
		return layout.Center.Layout(gtx, func(gtx C) D {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X-2*b, gtx.Dp(480))
			return gui.EmptyState{Icon: gui.IconScrollText, Title: "Плану ще немає",
				Text:   "Напиши історію на кроці «Сценарій» і натисни «Створити план» — ШІ розпише її на сцени, героїв і локації.",
				Action: "До сценарію", ActionIcon: gui.IconArrowLeft}.Layout(gtx, th, &s.backBtn)
		})
	}
	// The plan is being written: skeleton cards of what is coming.
	msg := "Читає історію, придумує героїв і локації, пише текст і промти для кожної сцени. Зазвичай це 20–90 секунд."
	return layout.Inset{Left: dp(gtx, b), Right: dp(gtx, b), Top: 2}.Layout(gtx, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp20,
			func(gtx C) D {
				return gui.Card{}.Layout(gtx, th, func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							sz := gtx.Dp(52)
							gui.FillCircle(gtx, 52, th.Pal.RunningSoft)
							is := gtx.Dp(24)
							o := op.Offset(image.Pt((sz-is)/2, (sz-is)/2)).Push(gtx.Ops)
							gui.Spinner{Size: 24, Color: th.Pal.RunningInk}.Layout(gtx, th)
							o.Pop()
							return D{Size: image.Pt(sz, sz)}
						}),
						layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
						layout.Flexed(1, func(gtx C) D {
							return gui.VStack(gtx, 2,
								gui.Text{Style: th.H3}.Widget(th, "ШІ пише план"),
								gui.Text{Style: th.Body, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, msg),
							)
						}),
					)
				})
			},
			func(gtx C) D {
				return gui.Grid{MinWidth: 380, Gap: gui.Sp16}.Layout(gtx, 6, func(gtx C, i int) D {
					p := s.skel.Progress(gtx, i)
					return gui.FadeUp(gtx, p, 8, func(gtx C) D {
						return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
							return gui.VStack(gtx, gui.Sp16,
								func(gtx C) D {
									return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
										layout.Rigid(gui.Skeleton{Width: 30, Height: 30, Radius: 15}.Widget(th)),
										layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
										layout.Flexed(1, gui.Skeleton{Width: 180, Height: 16}.Widget(th)),
									)
								},
								func(gtx C) D { return gui.SkeletonLines(gtx, th, 3) },
								gui.Skeleton{Height: 36, Radius: 12}.Widget(th),
							)
						})
					})
				})
			},
		)
	})
}

// ---- general ---------------------------------------------------------------------------------

func (s *step2) general(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	id := pv.ID
	p := &pv.P
	fr := c.Frame()
	s.title.update(gtx, &s.ps.bind, fr, p.Title, func(v string) {
		c.Edit(id, func(p *studio.Project) { p.Title = strings.TrimSpace(v) })
	})
	s.logline.update(gtx, &s.ps.bind, fr, p.Logline, func(v string) {
		v = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(v)
		c.Edit(id, func(p *studio.Project) { p.Logline = v })
	})
	s.guide.update(gtx, &s.ps.bind, fr, p.StyleGuide, func(v string) {
		c.Edit(id, func(p *studio.Project) { p.StyleGuide = v })
	})
	left := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D {
				return s.title.Layout(gtx, th, gui.FieldStyle{Label: "Назва", Placeholder: "Назва фільму", Style: th.BodyStrong})
			},
			func(gtx C) D {
				return s.logline.Layout(gtx, th, gui.FieldStyle{Label: "Коротко про що фільм", Placeholder: "Одне-два речення: хто, що хоче, що заважає", MinLines: 3, MaxLines: 6})
			},
		)
	}
	talk := p.Input.SoundMode() == studio.AudioInVideo
	if talk {
		s.narrVoice.update(gtx, &s.ps.bind, fr, p.NarratorVoice, func(v string) {
			c.Edit(id, func(p *studio.Project) { p.NarratorVoice = strings.TrimSpace(v) })
		})
	}
	right := func(gtx C) D {
		guide := func(gtx C) D {
			return s.guide.Layout(gtx, th, gui.FieldStyle{Label: "Візуальний стиль усіх кадрів (англ.)",
				Hint:     "Додається до кожної картинки — від нього залежить, щоб усі кадри були як з одного фільму.",
				MinLines: 6, MaxLines: 12})
		}
		if !talk {
			return guide(gtx)
		}
		return gui.VStack(gtx, gui.Sp16, guide, func(gtx C) D {
			return s.narrVoice.Layout(gtx, th, gui.FieldStyle{Label: "Голос за кадром (англ.)",
				Placeholder: "A calm, warm male voice, slow pace…",
				Hint:        "Так відеомодель озвучить репліки «Голосу за кадром».", MinLines: 2, MaxLines: 4})
		})
	}
	card := func(gtx C) D {
		return gui.Card{}.Layout(gtx, th, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp20,
				cardTitle(th, gui.IconBookOpen, "Загальне", "Назва, короткий опис і стиль, спільний для всіх кадрів", nil),
				func(gtx C) D {
					if gtx.Constraints.Max.X < gtx.Dp(760) {
						return gui.VStack(gtx, gui.Sp16, left, right)
					}
					return gui.Columns{Gap: gui.Sp24}.Layout(gtx, gui.Col{Span: 5, W: left}, gui.Col{Span: 7, W: right})
				},
			)
		})
	}
	if !multiVoiceShown(p.Input) {
		return card(gtx)
	}
	// The voice-over: «Голоси озвучки» right under it (voices.go).
	return gui.VStack(gtx, gui.Sp20, card, func(gtx C) D { return s.voices.layout(gtx, c, pv) })
}

// ---- cast ------------------------------------------------------------------------------------

type charUI struct {
	name, role, desc bound
	sound            bound // Character.Voice: how the video model voices them
	voice            pickers.Voice
	del              gui.Clickable
	img              gui.ImageView
}

func (s *step2) castHead(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	w := castWordsOf(pv.P.Input)
	var btns []layout.Widget
	if w.people {
		btns = append(btns, func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconUserPlus, Text: "Додати персонажа"}.Layout(gtx, th, &s.addChar)
		})
	}
	if w.objects {
		btns = append(btns, func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: w.icon, Text: w.add}.Layout(gtx, th, &s.addObj)
		})
	}
	return sectionHead(gtx, th, w.title, castMeta(&pv.P), btns...)
}

func (s *step2) cast(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := &pv.P
	if len(p.Characters) == 0 {
		return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return gui.Text{Style: th.Body, Color: th.Pal.Text2}.Layout(gtx, th, castWordsOf(p.Input).empty)
		})
	}
	return gui.Grid{MinWidth: 340, Gap: gui.Sp16, Stretch: true}.Layout(gtx, len(p.Characters), func(gtx C, i int) D {
		return s.charCard(gtx, c, pv, i)
	})
}

func (s *step2) charCard(gtx C, c *core.Ctx, pv *core.ProjectView, i int) D {
	th := c.Th
	id := pv.ID
	ch := pv.P.Characters[i]
	cid := ch.ID
	u := s.chars[cid]
	if u == nil {
		u = &charUI{}
		u.voice.Default = "Як у оповідача"
		u.voice.Flat = true // inside the cast card: a flat field, not a nested tile
		s.chars[cid] = u
	}
	fr := c.Frame()
	edit := func(fn func(ch *studio.Character)) {
		c.Edit(id, func(p *studio.Project) {
			for k := range p.Characters {
				if p.Characters[k].ID == cid {
					fn(&p.Characters[k])
					return
				}
			}
		})
	}
	u.name.update(gtx, &s.ps.bind, fr, ch.Name, func(v string) { edit(func(ch *studio.Character) { ch.Name = v }) })
	u.role.update(gtx, &s.ps.bind, fr, ch.Role, func(v string) { edit(func(ch *studio.Character) { ch.Role = v }) })
	u.desc.update(gtx, &s.ps.bind, fr, ch.Description, func(v string) { edit(func(ch *studio.Character) { ch.Description = v }) })
	talk := pv.P.Input.SoundMode() == studio.AudioInVideo && !ch.IsObject()
	if talk {
		u.sound.update(gtx, &s.ps.bind, fr, ch.Voice, func(v string) { edit(func(ch *studio.Character) { ch.Voice = strings.TrimSpace(v) }) })
	}
	if v, ok := u.voice.Changed(); ok {
		edit(func(ch *studio.Character) { ch.VoiceID = v })
	}
	if u.del.Clicked(gtx) {
		name := firstText(ch.Name, "без імені")
		title, text := "Прибрати персонажа «"+name+"»?", "Він зникне з усіх сцен; його репліки прочитає оповідач."
		if ch.IsObject() {
			title, text = "Прибрати «"+name+"»?", "Він зникне з усіх сцен."
		}
		c.Confirm(core.Confirm{
			Title: title, Text: text,
			Icon: gui.IconTrash2, OK: "Прибрати", Danger: true,
			OnOK: func() {
				c.Edit(id, func(p *studio.Project) {
					kept := p.Characters[:0:0]
					for _, x := range p.Characters {
						if x.ID != cid {
							kept = append(kept, x)
						}
					}
					p.Characters = kept
				})
			},
		})
	}
	thumb := func(gtx C) D {
		w := gtx.Dp(84)
		gtx.Constraints = layout.Exact(image.Pt(w, w*4/3))
		t := gui.Thumb{Ratio: 3.0 / 4, Radius: 12, Placeholder: objectThumbIcon(ch)}
		if ch.Image.Path == "" {
			return t.Layout(gtx, th)
		}
		return u.img.Layout(gtx, th, c.Img, pv.Abs(ch.Image.Path), ch.Image.UpdatedAt, t)
	}
	top := func(gtx C) D {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(thumb),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						ph := "Ім'я"
						if ch.IsObject() {
							ph = "Назва"
						}
						return u.name.Layout(gtx, th, gui.FieldStyle{Placeholder: ph, Style: th.BodyStrong})
					},
					func(gtx C) D {
						ph := "Роль в історії"
						if ch.IsObject() {
							ph = "Роль у сюжеті"
						}
						return u.role.Layout(gtx, th, gui.FieldStyle{Placeholder: ph})
					},
					func(gtx C) D {
						if ch.IsObject() && pv.P.Input.ContentMode() == studio.ContentMixed {
							return gui.Flow{Gap: gui.Sp8, RowGap: 6}.Layout(gtx,
								func(gtx C) D { return kindBadge(gtx, th) },
								func(gtx C) D { return tagRow(gtx, th, ch.Tag, fromPhoto(&pv.P, ch.MaterialID)) })
						}
						return tagRow(gtx, th, ch.Tag, fromPhoto(&pv.P, ch.MaterialID))
					},
				)
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				tip := "Прибрати персонажа"
				if ch.IsObject() {
					tip = "Прибрати об'єкт"
				}
				return gui.IconButton{Icon: gui.IconTrash2, Size: 36, Tip: tip}.Layout(gtx, th, &u.del)
			}),
		)
	}
	ws := []layout.Widget{
		top,
		func(gtx C) D {
			label, ph := "Зовнішність (англ.)", "A 10-year-old boy, freckles, messy red hair, green hoodie, blue jeans…"
			if ch.IsObject() {
				label, ph = "Як виглядає (англ.)", "A vintage brass compass with a cracked glass lid, green patina…"
			}
			return u.desc.Layout(gtx, th, gui.FieldStyle{Label: label, Placeholder: ph, MinLines: 3, MaxLines: 8})
		},
	}
	switch {
	case talk:
		ws = append(ws, func(gtx C) D {
			return u.sound.Layout(gtx, th, gui.FieldStyle{Label: "Голос у відео (англ.)",
				Placeholder: "A boy of about 8, soft high voice, a little shy…", MinLines: 2, MaxLines: 4})
		})
	case pv.P.Input.Voiceover && !ch.IsObject():
		ws = append(ws, func(gtx C) D { return u.voice.Layout(gtx, c, "Голос персонажа", ch.VoiceID) })
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D { return fillStack(gtx, gui.Sp16, ws...) })
}

// tagChip shows an @tag the prompts use.
func tagChip(gtx C, th *gui.Theme, tag string) D {
	gtx.Constraints.Min = image.Point{}
	h := gtx.Dp(26)
	pad := gtx.Dp(10)
	m := op.Record(gtx.Ops)
	d := gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, "@"+tag)
	call := m.Stop()
	w := d.Size.X + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.Surface)
	o := op.Offset(image.Pt(pad, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// ---- locations -------------------------------------------------------------------------------

type locUI struct {
	name, desc bound
	del        gui.Clickable
	img        gui.ImageView
}

func (s *step2) locHead(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	title := "Локації"
	if pv.P.Input.ContentMode() == studio.ContentMotion {
		title = "Фони й локації"
	}
	return sectionHead(gtx, th, title, locationsN(len(pv.P.Locations)),
		func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconMapPin, Text: "Додати локацію"}.Layout(gtx, th, &s.addLoc)
		})
}

func (s *step2) locations(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := &pv.P
	if len(p.Locations) == 0 {
		return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return gui.Text{Style: th.Body, Color: th.Pal.Text2}.Layout(gtx, th, "Локацій немає — місця дії ШІ опише в кожній сцені окремо.")
		})
	}
	return gui.Grid{MinWidth: 340, Gap: gui.Sp16, Stretch: true}.Layout(gtx, len(p.Locations), func(gtx C, i int) D {
		return s.locCard(gtx, c, pv, i)
	})
}

func (s *step2) locCard(gtx C, c *core.Ctx, pv *core.ProjectView, i int) D {
	th := c.Th
	id := pv.ID
	l := pv.P.Locations[i]
	lid := l.ID
	u := s.locs[lid]
	if u == nil {
		u = &locUI{}
		s.locs[lid] = u
	}
	fr := c.Frame()
	edit := func(fn func(l *studio.Location)) {
		c.Edit(id, func(p *studio.Project) {
			for k := range p.Locations {
				if p.Locations[k].ID == lid {
					fn(&p.Locations[k])
					return
				}
			}
		})
	}
	u.name.update(gtx, &s.ps.bind, fr, l.Name, func(v string) { edit(func(l *studio.Location) { l.Name = v }) })
	u.desc.update(gtx, &s.ps.bind, fr, l.Description, func(v string) { edit(func(l *studio.Location) { l.Description = v }) })
	if u.del.Clicked(gtx) {
		name := firstText(l.Name, "без назви")
		c.Confirm(core.Confirm{
			Title: "Видалити локацію «" + name + "»?", Text: "Сцени, що в ній відбуваються, залишаться без локації.",
			Icon: gui.IconTrash2, OK: "Видалити", Danger: true,
			OnOK: func() {
				c.Edit(id, func(p *studio.Project) {
					kept := p.Locations[:0:0]
					for _, x := range p.Locations {
						if x.ID != lid {
							kept = append(kept, x)
						}
					}
					p.Locations = kept
				})
			},
		})
	}
	ratio := pickers.AspectRatio(pv.P.Input.Aspect)
	thumb := func(gtx C) D {
		w := gtx.Dp(112)
		if ratio < 1 {
			w = gtx.Dp(72)
		}
		gtx.Constraints = layout.Exact(image.Pt(w, int(float32(w)/ratio)))
		t := gui.Thumb{Ratio: ratio, Radius: 12, Placeholder: gui.IconMountain}
		if l.Image.Path == "" {
			return t.Layout(gtx, th)
		}
		return u.img.Layout(gtx, th, c.Img, pv.Abs(l.Image.Path), l.Image.UpdatedAt, t)
	}
	top := func(gtx C) D {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(thumb),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return u.name.Layout(gtx, th, gui.FieldStyle{Placeholder: "Назва місця", Style: th.BodyStrong})
					},
					func(gtx C) D {
						return tagRow(gtx, th, l.Tag, fromPhoto(&pv.P, l.MaterialID))
					},
				)
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return gui.IconButton{Icon: gui.IconTrash2, Size: 36, Tip: "Видалити локацію"}.Layout(gtx, th, &u.del)
			}),
		)
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16, top, func(gtx C) D {
			return u.desc.Layout(gtx, th, gui.FieldStyle{Label: "Як виглядає (англ.)",
				Placeholder: "An old wooden cabin in a misty pine forest, warm light in the windows…", MinLines: 3, MaxLines: 8})
		})
	})
}

// tagRow shows the @tag and, for cast and places from the user's materials,
// a "з твого фото" badge.
func tagRow(gtx C, th *gui.Theme, tag string, photo bool) D {
	var ws []layout.Widget
	if tag != "" {
		ws = append(ws, func(gtx C) D { return tagChip(gtx, th, tag) })
	}
	if photo {
		ws = append(ws, func(gtx C) D { return photoBadge(gtx, th) })
	}
	if len(ws) == 0 {
		return D{}
	}
	return gui.Flow{Gap: gui.Sp8, RowGap: 6}.Layout(gtx, ws...)
}

// photoBadge marks a character or place that comes from the user's photo.
func photoBadge(gtx C, th *gui.Theme) D {
	gtx.Constraints.Min = image.Point{}
	h := gtx.Dp(26)
	padL, padR, gap := gtx.Dp(8), gtx.Dp(10), gtx.Dp(6)
	is := gtx.Dp(14)
	m := op.Record(gtx.Ops)
	d := gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.ActionText, MaxLines: 1}.Layout(gtx, th, "з твого фото")
	call := m.Stop()
	w := padL + is + gap + d.Size.X + padR
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.ActionSoft)
	o := op.Offset(image.Pt(padL, (h-is)/2)).Push(gtx.Ops)
	gui.IconImage.Layout(gtx, 14, th.Pal.ActionText)
	o.Pop()
	o = op.Offset(image.Pt(padL+is+gap, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}
