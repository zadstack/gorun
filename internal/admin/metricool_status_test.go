package admin_test

import (
	"net/url"
	"strings"
	"testing"
)

func TestMetricoolConnectionStatusRequiresCredentials(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	check := func(want, unwanted string) {
		t.Helper()
		r, body := s.do("GET", "/admin/settings/metricool", nil)
		if r.StatusCode != 200 || !strings.Contains(body, want) || strings.Contains(body, unwanted) {
			t.Fatalf("connection status should contain %q and exclude %q", want, unwanted)
		}
	}
	check("Not configured", "Saved · not tested")
	configureMetricool(t, s)
	check("Saved · not tested", "Not configured")
	c := metricoolConfigValue(t, s)
	if r, _ := s.post("/admin/settings/metricool", url.Values{"version": {c["version"].(string)}, "user_id": {"12"}, "blog_id": {"23"}, "clear_secret": {"on"}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	check("Not configured", "Saved · not tested")
}
