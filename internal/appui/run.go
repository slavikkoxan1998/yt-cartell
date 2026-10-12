package appui

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/dictate"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/tgbot"
	"ytcartell/internal/update"
)

// Window geometry (dp). The minimum is what the phone layout (bottom bar)
// handles, so scaled laptop screens can always shrink the window enough.
const (
	winW, winH       = 1440, 900
	winMinW, winMinH = 480, 560
)

// shutdownWait bounds how long a closed window waits for cancelled jobs to
// record their state before the process exits.
const shutdownWait = 5 * time.Second

// LogPath is where main mirrors the log (shown in the crash message).
var LogPath string

// Run opens the studio window ("YT Cartell Studio", 1440×900 dp — maximized
// when the screen's work area is smaller — min 480×560) and blocks: on
// desktop platforms app.Main never returns, so the process exits when the
// window closes (after the engine stopped its jobs and flushed its saves).
// notices are shown as toasts once the window is up (an unreadable settings
// file, a data folder that failed to load). Closing the window while
// something generates asks first. A panic in the UI is logged with its
// stack, pending saves are flushed and the user gets a message instead of a
// vanished window.
func Run(e *studio.Engine, notices ...string) error {
	th, err := gui.NewTheme()
	if err != nil {
		return err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("appui: panic: %v\n%s", r, debug.Stack())
				ferr := e.Flush()
				if ferr != nil {
					log.Printf("flush: %v", ferr)
				}
				crash(r, ferr == nil)
			}
		}()
		w := new(app.Window)
		place, center := windowPlacement(winW, winH)
		w.Option(append([]app.Option{app.Title("YT Cartell Studio"), app.MinSize(winMinW, winMinH)}, place...)...)
		if center {
			w.Perform(system.ActionCenter)
		}
		th.SetInvalidate(w.Invalidate)
		s := newShell(e, th, w.Invalidate, false)
		s.closeFn = func() { w.Perform(system.ActionClose) }
		// The Telegram remote control (Settings → Telegram): it listens only
		// while the remote control is on and paired.
		bot := tgbot.New(e, tgbot.Options{})
		bot.Start()
		for _, n := range notices {
			if strings.TrimSpace(n) != "" {
				th.Toast(gui.ToneError, strings.TrimSpace(n))
			}
		}
		if !e.Config().Get().UI.TourDone {
			s.tourReq = true
		}
		s.startUpdates()
		// The first w.Event (in loop) creates the native window.
		waitForDisplay()
		err := s.loop(w)
		s.Close()
		dictate.Shutdown() // the warm whisper-server of dictation (no kill-on-close job outside Windows)
		// The window is gone: stop running jobs (ffmpeg, downloads) so they
		// record "stopped" instead of dying mid-write, then flush the saves.
		bot.Closing() // live messages of the jobs stopped below say why
		if ferr := e.Shutdown(shutdownWait); ferr != nil {
			log.Printf("shutdown: %v", ferr)
		}
		bot.Stop(3 * time.Second)
		if err != nil {
			Fatal(err)
		}
		if s.relaunch != nil {
			// An update was installed: the window is closed, the jobs are
			// stopped and the saves flushed — start the new build.
			if rerr := s.relaunch(); rerr != nil {
				log.Printf("update: relaunch: %v", rerr)
				showFatal("YT Cartell Studio", "Нову версію встановлено, але не вдалося її запустити ("+rerr.Error()+"). Відкрий студію ще раз.")
			}
		}
		os.Exit(0)
	}()
	app.Main()
	return nil
}

func (s *Shell) loop(w *app.Window) error {
	var ops op.Ops
	focused := false
	for {
		switch ev := w.Event().(type) {
		case app.DestroyEvent:
			return ev.Err
		case *app.ClosingEvent:
			s.onClosing(ev)
		case app.FrameEvent:
			gtx := app.NewContext(&ops, ev)
			s.Layout(gtx)
			ev.Frame(gtx.Ops)
		case app.ConfigEvent:
			// "Як у системі" follows a Windows theme change made while the
			// studio was in the background.
			if ev.Config.Focused && !focused && s.th.Mode() == gui.ModeSystem {
				s.th.SetMode(gui.ModeSystem)
				w.Invalidate()
			}
			focused = ev.Config.Focused
			s.focused = focused // WindowFocused (dictation stops on leaving)
			w.Invalidate()
		default:
			platformEvent(ev)
		}
	}
}

// onClosing keeps the window open while something generates and asks
// first; «Закрити» closes it for real (the jobs stop and keep what is done).
func (s *Shell) onClosing(ev *app.ClosingEvent) {
	if s.closeOK {
		return
	}
	snap := s.feed.Snapshot()
	n := snap.Counts.Running
	if n == 0 {
		return
	}
	ev.Abort()
	s.Confirm(core.Confirm{
		Title: "Закрити програму?",
		Text: fmt.Sprintf("Зараз генерується: %s. Якщо закрити, ці етапи зупиняться — готові кадри, відео й озвучка збережуться, а продовжити можна кнопкою «Повторити».",
			runningLabels(snap)),
		Icon:   gui.IconAlertTriangle,
		OK:     "Закрити",
		Cancel: "Залишитися",
		Danger: true,
		OnOK: func() {
			s.closeOK = true
			if s.closeFn != nil {
				s.closeFn()
			}
		},
	})
}

// startUpdates wires the window to GitHub releases (package update): the
// ETag cache next to config.json, the leftovers of an earlier update
// removed, a "you are on X now" toast after one, and the quiet check
// updateStartDelay after the start (UI.AutoUpdate, at most every 6 h).
func (s *Shell) startUpdates() {
	u := s.upd
	u.src = update.New(config.Dir())
	u.installable = update.Installable
	go update.Cleanup("", u.current)
	if from, ok := update.UpdatedFrom(os.Args[1:]); ok && from != u.current {
		log.Printf("update: now %s (was %s)", u.current, from)
		s.c.Toast(gui.ToneSuccess, "Готово: студію оновлено до версії "+u.current+".")
	}
	if seen := s.e.Config().Get().UI.UpdateSeen; seen != "" && !update.Newer(seen, u.current) {
		e := s.e
		go func() { _ = e.Config().Update(func(c *config.Config) { c.UI.UpdateSeen = "" }) }()
	}
	s.scheduleUpdateCheck(updateStartDelay)
}

// Fatal logs err, shows it in a message box where possible and exits(1).
func Fatal(err error) {
	msg := "Не вдалося запустити YT Cartell Studio"
	if err != nil {
		msg += ":\n\n" + err.Error()
	}
	log.Print(msg)
	showFatal("YT Cartell Studio", msg)
	os.Exit(1)
}

// AlreadyRunning tells that another copy of the studio is open and exits:
// two copies would overwrite each other's settings.
func AlreadyRunning() {
	log.Print("another copy of YT Cartell Studio is running")
	showFatal("YT Cartell Studio", "YT Cartell Studio уже відкрита — переключись на її вікно.\n\nДві копії одночасно перезаписували б налаштування одна одної.")
	os.Exit(0)
}

// crash reports an internal error that stopped the UI and exits(2).
func crash(r any, saved bool) {
	msg := fmt.Sprintf("YT Cartell Studio зупинилася через внутрішню помилку:\n\n%v\n\n", r)
	if saved {
		msg += "Зміни збережено."
	} else {
		msg += "Останні зміни могли не зберегтися."
	}
	if LogPath != "" {
		msg += " Подробиці — у журналі:\n" + LogPath
	}
	log.Print(msg)
	showFatal("YT Cartell Studio", msg)
	os.Exit(2)
}
