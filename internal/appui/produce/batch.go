package produce

import (
	"strings"
	"sync/atomic"
	"time"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/studio"
)

// batchItem is one regeneration of a batch; then (optional) runs after it
// succeeded — a frame whose prompt changed is redrawn before its clip.
type batchItem struct {
	kind studio.RegenKind
	id   string
	then *batchItem
}

func (it batchItem) size() int {
	n := 1
	for t := it.then; t != nil; t = t.then {
		n++
	}
	return n
}

// batch regenerates many items ("Перегенерувати змінені") through the
// engine's single-item Regen, with the engine's own concurrency limits
// (studio.ImageConcurrency, ClipConcurrency, VoiceConcurrency). It follows the
// engine's notifications instead of polling every item, keeps going when an
// item fails (the failure stays on its card) and can be stopped (items
// already started finish, like a stopped job).
type batch struct {
	on    atomic.Bool
	stop  atomic.Bool
	total atomic.Int32
	done  atomic.Int32
	label string // what runs ("Перемальовуємо змінені кадри"), UI goroutine only
}

func (b *batch) active() bool { return b.on.Load() }

// progress is (done, total).
func (b *batch) progress() (int, int) { return int(b.done.Load()), int(b.total.Load()) }

// Stop starts no new items.
func (b *batch) Stop() { b.stop.Store(true) }

func regenClass(k studio.RegenKind) int {
	switch k {
	case studio.RegenVideo:
		return 1
	case studio.RegenVoice:
		return 2
	}
	return 0
}

// start runs items (no-op while a batch runs). The first one starts right
// here through Ctx.Regen — offline or refused, nothing starts — and the
// rest follow from the background as the limits allow.
func (b *batch) start(c *core.Ctx, pid, label string, items []batchItem) {
	if b.on.Load() || len(items) == 0 {
		return
	}
	first := items[0]
	if !c.Regen(pid, first.kind, first.id, "") {
		return
	}
	total := 0
	for _, it := range items {
		total += it.size()
	}
	b.on.Store(true)
	b.stop.Store(false)
	b.total.Store(int32(total))
	b.done.Store(0)
	b.label = label
	e := c.E
	cfg := e.Config().Get()
	limits := [3]int{studio.ImageConcurrency(cfg), studio.ClipConcurrency(cfg), studio.VoiceConcurrency(cfg)}
	c.Background(func() {
		defer func() {
			b.on.Store(false)
			c.Invalidate()
		}()
		wake := make(chan struct{}, 1)
		cancel := e.Subscribe(func(id string) {
			if id == pid {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		})
		defer cancel()
		queue := append([]batchItem(nil), items[1:]...)
		active := map[string]batchItem{string(first.kind) + ":" + first.id: first}
		var running [3]int
		running[regenClass(first.kind)]++
		for {
			p, ok := e.Get(pid)
			if !ok {
				return
			}
			// Settle the items that finished.
			for k, it := range active {
				a := assetOf(&p, it.kind, it.id)
				if a != nil && busy(*a) {
					continue
				}
				delete(active, k)
				running[regenClass(it.kind)]--
				b.done.Add(1)
				switch {
				case it.then == nil:
				case a != nil && a.Status == studio.StatusDone && !b.stop.Load():
					queue = append([]batchItem{*it.then}, queue...)
				default:
					b.done.Add(int32(it.then.size())) // skipped with its failed predecessor
				}
			}
			if b.stop.Load() {
				for _, it := range queue {
					b.done.Add(int32(it.size()))
				}
				queue = nil
			}
			// Start what the limits allow, in order.
			for i := 0; i < len(queue); {
				it := queue[i]
				cl := regenClass(it.kind)
				if running[cl] >= limits[cl] {
					i++
					continue
				}
				queue = append(queue[:i], queue[i+1:]...)
				if !c.Regen(pid, it.kind, it.id, "") { // toasts why
					b.done.Add(int32(it.size()))
					continue
				}
				active[string(it.kind)+":"+it.id] = it
				running[cl]++
			}
			c.Invalidate()
			if len(active) == 0 && len(queue) == 0 {
				return
			}
			select {
			case <-wake:
			case <-time.After(2 * time.Second):
			}
		}
	})
}

// assetOf finds the asset a regeneration of kind targets.
func assetOf(p *studio.Project, kind studio.RegenKind, id string) *studio.Asset {
	switch kind {
	case studio.RegenCharacter:
		for i := range p.Characters {
			if p.Characters[i].ID == id {
				return &p.Characters[i].Image
			}
		}
	case studio.RegenLocation:
		for i := range p.Locations {
			if p.Locations[i].ID == id {
				return &p.Locations[i].Image
			}
		}
	default:
		for i := range p.Scenes {
			s := &p.Scenes[i]
			if s.ID != id {
				continue
			}
			switch kind {
			case studio.RegenFrame:
				return &s.Frame
			case studio.RegenVideo:
				return &s.Video
			case studio.RegenVoice:
				return &s.Audio
			}
		}
	}
	return nil
}

// ---- stale rules (port of the old step views) ----------------------------------------

// spoken: the scene has a voice to make (text, or a slice of the user's
// own recording).
func spoken(s *studio.Scene) bool {
	return strings.TrimSpace(s.Narration) != "" || s.AudioSource == studio.AudioUpload
}

// clipStale: the clip exists but its motion prompt, or its frame, changed
// after it was shot — it no longer matches the plan.
func clipStale(s *studio.Scene) bool {
	return s.Video.Status == studio.StatusDone &&
		(s.Video.Stale || (s.Frame.Status == studio.StatusDone && s.Frame.Stale))
}

// lineStale: the voice exists but its text, speaker or slice changed.
func lineStale(p *studio.Project, s *studio.Scene) bool {
	return p.Input.Voiceover && s.Audio.Status == studio.StatusDone && s.Audio.Stale && spoken(s)
}

// frameStale: the frame (or 3×3 sheet) was drawn before its prompt changed.
func frameStale(s *studio.Scene) bool {
	return s.Frame.Status == studio.StatusDone && s.Frame.Stale
}
