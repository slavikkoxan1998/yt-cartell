package home

import (
	"fmt"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// ---- setup call-out ------------------------------------------------------------------------------

// setupCard is shown while the studio cannot make a film yet: keys are
// missing, G-Labs does not answer or FFmpeg is not found. ok is false when
// everything is in place.
func (p *Page) setupCard(c *core.Ctx) (layout.Widget, bool) {
	th := c.Th
	cfg := c.E.Config().Get()
	inst := p.ffWatch(c)
	var title, text string
	switch miss := setupMissing(cfg); {
	case len(miss) > 0:
		title = "Спершу налаштуй підключення"
		text = "Студія пише план, малює кадри, знімає відео й озвучує через твої сервіси. Бракує: " + strings.Join(miss, ", ") +
			". Встав у «Помічник підключення» свої адреси й ключі — він сам розбереться, що куди."
	case p.svc.glabs.done && !p.svc.glabs.ok:
		title = "G-Labs Studio не відповідає"
		text = "Кадри й відео малює G-Labs Studio. Запусти її на цьому комп'ютері або перевір адресу й ключ у налаштуваннях."
	case !p.svc.ffOK || inst.Running:
		return p.ffSetup(c, inst), true
	default:
		return nil, false
	}
	return func(gtx C) D {
		return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp12,
				func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							gui.FillCircle(gtx, 40, th.Pal.WarningSoft)
							return layout.UniformInset(10).Layout(gtx, gui.IconPlug.Widget(20, th.Pal.WarningInk))
						}),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Flexed(1, gui.Text{Style: th.H3, MaxLines: 2}.Widget(th, title)),
					)
				},
				gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2}.Widget(th, text),
				func(gtx C) D {
					return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
						func(gtx C) D {
							return gui.Button{Size: gui.Sm, Icon: gui.IconWandSparkles, Text: "Помічник підключення"}.Layout(gtx, th, &p.home.assist)
						},
						func(gtx C) D {
							return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconSettings, Text: "Відкрити налаштування"}.Layout(gtx, th, &p.home.setup)
						},
					)
				},
			)
		})
	}, true
}

// ffHint is the state of the FFmpeg call-out: its buttons and whether the
// engine's installation ran on the last frame (a toast when it ends).
type ffHint struct {
	install, cancel, settings gui.Clickable
	bar                       gui.Anim
	was                       bool
	localErr                  string
	pin                       *studio.ToolInstall // headless screenshots: a fixed installer state
}

// ffWatch follows the engine's FFmpeg installation (started here or in
// Settings): when it ends, a toast says how, and FFmpeg is looked up again
// at once.
func (p *Page) ffWatch(c *core.Ctx) studio.ToolInstall {
	st := c.E.FFmpegInstall()
	if p.ff.pin != nil {
		st = *p.ff.pin
	}
	// Only a run that just ended (not one that ended while Головна was not
	// shown) gets a toast.
	if p.ff.was && !st.Running && time.Since(st.At) < 10*time.Second {
		p.svc.ffAt = time.Time{} // re-read the located FFmpeg on the next frame
		switch {
		case st.Done:
			c.Toast(gui.ToneSuccess, "FFmpeg встановлено — монтаж готовий до роботи")
		case st.Err != "":
			c.Toast(gui.ToneError, "Не вдалося встановити FFmpeg")
		}
	}
	p.ff.was = st.Running
	return st
}

