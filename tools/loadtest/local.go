package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	loadAdminEmail = "loadtest@gorun.local"
	loadAdminPass  = "loadtest-correct-horse-battery"
)

// LocalInstance is a short-lived gorun serve process for the harness.
type LocalInstance struct {
	Bin     string
	DataDir string
	BaseURL string
	Token   string
	Themes  string
	cmd     *exec.Cmd
	logFile *os.File
}

// StartLocal builds (optional) and boots gorun serve on a free port with a
// fresh data dir, admin user, and API key. Caller must Close.
func StartLocal(ctx context.Context, bin, themes, dataDir string) (*LocalInstance, error) {
	if themes == "" {
		themes = "themes"
	}
	absThemes, err := filepath.Abs(themes)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absThemes); err != nil {
		return nil, fmt.Errorf("themes dir %s: %w (run from repo root or pass -themes)", absThemes, err)
	}
	if dataDir == "" {
		dataDir, err = os.MkdirTemp("", "gorun-loadtest-*")
		if err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}

	if bin == "" {
		bin = filepath.Join("bin", "gorun")
	}
	if _, err := os.Stat(bin); err != nil {
		if err := buildBinary(ctx, bin); err != nil {
			return nil, err
		}
	}
	absBin, err := filepath.Abs(bin)
	if err != nil {
		return nil, err
	}

	if err := runCLI(ctx, absBin, absData, absThemes, "user", "create",
		"--email", loadAdminEmail, "--password", loadAdminPass, "--name", "Load Test", "--admin"); err != nil {
		return nil, fmt.Errorf("user create: %w", err)
	}
	token, err := createAPIKey(ctx, absBin, absData, absThemes)
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	logPath := filepath.Join(absData, "loadtest-server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, absBin, "serve")
	cmd.Env = localEnv(absData, absThemes, base, fmt.Sprintf("127.0.0.1:%d", port))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("serve start: %w", err)
	}
	inst := &LocalInstance{Bin: absBin, DataDir: absData, BaseURL: base, Token: token, Themes: absThemes, cmd: cmd, logFile: logFile}
	if err := waitHealthy(ctx, base+"/healthz", cmd, 30*time.Second); err != nil {
		_ = inst.Close()
		return nil, fmt.Errorf("server unhealthy: %w (see %s)", err, logPath)
	}
	return inst, nil
}

// Close stops the server and closes the log file. Data dir is left for the caller.
func (l *LocalInstance) Close() error {
	if l == nil {
		return nil
	}
	if l.cmd != nil && l.cmd.Process != nil {
		_ = l.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- l.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = l.cmd.Process.Kill()
			<-done
		}
	}
	if l.logFile != nil {
		_ = l.logFile.Close()
	}
	return nil
}

// PID returns the server process id, or 0.
func (l *LocalInstance) PID() int {
	if l == nil || l.cmd == nil || l.cmd.Process == nil {
		return 0
	}
	return l.cmd.Process.Pid
}

func buildBinary(ctx context.Context, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", out, "./cmd/gorun")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCLI(ctx context.Context, bin, data, themes string, args ...string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = localEnv(data, themes, "http://127.0.0.1:9", "127.0.0.1:9")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncate(string(out), 400))
	}
	return nil
}

func createAPIKey(ctx context.Context, bin, data, themes string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "key", "create",
		"--name", "loadtest", "--user", loadAdminEmail, "--scopes", "*", "--rate", "10000")
	cmd.Env = localEnv(data, themes, "http://127.0.0.1:9", "127.0.0.1:9")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("key create: %w: %s", err, truncate(string(out), 400))
	}
	i := bytes.IndexByte(out, '{')
	if i < 0 {
		return "", fmt.Errorf("key create: no JSON in output: %s", truncate(string(out), 200))
	}
	var parsed struct {
		Token string `json:"token"`
	}
	dec := json.NewDecoder(bytes.NewReader(out[i:]))
	if err := dec.Decode(&parsed); err != nil || parsed.Token == "" {
		return "", fmt.Errorf("key create: parse token: %w (out=%s)", err, truncate(string(out), 400))
	}
	return parsed.Token, nil
}

func localEnv(data, themes, baseURL, addr string) []string {
	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"GORUN_ENV=dev",
		"GORUN_DATA=" + data,
		"GORUN_THEMES=" + themes,
		"GORUN_BASE_URL=" + baseURL,
		"GORUN_ADDR=" + addr,
	}
	// Strip inherited GORUN_*/STRIPE_* like e2e does.
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "GORUN_"), strings.HasPrefix(e, "STRIPE_"):
			continue
		case strings.HasPrefix(e, "PATH="), strings.HasPrefix(e, "HOME="), strings.HasPrefix(e, "TMPDIR="):
			continue
		default:
			base = append(base, e)
		}
	}
	return base
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func waitHealthy(ctx context.Context, url string, cmd *exec.Cmd, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("process exited early")
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
