package project

// The voice-over of a multi-voice scene («Мультиголос»): its parts in order —
// who says it (a narrator or a character), the words and how they are said —
// with a meter of how long they take against the clip.

import (
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// maxParts mirrors the studio's limit of parts per scene.
const maxParts = 12

// partsBlock edits the scene's speaker parts.
func (s *step2) partsBlock(gtx C, c *core.Ctx, pv *core.ProjectView, k int, u *sceneUI) D {
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
	parts := sc.Parts
	if len(parts) == 0 && strings.TrimSpace(sc.Narration) != "" {
		// A scene planned before «Мультиголос»: its narration is the narrator's part.
		parts = []studio.Line{{Speaker: "narrator", Text: sc.Narration}}
	}
	for len(u.parts) < len(parts) {
		u.parts = append(u.parts, &lineUI{})
	}
	var speakers []gui.SelectOption
	for _, n := range studio.NarratorIDs(p.Input) {
		speakers = append(speakers, gui.SelectOption{Value: n, Label: studio.SpeakerLabel(p, n), Icon: gui.IconMic})
	}
	if !p.Input.NoPeople() {
		for _, ch := range p.Characters {
			if !ch.IsObject() {
				speakers = append(speakers, gui.SelectOption{Value: ch.ID, Label: firstText(ch.Name, "Персонаж"), Icon: gui.IconUserRound})
			}
		}
	}
	// Edits always write the whole list (a narration-only scene gets its part).
	setParts := func(fn func(ps []studio.Line) []studio.Line) {
		base := append([]studio.Line(nil), parts...)
		edit(func(s *studio.Scene) {
			if len(s.Parts) > 0 {
				base = append([]studio.Line(nil), s.Parts...)
			}
			s.Parts = fn(base)
			if len(s.Parts) == 0 {
				s.Narration = ""
			}
		})
	}
	if u.addPart.Clicked(gtx) && len(parts) < maxParts {
		who := "narrator"
		for _, cid := range sc.CharacterIDs { // someone in the shot answers
			if ch := pv.Character(cid); ch != nil && !ch.IsObject() {
				who = cid
				break
			}
		}
		setParts(func(ps []studio.Line) []studio.Line { return append(ps, studio.Line{Speaker: who, Text: "…"}) })
	}
	var rows []layout.Widget
	for i := range parts {
		i := i
		l := parts[i]
		lu := u.parts[i]
		set := func(fn func(l *studio.Line)) {
			setParts(func(ps []studio.Line) []studio.Line {
				if i < len(ps) {
					fn(&ps[i])
				}
				return ps
			})
		}
		lu.text.update(gtx, &s.ps.bind, fr, l.Text, func(v string) { set(func(l *studio.Line) { l.Text = v }) })
		lu.delivery.update(gtx, &s.ps.bind, fr, l.Delivery, func(v string) { set(func(l *studio.Line) { l.Delivery = v }) })
		if v, ok := lu.speaker.Changed(gtx); ok && v != l.Speaker {
			set(func(l *studio.Line) { l.Speaker = v })
		}
		if lu.del.Clicked(gtx) {
			setParts(func(ps []studio.Line) []studio.Line {
				if i < len(ps) {
					return append(ps[:i:i], ps[i+1:]...)
				}
				return ps
			})
			u.parts = append(u.parts[:i:i], u.parts[i+1:]...) // the states follow their parts
			break
		}
		color := ""
		if p.Montage.SubtitleStyle.SpeakerColors {
			color = studio.SpeakerColor(p, l.Speaker)
		}
		rows = append(rows, func(gtx C) D {
			who := func(gtx C) D {
				sel := func(gtx C) D {
					return lu.speaker.Layout(gtx, th, gui.SelectStyle{Options: speakers, Value: l.Speaker, Leading: gui.IconMessageCircle})
				}
				if !p.Montage.SubtitleStyle.SpeakerColors {
					return sel(gtx)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D { return speakerDot(gtx, th, color) }),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Flexed(1, sel),
				)
			}
			how := func(gtx C) D {
				return lu.delivery.Layout(gtx, th, gui.FieldStyle{Placeholder: "Як сказати (англ.)", Style: th.Small.Sized(14)})
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
				return lu.text.Layout(gtx, th, gui.FieldStyle{Placeholder: "Що звучить…", MinLines: 1, MaxLines: 6})
			}
			return gui.VStack(gtx, gui.Sp8, head, text)
		})
	}
	label := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.IconUsers.Widget(16, th.Pal.Text2)),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Flexed(1, gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, "Текст озвучки за голосами")),
			layout.Rigid(func(gtx C) D {
				if gtx.Constraints.Max.X < gtx.Dp(200) { // narrow: the title is enough
					return D{}
				}
				return gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Layout(gtx, th, "кожна репліка — своїм голосом")
			}),
		)
	}
	ws := []layout.Widget{label}
	if len(rows) == 0 {
		ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th, "Без голосу — сцена звучить музикою й звуками."))
	}
	ws = append(ws, rows...)
	ws = append(ws, func(gtx C) D {
		add := func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconPlus, Text: "Додати репліку", Disabled: len(parts) >= maxParts}.Layout(gtx, th, &u.addPart)
		}
		if len(parts) == 0 {
			return layout.W.Layout(gtx, add)
		}
		var texts []string
		for i, l := range parts {
			t := l.Text
			if i < len(u.parts) {
				t = u.parts[i].text.text() // what the user sees, while typing too
			}
			texts = append(texts, t)
		}
		secs := studio.SpeechSeconds(strings.Join(texts, " ")) + 0.3*float64(len(texts)-1) // + the pauses between voices
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D { return meter(gtx, th, secs, clipOf(sc, s.ps.f), sc.ClipLen > 0) }),
			layout.Rigid(add),
		)
	})
	return gui.VStack(gtx, gui.Sp8, ws...)
}
