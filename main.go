// YT Cartell Studio — the desktop edition: story → plan → storyboard →
// video + voice → montage → mp4, in its own window.
//
// Entry point: settings store → production engine → UI.
package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"ytcartell/internal/appui"
	"ytcartell/internal/config"
	"ytcartell/internal/studio"
	"ytcartell/internal/sysx"
)

func main() {
	openLog()
	// One copy at a time (after an update, wait while the old one closes).
	wait := time.Duration(0)
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "--updated-from") {
			wait = 20 * time.Second
		}
	}
	single, ok := sysx.AcquireSingle("YTCartellStudio", wait)
	if !ok {
		appui.AlreadyRunning()
	}
	defer single.Release()
	var notices []string
	store, err := config.NewStore()
	if err != nil {
		// Unreadable settings fall back to defaults; say so instead of failing.
		log.Printf("config: %v", err)
		notices = append(notices, "Не вдалося прочитати налаштування ("+err.Error()+") — використано типові.")
	}
	e, err := studio.NewEngine(store)
	if e == nil {
		appui.Fatal(err)
	}
	if err != nil {
		// The engine still works; the user can pick another data folder.
		log.Printf("engine: %v", err)
		notices = append(notices, err.Error())
	}
	if err := appui.Run(e, notices...); err != nil {
		appui.Fatal(err)
	}
}

// openLog mirrors the log into <settings dir>/studio.log: the GUI build has
// no console, and the file is what a user can send when something breaks.
func openLog() {
	dir := config.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "studio.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	// The file first: in the GUI build stderr is invalid and MultiWriter
	// stops at the first failing writer.
	log.SetOutput(io.MultiWriter(f, os.Stderr))
	log.SetFlags(log.Ldate | log.Ltime)
	appui.LogPath = path
	// A panic on any goroutine (players, decoders, services) writes its trace
	// here too: in the GUI build stderr goes nowhere.
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		log.Printf("crash output: %v", err)
	}
}
