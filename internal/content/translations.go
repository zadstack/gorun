package content

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
)

// Validation keys of the translation operations.
const (
	msgTranslationTaken  = "content.validation.translation_taken"
	msgTranslationLocale = "content.validation.translation_locale"
	msgTranslationType   = "content.validation.translation_type"
	msgTranslationSelf   = "content.validation.translation_self"
	msgTranslationTrash  = "content.validation.translation_trash"
)

// Translations returns the item and its translations (the members of its group), by locale.
func (s *service) Translations(ctx context.Context, id string) ([]Item, error) {
	it, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.TranslationGroup == "" {
		return []Item{*it}, nil
	}
	b := &queryBuilder{}
	b.add("c.translation_group = ?", it.TranslationGroup)
	restrictStaffRead(ctx, b)
	rows, err := s.db.QueryContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE `+strings.Join(b.where, " AND ")+` ORDER BY c.locale, c.id`, b.args...)
	if err != nil {
		return nil, fmt.Errorf("content: translations of %s: %w", id, err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		m, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// CreateTranslation copies the item into locale as a draft (same type, slug, title, body, fields
// and terms) and links the copy to the item's group.
func (s *service) CreateTranslation(ctx context.Context, id, locale string) (*Item, error) {
	src, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(ctx, src); err != nil {
		return nil, err
	}
	if src.Status == "trash" {
		return nil, invalid(FieldErrors{"translation": msgTranslationTrash})
	}
	if locale == src.Locale || !slices.Contains(i18n.Supported, locale) {
		return nil, invalid(FieldErrors{"locale": msgTranslationLocale})
	}
	in := Input{Type: src.Type, Slug: src.Slug, Title: src.Title, Excerpt: src.Excerpt, Body: src.Body, Fields: src.Fields,
		Status: "draft", Template: src.Template, FeaturedMediaID: src.FeaturedMediaID, Locale: locale, Terms: src.Terms,
		ParentID: s.translatedParent(ctx, src.ParentID, locale)}
	return s.save(ctx, nil, in, saveOpts{action: "content.translate", translationOf: src})
}

// LinkTranslation puts other (with its own translations) into the group of id. Both must be of
// the same type, and the merged group may hold each locale once.
func (s *service) LinkTranslation(ctx context.Context, id, other string) error {
	a, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	b, err := s.Get(ctx, other)
	if err != nil {
		return err
	}
	if err := checkOwner(ctx, a); err != nil {
		return err
	}
	if err := checkOwner(ctx, b); err != nil {
		return err
	}
	switch {
	case a.ID == b.ID:
		return invalid(FieldErrors{"translation": msgTranslationSelf})
	case a.Type != b.Type:
		return invalid(FieldErrors{"type": msgTranslationType})
	case a.Status == "trash" || b.Status == "trash":
		return invalid(FieldErrors{"translation": msgTranslationTrash})
	case a.TranslationGroup != "" && a.TranslationGroup == b.TranslationGroup:
		return nil
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.guardMutationTx(ctx, tx, true, a, b); err != nil {
			return err
		}
		left, err := groupMembers(ctx, tx, a)
		if err != nil {
			return err
		}
		right, err := groupMembers(ctx, tx, b)
		if err != nil {
			return err
		}
		for loc := range right {
			if _, taken := left[loc]; taken {
				return invalid(FieldErrors{"locale": msgTranslationTaken})
			}
		}
		group := groupID(a)
		ids := []any{group, a.ID}
		for _, mid := range right {
			ids = append(ids, mid)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE content SET translation_group = ? WHERE id IN (`+placeholders(len(ids)-1)+`)`, ids...); err != nil {
			return fmt.Errorf("content: link %s and %s: %w", a.ID, b.ID, err)
		}
		s.audit.RecordTx(ctx, tx, "content.link_translation", "content", a.ID, map[string]any{"type": a.Type, "with": b.ID, "group": group})
		return nil
	})
}

