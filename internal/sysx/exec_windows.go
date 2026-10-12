//go:build windows

package sysx

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: a console program runs without a
// console window (no black flash when the GUI app starts ffmpeg).
const createNoWindow = 0x08000000

func hideConsole(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags |= createNoWindow
}

// noConsoleWindow suppresses only the console. Helpers that open their own
// windows (PowerShell dialogs) must not get HideWindow: Windows applies that
// SW_HIDE to the first window the process shows — the dialog itself.
func noConsoleWindow(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= createNoWindow
}
