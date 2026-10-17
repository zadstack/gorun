package content

import (
	"context"
	"errors"
	"testing"
)

// Unknown statuses and unparseable publish dates are field errors, and
// nothing is stored.
func TestCreateRejectsUnknownStatusAndBadPublishAt(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "note", Name: map[string]string{"en": "Notes"}})
	ctx := context.Background()
	for _, tc := range []struct {
		in    Input
		field string
		msg   string
	}{
		{Input{Type: "note", Title: "x", Status: "bogus"}, "status", "content.validation.status"},
		{Input{Type: "note", Title: "x", Status: "published", PublishAt: "not a date"}, "publish_at", "content.validation.datetime"},
	} {
		_, err := s.Create(ctx, tc.in)
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Fields[tc.field] != tc.msg {
			t.Errorf("%+v: %v", tc.in, err)
		}
	}
	res, err := s.Query(ctx, Query{Type: "note", IncludeTrash: true})
	if err != nil || res.Total != 0 {
		t.Fatalf("rejected creates stored items: %v %d", err, res.Total)
	}
}

// Trashing an already trashed item is a no-op that keeps it in the trash.
func TestDeleteTrashedItemAgainIsNoop(t *testing.T) {
	s := newTestService(t)
	mustType(t, s, Type{Slug: "note", Name: map[string]string{"en": "Notes"}})
	ctx := context.Background()
	it := mustCreate(t, s, Input{Type: "note", Title: "x"})
	if err := s.Delete(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Get(ctx, it.ID)
	if err := s.Delete(ctx, it.ID, false); err != nil {
		t.Fatalf("second trash: %v", err)
	}
	again, err := s.Get(ctx, it.ID)
	if err != nil || again.Status != "trash" || again.UpdatedAt != first.UpdatedAt {
		t.Fatalf("second trash changed the item: %+v %v", again, err)
	}
}
