package appui

// The updater of the window (package update does the work): a quiet check a
// few seconds after the start (Run only, UI.AutoUpdate, at most every 6 h),
// the rail item «Оновлення» with an orange dot while a newer version is
// known (on phones the dot sits on «Налаштування» — the «Про студію» card
// opens the sheet there), and the «Оновлення» sheet: this version and the
// new one, the release notes, «Завантажити й встановити» with progress and
// «Скасувати», then «Перезапустити зараз» (asks first while something
// generates; closes the window the normal way, so the engine stops its
// jobs and flushes, then starts the new build), «Відкрити сторінку релізу»
// and «Перевірити ще раз». Nothing is ever installed without a click.
// Headless shells have no Updater: they never contact GitHub (screenshots
// pin a state with ShowUpdate, tests inject update.Fake with SetUpdater).

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/update"
	"ytcartell/internal/version"
)

const (
	// updateStartDelay: the start-up check waits for the window to settle.
	updateStartDelay = 5 * time.Second
	// updateEvery: the start-up check runs at most this often.
	updateEvery = 6 * time.Hour
	// updateFresh: opening the sheet re-checks a result older than this.
	updateFresh = 10 * time.Minute
)

type updPhase uint8

const (
	updIdle        updPhase = iota // nothing checked yet
	updChecking                    // asking GitHub
	updLatest                      // this build is the newest
	updAvailable                   // rel is newer
	updDownloading                 // fetching rel's build
	updReady                       // downloaded and verified: restart to install
	updInstalling                  // swapping the files, then the window closes
	updFailed                      // the last step failed (err); stage says which
)

// updStage is the step a failure belongs to.
type updStage uint8

const (
	stageCheck updStage = iota
	stageDownload
	stageInstall
)

// updater is the shell's update state (UI goroutine, except the atomics).
type updater struct {
	src         update.Updater // nil: never checks (headless)
	installable func() error   // nil = installable (fakes)
	current     string

	phase   updPhase
	stage   updStage
	rel     update.Release
	hasRel  bool
	seen    string // a newer version remembered from the last check (config)
	err     string
	errKind update.Kind
	checked time.Time
	staged  string
	install error // installable() result
	instOK  bool  // install is known
	toasted string
	pinned  bool // ShowUpdate: a screenshot state, no actions

	done, total atomic.Int64
	cancel      context.CancelFunc
	seq         int // results of superseded actions are dropped

	drawer                                           gui.Drawer
	primary, cancelBtn, recheck, page, later, notesL gui.Clickable
	bar                                              gui.Anim
	notes                                            []update.Block
	notesOf                                          string
}

func newUpdater(cfg config.Config) *updater {
	u := &updater{current: version.String()}
	if update.Newer(cfg.UI.UpdateSeen, u.current) {
		u.seen = cfg.UI.UpdateSeen
	}
	return u
}

// newer is the newer version known ("" = none): the last check's, else the
// one a check before this start remembered.
func (u *updater) newer() string {
	if u.hasRel {
		if u.rel.Newer(u.current) {
			return u.rel.Version
		}
		return ""
	}
	return u.seen
}

// ---- shell API ---------------------------------------------------------------------------------

// SetUpdater replaces the release channel (tests: update.Fake). nil turns
// the checks off.
func (s *Shell) SetUpdater(src update.Updater) {
	s.upd.src = src
	s.upd.installable = nil
}

// OpenUpdates slides the «Оновлення» sheet in (and checks GitHub when the
// last answer is old). Settings › Про студію calls it too.
func (s *Shell) OpenUpdates() {
	u := s.upd
	s.drawer.Close()
	u.drawer.Open()
	if !u.pinned {
		stale := u.checked.IsZero() || time.Since(u.checked) > updateFresh
		switch u.phase {
		case updIdle:
			s.checkUpdate(true)
		case updLatest, updAvailable:
			if stale {
				s.checkUpdate(true)
			}
		case updFailed:
			if u.stage == stageCheck {
				s.checkUpdate(true)
			}
		}
		s.probeInstall()
	}
	s.Invalidate()
}

// UpdateAvailable is the newer version known ("" = none): the dot on the
// rail and the «Про студію» card.
func (s *Shell) UpdateAvailable() string { return s.upd.newer() }

