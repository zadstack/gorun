package content_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func editField(t *testing.T, page, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("missing %s", name)
	}
	return m[1]
}
func TestAdminEditTokenRejectsStaleAndAcceptsMatchingSave(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Original", Body: "<p>Original</p>", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/page/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	token := editField(t, page, "edit_token")
	in := content.InputFromItem(it)
	in.Body = "<p>Concurrent human edit</p>"
	current, err := cs.Update(ctx, it.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {token}, "terms_token": {editField(t, page, "terms_token")}, "title": {"Stale title"}, "body": {"<p>Original</p>"}}
	resp, conflictPage := s.do("POST", path, form, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale save %d", resp.StatusCode)
	}
	if !strings.Contains(conflictPage, `value="Stale title"`) || editField(t, conflictPage, "edit_token") != token || !strings.Contains(conflictPage, `role="alert"`) {
		t.Fatal("conflict discarded submitted fields or upgraded stale token")
	}
	got, _ := cs.Get(ctx, it.ID)
	if got.Body != current.Body || got.Title != current.Title {
		t.Fatal("concurrent edit overwritten")
	}
	_, page = s.do("GET", path, nil, nil)
	form.Set("edit_token", editField(t, page, "edit_token"))
	form.Set("terms_token", editField(t, page, "terms_token"))
	form.Set("body", current.Body)
	form.Set("status", "draft")
	resp, _ = s.do("POST", path, form, nil)
	got, _ = cs.Get(ctx, it.ID)
	if resp.StatusCode != 303 || got.Title != "Stale title" || got.Body != current.Body || got.Status != "draft" {
		t.Fatalf("matching save %d %#v", resp.StatusCode, got)
	}
	form.Set("edit_token", "")
	resp, _ = s.do("POST", path, form, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("explicit empty token %d", resp.StatusCode)
	}
}
func TestAdminEditTokenAllowsTaxonomyEditsButRejectsStaleTerms(t *testing.T) {
	s := loggedIn(t, "admin", taxonomy.New())
	cs := contentService(t, s.app)
	ctx := context.Background()
	tax, _ := kernel.Get[taxonomy.Service](s.app.Deps, kernel.ServiceTaxonomy)
	for _, slug := range []string{"one", "two", "three"} {
		if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: slug, Name: slug}); err != nil {
			t.Fatal(err)
		}
	}
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Terms", Terms: map[string][]string{"category": {"one"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/post/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")}, "terms_present[category]": {"1"}, "terms[category]": {"two"}}
	resp, _ := s.do("POST", path, form, nil)
	got, _ := cs.Get(ctx, it.ID)
	if resp.StatusCode != 303 || strings.Join(got.Terms["category"], ",") != "two" {
		t.Fatalf("intended taxonomy update %d %+v", resp.StatusCode, got.Terms)
	}
	_, page = s.do("GET", path, nil, nil)
	response, _ := s.do("POST", path, url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")}, "title": {"Metadata only"}}, nil)
	got, _ = cs.Get(ctx, it.ID)
	if response.StatusCode != 303 || strings.Join(got.Terms["category"], ",") != "two" {
		t.Fatal("metadata save changed categories")
	}
	_, page = s.do("GET", path, nil, nil)
	form.Set("edit_token", editField(t, page, "edit_token"))
	form.Set("terms_token", editField(t, page, "terms_token"))
	form.Set("terms[category]", "one")
	if err := tax.SetContentTerms(ctx, it.ID, map[string][]string{"category": {"three"}}); err != nil {
		t.Fatal(err)
	}
	resp, _ = s.do("POST", path, form, nil)
	got, _ = cs.Get(ctx, it.ID)
	if resp.StatusCode != 409 || strings.Join(got.Terms["category"], ",") != "three" {
		t.Fatalf("stale terms %d %+v", resp.StatusCode, got.Terms)
	}
	_, page = s.do("GET", path, nil, nil)
	raced := false
	s.app.Deps.Hooks.Filter("content.before_save", 1, func(hctx context.Context, v any) (any, error) {
		if !raced {
			raced = true
			if err := tax.SetContentTerms(hctx, it.ID, map[string][]string{"category": {"two"}}); err != nil {
				return nil, err
			}
		}
		return v, nil
	})
	beforeRevisions, _ := cs.Revisions(ctx, it.ID)
	response, _ = s.do("POST", path, url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")}, "title": {"Racing title"}}, nil)
	got, _ = cs.Get(ctx, it.ID)
	afterRevisions, _ := cs.Revisions(ctx, it.ID)
	if response.StatusCode != 409 || got.Title != "Metadata only" || strings.Join(got.Terms["category"], ",") != "two" || len(beforeRevisions) != len(afterRevisions) {
		t.Fatal("taxonomy transaction race lost concurrent state")
	}
}
func TestAdminStatusTrashRequiresDeletePermission(t *testing.T) {
	s := loggedIn(t, "author")
	cs := contentService(t, s.app)
	ctx := context.Background()
	var owner string
	if err := s.app.Deps.DB.QueryRow(`SELECT id FROM users WHERE email='author@example.com'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Mine", AuthorID: owner})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/post/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	resp, _ := s.do("POST", path, url.Values{"_csrf": {s.csrf(page)}, "status": {"trash"}}, nil)
	if resp.StatusCode != 403 {
		t.Fatalf("trash through edit bypass %d", resp.StatusCode)
	}
	got, _ := cs.Get(ctx, it.ID)
	if got.Status != "draft" {
		t.Fatal("unauthorized trash applied")
	}
}

func TestAdminEditTokenChecksInsideWriteTransaction(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Before", Body: "<p>Before</p>"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/admin/content/page/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	changed := false
	s.app.Deps.Hooks.Filter("content.before_save", 1, func(hctx context.Context, v any) (any, error) {
		if !changed {
			changed = true
			_, err := s.app.Deps.DB.ExecContext(hctx, `UPDATE content SET body=? WHERE id=?`, "<p>Concurrent</p>", it.ID)
			if err != nil {
				return nil, err
			}
		}
		return v, nil
	})
	before, _ := cs.Revisions(ctx, it.ID)
	resp, _ := s.do("POST", path, url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")}, "title": {"Stale"}, "body": {"<p>Stale</p>"}}, nil)
	got, _ := cs.Get(ctx, it.ID)
	after, _ := cs.Revisions(ctx, it.ID)
	if resp.StatusCode != 409 || got.Title != "Before" || got.Body != "<p>Concurrent</p>" || len(after) != len(before) {
		t.Fatalf("transaction race escaped guard: %d %#v", resp.StatusCode, got)
	}
}

func TestAdminTrashTokenRejectsStaleAndPreservesNativeTrash(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Trash fixture", Body: "<p>Original</p>"})
	if err != nil {
		t.Fatal(err)
	}
	translated, err := cs.CreateTranslation(ctx, it.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	it, _ = cs.Get(ctx, it.ID)
	path := "/admin/content/page/" + it.ID
	_, page := s.do("GET", path, nil, nil)
	form := url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")}}
	in := content.InputFromItem(it)
	in.Body = "<p>Concurrent</p>"
	if _, err = cs.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	response, _ := s.do("POST", path+"/trash", form, nil)
	stored, _ := cs.Get(ctx, it.ID)
	if response.StatusCode != 409 || stored.Status != "draft" || stored.Body != in.Body {
		t.Fatal("stale trash accepted")
	}
	var events int
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE target_id=? AND action='content.trash'`, it.ID).Scan(&events); err != nil || events != 0 {
		t.Fatal("stale trash created audit side effect")
	}
	linked, _ := cs.Get(ctx, translated.ID)
	if stored.TranslationGroup == "" || stored.TranslationGroup != linked.TranslationGroup {
		t.Fatal("stale trash changed translation links")
	}
	_, page = s.do("GET", path, nil, nil)
	form.Set("edit_token", editField(t, page, "edit_token"))
	form.Set("terms_token", editField(t, page, "terms_token"))
	response, _ = s.do("POST", path+"/trash", form, nil)
	stored, _ = cs.Get(ctx, it.ID)
	if response.StatusCode != 303 || stored.Status != "trash" || stored.Body != in.Body {
		t.Fatal("guarded native trash failed")
	}
	if err := s.app.Deps.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE target_id=? AND action='content.trash'`, it.ID).Scan(&events); err != nil || events != 1 {
		t.Fatal("native trash audit missing")
	}
	linked, _ = cs.Get(ctx, translated.ID)
	if stored.TranslationGroup != "" || linked.TranslationGroup != "" {
		t.Fatal("native trash failed to unlink translation pair")
	}
	_, page = s.do("GET", path, nil, nil)
	form.Set("edit_token", editField(t, page, "edit_token"))
	form.Set("terms_token", editField(t, page, "terms_token"))
	form.Set("status", "draft")
	response, _ = s.do("POST", path, form, nil)
	stored, _ = cs.Get(ctx, it.ID)
	if response.StatusCode != 303 || stored.Status != "draft" || stored.Body != in.Body {
		t.Fatal("native trash restoration failed")
	}
}
