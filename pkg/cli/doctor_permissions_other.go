//go:build !unix

package cli

import "os"

func doctorDirectoryWritable(_ string, info os.FileInfo) bool {
	return info.Mode().Perm()&0222 != 0
}
