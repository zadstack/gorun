package site_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/site"
)

func pinned(name string) site.FormField {
	return site.FormField{Name: name, Label: strings.ToUpper(name[:1]) + name[1:], Type: "text", State: "pinned"}
}

// seedForm inserts a forms row and returns its id.
func (e *env) seedForm(slug, source string, open bool, fields ...site.FormField) string {
	e.t.Helper()
	raw, _ := json.Marshal(fields)
	id, now := db.NewID(), db.Now()
	o := 0
	if open {
		o = 1
	}
	pageID := ""
	if source == "builder" {
		pageID = "page-1"
	}
	if _, err := e.app.Deps.DB.Exec(`INSERT INTO forms (id, slug, title, source, page_id, fields_json, open, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, slug, slug, source, pageID, string(raw), o, now, now); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// seedEntry inserts an entry of form formID.
func (e *env) seedEntry(formID string, data map[string]string, status, at string) string {
	e.t.Helper()
	raw, _ := json.Marshal(data)
	id := db.NewID()
	if _, err := e.app.Deps.DB.Exec(`INSERT INTO form_entries (id, form_id, data_json, page, status, created_at) VALUES (?, ?, ?, '/quote', ?, ?)`,
		id, formID, string(raw), status, at); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) entryStatus(id string) string {
	e.t.Helper()
	var s string
	if err := e.app.Deps.DB.QueryRow(`SELECT status FROM form_entries WHERE id = ?`, id).Scan(&s); err != nil {
		return ""
	}
	return s
}

// adminPost posts to an admin path with a CSRF token taken from page.
func (e *env) adminPost(path, csrfPage string, kv ...string) *http.Response {
	e.t.Helper()
	_, html := e.get(csrfPage)
	form := url.Values{"_csrf": {csrfFrom(e.t, html)}}
	for i := 0; i+1 < len(kv); i += 2 {
		form.Set(kv[i], kv[i+1])
	}
	resp, _ := e.do("POST", path, form)
	return resp
}

func csvRows(t *testing.T, body string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestFormsListShowsCountsAndContactRow(t *testing.T) {
	e := boot(t)
	id := e.seedForm("quote", "captured", true, pinned("name"))
	e.seedEntry(id, map[string]string{"name": "A"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(id, map[string]string{"name": "B"}, "new", "2026-03-02T10:00:00Z")
	e.seedEntry(id, map[string]string{"name": "C"}, "read", "2026-03-03T10:00:00Z")
	e.loginAdmin()
	resp, body := e.get("/admin/forms")
	if resp.StatusCode != 200 {
		t.Fatalf("list: %d", resp.StatusCode)
	}
	mustContain(t, body, `href="/admin/forms/quote"`, `data-entries="3"`, `data-new="2"`, `data-new-total="2"`,
		`href="/admin/system/form-submissions"`)
}

func TestFormEntriesTable(t *testing.T) {
	e := boot(t)
	var fields []site.FormField
	for i := 1; i <= 8; i++ {
		fields = append(fields, pinned(fmt.Sprintf("f%d", i)))
	}
	fields[1].Hidden = true
	id := e.seedForm("quote", "captured", true, fields...)
	for i := 0; i < 55; i++ {
		e.seedEntry(id, map[string]string{"f1": fmt.Sprintf("row%02d", i), "f2": "secret"}, "new", fmt.Sprintf("2026-03-01T10:%02d:00Z", i))
	}
	e.loginAdmin()
	resp, body := e.get("/admin/forms/quote")
	if resp.StatusCode != 200 {
		t.Fatalf("entries: %d", resp.StatusCode)
	}
	if n := strings.Count(body, `data-field="`); n < 6 {
		t.Fatalf("expected field columns, got %d", n)
	}
	mustContain(t, body, `<th data-field="f1"`, `<th data-field="f3"`, `<th data-field="f7"`, "<bdi>row54</bdi>")
	mustNotContain(t, body, `<th data-field="f2"`, `<th data-field="f8"`, "secret", "<bdi>row04</bdi>")
	if strings.Index(body, "row54") > strings.Index(body, "row53") {
		t.Fatal("not newest first")
	}
	_, p2 := e.get("/admin/forms/quote?page=2")
	mustContain(t, p2, "<bdi>row04</bdi>", "<bdi>row00</bdi>")
	mustNotContain(t, p2, "<bdi>row54</bdi>")
}

