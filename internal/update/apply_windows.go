//go:build windows

package update

import (
	"path/filepath"
	"strings"
)

func installable() error {
	exe, err := runningExe()
	if err != nil {
		return errManual("Не вдалося визначити, де лежить програма.")
	}
	return writable(filepath.Dir(exe))
}

// apply swaps the running exe for the downloaded one: the running file is
// renamed to <exe>.old (Windows allows renaming a running image), the new
// one takes its name. The Restart starts it with --updated-from.
func apply(staged string) (Restart, error) {
	if !strings.EqualFold(filepath.Ext(staged), ".exe") {
		return nil, errManual("Це не збірка для Windows.")
	}
	exe, err := runningExe()
	if err != nil {
		return nil, errManual("Не вдалося визначити, де лежить програма.")
	}
	if err := swapFile(staged, exe); err != nil {
		return nil, err
	}
	return func(from string) error { return launch(exe, UpdatedFlag+"="+from) }, nil
}
