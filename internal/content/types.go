package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// Field types (spec §3).
const (
	FieldText         FieldType = "text"
	FieldTextarea     FieldType = "textarea"
	FieldRichtext     FieldType = "richtext"
	FieldNumber       FieldType = "number"
	FieldDate         FieldType = "date"
	FieldDatetime     FieldType = "datetime"
	FieldBoolean      FieldType = "boolean"
	FieldSelect       FieldType = "select"
	FieldMultiselect  FieldType = "multiselect"
	FieldRepeater     FieldType = "repeater"
	FieldRelationship FieldType = "relationship"
	FieldImage        FieldType = "image"
	FieldFile         FieldType = "file"
	FieldJSON         FieldType = "json"
)

// FieldTypes lists every field type in builder order.
var FieldTypes = []FieldType{FieldText, FieldTextarea, FieldRichtext, FieldNumber, FieldDate, FieldDatetime, FieldBoolean,
	FieldSelect, FieldMultiselect, FieldRepeater, FieldRelationship, FieldImage, FieldFile, FieldJSON}

// Statuses lists every item status.
var Statuses = []Status{"draft", "pending", "scheduled", "published", "private", "trash"}

// Supports lists the editor features a type may enable.
var Supports = []string{"title", "body", "excerpt", "featured_image", "author", "revisions", "seo", "parent", "menu_order", "comments"}

// IsScalar reports whether values of ft can be indexed (spec: text, number,
// date, datetime, boolean, select).
func (ft FieldType) IsScalar() bool {
	switch ft {
	case FieldText, FieldNumber, FieldDate, FieldDatetime, FieldBoolean, FieldSelect:
		return true
	}
	return false
}