// ffSetup is the call-out while FFmpeg is missing: «Встановити
// автоматично» starts the engine's installation (progress and «Скасувати»
// right here), «Інші способи» goes to the FFmpeg card in Settings (manual
// ways, the path).
func (p *Page) ffSetup(c *core.Ctx, inst studio.ToolInstall) layout.Widget {
	th := c.Th
	h := &p.ff
	return func(gtx C) D {
		if h.install.Clicked(gtx) {
			if c.Offline {
				h.localErr = "Встановлення недоступне в демо-режимі."
			} else {
				h.localErr = ""
				c.E.InstallFFmpeg()
			}
		}
		if h.cancel.Clicked(gtx) {
			c.E.CancelFFmpegInstall()
		}
		if h.settings.Clicked(gtx) {
			c.Navigate(core.Route{Page: core.PageSettings, Section: "ffmpeg"})
		}
		title, text := "Не знайдено FFmpeg", "FFmpeg потрібен для монтажу. Студія може встановити його сама — офіційна збірка, без прав адміністратора. Або встанови вручну чи вкажи свій у налаштуваннях."
		icon, bg, ink := gui.IconScissors, th.Pal.WarningSoft, th.Pal.WarningInk
		errText := h.localErr
		switch {
		case inst.Running:
			title, text = "Встановлюємо FFmpeg", inst.Msg
			icon, bg, ink = gui.IconDownload, th.Pal.RunningSoft, th.Pal.RunningInk
		case inst.Err != "":
			errText = inst.Err
		}
		ws := []layout.Widget{
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						gui.FillCircle(gtx, 40, bg)
						return layout.UniformInset(10).Layout(gtx, icon.Widget(20, ink))
					}),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Flexed(1, gui.Text{Style: th.H3, MaxLines: 2}.Widget(th, title)),
				)
			},
		}
		if inst.Running {
			ws = append(ws,
				func(gtx C) D {
					return gui.ProgressBar{Value: float32(max(0, min(100, inst.Pct))) / 100, Tone: gui.ToneRunning, Anim: &h.bar}.Layout(gtx, th)
				},
				gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2, MaxLines: 2}.Widget(th, text),
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconX, Text: "Скасувати"}.Layout(gtx, th, &h.cancel)
				},
			)
			return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp12, ws...) })
		}
		ws = append(ws, gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2}.Widget(th, text))
		if errText != "" {
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.ErrorInk, MaxLines: 4}.Widget(th, errText))
		}
		label := "Встановити автоматично"
		if inst.Err != "" || inst.Cancelled {
			label = "Спробувати ще раз"
		}
		ws = append(ws, func(gtx C) D {
			return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
				func(gtx C) D {
					return gui.Button{Size: gui.Sm, Icon: gui.IconDownload, Text: label}.Layout(gtx, th, &h.install)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconBookOpen, Text: "Інші способи"}.Layout(gtx, th, &h.settings)
				},
			)
		})
		return gui.Card{Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp12, ws...) })
	}
}

// ---- next step ---------------------------------------------------------------------------------

// nextCard is "Продовж «…»": the most recent project that is not finished,
// with what to do next (or a nudge towards the first story).
func (p *Page) nextCard(gtx C, c *core.Ctx, list []studio.Summary) D {
	th := c.Th
	var pick *studio.Summary
	var in cardInfo
	for i := range list {
		if i >= 12 {
			break
		}
		if x := info(c, list[i]); x.status != stDone {
			pick, in = &list[i], x
			break
		}
	}
	if p.home.next.Clicked(gtx) {
		if pick != nil {
			c.Navigate(core.Route{Page: core.PageProject, ProjectID: pick.ID})
		} else {
			p.hero.focus = true
			p.home.scroll.List.Position = layout.Position{}
		}
	}
	if pick != nil {
		return p.ringNext(gtx, c, *pick, in)
	}
	card := gui.NextStepCard{Icon: gui.IconSparkles, Title: "Перша історія",
		Text: "Встав текст у «Нову історію» й натисни «Автопілот» — студія зробить відео сама."}
	if len(list) > 0 {
		card = gui.NextStepCard{Icon: gui.IconPartyPopper, Title: "Усі проєкти готові",
			Text: "Час для нової історії — встав її в картку «Нова історія»."}
	}
	return card.Layout(gtx, th, &p.home.next)
}

