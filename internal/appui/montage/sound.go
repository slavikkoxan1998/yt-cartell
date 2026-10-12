package montage

// Звук: the background music (pick / replace / remove / listen), its level,
// ducking under the voice, the level of the clips' own sound, and a line
// about the voice-over.

import (
	"context"
	"image"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

type soundCard struct {
	busy    bool // a music file is being copied in
	pick    gui.Clickable
	replace gui.Clickable
	remove  gui.Clickable
	pill    gui.AudioPill
	vol     valSlider
	amb     valSlider
	duck    gui.Toggle

	durPath string // music whose length is known / being probed
	dur     time.Duration
}

// musicExts are the audio files SetMusic accepts.
var musicExts = []string{".mp3", ".wav", ".m4a", ".aac", ".ogg", ".oga", ".opus", ".flac", ".wma"}

// pickMusic asks for a music file and copies it into the project.
func (s *proj) pickMusic(c *core.Ctx, pv *core.ProjectView) {
	if s.sound.busy {
		return
	}
	id := pv.ID
	replace := pv.P.Montage.MusicPath != ""
	c.PickFile("Вибери фонову музику", musicExts, func(path string) {
		s.sound.busy = true
		c.Background(func() {
			err := c.E.SetMusic(id, path)
			c.Post(func() { s.sound.busy = false })
			if err != nil {
				c.Error(err)
				return
			}
			if replace {
				c.Toast(gui.ToneSuccess, "Музику замінено")
			} else {
				c.Toast(gui.ToneSuccess, "Музику додано — вона зациклиться на весь фільм")
			}
		})
	})
}

func (s *proj) soundCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.sound
	m := pv.P.Montage
	id := pv.ID
	has := m.MusicPath != ""
	if st.pick.Clicked(gtx) || st.replace.Clicked(gtx) {
		s.pickMusic(c, pv)
	}
	if st.remove.Clicked(gtx) && has {
		c.Audio.Stop()
		c.Do(func() error { return c.E.SetMusic(id, "") }, func() { c.Toast(gui.ToneInfo, "Музику прибрано") })
	}
	if v, ok := st.vol.Commit(gtx); ok {
		lv := round2(float64(v))
		c.Edit(id, func(p *studio.Project) { p.Montage.MusicVolume = lv })
	}
	if v, ok := st.amb.Commit(gtx); ok {
		lv := round2(float64(v))
		c.Edit(id, func(p *studio.Project) { p.Montage.AmbientVolume = lv })
	}
	if v, ok := st.duck.Changed(gtx); ok {
		c.Edit(id, func(p *studio.Project) { p.Montage.Ducking = v })
	}
	src := pv.Abs(m.MusicPath)
	s.probeMusic(c, src)

	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return gui.VStack(gtx, gui.Sp20,
			func(gtx C) D {
				return cardHeader(gtx, th, gui.IconVolume2, "Звук", "Фонова музика, гучність і приглушення під голосом.", nil)
			},
			func(gtx C) D {
				if !has {
					return s.musicEmpty(gtx, c)
				}
				name := strings.TrimSpace(m.MusicName)
				if name == "" {
					name = filepath.Base(m.MusicPath)
				}
				return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
					func(gtx C) D {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
						return st.pill.Layout(gtx, th, gui.AudioStyle{Src: src, Label: name, Duration: st.dur, Backend: c.Audio})
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Замінити", Busy: st.busy}.Layout(gtx, th, &st.replace)
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconTrash2, Tip: "Прибрати музику"}.Layout(gtx, th, &st.remove)
					},
				)
			},
			func(gtx C) D {
				v := st.vol.Value(float32(m.MusicVolume))
				return fieldLabel(gtx, th, "Гучність музики", pctText(float64(v)), func(gtx C) D {
					return st.vol.Layout(gtx, th, gui.SliderStyle{Value: float32(m.MusicVolume), Min: 0, Max: 1, Step: 0.01, Disabled: !has,
						Format: func(v float32) string { return pctText(float64(v)) }})
				})
			},
			func(gtx C) D {
				st.duck.Disabled = !has
				return st.duck.Layout(gtx, th, m.Ducking, "Приглушувати музику, коли звучить голос")
			},
			func(gtx C) D {
				v := st.amb.Value(float32(m.AmbientVolume))
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return fieldLabel(gtx, th, "Звук самих кліпів (атмосфера)", pctText(float64(v)), func(gtx C) D {
							return st.amb.Layout(gtx, th, gui.SliderStyle{Value: float32(m.AmbientVolume), Min: 0, Max: 1, Step: 0.05,
								Format: func(v float32) string { return pctText(float64(v)) }})
						})
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
					layout.Rigid(hint(th, "0% — лише голос і музика. Кадри без кліпу звучать тишею.")),
				)
			},
			func(gtx C) D { return s.voiceNote(gtx, c, pv) },
		)
	})
}

