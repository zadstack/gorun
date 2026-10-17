package wpmigrate

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

func TestValidationMediaHTTPAndSafeSummary(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	it := e.item(id, "content", "post", "70")
	body := `<picture><source srcset='/media/missing-source.png?signature=private 2x'><img src='/media/missing.png?token=private' srcset='/media/missing.png?token=private 1x, /media/missing-wide.png 2x'></picture><img src='https://external.test/a.png?secret=private'>`
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, body, it.LocalID); err != nil {
		t.Fatal(err)
	}
	e.svc.cfg.BaseURL = e.srv.URL
	e.svc.check = e.svc.httpCheck
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.MissingMedia) != 3 {
		t.Fatalf("missing=%d: %+v", len(rep.MissingMedia), rep.MissingMedia)
	}
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	// Decode using generic JSON to check the public snake_case contract.
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["readiness"] != "blocked" {
		t.Fatalf("readiness=%v", fields["readiness"])
	}
	mediaErrors := 0
	for _, value := range fields["errors"].([]any) {
		issue := value.(map[string]any)
		if issue["kind"] != "media" {
			continue
		}
		mediaErrors++
		if issue["content_id"] != it.LocalID || issue["code"] != "media_http_status" || issue["checked_at"] == "" || issue["old_ref"] == "" || issue["new_ref"] == "" || issue["status"] != float64(http.StatusNotFound) {
			t.Fatalf("incomplete evidence: %v", issue)
		}
	}
	if mediaErrors != 3 {
		t.Fatalf("media errors=%d", mediaErrors)
	}
	if strings.Contains(string(raw), "private") || strings.Contains(rep.Markdown, "private") {
		t.Fatal("report leaks URL credentials")
	}
	c := apitest.New(t, e.app, apitest.Options{Scopes: []string{Permission}})
	status, response := c.Get("/v1/migrations/" + id + "/report")
	if status != http.StatusOK || apitest.Data(response)["readiness"] != "blocked" || len(apitest.Data(response)["errors"].([]any)) != len(rep.Errors) {
		t.Fatal("REST report does not expose the stored validation result")
	}
	if !strings.Contains(rep.Markdown, "Readiness: blocked") {
		t.Fatal("Markdown omits readiness")
	}
	b := e.browser()
	resp, page := b.do("GET", "/admin/migrations/"+id+"/validate", nil, "")
	if resp.StatusCode != 200 || !strings.Contains(page, `data-readiness="blocked"`) || !strings.Contains(page, "media_http_status") || strings.Contains(page, "token=private") || strings.Contains(page, "signature=private") {
		t.Fatalf("wizard differs from JSON summary: status=%d", resp.StatusCode)
	}
}

