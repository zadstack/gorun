package admin_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestConnectionModelsRoutePermissionCSRFNoWrite(t *testing.T) {
	s := boot(t)
	s.login("models-editor@example.com", "editor", "en")
	if r, _ := s.post("/admin/settings/connections/models", nil); r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
	a := boot(t)
	a.login("models-owner@example.com", "admin", "ar")
	f := url.Values{"kind": {"ai"}, "provider": {"openai"}, "endpoint": {"https://127.0.0.1/v1"}, "token": {"never-echo-private"}}
	if r, _ := a.do("POST", "/admin/settings/connections/models", f); r.StatusCode != 403 {
		t.Fatal("missing CSRF allowed")
	}
	if r, b := a.post("/admin/settings/connections/models", f); r.StatusCode != 422 || strings.Contains(b, "never-echo-private") || !strings.Contains(b, "راجع") {
		t.Fatalf("validation/localization %d %s", r.StatusCode, b)
	}
	_, b := a.do("GET", "/admin/settings/connections", nil)
	mustContain(t, b, "data-model-discovery", "connection-models.js", "جلب الموديلات المتاحة")
	if strings.Count(b, "data-model-discovery") != 1 {
		t.Fatal("duplicate discovery controls")
	}
	rows, e := a.app.Deps.Settings.All(context.Background(), "connections.profile.")
	if e != nil || len(rows) != 0 {
		t.Fatal("discovery wrote settings")
	}
}
