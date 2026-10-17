package site_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/mailer"
)

// fakeMailer stands in for *mailer.Mailer under mailer.ServiceName.
type fakeMailer struct {
	mu           sync.Mutex
	sent         []mailer.Message
	fail         error
	unconfigured bool
}

func (f *fakeMailer) Configured(context.Context) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.unconfigured
}

func (f *fakeMailer) Name() string      { return "fakemailer" }
func (f *fakeMailer) Migrations() fs.FS { return nil }
func (f *fakeMailer) Register(d *kernel.Deps) error {
	d.Services.Set(mailer.ServiceName, f)
	return nil
}
func (f *fakeMailer) SendMessage(_ context.Context, m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMailer) messages() []mailer.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mailer.Message(nil), f.sent...)
}

func (e *env) submitContact(name, email, msg string) {
	e.t.Helper()
	_, page := e.get("/contact")
	resp, body := e.do("POST", "/forms/contact", url.Values{"_csrf": {csrfFrom(e.t, page)}, "name": {name}, "email": {email},
		"message": {msg}, "_back": {"/contact"}})
	if resp.StatusCode != 200 {
		e.t.Fatalf("submit: %d\n%s", resp.StatusCode, body)
	}
}

func (e *env) auditCount(action string) int {
	e.t.Helper()
	var n int
	if err := e.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// I3: an accepted contact submission is mailed to contact.email with the
// visitor as Reply-To and the site title in the subject, and audited.
func TestContactFormIsMailedToContactEmail(t *testing.T) {
	fm := &fakeMailer{}
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	e.setting("site.locale", "en")
	e.setting("site.title", "ZAD Lab")
	e.setting("contact.email", "owner@zad.example")

	e.submitContact("Sara Ali", "sara@example.com", "Hello there\nsecond line")
	waitFor(t, "the contact email", func() bool { return len(fm.messages()) == 1 })
	m := fm.messages()[0]
	if m.To != "owner@zad.example" || m.ReplyTo != `"Sara Ali" <sara@example.com>` {
		t.Fatalf("to/reply-to = %q / %q", m.To, m.ReplyTo)
	}
	if !strings.Contains(m.Subject, "ZAD Lab") || !strings.Contains(m.Subject, "Sara Ali") {
		t.Fatalf("subject = %q", m.Subject)
	}
	if !strings.Contains(m.Text, "Hello there\nsecond line") || !strings.Contains(m.Text, "sara@example.com") || !strings.Contains(m.HTML, "Hello there") {
		t.Fatalf("body = %q / %q", m.Text, m.HTML)
	}
	waitFor(t, "site.form.delivered audit", func() bool { return e.auditCount("site.form.delivered") == 1 })
}

// Owner notifications must not be mistaken for paid or activated orders.
func TestContactMailClarifiesReviewOnly(t *testing.T) {
	for _, tc := range []struct{ locale, want string }{
		{"en", "Receiving it does not confirm payment, acceptance of a service request, or automatic activation or delivery."},
		{"ar", "استلامها لا يؤكد دفعًا أو قبول طلب خدمة أو تفعيلًا أو تسليمًا تلقائيًا."},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			fm := &fakeMailer{}
			e := boot(t, fm)
			e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact"})
			e.setting("site.locale", tc.locale)
			e.setting("contact.email", "owner@example.com")
			_, page := e.get("/contact")
			resp, body := e.do("POST", "/forms/contact", url.Values{"_csrf": {csrfFrom(t, page)}, "name": {"Visitor"}, "email": {"visitor@example.com"}, "message": {"Service request <script>alert(1)</script>"}, "_back": {"/contact"}})
			confirmation := "Your message has been saved for review."
			if tc.locale == "ar" {
				confirmation = "حُفظت رسالتك للمراجعة."
			}
			if resp.StatusCode != http.StatusOK || !strings.Contains(body, confirmation) {
				t.Fatalf("missing saved-for-review confirmation: %d %s", resp.StatusCode, body)
			}
			waitFor(t, "owner notification", func() bool { return len(fm.messages()) == 1 })
			m := fm.messages()[0]
			if !strings.Contains(m.Text, tc.want) || !strings.Contains(m.HTML, tc.want) {
				t.Fatalf("missing review-only disclaimer: %q / %q", m.Text, m.HTML)
			}
			if m.To != "owner@example.com" || strings.Contains(m.HTML, "<script>") {
				t.Fatalf("recipient or HTML escaping changed: %#v", m)
			}
		})
	}
}

