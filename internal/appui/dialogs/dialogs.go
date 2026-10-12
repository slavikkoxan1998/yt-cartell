// Package dialogs draws the global sheets the shell hosts (core.Sheet): new
// project, translate, own voice-over, remake after a YouTube example, profile
// editor and profiles manager.
// Every sheet is a gui.Drawer that slides in from the right over a scrim
// (the overlay layer is registered through th.Layer, so it sits above the
// page, popovers included) with its actions pinned at the bottom.
//
//	s := dialogs.New()
//	s.Open(c, core.Sheet{Kind: core.SheetNewProject, Text: story})   // Host.OpenSheet
//	s.Layout(gtx, c)                                                  // every frame
//
// Opening a sheet closes the one that is open, except the profile editor
// opened from the profiles manager: it slides in on top and the manager is
// still there when it closes.
package dialogs

import (
	"ytcartell/internal/appui/core"
)

type (
	C = core.C
	D = core.D
)

// sheet is one of the global sheets.
type sheet interface {
	kind() core.SheetKind
	// open resets the sheet for s and slides it in.
	open(c *core.Ctx, s core.Sheet)
	// shut slides it out.
	shut()
	// isOpen reports whether it is open (not closing).
	isOpen() bool
	// layout queues the sheet's overlay layer (nothing when fully hidden).
	layout(gtx C, c *core.Ctx, host *Sheets)
}

// Sheets is the state of every global sheet.
type Sheets struct {
	newProject newProjectSheet
	translate  translateSheet
	ownVoice   ownVoiceSheet
	profile    profileSheet
	profiles   profilesSheet
	remake     remakeSheet
	// order is the z-order (last = on top).
	order []sheet
}

// New creates the sheets host.
func New() *Sheets {
	s := &Sheets{}
	s.order = []sheet{&s.newProject, &s.translate, &s.ownVoice, &s.remake, &s.profiles, &s.profile}
	return s
}

func (s *Sheets) byKind(k core.SheetKind) sheet {
	for _, sh := range s.order {
		if sh.kind() == k {
			return sh
		}
	}
	return nil
}

// Open opens a sheet (replacing the one open, if any; the profile editor
// stacks over the profiles manager).
func (s *Sheets) Open(c *core.Ctx, sh core.Sheet) {
	target := s.byKind(sh.Kind)
	if target == nil {
		return
	}
	stack := sh.Kind == core.SheetProfile && s.profiles.isOpen()
	for _, o := range s.order {
		if o == target || (stack && o == sheet(&s.profiles)) {
			continue
		}
		if o.isOpen() {
			o.shut()
		}
	}
	// Bring the target to the top of the z-order.
	for i, o := range s.order {
		if o == target {
			s.order = append(append(s.order[:i:i], s.order[i+1:]...), target)
			break
		}
	}
	target.open(c, sh)
}

// IsOpen reports whether any sheet is open (the shell may pause shortcuts).
func (s *Sheets) IsOpen() bool {
	for _, o := range s.order {
		if o.isOpen() {
			return true
		}
	}
	return false
}

// Layout draws the open sheets as overlay layers (call once per frame after
// the page content; the sheets register themselves via th.Layer).
func (s *Sheets) Layout(gtx C, c *core.Ctx) {
	for _, o := range append([]sheet(nil), s.order...) {
		o.layout(gtx, c, s)
	}
}
