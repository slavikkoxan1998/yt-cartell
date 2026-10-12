package montage

// Фінальне відео: "Змонтувати відео" with the job's progress, then the
// inline player of the render, its length and size, open / reveal / save
// as / subtitles, and a hint when the film changed after the render.

import (
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type finalCard struct {
	video                       core.Video
	run, again, stop            gui.Clickable
	save, open, reveal          gui.Clickable
	srtSave, srtReveal, toSteps gui.Clickable
	ring                        gui.Anim
	size                        fileSize
}

func (s *proj) finalCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.fin
	p := &pv.P
	m := p.Montage
	out := m.Output
	id := pv.ID
	job := s.job(pv)
	running := s.running(pv) == studio.JobMontage || (out.Status == studio.StatusRunning && s.running(pv) != "")
	busy := s.running(pv) != ""
	usable := len(s.film.items) > 0
	done := out.Status == studio.StatusDone && out.Path != "" && !s.fake.fresh
	outdated := s.outdated(pv)
	abs := pv.Abs(out.Path)
	// The render's file was moved or deleted outside the app.
	missing := done && st.size.get(abs, out.UpdatedAt) == 0
	if missing {
		done = false
	}

	if (st.run.Clicked(gtx) || st.again.Clicked(gtx)) && usable && !busy {
		s.runMontage(c, pv)
	}
	if st.stop.Clicked(gtx) && running {
		c.E.Cancel(id)
	}
	if st.toSteps.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: id, Step: 3})
	}
	if done {
		if st.save.Clicked(gtx) {
			saveAs(c, abs, "Зберегти відео", fileName(p.Title)+".mp4", ".mp4")
		}
		if st.open.Clicked(gtx) {
			c.OpenPath(abs)
		}
		if st.reveal.Clicked(gtx) {
			c.Reveal(abs)
		}
	}
	if m.SRTPath != "" {
		srt := pv.Abs(m.SRTPath)
		if st.srtSave.Clicked(gtx) {
			saveAs(c, srt, "Зберегти субтитри", fileName(p.Title)+".srt", ".srt")
		}
		if st.srtReveal.Clicked(gtx) {
			c.Reveal(srt)
		}
	}

	status := func(gtx C) D {
		switch {
		case running:
			txt := "Монтуємо"
			if pct := jobPct(job, studio.JobMontage); pct > 0 {
				txt = fmt.Sprintf("Монтуємо %d%%", pct)
			}
			return gui.StatusPill{State: gui.StateRunning, Text: txt}.Layout(gtx, th)
		case missing:
			return gui.StatusPill{State: gui.StateStale, Text: "Файлу немає"}.Layout(gtx, th)
		case done && outdated:
			return gui.StatusPill{State: gui.StateStale, Text: "Є нові зміни"}.Layout(gtx, th)
		case done:
			return gui.StatusPill{State: gui.StateDone}.Layout(gtx, th)
		case out.Status == studio.StatusError:
			return gui.StatusPill{State: gui.StateError}.Layout(gtx, th)
		}
		return gui.StatusPill{State: gui.StateEmpty}.Layout(gtx, th)
	}
	ratio := ratioOf(p.Input.Aspect)
	vertical := ratio < 1

	box := func(gtx C) D {
		w := gtx.Constraints.Max.X
		if vertical {
			w = min(w, gtx.Dp(300))
		}
		h := int(float32(w) / ratio)
		size := pt(w, h)
		if done && !running {
			poster, posterVer := s.posterFor(pv)
			g := gtx
			g.Constraints = layout.Exact(size)
			return st.video.Layout(g, c, core.VideoStyle{Path: abs, Ver: out.UpdatedAt, Poster: poster, PosterVer: posterVer, Ratio: ratio})
		}
		// A calm well of the player's size, so the card never jumps (taller
		// only when its content needs it: narrow columns).
		content := func(gtx C) D {
			switch {
			case running:
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.ProgressRing{Value: core.Progress(job), Size: 96, Caption: "монтаж", Anim: &st.ring}.Widget(th)),
					layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return centerText(gtx, th, th.Body, th.Pal.Text, jobLine(job, "Склеюємо сцени, звук і субтитри"))
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconCircleStop, Text: "Зупинити"}.Layout(gtx, th, &st.stop)
					}),
				)
			case missing:
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.IconFileVideo.Widget(32, th.Pal.Text3)),
					layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						return centerText(gtx, th, th.H3, th.Pal.Text, "Файл відео не знайдено")
					}),
					layout.Rigid(func(gtx C) D {
						return centerText(gtx, th, th.Small, th.Pal.Text2, "Його могли перемістити чи видалити. Змонтуй ще раз — це безкоштовно.")
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Icon: gui.IconRefreshCw, Text: "Змонтувати заново", Disabled: busy || !usable}.Layout(gtx, th, &st.run)
					}),
				)
			case out.Status == studio.StatusError:
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.IconAlertTriangle.Widget(32, th.Pal.ErrorInk)),
					layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D { return centerText(gtx, th, th.H3, th.Pal.Text, "Монтаж не вдався") }),
					layout.Rigid(func(gtx C) D {
						msg := strings.TrimSpace(out.Error)
						if msg == "" {
							msg = "Спробуй ще раз — або перевір FFmpeg у налаштуваннях."
						}
						g := gtx
						if h := studio.FailureHint(msg); h != "" {
							msg += "\nПідказка: " + h
						}
						return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 10, Align: text.Middle}.Layout(g, th, msg)
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Icon: gui.IconRefreshCw, Text: "Спробувати ще раз", Disabled: busy || !usable}.Layout(gtx, th, &st.run)
					}),
				)
			case !usable:
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.IconImages.Widget(32, th.Pal.Text3)),
					layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						return centerText(gtx, th, th.H3, th.Pal.Text, "Спершу потрібні кадри")
					}),
					layout.Rigid(func(gtx C) D {
						return centerText(gtx, th, th.Small, th.Pal.Text2, "Згенеруй кадри сцен (крок 3) або кліпи (крок 4) — і фільм можна буде змонтувати.")
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: "До розкадровки", Trail: gui.IconArrowRight}.Layout(gtx, th, &st.toSteps)
					}),
				)
			}
			return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					d := gtx.Dp(64)
					fillCircle(gtx, float32(d)/2, float32(d)/2, float32(d)/2, th.Pal.ActionSoft)
					is := gtx.Dp(28)
					o := opOffset(gtx, (d-is)/2, (d-is)/2)
					gui.IconClapperboard.Layout(gtx, 28, th.Pal.ActionText)
					o.Pop()
					return D{Size: pt(d, d)}
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout),
				layout.Rigid(func(gtx C) D {
					return centerText(gtx, th, th.H3, th.Pal.Text, "Фільм ще не змонтовано")
				}),
				layout.Rigid(func(gtx C) D {
					return centerText(gtx, th, th.Small, th.Pal.Text2, fmt.Sprintf("%s · ≈ %s. Займе від кількох секунд до кількох хвилин.",
						count(len(s.film.items), "сцена", "сцени", "сцен"), core.Clock(s.film.total)))
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
				layout.Rigid(func(gtx C) D {
					// «Змонтувати» here, or the owner's own editor next to it.
					run := func(gtx C) D {
						return gui.Button{Icon: gui.IconClapperboard, Text: "Змонтувати відео", Disabled: busy}.Layout(gtx, th, &st.run)
					}
					exp := func(gtx C) D { return s.exportButton(gtx, c, pv, gui.Md, gui.Secondary, false) }
					return gui.Flow{Gap: gui.Sp12, RowGap: gui.Sp12}.Layout(gtx, run, exp)
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D {
					return centerText(gtx, th, th.Caption, th.Pal.Text3, "або перенеси сцени, голос і музику в DaVinci Resolve, Premiere, CapCut…")
				}),
			)
		}
		cg := gtx
		cg.Constraints = layout.Constraints{Max: pt(max(1, min(w-gtx.Dp(24), gtx.Dp(440))), 1<<16)}
		cd, call := measure(cg, content)
		size.Y = max(h, cd.Size.Y+2*gtx.Dp(20))
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.Pal.Surface)
		o := opOffset(gtx, (size.X-cd.Size.X)/2, (size.Y-cd.Size.Y)/2)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: size}
	}

	meta := func(gtx C) D {
		if !done || running {
			note := gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Монтаж не витрачає кредитів: усе склеюється на твоєму комп'ютері через FFmpeg.")
			// Scenes that are not ready are listed with their errors; the
			// render asks what to do with them (gaps.go) — no silent stills.
			return gui.VStack(gtx, gui.Sp12, func(gtx C) D { return s.gapsNote(gtx, c, pv, false) }, note)
		}
		parts := []string{core.Clock(out.Duration)}
		if n := st.size.get(abs, out.UpdatedAt); n > 0 {
			parts = append(parts, bytesText(n))
		}
		parts = append(parts, p.Input.Aspect)
		switch m.Subtitles {
		case "burn":
			parts = append(parts, "субтитри у відео")
		case "srt":
			if m.SRTPath != "" {
				parts = append(parts, "субтитри .srt")
			}
		}
		parts = append(parts, "змонтовано "+core.Updated(out.UpdatedAt))
		return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Layout(gtx, th, strings.Join(parts, " · "))
	}
	buttons := func(gtx C) D {
		if !done || running {
			return D{}
		}
		ws := []layout.Widget{
			func(gtx C) D {
				return gui.Button{Icon: gui.IconDownload, Text: "Зберегти як…", Tip: "Зберегти MP4 у вибрану папку"}.Layout(gtx, th, &st.save)
			},
			func(gtx C) D { return s.exportButton(gtx, c, pv, gui.Md, gui.Tinted, false) },
			func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Icon: gui.IconExternalLink, Text: "Відкрити"}.Layout(gtx, th, &st.open)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Icon: gui.IconFolderOpen, Text: "Показати в папці"}.Layout(gtx, th, &st.reveal)
			},
		}
		if m.SRTPath != "" {
			ws = append(ws,
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Icon: gui.IconFileText, Text: "Субтитри .srt", Tip: "Зберегти файл субтитрів"}.Layout(gtx, th, &st.srtSave)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Icon: gui.IconFolder, Tip: "Показати .srt у папці"}.Layout(gtx, th, &st.srtReveal)
				})
		}
		return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, ws...)
	}
	// What the last render left out or replaced, with why.
	gapsDone := func(gtx C) D {
		if !done || running {
			return D{}
		}
		return s.gapsNote(gtx, c, pv, true)
	}
	again := func(gtx C) D {
		if !done || running {
			return D{}
		}
		kind := gui.Tinted
		note := "Змінились музика, переходи чи субтитри? Змонтуй ще раз — це безкоштовно."
		tone := gui.ToneNeutral
		if outdated {
			kind = gui.Primary
			note = "Після монтажу фільм змінився — змонтуй заново, щоб побачити зміни."
			tone = gui.ToneWarning
		}
		btn := func(gtx C) D {
			return gui.Button{Kind: kind, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Змонтувати заново", Disabled: busy}.Layout(gtx, th, &st.again)
		}
		if gtx.Constraints.Max.X < gtx.Dp(460) {
			return gui.VStack(gtx, gui.Sp8, func(gtx C) D { return infoRowNoPad(gtx, th, tone, note) }, func(gtx C) D { return layout.W.Layout(gtx, btn) })
		}
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D { return infoRowNoPad(gtx, th, tone, note) }),
			layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
			layout.Rigid(btn),
		)
	}

	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		header := func(gtx C) D {
			return cardHeader(gtx, th, gui.IconClapperboard, "Фінальне відео", "Готовий фільм з голосом, музикою й субтитрами.", status)
		}
		if vertical && gtx.Constraints.Max.X >= gtx.Dp(560) {
			return gui.VStack(gtx, gui.Sp16, header, func(gtx C) D {
				return layout.Flex{Alignment: layout.Start}.Layout(gtx,
					layout.Rigid(box),
					layout.Rigid(layout.Spacer{Width: gui.Sp20}.Layout),
					layout.Flexed(1, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, meta, buttons, again, gapsDone) }),
				)
			})
		}
		return gui.VStack(gtx, gui.Sp16, header, box, meta, buttons, again, gapsDone)
	})
}