// Without contact.email the first active admin receives it; a send error
// is audited as site.form.failed.
func TestContactFormFallsBackToAdminAndAuditsFailure(t *testing.T) {
	fm := &fakeMailer{}
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	us, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	ctx := context.Background()
	if _, err := us.Create(ctx, "editor@example.com", "Ed", "correct horse battery", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := us.Create(ctx, "first-admin@example.com", "Admin", "correct horse battery", "admin"); err != nil {
		t.Fatal(err)
	}
	e.submitContact("Sara", "sara@example.com", "Hi")
	waitFor(t, "the contact email", func() bool { return len(fm.messages()) == 1 })
	if to := fm.messages()[0].To; to != "first-admin@example.com" {
		t.Fatalf("fallback recipient = %q", to)
	}

	fm.mu.Lock()
	fm.fail = errors.New("smtp down")
	fm.mu.Unlock()
	e.submitContact("Sara", "sara@example.com", "Hi again")
	waitFor(t, "site.form.failed audit", func() bool { return e.auditCount("site.form.failed") == 1 })
}

// Without SMTP the mailer only logs the message: the submission is audited
// as failed (reason mail_not_configured), never as delivered.
func TestContactFormWithoutSMTPIsAuditedAsNotDelivered(t *testing.T) {
	fm := &fakeMailer{unconfigured: true}
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	e.setting("contact.email", "owner@example.com")
	e.submitContact("Sara", "sara@example.com", "Hi")
	waitFor(t, "site.form.failed audit", func() bool { return e.auditCount("site.form.failed") == 1 })
	if n := e.auditCount("site.form.delivered"); n != 0 {
		t.Fatalf("delivered audits = %d", n)
	}
	var meta string
	_ = e.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'site.form.failed'`).Scan(&meta)
	if !strings.Contains(meta, "mail_not_configured") {
		t.Fatalf("reason: %s", meta)
	}
	if len(fm.messages()) != 1 {
		t.Fatal("the mailer is still called (it logs the message in dev)")
	}
}

func (e *env) submissionDelivered(msg string) (stored, delivered bool) {
	e.t.Helper()
	var d int
	err := e.app.Deps.DB.QueryRow(`SELECT delivered FROM form_submissions WHERE message = ?`, msg).Scan(&d)
	return err == nil, d == 1
}

// Addendum C: every accepted submission is stored before any mail attempt
// (also with no mailer at all), flagged delivered once mailed, and listed
// at System → Form submissions for settings.manage.
func TestContactSubmissionsAreStoredAndListed(t *testing.T) {
	e := boot(t) // no mailer service: delivery fails, the message must survive
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	e.submitContact("Sara Ali", "sara@example.com", "Kept without SMTP\nline two")
	if stored, delivered := e.submissionDelivered("Kept without SMTP\nline two"); !stored || delivered {
		t.Fatalf("stored=%v delivered=%v", stored, delivered)
	}
	waitFor(t, "site.form.failed audit", func() bool { return e.auditCount("site.form.failed") == 1 })

	e.loginAdmin()
	resp, page := e.get("/admin/system/form-submissions")
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	mustContain(t, page, "Sara Ali", "sara@example.com", "Kept without SMTP", `href="/admin/system/form-submissions"`)

	// Editors (no settings.manage) cannot read submissions.
	us, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := us.Create(context.Background(), "ed@example.com", "Ed", "correct horse battery", "editor"); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	e.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, login := e.get("/admin/login")
	if resp, _ := e.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(t, login)}, "email": {"ed@example.com"},
		"password": {"correct horse battery"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("editor login: %d", resp.StatusCode)
	}
	if resp, _ := e.get("/admin/system/form-submissions"); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("editor: %d", resp.StatusCode)
	}
}

func TestMailedSubmissionIsFlaggedDelivered(t *testing.T) {
	fm := &fakeMailer{}
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	e.setting("contact.email", "owner@example.com")
	e.submitContact("Sara", "sara@example.com", "Mailed message")
	waitFor(t, "delivered flag", func() bool { _, d := e.submissionDelivered("Mailed message"); return d })
}
