package taxonomy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/settings"
)

// Validation keys of the term translation operations.
const (
	msgTermLocale         = "taxonomy.validation.locale"
	msgTermTranslated     = "taxonomy.validation.translation_taken"
	msgTermTranslationTax = "taxonomy.validation.translation_taxonomy"
	msgTermTranslationOwn = "taxonomy.validation.translation_self"
)

// defaultLocale is the site's default content language, given to terms saved without one.
func (s *service) defaultLocale(ctx context.Context) string {
	return i18n.ContentLocale(ctx, s.settings)
}

// canonicalOr is the canonical form of a locale tag, or the tag as given when it does not parse.
func canonicalOr(locale string) string {
	if c, err := i18n.CanonicalLocale(locale); err == nil {
		return c
	}
	return locale
}

// backfillLocales gives terms stored before term translations (locale ”) the site's default
// content language. It runs at boot and touches nothing once every term has a locale.
func (s *service) backfillLocales(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE terms SET locale = ? WHERE locale = ''`, s.defaultLocale(ctx)); err != nil {
		return fmt.Errorf("taxonomy: backfill term locales: %w", err)
	}
	return nil
}

// checkTermLocaleTx validates the locale of term id before it is written: a new or changed locale
// must be a content language, and no other member of group may have it.
func checkTermLocaleTx(ctx context.Context, tx *sql.Tx, locale, group, id string) error {
	var current string
	err := tx.QueryRowContext(ctx, `SELECT locale FROM terms WHERE id = ?`, id).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("taxonomy: term locale: %w", err)
	}
	if current != locale {
		locales, err := settings.ContentLocalesTx(ctx, tx)
		if err != nil {
			return err
		}
		if !slices.Contains(locales, locale) {
			return validationError(map[string]string{"locale": msgTermLocale})
		}
	}
	if group == "" {
		return nil
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM terms WHERE translation_group = ? AND locale = ? AND id != ?`,
		group, locale, id).Scan(&n); err != nil {
		return fmt.Errorf("taxonomy: translation locale check: %w", err)
	}
	if n > 0 {
		return validationError(map[string]string{"locale": msgTermTranslated})
	}
	return nil
}

