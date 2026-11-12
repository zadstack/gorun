package taxonomy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/i18n"
)

// --- terms ---

// countExpr computes a term's count live: published items assigned to it
// (a stored counter would drift on cascading hard deletes).
const countExpr = `(SELECT COUNT(*) FROM content_terms ct JOIN content c ON c.id = ct.content_id
	WHERE ct.term_id = t.id AND c.status = 'published')`

const termColumns = `t.id, t.taxonomy, t.slug, t.name, t.description, COALESCE(t.parent_id, ''), t.meta_json, ` + countExpr +
	`, t.locale, t.translation_group`

func scanTerm(sc interface{ Scan(...any) error }) (*Term, error) {
	var t Term
	var meta string
	if err := sc.Scan(&t.ID, &t.Taxonomy, &t.Slug, &t.Name, &t.Description, &t.ParentID, &meta, &t.Count, &t.Locale, &t.TranslationGroup); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(meta), &t.Meta); err != nil || t.Meta == nil {
		t.Meta = map[string]any{}
	}
	return &t, nil
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Terms lists a taxonomy's terms ordered by name, with the total count.
// ParentID "0" or "root" selects top-level terms.
func (s *service) Terms(ctx context.Context, taxonomy string, q TermQuery) ([]Term, int, error) {
	if _, err := s.Get(ctx, taxonomy); err != nil {
		return nil, 0, err
	}
	where, args := []string{"t.taxonomy = ?"}, []any{taxonomy}
	if q.Search != "" {
		pat := "%" + likeEscape(q.Search) + "%"
		where = append(where, `(t.name LIKE ? ESCAPE '\' OR t.slug LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat)
	}
	switch q.ParentID {
	case "":
	case "0", "root":
		where = append(where, "t.parent_id IS NULL")
	default:
		where = append(where, "t.parent_id = ?")
		args = append(args, q.ParentID)
	}
	if q.Locale != "" {
		where = append(where, "t.locale = ?")
		args = append(args, canonicalOr(q.Locale))
	}
	if q.TranslationGroup != "" {
		where = append(where, "t.translation_group = ?")
		args = append(args, q.TranslationGroup)
	}
	cond := " WHERE " + strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM terms t`+cond, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("taxonomy: count terms: %w", err)
	}
	page, per := max(q.Page, 1), q.PerPage
	if per <= 0 {
		per = defaultPerPage
	}
	per = min(per, maxPerPage)
	rows, err := s.db.QueryContext(ctx, `SELECT `+termColumns+` FROM terms t`+cond+` ORDER BY t.name, t.id LIMIT ? OFFSET ?`,
		append(args, per, (page-1)*per)...)
	if err != nil {
		return nil, 0, fmt.Errorf("taxonomy: list terms: %w", err)
	}
	defer rows.Close()
	out := []Term{}
	for rows.Next() {
		t, err := scanTerm(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

// GetTerm returns the term or ErrNotFound.
func (s *service) GetTerm(ctx context.Context, id string) (*Term, error) {
	t, err := scanTerm(s.db.QueryRowContext(ctx, `SELECT `+termColumns+` FROM terms t WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("taxonomy: get term: %w", err)
	}
	return t, nil
}

// GetTermBySlug returns the term or ErrNotFound.
func (s *service) GetTermBySlug(ctx context.Context, taxonomy, slug string) (*Term, error) {
	t, err := scanTerm(s.db.QueryRowContext(ctx, `SELECT `+termColumns+` FROM terms t WHERE t.taxonomy = ? AND t.slug = ?`, taxonomy, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("taxonomy: get term: %w", err)
	}
	return t, nil
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// uniqueTermSlug returns base or base-2, base-3, ... free in taxonomy.
func uniqueTermSlug(ctx context.Context, q execQuerier, taxonomy, base, excludeID string) (string, error) {
	for i := 1; i < 10000; i++ {
		cand := base
		if i > 1 {
			cand = fmt.Sprintf("%s-%d", base, i)
		}
		var one int
		err := q.QueryRowContext(ctx, `SELECT 1 FROM terms WHERE taxonomy = ? AND slug = ? AND id != ?`, taxonomy, cand, excludeID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return cand, nil
		}
		if err != nil {
			return "", fmt.Errorf("taxonomy: slug check: %w", err)
		}
	}
	return "", fmt.Errorf("taxonomy: no free slug for %q", base)
}

// SaveTerm creates (ID empty) or updates a term. The slug comes from Slug
// or Name (Arabic kept), made unique per taxonomy; ParentID is allowed only
// in hierarchical taxonomies, must be in the same taxonomy and must not
// create a cycle. Locale defaults to the term's current locale, then to the
// site language, and must be a content language not already taken in the
// term's translation group; TranslationGroup is never changed here. Fires
// taxonomy.term_saved (sync, *Term).
func (s *service) SaveTerm(ctx context.Context, t Term) (*Term, error) {
	return s.saveTermWith(ctx, t, termSaveOpts{action: "taxonomy.term_save"})
}

// termSaveOpts: a new term joins group (as a translation of source, whose group is created in
// the same transaction when it has none yet).
type termSaveOpts struct {
	group, action string
	source        *Term
}

func (s *service) saveTermWith(ctx context.Context, t Term, opts termSaveOpts) (*Term, error) {
	group, action := opts.group, opts.action
	tax, err := s.Get(ctx, t.Taxonomy)
	if errors.Is(err, ErrNotFound) {
		return nil, validationError(map[string]string{"taxonomy": "taxonomy.validation.unknown"})
	}
	if err != nil {
		return nil, err
	}
	t.Name = strings.TrimSpace(t.Name)
	errs := map[string]string{}
	if t.Name == "" {
		errs["name"] = "taxonomy.validation.required"
	}
	var existing *Term
	if t.ID != "" {
		if existing, err = s.GetTerm(ctx, t.ID); err != nil {
			return nil, err
		}
		if existing.Taxonomy != t.Taxonomy {
			errs["taxonomy"] = "taxonomy.validation.immutable"
		}
		group = existing.TranslationGroup
	}
	if t.ParentID != "" {
		if err := s.checkTermParent(ctx, tax, t); err != nil {
			errs["parent_id"] = err.Error()
		}
	}
	switch {
	case strings.TrimSpace(t.Locale) != "":
		canonical, err := i18n.CanonicalLocale(t.Locale)
		if err != nil {
			errs["locale"] = msgTermLocale
		}
		t.Locale = canonical
	case existing != nil && existing.Locale != "":
		t.Locale = existing.Locale
	default:
		t.Locale = s.defaultLocale(ctx)
	}
	if err := validationError(errs); err != nil {
		return nil, err
	}
	if t.Meta == nil {
		t.Meta = map[string]any{}
	}
	meta, _ := json.Marshal(t.Meta)
	base := content.Slugify(t.Slug, false)
	if base == "" {
		base = content.Slugify(t.Name, false)
	}
	if t.ID == "" {
		t.ID = db.NewID()
	}
	if base == "" {
		base = strings.ToLower(t.ID)
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if src := opts.source; src != nil && src.TranslationGroup == "" {
			if _, err := tx.ExecContext(ctx, `UPDATE terms SET translation_group = ? WHERE id = ?`, group, src.ID); err != nil {
				return fmt.Errorf("taxonomy: start translation group: %w", err)
			}
		}
		if err := checkTermLocaleTx(ctx, tx, t.Locale, group, t.ID); err != nil {
			return err
		}
		slug, err := uniqueTermSlug(ctx, tx, t.Taxonomy, base, t.ID)
		if err != nil {
			return err
		}
		t.Slug = slug
		if existing == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO terms (id, taxonomy, slug, name, description, parent_id, meta_json, locale, translation_group)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				t.ID, t.Taxonomy, t.Slug, t.Name, t.Description, nullable(t.ParentID), string(meta), t.Locale, group)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE terms SET slug = ?, name = ?, description = ?, parent_id = ?, meta_json = ?, locale = ? WHERE id = ?`,
				t.Slug, t.Name, t.Description, nullable(t.ParentID), string(meta), t.Locale, t.ID)
		}
		if err != nil {
			return fmt.Errorf("taxonomy: save term: %w", err)
		}
		details := map[string]any{"taxonomy": t.Taxonomy, "name": t.Name, "locale": t.Locale}
		if group != "" {
			details["group"] = group
		}
		s.audit.RecordTx(ctx, tx, action, "term", t.ID, details)
		return nil
	})
	if err != nil {
		return nil, err
	}
	saved, err := s.GetTerm(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if s.hooks != nil {
		if err := s.hooks.Do(ctx, "taxonomy.term_saved", saved); err != nil {
			return saved, fmt.Errorf("taxonomy: term_saved hook: %w", err)
		}
	}
	return saved, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// checkTermParent returns an error (i18n key) when t's parent is invalid.
func (s *service) checkTermParent(ctx context.Context, tax *Taxonomy, t Term) error {
	if !tax.Hierarchical || t.ParentID == t.ID {
		return errors.New("taxonomy.validation.parent")
	}
	cur := t.ParentID
	for depth := 0; cur != "" && depth < 100; depth++ {
		var taxSlug, next string
		err := s.db.QueryRowContext(ctx, `SELECT taxonomy, COALESCE(parent_id, '') FROM terms WHERE id = ?`, cur).Scan(&taxSlug, &next)
		if err != nil || taxSlug != t.Taxonomy || (t.ID != "" && next == t.ID) {
			return errors.New("taxonomy.validation.parent")
		}
		cur = next
	}
	return nil
}

// DeleteTerm removes a term; its children move up to its parent and its
// content assignments are dropped.
func (s *service) DeleteTerm(ctx context.Context, id string) error {
	t, err := s.GetTerm(ctx, id)
	if err != nil {
		return err
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE terms SET parent_id = ? WHERE parent_id = ?`, nullable(t.ParentID), id); err != nil {
			return fmt.Errorf("taxonomy: reparent: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM terms WHERE id = ?`, id); err != nil {
			return fmt.Errorf("taxonomy: delete term: %w", err)
		}
		if err := pruneTermGroupTx(ctx, tx, t.TranslationGroup); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "taxonomy.term_delete", "term", id, map[string]any{"taxonomy": t.Taxonomy, "name": t.Name})
		return nil
	})
}

// --- content assignments ---

// SetContentTerms replaces every term assignment of contentID.
func (s *service) SetContentTerms(ctx context.Context, contentID string, terms map[string][]string) error {
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error { return s.SetContentTermsTx(ctx, tx, contentID, terms) })
}

