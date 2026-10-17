package kernel_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

const lockHelperEnv = "GORUN_TEST_DATALOCK_HELPER_DIR"

func skipWithoutFlock(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" || runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("data-directory lock needs flock(2)")
	}
}

func TestDataDirLockIsExclusiveAndReleasable(t *testing.T) {
	skipWithoutFlock(t)
	dir := filepath.Join(t.TempDir(), "data") // created on demand
	first, err := kernel.LockDataDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := os.ReadFile(first.Path())
	if err != nil || !strings.Contains(string(holder), fmt.Sprintf("pid %d ", os.Getpid())) {
		t.Fatalf("lock file should name the holder: %q %v", holder, err)
	}
	_, err = kernel.LockDataDir(dir)
	if !errors.Is(err, kernel.ErrDataDirLocked) {
		t.Fatalf("second lock must fail with ErrDataDirLocked, got %v", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("pid %d ", os.Getpid())) || !strings.Contains(err.Error(), "gorun mcp --jobs") {
		t.Fatalf("error should name the holder and how to proceed: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second release should be a no-op: %v", err)
	}
	again, err := kernel.LockDataDir(dir)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	again.Release()
	if _, err := os.Stat(filepath.Join(dir, kernel.DataLockFile)); err != nil {
		t.Fatalf("lock file should stay in place: %v", err)
	}
}

// TestDataLockHelperProcess is the child of
// TestDataDirLockHeldByAnotherProcess: it takes the lock, reports it on
// stdout and waits to be killed.
func TestDataLockHelperProcess(t *testing.T) {
	dir := os.Getenv(lockHelperEnv)
	if dir == "" {
		t.Skip("helper process only")
	}
	if _, err := kernel.LockDataDir(dir); err != nil {
		fmt.Println("LOCK-FAILED", err)
		os.Exit(3)
	}
	fmt.Println("LOCKED")
	time.Sleep(30 * time.Second)
	os.Exit(4)
}

func TestDataDirLockHeldByAnotherProcess(t *testing.T) {
	skipWithoutFlock(t)
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestDataLockHelperProcess$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "LOCKED" {
		t.Fatalf("helper did not take the lock: %q %v", line, err)
	}

	_, err = kernel.LockDataDir(dir)
	if !errors.Is(err, kernel.ErrDataDirLocked) || !strings.Contains(err.Error(), fmt.Sprintf("pid %d ", cmd.Process.Pid)) {
		t.Fatalf("lock held by pid %d must be refused naming it, got %v", cmd.Process.Pid, err)
	}
	cfg := testConfig(t)
	cfg.DataDir = dir
	if _, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()}); !errors.Is(err, kernel.ErrDataDirLocked) {
		t.Fatalf("Boot with job workers must refuse a locked data directory, got %v", err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatalf("a process without job workers needs no lock: %v", err)
	}
	app.Close()

	// A crash (SIGKILL) releases the lock: the OS drops it with the process.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	app, err = kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()})
	if err != nil {
		t.Fatalf("lock should be free after the holder died: %v", err)
	}
	app.Close()
}

func TestBootHoldsDataDirLockUntilClose(t *testing.T) {
	skipWithoutFlock(t)
	cfg := testConfig(t)
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()}); !errors.Is(err, kernel.ErrDataDirLocked) {
		t.Fatalf("second server on the same data directory must be refused, got %v", err)
	}
	if _, err := kernel.LockDataDir(cfg.DataDir); !errors.Is(err, kernel.ErrDataDirLocked) {
		t.Fatalf("running server must hold the lock, got %v", err)
	}
	app.Close()
	app2, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()})
	if err != nil {
		t.Fatalf("Close should release the lock: %v", err)
	}
	app2.Close()
}

func TestFailedBootReleasesDataDirLock(t *testing.T) {
	skipWithoutFlock(t)
	cfg := testConfig(t)
	// The database path is a directory, so opening it fails after the lock.
	if err := os.MkdirAll(cfg.DBPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()}); err == nil {
		t.Fatal("boot should fail")
	}
	l, err := kernel.LockDataDir(cfg.DataDir)
	if err != nil {
		t.Fatalf("failed boot kept the lock: %v", err)
	}
	l.Release()
}
