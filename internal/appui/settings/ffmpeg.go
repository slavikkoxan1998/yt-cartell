package settings

// «FFmpeg — монтаж і звук»: where FFmpeg was found; «Встановити
// автоматично» (Engine.InstallFFmpeg: the pinned official build into the
// studio's own folder, or Homebrew on a Mac — progress, cancel, repair; the
// state lives on the engine, so Головна shows the same run); «Встановити
// вручну» (short steps per system with the official pages and the commands
// to copy) and the paths.

import (
	"context"
	"image"
	"io"
	"runtime"
	"strings"
	"time"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

type ffCard struct {
	ffmpeg, ffprobe field
	pick            gui.Clickable
	test            gui.Clickable
	check           check
	saved           savedNote

	install, repair, cancel gui.Clickable
	manualBtn               gui.Clickable
	manual                  bool // «Встановити вручну» is open
	bar                     gui.Anim
	links                   [6]gui.Clickable
	copies                  [6]gui.Clickable
	pickManual              gui.Clickable
	offer                   *studio.ToolOffer // asked once (it looks for Homebrew)
	seen                    time.Time         // the installer result already reacted to
	localErr                string            // why the button did nothing (demo mode)
	// preview pins what the card shows (headless screenshots).
	preview *ffPreview
}

// ffPreview is a fixed FFmpeg location, installer state and system for
// screenshots.
type ffPreview struct {
	ff      media.FF
	inst    studio.ToolInstall
	managed bool
	offer   studio.ToolOffer
	goos    string
}

// state is the located FFmpeg, the installer and whether the located copy is
// the studio's own.
func (s *ffCard) state(c *core.Ctx) (media.FF, studio.ToolInstall, bool) {
	if s.preview != nil {
		return s.preview.ff, s.preview.inst, s.preview.managed
	}
	ff := c.E.FF()
	return ff, c.E.FFmpegInstall(), media.IsInstalled(ff.FFmpeg)
}

func (s *ffCard) offerFor(c *core.Ctx) studio.ToolOffer {
	if s.preview != nil {
		return s.preview.offer
	}
	if s.offer == nil {
		o := c.E.FFmpegOffer()
		s.offer = &o
	}
	return *s.offer
}

func (s *ffCard) goos() string {
	if s.preview != nil && s.preview.goos != "" {
		return s.preview.goos
	}
	return runtime.GOOS
}

// startFFInstall starts the engine's installation (never in demo mode: it
// reaches the internet).
func (p *Page) startFFInstall(c *core.Ctx) {
	s := &p.ff
	if c.Offline {
		s.localErr = "Встановлення недоступне в демо-режимі."
		return
	}
	s.localErr = ""
	if !c.E.InstallFFmpeg() {
		c.Toast(gui.ToneInfo, "Встановлення FFmpeg уже триває")
	}
}

func (p *Page) ffCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.ff
	cfg := p.now
	flash := func() { s.saved.want = true }
	pickPath := func() {
		var exts []string
		if runtime.GOOS == "windows" {
			exts = []string{"exe"}
		}
		c.PickFile("Де лежить ffmpeg", exts, func(path string) {
			s.ffmpeg.SetText(path)
			p.cfg.set(c, func(cf *config.Config) { cf.FFmpegPath = path }, flash)
		})
	}
	if s.pick.Clicked(gtx) || s.pickManual.Clicked(gtx) {
		pickPath()
	}
	if s.test.Clicked(gtx) {
		p.runCheck(c, &s.check, func(ctx context.Context) (bool, string) {
			ok, msg := c.E.TestFFmpeg(ctx)
			if ok {
				// "ffmpeg version 8.0-full_build-www.gyan.dev Copyright (c) …"
				msg = "FFmpeg " + ffVersion(msg)
			}
			return ok, msg
		})
	}
	if s.install.Clicked(gtx) || s.repair.Clicked(gtx) {
		p.startFFInstall(c)
	}
	if s.cancel.Clicked(gtx) {
		c.E.CancelFFmpegInstall()
	}
	if s.manualBtn.Clicked(gtx) {
		s.manual = !s.manual
	}
	ff, inst, managed := s.state(c)
	if !inst.Running && !inst.At.IsZero() && !inst.At.Equal(s.seen) {
		s.seen = inst.At // a run ended: an older «Перевірити» result is stale
		s.check.reset()
	}
	running := inst.Running
	found := func(gtx C) D {
		row := func(name, path, missing string) layout.Widget {
			return func(gtx C) D {
				ok := path != ""
				ic, col := gui.IconCheckCircle, th.Pal.SuccessInk
				txt := path
				if !ok {
					ic, col, txt = gui.IconXCircle, th.Pal.ErrorInk, missing
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(ic.Widget(18, col)),
					layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Min.X = gtx.Dp(64)
						return gui.Text{Style: th.Label, MaxLines: 1}.Layout(gtx, th, name)
					}),
					layout.Flexed(1, func(gtx C) D {
						t := txt
						c2 := th.Pal.Text2
						if ok {
							t = middleEllipsis(gtx, th, th.Small, t, gtx.Constraints.Max.X)
						} else {
							c2 = th.Pal.ErrorInk
						}
						return gui.Text{Style: th.Small, Color: c2, MaxLines: 2}.Layout(gtx, th, t)
					}),
				)
			}
		}
		ws := []layout.Widget{
			row("ffmpeg", ff.FFmpeg, "не знайдено"),
			row("ffprobe", ff.FFprobe, "не знайдено — має лежати поруч з ffmpeg"),
		}
		if managed && ff.OK() {
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th,
				"Цю копію встановила студія — у свою папку, без прав адміністратора. «Відновити» перевірить файли й за потреби завантажить їх знову."))
		}
		return gui.VStack(gtx, gui.Sp8, ws...)
	}
	body := []layout.Widget{found}
	switch {
	case running:
		body = append(body, func(gtx C) D { return p.ffProgress(gtx, c, inst) })
	case !ff.OK():
		body = append(body, func(gtx C) D { return p.ffOffer(gtx, c) })
	}
	if s.manual && !ff.OK() {
		body = append(body, func(gtx C) D { return p.ffManual(gtx, c) })
	}
	if !running {
		switch {
		case s.localErr != "":
			body = append(body, func(gtx C) D { return note(gtx, th, gui.IconInfo, s.localErr, nil) })
		case inst.Err != "" && (!ff.OK() || managed):
			body = append(body, func(gtx C) D {
				return resultPanel(gtx, th, false, false, inst.Err,
					"Перевір інтернет і місце на диску й натисни ще раз — завантаження продовжиться з того ж місця. Або встанови FFmpeg вручну.", "")
			})
		case inst.Done && ff.OK() && !s.check.done && !s.check.busy:
			msg := "Монтаж, мініатюри й звук готові до роботи."
			if v := ffVersion(inst.Version); v != "" {
				msg = "FFmpeg " + v + ". " + msg
			}
			body = append(body, func(gtx C) D { return resultPanel(gtx, th, false, true, msg, "", "FFmpeg встановлено") })
		case inst.Cancelled && !ff.OK():
			body = append(body, func(gtx C) D {
				return note(gtx, th, gui.IconInfo, "Встановлення скасовано. Натиснеш ще раз — завантаження продовжиться з того ж місця.", nil)
			})
		}
	}
	ffPath := labeled(th, "Шлях до ffmpeg", "Файл або папка. Порожньо — шукати автоматично.", func(gtx C) D {
		s.ffmpeg.bind(gtx, cfg.FFmpegPath, func(v string) {
			v = strings.Trim(strings.TrimSpace(v), `"`)
			p.cfg.set(c, func(cf *config.Config) { cf.FFmpegPath = v }, flash)
		})
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx C) D {
				return s.ffmpeg.Layout(gtx, th, gui.FieldStyle{Placeholder: "Автоматично"})
			}),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Rigid(func(gtx C) D {
				return gui.Button{Kind: gui.Tinted, Icon: gui.IconFolderOpen, Tip: "Вибрати файл ffmpeg"}.Layout(gtx, th, &s.pick)
			}),
		)
	})
	probe := textIn(&s.ffprobe, th, "Шлях до ffprobe", "Порожньо — поруч з ffmpeg.", "Автоматично", cfg.FFprobePath,
		func(v string) {
			v = strings.Trim(strings.TrimSpace(v), `"`)
			p.cfg.set(c, func(cf *config.Config) { cf.FFprobePath = v }, flash)
		})
	var repair layout.Widget
	if managed && ff.OK() && !running {
		repair = func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Icon: gui.IconRefreshCw, Text: "Відновити"}.Layout(gtx, th, &s.repair)
		}
	}
	body = append(body,
		func(gtx C) D { return fieldRow(gtx, 240, ffPath, probe) },
		actions(checkButton(th, &s.test, &s.check, "", running), repair),
		func(gtx C) D {
			return s.check.result(gtx, th, "Натисни «Встановити автоматично» або вкажи шлях до ffmpeg вище. ffprobe має лежати поруч.")
		},
	)
	status := s.check.statusPill(th)
	switch {
	case running:
		status = gui.StatusPill{State: gui.StateRunning, Percent: inst.Pct, Text: itoa(inst.Pct) + "%", Small: true}.Widget(th)
	case status == nil && !ff.OK():
		status = gui.StatusPill{State: gui.StateError, Text: "Не знайдено", Small: true}.Widget(th)
	case status == nil && inst.Done:
		status = gui.StatusPill{State: gui.StateDone, Text: "Встановлено", Small: true}.Widget(th)
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconScissors, Title: "FFmpeg — монтаж і звук",
		Text:  "Склеює сцени, накладає музику й субтитри, робить мініатюри й програє звук. Шукаємо за вказаним шляхом, поруч із програмою, у папці студії, потім у PATH.",
		Trail: trails(s.saved.widget(th), status),
	}, body...)
}