// ShowUpdate pins the sheet for screenshots: rel available, or downloading
// when progress is in [0, 1].
func (s *Shell) ShowUpdate(rel update.Release, progress float32) {
	u := s.upd
	u.pinned = true
	u.rel, u.hasRel = rel, true
	u.checked = time.Now()
	u.phase = updAvailable
	if progress >= 0 {
		u.phase = updDownloading
		total := int64(48 << 20)
		if a, ok := rel.AssetFor(runtime.GOOS, runtime.GOARCH); ok && a.Size > 0 {
			total = a.Size
		}
		u.total.Store(total)
		u.done.Store(int64(float32(total) * min(1, progress)))
	}
	s.drawer.Close()
	u.drawer.Open()
}

// ---- actions -----------------------------------------------------------------------------------

// scheduleUpdateCheck runs the quiet start-up check after updateStartDelay
// (Run only).
func (s *Shell) scheduleUpdateCheck(after time.Duration) {
	time.AfterFunc(after, func() {
		if s.closed.Load() {
			return
		}
		s.Post(s.autoCheckUpdate)
	})
}

// autoCheckUpdate checks when the user allows it and the last check is old.
func (s *Shell) autoCheckUpdate() {
	u := s.upd
	if u.src == nil || u.phase != updIdle {
		return
	}
	cfg := s.e.Config().Get()
	if !cfg.UI.AutoUpdate {
		return
	}
	if last := time.Unix(cfg.UI.UpdateChecked, 0); cfg.UI.UpdateChecked > 0 && time.Since(last) < updateEvery && time.Since(last) > -time.Hour {
		return
	}
	s.checkUpdate(false)
}

// checkUpdate asks GitHub for the latest release. manual: the user asked
// (errors are shown); else failures stay quiet (offline, rate limit).
func (s *Shell) checkUpdate(manual bool) {
	u := s.upd
	if u.src == nil || u.pinned {
		return
	}
	switch u.phase {
	case updChecking, updDownloading, updReady, updInstalling:
		return
	}
	prev := u.phase
	u.phase = updChecking
	u.seq++
	seq, src := u.seq, u.src
	s.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rel, err := src.Latest(ctx)
		s.Post(func() { s.checked(seq, manual, prev, rel, err) })
	})
	s.Invalidate()
}

func (s *Shell) checked(seq int, manual bool, prev updPhase, rel update.Release, err error) {
	u := s.upd
	if seq != u.seq || u.phase != updChecking {
		return
	}
	if err != nil && update.KindOf(err) != update.KindNoRelease {
		log.Printf("update: check: %v", logErr(err))
		if !manual {
			u.phase = prev // quiet: try again at the next start
			return
		}
		if u.hasRel && prev != updFailed {
			// An earlier answer still stands (its install button too): say
			// why this check failed and keep it.
			u.phase = prev
			s.c.Toast(gui.ToneError, err.Error())
			return
		}
		u.phase, u.stage = updFailed, stageCheck
		u.err, u.errKind = err.Error(), update.KindOf(err)
		return
	}
	u.checked = time.Now()
	if err != nil { // nothing published yet: nothing newer
		u.hasRel, u.rel, u.phase = false, update.Release{}, updLatest
		u.seen = ""
	} else {
		u.rel, u.hasRel = rel, true
		u.phase = updLatest
		if rel.Newer(u.current) {
			u.phase = updAvailable
		}
	}
	newer := u.newer()
	stamp := u.checked.Unix()
	e := s.e
	s.Background(func() {
		if err := e.Config().Update(func(c *config.Config) {
			c.UI.UpdateChecked = stamp
			c.UI.UpdateSeen = newer
		}); err != nil {
			log.Printf("update: remember the check: %v", err)
		}
	})
	if newer != "" && !manual && u.toasted != newer && !u.drawer.IsOpen() {
		u.toasted = newer
		msg := "Є нова версія " + newer + " — натисни «Оновлення»."
		if s.narrow || !s.updInRail {
			msg = "Є нова версія " + newer + " — відкрий «Налаштування» → «Про студію»."
		}
		s.c.Toast(gui.ToneAction, msg)
	}
}

