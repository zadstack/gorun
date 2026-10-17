package taxonomy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

// failingContent is a content.Service whose type calls fail on demand.
type failingContent struct {
	content.Service
	types      []content.Type
	typesErr   error
	saveErr    error
	savedTypes []content.Type
}

func (f *failingContent) Types(context.Context) ([]content.Type, error) {
	return f.types, f.typesErr
}

func (f *failingContent) SaveType(_ context.Context, t content.Type) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.savedTypes = append(f.savedTypes, t)
	return nil
}

func exec(t *testing.T, s *service, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func raiseOn(t *testing.T, s *service, name, event string) {
	t.Helper()
	exec(t, s, `CREATE TRIGGER `+name+` BEFORE `+event+` BEGIN SELECT RAISE(ABORT, 'boom'); END`)
}

func TestServiceWithoutContentService(t *testing.T) {
	app := bootApp(t)
	_, base := services(t, app)
	s := newService(base.db, nil, base.audit, nil)
	ctx := context.Background()
	if s.content() != nil {
		t.Fatal("nil contentFn must yield no content service")
	}
	// Without content types, Types fall back to the stored types_json and
	// any type name is accepted.
	if err := s.Save(ctx, Taxonomy{Slug: "genre", Name: map[string]string{"en": "Genres"}, Types: []string{"anything"}}); err != nil {
		t.Fatal(err)
	}
	g, err := s.Get(ctx, "genre")
	if err != nil || len(g.Types) != 1 || g.Types[0] != "anything" {
		t.Fatalf("stored types: %+v %v", g, err)
	}
	if err := s.Save(ctx, Taxonomy{Slug: "mood", Name: map[string]string{"ar": "مزاج"}}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Get(ctx, "mood"); m.Types == nil || len(m.Types) != 0 || m.Singular == nil {
		t.Fatalf("nil types/singular not normalised: %+v", m)
	}
	if err := s.Delete(ctx, "genre"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "genre"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
	if err := s.Delete(ctx, "genre"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestServiceContentTypeFailures(t *testing.T) {
	app := bootApp(t)
	_, base := services(t, app)
	ctx := context.Background()
	fc := &failingContent{types: []content.Type{{Slug: "post"}}}
	s := newService(base.db, nil, base.audit, func() content.Service { return fc })

	// SaveType failing surfaces as a sync error (the row is already saved).
	fc.saveErr = errors.New("type store down")
	err := s.Save(ctx, Taxonomy{Slug: "genre", Name: map[string]string{"en": "G"}, Types: []string{"post"}})
	if err == nil || !strings.Contains(err.Error(), "sync type post") || !errors.Is(err, fc.saveErr) {
		t.Fatalf("sync failure: %v", err)
	}
	fc.saveErr = nil
	if err := s.Save(ctx, Taxonomy{Slug: "genre", Name: map[string]string{"en": "G"}, Types: []string{"post"}}); err != nil || len(fc.savedTypes) != 1 || fc.savedTypes[0].Taxonomies[0] != "genre" {
		t.Fatalf("sync: %v %+v", err, fc.savedTypes)
	}

	fc.typesErr = errors.New("types down")
	if err := s.Save(ctx, Taxonomy{Slug: "other", Name: map[string]string{"en": "O"}}); !errors.Is(err, fc.typesErr) {
		t.Fatalf("save with failing types: %v", err)
	}
	if _, err := s.Get(ctx, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("taxonomy saved despite failing types: %v", err)
	}
	// attachTypes keeps the stored types when content types are unavailable.
	if g, err := s.Get(ctx, "genre"); err != nil || len(g.Types) != 1 || g.Types[0] != "post" {
		t.Fatalf("get with failing types: %+v %v", g, err)
	}
	if err := s.Delete(ctx, "genre"); !errors.Is(err, fc.typesErr) {
		t.Fatalf("delete with failing types: %v", err)
	}
}

func TestScanNormalisesNullJSON(t *testing.T) {
	app := bootApp(t)
	_, s := services(t, app)
	ctx := context.Background()
	exec(t, s, `INSERT INTO taxonomies (slug, name_json, singular_json, types_json) VALUES ('raw', 'null', 'null', 'null')`)
	exec(t, s, `INSERT INTO terms (id, taxonomy, slug, name, meta_json) VALUES ('t1', 'raw', 'x', 'X', 'null')`)
	tax, err := s.Get(ctx, "raw")
	if err != nil || tax.Name == nil || tax.Singular == nil || tax.Types == nil {
		t.Fatalf("null json maps: %+v %v", tax, err)
	}
	term, err := s.GetTerm(ctx, "t1")
	if err != nil || term.Meta == nil {
		t.Fatalf("null meta: %+v %v", term, err)
	}
}

func TestStoreReadErrors(t *testing.T) {
	app := bootApp(t)
	_, s := services(t, app)
	ctx := context.Background()
	// A non-boolean hierarchical value makes the row unscannable.
	exec(t, s, `INSERT INTO taxonomies (slug, name_json, hierarchical) VALUES ('broken', '{"en":"B"}', 'not-a-bool')`)
	if _, err := s.Taxonomies(ctx); err == nil {
		t.Fatal("list must fail on an unscannable row")
	}
	if _, err := s.Get(ctx, "broken"); err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "get broken") {
		t.Fatalf("get unscannable: %v", err)
	}
	if _, err := s.SaveTerm(ctx, Term{Taxonomy: "broken", Name: "x"}); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("save term under an unreadable taxonomy: %v", err)
	}
	exec(t, s, `DELETE FROM taxonomies WHERE slug = 'broken'`)

	term := mustTerm(t, s, Term{Taxonomy: "category", Name: "News"})
	// The live count joins content_terms: without it every term read fails
	// with a wrapped (non-NotFound) error.
	exec(t, s, `ALTER TABLE content_terms RENAME TO content_terms_gone`)
	if _, _, err := s.Terms(ctx, "category", TermQuery{}); err == nil || !strings.Contains(err.Error(), "list terms") {
		t.Fatalf("list terms: %v", err)
	}
	if _, err := s.GetTerm(ctx, term.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("get term: %v", err)
	}
	if _, err := s.GetTermBySlug(ctx, "category", "news"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("get term by slug: %v", err)
	}
	if _, err := s.ContentTerms(ctx, "c1"); err == nil || !strings.Contains(err.Error(), "content terms") {
		t.Fatalf("content terms: %v", err)
	}
	if _, err := s.ContentIDs(ctx, "category", []string{"news"}); err == nil || !strings.Contains(err.Error(), "content ids") {
		t.Fatalf("content ids: %v", err)
	}
	exec(t, s, `ALTER TABLE content_terms_gone RENAME TO content_terms`)

	exec(t, s, `ALTER TABLE terms RENAME TO terms_gone`)
	if _, _, err := s.Terms(ctx, "category", TermQuery{}); err == nil || !strings.Contains(err.Error(), "count terms") {
		t.Fatalf("count terms: %v", err)
	}
	exec(t, s, `ALTER TABLE terms_gone RENAME TO terms`)

	exec(t, s, `ALTER TABLE taxonomies RENAME TO taxonomies_gone`)
	if _, err := s.Taxonomies(ctx); err == nil || !strings.Contains(err.Error(), "taxonomy: list") {
		t.Fatalf("list taxonomies: %v", err)
	}
}

func TestTermEdgeCases(t *testing.T) {
	app := bootApp(t)
	_, s := services(t, app)
	ctx := context.Background()
	var ve *content.ValidationError

	if _, _, err := s.Terms(ctx, "nope", TermQuery{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("terms of a missing taxonomy: %v", err)
	}
	if _, err := s.GetTerm(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing term: %v", err)
	}
	if _, err := s.SaveTerm(ctx, Term{Taxonomy: "nope", Name: "x"}); !errors.As(err, &ve) || ve.Fields["taxonomy"] != "taxonomy.validation.unknown" {
		t.Fatalf("unknown taxonomy: %v", err)
	}
	if _, err := s.SaveTerm(ctx, Term{ID: "missing", Taxonomy: "tag", Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update a missing term: %v", err)
	}
	cat := mustTerm(t, s, Term{Taxonomy: "category", Name: "News"})
	if _, err := s.SaveTerm(ctx, Term{ID: cat.ID, Taxonomy: "tag", Name: "News"}); !errors.As(err, &ve) || ve.Fields["taxonomy"] != "taxonomy.validation.immutable" {
		t.Fatalf("moving a term between taxonomies: %v", err)
	}
	// A name with no letters or digits falls back to the (lowercased) ID.
	sym := mustTerm(t, s, Term{Taxonomy: "tag", Name: "!!!"})
	if sym.Slug == "" || sym.Slug != strings.ToLower(sym.ID) {
		t.Fatalf("symbol-only name slug: %q (id %s)", sym.Slug, sym.ID)
	}
	if err := s.DeleteTerm(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing term: %v", err)
	}
	if ids, err := s.ContentIDs(ctx, "category", nil); err != nil || ids == nil || len(ids) != 0 {
		t.Fatalf("no slugs: %v %v", ids, err)
	}

	// A failing term_saved hook still returns the saved term.
	hookErr := errors.New("listener failed")
	app.Deps.Hooks.Action("taxonomy.term_saved", 0, func(context.Context, any) error { return hookErr })
	saved, err := s.SaveTerm(ctx, Term{Taxonomy: "tag", Name: "hooked"})
	if !errors.Is(err, hookErr) || saved == nil || saved.Slug != "hooked" {
		t.Fatalf("hook failure: %+v %v", saved, err)
	}
	if _, err := s.GetTermBySlug(ctx, "tag", "hooked"); err != nil {
		t.Fatalf("term not stored despite hook error: %v", err)
	}
}

func TestTermWriteFailuresRollBack(t *testing.T) {
	app := bootApp(t)
	cs, s := services(t, app)
	ctx := context.Background()
	parent := mustTerm(t, s, Term{Taxonomy: "category", Name: "Parent"})
	child := mustTerm(t, s, Term{Taxonomy: "category", Name: "Child", ParentID: parent.ID})
	item, err := cs.Create(ctx, content.Input{Type: "post", Title: "A", Terms: map[string][]string{"tag": {"go"}}})
	if err != nil {
		t.Fatal(err)
	}

	raiseOn(t, s, "no_term_insert", "INSERT ON terms")
	if _, err := s.SaveTerm(ctx, Term{Taxonomy: "tag", Name: "fresh"}); err == nil || !strings.Contains(err.Error(), "save term") {
		t.Fatalf("insert failure: %v", err)
	}
	// Auto-creating a typed tag fails the same way.
	if err := s.SetContentTerms(ctx, item.ID, map[string][]string{"tag": {"brand new"}}); err == nil || !strings.Contains(err.Error(), "create term") {
		t.Fatalf("auto-create failure: %v", err)
	}
	exec(t, s, `DROP TRIGGER no_term_insert`)

	raiseOn(t, s, "no_reparent", "UPDATE ON terms")
	if err := s.DeleteTerm(ctx, parent.ID); err == nil || !strings.Contains(err.Error(), "reparent") {
		t.Fatalf("reparent failure: %v", err)
	}
	exec(t, s, `DROP TRIGGER no_reparent`)
	raiseOn(t, s, "no_term_delete", "DELETE ON terms")
	if err := s.DeleteTerm(ctx, child.ID); err == nil || !strings.Contains(err.Error(), "delete term") {
		t.Fatalf("delete failure: %v", err)
	}
	exec(t, s, `DROP TRIGGER no_term_delete`)
	if _, err := s.GetTerm(ctx, parent.ID); err != nil {
		t.Fatalf("failed delete removed the parent: %v", err)
	}

	raiseOn(t, s, "no_assign", "INSERT ON content_terms")
	if err := s.SetContentTerms(ctx, item.ID, map[string][]string{"tag": {"go"}}); err == nil || !strings.Contains(err.Error(), "assign term") {
		t.Fatalf("assign failure: %v", err)
	}
	exec(t, s, `DROP TRIGGER no_assign`)
	raiseOn(t, s, "no_clear", "DELETE ON content_terms")
	if err := s.SetContentTerms(ctx, item.ID, nil); err == nil || !strings.Contains(err.Error(), "clear content terms") {
		t.Fatalf("clear failure: %v", err)
	}
	exec(t, s, `DROP TRIGGER no_clear`)
	if got, _ := s.ContentTerms(ctx, item.ID); len(got["tag"]) != 1 || got["tag"][0] != "go" {
		t.Fatalf("failed writes changed assignments: %v", got)
	}

	var ve *content.ValidationError
	if err := s.SetContentTerms(ctx, item.ID, map[string][]string{"nope": {"x"}}); !errors.As(err, &ve) || ve.Fields["terms.nope"] != "taxonomy.validation.unknown" {
		t.Fatalf("unknown taxonomy assignment: %v", err)
	}
	// Blank values are ignored rather than creating empty terms.
	if err := s.SetContentTerms(ctx, item.ID, map[string][]string{"tag": {"  ", "go"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ContentTerms(ctx, item.ID); len(got["tag"]) != 1 {
		t.Fatalf("blank value assigned: %v", got)
	}
}

func TestTaxonomyWriteFailures(t *testing.T) {
	app := bootApp(t)
	_, s := services(t, app)
	ctx := context.Background()
	if err := s.Save(ctx, Taxonomy{Slug: "genre", Name: map[string]string{"en": "G"}}); err != nil {
		t.Fatal(err)
	}
	raiseOn(t, s, "no_tax_insert", "INSERT ON taxonomies")
	if err := s.Save(ctx, Taxonomy{Slug: "mood", Name: map[string]string{"en": "M"}}); err == nil || !strings.Contains(err.Error(), "save mood") {
		t.Fatalf("save failure: %v", err)
	}
	raiseOn(t, s, "no_tax_delete", "DELETE ON taxonomies")
	if err := s.Delete(ctx, "genre"); err == nil || !strings.Contains(err.Error(), "delete genre") {
		t.Fatalf("delete failure: %v", err)
	}
	if _, err := s.Get(ctx, "genre"); err != nil {
		t.Fatalf("failed delete removed the taxonomy: %v", err)
	}
}
