package site_test

import (
	"github.com/zadstack/gorun/internal/content"
	"net/url"
	"strings"
	"testing"
)

func TestFormDirectoryPublishedSourcesAndSanitizedSubmission(t *testing.T) {
	e := boot(t)
	e.setting("site.locale", "en")
	e.setting("forms.notify", false)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Public contact"})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "write", Title: "Arabic form", Locale: "ar", Template: "builder", Body: `<section class="gp-section gp-contact"></section>`})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "secret", Title: "Draft form", Status: "draft", Template: "builder", Body: `<section class="gp-contact"></section>`})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "plain", Title: "Plain page"})
	parent := e.fake.Content.Add(content.Item{Type: "page", Slug: "private-parent", Title: "Private parent", Status: "private"})
	e.fake.Content.Add(content.Item{Type: "page", Slug: "child-form", Title: "Unreachable child", ParentID: parent.ID, Template: "builder", Body: `<section class="gp-contact"></section>`})
	_, page := e.get("/contact")
	form := url.Values{"_csrf": {csrfFrom(t, page)}, "name": {"Source test"}, "email": {"qa@example.invalid"}, "message": {"Source test"}, "_back": {"/contact?email=private@example.invalid#secret"}}
	if r, _ := e.do("POST", "/forms/contact", form); r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	var source string
	if err := e.app.Deps.DB.QueryRow(`SELECT page FROM form_submissions WHERE message='Source test'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "/contact" {
		t.Fatalf("source leaks query: %q", source)
	}
	// Legacy rows may already contain parameters; the detail and CSV must strip them too.
	var id string
	if err := e.app.Deps.DB.QueryRow(`SELECT id FROM form_submissions WHERE message='Source test'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Deps.DB.Exec(`UPDATE form_submissions SET page='/contact?old-private-token=123' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	e.loginAdmin()
	for _, path := range []string{"/admin/system/form-submissions/" + id, "/admin/system/form-submissions.csv"} {
		response, body := e.get(path)
		if response.StatusCode != 200 {
			t.Fatal(path, response.StatusCode)
		}
		mustNotContain(t, body, "old-private-token")
		mustContain(t, body, "/contact")
	}
	r, body := e.get("/admin/system/form-submissions")
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	mustContain(t, body, `data-form-directory`, "Public contact", "Arabic form", `href="/ar/write"`)
	mustNotContain(t, body, "Draft form", "Plain page", "Unreachable child", "private@example.invalid", "#secret")
}

func TestPrivateIntakeSourceMetadataAndLegacyListing(t *testing.T) {
	e := boot(t)
	e.setting("site.locale", "en")
	e.setting("intake.enabled", true)
	_, page := e.get("/ar/reports/incident")
	id := requestID(t, page)
	form := url.Values{"_csrf": {csrfFrom(t, page)}, "request_id": {id}, "category": {"safety"}, "description": {"Confidential synthetic body"}, "consent": {"yes"}, "source_path": {"/admin/secrets"}}
	r, _ := e.do("POST", "/ar/reports/incident?token=private-source-token-123", form)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	var source string
	if err := e.app.Deps.DB.QueryRow(`SELECT source_path FROM private_intake WHERE id=?`, id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "/ar/reports/incident" {
		t.Fatalf("untrusted source %q", source)
	}
	if response, _ := e.do("POST", "/ar/reports/incident", form); response.StatusCode != 200 {
		t.Fatal("same source retry failed", response.StatusCode)
	}
	if response, _ := e.do("POST", "/reports/incident", form); response.StatusCode != 409 {
		t.Fatal("source-changing retry accepted", response.StatusCode)
	}
	// Listing must remain metadata-only even when encrypted payload is unreadable.
	if _, err := e.app.Deps.DB.Exec(`UPDATE private_intake SET payload='unreadable' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	e.loginAdmin()
	r, body := e.get("/admin/reports")
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	mustContain(t, body, `data-form-directory`, `href="/ar/reports/incident"`, `href="/reports/follow-up"`)
	mustNotContain(t, body, "Confidential synthetic body", "private-source-token-123", "/admin/secrets")
	if _, err := e.app.Deps.DB.Exec(`UPDATE private_intake SET source_path='' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	_, body = e.get("/admin/reports")
	mustContain(t, body, "لم يُسجّل مصدر الصفحة")
}
func requestID(t *testing.T, body string) string {
	t.Helper()
	p := strings.Split(body, `name="request_id" value="`)
	if len(p) != 2 {
		t.Fatal("missing id")
	}
	return strings.SplitN(p[1], `"`, 2)[0]
}

func TestPrivateIntakeDirectoryDisabled(t *testing.T) {
	e := boot(t)
	e.loginAdmin()
	response, body := e.get("/admin/reports")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	mustContain(t, body, `data-form-directory`, "معطّل من إعدادات البلاغات")
	mustNotContain(t, body, `href="/reports/incident"`, `href="/en/reports/incident"`)
}
