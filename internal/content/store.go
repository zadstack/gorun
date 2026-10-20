package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/i18n"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/settings"
)

// TermBackend is the slice of taxonomy.Service content needs. content boots
// before taxonomy and may not import it, so the backend is looked up lazily
// (kernel.ServiceTaxonomy) at call time; nil means "no taxonomy module".
type TermBackend interface {
	SetContentTerms(ctx context.Context, contentID string, terms map[string][]string) error
	ContentTerms(ctx context.Context, contentID string) (map[string][]string, error)
	ContentIDs(ctx context.Context, taxonomy string, termSlugs []string) ([]string, error)
}

// TxTermBackend is optionally implemented by the taxonomy service so content
// can write an item's terms inside its own save transaction.
type TxTermBackend interface {
	SetContentTermsTx(ctx context.Context, tx *sql.Tx, contentID string, terms map[string][]string) error
}

type service struct {
	db       *sql.DB
	hooks    *hooks.Registry
	audit    *audit.Writer
	settings *settings.Store
	log      *slog.Logger
	termsFn  func() TermBackend
	now      func() time.Time
}

var _ Service = (*service)(nil)

func newService(d *sql.DB, h *hooks.Registry, a *audit.Writer, st *settings.Store, log *slog.Logger) *service {
	return &service{db: d, hooks: h, audit: a, settings: st, log: log,
		termsFn: func() TermBackend { return nil }, now: time.Now}
}

func (s *service) nowString() string { return db.FormatTime(s.now()) }

func (s *service) terms() TermBackend {
	if s.termsFn == nil {
		return nil
	}
	return s.termsFn()
}

const itemColumns = `c.id, c.type, c.slug, c.title, c.excerpt, c.body, c.body_format, c.fields_json, c.author_id, c.status,
	COALESCE(c.parent_id, ''), c.menu_order, c.template, COALESCE(c.featured_media_id, ''), c.seo_json, c.locale,
	COALESCE(c.publish_at, ''), COALESCE(c.published_at, ''), c.created_at, c.updated_at, c.translation_group`

func scanItem(sc interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var fields, seo string
	if err := sc.Scan(&it.ID, &it.Type, &it.Slug, &it.Title, &it.Excerpt, &it.Body, &it.BodyFormat, &fields, &it.AuthorID,
		&it.Status, &it.ParentID, &it.MenuOrder, &it.Template, &it.FeaturedMediaID, &seo, &it.Locale, &it.PublishAt,
		&it.PublishedAt, &it.CreatedAt, &it.UpdatedAt, &it.TranslationGroup); err != nil {
		return nil, err
	}
	// Stored fields are returned as-is, never re-validated: a schema edit
	// must not make existing content unreadable (Review Focus 4).
	if err := json.Unmarshal([]byte(fields), &it.Fields); err != nil || it.Fields == nil {
		it.Fields = map[string]any{}
	}
	if err := json.Unmarshal([]byte(seo), &it.SEO); err != nil || it.SEO == nil {
		it.SEO = map[string]any{}
	}
	it.Terms = map[string][]string{}
	return &it, nil
}

// nullable stores "" as SQL NULL (optional FK and timestamp columns).
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *service) getItem(ctx context.Context, q sqlRunner, id string) (*Item, error) {
	it, err := scanItem(q.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM content c WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content: get %s: %w", id, err)
	}
	return it, nil
}

