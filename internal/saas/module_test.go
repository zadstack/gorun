package saas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api/apitest"
)

// This uses actual kernel Boot, auth/API middleware, migrations and catalog loader.
func TestModuleKernelBootCatalogAPIAndHealth(t *testing.T) {
	mod := New()
	app := apitest.Boot(t, mod)
	if got := app.Deps.I18n.T("ar", "saas.nav"); got != "التطبيقات والاشتراكات" {
		t.Fatalf("catalog: %q", got)
	}
	client := apitest.NewClient(t, app)
	req := httptest.NewRequest("GET", "/api/v1/saas", nil)
	req.Header.Set("Authorization", "Bearer "+client.Token)
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("authenticated API: %d %s", w.Code, w.Body.String())
	}
	var envelope map[string]any
	if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope["data"] == nil {
		t.Fatalf("envelope: %s", w.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/v1/saas", nil)
	w = httptest.NewRecorder()
	app.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
	w = httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/saas", nil))
	if w.Code != 303 {
		t.Fatalf("public sign-in: %d", w.Code)
	}
	_, e := app.Deps.DB.Exec(`INSERT INTO saas_apps(id,public_id,name,slug,status,mode,created_at,updated_at) VALUES('import-app','pub','Imported','imported','active','external','','');INSERT INTO saas_key_versions(id,public_id,app_id,status,created_at,updated_at) VALUES('import-key','key-pub','import-app','active','','');INSERT INTO saas_entitlements(id,public_id,app_id,subject_type,external_id,plan_id,status,created_at,updated_at) VALUES('import-ent','ent-pub','import-app','user','unknown','pro','active','','');`)
	if e != nil {
		t.Fatal(e)
	}
	checks := []admin.HealthCheck{}
	_, e = app.Deps.Hooks.Apply(context.Background(), admin.HealthCheckFilter, &checks)
	if e != nil {
		t.Fatal(e)
	}
	if len(checks) != 3 {
		t.Fatalf("actual gaps: %+v", checks)
	}
	for _, c := range checks {
		if c.Status != admin.CheckWarn || c.Detail == "" || strings.Contains(c.Detail, "secret_ciphertext") {
			t.Fatalf("unsafe health: %+v", c)
		}
	}
}
