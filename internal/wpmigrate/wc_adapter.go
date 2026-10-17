package wpmigrate

import (
	"context"
	"encoding/json"
	"strings"
)

// mcbbKeys are the WooCommerce meta keys of the MCBB plan mapping.
var mcbbKeys = []string{"_mcbb_server", "_mcbb_server_id", "_mcbb_plan", "_mcbb_plan_code", "_mcbb_months"}

// hasPlanMapping reports whether a product carries MCBB plan meta.
func hasPlanMapping(p WPProduct) bool {
	server := firstNonEmpty(p.Meta["_mcbb_server"], p.Meta["_mcbb_server_id"])
	plan := firstNonEmpty(p.Meta["_mcbb_plan"], p.Meta["_mcbb_plan_code"])
	return server != "" && plan != ""
}

// catalogMeta keeps the meta the catalog needs (MCBB mapping, SKU) and
// drops WooCommerce internals.
func catalogMeta(meta map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range meta {
		if strings.HasPrefix(k, "_mcbb_") || k == "_sku" || !strings.HasPrefix(k, "_") {
			out[k] = v
		}
	}
	return out
}

// importProduct hands a catalog product to the commerce module (catalog
// only: orders and customers are never migrated). Without the commerce
// module the item is skipped with "needs_commerce_module".
func (s *service) importProduct(ctx context.Context, m *migrationRow, it *itemRow) error {
	var p WPProduct
	if err := json.Unmarshal(it.Payload, &p); err != nil {
		return err
	}
	p.Meta = catalogMeta(p.Meta)
	if p.Currency == "" {
		p.Currency = m.Options.Currency
	}
	needsMapping := !hasPlanMapping(p)
	imp, ok := s.importer()
	it.Would = map[string]any{"action": "import", "price_minor": p.PriceMinor, "currency": p.Currency, "needs_mapping": needsMapping}
	if !ok {
		it.Status, it.Error = ItemSkipped, "needs_commerce_module"
		it.Would["action"] = "needs_commerce_module"
		return nil
	}
	if m.DryRun {
		return nil
	}
	id, err := imp.UpsertFromWP(ctx, p)
	if err != nil {
		return err
	}
	it.LocalKind, it.LocalID = "product", id
	it.Meta = map[string]any{"needs_mapping": needsMapping}
	if needsMapping {
		s.logEntry(ctx, m.ID, "warn", it.ID, "product_needs_mapping", map[string]any{"wp_id": p.ID, "name": p.Name})
	}
	return nil
}