// probeInstall finds out (once, off the UI goroutine) whether this
// install can update itself in place.
func (s *Shell) probeInstall() {
	u := s.upd
	if u.instOK || u.installable == nil {
		u.instOK = true
		return
	}
	u.instOK = true
	probe := u.installable
	s.Background(func() {
		err := probe()
		s.Post(func() { u.install = err })
	})
}

// startDownload fetches the new build (resumes a stopped one).
func (s *Shell) startDownload() {
	u := s.upd
	if u.src == nil || u.pinned || !u.hasRel || u.phase == updDownloading {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	u.cancel = cancel
	u.seq++
	seq, src, rel := u.seq, u.src, u.rel
	u.phase = updDownloading
	u.done.Store(0)
	u.total.Store(0)
	if a, ok := rel.AssetFor(runtime.GOOS, runtime.GOARCH); ok {
		u.total.Store(a.Size)
	}
	progress := update.Throttle(50*time.Millisecond, func(done, total int64) {
		u.done.Store(done)
		u.total.Store(total)
		s.Invalidate()
	})
	// Its own goroutine, not Background: a long download must not hold up
	// the headless renders that wait for background work.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("update: download panic: %v\n%s", r, debug.Stack())
				s.Post(func() {
					s.downloaded(seq, "", errors.New("Внутрішня помилка — подробиці в журналі."))
				})
			}
		}()
		path, err := src.Download(ctx, rel, progress)
		s.Post(func() { s.downloaded(seq, path, err) })
	}()
	s.Invalidate()
}

func (s *Shell) downloaded(seq int, path string, err error) {
	u := s.upd
	if seq != u.seq {
		return
	}
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	if errors.Is(err, context.Canceled) {
		u.phase = updAvailable
		return
	}
	if err != nil {
		log.Printf("update: download %s: %v", u.rel.Version, logErr(err))
		u.phase, u.stage = updFailed, stageDownload
		u.err, u.errKind = err.Error(), update.KindOf(err)
		return
	}
	u.staged, u.phase = path, updReady
	if !u.drawer.IsOpen() {
		s.c.Toast(gui.ToneSuccess, "Версію "+u.rel.Version+" завантажено — відкрий «Оновлення» й перезапусти студію.")
	}
}

// cancelDownload stops the download (the part stays for a resume).
func (s *Shell) cancelDownload() {
	u := s.upd
	if u.phase != updDownloading {
		return
	}
	u.seq++
	if u.cancel != nil {
		u.cancel()
		u.cancel = nil
	}
	u.phase = updAvailable
}

// restartForUpdate installs and restarts — after a question while
// something generates (the jobs stop like on window close).
func (s *Shell) restartForUpdate() {
	u := s.upd
	if u.phase != updReady && !(u.phase == updFailed && u.stage == stageInstall) {
		return
	}
	snap := s.feed.Snapshot()
	if n := snap.Counts.Running; n > 0 {
		c := confirmRestart(snap)
		c.OnOK = s.installUpdate
		s.Confirm(c)
		return
	}
	s.installUpdate()
}

// installUpdate swaps the files, then closes the window the normal way;
// Run starts the new build after the engine stopped and flushed.
func (s *Shell) installUpdate() {
	u := s.upd
	if u.src == nil || u.staged == "" || u.phase == updInstalling {
		return
	}
	u.phase = updInstalling
	src, staged, from := u.src, u.staged, u.current
	s.Background(func() {
		restart, err := src.Apply(staged)
		s.Post(func() {
			if err != nil {
				log.Printf("update: install: %v", logErr(err))
				u.phase, u.stage = updFailed, stageInstall
				u.err, u.errKind = err.Error(), update.KindOf(err)
				if k := u.errKind; k == update.KindManual || k == update.KindPermission {
					u.install = err
				}
				return
			}
			log.Printf("update: %s installed over %s; restarting", u.rel.Version, from)
			s.relaunch = func() error { return restart(from) }
			if s.closeFn != nil {
				s.closeOK = true
				s.closeFn()
			}
		})
	})
}

// openReleasePage opens the release (or the releases list) in the browser.
func (s *Shell) openReleasePage() {
	u := s.upd
	url := update.ReleasesURL()
	if u.hasRel {
		url = u.rel.PageURL()
	}
	s.c.OpenPath(url)
}

