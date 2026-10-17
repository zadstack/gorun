package site_test

import (
	"context"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/site"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestContactCustomerReceiptReplay(t *testing.T) {
	fm := &fakeMailer{}
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact"})
	e.setting("site.locale", "en")
	e.setting("site.title", "Example Co")
	e.setting("contact.email", "owner@example.com")
	e.setting("forms.customer_receipt", true)
	_, page := e.get("/contact")
	fields := url.Values{"_csrf": {csrfFrom(t, page)}, "name": {"Test"}, "email": {"customer@example.com"}, "message": {"<script>bad</script> Test request"}, "_back": {"/contact"}}
	for i := 0; i < 2; i++ {
		resp, _ := e.do("POST", "/forms/contact", fields)
		if resp.StatusCode != 200 {
			t.Fatalf("submit %d", resp.StatusCode)
		}
	}
	waitFor(t, "two distinct recipients", func() bool { return len(fm.messages()) == 2 })
	var count int
	if err := e.app.Deps.DB.QueryRow("SELECT count(*) FROM form_submissions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("submission count %d: %v", count, err)
	}
	messages := fm.messages()
	for _, m := range messages {
		if m.To == "owner@example.com" && (!strings.Contains(m.Text, "does not confirm payment") || strings.Contains(m.Text, "site.form.")) {
			t.Fatalf("owner notice not translated: %q", m.Text)
		}
	}
	var receiptFound bool
	for _, m := range messages {
		if m.To == "customer@example.com" {
			receiptFound = true
			if !strings.Contains(m.Text, "pending review") || !strings.Contains(m.Text, "not a payment") || strings.Contains(m.HTML, "<script>") {
				t.Fatalf("unsafe receipt %#v", m)
			}
		}
	}
	if !receiptFound {
		t.Fatal("missing customer receipt")
	}
	resp, _ := e.do("POST", "/forms/contact", fields)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	if len(fm.messages()) != 2 {
		t.Fatal("duplicate email")
	}
	fields.Set("message", "A different legitimate request")
	resp, _ = e.do("POST", "/forms/contact", fields)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	waitFor(t, "distinct request delivered", func() bool { return len(fm.messages()) == 4 })
}

func TestContactConcurrentHeaderReplayAndLaterRequest(t *testing.T) {
	fm := &fakeMailer{}
	e := boot(t, fm)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact"})
	e.setting("site.locale", "en")
	e.setting("contact.email", "owner@example.com")
	e.setting("forms.customer_receipt", true)
	_, page := e.get("/contact")
	token := csrfFrom(t, page)
	fields := url.Values{"name": {"Header visitor"}, "email": {"customer@example.com"}, "message": {"My request"}, "_back": {"/contact"}}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := e.do("POST", "/forms/contact", fields, "X-CSRF-Token", token)
			if resp.StatusCode != 200 {
				t.Errorf("concurrent submit: %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	waitFor(t, "one pair only", func() bool { return len(fm.messages()) == 2 })
	var id string
	if err := e.app.Deps.DB.QueryRow("SELECT id FROM form_submissions").Scan(&id); err != nil {
		t.Fatal(err)
	}
	e.app.Deps.Hooks.Emit(context.Background(), site.HookFormSubmitted, site.FormSubmission{ID: id, Email: "customer@example.com", Name: "Header visitor"})
	if len(fm.messages()) != 2 {
		t.Fatal("event replay duplicated mail")
	}
	if _, err := e.app.Deps.DB.Exec("UPDATE form_submissions SET created_at=? WHERE id=?", time.Now().UTC().Add(-11*time.Minute).Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
	resp, _ := e.do("POST", "/forms/contact", fields, "X-CSRF-Token", token)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	waitFor(t, "later intentional request", func() bool { return len(fm.messages()) == 4 })
}
