package kernel_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

type inventoryModule struct {
	fakeModule
	enabled bool
}

func (m inventoryModule) Enabled(context.Context, *sql.DB) (bool, error) { return m.enabled, nil }

func TestModuleInventoryTracksActualBootAndPreservesDisabledData(t *testing.T) {
	ctx, cfg := context.Background(), testConfig(t)
	cfg.Addons = map[string]string{"inventory-addon": "enabled", "not-in-this-build": "enabled"}
	registered := 0
	module := inventoryModule{enabled: true, fakeModule: fakeModule{
		name: "inventory-addon",
		migs: fstest.MapFS{"migrations/0001_data.sql": {Data: []byte(`CREATE TABLE inventory_data(value TEXT);`)}},
		register: func(d *kernel.Deps) error {
			registered++
			d.Router.Public.Get("/inventory-addon", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			d.Jobs.Register("inventory.work", func(context.Context, json.RawMessage) error { return nil }, 2)
			return nil
		},
	}}
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New(), module}, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.Deps.DB.Exec(`INSERT INTO inventory_data VALUES('existing content')`); err != nil {
		t.Fatal(err)
	}
	if err = app.Deps.Settings.Set(ctx, "site.theme", "existing-custom-theme"); err != nil {
		t.Fatal(err)
	}
	if err = app.Deps.Settings.Set(ctx, "theme.existing-custom-theme.options", map[string]string{"color": "#abcdef"}); err != nil {
		t.Fatal(err)
	}
	job, err := app.Deps.Jobs.Enqueue(ctx, "inventory.work", map[string]string{"data": "preserved"}, jobs.Idempotency("retained-job"))
	if err != nil {
		t.Fatal(err)
	}
	if status, _, _ := get(t, app.Handler, "GET", "/inventory-addon"); status != 204 {
		t.Fatalf("enabled route: %d", status)
	}
	app.Close()
	module.enabled = false // The actual optional gate wins over the requested config mode.
	app, err = kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New(), module}, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if registered != 1 {
		t.Fatal("disabled module registered routes or workers")
	}
	if status, _, _ := get(t, app.Handler, "GET", "/inventory-addon"); status != 404 {
		t.Fatalf("disabled route: %d", status)
	}
	states := kernel.Modules(app.Deps)
	found := map[string]kernel.ModuleState{}
	for _, state := range states {
		found[state.Name] = state
	}
	if got := found[module.Name()]; !got.Available || got.Enabled || !got.Optional || got.Mode != "enabled" {
		t.Fatalf("inventory guessed config activation: %+v", got)
	}
	if got := found["not-in-this-build"]; got.Available || got.Enabled || got.Name == "" {
		t.Fatalf("missing addon incorrectly available: %+v", got)
	}
	states[0].Name = "mutated"
	if kernel.Modules(app.Deps)[0].Name == "mutated" {
		t.Fatal("caller can mutate boot inventory")
	}
	var content, themeOptions, payload, status string
	if err = app.Deps.DB.QueryRow(`SELECT value FROM inventory_data`).Scan(&content); err != nil || content != "existing content" {
		t.Fatalf("content changed: %s %v", content, err)
	}
	if got := app.Deps.Settings.GetString(ctx, "site.theme", ""); got != "existing-custom-theme" {
		t.Fatalf("theme changed: %s", got)
	}
	if err = app.Deps.DB.QueryRow(`SELECT value_json FROM settings WHERE key='theme.existing-custom-theme.options'`).Scan(&themeOptions); err != nil || themeOptions != `{"color":"#abcdef"}` {
		t.Fatalf("theme options changed: %s %v", themeOptions, err)
	}
	if err = app.Deps.DB.QueryRow(`SELECT payload_json,status FROM jobs WHERE id=?`, job).Scan(&payload, &status); err != nil || payload != `{"data":"preserved"}` || status != "queued" {
		t.Fatalf("pending data changed: %s %s %v", payload, status, err)
	}
	probeJob, err := app.Deps.Jobs.Enqueue(ctx, "inventory.work", nil)
	if err != nil {
		t.Fatal(err)
	}
	var attempts int
	if err = app.Deps.DB.QueryRow(`SELECT max_attempts FROM jobs WHERE id=?`, probeJob).Scan(&attempts); err != nil || attempts == 2 {
		t.Fatalf("disabled worker registration survived: %d %v", attempts, err)
	}
}
