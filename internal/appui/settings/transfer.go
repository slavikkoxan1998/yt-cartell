package settings

// «Перенести налаштування» — the transfer block of the «Дані» card and its
// two sheets (gui.Drawer layers, like the global sheets of package dialogs):
//
//	Експорт  what to take (connections & keys, profiles, defaults on; the
//	         voice-over key states off), a password typed twice with a
//	         strength hint — or «Без пароля (небезпечно)» with a red warning —
//	         then the native save dialog: YT-Cartell-Studio-settings-<date>.ytcs
//	Імпорт   the native open dialog → the file's facts (version, system,
//	         date) → its password → what is inside with counts, a switch and
//	         «Замінити» / «Об'єднати» per section, the other computer's paths
//	         as opt-in hints (offered only when they exist here) → the result
//	         with «Відновити попередні» (the backup the engine saved first)
//
// The file format, crypto and merge rules live in config/export.go, the
// engine side in studio/settingsio.go. Reading and writing files, deriving
// keys and applying the import run in the background.

import (
	"context"
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/dictate"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/tts"
	"ytcartell/internal/version"
)

// The sections of a transfer, in the order the sheets list them.
const (
	secConnections = iota
	secProfiles
	secDefaults
	secKeyStates
	secCount
)

// The machine-specific hints an import may offer.
const (
	hintDataDir = iota
	hintFFmpeg
	hintWhisper
	hintMic
	hintCount
)

// Microphone hint states (FFmpeg lists the devices in the background).
const (
	micUnknown = iota
	micChecking
	micFound
	micMissing
	micUnchecked // no FFmpeg, or a demo render: cannot tell
)

// minPassword is the shortest password an export accepts.
const minPassword = 8

// settingsIO is the state of the block and its sheets (in dataCard).
type settingsIO struct {
	exportBtn, importBtn gui.Clickable
	exp                  exportSheet
	imp                  importSheet

	backups     []config.Backup // saved before imports, newest first
	backupsBusy bool
	backupsOK   bool
	restoreBtn  gui.Clickable
	restoring   bool
	result      string // what the last import of this visit did (shown in the card)

	// pickSave / pickOpen replace the native dialogs in tests (nil = the
	// core.Ctx ones, which never open in demo renders).
	pickSave func(title, name string, then func(path string))
	pickOpen func(title string, then func(path string))
}

// ---- the block in the «Дані» card ---------------------------------------------------------

func (p *Page) transferBlock(gtx C, c *core.Ctx) D {
	th := c.Th
	t := &p.data.io
	p.loadBackups(c)
	if t.exportBtn.Clicked(gtx) {
		t.exp.open(c)
	}
	if t.importBtn.Clicked(gtx) && !t.imp.loading {
		p.chooseImportFile(c)
	}
	if t.restoreBtn.Clicked(gtx) && len(t.backups) > 0 && !t.restoring {
		p.askRestore(c, t.backups[0])
	}
	ws := []layout.Widget{
		gui.Text{Style: th.Label}.Widget(th, "Перенести налаштування на інший комп'ютер"),
		gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th,
			"Ключі, підключення, профілі й типові налаштування — в одному файлі .ytcs. На іншому комп'ютері натисни тут «Імпортувати…», і все стане як тут. У файлі твої ключі: став пароль і не пересилай його в чати."),
		actions(
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconUpload, Text: "Експортувати налаштування…"}.Layout(gtx, th, &t.exportBtn)
			},
			func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Icon: gui.IconDownload, Text: "Імпортувати…", Busy: t.imp.loading}.Layout(gtx, th, &t.importBtn)
			},
		),
	}
	if t.result != "" {
		ws = append(ws, func(gtx C) D {
			return resultPanel(gtx, th, false, true, t.result, "", "Налаштування імпортовано")
		})
	}
	if len(t.backups) > 0 {
		b := t.backups[0]
		ws = append(ws, func(gtx C) D {
			return note(gtx, th, gui.IconHistory, "Перед останнім імпортом студія зберегла копію налаштувань ("+fullDate(b.Time)+").",
				func(gtx C) D {
					gtx.Constraints.Min.X = 0
					return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconRotateCcw, Text: "Відновити попередні", Busy: t.restoring}.Layout(gtx, th, &t.restoreBtn)
				})
		})
	}
	p.exportSheet(gtx, c)
	p.importSheet(gtx, c)
	return gui.VStack(gtx, gui.Sp12, ws...)
}

// loadBackups reads the backups once per visit (and after an import).
func (p *Page) loadBackups(c *core.Ctx) {
	t := &p.data.io
	if t.backupsOK || t.backupsBusy {
		return
	}
	t.backupsBusy = true
	e := c.E
	c.Background(func() {
		list := e.SettingsBackups()
		c.Post(func() { t.backups, t.backupsBusy, t.backupsOK = list, false, true })
	})
}

func (p *Page) askRestore(c *core.Ctx, b config.Backup) {
	c.Confirm(core.Confirm{
		Title: "Відновити попередні налаштування?",
		Text: "Ключі, підключення, профілі й типові налаштування стануть такими, як були " + fullDate(b.Time) +
			", перед імпортом. Те, що ти змінив після цього, загубиться.",
		Icon: gui.IconHistory,
		OK:   "Відновити",
		OnOK: func() { p.restoreSettings(c, b) },
	})
}

func (p *Page) restoreSettings(c *core.Ctx, b config.Backup) {
	t := &p.data.io
	if t.restoring {
		return
	}
	t.restoring = true
	p.flushFields()
	p.cfg.barrier(c, func() {
		c.Background(func() {
			err := c.E.RestoreSettings(b)
			c.Post(func() {
				t.restoring = false
				if err != nil {
					c.Error(fmt.Errorf("не вдалося відновити налаштування: %w", err))
					return
				}
				t.result, t.backupsOK = "", false
				t.imp.drawer.Close()
				c.ReloadVoices()
				c.Toast(gui.ToneSuccess, "Попередні налаштування повернуто.")
			})
		})
	})
}

// ---- export -----------------------------------------------------------------------------------

type exportSheet struct {
	drawer  gui.Drawer
	rows    [secCount]sectionRow
	on      [secCount]bool
	sum     *config.Bundle // the current settings, summarised when the sheet opened
	protect gui.Segmented
	plain   bool
	pw1     gui.TextField
	pw2     gui.TextField

	run, cancel gui.Clickable
	busy        bool
	err         string
	errA        gui.Anim

	saved          string // the written file ("" = not yet)
	reveal, finish gui.Clickable
}

