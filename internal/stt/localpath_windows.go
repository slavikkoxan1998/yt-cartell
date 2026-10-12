//go:build windows

package stt

import "syscall"

// shortPath is the 8.3 form of an existing path (ASCII on volumes that keep
// short names), "" when unavailable.
func shortPath(p string) string {
	u, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return ""
	}
	buf := make([]uint16, 520)
	n, err := syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
	if err == nil && n > uint32(len(buf)) {
		buf = make([]uint16, n)
		n, err = syscall.GetShortPathName(u, &buf[0], uint32(len(buf)))
	}
	if err != nil || n == 0 || n > uint32(len(buf)) {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
