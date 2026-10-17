package content_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// flashed reports whether resp set a flash cookie carrying key.
func flashed(resp *http.Response, key string) bool {
	return strings.Contains(strings.Join(resp.Header.Values("Set-Cookie"), ";"), key)
}

// Every item screen answers 404 for an unknown type, an unknown id, or an
// id that belongs to another type, and changes nothing.
func TestAdminContentScreensRejectUnknownTypesAndItems(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	if err := cs.SaveType(ctx, content.Type{Slug: "other", Name: map[string]string{"en": "Other"}, Fields: []content.Field{
		{Key: "rows", Type: "repeater", SubFields: []content.Field{{Key: "n", Type: "text"}}}, {Key: "plain", Type: "text"}}}); err != nil {
		t.Fatal(err)
	}
	post, _ := cs.Create(ctx, content.Input{Type: "post", Title: "A post"})
	_, page := s.do("GET", "/admin/content/post", nil, nil)
	tok := url.Values{"_csrf": {s.csrf(page)}, "title": {"x"}}

	for _, tc := range []struct{ method, path string }{
		{"GET", "/admin/content/nope/new"},
		{"POST", "/admin/content/nope"},
		{"POST", "/admin/content/nope/bulk"},
		{"GET", "/admin/content/nope/" + post.ID},
		{"GET", "/admin/content/other/" + post.ID}, // wrong type
		{"POST", "/admin/content/nope/" + post.ID},
		{"POST", "/admin/content/other/" + post.ID},
		{"POST", "/admin/content/post/missing"},
		{"POST", "/admin/content/nope/" + post.ID + "/trash"},
		{"POST", "/admin/content/other/" + post.ID + "/trash"},
		{"POST", "/admin/content/post/missing/trash"},
		{"POST", "/admin/content/nope/" + post.ID + "/delete"},
		{"POST", "/admin/content/nope/" + post.ID + "/revisions/r/restore"},
		{"POST", "/admin/content/post/" + post.ID + "/revisions/missing/restore"},
		{"GET", "/admin/x/content/nope/row?field=rows"},
		{"GET", "/admin/x/content/other/row?field=plain"}, // not a repeater
		{"GET", "/admin/x/content/other/row?field=missing"},
		{"GET", "/admin/x/content/pick?type=nope"},
	} {
		var form url.Values
		if tc.method == "POST" {
			form = tok
		}
		if resp, _ := s.do(tc.method, tc.path, form, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: %d", tc.method, tc.path, resp.StatusCode)
		}
	}
	if got, err := cs.Get(ctx, post.ID); err != nil || got.Title != "A post" || got.Status == "trash" {
		t.Fatalf("item changed by rejected requests: %+v %v", got, err)
	}
	// An unknown status filter is ignored rather than failing the list.
	if resp, page := s.do("GET", "/admin/content/post?status=bogus", nil, nil); resp.StatusCode != 200 || !strings.Contains(page, "A post") {
		t.Fatalf("list with unknown status: %d", resp.StatusCode)
	}
}

// Bulk actions skip ids of other types and unknown ids, hard-delete only
// trashed items, and flash a message matching what happened.
func TestAdminBulkOutcomes(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	live, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Live"})
	trashed, _ := cs.Create(ctx, content.Input{Type: "post", Title: "Trashed"})
	if err := cs.Delete(ctx, trashed.ID, false); err != nil {
		t.Fatal(err)
	}
	page, _ := cs.Create(ctx, content.Input{Type: "page", Title: "A page"})
	_, body := s.do("GET", "/admin/content/post", nil, nil)
	csrf := s.csrf(body)

	resp, _ := s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {csrf}, "action": {"delete"}, "ids": {"missing", page.ID}}, nil)
	if resp.StatusCode != http.StatusSeeOther || !flashed(resp, "content.flash.bulk_none") {
		t.Fatalf("bulk none: %d %v", resp.StatusCode, resp.Header.Values("Set-Cookie"))
	}
	if got, _ := cs.Get(ctx, page.ID); got.Status == "trash" {
		t.Fatal("bulk touched an item of another type")
	}

	resp, _ = s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {csrf}, "action": {"delete"}, "ids": {live.ID, trashed.ID}}, nil)
	if resp.StatusCode != http.StatusSeeOther || !flashed(resp, "content.flash.bulk_mixed") {
		t.Fatalf("bulk mixed: %d %v", resp.StatusCode, resp.Header.Values("Set-Cookie"))
	}
	if _, err := cs.Get(ctx, trashed.ID); err == nil {
		t.Fatal("trashed item not purged")
	}
	if got, _ := cs.Get(ctx, live.ID); got.Status != "trash" {
		t.Fatalf("live item not trashed: %s", got.Status)
	}

	resp, _ = s.do("POST", "/admin/content/post/bulk", url.Values{"_csrf": {csrf}, "action": {"delete"}, "ids": {live.ID}}, nil)
	if resp.StatusCode != http.StatusSeeOther || !flashed(resp, "content.flash.bulk_deleted") {
		t.Fatalf("bulk deleted: %d %v", resp.StatusCode, resp.Header.Values("Set-Cookie"))
	}
	if _, err := cs.Get(ctx, live.ID); err == nil {
		t.Fatal("item not purged")
	}
}

