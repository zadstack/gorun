package content

import (
	"context"

	"github.com/zadstack/gorun/internal/auth"
)

func (s *service) checkRead(ctx context.Context, it *Item) error {
	u := auth.UserFromContext(ctx)
	if !ownOnly(u) || it.AuthorID == u.ID {
		return nil
	}
	if it.Status != "published" {
		return ErrForbidden
	}
	typ, err := s.GetType(ctx, it.Type)
	if err != nil {
		return err
	}
	if !typ.Public {
		return ErrForbidden
	}
	return nil
}

func restrictStaffRead(ctx context.Context, b *queryBuilder) {
	if u := auth.UserFromContext(ctx); ownOnly(u) {
		b.add("(c.author_id = ? OR (c.status = 'published' AND EXISTS (SELECT 1 FROM content_types t WHERE t.slug = c.type AND t.public = 1)))", u.ID)
	}
}
