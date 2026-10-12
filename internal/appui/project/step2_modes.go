package project

// Step 2 for the sound and content modes (studio.Input.AudioMode, Content):
// the spoken lines of a scene («Голос у відео»: who says what, and how), the
// animated on-screen text (Scene.OnScreen) and the words the cast section
// uses when the film shows objects instead of (or beside) people.

import (
	"image"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// lineUI is the widget state of one spoken line.
type lineUI struct {
	speaker        gui.Select
	text, delivery bound
	del            gui.Clickable
}

// onScreenUI is the widget state of a scene's on-screen text.
type onScreenUI struct {
	title, value, caption bound
	anim                  gui.Select
}

// castWords are the cast section's words for the content mode.
type castWords struct {
	title, add, empty string
	icon              *gui.Icon
	objects, people   bool // the section adds objects / people
}

func castWordsOf(in studio.Input) castWords {
	switch in.ContentMode() {
	case studio.ContentNoPeople:
		return castWords{title: "Об'єкти й мотиви", add: "Додати об'єкт", icon: gui.IconShapes, objects: true,
			empty: "Об'єктів немає — кадри складаються з місць і деталей. Додай предмет, який має повторюватися."}
	case studio.ContentMotion:
		return castWords{title: "Мотиви графіки", add: "Додати мотив", icon: gui.IconShapes, objects: true,
			empty: "Мотивів немає — кожна сцена намалює свою графіку. Додай іконку чи персонажа-маскота, щоб вони повторювалися."}
	case studio.ContentMixed:
		return castWords{title: "Персонажі й об'єкти", add: "Додати об'єкт", icon: gui.IconShapes, objects: true, people: true,
			empty: "Ні персонажів, ні об'єктів — сцени будуть з місць і деталей. Додай, якщо потрібно."}
	}
	return castWords{title: "Акторський склад", add: "Додати персонажа", icon: gui.IconUserPlus, people: true,
		empty: "Персонажів немає — сцени будуть без героїв. Додай, якщо потрібно."}
}

// objectsN is "3 об'єкти".
func objectsN(n int) string { return core.Count(n, "об'єкт", "об'єкти", "об'єктів") }

// castMeta is the count line of the cast section.
func castMeta(p *studio.Project) string {
	people, objects := 0, 0
	for _, c := range p.Characters {
		if c.IsObject() {
			objects++
		} else {
			people++
		}
	}
	switch {
	case objects == 0 && people == 0 && p.Input.NoPeople():
		return objectsN(0)
	case objects == 0:
		return charactersN(people)
	case people == 0:
		return objectsN(objects)
	}
	return charactersN(people) + " · " + objectsN(objects)
}

// ---- spoken lines ------------------------------------------------------------------------------

// linesBlock edits the scene's spoken lines: speaker, words, delivery; a
// meter of how long they take against the clip.
func (s *step2) linesBlock(gtx C, c *core.Ctx, pv *core.ProjectView, k int, u *sceneUI) D {
	th := c.Th
	id := pv.ID
	p := &pv.P
	sc := p.Scenes[k]
	sid := sc.ID
	fr := c.Frame()
	edit := func(fn func(s *studio.Scene)) {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ID == sid {
					fn(&p.Scenes[i])
					return
				}
			}
		})
	}
	for len(u.lines) < len(sc.Lines) {
		u.lines = append(u.lines, &lineUI{})
	}
	speakers := []gui.SelectOption{{Value: "narrator", Label: "Голос за кадром", Icon: gui.IconMic}}
	if !p.Input.NoPeople() {
		for _, ch := range p.Characters {
			if !ch.IsObject() {
				speakers = append(speakers, gui.SelectOption{Value: ch.ID, Label: firstText(ch.Name, "Персонаж"), Icon: gui.IconUserRound})
			}
		}
	}
	if u.addLine.Clicked(gtx) && len(sc.Lines) < 8 {
		who := "narrator"
		for _, cid := range sc.CharacterIDs {
			if ch := pv.Character(cid); ch != nil && !ch.IsObject() {
				who = cid
				break
			}
		}
		edit(func(s *studio.Scene) { s.Lines = append(s.Lines, studio.Line{Speaker: who}) })
	}
	var rows []layout.Widget
	for i := range sc.Lines {
		i := i
		l := sc.Lines[i]
		lu := u.lines[i]
		setLine := func(fn func(l *studio.Line)) {
			edit(func(s *studio.Scene) {
				if i < len(s.Lines) {
					fn(&s.Lines[i])
				}
			})
		}
		lu.text.update(gtx, &s.ps.bind, fr, l.Text, func(v string) { setLine(func(l *studio.Line) { l.Text = v }) })
		lu.delivery.update(gtx, &s.ps.bind, fr, l.Delivery, func(v string) { setLine(func(l *studio.Line) { l.Delivery = v }) })
		if v, ok := lu.speaker.Changed(gtx); ok && v != l.Speaker {
			setLine(func(l *studio.Line) { l.Speaker = v })
		}
		if lu.del.Clicked(gtx) {
			edit(func(s *studio.Scene) {
				if i < len(s.Lines) {
					s.Lines = append(s.Lines[:i:i], s.Lines[i+1:]...)
				}
			})
			u.lines = append(u.lines[:i:i], u.lines[i+1:]...) // the states follow their lines
			break
		}
		rows = append(rows, func(gtx C) D {
			who := func(gtx C) D {
				return lu.speaker.Layout(gtx, th, gui.SelectStyle{Options: speakers, Value: l.Speaker, Leading: gui.IconMessageCircle})
			}
			how := func(gtx C) D {
				return lu.delivery.Layout(gtx, th, gui.FieldStyle{Placeholder: "Як: пошепки, радісно… (англ.)", Style: th.Small.Sized(14)})
			}
			del := func(gtx C) D {
				return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Прибрати репліку"}.Layout(gtx, th, &lu.del)
			}
			head := func(gtx C) D {
				if gtx.Constraints.Max.X < gtx.Dp(360) {
					return gui.VStack(gtx, gui.Sp8, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Flexed(1, who), layout.Rigid(del))
					}, how)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, who),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Flexed(1, how),
					layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
					layout.Rigid(del),
				)
			}
			text := func(gtx C) D {
				return lu.text.Layout(gtx, th, gui.FieldStyle{Placeholder: "Що звучить у кадрі…", MinLines: 1, MaxLines: 5})
			}
			return gui.VStack(gtx, gui.Sp8, head, text)
		})
	}
	label := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.IconMessageCircle.Widget(16, th.Pal.Text2)),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, "Репліки в кадрі")),
		)
	}
	ws := []layout.Widget{label}
	if len(rows) == 0 {
		ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Без слів — сцена говорить картинкою й звуками."))
	}
	ws = append(ws, rows...)
	foot := func(gtx C) D {
		add := func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconPlus, Text: "Додати репліку", Disabled: len(sc.Lines) >= 8}.Layout(gtx, th, &u.addLine)
		}
		if len(sc.Lines) == 0 {
			return layout.W.Layout(gtx, add)
		}
		var texts []studio.Line
		for i, l := range sc.Lines {
			if i < len(u.lines) {
				l.Text = u.lines[i].text.text() // what the user sees, while typing too
			}
			texts = append(texts, l)
		}
		secs := studio.DialogueClipSeconds(texts) // slow speech and the beats around, as the clip length counts it
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D { return meter(gtx, th, secs, clipOf(sc, s.ps.f), sc.ClipLen > 0) }),
			layout.Rigid(add),
		)
	}
	ws = append(ws, foot)
	return gui.VStack(gtx, gui.Sp8, ws...)
}