func (s *exportSheet) open(c *core.Ctx) {
	s.on = [secCount]bool{true, true, true, false}
	s.plain, s.busy, s.err, s.saved = false, false, "", ""
	s.pw1.SetText("")
	s.pw2.SetText("")
	s.sum = c.E.SettingsBundle(config.ExportOptions{Connections: true, Profiles: true, Defaults: true, KeyStates: true})
	s.drawer.Open()
}

func (s *exportSheet) close() {
	s.drawer.Close()
	s.pw1.SetText("")
	s.pw2.SetText("")
}

func (p *Page) exportSheet(gtx C, c *core.Ctx) {
	th := c.Th
	t := &p.data.io
	s := &t.exp
	if s.drawer.Dismissed() {
		s.close()
	}
	if s.cancel.Clicked(gtx) || s.finish.Clicked(gtx) {
		s.close()
	}
	if s.reveal.Clicked(gtx) && s.saved != "" {
		c.Reveal(s.saved)
	}
	if v, ok := s.protect.Changed(gtx); ok {
		s.plain = v == "plain"
		s.err = ""
	}
	if _, ok := s.pw1.Changed(gtx); ok {
		s.err = ""
	}
	if _, ok := s.pw2.Changed(gtx); ok {
		s.err = ""
	}
	if s.run.Clicked(gtx) || (s.pw2.Submitted(gtx) && !s.plain) {
		p.startExport(c)
	}
	any := false
	for _, on := range s.on {
		any = any || on
	}
	body := func(gtx C) D {
		if s.saved != "" {
			return s.doneBody(gtx, c)
		}
		ws := []layout.Widget{gui.Text{Style: th.Label}.Widget(th, "Що взяти")}
		ws = append(ws, s.sectionRows(th)...)
		ws = append(ws,
			func(gtx C) D {
				return note(gtx, th, gui.IconMapPin, "Шляхи цього комп'ютера — папка з проєктами, FFmpeg, whisper.cpp, мікрофон — підуть лише як підказки: на іншому комп'ютері студія спитає, чи брати їх.", nil)
			},
			layout.Spacer{Height: gui.Sp4}.Layout,
			gui.Text{Style: th.Label}.Widget(th, "Захист файлу"),
			func(gtx C) D { return s.protection(gtx, th) },
		)
		return gui.VStack(gtx, gui.Sp12, ws...)
	}
	foot := func(gtx C) D {
		if s.saved != "" {
			return sheetButtons(gtx, th, nil, nil, nil, func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Primary, Icon: gui.IconCheck, Text: "Готово", Full: full}.Layout(gtx, th, &s.finish)
			})
		}
		return sheetButtons(gtx, th, &s.err, &s.errA,
			func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Tinted, Text: "Скасувати", Full: full}.Layout(gtx, th, &s.cancel)
			},
			func(gtx C, full bool) D {
				return gui.Button{Kind: gui.Primary, Icon: gui.IconUpload, Text: "Експортувати…", Busy: s.busy, Disabled: !any, Full: full}.Layout(gtx, th, &s.run)
			})
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{
		Title:    "Експорт налаштувань",
		Subtitle: "Один файл, щоб на іншому комп'ютері все стало як тут.",
		Width:    560,
		Body:     body,
		Footer:   foot,
	})
}

// sectionRows are the four switches of the export.
func (s *exportSheet) sectionRows(th *gui.Theme) []layout.Widget {
	b := s.sum
	captions := [secCount]string{
		connectionsSummary(b.Connections),
		profilesSummary(b.Profiles),
		defaultsSummary(b.Defaults),
		"Які ключі вичерпали місячний ліміт і скільки використано. Зазвичай не потрібно: студія дізнається це сама.",
	}
	if n, ex := b.KeyStateCounts(); n > 0 {
		captions[secKeyStates] = keyStatesSummary(n, ex) + ". Зазвичай не потрібно: студія дізнається це сама."
	}
	var ws []layout.Widget
	for i := 0; i < secCount; i++ {
		i := i
		ws = append(ws, func(gtx C) D {
			return s.rows[i].layout(gtx, th, rowStyle{Icon: sectionIcons[i], Title: sectionTitles[i], Caption: captions[i], On: s.on[i]},
				func(v bool) { s.on[i] = v; s.err = "" }, nil)
		})
	}
	return ws
}

// protection is the password block (or the warning of a plain file).
func (s *exportSheet) protection(gtx C, th *gui.Theme) D {
	narrow := gtx.Constraints.Max.X < gtx.Dp(400)
	plainLabel, pwLabel := "Без пароля (небезпечно)", "З паролем"
	var plainIcon, pwIcon *gui.Icon = gui.IconAlertTriangle, gui.IconLock
	if narrow {
		plainLabel, plainIcon, pwIcon = "Без пароля", nil, nil
	}
	mode := "password"
	if s.plain {
		mode = "plain"
	}
	seg := func(gtx C) D {
		s.protect.Full = true
		return s.protect.Layout(gtx, th, mode, []gui.SegOption{
			{Value: "password", Label: pwLabel, Icon: pwIcon},
			{Value: "plain", Label: plainLabel, Icon: plainIcon},
		})
	}
	if s.plain {
		return gui.VStack(gtx, gui.Sp12, seg, func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneError, gui.IconAlertTriangle, "Небезпечно: без пароля файл прочитає будь-хто",
				"У ньому всі твої ключі відкритим текстом. Не пересилай його в чати, не клади в спільні папки, а після перенесення — видали.")
		})
	}
	pw1, pw2 := s.pw1.Text(), s.pw2.Text()
	mismatch := ""
	if pw2 != "" && !strings.HasPrefix(pw1, pw2) {
		mismatch = "Паролі не збігаються."
	}
	return gui.VStack(gtx, gui.Sp12, seg,
		func(gtx C) D {
			return s.pw1.Layout(gtx, th, gui.FieldStyle{Label: "Пароль", Placeholder: "Щонайменше 8 символів", Password: true, Leading: gui.IconKeyRound})
		},
		func(gtx C) D { return strengthMeter(gtx, th, pw1) },
		func(gtx C) D {
			return s.pw2.Layout(gtx, th, gui.FieldStyle{Label: "Ще раз", Placeholder: "Той самий пароль", Password: true, Leading: gui.IconKeyRound,
				Error: mismatch, Submit: true})
		},
		func(gtx C) D {
			return note(gtx, th, gui.IconLock, "Запам'ятай пароль: без нього файл не відкрити, а відновити його неможливо.", nil)
		},
	)
}

