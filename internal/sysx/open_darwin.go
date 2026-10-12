//go:build darwin

package sysx

import (
	"fmt"
	"os/exec"
)

func openDefault(target string) error { return runOpen(exec.Command("open", target)) }

// reveal selects the item in a Finder window.
func reveal(abs string) error { return runOpen(exec.Command("open", "-R", abs)) }

func runOpen(c *exec.Cmd) error {
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("open: %v %s", err, tail(out, 300))
	}
	return nil
}
