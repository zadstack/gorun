package taxonomy

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func TestTermsHierarchyAndCount(t *testing.T) {
	app := bootApp(t)
	cs, ts := services(t, app)
	ctx := context.Background()
	var saved []string
	app.Deps.Hooks.Action("taxonomy.term_saved", 0, func(_ context.Context, p any) error {
		saved = append(saved, p.(*Term).Slug)
		return nil
	})

	tech := mustTerm(t, ts, Term{Taxonomy: "category", Name: "تقنية"})
	if tech.Slug != "تقنية" {
		t.Fatalf("arabic term slug: %q", tech.Slug)
	}
	goTerm := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Go", ParentID: tech.ID})
	dup := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Go"})
	if dup.Slug != "go-2" || len(saved) != 3 {
		t.Fatalf("dup slug %q, hooks %v", dup.Slug, saved)
	}
	if _, err := ts.SaveTerm(ctx, Term{Taxonomy: "tag", Name: "x", ParentID: tech.ID}); err == nil {
		t.Fatal("flat taxonomy must reject a parent")
	}
	tech.ParentID = goTerm.ID
	if _, err := ts.SaveTerm(ctx, *tech); err == nil {
		t.Fatal("cycle must be rejected")
	}
	top, total, _ := ts.Terms(ctx, "category", TermQuery{ParentID: "root"})
	if total != 2 || len(top) != 2 {
		t.Fatalf("top-level: %d %+v", total, top)
	}
	kids, _, _ := ts.Terms(ctx, "category", TermQuery{ParentID: tech.ID})
	if len(kids) != 1 || kids[0].ID != goTerm.ID {
		t.Fatalf("children: %+v", kids)
	}

	pub, err := cs.Create(ctx, content.Input{Type: "post", Title: "A", Status: "published",
		Terms: map[string][]string{"category": {goTerm.Slug}, "tag": {"New Tag"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pub.Terms["tag"], []string{"new-tag"}) || !slices.Equal(pub.Terms["category"], []string{"go"}) {
		t.Fatalf("item terms: %v", pub.Terms)
	}
	draft, _ := cs.Create(ctx, content.Input{Type: "post", Title: "B", Terms: map[string][]string{"category": {"go"}}})
	g, _ := ts.GetTerm(ctx, goTerm.ID)
	if g.Count != 1 {
		t.Fatalf("count counts published only: %d", g.Count)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "C", Terms: map[string][]string{"category": {"missing"}}}); err == nil {
		t.Fatal("unknown hierarchical term must fail")
	} else {
		var ve *content.ValidationError
		if !errors.As(err, &ve) || ve.Fields["terms.category"] == "" {
			t.Fatalf("want terms.category validation error, got %v", err)
		}
	}
	// Hard delete cascades: count drops without a stored counter to fix.
	if err := cs.Delete(ctx, pub.ID, true); err != nil {
		t.Fatal(err)
	}
	if g, _ = ts.GetTerm(ctx, goTerm.ID); g.Count != 0 {
		t.Fatalf("count after delete: %d", g.Count)
	}
	var assigned int
	app.Deps.DB.QueryRow(`SELECT COUNT(*) FROM content_terms WHERE content_id = ?`, pub.ID).Scan(&assigned)
	if assigned != 0 {
		t.Fatalf("hard delete left %d content_terms rows", assigned)
	}
	// Deleting a parent moves children up.
	if err := ts.DeleteTerm(ctx, tech.ID); err != nil {
		t.Fatal(err)
	}
	if g, _ = ts.GetTerm(ctx, goTerm.ID); g.ParentID != "" {
		t.Fatalf("child not reparented: %+v", g)
	}
	_ = draft
}

func TestQueryTaxonomyFilter(t *testing.T) {
	app := bootApp(t)
	cs, ts := services(t, app)
	ctx := context.Background()
	news := mustTerm(t, ts, Term{Taxonomy: "category", Name: "News"})
	local := mustTerm(t, ts, Term{Taxonomy: "category", Name: "Local", ParentID: news.ID})
	mustTerm(t, ts, Term{Taxonomy: "category", Name: "Sport"})
	a, _ := cs.Create(ctx, content.Input{Type: "post", Title: "A", Terms: map[string][]string{"category": {"news"}}})
	b, _ := cs.Create(ctx, content.Input{Type: "post", Title: "B", Terms: map[string][]string{"category": {local.Slug}, "tag": {"go"}}})
	cs.Create(ctx, content.Input{Type: "post", Title: "C", Terms: map[string][]string{"category": {"sport"}, "tag": {"go"}}})

	page, err := cs.Query(ctx, content.Query{Type: "post", Taxonomy: map[string][]string{"category": {"news"}}, Order: []content.Order{{By: "title"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != a.ID || page.Items[1].ID != b.ID {
		t.Fatalf("news (with child local): %+v", page.Items)
	}
	page, _ = cs.Query(ctx, content.Query{Type: "post", Taxonomy: map[string][]string{"category": {"news"}, "tag": {"go"}}})
	if len(page.Items) != 1 || page.Items[0].ID != b.ID || !slices.Equal(page.Items[0].Terms["tag"], []string{"go"}) {
		t.Fatalf("news AND go: %+v", page.Items)
	}
	page, _ = cs.Query(ctx, content.Query{Type: "post", Taxonomy: map[string][]string{"category": {"nothing"}}})
	if page.Total != 0 {
		t.Fatalf("unknown term matches nothing: %d", page.Total)
	}
}

func TestTaxonomySaveSyncsContentTypes(t *testing.T) {
	app := bootApp(t)
	cs, ts := services(t, app)
	ctx := context.Background()
	if err := ts.Save(ctx, Taxonomy{Slug: "genre", Name: map[string]string{"en": "Genres"}, Types: []string{"page"}}); err != nil {
		t.Fatal(err)
	}
	page, _ := cs.GetType(ctx, "page")
	if !slices.Contains(page.Taxonomies, "genre") {
		t.Fatalf("page taxonomies: %v", page.Taxonomies)
	}
	g, _ := ts.Get(ctx, "genre")
	if !slices.Equal(g.Types, []string{"page"}) || g.System {
		t.Fatalf("genre: %+v", g)
	}
	if err := ts.Delete(ctx, "category"); !errors.Is(err, ErrSystem) {
		t.Fatalf("system delete: %v", err)
	}
	if err := ts.Delete(ctx, "genre"); err != nil {
		t.Fatal(err)
	}
	if page, _ = cs.GetType(ctx, "page"); slices.Contains(page.Taxonomies, "genre") {
		t.Fatal("deleted taxonomy left on the type")
	}
	if err := ts.Save(ctx, Taxonomy{Slug: "Bad Slug", Types: []string{"nope"}}); err == nil {
		t.Fatal("invalid taxonomy accepted")
	}
}

// Detaching a taxonomy from a type after items were tagged must not make
// those items unsaveable (the stale terms are dropped on the next save).
func TestDetachedTaxonomyKeepsItemsSaveable(t *testing.T) {
	s := loggedIn(t, "admin")
	cs, ts := services(t, s.app)
	ctx := context.Background()
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Tagged", Terms: map[string][]string{"tag": {"go"}, "category": {}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Save(ctx, Taxonomy{Slug: "tag", Name: map[string]string{"en": "Tags"}, Types: []string{}}); err != nil {
		t.Fatal(err)
	}
	got, _ := cs.Get(ctx, it.ID)
	if !slices.Equal(got.Terms["tag"], []string{"go"}) {
		t.Fatalf("stored terms: %v", got.Terms)
	}
	up, err := cs.Update(ctx, it.ID, content.InputFromItem(got))
	if err != nil {
		t.Fatalf("update after detach: %v", err)
	}
	if _, ok := up.Terms["tag"]; ok {
		t.Fatalf("stale tag terms kept: %v", up.Terms)
	}
	_, page := s.do("GET", "/admin/content/post/"+it.ID, nil, nil)
	resp, _ := s.do("POST", "/admin/content/post/"+it.ID, url.Values{"_csrf": {s.csrf(page)}, "title": {"Still saveable"}, "intent": {"save"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("editor save after detach: %d", resp.StatusCode)
	}
	// Newly supplying terms of a taxonomy the type does not use still fails.
	in := content.InputFromItem(up)
	in.Terms = map[string][]string{"tag": {"rust"}}
	if _, err := cs.Update(ctx, it.ID, in); err == nil {
		t.Fatal("new terms for a detached taxonomy accepted")
	}
}
