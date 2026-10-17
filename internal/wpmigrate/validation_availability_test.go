package wpmigrate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/db"
)

func unverifiedChecks(t *testing.T, rep *ValidateReport) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["unverified_checks"] == nil {
		t.Fatal("report omits unverified checks")
	}
	var checks []map[string]any
	if err = json.Unmarshal(fields["unverified_checks"], &checks); err != nil {
		t.Fatal(err)
	}
	return checks
}

func TestValidationReportsUnavailableAndSkippedHTTP(t *testing.T) {
	for _, mode := range []string{"unreachable", "empty_base", "disabled", "available"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			id := e.wxrMigration(Options{})
			if status := e.run(id); status.Status != StatusValidated {
				t.Fatal(status.Status)
			}
			e.svc.check = e.svc.httpCheck
			switch mode {
			case "unreachable":
				closed := httptest.NewServer(http.NotFoundHandler())
				e.svc.cfg.BaseURL = closed.URL
				closed.Close()
			case "empty_base":
				e.svc.cfg.BaseURL = ""
			case "disabled":
				e.svc.check = nil
			case "available":
				e.svc.cfg.BaseURL = e.srv.URL
			}
			rep, err := e.svc.Validate(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			checks := unverifiedChecks(t, rep)
			if mode == "available" {
				if rep.RenderChecked != 3 || len(rep.RenderFailures) != 0 || len(checks) != 0 || len(rep.BrokenLinks) != 1 {
					t.Fatalf("known HTTP report: checked=%d failed=%d unverified=%d broken=%d", rep.RenderChecked, len(rep.RenderFailures), len(checks), len(rep.BrokenLinks))
				}
				return
			}
			if rep.RenderChecked != 0 || len(rep.RenderFailures) != 0 || len(rep.BrokenLinks) != 0 {
				t.Fatalf("unreceived HTTP counted as verified or broken: %+v", rep)
			}
			if len(checks) != 6 {
				t.Fatalf("unverified checks=%d, want three renders, one link and two media references", len(checks))
			}
			outcome, reason := "skipped", "no_base_url"
			if mode == "unreachable" {
				outcome, reason = "unavailable", "request_failed"
			}
			if mode == "disabled" {
				reason = "check_disabled"
			}
			kinds := map[string]int{}
			for _, check := range checks {
				kinds[check["kind"].(string)]++
				if check["outcome"] != outcome || check["reason"] != reason || check["path"] == "" {
					t.Fatalf("incomplete check metadata: %v", check)
				}
			}
			if kinds["render"] != 3 || kinds["link"] != 1 || kinds["media"] != 2 || rep.MediaChecked != 0 || rep.Readiness != "unverified" {
				t.Fatalf("incomplete unavailable evidence: kinds=%v media_checked=%d readiness=%s", kinds, rep.MediaChecked, rep.Readiness)
			}
			if !strings.Contains(rep.Markdown, "Not verified") {
				t.Fatal("Markdown omits unverified checks")
			}
			stored, err := e.svc.report(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			if len(unverifiedChecks(t, stored)) != 6 {
				t.Fatal("availability evidence not stored")
			}
			status, err := e.svc.Status(e.ctx(), id)
			if err != nil || status.Status != StatusValidated {
				t.Fatal("report generation changed migration completion semantics")
			}
		})
	}
}

func TestMigrationHealthFailsWhenQueriesUnavailable(t *testing.T) {
	e := newEnv(t)
	isolated, err := db.Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close()
	e.svc.db = isolated
	if hc := e.svc.healthCheck(e.ctx()); hc.Status != admin.CheckFail {
		t.Fatalf("missing migrations table falsely healthy: %+v", hc)
	}
	if _, err := isolated.Exec(`CREATE TABLE migrations(id TEXT,status TEXT,created_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if hc := e.svc.healthCheck(e.ctx()); hc.Status != admin.CheckOK {
		t.Fatalf("empty history should be healthy: %+v", hc)
	}
	if _, err := isolated.Exec(`INSERT INTO migrations VALUES ('migration','validated','2026-10-12')`); err != nil {
		t.Fatal(err)
	}
	if hc := e.svc.healthCheck(e.ctx()); hc.Status != admin.CheckFail {
		t.Fatalf("missing items table falsely healthy: %+v", hc)
	}
}

func TestValidationWizardShowsUnverifiedPaths(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	e.svc.check = nil
	// The wizard uses the originally registered service; store the additive
	// report through the same database used by the synchronous test runner.
	if _, err := e.svc.Validate(e.ctx(), id); err != nil {
		t.Fatal(err)
	}
	b := e.browser()
	resp, page := b.do("GET", "/admin/migrations/"+id+"/validate", nil, "")
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	must(t, page, `id="val-unverified"`, `data-outcome="skipped"`, safeSourceReference("/pricing/"))
}

func TestUnavailableLocalLinkDoesNotHideWordPressLink(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	var contentID string
	if err := e.svc.db.QueryRow(`SELECT local_id FROM migration_items WHERE migration_id=? AND kind='content' AND status='done' LIMIT 1`, id).Scan(&contentID); err != nil {
		t.Fatal(err)
	}
	body := `<a href="/unknown">local</a><a href="` + e.wp.URL() + `/unknown">source</a>`
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, body, contentID); err != nil {
		t.Fatal(err)
	}
	e.svc.check = nil
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range rep.BrokenLinks {
		if issue.URL == safeReference(e.wp.URL()+"/unknown") {
			found = true
		}
	}
	if !found {
		t.Fatal("a retained WordPress link was mislabeled as an unavailable local HTTP check")
	}
}
