// Package queue is «Черга» of the new UI (GUI.md → Screens → 3): what is
// generating right now across every project (studio.Engine.Queue) with
// animated progress, the recent results with «Повторити» for errors,
// «Відкрити проєкт» on every row and «Скасувати все». The shell shows the
// same content in two places: the drawer that slides in from the rail
// (DrawerBody + DrawerFooter inside a gui.Drawer) and the full page (Page),
// which lays it out wide (running | recent).
package queue

import (
	"sync/atomic"

	"ytcartell/internal/studio"
)

// Feed caches the engine's queue snapshot and refreshes it only after the
// engine reported a change, so the rail badge, the Home card, the drawer and
// the page can all read it every frame for free. Create one per window.
type Feed struct {
	e          *studio.Engine
	invalidate func()
	dirty      atomic.Bool
	snap       studio.QueueSnapshot
	unsub      func()
}

// NewFeed subscribes to the engine; invalidate (may be nil) asks for a new
// frame after a change, so a notification that lands just after a frame read
// the snapshot is never lost.
func NewFeed(e *studio.Engine, invalidate func()) *Feed {
	f := &Feed{e: e, invalidate: invalidate}
	f.dirty.Store(true)
	f.unsub = e.Subscribe(func(string) {
		f.dirty.Store(true)
		if f.invalidate != nil {
			f.invalidate()
		}
	})
	return f
}

// Snapshot is the queue as of the last engine change (UI goroutine).
func (f *Feed) Snapshot() studio.QueueSnapshot {
	if f.dirty.Swap(false) {
		f.snap = f.e.Queue()
	}
	return f.snap
}

// Running is the number of rows generating right now (the rail badge).
func (f *Feed) Running() int { return f.Snapshot().Counts.Running }

// Jobs is the number of project jobs running (single regenerations cannot
// be stopped, so «Скасувати все» and the close warning count jobs only).
func (f *Feed) Jobs() int {
	n := 0
	for _, it := range f.Snapshot().Running {
		if it.Kind != "" {
			n++
		}
	}
	return n
}

// Close unsubscribes from the engine.
func (f *Feed) Close() {
	if f.unsub != nil {
		f.unsub()
	}
}
