package content

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestScheduledPublishAtFuturePublishesViaCron(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	var published []string
	s.hooks.Action("content.published", 0, func(_ context.Context, p any) error {
		var it Item
		json.Unmarshal(p.(json.RawMessage), &it)
		published = append(published, it.ID)
		return nil
	})
	future := time.Now().Add(time.Hour).UTC()
	it := mustCreate(t, s, Input{Type: "post", Title: "Later", Status: "published", PublishAt: future.Format("2006-01-02T15:04")})
	if it.Status != "scheduled" || it.PublishedAt != "" {
		t.Fatalf("future publish_at should schedule: %+v", it)
	}
	if err := s.publishDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, it.ID); got.Status != "scheduled" || len(published) != 0 {
		t.Fatalf("published too early: %s %v", got.Status, published)
	}
	if err := s.publishDue(ctx, future.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, it.ID)
	if got.Status != "published" || got.PublishedAt == "" || len(published) != 1 || published[0] != it.ID {
		t.Fatalf("after due: %+v hooks %v", got, published)
	}
	if _, err := s.Create(ctx, Input{Type: "post", Title: "x", Status: "scheduled"}); err == nil {
		t.Fatal("scheduled without publish_at must fail")
	}
	past := mustCreate(t, s, Input{Type: "post", Title: "Past", Status: "scheduled", PublishAt: "2020-01-01T00:00:00Z"})
	if past.Status != "published" || past.PublishedAt != "2020-01-01T00:00:00Z" {
		t.Fatalf("past schedule publishes immediately: %+v", past)
	}
}

func TestTrashThenHardDelete(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "rel", Name: map[string]string{"en": "Rel"}, Fields: []Field{
		{Key: "n", Type: FieldNumber, Indexed: true}, {Key: "to", Type: FieldRelationship, TargetType: "post"}}})
	var deleted []string
	s.hooks.Action("content.deleted", 0, func(_ context.Context, p any) error {
		deleted = append(deleted, string(p.(json.RawMessage)))
		return nil
	})
	it := mustCreate(t, s, Input{Type: "post", Title: "Bye", Status: "published"})
	if err := s.Delete(ctx, it.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, it.ID)
	if err != nil || got.Status != "trash" {
		t.Fatalf("trashed: %+v %v", got, err)
	}
	if page, _ := s.Query(ctx, Query{Type: "post"}); page.Total != 0 {
		t.Fatal("trash must be hidden from default queries")
	}
	// Restoring from trash by update clears deleted_at.
	in := InputFromItem(got)
	in.Status = "draft"
	if _, err := s.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	var del *string
	s.db.QueryRow(`SELECT deleted_at FROM content WHERE id = ?`, it.ID).Scan(&del)
	if del != nil {
		t.Fatalf("deleted_at kept: %v", *del)
	}
	s.Delete(ctx, it.ID, false)
	// Not yet 30 days old: the purge keeps it.
	if err := s.purgeTrash(ctx, time.Now().Add(29*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, it.ID); err != nil {
		t.Fatalf("purged too early: %v", err)
	}
	if err := s.purgeTrash(ctx, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purge should hard delete: %v", err)
	}
	var revs int
	s.db.QueryRow(`SELECT COUNT(*) FROM revisions WHERE content_id = ?`, it.ID).Scan(&revs)
	if revs != 0 || len(deleted) != 3 {
		t.Fatalf("cascade revisions %d, deleted events %d", revs, len(deleted))
	}
	other := mustCreate(t, s, Input{Type: "post", Title: "Direct"})
	linked := mustCreate(t, s, Input{Type: "rel", Title: "L", Fields: map[string]any{"n": 1, "to": other.ID}})
	if err := s.Delete(ctx, linked.ID, true); err != nil {
		t.Fatal(err)
	}
	var idx, rels int
	s.db.QueryRow(`SELECT COUNT(*) FROM content_index WHERE content_id = ?`, linked.ID).Scan(&idx)
	s.db.QueryRow(`SELECT COUNT(*) FROM content_relations WHERE from_id = ? OR to_id = ?`, linked.ID, linked.ID).Scan(&rels)
	if idx != 0 || rels != 0 {
		t.Fatalf("hard delete left index=%d relations=%d rows", idx, rels)
	}
	if err := s.Delete(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("hard delete")
	}
}