// SetContentTermsTx is SetContentTerms inside tx (content's save
// transaction). Values are term slugs; in flat taxonomies an unknown value
// (a typed tag) creates the term, in hierarchical ones it is a validation
// error keyed "terms.<taxonomy>".
func (s *service) SetContentTermsTx(ctx context.Context, tx *sql.Tx, contentID string, terms map[string][]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM content_terms WHERE content_id = ?`, contentID); err != nil {
		return fmt.Errorf("taxonomy: clear content terms: %w", err)
	}
	taxes := make([]string, 0, len(terms))
	for tax := range terms {
		taxes = append(taxes, tax)
	}
	slices.Sort(taxes)
	for _, taxSlug := range taxes {
		var hierarchical bool
		err := tx.QueryRowContext(ctx, `SELECT hierarchical FROM taxonomies WHERE slug = ?`, taxSlug).Scan(&hierarchical)
		if errors.Is(err, sql.ErrNoRows) {
			return validationError(map[string]string{"terms." + taxSlug: "taxonomy.validation.unknown"})
		}
		if err != nil {
			return fmt.Errorf("taxonomy: load %s: %w", taxSlug, err)
		}
		for _, value := range terms[taxSlug] {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			id, err := s.resolveTerm(ctx, tx, contentID, taxSlug, value, !hierarchical)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO content_terms (content_id, term_id) VALUES (?, ?)`, contentID, id); err != nil {
				return fmt.Errorf("taxonomy: assign term: %w", err)
			}
		}
	}
	return nil
}

