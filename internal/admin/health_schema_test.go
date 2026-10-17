package admin_test

import (
	"strings"
	"testing"
)

// System Health shows a "schema" check that turns warn when the database
// holds migrations this binary does not know (a newer binary ran).
func TestHealthSchemaCheckWarnsWhenSchemaIsAhead(t *testing.T) {
	s := boot(t)
	u := s.createUser("api@example.com", "admin", "en")
	check := func() map[string]any {
		t.Helper()
		_, out := apiCall(t, s, u, "GET", "/api/v1/system/health", "")
		checks, _ := out["data"].(map[string]any)["checks"].([]any)
		for _, c := range checks {
			if m, _ := c.(map[string]any); m["name"] == "schema" {
				return m
			}
		}
		t.Fatalf("no schema check in %v", checks)
		return nil
	}
	if c := check(); c["status"] != "ok" {
		t.Fatalf("fresh database: %v", c)
	}
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO schema_migrations(module, name, applied_at) VALUES ('future', '0001_x.sql', '2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	c := check()
	if c["status"] != "warn" || !strings.Contains(c["detail"].(string), "future/0001_x.sql") {
		t.Fatalf("schema ahead: %v", c)
	}
}
