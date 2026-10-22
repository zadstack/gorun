package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/jobs"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/wpmigrate"
)

var optionalFeatures = []string{"launch", "workagents", "wpmigrate", "mcp", "ext"}
var featurePaths = map[string][]string{
	"launch":     {"/start", "/start/login", "/start/verify"},
	"workagents": {"/agents", "/workspace", "/static/workagents.css"},
	"wpmigrate":  {"/api/v1/migrations/sources"},
	"mcp":        {"/mcp", "/api/v1/mcp/approvals"},
	"ext":        {"/api/v1/extensions"},
}

// Real modules are booted together: disabling migrator must leave MCP usable,
// and disabling MCP must leave the other four modules registered.
func TestOptionalFeaturesIndependentDisableKeepsRegistrationsAndData(t *testing.T) {
	ctx, data := context.Background(), t.TempDir()
	app := bootData(t, data, "")
	baseline := moduleStates(app)
	key := adminKey(t, app, "api")
	bearer := http.Header{"Authorization": {"Bearer " + key}}
	for _, name := range optionalFeatures {
		state := baseline[name]
		if !state.Enabled || !state.Optional || state.Registered == nil {
			t.Fatalf("default %s: %+v", name, state)
		}
	}
	// Persist module-owned rows, including an approval and inactive extension.
	for _, statement := range []string{
		`INSERT INTO wp_sources(id,name,created_at) VALUES('kept-source','Kept source','2026-10-22T00:00:00Z')`,
		`INSERT INTO extensions(slug,name,version,manifest_json,installed_at) VALUES('kept-extension','Kept extension','1','{}','2026-10-22T00:00:00Z')`,
		`INSERT INTO workagents_agents(id,owner_id,name,created_at,updated_at) SELECT 'kept-agent',id,'Kept agent','2026-10-22T00:00:00Z','2026-10-22T00:00:00Z' FROM users WHERE email='api-owner@example.com'`,
		`INSERT INTO mcp_approvals(id,api_key_id,tool,requested_at,expires_at) SELECT 'kept-approval',id,'kept-tool','2026-10-22T00:00:00Z','2099-10-22T00:00:00Z' FROM api_keys LIMIT 1`,
	} {
		if _, err := app.Deps.DB.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	queued := map[string]string{}
	for _, kind := range []string{"workagents.run", "migration.step", "ext.deliver"} {
		id, err := app.Deps.Jobs.Enqueue(ctx, kind, map[string]string{"fixture": "preserved"}, jobs.RunAt(time.Now().Add(24*time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		queued[kind] = id
	}
	app.Close()
	for _, disabled := range optionalFeatures {
		t.Run(disabled, func(t *testing.T) {
			modes, _ := json.Marshal(map[string]string{disabled: "disabled"})
			app := bootData(t, data, string(modes))
			defer app.Close()
			assertFeatureBoot(t, app, baseline, []string{disabled}, bearer)
			assertFeatureRows(t, app)
			if disabled == "wpmigrate" {
				for _, tool := range app.Deps.MCP.Tools() {
					if tool.Name == "get_migration_status" || tool.Name == "run_migration_preview" {
						t.Fatalf("MCP retained disabled migration tool %s", tool.Name)
					}
				}
				if len(app.Deps.MCP.Tools()) == 0 {
					t.Fatal("MCP lost all tools when only migrator disabled")
				}
			}
		})
	}
	// The persisted settings gate cannot be overridden by mode enabled or auto.
	app = bootData(t, data, "")
	modes := map[string]string{}
	for _, name := range optionalFeatures {
		if err := app.Deps.Settings.Set(ctx, kernel.AddonSettingKey(name), false); err != nil {
			t.Fatal(err)
		}
		modes[name] = "enabled"
	}
	app.Close()
	encoded, _ := json.Marshal(modes)
	app = bootData(t, data, string(encoded))
	app = restartFeatureWorkers(t, app)
	assertFeatureBoot(t, app, baseline, optionalFeatures, bearer)
	assertFeatureRows(t, app)
	for kind, id := range queued {
		job, err := app.Deps.Jobs.Get(ctx, id)
		if err != nil || job.Status != "queued" || job.ParkedReason == "" || job.Attempts != 0 {
			t.Fatalf("parked %s: %+v %v", kind, job, err)
		}
	}
	if err := mcp.RunIO(ctx, app, key, io.NopCloser(strings.NewReader("")), nopWriteCloser{io.Discard}); err == nil || !strings.Contains(err.Error(), "module not booted") {
		t.Fatalf("disabled stdio: %v", err)
	}
	assertDisabledWXR(t, app)
	for _, name := range optionalFeatures {
		if err := app.Deps.Settings.Set(ctx, kernel.AddonSettingKey(name), true); err != nil {
			t.Fatal(err)
		}
	}
	app.Close()
	app = bootData(t, data, "")
	app = restartFeatureWorkers(t, app)
	defer app.Close()
	assertFeatureBoot(t, app, baseline, nil, bearer)
	assertFeatureRows(t, app)
	// Deliberately malformed payloads cannot call providers or remote sites. A
	// handler attempt after re-enable proves parked work resumed with its payload.
	for kind, id := range queued {
		deadline := time.Now().Add(5 * time.Second)
		for {
			job, err := app.Deps.Jobs.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if (job.Attempts > 0 || job.Status == "done") && job.ParkedReason == "" && string(job.Payload) == `{"fixture":"preserved"}` {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("released %s did not resume: %+v", kind, job)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func assertFeatureRows(t *testing.T, app *kernel.App) {
	t.Helper()
	for table, condition := range map[string]string{"wp_sources": "id='kept-source' AND name='Kept source'", "extensions": "slug='kept-extension' AND name='Kept extension'", "workagents_agents": "id='kept-agent' AND name='Kept agent'", "mcp_approvals": "id='kept-approval' AND tool='kept-tool' AND status='pending'"} {
		var count int
		if err := app.Deps.DB.QueryRow("SELECT count(*) FROM " + table + " WHERE " + condition).Scan(&count); err != nil || count != 1 {
			t.Fatalf("preserved %s: %d %v", table, count, err)
		}
	}
}

func assertFeatureBoot(t *testing.T, app *kernel.App, baseline map[string]kernel.ModuleState, disabled []string, bearer http.Header) {
	t.Helper()
	states := moduleStates(app)
	for _, name := range optionalFeatures {
		off := slices.Contains(disabled, name)
		state := states[name]
		if !state.Optional || state.Enabled == off || (state.Registered == nil) != off {
			t.Fatalf("%s disabled=%v: %+v", name, off, state)
		}
		if !off {
			continue
		}
		for _, path := range featurePaths[name] {
			if status, _ := serveStatus(app.Handler, "GET", path, bearer); status != 404 {
				t.Fatalf("disabled %s: HTTP %d", path, status)
			}
		}
		reg := baseline[name].Registered
		for _, kind := range reg.JobKinds {
			if slices.Contains(app.Deps.Jobs.Kinds(), kind) {
				t.Fatalf("disabled %s retained job %s", name, kind)
			}
		}
		for _, cron := range reg.Crons {
			if slices.Contains(app.Deps.Jobs.CronNames(), cron) {
				t.Fatalf("disabled %s retained cron %s", name, cron)
			}
		}
		for _, nav := range reg.Nav {
			if slices.Contains(app.Deps.Admin.NavIDs(), nav) {
				t.Fatalf("disabled %s retained nav %s", name, nav)
			}
		}
		for _, service := range reg.ServicesPublished {
			if slices.Contains(app.Deps.Services.Names(), service) {
				t.Fatalf("disabled %s retained service %s", name, service)
			}
		}
		for _, operation := range reg.APIOperations {
			for _, current := range app.Deps.API.Operations() {
				if current.Method+" "+current.Path == operation {
					t.Fatalf("disabled %s retained API %s", name, operation)
				}
			}
		}
	}
	// Shared health filters must drop only subscribers belonging to disabled modules.
	expectedHealth := 0
	for name, state := range baseline {
		if !slices.Contains(disabled, name) && state.Registered != nil && slices.Contains(state.Registered.HookSubscriptions, "system.health.checks") {
			expectedHealth++
		}
	}
	if got := app.Deps.Hooks.Subscriptions()["system.health.checks"]; got != expectedHealth {
		t.Fatalf("health subscribers %d want %d", got, expectedHealth)
	}
	status, body := serveStatus(app.Handler, "GET", "/api/v1/system/modules", bearer)
	if status != 200 {
		t.Fatalf("module catalog HTTP %d: %s", status, body)
	}
	var envelope struct {
		Data admin.ModuleCatalog `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range envelope.Data.Entries {
		if slices.Contains(optionalFeatures, entry.ID) {
			seen[entry.ID] = true
			if !entry.State.Optional || entry.State.Switchable != (entry.State.Mode != kernel.AddonModeDisabled) || entry.State.Enabled == slices.Contains(disabled, entry.ID) {
				t.Fatalf("catalog %s: %+v", entry.ID, entry.State)
			}
		}
	}
	if len(seen) != len(optionalFeatures) {
		t.Fatalf("catalog missing optional feature rows: %v", seen)
	}
	if status, _ := serveStatus(app.Handler, "GET", "/", nil); status != 200 {
		t.Fatalf("core homepage HTTP %d", status)
	}
}

func assertDisabledWXR(t *testing.T, app *kernel.App) {
	t.Helper()
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "export.xml")
	if err := os.WriteFile(file, []byte(`<rss version="2.0"><channel><title>Private fixture</title></channel></rss>`), 0600); err != nil {
		t.Fatal(err)
	}
	before := migrationRowCounts(t, app)
	importsBefore, err := filepath.Glob(filepath.Join(app.Deps.Config.DataDir, "imports", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, preview := range []bool{false, true} {
		var err error
		if preview {
			_, err = wpmigrate.PreviewWXR(ctx, app.Deps, wpmigrate.WXRFile{Path: file}, wpmigrate.Options{})
		} else {
			_, err = wpmigrate.ImportWXRFile(ctx, app.Deps, file, wpmigrate.Options{})
		}
		if err == nil || !strings.Contains(err.Error(), "addon disabled") {
			t.Fatalf("WXR preview=%v: %v", preview, err)
		}
	}
	if after := migrationRowCounts(t, app); !reflect.DeepEqual(before, after) {
		t.Fatalf("disabled WXR changed stored rows: %v -> %v", before, after)
	}
	importsAfter, err := filepath.Glob(filepath.Join(app.Deps.Config.DataDir, "imports", "*"))
	if err != nil || !slices.Equal(importsBefore, importsAfter) {
		t.Fatalf("disabled WXR changed imports: %v -> %v (%v)", importsBefore, importsAfter, err)
	}
}

func TestOptionalFeaturesDisabledOnFreshInstallSkipOwnedSchema(t *testing.T) {
	modes := map[string]string{}
	for _, name := range optionalFeatures {
		modes[name] = "disabled"
	}
	encoded, err := json.Marshal(modes)
	if err != nil {
		t.Fatal(err)
	}
	app := bootData(t, t.TempDir(), string(encoded))
	defer app.Close()
	for _, table := range []string{"workagents_agents", "wp_sources", "mcp_approvals", "extensions"} {
		var count int
		if err := app.Deps.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("disabled fresh schema %s: %d %v", table, count, err)
		}
	}
	// The CLI fallback must reject operator-disabled modules even before their
	// tables exist, rather than creating a source or reporting a SQL error.
	assertDisabledWXR(t, app)
}

func restartFeatureWorkers(t *testing.T, app *kernel.App) *kernel.App {
	t.Helper()
	cfg := app.Deps.Config
	app.Close()
	running, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{StartJobs: true})
	if err != nil {
		t.Fatal(err)
	}
	return running
}

func migrationRowCounts(t *testing.T, app *kernel.App) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"wp_sources", "migrations", "migration_items", "migration_log", "content", "media"} {
		var exists int
		if err := app.Deps.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			counts[table] = -1
			continue
		}
		var count int
		if err := app.Deps.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	return counts
}

func TestDisabledWXRCommandRejectsImportAndPreview(t *testing.T) {
	for _, mode := range []string{"config", "setting"} {
		t.Run(mode, func(t *testing.T) {
			data := t.TempDir()
			app := bootData(t, data, "")
			if mode == "setting" {
				if err := app.Deps.Settings.Set(context.Background(), kernel.AddonSettingKey("wpmigrate"), false); err != nil {
					t.Fatal(err)
				}
			}
			before := migrationRowCounts(t, app)
			app.Close()
			if mode == "config" {
				t.Setenv("GORUN_ADDONS", `{"wpmigrate":"disabled"}`)
			}
			file := filepath.Join(t.TempDir(), "export.xml")
			if err := os.WriteFile(file, []byte(`<rss><channel><title>fixture</title></channel></rss>`), 0600); err != nil {
				t.Fatal(err)
			}
			for _, preview := range []bool{false, true} {
				args := []string{file, "--data", data}
				if preview {
					args = append(args, "--dry-run")
				}
				var output bytes.Buffer
				if err := importWXR(args, &output); err == nil || !strings.Contains(err.Error(), "addon disabled") {
					t.Fatalf("CLI preview=%v: %v %s", preview, err, output.String())
				}
			}
			addons := ""
			if mode == "config" {
				addons = `{"wpmigrate":"disabled"}`
			}
			app = bootData(t, data, addons)
			defer app.Close()
			if after := migrationRowCounts(t, app); !reflect.DeepEqual(before, after) {
				t.Fatalf("disabled CLI changed rows: %v -> %v", before, after)
			}
		})
	}
}

func TestWXRPreviewWithoutPublishedServiceKeepsEnabledEmbeddingFallback(t *testing.T) {
	app := bootData(t, t.TempDir(), "")
	defer app.Close()
	// Embedders may apply migrations without registering the migrator service.
	// Use real core services and the migrated database, with no migrator entry.
	deps := *app.Deps
	deps.Services = kernel.NewServiceRegistry()
	for _, name := range app.Deps.Services.Names() {
		if name == wpmigrate.ServiceName {
			continue
		}
		service, ok := kernel.Get[any](app.Deps, name)
		if !ok {
			t.Fatalf("published service %s unavailable", name)
		}
		deps.Services.Set(name, service)
	}
	preview, err := wpmigrate.PreviewWXR(context.Background(), &deps, wpmigrate.WXRFile{Path: wxrFixture(t)}, wpmigrate.Options{})
	if err != nil || preview == nil {
		t.Fatalf("enabled embedding preview: %+v %v", preview, err)
	}
	var count int
	if err := app.Deps.DB.QueryRow("SELECT count(*) FROM wp_sources").Scan(&count); err != nil || count != 1 {
		t.Fatalf("enabled fallback did not create preview source: %d %v", count, err)
	}
}

func TestMembershipNavigationTracksWorkAgentsAcrossDisableAndReenable(t *testing.T) {
	ctx, data := context.Background(), t.TempDir()
	app := bootData(t, data, "")
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	user, err := users.Create(ctx, "membership-nav@fixture.test", "Member", "fixture-password-12345", "author")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.ExecContext(ctx, `INSERT INTO membership_plans(id,name,limits_json) VALUES('kept-navigation-plan','Kept navigation plan','{}')`); err != nil {
		t.Fatal(err)
	}
	assertMembershipAgentsLink(t, app, user, true)
	app.Close()
	app = bootData(t, data, `{"workagents":"disabled"}`)
	assertMembershipAgentsLink(t, app, user, false)
	key := adminKey(t, app, "api")
	status, body := serveStatus(app.Handler, "GET", "/api/v1/system/modules", http.Header{"Authorization": {"Bearer " + key}})
	if status != 200 {
		t.Fatalf("catalog HTTP %d: %s", status, body)
	}
	var envelope struct {
		Data admin.ModuleCatalog `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range envelope.Data.Entries {
		if entry.ID != "membership" {
			continue
		}
		found = true
		for _, dependency := range []string{"commerce", "saas", "workagents"} {
			if !slices.Contains(entry.OptionalDependencies, dependency) {
				t.Errorf("membership lacks optional %s", dependency)
			}
		}
		for locale, consequence := range map[string]string{"en": "Agents link and /agents route are unavailable", "ar": "رابط الإيجنتس ومسار /agents غير متاحين"} {
			matched := false
			for _, message := range entry.Messages {
				if strings.Contains(message.Text[locale], consequence) {
					matched = true
				}
			}
			if !matched {
				t.Errorf("missing %s WorkAgents consequence: %+v", locale, entry.Messages)
			}
		}
	}
	if !found {
		t.Fatal("membership catalog row missing")
	}
	if status, _ := serveStatus(app.Handler, "GET", "/agents", nil); status != 404 {
		t.Fatalf("disabled agents HTTP %d", status)
	}
	app.Close()
	app = bootData(t, data, "")
	defer app.Close()
	assertMembershipAgentsLink(t, app, user, true)
}

func assertMembershipAgentsLink(t *testing.T, app *kernel.App, user *auth.User, available bool) {
	t.Helper()
	request := httptest.NewRequest("GET", "/membership", nil).WithContext(auth.WithUser(context.Background(), user))
	response := httptest.NewRecorder()
	app.Handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("membership HTTP %d: %s", response.Code, response.Body.String())
	}
	if linked := strings.Contains(response.Body.String(), `href="/agents"`); linked != available {
		t.Fatalf("membership agents link=%v want %v", linked, available)
	}
	var count int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM membership_plans WHERE id='kept-navigation-plan' AND name='Kept navigation plan'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("membership row changed across workagents activation: %d %v", count, err)
	}
}
