//go:build !windows && !unix

package sysx

import "os/exec"

func launch(c *exec.Cmd) error { return startDetached(c) }
