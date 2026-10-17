package admin_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/mailer"
)

func TestConnectionsMetricoolSummaryReadOnlyStates(t *testing.T) {
	s := boot(t)
	s.login("summary@example.invalid", "admin", "en")
	calls := metricoolFake(t, `{}`)
	secret, err := mailer.EncryptSecret(s.app.Deps.Config.SecretKey, "metricool-summary-private-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ secret, status, want string }{{"", "", "not_configured"}, {secret, "", "untested"}, {secret, "pass", "pass"}, {secret, "fail", "fail"}, {"", "pass", "not_configured"}} {
		cfg := map[string]string{"version": "summary-version-private", "secret": tc.secret, "user_id": "982347651", "blog_id": "982347652", "test_status": tc.status, "tested_at": "2026-10-12T00:00:00Z"}
		if err = s.app.Deps.Settings.Set(context.Background(), "metricool.config", cfg); err != nil {
			t.Fatal(err)
		}
		before, _ := s.app.Deps.Settings.All(context.Background(), "")
		beforeRaw, _ := json.Marshal(before)
		var auditBefore int
		s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&auditBefore)
		resp, body := s.do("GET", "/admin/settings/connections", nil)
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
		mustContain(t, body, `data-metricool-summary`, `data-metricool-state="`+tc.want+`"`, `href="/admin/settings/metricool"`, `data-metricool-drafts`)
		mustNotContain(t, body, "metricool-summary-private-token", secret, "982347651", "982347652", "summary-version-private")
		if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("summary cached")
		}
		after, _ := s.app.Deps.Settings.All(context.Background(), "")
		afterRaw, _ := json.Marshal(after)
		var auditAfter int
		s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&auditAfter)
		if string(beforeRaw) != string(afterRaw) || auditBefore != auditAfter || *calls != 0 {
			t.Fatal("GET changed settings/audit or called provider")
		}
	}
	other := boot(t)
	other.login("other@example.invalid", "admin", "en")
	_, body := other.do("GET", "/admin/settings/connections", nil)
	mustContain(t, body, `data-metricool-state="not_configured"`)
}

func TestConnectionsMetricoolDraftLinkRequiresPublishing(t *testing.T) {
	s := boot(t)
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO role_permissions(role,permission) VALUES('contributor','settings.manage')`); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(context.Background(), s.app.Deps.DB); err != nil {
		t.Fatal(err)
	}
	s.login("settings-only@example.invalid", "contributor", "ar")
	resp, body := s.do("GET", "/admin/settings/connections", nil)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	mustContain(t, body, `data-metricool-summary`, `href="/admin/settings/metricool"`)
	mustNotContain(t, body, `data-metricool-drafts`)
}