// ---- on-screen text -----------------------------------------------------------------------------

var animOptions = []gui.SelectOption{
	{Value: "", Label: "Анімація: авто", Icon: gui.IconSparkles},
	{Value: studio.OverlayFade, Label: "Поява", Icon: gui.IconSparkle},
	{Value: studio.OverlaySlide, Label: "Виїзд збоку", Icon: gui.IconArrowRight},
	{Value: studio.OverlayPop, Label: "Пружний «поп»", Icon: gui.IconCircleDot},
	{Value: studio.OverlayCount, Label: "Лічильник числа", Icon: gui.IconHash},
	{Value: studio.OverlayType, Label: "Слово за словом", Icon: gui.IconType},
}

// onScreenBlock edits the scene's on-screen text: headline, number, caption
// and the animation.
func (s *step2) onScreenBlock(gtx C, c *core.Ctx, pv *core.ProjectView, k int, u *sceneUI) D {
	th := c.Th
	id := pv.ID
	sc := pv.P.Scenes[k]
	sid := sc.ID
	fr := c.Frame()
	o := &u.screen
	edit := func(fn func(o *studio.OnScreen)) {
		c.Edit(id, func(p *studio.Project) {
			for i := range p.Scenes {
				if p.Scenes[i].ID == sid {
					fn(&p.Scenes[i].OnScreen)
					return
				}
			}
		})
	}
	o.title.update(gtx, &s.ps.bind, fr, sc.OnScreen.Title, func(v string) { edit(func(o *studio.OnScreen) { o.Title = v }) })
	o.value.update(gtx, &s.ps.bind, fr, sc.OnScreen.Value, func(v string) { edit(func(o *studio.OnScreen) { o.Value = v }) })
	o.caption.update(gtx, &s.ps.bind, fr, sc.OnScreen.Caption, func(v string) { edit(func(o *studio.OnScreen) { o.Caption = v }) })
	if v, ok := o.anim.Changed(gtx); ok && v != sc.OnScreen.Anim {
		edit(func(o *studio.OnScreen) { o.Anim = v })
	}
	small := th.Small.Sized(14)
	title := func(gtx C) D {
		return o.title.Layout(gtx, th, gui.FieldStyle{Placeholder: "Заголовок (до 6 слів)", Style: small})
	}
	value := func(gtx C) D {
		return o.value.Layout(gtx, th, gui.FieldStyle{Placeholder: "Число чи факт: 73%", Style: small})
	}
	caption := func(gtx C) D {
		return o.caption.Layout(gtx, th, gui.FieldStyle{Placeholder: "Підпис (необов'язково)", Style: small})
	}
	anim := func(gtx C) D {
		return o.anim.Layout(gtx, th, gui.SelectStyle{Options: animOptions, Value: sc.OnScreen.Anim, Leading: gui.IconSparkles})
	}
	pair := func(a, b layout.Widget) layout.Widget {
		return func(gtx C) D {
			if gtx.Constraints.Max.X < gtx.Dp(340) {
				return gui.VStack(gtx, gui.Sp8, a, b)
			}
			return gui.Columns{Gap: gui.Sp8}.Layout(gtx, gui.Col{Span: 3, W: a}, gui.Col{Span: 2, W: b})
		}
	}
	label := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.IconType.Widget(16, th.Pal.Text2)),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, "Текст у кадрі")),
			layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "анімує студія")),
		)
	}
	return gui.Card{Tint: true, Radius: gui.RadiusInner, Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return gui.VStack(gtx, gui.Sp8, label, pair(title, value), pair(caption, anim))
	})
}

