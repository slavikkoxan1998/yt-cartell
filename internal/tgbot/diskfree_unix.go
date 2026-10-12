//go:build !windows

package tgbot

import "golang.org/x/sys/unix"

// diskFree reports the free and total bytes of the volume holding dir.
func diskFree(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize) //nolint:unconvert // int64 on Linux, uint32 on macOS
	return uint64(st.Bavail) * bs, uint64(st.Blocks) * bs, nil
}
