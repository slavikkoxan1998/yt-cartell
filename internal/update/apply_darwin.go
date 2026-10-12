//go:build darwin

package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ytcartell/internal/sysx"
)

func installable() error {
	exe, err := runningExe()
	if err != nil {
		return errManual("Не вдалося визначити, де лежить програма.")
	}
	bundle, ok := bundleOf(exe)
	if !ok {
		return errManual("Студію запущено не з пакета «YT Cartell Studio.app».")
	}
	return writable(filepath.Dir(bundle))
}

// apply replaces the running .app bundle with the one in the zip: unpacked
// next to it (exec bits kept, no path escapes), the bundles swapped (the
// running one becomes "<name>.old.app", removed at the next start), the
// quarantine flag cleared. The Restart opens a new instance with `open -n`.
// A binary started outside a bundle cannot be swapped: KindManual.
func apply(staged string) (Restart, error) {
	if !strings.EqualFold(filepath.Ext(staged), ".zip") {
		return nil, errManual("Це не збірка для macOS.")
	}
	exe, err := runningExe()
	if err != nil {
		return nil, errManual("Не вдалося визначити, де лежить програма.")
	}
	bundle, ok := bundleOf(exe)
	if !ok {
		return nil, errManual("Студію запущено не з пакета «YT Cartell Studio.app».")
	}
	app, stage, err := stageBundle(staged, bundle)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err := swapBundle(app, bundle); err != nil {
		return nil, err
	}
	_ = os.Remove(staged)
	// Downloaded by the app itself the files carry no quarantine flag, but
	// a bundle copied in by hand might: Gatekeeper would stop the relaunch.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = sysx.Command(ctx, "/usr/bin/xattr", "-dr", "com.apple.quarantine", bundle).Run()
	return func(from string) error {
		return launch("/usr/bin/open", "-n", bundle, "--args", UpdatedFlag+"="+from)
	}, nil
}
