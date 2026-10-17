package taxonomy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/audit"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/hooks"
)

var (
	// ErrNotFound: no such taxonomy or term.
	ErrNotFound = errors.New("taxonomy: not found")
	// ErrSystem: system taxonomies (category, tag) cannot be deleted.
	ErrSystem = errors.New("taxonomy: system taxonomy")
)

var taxSlugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)

const (
	defaultPerPage = 100
	maxPerPage     = 500
)

// service implements Service (and content.TermBackend + TxTermBackend).
type service struct {
	db        *sql.DB
	hooks     *hooks.Registry
	audit     *audit.Writer
	contentFn func() content.Service // nil-safe; content types are the source of truth for Types
}

var (
	_ Service               = (*service)(nil)
	_ content.TermBackend   = (*service)(nil)
	_ content.TxTermBackend = (*service)(nil)
)

func newService(d *sql.DB, h *hooks.Registry, a *audit.Writer, contentFn func() content.Service) *service {
	return &service{db: d, hooks: h, audit: a, contentFn: contentFn}
}

func (s *service) content() content.Service {
	if s.contentFn == nil {
		return nil
	}
	return s.contentFn()
}

func validationError(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return &content.ValidationError{Fields: content.FieldErrors(fields)}
}

// --- taxonomies ---

func scanTaxonomy(sc interface{ Scan(...any) error }) (*Taxonomy, error) {
	var t Taxonomy
	var name, singular, types string
	if err := sc.Scan(&t.Slug, &name, &singular, &t.Hierarchical, &types, &t.Public, &t.System); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(name), &t.Name)
	_ = json.Unmarshal([]byte(singular), &t.Singular)
	_ = json.Unmarshal([]byte(types), &t.Types)
	if t.Name == nil {
		t.Name = map[string]string{}
	}
	if t.Singular == nil {
		t.Singular = map[string]string{}
	}
	if t.Types == nil {
		t.Types = []string{}
	}
	return &t, nil
}

const taxColumns = `slug, name_json, singular_json, hierarchical, types_json, public, system`

// attachTypes derives Types from the content types that list the taxonomy
// (content types are authoritative; types_json is the fallback).
func (s *service) attachTypes(ctx context.Context, list []Taxonomy) {
	cs := s.content()
	if cs == nil {
		return
	}
	types, err := cs.Types(ctx)
	if err != nil {
		return
	}
	for i := range list {
		list[i].Types = []string{}
		for _, ct := range types {
			if slices.Contains(ct.Taxonomies, list[i].Slug) {
				list[i].Types = append(list[i].Types, ct.Slug)
			}
		}
	}
}

// Taxonomies returns every taxonomy (system first, then by slug).
func (s *service) Taxonomies(ctx context.Context) ([]Taxonomy, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+taxColumns+` FROM taxonomies ORDER BY system DESC, slug`)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: list: %w", err)
	}
	defer rows.Close()
	out := []Taxonomy{}
	for rows.Next() {
		t, err := scanTaxonomy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	s.attachTypes(ctx, out)
	return out, nil
}

// Get returns the taxonomy or ErrNotFound.
func (s *service) Get(ctx context.Context, slug string) (*Taxonomy, error) {
	t, err := scanTaxonomy(s.db.QueryRowContext(ctx, `SELECT `+taxColumns+` FROM taxonomies WHERE slug = ?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("taxonomy: get %s: %w", slug, err)
	}
	list := []Taxonomy{*t}
	s.attachTypes(ctx, list)
	return &list[0], nil
}

// Save inserts or updates a taxonomy and syncs the Taxonomies list of
// every content type to t.Types. System stays as stored.
func (s *service) Save(ctx context.Context, t Taxonomy) error {
	errs := map[string]string{}
	if !taxSlugRe.MatchString(t.Slug) {
		errs["slug"] = "taxonomy.validation.slug"
	}
	hasName := false
	for _, v := range t.Name {
		hasName = hasName || strings.TrimSpace(v) != ""
	}
	if !hasName {
		errs["name"] = "taxonomy.validation.required"
	}
	cs := s.content()
	var ctypes []content.Type
	if cs != nil {
		var err error
		if ctypes, err = cs.Types(ctx); err != nil {
			return err
		}
		for _, typ := range t.Types {
			if !slices.ContainsFunc(ctypes, func(ct content.Type) bool { return ct.Slug == typ }) {
				errs["types"] = "taxonomy.validation.types"
			}
		}
	}
	if err := validationError(errs); err != nil {
		return err
	}
	if t.Types == nil {
		t.Types = []string{}
	}
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	if t.Name == nil {
		t.Name = map[string]string{}
	}
	if t.Singular == nil {
		t.Singular = map[string]string{}
	}
	err := db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO taxonomies (`+taxColumns+`) VALUES (?, ?, ?, ?, ?, ?, 0)
			ON CONFLICT (slug) DO UPDATE SET name_json = excluded.name_json, singular_json = excluded.singular_json,
			hierarchical = excluded.hierarchical, types_json = excluded.types_json, public = excluded.public`,
			t.Slug, enc(t.Name), enc(t.Singular), t.Hierarchical, enc(t.Types), t.Public)
		if err != nil {
			return fmt.Errorf("taxonomy: save %s: %w", t.Slug, err)
		}
		s.audit.RecordTx(ctx, tx, "taxonomy.save", "taxonomy", t.Slug, map[string]any{"types": t.Types})
		return nil
	})
	if err != nil {
		return err
	}
	return s.syncTypes(ctx, cs, ctypes, t.Slug, t.Types)
}

// syncTypes makes exactly the content types in want list taxonomy slug.
func (s *service) syncTypes(ctx context.Context, cs content.Service, ctypes []content.Type, slug string, want []string) error {
	if cs == nil {
		return nil
	}
	for _, ct := range ctypes {
		has := slices.Contains(ct.Taxonomies, slug)
		should := slices.Contains(want, ct.Slug)
		if has == should {
			continue
		}
		if should {
			ct.Taxonomies = append(ct.Taxonomies, slug)
		} else {
			ct.Taxonomies = slices.DeleteFunc(ct.Taxonomies, func(x string) bool { return x == slug })
		}
		if err := cs.SaveType(ctx, ct); err != nil {
			return fmt.Errorf("taxonomy: sync type %s: %w", ct.Slug, err)
		}
	}
	return nil
}

// Delete removes a non-system taxonomy with its terms and assignments.
func (s *service) Delete(ctx context.Context, slug string) error {
	t, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	if t.System {
		return ErrSystem
	}
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM taxonomies WHERE slug = ?`, slug); err != nil {
			return fmt.Errorf("taxonomy: delete %s: %w", slug, err)
		}
		s.audit.RecordTx(ctx, tx, "taxonomy.delete", "taxonomy", slug, nil)
		return nil
	})
	if err != nil {
		return err
	}
	if cs := s.content(); cs != nil {
		ctypes, err := cs.Types(ctx)
		if err != nil {
			return err
		}
		return s.syncTypes(ctx, cs, ctypes, slug, nil)
	}
	return nil
}