// resolveTerm finds a term by slug (as given, then slugified), creating it
// when create is set, in the locale of the content item.
func (s *service) resolveTerm(ctx context.Context, tx *sql.Tx, contentID, taxSlug, value string, create bool) (string, error) {
	slug := content.Slugify(value, false)
	for _, cand := range []string{value, slug} {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM terms WHERE taxonomy = ? AND slug = ?`, taxSlug, cand).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("taxonomy: resolve term: %w", err)
		}
	}
	if !create || slug == "" {
		return "", validationError(map[string]string{"terms." + taxSlug: "taxonomy.validation.unknown_term"})
	}
	var locale string
	if err := tx.QueryRowContext(ctx, `SELECT locale FROM content WHERE id = ?`, contentID).Scan(&locale); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("taxonomy: item locale: %w", err)
	}
	if locale == "" {
		locale = s.defaultLocale(ctx)
	}
	id := db.NewID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO terms (id, taxonomy, slug, name, locale) VALUES (?, ?, ?, ?, ?)`, id, taxSlug, slug, value, locale); err != nil {
		return "", fmt.Errorf("taxonomy: create term: %w", err)
	}
	s.audit.RecordTx(ctx, tx, "taxonomy.term_save", "term", id, map[string]any{"taxonomy": taxSlug, "name": value, "locale": locale, "auto": true})
	return id, nil
}

