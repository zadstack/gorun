package wpmigrate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/db"
)

// Mapping decides where WordPress data goes (wizard steps 3 and 4).
type Mapping struct {
	Types      map[string]TypeMap             `json:"types"`      // wp type → target
	Taxonomies map[string]string              `json:"taxonomies"` // wp taxonomy → local taxonomy ("" skips)
	Fields     map[string]map[string]FieldMap `json:"fields"`     // wp type → meta key → field
	SkipSlugs  []string                       `json:"skip_slugs"` // pages never migrated (plugin pages)
}

// Type targets.
const (
	TargetSkip    = "skip"
	TargetCreate  = "create"           // create a content type with the WordPress slug
	TargetProduct = "commerce.product" // WooCommerce catalog adapter
)

// TypeMap maps one WordPress post type.
type TypeMap struct {
	Target string `json:"target"` // post | page | <existing type> | create | skip | commerce.product
	Name   string `json:"name"`   // WordPress label (for created types)
}

// FieldMap maps one meta key to a GoRun field ("" Target skips it).
type FieldMap struct {
	Target string `json:"target"`
	Type   string `json:"type"`
}

// defaultMapping builds the default map from a discovery report: post→post,
// page→page, product→commerce.product, other public types → create;
// category→category, post_tag→tag; meta keys skipped; plugin pages skipped.
func defaultMapping(rep *Report) Mapping {
	mp := Mapping{Types: map[string]TypeMap{}, Taxonomies: map[string]string{"category": "category", "post_tag": "tag"},
		Fields: map[string]map[string]FieldMap{}}
	if rep == nil {
		mp.Types["post"] = TypeMap{Target: "post", Name: "Posts"}
		mp.Types["page"] = TypeMap{Target: "page", Name: "Pages"}
		return mp
	}
	for _, f := range append(append([]Finding{}, rep.Available...), rep.Partial...) {
		slug, ok := strings.CutPrefix(f.Kind, "type:")
		if !ok {
			continue
		}
		name := f.Label["en"]
		if _, after, found := strings.Cut(name, ": "); found {
			name = after
		}
		switch slug {
		case "post", "page":
			mp.Types[slug] = TypeMap{Target: slug, Name: name}
		case "product":
			mp.Types[slug] = TypeMap{Target: TargetProduct, Name: name}
		default:
			mp.Types[slug] = TypeMap{Target: TargetCreate, Name: name}
		}
	}
	if f, _ := rep.Find("products"); f != nil {
		mp.Types["product"] = TypeMap{Target: TargetProduct, Name: "Products"}
	}
	for typ, keys := range rep.MetaKeys {
		fm := map[string]FieldMap{}
		for _, k := range keys {
			fm[k] = FieldMap{}
		}
		mp.Fields[typ] = fm
	}
	if f, _ := rep.Find("plugin_pages"); f != nil {
		mp.SkipSlugs = append(mp.SkipSlugs, f.Items...)
	}
	return mp
}

// normalized fills a zero mapping with the defaults.
func (mp Mapping) normalized(rep *Report) Mapping {
	if len(mp.Types) == 0 {
		return defaultMapping(rep)
	}
	if mp.Taxonomies == nil {
		mp.Taxonomies = map[string]string{"category": "category", "post_tag": "tag"}
	}
	if mp.Fields == nil {
		mp.Fields = map[string]map[string]FieldMap{}
	}
	return mp
}

// contentTypes returns the WordPress types imported as content, sorted
// with pages first (hierarchy) then posts then the rest.
func (mp Mapping) contentTypes() []string {
	var out []string
	for typ, tm := range mp.Types {
		if tm.Target != TargetSkip && tm.Target != TargetProduct && tm.Target != "" {
			out = append(out, typ)
		}
	}
	rank := func(t string) int {
		switch t {
		case "page":
			return 0
		case "post":
			return 1
		}
		return 2
	}
	sort.Slice(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// localType returns the GoRun type for a WordPress type.
func (mp Mapping) localType(wpType string) (string, bool) {
	tm, ok := mp.Types[wpType]
	if !ok || tm.Target == TargetSkip || tm.Target == TargetProduct || tm.Target == "" {
		return "", false
	}
	if tm.Target == TargetCreate {
		return wpType, true
	}
	return tm.Target, true
}

// wantsProducts reports whether products go to the commerce adapter.
func (mp Mapping) wantsProducts() bool { return mp.Types["product"].Target == TargetProduct }

// taxonomies returns the WordPress taxonomies to import, sorted.
func (mp Mapping) taxonomies() []string {
	var out []string
	for wp, local := range mp.Taxonomies {
		if local != "" {
			out = append(out, wp)
		}
	}
	sort.Strings(out)
	return out
}

func (mp Mapping) skipSlug(slug string) bool { return slices.Contains(mp.SkipSlugs, slug) }

// fieldKey is GoRun's field key format.
var fieldKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// SaveMapping stores the mapping (wizard steps 3–4 / API) and marks the
// migration mapped.
func (s *service) SaveMapping(ctx context.Context, migrationID string, mp Mapping) error {
	m, err := s.getMigration(ctx, migrationID)
	if err != nil {
		return err
	}
	if m.Status == StatusRunning {
		return fmt.Errorf("%w: running", ErrBadState)
	}
	for typ, tm := range mp.Types {
		if !slices.Contains([]string{"post", "page", TargetCreate, TargetSkip, TargetProduct}, tm.Target) {
			if _, err := s.content.GetType(ctx, tm.Target); err != nil {
				return fmt.Errorf("%w: unknown target %q for %s", ErrInvalidSource, tm.Target, typ)
			}
		}
	}
	for _, fields := range mp.Fields {
		for key, fm := range fields {
			if fm.Target != "" && !fieldKey.MatchString(fm.Target) {
				return fmt.Errorf("%w: invalid field key %q for %s", ErrInvalidSource, fm.Target, key)
			}
		}
	}
	b, _ := json.Marshal(mp)
	status := m.Status
	if status == StatusDraft || status == StatusScanned || status == StatusPreviewed {
		status = StatusMapped
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE migrations SET mapping_json = ?, status = ?, updated_at = ? WHERE id = ?`,
		string(b), status, db.Now(), migrationID); err != nil {
		return err
	}
	s.audit.Record(ctx, "wpmigrate.map", "migration", migrationID, nil)
	return nil
}
