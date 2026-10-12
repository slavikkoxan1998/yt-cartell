// Package core is the contract between the app shell (package appui) and the
// screen packages of the new "warm app" UI (GUI.md):
//
//	appui     window, event loop, left rail, routes + page transitions, queue
//	          drawer/page, tour, confirm dialog, toasts, headless renders
//	home      Головна (story box, profiles, autopilot, quick actions, projects
//	          grid, live queue, tip) and the Проєкти page
//	project   the project workspace (header, step tabs, context column) and
//	          steps 1 Сценарій + 2 План
//	produce   steps 3 Розкадровка + 4 Відео й озвучка
//	montage   step 5 Монтаж (timeline, sound, transitions, effects, filters,
//	          subtitles, cover studio, final render)
//	settings  Налаштування (connections, profiles, autopilot, Telegram,
//	          appearance, data, STT)
//	dialogs   global sheets: new project, translate, own voice-over, profile
//	          editor, profiles manager
//
// Every screen receives a *Ctx: the engine, the theme, the image cache, audio,
// navigation and the overlays the shell hosts. Screens never import each
// other except project → produce/montage (step views) and anything → core.
//
// Threading: Gio runs one UI goroutine. Everything here runs on it unless a
// doc comment says "any goroutine". Engine calls that may block (network,
// ffmpeg, OS dialogs) go through Background/Do; their results come back
// through Post. Engine notifications only mark snapshots dirty; the shell
// calls Refresh at the start of every frame so a screen reads consistent data
// for the whole frame.
package core

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"

	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
	"ytcartell/internal/sysx"
	"ytcartell/internal/tts"
)

// C and D shorten layout signatures.
type (
	C = layout.Context
	D = layout.Dimensions
)

// Page is a top-level destination of the left rail.
type Page uint8

const (
	PageHome     Page = iota // Головна
	PageProjects             // Проєкти (all projects)
	PageQueue                // Черга (full page; the drawer is separate)
	PageSettings             // Налаштування
	PageProject              // one project's workspace
)

// Route is where the window is.
type Route struct {
	Page      Page
	ProjectID string // PageProject
	// Step 1–5 on PageProject (0 = the step the project is at).
	Step int
	// Section scrolls Settings to a card: "connections" | "profiles" |
	// "autopilot" | "telegram" | "appearance" | "data" | "stt" ("" = top).
	Section string
	// Scene asks the project steps to scroll to (and highlight) a scene.
	Scene string
}

// Host is implemented by the shell (package appui).
type Host interface {
	// Navigate changes the route (animated). UI goroutine.
	Navigate(r Route)
	// Current is the route being shown.
	Current() Route
	// Back returns to the previous route (Home when there is none).
	Back()
	// Post runs fn on the UI goroutine before the next frame. Any goroutine.
	Post(fn func())
	// Background runs fn on a new goroutine; headless renders wait for it.
	// Any goroutine.
	Background(fn func())
	// Invalidate requests a new frame. Any goroutine.
	Invalidate()
	// OpenSheet opens a global sheet (package dialogs) above everything.
	OpenSheet(s Sheet)
	// Confirm opens the shared confirmation dialog.
	Confirm(c Confirm)
	// OpenQueue slides the queue drawer in (from the rail, the Home queue card…).
	OpenQueue()
	// StartTour replays the first-run tour (Settings → Вигляд → Показати підказки знову).
	StartTour()
}

// SheetKind names a global sheet hosted by the shell and drawn by package dialogs.
type SheetKind uint8

const (
	// SheetNewProject: "Новий проєкт" — story box, profile, language, duration,
	// Автопілот / Покроково. Text pre-fills the story.
	SheetNewProject SheetKind = iota + 1
	// SheetTranslate: "Перекласти проєкт" (ProjectID; "" = pick a project first).
	SheetTranslate
	// SheetOwnVoice: "Своя озвучка" — pick an audio file → transcribe → scenes
	// timed to the speech. ProjectID "" creates a new project.
	SheetOwnVoice
	// SheetProfile: create (ProfileID "") or edit a settings profile.
	// FromProject pre-fills a new profile from that project's settings.
	SheetProfile
	// SheetProfiles: the profiles manager (list + create/edit/duplicate/delete).
	SheetProfiles
	// SheetRemake: «Перезняти за прикладом» — a YouTube link + what to change
	// → a new project after it (Text pre-fills the wish).
	SheetRemake
)

