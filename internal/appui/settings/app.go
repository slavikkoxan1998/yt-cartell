package settings

// «Вигляд», «Дані» and «Про студію».

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/assets"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/sysx"
	"ytcartell/internal/version"
)

// CommunityURL is the YT CARTELL community in Telegram.
const CommunityURL = "https://t.me/YT_cartell"

// ---- Вигляд ---------------------------------------------------------------------------------

type appearanceCard struct {
	theme  gui.Segmented
	motion switchRow
	tips   switchRow
	tour   gui.Clickable
	saved  savedNote
}

func (p *Page) appearanceCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.appearance
	flash := func() { s.saved.want = true }
	if v, ok := s.theme.Changed(gtx); ok {
		p.cfg.set(c, func(cf *config.Config) { cf.UI.Theme = v }, flash)
	}
	if s.tour.Clicked(gtx) {
		p.cfg.set(c, func(cf *config.Config) { cf.UI.TourDone = false }, nil)
		c.StartTour()
	}
	ui := p.now.UI
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconPalette, Title: "Вигляд", Big: true,
		Text:  "Тема, анімації й підказки — як тобі зручніше.",
		Trail: s.saved.widget(th),
	},
		func(gtx C) D {
			return lines(gtx, th,
				func(gtx C) D {
					return line(gtx, th, "Тема", "«Як у системі» стежить за темою Windows.", 380, func(gtx C) D {
						s.theme.Full = true
						opts := []gui.SegOption{
							{Value: "light", Label: "Світла", Icon: gui.IconSun},
							{Value: "dark", Label: "Темна", Icon: gui.IconMoon},
							{Value: "system", Label: "Як у системі", Icon: gui.IconMonitor},
						}
						if gtx.Constraints.Max.X < gtx.Dp(360) {
							for i := range opts {
								opts[i].Icon = nil // phones: the words only
							}
						}
						return s.theme.Layout(gtx, th, ui.Theme, opts)
					})
				},
				func(gtx C) D {
					return s.motion.layout(gtx, th, ui.ReducedMotion, "Менше анімацій", "Переходи, шторки й появи стають миттєвими.", func(v bool) {
						gui.SetReducedMotion(v)
						p.cfg.set(c, func(cf *config.Config) { cf.UI.ReducedMotion = v }, flash)
					})
				},
				func(gtx C) D {
					return s.tips.layout(gtx, th, !ui.TipsHidden, "Показувати поради", "Картки з порадами на головній.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.UI.TipsHidden = !v }, flash)
					})
				},
				func(gtx C) D {
					return line(gtx, th, "Тур по студії", "Коротка екскурсія: історія → профілі → автопілот → черга.", 260, func(gtx C) D {
						return gui.Button{Kind: gui.Secondary, Icon: gui.IconHelpCircle, Text: "Показати підказки знову"}.Layout(gtx, th, &s.tour)
					})
				},
			)
		},
	)
}

// ---- Дані ----------------------------------------------------------------------------------

type dataCard struct {
	change, open gui.Clickable
	moving       bool
	importBtn    gui.Clickable
	importing    bool
	importRes    check
	saved        savedNote
	io           settingsIO // «Перенести налаштування»: export / import of the settings (transfer.go)
}

