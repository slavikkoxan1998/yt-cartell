package dialogs

// «Налаштувати з опису»: the user describes their channel in free words and
// the text model fills the whole profile (Engine.SuggestProfileWithReasons).
// The editor shows the result for review — the fields it changed are
// highlighted and the reasons listed — and nothing is saved until
// «Зберегти профіль».

import (
	"context"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

type aiSetup struct {
	open    bool // the block is expanded
	toggle  gui.Clickable
	desc    gui.TextField
	run     gui.Clickable
	busy    bool
	err     string
	reasons []string
	changed map[string]bool
	seq     int
	show    gui.Anim
}

// reset clears the block for a newly opened editor (a late answer of an
// earlier request is dropped by seq).
func (a *aiSetup) reset(expanded bool) {
	a.open, a.busy, a.err = expanded, false, ""
	a.reasons, a.changed = nil, nil
	a.desc.SetText("")
	a.seq++
}

// touch drops the highlight of a field the user changed by hand.
func (s *profileSheet) touch(key string) { delete(s.ai.changed, key) }

func (s *profileSheet) aiUpdate(gtx C, c *core.Ctx) {
	if s.ai.toggle.Clicked(gtx) {
		s.ai.open = !s.ai.open
	}
	if s.ai.run.Clicked(gtx) {
		s.suggest(c)
	}
	if _, ok := s.ai.desc.Changed(gtx); ok {
		s.ai.err = ""
	}
}

// suggest asks the text model for a profile in the background.
func (s *profileSheet) suggest(c *core.Ctx) {
	if s.ai.busy {
		return
	}
	desc := strings.TrimSpace(s.ai.desc.Editor.Text())
	if desc == "" {
		s.ai.err = "Опиши хоча б кількома словами, які відео ти робиш."
		return
	}
	if c.Offline {
		s.ai.err = "Підбір недоступний у демо-режимі."
		return
	}
	s.ai.busy, s.ai.err = true, ""
	s.ai.seq++
	seq := s.ai.seq
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p, reasons, err := c.E.SuggestProfileWithReasons(ctx, desc)
		c.Post(func() {
			if seq != s.ai.seq {
				return // the editor was closed or asked again
			}
			s.ai.busy = false
			if err != nil {
				s.ai.err = core.ErrorText(err)
				return
			}
			s.applySuggestion(p, reasons)
		})
	})
}

// applySuggestion puts a suggested profile into the draft (id, dates and a
// name the user typed are kept) and remembers which fields changed.
func (s *profileSheet) applySuggestion(p config.Profile, reasons []string) {
	old := s.draft
	d := p
	d.ID, d.CreatedAt, d.UpdatedAt = old.ID, old.CreatedAt, old.UpdatedAt
	typed := strings.TrimSpace(s.name.Editor.Text())
	if strings.TrimSpace(p.Name) == "" || (typed != "" && typed != newProfile().Name && !s.isNew) {
		d.Name = typed
	}
	if strings.TrimSpace(d.Name) == "" {
		d.Name = old.Name
	}
	ch := map[string]bool{}
	mark := func(key string, changed bool) {
		if changed {
			ch[key] = true
		}
	}
	mark("name", strings.TrimSpace(d.Name) != typed)
	mark("dur", d.TargetSeconds != old.TargetSeconds)
	mark("aspect", d.Aspect != old.Aspect)
	mark("lang", d.Language != old.Language)
	mark("style", d.Style != old.Style)
	mark("custom", d.CustomStyle != old.CustomStyle)
	mark("model", d.ImageModel != old.ImageModel)
	mark("camera", d.Camera != old.Camera)
	mark("genre", d.Genre != old.Genre)
	mark("tone", d.Tone != old.Tone)
	mark("tempo", d.Tempo != old.Tempo)
	mark("voiceover", d.Voiceover != old.Voiceover)
	mark("voice", d.NarratorVoiceID != old.NarratorVoiceID)
	mark("notes", d.Notes != old.Notes)
	mark("board", d.StoryboardMode != old.StoryboardMode)
	mark("chain", d.ChainFrames != old.ChainFrames)
	mark("trans", d.Transition != old.Transition)
	mark("transD", d.TransitionDuration != old.TransitionDuration)
	mark("effect", d.Effect != old.Effect)
	mark("filter", d.Filter != old.Filter)
	mark("subs", d.Subtitles != old.Subtitles)
	mark("subSt", d.SubtitlePreset != old.SubtitlePreset)
	mark("music", d.MusicVolume != old.MusicVolume)
	mark("amb", d.AmbientVolume != old.AmbientVolume)
	mark("duck", d.Ducking != old.Ducking)
	s.draft = d
	s.name.SetText(d.Name)
	s.custom.SetText(d.CustomStyle)
	s.notes.SetText(d.Notes)
	s.ai.changed, s.ai.reasons = ch, reasons
	s.nameErr = ""
	s.ai.show.Set(0)
}