// Sheet asks the shell to open a global sheet.
type Sheet struct {
	Kind        SheetKind
	ProjectID   string
	ProfileID   string
	FromProject string
	Text        string
	// Done (optional) runs on the UI goroutine with the id of what the sheet
	// created or saved (project id, profile id).
	Done func(id string)
}

// Confirm describes the shared confirmation dialog.
type Confirm struct {
	Title  string
	Text   string
	Icon   *gui.Icon
	OK     string // "" = "Так"
	Cancel string // "" = "Скасувати"
	Danger bool   // destructive: the OK button uses the error fill
	OnOK   func()
}

// Ctx is shared by every screen. Create it with New; the shell calls Refresh
// at the start of each frame and EndFrame at its end.
type Ctx struct {
	Host
	E     *studio.Engine
	Th    *gui.Theme
	Img   *gui.ImageCache
	Audio gui.AudioBackend
	// Offline: headless screenshots and tests. Never call network services,
	// never open OS dialogs or players.
	Offline bool
	// LogPath is the studio.log file ("" when not logging to a file).
	LogPath string

	unsub func()

	mu       sync.Mutex // guards dirty*, voices
	dirtyAll bool
	dirty    map[string]bool

	frame  int64
	list   []studio.Summary
	listOK bool
	views  map[string]*ProjectView
	ui     map[string]*ProjectUI
	videos map[*Video]int64 // open players → frame last laid out
	voices voiceState
}

// New wires a context to the engine (subscribes to its notifications).
func New(h Host, e *studio.Engine, th *gui.Theme, img *gui.ImageCache, offline bool) *Ctx {
	c := &Ctx{
		Host:    h,
		E:       e,
		Th:      th,
		Img:     img,
		Offline: offline,
		dirty:   map[string]bool{},
		views:   map[string]*ProjectView{},
		ui:      map[string]*ProjectUI{},
		videos:  map[*Video]int64{},
	}
	c.Audio = audioBackend{c}
	c.unsub = e.Subscribe(c.markDirty)
	return c
}

// Close unsubscribes from the engine and stops every player.
func (c *Ctx) Close() {
	if c.unsub != nil {
		c.unsub()
	}
	c.StopMedia()
}

// markDirty is the engine subscription (any goroutine).
func (c *Ctx) markDirty(id string) {
	c.mu.Lock()
	if id == "" {
		c.dirtyAll = true
	} else {
		c.dirty[id] = true
	}
	c.mu.Unlock()
	c.Invalidate()
}

// Refresh applies the engine notifications received since the last frame.
// The shell calls it at the start of every frame.
func (c *Ctx) Refresh() {
	c.frame++
	c.mu.Lock()
	all, ids := c.dirtyAll, c.dirty
	c.dirtyAll, c.dirty = false, map[string]bool{}
	c.mu.Unlock()
	if all || len(ids) > 0 {
		// Any project change can move it in the list (step, thumbnail, job).
		c.listOK = false
	}
	for id := range ids {
		if v := c.views[id]; v != nil {
			v.stale = true
		}
	}
	if all {
		for _, v := range c.views {
			v.stale = true
		}
	}
}

// EndFrame closes the players that were not laid out this frame (scrolled
// away, route changed). The shell calls it after the frame is drawn.
func (c *Ctx) EndFrame() {
	for v, seen := range c.videos {
		if seen < c.frame {
			v.close()
			delete(c.videos, v)
		}
	}
}

// Frame is the number of the current frame (starts at 1).
func (c *Ctx) Frame() int64 { return c.frame }

// List returns the projects (newest first), cached until the engine reports a change.
func (c *Ctx) List() []studio.Summary {
	if !c.listOK {
		c.list, c.listOK = c.E.List(), true
	}
	return c.list
}

