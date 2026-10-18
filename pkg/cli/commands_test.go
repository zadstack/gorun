package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"image"
	pngenc "image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

// seed boots a data dir, creates a post and an agent key, and closes it.
func seed(t *testing.T) (dir, key string) {
	t.Helper()
	kernel.LogOutput = io.Discard
	dir = t.TempDir()
	cfg, err := config.Load([]string{"--data", dir})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.Boot(context.Background(), cfg, Modules())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if _, err := cs.Create(context.Background(), content.Input{Type: "post", Title: "Backed up"}); err != nil {
		t.Fatal(err)
	}
	return dir, adminKey(t, app, auth.KeyKindAgent)
}

func TestBackupCommand(t *testing.T) {
	dir, _ := seed(t)
	for p, body := range map[string]string{"media/2026/10/a.png": "png", "overrides/example/page.html": "<p>o</p>", "extensions/demo/manifest.json": "{}", "form-uploads/01ABC": "%PDF-"} {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	os.Symlink("/etc/passwd", filepath.Join(dir, "media", "evil"))
	var out bytes.Buffer
	if err := run([]string{"backup", "--data", dir}); err != nil { // dispatch works
		t.Fatalf("run backup: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // a second backup gets a new timestamp
	if err := backup([]string{"--data", dir}, &out); err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`database: (\S+)\nfiles:    (\S+) \((\d+) files\)`).FindStringSubmatch(out.String())
	if m == nil || m[3] != "4" || !strings.HasPrefix(m[1], filepath.Join(dir, "backups")+string(os.PathSeparator)) {
		t.Fatalf("output %q", out.String())
	}
	bdb, err := db.Open(m[1])
	if err != nil {
		t.Fatal(err)
	}
	defer bdb.Close()
	var n int
	if err := bdb.QueryRow(`SELECT count(*) FROM content WHERE title = 'Backed up'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("database copy: %d %v", n, err)
	}
	f, _ := os.Open(m[2])
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			names = append(names, h.Name)
		}
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "extensions/demo/manifest.json,form-uploads/01ABC,media/2026/10/a.png,overrides/example/page.html" {
		t.Fatalf("archive (symlinks and secrets must be skipped): %v", names)
	}
	if !strings.Contains(out.String(), "secrets:  not archived") {
		t.Fatalf("output must say the secrets were left out: %q", out.String())
	}
	out.Reset()
	time.Sleep(1100 * time.Millisecond)
	if err := backup([]string{"--data", dir, "--include-secrets"}, &out); err != nil {
		t.Fatal(err)
	}
	m = regexp.MustCompile(`files:    (\S+) \((\d+) files\)`).FindStringSubmatch(out.String())
	if m == nil || m[2] != "5" || !strings.Contains(out.String(), "plain text") {
		t.Fatalf("--include-secrets output %q", out.String())
	}
}

func TestMCPCommandServesStdio(t *testing.T) {
	dir, key := seed(t)
	cr, sw := io.Pipe() // server → client
	sr, cw := io.Pipe() // client → server
	done := make(chan error, 1)
	var logs syncBuffer
	kernel.LogOutput = &logs
	t.Cleanup(func() { kernel.LogOutput = io.Discard })
	go func() { done <- mcpStdio([]string{"--key", key, "--data", dir}, sr, sw) }()
	cl, err := sdk.NewClient(&sdk.Implementation{Name: "cli-test", Version: "v0"}, nil).
		Connect(context.Background(), &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cl.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Tools, func(tl *sdk.Tool) bool { return tl.Name == "list_content" }) {
		t.Fatalf("tools over stdio: %d", len(res.Tools))
	}
	cl.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mcp exit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mcp did not exit after the client closed")
	}

	// By default the stdio process runs no job workers.
	if l := logs.String(); !strings.Contains(l, "jobs: workers not started") || strings.Contains(l, "jobs: workers started") {
		t.Fatalf("default mcp must not start workers:\n%s", l)
	}
	logs.Reset()
	if err := mcpStdio([]string{"--jobs", "--key", key, "--data", dir}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}); err != nil {
		t.Fatalf("--jobs: %v", err)
	}
	if !strings.Contains(logs.String(), "jobs: workers started") {
		t.Fatalf("--jobs must start workers:\n%s", logs.String())
	}

	// GORUN_MCP_KEY is the fallback; no key at all is an error.
	t.Setenv("GORUN_MCP_KEY", "")
	if err := mcpStdio([]string{"--data", dir}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}); err == nil || !strings.Contains(err.Error(), "GORUN_MCP_KEY") {
		t.Fatalf("missing key: %v", err)
	}
	t.Setenv("GORUN_MCP_KEY", key)
	if err := mcpStdio([]string{"--no-jobs", "--data", dir}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}); err != nil {
		t.Fatalf("env key with an immediately closed stdin: %v", err)
	}
	if err := mcpStdio([]string{"--key", "gp_wrong", "--data", dir}, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}); err == nil {
		t.Fatal("an invalid key must fail")
	}
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
func (s *syncBuffer) Reset()         { s.mu.Lock(); defer s.mu.Unlock(); s.b.Reset() }

func TestFlagHelpers(t *testing.T) {
	on, rest := boolFlag([]string{"--data", "d", "--no-jobs"}, "no-jobs")
	if !on || strings.Join(rest, " ") != "--data d" {
		t.Fatalf("boolFlag: %v %v", on, rest)
	}
	v, rest, err := stringFlag([]string{"--key=gp_x", "--data", "d"}, "key")
	if err != nil || v != "gp_x" || strings.Join(rest, " ") != "--data d" {
		t.Fatalf("stringFlag =: %q %v %v", v, rest, err)
	}
	v, _, _ = stringFlag([]string{"--key", "gp_y"}, "key")
	if v != "gp_y" {
		t.Fatalf("stringFlag space: %q", v)
	}
	if _, _, err := stringFlag([]string{"--key"}, "key"); err == nil {
		t.Fatal("--key without a value must fail")
	}
}

func TestBackupRetainKeepsNewestPairs(t *testing.T) {
	dir, _ := seed(t)
	for i := 0; i < 3; i++ {
		time.Sleep(1100 * time.Millisecond)
		var out bytes.Buffer
		if err := backup([]string{"--data", dir, "--retain", "2"}, &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "retain:") {
			t.Fatalf("missing retain line: %q", out.String())
		}
	}
	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	dbs := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db") {
			dbs++
		}
	}
	if dbs != 2 {
		t.Fatalf("want 2 .db files after retain, got %d (%v)", dbs, entries)
	}
	if err := backup([]string{"--data", dir, "--retain", "0"}, &bytes.Buffer{}); err == nil {
		t.Fatal("--retain 0 must fail")
	}
}

func TestBackupRefusesMissingDatabaseAndCleansUpOnFailure(t *testing.T) {
	kernel.LogOutput = io.Discard
	// A mistyped data dir has no database: refuse, write nothing.
	empty := t.TempDir()
	var out bytes.Buffer
	err := backup([]string{"--data", empty}, &out)
	if err == nil || !strings.Contains(err.Error(), "no database") {
		t.Fatalf("missing database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(empty, "gorun.db")); err == nil {
		t.Fatal("backup must not create a database")
	}
	if _, err := os.Stat(filepath.Join(empty, "backups")); err == nil {
		t.Fatal("backup must not create a backups dir for a missing database")
	}

	// The tar step fails (unreadable media subtree): no half backup is left.
	dir, _ := seed(t)
	locked := filepath.Join(dir, "media", "locked")
	os.MkdirAll(locked, 0o755)
	os.WriteFile(filepath.Join(locked, "x.png"), []byte("x"), 0o644)
	os.Chmod(locked, 0o000)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if os.Geteuid() == 0 {
		t.Skip("root reads chmod-000 directories; the tar failure cannot be provoked")
	}
	if err := backup([]string{"--data", dir}, &out); err == nil {
		t.Fatal("unreadable media must fail the backup")
	}
	left, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(left) != 0 {
		t.Fatalf("failed backup left files: %v", left)
	}
}

// wxrFixture copies the migrator's sample WXR, pointing its media at a local
// server that serves a tiny PNG for every upload path.
func wxrFixture(t *testing.T) string {
	t.Helper()
	var png bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_ = pngenc.Encode(&png, img)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/wp-content/uploads/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png.Bytes())
	}))
	t.Cleanup(srv.Close)
	raw, err := os.ReadFile("../../internal/wpmigrate/testdata/wxr/sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.ReplaceAll(raw, []byte("https://example.org"), []byte(srv.URL))
	p := filepath.Join(t.TempDir(), "export.xml")
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestImportWXRCommand(t *testing.T) {
	kernel.LogOutput = io.Discard
	t.Setenv("GORUN_ALLOW_PRIVATE_NET", "true") // the fixture media server is on 127.0.0.1
	t.Setenv(theme.EnvThemesDir, "../../themes")
	dir, file := t.TempDir(), wxrFixture(t)
	migID := regexp.MustCompile(`(?m)^(?:preview|import): migration (\S+)`)

	var out bytes.Buffer
	if err := importWXR([]string{file, "--dry-run", "--source-name", "Sample site", "--data", dir}, &out); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out.String())
	}
	if !regexp.MustCompile(`(?m)^preview: migration \S+ \(dry run, nothing imported\)$`).MatchString(out.String()) || !strings.Contains(out.String(), "content") {
		t.Fatalf("dry-run output:\n%s", out.String())
	}
	previewID := migID.FindStringSubmatch(out.String())[1]

	out.Reset()
	if err := importWXR([]string{"--data", dir, file}, &out); err != nil { // flags before the file
		t.Fatalf("import: %v\n%s", err, out.String())
	}
	m := regexp.MustCompile(`(?m)^import: migration (\S+) status (validated|completed)$`).FindStringSubmatch(out.String())
	if m == nil || !strings.Contains(out.String(), "/admin/migrations/"+m[1]+"/report.md") {
		t.Fatalf("import output:\n%s", out.String())
	}
	// Render checks run in-process through the booted app (no server needed).
	if !regexp.MustCompile(`render failures 0 of [1-9]\d* checked`).MatchString(out.String()) {
		t.Fatalf("render checks must pass in-process:\n%s", out.String())
	}
	if m[1] != previewID {
		t.Fatalf("the import must reuse the dry run's migration: %s vs %s", m[1], previewID)
	}

	cfg, _ := config.Load([]string{"--data", dir})
	d, err := db.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	snapshot := func() string {
		var content, menus, items, redirects, sources int
		var targets string
		d.QueryRow(`SELECT count(*) FROM content`).Scan(&content)
		d.QueryRow(`SELECT count(*) FROM menus`).Scan(&menus)
		d.QueryRow(`SELECT count(*) FROM menu_items`).Scan(&items)
		d.QueryRow(`SELECT count(*) FROM redirects`).Scan(&redirects)
		d.QueryRow(`SELECT count(*) FROM wp_sources`).Scan(&sources)
		d.QueryRow(`SELECT coalesce(group_concat(from_path || '->' || to_url, ' '), '') FROM (SELECT from_path, to_url FROM redirects ORDER BY from_path)`).Scan(&targets)
		return fmt.Sprintf("content=%d menus=%d menu_items=%d redirects=%d sources=%d [%s]", content, menus, items, redirects, sources, targets)
	}
	before := snapshot()
	if !strings.Contains(before, "sources=1 ") || strings.Contains(before, "content=0 ") {
		t.Fatalf("after the first import: %s", before)
	}

	// Re-running the same file (also with its source name) updates in place:
	// no duplicate content, menus or redirects, and redirect targets stay.
	for _, args := range [][]string{{file, "--data", dir}, {file, "--source-name", "Sample site", "--data", dir}} {
		out.Reset()
		if err := importWXR(args, &out); err != nil {
			t.Fatalf("re-import %v: %v\n%s", args, err, out.String())
		}
		if after := snapshot(); after != before {
			t.Fatalf("re-import changed the site:\nbefore %s\nafter  %s", before, after)
		}
		if id := migID.FindStringSubmatch(out.String())[1]; id != previewID {
			t.Fatalf("re-import must reuse the migration: %s vs %s", id, previewID)
		}
	}
	// The same export copied elsewhere (e.g. a fresh download) is the same
	// site: still one source, nothing duplicated.
	cp := filepath.Join(t.TempDir(), "again.xml")
	raw, _ := os.ReadFile(file)
	os.WriteFile(cp, raw, 0o600)
	out.Reset()
	if err := importWXR([]string{cp, "--data", dir}, &out); err != nil {
		t.Fatalf("copy import: %v\n%s", err, out.String())
	}
	if after := snapshot(); after != before {
		t.Fatalf("importing a copy changed the site:\nbefore %s\nafter  %s", before, after)
	}

	// A re-run keeps the migration's stored options: new option flags are
	// ignored (and said so) unless --reset-options is passed.
	out.Reset()
	if err := importWXR([]string{file, "--currency", "usd", "--data", dir}, &out); err != nil || !strings.Contains(out.String(), "--reset-options") {
		t.Fatalf("options-kept note missing: %v\n%s", err, out.String())
	}
	var stored string
	d.QueryRow(`SELECT options_json FROM migrations WHERE id = ?`, previewID).Scan(&stored)
	if strings.Contains(stored, "USD") {
		t.Fatalf("ignored flags must not change the stored options: %s", stored)
	}
	var keptLogs int
	d.QueryRow(`SELECT count(*) FROM migration_log WHERE migration_id = ? AND message = 'options_kept'`, previewID).Scan(&keptLogs)
	if keptLogs != 1 {
		t.Fatalf("options_kept log rows = %d", keptLogs)
	}
	out.Reset()
	if err := importWXR([]string{file, "--currency", "usd", "--reset-options", "--data", dir}, &out); err != nil || strings.Contains(out.String(), "--reset-options") {
		t.Fatalf("--reset-options: %v\n%s", err, out.String())
	}
	d.QueryRow(`SELECT options_json FROM migrations WHERE id = ?`, previewID).Scan(&stored)
	if !strings.Contains(stored, `"currency":"USD"`) {
		t.Fatalf("--reset-options must store the new options: %s", stored)
	}
	if after := snapshot(); after != before {
		t.Fatalf("option re-runs changed the site:\nbefore %s\nafter  %s", before, after)
	}

	// A boolean config flag before the file must not swallow it.
	out.Reset()
	if err := importWXR([]string{"--allow-private-net", file, "--dry-run", "--data", dir}, &out); err != nil || !strings.Contains(out.String(), "preview: migration") {
		t.Fatalf("bool flag before the file: %v\n%s", err, out.String())
	}
	if err := importWXR([]string{filepath.Join(dir, "missing.xml"), "--data", dir}, &out); err == nil {
		t.Fatal("a missing file must fail")
	}
	if err := run([]string{"import-wxr", "--data", dir}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("no file (via run dispatch): %v", err)
	}
}
