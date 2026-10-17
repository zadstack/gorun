package content

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// indexColumn returns the content_index column holding values of ft.
func indexColumn(ft FieldType) string {
	switch ft {
	case FieldNumber, FieldBoolean:
		return "num_value"
	case FieldDate, FieldDatetime:
		return "date_value"
	}
	return "text_value"
}

// indexValue converts a stored scalar to its content_index value. Stored
// values are coerced, so a field retyped after content exists (text →
// number, ...) still indexes values that parse under the new type.
func indexValue(ft FieldType, v any) (any, bool) {
	switch v.(type) {
	case nil, map[string]any, []any, []string, []map[string]any:
		return nil, false
	}
	switch ft {
	case FieldBoolean:
		b, msg := coerceBool(v)
		if msg != "" || b == nil {
			return nil, false
		}
		if b.(bool) {
			return 1.0, true
		}
		return 0.0, true
	case FieldNumber:
		n, msg := coerceNumber(Field{}, v)
		if msg != "" || n == nil {
			return nil, false
		}
		return n, true
	case FieldDatetime:
		if ts, ok := ParseDatetime(scalarString(v)); ok {
			return db.FormatTime(ts), true
		}
		return nil, false
	default:
		str := scalarString(v)
		return str, str != ""
	}
}

// writeIndex replaces the content_index rows of it: one row per indexed
// scalar field (text, number, date, datetime, boolean, select) with a value.
func writeIndex(ctx context.Context, tx *sql.Tx, typ *Type, it *Item) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM content_index WHERE content_id = ?`, it.ID); err != nil {
		return fmt.Errorf("content: clear index: %w", err)
	}
	for _, f := range typ.Fields {
		if !f.Indexed || !f.Type.IsScalar() {
			continue
		}
		v, ok := indexValue(f.Type, it.Fields[f.Key])
		if !ok {
			continue
		}
		col := indexColumn(f.Type)
		if _, err := tx.ExecContext(ctx, `INSERT INTO content_index (content_id, field, `+col+`) VALUES (?, ?, ?)`,
			it.ID, f.Key, v); err != nil {
			return fmt.Errorf("content: index %s.%s: %w", it.ID, f.Key, err)
		}
	}
	return nil
}

// Reindex rebuilds content_index, content_relations and the search index for
// every item from the current schemas (`gorun reindex`).
func (s *service) Reindex(ctx context.Context) error {
	types, err := s.Types(ctx)
	if err != nil {
		return err
	}
	for i := range types {
		if err := s.reindexType(ctx, &types[i]); err != nil {
			return err
		}
	}
	if err := s.rebuildSearch(ctx); err != nil {
		return err
	}
	s.audit.Record(ctx, "content.reindex", "content", "", nil)
	return nil
}

// reindexType rebuilds the index and relation rows of every item of typ.
func (s *service) reindexType(ctx context.Context, typ *Type) error {
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE c.type = ?`, typ.Slug)
	if err != nil {
		return fmt.Errorf("content: reindex list: %w", err)
	}
	var items []*Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		for _, it := range items {
			if err := writeIndex(ctx, tx, typ, it); err != nil {
				return err
			}
			if err := writeRelations(ctx, tx, typ, it); err != nil {
				return err
			}
		}
		return nil
	})
}

// indexSignature describes the schema parts that shape content_index and
// content_relations; a change means the type's rows must be rebuilt.
func indexSignature(fields []Field) string {
	var parts []string
	for _, f := range fields {
		if (f.Indexed && f.Type.IsScalar()) || f.Type == FieldRelationship {
			parts = append(parts, f.Key+":"+string(f.Type)+":"+strconv.FormatBool(f.Indexed)+":"+f.TargetType)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "|")
}
