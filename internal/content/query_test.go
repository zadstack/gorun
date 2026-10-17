package content

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func productType() Type {
	return Type{Slug: "product", Name: map[string]string{"en": "Products"}, Public: true, Taxonomies: []string{"category"}, Fields: []Field{
		{Key: "price", Type: FieldNumber, Indexed: true},
		{Key: "sku", Type: FieldText, Indexed: true},
		{Key: "launch", Type: FieldDate, Indexed: true},
		{Key: "featured", Type: FieldBoolean, Indexed: true},
		{Key: "tier", Type: FieldSelect, Indexed: true, Options: []Option{{Value: "basic"}, {Value: "pro"}}},
		{Key: "notes", Type: FieldText},
		{Key: "colors", Type: FieldMultiselect, Options: []Option{{Value: "red"}}},
		{Key: "specs", Type: FieldRepeater, SubFields: []Field{{Key: "k", Type: FieldText}}},
	}}
}

func indexRows(t *testing.T, s *service, id string) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT field FROM content_index WHERE content_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var f string
		rows.Scan(&f)
		out[f] = true
	}
	return out
}

func TestIndexWritesScalarIndexedFieldsOnly(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, productType())
	it := mustCreate(t, s, Input{Type: "product", Title: "A", Fields: map[string]any{
		"price": 10, "sku": "A-1", "launch": "2026-01-02", "featured": true, "tier": "pro",
		"notes": "not indexed", "colors": []any{"red"}, "specs": []any{map[string]any{"k": "v"}},
	}})
	got := indexRows(t, s, it.ID)
	want := map[string]bool{"price": true, "sku": true, "launch": true, "featured": true, "tier": true}
	if len(got) != len(want) {
		t.Fatalf("index rows: %v", got)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing index row %s", k)
		}
	}
	var num float64
	s.db.QueryRow(`SELECT num_value FROM content_index WHERE content_id = ? AND field = 'price'`, it.ID).Scan(&num)
	if num != 10 {
		t.Fatalf("num_value %v", num)
	}
	// Reindex rebuilds rows that were lost.
	s.db.Exec(`DELETE FROM content_index`)
	if err := s.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(indexRows(t, s, it.ID)) != len(want) {
		t.Fatal("reindex did not rebuild")
	}
}

func titles(p Page[Item]) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.Title)
	}
	return out
}

