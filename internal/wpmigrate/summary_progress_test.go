package wpmigrate

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/db"
)

func TestMigrationListProgressKeepsPreviewSeparateAndIsolatesMigrations(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	source, err := e.svc.CreateSource(ctx, SourceInput{Name: "Synthetic counts", URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := e.svc.CreateMigration(ctx, source.ID, Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.CreateMigration(ctx, source.ID, Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := e.svc.Migration(ctx, first.ID)
	if err != nil || len(empty.ItemProgress) != 0 {
		t.Fatal("empty queue must have no inferred progress", err, empty)
	}
	insert := func(mid, kind, status string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := db.NewID()
			_, err := e.svc.db.ExecContext(ctx, `INSERT INTO migration_items(id,migration_id,kind,wp_id,status,updated_at) VALUES(?,?,?,?,?,?)`, id, mid, kind, id+strconv.Itoa(i), status, db.Now())
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, status := range []string{ItemDone, ItemPreview, ItemPending, ItemFailed, ItemSkipped} {
		insert(first.ID, "content", status, 1)
	}
	insert(first.ID, "media", ItemDone, 2)
	insert(first.ID, "redirect", ItemFailed, 1)
	insert(first.ID, "rewrite", ItemSkipped, 1)
	insert(second.ID, "content", ItemDone, 3)
	check := func(view MigrationView) {
		t.Helper()
		rows := map[string]MigrationItemProgress{}
		for _, row := range view.ItemProgress {
			rows[row.Kind] = row
		}
		if row := rows["content"]; row.Total != 5 || row.Done != 1 || row.Preview != 1 || row.Pending != 1 || row.Failed != 1 || row.Skipped != 1 {
			t.Fatalf("content preview/import confusion: %+v", row)
		}
		if rows["media"].Total != 2 || rows["media"].Done != 2 || rows["redirect"].Failed != 1 || rows["rewrite"].Skipped != 1 || view.Failed != 2 {
			t.Fatalf("real kind counts incorrect: %+v", view)
		}
		raw, _ := json.Marshal(view)
		if strings.Contains(string(raw), "ItemProgress") || strings.Contains(string(raw), "item_progress") {
			t.Fatal("operator projection changed API contract")
		}
	}
	view, err := e.svc.Migration(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(*view)
	list, err := e.svc.Migrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range list {
		if view.ID == first.ID {
			check(view)
		} else if view.ID == second.ID {
			if len(view.ItemProgress) != 1 || view.ItemProgress[0].Done != 3 {
				t.Fatal("cross-migration counts", view)
			}
		}
	}
	b := e.browser()
	response, body := b.do("GET", "/admin/migrations", nil, "")
	if !strings.Contains(body, "تقدم العناصر المسجلة") || strings.Contains(body, "migrationprogress.counts") {
		t.Fatal("migration summary translation missing")
	}
	if response.StatusCode != 200 || !strings.Contains(body, `data-migration-kind="content" data-total="5" data-done="1" data-preview="1"`) {
		t.Fatal("operator queue summary missing or confuses preview", response.StatusCode)
	}

}
