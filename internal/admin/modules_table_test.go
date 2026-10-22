package admin

import (
	"slices"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

// Every domain module in kernel.BootOrder has a catalog row (kind and
// dependencies), every row names a real module, dependencies point at
// earlier modules, and the consequence notes refer to declared edges.
func TestBuiltinCatalogCoversBootOrder(t *testing.T) {
	infra := map[string]bool{"db": true, "i18n": true, "audit": true, "jobs": true}
	for _, name := range kernel.BootOrder {
		if infra[name] {
			continue
		}
		b, ok := moduleCatalogTable[name]
		if !ok {
			t.Errorf("BootOrder module %q has no catalog row", name)
			continue
		}
		if b.kind != CatalogKindCore && b.kind != CatalogKindFeature {
			t.Errorf("%s: kind %q", name, b.kind)
		}
		pos := slices.Index(kernel.BootOrder, name)
		for _, dep := range append(slices.Clone(b.dependencies), b.optional...) {
			dp := slices.Index(kernel.BootOrder, dep)
			if dp < 0 {
				t.Errorf("%s depends on unknown module %q", name, dep)
			} else if dp > pos && b.kind != CatalogKindCore {
				t.Errorf("%s depends on later module %q", name, dep)
			}
		}
	}
	for name := range moduleCatalogTable {
		if !slices.Contains(kernel.BootOrder, name) {
			t.Errorf("catalog row %q is not in BootOrder", name)
		}
	}
	for edge := range dependencyNotes {
		b := moduleCatalogTable[edge[1]]
		if !slices.Contains(b.dependencies, edge[0]) && !slices.Contains(b.optional, edge[0]) {
			t.Errorf("consequence note %v has no declared dependency", edge)
		}
	}
}
