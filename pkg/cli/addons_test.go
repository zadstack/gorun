package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
)

// optionalBuiltins are the built-in modules behind kernel.OptionalModule.
var optionalBuiltins = []string{"commerce", "saas", "membership"}

// bootData boots every module of this build on data (an existing data dir
// is reused) with the given GORUN_ADDONS value, without job workers.
func bootData(t *testing.T, data, addons string) *kernel.App {
	t.Helper()
	kernel.LogOutput = logSink
	if addons != "" {
		t.Setenv("GORUN_ADDONS", addons)
	} else {
		t.Setenv("GORUN_ADDONS", "{}")
	}
	cfg, err := config.Load([]string{"--data", data, "--base-url", "http://gorun.test"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.BootWithOptions(context.Background(), cfg, Modules(cfg), kernel.BootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func serveStatus(h http.Handler, method, path string, header http.Header) (int, string) {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func moduleStates(app *kernel.App) map[string]kernel.ModuleState {
	out := map[string]kernel.ModuleState{}
	for _, s := range kernel.Modules(app.Deps) {
		out[s.Name] = s
	}
	return out
}

// Commerce, saas and membership are optional registrations: enabled by
// default (existing installs change nothing), switched off by the operator
// config, and their data survives a disable/enable cycle. While off, their
// public routes, API operations, job kinds, admin navigation and theme kinds
// are absent and the rest of the site renders.
func TestOptionalBuiltinsDisabledByConfigKeepData(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()

	// 1. Default boot: all three enabled; create data worth preserving.
	app := bootData(t, data, "")
	states := moduleStates(app)
	for _, name := range optionalBuiltins {
		if s := states[name]; !s.Available || !s.Enabled || !s.Optional || s.Mode != "" {
			t.Fatalf("default boot: %s = %+v, want available, enabled, optional, no mode", name, s)
		}
	}
	cs, ok := kernel.Get[commerce.Service](app.Deps, kernel.ServiceCommerce)
	if !ok {
		t.Fatal("commerce service missing while enabled")
	}
	if _, err := cs.SaveProduct(ctx, commerce.Product{Kind: commerce.ProductService, Name: "Kept plan", Slug: "kept-plan", PriceMinor: 1900, Currency: "AED", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Deps.DB.ExecContext(ctx, `INSERT INTO membership_plans(id, name, limits_json) VALUES('plan-kept','Kept','{}')`); err != nil {
		t.Fatal(err)
	}
	if status, _ := serveStatus(app.Handler, "GET", "/shop", nil); status != 200 {
		t.Fatalf("enabled /shop: %d", status)
	}
	kinds := app.Deps.Jobs.Kinds()
	for _, k := range []string{commerce.JobProvision, commerce.JobSuspend} {
		if !slices.Contains(kinds, k) {
			t.Fatalf("enabled boot lacks job kind %s: %v", k, kinds)
		}
	}
	key := adminKey(t, app, "api")
	app.Close()

	// 2. Disabled by GORUN_ADDONS on the same data dir.
	app = bootData(t, data, `{"commerce":"disabled","saas":"disabled","membership":"disabled"}`)
	states = moduleStates(app)
	for _, name := range optionalBuiltins {
		if s := states[name]; !s.Available || s.Enabled || !s.Optional || s.Mode != "disabled" {
			t.Fatalf("disabled boot: %s = %+v, want available, not enabled, optional, mode disabled", name, s)
		}
	}
	if _, ok := kernel.Get[commerce.Service](app.Deps, kernel.ServiceCommerce); ok {
		t.Fatal("commerce service published while disabled")
	}
	for _, k := range app.Deps.Jobs.Kinds() {
		if strings.HasPrefix(k, "commerce.") {
			t.Fatalf("disabled commerce still registered job kind %s", k)
		}
	}
	for _, path := range []string{"/shop", "/cart", "/checkout", "/account", "/product/kept-plan", "/saas", "/membership"} {
		if status, _ := serveStatus(app.Handler, "GET", path, nil); status != 404 {
			t.Fatalf("disabled %s: %d, want 404", path, status)
		}
	}
	for _, path := range []string{"/", "/sitemap.xml", "/robots.txt"} {
		if status, _ := serveStatus(app.Handler, "GET", path, nil); status != 200 {
			t.Fatalf("site without the optional modules: %s → %d", path, status)
		}
	}
	bearer := http.Header{"Authorization": {"Bearer " + key}}
	for _, path := range []string{"/api/v1/commerce/products", "/api/v1/saas", "/api/v1/membership"} {
		if status, _ := serveStatus(app.Handler, "GET", path, bearer); status != 404 {
			t.Fatalf("disabled API %s: %d, want 404", path, status)
		}
	}
	status, body := serveStatus(app.Handler, "GET", "/api/v1/openapi.json", bearer)
	if status != 200 {
		t.Fatalf("openapi: %d", status)
	}
	for _, prefix := range []string{`"/commerce/`, `"/saas`, `"/membership`} {
		if strings.Contains(body, prefix) {
			t.Fatalf("openapi still documents %s while disabled", prefix)
		}
	}
	status, body = serveStatus(app.Handler, "GET", "/api/v1/site/capabilities", bearer)
	if status != 200 {
		t.Fatalf("capabilities: %d %s", status, body)
	}
	var report struct {
		Data struct {
			Modules []struct {
				ID      string `json:"id"`
				Enabled *bool  `json:"enabled"`
				Reason  string `json:"reason"`
			} `json:"modules"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, m := range report.Data.Modules {
		if slices.Contains(optionalBuiltins, m.ID) {
			seen++
			if m.Enabled == nil || *m.Enabled || m.Reason != "module_disabled" {
				t.Fatalf("capability report for %s: enabled=%v reason=%s", m.ID, m.Enabled, m.Reason)
			}
		}
	}
	if seen != len(optionalBuiltins) {
		t.Fatalf("capability report lists %d of %d optional modules", seen, len(optionalBuiltins))
	}
	var n int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM products WHERE slug = 'kept-plan'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("product row while disabled: %d %v", n, err)
	}
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM membership_plans WHERE id = 'plan-kept'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("membership plan row while disabled: %d %v", n, err)
	}
	app.Close()

	// 3. Enabled again: the same data is read back through the service.
	app = bootData(t, data, "")
	defer app.Close()
	cs, ok = kernel.Get[commerce.Service](app.Deps, kernel.ServiceCommerce)
	if !ok {
		t.Fatal("commerce service missing after re-enable")
	}
	if p, err := cs.Product(ctx, "kept-plan"); err != nil || p.PriceMinor != 1900 {
		t.Fatalf("product after re-enable: %+v %v", p, err)
	}
	if status, _ := serveStatus(app.Handler, "GET", "/product/kept-plan", nil); status != 200 {
		t.Fatalf("re-enabled /product/kept-plan: %d", status)
	}
}

// The addons.<name>.enabled setting is the module's own activation
// (an admin toggle writes it); config "enabled" does not override it, and a
// missing key keeps the module on.
func TestOptionalBuiltinsSettingGate(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	app := bootData(t, data, "")
	if err := app.Deps.Settings.Set(ctx, kernel.AddonSettingKey("membership"), false); err != nil {
		t.Fatal(err)
	}
	app.Close()

	app = bootData(t, data, `{"membership":"enabled","saas":"auto"}`)
	states := moduleStates(app)
	if s := states["membership"]; s.Enabled || s.Mode != "enabled" {
		t.Fatalf("setting off + config enabled: %+v, want disabled (config never forces a module on)", s)
	}
	if s := states["saas"]; !s.Enabled || s.Mode != "auto" {
		t.Fatalf("saas with mode auto and no setting: %+v, want enabled", s)
	}
	if status, _ := serveStatus(app.Handler, "GET", "/membership", nil); status != 404 {
		t.Fatalf("/membership while off: %d", status)
	}
	if err := app.Deps.Settings.Set(ctx, kernel.AddonSettingKey("membership"), true); err != nil {
		t.Fatal(err)
	}
	app.Close()

	app = bootData(t, data, "")
	defer app.Close()
	if s := moduleStates(app)["membership"]; !s.Enabled {
		t.Fatalf("setting on: %+v", s)
	}
	// Unauthenticated /membership redirects to the account page: the route exists again.
	if status, _ := serveStatus(app.Handler, "GET", "/membership", nil); status != 303 {
		t.Fatalf("/membership after re-enable: %d, want 303", status)
	}
}
