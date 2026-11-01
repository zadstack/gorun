package cli

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/pattern"
)

// A real process with the image's baked environment: the banner shows the
// password once, and the admin answers on any host, such as the server's IP.
func TestServeTrialProcess(t *testing.T) {
	if os.Getenv("GORUN_TRIAL_TEST_HELPER") == "1" {
		os.Unsetenv("GORUN_THEMES") // TestMain sets a repository fixture; exercise the standalone fallback.
		os.Exit(Main([]string{"serve"}))
	}
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for child processes on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	root := t.TempDir()
	for attempt := 0; attempt < 2; attempt++ {
		func() {
			cmd := exec.Command(exe, "-test.run=^TestServeTrialProcess$")
			cmd.Dir = root
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "GORUN_") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, "GORUN_TRIAL_TEST_HELPER=1", "GORUN_TRIAL=1", "GORUN_ENV=prod", "GORUN_DATA=data", "GORUN_ADDR=:"+strconv.Itoa(port))
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Process.Signal(os.Interrupt)
				_, _ = io.Copy(io.Discard, stdout)
				_ = cmd.Wait()
			}()
			lines := make(chan string, 16)
			go func() {
				defer close(lines)
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
			}()
			password, warned := "", false
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
		reading:
			for {
				select {
				case line, ok := <-lines:
					if !ok {
						t.Fatal("serve exited before the trial banner")
					}
					warned = warned || strings.Contains(line, "not for production")
					if strings.HasPrefix(line, "Password (shown once; save it now): ") {
						password = strings.TrimPrefix(line, "Password (shown once; save it now): ")
						break reading
					}
					if strings.HasPrefix(line, "Existing accounts preserved;") {
						break reading
					}
				case <-timer.C:
					t.Fatal("trial banner timed out")
				}
			}
			go func() {
				for range lines { // Drain so the child never blocks on stdout.
				}
			}()
			if !warned {
				t.Fatal("trial banner must warn that this is not for production")
			}
			if attempt == 0 && len(password) != 43 {
				t.Fatal("fresh trial must print a generated password")
			}
			if attempt == 1 && password != "" {
				t.Fatal("restart printed a new password")
			}
			req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/admin/login", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "203.0.113.10:8080"
			client := &http.Client{Timeout: 5 * time.Second}
			var resp *http.Response
			for i := 0; i < 50; i++ {
				if resp, err = client.Do(req); err == nil {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("admin login through a server IP: %d", resp.StatusCode)
			}
		}()
	}
}

func TestApplyFirstRunToken(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_BASE_URL", "https://example.com")
	t.Setenv("GORUN_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	app, err := kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var out strings.Builder
	if err := applyFirstRunToken(ctx, app, &out); err != nil {
		t.Fatal(err)
	}
	token := app.Deps.Config.SetupToken
	if len(token) != 48 || !strings.Contains(out.String(), token) || !strings.Contains(out.String(), "https://example.com/admin/setup") {
		t.Fatalf("no usable token (length %d)", len(token))
	}
	// A configured token is never replaced or printed.
	out.Reset()
	if err := applyFirstRunToken(ctx, app, &out); err != nil {
		t.Fatal(err)
	}
	if app.Deps.Config.SetupToken != token || out.Len() != 0 {
		t.Fatal("a set token must stay and print nothing")
	}
	// An account closes setup, so a later boot generates nothing.
	app.Deps.Config.SetupToken = ""
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := auth.BootstrapAdmin(ctx, app.Deps.DB, app.Deps.Audit, strings.Repeat("Aa1!", 8)); err != nil {
		t.Fatal(err)
	}
	if n, err := users.Count(ctx); err != nil || n != 1 {
		t.Fatalf("users %d: %v", n, err)
	}
	if err := applyFirstRunToken(ctx, app, &out); err != nil {
		t.Fatal(err)
	}
	if app.Deps.Config.SetupToken != "" || out.Len() != 0 {
		t.Fatal("an existing account must not get a setup token")
	}
}

// Development needs no token, so none is invented there.
func TestApplyFirstRunTokenSkipsDev(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	app, err := kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	var out strings.Builder
	if err := applyFirstRunToken(ctx, app, &out); err != nil {
		t.Fatal(err)
	}
	if app.Deps.Config.SetupToken != "" || out.Len() != 0 {
		t.Fatal("dev must not get a generated token")
	}
}

func TestSecretKeyPrintsTrialKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORUN_DATA", dir)
	t.Setenv("GORUN_TRIAL", "1")
	cfg, err := config.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := secretKey(nil, &out); err != nil {
		t.Fatal(err)
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.String()))
	if err != nil || string(got) != string(cfg.SecretKey) {
		t.Fatalf("secret-key must print the site key: %v", err)
	}
	// The printed value is what production accepts.
	t.Setenv("GORUN_TRIAL", "")
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_BASE_URL", "https://example.com")
	t.Setenv("GORUN_SECRET_KEY", strings.TrimSpace(out.String()))
	prod, err := config.Load(nil)
	if err != nil || string(prod.SecretKey) != string(cfg.SecretKey) {
		t.Fatalf("production must accept the printed key: %v", err)
	}
}

// With no patterns directory beside the executable, the embedded ones are offered.
func TestBundledPatternsWithoutDirectory(t *testing.T) {
	themesDir, err := filepath.Abs("../../themes")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GORUN_THEMES", themesDir)
	data := t.TempDir()
	t.Chdir(t.TempDir())
	cfg, err := config.Load([]string{"--data", data})
	if err != nil {
		t.Fatal(err)
	}
	if err := installBundledPatterns(cfg); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	app, err := kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	svc, ok := kernel.Get[pattern.Service](app.Deps, kernel.ServicePattern)
	if !ok {
		t.Fatal("pattern service missing")
	}
	list, err := svc.List(ctx)
	if err != nil || len(list) != 4 {
		t.Fatalf("want the 4 bundled patterns, got %d: %v", len(list), err)
	}
}
