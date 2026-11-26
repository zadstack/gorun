package services

import (
	"context"
	"database/sql"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// testDeps boots the settings module on a fresh data directory, as the CLI does.
func testDeps(t *testing.T) *kernel.Deps {
	t.Helper()
	t.Setenv("GORUN_ENV", "dev")
	cfg, err := config.Load([]string{"--data", t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app.Deps
}

// fakeService stores its one field "value" under fake.value; "bad" is a field error after the write.
type fakeService struct{ d *kernel.Deps }

func (fakeService) Name() string         { return "fake" }
func (fakeService) Providers() []string  { return []string{"one"} }
func (fakeService) Fields() []Field      { return []Field{{Name: "value", Type: "string"}} }
func (fakeService) Secret(string) string { return "" }
func (f fakeService) Status(ctx context.Context) (Status, error) {
	v := f.d.Settings.GetString(ctx, "fake.value", "")
	st := Status{Name: "fake", State: "off"}
	if v != "" {
		st.State, st.Fields = "configured", map[string]any{"value": v}
	}
	return st, nil
}
func (f fakeService) Apply(ctx context.Context, tx *sql.Tx, values map[string]string, _ *string) (map[string]string, error) {
	if err := f.d.Settings.SetTx(ctx, tx, "fake.value", values["value"]); err != nil {
		return nil, err
	}
	if values["value"] == "bad" {
		return map[string]string{"value": "invalid"}, nil
	}
	return nil, nil
}
func (fakeService) Test(context.Context, string) (TestResult, error) {
	return TestResult{OK: true}, nil
}
func (f fakeService) Off(ctx context.Context, tx *sql.Tx) error {
	return f.d.Settings.DeleteTx(ctx, tx, "fake.value")
}
