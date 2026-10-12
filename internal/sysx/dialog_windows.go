//go:build windows

package sysx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

func powershell() string {
	if p := systemDir("System32", "WindowsPowerShell", "v1.0", "powershell.exe"); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p
	}
	return "powershell.exe"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// runPS runs a WinForms dialog script in a windowless, single-threaded
// apartment PowerShell (-STA is required by WinForms dialogs).
func runPS(script string) (string, error) {
	c := exec.Command(powershell(), psArgs(script)...)
	noConsoleWindow(c)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("не вдалося відкрити діалог: %v %s", err, tail(stderr.Bytes(), 300))
	}
	return psResult(stdout.Bytes()), nil
}

func pickFolder(title, start string) (string, error) {
	return runPS(psFolderScript(title, start))
}

func pickFile(title string, exts []string) (string, error) {
	return runPS(psFileScript(title, exts))
}

func pickSave(title, dir, name, ext string) (string, error) {
	return runPS(psSaveScript(title, dir, name, ext))
}
