//go:build !darwin

package appui

// waitForDisplay: only macOS needs an active display to open the window.
func waitForDisplay() {}