// Project returns this frame's snapshot of a project (nil when it does not exist).
func (c *Ctx) Project(id string) *ProjectView {
	if id == "" {
		return nil
	}
	v := c.views[id]
	if v == nil {
		v = &ProjectView{ID: id, stale: true}
		c.views[id] = v
	}
	if v.stale {
		p, ok := c.E.Get(id)
		v.P, v.OK, v.stale = p, ok, false
		v.Ver++
		v.Dir = c.E.Dir(id)
	}
	if !v.OK {
		return nil
	}
	return v
}

// UI returns the per-project UI state shared by the workspace and its steps.
func (c *Ctx) UI(id string) *ProjectUI {
	u := c.ui[id]
	if u == nil {
		u = &ProjectUI{}
		c.ui[id] = u
	}
	return u
}

// ---- actions -------------------------------------------------------------------------

// Toast shows a message in the bottom-right corner (any goroutine).
func (c *Ctx) Toast(tone gui.Tone, msg string) { c.Th.Toast(tone, msg) }

// Error toasts err in plain Ukrainian (nil does nothing). Any goroutine.
func (c *Ctx) Error(err error) {
	if err == nil {
		return
	}
	c.Th.Toast(gui.ToneError, ErrorText(err))
}

// ErrorText is the user-facing text of an engine error.
func ErrorText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, studio.ErrBusy):
		return "Зачекайте: на проєкті вже виконується етап. Його можна зупинити кнопкою «Зупинити»."
	case errors.Is(err, studio.ErrNotFound):
		return "Проєкт не знайдено — можливо, його видалили."
	case errors.Is(err, studio.ErrClosing):
		return "Програма закривається."
	case errors.Is(err, context.Canceled):
		return "Скасовано."
	}
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return "Щось пішло не так."
	}
	r := []rune(s)
	if len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

// errOffline refuses generation in headless renders and tests.
var errOffline = errors.New("Демо-режим: генерація вимкнена")

// Run starts a stage on a project, toasting a refusal. Reports whether it
// started. Offline contexts never start anything.
func (c *Ctx) Run(id string, stage studio.Stage, force bool, prompt string) bool {
	if c.Offline {
		c.Error(errOffline)
		return false
	}
	if err := c.E.Run(id, stage, force, prompt); err != nil {
		c.Error(err)
		return false
	}
	return true
}

// Regen regenerates one asset (a reference, frame, clip or voice), toasting a
// refusal. Reports whether it started. Offline contexts never start anything.
func (c *Ctx) Regen(id string, kind studio.RegenKind, itemID, prompt string) bool {
	if c.Offline {
		c.Error(errOffline)
		return false
	}
	if err := c.E.Regen(id, kind, itemID, prompt); err != nil {
		c.Error(err)
		return false
	}
	return true
}

// Edit applies fn to the project (in memory; saved shortly after), toasting
// a refusal. Reports success.
func (c *Ctx) Edit(id string, fn func(p *studio.Project)) bool {
	if err := c.E.Edit(id, fn); err != nil {
		c.Error(err)
		return false
	}
	return true
}

// Do runs fn in the background and toasts its error; ok (optional) runs on
// the UI goroutine after success.
func (c *Ctx) Do(fn func() error, ok func()) {
	c.Background(func() {
		if err := fn(); err != nil {
			c.Error(err)
			return
		}
		if ok != nil {
			c.Post(ok)
		}
	})
}

// ---- OS integration ------------------------------------------------------------------

// PickFile asks for a file (exts like ".mp3"; nil = any) off the UI
// goroutine; then runs on the UI goroutine with the chosen path (not when cancelled).
func (c *Ctx) PickFile(title string, exts []string, then func(path string)) {
	if c.Offline {
		return
	}
	c.Background(func() {
		p, err := sysx.PickFile(title, exts)
		c.afterPick(p, err, then)
	})
}

// PickFolder asks for a folder (start preselected).
func (c *Ctx) PickFolder(title, start string, then func(path string)) {
	if c.Offline {
		return
	}
	c.Background(func() {
		p, err := sysx.PickFolder(title, start)
		c.afterPick(p, err, then)
	})
}

