package montage

// Context column cards of the step: a summary of what the render will be
// (length, scenes, format, look, sound, subtitles) and the cover status.

import (
	"strings"

	"gioui.org/layout"
	"gioui.org/text"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type sideCards struct {
	toCover gui.Clickable
	toFinal gui.Clickable
	thumb   gui.ImageView
}

func (s *proj) sideCards(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	return gui.VStack(gtx, gui.Sp16, // the context column's rhythm
		func(gtx C) D { return s.summaryCard(gtx, c, pv) },
		func(gtx C) D { return s.coverStatusCard(gtx, c, pv) },
	)
}

// summaryCard lists what the render will contain.
func (s *proj) summaryCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.side
	p := &pv.P
	m := p.Montage
	f := &s.film
	if st.toFinal.Clicked(gtx) {
		s.scrollTo = "final"
	}
	dur := core.Clock(f.total)
	if out := m.Output; out.Status == studio.StatusDone && out.Duration > 0 && !s.outdated(pv) {
		dur = core.Clock(out.Duration)
	}
	scenes := itoa(len(f.items))
	switch {
	case len(f.items) == 0:
		scenes = "Ще немає"
	case f.stills == len(f.items):
		scenes += " — кадри з рухом"
	case f.stills > 0:
		scenes += " — " + count(f.stills, "кадр", "кадри", "кадрів") + " з рухом"
	default:
		scenes += " — усі з кліпами"
	}
	format := "16:9 — горизонтальне"
	if p.Input.Aspect == "9:16" {
		format = "9:16 — вертикальне, Shorts"
	}
	trans := transitionLabel(projectTransition(m))
	if tr, ok := media.TransitionByID(projectTransition(m)); ok && tr.Kind != media.KindCut {
		trans += " · " + secText(transDuration(m))
	}
	var subs string
	switch m.Subtitles {
	case "none":
		subs = "Без субтитрів"
	case "burn":
		pr := mediaStyle(m.SubtitleStyle).Resolve()
		subs = "У відео · " + pr.Label
	default:
		subs = "Файл .srt"
	}
	music := "Без музики"
	if m.MusicPath != "" {
		music = strings.TrimSpace(m.MusicName)
		if music == "" {
			music = "Музика"
		}
		music += " · " + pctText(m.MusicVolume)
	}
	voice := "Вимкнено"
	switch {
	case p.Input.Voiceover && f.voices > 0:
		voice = count(f.voices, "репліка", "репліки", "реплік")
	case p.Input.Voiceover:
		voice = "Ще немає"
	}
	rows := []struct {
		icon  *gui.Icon
		label string
		value string
	}{
		{gui.IconClock, "Тривалість", dur},
		{gui.IconFilm, "Сцени", scenes},
		{gui.IconRatio, "Формат", format},
		{gui.IconBlend, "Переходи", trans},
		{gui.IconMove, "Рух кадрів", effectLabel(m.Effect)},
		{gui.IconPalette, "Фільтр", filterLabel(m.Filter)},
		{gui.IconCaptions, "Субтитри", subs},
		{gui.IconMusic, "Музика", music},
		{gui.IconMic, "Озвучка", voice},
	}
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		ch := []layout.FlexChild{
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, gui.Text{Style: th.H3}.Widget(th, "Що вийде")),
					layout.Rigid(func(gtx C) D {
						return gui.Link{Text: "До відео", Trail: gui.IconChevronRight}.Layout(gtx, th, &st.toFinal)
					}),
				)
			}),
			layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
		}
		for i, r := range rows {
			r := r
			last := i == len(rows)-1
			ch = append(ch, layout.Rigid(func(gtx C) D {
				return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp8}.Layout(gtx, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: gui.Sp12}.Layout(gtx, r.icon.Widget(18, th.Pal.Text3))
						}),
						layout.Rigid(func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Dp(92)
							return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, r.label)
						}),
						layout.Flexed(1, func(gtx C) D {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return gui.Text{Style: th.Small.Weighted(gui.FontMedium), MaxLines: 2, Align: text.End}.Layout(gtx, th, r.value)
						}),
					)
				})
			}))
			if !last {
				ch = append(ch, layout.Rigid(gui.Divider{}.Widget(th)))
			}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
}

// coverStatusCard shows the cover and where it stands.
func (s *proj) coverStatusCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.side
	p := &pv.P
	m := p.Montage
	if st.toCover.Clicked(gtx) {
		s.scrollTo = "cover"
	}
	file, final := coverFile(p)
	var pill gui.StatusPill
	var text string
	running := s.running(pv) == studio.JobCover || m.Cover.Status == studio.StatusRunning
	switch {
	case running:
		pill = gui.StatusPill{State: gui.StateRunning, Percent: jobPct(s.job(pv), studio.JobCover), Small: true}
		text = "Малюємо картинку для обкладинки…"
	case m.Cover.Path == "":
		pill = gui.StatusPill{State: gui.StateEmpty, Text: "Ще немає", Small: true}
		text = "Намалюй картинку й додай великий заголовок — обкладинка вирішує, чи натиснуть на відео."
	case studio.CoverFinalOutdated(*p) || !final:
		pill = gui.StatusPill{State: gui.StateStale, Text: "Не застосовано", Small: true}
		text = "Картинка є — застосуй текст, щоб зберегти готову обкладинку."
	default:
		pill = gui.StatusPill{State: gui.StateDone, Small: true}
		// The title without its *accent* marks.
		title := strings.Join(strings.Fields(strings.ReplaceAll(m.CoverText.Title, "*", "")), " ")
		text = "Готова для YouTube: «" + firstNonEmpty(title, "без тексту") + "»."
		if title == "" {
			text = "Готова для YouTube, без тексту."
		}
	}
	ratio := ratioOf(p.Input.Aspect)
	return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, gui.Text{Style: th.H3}.Widget(th, "Обкладинка")),
					layout.Rigid(pill.Widget(th)),
				)
			},
			func(gtx C) D {
				if file.Path == "" {
					return D{}
				}
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(func(gtx C) D {
					if ratio < 1 {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(160))
					}
					return st.thumb.Layout(gtx, th, c.Img, pv.Abs(file.Path), file.UpdatedAt, gui.Thumb{Ratio: ratio})
				}))
			},
			hint(th, text),
			func(gtx C) D {
				return gui.Link{Text: "Відкрити студію обкладинки", Trail: gui.IconChevronRight}.Layout(gtx, th, &st.toCover)
			},
		)
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
