//go:build windows

package sysx

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	modShell32         = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW  = modShell32.NewProc("ShellExecuteW")
	modOle32           = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize = modOle32.NewProc("CoUninitialize")
)

const (
	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
	swShowNormal            = 1
	seErrNoAssoc            = 31 // SE_ERR_NOASSOC: no app registered for the type
)

func systemDir(parts ...string) string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

// openDefault = ShellExecute "open": default app for files/URLs, Explorer for folders.
func openDefault(target string) error {
	if procShellExecuteW.Find() != nil {
		return startDetached(exec.Command(systemDir("explorer.exe"), target))
	}
	code := shellExecute("open", target)
	if code == seErrNoAssoc {
		// Unknown type → Windows' own "Open with" chooser.
		code = shellExecute("openas", target)
	}
	switch {
	case code > 32:
		return nil
	case code == seErrNoAssoc:
		return fmt.Errorf("немає програми, щоб відкрити %s", target)
	default:
		return fmt.Errorf("не вдалося відкрити %s (ShellExecute %d)", target, code)
	}
}

// shellExecute runs ShellExecuteW on a dedicated COM-initialised (STA) thread:
// it may hand the verb to COM shell extensions.
func shellExecute(verb, target string) uintptr {
	v, err := syscall.UTF16PtrFromString(verb)
	if err != nil {
		return 0
	}
	f, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return 0
	}
	done := make(chan uintptr, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE); int32(hr) >= 0 {
			defer procCoUninitialize.Call()
		}
		r, _, _ := procShellExecuteW.Call(0,
			uintptr(unsafe.Pointer(v)), uintptr(unsafe.Pointer(f)), 0, 0, swShowNormal)
		done <- r
	}()
	return <-done
}

// reveal opens Explorer with the item selected. Explorer parses its own
// command line, so /select,"<path>" is passed raw (Go would quote the whole
// argument, which Explorer does not understand).
func reveal(abs string) error {
	c := exec.Command(systemDir("explorer.exe"))
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + abs + `"`}
	return startDetached(c)
}
