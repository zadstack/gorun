//go:build !(darwin || linux)

package diagnostics

// DiskUsage is unsupported on this platform.
func DiskUsage(string) (free, total uint64, ok bool) { return 0, 0, false }
