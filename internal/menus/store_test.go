package menus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeContent implements only what menus calls.
type fakeContent struct {
	content.Service
	items map[string]*content.Item
	types map[string]*content.Type
}

func (f fakeContent) Get(_ context.Context, id string) (*content.Item, error) {
	if it, ok := f.items[id]; ok {
		return it, nil
	}
	return nil, content.ErrNotFound
}

func (f fakeContent) GetType(_ context.Context, slug string) (*content.Type, error) {
	if t, ok := f.types[slug]; ok {
		return t, nil
	}
	return nil, content.ErrNotFound
}

type fakeTaxonomy struct {
	taxonomy.Service
	terms map[string]*taxonomy.Term
}

func (f fakeTaxonomy) GetTerm(_ context.Context, id string) (*taxonomy.Term, error) {
	if t, ok := f.terms[id]; ok {
		return t, nil
	}
	return nil, errors.New("no term")
}

func testService(t *testing.T) (*service, *hooks.Registry) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "audit", audit.Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), d, "menus", migrations); err != nil {
		t.Fatal(err)
	}
	h := hooks.New()
	fc := fakeContent{
		items: map[string]*content.Item{
			"c1": {ID: "c1", Slug: "about", Title: "About us", Status: "published"},
			"c2": {ID: "c2", Slug: "draft", Title: "Draft", Status: "draft"},
		},
		types: map[string]*content.Type{"post": {Slug: "post", ArchiveSlug: "blog", Plural: map[string]string{"en": "Posts", "ar": "المقالات"}}},
	}
	ft := fakeTaxonomy{terms: map[string]*taxonomy.Term{"t1": {ID: "t1", Taxonomy: "category", Slug: "news", Name: "News"}}}
	s := &service{db: d, hooks: h, audit: audit.New(d, discardLog()), log: discardLog(),
		content:  func() (content.Service, bool) { return fc, true },
		taxonomy: func() (taxonomy.Service, bool) { return ft, true }}
	return s, h
}