// ffVersion shortens "ffmpeg version 9.0.2-essentials_build-www.gyan.dev
// Copyright (c) …" to "9.0.2-essentials_build-www.gyan.dev".
func ffVersion(line string) string {
	if i := strings.Index(line, " Copyright"); i > 0 {
		line = line[:i]
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "ffmpeg version "))
}

// ffOffer is the call to action while FFmpeg is missing: what the automatic
// installation does here, «Встановити автоматично» and «Встановити вручну».
func (p *Page) ffOffer(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.ff
	o := s.offerFor(c)
	text := "Студія завантажить офіційну збірку FFmpeg " + o.Version
	switch {
	case o.Homebrew:
		text = "Студія встановить FFmpeg через Homebrew (brew install ffmpeg) — це може тривати кілька хвилин. Права адміністратора не потрібні."
	case o.OK:
		if o.Bytes > 0 {
			text += " (" + sizeMB(o.Bytes) + ", " + o.From + ")"
		}
		text += " у свою папку й звірить контрольну суму. Права адміністратора не потрібні."
	default:
		text = o.Note
	}
	chev := gui.IconChevronDown
	if s.manual {
		chev = gui.IconChevronUp
	}
	buttons := []layout.Widget{}
	if o.OK {
		buttons = append(buttons, func(gtx C) D {
			return gui.Button{Icon: gui.IconDownload, Text: "Встановити автоматично"}.Layout(gtx, th, &s.install)
		})
	}
	buttons = append(buttons, func(gtx C) D {
		return gui.Button{Kind: gui.Secondary, Icon: gui.IconBookOpen, Text: "Встановити вручну", Trail: chev}.Layout(gtx, th, &s.manualBtn)
	})
	return inset(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D {
				return insetHead(gtx, th, gui.IconDownload, "FFmpeg за один натиск", text, nil)
			},
			func(gtx C) D { return gui.Flow{Gap: gui.Sp12}.Layout(gtx, buttons...) },
		)
	})
}

