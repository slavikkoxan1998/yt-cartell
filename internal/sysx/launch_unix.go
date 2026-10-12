//go:build unix

package sysx

import (
	"os/exec"
	"syscall"
)

// launch starts c in its own session: it survives the app and its terminal.
func launch(c *exec.Cmd) error {
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return startDetached(c)
}