// ringNext is the next-step card of a project: the project's progress ring
// (the running job's percent, else its step x/5) where the kit's card has an
// icon, the title, what to do next and the round arrow button.
func (p *Page) ringNext(gtx C, c *core.Ctx, s studio.Summary, in cardInfo) D {
	th := c.Th
	h := &p.home
	value, label, tone := float32(in.step)/5, fmt.Sprintf("%d/5", in.step), gui.ToneNeutral
	switch in.status {
	case stRunning:
		pct := 0
		if pv := c.Project(s.ID); pv != nil && pv.P.Job != nil {
			pct = max(0, min(100, pv.P.Job.Progress))
		}
		value, label, tone = float32(pct)/100, "", gui.ToneRunning
	case stError:
		tone = gui.ToneError
	}
	busy := in.status == stRunning
	h.nextHover.Duration = gui.DurHover
	hv := h.nextHover.Animate(gtx, b2f(h.next.Hovered()))
	return gui.Card{Click: &h.next, Padding: gui.Sp20}.Layout(gtx, th, func(gtx C) D {
		btn := gtx.Dp(44)
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(gui.ProgressRing{Value: value, Size: 64, Stroke: 6, Tone: tone, Label: label, Anim: &h.nextRing}.Widget(th)),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.H3, MaxLines: 2}.Widget(th, "Продовж «"+in.title+"»")),
					layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
					layout.Rigid(gui.Text{Style: th.Small.Sized(14), Color: th.Pal.Text2, MaxLines: 3}.Widget(th, nextText(c, s, in))),
				)
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
			layout.Rigid(func(gtx C) D {
				gui.FillRRect(gtx, image.Rect(0, 0, btn, btn), gui.RadiusControl, gui.LerpColor(th.Pal.Action, th.Pal.ActionHover, hv))
				ic := gtx.Dp(22)
				off := op.Offset(image.Pt((btn-ic)/2, (btn-ic)/2)).Push(gtx.Ops)
				if busy {
					gui.Spinner{Size: 22, Color: th.Pal.OnAction}.Layout(gtx, th)
				} else {
					gui.Offset(gtx, unit.Dp(3*hv), 0, gui.IconArrowRight.Widget(22, th.Pal.OnAction))
				}
				off.Pop()
				return D{Size: image.Pt(btn, btn)}
			}),
		)
	})
}

func nextText(c *core.Ctx, s studio.Summary, in cardInfo) string {
	switch in.status {
	case stRunning:
		pct := 0
		if pv := c.Project(s.ID); pv != nil && pv.P.Job != nil {
			pct = pv.P.Job.Progress
		}
		if pct <= 0 {
			return fmt.Sprintf("Зараз: %s. Можна дивитися, як з'являється результат.", core.JobLabel(s.Running))
		}
		return fmt.Sprintf("Зараз: %s · %d%%. Можна дивитися, як з'являється результат.", core.JobLabel(s.Running), min(100, pct))
	case stError:
		return "Останній етап не вдався — відкрий проєкт і натисни «Повторити»."
	case stDraft:
		return "Крок 1 з 5 — допиши історію й створи план."
	}
	switch in.step {
	case 1:
		return "Крок 1 з 5 — допиши історію й створи план."
	case 2:
		return "Крок 2 з 5 — перевір план і сцени."
	case 3:
		return "Крок 3 з 5 — розкадровка чекає на тебе."
	case 4:
		return "Крок 4 з 5 — час оживити кадри й озвучити сцени."
	}
	return "Крок 5 з 5 — лишився монтаж, і відео готове."
}

// ---- live queue ----------------------------------------------------------------------------------

