package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"

	gpbackup "github.com/zadstack/gorun/internal/backup"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	settingsstore "github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/theme"
	"github.com/zadstack/gorun/themes"
)

type backupPreview struct {
	App                   *kernel.App
	Listener              net.Listener
	Handler               http.Handler
	URL, DataDir, tempDir string
}

func (p *backupPreview) Close() error {
	if p.Listener != nil {
		_ = p.Listener.Close()
	}
	if p.App != nil {
		p.App.Close()
	}
	if p.tempDir != "" {
		return os.RemoveAll(p.tempDir)
	}
	return nil
}

func backupPreviewCmd(args []string, out io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("backup-preview", flag.ContinueOnError)
	input := fs.String("input", "", "local backup manifest (required)")
	addr := fs.String("addr", "127.0.0.1:0", "loopback listen address; default chooses a free port")
	noBrowser := fs.Bool("no-browser", false, "print the local preview URL without opening a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" || fs.NArg() != 0 {
		return errors.New("usage: gorun backup-preview --input <stamp>.manifest.json [--addr 127.0.0.1:0] [--no-browser]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	preview, err := prepareBackupPreview(ctx, *input, *addr)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, preview.Close()) }()
	server := &http.Server{Handler: preview.Handler, ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- server.Serve(preview.Listener) }()
	if _, err := fmt.Fprintf(out, "Backup preview: %s\nRead-only public pages; scripts, forms, external resources, integrations, accounts and workers are disabled.\nStop with Ctrl+C to remove the disposable copy. Live data and backup files are unchanged.\nThis preview is not proof of complete recovery: check original keys, matching themes and external storage separately.\n", preview.URL); err != nil {
		_ = server.Close()
		return err
	}
	if !*noBrowser {
		if err := openLocalBrowser(preview.URL); err != nil {
			fmt.Fprintln(out, "Open the printed preview URL in your browser.")
		}
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func prepareBackupPreview(ctx context.Context, input, addr string) (result *backupPreview, resultErr error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, errors.New("backup preview requires a loopback host:port")
	}
	if host == "localhost" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("backup preview may listen only on loopback")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	p := &backupPreview{Listener: listener}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, p.Close())
		}
	}()
	p.URL = "http://" + listener.Addr().String()
	temp, err := os.MkdirTemp("", "gorun-backup-preview-")
	if err != nil {
		return nil, err
	}
	p.tempDir = temp
	p.DataDir = filepath.Join(temp, "site")
	if _, err := gpbackup.RestoreWith(input, p.DataDir, secretNearManifest(input)); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// Never load archived TOML, the original secret, environment credentials or
	// downstream Modules options. Only the disposable copy is migrated/sanitized.
	cfg := config.Config{Env: "dev", DataDir: p.DataDir, Addr: listener.Addr().String(), BaseURL: p.URL, SecretKey: key, Theme: "default"}
	if err := sanitizePreviewDB(ctx, cfg); err != nil {
		return nil, err
	}
	for _, name := range []string{"secret.key", "gorun.toml"} {
		if err := os.Remove(filepath.Join(p.DataDir, name)); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	root := theme.ThemesDir(cfg)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		root = filepath.Join(p.DataDir, "themes")
		if err := themes.InstallDefault(root); err != nil {
			return nil, err
		}
	}
	// A fixed allowlist deliberately omits commerce, remote services, extensions,
	// webhooks, API/admin, MCP and all downstream module code.
	safe := map[string]bool{"settings": true, "auth": true, "content": true, "taxonomy": true, "media": true, "menus": true, "redirects": true, "seo": true, "theme": true, "builder": true, "site": true}
	var modules []kernel.Module
	for _, module := range CoreModules() {
		if safe[module.Name()] {
			if module.Name() == "theme" {
				module = theme.New(theme.WithThemesDir(root))
			}
			modules = append(modules, module)
		}
	}
	app, err := kernel.BootWithOptions(ctx, cfg, modules, kernel.BootOptions{StartJobs: false})
	if err != nil {
		return nil, err
	}
	p.App = app
	_, actualPort, _ := net.SplitHostPort(listener.Addr().String())
	p.Handler = backupPreviewGuard(app.Handler, actualPort)
	return p, nil
}

func sanitizePreviewDB(ctx context.Context, cfg config.Config) error {
	d, err := db.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer d.Close()
	return db.InTx(ctx, d, func(tx *sql.Tx) error {
		// Keep public presentation settings; exclude mail, destinations, backup
		// scheduling, tokens, integrations and extension state before any module boots.
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key NOT LIKE 'theme.%' AND key NOT IN ('site.title','site.tagline','site.locale','site.theme','site.front_page','site.logo','site.timezone','site.posts_per_page')`)
		if err != nil {
			return err
		}
		store := settingsstore.New(d)
		if err := store.SetTx(ctx, tx, "site.base_url", cfg.BaseURL); err != nil {
			return err
		}
		if err := store.SetTx(ctx, tx, "backup.schedule_hours", 0); err != nil {
			return err
		}
		// Legacy backups can predate individual auth tables.
		for _, table := range []string{"sessions", "api_keys", "magic_links", "jobs"} {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

const previewResponseLimit = 16 << 20
const previewCSP = "default-src 'self'; script-src 'none'; connect-src 'none'; form-action 'none'; frame-src 'none'; object-src 'none'; img-src 'self' data:; font-src 'self' data:; style-src 'self' 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; sandbox allow-same-origin"

type previewResponse struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	tooLarge bool
}

func (r *previewResponse) Header() http.Header { return r.header }
func (r *previewResponse) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *previewResponse) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	if r.body.Len()+len(b) > previewResponseLimit {
		r.tooLarge = true
		return 0, errors.New("preview response too large")
	}
	return r.body.Write(b)
}

func backupPreviewGuard(next http.Handler, port string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _, _ := net.SplitHostPort(r.RemoteAddr)
		host, p, err := net.SplitHostPort(r.Host)
		hostIP := net.ParseIP(host)
		if !net.ParseIP(peer).IsLoopback() || err != nil || p != port || !(host == "localhost" || hostIP != nil && hostIP.IsLoopback()) {
			http.Error(w, "Local preview only", 403)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Backup preview is read-only", 403)
			return
		}
		clean := strings.ToLower(path.Clean("/" + strings.ReplaceAll(r.URL.Path, "\\", "/")))
		for _, prefix := range []string{"/admin", "/api", "/mcp", "/auth", "/account", "/cart", "/checkout", "/webhooks", "/metrics", "/reports"} {
			if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
				http.Error(w, "Unavailable in backup preview", 403)
				return
			}
		}
		r = r.Clone(r.Context())
		r.Header.Del("Cookie")
		r.Header.Del("Authorization")
		r.Header.Del("Accept-Encoding")
		r.Header.Del("If-None-Match")
		r.Header.Del("If-Modified-Since")
		r.Header.Del("If-Range")
		r.Header.Del("Range")
		capture := &previewResponse{header: make(http.Header)}
		next.ServeHTTP(capture, r)
		if capture.tooLarge {
			http.Error(w, "Preview resource exceeds the 16 MiB limit", 413)
			return
		}
		if location := capture.header.Get("Location"); location != "" {
			if _, ok := previewURL(location, r.Host, false); !ok {
				http.Error(w, "External navigation is disabled in backup preview", 403)
				return
			}
		}
		for name, values := range capture.header {
			if strings.EqualFold(name, "Set-Cookie") || strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Content-Encoding") || strings.EqualFold(name, "Refresh") || strings.EqualFold(name, "Link") {
				continue
			}
			w.Header()[name] = values
		}
		w.Header().Set("Content-Security-Policy", previewCSP)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		body := capture.body.Bytes()
		contentType := capture.header.Get("Content-Type")
		if contentType == "" {
			contentType = http.DetectContentType(body)
			w.Header().Set("Content-Type", contentType)
		}
		if strings.Contains(strings.ToLower(contentType), "text/html") || strings.Contains(strings.ToLower(contentType), "application/xhtml+xml") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			body = previewHTML(body, r.Host)
		}
		status := capture.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		if r.Method != "HEAD" {
			_, _ = w.Write(body)
		}
	})
}

func previewURL(raw, host string, allowData bool) (string, bool) {
	if strings.ContainsAny(raw, "\\\r\n\t") {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil {
		return "", false
	}
	if u.Scheme == "data" {
		return raw, allowData
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	if u.Host != "" && u.Host != host {
		return "", false
	}
	if u.Scheme != "" && u.Host == "" {
		return "", false
	}
	return raw, true
}

func previewHTML(body []byte, host string) []byte {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	var out bytes.Buffer
	dropScript := false
	inStyle := false
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		if kind == html.TextToken && inStyle {
			out.WriteString(token.Data)
			continue
		}
		if kind == html.StartTagToken || kind == html.SelfClosingTagToken {
			if token.Data == "style" {
				inStyle = true
			}
			if token.Data == "script" {
				dropScript = true
				continue
			}
			if token.Data == "base" || token.Data == "iframe" || token.Data == "object" || token.Data == "embed" {
				continue
			}
			if token.Data == "meta" {
				refresh := false
				for _, a := range token.Attr {
					if a.Key == "http-equiv" && strings.EqualFold(strings.TrimSpace(a.Val), "refresh") {
						refresh = true
					}
				}
				if refresh {
					continue
				}
			}
			if token.Data == "form" {
				token.Data = "div"
			}
			attrs := token.Attr[:0]
			for _, a := range token.Attr {
				if strings.HasPrefix(a.Key, "on") || a.Key == "ping" || a.Key == "srcset" || a.Key == "srcdoc" || a.Key == "target" || a.Key == "action" || a.Key == "formaction" {
					continue
				}
				if a.Key == "src" || a.Key == "href" || a.Key == "poster" || a.Key == "data" {
					if _, ok := previewURL(a.Val, host, a.Key == "src" && token.Data == "img"); !ok {
						continue
					}
				}
				attrs = append(attrs, a)
			}
			token.Attr = attrs
			if token.Data == "input" || token.Data == "button" || token.Data == "select" || token.Data == "textarea" {
				token.Attr = append(token.Attr, html.Attribute{Key: "disabled", Val: "disabled"})
			}
		}
		if kind == html.EndTagToken {
			if token.Data == "style" {
				inStyle = false
			}
			if token.Data == "script" {
				dropScript = false
				continue
			}
			if token.Data == "iframe" || token.Data == "object" || token.Data == "embed" {
				continue
			}
			if token.Data == "form" {
				token.Data = "div"
			}
		}
		if dropScript {
			continue
		}
		out.WriteString(token.String())
		if kind == html.StartTagToken && token.Data == "body" {
			out.WriteString(`<div role="status" style="padding:12px;background:#fff3cd;color:#332701;text-align:center;font:16px sans-serif">Backup preview — read-only / معاينة نسخة احتياطية — للقراءة فقط</div>`)
		}
	}
	return out.Bytes()
}
