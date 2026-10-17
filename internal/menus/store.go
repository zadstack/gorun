package menus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// Hook names.
const (
	// HookSaved is emitted (async) after a menu or its items change; the
	// payload is SavedEvent.
	HookSaved = "menus.saved"
	// FilterResolveURL runs for every item (all kinds) when a menu is read
	// for display; the payload is *Item with URL (and Label) already
	// set to the fallback. Filters may change URL/Label and must return the
	// *Item.
	FilterResolveURL = "menus.resolve_url"
)

// Item kinds.
const (
	KindCustom  = "custom"
	KindContent = "content"
	KindTerm    = "term"
	KindArchive = "archive"
)

const (
	maxItems = 500
	maxDepth = 6
	maxLabel = 200
	maxURL   = 2048
)

// DefaultLocations are offered when the active theme declares none.
var DefaultLocations = []string{"primary", "footer"}

// ErrNotFound is returned for unknown menus.
var ErrNotFound = errors.New("menus: not found")

// ValidationError lists invalid fields (values are i18n keys).
type ValidationError struct {
	Fields map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string { return fmt.Sprintf("menus: invalid input: %v", e.Fields) }

// SavedEvent is the HookSaved payload.
type SavedEvent struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type service struct {
	db       *sql.DB
	hooks    *hooks.Registry
	audit    *audit.Writer
	log      *slog.Logger
	content  func() (content.Service, bool)
	taxonomy func() (taxonomy.Service, bool)
}

// ContentPath is the fallback permalink of a content item ("/{slug}"),
// used only when no theme is booted: the theme Linker overwrites it through
// FilterResolveURL with the real permalink (/post/<slug>, /parent/child).
func ContentPath(it *content.Item) string { return "/" + it.Slug }

// TermPath is the fallback permalink of a term ("/{taxonomy}/{slug}").
func TermPath(t *taxonomy.Term) string { return "/" + t.Taxonomy + "/" + t.Slug }

// ArchivePath is the archive URL of a content type ("/{archive_slug}/").
func ArchivePath(t *content.Type) string {
	if t.ArchiveSlug != "" {
		return "/" + t.ArchiveSlug + "/"
	}
	return "/" + t.Slug + "/"
}

func (s *service) List(ctx context.Context) ([]Menu, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, name, location, locale FROM menus ORDER BY name, slug`)
	if err != nil {
		return nil, fmt.Errorf("menus: list: %w", err)
	}
	var out []Menu
	for rows.Next() {
		var m Menu
		if err := rows.Scan(&m.ID, &m.Slug, &m.Name, &m.Location, &m.Locale); err != nil {
			rows.Close()
			return nil, fmt.Errorf("menus: scan: %w", err)
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Items, err = s.items(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get returns the menu with slug and its items resolved for display.
func (s *service) Get(ctx context.Context, slug string) (*Menu, error) {
	m, err := s.load(ctx, `slug = ? ORDER BY slug`, slug)
	if err != nil {
		return nil, err
	}
	m.Items = s.resolve(ctx, m.Items)
	return m, nil
}

// ByLocation returns the menu assigned to loc, resolved for display.
func (s *service) ByLocation(ctx context.Context, loc string) (*Menu, error) {
	if loc == "" {
		return nil, ErrNotFound
	}
	return s.ByLocationLocale(ctx, loc, "")
}

// ByLocationLocale returns the menu assigned to loc for locale, else the one for every locale,
// resolved for display.
func (s *service) ByLocationLocale(ctx context.Context, loc, locale string) (*Menu, error) {
	if loc == "" {
		return nil, ErrNotFound
	}
	m, err := s.load(ctx, `location = ? AND locale IN (?, '') ORDER BY locale = '', slug`, loc, locale)
	if err != nil {
		return nil, err
	}
	m.Items = s.resolve(ctx, m.Items)
	return m, nil
}

// getRaw returns a menu by ID with its stored (unresolved) items.
func (s *service) getRaw(ctx context.Context, id string) (*Menu, error) {
	return s.load(ctx, `id = ? ORDER BY slug`, id)
}

// load returns the first menu matching where (which ends with its ORDER BY) and its items.
func (s *service) load(ctx context.Context, where string, args ...any) (*Menu, error) {
	return loadMenu(ctx, s.db, where, args...)
}

func loadMenu(ctx context.Context, q menuReader, where string, args ...any) (*Menu, error) {
	var m Menu
	err := q.QueryRowContext(ctx, `SELECT id, slug, name, location, locale FROM menus WHERE `+where+` LIMIT 1`, args...).
		Scan(&m.ID, &m.Slug, &m.Name, &m.Location, &m.Locale)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("menus: get: %w", err)
	}
	if m.Items, err = readMenuItems(ctx, q, m.ID); err != nil {
		return nil, err
	}
	return &m, nil
}

// items loads a menu's items as a tree ordered by position.
func (s *service) items(ctx context.Context, menuID string) ([]Item, error) {
	return readMenuItems(ctx, s.db, menuID)
}

func readMenuItems(ctx context.Context, q menuReader, menuID string) ([]Item, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, menu_id, COALESCE(parent_id, ''), position, label, kind, ref_id,
		url, target, css_class, open_in_new FROM menu_items WHERE menu_id = ? ORDER BY position, id`, menuID)
	if err != nil {
		return nil, fmt.Errorf("menus: items: %w", err)
	}
	defer rows.Close()
	var flat []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.MenuID, &it.ParentID, &it.Position, &it.Label, &it.Kind, &it.RefID, &it.URL,
			&it.Target, &it.CSSClass, &it.OpenInNew); err != nil {
			return nil, fmt.Errorf("menus: scan item: %w", err)
		}
		flat = append(flat, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildTree(flat), nil
}