func (s *exportSheet) doneBody(gtx C, c *core.Ctx) D {
	th := c.Th
	ws := []layout.Widget{
		func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneSuccess, gui.IconCheckCircle, "Файл налаштувань збережено",
				"Перенеси його на інший комп'ютер (флешка, своя хмара) і там відкрий: Налаштування → Дані → «Імпортувати…».")
		},
		func(gtx C) D { return pathWell(gtx, th, gui.IconFileText, s.saved, "") },
		actions(func(gtx C) D {
			return gui.Button{Kind: gui.Secondary, Icon: gui.IconFolderOpen, Text: "Показати в папці"}.Layout(gtx, th, &s.reveal)
		}),
	}
	if s.plain {
		ws = append(ws, func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneWarning, gui.IconAlertTriangle, "Файл без пароля",
				"Після перенесення видали його звідусіль, де він полежав: у ньому відкриті ключі.")
		})
	}
	return gui.VStack(gtx, gui.Sp16, ws...)
}

// startExport checks the choices, asks where to save and writes the file.
func (p *Page) startExport(c *core.Ctx) {
	t := &p.data.io
	s := &t.exp
	if s.busy {
		return
	}
	opt := config.ExportOptions{Connections: s.on[secConnections], Profiles: s.on[secProfiles], Defaults: s.on[secDefaults],
		KeyStates: s.on[secKeyStates], AppVersion: version.String()}
	switch pw1, pw2 := s.pw1.Text(), s.pw2.Text(); {
	case !opt.Connections && !opt.Profiles && !opt.Defaults && !opt.KeyStates:
		s.err = "Вибери, що експортувати."
		return
	case s.plain:
	case utf8.RuneCountInString(pw1) < minPassword:
		s.err = fmt.Sprintf("Пароль закороткий — потрібно щонайменше %d символів.", minPassword)
		return
	case pw1 != pw2:
		s.err = "Паролі не збігаються — введи той самий пароль двічі."
		return
	default:
		opt.Password = pw1
	}
	s.err = ""
	p.flushFields()
	name := config.ExportFileName(time.Now())
	if home, err := os.UserHomeDir(); err == nil {
		name = filepath.Join(home, "Documents", name) // the dialog opens there when it exists
	}
	save := t.pickSave
	if save == nil {
		save = func(title, name string, then func(string)) { c.PickSave(title, name, then) }
	}
	save("Куди зберегти файл налаштувань", name, func(path string) {
		if !strings.EqualFold(filepath.Ext(path), config.ExportExt) {
			path += config.ExportExt
		}
		s.busy = true
		p.cfg.barrier(c, func() {
			c.Background(func() {
				data, err := c.E.ExportSettings(opt)
				if err == nil {
					err = config.WriteExportFile(path, data)
				}
				c.Post(func() {
					s.busy = false
					if err != nil {
						s.err = core.ErrorText(err)
						return
					}
					s.saved = path
					s.pw1.SetText("")
					s.pw2.SetText("")
					c.Toast(gui.ToneSuccess, "Файл налаштувань збережено: "+filepath.Base(path))
				})
			})
		})
	})
}

// ---- import -----------------------------------------------------------------------------------

type importSheet struct {
	drawer  gui.Drawer
	loading bool // the chosen file is being read
	name    string
	file    *config.ExportFile
	b       *config.Bundle
	plan    config.ImportPlan
	cur     config.Config   // the settings when the file opened (which hints differ)
	here    config.HintPlan // hints that exist on this computer
	mic     int

	pw      gui.TextField
	openBtn gui.Clickable
	opening bool
	pwErr   string

	rows  [secCount]sectionRow
	hints [hintCount]sectionRow

	run, cancel gui.Clickable
	busy        bool
	err         string
	errA        gui.Anim

	done            bool
	res             config.ImportResult
	lines           []string
	restore, finish gui.Clickable
}

// start shows a file that was read (asking for its password when it has one).
func (s *importSheet) start(name string, f *config.ExportFile) {
	s.name, s.file, s.b = name, f, nil
	s.plan, s.cur, s.here, s.mic = config.ImportPlan{}, config.Config{}, config.HintPlan{}, micUnknown
	s.opening, s.pwErr = false, ""
	s.busy, s.err = false, ""
	s.done, s.res, s.lines = false, config.ImportResult{}, nil
	s.pw.SetText("")
	s.drawer.Open()
}

// setBundle shows what an opened file carries.
func (s *importSheet) setBundle(c *core.Ctx, b *config.Bundle, here config.HintPlan) {
	s.b, s.here = b, here
	s.plan = b.DefaultPlan()
	s.cur = c.E.Config().Get()
	s.pw.SetText("")
	s.mic = micUnknown
	if s.offers(hintMic) {
		s.checkMic(c)
	}
}

func (p *Page) chooseImportFile(c *core.Ctx) {
	open := p.data.io.pickOpen
	if open == nil {
		open = func(title string, then func(string)) { c.PickFile(title, []string{"ytcs"}, then) }
	}
	open("Файл налаштувань YT Cartell Studio (.ytcs)", func(path string) { p.loadImportFile(c, path) })
}

// loadImportFile reads a settings file in the background and opens the sheet.
func (p *Page) loadImportFile(c *core.Ctx, path string) {
	s := &p.data.io.imp
	s.loading = true
	c.Background(func() {
		f, err := config.ReadExportFile(path)
		var b *config.Bundle
		var here config.HintPlan
		if err == nil && !f.Encrypted() {
			if b, err = f.Open(""); err == nil {
				here = b.Hints.Here()
			}
		}
		c.Post(func() {
			s.loading = false
			if err != nil {
				c.Toast(gui.ToneError, core.ErrorText(err))
				return
			}
			s.start(filepath.Base(path), f)
			if b != nil {
				s.setBundle(c, b, here)
			}
		})
	})
}

// unlock checks the password in the background (PBKDF2 takes a moment).
func (s *importSheet) unlock(c *core.Ctx) {
	if s.opening || s.file == nil {
		return
	}
	pw := s.pw.Text()
	if pw == "" {
		s.pwErr = "Введи пароль, який ти задав під час експорту."
		return
	}
	s.opening, s.pwErr = true, ""
	f := s.file
	c.Background(func() {
		b, err := f.Open(pw)
		var here config.HintPlan
		if err == nil {
			here = b.Hints.Here()
		}
		c.Post(func() {
			s.opening = false
			if s.file != f {
				return // another file was chosen meanwhile
			}
			if err != nil {
				s.pwErr = core.ErrorText(err)
				return
			}
			s.setBundle(c, b, here)
		})
	})
}

// checkMic asks FFmpeg whether the file's microphone is connected here.
func (s *importSheet) checkMic(c *core.Ctx) {
	if c.Offline {
		s.mic = micUnchecked
		return
	}
	s.mic = micChecking
	b, e := s.b, c.E
	name := b.Hints.Microphone
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		devs, err := dictate.ListDevices(ctx, e.FF().FFmpeg)
		state := micMissing
		if err != nil {
			state = micUnchecked
		}
		for _, d := range devs {
			if d.Name == name {
				state = micFound
			}
		}
		c.Post(func() {
			if s.b == b {
				s.mic = state
			}
		})
	})
}

