//go:build !windows

package sysx

import "os/exec"

// There is no console window to hide outside Windows.
func hideConsole(*exec.Cmd) {}

func noConsoleWindow(*exec.Cmd) {}