// PickSave asks where to save a file (defaultName pre-filled).
func (c *Ctx) PickSave(title, defaultName string, then func(path string)) {
	if c.Offline {
		return
	}
	c.Background(func() {
		p, err := sysx.PickSaveFile(title, defaultName)
		c.afterPick(p, err, then)
	})
}

func (c *Ctx) afterPick(p string, err error, then func(string)) {
	if err != nil {
		c.Error(fmt.Errorf("не вдалося відкрити вікно вибору: %w", err))
		return
	}
	if p != "" && then != nil {
		c.Post(func() { then(p) })
	}
}

// Reveal shows a file selected in Explorer (its folder when missing).
func (c *Ctx) Reveal(path string) {
	if c.Offline || path == "" {
		return
	}
	c.Background(func() { c.Error(sysx.RevealPath(path)) })
}

// OpenPath opens a file, folder or http(s) URL with the default app.
func (c *Ctx) OpenPath(path string) {
	if c.Offline || path == "" {
		return
	}
	c.Background(func() { c.Error(sysx.OpenPath(path)) })
}

// ---- voices ----------------------------------------------------------------------------

type voiceState struct {
	list    tts.VoiceList
	err     error
	loading bool
	loaded  bool
	again   bool // ReloadVoices while loading: load once more afterwards
	// provider is the TTS provider the list (or the error) belongs to.
	provider string
}

// ttsProvider is the configured TTS provider ("" without an engine).
func (c *Ctx) ttsProvider() string {
	if c.E == nil {
		return ""
	}
	return c.E.Config().Get().TTS.Provider
}

// Voices returns the narrator voices of the configured TTS provider, loading
// them in the background on first use (and after ReloadVoices). Screens show
// a skeleton while loading is true. After the provider changed, the previous
// provider's list or error never shows: the list is empty and loading until
// the new provider's voices arrive.
func (c *Ctx) Voices() (list tts.VoiceList, loading bool, err error) {
	prov := c.ttsProvider()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.voices.loaded && !c.voices.loading && c.voices.provider != prov {
		c.voices = voiceState{}
	}
	if !c.voices.loaded && !c.voices.loading && !c.Offline {
		c.voices.loading = true
		c.Background(func() { c.loadVoices(false) })
	}
	if c.voices.provider != prov {
		return tts.VoiceList{Provider: prov, CharsLeft: -1}, c.voices.loading, nil
	}
	return c.voices.list, c.voices.loading, c.voices.err
}

// ReloadVoices refetches the voice list (after the TTS key or provider
// changed); asked while a list is loading, it loads once more afterwards.
func (c *Ctx) ReloadVoices() {
	if c.Offline {
		return
	}
	c.mu.Lock()
	if c.voices.loading {
		c.voices.again = true
		c.mu.Unlock()
		return
	}
	c.voices.loading = true
	c.mu.Unlock()
	c.Background(func() { c.loadVoices(true) })
}

func (c *Ctx) loadVoices(force bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prov := c.ttsProvider()
	list, err := c.E.Voices(ctx, force)
	if err != nil {
		log.Printf("voices: %v", err)
	}
	if list.Provider != "" {
		prov = list.Provider
	}
	c.mu.Lock()
	again := c.voices.again
	c.voices = voiceState{list: list, err: err, loaded: true, provider: prov, loading: again}
	c.mu.Unlock()
	if again {
		c.Background(func() { c.loadVoices(true) })
	}
	c.Invalidate()
}

// SetVoicesForTest pins the voice list (headless screenshots); a list
// without a provider belongs to the configured one.
func (c *Ctx) SetVoicesForTest(list tts.VoiceList) {
	prov := list.Provider
	if prov == "" {
		prov = c.ttsProvider()
	}
	c.mu.Lock()
	c.voices = voiceState{list: list, loaded: true, provider: prov}
	c.mu.Unlock()
}

// WindowSize is the window size in px of the current frame.
func (c *Ctx) WindowSize() image.Point { return c.Th.WindowSize() }