// The new-item editor pre-fills field defaults and lists templates; the
// author select lists staff (never customers), labelled by name or email.
func TestAdminEditorDefaultsTemplatesAndAuthors(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	if err := cs.SaveType(ctx, content.Type{Slug: "recipe", Name: map[string]string{"en": "Recipes"}, Supports: []string{"title", "author"},
		Templates: []string{"wide-layout"}, Fields: []content.Field{{Key: "serves", Type: "text", Default: "four people"}}}); err != nil {
		t.Fatal(err)
	}
	us, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	if _, err := us.Create(ctx, "buyer@example.com", "Buyer Person", "correct horse battery", "customer"); err != nil {
		t.Fatal(err)
	}
	noName, nameErr := us.Create(ctx, "nameless@example.com", "", "correct horse battery", "editor")

	resp, page := s.do("GET", "/admin/content/recipe/new", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("new editor: %d", resp.StatusCode)
	}
	if !strings.Contains(page, "four people") {
		t.Error("field default not pre-filled")
	}
	if !strings.Contains(page, "wide-layout") {
		t.Error("template choice missing")
	}
	if strings.Contains(page, "Buyer Person") {
		t.Error("a customer is offered as author")
	}
	if nameErr == nil && !strings.Contains(page, `value="`+noName.ID+`"`) {
		t.Error("a staff user without a name is missing from the author select")
	}
}

// A rejected submission keeps the menu order typed; clearing an SEO input
// removes that key; a revision that no longer validates is not restored.
func TestAdminEditorFormOverlayAndRestoreInvalid(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	typ := content.Type{Slug: "doc", Name: map[string]string{"en": "Docs"}, Hierarchical: true,
		Supports: []string{"title", "seo", "menu_order", "parent", "revisions"},
		Fields:   []content.Field{{Key: "summary", Type: "text"}}}
	if err := cs.SaveType(ctx, typ); err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "doc", Title: "v1", SEO: map[string]any{"title": "Old SEO", "description": "Keep me"}})
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/content/doc/"+it.ID, nil, nil)
	csrf := s.csrf(page)

	resp, _ := s.do("POST", "/admin/content/doc/"+it.ID, url.Values{"_csrf": {csrf}, "title": {"v2"}, "status": {"draft"},
		"seo[title]": {"  "}, "seo[description]": {"Keep me"}, "menu_order": {" 7 "}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update: %d", resp.StatusCode)
	}
	got, _ := cs.Get(ctx, it.ID)
	if _, ok := got.SEO["title"]; ok || got.SEO["description"] != "Keep me" || got.MenuOrder != 7 || got.Title != "v2" {
		t.Fatalf("saved: seo=%v order=%d title=%q", got.SEO, got.MenuOrder, got.Title)
	}

	// Make summary required: a submission without it is rejected and the
	// editor re-renders with the typed menu order.
	typ.Fields = []content.Field{{Key: "summary", Type: "text", Required: true}}
	if err := cs.SaveType(ctx, typ); err != nil {
		t.Fatal(err)
	}
	resp, page = s.do("POST", "/admin/content/doc/"+it.ID, url.Values{"_csrf": {csrf}, "title": {"v3"}, "status": {"draft"}, "menu_order": {"12"}}, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, `value="12"`) || !strings.Contains(page, "v3") {
		t.Fatalf("invalid save: %d", resp.StatusCode)
	}
	if got, _ := cs.Get(ctx, it.ID); got.Title != "v2" || got.MenuOrder != 7 {
		t.Fatalf("rejected save changed the item: %q %d", got.Title, got.MenuOrder)
	}

	// The oldest revision lacks the now-required field: restoring it
	// redirects back with an error flash and leaves the item as it was.
	revs, err := cs.Revisions(ctx, it.ID)
	if err != nil || len(revs) == 0 {
		t.Fatalf("revisions: %v %d", err, len(revs))
	}
	resp, _ = s.do("POST", "/admin/content/doc/"+it.ID+"/revisions/"+revs[len(revs)-1].ID+"/restore", url.Values{"_csrf": {csrf}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/content/doc/"+it.ID || !flashed(resp, "content.flash.restore_invalid") {
		t.Fatalf("restore invalid: %d %s %v", resp.StatusCode, resp.Header.Get("Location"), resp.Header.Values("Set-Cookie"))
	}
	if got, _ := cs.Get(ctx, it.ID); got.Title != "v2" {
		t.Fatalf("invalid restore changed the item: %q", got.Title)
	}
}