func (p *Page) dataCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.data
	cfg := p.now
	dir := cfg.DataDir
	if s.change.Clicked(gtx) && !s.moving {
		old := dir
		c.PickFolder("Папка для проєктів YT Cartell Studio", old, func(path string) {
			path = filepath.Clean(path)
			if strings.EqualFold(path, filepath.Clean(old)) {
				return
			}
			s.moving = true
			p.cfg.set(c, func(cf *config.Config) { cf.DataDir = path }, func() {
				s.moving = false
				s.saved.want = true
				c.Toast(gui.ToneSuccess, "Папку з проєктами змінено. Попередні проєкти лишились у старій папці.")
			})
		})
	}
	if s.open.Clicked(gtx) && !c.Offline && dir != "" {
		c.Background(func() {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				c.Error(fmt.Errorf("не вдалося створити папку: %w", err))
				return
			}
			c.OpenPath(dir)
		})
	}
	if s.importBtn.Clicked(gtx) && !s.importing {
		p.importWeb(c)
	}
	n := len(c.List())
	folder := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			gui.Text{Style: th.Label}.Widget(th, "Папка з проєктами"),
			func(gtx C) D { return pathWell(gtx, th, gui.IconFolder, dir, "Папку не задано") },
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
				"Зараз тут "+core.Count(n, "проєкт", "проєкти", "проєктів")+". Якщо змінити папку, студія працюватиме з новою, а наявні проєкти лишаться в старій — їх можна перенести вручну."),
			actions(
				func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Icon: gui.IconFolderOpen, Text: "Змінити…", Busy: s.moving}.Layout(gtx, th, &s.change)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Icon: gui.IconExternalLink, Text: "Відкрити"}.Layout(gtx, th, &s.open)
				},
			),
		)
	}
	importBlock := func(gtx C) D {
		return gui.VStack(gtx, gui.Sp12,
			gui.Text{Style: th.Label}.Widget(th, "Імпорт з веб-версії"),
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
				"Перенесе ключі, налаштування й проєкти з веб-студії — вибери її папку (де лежать data і uploads). Веб-версія не змінюється, наявні проєкти пропускаються."),
			actions(func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconImport, Text: "Вибрати папку й імпортувати", Busy: s.importing}.Layout(gtx, th, &s.importBtn)
			}),
			func(gtx C) D {
				if !s.importRes.busy && !s.importRes.done {
					return D{}
				}
				return resultPanel(gtx, th, s.importRes.busy, s.importRes.ok, s.importRes.msg, "Перевір, що вибрано корінь веб-студії: у ній є папки data/studio та uploads.", "Імпорт завершено")
			},
		)
	}
	return card(gtx, th, m.narrow, head{
		Icon: gui.IconHardDrive, Title: "Дані", Big: true,
		Text:  "Тут лежать проєкти й усе, що студія створює: кадри, відео, озвучка, монтаж.",
		Trail: s.saved.widget(th),
	},
		folder,
		gui.Divider{}.Widget(th),
		func(gtx C) D { return p.transferBlock(gtx, c) },
		gui.Divider{}.Widget(th),
		importBlock,
	)
}

// importWeb picks the web studio's folder and imports it in the background.
func (p *Page) importWeb(c *core.Ctx) {
	s := &p.data
	start := guessWebRoot()
	if start == "" {
		start = filepath.Dir(sysx.ExeDir())
	}
	c.PickFolder("Папка веб-версії студії (де лежать data і uploads)", start, func(root string) {
		s.importing = true
		s.importRes = check{busy: true, msg: "Імпортуємо… Файли проєктів копіюються, це може зайняти хвилину."}
		c.Background(func() {
			n, err := c.E.ImportFromWeb(root)
			c.Post(func() {
				s.importing = false
				done := "Налаштування імпортовано, нових проєктів немає."
				if n > 0 {
					done = "Імпортовано " + core.Count(n, "проєкт", "проєкти", "проєктів") + " і налаштування."
				}
				if err != nil {
					s.importRes = check{done: true, ok: n > 0, msg: done + " Не все вдалося: " + core.ErrorText(err)}
					c.Toast(gui.ToneWarning, done+" Не все вдалося — подробиці в картці «Дані».")
					return
				}
				s.importRes = check{done: true, ok: true, msg: done}
				c.Toast(gui.ToneSuccess, done)
			})
		})
	})
}