// ffProgress shows the running installation with «Скасувати».
func (p *Page) ffProgress(gtx C, c *core.Ctx, inst studio.ToolInstall) D {
	th := c.Th
	s := &p.ff
	msg := inst.Msg
	if msg == "" {
		msg = "Готуємо встановлення…"
	}
	return inset(gtx, th, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				return insetHead(gtx, th, gui.IconDownload, "Встановлюємо FFmpeg",
					"Можна перейти на інші екрани — встановлення триватиме.", nil)
			},
			func(gtx C) D {
				return gui.ProgressBar{Value: float32(max(0, min(100, inst.Pct))) / 100, Tone: gui.ToneRunning, Anim: &s.bar}.Layout(gtx, th)
			},
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Widget(th, msg)),
					layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
					layout.Rigid(func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconX, Text: "Скасувати"}.Layout(gtx, th, &s.cancel)
					}),
				)
			},
		)
	})
}

// ---- «Встановити вручну» --------------------------------------------------------------------

type manualLink struct{ label, url string }

// manualStep is one step of the manual installation: a line, commands to
// copy, official pages to open, «Вказати шлях».
type manualStep struct {
	text  string
	cmds  []string
	links []manualLink
	pick  bool
}

var (
	linkFFmpegOrg = manualLink{"ffmpeg.org", "https://ffmpeg.org/download.html"}
	linkGyan      = manualLink{"gyan.dev", "https://www.gyan.dev/ffmpeg/builds/"}
	linkBtbN      = manualLink{"BtbN на GitHub", "https://github.com/BtbN/FFmpeg-Builds/releases"}
)