// logErr adds the cause of an update.Error for the log.
func logErr(err error) string {
	var e *update.Error
	if errors.As(err, &e) && e.Err != nil {
		return e.Msg + " (" + e.Err.Error() + ")"
	}
	return err.Error()
}

// ---- the sheet ---------------------------------------------------------------------------------

// canInstall: the build for this computer exists and can be swapped in.
func (u *updater) canInstall() bool {
	if !u.hasRel || u.install != nil {
		return false
	}
	_, ok := u.rel.AssetFor(runtime.GOOS, runtime.GOARCH)
	return ok
}

// layoutUpdate handles the sheet's clicks and queues it (UI goroutine).
func (s *Shell) layoutUpdate(gtx C) {
	u := s.upd
	th := s.th
	if u.drawer.Dismissed() {
		u.drawer.Close()
	}
	if !u.pinned {
		if u.primary.Clicked(gtx) {
			switch {
			case u.phase == updReady || u.phase == updFailed && u.stage == stageInstall && u.install == nil:
				s.restartForUpdate()
			case (u.phase == updAvailable || u.phase == updFailed && u.stage == stageDownload) && u.canInstall():
				s.startDownload()
			case u.phase == updAvailable || u.phase == updFailed && u.hasRel && u.rel.Newer(u.current):
				s.openReleasePage()
			default:
				s.checkUpdate(true)
			}
		}
		if u.cancelBtn.Clicked(gtx) {
			s.cancelDownload()
		}
		if u.recheck.Clicked(gtx) {
			s.checkUpdate(true)
		}
		if u.page.Clicked(gtx) || u.notesL.Clicked(gtx) {
			s.openReleasePage()
		}
		if u.later.Clicked(gtx) {
			u.drawer.Close()
		}
	}
	u.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Оновлення",
		Subtitle: u.subtitle(),
		Width:    540,
		Body:     func(gtx C) D { return s.updateBody(gtx) },
		Footer:   func(gtx C) D { return s.updateFooter(gtx) },
	})
}

func (u *updater) subtitle() string {
	switch u.phase {
	case updChecking:
		return "Перевіряємо GitHub…"
	case updLatest:
		return "У тебе найновіша версія."
	case updAvailable:
		return "Вийшла версія " + u.rel.Version + "."
	case updDownloading:
		return "Завантажуємо версію " + u.rel.Version + "…"
	case updReady:
		return "Версію " + u.rel.Version + " завантажено й перевірено."
	case updInstalling:
		return "Встановлюємо версію " + u.rel.Version + "…"
	case updFailed:
		switch u.stage {
		case stageDownload:
			return "Не вдалося завантажити оновлення."
		case stageInstall:
			return "Не вдалося встановити оновлення."
		}
		return "Не вдалося перевірити оновлення."
	}
	return "YT Cartell Studio · версія " + u.current
}

func (s *Shell) updateBody(gtx C) D {
	u := s.upd
	th := s.th
	ws := []layout.Widget{
		func(gtx C) D { return s.updateVersions(gtx) },
		func(gtx C) D { return s.updateStatus(gtx) },
	}
	newer := u.hasRel && u.rel.Newer(u.current)
	if newer {
		if u.notesOf != u.rel.Tag {
			u.notes, u.notesOf = update.ParseNotes(u.rel.Notes), u.rel.Tag
		}
		if len(u.notes) > 0 {
			ws = append(ws, func(gtx C) D { return s.updateNotes(gtx) })
		}
		if a, ok := u.rel.AssetFor(runtime.GOOS, runtime.GOARCH); ok && a.Size > 0 {
			ws = append(ws, gui.Text{Style: th.Small, Color: th.Pal.Text3}.Widget(th,
				"Розмір: "+sizeText(a.Size)+" · "+a.Name))
		}
	}
	return gui.VStack(gtx, gui.Sp20, ws...)
}