// ContentTerms returns contentID's terms as taxonomy → slugs (by name).
func (s *service) ContentTerms(ctx context.Context, contentID string) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.taxonomy, t.slug FROM content_terms ct JOIN terms t ON t.id = ct.term_id
        WHERE ct.content_id = ? ORDER BY t.taxonomy, t.name`, contentID)
	return readContentTerms(rows, err)
}

// ContentTermsTx reads assignments under the content save's write lock.
func (s *service) ContentTermsTx(ctx context.Context, tx *sql.Tx, contentID string) (map[string][]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.taxonomy, t.slug FROM content_terms ct JOIN terms t ON t.id = ct.term_id
        WHERE ct.content_id = ? ORDER BY t.taxonomy, t.name`, contentID)
	return readContentTerms(rows, err)
}
func readContentTerms(rows *sql.Rows, err error) (map[string][]string, error) {
	if err != nil {
		return nil, fmt.Errorf("taxonomy: content terms: %w", err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var tax, slug string
		if err := rows.Scan(&tax, &slug); err != nil {
			return nil, err
		}
		out[tax] = append(out[tax], slug)
	}
	return out, rows.Err()
}

// ContentIDs returns the IDs of content assigned to any of termSlugs in
// taxonomy; in hierarchical taxonomies descendants of those terms match
// too.
func (s *service) ContentIDs(ctx context.Context, taxonomy string, termSlugs []string) ([]string, error) {
	if len(termSlugs) == 0 {
		return []string{}, nil
	}
	raw, _ := json.Marshal(termSlugs)
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE sel(id) AS (
			SELECT id FROM terms WHERE taxonomy = ? AND slug IN (SELECT value FROM json_each(?))
			UNION
			SELECT t.id FROM terms t JOIN sel ON t.parent_id = sel.id JOIN taxonomies x ON x.slug = t.taxonomy AND x.hierarchical = 1
		)
		SELECT DISTINCT content_id FROM content_terms WHERE term_id IN (SELECT id FROM sel) ORDER BY content_id`, taxonomy, string(raw))
	if err != nil {
		return nil, fmt.Errorf("taxonomy: content ids: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ContentTermsBatch uses bounded queries for public listings and sitemap builds.
func (s *service) ContentTermsBatch(ctx context.Context, ids []string) (map[string]map[string][]string, error) {
	assignments := make(map[string]map[string][]string, len(ids))
	for start := 0; start < len(ids); start += 100 {
		chunk := ids[start:min(start+100, len(ids))]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		rows, err := s.db.QueryContext(ctx, `SELECT ct.content_id,t.taxonomy,t.slug FROM content_terms ct JOIN terms t ON t.id=ct.term_id WHERE ct.content_id IN (`+placeholders+`) ORDER BY ct.content_id,t.taxonomy,t.name`, args...)
		if err != nil {
			return nil, err
		}
		err = scanTermAssignments(rows, assignments)
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return assignments, nil
}

func scanTermAssignments(rows *sql.Rows, assignments map[string]map[string][]string) error {
	for rows.Next() {
		var id, tax, slug string
		if err := rows.Scan(&id, &tax, &slug); err != nil {
			return err
		}
		if assignments[id] == nil {
			assignments[id] = map[string][]string{}
		}
		assignments[id][tax] = append(assignments[id][tax], slug)
	}
	return rows.Err()
}