// musicEmpty invites to add music (a soft dashed well with a button).
func (s *proj) musicEmpty(gtx C, c *core.Ctx) D {
	th := c.Th
	st := &s.sound
	w := gtx.Constraints.Max.X
	pad := gtx.Dp(16)
	g := gtx
	g.Constraints = layout.Constraints{Min: pt(w-2*pad, 0), Max: pt(w-2*pad, 1<<16)}
	d, call := measure(g, func(gtx C) D {
		text := func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(gui.Text{Style: th.Label, MaxLines: 1}.Widget(th, "Фонова музика")),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, "MP3, WAV, M4A, OGG або FLAC. Зациклиться на весь фільм і м'яко згасне наприкінці.")),
			)
		}
		btn := func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconPlus, Text: "Вибрати файл", Busy: st.busy}.Layout(gtx, th, &st.pick)
		}
		if gtx.Constraints.Max.X < gtx.Dp(380) {
			return gui.VStack(gtx, gui.Sp12, text, func(gtx C) D { return layout.W.Layout(gtx, btn) })
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				s := gtx.Dp(40)
				fillCircle(gtx, float32(s)/2, float32(s)/2, float32(s)/2, th.Pal.Card)
				is := gtx.Dp(20)
				o := op.Offset(pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
				gui.IconMusic.Layout(gtx, 20, th.Pal.Text2)
				o.Pop()
				return layout.Inset{Right: gui.Sp12}.Layout(gtx, func(gtx C) D { return D{Size: pt(s, s)} })
			}),
			layout.Flexed(1, text),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(btn),
		)
	})
	size := pt(w, d.Size.Y+2*pad)
	r := image.Rectangle{Max: size}
	fillRR(gtx, r, gtx.Dp(gui.RadiusInner), th.Pal.Surface)
	dashRR(gtx, r, gtx.Dp(gui.RadiusInner), th.Pal.BorderStrong)
	o := op.Offset(pt(pad, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// voiceNote summarises the voice-over.
func (s *proj) voiceNote(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := &pv.P
	switch {
	case !p.Input.Voiceover:
		return infoRow(gtx, th, gui.IconMic, gui.ToneNeutral, "Озвучку вимкнено на кроці «Сценарій» — у фільмі звучатимуть лише музика й кліпи.")
	case s.film.voices == 0:
		return infoRow(gtx, th, gui.IconMic, gui.ToneWarning, "Озвучки ще немає — згенеруй її на кроці «Відео й озвучка», і голос ляже на таймлайн.")
	}
	own := p.Input.Mode == studio.ModeVoice
	txt := "Озвучка: " + count(s.film.voices, "репліка", "репліки", "реплік") + " · " + core.Clock(s.film.speech) + " голосу. Натисни на репліку на таймлайні, щоб послухати."
	if own {
		txt = "Твоя озвучка нарізана на " + count(s.film.voices, "репліку", "репліки", "реплік") + " · " + core.Clock(s.film.speech) + ". Натисни на репліку на таймлайні, щоб послухати."
	}
	return infoRow(gtx, th, gui.IconMic, gui.ToneNeutral, txt)
}

// probeMusic learns the music's length once (ffprobe, in the background).
func (s *proj) probeMusic(c *core.Ctx, src string) {
	st := &s.sound
	if src == st.durPath {
		return
	}
	st.durPath, st.dur = src, 0
	if src == "" || c.Offline {
		return
	}
	ff := c.E.FF()
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := ff.Probe(ctx, src)
		if err != nil || info.Duration <= 0 {
			return
		}
		c.Post(func() {
			if st.durPath == src {
				st.dur = time.Duration(info.Duration * float64(time.Second))
			}
		})
	})
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