type sqlRunner interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Get returns the item (any status, trash included) with its terms.
func (s *service) Get(ctx context.Context, id string) (*Item, error) {
	it, err := s.getItem(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if err := s.checkRead(ctx, it); err != nil {
		return nil, err
	}
	s.fillTerms(ctx, it)
	return it, nil
}

// GetBySlug returns the non-trashed item of typ with slug in locale.
func (s *service) GetBySlug(ctx context.Context, typ, slug, locale string) (*Item, error) {
	it, err := scanItem(s.db.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM content c
		WHERE c.type = ? AND c.slug = ? AND c.locale = ? AND c.status != 'trash'`, typ, slug, locale))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content: get by slug: %w", err)
	}
	if err := s.checkRead(ctx, it); err != nil {
		return nil, err
	}
	s.fillTerms(ctx, it)
	return it, nil
}

func (s *service) fillTerms(ctx context.Context, it *Item) {
	tb := s.terms()
	if tb == nil {
		return
	}
	terms, err := tb.ContentTerms(ctx, it.ID)
	if err != nil {
		s.log.Error("content: load terms", "id", it.ID, "err", err)
		return
	}
	if terms != nil {
		it.Terms = terms
	}
}

// ownOnly reports whether u may only touch their own items (authors and
// contributors).
func ownOnly(u *auth.User) bool { return u != nil && (u.Role == "author" || u.Role == "contributor") }

// checkOwner enforces "own items only" for authors and contributors. A
// context without a user (system, cron, CLI) passes.
func checkOwner(ctx context.Context, it *Item) error {
	if u := auth.UserFromContext(ctx); ownOnly(u) && it.AuthorID != u.ID {
		return ErrForbidden
	}
	return nil
}

// defaultLocale picks the locale for a new item when Input.Locale is empty.
// Prefer the request/admin content locale (editor chrome) when one is on the
// context; otherwise fall back to site.locale. This keeps bilingual hosters
// from storing EN-authored drafts as ar merely because the public site default
// is Arabic.
func (s *service) defaultLocale(ctx context.Context) string {
	if loc, ok := i18n.RequestLocale(ctx); ok && slices.Contains(i18n.Supported, loc) {
		return loc
	}
	if s.settings == nil {
		return i18n.DefaultLocale
	}
	return s.settings.GetString(ctx, "site.locale", i18n.DefaultLocale)
}

func (s *service) revisionsKeep(ctx context.Context) int {
	n := 25
	if s.settings != nil {
		var v int
		if s.settings.Get(ctx, "content.revisions_keep", &v) == nil && v > 0 {
			n = v
		}
	}
	return n
}

// Create validates in and inserts a new item (see save).
func (s *service) Create(ctx context.Context, in Input) (*Item, error) {
	return s.save(ctx, nil, in, saveOpts{})
}

// Update replaces the writable state of item id with in (full replace).
func (s *service) Update(ctx context.Context, id string, in Input) (*Item, error) {
	existing, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkOwner(ctx, existing); err != nil {
		return nil, err
	}
	return s.save(ctx, existing, in, saveOpts{})
}

// saveOpts tunes save for callers like Restore.
type saveOpts struct {
	prev       []map[string]any       // extra stored field maps whose stale relationship ids may be dropped
	inTx       func(tx *sql.Tx) error // runs first inside the save transaction
	action     string                 // audit action (default content.create / content.update)
	afterWrite func(*sql.Tx, *Item) error
	// translationOf is the source of a new translation: the new item joins its group.
	translationOf *Item
}

// save runs the content.before_save filter, validates and normalizes the
// input, then in one transaction writes the row, index, relations, a
// revision, the audit entry and (when the taxonomy backend supports it) the
// terms. After commit it fires content.after_save (sync) and emits
// content.published when the item went live.
func (s *service) save(ctx context.Context, existing *Item, in Input, opts saveOpts) (*Item, error) {
	if s.hooks != nil {
		v, err := s.hooks.Apply(ctx, "content.before_save", &in)
		if err != nil {
			return nil, err
		}
		if p, ok := v.(*Input); ok && p != nil {
			in = *p
		}
	}
	it, typ, err := s.prepare(ctx, existing, in, opts.prev)
	if err != nil {
		return nil, err
	}
	keep := s.revisionsKeep(ctx)
	ascii := s.transliterate(ctx)
	tb := s.terms()
	txTerms, _ := tb.(TxTermBackend)
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if guardedMutation(ctx) && isLive(it.Status) {
			return ErrConflict
		}
		if err := s.guardMutationTx(ctx, tx, false, existing); err != nil {
			return err
		}
		if opts.inTx != nil {
			if err := opts.inTx(tx); err != nil {
				return err
			}
		}
		base := Slugify(in.Slug, ascii)
		if base == "" {
			base = Slugify(it.Title, ascii)
		}
		if base == "" {
			base = strings.ToLower(it.ID)
		}
		if it.Type == "page" && it.ParentID == "" && slices.Contains(i18n.Supported, base) {
			base += "-page" // a top-level page at /en would sit where the English site is
		}
		slug, err := uniqueSlug(ctx, tx, it.Type, it.Locale, base, it.ID)
		if err != nil {
			return err
		}
		it.Slug = slug
		if err := checkTranslationTx(ctx, tx, existing, it, opts.translationOf); err != nil {
			return err
		}
		if s.hooks != nil {
			if _, err := s.hooks.Apply(ctx, "content.authorize_save", &SaveAuthorization{Tx: tx, Item: it}); err != nil {
				return err
			}
		}
		if err := s.writeRow(ctx, tx, it, existing == nil); err != nil {
			return err
		}
		if err := linkTranslationTx(ctx, tx, it, opts.translationOf); err != nil {
			return err
		}
		if err := writeIndex(ctx, tx, typ, it); err != nil {
			return err
		}
		if err := indexSearchTx(ctx, tx, it); err != nil {
			return err
		}
		if err := writeRelations(ctx, tx, typ, it); err != nil {
			return err
		}
		if err := s.addRevision(ctx, tx, it, keep); err != nil {
			return err
		}
		if txTerms != nil {
			if err := txTerms.SetContentTermsTx(ctx, tx, it.ID, it.Terms); err != nil {
				return err
			}
		}
		if opts.afterWrite != nil {
			if err := opts.afterWrite(tx, it); err != nil {
				return err
			}
		}
		action := opts.action
		switch {
		case action != "":
		case existing == nil:
			action = "content.create"
		default:
			action = "content.update"
		}
		s.audit.RecordTx(ctx, tx, action, "content", it.ID, map[string]any{"type": it.Type, "status": it.Status, "title": it.Title})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if tb != nil && txTerms == nil {
		if err := tb.SetContentTerms(ctx, it.ID, it.Terms); err != nil {
			return nil, fmt.Errorf("content: saved %s but setting terms failed: %w", it.ID, err)
		}
	}
	s.fillTerms(ctx, it)
	s.afterSave(ctx, existing, it)
	return it, nil
}

func (s *service) afterSave(ctx context.Context, existing, it *Item) {
	if s.hooks == nil {
		return
	}
	if err := s.hooks.Do(ctx, "content.after_save", it); err != nil {
		s.log.Error("content: after_save hook", "id", it.ID, "err", err)
	}
	if it.Status == "published" && (existing == nil || existing.Status != "published") {
		s.hooks.Emit(ctx, "content.published", it)
	}
}

func (s *service) writeRow(ctx context.Context, tx *sql.Tx, it *Item, isNew bool) error {
	fields, _ := json.Marshal(it.Fields)
	seo, _ := json.Marshal(it.SEO)
	var deletedAt any
	if it.Status == "trash" {
		deletedAt = it.UpdatedAt
	}
	var err error
	if isNew {
		_, err = tx.ExecContext(ctx, `INSERT INTO content (id, type, slug, title, excerpt, body, body_format, fields_json,
			author_id, status, parent_id, menu_order, template, featured_media_id, seo_json, locale, publish_at, published_at,
			created_at, updated_at, deleted_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			it.ID, it.Type, it.Slug, it.Title, it.Excerpt, it.Body, it.BodyFormat, string(fields), it.AuthorID, it.Status,
			nullable(it.ParentID), it.MenuOrder, it.Template, nullable(it.FeaturedMediaID), string(seo), it.Locale,
			nullable(it.PublishAt), nullable(it.PublishedAt), it.CreatedAt, it.UpdatedAt, deletedAt)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE content SET slug=?, title=?, excerpt=?, body=?, body_format=?, fields_json=?,
			author_id=?, status=?, parent_id=?, menu_order=?, template=?, featured_media_id=?, seo_json=?, locale=?,
			publish_at=?, published_at=?, updated_at=?,
			deleted_at=CASE WHEN ? IS NULL THEN NULL ELSE COALESCE(deleted_at, ?) END WHERE id=?`,
			it.Slug, it.Title, it.Excerpt, it.Body, it.BodyFormat, string(fields), it.AuthorID, it.Status,
			nullable(it.ParentID), it.MenuOrder, it.Template, nullable(it.FeaturedMediaID), string(seo), it.Locale,
			nullable(it.PublishAt), nullable(it.PublishedAt), it.UpdatedAt, deletedAt, deletedAt, it.ID)
	}
	if err != nil {
		return fmt.Errorf("content: write %s: %w", it.ID, err)
	}
	return nil
}

// prepare validates in against the type and builds the item to store.
// existing is nil on create.
func (s *service) prepare(ctx context.Context, existing *Item, in Input, extraPrev []map[string]any) (*Item, *Type, error) {
	errs := FieldErrors{}
	if existing != nil {
		if in.Type == "" {
			in.Type = existing.Type
		}
		if in.Type != existing.Type {
			return nil, nil, invalid(FieldErrors{"type": "content.validation.type_immutable"})
		}
	}
	typ, err := s.GetType(ctx, in.Type)
	if errors.Is(err, ErrNotFound) {
		return nil, nil, invalid(FieldErrors{"type": "content.validation.unknown_type"})
	}
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	it := &Item{Type: typ.Slug, Title: strings.TrimSpace(in.Title), Excerpt: strings.TrimSpace(in.Excerpt),
		Body: Sanitize(in.Body), BodyFormat: "html", AuthorID: in.AuthorID, Status: in.Status, ParentID: in.ParentID,
		Template: in.Template, FeaturedMediaID: in.FeaturedMediaID, SEO: in.SEO, Locale: in.Locale,
		Terms: in.Terms, UpdatedAt: db.FormatTime(now)}
	if in.MenuOrder != nil {
		it.MenuOrder = *in.MenuOrder
	}
	if it.SEO == nil {
		it.SEO = map[string]any{}
	}
	if it.Terms == nil {
		it.Terms = map[string][]string{}
	}
	if existing == nil {
		it.ID, it.CreatedAt = db.NewID(), it.UpdatedAt
	} else {
		it.ID, it.CreatedAt, it.PublishedAt = existing.ID, existing.CreatedAt, existing.PublishedAt
	}
	if it.Locale == "" {
		if existing != nil {
			it.Locale = existing.Locale
		} else {
			it.Locale = s.defaultLocale(ctx)
		}
	}
	if !slices.Contains(i18n.Supported, it.Locale) {
		errs["locale"] = "content.validation.locale"
		return nil, nil, invalid(errs)
	}
	s.applyAuthor(ctx, existing, it)

	var prev []map[string]any
	if existing != nil {
		prev = append(prev, existing.Fields)
	}
	fields, ferrs := s.validate(typ, in.Fields, append(prev, extraPrev...))
	maps.Copy(errs, ferrs)
	if existing != nil {
		// Keep values of fields no longer in the schema, so re-adding the
		// field brings the data back (Review Focus 4).
		for k, v := range existing.Fields {
			if !slices.ContainsFunc(typ.Fields, func(f Field) bool { return f.Key == k }) {
				fields[k] = v
			}
		}
	}
	it.Fields = fields

	if err := s.applyStatus(ctx, existing, it, in.PublishAt, now, errs); err != nil {
		return nil, nil, err
	}
	if err := s.checkParent(ctx, typ, it, errs); err != nil {
		return nil, nil, err
	}
	it.Terms = maps.Clone(it.Terms) // never mutate the caller's map
	for tax, vals := range it.Terms {
		if slices.Contains(typ.Taxonomies, tax) {
			continue
		}
		// A taxonomy detached from the type after items were tagged: the
		// stale terms (unchanged, or cleared) are dropped so the item stays
		// saveable; newly supplying terms for it is an error.
		if len(vals) == 0 || (existing != nil && slices.Equal(vals, existing.Terms[tax])) {
			delete(it.Terms, tax)
			continue
		}
		errs["terms."+tax] = "content.validation.taxonomy"
	}
	if err := invalid(errs); err != nil {
		return nil, nil, err
	}
	return it, typ, nil
}

func (s *service) applyAuthor(ctx context.Context, existing, it *Item) {
	u := auth.UserFromContext(ctx)
	switch {
	case ownOnly(u) && existing != nil:
		it.AuthorID = existing.AuthorID
	case ownOnly(u):
		it.AuthorID = u.ID
	case it.AuthorID == "" && existing != nil:
		it.AuthorID = existing.AuthorID
	case it.AuthorID == "" && u != nil:
		it.AuthorID = u.ID
	}
}

func (s *service) checkParent(ctx context.Context, typ *Type, it *Item, errs FieldErrors) error {
	if it.ParentID == "" {
		return nil
	}
	if !typ.Hierarchical || it.ParentID == it.ID {
		errs["parent_id"] = "content.validation.parent"
		return nil
	}
	// Walk up from the parent: it must exist, share the type, and never
	// reach this item (no cycles).
	cur := it.ParentID
	for depth := 0; cur != "" && depth < 100; depth++ {
		var typSlug, next string
		err := s.db.QueryRowContext(ctx, `SELECT type, COALESCE(parent_id, '') FROM content WHERE id = ?`, cur).Scan(&typSlug, &next)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (typSlug != typ.Slug || next == it.ID)) {
			errs["parent_id"] = "content.validation.parent"
			return nil
		}
		if err != nil {
			return fmt.Errorf("content: check parent: %w", err)
		}
		cur = next
	}
	return nil
}
