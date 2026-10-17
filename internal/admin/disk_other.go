//go:build !(darwin || linux)

package admin

// diskUsage is unsupported on this platform.
func diskUsage(string) (free, total uint64, ok bool) { return 0, 0, false }
