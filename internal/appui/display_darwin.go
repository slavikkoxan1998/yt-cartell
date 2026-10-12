//go:build darwin

package appui

/*
#cgo CFLAGS: -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreVideo
#include <CoreVideo/CoreVideo.h>

// ytcDisplayLinkOK reports whether a display link for the active displays
// can be created right now — the call Gio makes when it opens a window.
static int ytcDisplayLinkOK(void) {
	CVDisplayLinkRef dl = NULL;
	if (CVDisplayLinkCreateWithActiveCGDisplays(&dl) != kCVReturnSuccess || dl == NULL) {
		return 0;
	}
	CVDisplayLinkRelease(dl);
	return 1;
}
*/
import "C"

import (
	"log"
	"time"
)

// waitForDisplay blocks until macOS has an active display. Gio (v0.10.3)
// opens its window with a CVDisplayLink of the active displays; with none —
// the lid closed or the screen off while macOS reopens apps after a night
// update — that fails and Gio panics ("runtime/cgo: misuse of an invalid
// Handle") instead of returning the error. So the window opens once there
// is a screen to show it on.
func waitForDisplay() {
	if C.ytcDisplayLinkOK() != 0 {
		return
	}
	log.Printf("appui: no active display — the window opens when one is back")
	start := time.Now()
	for C.ytcDisplayLinkOK() == 0 {
		time.Sleep(2 * time.Second)
	}
	log.Printf("appui: display is back after %s", time.Since(start).Round(time.Second))
}