// guessWebRoot: the web studio usually sits one level above the desktop app
// (…/YT-CARTELL-AI-MAKER/GO/YT-Cartell-Studio.exe).
func guessWebRoot() string {
	exe := sysx.ExeDir()
	for _, d := range []string{filepath.Dir(exe), exe} {
		if st, err := os.Stat(filepath.Join(d, "data", "studio")); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

// ---- Про студію ------------------------------------------------------------------------------

type aboutCard struct {
	community        gui.Clickable
	openLog, showLog gui.Clickable
	update           gui.Clickable
	auto             switchRow
}

// updates is what the shell (package appui) offers this card: the
// «Оновлення» sheet and the newer version it knows. Hosts without it
// (headless tests) show no update button.
type updates interface {
	OpenUpdates()
	UpdateAvailable() string // "" = none known
}

func (p *Page) aboutCard(gtx C, c *core.Ctx, m metrics) D {
	th := c.Th
	s := &p.about
	if s.community.Clicked(gtx) {
		c.OpenPath(CommunityURL)
	}
	if s.openLog.Clicked(gtx) {
		c.OpenPath(c.LogPath)
	}
	if s.showLog.Clicked(gtx) {
		c.Reveal(c.LogPath)
	}
	host, canUpdate := c.Host.(updates)
	if s.update.Clicked(gtx) && canUpdate {
		host.OpenUpdates()
	}
	updBlock := func(gtx C) D {
		newer := ""
		if canUpdate {
			newer = host.UpdateAvailable()
		}
		text := "Студія сама дивиться на GitHub, чи вийшла нова версія, і нічого не встановлює без тебе."
		btn := gui.Button{Kind: gui.Secondary, Icon: gui.IconRefreshCw, Text: "Перевірити оновлення"}
		if newer != "" {
			text = "Вийшла версія " + newer + ". Встановлення займе хвилину, проєкти й налаштування збережуться."
			btn = gui.Button{Kind: gui.Secondary, Icon: gui.IconDownload, Text: "Оновити до " + newer}
		}
		ws := []layout.Widget{
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Label}.Widget(th, "Оновлення")),
					layout.Rigid(func(gtx C) D {
						if newer == "" {
							return D{}
						}
						return layout.Inset{Left: gui.Sp8}.Layout(gtx, gui.Badge{Text: "нова " + newer, Tone: gui.ToneAction}.Widget(th))
					}),
				)
			},
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, text),
		}
		if canUpdate {
			ws = append(ws, actions(func(gtx C) D { return btn.Layout(gtx, th, &s.update) }))
		}
		ws = append(ws, func(gtx C) D {
			return layout.Inset{Top: gui.Sp8}.Layout(gtx, func(gtx C) D {
				return s.auto.layout(gtx, th, p.now.UI.AutoUpdate, "Перевіряти автоматично",
					"Раз на 6 годин, за кілька секунд після запуску. Встановлює — лише коли ти натиснеш.", func(v bool) {
						p.cfg.set(c, func(cf *config.Config) { cf.UI.AutoUpdate = v }, nil)
					})
			})
		})
		return gui.VStack(gtx, gui.Sp12, ws...)
	}
	logBlock := func(gtx C) D {
		hint := "Якщо щось зламалося — надішли цей файл у спільноту, так простіше допомогти."
		if c.LogPath == "" {
			hint = "У цьому режимі журнал не ведеться."
		}
		return gui.VStack(gtx, gui.Sp12,
			gui.Text{Style: th.Label}.Widget(th, "Журнал роботи"),
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, hint),
			actions(
				func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Icon: gui.IconFileText, Text: "Відкрити журнал", Disabled: c.LogPath == ""}.Layout(gtx, th, &s.openLog)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Ghost, Icon: gui.IconFolder, Text: "Показати в папці", Disabled: c.LogPath == ""}.Layout(gtx, th, &s.showLog)
				},
			),
		)
	}
	return gui.Card{Padding: gui.Sp24}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return gui.VStack(gtx, gui.Sp20,
			func(gtx C) D { return s.head(gtx, c) },
			gui.Divider{}.Widget(th),
			updBlock,
			gui.Divider{}.Widget(th),
			logBlock,
		)
	})
}

// head is the name, build and community link of the studio.
func (s *aboutCard) head(gtx C, c *core.Ctx) D {
	th := c.Th
	logo := func(gtx C) D {
		img, _, ok := c.Img.FromBytesFunc("app:logo", assets.Logo)
		sz := gtx.Dp(56)
		g := gtx
		g.Constraints = layout.Exact(image.Pt(sz, sz))
		if !ok {
			return gui.Thumb{Radius: gui.RadiusInner}.Layout(g, th)
		}
		return gui.Thumb{Image: img, Radius: gui.RadiusInner, Fit: gui.FitContain}.Layout(g, th)
	}
	info := func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(gui.Text{Style: th.H3}.Widget(th, "YT Cartell Studio")),
			layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, buildInfo())),
		)
	}
	link := func(gtx C) D {
		return gui.Button{Kind: gui.Tinted, Icon: gui.IconMessageCircle, Text: "Спільнота YT CARTELL", Trail: gui.IconExternalLink}.Layout(gtx, th, &s.community)
	}
	if gtx.Constraints.Max.X < gtx.Dp(520) {
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, layout.Rigid(logo),
					layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout), layout.Flexed(1, info))
			},
			link,
		)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(logo),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Flexed(1, info),
		layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
		layout.Rigid(link),
	)
}

var (
	buildOnce sync.Once
	buildText string
)

// buildInfo describes this build: "Версія 2.0.0 · збірка 30 вер 2026".
func buildInfo() string {
	buildOnce.Do(func() {
		text := "Версія " + version.String()
		stamp := version.Built()
		if bi, ok := debug.ReadBuildInfo(); ok && stamp.IsZero() {
			for _, s := range bi.Settings {
				if s.Key == "vcs.time" {
					stamp, _ = time.Parse(time.RFC3339, s.Value)
				}
			}
		}
		if stamp.IsZero() {
			if exe, err := os.Executable(); err == nil {
				if st, err := os.Stat(exe); err == nil {
					stamp = st.ModTime()
				}
			}
		}
		if !stamp.IsZero() {
			months := [...]string{"січ", "лют", "бер", "кві", "тра", "чер", "лип", "сер", "вер", "жов", "лис", "гру"}
			text += fmt.Sprintf(" · збірка %d %s %d", stamp.Day(), months[stamp.Month()-1], stamp.Year())
		}
		buildText = text
	})
	return buildText
}
