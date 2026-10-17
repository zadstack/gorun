package site_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/content"
)

func TestContactFormCSRFAndHook(t *testing.T) {
	e := boot(t)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact us"})
	var mu sync.Mutex
	var got []map[string]any
	e.app.Deps.Hooks.Action("site.form.submitted", 10, func(_ context.Context, p any) error {
		var m map[string]any
		raw, _ := p.(json.RawMessage)
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
		return nil
	})

	resp, page := e.get("/contact")
	if resp.StatusCode != 200 {
		t.Fatalf("contact page: %d", resp.StatusCode)
	}
	mustContain(t, page, `action="/forms/contact"`, `name="website"`, "Contact us")
	token := csrfFrom(t, page)
	valid := url.Values{"_csrf": {token}, "name": {"Sara"}, "email": {"sara@example.com"}, "message": {"Hello there"}, "_back": {"/contact"}}

	// No token → 403.
	noToken := url.Values{"name": {"Sara"}, "email": {"sara@example.com"}, "message": {"Hi"}}
	if resp, _ := e.do("POST", "/forms/contact", noToken); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("without csrf: %d", resp.StatusCode)
	}
	// Validation errors re-render the result page with 422.
	resp, body := e.do("POST", "/forms/contact", url.Values{"_csrf": {token}, "name": {""}, "email": {"bad"}, "message": {""}})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-kind="form-result"`, "role=\"alert\"")
	// Valid → result page + hook + audit.
	resp, body = e.do("POST", "/forms/contact", valid)
	if resp.StatusCode != 200 {
		t.Fatalf("valid: %d\n%s", resp.StatusCode, body)
	}
	mustContain(t, body, `data-kind="form-result"`, `href="/contact"`)
	// Honeypot → success page, but nothing emitted.
	hp := url.Values{"_csrf": {token}, "name": {"Bot"}, "email": {"bot@example.com"}, "message": {"spam"}, "website": {"http://spam.test"}}
	if resp, _ := e.do("POST", "/forms/contact", hp); resp.StatusCode != 200 {
		t.Fatalf("honeypot: %d", resp.StatusCode)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if len(got) != 1 || got[0]["email"] != "sara@example.com" || got[0]["name"] != "Sara" || got[0]["form"] != "contact" || got[0]["message"] != "Hello there" {
		mu.Unlock()
		t.Fatalf("hook payloads: %+v", got)
	}
	mu.Unlock()
	entries, _, _ := e.app.Deps.Audit.List(context.Background(), audit.ListQuery{Action: "site.form.submit"})
	if len(entries) != 1 {
		t.Fatalf("audit entries: %d", len(entries))
	}

	// Rate limit: 5 posts per minute per IP, counted before the body is read,
	// so the 4 above (also the one without a token) count.
	for i := 5; i <= 5; i++ {
		if resp, _ := e.do("POST", "/forms/contact", valid); resp.StatusCode != 200 {
			t.Fatalf("submission %d: %d", i, resp.StatusCode)
		}
	}
	if resp, _ := e.do("POST", "/forms/contact", valid); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("6th: %d, want 429", resp.StatusCode)
	}
}
