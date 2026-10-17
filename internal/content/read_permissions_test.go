package content

import (
	"context"
	"errors"
	"github.com/zadstack/gorun/internal/auth"
	"testing"
)

func TestStaffReadVisibility(t *testing.T) {
	for _, role := range []string{"author", "contributor"} {
		t.Run(role, func(t *testing.T) {
			s := newTestService(t)
			ctx := auth.WithUser(context.Background(), &auth.User{ID: "reader", Role: role})
			mustType(t, s, Type{Slug: "invoice", Name: map[string]string{"en": "Invoice"}, Public: false})
			own := mustCreate(t, s, Input{Type: "invoice", Title: "Own private", AuthorID: "reader", Status: "private"})
			public := mustCreate(t, s, Input{Type: "post", Title: "Public", AuthorID: "other", Status: "published", Locale: "ar"})
			hidden := mustCreate(t, s, Input{Type: "post", Title: "Hidden draft", AuthorID: "other", Status: "draft", Locale: "ar"})
			invoice := mustCreate(t, s, Input{Type: "invoice", Title: "Other invoice", AuthorID: "other", Status: "published"})
			for _, it := range []*Item{hidden, invoice} {
				if _, err := s.Get(ctx, it.ID); !errors.Is(err, ErrForbidden) {
					t.Errorf("Get %s: %v", it.Title, err)
				}
				if _, err := s.GetBySlug(ctx, it.Type, it.Slug, it.Locale); !errors.Is(err, ErrForbidden) {
					t.Errorf("GetBySlug %s: %v", it.Title, err)
				}
			}
			for _, it := range []*Item{own, public} {
				if _, err := s.Get(ctx, it.ID); err != nil {
					t.Errorf("allowed %s: %v", it.Title, err)
				}
			}
			page, err := s.Query(ctx, Query{})
			if err != nil || page.Total != 2 || len(page.Items) != 2 {
				t.Errorf("visibility count=%d items=%d err=%v", page.Total, len(page.Items), err)
			}
			for pageNumber := 1; pageNumber <= 2; pageNumber++ {
				page, err := s.Query(ctx, Query{Page: pageNumber, PerPage: 1})
				if err != nil || page.Total != 2 || len(page.Items) != 1 || (page.Items[0].ID != own.ID && page.Items[0].ID != public.ID) {
					t.Errorf("visibility pagination: %+v %v", page, err)
				}
			}
			if _, err := s.Revisions(ctx, public.ID); !errors.Is(err, ErrForbidden) {
				t.Errorf("public revisions: %v", err)
			}
			if _, err := s.Revisions(ctx, own.ID); err != nil {
				t.Errorf("own revisions: %v", err)
			}
			tr, err := s.CreateTranslation(context.Background(), public.ID, "en")
			if err != nil {
				t.Fatal(err)
			}
			members, err := s.Translations(ctx, public.ID)
			if err != nil || len(members) != 1 {
				t.Errorf("translation draft leak: %d %v", len(members), err)
			}
			for _, it := range []*Item{hidden, invoice, tr} {
				if _, err := s.Get(auth.WithUser(context.Background(), &auth.User{ID: "editor", Role: "editor"}), it.ID); err != nil {
					t.Errorf("editor access: %v", err)
				}
			}
		})
	}
}
