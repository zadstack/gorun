package content

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Review fix 2: SaveType rebuilds the index when indexed flags, field types
// or relationship fields change.
func TestSaveTypeReindexesOnSchemaChange(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	typ := Type{Slug: "gadget", Name: map[string]string{"en": "Gadgets"}, Fields: []Field{
		{Key: "sku", Type: FieldText}, {Key: "weight", Type: FieldText, Indexed: true}}}
	mustType(t, s, typ)
	a := mustCreate(t, s, Input{Type: "gadget", Title: "A", Fields: map[string]any{"sku": "A-1", "weight": "12"}})
	mustCreate(t, s, Input{Type: "gadget", Title: "B", Fields: map[string]any{"sku": "B-1", "weight": "3"}})

	typ.Fields[0].Indexed = true // sku becomes indexed
	mustType(t, s, typ)
	page, err := s.Query(ctx, Query{Type: "gadget", Fields: map[string]Filter{"sku": {Op: "eq", Value: "A-1"}}})
	if err != nil || page.Total != 1 || page.Items[0].ID != a.ID {
		t.Fatalf("query by newly indexed field: %+v %v", page, err)
	}

	typ.Fields[1].Type = FieldNumber // weight: text → number
	mustType(t, s, typ)
	var num *float64
	var text *string
	s.db.QueryRow(`SELECT num_value, text_value FROM content_index WHERE content_id = ? AND field = 'weight'`, a.ID).Scan(&num, &text)
	if num == nil || *num != 12 || text != nil {
		t.Fatalf("retyped index row: num=%v text=%v", num, text)
	}
	page, _ = s.Query(ctx, Query{Type: "gadget", Fields: map[string]Filter{"weight": {Op: "gt", Value: 5}}})
	if page.Total != 1 || page.Items[0].ID != a.ID {
		t.Fatalf("numeric query after retype: %d", page.Total)
	}
}

// Review fix 3: a deleted relationship target never makes the item
// unsaveable; stale stored ids are dropped, new unknown ids still fail.
func TestDeletedRelationshipTargetDroppedOnSave(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "book", Name: map[string]string{"en": "Books"}, Fields: []Field{
		{Key: "author_page", Type: FieldRelationship, TargetType: "page"},
		{Key: "related", Type: FieldRelationship, TargetType: "page", Multiple: true}}})
	b := mustCreate(t, s, Input{Type: "page", Title: "B"})
	c := mustCreate(t, s, Input{Type: "page", Title: "C"})
	a := mustCreate(t, s, Input{Type: "book", Title: "A", Fields: map[string]any{"author_page": b.ID, "related": []any{b.ID, c.ID}}})
	revs, _ := s.Revisions(ctx, a.ID)
	if err := s.Delete(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a.ID)
	in := InputFromItem(got)
	in.Title = "A2"
	up, err := s.Update(ctx, a.ID, in)
	if err != nil {
		t.Fatalf("save after target deleted: %v", err)
	}
	if _, ok := up.Fields["author_page"]; ok || len(up.Fields["related"].([]string)) != 1 {
		t.Fatalf("stale ids kept: %v", up.Fields)
	}
	ids, _ := s.RelatedIDs(ctx, a.ID, "related")
	if len(ids) != 1 || ids[0] != c.ID {
		t.Fatalf("relations: %v", ids)
	}
	if _, err := s.Restore(ctx, a.ID, revs[0].ID); err != nil {
		t.Fatalf("restore with a deleted target: %v", err)
	}
	in = InputFromItem(up)
	in.Fields = map[string]any{"related": []any{c.ID, "01NOPE"}}
	var ve *ValidationError
	if _, err := s.Update(ctx, a.ID, in); !errors.As(err, &ve) {
		t.Fatalf("new unknown id must fail: %v", err)
	}
}

// Review fix 5: trash only through Delete.
func TestStatusTrashOnlyViaDelete(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	var ve *ValidationError
	if _, err := s.Create(ctx, Input{Type: "post", Title: "x", Status: "trash"}); !errors.As(err, &ve) || ve.Fields["status"] == "" {
		t.Fatalf("create as trash: %v", err)
	}
	it := mustCreate(t, s, Input{Type: "post", Title: "y"})
	in := InputFromItem(it)
	in.Status = "trash"
	if _, err := s.Update(ctx, it.ID, in); !errors.As(err, &ve) {
		t.Fatalf("update to trash: %v", err)
	}
	if err := s.Delete(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	trashed, _ := s.Get(ctx, it.ID)
	if trashed.Status != "trash" {
		t.Fatal("delete did not trash")
	}
	// Publish/Unpublish refuse trashed items and leave deleted_at alone.
	if err := s.Publish(ctx, it.ID); !errors.As(err, &ve) {
		t.Fatalf("publish trashed: %v", err)
	}
	if err := s.Unpublish(ctx, it.ID); !errors.As(err, &ve) {
		t.Fatalf("unpublish trashed: %v", err)
	}
	var del *string
	s.db.QueryRow(`SELECT deleted_at FROM content WHERE id = ?`, it.ID).Scan(&del)
	if del == nil {
		t.Fatal("deleted_at cleared")
	}
	// Editing a trashed item keeps it in the trash (no transition).
	in = InputFromItem(trashed)
	in.Title = "edited in trash"
	if _, err := s.Update(ctx, it.ID, in); err != nil {
		t.Fatalf("edit while trashed: %v", err)
	}
}

// Minor: one failing scheduled item must not block the others.
func TestPublishDueContinuesPastFailures(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	bad := mustCreate(t, s, Input{Type: "post", Title: "bad", Status: "scheduled", PublishAt: future})
	good := mustCreate(t, s, Input{Type: "post", Title: "good", Status: "scheduled", PublishAt: future})
	if _, err := s.db.Exec(`CREATE TRIGGER boom BEFORE UPDATE ON content WHEN NEW.id = '` + bad.ID + `' BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.publishDue(ctx, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("publishDue returned %v", err)
	}
	if g, _ := s.Get(ctx, good.ID); g.Status != "published" {
		t.Fatalf("good item: %s", g.Status)
	}
	if b, _ := s.Get(ctx, bad.ID); b.Status != "scheduled" {
		t.Fatalf("bad item: %s", b.Status)
	}
}

// Minor: Restore snapshots and saves in one transaction, so a failed
// restore leaves no extra revision.
func TestRestoreFailureLeavesNoSnapshot(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	typ := Type{Slug: "note", Name: map[string]string{"en": "Notes"}, Fields: []Field{{Key: "body2", Type: FieldText}}}
	mustType(t, s, typ)
	it := mustCreate(t, s, Input{Type: "note", Title: "v1"})
	in := InputFromItem(it)
	in.Title = "v2"
	in.Fields = map[string]any{"body2": "x"}
	s.Update(ctx, it.ID, in)
	typ.Fields[0].Required = true // the v1 snapshot (no body2) no longer validates
	mustType(t, s, typ)
	revs, _ := s.Revisions(ctx, it.ID)
	if _, err := s.Restore(ctx, it.ID, revs[len(revs)-1].ID); err == nil {
		t.Fatal("restore of an invalid snapshot should fail")
	}
	if after, _ := s.Revisions(ctx, it.ID); len(after) != len(revs) {
		t.Fatalf("failed restore left %d revisions (had %d)", len(after), len(revs))
	}
}
