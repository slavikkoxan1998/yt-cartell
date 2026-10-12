package core

import "ytcartell/internal/gui"

// Placeholder draws a calm "coming soon" card; stub screens use it until
// their package is implemented.
func Placeholder(gtx C, c *Ctx, title string) D {
	var none gui.Clickable
	return gui.EmptyState{Icon: gui.IconSparkles, Title: title, Text: "Екран у розробці."}.Layout(gtx, c.Th, &none)
}

// stepStub is a StepView placeholder.
type stepStub struct{ title string }

// StepStub returns a placeholder StepView (used by stub packages).
func StepStub(title string) StepView { return stepStub{title} }

func (s stepStub) Layout(gtx C, c *Ctx, _ *ProjectView) D { return Placeholder(gtx, c, s.title) }
func (stepStub) Side(C, *Ctx, *ProjectView) D             { return D{} }
func (stepStub) Next(*Ctx, *ProjectView) Next             { return Next{} }
