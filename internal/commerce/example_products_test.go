package commerce

import (
	"context"
	"testing"
)

func TestSeedExampleProducts(t *testing.T) {
	e := boot(t)
	ctx := context.Background()
	if err := seedExampleProducts(ctx, e.svc); err != nil {
		t.Fatal(err)
	}
	for _, slug := range exampleProductSlugs {
		p, err := e.svc.Product(ctx, slug)
		if err != nil {
			t.Fatalf("missing %s: %v", slug, err)
		}
		if p.Active {
			t.Fatalf("%s should stay inactive", slug)
		}
		if p.GroupSlug != "learn" {
			t.Fatalf("%s group = %q", slug, p.GroupSlug)
		}
	}
	if err := seedExampleProducts(ctx, e.svc); err != nil {
		t.Fatal(err)
	}
	list, err := e.svc.Products(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range list {
		if isExampleProductSlug(p.Slug) {
			n++
		}
	}
	if n != len(exampleProductSlugs) {
		t.Fatalf("want %d examples, got %d", len(exampleProductSlugs), n)
	}
}
