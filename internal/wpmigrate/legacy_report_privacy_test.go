package wpmigrate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

func TestLegacyCachedReportRedactsReferencesWithoutProbesOrWrites(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	stamp := "2001-02-03T04:05:06Z"
	native := "/known-native-page"
	unsafe := "/protected/cached-private-token/a?sig=query-private-token#fragment-private-token"
	legacy := ValidateReport{MigrationID: id, GeneratedAt: stamp, Readiness: "ready", Markdown: "old cached-markdown-private-token", BrokenLinks: []LinkIssue{{ContentID: "fixture", URL: unsafe}}, MissingMedia: []LinkIssue{{URL: "/media/known-native.png?sig=query-private-token"}}, Errors: []ValidationIssue{{ContentID: "fixture", Kind: "link", Code: "broken_internal_link", OldRef: unsafe, NewRef: unsafe, Status: 404, CheckedAt: stamp}}, Warnings: []ValidationIssue{{Kind: "media", Code: "media_unavailable", OldRef: "https://user:credential-private-token@cdn.invalid/cached-path-private-token/a?sig=query-private-token", NewRef: "/media/known-native.png?sig=query-private-token", CheckedAt: stamp}}, RenderFailures: []PathStatus{{ContentID: "native", Path: native, Status: 503}}, UnverifiedChecks: []CheckIssue{{Kind: "link", Path: unsafe, OldRef: unsafe, NewRef: unsafe, Outcome: "unavailable", Reason: "request_failed", CheckedAt: stamp}, {Kind: "render", Path: native, NewRef: native, Outcome: "skipped", Reason: "check_disabled", CheckedAt: stamp}}}
	legacy.Errors = append(legacy.Errors, ValidationIssue{Kind: "render", OldRef: unsafe, NewRef: native, Code: "render_http_status", Status: 503, CheckedAt: stamp})
	if err := e.svc.setStats(e.ctx(), id, "validate", legacy); err != nil {
		t.Fatal(err)
	}
	e.svc.logEntry(e.ctx(), id, "info", "", "validate_report", map[string]any{"markdown": "cached-markdown-private-token", "broken_links": 7})
	var before, after, logBefore, logAfter string
	if err := e.svc.db.QueryRow(`SELECT meta_json FROM migration_log WHERE migration_id=? AND meta_json LIKE '%cached-markdown-private-token%'`, id).Scan(&logBefore); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.db.QueryRow(`SELECT stats_json||updated_at||status FROM migrations WHERE id=?`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	e.svc.check = func(context.Context, string) int { t.Error("cached report performed a new probe"); return 0 }
	report, err := e.svc.report(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if report.GeneratedAt != stamp || report.Errors[0].CheckedAt != stamp || report.UnverifiedChecks[0].CheckedAt != stamp {
		t.Fatal("cached evidence timestamps changed")
	}
	if report.Readiness != "unverified" {
		t.Error("legacy incomplete checks claimed verified readiness")
	}
	if report.Errors[1].NewRef != native || report.RenderFailures[0].Path != native || report.UnverifiedChecks[1].Path != native || report.MissingMedia[0].URL != "/media/known-native.png" || report.Warnings[0].NewRef != "/media/known-native.png" {
		t.Error("known native references lost")
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	client := apitest.New(t, e.app, apitest.Options{Scopes: []string{Permission}})
	status, data := client.Get("/v1/migrations/" + id + "/report")
	if status != 200 {
		t.Fatal(status)
	}
	public, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	status, logs := client.Get("/v1/migrations/" + id + "/log?limit=1000")
	if status != 200 {
		t.Fatal(status)
	}
	logJSON, err := json.Marshal(logs)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := e.svc.Log(e.ctx(), id, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Message == "validate_report" && entry.Meta["broken_links"] == float64(7) {
			found = true
			if _, exists := entry.Meta["markdown"]; exists || entry.Meta["current_report_path"] != "/api/v1/migrations/"+id+"/report" || entry.ID == "" || entry.At == "" {
				t.Error("legacy validation log was not safely referenced")
			}
		}
	}
	if !found {
		t.Fatal("historical log counters lost")
	}
	b := e.browser()
	response, page := b.do("GET", "/admin/migrations/"+id+"/validate", nil, "")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	response, markdown := b.do("GET", "/admin/migrations/"+id+"/report.md", nil, "")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	for _, key := range []string{"validate.scope.stored_legacy_report", "validate.limitation.legacy_report_checks_not_recorded", "check.kind.report", "check.reason.legacy_report_checks_not_recorded"} {
		if strings.Contains(page, "wpmigrate."+key) {
			t.Error("legacy UI exposes untranslated key")
		}
	}
	for _, surface := range []string{string(raw), report.Markdown, string(public), string(logJSON), page, markdown} {
		for _, token := range []string{"cached-private-token", "cached-path-private-token", "cached-markdown-private-token", "query-private-token", "fragment-private-token", "credential-private-token"} {
			if strings.Contains(surface, token) {
				t.Error("cached report surface exposes synthetic capability")
			}
		}
	}
	if err := e.svc.db.QueryRow(`SELECT stats_json||updated_at||status FROM migrations WHERE id=?`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.db.QueryRow(`SELECT meta_json FROM migration_log WHERE migration_id=? AND meta_json LIKE '%cached-markdown-private-token%'`, id).Scan(&logAfter); err != nil {
		t.Fatal(err)
	}
	if logBefore != logAfter {
		t.Fatal("reading logs rewrote historical metadata")
	}
	if before != after {
		t.Fatal("reading the legacy report rewrote stored evidence")
	}
}

func TestLegacyEmptyReportRemainsUnverified(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	legacy := ValidateReport{MigrationID: id, GeneratedAt: "2001-02-03T04:05:06Z", Markdown: "old"}
	if err := e.svc.setStats(e.ctx(), id, "validate", legacy); err != nil {
		t.Fatal(err)
	}
	report, err := e.svc.report(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if report.Readiness != "unverified" || len(report.UnverifiedChecks) != 1 || report.UnverifiedChecks[0].Code != "legacy_report_checks_not_recorded" || report.GeneratedAt != legacy.GeneratedAt {
		t.Fatal("legacy missing checks implied acceptance")
	}
}
