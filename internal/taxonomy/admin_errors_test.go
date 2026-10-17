package taxonomy

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAdminTaxonomyNotFoundAndWrongTaxonomy(t *testing.T) {
	s := loggedIn(t, "editor")
	_, ts := services(t, s.app)
	_, page := s.do("GET", "/admin/taxonomies", nil, nil)
	token := s.csrf(page)
	news := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News"})
	hx := map[string]string{"HX-Request": "true"}

	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/taxonomies/nope"},
		{"GET", "/admin/taxonomies/category?edit=missing"},
		{"GET", "/admin/taxonomies/tag?edit=" + news.ID},
		{"POST", "/admin/taxonomies/nope/terms"},
		{"POST", "/admin/taxonomies/nope/delete"},
		{"POST", "/admin/taxonomies/tag/terms/" + news.ID + "/delete"},
		{"POST", "/admin/taxonomies/category/terms/missing/delete"},
		{"GET", "/admin/x/taxonomy/nope/picker"},
		{"GET", "/admin/x/taxonomy/nope/terms?q=x"},
	} {
		var form url.Values
		var hdr map[string]string
		if c.method == "POST" {
			form = url.Values{"_csrf": {token}, "name": {"x"}}
		} else {
			hdr = hx
		}
		if resp, _ := s.do(c.method, c.path, form, hdr); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c.method, c.path, resp.StatusCode)
		}
	}
	// Editing a term by an ID from another taxonomy is refused.
	if resp, _ := s.do("POST", "/admin/taxonomies/tag/terms", url.Values{"_csrf": {token}, "id": {news.ID}, "name": {"Hijack"}}, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-taxonomy edit: %d", resp.StatusCode)
	}
	if got, _ := ts.GetTerm(context.Background(), news.ID); got.Name != "News" || got.Taxonomy != "category" {
		t.Fatalf("term changed by a refused edit: %+v", got)
	}
	// An empty suggestion query renders without hitting the store.
	if resp, _ := s.do("GET", "/admin/x/taxonomy/nope/terms?q=", nil, hx); resp.StatusCode != http.StatusOK {
		t.Fatalf("empty suggest: %d", resp.StatusCode)
	}
}

func TestAdminTaxonomySettingsAndTermEditing(t *testing.T) {
	s := loggedIn(t, "editor")
	_, ts := services(t, s.app)
	ctx := context.Background()
	_, page := s.do("GET", "/admin/taxonomies", nil, nil)
	token := s.csrf(page)

	// Creating a taxonomy whose slug exists is a form error, not an overwrite.
	resp, page := s.do("POST", "/admin/taxonomies", url.Values{"_csrf": {token}, "slug": {"category"}, "name_en": {"Hijacked"}}, nil)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "field-error") {
		t.Fatalf("duplicate slug: %d", resp.StatusCode)
	}
	if c, _ := ts.Get(ctx, "category"); c.Name["en"] == "Hijacked" {
		t.Fatal("duplicate create overwrote the taxonomy")
	}
	// The settings form (existing=1) updates in place.
	resp, _ = s.do("POST", "/admin/taxonomies", url.Values{"_csrf": {token}, "existing": {"1"}, "slug": {"category"}, "name_en": {"Sections"},
		"hierarchical": {"on"}, "public": {"true"}, "types": {"post", " "}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/taxonomies/category" {
		t.Fatalf("settings update: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if c, _ := ts.Get(ctx, "category"); c.Name["en"] != "Sections" || !c.Hierarchical || !c.Public {
		t.Fatalf("settings not saved: %+v", c)
	}
	// An invalid settings form redirects back with an error flash.
	resp, _ = s.do("POST", "/admin/taxonomies", url.Values{"_csrf": {token}, "existing": {"1"}, "slug": {"category"}, "types": {"nope"}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/taxonomies/category" {
		t.Fatalf("invalid settings: %d", resp.StatusCode)
	}
	if c, _ := ts.Get(ctx, "category"); c.Name["en"] != "Sections" {
		t.Fatalf("invalid settings saved: %+v", c)
	}

	parent := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Parent", Meta: map[string]any{"color": "red"}})
	child := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Child", ParentID: parent.ID})
	// The edit screen excludes the edited term from its own parent choices.
	resp, page = s.do("GET", "/admin/taxonomies/category?edit="+parent.ID, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, `value="`+child.ID+`"`) || strings.Contains(page, `<option value="`+parent.ID+`"`) {
		t.Fatalf("edit screen: %d", resp.StatusCode)
	}
	// Search shows a flat list and keeps the full parent list.
	resp, page = s.do("GET", "/admin/taxonomies/category?q=Chi", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(page, "Child") || !strings.Contains(page, `value="`+parent.ID+`"`) {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	// Updating a term keeps its meta.
	resp, _ = s.do("POST", "/admin/taxonomies/category/terms", url.Values{"_csrf": {token}, "id": {parent.ID}, "name": {"Renamed"}, "slug": {"parent"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update term: %d", resp.StatusCode)
	}
	if got, _ := ts.GetTerm(ctx, parent.ID); got.Name != "Renamed" || got.Meta["color"] != "red" {
		t.Fatalf("updated term: %+v", got)
	}

	// Deleting a custom taxonomy.
	if err := ts.Save(ctx, Taxonomy{Slug: "genre", Name: map[string]string{"en": "Genres"}}); err != nil {
		t.Fatal(err)
	}
	resp, _ = s.do("POST", "/admin/taxonomies/genre/delete", url.Values{"_csrf": {token}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/taxonomies" {
		t.Fatalf("delete taxonomy: %d", resp.StatusCode)
	}
	if _, err := ts.Get(ctx, "genre"); err != ErrNotFound {
		t.Fatalf("genre survived: %v", err)
	}
}

func TestAdminTaxonomyStoreFailuresAre500(t *testing.T) {
	s := loggedIn(t, "editor")
	_, ts := services(t, s.app)
	_, page := s.do("GET", "/admin/taxonomies", nil, nil)
	token := s.csrf(page)
	hx := map[string]string{"HX-Request": "true"}

	raiseOn(t, ts, "no_tax_insert", "INSERT ON taxonomies")
	if resp, _ := s.do("POST", "/admin/taxonomies", url.Values{"_csrf": {token}, "slug": {"genre"}, "name_en": {"G"}}, nil); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("taxonomy save failure: %d", resp.StatusCode)
	}
	raiseOn(t, ts, "no_term_insert", "INSERT ON terms")
	if resp, _ := s.do("POST", "/admin/taxonomies/tag/terms", url.Values{"_csrf": {token}, "name": {"x"}}, nil); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("term save failure: %d", resp.StatusCode)
	}

	// Term reads fail without content_terms (live counts).
	exec(t, ts, `ALTER TABLE content_terms RENAME TO content_terms_gone`)
	for _, path := range []string{"/admin/taxonomies/category", "/admin/x/taxonomy/category/picker", "/admin/x/taxonomy/tag/terms?q=go"} {
		if resp, _ := s.do("GET", path, nil, hx); resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s: %d, want 500", path, resp.StatusCode)
		}
	}
	exec(t, ts, `ALTER TABLE content_terms_gone RENAME TO content_terms`)

	exec(t, ts, `ALTER TABLE taxonomies RENAME TO taxonomies_gone`)
	if resp, _ := s.do("GET", "/admin/taxonomies", nil, nil); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("list failure: %d", resp.StatusCode)
	}
	if resp, _ := s.do("POST", "/admin/taxonomies/genre/delete", url.Values{"_csrf": {token}}, nil); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("delete failure: %d", resp.StatusCode)
	}
}