// updateVersions: this build and the new one, side by side in wells.
func (s *Shell) updateVersions(gtx C) D {
	u := s.upd
	th := s.th
	well := func(label, value, caption string, accent bool) layout.Widget {
		return func(gtx C) D {
			pad := gtx.Dp(gui.Sp16)
			W := gtx.Constraints.Max.X
			cg := gtx
			cg.Constraints = layout.Constraints{Max: image.Pt(max(0, W-2*pad), gtx.Constraints.Max.Y)}
			d, call := record(cg, func(gtx C) D {
				ink := th.Pal.Text
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, label)),
					layout.Rigid(layout.Spacer{Height: 2}.Layout),
					layout.Rigid(gui.Text{Style: th.H2.Sized(22), Color: ink, MaxLines: 1}.Widget(th, value)),
					layout.Rigid(func(gtx C) D {
						if caption == "" {
							return D{}
						}
						return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, caption))
					}),
				)
			})
			size := image.Pt(W, max(d.Size.Y+2*pad, gtx.Constraints.Min.Y))
			fill := th.Pal.Surface
			border := color.NRGBA{}
			if accent {
				fill, border = th.Pal.ActionSoft, gui.WithAlpha(th.Pal.Action, 0.35)
			}
			gui.Box(gtx, image.Rectangle{Max: size}, gui.RadiusInner, fill, border)
			o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
			call.Add(gtx.Ops)
			o.Pop()
			return D{Size: size}
		}
	}
	built := buildDate()
	if built == "" {
		built = "на цьому комп'ютері"
	}
	cur := well("Встановлено", u.current, built, false)
	newer := u.hasRel && u.rel.Newer(u.current)
	if !newer {
		label, value, caption := "Найновіша на GitHub", "—", ""
		switch {
		case u.hasRel:
			value, caption = u.rel.Version, ukDate(u.rel.Published)
		case u.phase == updLatest:
			value = "ще немає"
		}
		if u.phase == updChecking || u.phase == updIdle {
			value = "…"
		}
		return s.pair(gtx, cur, well(label, value, caption, false), false)
	}
	return s.pair(gtx, cur, well("Нова версія", u.rel.Version, ukDate(u.rel.Published), true), true)
}

// pair lays two wells in a row (an arrow between them when arrow), or one
// under the other on a narrow sheet.
func (s *Shell) pair(gtx C, a, b layout.Widget, arrow bool) D {
	W := gtx.Constraints.Max.X
	if W < gtx.Dp(360) {
		return gui.VStack(gtx, gui.Sp12, a, b)
	}
	gap := gtx.Dp(gui.Sp12)
	aw := 0
	if arrow {
		aw = gtx.Dp(20) + gap
	}
	cw := (W - gap - aw) / 2
	cg := gtx
	cg.Constraints = layout.Constraints{Min: image.Pt(cw, 0), Max: image.Pt(cw, gtx.Constraints.Max.Y)}
	ad, ac := record(cg, a)
	bd, bc := record(cg, b)
	h := max(ad.Size.Y, bd.Size.Y)
	// Both wells as tall as the taller one.
	cg.Constraints.Min.Y = h
	if ad.Size.Y < h {
		_, ac = record(cg, a)
	}
	if bd.Size.Y < h {
		_, bc = record(cg, b)
	}
	ac.Add(gtx.Ops)
	x := cw + gap
	if arrow {
		ic := gtx.Dp(20)
		o := op.Offset(image.Pt(x, (h-ic)/2)).Push(gtx.Ops)
		gui.IconArrowRight.Layout(gtx, 20, s.th.Pal.Text3)
		o.Pop()
		x += aw
	}
	o := op.Offset(image.Pt(x, 0)).Push(gtx.Ops)
	bc.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(W, h)}
}

