package kernel_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func demoModule(body string) fakeModule {
	return fakeModule{name: "demo", migs: fstest.MapFS{"migrations/0001_demo.sql": {Data: []byte(body)}}}
}

// captureBootLog routes Boot's logger into a buffer for the test.
func captureBootLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	kernel.LogOutput = &buf
	t.Cleanup(func() { kernel.LogOutput = io.Discard })
	return &buf
}

// captureDefaultLog routes slog.Default (MigrateAll's logger) into a buffer.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func bootOnce(t *testing.T, cfg config.Config, mods ...kernel.Module) {
	t.Helper()
	app, err := kernel.BootWithOptions(context.Background(), cfg, append([]kernel.Module{settingsmod.New()}, mods...), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
}

func warnLines(log, needle string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, "WARN") && strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return out
}

func TestBootWarnsWhenAppliedMigrationChanged(t *testing.T) {
	cfg := testConfig(t)
	bootOnce(t, cfg, demoModule("CREATE TABLE demo(x);"))
	logs := captureBootLog(t)
	bootOnce(t, cfg, demoModule("CREATE TABLE demo(x, y);")) // edited after it was applied
	lines := warnLines(logs.String(), "checksum")
	if len(lines) != 1 || !strings.Contains(lines[0], "module=demo") || !strings.Contains(lines[0], "0001_demo.sql") {
		t.Fatalf("want one checksum WARN naming demo/0001_demo.sql, got %q\nlog:\n%s", lines, logs)
	}
	logs.Reset()
	bootOnce(t, cfg, demoModule("CREATE TABLE demo(x);"))
	if lines := warnLines(logs.String(), "checksum"); len(lines) != 0 {
		t.Fatalf("unchanged file must not warn: %q", lines)
	}
}

func TestMigrateAllWarnsWhenAppliedMigrationChanged(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := kernel.MigrateAll(ctx, d, []kernel.Module{settingsmod.New(), demoModule("CREATE TABLE demo(x);")}); err != nil {
		t.Fatal(err)
	}
	logs := captureDefaultLog(t)
	if err := kernel.MigrateAll(ctx, d, []kernel.Module{settingsmod.New(), demoModule("CREATE TABLE demo(y);")}); err != nil {
		t.Fatalf("a checksum mismatch must not fail: %v", err)
	}
	if lines := warnLines(logs.String(), "checksum"); len(lines) != 1 || !strings.Contains(lines[0], "module=demo") {
		t.Fatalf("want one checksum WARN, got %q", lines)
	}
}

func TestBootWarnsWhenSchemaIsAheadOfBinary(t *testing.T) {
	cfg := testConfig(t)
	newer := fakeModule{name: "demo", migs: fstest.MapFS{
		"migrations/0001_demo.sql": {Data: []byte("CREATE TABLE demo(x);")},
		"migrations/0002_demo.sql": {Data: []byte("ALTER TABLE demo ADD COLUMN y TEXT;")},
	}}
	bootOnce(t, cfg, newer)
	logs := captureBootLog(t)
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New(), demoModule("CREATE TABLE demo(x);")}, kernel.BootOptions{})
	if err != nil {
		t.Fatalf("an older binary must still boot: %v", err)
	}
	defer app.Close()
	lines := warnLines(logs.String(), "unknown to this binary")
	if len(lines) != 1 || !strings.Contains(lines[0], "demo/0002_demo.sql") {
		t.Fatalf("want one schema-ahead WARN listing demo/0002_demo.sql, got %q\nlog:\n%s", lines, logs)
	}
	inspect, ok := kernel.Get[kernel.SchemaInspector](app.Deps, kernel.ServiceSchema)
	if !ok {
		t.Fatal("schema inspector not published")
	}
	got, err := inspect(context.Background())
	if err != nil || len(got.Unknown) != 1 || got.Unknown[0] != "demo/0002_demo.sql" {
		t.Fatalf("inspect = %+v %v", got, err)
	}
}

// optionalDemo is an optional add-on without a catalog.
type optionalDemo struct {
	fakeModule
	enabled bool
}

func (m optionalDemo) Enabled(context.Context, *sql.DB) (bool, error) { return m.enabled, nil }

func TestDisabledAddonRowsAreNotUnknown(t *testing.T) {
	cfg := testConfig(t)
	bootOnce(t, cfg, optionalDemo{demoModule("CREATE TABLE demo(x);"), true})
	logs := captureBootLog(t)
	bootOnce(t, cfg, optionalDemo{demoModule("CREATE TABLE demo(x);"), false})
	if lines := warnLines(logs.String(), "unknown to this binary"); len(lines) != 0 {
		t.Fatalf("a disabled add-on's applied migrations are known: %q", lines)
	}
}

func TestMigrateAllWarnsWhenSchemaIsAheadAndInspectReportsIt(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, d, "future", fstest.MapFS{"migrations/0001_f.sql": {Data: []byte("CREATE TABLE f(x);")}}); err != nil {
		t.Fatal(err)
	}
	// A BootOrder module left out of a partial list (CLI commands that boot
	// only settings) cannot be judged: its rows are not reported.
	if err := db.Migrate(ctx, d, "content", fstest.MapFS{"migrations/0001_c.sql": {Data: []byte("CREATE TABLE c(x);")}}); err != nil {
		t.Fatal(err)
	}
	mods := []kernel.Module{settingsmod.New()}
	logs := captureDefaultLog(t)
	if err := kernel.MigrateAll(ctx, d, mods); err != nil {
		t.Fatalf("schema ahead must not fail: %v", err)
	}
	if lines := warnLines(logs.String(), "future/0001_f.sql"); len(lines) != 1 {
		t.Fatalf("want one WARN listing future/0001_f.sql, got %q", lines)
	}
	got, err := kernel.InspectSchema(ctx, d, mods)
	if err != nil || len(got.Unknown) != 1 || got.Unknown[0] != "future/0001_f.sql" {
		t.Fatalf("InspectSchema = %+v %v", got, err)
	}
}
