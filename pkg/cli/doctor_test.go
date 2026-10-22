package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestDoctorMissingSiteMakesNoFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	var out bytes.Buffer
	err := doctorCmd([]string{"--data", dir, "--json"}, &out)
	if err == nil {
		t.Fatal("missing site must fail")
	}
	var report map[string]any
	if json.Unmarshal(out.Bytes(), &report) != nil {
		t.Fatal("doctor did not return JSON")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("doctor created data directory")
	}
	if !strings.Contains(out.String(), "gorun start") {
		t.Fatal("missing recovery advice")
	}
}

func TestDoctorHealthySiteIsReadOnly(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir(), "--addr", "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	if _, err := users.Create(context.Background(), "owner@example.com", "Owner", "existing secure password", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	app.Close()
	before, err := os.ReadFile(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := doctorCmd([]string{"--data", cfg.DataDir, "--addr", "127.0.0.1:0", "--json"}, &out); err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, out.String())
	}
	after, _ := os.ReadFile(cfg.DBPath())
	if !bytes.Equal(before, after) {
		t.Fatal("doctor modified database")
	}
	var report struct {
		OK     bool                          `json:"ok"`
		Checks []struct{ ID, Status string } `json:"checks"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || !report.OK || len(report.Checks) < 6 {
		t.Fatalf("invalid report: %v", err)
	}
	for _, secret := range []string{"existing secure password", "password_hash", "secret.key\n"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("secret in report")
		}
	}
}

func TestDoctorReportsPortSchemaAndThemeProblems(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir(), "--addr", "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.BootstrapAdmin(context.Background(), app.Deps.DB, app.Deps.Audit, "temporary secure password"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.Exec(`DELETE FROM schema_migrations WHERE module = 'auth' AND name = '0011_onboarding.sql'`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.Exec(`UPDATE schema_migrations SET checksum = 'edited' WHERE module = 'auth' AND name = '0010_login_name.sql'`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.Exec(`INSERT INTO schema_migrations (module,name,applied_at,checksum) VALUES ('future','9999_future.sql','2026-10-19','unknown')`); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(context.Background(), "site.theme", "missing-theme"); err != nil {
		t.Fatal(err)
	}
	app.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var out bytes.Buffer
	err = doctorCmd([]string{"--data", cfg.DataDir, "--addr", listener.Addr().String(), "--json"}, &out)
	if err == nil {
		t.Fatal("broken site must fail")
	}
	var report siteDoctorReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 pending, 1 changed, 1 unknown") {
		t.Fatal("migration drift not reported correctly")
	}
	statuses := map[string]string{}
	for _, c := range report.Checks {
		statuses[c.ID] = c.Status
	}
	for _, id := range []string{"port", "schema", "theme"} {
		if statuses[id] != "fail" {
			t.Fatalf("%s=%s", id, statuses[id])
		}
	}
	if statuses["onboarding"] != "warn" {
		t.Fatal("unfinished onboarding missing")
	}
	// Inspecting a pending schema must not apply it.
	d, err := sql.Open("sqlite", "file:"+cfg.DBPath()+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM schema_migrations WHERE module='auth' AND name='0011_onboarding.sql'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("doctor applied a migration")
	}
}

func TestDoctorRunningHealthEndpointIsWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Error("unexpected probe path")
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	report := siteDoctorReport{OK: true}
	doctorPort(config.Config{Addr: parsed.Host}, &report)
	if len(report.Checks) != 1 || report.Checks[0].Status != "warn" || !report.OK {
		t.Fatalf("%+v", report)
	}
}

func TestDoctorRedactsConfigurationSecrets(t *testing.T) {
	t.Setenv("GORUN_ENV", "prod")
	t.Setenv("GORUN_BASE_URL", "https://user:super-secret@example.com/path")
	t.Setenv("GORUN_SECRET_KEY", "private-secret-not-base64")
	var out bytes.Buffer
	if err := doctorCmd([]string{"--data", t.TempDir(), "--json"}, &out); err == nil {
		t.Fatal("invalid config accepted")
	}
	if strings.Contains(out.String(), "super-secret") || strings.Contains(out.String(), "private-secret") {
		t.Fatal("configuration secret leaked")
	}
}

func TestDoctorCorruptDatabase(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.DBPath(), []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := doctorCmd([]string{"--data", cfg.DataDir, "--addr", "127.0.0.1:0"}, &out); err == nil || !strings.Contains(out.String(), "[fail] database") {
		t.Fatalf("missing corruption failure: %v", err)
	}
}

type doctorOptionalModule struct {
	enabled       bool
	activationErr error
}

func (doctorOptionalModule) Name() string { return "doctor-optional" }
func (doctorOptionalModule) Migrations() fs.FS {
	return fstest.MapFS{"migrations/0001_optional.sql": &fstest.MapFile{Data: []byte("CREATE TABLE doctor_optional (id TEXT);")}}
}
func (doctorOptionalModule) Register(*kernel.Deps) error { return nil }
func (m doctorOptionalModule) Enabled(context.Context, *sql.DB) (bool, error) {
	return m.enabled, m.activationErr
}

func TestDoctorDisabledAddonDoesNotRequireMigrations(t *testing.T) {
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	mods := append(Modules(cfg), doctorOptionalModule{})
	report := siteDoctorReport{OK: true}
	doctorSchema(context.Background(), app.Deps.DB, mods, &report)
	if !report.OK {
		t.Fatalf("disabled addon reported pending migrations: %+v", report)
	}
	// identityai ships off by default, so the fixture's disabled module makes two.
	if len(report.Checks) != 2 || !strings.Contains(report.Checks[0].Message, "2 disabled") {
		t.Fatalf("activation not reported: %+v", report)
	}
	mods[len(mods)-1] = doctorOptionalModule{activationErr: errors.New("bad configuration with private data")}
	report = siteDoctorReport{OK: true}
	doctorSchema(context.Background(), app.Deps.DB, mods, &report)
	if report.OK || report.Checks[0].ID != "addons" || strings.Contains(report.Checks[0].Message, "private data") {
		t.Fatal("activation error missing or private detail leaked")
	}
}
