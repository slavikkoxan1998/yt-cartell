package settings

// The page's view of the settings: a mirror of config.Store that applies the
// user's changes at once (optimistically) while one background goroutine
// writes them through config.Store.Update, in order. Update persists the
// JSON file and notifies the engine (which may re-locate FFmpeg or switch the
// data folder), so it never runs on the UI goroutine.

import (
	"context"
	"fmt"
	"image"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/op/clip"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
)

type saveJob struct {
	fn    func(*config.Config) // nil = a barrier (runs after every earlier save)
	after func(err error)      // UI goroutine
}

type mirror struct {
	cfg     config.Config
	pending int // saves not finished yet
	loaded  bool
	// view is the page's settings of this frame (Page.now): a change updates
	// it at once, so every control drawn after the change in the same frame
	// shows the new value — never the old one for a frame.
	view *config.Config

	mu    sync.Mutex
	queue []saveJob
	run   bool
}

// get returns the settings of this frame: the store's, unless the page's own
// changes are still being written (then the optimistic copy).
func (m *mirror) get(c *core.Ctx) config.Config {
	if m.pending == 0 || !m.loaded {
		m.cfg, m.loaded = c.E.Config().Get(), true
	}
	return m.cfg
}

// set applies fn to the mirror now and queues the same change for the store.
// done (optional) runs on the UI goroutine once it is saved.
func (m *mirror) set(c *core.Ctx, fn func(*config.Config), done func()) {
	if !m.loaded {
		m.get(c)
	}
	fn(&m.cfg)
	m.cfg.Normalize()
	if m.view != nil {
		*m.view = m.cfg
	}
	m.pending++
	m.push(c, saveJob{fn: fn, after: func(err error) {
		m.pending--
		if err != nil {
			c.Toast(gui.ToneError, "Не вдалося записати налаштування на диск: "+core.ErrorText(err))
			return
		}
		if done != nil {
			done()
		}
	}})
}

// barrier runs then (UI goroutine) after every save queued so far — checks
// start there, so they test what the user just typed.
func (m *mirror) barrier(c *core.Ctx, then func()) {
	m.push(c, saveJob{after: func(error) { then() }})
}

func (m *mirror) push(c *core.Ctx, j saveJob) {
	m.mu.Lock()
	m.queue = append(m.queue, j)
	start := !m.run
	m.run = true
	m.mu.Unlock()
	if !start {
		return
	}
	c.Background(func() {
		for {
			m.mu.Lock()
			if len(m.queue) == 0 {
				m.run = false
				m.mu.Unlock()
				return
			}
			j := m.queue[0]
			m.queue = m.queue[1:]
			m.mu.Unlock()
			var err error
			if j.fn != nil {
				err = safeUpdate(c, j.fn)
			}
			if j.after != nil {
				after := j.after
				c.Post(func() { after(err) })
			}
		}
	})
}

// safeUpdate writes one change; a panic in it (the change, a settings
// subscriber) becomes an error. Before, it killed the writer goroutine with
// run still set: every later change stayed only on screen (pending never
// reached 0, so the page kept showing its optimistic copy) and was never
// saved — the selectors showed values the settings file did not have.
func safeUpdate(c *core.Ctx, fn func(*config.Config)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("settings: saving a change panicked: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("внутрішня помилка під час збереження: %v", r)
		}
	}()
	return c.E.Config().Update(fn)
}

// ---- bound text fields -------------------------------------------------------------

// field is a text input bound to one settings value: it shows the model
// while the user is not typing and commits on Enter or when focus leaves it
// (never per keystroke).
type field struct {
	gui.TextField
	was    bool // focused last frame
	edited bool // typed since the last commit
	commit func(string)
	// enterOnly commits on Enter only, never on blur (inputs that add
	// something, where half-typed text must not be taken).
	enterOnly bool
	// over tracks the pointer over the field (its label, box, eye button),
	// so a press there does not end the typing (see Page.Layout).
	over    int // tag
	hovered bool
}

// Layout draws the field (the kit's TextField) and watches the pointer over it.
func (f *field) Layout(gtx C, th *gui.Theme, st gui.FieldStyle) D {
	for {
		e, ok := gtx.Event(pointer.Filter{Target: &f.over, Kinds: pointer.Enter | pointer.Leave | pointer.Cancel})
		if !ok {
			break
		}
		if pe, ok := e.(pointer.Event); ok {
			f.hovered = pe.Kind == pointer.Enter
		}
	}
	d := f.TextField.Layout(gtx, th, st)
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect(image.Rectangle{Max: d.Size}).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &f.over)
	return d
}

// bind drains the field's events, commits a finished edit and syncs value.
func (f *field) bind(gtx C, value string, commit func(string)) {
	f.commit = commit
	if _, ok := f.Changed(gtx); ok {
		f.edited = true
	}
	sub := f.Submitted(gtx)
	foc := f.Focused(gtx)
	if f.edited && (sub || (f.was && !foc && !f.enterOnly)) {
		f.flush()
	}
	f.was = foc
	if !f.edited {
		f.Sync(gtx, value)
	}
}

// flush commits a pending edit right away (before a check reads the settings).
func (f *field) flush() {
	if !f.edited || f.commit == nil {
		return
	}
	f.edited = false
	f.commit(f.Editor.Text())
}

// ---- checks ------------------------------------------------------------------------

// runCheck commits every pending edit, waits for the saves, then runs fn in
// the background with a timeout and shows its result on k. Network checks
// never run offline (headless renders and tests).
func (p *Page) runCheck(c *core.Ctx, k *check, fn func(ctx context.Context) (bool, string)) {
	if k.busy {
		return
	}
	p.flushFields()
	k.seq++
	seq := k.seq
	k.busy, k.done = true, false
	if c.Offline {
		k.busy, k.done, k.ok, k.msg = false, true, false, "Перевірка недоступна в демо-режимі."
		return
	}
	p.cfg.barrier(c, func() {
		c.Background(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			ok, msg := fn(ctx)
			c.Post(func() {
				if k.seq == seq {
					k.busy, k.done, k.ok, k.msg = false, true, ok, msg
				}
			})
		})
	})
}
