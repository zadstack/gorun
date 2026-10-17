package content

import (
	"context"
	"errors"
	"testing"
)

func TestPublishUnpublishParentAndRelations(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	parent := mustCreate(t, s, Input{Type: "page", Title: "About"})
	child := mustCreate(t, s, Input{Type: "page", Title: "Team", ParentID: parent.ID})
	if child.ParentID != parent.ID {
		t.Fatalf("parent: %+v", child)
	}
	in := InputFromItem(parent)
	in.ParentID = child.ID
	if _, err := s.Update(ctx, parent.ID, in); err == nil {
		t.Fatal("cycle accepted")
	}
	if _, err := s.Create(ctx, Input{Type: "post", Title: "x", ParentID: parent.ID}); err == nil {
		t.Fatal("parent on a flat type accepted")
	}
	if page, _ := s.Query(ctx, Query{Type: "page", ParentID: "root"}); page.Total != 1 {
		t.Fatalf("top-level pages: %d", page.Total)
	}
	if err := s.Publish(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Unpublish(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, child.ID); got.Status != "draft" || got.PublishedAt == "" {
		t.Fatalf("unpublish: %+v", got)
	}
	if err := s.Publish(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publish missing: %v", err)
	}

	mustType(t, s, Type{Slug: "book", Name: map[string]string{"en": "Books"}, Fields: []Field{
		{Key: "related", Type: FieldRelationship, TargetType: "page", Multiple: true}}})
	b := mustCreate(t, s, Input{Type: "book", Title: "B", Fields: map[string]any{"related": []any{child.ID, parent.ID}}})
	ids, err := s.RelatedIDs(ctx, b.ID, "related")
	if err != nil || len(ids) != 2 || ids[0] != child.ID {
		t.Fatalf("relations: %v %v", ids, err)
	}
	if err := s.DeleteType(ctx, "book"); !errors.Is(err, ErrTypeInUse) {
		t.Fatalf("delete type in use: %v", err)
	}
	var ve *ValidationError
	if _, err := s.Create(ctx, Input{Type: "nope"}); !errors.As(err, &ve) || ve.Error() == "" {
		t.Fatalf("unknown type: %v", err)
	}
	in = InputFromItem(b)
	in.Type = "page"
	if _, err := s.Update(ctx, b.ID, in); !errors.As(err, &ve) {
		t.Fatalf("type change: %v", err)
	}
}

func TestBeforeSaveFilterAndAfterSaveHook(t *testing.T) {
	s := newTestService(t)
	var saved []string
	s.hooks.Filter("content.before_save", 0, func(_ context.Context, v any) (any, error) {
		in := v.(*Input)
		in.Excerpt = "filtered"
		return in, nil
	})
	s.hooks.Action("content.after_save", 0, func(_ context.Context, p any) error {
		saved = append(saved, p.(*Item).ID)
		return nil
	})
	it := mustCreate(t, s, Input{Type: "post", Title: "Hooked"})
	if it.Excerpt != "filtered" || len(saved) != 1 || saved[0] != it.ID {
		t.Fatalf("hooks: %q %v", it.Excerpt, saved)
	}
}
