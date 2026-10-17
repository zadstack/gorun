//go:build darwin || linux

package admin

import "syscall"

// diskUsage returns free and total bytes of the filesystem holding path.
func diskUsage(path string) (free, total uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, false
	}
	bsize := uint64(st.Bsize) //nolint:gosec // block size is positive
	return uint64(st.Bavail) * bsize, uint64(st.Blocks) * bsize, true
}