func countRows(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMenuSaveItemsTreeRoundtrip(t *testing.T) {
	s, h := testService(t)
	ctx := context.Background()
	var saved atomic.Int32
	h.Action(HookSaved, 0, func(_ context.Context, p any) error {
		var ev SavedEvent
		if err := json.Unmarshal(p.(json.RawMessage), &ev); err != nil || ev.Slug != "main" {
			t.Errorf("saved payload %s: %v", p, err)
		}
		saved.Add(1)
		return nil
	})
	m, err := s.Save(ctx, Menu{Name: "Main Menu", Slug: "main"})
	if err != nil {
		t.Fatal(err)
	}
	tree := []Item{
		{Label: "Home", Kind: KindCustom, URL: "/"},
		{Label: "", Kind: KindContent, RefID: "c1", URL: "/ignored", Children: []Item{
			{Label: "Team", URL: "/about/team", CSSClass: "  a   b ", OpenInNew: true, Children: []Item{
				{Label: "Deep", URL: "https://example.com/x"},
			}},
			{Label: "News", Kind: KindTerm, RefID: "t1"},
		}},
		{Label: "Blog", Kind: KindArchive, RefID: "post"},
		{Label: "Hidden draft", Kind: KindContent, RefID: "c2"},
	}
	if err := s.SaveItems(ctx, m.ID, tree); err != nil {
		t.Fatal(err)
	}
	raw, err := s.getRaw(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw.Items) != 4 || len(raw.Items[1].Children) != 2 || len(raw.Items[1].Children[0].Children) != 1 {
		t.Fatalf("raw tree shape: %+v", raw.Items)
	}
	team := raw.Items[1].Children[0]
	if team.ParentID != raw.Items[1].ID || team.Position != 0 || team.CSSClass != "a b" || team.Target != "_blank" ||
		raw.Items[1].Children[1].Position != 1 || raw.Items[3].Position != 3 || team.Children[0].ParentID != team.ID {
		t.Fatalf("raw items: %+v", raw.Items)
	}
	// Saving the raw tree back keeps IDs and structure (idempotent).
	if err := s.SaveItems(ctx, m.ID, raw.Items); err != nil {
		t.Fatal(err)
	}
	again, _ := s.getRaw(ctx, m.ID)
	if again.Items[1].Children[0].ID != team.ID || again.Items[1].Children[0].Children[0].Label != "Deep" {
		t.Fatalf("resave changed tree: %+v", again.Items)
	}
	// Display view: resolved URLs/labels, unpublished content dropped.
	got, err := s.Get(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("resolved top level: %+v", got.Items)
	}
	about := got.Items[1]
	if about.Label != "About us" || about.URL != "/about" || about.Children[1].URL != "/category/news" ||
		about.Children[1].Label != "News" || got.Items[2].URL != "/blog/" {
		t.Fatalf("resolved: %+v", got.Items)
	}
	// The filter can override resolution (site module).
	h.Filter(FilterResolveURL, 10, func(_ context.Context, v any) (any, error) {
		it := v.(*Item)
		if it.Kind == KindContent || it.Kind == KindCustom {
			it.URL = "/pages" + it.URL
		}
		return it, nil
	})
	got, _ = s.Get(ctx, "main")
	if got.Items[1].URL != "/pages/about" || got.Items[0].URL != "/pages/" {
		t.Fatalf("filter (content and custom): %q %q", got.Items[1].URL, got.Items[0].URL)
	}
	// Invalid items are rejected without touching the stored tree.
	err = s.SaveItems(ctx, m.ID, []Item{{Label: "x", URL: "javascript:alert(1)"}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["items.0.url"] == "" {
		t.Fatalf("javascript url: %v", err)
	}
	if err := s.SaveItems(ctx, m.ID, []Item{{Kind: KindContent}}); !errors.As(err, &ve) {
		t.Fatalf("content without ref: %v", err)
	}
	if err := s.SaveItems(ctx, m.ID, []Item{{Kind: KindContent, RefID: "c1", URL: "javascript:x"}}); !errors.As(err, &ve) {
		t.Fatalf("unsafe url on a content item: %v", err)
	}
	if n := countRows(t, s.db, `SELECT COUNT(*) FROM menu_items WHERE menu_id = ?`, m.ID); n != 7 {
		t.Fatalf("stored items: %d", n)
	}
	if err := s.SaveItems(ctx, "nope", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown menu: %v", err)
	}
	if saved.Load() != 3 || countRows(t, s.db, `SELECT COUNT(*) FROM audit_log WHERE action = 'menus.save_items'`) != 2 {
		t.Fatalf("hooks=%d", saved.Load())
	}
	// Delete cascades.
	if err := s.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s.db, `SELECT COUNT(*) FROM menu_items`); n != 0 {
		t.Fatalf("items left: %d", n)
	}
}

func TestMenuByLocation(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	a, err := s.Save(ctx, Menu{Name: "Header", Location: "primary", Items: []Item{{Label: "Home", URL: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Slug != "header" || len(a.Items) != 1 {
		t.Fatalf("save with items: %+v", a)
	}
	got, err := s.ByLocation(ctx, "primary")
	if err != nil || got.ID != a.ID || got.Items[0].URL != "/" {
		t.Fatalf("by location: %+v %v", got, err)
	}
	if _, err := s.ByLocation(ctx, "footer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty location: %v", err)
	}
	if _, err := s.ByLocation(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("blank location must not match unassigned menus")
	}
	// Assigning the location to another menu moves it.
	b, err := s.Save(ctx, Menu{Name: "Other", Slug: "other", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ByLocation(ctx, "primary"); got.ID != b.ID {
		t.Fatalf("location not moved: %+v", got)
	}
	if old, _ := s.getRaw(ctx, a.ID); old.Location != "" {
		t.Fatalf("old menu kept location %q", old.Location)
	}
	// Validation: duplicate slug, bad location, empty name.
	var ve *ValidationError
	if _, err := s.Save(ctx, Menu{Name: "Dup", Slug: "other"}); !errors.As(err, &ve) || ve.Fields["slug"] != "menus.error.slug_taken" {
		t.Fatalf("dup slug: %v", err)
	}
	if _, err := s.Save(ctx, Menu{Name: "X", Location: "Bad Loc"}); !errors.As(err, &ve) || ve.Fields["location"] == "" {
		t.Fatalf("bad location: %v", err)
	}
	if _, err := s.Save(ctx, Menu{Name: " "}); !errors.As(err, &ve) || ve.Fields["name"] == "" {
		t.Fatalf("empty name: %v", err)
	}
	if m, err := s.Save(ctx, Menu{Name: "القائمة"}); err != nil || !slugPattern.MatchString(m.Slug) {
		t.Fatalf("arabic name slug: %+v %v", m, err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 3 || list[0].Name != "Header" || len(list[0].Items) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := s.Save(ctx, Menu{ID: "missing", Name: "Ghost"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

// A location holds one menu per locale: ByLocationLocale prefers the menu of the locale and falls
// back to the menu for every locale ("").
func TestMenuByLocationLocale(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	all, err := s.Save(ctx, Menu{Name: "Main", Location: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	en, err := s.Save(ctx, Menu{Name: "Main EN", Slug: "main-en", Location: "primary", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.ByLocationLocale(ctx, "primary", "en"); err != nil || got.ID != en.ID {
		t.Fatalf("en: %+v %v", got, err)
	}
	if got, err := s.ByLocationLocale(ctx, "primary", "ar"); err != nil || got.ID != all.ID {
		t.Fatalf("ar falls back: %+v %v", got, err)
	}
	if got, err := s.ByLocation(ctx, "primary"); err != nil || got.ID != all.ID {
		t.Fatalf("ByLocation keeps the all-locales menu: %+v %v", got, err)
	}
	// both keep the location: one per (location, locale)
	if m, _ := s.Get(ctx, "main"); m.Location != "primary" {
		t.Fatalf("the en menu took the location from the all-locales one: %+v", m)
	}
	if _, err := s.Save(ctx, Menu{Name: "Bad", Location: "primary", Locale: "fr"}); err == nil {
		t.Fatal("unsupported locale accepted")
	}
}
