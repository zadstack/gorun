//go:build unix

package cli

import (
	"fmt"
	"os"
	"syscall"
)

// Lock the directory inode, which survives the executable's replacement and
// also serializes operations on the same binary from different data directories.
func lockUpgradeDirectory(dir string) (*os.File, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock binary directory (another update may be running): %w", err)
	}
	return f, nil
}
