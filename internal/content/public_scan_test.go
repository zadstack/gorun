package content

import (
	"context"
	"fmt"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/db"
)

func TestPublishedScanAppliesVisibilityBeforeCursorPagination(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, s.db, "auth", auth.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := auth.ReloadRoles(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	mustType(t, s, Type{Slug: "article", Public: true, Name: map[string]string{"en": "Article"}})
	mustType(t, s, Type{Slug: "internal-note", Public: false, Name: map[string]string{"en": "Note"}})
	protected := mustCreate(t, s, Input{Type: "article", Title: "Protected", Status: "published"})
	excluded := mustCreate(t, s, Input{Type: "article", Title: "Host excluded", Status: "published"})
	admin := auth.WithUser(ctx, &auth.User{ID: "owner", Role: "admin", Status: "active"})
	if _, err := s.SavePageAccess(admin, protected.ID, PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, Input{Type: "article", Title: "Draft", Status: "draft"})
	mustCreate(t, s, Input{Type: "internal-note", Title: "Internal", Status: "published"})
	for i := range 105 {
		mustCreate(t, s, Input{Type: "article", Title: fmt.Sprintf("Visible %d", i), Status: "published"})
	}
	ctx = WithPublicExclusions(ctx, []string{excluded.ID})
	afterID, count := "", 0
	for {
		items, err := s.PublishedAfter(ctx, "article", afterID)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 && len(items) != 100 {
			t.Fatalf("filtered after pagination: %d", len(items))
		}
		for _, it := range items {
			if it.ID <= afterID || it.ID == excluded.ID || it.ID == protected.ID || it.Status != "published" {
				t.Fatalf("invalid scan result: %+v", it)
			}
			afterID = it.ID
			count++
		}
		if len(items) < 100 {
			break
		}
	}
	if count != 105 {
		t.Fatalf("lost/extra public items: %d", count)
	}
	hidden, err := s.PublishedAfter(ctx, "internal-note", "")
	if err != nil || len(hidden) != 0 {
		t.Fatalf("internal type exposed: %+v %v", hidden, err)
	}
}
