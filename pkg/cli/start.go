package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/pattern"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/patterns"
	"github.com/zadstack/gorun/themes"
)

// startPrivilegedPrefixes are paths that must stay on loopback during
// `gorun start` LAN preview (administration, accounts, APIs, private intake).
var startPrivilegedPrefixes = []string{
	"/admin", "/api", "/mcp", "/account", "/auth", "/webhooks", "/metrics",
	"/cart", "/checkout", "/reports", "/forms", "/start",
}

// startPathPrivileged reports whether clean (already path.Clean'd) is a
// privileged URL, including locale-prefixed public routes such as
// /en/account or /ar/reports/incident.
func startPathPrivileged(clean string) bool {
	_, rest := i18n.SplitLanguagePath(clean)
	for _, p := range []string{clean, rest} {
		for _, prefix := range startPrivilegedPrefixes {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				return true
			}
		}
	}
	return false
}

func start(args []string, out io.Writer) error {
	localOnly, args := boolFlag(args, "local-only")
	noBrowser, args := boolFlag(args, "no-browser")
	cfg, err := config.Load(args)
	if err != nil {
		return err
	}
	if cfg.Env != "dev" {
		return errors.New("start is for local development; use serve for production")
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("start address: %w", err)
	}
	host := "0.0.0.0"
	if localOnly {
		host = "127.0.0.1"
	}
	// Always bind ourselves: --local-only cannot be weakened by configuration.
	listener, err := net.Listen("tcp4", net.JoinHostPort(host, port))
	if err != nil {
		return startListenError(err, port)
	}
	defer listener.Close()
	port = fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	cfg.Addr = listener.Addr().String()
	// Match the tcp4 listener exactly; localhost can resolve to an unrelated
	// IPv6 listener/proxy on the same port.
	cfg.BaseURL = "http://127.0.0.1:" + port
	cfg.TrustedProxies = nil // Never trust a LAN peer's forwarded identity.
	if err := installStartTheme(cfg); err != nil {
		return err
	}
	if err := installBundledPatterns(cfg); err != nil {
		return err
	}
	var ips []string
	if !localOnly {
		ips, err = lanIPs()
		if err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := kernel.Boot(ctx, cfg, Modules(cfg))
	if err != nil {
		return startBootError(err)
	}
	defer app.Close()
	password, err := bootstrapStart(ctx, app)
	if err != nil {
		return err
	}
	handler := startGuard(app.Handler, port, ips)
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(listener) }()
	if _, err := fmt.Fprintf(out, "Local: %s\nAdmin (this computer only): %s/admin\n", cfg.BaseURL, cfg.BaseURL); err != nil {
		srv.Close()
		return err
	}
	if !localOnly {
		for _, ip := range ips {
			fmt.Fprintf(out, "LAN: http://%s (public preview only)\n", net.JoinHostPort(ip, port))
		}
		if len(ips) == 0 {
			fmt.Fprintln(out, "LAN: no private IPv4 interface found")
		}
		fmt.Fprintln(out, "LAN uses unencrypted HTTP; use a trusted network or --local-only. Admin, API, accounts, intake, forms, start and writes are restricted to localhost.")
	}
	if password != "" {
		if _, err := fmt.Fprintf(out, "Username: admin\nEmail: admin@localhost.invalid\nPassword (shown once; save it now): %s\nFirst sign-in: replace this password and choose your email, site name and language.\n", password); err != nil {
			srv.Close()
			return err
		}
	} else {
		fmt.Fprintln(out, "Existing accounts preserved; password is not displayed again.")
		fmt.Fprintln(out, startRecoveryHint(cfg.DataDir))
	}
	if !noBrowser {
		if err := openLocalBrowser(cfg.BaseURL + "/admin"); err != nil {
			fmt.Fprintf(out, "Open the local link in your browser (%v).\n", err)
		}
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// startListenError explains a port that is already taken: usually GoRun is
// already running (open it) or another program uses the port (pick another).
func startListenError(err error, port string) error {
	if !errors.Is(err, syscall.EADDRINUSE) {
		return err
	}
	next := "8081"
	if n, convErr := strconv.Atoi(port); convErr == nil && n > 0 && n < 65535 {
		next = strconv.Itoa(n + 1)
	}
	return fmt.Errorf("port %s is already in use. If GoRun is already running, open http://127.0.0.1:%s/admin in your browser. "+
		"Otherwise start on another port: gorun start --addr :%s (or --addr :0 to pick any free port)", port, port, next)
}

// startBootError adds the next step to a data directory held by another
// process: for start that is nearly always an earlier start of the same site.
func startBootError(err error) error {
	if !errors.Is(err, kernel.ErrDataDirLocked) {
		return err
	}
	return fmt.Errorf("%w\nGoRun is already running for this site. Open the Admin link it printed, "+
		"or stop it with Ctrl+C in its terminal and run gorun start again", err)
}

// startRecoveryHint tells a returning user how to get back in when the
// one-time password was not saved. It names the data directory in use.
func startRecoveryHint(dataDir string) string {
	return fmt.Sprintf("Lost your password? Reset it with: gorun user password --data %q --email YOUR-EMAIL --password-stdin", dataDir)
}

// installStartTheme installs the bundled default theme when it is missing.
func installStartTheme(cfg config.Config) error {
	themeRoot := theme.ThemesDir(cfg)
	if _, err := os.Stat(themeRoot); os.IsNotExist(err) {
		if os.Getenv(theme.EnvThemesDir) != "" {
			return fmt.Errorf("configured theme directory does not exist: %w", err)
		}
		themeRoot = filepath.Join(cfg.DataDir, "themes")
	}
	return themes.InstallDefault(themeRoot)
}

// installBundledPatterns installs the embedded patterns unless a patterns
// directory other than the bundled copy is in use.
func installBundledPatterns(cfg config.Config) error {
	bundled := filepath.Join(filepath.Clean(cfg.DataDir), "bundled-patterns")
	root := pattern.PatternsDir(cfg)
	if st, err := os.Stat(root); err == nil && st.IsDir() && filepath.Clean(root) != bundled {
		return nil
	}
	return patterns.Install(bundled)
}

// writeTrialBanner prints how to reach a GORUN_TRIAL site. Unlike the local
// preview of start, its admin answers on every address the container publishes.
func writeTrialBanner(out io.Writer, cfg config.Config, password string) error {
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("trial address: %w", err)
	}
	banner := fmt.Sprintf("GoRun trial mode: not for production.\nOpen: http://localhost:%s/admin (on a server: http://SERVER-IP:%s/admin)\nThe admin is reachable over unencrypted HTTP on every published address. Use a trusted network. To keep this site, see docs/local-start.md.\n", port, port)
	if password != "" {
		banner += fmt.Sprintf("Username: admin\nEmail: admin@localhost.invalid\nPassword (shown once; save it now): %s\nFirst sign-in: replace this password and choose your email, site name and language.\n", password)
	} else {
		banner += "Existing accounts preserved; password is not displayed again.\n"
	}
	_, err = io.WriteString(out, banner)
	return err
}

func bootstrapStart(ctx context.Context, app *kernel.App) (string, error) {
	users, ok := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if !ok {
		return "", notInstalled("start")
	}
	n, err := users.Count(ctx)
	if err != nil || n != 0 {
		return "", err
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	password := base64.RawURLEncoding.EncodeToString(b[:])
	_, err = auth.BootstrapAdmin(ctx, app.Deps.DB, app.Deps.Audit, password)
	if errors.Is(err, auth.ErrSetupDone) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return password, nil
}

func lanIPs() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var ips []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.To4() != nil && ip.IsPrivate() {
				ips = append(ips, ip.String())
			}
		}
	}
	return ips, nil
}