// offers reports whether the file has hint h and it differs from here.
func (s *importSheet) offers(h int) bool {
	if s.b == nil {
		return false
	}
	hs := s.b.Hints
	switch h {
	case hintDataDir:
		return hs.DataDir != "" && !samePath(hs.DataDir, s.cur.DataDir)
	case hintFFmpeg:
		return hs.FFmpeg != "" && !samePath(hs.FFmpeg, s.cur.FFmpegPath)
	case hintWhisper:
		return hs.WhisperBin != "" && !samePath(hs.WhisperBin, s.cur.STT.LocalBin)
	case hintMic:
		return hs.Microphone != "" && hs.Microphone != s.cur.Dictation.Device
	}
	return false
}

// available reports whether hint h can be taken on this computer.
func (s *importSheet) available(h int) bool {
	switch h {
	case hintDataDir:
		return s.here.DataDir
	case hintFFmpeg:
		return s.here.FFmpeg
	case hintWhisper:
		return s.here.Whisper
	case hintMic:
		return s.mic == micFound
	}
	return false
}

// hintOn is the plan's switch of hint h.
func (s *importSheet) hintOn(h int) *bool { return planHint(&s.plan, h) }

// section is the plan of section i.
func (s *importSheet) section(i int) *config.SectionPlan { return planSection(&s.plan, i) }

// has reports whether the file carries section i.
func (s *importSheet) has(i int) bool {
	b := s.b
	switch i {
	case secConnections:
		return b.Connections != nil
	case secProfiles:
		return b.HasProfiles
	case secDefaults:
		return b.Defaults != nil
	}
	return len(b.KeyStates) > 0
}

// finalPlan is the plan with only the hints that can be taken here.
func (s *importSheet) finalPlan() config.ImportPlan {
	pl := s.plan
	for h := 0; h < hintCount; h++ {
		if on := planHint(&pl, h); *on && (!s.offers(h) || !s.available(h)) {
			*on = false
		}
	}
	for i := 0; i < secCount; i++ {
		if !s.has(i) {
			*planSection(&pl, i) = config.SectionPlan{}
		}
	}
	return pl
}

func (p *Page) importSheet(gtx C, c *core.Ctx) {
	th := c.Th
	t := &p.data.io
	s := &t.imp
	if s.drawer.Dismissed() {
		s.pw.SetText("")
	}
	if s.cancel.Clicked(gtx) || s.finish.Clicked(gtx) {
		s.drawer.Close()
		s.pw.SetText("")
	}
	if s.restore.Clicked(gtx) && s.res.Backup.Time != (time.Time{}) && !t.restoring {
		p.askRestore(c, s.res.Backup)
	}
	if _, ok := s.pw.Changed(gtx); ok {
		s.pwErr = ""
	}
	if s.b == nil && (s.openBtn.Clicked(gtx) || s.pw.Submitted(gtx)) {
		s.unlock(c)
	}
	if s.b != nil && !s.done && s.run.Clicked(gtx) {
		p.applyImport(c)
	}
	var body, foot layout.Widget
	title, sub := "Імпорт налаштувань", "Ключі, підключення й профілі з іншого комп'ютера."
	switch {
	case s.done:
		title = "Налаштування перенесено"
		body = func(gtx C) D { return s.doneBody(gtx, c, t) }
		foot = func(gtx C) D {
			return sheetButtons(gtx, th, nil, nil, nil,
				func(gtx C, full bool) D {
					return gui.Button{Kind: gui.Secondary, Icon: gui.IconRotateCcw, Text: "Відновити попередні", Busy: t.restoring, Full: full}.Layout(gtx, th, &s.restore)
				},
				func(gtx C, full bool) D {
					return gui.Button{Kind: gui.Primary, Icon: gui.IconCheck, Text: "Готово", Full: full}.Layout(gtx, th, &s.finish)
				})
		}
	case s.b == nil:
		body = func(gtx C) D { return s.passwordBody(gtx, th) }
		foot = func(gtx C) D {
			return sheetButtons(gtx, th, nil, nil,
				func(gtx C, full bool) D {
					return gui.Button{Kind: gui.Tinted, Text: "Скасувати", Full: full}.Layout(gtx, th, &s.cancel)
				},
				func(gtx C, full bool) D {
					return gui.Button{Kind: gui.Primary, Icon: gui.IconLock, Text: "Відкрити", Busy: s.opening, Full: full}.Layout(gtx, th, &s.openBtn)
				})
		}
	default:
		body = func(gtx C) D { return s.contentsBody(gtx, c) }
		pl := s.finalPlan()
		foot = func(gtx C) D {
			return sheetButtons(gtx, th, &s.err, &s.errA,
				func(gtx C, full bool) D {
					return gui.Button{Kind: gui.Tinted, Text: "Скасувати", Full: full}.Layout(gtx, th, &s.cancel)
				},
				func(gtx C, full bool) D {
					return gui.Button{Kind: gui.Primary, Icon: gui.IconDownload, Text: "Імпортувати", Busy: s.busy, Disabled: !pl.Any(), Full: full}.Layout(gtx, th, &s.run)
				})
		}
	}
	s.drawer.Layout(gtx, th, gui.DrawerStyle{Title: title, Subtitle: sub, Width: 600, Body: body, Footer: foot})
}

func (s *importSheet) passwordBody(gtx C, th *gui.Theme) D {
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D { return fileTile(gtx, th, s.name, s.file.Header) },
		func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneNeutral, gui.IconLock, "Файл захищено паролем",
				"Введи пароль, який ти задав під час експорту. Поки пароль не підійде, нічого не зміниться.")
		},
		func(gtx C) D {
			return s.pw.Layout(gtx, th, gui.FieldStyle{Label: "Пароль файлу", Password: true, Leading: gui.IconKeyRound, Submit: true, Error: s.pwErr})
		},
	)
}