func TestQueryByIndexedFieldGtAndOrder(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, productType())
	for _, p := range []struct {
		title string
		price float64
		tier  string
	}{{"cheap", 5, "basic"}, {"mid", 50, "pro"}, {"pricey", 500, "pro"}, {"mid2", 50, "basic"}} {
		mustCreate(t, s, Input{Type: "product", Title: p.title, Status: "published", Fields: map[string]any{"price": p.price, "tier": p.tier}})
	}
	page, err := s.Query(ctx, Query{Type: "product", Fields: map[string]Filter{"price": {Op: "gt", Value: 10}},
		Order: []Order{{By: "price", Dir: "desc"}, {By: "title", Dir: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(page); !slices.Equal(got, []string{"pricey", "mid", "mid2"}) || page.Total != 3 {
		t.Fatalf("got %v total %d", got, page.Total)
	}
	page, _ = s.Query(ctx, Query{Type: "product", Fields: map[string]Filter{"tier": {Op: "in", Value: []any{"basic"}}}, Order: []Order{{By: "price"}}})
	if got := titles(page); !slices.Equal(got, []string{"cheap", "mid2"}) {
		t.Fatalf("in: %v", got)
	}
	page, _ = s.Query(ctx, Query{Type: "product", Fields: map[string]Filter{"price": {Op: "lte", Value: "50"}, "tier": {Op: "ne", Value: "pro"}}, Order: []Order{{By: "title"}}, PerPage: 1})
	if got := titles(page); !slices.Equal(got, []string{"cheap"}) || page.Total != 2 || page.PerPage != 1 {
		t.Fatalf("paged: %v %d", got, page.Total)
	}
	var ve *ValidationError
	if _, err := s.Query(ctx, Query{Type: "product", Fields: map[string]Filter{"notes": {Op: "eq", Value: "x"}}}); !errors.As(err, &ve) {
		t.Fatalf("filter on non-indexed field must fail: %v", err)
	}
	if _, err := s.Query(ctx, Query{Type: "product", Order: []Order{{By: "title; DROP TABLE content", Dir: "asc"}}}); !errors.As(err, &ve) {
		t.Fatalf("bad order must fail: %v", err)
	}
	if _, err := s.Query(ctx, Query{Type: "product", Order: []Order{{By: "title", Dir: "sideways"}}}); !errors.As(err, &ve) {
		t.Fatalf("bad dir must fail: %v", err)
	}
}

// fakeTerms is an in-memory TermBackend.
type fakeTerms struct {
	byContent map[string]map[string][]string
}

func (f *fakeTerms) SetContentTerms(_ context.Context, id string, terms map[string][]string) error {
	f.byContent[id] = terms
	return nil
}
func (f *fakeTerms) ContentTerms(_ context.Context, id string) (map[string][]string, error) {
	return f.byContent[id], nil
}
func (f *fakeTerms) ContentIDs(_ context.Context, tax string, slugs []string) ([]string, error) {
	var out []string
	for id, terms := range f.byContent {
		for _, sl := range terms[tax] {
			if slices.Contains(slugs, sl) {
				out = append(out, id)
				break
			}
		}
	}
	return out, nil
}

func TestQueryTermsBackendFilter(t *testing.T) {
	s := newTestService(t)
	ft := &fakeTerms{byContent: map[string]map[string][]string{}}
	s.termsFn = func() TermBackend { return ft }
	a := mustCreate(t, s, Input{Type: "post", Title: "A", Terms: map[string][]string{"category": {"news"}}})
	mustCreate(t, s, Input{Type: "post", Title: "B", Terms: map[string][]string{"category": {"sport"}, "tag": {"x"}}})
	page, err := s.Query(context.Background(), Query{Type: "post", Taxonomy: map[string][]string{"category": {"news"}}})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != a.ID || !slices.Equal(page.Items[0].Terms["category"], []string{"news"}) {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := s.Create(context.Background(), Input{Type: "page", Title: "P", Terms: map[string][]string{"category": {"news"}}}); err == nil {
		t.Fatal("terms of a taxonomy the type does not use must fail")
	}
}

func TestQuerySearchTitleBody(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustCreate(t, s, Input{Type: "post", Title: "Golang tips", Body: "<p>intro</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "Other", Body: "<p>we love golang</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "مقال عربي", Body: "<p>محتوى عن البرمجة</p>"})
	mustCreate(t, s, Input{Type: "post", Title: "100% off_sale"})
	trashed := mustCreate(t, s, Input{Type: "post", Title: "golang trashed"})
	if err := s.Delete(ctx, trashed.ID, false); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Query(ctx, Query{Type: "post", Search: "golang", Order: []Order{{By: "title"}}})
	if got := titles(page); !slices.Equal(got, []string{"Golang tips", "Other"}) {
		t.Fatalf("search: %v", got)
	}
	page, _ = s.Query(ctx, Query{Search: "البرمجة"})
	if len(page.Items) != 1 {
		t.Fatalf("arabic search: %v", titles(page))
	}
	page, _ = s.Query(ctx, Query{Search: "%"})
	if got := titles(page); !slices.Equal(got, []string{"100% off_sale"}) {
		t.Fatalf("wildcards must be literal: %v", got)
	}
	page, _ = s.Query(ctx, Query{Search: "golang", IncludeTrash: true})
	if page.Total != 3 {
		t.Fatalf("include trash: %d", page.Total)
	}
	page, _ = s.Query(ctx, Query{Status: "trash"})
	if page.Total != 1 {
		t.Fatalf("trash only: %d", page.Total)
	}
}
