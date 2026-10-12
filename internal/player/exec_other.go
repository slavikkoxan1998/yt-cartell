//go:build !windows

package player

import "os/exec"

func hideConsole(*exec.Cmd) {}