// queueCard shows the top of the queue with animated progress; «Відкрити»
// slides the queue drawer in.
func (p *Page) queueCard(gtx C, c *core.Ctx) D {
	th := c.Th
	if p.home.queue.Clicked(gtx) {
		c.OpenQueue()
	}
	snap := p.queueSnap(c)
	items := append([]studio.QueueItem(nil), snap.Running[:min(3, len(snap.Running))]...)
	for _, it := range snap.Recent {
		if len(items) >= 3 {
			break
		}
		items = append(items, it)
	}
	return gui.Anchor(gtx, th, "home.queue", func(gtx C) D {
		return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
			ws := []layout.Widget{func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Черга")),
					layout.Rigid(func(gtx C) D {
						if snap.Counts.Running == 0 {
							return D{}
						}
						return layout.Inset{Left: gui.Sp8}.Layout(gtx, gui.Badge{Text: fmt.Sprint(snap.Counts.Running), Tone: gui.ToneAction}.Widget(th))
					}),
					layout.Flexed(1, func(gtx C) D {
						return layout.E.Layout(gtx, func(gtx C) D {
							return gui.Link{Text: "Відкрити", Trail: gui.IconChevronRight, Style: th.Label}.Layout(gtx, th, &p.home.queue)
						})
					}),
				)
			}}
			if len(items) == 0 {
				ws = append(ws, func(gtx C) D {
					return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp4}.Layout(gtx, func(gtx C) D {
						return gui.ActivityRow{Icon: gui.IconCoffee, Title: "Зараз нічого не генерується",
							Meta: "Запусти автопілот — прогрес з'явиться тут", Progress: -1}.Layout(gtx, th, nil)
					})
				})
			}
			for i, it := range items {
				it, last := it, i == len(items)-1
				ws = append(ws, func(gtx C) D { return p.queueRow(gtx, c, it, !last) })
			}
			return gui.VStack(gtx, gui.Sp4, ws...)
		})
	})
}

func (p *Page) queueRow(gtx C, c *core.Ctx, it studio.QueueItem, divider bool) D {
	th := c.Th
	a := p.home.qrows[it.Key]
	if a == nil {
		a = new(gui.Anim)
		p.home.qrows[it.Key] = a
		if len(p.home.qrows) > 64 {
			for k := range p.home.qrows {
				if k != it.Key {
					delete(p.home.qrows, k)
					break
				}
			}
		}
	}
	title := it.Label
	if t := strings.TrimSpace(it.ProjectTitle); t != "" {
		title += " · " + t
	}
	row := gui.ActivityRow{Icon: qIcon(it), Title: title, Progress: -1, Divider: divider}
	switch it.Status {
	case studio.JobRunning:
		row.Tone, row.Solid = gui.ToneRunning, true
		row.Meta = qMessage(it)
		if it.Progress <= 0 && it.Regen != studio.RegenVideo {
			// Nothing measured yet: a live pill instead of a stuck 0 %.
			row.Trailing = gui.StatusPill{State: gui.StateRunning, Percent: -1, Small: true}.Widget(th)
		} else {
			a.Duration, a.Ease = gui.DurValue, gui.EaseOutCubic
			row.Progress = a.Animate(gtx, float32(max(0, min(100, it.Progress)))/100)
			row.Value = fmt.Sprintf("%d%%", max(0, min(100, it.Progress)))
		}
	case studio.JobDone:
		row.Tone, row.Meta = gui.ToneSuccess, "Готово · "+core.Updated(it.FinishedAt)
		if it.Kind != "" && strings.Contains(strings.ToLower(it.Message), "помил") {
			row.Tone, row.Meta = gui.ToneWarning, it.Message
			row.Trailing = gui.StatusPill{State: gui.StateStale, Text: "З помилками", Small: true}.Widget(th)
		}
	case studio.JobError:
		row.Tone, row.Meta = gui.ToneError, "Помилка · "+core.Updated(it.FinishedAt)
		row.Trailing = gui.StatusPill{State: gui.StateError, Small: true}.Widget(th)
	default:
		row.Meta = "Зупинено · " + core.Updated(it.FinishedAt)
	}
	// The whole row opens the drawer, where the row's actions live.
	return row.Layout(gtx, th, &p.home.queue)
}

func qMessage(it studio.QueueItem) string {
	msg := strings.TrimSpace(it.Message)
	if rest, ok := strings.CutPrefix(msg, it.Label+" · "); ok {
		msg = rest
	}
	if msg == "" || msg == it.Label {
		return "Генерується…"
	}
	return msg
}