var (
	typeSlugRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	fieldKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

const typeColumns = `slug, name_json, singular_json, plural_json, icon, public, has_archive, archive_slug, hierarchical,
	supports_json, fields_json, taxonomies_json, templates_json, system, menu_position, created_at, updated_at`

func scanType(sc interface{ Scan(...any) error }) (*Type, error) {
	var t Type
	var name, singular, plural, supports, fields, taxes, templates string
	if err := sc.Scan(&t.Slug, &name, &singular, &plural, &t.Icon, &t.Public, &t.HasArchive, &t.ArchiveSlug, &t.Hierarchical,
		&supports, &fields, &taxes, &templates, &t.System, &t.MenuPosition, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	for _, p := range []struct {
		raw string
		dst any
	}{{name, &t.Name}, {singular, &t.Singular}, {plural, &t.Plural}, {supports, &t.Supports}, {fields, &t.Fields},
		{taxes, &t.Taxonomies}, {templates, &t.Templates}} {
		if err := json.Unmarshal([]byte(p.raw), p.dst); err != nil {
			return nil, fmt.Errorf("content: decode type %s: %w", t.Slug, err)
		}
	}
	normalizeType(&t)
	return &t, nil
}

func normalizeType(t *Type) {
	if t.Name == nil {
		t.Name = map[string]string{}
	}
	if t.Singular == nil {
		t.Singular = map[string]string{}
	}
	if t.Plural == nil {
		t.Plural = map[string]string{}
	}
	if t.Supports == nil {
		t.Supports = []string{}
	}
	if t.Fields == nil {
		t.Fields = []Field{}
	}
	if t.Taxonomies == nil {
		t.Taxonomies = []string{}
	}
	if t.Templates == nil {
		t.Templates = []string{}
	}
}

// Types returns every content type ordered by menu position then slug.
func (s *service) Types(ctx context.Context) ([]Type, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+typeColumns+` FROM content_types ORDER BY menu_position, slug`)
	if err != nil {
		return nil, fmt.Errorf("content: list types: %w", err)
	}
	defer rows.Close()
	out := []Type{}
	for rows.Next() {
		t, err := scanType(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetType returns the type or ErrNotFound.
func (s *service) GetType(ctx context.Context, slug string) (*Type, error) {
	t, err := scanType(s.db.QueryRowContext(ctx, `SELECT `+typeColumns+` FROM content_types WHERE slug = ?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("content: get type %s: %w", slug, err)
	}
	return t, nil
}

// SaveType validates the schema and inserts or replaces the type. The
// System flag and CreatedAt are kept from the stored row.
func (s *service) SaveType(ctx context.Context, t Type) error {
	normalizeType(&t)
	if err := invalid(ValidateTypeSchema(&t)); err != nil {
		return err
	}
	now := s.nowString()
	existing, err := s.GetType(ctx, t.Slug)
	reindex := err == nil && indexSignature(existing.Fields) != indexSignature(t.Fields)
	switch {
	case errors.Is(err, ErrNotFound):
		t.System, t.CreatedAt = false, now
	case err != nil:
		return err
	default:
		t.System, t.CreatedAt = existing.System, existing.CreatedAt
	}
	t.UpdatedAt = now
	enc := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	err = db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO content_types (`+typeColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT (slug) DO UPDATE SET name_json=excluded.name_json, singular_json=excluded.singular_json,
			plural_json=excluded.plural_json, icon=excluded.icon, public=excluded.public, has_archive=excluded.has_archive,
			archive_slug=excluded.archive_slug, hierarchical=excluded.hierarchical, supports_json=excluded.supports_json,
			fields_json=excluded.fields_json, taxonomies_json=excluded.taxonomies_json, templates_json=excluded.templates_json,
			menu_position=excluded.menu_position, updated_at=excluded.updated_at`,
			t.Slug, enc(t.Name), enc(t.Singular), enc(t.Plural), t.Icon, t.Public, t.HasArchive, t.ArchiveSlug, t.Hierarchical,
			enc(t.Supports), enc(t.Fields), enc(t.Taxonomies), enc(t.Templates), t.System, t.MenuPosition, t.CreatedAt, t.UpdatedAt)
		if err != nil {
			return fmt.Errorf("content: save type %s: %w", t.Slug, err)
		}
		s.audit.RecordTx(ctx, tx, "content.type_save", "content_type", t.Slug, map[string]any{"fields": len(t.Fields)})
		return nil
	})
	if err != nil || !reindex {
		return err
	}
	// Indexed flags, field types or relationship fields changed: rebuild the
	// type's index and relation rows now (synchronous in v1).
	start := time.Now()
	if err := s.reindexType(ctx, &t); err != nil {
		return fmt.Errorf("content: type %s saved but reindex failed: %w", t.Slug, err)
	}
	s.log.Info("content: reindexed type after schema change", "type", t.Slug, "duration", time.Since(start))
	return nil
}

// DeleteType removes a non-system type that has no content.
func (s *service) DeleteType(ctx context.Context, slug string) error {
	t, err := s.GetType(ctx, slug)
	if err != nil {
		return err
	}
	if t.System {
		return ErrSystemType
	}
	return db.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM content WHERE type = ?`, slug).Scan(&n); err != nil {
			return fmt.Errorf("content: count type items: %w", err)
		}
		if n > 0 {
			return ErrTypeInUse
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM content_types WHERE slug = ?`, slug); err != nil {
			return fmt.Errorf("content: delete type: %w", err)
		}
		s.audit.RecordTx(ctx, tx, "content.type_delete", "content_type", slug, nil)
		return nil
	})
}

// ValidateTypeSchema checks a type definition: slug format, a name, valid
// supports, and every field (snake_case unique keys, known types, indexed
// only on scalars, sub_fields only on repeaters, target_type only — and
// always — on relationships, options on select/multiselect). Error keys are
// "slug", "name", "supports", "fields.<i>.<attr>".
func ValidateTypeSchema(t *Type) FieldErrors {
	errs := FieldErrors{}
	if !typeSlugRe.MatchString(t.Slug) {
		errs["slug"] = "content.validation.type_slug"
	}
	hasName := false
	for _, v := range t.Name {
		hasName = hasName || v != ""
	}
	if !hasName {
		errs["name"] = "content.validation.required"
	}
	for _, sup := range t.Supports {
		if !slices.Contains(Supports, sup) {
			errs["supports"] = "content.validation.supports"
		}
	}
	validateFields("fields", t.Fields, true, errs)
	return errs
}

func validateFields(prefix string, fields []Field, top bool, errs FieldErrors) {
	seen := map[string]bool{}
	for i, f := range fields {
		p := prefix + "." + strconv.Itoa(i) + "."
		switch {
		case !fieldKeyRe.MatchString(f.Key):
			errs[p+"key"] = "content.validation.field_key"
		case seen[f.Key]:
			errs[p+"key"] = "content.validation.field_key_dup"
		}
		seen[f.Key] = true
		if !slices.Contains(FieldTypes, f.Type) {
			errs[p+"type"] = "content.validation.field_type"
			continue
		}
		if f.Indexed && (!f.Type.IsScalar() || !top) {
			errs[p+"indexed"] = "content.validation.indexed_scalar"
		}
		if len(f.SubFields) > 0 && f.Type != FieldRepeater {
			errs[p+"sub_fields"] = "content.validation.sub_fields_repeater"
		}
		if f.Type == FieldRepeater {
			if len(f.SubFields) == 0 {
				errs[p+"sub_fields"] = "content.validation.sub_fields_required"
			}
			validateFields(p+"sub_fields", f.SubFields, false, errs)
		}
		if f.TargetType != "" && f.Type != FieldRelationship {
			errs[p+"target_type"] = "content.validation.target_type_relationship"
		}
		if f.Type == FieldRelationship && f.TargetType == "" {
			errs[p+"target_type"] = "content.validation.required"
		}
		if (f.Type == FieldSelect || f.Type == FieldMultiselect) && len(f.Options) == 0 {
			errs[p+"options"] = "content.validation.options_required"
		}
		if len(f.Options) > 0 && f.Type != FieldSelect && f.Type != FieldMultiselect {
			errs[p+"options"] = "content.validation.options_select"
		}
		opt := map[string]bool{}
		for _, o := range f.Options {
			if o.Value == "" || opt[o.Value] {
				errs[p+"options"] = "content.validation.options_invalid"
			}
			opt[o.Value] = true
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			errs[p+"min"] = "content.validation.min_max"
		}
	}
}
