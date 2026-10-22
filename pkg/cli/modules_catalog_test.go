package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/kernel"
)

func catalogOf(t *testing.T, h http.Handler, bearer http.Header) admin.ModuleCatalog {
	t.Helper()
	status, body := serveStatus(h, "GET", "/api/v1/system/modules", bearer)
	if status != 200 {
		t.Fatalf("GET /system/modules: %d %s", status, body)
	}
	var out struct {
		Data admin.ModuleCatalog `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out.Data
}

func entry(t *testing.T, cat admin.ModuleCatalog, id string) admin.CatalogEntry {
	t.Helper()
	i := slices.IndexFunc(cat.Entries, func(e admin.CatalogEntry) bool { return e.ID == id })
	if i < 0 {
		t.Fatalf("catalog lacks %s", id)
	}
	return cat.Entries[i]
}

func putJSON(h http.Handler, path, body string, header http.Header) (int, string) {
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// The catalog lists every module of this build with its kind, declared
// dependencies, observed registrations and activation; optional modules can
// be switched for the next boot through the API, and the switch is visible
// as pending until a restart applies it. Data and queued jobs survive.
func TestModuleCatalogListsBuildAndSwitchesOptionalModules(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	app := bootData(t, data, "")
	bearer := freshKey(t, app, 1)
	cat := catalogOf(t, app.Handler, bearer)
	if cat.Build == "" || cat.Coverage == "" {
		t.Fatalf("catalog header: %+v", cat)
	}
	for _, name := range kernel.BootOrder {
		if slices.Contains([]string{"db", "i18n", "audit", "jobs"}, name) {
			continue
		}
		e := entry(t, cat, name)
		if e.Source != admin.CatalogSourceBuiltIn || e.Version != cat.Build || e.Summary["ar"] == "" || e.Summary["en"] == "" || e.Name["en"] == "" {
			t.Errorf("%s: %+v", name, e)
		}
		if e.Kind != admin.CatalogKindCore && e.Kind != admin.CatalogKindFeature {
			t.Errorf("%s kind %q", name, e.Kind)
		}
		if e.Registered == nil || !e.State.Enabled {
			t.Errorf("%s not reported as booted with registrations", name)
		}
	}
	for _, svc := range []string{"forms", "storage", "backups", "mail"} {
		e := entry(t, cat, svc)
		if e.Kind != admin.CatalogKindService || e.Owner == "" || e.State.Status != "always_on" || e.Configuration.Status == "" {
			t.Errorf("service %s: %+v", svc, e)
		}
	}
	c := entry(t, cat, "commerce")
	if !c.State.Optional || !c.State.Switchable || c.State.PendingRestart || c.State.Desired == nil || !*c.State.Desired || c.State.Status != "enabled" {
		t.Fatalf("commerce state: %+v", c.State)
	}
	if !slices.Contains(c.Registered.JobKinds, commerce.JobProvision) || !slices.Contains(c.Registered.APIOperations, "GET /v1/commerce/products") || !slices.Contains(c.Permissions, commerce.PermManage) || !slices.Contains(c.Registered.ThemeKinds, "shop") {
		t.Fatalf("commerce registrations: %+v", c.Registered)
	}
	if !slices.Contains(c.Dependents, "saas") || !slices.Contains(c.Dependents, "wpmigrate") {
		t.Fatalf("commerce dependents: %v", c.Dependents)
	}
	if a := entry(t, cat, "auth"); a.State.Optional || a.State.Status != "always_on" || a.State.Desired != nil {
		t.Fatalf("auth state: %+v", a.State)
	}
	if s := entry(t, cat, "saas"); !slices.Contains(s.OptionalDependencies, "commerce") {
		t.Fatalf("saas should list commerce as an optional dependency: %+v", s)
	}
	if !slices.ContainsFunc(c.Messages, func(m admin.CatalogMessage) bool { return strings.Contains(m.Text["en"], "saas") }) {
		t.Fatalf("commerce should warn that turning it off affects saas: %v", messageTexts(c, "en"))
	}

	// Switching: always-on and unknown modules are refused; commerce is saved for the next boot.
	if status, _ := putJSON(app.Handler, "/api/v1/system/modules/auth/activation", `{"enabled":false}`, bearer); status != 409 {
		t.Fatalf("disable auth: %d", status)
	}
	if status, _ := putJSON(app.Handler, "/api/v1/system/modules/nope/activation", `{"enabled":false}`, bearer); status != 404 {
		t.Fatalf("disable unknown: %d", status)
	}
	// A missing, null or non-boolean "enabled" is refused and changes nothing.
	for _, body := range []string{`{}`, `{"enabled":null}`, `{"enabled":"false"}`, `{"enabled":0}`} {
		status, resp := putJSON(app.Handler, "/api/v1/system/modules/commerce/activation", body, bearer)
		if status != 422 && status != 400 {
			t.Fatalf("activation body %s: %d %s, want a validation error", body, status, resp)
		}
	}
	if on, err := kernel.AddonEnabled(ctx, app.Deps.DB, "commerce"); err != nil || !on {
		t.Fatalf("invalid bodies must not write the setting: on=%v err=%v", on, err)
	}
	if n := auditRows(t, app, "module.activation"); n != 0 {
		t.Fatalf("invalid bodies audited %d activations", n)
	}
	status, body := putJSON(app.Handler, "/api/v1/system/modules/commerce/activation", `{"enabled":false}`, bearer)
	if status != 200 {
		t.Fatalf("disable commerce: %d %s", status, body)
	}
	if n := auditRows(t, app, "module.activation"); n != 1 {
		t.Fatalf("activation audited %d times, want 1", n)
	}
	c = entry(t, catalogOf(t, app.Handler, bearer), "commerce")
	if !c.State.Enabled || c.State.Desired == nil || *c.State.Desired || !c.State.PendingRestart || c.State.Status != "pending_disable" {
		t.Fatalf("commerce after switch: %+v", c.State)
	}
	if !strings.Contains(strings.Join(messageTexts(c, "en"), " "), "restart") {
		t.Fatalf("pending restart message missing: %v", messageTexts(c, "en"))
	}
	if on, err := kernel.AddonEnabled(ctx, app.Deps.DB, "commerce"); err != nil || on {
		t.Fatalf("setting after switch: on=%v err=%v", on, err)
	}
	// Queue store work while it is still on; it must wait, not fail, after the restart.
	jobID, err := app.Deps.Jobs.Enqueue(ctx, commerce.JobMagicMail, map[string]string{"email": "kept@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()

	app = bootData(t, data, "")
	bearer = freshKey(t, app, 2)
	cat = catalogOf(t, app.Handler, bearer)
	c = entry(t, cat, "commerce")
	if c.State.Enabled || c.State.Status != "disabled" || c.State.PendingRestart || c.Registered != nil || !slices.Contains(c.Jobs.Kinds, commerce.JobMagicMail) {
		t.Fatalf("commerce after restart: %+v jobs=%+v", c.State, c.Jobs)
	}
	if c.Configuration.Status != "unknown" {
		t.Fatalf("configuration of a module that did not boot must be unknown: %+v", c.Configuration)
	}
	for _, dependent := range []string{"saas", "wpmigrate", "mcp", "theme"} {
		d := entry(t, cat, dependent)
		if !slices.ContainsFunc(d.Messages, func(m admin.CatalogMessage) bool {
			return m.Level == "warning" && strings.Contains(m.Text["en"], "Commerce is off") && m.Text["ar"] != ""
		}) {
			t.Errorf("%s lacks the commerce-off consequence: %v", dependent, messageTexts(d, "en"))
		}
	}
	job, err := app.Deps.Jobs.Get(ctx, jobID)
	if err != nil || job.Status != "queued" {
		t.Fatalf("queued store job after disable: %+v %v", job, err)
	}
	if c.Jobs.Queued+c.Jobs.Parked != 1 {
		t.Fatalf("store jobs attributed: %+v", c.Jobs)
	}
	// Enable again for the next boot.
	if status, body := putJSON(app.Handler, "/api/v1/system/modules/commerce/activation", `{"enabled":true}`, bearer); status != 200 {
		t.Fatalf("enable commerce: %d %s", status, body)
	}
	if c = entry(t, catalogOf(t, app.Handler, bearer), "commerce"); c.State.Status != "pending_enable" {
		t.Fatalf("after enable: %+v", c.State)
	}
	app.Close()

	app = bootData(t, data, "")
	defer app.Close()
	bearer = freshKey(t, app, 3)
	if c = entry(t, catalogOf(t, app.Handler, bearer), "commerce"); c.State.Status != "enabled" || c.Registered == nil {
		t.Fatalf("after re-enable boot: %+v", c.State)
	}
	if job, err := app.Deps.Jobs.Get(ctx, jobID); err != nil || job.ParkedReason != "" || (job.Status != "queued" && job.Status != "done" && job.Status != "failed") {
		t.Fatalf("store job released after enable: %+v %v", job, err)
	}
}

// Operator config "disabled" is reported as forced: the catalog shows the
// module off and not switchable, and the API refuses to switch it.
func TestModuleCatalogReportsConfigForcedModules(t *testing.T) {
	app := bootData(t, t.TempDir(), `{"membership":"disabled"}`)
	defer app.Close()
	bearer := freshKey(t, app, 1)
	m := entry(t, catalogOf(t, app.Handler, bearer), "membership")
	if m.State.Enabled || m.State.Switchable || m.State.Mode != "disabled" || m.State.Status != "disabled" {
		t.Fatalf("membership: %+v", m.State)
	}
	if status, _ := putJSON(app.Handler, "/api/v1/system/modules/membership/activation", `{"enabled":true}`, bearer); status != 409 {
		t.Fatalf("switching a forced module: %d", status)
	}
}

// freshKey returns an admin bearer key for a new user, so reboots on the
// same data dir can mint one each time.
func freshKey(t *testing.T, app *kernel.App, n int) http.Header {
	t.Helper()
	ctx := context.Background()
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	u, err := users.Create(ctx, fmt.Sprintf("catalog-%d@example.com", n), "Owner", "correct horse battery staple", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := keys.CreateKind(ctx, auth.KeyKindAPI, "catalog", u.ID, []string{"*"}, 10000, "")
	if err != nil {
		t.Fatal(err)
	}
	return http.Header{"Authorization": {"Bearer " + plain}}
}

func auditRows(t *testing.T, app *kernel.App, action string) int {
	t.Helper()
	var n int
	if err := app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func messageTexts(e admin.CatalogEntry, locale string) []string {
	out := []string{}
	for _, m := range e.Messages {
		out = append(out, m.Text[locale])
	}
	return out
}
