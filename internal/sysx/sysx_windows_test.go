//go:build windows

package sysx

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCommandHiddenOnWindows(t *testing.T) {
	c := Command(context.Background(), "ffmpeg", "-version")
	if c.SysProcAttr == nil || !c.SysProcAttr.HideWindow || c.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("console not hidden: %+v", c.SysProcAttr)
	}
	d := Command(context.Background(), "powershell.exe")
	noConsoleWindow(d) // idempotent flag merge
	if d.SysProcAttr.CreationFlags != createNoWindow {
		t.Fatalf("flags = %#x", d.SysProcAttr.CreationFlags)
	}
}

func psAvailable(t *testing.T) string {
	t.Helper()
	p := powershell()
	if _, err := os.Stat(p); err != nil {
		t.Skip("powershell.exe not available")
	}
	return p
}

// runPSNoDialog runs a script the same way runPS does (no dialog involved).
func runPSNoDialog(t *testing.T, script string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := Command(ctx, psAvailable(t), psArgs(script)...)
	out, err := c.Output()
	if err != nil {
		t.Fatalf("powershell: %v", err)
	}
	return string(out)
}

// The Windows command line must deliver quotes, apostrophes, backslashes,
// '$' and Cyrillic untouched, and the base64 output must decode back.
func TestPowerShellRoundTrip(t *testing.T) {
	tricky := `пам’ять "лапки" it's C:\Темп\ $env:PATH ‘x’ | *.mp3;*.wav`
	script := "[Console]::Out.Write([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(" + psQuote(tricky) + ")))"
	if got := psResult([]byte(runPSNoDialog(t, script))); got != tricky {
		t.Fatalf("round trip:\n got %q\nwant %q", got, tricky)
	}
}

// The dialog scripts must be syntactically valid PowerShell (parsed, not run).
func TestPowerShellDialogScriptsParse(t *testing.T) {
	scripts := map[string]string{
		"folder": psFolderScript("Вибери папку для проєктів", `C:\Users\пам’ять`),
		"file":   psFileScript("Вибери музику", []string{"mp3", "wav"}),
		"save":   psSaveScript("Зберегти відео як…", `C:\Out`, "фінал.mp4", "mp4"),
	}
	for name, s := range scripts {
		check := "$e = $null; [void][System.Management.Automation.Language.Parser]::ParseInput(" +
			psQuote(s) + ", [ref]$null, [ref]$e); [Console]::Out.Write($e.Count)"
		if got := strings.TrimSpace(runPSNoDialog(t, check)); got != "0" {
			t.Errorf("%s script has %s parse errors:\n%s", name, got, s)
		}
	}
}
