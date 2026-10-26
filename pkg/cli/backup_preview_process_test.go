package cli

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestBackupPreviewProcessCleanup(t *testing.T) {
	if os.Getenv("GORUN_PREVIEW_TEST_HELPER") == "1" {
		os.Exit(Main([]string{"backup-preview", "--input", os.Getenv("GORUN_PREVIEW_TEST_INPUT"), "--no-browser"}))
	}
	if runtime.GOOS == "windows" {
		t.Skip("child process interrupt is not supported on Windows")
	}
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	res, err := gpbackup.CreateWith(cfg.DataDir, cfg.DBPath(), time.Now(), gpbackup.Options{SecretKey: cfg.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(res.DB)
	if err != nil {
		t.Fatal(err)
	}
	tempBase := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestBackupPreviewProcessCleanup$")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GORUN_") && !strings.HasPrefix(entry, "TMPDIR=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GORUN_PREVIEW_TEST_HELPER=1", "GORUN_PREVIEW_TEST_INPUT="+res.Manifest, "TMPDIR="+tempBase)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	lines := make(chan string)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-done:
				return
			}
		}
	}()
	var previewURL string
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for previewURL == "" {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("preview exited during startup")
			}
			if strings.HasPrefix(line, "Backup preview: ") {
				previewURL = strings.TrimPrefix(line, "Backup preview: ")
			}
		case <-timer.C:
			t.Fatal("preview startup timed out")
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(previewURL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("preview page: %d", response.StatusCode)
	}
	entries, err := filepath.Glob(filepath.Join(tempBase, "gorun-backup-preview-*"))
	if err != nil || len(entries) != 1 {
		t.Fatal("preview sandbox not created")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			t.Fatal("unclean preview shutdown", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("preview shutdown timed out")
	}
	if _, err := os.Stat(entries[0]); !os.IsNotExist(err) {
		t.Fatal("preview left restored data after shutdown")
	}
	after, err := os.ReadFile(res.DB)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("preview process modified backup")
	}
}
