package builder

import (
	"errors"
	"github.com/zadstack/gorun/internal/content"
	"testing"
)

func TestResetGuardedPreservesConcurrentContentAndPrivateDraft(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Page", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	sec := DefaultSection("hero", "en")
	sec.Title = "Published"
	draft, err := s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{sec}}, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, it.ID, draft.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	draft, _ = s.Load(ctx, it.ID)
	sec.Title = "Private draft"
	draft, err = s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{sec}}, draft.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := cs.Get(ctx, it.ID)
	expected := content.EditToken(before)
	in := content.InputFromItem(before)
	in.Body = "<p>New human content</p>"
	after, err := cs.Update(ctx, it.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ResetGuarded(ctx, it.ID, draft.UpdatedAt, &expected); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale content accepted: %v", err)
	}
	unchanged, _ := s.Load(ctx, it.ID)
	if unchanged.Document.Sections[0].Title != "Private draft" || unchanged.UpdatedAt != draft.UpdatedAt {
		t.Fatal("private draft lost")
	}
	empty := ""
	if err = s.ResetGuarded(ctx, it.ID, draft.UpdatedAt, &empty); !errors.Is(err, ErrConflict) {
		t.Fatalf("explicit empty accepted: %v", err)
	}
	current := content.EditToken(after)
	if err = s.ResetGuarded(ctx, it.ID, draft.UpdatedAt, &current); err != nil {
		t.Fatal(err)
	}
	restored, _ := s.Load(ctx, it.ID)
	live, _ := cs.Get(ctx, it.ID)
	if restored.Document.Sections[0].Title != "Published" || live.Body != after.Body || restored.ContentChanged || restored.ContentToken != current {
		t.Fatal("guarded reset changed live content or incorrect baseline")
	}
}