func (s *importSheet) contentsBody(gtx C, c *core.Ctx) D {
	th := c.Th
	b := s.b
	ws := []layout.Widget{func(gtx C) D { return fileTile(gtx, th, s.name, b.Header) }}
	if !b.Header.Encrypted {
		ws = append(ws, func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneWarning, gui.IconAlertTriangle, "Файл без пароля",
				"Після імпорту видали його звідусіль, де він лежав: у ньому відкриті ключі.")
		})
	}
	for _, w := range b.Warnings {
		w := w
		ws = append(ws, func(gtx C) D { return tonePanel(gtx, th, gui.ToneWarning, gui.IconInfo, "", w) })
	}
	any := false
	for i := 0; i < secCount; i++ {
		any = any || s.has(i)
	}
	if !any {
		ws = append(ws, func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneWarning, gui.IconInfo, "У файлі немає налаштувань", "Можна перенести лише шляхи нижче (якщо вони є).")
		})
	} else {
		ws = append(ws, gui.Text{Style: th.Label}.Widget(th, "Що імпортувати"))
	}
	for i := 0; i < secCount; i++ {
		if !s.has(i) {
			continue
		}
		i := i
		ws = append(ws, func(gtx C) D {
			sp := s.section(i)
			st := rowStyle{Icon: sectionIcons[i], Title: sectionTitles[i], Caption: s.caption(i), On: sp.On, Modes: i != secDefaults, Mode: sp.Mode}
			st.ModeHint = func(m config.ImportMode) string { return modeHint(i, m) }
			return s.rows[i].layout(gtx, th, st, func(v bool) { s.section(i).On = v; s.err = "" },
				func(m config.ImportMode) { s.section(i).Mode = m })
		})
	}
	var hints []int
	for h := 0; h < hintCount; h++ {
		if s.offers(h) {
			hints = append(hints, h)
		}
	}
	if len(hints) > 0 {
		ws = append(ws,
			layout.Spacer{Height: gui.Sp4}.Layout,
			gui.Text{Style: th.Label}.Widget(th, "Шляхи з того комп'ютера"),
			gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Беремо лише ті, що є й на цьому комп'ютері, і лише якщо ти їх увімкнеш. Інакше лишаються поточні."),
		)
		for _, h := range hints {
			h := h
			ws = append(ws, func(gtx C) D {
				st := s.hintStyle(h)
				return s.hints[h].layout(gtx, th, st, func(v bool) { *s.hintOn(h) = v }, nil)
			})
		}
	}
	ws = append(ws, func(gtx C) D {
		return note(gtx, th, gui.IconHistory, "Перед імпортом студія збереже копію поточних налаштувань — їх можна буде повернути кнопкою «Відновити попередні».", nil)
	})
	return gui.VStack(gtx, gui.Sp12, ws...)
}

// caption is the summary of section i of the file.
func (s *importSheet) caption(i int) string {
	b := s.b
	switch i {
	case secConnections:
		return connectionsSummary(b.Connections)
	case secProfiles:
		return profilesSummary(b.Profiles)
	case secDefaults:
		return defaultsSummary(b.Defaults)
	}
	n, ex := b.KeyStateCounts()
	return keyStatesSummary(n, ex)
}

func (s *importSheet) hintStyle(h int) rowStyle {
	hs := s.b.Hints
	avail := s.available(h)
	st := rowStyle{On: *s.hintOn(h) && avail, Disabled: !avail}
	here, missing := "Є на цьому комп'ютері.", "Немає на цьому комп'ютері — лишиться поточна."
	switch h {
	case hintDataDir:
		st.Icon, st.Title, st.Detail = gui.IconFolder, "Папка з проєктами", hs.DataDir
		here = "Є на цьому комп'ютері — студія працюватиме з нею (проєкти в поточній папці лишаться там)."
	case hintFFmpeg:
		st.Icon, st.Title, st.Detail = gui.IconFilm, "FFmpeg", hs.FFmpeg
		missing = "Немає на цьому комп'ютері — студія знайде FFmpeg сама або встановить."
	case hintWhisper:
		st.Icon, st.Title, st.Detail = gui.IconAudioLines, "whisper.cpp", hs.WhisperBin
		missing = "Немає на цьому комп'ютері — лишиться поточний."
	case hintMic:
		st.Icon, st.Title, st.Detail = gui.IconMic, "Мікрофон", hs.Microphone
		here = "Підключений до цього комп'ютера."
		switch s.mic {
		case micChecking:
			missing = "Шукаємо цей мікрофон…"
		case micMissing:
			missing = "Такого мікрофона тут немає — лишиться поточний."
		default:
			missing = "Не вдалося перевірити мікрофони — лишиться поточний."
		}
	}
	st.Caption = missing
	if avail {
		st.Caption = here
	}
	return st
}

func (s *importSheet) doneBody(gtx C, c *core.Ctx, t *settingsIO) D {
	th := c.Th
	ws := []layout.Widget{func(gtx C) D {
		return tonePanel(gtx, th, gui.ToneSuccess, gui.IconCheckCircle, "Готово",
			"Студія вже працює з новими налаштуваннями. Перевір підключення в «Підключення» кнопками «Перевірити».")
	}}
	for _, l := range s.lines {
		l := l
		ws = append(ws, func(gtx C) D {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return layout.Inset{Top: 2}.Layout(gtx, gui.IconCheck.Widget(18, th.Pal.SuccessInk)) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, gui.Text{Style: th.Body}.Widget(th, l)),
			)
		})
	}
	for _, w := range s.res.Warnings {
		w := w
		ws = append(ws, func(gtx C) D { return tonePanel(gtx, th, gui.ToneWarning, gui.IconAlertTriangle, "", w) })
	}
	if !s.res.Backup.Time.IsZero() {
		ws = append(ws, func(gtx C) D {
			return note(gtx, th, gui.IconHistory, "Попередні налаштування збережено ("+fullDate(s.res.Backup.Time)+"). Якщо щось не так — «Відновити попередні».", nil)
		})
	}
	if s.b != nil && !s.b.Header.Encrypted {
		ws = append(ws, func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneWarning, gui.IconAlertTriangle, "Видали файл без пароля", "У ньому відкриті ключі — він більше не потрібен.")
		})
	}
	return gui.VStack(gtx, gui.Sp12, ws...)
}

// applyImport runs the import after every pending settings save.
func (p *Page) applyImport(c *core.Ctx) {
	t := &p.data.io
	s := &t.imp
	if s.busy || s.b == nil {
		return
	}
	plan := s.finalPlan()
	if !plan.Any() {
		s.err = "Вибери, що імпортувати."
		return
	}
	s.busy, s.err = true, ""
	b, name := s.b, s.name
	p.flushFields()
	p.cfg.barrier(c, func() {
		c.Background(func() {
			res, err := c.E.ImportSettings(b, plan)
			c.Post(func() {
				s.busy = false
				if err != nil {
					s.err = core.ErrorText(err)
					return
				}
				s.done, s.res = true, res
				s.lines = importLines(b, plan, res, c.E.Config().Get())
				t.result = "Налаштування з файлу " + name + " перенесено. Перевір підключення кнопками «Перевірити»."
				t.backupsOK = false
				c.ReloadVoices()
				c.Toast(gui.ToneSuccess, "Налаштування імпортовано.")
			})
		})
	})
}

