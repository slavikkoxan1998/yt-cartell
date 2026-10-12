//go:build !windows && !darwin

package sysx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/"
}

// runLinuxDialog prefers zenity (GTK), then kdialog (KDE). Both exit with 1
// when the user cancels.
func runLinuxDialog(zenity, kdialog []string) (string, error) {
	bin, args := "", []string(nil)
	if p, err := exec.LookPath("zenity"); err == nil {
		bin, args = p, zenity
	} else if p, err := exec.LookPath("kdialog"); err == nil {
		bin, args = p, kdialog
	} else {
		return "", errors.New("не знайдено zenity або kdialog — встанови один із них, щоб вибирати файли")
	}
	c := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("не вдалося відкрити діалог: %v %s", err, tail(stderr.Bytes(), 300))
	}
	return dialogOutput(stdout.Bytes()), nil
}

func pickFolder(title, start string) (string, error) {
	p, err := runLinuxDialog(zenityFolderArgs(title, start), kdialogFolderArgs(title, start, home()))
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
	}
	return p, err
}

func pickFile(title string, exts []string) (string, error) {
	return runLinuxDialog(zenityFileArgs(title, exts), kdialogFileArgs(title, exts, home()))
}

func pickSave(title, dir, name, ext string) (string, error) {
	p, err := runLinuxDialog(zenitySaveArgs(title, dir, name, ext), kdialogSaveArgs(title, dir, name, ext, home()))
	if p != "" && ext != "" && !strings.Contains(p[strings.LastIndex(p, "/")+1:], ".") {
		p += "." + ext
	}
	return p, err
}