// qIcon is the icon of the work a queue row stands for.
func qIcon(it studio.QueueItem) *gui.Icon {
	switch it.Regen {
	case studio.RegenCharacter:
		return gui.IconUserRound
	case studio.RegenLocation:
		return gui.IconMapPin
	case studio.RegenFrame:
		return gui.IconImage
	case studio.RegenVideo:
		return gui.IconFilm
	case studio.RegenVoice:
		return gui.IconMic
	}
	switch it.Kind {
	case studio.JobPlan:
		return gui.IconListOrdered
	case studio.JobRefs:
		return gui.IconUsers
	case studio.JobFrames:
		return gui.IconImages
	case studio.JobVideos:
		return gui.IconFilm
	case studio.JobVoices:
		return gui.IconMic
	case studio.JobRender:
		return gui.IconClapperboard
	case studio.JobMontage:
		return gui.IconScissors
	case studio.JobCover:
		return gui.IconImagePlus
	case studio.JobAutopilot:
		return gui.IconRocket
	case "translate":
		return gui.IconLanguages
	case "transcribe":
		return gui.IconAudioLines
	}
	return gui.IconSparkles
}

// ---- tips ----------------------------------------------------------------------------------------

type tipDef struct {
	text   string
	action string
	do     func(c *core.Ctx)
}

var tips = []tipDef{
	{"Додай 2–3 профілі — і автопілот сам підбере голос, стиль і темп для кожної історії.", "Налаштувати профілі",
		func(c *core.Ctx) { c.OpenSheet(core.Sheet{Kind: core.SheetProfiles}) }},
	{"Маєш власний запис голосу? «Своя озвучка» розріже його на сцени й підбере кадри під кожну фразу.", "Спробувати",
		func(c *core.Ctx) { c.OpenSheet(core.Sheet{Kind: core.SheetOwnVoice}) }},
	{"Один проєкт — багато мов: «Перекласти проєкт» зробить копію з новою озвучкою, а кадри й відео залишаться.", "Перекласти",
		func(c *core.Ctx) { c.OpenSheet(core.Sheet{Kind: core.SheetTranslate}) }},
	{"Черга показує все, що генерується, в усіх проєктах — проєкт можна закрити, робота триває.", "Відкрити чергу",
		func(c *core.Ctx) { c.OpenQueue() }},
	{"Сповіщення в Telegram скажуть, коли відео готове, — навіть якщо ти відійшов від комп'ютера.", "Налаштувати",
		func(c *core.Ctx) { c.Navigate(core.Route{Page: core.PageSettings, Section: "telegram"}) }},
	{"Змінив промт після генерації? Жовта позначка «Змінено» підкаже, що кадр варто перегенерувати.", "", nil},
	{"Ctrl+N відкриває новий проєкт з будь-якого екрана, а Esc повертає назад.", "", nil},
	{"Готовий сценарій? На кроці «Сценарій» обери «Готовий сценарій» — студія озвучить текст слово в слово.", "", nil},
}

const tipPeriod = 25 // seconds a tip stays

type tipState struct {
	shift int
	last  int
	fade  gui.Anim
	next  gui.Clickable
	hide  gui.Clickable
	act   gui.Clickable
}

