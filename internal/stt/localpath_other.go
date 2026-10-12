//go:build !windows

package stt

// shortPath: paths are passed to whisper.cpp as UTF-8 bytes outside Windows.
func shortPath(string) string { return "" }
