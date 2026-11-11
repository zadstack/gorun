package content_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func userByEmail(t *testing.T, app *kernel.App, email string) *auth.User {
	t.Helper()
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := us.GetByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func status(t *testing.T, cs content.Service, id string) content.Status {
	t.Helper()
	it, err := cs.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it.Status
}

// english switches the signed-in admin to English (the default UI is Arabic).
func english(t *testing.T, s *site) *site {
	t.Helper()
	_, page := s.do("GET", "/admin/content/post", nil, nil)
	if resp, _ := s.do("POST", "/admin/locale", url.Values{"_csrf": {s.csrf(page)}, "locale": {"en"}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("locale switch: %d", resp.StatusCode)
	}
	return s
}

func bulkPost(t *testing.T, s *site, typ string, form url.Values) *http.Response {
	t.Helper()
	_, page := s.do("GET", "/admin/content/"+typ, nil, nil)
	form.Set("_csrf", s.csrf(page))
	resp, _ := s.do("POST", "/admin/content/"+typ+"/bulk", form, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bulk %v: %d", form, resp.StatusCode)
	}
	return resp
}

// An author bulk-publishes a mixed selection: their own drafts go live
// through the single-item path (audited), another author's draft, an
// already published item and a trashed item are skipped and reported by
// reason, and the summary is shown once on the filtered list.
func TestAdminBulkPublishReportsEachItem(t *testing.T) {
	s := english(t, loggedIn(t, "author"))
	cs := contentService(t, s.app)
	ctx := context.Background()
	me := userByEmail(t, s.app, "author@example.com")
	other := mustUser(t, s.app, "other@example.com", "author")
	mineA, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Mine A", AuthorID: me.ID})
	mineB, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Mine B", AuthorID: me.ID})
	theirs, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Their draft", AuthorID: other.ID})
	live, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Already live", AuthorID: me.ID, Status: "published"})
	binned, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Binned", AuthorID: me.ID})
	if err := cs.Delete(ctx, binned.ID, false); err != nil {
		t.Fatal(err)
	}

	resp := bulkPost(t, s, "post", url.Values{"action": {"publish"}, "return": {"status=draft&bogus=1"},
		"ids": {mineA.ID, mineB.ID, theirs.ID, live.ID, binned.ID, mineA.ID}})
	if loc := resp.Header.Get("Location"); loc != "/admin/content/post?status=draft" {
		t.Fatalf("redirect %q", loc)
	}
	for id, want := range map[string]content.Status{mineA.ID: "published", mineB.ID: "published", theirs.ID: "draft", binned.ID: "trash"} {
		if got := status(t, cs, id); got != want {
			t.Errorf("%s: status %s, want %s", id, got, want)
		}
	}
	var audited int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='content.publish' AND target_id IN (?,?)`, mineA.ID, mineB.ID).Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("publish audit entries %d %v", audited, err)
	}

	_, page := s.do("GET", "/admin/content/post?status=draft", nil, nil)
	for _, want := range []string{"data-bulk-result", "2 published", "3 skipped", "Not yours", "An item you cannot open",
		"Already in that state", "<bdi>Already live</bdi>", "In the trash", "<bdi>Binned</bdi>"} {
		if !strings.Contains(page, want) {
			t.Errorf("summary lacks %q", want)
		}
	}
	if strings.Contains(page, "Their draft") {
		t.Error("summary shows the title of an item the author may not open")
	}
	if _, page = s.do("GET", "/admin/content/post", nil, nil); strings.Contains(page, "data-bulk-result") {
		t.Error("summary shown twice")
	}
}

// A contributor may not publish: every item is refused one by one, and the
// list offers no bulk controls (no publish, delete, author or term actions).
func TestAdminBulkPublishRefusedWithoutPermission(t *testing.T) {
	s := english(t, loggedIn(t, "contributor"))
	cs := contentService(t, s.app)
	ctx := context.Background()
	me := userByEmail(t, s.app, "contributor@example.com")
	mine, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Contributor draft", AuthorID: me.ID})

	_, page := s.do("GET", "/admin/content/post", nil, nil)
	if strings.Contains(page, `name="ids"`) || strings.Contains(page, `data-bulk-action`) {
		t.Fatal("contributor offered bulk actions")
	}
	bulkPost(t, s, "post", url.Values{"action": {"publish"}, "ids": {mine.ID}})
	if got := status(t, cs, mine.ID); got != "draft" {
		t.Fatalf("contributor published: %s", got)
	}
	_, page = s.do("GET", "/admin/content/post", nil, nil)
	for _, want := range []string{"0 published", "1 skipped", "No permission", "<bdi>Contributor draft</bdi>"} {
		if !strings.Contains(page, want) {
			t.Errorf("summary lacks %q", want)
		}
	}
	// Trash and delete still need content.delete for the whole request.
	_, page = s.do("GET", "/admin/content/post", nil, nil)
	if resp, _ := s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {s.csrf(page)}, "action": {"trash"}, "ids": {mine.ID}}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("contributor bulk trash: %d", resp.StatusCode)
	}
}

// Scheduling needs a valid future time; a past or missing one refuses the
// whole request before any item changes. Unpublish returns live items to
// draft and skips drafts.
func TestAdminBulkScheduleAndUnpublish(t *testing.T) {
	s := english(t, loggedIn(t, "admin"))
	cs := contentService(t, s.app)
	ctx := context.Background()
	a, _ := cs.Create(ctx, content.Input{Type: "post", Title: "A"})
	b, _ := cs.Create(ctx, content.Input{Type: "post", Title: "B", Status: "published"})

	for at, key := range map[string]string{"": "content.bulk.error.schedule_invalid", "tomorrow": "content.bulk.error.schedule_invalid",
		time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04"): "content.bulk.error.schedule_past"} {
		resp := bulkPost(t, s, "post", url.Values{"action": {"schedule"}, "publish_at": {at}, "ids": {a.ID, b.ID}})
		if !flashed(resp, key) {
			t.Errorf("schedule %q: want flash %s, got %v", at, key, resp.Header.Values("Set-Cookie"))
		}
	}
	if status(t, cs, a.ID) != "draft" || status(t, cs, b.ID) != "published" {
		t.Fatal("a refused schedule changed items")
	}

	future := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Minute)
	bulkPost(t, s, "post", url.Values{"action": {"schedule"}, "publish_at": {future.Format("2006-01-02T15:04")}, "ids": {a.ID, b.ID}})
	for _, id := range []string{a.ID, b.ID} {
		it, _ := cs.Get(ctx, id)
		at, _ := content.ParseDatetime(it.PublishAt)
		if it.Status != "scheduled" || !at.Equal(future) {
			t.Errorf("%s not scheduled for %v: %s %s", id, future, it.Status, it.PublishAt)
		}
	}
	_, page := s.do("GET", "/admin/content/post", nil, nil)
	if !strings.Contains(page, "2 scheduled") {
		t.Error("schedule summary missing")
	}

	c, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Still a draft"})
	bulkPost(t, s, "post", url.Values{"action": {"unpublish"}, "ids": {a.ID, c.ID}})
	if status(t, cs, a.ID) != "draft" {
		t.Fatal("unpublish left the item scheduled")
	}
	_, page = s.do("GET", "/admin/content/post", nil, nil)
	for _, want := range []string{"1 moved back to draft", "1 skipped", "<bdi>Still a draft</bdi>"} {
		if !strings.Contains(page, want) {
			t.Errorf("unpublish summary lacks %q", want)
		}
	}

	resp := bulkPost(t, s, "post", url.Values{"action": {"explode"}, "ids": {a.ID}})
	if !flashed(resp, "content.bulk.error.action") || status(t, cs, a.ID) != "draft" {
		t.Fatal("unknown bulk action was not refused")
	}
}

// Author changes accept only active users who may write content; terms are
// added and removed through the editor's save path; authors keep their
// byline.
func TestAdminBulkAuthorAndTerms(t *testing.T) {
	s := english(t, loggedIn(t, "admin", taxonomy.New()))
	cs := contentService(t, s.app)
	ctx := context.Background()
	editor := mustUser(t, s.app, "editor@example.com", "editor")
	customer := mustUser(t, s.app, "customer@example.com", "customer")
	a, _ := cs.Create(ctx, content.Input{Type: "post", Title: "A"})
	b, _ := cs.Create(ctx, content.Input{Type: "post", Title: "B"})
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO terms (id, taxonomy, slug, name) VALUES ('term-news', 'category', 'news', 'News')`); err != nil {
		t.Fatal(err)
	}

	_, page := s.do("GET", "/admin/content/post", nil, nil)
	for _, want := range []string{`value="author"`, `value="term_add"`, `value="category/news"`, `>News<`, editor.ID} {
		if !strings.Contains(page, want) {
			t.Errorf("bulk controls lack %q", want)
		}
	}
	if strings.Contains(page, customer.ID) {
		t.Error("a customer is offered as author")
	}

	for _, id := range []string{customer.ID, "missing", ""} {
		if resp := bulkPost(t, s, "post", url.Values{"action": {"author"}, "author_id": {id}, "ids": {a.ID}}); !flashed(resp, "content.bulk.error.author_invalid") {
			t.Errorf("author %q accepted", id)
		}
	}
	bulkPost(t, s, "post", url.Values{"action": {"author"}, "author_id": {editor.ID}, "ids": {a.ID, b.ID}})
	for _, id := range []string{a.ID, b.ID} {
		if it, _ := cs.Get(ctx, id); it.AuthorID != editor.ID {
			t.Errorf("%s author %s", id, it.AuthorID)
		}
	}
	_, page = s.do("GET", "/admin/content/post", nil, nil)
	if !strings.Contains(page, "2 given a new author") {
		t.Error("author summary missing")
	}

	for _, term := range []string{"category/nope", "nope/news", "news", ""} {
		if resp := bulkPost(t, s, "post", url.Values{"action": {"term_add"}, "term": {term}, "ids": {a.ID}}); !flashed(resp, "content.bulk.error.term_invalid") {
			t.Errorf("term %q accepted", term)
		}
	}
	bulkPost(t, s, "post", url.Values{"action": {"term_add"}, "term": {"category/news"}, "ids": {a.ID}})
	if it, _ := cs.Get(ctx, a.ID); len(it.Terms["category"]) != 1 || it.Terms["category"][0] != "news" {
		t.Fatalf("term not added: %v", it.Terms)
	}
	bulkPost(t, s, "post", url.Values{"action": {"term_remove"}, "term": {"category/news"}, "ids": {a.ID, b.ID}})
	if it, _ := cs.Get(ctx, a.ID); len(it.Terms["category"]) != 0 {
		t.Fatalf("term not removed: %v", it.Terms)
	}
	_, page = s.do("GET", "/admin/content/post", nil, nil)
	if !strings.Contains(page, "Term removed from 1") || !strings.Contains(page, "<bdi>B</bdi>") {
		t.Error("term summary missing the unchanged item")
	}
}

// Authors keep their byline: the author action is not offered and is
// refused per item when posted.
func TestAdminBulkAuthorRefusedForAuthors(t *testing.T) {
	s := english(t, loggedIn(t, "author"))
	cs := contentService(t, s.app)
	ctx := context.Background()
	me := userByEmail(t, s.app, "author@example.com")
	editor := mustUser(t, s.app, "editor@example.com", "editor")
	mine, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Mine", AuthorID: me.ID})
	_, page := s.do("GET", "/admin/content/post", nil, nil)
	if strings.Contains(page, `value="author"`) {
		t.Fatal("author offered the author action")
	}
	bulkPost(t, s, "post", url.Values{"action": {"author"}, "author_id": {editor.ID}, "ids": {mine.ID}})
	if it, _ := cs.Get(ctx, mine.ID); it.AuthorID != me.ID {
		t.Fatalf("author reassigned their item to %s", it.AuthorID)
	}
	if _, page = s.do("GET", "/admin/content/post", nil, nil); !strings.Contains(page, "No permission") {
		t.Error("refusal not reported")
	}
}
