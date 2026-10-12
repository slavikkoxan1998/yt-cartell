// Package produce holds steps 3 Розкадровка and 4 Відео й озвучка of the
// project workspace (GUI.md → Screens → 2) and Lightbox, the full-window
// picture viewer they open.
//
// Both steps scroll their own content through a virtualised list (one
// element per grid row), so projects with dozens of scenes stay at 60 fps;
// pictures decode at display size through the shared image cache. Paid
// actions go through the engine (Run, Regen — both return at once) and are
// refused offline; slow work (file pickers, imports, the exact timeline)
// runs through Ctx.Background/Do.
package produce

import (
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
)

// NewStoryboard returns step 3 (references + scene frames + 3×3 boards).
func NewStoryboard() core.StepView { return newStoryboard() }

// NewRender returns step 4 (scene clips + voices).
func NewRender() core.StepView { return newRender() }

// bleed is how far both steps' pages reach past their cards on each side
// (card shadows, the scrollbar): the workspace widens the main column by it
// (core.Bleeder) and the steps inset their content by the same amount.
const bleed unit.Dp = 12

var (
	_ core.Bleeder = (*storyboard)(nil)
	_ core.Bleeder = (*render)(nil)
)

// Bleed implements core.Bleeder.
func (v *storyboard) Bleed() unit.Dp { return bleed }

// Bleed implements core.Bleeder.
func (v *render) Bleed() unit.Dp { return bleed }
