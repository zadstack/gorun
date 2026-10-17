//go:build !unix

package kernel

import "os"

// Platforms without flock(2) get no data-directory lock: GoRun's supported
// deployments (Linux containers, macOS for development) are unix.
func tryLock(*os.File) (bool, error) { return false, nil }

func unlock(*os.File) error { return nil }
