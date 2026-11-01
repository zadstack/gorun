package cli

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