// tipCard is the rotating smart tip ("Порада") with «Наступна» and «Сховати».
func (p *Page) tipCard(gtx C, c *core.Ctx) D {
	th := c.Th
	t := &p.tip
	if t.next.Clicked(gtx) {
		t.shift++
	}
	if t.hide.Clicked(gtx) {
		c.Background(func() {
			if err := c.E.Config().Update(func(cfg *config.Config) { cfg.UI.TipsHidden = true }); err != nil {
				c.Error(fmt.Errorf("не вдалося зберегти налаштування: %w", err))
			}
		})
		c.Toast(gui.ToneInfo, "Поради сховано. Повернути їх можна в «Налаштування → Вигляд».")
	}
	base := int(gtx.Now.Unix() / tipPeriod)
	idx := (base + t.shift) % len(tips)
	if idx < 0 {
		idx += len(tips)
	}
	gtx.Execute(op.InvalidateCmd{At: time.Unix(int64(base+1)*tipPeriod, 0)})
	if idx != t.last {
		t.last = idx
		t.fade.Set(0)
	}
	t.fade.Duration = gui.DurRoute
	a := t.fade.Animate(gtx, 1)
	tip := tips[idx]
	if t.act.Clicked(gtx) && tip.do != nil {
		tip.do(c)
	}
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp8,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						return layout.Inset{Right: gui.Sp8}.Layout(gtx, gui.IconLightbulb.Widget(20, th.Pal.ActionText))
					}),
					layout.Flexed(1, gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Порада")),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconChevronRight, Tip: "Наступна порада"}.Layout(gtx, th, &t.next)
					}),
					layout.Rigid(func(gtx C) D {
						return gui.IconButton{Icon: gui.IconX, Tip: "Сховати поради"}.Layout(gtx, th, &t.hide)
					}),
				)
			},
			func(gtx C) D {
				return gui.FadeUp(gtx, a, 4, func(gtx C) D {
					return gui.VStack(gtx, gui.Sp12,
						gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, tip.text),
						func(gtx C) D {
							if tip.action == "" {
								return D{}
							}
							return gui.Link{Text: tip.action, Trail: gui.IconChevronRight}.Layout(gtx, th, &t.act)
						},
					)
				})
			},
		)
	})
}

// ---- quick actions -------------------------------------------------------------------------------

// quickDefs are «Швидкі дії» — only what the story card itself does not
// offer (it already starts a new story and the autopilot).
var quickDefs = []struct {
	icon        *gui.Icon
	label, text string
}{
	{gui.IconRepeat, "За прикладом", "Перезняти за прикладом: встав посилання на YouTube — студія подивиться відео й зробить своє за його зразком"},
	{gui.IconLanguages, "Перекласти проєкт", "Копія іншою мовою: нова озвучка, ті самі кадри"},
	{gui.IconMic, "Своя озвучка", "Відео під твій власний запис голосу"},
	{gui.IconImage, "Обкладинка", "Обкладинка для YouTube останнього проєкту"},
}

func (p *Page) tileAction(c *core.Ctx, i int) {
	switch i {
	case 0:
		c.OpenSheet(core.Sheet{Kind: core.SheetRemake})
	case 1:
		c.OpenSheet(core.Sheet{Kind: core.SheetTranslate})
	case 2:
		c.OpenSheet(core.Sheet{Kind: core.SheetOwnVoice})
	case 3:
		list := c.List()
		if len(list) == 0 {
			c.Toast(gui.ToneInfo, "Спершу створи проєкт — обкладинка робиться на кроці «Монтаж».")
			return
		}
		c.Navigate(core.Route{Page: core.PageProject, ProjectID: list[0].ID, Step: 5})
	}
}

// quickCard is «Швидкі дії»: the world's quick-action tiles — an icon in a
// soft square with its label under it — four across the card (the text of
// each action is its tooltip).
func (p *Page) quickCard(gtx C, c *core.Ctx) D {
	th := c.Th
	return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
		size := unit.Dp(64)
		if gtx.Constraints.Max.X < gtx.Dp(300) {
			size = 52
		}
		return gui.VStack(gtx, gui.Sp16,
			gui.Text{Style: th.H3, MaxLines: 1}.Widget(th, "Швидкі дії"),
			func(gtx C) D {
				var ch []layout.FlexChild
				for i, q := range quickDefs {
					i, q := i, q
					ch = append(ch, layout.Flexed(1, func(gtx C) D {
						return layout.N.Layout(gtx, func(gtx C) D {
							return gui.Tile{Icon: q.icon, Label: q.label, Tip: q.text, Size: size}.Layout(gtx, th, &p.home.tiles[i])
						})
					}))
				}
				return layout.Flex{}.Layout(gtx, ch...)
			},
		)
	})
}
