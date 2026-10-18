package builder

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

func heroDoc(title string) Document {
	sec := DefaultSection("hero", "en")
	sec.Title = title
	return Document{Version: 1, Sections: []Section{sec}}
}

func TestLayoutHistorySavePublishRestoreAndCap(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "History", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Save(ctx, it.ID, heroDoc("One"), content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	if revs, _ := s.ListRevisions(ctx, it.ID); len(revs) != 0 {
		t.Fatalf("first save should not snapshot empty previous: %d", len(revs))
	}
	second, err := s.Save(ctx, it.ID, heroDoc("Two"), first.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	revs, err := s.ListRevisions(ctx, it.ID)
	if err != nil || len(revs) != 1 || revs[0].Kind != RevisionKindSave || revs[0].Summary != "One" {
		t.Fatalf("save revision: err=%v revs=%+v", err, revs)
	}
	if err = s.Publish(ctx, it.ID, second.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	published, err := s.Load(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	revs, err = s.ListRevisions(ctx, it.ID)
	if err != nil || len(revs) < 2 || revs[0].Kind != RevisionKindPublish || revs[0].Summary != "Two" {
		t.Fatalf("publish revision: err=%v revs=%+v", err, revs)
	}
	third, err := s.Save(ctx, it.ID, heroDoc("Three"), published.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	oldest := revs[len(revs)-1]
	restored, err := s.RestoreRevision(ctx, it.ID, oldest.ID, third.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Document.Sections) != 1 || restored.Document.Sections[0].Title != "One" {
		t.Fatalf("restore draft: %+v", restored.Document)
	}
	fresh, _ := cs.Get(ctx, it.ID)
	if !strings.Contains(fresh.Body, "Two") {
		t.Fatalf("restore changed public body: %s", fresh.Body)
	}
	after, err := s.ListRevisions(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundRestore := false
	for _, r := range after {
		if r.Kind == RevisionKindRestore && r.Summary == "Three" {
			foundRestore = true
		}
	}
	if !foundRestore {
		t.Fatalf("missing pre-restore snapshot: %+v", after)
	}
	if _, err = s.RestoreRevision(ctx, it.ID, oldest.ID, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale restore: %v", err)
	}
	if _, err = s.RestoreRevision(ctx, it.ID, "missing", restored.UpdatedAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing revision: %v", err)
	}

	s.d.Settings.Set(ctx, "builder.revisions_keep", 3)
	token := restored.UpdatedAt
	for i := 0; i < 6; i++ {
		saved, err := s.Save(ctx, it.ID, heroDoc(fmt.Sprintf("Cap%d", i)), token)
		if err != nil {
			t.Fatal(err)
		}
		token = saved.UpdatedAt
	}
	capped, err := s.ListRevisions(ctx, it.ID)
	if err != nil || len(capped) != 3 {
		t.Fatalf("cap keep=3: err=%v len=%d", err, len(capped))
	}
}

func TestLayoutHistoryPermissions(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Owned", AuthorID: "admin", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.Save(ctx, it.ID, heroDoc("A"), content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Save(ctx, it.ID, heroDoc("B"), saved.UpdatedAt)
	foreign := contributorCtx()
	if _, err := s.ListRevisions(foreign, it.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("list foreign: %v", err)
	}
	revs, _ := s.ListRevisions(ctx, it.ID)
	if len(revs) == 0 {
		t.Fatal("expected revision")
	}
	if _, err := s.RestoreRevision(foreign, it.ID, revs[0].ID, saved.UpdatedAt); !errors.Is(err, ErrForbidden) {
		t.Fatalf("restore foreign: %v", err)
	}
}
