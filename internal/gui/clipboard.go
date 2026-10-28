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

// WriteSystemClipboard writes text to the system clipboard using native desktop utilities
// (wl-copy for Wayland, xclip or xsel for X11) when available.
func WriteSystemClipboard(text string) {
	if text == "" {
		return
	}
	// 1. Wayland
	if p, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command(p)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return
		}
	}
	// 2. X11 xclip
	if p, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command(p, "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return
		}
	}
	// 3. X11 xsel
	if p, err := exec.LookPath("xsel"); err == nil {
		cmd := exec.Command(p, "--clipboard", "--input")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return
		}
	}
}
