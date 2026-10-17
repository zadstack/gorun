package audit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

func newWriter(t *testing.T) *Writer {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if err := db.Migrate(context.Background(), d, "audit", Migrations()); err != nil {
		t.Fatal(err)
	}
	return New(d, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestRecordDefaultsToSystemActor(t *testing.T) {
	w, ctx := newWriter(t), context.Background()
	w.Record(WithIP(ctx, "203.0.113.7"), "settings.update", "setting", "site.title", map[string]string{"from": "a", "to": "b"})
	entries, total, err := w.List(ctx, ListQuery{})
	if err != nil || total != 1 || len(entries) != 1 {
		t.Fatalf("%v %d %v", entries, total, err)
	}
	e := entries[0]
	if e.Actor != (Actor{Kind: "system", ID: "", Name: "system"}) || e.Action != "settings.update" ||
		e.TargetKind != "setting" || e.TargetID != "site.title" || e.IP != "203.0.113.7" {
		t.Fatalf("%+v", e)
	}
	var meta map[string]string
	if err := json.Unmarshal(e.Meta, &meta); err != nil || meta["to"] != "b" {
		t.Fatalf("meta %s %v", e.Meta, err)
	}
}

func TestRecordUsesActorFromContextHook(t *testing.T) {
	w, ctx := newWriter(t), context.Background()
	orig := ActorFromContext
	t.Cleanup(func() { ActorFromContext = orig })
	ActorFromContext = func(context.Context) (Actor, bool) {
		return Actor{Kind: "user", ID: "01USER", Name: "Ahmed"}, true
	}
	w.Record(ctx, "content.publish", "content", "01POST", nil)
	entries, _, _ := w.List(ctx, ListQuery{})
	if len(entries) != 1 || entries[0].Actor.ID != "01USER" || entries[0].Actor.Name != "Ahmed" || string(entries[0].Meta) != "{}" {
		t.Fatalf("%+v", entries)
	}
}

func TestListFiltersAndPaginates(t *testing.T) {
	w, ctx := newWriter(t), context.Background()
	for i := 0; i < 5; i++ {
		w.Record(ctx, "content.save", "content", "A", nil)
	}
	w.Record(ctx, "content.delete", "content", "B", nil)
	entries, total, err := w.List(ctx, ListQuery{Action: "content.save", Limit: 2})
	if err != nil || total != 5 || len(entries) != 2 {
		t.Fatalf("%d %d %v", total, len(entries), err)
	}
	if entries[0].ID <= entries[1].ID {
		t.Fatal("want newest first")
	}
	entries, total, _ = w.List(ctx, ListQuery{TargetKind: "content", TargetID: "B"})
	if total != 1 || entries[0].Action != "content.delete" {
		t.Fatalf("%+v", entries)
	}
	page2, _, _ := w.List(ctx, ListQuery{Action: "content.save", Limit: 2, Offset: 4})
	if len(page2) != 1 {
		t.Fatalf("want 1 on last page got %d", len(page2))
	}
}

func TestRecordSurvivesCancelledContext(t *testing.T) {
	w := newWriter(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Record(ctx, "user.logout", "user", "01U", nil)
	if _, total, err := w.List(context.Background(), ListQuery{}); err != nil || total != 1 {
		t.Fatalf("want 1 row after cancelled ctx, got %d %v", total, err)
	}
}
