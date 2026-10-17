package kernel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DataLockFile is the lock file inside the data directory. The process that
// runs job workers (gorun serve, gorun mcp --jobs, reindex without
// --no-jobs) holds an exclusive OS lock on it for its whole lifetime.
const DataLockFile = "gorun.lock"

// ErrDataDirLocked is returned (wrapped) when another process holds the
// data-directory lock.
var ErrDataDirLocked = errors.New("data directory is in use by another GoRun process")

// DataLock is a held data-directory lock. The OS releases it when the
// process exits, even after a crash, so a leftover gorun.lock file never
// blocks a restart.
type DataLock struct {
	f    *os.File
	path string
}

// LockDataDir takes the exclusive, non-blocking lock on <dir>/gorun.lock,
// creating dir and the file when needed, and records this process (pid,
// host, start time) in the file for the error message another process sees.
// It fails with ErrDataDirLocked when the lock is already held.
func LockDataDir(dir string) (*DataLock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("kernel: data directory lock: %w", err)
	}
	path := filepath.Join(dir, DataLockFile)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("kernel: data directory lock: %w", err)
	}
	held, err := tryLock(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("kernel: data directory lock %s: %w", path, err)
	}
	if held {
		holder, _ := os.ReadFile(path)
		f.Close()
		who := strings.TrimSpace(string(holder))
		if who == "" {
			who = "unknown process"
		}
		return nil, fmt.Errorf("kernel: %w: %s is locked by %s. Only one process may run job workers for a data "+
			"directory: stop the other `gorun serve` (or `gorun mcp --jobs`) first, or use a command without "+
			"job workers (`gorun mcp` without --jobs, `gorun reindex --no-jobs`)", ErrDataDirLocked, path, who)
	}
	host, _ := os.Hostname()
	info := fmt.Sprintf("pid %d on %s since %s (%s)\n", os.Getpid(), host,
		time.Now().UTC().Format(time.RFC3339), strings.Join(os.Args, " "))
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(info), 0)
	}
	return &DataLock{f: f, path: path}, nil
}

// Path is the lock file.
func (l *DataLock) Path() string { return l.path }

// Release clears the holder record and releases the lock. The file is kept:
// removing it could let two processes lock different inodes. Safe to call
// more than once and on a nil lock.
func (l *DataLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	_ = f.Truncate(0)
	errUnlock := unlock(f)
	errClose := f.Close()
	return errors.Join(errUnlock, errClose)
}
