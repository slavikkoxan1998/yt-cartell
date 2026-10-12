//go:build windows

package media

import (
	"context"
	"testing"
)

func TestCommandHidesConsole(t *testing.T) {
	c := command(context.Background(), "cmd", "/c", "exit")
	if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow || c.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("console not hidden")
	}
}
