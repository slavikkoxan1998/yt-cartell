//go:build !windows

package sysx

import (
	"os"
	"path/filepath"
	"syscall"
)

// acquire: an exclusive flock on a file in the temp folder (freed by the
// kernel when the process dies).
func acquire(name string) (func(), bool) {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), name+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, true // cannot tell: never block the app
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() { f.Close() }, true
}
