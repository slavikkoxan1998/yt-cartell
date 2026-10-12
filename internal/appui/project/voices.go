package project

// «Голоси озвучки» (steps 1 and 2): the «Мультиголос» switch of the voice-over
// mode and, when it is on, a voice for every speaker — «Диктор», «Диктор 2/3»
// (added here) and the characters who speak — from the current TTS
// provider's voices, «Підібрати голоси» (gender, age and the film's language;
// studio.AssignVoices) and the speaker-coloured subtitles. Every scene's
// parts are edited on its card (partsBlock).

import (
	"context"
	"image"
	"image/color"
	"strconv"
	"strings"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// voicesCard is the widget state of «Голоси озвучки».
type voicesCard struct {
	multi, colors gui.Toggle
	narr          pickers.Voice
	extra         [studio.MaxExtraNarrators]pickers.Voice
	drop          [studio.MaxExtraNarrators]gui.Clickable
	add, auto     gui.Clickable
	chars         map[string]*pickers.Voice
	busy          bool
}

// multiVoiceShown reports whether the film can be multi-voiced: the voice-over
// mode read by TTS (never the user's own recording).
func multiVoiceShown(in studio.Input) bool {
	return in.Mode != studio.ModeVoice && in.SoundMode() == studio.AudioVoiceover
}

// layout draws the card (nothing outside the voice-over mode).
func (v *voicesCard) layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := &pv.P
	in := p.Input
	if !multiVoiceShown(in) {
		return D{}
	}
	v.handle(gtx, c, pv)
	on := in.MultiVoice
	note := "Увімкни «Мультиголос» — диктор і герої читатимуть свої репліки різними голосами"
	if on {
		note = "Диктор і герої читають свої репліки різними голосами"
	}
	toggle := func(gtx C) D {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.Label, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Мультиголос")),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D { return v.multi.Layout(gtx, th, on, "") }),
		)
	}
	head := func(gtx C) D {
		if gtx.Constraints.Max.X < gtx.Dp(520) { // a phone: the switch under the title
			return gui.VStack(gtx, gui.Sp12, cardTitle(th, gui.IconUsers, "Голоси озвучки", note, nil), toggle)
		}
		return cardTitle(th, gui.IconUsers, "Голоси озвучки", note, toggle)(gtx)
	}
	if !on {
		return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return head(gtx)
		})
	}

	// The speakers: narrators, then the characters who speak (in cast order).
	speakers := studio.NarratorIDs(in)
	speaks := map[string]bool{}
	parts := false
	for _, s := range p.Scenes {
		for _, l := range s.Parts {
			if strings.TrimSpace(l.Text) != "" {
				speaks[l.Speaker], parts = true, true
			}
		}
	}
	for _, ch := range p.Characters {
		if !ch.IsObject() && speaks[ch.ID] {
			speakers = append(speakers, ch.ID)
		}
	}
	colors := p.Montage.SubtitleStyle.SpeakerColors
	var tiles []layout.Widget
	for k, sp := range speakers {
		sp := sp
		var field func(gtx C) D
		switch {
		case sp == "narrator":
			field = func(gtx C) D { return v.narr.Layout(gtx, c, "Диктор", in.NarratorVoiceID) }
		case k <= studio.MaxExtraNarrators && studio.IsNarrator(sp):
			i := k - 1
			val := ""
			if i < len(in.ExtraNarrators) {
				val = in.ExtraNarrators[i]
			}
			v.extra[i].Default, v.extra[i].Tip = "Як у диктора", ""
			field = func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx C) D { return v.extra[i].Layout(gtx, c, studio.SpeakerLabel(p, sp), val) }),
					layout.Rigid(layout.Spacer{Width: gui.Sp4}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Прибрати «" + studio.SpeakerLabel(p, sp) + "»"}.Layout(gtx, th, &v.drop[i])
					}),
				)
			}
		default:
			u := v.charVoice(sp)
			ch := pv.Character(sp)
			if ch == nil {
				continue
			}
			field = func(gtx C) D { return u.Layout(gtx, c, firstText(ch.Name, "Персонаж"), ch.VoiceID) }
		}
		tiles = append(tiles, func(gtx C) D {
			if !colors {
				return field(gtx)
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return speakerDot(gtx, th, studio.SpeakerColor(p, sp)) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, field),
			)
		})
	}
	var notes []layout.Widget
	hint := func(icon *gui.Icon, text string, warn bool) layout.Widget {
		return func(gtx C) D {
			col := th.Pal.Text2
			if warn {
				col = th.Pal.WarningInk
			}
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 1}.Layout(gtx, icon.Widget(16, col)) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Small, Color: col, MaxLines: 3}.Widget(th, text)),
			)
		}
	}
	switch {
	case len(p.Scenes) == 0:
		notes = append(notes, hint(gui.IconSparkles, "План розпише текст між диктором і героями, а студія сама підбере кожному голос — за статтю, віком і мовою фільму. Змінити можна будь-коли.", false))
	case !parts:
		notes = append(notes, hint(gui.IconCircleAlert, "План складено без мультиголосу — онови план, щоб розписати текст між голосами.", true))
	}
	foot := func(gtx C) D {
		var btns []layout.Widget
		if len(in.ExtraNarrators) < studio.MaxExtraNarrators {
			btns = append(btns, func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconPlus, Text: "Додати диктора",
					Tip: "Ще один голос оповіді — для цитат, документів чи другого ведучого"}.Layout(gtx, th, &v.add)
			})
		}
		btns = append(btns, func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconWandSparkles, Text: "Підібрати голоси", Busy: v.busy,
				Tip: "Заново підібрати голоси всім мовцям — за статтю, віком і мовою фільму"}.Layout(gtx, th, &v.auto)
		})
		toggle := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, "Субтитри кольором мовця")),
				layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D { return v.colors.Layout(gtx, th, colors, "") }),
			)
		}
		return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, append(btns, toggle)...)
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ws := []layout.Widget{head}
		ws = append(ws, notes...)
		ws = append(ws, func(gtx C) D {
			return gui.Grid{MinWidth: 280, Gap: gui.Sp12, Stretch: true}.Layout(gtx, len(tiles), func(gtx C, i int) D { return tiles[i](gtx) })
		}, foot)
		return gui.VStack(gtx, gui.Sp16, ws...)
	})
}

