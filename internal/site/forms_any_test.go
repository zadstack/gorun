package site_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/site"
)

// formsEnv boots the site with a contact page and returns a CSRF token
// for the client's session.
func formsEnv(t *testing.T, extra ...kernel.Module) (*env, string) {
	t.Helper()
	e := boot(t, extra...)
	e.fake.Content.Add(content.Item{Type: "page", Slug: "contact", Title: "Contact"})
	_, page := e.get("/contact")
	return e, csrfFrom(t, page)
}

// postForm posts kv as a urlencoded body in the given order (url.Values
// would sort it, hiding the first-seen column order).
func (e *env) postForm(slug, token string, kv ...string) (*http.Response, string) {
	e.t.Helper()
	pairs := []string{"_csrf=" + url.QueryEscape(token), "_back=%2Fcontact"}
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, url.QueryEscape(kv[i])+"="+url.QueryEscape(kv[i+1]))
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/forms/"+slug, strings.NewReader(strings.Join(pairs, "&")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := e.c.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func okStatus(code int) bool { return code == http.StatusOK || code == http.StatusSeeOther }

// formRow returns the forms row for slug (ok false when there is none).
func (e *env) formRow(slug string) (source string, fields []site.FormField, ok bool) {
	e.t.Helper()
	var raw string
	err := e.app.Deps.DB.QueryRow(`SELECT source, fields_json FROM forms WHERE slug = ?`, slug).Scan(&source, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		e.t.Fatal(err)
	}
	return source, fields, true
}

