//go:build !windows

package appui

import (
	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/unit"
)

// platformEvent: the icon comes from the app bundle / desktop file elsewhere.
func platformEvent(event.Event) {}

// showFatal: the message is already logged to stderr.
func showFatal(title, msg string) {}

// windowPlacement: the window manager places and clamps the window.
func windowPlacement(w, h unit.Dp) (opts []app.Option, center bool) {
	return []app.Option{app.Size(w, h)}, false
}
