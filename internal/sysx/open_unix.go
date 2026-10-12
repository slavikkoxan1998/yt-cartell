//go:build !windows && !darwin

package sysx

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// openDefault uses xdg-open (or gio). Some xdg-open fallbacks block until the
// app exits, so the helper is reaped in the background.
func openDefault(target string) error {
	for _, cand := range [][]string{{"xdg-open", target}, {"gio", "open", target}} {
		if p, err := exec.LookPath(cand[0]); err == nil {
			return startDetached(exec.Command(p, cand[1:]...))
		}
	}
	return errors.New("не знайдено xdg-open — встанови пакет xdg-utils")
}

// reveal asks the file manager over D-Bus (org.freedesktop.FileManager1 —
// Nautilus, Dolphin, Nemo, Caja, Thunar…) to select the item; falls back to
// opening the containing folder.
func reveal(abs string) error {
	if p, err := exec.LookPath("dbus-send"); err == nil {
		// dbus-send splits array items on ',', so commas must be escaped.
		uri := strings.ReplaceAll((&url.URL{Scheme: "file", Path: abs}).String(), ",", "%2C")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, p, "--session", "--print-reply", "--type=method_call",
			"--dest=org.freedesktop.FileManager1", "/org/freedesktop/FileManager1",
			"org.freedesktop.FileManager1.ShowItems", "array:string:"+uri, "string:")
		if c.Run() == nil {
			return nil
		}
	}
	return openDefault(filepath.Dir(abs))
}
