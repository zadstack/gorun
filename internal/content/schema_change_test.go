package content

import (
	"context"
	"testing"
)

// Review Focus 4: editing a type's field schema after content exists keeps
// stored fields readable and only re-validates on the next save.
func TestSchemaChangeKeepsOldFieldsReadable(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	typ := Type{Slug: "event", Name: map[string]string{"en": "Events"}, Fields: []Field{
		{Key: "venue", Type: FieldText},
		{Key: "seats", Type: FieldText},
		{Key: "old", Type: FieldText, Indexed: true},
	}}
	mustType(t, s, typ)
	it := mustCreate(t, s, Input{Type: "event", Title: "Conf", Fields: map[string]any{"venue": "Cairo", "seats": "many", "old": "keep me"}})

	// seats: text → required number; old: removed; new required field added.
	typ.Fields = []Field{
		{Key: "venue", Type: FieldText},
		{Key: "seats", Type: FieldNumber, Required: true},
		{Key: "capacity", Type: FieldNumber, Required: true},
	}
	mustType(t, s, typ)

	got, err := s.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fields["seats"] != "many" || got.Fields["old"] != "keep me" || got.Fields["venue"] != "Cairo" {
		t.Fatalf("stored fields changed on read: %v", got.Fields)
	}
	if page, err := s.Query(ctx, Query{Type: "event"}); err != nil || page.Items[0].Fields["seats"] != "many" {
		t.Fatalf("query read: %v %v", page, err)
	}
	// The next save re-validates: the stale value is rejected, not crashed on.
	if _, err := s.Update(ctx, it.ID, InputFromItem(got)); err == nil {
		t.Fatal("stale value must fail validation on save")
	}
	in := InputFromItem(got)
	in.Fields = map[string]any{"venue": "Giza", "seats": "120", "capacity": 200}
	up, err := s.Update(ctx, it.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if up.Fields["seats"] != 120.0 || up.Fields["old"] != "keep me" {
		t.Fatalf("after save: %v (orphaned field must be kept)", up.Fields)
	}
	if rows := indexRows(t, s, it.ID); rows["old"] {
		t.Fatal("removed field must leave the index")
	}
}
