package kernel_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// invAddon registers one of everything the inventory tracks.
type invAddon struct {
	fakeModule
	enabled *atomic.Bool
	runs    *atomic.Int32
}

func (m invAddon) Enabled(context.Context, *sql.DB) (bool, error) { return m.enabled.Load(), nil }

func newInvAddon() invAddon {
	m := invAddon{enabled: &atomic.Bool{}, runs: &atomic.Int32{}}
	m.enabled.Store(true)
	m.fakeModule = fakeModule{
		name: "inv-addon",
		migs: fstest.MapFS{"migrations/0001_inv.sql": {Data: []byte(`CREATE TABLE inv_rows(id TEXT);`)}},
		register: func(d *kernel.Deps) error {
			kernel.Get[any](d, kernel.ServiceContent) // absent, still a recorded lookup
			d.Perms.Declare("inv.manage", map[string]string{"en": "Manage inv"})
			d.Services.Set("inv", struct{}{})
			d.Jobs.Register("inv.work", func(context.Context, json.RawMessage) error { m.runs.Add(1); return nil }, 2)
			d.Jobs.Cron("inv.sweep", time.Hour, func(context.Context) error { return nil })
			d.Theme.AddFunc("invThing", func() string { return "" })
			d.Theme.AddKind("inv", func(kernel.KindRequest) []string { return []string{"inv.html"} })
			d.Admin.AddNav(kernel.NavItem{ID: "inv.nav", LabelKey: "inv", Href: "/admin/inv"})
			d.Hooks.Filter("inv.filter", 10, func(_ context.Context, v any) (any, error) { return v, nil })
			d.API.Add(api.Op("GET", "/v1/inv", "Inv", []string{"inv"}, nil, nil, nil, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			d.MCP.AddTool(kernel.MCPTool{Name: "inv_tool", Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
			return nil
		},
	}
	return m
}

func findState(t *testing.T, app *kernel.App, name string) kernel.ModuleState {
	t.Helper()
	for _, s := range kernel.Modules(app.Deps) {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("module %s missing from inventory", name)
	return kernel.ModuleState{}
}

// The inventory records what each module registered during its Register,
// as observed by the kernel, and remembers its job kinds across boots.
func TestModuleInventoryRecordsRegistrations(t *testing.T) {
	ctx, cfg := context.Background(), testConfig(t)
	m := newInvAddon()
	app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New(), m}, kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	st := findState(t, app, "inv-addon")
	if st.Registered == nil {
		t.Fatal("no registration recorded")
	}
	r := *st.Registered
	for name, got := range map[string][]string{
		"permissions": r.Permissions, "api": r.APIOperations, "mcp": r.MCPTools, "jobs": r.JobKinds, "crons": r.Crons, "nav": r.Nav,
		"theme funcs": r.ThemeFuncs, "theme kinds": r.ThemeKinds, "services": r.ServicesPublished, "hooks": r.HookSubscriptions, "consumed": r.ServicesConsumed,
	} {
		want := map[string]string{"permissions": "inv.manage", "api": "GET /v1/inv", "mcp": "inv_tool", "jobs": "inv.work", "crons": "inv.sweep", "nav": "inv.nav",
			"theme funcs": "invThing", "theme kinds": "inv", "services": "inv", "hooks": "inv.filter", "consumed": kernel.ServiceContent}[name]
		if !slices.Equal(got, []string{want}) {
			t.Errorf("%s = %v, want [%s]", name, got, want)
		}
	}
	if r.Migrations != 1 || r.Catalog {
		t.Errorf("migrations=%d catalog=%v", r.Migrations, r.Catalog)
	}
	if !slices.Equal(st.LastKnownJobKinds, []string{"inv.work"}) {
		t.Errorf("last known job kinds = %v", st.LastKnownJobKinds)
	}
	// The settings module itself registered nothing visible but is listed.
	if s := findState(t, app, "settings"); s.Registered == nil || len(s.Registered.JobKinds) != 0 {
		t.Errorf("settings registration: %+v", s.Registered)
	}
}

// Queued work of a module that is disabled on the next boot is parked, not
// run, failed or deleted, even with workers running; enabling the module
// again releases and runs it unchanged.
func TestDisabledModuleParksQueuedJobsWhileWorkersRunAndResumesOnEnable(t *testing.T) {
	ctx, cfg := context.Background(), testConfig(t)
	m := newInvAddon()
	boot := func(startJobs bool) *kernel.App {
		t.Helper()
		app, err := kernel.BootWithOptions(ctx, cfg, []kernel.Module{settingsmod.New(), m}, kernel.BootOptions{StartJobs: startJobs})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	app := boot(false)
	id, err := app.Deps.Jobs.Enqueue(ctx, "inv.work", map[string]string{"keep": "me"})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()

	m.enabled.Store(false)
	app = boot(true)
	st := findState(t, app, "inv-addon")
	if st.Enabled || st.Registered != nil || !slices.Equal(st.LastKnownJobKinds, []string{"inv.work"}) {
		t.Fatalf("disabled state: %+v", st)
	}
	time.Sleep(300 * time.Millisecond)
	job, err := app.Deps.Jobs.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "queued" || job.ParkedReason != "module inv-addon disabled" || job.Attempts != 0 || m.runs.Load() != 0 {
		t.Fatalf("job while disabled: status=%s parked=%q attempts=%d runs=%d", job.Status, job.ParkedReason, job.Attempts, m.runs.Load())
	}
	parked, _ := app.Deps.Jobs.Parked(ctx)
	if parked["inv.work"] != 1 {
		t.Fatalf("parked counts: %v", parked)
	}
	app.Close()

	m.enabled.Store(true)
	app = boot(true)
	defer app.Close()
	// The handler counts its run before the worker records the outcome, so
	// wait for the job itself to finish rather than for the handler to start.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if job, err = app.Deps.Jobs.Get(ctx, id); err == nil && job.Status == "done" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	job, _ = app.Deps.Jobs.Get(ctx, id)
	if job.Status != "done" || job.ParkedReason != "" || m.runs.Load() != 1 || string(job.Payload) != `{"keep":"me"}` {
		t.Fatalf("job after enable: status=%s parked=%q runs=%d payload=%s", job.Status, job.ParkedReason, m.runs.Load(), job.Payload)
	}
}
