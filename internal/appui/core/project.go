package core

import (
	"path/filepath"

	"gioui.org/unit"

	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// ProjectView is one frame's read-only snapshot of a project. Change the
// project only through the engine (c.Edit, c.Run, c.E.Regen …); the snapshot
// refreshes on the next frame after the engine reports the change.
type ProjectView struct {
	ID  string
	P   studio.Project
	OK  bool
	Dir string // absolute project folder
	// Ver increments on every refresh: key caches and editors' "re-sync from
	// the model" checks on it.
	Ver   int64
	stale bool
}

// Abs is the absolute path of a project-relative file ("" stays "").
func (v *ProjectView) Abs(rel string) string {
	if rel == "" {
		return ""
	}
	return filepath.Join(v.Dir, filepath.FromSlash(rel))
}

// Running reports that a stage job is running on the project.
func (v *ProjectView) Running() bool {
	return v.P.Job != nil && v.P.Job.Status == studio.JobRunning
}

// RunningKind is the kind of the running job ("" when idle).
func (v *ProjectView) RunningKind() studio.JobKind {
	if v.Running() {
		return v.P.Job.Kind
	}
	return ""
}

// Scene returns the scene with id and its index (nil, -1 when missing).
func (v *ProjectView) Scene(id string) (*studio.Scene, int) {
	for i := range v.P.Scenes {
		if v.P.Scenes[i].ID == id {
			return &v.P.Scenes[i], i
		}
	}
	return nil, -1
}

// Character returns the cast member with id (nil when missing).
func (v *ProjectView) Character(id string) *studio.Character {
	for i := range v.P.Characters {
		if v.P.Characters[i].ID == id {
			return &v.P.Characters[i]
		}
	}
	return nil
}

// Location returns the location with id (nil when missing).
func (v *ProjectView) Location(id string) *studio.Location {
	for i := range v.P.Locations {
		if v.P.Locations[i].ID == id {
			return &v.P.Locations[i]
		}
	}
	return nil
}

// ProjectUI is per-project UI state shared by the workspace and its steps
// (kept while the app runs, per project id).
type ProjectUI struct {
	// Scene is the selected scene (scene navigator ↔ step grids).
	Scene string
	// ScrollTo asks the visible step to scroll to this scene once; the step
	// clears it after scrolling.
	ScrollTo string
	// Grid: scene lists show cards in a grid (true) or as a list (false).
	List bool
	// Export asks step 5 to open its «Експортувати в…» drawer once (the
	// project menu sets it and navigates there; the step clears it).
	Export bool
	// Gaps asks step 5 to open its «Не всі сцени готові» choice once (the
	// banner of an autopilot that stopped before the montage sets it).
	Gaps bool
}

// StepView is one step of the project workspace. The workspace (package
// project) owns the header card, the step tabs and the right context column;
// a StepView draws the step's main column and may add context cards.
//
// Layout gets the whole main-column area (gtx.Constraints.Max) and scrolls
// its own content (gui.Scroll / layout.List), so long scene lists can be
// virtualised; the header and tabs stay put above it.
type StepView interface {
	Layout(gtx C, c *Ctx, pv *ProjectView) D
	// Side draws step-specific cards for the context column (width 360 dp at
	// ≥ 1440 dp, else stacked under the main column). Return D{} for none.
	Side(gtx C, c *Ctx, pv *ProjectView) D
	// Next is the step's call to action for the workspace's NextStepCard.
	Next(c *Ctx, pv *ProjectView) Next
}

// Bleeder is an optional StepView interface: the workspace lays the step out
// Bleed() dp wider on each side than the main column (so card shadows are
// not cut at the column's edge) and the step insets its content by the same
// amount.
type Bleeder interface {
	Bleed() unit.Dp
}

// Next is a "next step" call to action.
type Next struct {
	Icon  *gui.Icon
	Title string
	Text  string
	Busy  bool   // the step is running: spinner instead of the arrow
	Do    func() // nil = informational card without a button
}

// AssetState maps an engine asset to the kit's status pill state and percent.
func AssetState(a studio.Asset) (gui.AssetState, int) {
	switch {
	case a.Status == studio.StatusRunning:
		return gui.StateRunning, a.Progress
	case a.Status == studio.StatusQueued:
		return gui.StateQueued, -1
	case a.Status == studio.StatusError:
		return gui.StateError, -1
	case a.Status == studio.StatusDone && a.Stale:
		return gui.StateStale, -1
	case a.Status == studio.StatusDone:
		return gui.StateDone, -1
	}
	return gui.StateEmpty, -1
}

// Pill is the status pill of an asset.
func Pill(a studio.Asset) gui.StatusPill {
	s, pct := AssetState(a)
	return gui.StatusPill{State: s, Percent: pct}
}
