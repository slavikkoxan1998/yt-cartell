//go:build !windows

package gui

// SystemDark reports the system dark-mode preference. Only Windows is read;
// elsewhere the app starts light.
func SystemDark() bool { return false }
