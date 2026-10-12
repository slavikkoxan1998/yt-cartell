package tgbot

import "golang.org/x/sys/windows"

// diskFree reports the free and total bytes of the volume holding dir.
func diskFree(dir string) (free, total uint64, err error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var avail, all, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &all, &totalFree); err != nil {
		return 0, 0, err
	}
	return avail, all, nil
}
