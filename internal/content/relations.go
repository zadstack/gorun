package content

import (
	"context"
	"database/sql"
	"fmt"
)

// writeRelations replaces the content_relations rows of it from its
// relationship fields (denormalized for joins), skipping targets that no
// longer exist.
func writeRelations(ctx context.Context, tx *sql.Tx, typ *Type, it *Item) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM content_relations WHERE from_id = ?`, it.ID); err != nil {
		return fmt.Errorf("content: clear relations: %w", err)
	}
	for _, f := range typ.Fields {
		if f.Type != FieldRelationship {
			continue
		}
		v, ok := it.Fields[f.Key]
		if !ok {
			continue
		}
		for pos, to := range stringList(v) {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO content_relations (from_id, field, to_id, position)
				SELECT ?, ?, id, ? FROM content WHERE id = ?`, it.ID, f.Key, pos, to); err != nil {
				return fmt.Errorf("content: relation %s.%s: %w", it.ID, f.Key, err)
			}
		}
	}
	return nil
}

// RelatedIDs returns the IDs item id points to through field, in order.
func (s *service) RelatedIDs(ctx context.Context, id, field string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT to_id FROM content_relations WHERE from_id = ? AND field = ? ORDER BY position`, id, field)
	if err != nil {
		return nil, fmt.Errorf("content: relations: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var to string
		if err := rows.Scan(&to); err != nil {
			return nil, err
		}
		out = append(out, to)
	}
	return out, rows.Err()
}