// buildTree nests flat (already position-ordered) items; orphans whose
// parent is missing become top-level items.
func buildTree(flat []Item) []Item {
	ids := make(map[string]bool, len(flat))
	children := map[string][]Item{}
	for _, it := range flat {
		ids[it.ID] = true
	}
	for _, it := range flat {
		p := it.ParentID
		if !ids[p] {
			p = ""
		}
		children[p] = append(children[p], it)
	}
	var build func(parent string, depth int) []Item
	build = func(parent string, depth int) []Item {
		list := children[parent]
		out := make([]Item, 0, len(list))
		for _, it := range list {
			if depth < maxDepth {
				it.Children = build(it.ID, depth+1)
			}
			if it.Children == nil {
				it.Children = []Item{}
			}
			out = append(out, it)
		}
		return out
	}
	return build("", 0)
}

// Save creates (empty ID) or updates a menu. A non-nil Items replaces the
// item tree in the same transaction. Assigning a location takes it away from
// any other menu.
func (s *service) Save(ctx context.Context, m Menu) (*Menu, error) {
	return s.saveGuarded(ctx, m, nil)
}

func (s *service) saveGuarded(ctx context.Context, m Menu, expected *string) (*Menu, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.Slug = strings.TrimSpace(strings.ToLower(m.Slug))
	m.Location = strings.TrimSpace(m.Location)
	m.Locale = strings.TrimSpace(m.Locale)
	if m.Slug == "" {
		if m.Slug = Slugify(m.Name); m.Slug == "" {
			m.Slug = "menu-" + strings.ToLower(db.NewID()[18:])
		}
	}
	fields := map[string]string{}
	if m.Name == "" || utf8.RuneCountInString(m.Name) > maxLabel {
		fields["name"] = "menus.error.name_required"
	}
	if !slugPattern.MatchString(m.Slug) || len(m.Slug) > 100 {
		fields["slug"] = "menus.error.slug_invalid"
	}
	if m.Location != "" && (!slugPattern.MatchString(m.Location) || len(m.Location) > 64) {
		fields["location"] = "menus.error.location_invalid"
	}
	if m.Locale != "" && !slices.Contains(i18n.Supported, m.Locale) {
		fields["locale"] = "menus.error.locale_invalid"
	}
	var flat []Item
	if m.Items != nil {
		owned, err := s.itemIDs(ctx, m.ID)
		if err != nil {
			return nil, err
		}
		if flat, err = flatten(m.Items, owned); err != nil {
			return nil, err
		}
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	created := m.ID == ""
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if expected != nil {
			if err := checkMenuToken(ctx, tx, m.ID, *expected); err != nil {
				return err
			}
		}
		var taken string
		err := tx.QueryRowContext(ctx, `SELECT id FROM menus WHERE slug = ? AND id != ?`, m.Slug, m.ID).Scan(&taken)
		if err == nil {
			return &ValidationError{Fields: map[string]string{"slug": "menus.error.slug_taken"}}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := db.Now()
		if created {
			m.ID = db.NewID()
			if _, err := tx.ExecContext(ctx, `INSERT INTO menus (id, slug, name, location, locale, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`, m.ID, m.Slug, m.Name, m.Location, m.Locale, now, now); err != nil {
				return err
			}
		} else {
			res, err := tx.ExecContext(ctx, `UPDATE menus SET slug = ?, name = ?, location = ?, locale = ?, updated_at = ? WHERE id = ?`,
				m.Slug, m.Name, m.Location, m.Locale, now, m.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}
		if m.Location != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE menus SET location = '' WHERE location = ? AND locale = ? AND id != ?`,
				m.Location, m.Locale, m.ID); err != nil {
				return err
			}
		}
		if m.Items != nil {
			if err := writeItems(ctx, tx, m.ID, flat); err != nil {
				return err
			}
		}
		action := "menus.update"
		if created {
			action = "menus.create"
		}
		s.audit.RecordTx(ctx, tx, action, "menu", m.ID, map[string]any{"slug": m.Slug, "name": m.Name, "location": m.Location, "locale": m.Locale})
		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}
	s.hooks.Emit(ctx, HookSaved, SavedEvent{ID: m.ID, Slug: m.Slug})
	return s.getRaw(ctx, m.ID)
}

func wrap(err error) error {
	var ve *ValidationError
	if errors.As(err, &ve) || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("menus: save: %w", err)
}

// Delete removes a menu and its items.
func (s *service) Delete(ctx context.Context, id string) error {
	m, err := s.getRaw(ctx, id)
	if err != nil {
		return err
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM menus WHERE id = ?`, id); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "menus.delete", "menu", id, map[string]any{"slug": m.Slug})
		return nil
	})
	if err != nil {
		return fmt.Errorf("menus: delete: %w", err)
	}
	s.hooks.Emit(ctx, HookSaved, SavedEvent{ID: id, Slug: m.Slug})
	return nil
}