// entryData returns every entry's data_json for slug, oldest first.
func (e *env) entryData(slug string) []string {
	e.t.Helper()
	rows, err := e.app.Deps.DB.Query(`SELECT fe.data_json FROM form_entries fe JOIN forms f ON f.id = fe.form_id
		WHERE f.slug = ? ORDER BY fe.created_at, fe.rowid`, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func fieldNames(fs []site.FormField) string {
	var n []string
	for _, f := range fs {
		n = append(n, f.Name+":"+f.State)
	}
	return strings.Join(n, ",")
}

func TestCaptureCreatesFormOnFirstSubmit(t *testing.T) {
	e, tok := formsEnv(t)
	resp, body := e.postForm("quote", tok, "name", "A", "budget", "500")
	if !okStatus(resp.StatusCode) {
		t.Fatalf("submit: %d\n%s", resp.StatusCode, body)
	}
	source, fields, ok := e.formRow("quote")
	if !ok || source != "captured" {
		t.Fatalf("form row: ok=%v source=%q", ok, source)
	}
	if len(fields) != 2 || fields[0].State != "discovered" || fields[1].State != "discovered" {
		t.Fatalf("fields: %+v", fields)
	}
	if got := e.entryData("quote"); len(got) != 1 || got[0] != `{"budget":"500","name":"A"}` {
		t.Fatalf("entries: %v", got)
	}
}

func TestCaptureAddsNewFieldsAsDiscovered(t *testing.T) {
	e, tok := formsEnv(t)
	if resp, _ := e.postForm("quote", tok, "name", "A", "budget", "500"); !okStatus(resp.StatusCode) {
		t.Fatalf("first: %d", resp.StatusCode)
	}
	if resp, _ := e.postForm("quote", tok, "name", "B", "budget", "600", "phone", "123"); !okStatus(resp.StatusCode) {
		t.Fatalf("second: %d", resp.StatusCode)
	}
	_, fields, _ := e.formRow("quote")
	if got := fieldNames(fields); got != "name:discovered,budget:discovered,phone:discovered" {
		t.Fatalf("fields = %s", got)
	}
	if n := len(e.entryData("quote")); n != 2 {
		t.Fatalf("entries = %d", n)
	}
}

func TestCaptureRejectionCreatesNothing(t *testing.T) {
	e, tok := formsEnv(t)
	var many []string
	for i := range 31 {
		many = append(many, fmt.Sprintf("f%d", i), "x")
	}
	cases := map[string][]string{
		"31 fields":  many,
		"long value": {"name", strings.Repeat("a", 4001)},
		"bad name":   {"name", "A", "Bad-Name", "x"},
	}
	for name, kv := range cases {
		resp, body := e.postForm("quote", tok, kv...)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s: %d\n%s", name, resp.StatusCode, body)
		}
	}
	if _, _, ok := e.formRow("quote"); ok {
		t.Fatal("a rejected submission created a form")
	}
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM form_entries`).Scan(&n)
	if n != 0 {
		t.Fatalf("entries = %d", n)
	}
}

func TestCaptureFormCap(t *testing.T) {
	e, tok := formsEnv(t)
	for i := range 50 {
		if _, err := e.app.Deps.DB.Exec(`INSERT INTO forms (id, slug, title, source, fields_json, created_at, updated_at)
			VALUES (?, ?, ?, 'captured', '[]', ?, ?)`, db.NewID(), fmt.Sprintf("cap-%d", i), "cap", db.Now(), db.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if resp, _ := e.postForm("one-more", tok, "name", "A"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("51st form: %d", resp.StatusCode)
	}
	if _, _, ok := e.formRow("one-more"); ok {
		t.Fatal("cap exceeded")
	}
	// An existing captured form still accepts entries.
	if resp, _ := e.postForm("cap-3", tok, "name", "A"); !okStatus(resp.StatusCode) {
		t.Fatalf("existing captured form: %d", resp.StatusCode)
	}
}

func TestCaptureIgnoresReserved(t *testing.T) {
	e, tok := formsEnv(t)
	if resp, _ := e.postForm("quote", tok, "name", "A", "_extra", "z"); !okStatus(resp.StatusCode) {
		t.Fatalf("submit: %d", resp.StatusCode)
	}
	_, fields, _ := e.formRow("quote")
	if got := fieldNames(fields); got != "name:discovered" {
		t.Fatalf("fields = %s", got)
	}
	if got := e.entryData("quote"); len(got) != 1 || got[0] != `{"name":"A"}` {
		t.Fatalf("entries: %v", got)
	}
	// A filled honeypot: silent success, nothing stored.
	if resp, _ := e.postForm("quote", tok, "name", "Bot", "website", "http://spam.test"); !okStatus(resp.StatusCode) {
		t.Fatalf("honeypot: %d", resp.StatusCode)
	}
	if got := e.entryData("quote"); len(got) != 1 {
		t.Fatalf("honeypot stored an entry: %v", got)
	}
}

func builderQuote(t *testing.T, e *env) {
	t.Helper()
	err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{{Slug: "booking", Title: "Booking",
		Fields: []site.FormField{{Name: "name", Label: "Name", Type: "text"}, {Name: "email", Label: "Email", Type: "email", Required: true}}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBuilderFormValidates(t *testing.T) {
	e, tok := formsEnv(t)
	builderQuote(t, e)
	resp, body := e.postForm("booking", tok, "name", "A")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing email: %d\n%s", resp.StatusCode, body)
	}
	// The error page lists the errors and does not offer the contact form instead.
	mustContain(t, body, `role="alert"`)
	mustNotContain(t, body, `action="/forms/contact"`)
	if resp, _ := e.postForm("booking", tok, "name", "A", "email", "not-an-email"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad email: %d", resp.StatusCode)
	}
	if got := e.entryData("booking"); len(got) != 0 {
		t.Fatalf("invalid entries stored: %v", got)
	}
	if resp, body := e.postForm("booking", tok, "name", "A", "email", "a@example.com", "extra", "drop me"); !okStatus(resp.StatusCode) {
		t.Fatalf("valid: %d\n%s", resp.StatusCode, body)
	}
	if got := e.entryData("booking"); len(got) != 1 || got[0] != `{"email":"a@example.com","name":"A"}` {
		t.Fatalf("entries: %v", got)
	}
	_, fields, _ := e.formRow("booking")
	if got := fieldNames(fields); got != "name:pinned,email:pinned" {
		t.Fatalf("an unknown field reached the schema: %s", got)
	}
}

func TestClosedFormGone(t *testing.T) {
	e, tok := formsEnv(t)
	builderQuote(t, e)
	if _, err := e.app.Deps.DB.Exec(`UPDATE forms SET open = 0 WHERE slug = 'booking'`); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.postForm("booking", tok, "email", "a@example.com"); resp.StatusCode != http.StatusGone {
		t.Fatalf("closed form: %d", resp.StatusCode)
	}
	if got := e.entryData("booking"); len(got) != 0 {
		t.Fatalf("entries: %v", got)
	}
}

func TestFormsReplayCreatesOneEntry(t *testing.T) {
	e, tok := formsEnv(t)
	for i := range 2 {
		if resp, _ := e.postForm("quote", tok, "name", "A", "budget", "500"); !okStatus(resp.StatusCode) {
			t.Fatalf("post %d: %d", i, resp.StatusCode)
		}
	}
	if got := e.entryData("quote"); len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	// A different payload is a new entry.
	if resp, _ := e.postForm("quote", tok, "name", "A", "budget", "501"); !okStatus(resp.StatusCode) {
		t.Fatalf("third: %d", resp.StatusCode)
	}
	if got := e.entryData("quote"); len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
}

func TestFormEventCarriesFields(t *testing.T) {
	fm := &fakeMailer{}
	e, tok := formsEnv(t, fm)
	e.setting("contact.email", "owner@example.com")
	var mu sync.Mutex
	var got []site.FormSubmission
	e.app.Deps.Hooks.Action(site.HookFormSubmitted, 10, func(_ context.Context, p any) error {
		raw, _ := p.(json.RawMessage)
		var s site.FormSubmission
		if err := json.Unmarshal(raw, &s); err != nil {
			return err
		}
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
		return nil
	})
	if resp, _ := e.postForm("quote", tok, "name", "A", "budget", "500"); !okStatus(resp.StatusCode) {
		t.Fatalf("submit: %d", resp.StatusCode)
	}
	waitFor(t, "the form event", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	s := got[0]
	mu.Unlock()
	if s.Form != "quote" || s.Fields["budget"] != "500" || s.Fields["name"] != "A" || s.ID == "" || s.SubmittedAt == "" {
		t.Fatalf("payload: %+v", s)
	}
	if n := len(fm.messages()); n != 0 {
		t.Fatalf("deliverForm mailed a non-contact form: %d messages", n)
	}
	var claims int
	_ = e.app.Deps.DB.QueryRow(`SELECT count(*) FROM form_event_deliveries`).Scan(&claims)
	if claims != 0 {
		t.Fatalf("form_event_deliveries claims = %d", claims)
	}
	if n := e.auditCount("site.form.submit"); n != 1 {
		t.Fatalf("audit site.form.submit = %d", n)
	}
	var meta string
	_ = e.app.Deps.DB.QueryRow(`SELECT meta_json FROM audit_log WHERE action = 'site.form.submit'`).Scan(&meta)
	if strings.Contains(meta, "500") {
		t.Fatalf("audit carries values: %s", meta)
	}
}

func TestUpsertBuilderFormsAdoptsCaptured(t *testing.T) {
	e, tok := formsEnv(t)
	if resp, _ := e.postForm("booking", tok, "name", "A", "notes", "hi"); !okStatus(resp.StatusCode) {
		t.Fatalf("capture: %d", resp.StatusCode)
	}
	before, err := site.FormBySlug(e.app.Deps, "booking")
	if err != nil || before.Source != "captured" {
		t.Fatalf("captured: %+v %v", before, err)
	}
	builderQuote(t, e)
	after, err := site.FormBySlug(e.app.Deps, "booking")
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.Source != "builder" || after.PageID != "page-1" || !after.Open || after.Title != "Booking" {
		t.Fatalf("adopted: %+v", after)
	}
	// Declared fields are pinned and first; an earlier discovered column stays for old entries.
	if got := fieldNames(after.Fields); got != "name:pinned,email:pinned,notes:discovered" {
		t.Fatalf("fields = %s", got)
	}
	if got := e.entryData("booking"); len(got) != 1 {
		t.Fatalf("entries lost on adoption: %v", got)
	}
}

func TestUpsertClosesRemoved(t *testing.T) {
	e, _ := formsEnv(t)
	defs := []site.FormDefinition{
		{Slug: "a", Title: "A", Fields: []site.FormField{{Name: "x", Type: "text"}}},
		{Slug: "b", Title: "B", Fields: []site.FormField{{Name: "y", Type: "text"}}},
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", defs); err != nil {
		t.Fatal(err)
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "p2", []site.FormDefinition{{Slug: "c", Title: "C"}}); err != nil {
		t.Fatal(err)
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", defs[:1]); err != nil {
		t.Fatal(err)
	}
	open := func(slug string) bool {
		f, err := site.FormBySlug(e.app.Deps, slug)
		if err != nil {
			t.Fatal(err)
		}
		return f.Open
	}
	if !open("a") || open("b") || !open("c") {
		t.Fatalf("open: a=%v b=%v c=%v", open("a"), open("b"), open("c"))
	}
	// Republishing with the section back reopens it.
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", defs); err != nil {
		t.Fatal(err)
	}
	if !open("b") {
		t.Fatal("b not reopened")
	}
	// No forms on the page closes all of its forms.
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", nil); err != nil {
		t.Fatal(err)
	}
	if open("a") || open("b") || !open("c") {
		t.Fatal("empty publish did not close p1's forms only")
	}
	if _, err := site.FormBySlug(e.app.Deps, "missing"); !errors.Is(err, site.ErrNoForm) {
		t.Fatalf("missing form: %v", err)
	}
}

func TestUpsertBuilderFormsRejectsInvalid(t *testing.T) {
	e, _ := formsEnv(t)
	for name, d := range map[string]site.FormDefinition{
		"bad slug":       {Slug: "Bad Slug"},
		"reserved slug":  {Slug: "contact"},
		"bad field":      {Slug: "ok", Fields: []site.FormField{{Name: "Bad-Name", Type: "text"}}},
		"bad type":       {Slug: "ok", Fields: []site.FormField{{Name: "x", Type: "color"}}},
		"duplicate name": {Slug: "ok", Fields: []site.FormField{{Name: "x", Type: "text"}, {Name: "x", Type: "text"}}},
	} {
		if err := site.UpsertBuilderForms(e.app.Deps, "p1", []site.FormDefinition{d}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := site.FormBySlug(e.app.Deps, "ok"); !errors.Is(err, site.ErrNoForm) {
		t.Fatalf("an invalid definition was stored: %v", err)
	}
}

func TestUpsertRefusesSlugOfAnotherPage(t *testing.T) {
	e, _ := formsEnv(t)
	def := site.FormDefinition{Slug: "booking", Title: "Booking", Fields: []site.FormField{{Name: "name", Type: "text"}}}
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", []site.FormDefinition{def}); err != nil {
		t.Fatal(err)
	}
	other := def
	other.Title = "Stolen"
	if err := site.UpsertBuilderForms(e.app.Deps, "p2", []site.FormDefinition{other}); err == nil || !strings.Contains(err.Error(), "booking") {
		t.Fatalf("slug of another page reassigned: %v", err)
	}
	f, err := site.FormBySlug(e.app.Deps, "booking")
	if err != nil || f.PageID != "p1" || f.Title != "Booking" || !f.Open {
		t.Fatalf("owner's form changed: %+v %v", f, err)
	}
	// The refusal rolls back the whole publish: p2's other forms are not stored either.
	if err := site.UpsertBuilderForms(e.app.Deps, "p2", []site.FormDefinition{{Slug: "quote", Title: "Quote"}, other}); err == nil {
		t.Fatal("mixed publish accepted")
	}
	if _, err := site.FormBySlug(e.app.Deps, "quote"); !errors.Is(err, site.ErrNoForm) {
		t.Fatalf("partial upsert stored quote: %v", err)
	}
	// Once p1 closes it the slug is still p1's; p1 itself can always reopen it.
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", []site.FormDefinition{def}); err != nil {
		t.Fatalf("owner republish: %v", err)
	}
}

func TestUpsertCapsFieldsAt30(t *testing.T) {
	e, tok := formsEnv(t)
	var kv []string
	for i := 0; i < 30; i++ {
		kv = append(kv, fmt.Sprintf("f%d", i), "v")
	}
	if resp, body := e.postForm("survey", tok, kv...); !okStatus(resp.StatusCode) {
		t.Fatalf("capture: %d\n%s", resp.StatusCode, body)
	}
	var declared []site.FormField
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		declared = append(declared, site.FormField{Name: n, Type: "text"})
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "p1", []site.FormDefinition{{Slug: "survey", Title: "Survey", Fields: declared}}); err != nil {
		t.Fatal(err)
	}
	f, err := site.FormBySlug(e.app.Deps, "survey")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Fields) != 30 {
		t.Fatalf("stored %d fields", len(f.Fields))
	}
	want := "a:pinned,b:pinned,c:pinned,d:pinned,e:pinned"
	for i := 5; i < 30; i++ {
		want += fmt.Sprintf(",f%d:discovered", i)
	}
	if got := fieldNames(f.Fields); got != want {
		t.Fatalf("fields = %s", got)
	}
}

func TestBuilderFormsHookUpserts(t *testing.T) {
	e, _ := formsEnv(t)
	ctx := context.Background()
	raw := json.RawMessage(`{"page_id":"p1","forms":[{"slug":"booking","title":"Book a call","submit_label":"Send","success":"Thanks",
		"fields":[{"name":"name","label":"Name","type":"text","required":true},{"name":"topic","label":"Topic","type":"choice","help":"Pick one","choices":["Sales","Support"]}]}]}`)
	if err := e.app.Deps.Hooks.Do(ctx, "builder.forms.published", raw); err != nil {
		t.Fatal(err)
	}
	f, err := site.FormBySlug(e.app.Deps, "booking")
	if err != nil {
		t.Fatal(err)
	}
	if f.Source != "builder" || f.PageID != "p1" || f.Title != "Book a call" || !f.Open || len(f.Fields) != 2 {
		t.Fatalf("form = %+v", f)
	}
	topic := f.Fields[1]
	if !f.Fields[0].Required || topic.Type != "choice" || topic.Help != "Pick one" || strings.Join(topic.Choices, "|") != "Sales|Support" || topic.State != "pinned" {
		t.Fatalf("fields = %+v", f.Fields)
	}
	// A typed value (sync delivery of a struct) decodes too; an empty list closes the page's forms.
	type payload struct {
		PageID string `json:"page_id"`
		Forms  []any  `json:"forms"`
	}
	if err := e.app.Deps.Hooks.Do(ctx, "builder.forms.published", payload{PageID: "p1", Forms: []any{}}); err != nil {
		t.Fatal(err)
	}
	if f, _ = site.FormBySlug(e.app.Deps, "booking"); f.Open {
		t.Fatal("empty publish left the form open")
	}
	// A malformed payload is dropped, not retried forever.
	if err := e.app.Deps.Hooks.Do(ctx, "builder.forms.published", json.RawMessage(`{"forms":`)); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
}
