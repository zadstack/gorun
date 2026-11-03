package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/site"
)

// storedSubmits flushes the buffer and returns every submit row, each
// column joined into one string so the test can check nothing private
// slipped into any of them.
func storedSubmits(t *testing.T, e *collectEnv) (forms, pages, rows []string) {
	t.Helper()
	if err := e.mod.buf.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := e.mod.store.With(context.Background(), func(d *sql.DB) error {
		r, err := d.Query(`SELECT form, path, ts || visitor || ref_host || utm_source || utm_medium || utm_campaign || browser || os || device || country || locale || form || path
			FROM events WHERE kind = 'submit' ORDER BY id`)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var f, p, all string
			if err := r.Scan(&f, &p, &all); err != nil {
				return err
			}
			forms, pages, rows = append(forms, f), append(pages, p), append(rows, all)
		}
		return r.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return forms, pages, rows
}

func TestFormSubmittedHookRecordsSubmit(t *testing.T) {
	e := bootCollect(t, nil, nil)
	ctx := context.Background()
	sub := site.FormSubmission{ID: "sub-1", Form: "contact", Name: "Secret Name", Email: "who@example.com",
		Message: "private message", Page: "/contact-us?token=abc#x", IP: "203.0.113.9", SubmittedAt: "2026-11-02T23:59:58Z"}
	if err := e.app.Deps.Hooks.Do(ctx, hookFormSubmitted, sub); err != nil {
		t.Fatalf("struct payload: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"id": "sub-2", "form": "quote", "page": "/pricing", "email": "x@example.com",
		"ip": "198.51.100.1", "fields": map[string]string{"company": "Acme"}})
	if err := e.app.Deps.Hooks.Do(ctx, hookFormSubmitted, json.RawMessage(raw)); err != nil {
		t.Fatalf("json payload: %v", err)
	}
	// A retried hooks.event job (another action failed) delivers the same
	// submission again: it is counted once.
	if err := e.app.Deps.Hooks.Do(ctx, hookFormSubmitted, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	// Undecodable payloads are dropped without failing the event (which
	// would retry every other action, mail delivery included).
	if err := e.app.Deps.Hooks.Do(ctx, hookFormSubmitted, json.RawMessage(`{"form":`)); err != nil {
		t.Fatalf("bad payload returned an error: %v", err)
	}

	forms, pages, rows := storedSubmits(t, e)
	if strings.Join(forms, ",") != "contact,quote" || strings.Join(pages, ",") != "/contact-us,/pricing" {
		t.Fatalf("submits = forms %q pages %q, want contact,quote on /contact-us,/pricing", forms, pages)
	}
	for _, r := range rows {
		for _, private := range []string{"Secret", "example.com", "private", "203.0.113", "198.51.100", "token", "Acme"} {
			if strings.Contains(r, private) {
				t.Errorf("stored row %q contains %q", r, private)
			}
		}
	}
	// The submission keeps its own day, not the day the job ran.
	var ts string
	if err := e.mod.store.With(ctx, func(d *sql.DB) error {
		return d.QueryRow(`SELECT ts FROM events WHERE form = 'contact'`).Scan(&ts)
	}); err != nil {
		t.Fatal(err)
	}
	if ts != "2026-11-02T23:59:58Z" {
		t.Errorf("ts = %q, want the submitted_at", ts)
	}

	// Tracking off records nothing.
	e.setting(SettingEnabled, false)
	if err := e.app.Deps.Hooks.Do(ctx, hookFormSubmitted, site.FormSubmission{ID: "sub-3", Form: "contact", Page: "/contact-us"}); err != nil {
		t.Fatal(err)
	}
	if forms, _, _ := storedSubmits(t, e); len(forms) != 2 {
		t.Errorf("tracking off: %d submits, want 2", len(forms))
	}
}

// TestFormHookNameMatchesSite pins the string the module subscribes to
// (it does not import site).
func TestFormHookNameMatchesSite(t *testing.T) {
	if hookFormSubmitted != site.HookFormSubmitted {
		t.Fatalf("hookFormSubmitted = %q, site emits %q", hookFormSubmitted, site.HookFormSubmitted)
	}
}

func TestSeenIDsBounded(t *testing.T) {
	s := newSeenIDs(3)
	for _, id := range []string{"a", "b", "c"} {
		if !s.add(id) {
			t.Fatalf("%s reported seen", id)
		}
	}
	if s.add("a") {
		t.Fatal("a not reported seen")
	}
	s.add("d") // evicts a
	if !s.add("a") {
		t.Fatal("a still remembered past the bound")
	}
	for range 2 {
		if !s.add("") {
			t.Fatal("an empty id is never deduplicated")
		}
	}
}