// updateStatus is the line under the versions: busy, done, ready or what failed.
func (s *Shell) updateStatus(gtx C) D {
	u := s.upd
	th := s.th
	spin := func(gtx C) D { return gui.Spinner{Size: 18, Color: th.Pal.Running}.Layout(gtx, th) }
	switch u.phase {
	case updChecking:
		return statusRow(gtx, th, spin, "Перевіряємо, чи вийшла нова версія…")
	case updInstalling:
		return statusRow(gtx, th, spin, "Встановлюємо оновлення — студія зараз перезапуститься.")
	case updLatest:
		hint := ""
		if !u.checked.IsZero() {
			hint = "Перевірено о " + u.checked.Format("15:04") + "."
		}
		return statusNote(gtx, th, gui.ToneSuccess, gui.IconCheckCircle, "У тебе найновіша версія студії.", hint)
	case updReady:
		return statusNote(gtx, th, gui.ToneSuccess, gui.IconShieldCheck, "Завантажено й перевірено (SHA-256).",
			"Перезапусти студію, щоб перейти на нову версію. Проєкти й налаштування збережуться; якщо щось генерується — спершу спитаємо.")
	case updDownloading:
		return s.updateProgress(gtx)
	case updFailed:
		hint := ""
		switch u.errKind {
		case update.KindRateLimited, update.KindOffline:
			hint = "Студія працює як звичайно — перевір пізніше."
		case update.KindManual, update.KindPermission:
			hint = "Нову версію можна завантажити зі сторінки релізу й замінити програму вручну."
		}
		return statusNote(gtx, th, gui.ToneError, gui.IconAlertTriangle, u.err, hint)
	case updAvailable:
		if u.install != nil {
			return statusNote(gtx, th, gui.ToneWarning, gui.IconInfo, strings.TrimSpace(u.install.Error()), "")
		}
		if _, ok := u.rel.AssetFor(runtime.GOOS, runtime.GOARCH); !ok {
			return statusNote(gtx, th, gui.ToneWarning, gui.IconInfo, "У цьому релізі немає збірки для твого комп'ютера.",
				"Подивись сторінку релізу — можливо, її додадуть трохи пізніше.")
		}
		return gui.Text{Style: th.Body, Color: th.Pal.Text2}.Layout(gtx, th,
			"Студія завантажить нову версію, перевірить її цифровий відбиток і встановить після перезапуску. Твої проєкти й налаштування не зміняться.")
	}
	return gui.Text{Style: th.Body, Color: th.Pal.Text2}.Layout(gtx, th,
		"Студія дивиться на GitHub, чи вийшла нова версія, і нічого не встановлює без тебе.")
}

// updateProgress is the download bar with its numbers.
func (s *Shell) updateProgress(gtx C) D {
	u := s.upd
	th := s.th
	done, total := u.done.Load(), u.total.Load()
	frac := float32(-1)
	label := "Завантажено " + sizeText(done)
	if total > 0 {
		frac = min(1, float32(done)/float32(total))
		label = fmt.Sprintf("Завантажено %s з %s · %d %%", sizeText(done), sizeText(total), int(frac*100+0.5))
	}
	u.bar.Duration = gui.DurValue
	return gui.VStack(gtx, gui.Sp8,
		func(gtx C) D {
			return gui.ProgressBar{Value: frac, Tone: gui.ToneRunning, Anim: &u.bar}.Layout(gtx, th)
		},
		gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, label),
	)
}

// updateNotes renders the release notes as headings, paragraphs and bullets.
func (s *Shell) updateNotes(gtx C) D {
	u := s.upd
	th := s.th
	const maxBlocks = 40
	blocks := u.notes
	more := len(blocks) > maxBlocks
	if more {
		blocks = blocks[:maxBlocks]
	}
	// The notes' own first heading ("## Що нового") titles the section.
	title := "Що нового"
	if len(blocks) > 0 && blocks[0].Kind == update.Heading {
		title, blocks = blocks[0].Text, blocks[1:]
	}
	ws := []layout.Widget{gui.Text{Style: th.H3}.Widget(th, title)}
	for _, b := range blocks {
		b := b
		switch b.Kind {
		case update.Heading:
			ws = append(ws, func(gtx C) D {
				return layout.Inset{Top: gui.Sp4}.Layout(gtx, gui.Text{Style: th.BodyStrong}.Widget(th, b.Text))
			})
		case update.Bullet:
			ws = append(ws, func(gtx C) D { return bullet(gtx, th, b) })
		default:
			ws = append(ws, gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, b.Text))
		}
	}
	link := "Сторінка релізу на GitHub"
	if more {
		link = "Повний опис — на сторінці релізу"
	}
	ws = append(ws, func(gtx C) D {
		return layout.Inset{Top: gui.Sp4}.Layout(gtx, func(gtx C) D {
			return gui.Link{Text: link, Trail: gui.IconExternalLink}.Layout(gtx, th, &u.notesL)
		})
	})
	return gui.VStack(gtx, gui.Sp8, ws...)
}