// manualSteps are the short how-to for goos.
func manualSteps(goos string) []manualStep {
	switch goos {
	case "windows":
		return []manualStep{
			{text: "Найпростіше — одна команда в терміналі (Windows 10/11): winget, або scoop, якщо ним користуєшся. Потім натисни «Перевірити».",
				cmds: []string{"winget install Gyan.FFmpeg", "scoop install ffmpeg"}},
			{text: "Або завантаж архів (essentials або full build) з офіційної сторінки й розпакуй, наприклад, у C:\\ffmpeg.",
				links: []manualLink{linkGyan, linkBtbN, linkFFmpegOrg}},
			{text: "Вкажи шлях до ffmpeg.exe — він у папці bin. ffprobe.exe лежить поруч, його студія знайде сама.", pick: true},
		}
	case "darwin":
		return []manualStep{
			{text: "У Терміналі через Homebrew (якщо його ще немає — встанови з brew.sh). Потім натисни «Перевірити».",
				cmds: []string{"brew install ffmpeg"}},
			{text: "Або завантаж готові ffmpeg і ffprobe зі сторінки FFmpeg (розділ macOS) і поклади їх в одну папку.",
				links: []manualLink{linkFFmpegOrg}},
			{text: "Вкажи шлях до ffmpeg — ffprobe має лежати поруч.", pick: true},
		}
	}
	return []manualStep{
		{text: "Встанови з менеджера пакетів (Fedora — sudo dnf install ffmpeg, Arch — sudo pacman -S ffmpeg). Потім натисни «Перевірити».",
			cmds: []string{"sudo apt install ffmpeg"}},
		{text: "Інші способи — на офіційній сторінці FFmpeg.", links: []manualLink{linkFFmpegOrg}},
		{text: "Вкажи шлях до ffmpeg, якщо його немає в PATH.", pick: true},
	}
}

// ffManual is the open «Встановити вручну» panel.
func (p *Page) ffManual(gtx C, c *core.Ctx) D {
	th := c.Th
	s := &p.ff
	steps := manualSteps(s.goos())
	nCmd, nLink := 0, 0
	var rows []layout.Widget
	rows = append(rows, func(gtx C) D {
		return insetHead(gtx, th, gui.IconBookOpen, "Встановити вручну", "Будь-який спосіб підійде — студія знайде FFmpeg сама.", nil)
	})
	for i, st := range steps {
		var parts []layout.Widget
		parts = append(parts, gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: th.Pal.Text}.Widget(th, st.text))
		for _, cmd := range st.cmds {
			if nCmd >= len(s.copies) {
				break
			}
			cmd, btn := cmd, &s.copies[nCmd]
			nCmd++
			if btn.Clicked(gtx) {
				gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(cmd))})
				c.Toast(gui.ToneSuccess, "Скопійовано: "+cmd)
			}
			parts = append(parts, func(gtx C) D { return cmdBox(gtx, th, cmd, btn) })
		}
		if len(st.links) > 0 {
			var ls []layout.Widget
			for _, l := range st.links {
				if nLink >= len(s.links) {
					break
				}
				l, btn := l, &s.links[nLink]
				nLink++
				if btn.Clicked(gtx) {
					c.OpenPath(l.url)
				}
				ls = append(ls, func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Text: l.label, Trail: gui.IconExternalLink, Tip: l.url}.Layout(gtx, th, btn)
				})
			}
			parts = append(parts, func(gtx C) D { return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ls...) })
		}
		if st.pick {
			parts = append(parts, func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconFolderOpen, Text: "Вказати шлях"}.Layout(gtx, th, &s.pickManual)
			})
		}
		n := i + 1
		rows = append(rows, func(gtx C) D { return stepRow(gtx, th, n, parts...) })
	}
	return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, rows...) })
}