// infoRowNoPad is a short secondary note (tone colours the text only).
func infoRowNoPad(gtx C, th *gui.Theme, tone gui.Tone, s string) D {
	col := th.Pal.Text2
	if tone == gui.ToneWarning {
		col = th.Pal.WarningInk
	}
	return gui.Text{Style: th.Small, Color: col, MaxLines: 3}.Layout(gtx, th, s)
}

// posterFor is the picture shown before the video plays: the finished
// cover, else the cover picture, else the first frame.
func (s *proj) posterFor(pv *core.ProjectView) (string, int64) {
	p := &pv.P
	if f := p.Montage.CoverFinal; f.Path != "" && f.Status == studio.StatusDone {
		return pv.Abs(f.Path), f.UpdatedAt
	}
	if cv := p.Montage.Cover; cv.Path != "" && cv.Status == studio.StatusDone {
		return pv.Abs(cv.Path), cv.UpdatedAt
	}
	for _, it := range s.film.items {
		if sc := sceneOf(pv, it.SceneID); sc != nil && sc.Frame.Path != "" {
			return pv.Abs(sc.Frame.Path), sc.Frame.UpdatedAt
		}
	}
	return "", 0
}

// ---- files -------------------------------------------------------------------------------------------

// saveAs asks where to save a copy of src (default name, extension ext).
func saveAs(c *core.Ctx, src, title, defaultName, ext string) {
	c.PickSave(title, defaultName, func(dst string) {
		if filepath.Ext(dst) == "" {
			dst += ext
		}
		c.Do(func() error { return copyFile(src, dst) }, func() { c.Toast(gui.ToneSuccess, "Збережено: "+filepath.Base(dst)) })
	})
}

// copyFile copies src to dst through a temporary file next to dst.
func copyFile(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("не вдалося відкрити файл: %w", err)
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("не вдалося створити файл: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("не вдалося скопіювати: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Remove(dst)
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("не вдалося зберегти (файл відкрито в іншій програмі?): %w", err)
	}
	return nil
}

var _ = media.DefaultEffect