// importLines say what an import did, one line per part.
func importLines(b *config.Bundle, pl config.ImportPlan, res config.ImportResult, now config.Config) []string {
	var out []string
	if pl.Connections.On && b.Connections != nil {
		keys := core.Count(b.Connections.KeyCount(), "ключ", "ключі", "ключів")
		if pl.Connections.Merge() {
			out = append(out, "Підключення об'єднано: ключі з файлу додано без повторів, сервіси, яких тут не було, — налаштовано ("+keys+" у файлі).")
		} else {
			out = append(out, "Підключення й ключі — як у файлі ("+keys+").")
		}
	}
	if pl.Profiles.On && b.HasProfiles {
		if pl.Profiles.Merge() {
			line := "Профілі: додано " + itoa(res.ProfilesAdded)
			if res.ProfilesSame > 0 {
				line += ", однакових пропущено " + itoa(res.ProfilesSame)
			}
			out = append(out, line+" (усього "+itoa(res.ProfilesTotal)+").")
		} else {
			out = append(out, "Профілі — як у файлі ("+core.Count(res.ProfilesTotal, "профіль", "профілі", "профілів")+").")
		}
	}
	if pl.Defaults.On && b.Defaults != nil {
		out = append(out, "Типові налаштування — як у файлі.")
	}
	if pl.KeyStates.On && len(b.KeyStates) > 0 {
		out = append(out, "Стан ключів озвучки: перенесено "+core.Count(res.KeyStates, "запис", "записи", "записів")+".")
	}
	h := b.Hints
	if pl.Hints.DataDir {
		out = append(out, "Папка з проєктами: "+h.DataDir)
	}
	if pl.Hints.FFmpeg {
		out = append(out, "FFmpeg: "+h.FFmpeg)
	}
	if pl.Hints.Whisper {
		out = append(out, "whisper.cpp: "+h.WhisperBin)
	}
	if pl.Hints.Microphone {
		out = append(out, "Мікрофон: "+h.Microphone)
	}
	if tg := b.Connections; pl.Connections.On && tg != nil && tg.Telegram != nil && tg.Telegram.Remote && !now.Telegram.Remote {
		out = append(out, "Керування через Telegram-бота тут поки вимкнено: один бот не може приймати команди на двох комп'ютерах одночасно. Увімкни його в «Сповіщення», коли старий комп'ютер перестане ним користуватися.")
	}
	return out
}

// planHint is the switch of hint h in pl.
func planHint(pl *config.ImportPlan, h int) *bool {
	switch h {
	case hintDataDir:
		return &pl.Hints.DataDir
	case hintFFmpeg:
		return &pl.Hints.FFmpeg
	case hintWhisper:
		return &pl.Hints.Whisper
	}
	return &pl.Hints.Microphone
}

// planSection is section i of pl.
func planSection(pl *config.ImportPlan, i int) *config.SectionPlan {
	switch i {
	case secConnections:
		return &pl.Connections
	case secProfiles:
		return &pl.Profiles
	case secDefaults:
		return &pl.Defaults
	}
	return &pl.KeyStates
}

// ---- summaries ---------------------------------------------------------------------------------

var (
	sectionTitles = [secCount]string{"Підключення й ключі", "Профілі", "Типові налаштування", "Стан ключів озвучки"}
	sectionIcons  = [secCount]*gui.Icon{gui.IconPlug, gui.IconLayers, gui.IconSlidersHorizontal, gui.IconActivity}
)

// modeHint explains «Замінити» / «Об'єднати» for section i.
func modeHint(i int, m config.ImportMode) string {
	merge := m == config.ModeMerge
	switch i {
	case secConnections:
		if merge {
			return "Твої підключення лишаються. Ключі з файлу додаються без повторів, а сервіси, які тут ще не налаштовані, беруться з файлу."
		}
		return "Підключення й ключі стануть як у файлі; тих, яких у файлі немає, тут не буде."
	case secProfiles:
		if merge {
			return "Профілі з файлу додаються до твоїх: однакові пропускаємо, а з тією самою назвою додаємо як «… (імпорт)»."
		}
		return "Профілі стануть як у файлі."
	case secKeyStates:
		if merge {
			return "Стан береться лише для ключів, про які тут ще нічого не відомо."
		}
		return "Стан із файлу замінить тутешній для тих самих ключів."
	}
	return ""
}

// connectionsSummary: "Ключі озвучки: Cartesia 52, ElevenLabs 1 · Google: 3 ключі · …".
func connectionsSummary(cn *config.Connections) string {
	if cn == nil {
		return ""
	}
	var parts []string
	if c := cn.Text; c != nil && (c.APIKey != "" || c.Provider == "google") {
		host := "Google"
		if c.Provider != "google" {
			host = hostOf(c.BaseURL)
		}
		parts = append(parts, "Текст: "+host)
	}
	if c := cn.Glabs; c != nil && c.APIKey != "" {
		parts = append(parts, "G-Labs")
	}
	if c := cn.Google; c != nil && len(c.Keys) > 0 {
		parts = append(parts, "Google: "+core.Count(len(c.Keys), "ключ", "ключі", "ключів"))
	}
	if list := cn.TTSKeys(); len(list) > 0 {
		var ps []string
		for _, pk := range list {
			l := ttsShort(pk.ID)
			if pk.Keys > 0 {
				l += " " + itoa(pk.Keys)
			}
			ps = append(ps, l)
		}
		parts = append(parts, "Ключі озвучки: "+strings.Join(ps, ", "))
	}
	if c := cn.Custom; c != nil {
		var ps []string
		if pr, ok := providers.FindImagePreset(c.ImagePreset); ok && c.ImagePreset != "" {
			ps = append(ps, shortLabel(pr.Label))
		}
		if pr, ok := providers.FindVideoPreset(c.VideoPreset); ok && c.VideoPreset != "" && !contains(ps, shortLabel(pr.Label)) {
			ps = append(ps, shortLabel(pr.Label))
		}
		if len(ps) > 0 {
			parts = append(parts, "Своє API: "+strings.Join(ps, ", "))
		}
	}
	if c := cn.STT; c != nil && (c.APIKey != "" || c.AssemblyKey != "") {
		parts = append(parts, "Розпізнавання мови")
	}
	if d := cn.Dictation; d != nil && (d.ElevenKey != "" || d.OpenAIKey != "" || d.ChunksKey != "" || d.DeepgramKey != "" || d.AssemblyKey != "" || d.GoogleKey != "") {
		parts = append(parts, "Диктування")
	}
	if c := cn.Telegram; c != nil && c.BotToken != "" {
		parts = append(parts, "Telegram")
	}
	if len(parts) == 0 {
		return "Ключів ще немає — лише вибір сервісів і моделей."
	}
	return strings.Join(parts, " · ") + " — усього " + core.Count(cn.KeyCount(), "ключ", "ключі", "ключів") + "."
}

