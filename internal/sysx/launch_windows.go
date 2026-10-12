//go:build windows

package sysx

import (
	"os/exec"
	"syscall"
)

const (
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// launch starts c outside any job the app may run in (a terminal's or an
// IDE's job would otherwise take the new build down with it). A job that
// forbids breaking away refuses the flag — then the child starts normally.
func launch(c *exec.Cmd) error {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createBreakawayFromJob}
	if err := startDetached(c); err == nil {
		return nil
	}
	retry := exec.Command(c.Path, c.Args[1:]...)
	retry.Dir = c.Dir
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
	return startDetached(retry)
}