// stepRow is a numbered step: a small circle with the number and its parts
// stacked beside it.
func stepRow(gtx C, th *gui.Theme, n int, parts ...layout.Widget) D {
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			sz := gtx.Dp(24)
			fillCircle(gtx, sz, th.Pal.Card)
			lg := gtx
			lg.Constraints = layout.Constraints{Max: image.Pt(sz, sz)}
			nd, nc := record(lg, gui.Text{Style: th.Caption.Weighted(gui.FontSemiBold), Color: th.Pal.Text, MaxLines: 1}.Widget(th, itoa(n)))
			o := op.Offset(image.Pt((sz-nd.Size.X)/2, (sz-nd.Size.Y)/2)).Push(gtx.Ops)
			nc.Add(gtx.Ops)
			o.Pop()
			return D{Size: image.Pt(sz, sz)}
		}),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Flexed(1, func(gtx C) D {
			return layout.Inset{Top: 2}.Layout(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, parts...) })
		}),
	)
}

// cmdBox is a command to copy: a flat field-like row (card fill, hairline)
// with the command and a copy button, at most 460 dp wide.
func cmdBox(gtx C, th *gui.Theme, cmd string, btn *gui.Clickable) D {
	w := min(gtx.Constraints.Max.X, gtx.Dp(460))
	h := gtx.Dp(gui.HeightMd)
	padL, padR := gtx.Dp(14), gtx.Dp(6)
	bs := gtx.Dp(32)
	tw := max(0, w-padL-padR-bs-gtx.Dp(8))
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(tw, h)}
	ld, lc := record(lg, gui.Text{Style: th.Body.Weighted(gui.FontMedium), Color: th.Pal.Text, MaxLines: 1}.Widget(th, cmd))
	gui.Box(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.Card, th.Pal.Border)
	o := op.Offset(image.Pt(padL, (h-ld.Size.Y)/2)).Push(gtx.Ops)
	lc.Add(gtx.Ops)
	o.Pop()
	o = op.Offset(image.Pt(w-padR-bs, (h-bs)/2)).Push(gtx.Ops)
	bg := gtx
	bg.Constraints = layout.Exact(image.Pt(bs, bs))
	gui.IconButton{Icon: gui.IconCopy, Size: unit.Dp(32), Tip: "Скопіювати команду"}.Layout(bg, th, btn)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// sizeMB is a download size for people: "109 МБ", "55 МБ".
func sizeMB(b int64) string {
	mb := (b + (1<<20)/2) >> 20
	return itoa(int(mb)) + " МБ"
}

// softWarning is a quiet amber panel for a value past what is recommended
// (also used by «Сцен одночасно»).
func softWarning(gtx C, th *gui.Theme, text string) D {
	pad := gtx.Dp(gui.Sp12)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	d, call := record(cg, gui.Text{Style: th.Small.Sized(14), Color: th.Pal.WarningInk}.Widget(th, text))
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, th.Pal.WarningSoft)
	o := op.Offset(image.Pt(pad, pad+1)).Push(gtx.Ops)
	gui.IconAlertTriangle.Layout(gtx, 18, th.Pal.WarningInk)
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}