func profilesSummary(list []config.Profile) string {
	if len(list) == 0 {
		return "Профілів немає."
	}
	var names []string
	for i, p := range list {
		if i == 3 {
			names = append(names, "…")
			break
		}
		names = append(names, p.Name)
	}
	return core.Count(len(list), "профіль", "профілі", "профілів") + ": " + strings.Join(names, ", ")
}

func defaultsSummary(d *config.Defaults) string {
	if d == nil {
		return ""
	}
	var parts []string
	if d.Autopilot != nil {
		parts = append(parts, "автопілот")
	}
	if d.Concurrency != nil {
		parts = append(parts, core.Count(*d.Concurrency, "сцена", "сцени", "сцен")+" одночасно")
	}
	if u := d.UI; u != nil {
		theme := map[string]string{"light": "світла", "dark": "темна", "system": "як у системі"}[u.Theme]
		if theme != "" {
			parts = append(parts, "тема «"+theme+"»")
		}
		if u.ReducedMotion {
			parts = append(parts, "менше анімацій")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	r := []rune(strings.Join(parts, ", "))
	r[0] = unicode.ToUpper(r[0])
	return string(r) + "."
}

func keyStatesSummary(n, exhausted int) string {
	if n == 0 {
		return "Записів немає."
	}
	s := core.Count(n, "запис", "записи", "записів")
	if exhausted > 0 {
		s += ", вичерпано " + itoa(exhausted)
	}
	return s
}

func ttsShort(id string) string {
	if pi, ok := tts.ProviderByID(id); ok {
		return shortLabel(pi.Label)
	}
	return id
}

// shortLabel drops a parenthesised tail: "OpenAI (або сумісний)" → "OpenAI".
func shortLabel(s string) string {
	if i := strings.Index(s, " ("); i > 0 {
		return s[:i]
	}
	return s
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(strings.TrimSpace(a)), filepath.Clean(strings.TrimSpace(b)))
}

var monthsGen = [...]string{"січня", "лютого", "березня", "квітня", "травня", "червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}

// fullDate is "2 жовтня 2026, 15:30" (local time).
func fullDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.Local()
	return fmt.Sprintf("%d %s %d, %02d:%02d", t.Day(), monthsGen[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

// ---- password strength -----------------------------------------------------------------------

var commonPasswords = map[string]bool{
	"12345678": true, "123456789": true, "1234567890": true, "password": true, "password1": true, "qwerty123": true,
	"qwertyui": true, "11111111": true, "00000000": true, "iloveyou": true, "admin123": true, "abcd1234": true,
	"йцукенгш": true, "пароль123": true, "87654321": true, "qwerty12": true,
}

// strength rates a password: 0 too short (refused), 1 weak, 2 fair, 3 strong.
func strength(pw string) int {
	n := utf8.RuneCountInString(pw)
	if n < minPassword {
		return 0
	}
	var lower, upper, digit, other bool
	distinct := map[rune]bool{}
	for _, r := range pw {
		distinct[r] = true
		switch {
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		default:
			other = true
		}
	}
	classes := 0
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			classes++
		}
	}
	switch {
	case len(distinct) <= 3 || commonPasswords[strings.ToLower(pw)]:
		return 1
	case n >= 16 || (n >= 12 && classes >= 3):
		return 3
	case n >= 12 || (n >= 10 && classes >= 3):
		return 2
	}
	return 1
}

// strengthMeter is the bar and the hint under the password field.
func strengthMeter(gtx C, th *gui.Theme, pw string) D {
	if pw == "" {
		return D{}
	}
	score := strength(pw)
	label, hint, tone, v := "", "", gui.ToneError, float32(0.1)
	switch score {
	case 0:
		label, hint = "Закороткий", fmt.Sprintf("Потрібно щонайменше %d символів.", minPassword)
	case 1:
		label, hint, v = "Слабкий", "Його легко підібрати. Візьми довший: 3–4 слова через пробіл — легко запам'ятати й важко вгадати.", 0.34
	case 2:
		label, hint, tone, v = "Нормальний", "Ще кілька символів — і буде надійний.", gui.ToneWarning, 0.67
	default:
		label, hint, tone, v = "Надійний", "Добрий пароль.", gui.ToneSuccess, 1
	}
	_, _, ink := th.Pal.ToneColors(tone)
	return gui.VStack(gtx, 6,
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D { return gui.ProgressBar{Value: v, Height: 6, Tone: tone}.Layout(gtx, th) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontMedium), Color: ink, MaxLines: 1}.Widget(th, label)),
			)
		},
		gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, hint),
	)
}

// ---- pieces ------------------------------------------------------------------------------------

// sectionRow is one switchable part of a transfer: a flat panel with an
// icon, title, caption and switch; the import adds «Замінити» / «Об'єднати».
type sectionRow struct {
	toggle gui.Toggle
	click  gui.Clickable
	mode   gui.Segmented
}

type rowStyle struct {
	Icon           *gui.Icon
	Title, Caption string
	Detail         string // a path or a name under the caption (shortened in the middle)
	On, Disabled   bool
	Modes          bool // offer «Замінити» / «Об'єднати» while on
	Mode           config.ImportMode
	ModeHint       func(config.ImportMode) string // explains the chosen mode
}

func (r *sectionRow) layout(gtx C, th *gui.Theme, st rowStyle, setOn func(bool), setMode func(config.ImportMode)) D {
	r.toggle.Disabled = st.Disabled
	if v, ok := r.toggle.Changed(gtx); ok && !st.Disabled {
		st.On = v
		setOn(v)
	}
	if r.click.Clicked(gtx) && !st.Disabled {
		st.On = !st.On
		setOn(st.On)
	}
	if v, ok := r.mode.Changed(gtx); ok && setMode != nil {
		st.Mode = config.ImportMode(v)
		setMode(st.Mode)
	}
	mode := st.Mode
	if mode == "" {
		mode = config.ModeReplace
	}
	return inset(gtx, th, func(gtx C) D {
		// Phones: no icon column, the words get the room.
		withIcon := st.Icon != nil && gtx.Constraints.Max.X >= gtx.Dp(340)
		head := func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return r.click.Clickable.Layout(gtx, func(gtx C) D {
						d := rowTexts(gtx, th, st, withIcon)
						defer clip.Rect(image.Rectangle{Max: d.Size}).Push(gtx.Ops).Pop()
						if !st.Disabled {
							pointer.CursorPointer.Add(gtx.Ops)
						}
						return d
					})
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(func(gtx C) D { return r.toggle.Layout(gtx, th, st.On, "") }),
			)
		}
		if !st.Modes || !st.On {
			return head(gtx)
		}
		modes := []layout.Widget{func(gtx C) D {
			r.mode.Full = true
			return r.mode.Layout(gtx, th, string(mode), []gui.SegOption{
				{Value: string(config.ModeReplace), Label: "Замінити"},
				{Value: string(config.ModeMerge), Label: "Об'єднати"},
			})
		}}
		if st.ModeHint != nil {
			if h := st.ModeHint(mode); h != "" {
				modes = append(modes, gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, h))
			}
		}
		return gui.VStack(gtx, gui.Sp12, head, func(gtx C) D {
			in := layout.Inset{}
			if withIcon {
				in.Left = 36 + gui.Sp12 // under the texts
			}
			return in.Layout(gtx, func(gtx C) D { return gui.VStack(gtx, gui.Sp8, modes...) })
		})
	})
}