// bullet is one list item: a dot (or its number) and the wrapped text.
func bullet(gtx C, th *gui.Theme, b update.Block) D {
	indent := 0
	if b.Level > 0 {
		indent = gtx.Dp(gui.Sp20)
	}
	mw := gtx.Dp(18)
	if b.Marker != "" {
		mw = max(mw, th.TextWidth(gtx, th.Body, b.Marker)+gtx.Dp(6))
	}
	W := gtx.Constraints.Max.X
	tg := gtx
	tg.Constraints = layout.Constraints{Max: image.Pt(max(0, W-indent-mw), gtx.Constraints.Max.Y)}
	d, call := record(tg, gui.Text{Style: th.Body}.Widget(th, b.Text))
	line := th.Body.LinePx(gtx)
	if b.Marker != "" {
		o := op.Offset(image.Pt(indent, 0)).Push(gtx.Ops)
		gui.Text{Style: th.Body, Color: th.Pal.Text2}.Layout(gtx, th, b.Marker)
		o.Pop()
	} else {
		dot := gtx.Dp(6)
		o := op.Offset(image.Pt(indent+gtx.Dp(4), (line-dot)/2)).Push(gtx.Ops)
		gui.FillCircle(gtx, 6, th.Pal.Text3)
		o.Pop()
	}
	o := op.Offset(image.Pt(indent+mw, 0)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(W, d.Size.Y)}
}

// statusRow: a leading widget (a spinner) and a line of text.
func statusRow(gtx C, th *gui.Theme, lead layout.Widget, text string) D {
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(lead),
		layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
		layout.Flexed(1, gui.Text{Style: th.Body, Color: th.Pal.Text2}.Widget(th, text)),
	)
}

// statusNote is a soft tone panel with an icon, a message and a hint.
func statusNote(gtx C, th *gui.Theme, tone gui.Tone, icon *gui.Icon, msg, hint string) D {
	_, soft, ink := th.Pal.ToneColors(tone)
	pad := gtx.Dp(gui.Sp16)
	is := gtx.Dp(20)
	gap := gtx.Dp(gui.Sp12)
	W := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, W-2*pad-is-gap), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium), Color: ink}.Widget(th, msg))}
		if hint != "" {
			ch = append(ch, layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, hint)))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	size := image.Pt(W, d.Size.Y+2*pad)
	gui.FillRRect(gtx, image.Rectangle{Max: size}, gui.RadiusInner, soft)
	line := th.Body.LinePx(gtx)
	o := op.Offset(image.Pt(pad, pad+(line-is)/2)).Push(gtx.Ops)
	icon.Layout(gtx, 20, ink)
	o.Pop()
	o = op.Offset(image.Pt(pad+is+gap, pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: size}
}

