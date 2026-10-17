package content

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestLinkTranslationExpectedMembersConflictPreservesAllGroups(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mustCreate(t, s, Input{Type: "page", Title: "English", Locale: "en"})
	b := mustCreate(t, s, Input{Type: "page", Title: "Arabic", Locale: "ar"})
	foreign := mustCreate(t, s, Input{Type: "page", Title: "Existing foreign locale", Locale: "en"})
	// A persisted additional locale is deliberately introduced between the caller's
	// discovery and link request, without expanding supported UI languages.
	if _, err := s.db.Exec(`UPDATE content SET locale='fr',translation_group=? WHERE id=?`, a.ID, foreign.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE content SET translation_group=? WHERE id=?`, a.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, id := range []string{a.ID, b.ID, foreign.ID} {
		it, _ := s.Get(ctx, id)
		before[id] = it.TranslationGroup
	}
	var auditBefore int
	if err := s.db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkTranslationGuarded(ctx, a.ID, b.ID, []string{a.ID, b.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	after := map[string]string{}
	for id := range before {
		it, _ := s.Get(ctx, id)
		after[id] = it.TranslationGroup
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("a rejected link changed a group")
	}
	var audits int
	if err := s.db.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != auditBefore {
		t.Fatal("rejected link wrote audit")
	}
	// The legacy omitted-guard contract still merges this exact persisted state.
	if err := s.LinkTranslation(ctx, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	group, err := s.Translations(ctx, a.ID)
	if err != nil || len(group) != 3 {
		t.Fatalf("legacy behavior changed: %d %v", len(group), err)
	}
}

func TestLinkTranslationGuardedExactPairAndIdempotence(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := mustCreate(t, s, Input{Type: "page", Title: "A", Locale: "en"})
	b := mustCreate(t, s, Input{Type: "page", Title: "B", Locale: "ar"})
	for _, expected := range [][]string{nil, {}, {a.ID}, {a.ID, a.ID}, {a.ID, b.ID, "unknown"}} {
		if err := s.LinkTranslationGuarded(ctx, a.ID, b.ID, expected); !errors.Is(err, ErrConflict) {
			t.Fatalf("bad expected IDs accepted: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.LinkTranslationGuarded(ctx, a.ID, b.ID, []string{b.ID, a.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.LinkTranslationGuarded(ctx, a.ID, b.ID, []string{a.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-group shortcut bypassed guard: %v", err)
	}
}

func TestLinkTranslationGuardedChecksCurrentOwnersAndMutationGuard(t *testing.T) {
	s := newTestService(t)
	a := mustCreate(t, s, Input{Type: "page", Title: "A", Locale: "en"})
	b := mustCreate(t, s, Input{Type: "page", Title: "B", Locale: "ar", Status: "published"})
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "not-owner", Role: "author"})
	if err := s.LinkTranslationGuarded(ctx, a.ID, b.ID, []string{a.ID, b.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ownership bypassed: %v", err)
	}
	if err := s.LinkTranslationGuarded(GuardUnapprovedMutation(context.Background()), a.ID, b.ID, []string{a.ID, b.ID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("approval guard bypassed: %v", err)
	}
}

func TestLinkTranslationGuardedRejectsInvalidCurrentItems(t *testing.T) {
	for _, scenario := range []string{"same locale", "different type", "trash", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			s := newTestService(t)
			ctx := context.Background()
			a := mustCreate(t, s, Input{Type: "page", Title: "A", Locale: "en"})
			b := mustCreate(t, s, Input{Type: "page", Title: "B", Locale: "ar"})
			switch scenario {
			case "same locale":
				if _, err := s.db.Exec(`UPDATE content SET locale='en' WHERE id=?`, b.ID); err != nil {
					t.Fatal(err)
				}
			case "different type":
				if _, err := s.db.Exec(`UPDATE content SET type='post' WHERE id=?`, b.ID); err != nil {
					t.Fatal(err)
				}
			case "trash":
				if _, err := s.db.Exec(`UPDATE content SET status='trash' WHERE id=?`, b.ID); err != nil {
					t.Fatal(err)
				}
			case "missing":
				b.ID = "missing"
			}
			if err := s.LinkTranslationGuarded(ctx, a.ID, b.ID, []string{a.ID, b.ID}); err == nil {
				t.Fatal("invalid current pair accepted")
			}
			current, err := s.Get(ctx, a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.TranslationGroup != "" {
				t.Fatal("rejected pair changed translation group")
			}
		})
	}
}
