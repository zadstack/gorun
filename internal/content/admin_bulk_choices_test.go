package content_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// crowdWithCustomers adds n customers whose names sort before every staff
// user, the shape of a shop with many accounts.
func crowdWithCustomers(t *testing.T, app *kernel.App, n int) {
	t.Helper()
	for i := range n {
		if _, err := app.Deps.DB.Exec(`INSERT INTO users (id, email, name, role, created_at, updated_at)
			VALUES (?, ?, ?, 'customer', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			fmt.Sprintf("cust%04d", i), fmt.Sprintf("c%d@example.com", i), fmt.Sprintf("Aaa Customer %04d", i)); err != nil {
			t.Fatal(err)
		}
	}
}

func namedUser(t *testing.T, app *kernel.App, email, name, role string) *auth.User {
	t.Helper()
	u := mustUser(t, app, email, role)
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := us.Update(context.Background(), u.ID, auth.UserPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func addTerm(t *testing.T, app *kernel.App, tax, slug, name string) {
	t.Helper()
	if _, err := app.Deps.DB.Exec(`INSERT INTO terms (id, taxonomy, slug, name) VALUES (?, ?, ?, ?)`, "term-"+tax+"-"+slug, tax, slug, name); err != nil {
		t.Fatal(err)
	}
}

// The list page renders no author or term options: the bulk bar asks for
// them only when the action is chosen. The author choices are filtered in
// SQL, so staff whose names sort after many customers are still offered,
// and both lists can be searched.
func TestAdminBulkChoicesLoadOnDemand(t *testing.T) {
	s := english(t, loggedIn(t, "admin", taxonomy.New()))
	crowdWithCustomers(t, s.app, 150)
	zed := namedUser(t, s.app, "zed@example.com", "Zed Editor", "editor")
	addTerm(t, s.app, "category", "news", "News")
	addTerm(t, s.app, "tag", "sport", "Sport")
	cs := contentService(t, s.app)
	if _, err := cs.Create(context.Background(), content.Input{Type: "post", Title: "A"}); err != nil {
		t.Fatal(err)
	}

	_, page := s.do("GET", "/admin/content/post", nil, nil)
	for _, want := range []string{`value="author"`, `value="term_add"`, `value="term_remove"`,
		`data-choices-src="/admin/x/content/post/choices?kind=author"`, `data-choices-src="/admin/x/content/post/choices?kind=term"`} {
		if !strings.Contains(page, want) {
			t.Errorf("list lacks %q", want)
		}
	}
	for _, unwanted := range []string{zed.ID, "cust0001", `value="category/news"`} {
		if strings.Contains(page, unwanted) {
			t.Errorf("list renders choice %q up front", unwanted)
		}
	}

	resp, opts := s.do("GET", "/admin/x/content/post/choices?kind=author", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(opts, `value="`+zed.ID+`"`) || !strings.Contains(opts, "Zed Editor") {
		t.Fatalf("author choices %d lack the editor past 150 customers:\n%s", resp.StatusCode, opts)
	}
	if strings.Contains(opts, "cust0") || strings.Contains(opts, "<html") {
		t.Fatalf("author choices offer customers or a full page:\n%s", opts)
	}
	if _, opts = s.do("GET", "/admin/x/content/post/choices?kind=author&q=zed", nil, nil); !strings.Contains(opts, zed.ID) {
		t.Error("search does not find the editor")
	}
	if _, opts = s.do("GET", "/admin/x/content/post/choices?kind=author&q=nobody", nil, nil); strings.Contains(opts, zed.ID) {
		t.Error("search ignores the query")
	}

	_, opts = s.do("GET", "/admin/x/content/post/choices?kind=term", nil, nil)
	for _, want := range []string{`value="category/news"`, ">News<", `value="tag/sport"`, "<optgroup"} {
		if !strings.Contains(opts, want) {
			t.Errorf("term choices lack %q:\n%s", want, opts)
		}
	}
	if _, opts = s.do("GET", "/admin/x/content/post/choices?kind=term&q=spo", nil, nil); strings.Contains(opts, "category/news") || !strings.Contains(opts, "tag/sport") {
		t.Errorf("term search:\n%s", opts)
	}
	if resp, _ = s.do("GET", "/admin/x/content/post/choices?kind=bogus", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown kind: %d", resp.StatusCode)
	}
	if resp, _ = s.do("GET", "/admin/x/content/nope/choices?kind=term", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown type: %d", resp.StatusCode)
	}
}

// Authors keep their byline, so they may not list the people an item could
// be reassigned to; they may still list terms.
func TestAdminBulkChoicesPermissions(t *testing.T) {
	s := loggedIn(t, "author", taxonomy.New())
	addTerm(t, s.app, "category", "news", "News")
	if resp, body := s.do("GET", "/admin/x/content/post/choices?kind=author", nil, nil); resp.StatusCode != http.StatusForbidden || strings.Contains(body, "author@example.com") {
		t.Fatalf("author lists authors: %d", resp.StatusCode)
	}
	if resp, body := s.do("GET", "/admin/x/content/post/choices?kind=term", nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(body, "category/news") {
		t.Fatalf("author term choices: %d", resp.StatusCode)
	}
}

// Without JavaScript the select stays empty: posting the author or term
// action without a value shows a server-rendered step that asks for it,
// keeps the selection, and can search; choosing then applies the action.
func TestAdminBulkChooseStepWithoutJS(t *testing.T) {
	s := english(t, loggedIn(t, "admin", taxonomy.New()))
	crowdWithCustomers(t, s.app, 120)
	zed := namedUser(t, s.app, "zed@example.com", "Zed Editor", "editor")
	addTerm(t, s.app, "category", "news", "News")
	cs := contentService(t, s.app)
	ctx := context.Background()
	a, _ := cs.Create(ctx, content.Input{Type: "post", Title: "A"})
	b, _ := cs.Create(ctx, content.Input{Type: "post", Title: "B"})

	_, list := s.do("GET", "/admin/content/post?status=draft", nil, nil)
	form := url.Values{"_csrf": {s.csrf(list)}, "action": {"author"}, "return": {"status=draft"}, "ids": {a.ID, b.ID}}
	resp, step := s.do("POST", "/admin/content/post/bulk", form, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("author step: %d", resp.StatusCode)
	}
	for _, want := range []string{`name="author_id"`, `value="` + zed.ID + `"`, `name="ids" value="` + a.ID + `"`, `name="ids" value="` + b.ID + `"`,
		`name="action" value="author"`, `name="return" value="status=draft"`, `name="q"`} {
		if !strings.Contains(step, want) {
			t.Errorf("author step lacks %q", want)
		}
	}
	if strings.Contains(step, "cust0") {
		t.Error("author step offers customers")
	}
	form.Set("step", "search")
	form.Set("q", "nobody")
	if _, step = s.do("POST", "/admin/content/post/bulk", form, nil); strings.Contains(step, `value="`+zed.ID+`"`) || !strings.Contains(step, `value="nobody"`) {
		t.Error("step search ignores the query")
	}
	if status(t, cs, a.ID) != "draft" {
		t.Fatal("the step changed an item")
	}

	form = url.Values{"_csrf": {s.csrf(list)}, "action": {"term_add"}, "ids": {a.ID}}
	if resp, step = s.do("POST", "/admin/content/post/bulk", form, nil); resp.StatusCode != http.StatusOK || !strings.Contains(step, `value="category/news"`) {
		t.Fatalf("term step %d:\n%s", resp.StatusCode, step)
	}

	resp = bulkPost(t, s, "post", url.Values{"action": {"author"}, "author_id": {zed.ID}, "return": {"status=draft"}, "ids": {a.ID, b.ID}})
	if loc := resp.Header.Get("Location"); loc != "/admin/content/post?status=draft" {
		t.Fatalf("redirect %q", loc)
	}
	if it, _ := cs.Get(ctx, b.ID); it.AuthorID != zed.ID {
		t.Fatalf("author not applied: %s", it.AuthorID)
	}
	// Nothing selected: no step, the usual notice.
	if resp = bulkPost(t, s, "post", url.Values{"action": {"author"}}); !flashed(resp, "content.flash.bulk_none") {
		t.Error("empty selection reached the step")
	}
}

// The editor's author select uses the same SQL filter, and keeps the
// item's current author selected even when that user may no longer write.
func TestEditorAuthorChoicesKeepCurrentAuthor(t *testing.T) {
	s := english(t, loggedIn(t, "admin"))
	crowdWithCustomers(t, s.app, 120)
	zed := namedUser(t, s.app, "zed@example.com", "Zed Editor", "editor")
	gone := namedUser(t, s.app, "gone@example.com", "Gone Writer", "editor")
	us, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	disabled := auth.StatusDisabled
	if _, err := us.Update(context.Background(), gone.ID, auth.UserPatch{Status: &disabled}); err != nil {
		t.Fatal(err)
	}
	cs := contentService(t, s.app)
	it, _ := cs.Create(context.Background(), content.Input{Type: "post", Title: "Old byline", AuthorID: gone.ID})

	_, page := s.do("GET", "/admin/content/post/"+it.ID, nil, nil)
	if !strings.Contains(page, `value="`+zed.ID+`"`) {
		t.Error("editor lacks an eligible author past 120 customers")
	}
	if !strings.Contains(page, `value="`+gone.ID+`" selected`) {
		t.Error("editor dropped the current author")
	}
	if strings.Contains(page, `value="cust0`) {
		t.Error("editor offers customers")
	}
}
