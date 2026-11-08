package content

import (
	"context"
	"fmt"
	"strings"
)

func (s *service) PublishedAfter(ctx context.Context, typeSlug, afterID string) ([]Item, error) {
	typ, err := s.GetType(ctx, typeSlug)
	if err != nil {
		return nil, err
	}
	b := &queryBuilder{}
	restrictStaffRead(ctx, b)
	if err := s.buildWhere(ctx, b, Query{Type: typeSlug, Status: "published", PublicOnly: true}, typ); err != nil {
		return nil, err
	}
	b.add("c.id > ?", afterID)
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE `+strings.Join(b.where, " AND ")+` ORDER BY c.id LIMIT 100`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("content: public scan: %w", err)
	}
	items, err := readQueryItems(rows)
	if err != nil {
		return nil, err
	}
	if err := s.fillQueryTerms(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}
