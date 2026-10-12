//go:build darwin

package sysx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func runOSA(args []string) (string, error) {
	c := exec.Command("osascript", args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		if osaCancelled(stderr.Bytes()) {
			return "", nil
		}
		return "", fmt.Errorf("не вдалося відкрити діалог: %v %s", err, tail(stderr.Bytes(), 300))
	}
	return dialogOutput(stdout.Bytes()), nil
}

func pickFolder(title, start string) (string, error) {
	p, err := runOSA(osaFolderArgs(title, start))
	if len(p) > 1 {
		p = strings.TrimRight(p, "/") // POSIX path of a folder ends with "/"
	}
	return p, err
}

func pickFile(title string, exts []string) (string, error) {
	return runOSA(osaFileArgs(title, exts))
}

func pickSave(title, dir, name, ext string) (string, error) {
	p, err := runOSA(osaSaveArgs(title, dir, name))
	// "choose file name" keeps whatever the user typed; add the extension back.
	if p != "" && ext != "" && !strings.Contains(p[strings.LastIndex(p, "/")+1:], ".") {
		p += "." + ext
	}
	return p, err
}
