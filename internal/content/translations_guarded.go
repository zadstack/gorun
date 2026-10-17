package content

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/zadstack/gorun/internal/db"
)

// LinkTranslationGuarded compares the entire current union while holding the
// write transaction. Unlike the legacy method, even an already-linked pair must
// satisfy the caller's expected membership before it can succeed.
func (s *service) LinkTranslationGuarded(ctx context.Context, id, other string, expectedIDs []string) error {
	expected := make(map[string]bool, len(expectedIDs))
	if len(expectedIDs) < 2 || len(expectedIDs) > 256 {
		return ErrConflict
	}
	for _, mid := range expectedIDs {
		if mid == "" || expected[mid] {
			return ErrConflict
		}
		expected[mid] = true
	}
	if !expected[id] || !expected[other] {
		return ErrConflict
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		a, err := s.getItem(ctx, tx, id)
		if err != nil {
			return err
		}
		b, err := s.getItem(ctx, tx, other)
		if err != nil {
			return err
		}
		for _, it := range []*Item{a, b} {
			if err := checkOwner(ctx, it); err != nil {
				return err
			}
		}
		switch {
		case a.ID == b.ID:
			return invalid(FieldErrors{"translation": msgTranslationSelf})
		case a.Type != b.Type:
			return invalid(FieldErrors{"type": msgTranslationType})
		case a.Status == "trash" || b.Status == "trash":
			return invalid(FieldErrors{"translation": msgTranslationTrash})
		}
		if err := s.guardMutationTx(ctx, tx, true, a, b); err != nil {
			return err
		}
		members := map[string]*Item{}
		for _, it := range []*Item{a, b} {
			group, err := translationMembersTx(ctx, tx, it)
			if err != nil {
				return err
			}
			for _, member := range group {
				members[member.ID] = member
			}
		}
		if len(members) != len(expected) {
			return ErrConflict
		}
		locales := map[string]bool{}
		for mid, member := range members {
			if !expected[mid] {
				return ErrConflict
			}
			if err := checkOwner(ctx, member); err != nil {
				return err
			}
			if member.Type != a.Type {
				return invalid(FieldErrors{"type": msgTranslationType})
			}
			if member.Status == "trash" {
				return invalid(FieldErrors{"translation": msgTranslationTrash})
			}
			if locales[member.Locale] {
				return invalid(FieldErrors{"locale": msgTranslationTaken})
			}
			locales[member.Locale] = true
		}
		if a.TranslationGroup != "" && a.TranslationGroup == b.TranslationGroup {
			return nil
		}
		group := groupID(a)
		args := []any{group}
		for mid := range members {
			args = append(args, mid)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE content SET translation_group=? WHERE id IN (`+placeholders(len(members))+`)`, args...); err != nil {
			return fmt.Errorf("content: guarded translation link: %w", err)
		}
		s.audit.RecordTx(ctx, tx, "content.link_translation", "content", a.ID, map[string]any{"type": a.Type, "with": b.ID, "group": group})
		return nil
	})
}

func translationMembersTx(ctx context.Context, tx *sql.Tx, it *Item) ([]*Item, error) {
	if it.TranslationGroup == "" {
		return []*Item{it}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE c.translation_group=?`, it.TranslationGroup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*Item
	for rows.Next() {
		member, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, member)
	}
	return items, rows.Err()
}
