package cli

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A real process exercises signal shutdown, installed-binary theme discovery,
// actual port reporting, the login form and one-time credential output.
func TestStartProcess(t *testing.T) {
	if os.Getenv("GORUN_START_TEST_HELPER") == "1" {
		os.Unsetenv("GORUN_THEMES") // TestMain sets a repository fixture; exercise the standalone fallback.
		os.Exit(Main([]string{"start", "--data", "data", "--addr", ":0", "--local-only", "--no-browser"}))
	}
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for child processes on Windows")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var password string
	for attempt := 0; attempt < 2; attempt++ {
		func() {
			cmd := exec.Command(exe, "-test.run=^TestStartProcess$")
			cmd.Dir = root
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "GORUN_") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = append(cmd.Env, "GORUN_START_TEST_HELPER=1")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			// Suppress noisy startup logs and never include generated credentials in test output.
			cmd.Stderr = io.Discard
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill() }()
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
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			var local string
		reading:
			for {
				select {
				case line, ok := <-lines:
					if !ok {
						t.Fatal("start exited before credentials/status output")
					}
					if strings.HasPrefix(line, "Local: ") {
						local = strings.TrimPrefix(line, "Local: ")
					}
					if strings.HasPrefix(line, "LAN:") {
						t.Fatal("local-only advertised a LAN listener")
					}
					if strings.HasPrefix(line, "Password (shown once; save it now): ") {
						if attempt != 0 {
							t.Fatal("restart printed a new password")
						}
						password = strings.TrimPrefix(line, "Password (shown once; save it now): ")
						break reading
					}
					if strings.HasPrefix(line, "Existing accounts preserved;") {
						if attempt == 0 {
							t.Fatal("fresh start did not create an account")
						}
						break reading
					}
				case <-timer.C:
					t.Fatal("start timed out")
				}
			}
			if local == "" || len(password) != 43 {
				t.Fatal("missing local URL or invalid generated password length")
			}
			if _, err := os.Stat(filepath.Join(root, "data", "themes", "default", "theme.json")); err != nil {
				t.Fatal("bundled theme missing", err)
			}
			// A listener bound only to loopback must not accept a connection to a LAN address.
			u, _ := url.Parse(local)
			if u.Hostname() != "127.0.0.1" {
				t.Fatal("IPv4 listener must advertise its IPv4 loopback address")
			}
			ips, err := lanIPs()
			if err != nil {
				t.Fatal(err)
			}
			for _, ip := range ips {
				conn, err := net.DialTimeout("tcp4", net.JoinHostPort(ip, u.Port()), time.Second)
				if err == nil {
					conn.Close()
					t.Fatal("local-only listener accepted a LAN connection")
				}
			}
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
			response, err := client.Get(local)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal("installed site did not serve its advertised local URL")
			}
			response, err = client.Get(local + "/admin/login")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			csrf := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindSubmatch(body)
			if len(csrf) != 2 || !strings.Contains(string(body), `type="text"`) {
				t.Fatal("login form does not accept a username")
			}
			response, err = client.PostForm(local+"/admin/login", url.Values{"email": {"admin"}, "password": {password}, "_csrf": {string(csrf[1])}})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK || response.Request.URL.Path == "/admin/login" {
				t.Fatal("generated credentials did not sign in")
			}
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			select {
			case err := <-waited:
				if err != nil {
					t.Fatal("unclean shutdown", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("shutdown timed out")
			}
		}()
	}
}