func TestValidationDetectsMissingFeaturedVariantFile(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	it := e.item(id, "content", "post", "70")
	c, err := e.svc.content.Get(e.ctx(), it.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	m, err := ms.Get(e.ctx(), c.FeaturedMediaID)
	if err != nil {
		t.Fatal(err)
	}
	missing := strings.TrimSuffix(m.URL, ".png") + "-missing.png"
	variants, err := json.Marshal(map[string]media.Variant{"medium": {URL: missing, Width: 300, Height: 200}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.db.Exec(`UPDATE media SET variants_json=? WHERE id=?`, string(variants), m.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.cfg.BaseURL, e.svc.check = e.srv.URL, e.svc.httpCheck
	if status := e.svc.httpCheck(e.ctx(), m.URL); status != http.StatusOK {
		t.Fatalf("healthy featured media check returned HTTP %d", status)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range rep.MissingMedia {
		if issue.URL == missing && issue.ContentID == c.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing featured variant not detected: %+v", rep.MissingMedia)
	}
}

func TestValidationDetectsDeletedMediaFileDespiteRecord(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	att := e.item(id, "media", "attachment", "862")
	m, err := e.svc.media.Get(e.ctx(), att.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.cfg.BaseURL, e.svc.check = e.srv.URL, e.svc.httpCheck
	if status := e.svc.httpCheck(e.ctx(), m.URL); status != http.StatusOK {
		t.Fatal(status)
	}
	if err := os.Remove(filepath.Join(e.app.Deps.Config.DataDir, "media", m.StorageKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.media.Get(e.ctx(), att.LocalID); err != nil {
		t.Fatal("test must retain the media row", err)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" {
		t.Fatal(rep.Readiness)
	}
	missingForContent := false
	for _, issue := range rep.Errors {
		if issue.Kind == "media" && issue.Status == http.StatusNotFound && issue.ContentID != "" && issue.NewRef == m.URL {
			missingForContent = true
		}
	}
	if !missingForContent {
		t.Fatalf("missing file hidden by media row or successful page: %+v", rep.Errors)
	}
}

func TestValidationMediaScopeAndLocalProbeBoundaries(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	it := e.item(id, "content", "post", "70")
	e.svc.cfg.BaseURL = e.srv.URL
	m, err := e.svc.media.Get(e.ctx(), e.item(id, "media", "attachment", "862").LocalID)
	if err != nil {
		t.Fatal(err)
	}
	body := `<img src="` + e.srv.URL + m.URL + `?signed=private"><img src="https://external.test/a.png?key=private"><img src="https://%broken"><img src="data:image/png;base64,private">`
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, body, it.LocalID); err != nil {
		t.Fatal(err)
	}
	probe := e.svc.httpCheck
	e.svc.check = func(ctx context.Context, path string) int {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			t.Fatalf("unsafe probe: %s", path)
		}
		if strings.Contains(path, "?") && path != m.URL+"?signed=private" {
			t.Fatalf("unexpected query probe: %s", path)
		}
		return probe(ctx, path)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.MissingMedia) != 0 || len(rep.Errors) != 0 || rep.Readiness != "unverified" {
		t.Fatalf("wrong media evidence: %+v", rep)
	}
	reasons := map[string]int{}
	for _, issue := range rep.UnverifiedChecks {
		reasons[issue.Reason]++
	}
	if reasons["external_media_not_probed"] != 1 || reasons["invalid_media_reference"] != 2 {
		t.Fatal(reasons)
	}
	if len(rep.Scope) == 0 || len(rep.Limitations) != 1 || rep.Limitations[0] != "semantic_reconciliation_not_performed" {
		t.Fatal("readiness scope omitted")
	}
}

func TestValidationPendingItemsCannotBeReady(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	if _, err := e.svc.db.Exec(`UPDATE content SET body=''`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.db.Exec(`UPDATE migration_items SET status='pending' WHERE migration_id=? AND kind='product'`, id); err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "unverified" {
		t.Fatal(rep.Readiness)
	}
	incomplete := 0
	for _, issue := range rep.UnverifiedChecks {
		if issue.Reason == "import_incomplete" {
			incomplete++
		}
	}
	if incomplete != 2 {
		t.Fatalf("incomplete imports=%d", incomplete)
	}
}

func TestValidationMediaRecordFailuresAreExplicit(t *testing.T) {
	for _, mode := range []string{"missing", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			e := newEnv(t)
			id := e.wxrMigration(Options{})
			e.run(id)
			if mode == "missing" {
				if _, err := e.svc.db.Exec(`DELETE FROM media`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := e.svc.db.Exec(`ALTER TABLE media RENAME TO fixture_unavailable_media`); err != nil {
					t.Fatal(err)
				}
			}
			rep, err := e.svc.Validate(e.ctx(), id)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			if mode == "missing" {
				for _, issue := range rep.Errors {
					if issue.Code == "media_record_missing" {
						found = true
					}
				}
				if rep.Readiness != "blocked" {
					t.Fatal(rep.Readiness)
				}
			} else {
				for _, issue := range rep.UnverifiedChecks {
					if issue.Reason == "media_unavailable" && issue.CheckedAt != "" {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("missing %s record evidence", mode)
			}
			if strings.Contains(rep.Markdown, "no such table") {
				t.Fatal("raw database failure leaked into report")
			}
		})
	}
}

func TestValidationReadinessDistinguishesWarningsAndConfirmedErrors(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	if _, err := e.svc.db.Exec(`UPDATE content SET body=''`); err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "warnings" || len(rep.Warnings) != 2 || len(rep.Errors) != 0 || len(rep.UnverifiedChecks) != 0 {
		t.Fatalf("skipped imports mislabeled: %+v", rep)
	}
	if _, err := e.svc.db.Exec(`UPDATE migration_items SET status='failed',error='secret backend exception' WHERE migration_id=? AND kind='product'`, id); err != nil {
		t.Fatal(err)
	}
	rep, err = e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Readiness != "blocked" || len(rep.Errors) != 2 || strings.Contains(rep.Markdown, "secret backend") {
		t.Fatalf("failed import evidence: %+v", rep.Errors)
	}
}

func TestValidationUnavailableMediaPreventsReadiness(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	e.svc.check = nil
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rep)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["readiness"] != "unverified" || len(rep.MissingMedia) != 0 {
		t.Fatalf("unavailable resources counted as ready or missing: %s", raw)
	}
	found := false
	for _, issue := range rep.UnverifiedChecks {
		if issue.Kind == "media" && issue.Reason == "check_disabled" {
			found = true
		}
	}
	if !found {
		t.Fatal("unavailable media checks omitted")
	}
}
