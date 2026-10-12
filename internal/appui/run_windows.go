//go:build windows

package appui

// Windows bits of the shell: the window icon (from the embedded PNG, so a
// plain `go build` shows the logo too) and the fatal-error message box.

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"sync"
	"syscall"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/event"
	"gioui.org/unit"
	xdraw "golang.org/x/image/draw"

	"ytcartell/internal/assets"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	procGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
)

const (
	wmSetIcon    = 0x0080
	iconSmall    = 0
	iconBig      = 1
	smCxIcon     = 11
	smCxSmIcon   = 49
	mbIconError  = 0x10
	mbSetForegnd = 0x10000
	spiWorkArea  = 0x0030
)

var (
	iconOnce          sync.Once
	hIconBig, hIconSm uintptr
)

// platformEvent sets the window icon once the native window exists.
func platformEvent(ev event.Event) {
	v, ok := ev.(app.Win32ViewEvent)
	if !ok || v.HWND == 0 {
		return
	}
	iconOnce.Do(func() {
		data := squarePNG(assets.Icon(), 256)
		if len(data) == 0 {
			return
		}
		big, _, _ := procGetSystemMetrics.Call(smCxIcon)
		small, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		hIconBig = iconFromPNG(data, int(big))
		hIconSm = iconFromPNG(data, int(small))
	})
	// PostMessage, not SendMessage: the window thread may be waiting on us.
	if hIconBig != 0 {
		procPostMessageW.Call(v.HWND, wmSetIcon, iconBig, hIconBig)
	}
	if hIconSm != 0 {
		procPostMessageW.Call(v.HWND, wmSetIcon, iconSmall, hIconSm)
	}
}

// iconFromPNG makes an HICON from PNG bytes (Vista+ accepts PNG icon data).
func iconFromPNG(data []byte, size int) uintptr {
	if size <= 0 {
		size = 32
	}
	h, _, _ := procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), 1, 0x00030000, uintptr(size), uintptr(size), 0)
	return h
}

// squarePNG fits a PNG into a transparent size×size square (icons must be square).
func squarePNG(src []byte, size int) []byte {
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil
	}
	b := img.Bounds()
	w, h := size, size
	if b.Dx() >= b.Dy() {
		h = max(1, b.Dy()*size/b.Dx())
	} else {
		w = max(1, b.Dx()*size/b.Dy())
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	r := image.Rect((size-w)/2, (size-h)/2, (size-w)/2+w, (size-h)/2+h)
	xdraw.CatmullRom.Scale(dst, r, img, b, draw.Over, nil)
	var out bytes.Buffer
	if png.Encode(&out, dst) != nil {
		return nil
	}
	return out.Bytes()
}

// showFatal shows a blocking error box (there is no console in the GUI build).
func showFatal(title, msg string) {
	t, err1 := syscall.UTF16PtrFromString(title)
	m, err2 := syscall.UTF16PtrFromString(msg)
	if err1 != nil || err2 != nil {
		return
	}
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError|mbSetForegnd)
}

// windowPlacement sizes the window for the primary monitor's work area (the
// screen minus the taskbar): w×h dp centred when it fits with its frame,
// else maximized — on scaled laptop screens (1920×1080 at 150% is 1280×720
// dp) a fixed 1440×900 window would hide the dock behind the taskbar.
func windowPlacement(w, h unit.Dp) (opts []app.Option, center bool) {
	var r struct{ Left, Top, Right, Bottom int32 }
	if ok, _, _ := procSystemParametersInfoW.Call(spiWorkArea, 0, uintptr(unsafe.Pointer(&r)), 0); ok == 0 {
		return []app.Option{app.Size(w, h)}, false
	}
	dpi := uintptr(96)
	if procGetDpiForSystem.Find() == nil {
		if d, _, _ := procGetDpiForSystem.Call(); d > 0 {
			dpi = d
		}
	}
	scale := float32(dpi) / 96
	workW := unit.Dp(float32(r.Right-r.Left) / scale)
	workH := unit.Dp(float32(r.Bottom-r.Top) / scale)
	// Room for the borders, the title bar and a little air.
	availW, availH := workW-24, workH-56
	if availW <= 0 || availH <= 0 {
		return []app.Option{app.Size(w, h)}, false
	}
	if w <= availW && h <= availH {
		return []app.Option{app.Size(w, h)}, true
	}
	// Restoring from maximized then gives a window that fits too.
	return []app.Option{app.Size(min(w, availW), min(h, availH)), app.Maximized.Option()}, false
}