// rowTexts is the icon (withIcon) and the text column of a sectionRow.
func rowTexts(gtx C, th *gui.Theme, st rowStyle, withIcon bool) D {
	titleC, iconC := th.Pal.Text, th.Pal.Text
	if st.Disabled {
		titleC, iconC = th.Pal.Text2, th.Pal.Text3
	}
	return layout.Flex{Alignment: layout.Start}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			if !withIcon {
				return D{}
			}
			s := gtx.Dp(36)
			gui.FillRRect(gtx, image.Rect(0, 0, s, s), gui.RadiusSmall+2, th.Pal.Card)
			is := gtx.Dp(18)
			o := op.Offset(image.Pt((s-is)/2, (s-is)/2)).Push(gtx.Ops)
			st.Icon.Layout(gtx, 18, iconC)
			o.Pop()
			return D{Size: image.Pt(s+gtx.Dp(gui.Sp12), s)}
		}),
		layout.Flexed(1, func(gtx C) D {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			ch := []layout.FlexChild{layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontSemiBold), Color: titleC, MaxLines: 2}.Widget(th, st.Title))}
			if st.Detail != "" {
				ch = append(ch, layout.Rigid(func(gtx C) D {
					txt := middleEllipsis(gtx, th, th.Small, st.Detail, gtx.Constraints.Max.X)
					return gui.Text{Style: th.Small, Color: th.Pal.Text, MaxLines: 1}.Layout(gtx, th, txt)
				}))
			}
			if st.Caption != "" {
				ch = append(ch, layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, st.Caption)))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		}),
	)
}

// fileTile shows a settings file: its name, the studio version, system and date.
func fileTile(gtx C, th *gui.Theme, name string, h config.ExportHeader) D {
	var facts []string
	if h.AppVersion != "" {
		facts = append(facts, "версія "+h.AppVersion)
	}
	if l := h.OSLabel(); l != "" {
		facts = append(facts, l)
	}
	if !h.CreatedAt.IsZero() {
		facts = append(facts, fullDate(h.CreatedAt))
	}
	st := rowStyle{Icon: gui.IconFileText, Title: name, Caption: "Зроблено: " + strings.Join(facts, " · ")}
	if h.Encrypted {
		st.Icon = gui.IconLock
	}
	return inset(gtx, th, func(gtx C) D { return rowTexts(gtx, th, st, gtx.Constraints.Max.X >= gtx.Dp(340)) })
}

// tonePanel is a tinted box with an icon, an optional title and a text.
func tonePanel(gtx C, th *gui.Theme, tone gui.Tone, icon *gui.Icon, title, text string) D {
	p := &th.Pal
	fill, iconC, titleC, textC := p.Surface, p.Text2, p.Text, p.Text2
	switch tone {
	case gui.ToneError:
		fill, iconC, titleC, textC = p.ErrorSoft, p.ErrorInk, p.ErrorInk, p.Text
	case gui.ToneSuccess:
		fill, iconC, titleC, textC = p.SuccessSoft, p.SuccessInk, p.SuccessInk, p.Text
	case gui.ToneWarning:
		fill, iconC, titleC, textC = p.WarningSoft, p.WarningInk, p.WarningInk, p.Text
	}
	pad := gtx.Dp(gui.Sp12 + 2)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	d, call := record(cg, func(gtx C) D {
		var ch []layout.FlexChild
		if title != "" {
			ch = append(ch, layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: titleC}.Widget(th, title)),
				layout.Rigid(layout.Spacer{Height: 2}.Layout))
		}
		ch = append(ch, layout.Rigid(gui.Text{Style: th.Small.Sized(14).Lined(1.45), Color: textC}.Widget(th, text)))
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
	})
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, fill)
	o := op.Offset(image.Pt(pad, pad+1)).Push(gtx.Ops)
	icon.Layout(gtx, 18, iconC)
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

// footButton is one button of a sheet footer (full: stretch over the width).
type footButton func(gtx C, full bool) D

// sheetButtons is a sheet footer: the error of the last action (if any)
// above the buttons, right-aligned with the primary last. On phones cancel
// is left out (the sheet's × closes it) and the buttons stack at full width,
// the primary on top, so no label is cut.
func sheetButtons(gtx C, th *gui.Theme, msg *string, a *gui.Anim, cancel footButton, buttons ...footButton) D {
	narrow := gtx.Constraints.Max.X < gtx.Dp(440)
	row := func(gtx C) D {
		if narrow {
			ws := make([]layout.Widget, 0, len(buttons))
			for i := len(buttons) - 1; i >= 0; i-- {
				b := buttons[i]
				ws = append(ws, func(gtx C) D { return b(gtx, true) })
			}
			return gui.VStack(gtx, gui.Sp8, ws...)
		}
		ch := []layout.FlexChild{layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(gtx.Constraints.Min.X, 0)} })}
		all := buttons
		if cancel != nil {
			all = append([]footButton{cancel}, buttons...)
		}
		for i, b := range all {
			b := b
			if i > 0 {
				ch = append(ch, layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout))
			}
			ch = append(ch, layout.Rigid(func(gtx C) D { return b(gtx, false) }))
		}
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
	}
	if msg == nil || *msg == "" {
		if a != nil {
			a.Set(0)
		}
		return row(gtx)
	}
	return gui.VStack(gtx, gui.Sp12, func(gtx C) D {
		a.Duration = gui.DurDialog
		v := a.Animate(gtx, 1)
		return gui.FadeUp(gtx, v, 6, func(gtx C) D {
			return tonePanel(gtx, th, gui.ToneError, gui.IconCircleAlert, "Не вийшло", *msg)
		})
	}, row)
}