// ---- meters ---------------------------------------------------------------------------------------

// meter shows speech seconds against the clip: a short bar and "≈ 5,2 с із
// 8 с" ("≈ 5,2 с · кліп 6 с" for a clip chosen for the line); too long turns
// it red with a hint.
func meter(gtx C, th *gui.Theme, speech, clip float64, chosen bool) D {
	over := clip > 0 && speech > clip-0.3
	tone, txt := gui.ToneSuccess, th.Pal.Text2
	if over {
		tone, txt = gui.ToneError, th.Pal.ErrorInk
	}
	frac := float32(0)
	if clip > 0 {
		frac = float32(min(1, speech/clip))
	}
	sec := strings.Replace(strconv.FormatFloat(speech, 'f', 1, 64), ".", ",", 1)
	label := "≈ " + sec + " с із " + strconv.Itoa(int(clip)) + " с"
	if chosen {
		label = "≈ " + sec + " с · кліп " + strconv.Itoa(int(clip)) + " с"
	}
	ch := []layout.FlexChild{
		layout.Rigid(func(gtx C) D {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(96), gtx.Dp(6)))
			return gui.ProgressBar{Value: frac, Height: 6, Tone: tone}.Layout(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Rigid(gui.Text{Style: th.Caption.Weighted(gui.FontMedium), Color: txt, MaxLines: 1}.Widget(th, label)),
	}
	if over {
		ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Caption, Color: txt, MaxLines: 2}.Widget(th, "довше за кліп — скороти")))
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
}

// objectThumbIcon is the placeholder of a cast card: a person or an object.
func objectThumbIcon(ch studio.Character) *gui.Icon {
	if ch.IsObject() {
		return gui.IconShapes
	}
	return gui.IconUserRound
}

// kindBadge marks an object in a mixed cast.
func kindBadge(gtx C, th *gui.Theme) D {
	gtx.Constraints.Min = image.Point{}
	h := gtx.Dp(26)
	pad := gtx.Dp(10)
	m := op.Record(gtx.Ops)
	d := gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, "об'єкт")
	call := m.Stop()
	w := d.Size.X + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusPill, th.Pal.Surface)
	o := op.Offset(image.Pt(pad, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}
