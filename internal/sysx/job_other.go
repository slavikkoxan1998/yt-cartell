//go:build !windows

package sysx

import "os/exec"

// Elsewhere a child is simply started; the context passed to Command stops it.
func start(c *exec.Cmd) error { return c.Start() }