func TestFormEntriesFilters(t *testing.T) {
	e := boot(t)
	id := e.seedForm("quote", "captured", true, pinned("name"), pinned("note"))
	e.seedEntry(id, map[string]string{"name": "Alpha", "note": "100% sure"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(id, map[string]string{"name": "Beta", "note": "under_score"}, "read", "2026-03-05T10:00:00Z")
	e.seedEntry(id, map[string]string{"name": "Gamma", "note": "plain"}, "new", "2026-03-10T23:30:00Z")
	e.loginAdmin()
	has := func(q string, want ...string) {
		t.Helper()
		_, body := e.get("/admin/forms/quote?" + q)
		for _, n := range []string{"Alpha", "Beta", "Gamma"} {
			if got := strings.Contains(body, "<bdi>"+n+"</bdi>"); got != contains(want, n) {
				t.Fatalf("%s: %s present=%v, want %v", q, n, got, want)
			}
		}
	}
	has("status=new", "Alpha", "Gamma")
	has("from=2026-03-02", "Beta", "Gamma")
	has("to=2026-03-05", "Alpha", "Beta")
	has("from=2026-03-10&to=2026-03-10", "Gamma")
	has("q=beta", "Beta")
	has("q=100%25", "Alpha") // % is literal, not a wildcard
	has("q=%25", "Alpha")    // only the row that really holds a %
	has("q=_", "Beta")       // _ is literal too
	has("q=" + url.QueryEscape("x' OR '1'='1"))
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

func TestFormEntryMarksRead(t *testing.T) {
	e := boot(t)
	id := e.seedForm("quote", "captured", true, pinned("name"))
	eid := e.seedEntry(id, map[string]string{"name": "Sara"}, "new", "2026-03-01T10:00:00Z")
	e.loginAdmin()
	base := "/admin/forms/quote/entries/" + eid
	resp, body := e.get(base)
	if resp.StatusCode != 200 {
		t.Fatalf("show: %d", resp.StatusCode)
	}
	mustContain(t, body, "Sara")
	if s := e.entryStatus(eid); s != "read" {
		t.Fatalf("status = %q", s)
	}
	if resp := e.adminPost(base+"/status", base, "status", "spam"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("spam: %d", resp.StatusCode)
	}
	if s := e.entryStatus(eid); s != "spam" {
		t.Fatalf("status = %q", s)
	}
	if resp := e.adminPost(base+"/status", base, "status", "bogus"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus: %d", resp.StatusCode)
	}
	if resp := e.adminPost(base+"/delete", base); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if s := e.entryStatus(eid); s != "" {
		t.Fatalf("entry kept: %q", s)
	}
	if resp, _ := e.get(base); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted: %d", resp.StatusCode)
	}
	if e.auditCount("site.form.entry.delete") != 1 || e.auditCount("site.form.entry.status") != 1 {
		t.Fatal("status/delete not audited")
	}
}

func TestFormsCSVKeepsRetiredFields(t *testing.T) {
	e := boot(t)
	retired := pinned("old")
	retired.State = "discovered"
	id := e.seedForm("quote", "builder", true, pinned("name"), retired, pinned("email"))
	e.seedEntry(id, map[string]string{"name": "=cmd()", "old": "legacy", "zzz": "extra1", "email": "a@x.test"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(id, map[string]string{"name": "B", "aaa": "extra2"}, "read", "2026-03-02T10:00:00Z")
	e.loginAdmin()
	resp, body := e.get("/admin/forms/quote/export.csv")
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	rows := csvRows(t, body)
	want := []string{"id", "created_at", "status", "page", "Name", "Old", "Email", "zzz", "aaa"}
	if len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(want, ",") {
		t.Fatalf("rows: %q", rows)
	}
	// Newest first, like the table.
	if rows[2][4] != `'=cmd()` || rows[2][5] != "legacy" || rows[2][7] != "extra1" || rows[1][8] != "extra2" {
		t.Fatalf("values: %q", rows)
	}
	if e.auditCount("site.form.entries.export") != 1 {
		t.Fatal("export not audited")
	}
}

func TestPinDiscoveredField(t *testing.T) {
	e, tok := formsEnv(t)
	if resp, _ := e.postForm("quote", tok, "name", "Sara", "contact", "not-an-email"); !okStatus(resp.StatusCode) {
		t.Fatalf("capture: %d", resp.StatusCode)
	}
	e.loginAdmin()
	_, page := e.get("/admin/forms/quote")
	mustContain(t, page, "contact")
	if resp := e.adminPost("/admin/forms/quote/fields/contact/pin", "/admin/forms/quote", "type", "email", "label", "Email"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("pin: %d", resp.StatusCode)
	}
	_, fields, _ := e.formRow("quote")
	var f site.FormField
	for _, x := range fields {
		if x.Name == "contact" {
			f = x
		}
	}
	if f.State != "pinned" || f.Type != "email" || f.Label != "Email" {
		t.Fatalf("field = %+v", f)
	}
	// The public client logged in as admin; use a fresh visitor.
	v, vtok := formsEnvFor(t, e)
	if resp, _ := v.postForm("quote", vtok, "name", "Bad", "contact", "nope"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid email accepted: %d", resp.StatusCode)
	}
	if resp, _ := v.postForm("quote", vtok, "name", "Good", "contact", "ok@example.com"); !okStatus(resp.StatusCode) {
		t.Fatalf("valid email: %d", resp.StatusCode)
	}
	if len(e.entryData("quote")) != 2 {
		t.Fatalf("entries: %v", e.entryData("quote"))
	}
	if resp := e.adminPost("/admin/forms/quote/fields/contact/pin", "/admin/forms/quote", "type", "bogus"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad type: %d", resp.StatusCode)
	}
}

func TestFieldPresentationOnly(t *testing.T) {
	e := boot(t)
	id := e.seedForm("quote", "captured", true, pinned("a"), pinned("b"), pinned("c"))
	eid := e.seedEntry(id, map[string]string{"a": "1", "b": "2", "c": "3"}, "new", "2026-03-01T10:00:00Z")
	var before string
	_ = e.app.Deps.DB.QueryRow(`SELECT data_json FROM form_entries WHERE id = ?`, eid).Scan(&before)
	e.loginAdmin()
	page := "/admin/forms/quote"
	post := func(path string, kv ...string) {
		t.Helper()
		if resp := e.adminPost(page+path, page, kv...); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	post("/fields/b/hide")
	post("/fields/a/rename", "label", "First")
	post("/fields/c/up")
	_, fields, _ := e.formRow("quote")
	names := ""
	for _, f := range fields {
		names += f.Name
	}
	if names != "abc" {
		t.Fatalf("reorder rewrote the schema order: %s", names)
	}
	// c moves above b only (b is hidden, so the table shows a then c).
	if a, b, c := e.fieldOf("quote", "a").AdminPos, e.fieldOf("quote", "b").AdminPos, e.fieldOf("quote", "c").AdminPos; !(a < c && c < b) {
		t.Fatalf("admin_pos a=%d b=%d c=%d", a, b, c)
	}
	_, table := e.get(page)
	if got := thOrder(table); got != "a,c" {
		t.Fatalf("table order = %s", got)
	}
	for _, f := range fields {
		switch f.Name {
		case "b":
			if !f.Hidden {
				t.Fatal("b not hidden")
			}
		case "a":
			if f.AdminLabel != "First" || f.Label != "A" {
				t.Fatalf("label = %q admin_label = %q", f.Label, f.AdminLabel)
			}
		}
	}
	post("/fields/b/show")
	_, fields, _ = e.formRow("quote")
	if fields[1].Hidden {
		t.Fatal("b still hidden")
	}
	if _, table = e.get(page); thOrder(table) != "a,c,b" {
		t.Fatalf("table order after show = %s", thOrder(table))
	}
	var after string
	_ = e.app.Deps.DB.QueryRow(`SELECT data_json FROM form_entries WHERE id = ?`, eid).Scan(&after)
	if before != after {
		t.Fatalf("data_json changed: %s -> %s", before, after)
	}
}

func TestDeleteCapturedForm(t *testing.T) {
	e := boot(t)
	cid := e.seedForm("cap", "captured", true, pinned("a"))
	e.seedEntry(cid, map[string]string{"a": "1"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(cid, map[string]string{"a": "2"}, "new", "2026-03-02T10:00:00Z")
	bid := e.seedForm("book", "builder", true, pinned("a"))
	e.fake.Content.Add(content.Item{ID: "page-1", Type: "page", Slug: "book", Title: "Book"}) // its page is live
	e.seedEntry(bid, map[string]string{"a": "1"}, "new", "2026-03-01T10:00:00Z")
	e.loginAdmin()

	resp, body := e.get("/admin/forms/cap/delete")
	if resp.StatusCode != 200 {
		t.Fatalf("confirm: %d", resp.StatusCode)
	}
	mustContain(t, body, `data-entries="2"`)
	if resp := e.adminPost("/admin/forms/cap/delete", "/admin/forms/cap/delete"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	var n int
	_ = e.app.Deps.DB.QueryRow(`SELECT (SELECT count(*) FROM forms WHERE slug='cap') + (SELECT count(*) FROM form_entries WHERE form_id = ?)`, cid).Scan(&n)
	if n != 0 {
		t.Fatalf("form/entries kept: %d", n)
	}
	if e.auditCount("site.form.delete_form") != 1 {
		t.Fatal("not audited")
	}

	// A builder form that is still open cannot be deleted.
	_, bp := e.get("/admin/forms/book/delete")
	resp, blocked := e.do("POST", "/admin/forms/book/delete", url.Values{"_csrf": {csrfFrom(t, bp)}})
	_ = bp
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("open builder delete: %d", resp.StatusCode)
	}
	mustContain(t, blocked, "هذا النموذج ما زال في صفحة")
	if _, _, ok := e.formRow("book"); !ok {
		t.Fatal("open builder form deleted")
	}
	// Once the page stops declaring it, it can.
	_, _ = e.app.Deps.DB.Exec(`UPDATE forms SET open = 0 WHERE slug = 'book'`)
	if resp := e.adminPost("/admin/forms/book/delete", "/admin/forms/book"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("closed builder delete: %d", resp.StatusCode)
	}
}

func TestFormsAdminForbidden(t *testing.T) {
	e := boot(t)
	e.seedForm("quote", "captured", true, pinned("a"))
	e.loginEditor()
	for _, p := range []string{"/admin/forms", "/admin/forms/quote", "/admin/forms/quote/export.csv", "/admin/forms/quote/delete"} {
		if resp, _ := e.get(p); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestNavBadgeCountsNew(t *testing.T) {
	// kernel.NavItem has no badge field, so the total of new entries across
	// forms is shown on the list page (data-new-total) and per form.
	e := boot(t)
	a := e.seedForm("a", "captured", true, pinned("x"))
	b := e.seedForm("b", "captured", true, pinned("x"))
	e.seedEntry(a, map[string]string{"x": "1"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(b, map[string]string{"x": "1"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(b, map[string]string{"x": "2"}, "spam", "2026-03-01T10:00:00Z")
	e.loginAdmin()
	_, body := e.get("/admin/forms")
	mustContain(t, body, `data-new-total="2"`)
}

// formsEnvFor returns a second, anonymous client on e's server and a CSRF
// token for its session.
func formsEnvFor(t *testing.T, e *env) (*env, string) {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	v := *e
	v.c = &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	_, page := v.get("/contact")
	return &v, csrfFrom(t, page)
}

// loginEditor logs in a user without settings.manage.
func (e *env) loginEditor() {
	e.t.Helper()
	us, _ := kernel.Get[auth.Service](e.app.Deps, kernel.ServiceAuth)
	if _, err := us.Create(context.Background(), "ed@example.com", "Ed", "correct horse battery", "editor"); err != nil {
		e.t.Fatal(err)
	}
	_, login := e.get("/admin/login")
	if resp, _ := e.do("POST", "/admin/login", url.Values{"_csrf": {csrfFrom(e.t, login)}, "email": {"ed@example.com"},
		"password": {"correct horse battery"}}); resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("editor login: %d", resp.StatusCode)
	}
}

func TestPinChoiceFieldSetsChoices(t *testing.T) {
	e, tok := formsEnv(t)
	if resp, _ := e.postForm("poll", tok, "name", "Sara", "plan", "a"); !okStatus(resp.StatusCode) {
		t.Fatalf("capture: %d", resp.StatusCode)
	}
	e.loginAdmin()
	pin := func(kv ...string) int {
		return e.adminPost("/admin/forms/poll/fields/plan/pin", "/admin/forms/poll", kv...).StatusCode
	}
	if c := pin("type", "choice"); c != http.StatusBadRequest {
		t.Fatalf("choice without choices: %d", c)
	}
	if c := pin("type", "choice", "choices", strings.Repeat("x\n", 0)+strings.Join(func() []string {
		var l []string
		for i := 0; i < 21; i++ {
			l = append(l, fmt.Sprintf("c%d", i))
		}
		return l
	}(), "\n")); c != http.StatusBadRequest {
		t.Fatalf("21 choices: %d", c)
	}
	if c := pin("type", "choice", "choices", "a\nb"); c != http.StatusSeeOther {
		t.Fatalf("pin: %d", c)
	}
	_, fields, _ := e.formRow("poll")
	for _, f := range fields {
		if f.Name == "plan" && strings.Join(f.Choices, ",") != "a,b" {
			t.Fatalf("choices = %v", f.Choices)
		}
	}
	v, vtok := formsEnvFor(t, e)
	if resp, _ := v.postForm("poll", vtok, "name", "X", "plan", "c"); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("c accepted: %d", resp.StatusCode)
	}
	if resp, _ := v.postForm("poll", vtok, "name", "X", "plan", "b"); !okStatus(resp.StatusCode) {
		t.Fatalf("b rejected: %d", resp.StatusCode)
	}
	_, page := e.get("/admin/forms/poll")
	mustContain(t, page, `name="choices"`, "يُفشل كل رسالة")
}

func TestExportLinkHasNoPage(t *testing.T) {
	e := boot(t)
	id := e.seedForm("quote", "captured", true, pinned("a"))
	e.seedEntry(id, map[string]string{"a": "1"}, "new", "2026-03-01T10:00:00Z")
	e.loginAdmin()
	_, body := e.get("/admin/forms/quote?status=new&q=x&page=2")
	mustContain(t, body, `href="/admin/forms/quote/export.csv?q=x&amp;status=new"`)
	_, plain := e.get("/admin/forms/quote")
	mustContain(t, plain, `href="/admin/forms/quote/export.csv"`)
}

// fieldOf returns the field named name of slug's schema.
func (e *env) fieldOf(slug, name string) site.FormField {
	e.t.Helper()
	_, fields, _ := e.formRow(slug)
	for _, f := range fields {
		if f.Name == name {
			return f
		}
	}
	e.t.Fatalf("%s has no field %s: %+v", slug, name, fields)
	return site.FormField{}
}

// thOrder lists the data-field names of the entries table header, in order.
func thOrder(body string) string {
	var out []string
	for _, m := range thRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return strings.Join(out, ",")
}

var thRe = regexp.MustCompile(`<th data-field="([^"]+)"`)

func TestAdminPresentationSurvivesRepublish(t *testing.T) {
	e := boot(t)
	defs := []site.FormDefinition{{Slug: "booking", Title: "Booking", Fields: []site.FormField{
		{Name: "name", Label: "Name", Type: "text"}, {Name: "email", Label: "Email", Type: "email"}, {Name: "phone", Label: "Phone", Type: "text"}}}}
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", defs); err != nil {
		t.Fatal(err)
	}
	def, _ := site.FormBySlug(e.app.Deps, "booking")
	e.seedEntry(def.ID, map[string]string{"name": "A", "email": "a@x.test", "phone": "1"}, "new", "2026-03-01T10:00:00Z")
	e.loginAdmin()
	page := "/admin/forms/booking"
	for _, p := range [][]string{{"/fields/name/rename", "label", "Full name"}, {"/fields/email/up"}, {"/fields/phone/hide"}} {
		if resp := e.adminPost(page+p[0], page, p[1:]...); resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s: %d", p[0], resp.StatusCode)
		}
	}
	check := func(when string) {
		t.Helper()
		_, body := e.get(page)
		if got := thOrder(body); got != "email,name" {
			t.Fatalf("%s: admin order = %s", when, got)
		}
		mustContain(t, body, "<bdi>Full name</bdi>")
		if f := e.fieldOf("booking", "name"); f.Label != "Name" || f.AdminLabel != "Full name" {
			t.Fatalf("%s: name field = %+v", when, f)
		}
		if !e.fieldOf("booking", "phone").Hidden {
			t.Fatalf("%s: phone not hidden", when)
		}
		out := string(e.formFunc()("booking", "tok", "/book", "en"))
		mustNotContain(t, out, "Full name")
		if i, j := strings.Index(out, `name="name"`), strings.Index(out, `name="email"`); i < 0 || j < 0 || i > j {
			t.Fatalf("%s: public order changed:\n%s", when, out)
		}
		mustContain(t, out, `>Name</label>`, `name="phone"`) // hidden is admin-only
	}
	check("before republish")
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", defs); err != nil {
		t.Fatal(err)
	}
	check("after republish")
	_, csvBody := e.get(page + "/export.csv")
	if got := strings.Join(csvRows(t, csvBody)[0], ","); got != "id,created_at,status,page,Email,Full name,Phone" {
		t.Fatalf("csv header = %s", got)
	}
}

func TestRetiredBuilderFieldHiddenButInCSV(t *testing.T) {
	e := boot(t)
	two := []site.FormField{{Name: "name", Label: "Name", Type: "text"}, {Name: "old", Label: "Old", Type: "text"}}
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{{Slug: "booking", Fields: two}}); err != nil {
		t.Fatal(err)
	}
	def, _ := site.FormBySlug(e.app.Deps, "booking")
	e.seedEntry(def.ID, map[string]string{"name": "A", "old": "legacy"}, "new", "2026-03-01T10:00:00Z")
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{{Slug: "booking", Fields: two[:1]}}); err != nil {
		t.Fatal(err)
	}
	if f := e.fieldOf("booking", "old"); !f.Hidden || f.State != "discovered" {
		t.Fatalf("retired field = %+v", f)
	}
	e.loginAdmin()
	_, body := e.get("/admin/forms/booking")
	if got := thOrder(body); got != "name" {
		t.Fatalf("table columns = %s", got)
	}
	_, csvBody := e.get("/admin/forms/booking/export.csv")
	rows := csvRows(t, csvBody)
	if strings.Join(rows[0], ",") != "id,created_at,status,page,Name,Old" || rows[1][5] != "legacy" {
		t.Fatalf("csv = %q", rows)
	}
	mustNotContain(t, string(e.formFunc()("booking", "tok", "/", "en")), `name="old"`)
}

func TestPinRefusedOnBuilderForm(t *testing.T) {
	e := boot(t)
	two := []site.FormField{{Name: "name", Label: "Name", Type: "text"}, {Name: "old", Label: "Old", Type: "text"}}
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{{Slug: "booking", Fields: two}}); err != nil {
		t.Fatal(err)
	}
	if err := site.UpsertBuilderForms(e.app.Deps, "page-1", []site.FormDefinition{{Slug: "booking", Fields: two[:1]}}); err != nil {
		t.Fatal(err)
	}
	e.loginAdmin()
	_, page := e.get("/admin/forms/booking")
	mustNotContain(t, page, "/fields/old/pin")
	_, html := e.get("/admin/forms/booking")
	resp, body := e.do("POST", "/admin/forms/booking/fields/old/pin", url.Values{"_csrf": {csrfFrom(t, html)}, "type": {"text"}})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pin on builder form: %d", resp.StatusCode)
	}
	mustContain(t, body, "منشئ الصفحات")
	if f := e.fieldOf("booking", "old"); f.State != "discovered" {
		t.Fatalf("field pinned: %+v", f)
	}
	mustNotContain(t, string(e.formFunc()("booking", "tok", "/", "en")), `name="old"`)
}

func TestFormsListShowsLastEntry(t *testing.T) {
	e := boot(t)
	id := e.seedForm("quote", "captured", true, pinned("name"))
	e.seedEntry(id, map[string]string{"name": "A"}, "new", "2026-03-01T10:00:00Z")
	e.seedEntry(id, map[string]string{"name": "B"}, "read", "2026-03-04T09:30:00Z")
	e.seedForm("empty", "captured", true, pinned("name"))
	e.loginAdmin()
	_, body := e.get("/admin/forms")
	mustContain(t, body, `data-last="2026-03-04T09:30:00Z"`, `data-last=""`, "آخر رسالة")
}

func TestSearchFindsHTMLCharacters(t *testing.T) {
	e, tok := formsEnv(t)
	if resp, _ := e.postForm("quote", tok, "dept", "R&D <team>"); !okStatus(resp.StatusCode) {
		t.Fatalf("capture: %d", resp.StatusCode)
	}
	if got := e.entryData("quote"); len(got) != 1 || got[0] != `{"dept":"R&D <team>"}` {
		t.Fatalf("stored = %v", got)
	}
	e.loginAdmin()
	_, body := e.get("/admin/forms/quote?q=" + url.QueryEscape("R&D"))
	mustContain(t, body, "<bdi>R&amp;D &lt;team&gt;</bdi>")
	_, body = e.get("/admin/forms/quote?q=" + url.QueryEscape("<team>"))
	mustContain(t, body, "<bdi>R&amp;D &lt;team&gt;</bdi>")
}

func TestDeleteBuilderFormOfDeletedPage(t *testing.T) {
	e := boot(t)
	// No content item "gone" exists: its page was hard-deleted.
	if err := site.UpsertBuilderForms(e.app.Deps, "gone", []site.FormDefinition{{Slug: "booking", Fields: []site.FormField{{Name: "name", Type: "text"}}}}); err != nil {
		t.Fatal(err)
	}
	e.loginAdmin()
	_, confirm := e.get("/admin/forms/booking/delete")
	mustNotContain(t, confirm, "هذا النموذج ما زال في صفحة")
	if resp := e.adminPost("/admin/forms/booking/delete", "/admin/forms/booking"); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, _, ok := e.formRow("booking"); ok {
		t.Fatal("form kept")
	}
}
