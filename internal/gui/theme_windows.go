//go:build windows

package gui

import "golang.org/x/sys/windows/registry"

// SystemDark reports whether Windows apps are set to the dark theme
// (HKCU\...\Themes\Personalize, AppsUseLightTheme = 0). Any failure means light.
func SystemDark() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	return err == nil && v == 0
}