// mark wraps a field: a violet ring and a sparkle badge while the AI's
// value is still in it.
func (s *profileSheet) mark(th *gui.Theme, key string, w layout.Widget) layout.Widget {
	return func(gtx C) D {
		d := w(gtx)
		if !s.ai.changed[key] {
			return d
		}
		// Tiles get a ring; text fields (label, box, counter) only the badge.
		if key != "name" && key != "custom" && key != "notes" {
			o := gtx.Dp(3)
			r := image.Rectangle{Min: image.Pt(-o, -o), Max: d.Size.Add(image.Pt(o, o))}
			gui.Border(gtx, r, gui.RadiusControl+3, 2, gui.WithAlpha(th.Pal.Running, 0.85))
		}
		b := gtx.Dp(22)
		off := op.Offset(image.Pt(d.Size.X-b+gtx.Dp(6), -gtx.Dp(8))).Push(gtx.Ops)
		gui.FillRRect(gtx, image.Rect(0, 0, b, b), gui.RadiusPill, th.Pal.Running)
		is := gtx.Dp(13)
		io := op.Offset(image.Pt((b-is)/2, (b-is)/2)).Push(gtx.Ops)
		gui.IconSparkles.LayoutStroke(gtx, 13, 2, gui.OnColor(th.Pal.Running))
		io.Pop()
		off.Pop()
		return d
	}
}

// aiBlock is the «Налаштувати з опису» panel at the top of the editor.
func (s *profileSheet) aiBlock(gtx C, th *gui.Theme) D {
	a := &s.ai
	head := func(gtx C) D {
		chev := gui.IconChevronDown
		if a.open {
			chev = gui.IconChevronUp
		}
		return tileRow(gtx, flatVisual(th, gui.IconWandSparkles),
			tileTexts(th, "Швидкий старт", "Налаштувати з опису", "Опиши свій канал — студія сама підбере всі поля нижче."),
			func(gtx C) D {
				return gui.IconButton{Icon: chev, Size: 36, Tip: "Показати або сховати"}.Layout(gtx, th, &a.toggle)
			})
	}
	return flatTile(gtx, th, gui.Sp16, false, func(gtx C) D {
		if !a.open {
			return head(gtx)
		}
		ws := []layout.Widget{head,
			func(gtx C) D {
				return a.desc.Layout(gtx, th, gui.FieldStyle{MinLines: 3, MaxLines: 6, MaxLen: 1500,
					Placeholder: "Опиши свій канал: про що відео, формат, тривалість, стиль, мова… Напр. «Казки на ніч для малечі 4–7 років, 3 хвилини, м'яка акварель, українською»"})
			},
			func(gtx C) D {
				btn := func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Icon: gui.IconWandSparkles, Text: "Підібрати налаштування", Busy: a.busy}.Layout(gtx, th, &a.run)
				}
				note := gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Нічого не збережеться, доки не натиснеш «Зберегти профіль».")
				if gtx.Constraints.Max.X < gtx.Dp(520) {
					return gui.VStack(gtx, gui.Sp8, func(gtx C) D { gtx.Constraints.Min.X = 0; return btn(gtx) }, note)
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(btn),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Flexed(1, note),
				)
			},
		}
		if a.err != "" {
			ws = append(ws, func(gtx C) D {
				return panel(gtx, th, gui.ToneError, gui.ToneError, gui.IconCircleAlert, "Не вийшло", a.err)
			})
		}
		if len(a.reasons) > 0 || len(a.changed) > 0 {
			ws = append(ws, func(gtx C) D {
				a.show.Duration = gui.DurDialog
				v := a.show.Animate(gtx, 1)
				return gui.FadeUp(gtx, v, 6, func(gtx C) D { return s.reasonsPanel(gtx, th) })
			})
		}
		return gui.VStack(gtx, gui.Sp12, ws...)
	})
}

// reasonsPanel is «Чому так»: what the model chose and why.
func (s *profileSheet) reasonsPanel(gtx C, th *gui.Theme) D {
	a := &s.ai
	pad := gtx.Dp(gui.Sp12 + 2)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		n := len(a.changed)
		title := "Чому так"
		sub := "Підсвічені поля підібрано за описом — перевір і збережи."
		if n == 0 {
			sub = "Нічого міняти не довелося — поля вже підходять до опису."
		}
		ch := []layout.FlexChild{
			layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: th.Pal.RunningInk}.Widget(th, title)),
			layout.Rigid(layout.Spacer{Height: 2}.Layout),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, sub)),
		}
		for _, r := range a.reasons {
			r := strings.TrimSpace(r)
			if r == "" {
				continue
			}
			ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout), layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						sz := gtx.Dp(6)
						o := op.Offset(image.Pt(0, gtx.Dp(7))).Push(gtx.Ops)
						defer o.Pop()
						gui.FillRRect(gtx, image.Rect(0, 0, sz, sz), gui.RadiusPill, th.Pal.Running)
						return D{Size: image.Pt(sz, sz+gtx.Dp(7))}
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Flexed(1, gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: th.Pal.Text}.Widget(th, r)),
				)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.RunningSoft)
	o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}