// startGuard keeps privileged HTTP traffic on loopback, even with forged Host,
// forwarding headers, path aliases, or cookies copied from a local session.
func startGuard(next http.Handler, port string, ips []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _, _ := net.SplitHostPort(r.RemoteAddr)
		peerIP := net.ParseIP(peer)
		local := peerIP.IsLoopback()
		if !local && !peerIP.IsPrivate() {
			http.Error(w, "LAN preview requires a private network peer", http.StatusForbidden)
			return
		}
		host, p, err := net.SplitHostPort(r.Host)
		localHost := host == "localhost" || host == "127.0.0.1"
		allowed := local && localHost
		for _, ip := range ips {
			allowed = allowed || host == ip
		}
		if err != nil || p != port || !allowed {
			http.Error(w, "Invalid local site host", http.StatusForbidden)
			return
		}
		if !local || !localHost {
			clean := strings.ToLower(path.Clean("/" + strings.ReplaceAll(r.URL.Path, "\\", "/")))
			if startPathPrivileged(clean) {
				http.Error(w, "Use localhost for administration and accounts", http.StatusForbidden)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				http.Error(w, "LAN preview is read-only", http.StatusForbidden)
				return
			}
			r = r.Clone(r.Context())
			r.Header.Del("Cookie")
			r.Header.Del("Authorization")
		}
		next.ServeHTTP(w, r)
	})
}

func openLocalBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// Reap the launcher without delaying startup on a browser process.
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
