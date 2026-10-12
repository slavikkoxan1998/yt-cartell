package sysx

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

// Launch starts a program that must outlive the app — the freshly installed
// build after an update, `open -n` on macOS. Unlike Start it never joins the
// kill-on-close job, gets no hidden window (it is a GUI app) and is detached
// from the app's console/session; it runs in the program's own folder. The
// child is reaped in the background, so Launch returns at once.
func Launch(name string, args ...string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("не вказано програму")
	}
	c := exec.Command(name, args...)
	if filepath.IsAbs(name) {
		c.Dir = filepath.Dir(name)
	}
	return launch(c)
}
