package wpmigrate

import (
	"context"

	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/kernel"
)

// commerceImporter adapts commerce.Service to ProductImporter and
// ProductMediaReplacer (wpmigrate boots after commerce, so it may import it).
type commerceImporter struct{ cs commerce.Service }

// UpsertFromWP implements ProductImporter: the WPProduct structs are
// identical, field for field.
func (a commerceImporter) UpsertFromWP(ctx context.Context, p WPProduct) (string, error) {
	prod, err := a.cs.UpsertFromWP(ctx, commerce.WPProduct(p))
	if err != nil {
		return "", err
	}
	return prod.ID, nil
}

// ReplaceMediaRef implements ProductMediaReplacer.
func (a commerceImporter) ReplaceMediaRef(ctx context.Context, oldID, newID string) error {
	return a.cs.ReplaceMediaRef(ctx, oldID, newID)
}

// registerCommerceImporter publishes the adapter under
// ProductImporterService when the commerce module is booted.
func registerCommerceImporter(d *kernel.Deps) {
	if cs, ok := kernel.Get[commerce.Service](d, kernel.ServiceCommerce); ok {
		d.Services.Set(ProductImporterService, ProductImporter(commerceImporter{cs: cs}))
	}
}
