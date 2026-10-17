package admin_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/diagnostics"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestErrorInventoryOwnerAndRedactedSources(t *testing.T) {
	s := boot(t)
	s.login("errors-owner@example.invalid", "admin", "en")
	ctx := context.Background()
	s.app.Deps.Log.Error("admin: private child details", "token", "secret_API_key", "err", "private body")
	c, _ := kernel.Get[*diagnostics.Collector](s.app.Deps, diagnostics.ServiceName)
	c.Close()
	now := db.Now()
	_, err := s.app.Deps.DB.Exec(`INSERT INTO jobs(id,kind,payload_json,status,attempts,max_attempts,last_error,run_at,created_at,updated_at) VALUES(?,?,'{"password":"private_payload"}','dead',1,1,'secret_provider_error',?,?,?)`, db.NewID(), "site.form.notify", now, now, now)
	if err != nil {
		t.Fatal(err)
	}
	s.app.Deps.Audit.Record(ctx, "user.login_failed", "user", "private-user-id", map[string]string{"email": "private@example.invalid", "password": "private_audit"})
	s.app.Deps.Audit.Record(ctx, "mcp.call", "tool", "private-tool-name", map[string]any{"ok": false, "error": "private_mcp_error"})
	s.app.Deps.Audit.Record(ctx, "mcp.call", "tool", "private-tool-name", map[string]any{"ok": true})
	s.app.Deps.Audit.Record(ctx, "security.ip_blocked", "ip", "private-address", nil)
	resp, body := s.do("GET", "/admin/system/errors", nil)
	if resp.StatusCode != 200 {
		t.Fatal("status", resp.StatusCode)
	}
	for _, v := range []string{"secret_API_key", "private body", "private_payload", "secret_provider_error", "private@example.invalid", "private_audit", "private-user-id", "private-tool-name", "private_mcp_error"} {
		if strings.Contains(body, v) {
			t.Fatal("leak", v)
		}
	}
	for _, v := range []string{"/admin/system/jobs?status=dead", "/admin/agents/audit?action=user.login_failed", "/admin/agents/audit?action=security.ip_blocked", "/admin/system/health"} {
		if !strings.Contains(body, v) {
			t.Fatal("missing safe action link", v)
		}
	}
	for _, v := range []string{"Runtime", "Jobs", "Audit", "Recorded", "Failed sign-in"} {
		if !strings.Contains(body, v) {
			t.Fatal("missing source", v)
		}
	}
	if _, err := s.app.Deps.DB.Exec(`INSERT OR IGNORE INTO role_permissions(role,permission) VALUES('editor','system.health')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(ctx, s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	editor := s.login("errors-editor@example.invalid", "editor", "en")
	for _, n := range s.app.Deps.Admin.Nav(auth.WithUser(ctx, editor)) {
		if n.ID == "system.errors" {
			t.Fatal("owner-only link visible to delegated staff")
		}
	}
	if resp, _ := s.do("GET", "/admin/system/errors", nil); resp.StatusCode != 403 {
		t.Fatal("editor allowed", resp.StatusCode)
	}
}
func TestErrorInventoryFiltersAndNoWrites(t *testing.T) {
	s := boot(t)
	s.login("filters-owner@example.invalid", "admin", "en")
	s.app.Deps.Log.Error("mailer: private provider error")
	c, _ := kernel.Get[*diagnostics.Collector](s.app.Deps, diagnostics.ServiceName)
	c.Close()
	r, b := s.do("GET", "/admin/system/errors?source=runtime&category=mailer", nil)
	if r.StatusCode != 200 || !strings.Contains(b, "Runtime error") {
		t.Fatal("runtime filter", r.StatusCode)
	}
	r, _ = s.do("GET", "/admin/system/errors?source=unknown", nil)
	if r.StatusCode != 400 {
		t.Fatal("invalid filter", r.StatusCode)
	}
	r, _ = s.do("GET", "/admin/system/errors?since=not-a-date", nil)
	if r.StatusCode != 400 {
		t.Fatal("invalid date", r.StatusCode)
	}
	future := time.Now().Add(48 * time.Hour).Format(time.DateOnly)
	r, b = s.do("GET", "/admin/system/errors?source=runtime&since="+future, nil)
	if r.StatusCode != 200 || strings.Contains(b, "Runtime error</td>") {
		t.Fatal("date filter")
	}
}
