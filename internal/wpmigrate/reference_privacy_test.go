package wpmigrate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

func TestSafeReferenceRedactsExternalCapabilityPaths(t *testing.T) {
	inputs := []string{
		"https://user:credential@cdn.test/capability/private-path-token/a.png?signature=query-token#fragment-token",
		"//cdn.test/capability/private-path-token/a.png?signature=query-token",
		"https://cdn.test/%70rivate-path-token/a.png",
	}
	for _, raw := range inputs {
		clean := safeReference(raw)
		if !strings.Contains(clean, "cdn.test") || strings.Contains(clean, "private-path-token") || strings.Contains(clean, "credential") || strings.Contains(clean, "query-token") || strings.Contains(clean, "fragment-token") {
			t.Errorf("external capability reference was not redacted")
		}
		if clean != safeReference(raw) || clean != safeReference(clean) {
			t.Fatal("reference redaction must be stable")
		}
	}
	if safeReference("https://cdn.test/first-token/a.png") == safeReference("https://cdn.test/second-token/a.png") {
		t.Fatal("different source paths collapsed into one reference")
	}
	if got := safeReference("/media/2026/10/local.png?signature=private"); got != "/media/2026/10/local.png" {
		t.Fatalf("native media path lost: %s", got)
	}
}

func TestValidationExternalPathTokensAreNeverReturned(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	e.run(id)
	it := e.item(id, "content", "post", "70")
	raw := `<img src="https://cdn.test/capability/private-path-token/a.png?signature=query-token"><img src="//cdn.test/capability/second-path-token/a.png"><img src="capability/relative-path-token/a.png"><img src="/protected/root-path-token/a.png">`
	if _, err := e.svc.db.Exec(`UPDATE content SET body=? WHERE id=?`, raw, it.LocalID); err != nil {
		t.Fatal(err)
	}
	rep, err := e.svc.Validate(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	browser := e.browser()
	response, page := browser.do("GET", "/admin/migrations/"+id+"/validate", nil, "")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	for _, text := range []string{string(encoded), rep.Markdown, page} {
		for _, secret := range []string{"private-path-token", "second-path-token", "query-token", "relative-path-token", "root-path-token"} {
			if strings.Contains(text, secret) {
				t.Errorf("validation surface exposes a synthetic URL capability")
			}
		}
	}
	client := apitest.New(t, e.app, apitest.Options{Scopes: []string{Permission}})
	status, result := client.Get("/v1/migrations/" + id + "/report")
	if status != 200 {
		t.Fatal(status)
	}
	public, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), "private-path-token") || strings.Contains(string(public), "second-path-token") || strings.Contains(string(public), "query-token") || strings.Contains(string(public), "relative-path-token") || strings.Contains(string(public), "root-path-token") {
		t.Fatal("REST report exposes a synthetic URL capability")
	}
	var count int
	for _, issue := range rep.UnverifiedChecks {
		if issue.Reason == "external_media_not_probed" {
			count++
			if issue.OldRef == "" || issue.NewRef == "" {
				t.Fatal("safe reference evidence was omitted")
			}
		}
	}
	if count != 2 {
		t.Fatalf("external references=%d want2; local references must be probed", count)
	}
	if rep.MediaChecked < 2 {
		t.Fatal("root-relative and document-relative media were not probed")
	}
}
