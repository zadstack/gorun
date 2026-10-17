package wpmigrate

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zadstack/gorun/internal/taxonomy"
)

// importTerm creates (or reuses by slug) a term in the mapped taxonomy.
func (s *service) importTerm(ctx context.Context, m *migrationRow, it *itemRow) error {
	var t WPTerm
	if err := json.Unmarshal(it.Payload, &t); err != nil {
		return err
	}
	local := m.Mapping.Taxonomies[t.Taxonomy]
	if local == "" {
		it.Status, it.Error = ItemSkipped, "taxonomy_not_mapped"
		return nil
	}
	if _, err := s.tax.Get(ctx, local); err != nil {
		if errors.Is(err, taxonomy.ErrNotFound) {
			it.Status, it.Error = ItemSkipped, "taxonomy_missing"
			return nil
		}
		return err
	}
	slug := decodeSlug(t.Slug)
	it.LocalKind = "term"
	if existing, err := s.tax.GetTermBySlug(ctx, local, slug); err == nil {
		it.LocalID, it.LocalURL = existing.ID, s.linker.Term(existing)
		it.Would = map[string]any{"action": "reuse", "taxonomy": local, "slug": slug}
		return nil
	}
	it.Would = map[string]any{"action": "create", "taxonomy": local, "slug": slug}
	if m.DryRun {
		it.LocalURL = "/" + local + "/" + slug
		return nil
	}
	parentID, _, _ := s.mapped(ctx, m, "term", t.Taxonomy, t.Parent)
	saved, err := s.tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: local, Slug: slug, Name: firstNonEmpty(t.Name, slug),
		Description: t.Description, ParentID: parentID, Meta: map[string]any{"wp_id": t.ID}})
	if err != nil {
		return err
	}
	it.LocalID, it.LocalURL = saved.ID, s.linker.Term(saved)
	return nil
}