// pruneTermGroupTx dissolves group when fewer than two terms are left in it.
func pruneTermGroupTx(ctx context.Context, tx *sql.Tx, group string) error {
	if group == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE terms SET translation_group = '' WHERE translation_group = ?
		AND (SELECT count(*) FROM terms WHERE translation_group = ?) < 2`, group, group); err != nil {
		return fmt.Errorf("taxonomy: prune term group %s: %w", group, err)
	}
	return nil
}

// termGroupID is the group a term's translations join: its own, or a new one named after it.
func termGroupID(t *Term) string {
	if t.TranslationGroup != "" {
		return t.TranslationGroup
	}
	return t.ID
}

// TermTranslations returns the term and its translations, ordered by locale.
func (s *service) TermTranslations(ctx context.Context, id string) ([]Term, error) {
	t, err := s.GetTerm(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.TranslationGroup == "" {
		return []Term{*t}, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+termColumns+` FROM terms t WHERE t.translation_group = ? ORDER BY t.locale, t.id`, t.TranslationGroup)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: translations of %s: %w", id, err)
	}
	defer rows.Close()
	out := []Term{}
	for rows.Next() {
		m, err := scanTerm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// translationIn returns the member of t's group in locale, or nil.
func (s *service) translationIn(ctx context.Context, t *Term, locale string) *Term {
	if t == nil || t.TranslationGroup == "" {
		return nil
	}
	members, err := s.TermTranslations(ctx, t.ID)
	if err != nil {
		return nil
	}
	for i := range members {
		if members[i].Locale == locale {
			return &members[i]
		}
	}
	return nil
}

// CreateTermTranslation creates t in t.Locale as a translation of the term id: same taxonomy,
// the source's description and meta when t has none, and the source parent's translation in
// that locale as parent (top level when it has none).
func (s *service) CreateTermTranslation(ctx context.Context, id string, t Term) (*Term, error) {
	src, err := s.GetTerm(ctx, id)
	if err != nil {
		return nil, err
	}
	locale, err := i18n.CanonicalLocale(t.Locale)
	switch {
	case err != nil:
		return nil, validationError(map[string]string{"locale": msgTermLocale})
	case locale == src.Locale:
		return nil, validationError(map[string]string{"locale": msgTermTranslated})
	}
	t.ID, t.Taxonomy, t.Locale, t.TranslationGroup = "", src.Taxonomy, locale, ""
	if strings.TrimSpace(t.Description) == "" {
		t.Description = src.Description
	}
	if t.Meta == nil {
		t.Meta = src.Meta
	}
	if t.ParentID == "" && src.ParentID != "" {
		parent, err := s.GetTerm(ctx, src.ParentID)
		if err == nil {
			if tr := s.translationIn(ctx, parent, locale); tr != nil {
				t.ParentID = tr.ID
			}
		}
	}
	return s.saveTermWith(ctx, t, termSaveOpts{group: termGroupID(src), source: src, action: "taxonomy.term_translate"})
}

// createTranslationIn is CreateTermTranslation for a source that must be in taxonomy (the REST
// and MCP create calls name the taxonomy and the source separately).
func (s *service) createTranslationIn(ctx context.Context, taxonomy, sourceID string, t Term) (*Term, error) {
	src, err := s.GetTerm(ctx, sourceID)
	if errors.Is(err, ErrNotFound) {
		return nil, validationError(map[string]string{"translation_of": "taxonomy.validation.unknown_term"})
	}
	if err != nil {
		return nil, err
	}
	if src.Taxonomy != taxonomy {
		return nil, validationError(map[string]string{"translation_of": msgTermTranslationTax})
	}
	return s.CreateTermTranslation(ctx, src.ID, t)
}

// LinkTermTranslation puts other (with its own translations) into the group of id. Both must be
// in the same taxonomy, and the merged group may hold each locale once.
func (s *service) LinkTermTranslation(ctx context.Context, id, other string) error {
	a, err := s.GetTerm(ctx, id)
	if err != nil {
		return err
	}
	b, err := s.GetTerm(ctx, other)
	if err != nil {
		return err
	}
	switch {
	case a.ID == b.ID:
		return validationError(map[string]string{"translation": msgTermTranslationOwn})
	case a.Taxonomy != b.Taxonomy:
		return validationError(map[string]string{"taxonomy": msgTermTranslationTax})
	case a.TranslationGroup != "" && a.TranslationGroup == b.TranslationGroup:
		return nil
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		left, err := termGroupMembersTx(ctx, tx, a)
		if err != nil {
			return err
		}
		right, err := termGroupMembersTx(ctx, tx, b)
		if err != nil {
			return err
		}
		for loc := range right {
			if _, taken := left[loc]; taken {
				return validationError(map[string]string{"locale": msgTermTranslated})
			}
		}
		group := termGroupID(a)
		ids := []any{group, a.ID}
		for _, mid := range right {
			ids = append(ids, mid)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE terms SET translation_group = ? WHERE id IN (`+placeholders(len(ids)-1)+`)`, ids...); err != nil {
			return fmt.Errorf("taxonomy: link %s and %s: %w", a.ID, b.ID, err)
		}
		if err := pruneTermGroupTx(ctx, tx, b.TranslationGroup); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "taxonomy.term_link_translation", "term", a.ID, map[string]any{"taxonomy": a.Taxonomy, "with": b.ID, "group": group})
		return nil
	})
}

// UnlinkTermTranslation takes the term out of its group; a group left with one term is dissolved.
func (s *service) UnlinkTermTranslation(ctx context.Context, id string) error {
	t, err := s.GetTerm(ctx, id)
	if err != nil {
		return err
	}
	if t.TranslationGroup == "" {
		return nil
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE terms SET translation_group = '' WHERE id = ?`, id); err != nil {
			return fmt.Errorf("taxonomy: unlink %s: %w", id, err)
		}
		if err := pruneTermGroupTx(ctx, tx, t.TranslationGroup); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "taxonomy.term_unlink_translation", "term", id, map[string]any{"taxonomy": t.Taxonomy, "group": t.TranslationGroup})
		return nil
	})
}

// termGroupMembersTx maps locale → id over t's group (t alone when it has none).
func termGroupMembersTx(ctx context.Context, tx *sql.Tx, t *Term) (map[string]string, error) {
	if t.TranslationGroup == "" {
		return map[string]string{t.Locale: t.ID}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT locale, id FROM terms WHERE translation_group = ?`, t.TranslationGroup)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: group of %s: %w", t.ID, err)
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

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
