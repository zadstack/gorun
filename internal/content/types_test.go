package content

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestSystemTypesSeeded(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	types, err := s.Types(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].Slug != "post" || types[1].Slug != "page" {
		t.Fatalf("types: %+v", types)
	}
	post, err := s.GetType(ctx, "post")
	if err != nil {
		t.Fatal(err)
	}
	if !post.System || !post.Public || !post.HasArchive || post.Hierarchical ||
		!slices.Equal(post.Taxonomies, []string{"category", "tag", "series"}) || post.Name["ar"] == "" {
		t.Fatalf("post: %+v", post)
	}
	// Migration 0008 gives posts an indexed series_order number field.
	if !slices.ContainsFunc(post.Fields, func(f Field) bool { return f.Key == "series_order" && f.Type == FieldNumber && f.Indexed }) {
		t.Fatalf("post fields: %+v", post.Fields)
	}
	page, _ := s.GetType(ctx, "page")
	if !page.System || !page.Hierarchical || page.HasArchive || !slices.Contains(page.Supports, "parent") {
		t.Fatalf("page: %+v", page)
	}
	if err := s.DeleteType(ctx, "post"); !errors.Is(err, ErrSystemType) {
		t.Fatalf("deleting a system type: %v", err)
	}
	if _, err := s.GetType(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing type: %v", err)
	}
}

func TestSaveTypeValidatesSchema(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	bad := Type{Slug: "product", Name: map[string]string{"en": "Products"}, Fields: []Field{
		{Key: "Price", Type: "number"},
		{Key: "dup", Type: "text"},
		{Key: "dup", Type: "text"},
		{Key: "body2", Type: "richtext", Indexed: true},
		{Key: "x", Type: "wat"},
		{Key: "subs", Type: "text", SubFields: []Field{{Key: "a", Type: "text"}}},
		{Key: "rel", Type: "text", TargetType: "post"},
		{Key: "rel2", Type: "relationship"},
		{Key: "choice", Type: "select"},
	}}
	err := s.SaveType(ctx, bad)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	for _, k := range []string{"fields.0.key", "fields.2.key", "fields.3.indexed", "fields.4.type", "fields.5.sub_fields", "fields.6.target_type", "fields.7.target_type", "fields.8.options"} {
		if ve.Fields[k] == "" {
			t.Errorf("missing error %s in %v", k, ve.Fields)
		}
	}
	good := Type{Slug: "product", Name: map[string]string{"en": "Products"}, Public: true, Fields: []Field{
		{Key: "price", Type: "number", Indexed: true},
		{Key: "specs", Type: "repeater", SubFields: []Field{{Key: "name", Type: "text"}}},
	}}
	if err := s.SaveType(ctx, good); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetType(ctx, "product")
	if err != nil || len(got.Fields) != 2 || got.System || got.CreatedAt == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.DeleteType(ctx, "product"); err != nil {
		t.Fatal(err)
	}
}
