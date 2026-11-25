//go:build unix

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

func doctorDirectoryWritable(path string, info os.FileInfo) bool {
	return info.Mode().Perm()&0222 != 0 && unix.Access(path, unix.W_OK|unix.X_OK) == nil
}
