//go:build unix

package kernel

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// tryLock takes a non-blocking exclusive flock(2). held reports that another
// open file description (another process, or another open in this one)
// already has it.
func tryLock(f *os.File) (held bool, err error) {
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	return false, err
}

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// ProbeDataDirLock checks the existing lock without creating or changing its file.
func ProbeDataDirLock(dir string) (held bool, err error) {
	f, err := os.OpenFile(filepath.Join(dir, DataLockFile), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("gorun.lock is not a regular file")
	}
	return tryLock(f)
}
