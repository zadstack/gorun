package content

import (
	"context"
	"errors"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

func TestCreateTranslationChecksSourceOwnership(t *testing.T) {
	for _, role := range []string{"author", "contributor", "editor"} {
		t.Run(role, func(t *testing.T) {
			s := newTestService(t)
			src := mustCreate(t, s, Input{Type: "post", Title: "Private draft", Locale: "ar", AuthorID: "owner"})
			ctx := auth.WithUser(context.Background(), &auth.User{ID: "other", Role: role})
			_, err := s.CreateTranslation(ctx, src.ID, "en")
			if role == "editor" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("cross-owner translation: %v", err)
			}
			after, err := s.Get(context.Background(), src.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.TranslationGroup != "" {
				t.Fatal("refused translation mutated the source")
			}
			items, err := s.Translations(context.Background(), src.ID)
			if err != nil || len(items) != 1 {
				t.Fatalf("translations: %v %v", items, err)
			}
			if _, err := s.CreateTranslation(auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: role}), src.ID, "en"); err != nil {
				t.Fatalf("owner translation: %v", err)
			}
		})
	}
}