// updateFooter: the actions of the phase — the primary one on the right.
func (s *Shell) updateFooter(gtx C) D {
	u := s.upd
	th := s.th
	newer := u.hasRel && u.rel.Newer(u.current)
	btn := func(b gui.Button, c *gui.Clickable) layout.Widget {
		return func(gtx C) D { return b.Layout(gtx, th, c) }
	}
	pageBtn := btn(gui.Button{Kind: gui.Ghost, Icon: gui.IconExternalLink, Text: "Сторінка релізу"}, &u.page)
	recheck := btn(gui.Button{Kind: gui.Ghost, Icon: gui.IconRefreshCw, Tip: "Перевірити ще раз"}, &u.recheck)
	var lead []layout.Widget
	var primary layout.Widget
	switch {
	case u.phase == updDownloading:
		lead = []layout.Widget{btn(gui.Button{Kind: gui.Tinted, Text: "Скасувати"}, &u.cancelBtn)}
		primary = btn(gui.Button{Kind: gui.Primary, Text: "Завантажуємо…", Busy: true}, &u.primary)
	case u.phase == updInstalling:
		primary = btn(gui.Button{Kind: gui.Primary, Text: "Перезапускаємо…", Busy: true}, &u.primary)
	case u.phase == updReady:
		lead = []layout.Widget{btn(gui.Button{Kind: gui.Ghost, Text: "Пізніше"}, &u.later)}
		primary = btn(gui.Button{Kind: gui.Primary, Icon: gui.IconRefreshCw, Text: "Перезапустити зараз"}, &u.primary)
	case u.phase == updFailed && u.stage == stageInstall && u.install == nil:
		lead = []layout.Widget{pageBtn}
		primary = btn(gui.Button{Kind: gui.Primary, Icon: gui.IconRefreshCw, Text: "Спробувати ще раз"}, &u.primary)
	case (u.phase == updAvailable || u.phase == updFailed && u.stage == stageDownload) && u.canInstall():
		lead = []layout.Widget{pageBtn, recheck}
		text := "Завантажити й встановити"
		if u.phase == updFailed {
			text = "Завантажити ще раз"
		}
		primary = btn(gui.Button{Kind: gui.Primary, Icon: gui.IconDownload, Text: text}, &u.primary)
	case newer && u.phase != updChecking:
		lead = []layout.Widget{recheck}
		primary = btn(gui.Button{Kind: gui.Primary, Icon: gui.IconExternalLink, Text: "Відкрити сторінку релізу"}, &u.primary)
	default:
		lead = []layout.Widget{pageBtn}
		primary = btn(gui.Button{Kind: gui.Secondary, Icon: gui.IconRefreshCw, Text: "Перевірити ще раз",
			Busy: u.phase == updChecking, Disabled: u.src == nil && !u.pinned}, &u.primary)
	}
	if gtx.Constraints.Max.X < gtx.Dp(400) {
		// A narrow sheet: the main action full width, the rest under it.
		ws := []layout.Widget{func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return primary(gtx)
		}}
		if len(lead) > 0 {
			ws = append(ws, func(gtx C) D { return gui.HStack(gtx, gui.Sp8, lead...) })
		}
		return gui.VStack(gtx, gui.Sp8, ws...)
	}
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx C) D { return gui.HStack(gtx, gui.Sp4, lead...) }),
		layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} }),
		layout.Rigid(primary),
	)
}

// ---- small helpers -----------------------------------------------------------------------------

// sizeText is a byte count in Ukrainian units: "38,4 МБ", "820 КБ".
func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return strings.Replace(fmt.Sprintf("%.1f ГБ", float64(n)/(1<<30)), ".", ",", 1)
	case n >= 1<<20:
		return strings.Replace(fmt.Sprintf("%.1f МБ", float64(n)/(1<<20)), ".", ",", 1)
	case n >= 1<<10:
		return fmt.Sprintf("%d КБ", n>>10)
	}
	return fmt.Sprintf("%d Б", n)
}

// ukDate is "30 вересня 2026" ("" for a zero time).
func ukDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	months := [...]string{"січня", "лютого", "березня", "квітня", "травня", "червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}
	t = t.Local()
	return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
}

// buildDate is this build's day ("" when unknown).
func buildDate() string {
	if t := version.Built(); !t.IsZero() {
		return "збірка " + ukDate(t)
	}
	return ""
}

// runningLabels names up to three running jobs: "Відео («Лист»), …".
func runningLabels(snap studio.QueueSnapshot) string {
	var labels []string
	for _, it := range snap.Running {
		if len(labels) == 3 {
			labels = append(labels, "…")
			break
		}
		l := it.Label
		if t := strings.TrimSpace(it.ProjectTitle); t != "" {
			l += " («" + t + "»)"
		}
		labels = append(labels, l)
	}
	return strings.Join(labels, ", ")
}

// confirmRestart asks before an update restart stops running jobs.
func confirmRestart(snap studio.QueueSnapshot) core.Confirm {
	return core.Confirm{
		Title: "Перезапустити зараз?",
		Text: "Зараз генерується: " + runningLabels(snap) +
			". Під час перезапуску ці етапи зупиняться — готові кадри, відео й озвучка збережуться, а продовжити можна кнопкою «Повторити».",
		Icon:   gui.IconRefreshCw,
		OK:     "Перезапустити",
		Cancel: "Пізніше",
		Danger: true,
	}
}
