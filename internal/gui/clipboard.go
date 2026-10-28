package gui

import (
	"os/exec"
	"strings"
)

// ReadSystemClipboard reads text from the system clipboard using native desktop utilities
// (wl-paste for Wayland, xclip or xsel for X11) when available.
func ReadSystemClipboard() string {
	// 1. Wayland
	if p, err := exec.LookPath("wl-paste"); err == nil {
		if out, err := exec.Command(p, "--no-newline").Output(); err == nil && len(out) > 0 {
			return strings.TrimSpace(string(out))
		}
	}
	// 2. X11 xclip
	if p, err := exec.LookPath("xclip"); err == nil {
		if out, err := exec.Command(p, "-selection", "clipboard", "-o").Output(); err == nil && len(out) > 0 {
			return strings.TrimSpace(string(out))
		}
	}
	// 3. X11 xsel
	if p, err := exec.LookPath("xsel"); err == nil {
		if out, err := exec.Command(p, "--clipboard", "--output").Output(); err == nil && len(out) > 0 {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}
