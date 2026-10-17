package menus

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func mustExec(t *testing.T, s *service, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func failOn(t *testing.T, s *service, name, event string) {
	t.Helper()
	mustExec(t, s, `CREATE TRIGGER `+name+` BEFORE `+event+` BEGIN SELECT RAISE(ABORT, 'boom'); END`)
}

func isValidation(err error) bool {
	var ve *ValidationError
	return errors.As(err, &ve)
}

func TestMenuWriteFailuresRollBack(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	m, err := s.Save(ctx, Menu{Name: "Main", Location: "primary", Items: []Item{{Label: "Home", URL: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	expectFail := func(what string, err error) {
		t.Helper()
		if err == nil || isValidation(err) || errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: want a store error, got %v", what, err)
		}
	}

	failOn(t, s, "no_menu_insert", "INSERT ON menus")
	_, err = s.Save(ctx, Menu{Name: "Other"})
	expectFail("create", err)
	if !strings.HasPrefix(err.Error(), "menus: save:") {
		t.Fatalf("create error not wrapped: %v", err)
	}
	mustExec(t, s, `DROP TRIGGER no_menu_insert`)

	// Moving a location clears it on the previous holder; failing that aborts.
	failOn(t, s, "no_location_clear", "UPDATE OF location ON menus WHEN OLD.location <> '' AND NEW.location = ''")
	_, err = s.Save(ctx, Menu{Name: "Usurper", Location: "primary"})
	expectFail("location move", err)
	mustExec(t, s, `DROP TRIGGER no_location_clear`)
	if list, _ := s.List(ctx); len(list) != 1 {
		t.Fatalf("rolled-back create left a menu: %+v", list)
	}

	failOn(t, s, "no_menu_update", "UPDATE ON menus")
	_, err = s.Save(ctx, Menu{ID: m.ID, Name: "Renamed"})
	expectFail("update", err)
	// SaveItems bumps updated_at after writing the tree.
	expectFail("save items touch", s.SaveItems(ctx, m.ID, []Item{{Label: "New", URL: "/new"}}))
	mustExec(t, s, `DROP TRIGGER no_menu_update`)

	failOn(t, s, "no_item_insert", "INSERT ON menu_items")
	_, err = s.Save(ctx, Menu{ID: m.ID, Name: "Main", Items: []Item{{Label: "A", URL: "/a"}}})
	expectFail("save with items", err)
	expectFail("save items insert", s.SaveItems(ctx, m.ID, []Item{{Label: "A", URL: "/a"}}))
	mustExec(t, s, `DROP TRIGGER no_item_insert`)

	failOn(t, s, "no_item_delete", "DELETE ON menu_items")
	expectFail("save items clear", s.SaveItems(ctx, m.ID, nil))
	mustExec(t, s, `DROP TRIGGER no_item_delete`)

	failOn(t, s, "no_menu_delete", "DELETE ON menus")
	if err := s.Delete(ctx, m.ID); err == nil || !strings.HasPrefix(err.Error(), "menus: delete:") {
		t.Fatalf("delete: %v", err)
	}
	mustExec(t, s, `DROP TRIGGER no_menu_delete`)

	got, err := s.getRaw(ctx, m.ID)
	if err != nil || got.Name != "Main" || got.Location != "primary" || len(got.Items) != 1 || got.Items[0].Label != "Home" {
		t.Fatalf("failed writes changed the menu: %+v %v", got, err)
	}
}

func TestMenuReadFailures(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	m, err := s.Save(ctx, Menu{Name: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `ALTER TABLE menu_items RENAME TO menu_items_gone`)
	if _, err := s.List(ctx); err == nil || !strings.Contains(err.Error(), "menus: items") {
		t.Fatalf("list without items table: %v", err)
	}
	if _, err := s.getRaw(ctx, m.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("get without items table: %v", err)
	}
	if _, err := s.Save(ctx, Menu{ID: m.ID, Name: "Main", Items: []Item{{Label: "A", URL: "/a"}}}); err == nil || !strings.Contains(err.Error(), "item ids") {
		t.Fatalf("save items without items table: %v", err)
	}
	if err := s.Delete(ctx, m.ID); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("delete without items table: %v", err)
	}
	mustExec(t, s, `ALTER TABLE menu_items_gone RENAME TO menu_items`)

	mustExec(t, s, `ALTER TABLE menus RENAME TO menus_gone`)
	if _, err := s.List(ctx); err == nil || !strings.Contains(err.Error(), "menus: list") {
		t.Fatalf("list without menus table: %v", err)
	}
	if _, err := s.Get(ctx, "main"); err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "menus: get") {
		t.Fatalf("get without menus table: %v", err)
	}
	if _, err := s.Save(ctx, Menu{Name: "New"}); err == nil || isValidation(err) {
		t.Fatalf("save without menus table: %v", err)
	}
}

func TestMenuValidationEdges(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	field := func(err error, key string) string {
		t.Helper()
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("want validation error for %s, got %v", key, err)
		}
		return ve.Fields[key]
	}

	if _, err := s.Save(ctx, Menu{Name: "X", Slug: "Not Valid!"}); field(err, "slug") != "menus.error.slug_invalid" {
		t.Fatalf("bad slug: %v", err)
	}
	// Invalid items are reported alongside metadata fields.
	_, err := s.Save(ctx, Menu{Name: "X", Items: []Item{{Kind: KindCustom, URL: "/"}}})
	if field(err, "items.0.label") != "menus.error.label_required" {
		t.Fatalf("custom without label: %v", err)
	}
	if _, err := s.ByLocationLocale(ctx, "", "en"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blank location: %v", err)
	}

	m, err := s.Save(ctx, Menu{Name: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		items []Item
		key   string
		want  string
	}{
		{"label too long", []Item{{Label: strings.Repeat("ل", maxLabel+1), URL: "/"}}, "items.0.label", "menus.error.label_long"},
		{"protocol-relative url", []Item{{Label: "x", URL: "//evil.example"}}, "items.0.url", "menus.error.url_invalid"},
		{"archive without ref", []Item{{Label: "x", Kind: KindArchive}}, "items.0.ref_id", "menus.error.ref_required"},
		{"unknown kind", []Item{{Label: "x", Kind: "widget", URL: "/"}}, "items.0.kind", "menus.error.kind_invalid"},
	}
	for _, c := range cases {
		if got := field(s.SaveItems(ctx, m.ID, c.items), c.key); got != c.want {
			t.Errorf("%s: %s = %q, want %q", c.name, c.key, got, c.want)
		}
	}
	deep := Item{Label: "L6", URL: "/6"}
	for i := maxDepth - 1; i >= 0; i-- {
		deep = Item{Label: fmt.Sprintf("L%d", i), URL: "/", Children: []Item{deep}}
	}
	if got := field(s.SaveItems(ctx, m.ID, []Item{deep}), fmt.Sprintf("items.%d.depth", maxDepth)); got != "menus.error.too_deep" {
		t.Fatalf("too deep: %q", got)
	}
	many := make([]Item, maxItems+1)
	for i := range many {
		many[i] = Item{Label: "x", URL: "/"}
	}
	if got := field(s.SaveItems(ctx, m.ID, many), "items"); got != "menus.error.too_many" {
		t.Fatalf("too many: %q", got)
	}
	if n := countRows(t, s.db, `SELECT COUNT(*) FROM menu_items WHERE menu_id = ?`, m.ID); n != 0 {
		t.Fatalf("invalid trees stored %d items", n)
	}

	// target=_blank alone implies open_in_new; other targets are dropped.
	if err := s.SaveItems(ctx, m.ID, []Item{{Label: "a", URL: "/a", Target: "_blank"}, {Label: "b", URL: "/b", Target: "_top"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := s.getRaw(ctx, m.ID)
	if !raw.Items[0].OpenInNew || raw.Items[0].Target != "_blank" || raw.Items[1].OpenInNew || raw.Items[1].Target != "" {
		t.Fatalf("targets: %+v", raw.Items)
	}

	// The edit token check fails for a menu that no longer exists.
	tok := "x"
	if _, err := s.saveGuarded(ctx, Menu{ID: "missing", Name: "Ghost"}, &tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("guarded save of a missing menu: %v", err)
	}
}

func TestMenuTreeStopsAtMaxDepth(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	m, err := s.Save(ctx, Menu{Name: "Main"})
	if err != nil {
		t.Fatal(err)
	}
	// Rows written behind the service's back may nest deeper than maxDepth:
	// the item at depth maxDepth is kept as a leaf, deeper rows are dropped.
	parent := ""
	for i := 0; i <= maxDepth+1; i++ {
		id := fmt.Sprintf("i%d", i)
		var p any
		if parent != "" {
			p = parent
		}
		mustExec(t, s, `INSERT INTO menu_items (id, menu_id, parent_id, position, label, kind, ref_id, url, target, css_class, open_in_new)
			VALUES (?, ?, ?, 0, ?, 'custom', '', '/', '', '', 0)`, id, m.ID, p, id)
		parent = id
	}
	raw, err := s.getRaw(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	it := raw.Items[0]
	for d := 1; d <= maxDepth; d++ {
		if len(it.Children) != 1 {
			t.Fatalf("depth %d: %+v", d, it)
		}
		it = it.Children[0]
	}
	if it.ID != fmt.Sprintf("i%d", maxDepth) || it.Children == nil || len(it.Children) != 0 {
		t.Fatalf("deepest built item: %+v", it)
	}
}

type brokenContent struct{ content.Service }

func (brokenContent) Get(context.Context, string) (*content.Item, error) {
	return nil, errors.New("content store down")
}

func TestResolveEdgeCases(t *testing.T) {
	s, h := testService(t)
	ctx := context.Background()
	fc := fakeContent{types: map[string]*content.Type{"page": {Slug: "page", Plural: map[string]string{"en": "Pages"}}}}
	s.content = func() (content.Service, bool) { return fc, true }
	s.taxonomy = func() (taxonomy.Service, bool) {
		return fakeTaxonomy{terms: map[string]*taxonomy.Term{"t1": {ID: "t1", Taxonomy: "tag", Slug: "go", Name: "Go"}}}, true
	}
	got := s.resolve(ctx, []Item{
		{ID: "a", Kind: KindTerm, RefID: "t1"},
		{ID: "b", Kind: KindTerm, RefID: "gone"},
		{ID: "c", Kind: KindArchive, RefID: "page"},
		{ID: "d", Kind: KindArchive, RefID: "missing"},
	})
	if len(got) != 2 || got[0].Label != "Go" || got[0].URL != "/tag/go" || got[1].Label != "Pages" || got[1].URL != "/page/" {
		t.Fatalf("resolved: %+v", got)
	}

	// A failing content lookup drops the item rather than linking blindly.
	s.content = func() (content.Service, bool) { return brokenContent{}, true }
	if got := s.resolve(ctx, []Item{{ID: "e", Kind: KindContent, RefID: "c1", URL: "/stale"}}); len(got) != 0 {
		t.Fatalf("content error kept item: %+v", got)
	}

	// A failing filter keeps the fallback; a filter may return a new *Item.
	h.Filter(FilterResolveURL, 10, func(_ context.Context, v any) (any, error) {
		it := v.(*Item)
		switch it.ID {
		case "bad":
			return nil, errors.New("filter failed")
		case "swap":
			cp := *it
			cp.URL, cp.Label = "/swapped", "Swapped"
			return &cp, nil
		}
		return it, nil
	})
	got = s.resolve(ctx, []Item{{ID: "bad", Kind: KindCustom, Label: "Bad", URL: "/bad"}, {ID: "swap", Kind: KindCustom, Label: "S", URL: "/s"}})
	if len(got) != 2 || got[0].URL != "/bad" || got[1].URL != "/swapped" || got[1].Label != "Swapped" {
		t.Fatalf("filters: %+v", got)
	}
}

func TestLocalizedFallbacks(t *testing.T) {
	for _, c := range []struct {
		m      map[string]string
		locale string
		want   string
	}{
		{map[string]string{"ar": "مقالات", "en": "Posts"}, "ar", "مقالات"},
		{map[string]string{"en": "Posts"}, "ar", "Posts"},
		{map[string]string{"fr": "Articles"}, "ar", "Articles"},
		{nil, "ar", ""},
	} {
		if got := localized(c.m, c.locale); got != c.want {
			t.Errorf("localized(%v, %s) = %q, want %q", c.m, c.locale, got, c.want)
		}
	}
	if got := ArchivePath(&content.Type{Slug: "news"}); got != "/news/" {
		t.Fatalf("archive path without archive slug: %q", got)
	}
}

type stringTheme struct{}

func (stringTheme) Active(context.Context) string { return "not a struct" }

func TestThemeLocationsRejectsUnusableThemes(t *testing.T) {
	d := &kernel.Deps{Services: kernel.NewServiceRegistry()}
	ctx := context.Background()
	d.Services.Set(kernel.ServiceTheme, stringTheme{})
	if got := themeLocations(ctx, d); !reflect.DeepEqual(got, DefaultLocations) {
		t.Fatalf("non-struct Active: %v", got)
	}
	d.Services.Set(kernel.ServiceTheme, fakeTheme{locs: []string{"Bad Name", "UPPER"}})
	if got := themeLocations(ctx, d); !reflect.DeepEqual(got, DefaultLocations) {
		t.Fatalf("all-invalid locations: %v", got)
	}
}

func TestRegisterRequiresAdminTemplates(t *testing.T) {
	d := &kernel.Deps{Services: kernel.NewServiceRegistry(), Log: discardLog()}
	err := New().Register(d)
	if err == nil || !strings.Contains(err.Error(), "admin templates") {
		t.Fatalf("register without templates: %v", err)
	}
	if _, ok := kernel.Get[Service](d, kernel.ServiceMenus); !ok {
		t.Fatal("the menus service is published before the admin check")
	}
}