// UnlinkTranslation takes the item out of its group; a group left with one item is dissolved.
func (s *service) UnlinkTranslation(ctx context.Context, id string) error {
	it, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := checkOwner(ctx, it); err != nil {
		return err
	}
	if it.TranslationGroup == "" {
		return nil
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.guardMutationTx(ctx, tx, true, it); err != nil {
			return err
		}
		if err := leaveGroupTx(ctx, tx, it.ID, it.TranslationGroup); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "content.unlink_translation", "content", it.ID, map[string]any{"type": it.Type, "group": it.TranslationGroup})
		return nil
	})
}

// translatedParent is the translation in locale of the parent page parentID, or "" (top level).
func (s *service) translatedParent(ctx context.Context, parentID, locale string) string {
	if parentID == "" {
		return ""
	}
	members, err := s.Translations(ctx, parentID)
	if err != nil {
		return ""
	}
	for _, m := range members {
		if m.Locale == locale && m.Status != "trash" {
			return m.ID
		}
	}
	return ""
}

// groupID is the group an item's translations join: its own, or a new one named after it.
func groupID(it *Item) string {
	if it.TranslationGroup != "" {
		return it.TranslationGroup
	}
	return it.ID
}

// groupMembers maps locale → id over the item's group (the item alone when ungrouped).
func groupMembers(ctx context.Context, tx *sql.Tx, it *Item) (map[string]string, error) {
	if it.TranslationGroup == "" {
		return map[string]string{it.Locale: it.ID}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT locale, id FROM content WHERE translation_group = ?`, it.TranslationGroup)
	if err != nil {
		return nil, fmt.Errorf("content: group of %s: %w", it.ID, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var loc, id string
		if err := rows.Scan(&loc, &id); err != nil {
			return nil, err
		}
		out[loc] = id
	}
	return out, rows.Err()
}

// localeTakenTx reports whether another member of group already has locale.
func localeTakenTx(ctx context.Context, tx *sql.Tx, group, locale, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM content WHERE translation_group = ? AND locale = ? AND id != ?`,
		group, locale, id).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("content: translation locale check: %w", err)
	}
	return n > 0, nil
}

// leaveGroupTx takes id out of group and dissolves the group when one item is left.
func leaveGroupTx(ctx context.Context, tx *sql.Tx, id, group string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE content SET translation_group = '' WHERE id = ?`, id); err != nil {
		return fmt.Errorf("content: unlink %s: %w", id, err)
	}
	return pruneGroupTx(ctx, tx, group)
}

// pruneGroupTx dissolves group when it has fewer than two items.
func pruneGroupTx(ctx context.Context, tx *sql.Tx, group string) error {
	if group == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE content SET translation_group = '' WHERE translation_group = ?
		AND (SELECT count(*) FROM content WHERE translation_group = ?) < 2`, group, group); err != nil {
		return fmt.Errorf("content: prune group %s: %w", group, err)
	}
	return nil
}

// checkTranslationTx runs before the row is written: a trashed item leaves its group, and the
// item's locale may not collide inside its group (or, for a new translation, its source's group).
func checkTranslationTx(ctx context.Context, tx *sql.Tx, existing, it, source *Item) error {
	group := ""
	switch {
	case source != nil:
		group = groupID(source)
	case existing == nil || existing.TranslationGroup == "":
		return nil
	case it.Status == "trash":
		return leaveGroupTx(ctx, tx, it.ID, existing.TranslationGroup)
	default:
		group = existing.TranslationGroup
	}
	taken, err := localeTakenTx(ctx, tx, group, it.Locale, it.ID)
	if err != nil {
		return err
	}
	if taken {
		return invalid(FieldErrors{"locale": msgTranslationTaken})
	}
	if source == nil {
		it.TranslationGroup = group
	}
	return nil
}

// linkTranslationTx runs after a new translation is written: it and its source share a group.
func linkTranslationTx(ctx context.Context, tx *sql.Tx, it, source *Item) error {
	if source == nil {
		return nil
	}
	group := groupID(source)
	if _, err := tx.ExecContext(ctx, `UPDATE content SET translation_group = ? WHERE id IN (?, ?)`, group, source.ID, it.ID); err != nil {
		return fmt.Errorf("content: link translation %s: %w", it.ID, err)
	}
	it.TranslationGroup = group
	return nil
}
