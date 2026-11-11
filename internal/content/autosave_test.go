package content_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"html"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

var jsonHeaders = map[string]string{"Accept": "application/json"}

func autosaveCount(t *testing.T, d *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM content_autosaves WHERE content_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func revisionCount(t *testing.T, d *sql.DB, id string) int {
	t.Helper()
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM revisions WHERE content_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A published item autosaves into the user's record: the live item, its
// revisions and its edit token stay as they are; the editor offers a restore;
// a normal save clears the record.
func TestAutosaveKeepsPublishedItemAndOffersRestore(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Live title", Body: "<p>Live body</p>", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/post/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	if !strings.Contains(page, `data-autosave-url="`+path+`/autosave"`) || strings.Contains(page, "data-autosave-banner") {
		t.Fatal("editor misses the autosave endpoint or shows a banner without an autosave")
	}
	token := editField(t, page, "edit_token")
	revs := revisionCount(t, s.app.Deps.DB, it.ID)
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {token}, "title": {"Draft title"}, "body": {"<p>Draft body</p>"},
		"status": {"draft"}, "seo[title]": {"Draft SEO"}}
	resp, body := s.do("POST", path+"/autosave", form, jsonHeaders)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"saved_at"`) {
		t.Fatalf("autosave %d %s", resp.StatusCode, body)
	}
	got, _ := cs.Get(ctx, it.ID)
	if got.Title != "Live title" || got.Body != "<p>Live body</p>" || got.Status != "published" || content.EditToken(got) != token {
		t.Fatalf("autosave changed the live item: %+v", got)
	}
	if n := revisionCount(t, s.app.Deps.DB, it.ID); n != revs {
		t.Fatalf("autosave added revisions: %d -> %d", revs, n)
	}

	_, page = s.do("GET", path, nil, nil)
	m := regexp.MustCompile(`data-autosave-payload="([^"]*)"`).FindStringSubmatch(page)
	if m == nil || strings.Contains(page, "data-autosave-stale") {
		t.Fatal("editor misses a fresh restore banner")
	}
	var payload map[string][]string
	if err := json.Unmarshal([]byte(html.UnescapeString(m[1])), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["title"][0] != "Draft title" || payload["seo[title]"][0] != "Draft SEO" || payload["status"] != nil {
		t.Fatalf("payload %+v", payload)
	}

	// A normal save by the same user removes the autosave.
	save := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")},
		"title": {"Draft title"}, "body": {"<p>Draft body</p>"}}
	if resp, _ = s.do("POST", path, save, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save %d", resp.StatusCode)
	}
	if n := autosaveCount(t, s.app.Deps.DB, it.ID); n != 0 {
		t.Fatalf("autosave kept after save: %d", n)
	}
}

// When someone saves the item after the editor opened, autosave refuses
// with 409 and stores nothing.
func TestAutosaveConflictStoresNothing(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Original", Body: "<p>One</p>"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/post/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	token := editField(t, page, "edit_token")
	in := content.InputFromItem(it)
	in.Title = "Someone else"
	if _, err := cs.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {token}, "title": {"Mine"}}
	resp, body := s.do("POST", path+"/autosave", form, jsonHeaders)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, `"code":"conflict"`) {
		t.Fatalf("stale autosave %d %s", resp.StatusCode, body)
	}
	form.Set("edit_token", "")
	if resp, _ = s.do("POST", path+"/autosave", form, jsonHeaders); resp.StatusCode != http.StatusConflict {
		t.Fatalf("empty token %d", resp.StatusCode)
	}
	if n := autosaveCount(t, s.app.Deps.DB, it.ID); n != 0 {
		t.Fatalf("conflicting autosave stored: %d", n)
	}
	if got, _ := cs.Get(ctx, it.ID); got.Title != "Someone else" {
		t.Fatalf("newer save overwritten: %q", got.Title)
	}
}

// An autosave older than the current version shows the stale warning.
func TestAutosaveBannerWarnsWhenItemMovedOn(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Base"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/post/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "title": {"Autosaved"}}
	if resp, _ := s.do("POST", path+"/autosave", form, jsonHeaders); resp.StatusCode != http.StatusOK {
		t.Fatalf("autosave %d", resp.StatusCode)
	}
	in := content.InputFromItem(it)
	in.Title = "Newer"
	if _, err := cs.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	_, page = s.do("GET", path, nil, nil)
	if !strings.Contains(page, "data-autosave-stale") {
		t.Fatal("stale autosave not flagged")
	}
	// Discard (plain form post) removes it and returns to the editor.
	resp, _ := s.do("POST", path+"/autosave/discard", url.Values{"_csrf": {s.csrf(page)}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != path {
		t.Fatalf("discard %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if n := autosaveCount(t, s.app.Deps.DB, it.ID); n != 0 {
		t.Fatalf("discard kept %d", n)
	}
}

// Autosave needs the same rights as editing the item: authors only autosave
// their own items, roles without content.edit never.
func TestAutosavePermission(t *testing.T) {
	s := loggedIn(t, "author")
	cs := contentService(t, s.app)
	ctx := context.Background()
	other := mustUser(t, s.app, "other@example.com", "editor")
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Not yours", AuthorID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/content/post/new", nil, nil)
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {content.EditToken(it)}, "title": {"Hijack"}}
	resp, body := s.do("POST", "/admin/content/post/"+it.ID+"/autosave", form, jsonHeaders)
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(body, `"code":"forbidden"`) {
		t.Fatalf("foreign autosave %d %s", resp.StatusCode, body)
	}
	if n := autosaveCount(t, s.app.Deps.DB, it.ID); n != 0 {
		t.Fatalf("foreign autosave stored: %d", n)
	}

	// A signed-in reader (content.view only) sees the editor but cannot autosave.
	reader := readerSession(t, s)
	_, page = reader.do("GET", "/admin/content/post/"+it.ID, nil, nil)
	if strings.Contains(page, "data-autosave-url") {
		t.Fatal("reader offered autosave")
	}
	form = url.Values{"_csrf": {reader.csrf(page)}, "edit_token": {content.EditToken(it)}, "title": {"x"}}
	if resp, _ = reader.do("POST", "/admin/content/post/"+it.ID+"/autosave", form, jsonHeaders); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("reader autosave %d", resp.StatusCode)
	}
	if n := autosaveCount(t, s.app.Deps.DB, it.ID); n != 0 {
		t.Fatalf("reader autosave stored: %d", n)
	}
}

// readerSession signs in, on s's server, a user whose role holds only content.view.
func readerSession(t *testing.T, s *site) *site {
	t.Helper()
	roles, _ := kernel.Get[auth.RoleService](s.app.Deps, kernel.ServiceRoles)
	if _, err := roles.Create(context.Background(), "reader", map[string]string{"en": "Reader", "ar": "قارئ"}, []string{"content.view"}); err != nil {
		t.Fatal(err)
	}
	mustUser(t, s.app, "reader@example.com", "reader")
	jar, _ := cookiejar.New(nil)
	r := &site{t: t, srv: s.srv, app: s.app, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	_, page := r.do("GET", "/admin/login", nil, nil)
	resp, _ := r.do("POST", "/admin/login", url.Values{"_csrf": {r.csrf(page)}, "email": {"reader@example.com"}, "password": {"correct horse battery"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reader login %d", resp.StatusCode)
	}
	return r
}