// SaveItems replaces the menu's item tree. Positions follow slice order;
// IDs this menu does not already own (missing, duplicate, or copied from
// another menu) are regenerated.
func (s *service) SaveItems(ctx context.Context, menuID string, items []Item) error {
	return s.saveItemsGuarded(ctx, menuID, items, nil)
}

func (s *service) saveItemsGuarded(ctx context.Context, menuID string, items []Item, expected *string) error {
	m, err := s.getRaw(ctx, menuID)
	if err != nil {
		return err
	}
	owned, err := s.itemIDs(ctx, menuID)
	if err != nil {
		return err
	}
	flat, err := flatten(items, owned)
	if err != nil {
		return err
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if expected != nil {
			if err := checkMenuToken(ctx, tx, menuID, *expected); err != nil {
				return err
			}
		}
		if err := writeItems(ctx, tx, menuID, flat); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE menus SET updated_at = ? WHERE id = ?`, db.Now(), menuID); err != nil {
			return err
		}
		s.audit.RecordTx(ctx, tx, "menus.save_items", "menu", menuID, map[string]any{"slug": m.Slug, "items": len(flat)})
		return nil
	})
	if err != nil {
		return fmt.Errorf("menus: save items: %w", err)
	}
	s.hooks.Emit(ctx, HookSaved, SavedEvent{ID: menuID, Slug: m.Slug})
	return nil
}

func writeItems(ctx context.Context, tx *sql.Tx, menuID string, flat []Item) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM menu_items WHERE menu_id = ?`, menuID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO menu_items (id, menu_id, parent_id, position, label, kind, ref_id,
		url, target, css_class, open_in_new) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, it := range flat {
		var parent any
		if it.ParentID != "" {
			parent = it.ParentID
		}
		if _, err := stmt.ExecContext(ctx, it.ID, menuID, parent, it.Position, it.Label, it.Kind, it.RefID, it.URL,
			it.Target, it.CSSClass, it.OpenInNew); err != nil {
			return err
		}
	}
	return nil
}

// itemIDs returns the IDs of the items menuID currently owns.
func (s *service) itemIDs(ctx context.Context, menuID string) (map[string]bool, error) {
	owned := map[string]bool{}
	if menuID == "" {
		return owned, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM menu_items WHERE menu_id = ?`, menuID)
	if err != nil {
		return nil, fmt.Errorf("menus: item ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("menus: item ids: %w", err)
		}
		owned[id] = true
	}
	return owned, rows.Err()
}

// flatten validates the tree and returns its items in depth-first order
// with IDs, parent IDs and per-level positions assigned. Only IDs in owned
// are kept (once); every other item gets a fresh ID.
func flatten(items []Item, owned map[string]bool) ([]Item, error) {
	var out []Item
	seen := map[string]bool{}
	fields := map[string]string{}
	var walk func(list []Item, parent string, depth int)
	walk = func(list []Item, parent string, depth int) {
		for i, it := range list {
			idx := len(out)
			key := func(f string) string { return fmt.Sprintf("items.%d.%s", idx, f) }
			if depth >= maxDepth {
				fields[key("depth")] = "menus.error.too_deep"
				return
			}
			if !owned[it.ID] || seen[it.ID] {
				it.ID = db.NewID()
			}
			seen[it.ID] = true
			it.ParentID, it.Position = parent, i
			it.Label = strings.TrimSpace(it.Label)
			it.URL = strings.TrimSpace(it.URL)
			it.RefID = strings.TrimSpace(it.RefID)
			it.CSSClass = strings.Join(strings.Fields(it.CSSClass), " ")
			if it.Kind == "" {
				it.Kind = KindCustom
			}
			if it.OpenInNew {
				it.Target = "_blank"
			} else if it.Target != "_blank" {
				it.Target = ""
			} else {
				it.OpenInNew = true
			}
			validateItem(it, key, fields)
			children := it.Children
			it.Children = nil
			out = append(out, it)
			walk(children, it.ID, depth+1)
		}
	}
	walk(items, "", 0)
	if len(out) > maxItems {
		fields["items"] = "menus.error.too_many"
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	return out, nil
}

func validateItem(it Item, key func(string) string, fields map[string]string) {
	if utf8.RuneCountInString(it.Label) > maxLabel {
		fields[key("label")] = "menus.error.label_long"
	}
	if len(it.URL) > maxURL || len(it.CSSClass) > 200 || (it.URL != "" && !SafeURL(it.URL)) {
		fields[key("url")] = "menus.error.url_invalid"
	}
	switch it.Kind {
	case KindCustom:
		if it.Label == "" {
			fields[key("label")] = "menus.error.label_required"
		}
		if !SafeURL(it.URL) {
			fields[key("url")] = "menus.error.url_invalid"
		}
	case KindContent, KindTerm:
		if it.RefID == "" {
			fields[key("ref_id")] = "menus.error.ref_required"
		}
	case KindArchive:
		if it.RefID == "" {
			fields[key("ref_id")] = "menus.error.ref_required"
		}
	default:
		fields[key("kind")] = "menus.error.kind_invalid"
	}
}

// SafeURL accepts http(s), mailto and tel URLs, site-relative paths, query
// strings and fragments — never javascript: or other schemes.
func SafeURL(u string) bool {
	if u == "" || strings.ContainsAny(u, "\x00\r\n\t") {
		return false
	}
	l := strings.ToLower(u)
	switch {
	case strings.HasPrefix(l, "//"):
		return false
	case strings.HasPrefix(l, "/"), strings.HasPrefix(l, "#"), strings.HasPrefix(l, "?"):
		return true
	}
	for _, p := range []string{"http://", "https://", "mailto:", "tel:"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// Slugify makes a URL-safe slug from s (ASCII letters and digits; other
// runs become "-"). Names without Latin letters or digits yield "".
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