// charVoice is the voice field of a character in the card.
func (v *voicesCard) charVoice(id string) *pickers.Voice {
	if v.chars == nil {
		v.chars = map[string]*pickers.Voice{}
	}
	u := v.chars[id]
	if u == nil {
		u = &pickers.Voice{Default: "Як у диктора"}
		v.chars[id] = u
	}
	return u
}

// handle applies the switches, picks and clicks of the last frame.
func (v *voicesCard) handle(gtx C, c *core.Ctx, pv *core.ProjectView) {
	id := pv.ID
	in := pv.P.Input
	if on, ok := v.multi.Changed(gtx); ok {
		editInput(c, id, func(in *studio.Input) { in.MultiVoice = on })
		if on && len(pv.P.Scenes) > 0 {
			v.autoPick(c, pv, false) // voices for the speakers still without one
		}
	}
	if on, ok := v.colors.Changed(gtx); ok {
		c.Edit(id, func(p *studio.Project) { p.Montage.SubtitleStyle.SpeakerColors = on })
	}
	if val, ok := v.narr.Changed(); ok {
		editInput(c, id, func(in *studio.Input) { in.NarratorVoiceID = val })
	}
	for i := range v.extra {
		if val, ok := v.extra[i].Changed(); ok {
			editInput(c, id, func(in *studio.Input) {
				if i < len(in.ExtraNarrators) {
					in.ExtraNarrators[i] = val
				}
			})
		}
		if v.drop[i].Clicked(gtx) {
			editInput(c, id, func(in *studio.Input) {
				if i < len(in.ExtraNarrators) {
					in.ExtraNarrators = append(in.ExtraNarrators[:i:i], in.ExtraNarrators[i+1:]...)
				}
			})
		}
	}
	for cid, u := range v.chars {
		if val, ok := u.Changed(); ok {
			c.Edit(id, func(p *studio.Project) {
				for k := range p.Characters {
					if p.Characters[k].ID == cid {
						p.Characters[k].VoiceID = val
					}
				}
			})
		}
	}
	if v.add.Clicked(gtx) && len(in.ExtraNarrators) < studio.MaxExtraNarrators {
		editInput(c, id, func(in *studio.Input) { in.ExtraNarrators = append(in.ExtraNarrators, "") })
		v.autoPick(c, pv, false)
	}
	if v.auto.Clicked(gtx) && !v.busy {
		voiced := 0
		for _, s := range pv.P.Scenes {
			if s.Audio.Status == studio.StatusDone && s.AudioSource != studio.AudioUpload {
				voiced++
			}
		}
		if voiced == 0 {
			v.autoPick(c, pv, true)
		} else {
			c.Confirm(core.Confirm{
				Title: "Підібрати голоси заново?",
				Text: "Кожен мовець отримає голос, підібраний за статтю, віком і мовою фільму. " + strconv.Itoa(voiced) +
					" вже озвучених сцен позначаться як змінені — їх треба буде переозвучити.",
				Icon: gui.IconWandSparkles, OK: "Підібрати",
				OnOK: func() { v.autoPick(c, pv, true) },
			})
		}
	}
}

// autoPick asks the studio for voices (all: every speaker again).
func (v *voicesCard) autoPick(c *core.Ctx, pv *core.ProjectView, all bool) {
	if c.Offline || c.E == nil {
		if all {
			c.Toast(gui.ToneError, offlineText)
		}
		return
	}
	id := pv.ID
	v.busy = true
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := c.E.AssignVoices(ctx, id, all)
		c.Post(func() {
			v.busy = false
			switch {
			case err != nil && all:
				c.Error(err)
			case err == nil && all:
				c.Toast(gui.ToneSuccess, "Голоси підібрано")
			}
		})
	})
}

// speakerDot is the subtitle colour of a speaker ("" = the style's own: a ring).
func speakerDot(gtx C, th *gui.Theme, hex string) D {
	sz := gtx.Dp(14)
	if col, ok := speakerRGB(hex); ok {
		return gui.FillCircle(gtx, 14, col)
	}
	gui.FillCircle(gtx, 14, th.Pal.Surface)
	gui.Border(gtx, image.Rect(0, 0, sz, sz), 7, 1.5, th.Pal.BorderStrong)
	return D{Size: image.Pt(sz, sz)}
}

// speakerRGB reads a #RRGGBB speaker colour.
func speakerRGB(hex string) (color.NRGBA, bool) {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) != 6 {
		return color.NRGBA{}, false
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, false
	}
	return color.NRGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}, true
}
