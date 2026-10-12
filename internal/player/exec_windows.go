//go:build windows

package player

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW: the decoder gets no console, so nothing flashes.
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
