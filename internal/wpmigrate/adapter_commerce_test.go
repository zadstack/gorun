package wpmigrate

import (
	"testing"

	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

// With the real commerce module booted, the WooCommerce products of the WXR
// sample become commerce products (not "needs commerce module"), and a
// re-imported product image is re-pointed before the old media goes.
func TestWXRProductsImportIntoCommerce(t *testing.T) {
	e := newEnvWith(t, commerce.New())
	imp, ok := kernel.Get[ProductImporter](e.app.Deps, ProductImporterService)
	if !ok {
		t.Fatal("commerce adapter not registered")
	}
	if _, ok := imp.(ProductMediaReplacer); !ok {
		t.Fatal("the adapter must also replace product media")
	}
	cs, _ := kernel.Get[commerce.Service](e.app.Deps, kernel.ServiceCommerce)
	// The billing server the fixture's _mcbb_server=garden-kit refers to.
	if err := cs.SaveServer(e.ctx(), commerce.Server{Name: "garden-kit", BaseURL: "https://billing.example.com", ClientID: "gorun", Secret: "s3cret-s3cret-s3cret"}); err != nil {
		t.Fatal(err)
	}
	servers, _ := cs.Servers(e.ctx())
	id := e.wxrMigration(Options{Currency: "AED"})
	if st := e.run(id); st.Status != StatusValidated || st.Counts["product"][ItemDone] != 2 {
		t.Fatalf("status %s products %v %+v", st.Status, st.Counts["product"], e.failedItems(id))
	}
	prods, err := cs.Products(e.ctx(), false)
	if err != nil {
		t.Fatalf("commerce products: %v", err)
	}
	imported := 0
	for _, p := range prods {
		if !commerce.IsExampleProductSlug(p.Slug) {
			imported++
		}
	}
	if imported != 2 {
		t.Fatalf("imported commerce products: %d (total %d)", imported, len(prods))
	}
	item := e.item(id, "product", "product", "1068")
	var p *commerce.Product
	for i := range prods {
		if prods[i].ID == item.LocalID {
			p = &prods[i]
		}
	}
	if p == nil || p.PriceMinor != 9900 || p.WPProductID != "1068" || p.ImageMediaID == "" {
		t.Fatalf("product 1068 = %+v (item local id %s)", p, item.LocalID)
	}
	// The MCBB plan mapping: plan code, months and the named billing server.
	if p.PlanCode != "basic" || len(servers) != 1 || p.ServerID != servers[0].ID || p.AccessMonths != 1 || !p.Active {
		t.Fatalf("billing mapping of product 1068 = plan %q server %q months %d active %v", p.PlanCode, p.ServerID, p.AccessMonths, p.Active)
	}
	oldImage := p.ImageMediaID
	if oldImage != e.item(id, "media", "attachment", "862").LocalID {
		t.Fatalf("product image %s is not the imported cover attachment", oldImage)
	}

	// The cover changes in WordPress: the product follows the new media.
	e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", redPNG())
	bumpWXRAttachment(t, e, id)
	if st := e.run(id); st.Status != StatusValidated || st.Failed != 0 {
		t.Fatalf("re-run: %s %+v", st.Status, e.failedItems(id))
	}
	newImage := e.item(id, "media", "attachment", "862").LocalID
	if newImage == oldImage {
		t.Fatal("media not replaced")
	}
	got, err := cs.Product(e.ctx(), p.Slug)
	if err != nil || got.ImageMediaID != newImage {
		t.Fatalf("product image after replace = %v (want %s) %v", got, newImage, err)
	}
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	if _, err := ms.Get(e.ctx(), oldImage); err == nil {
		t.Fatal("old media should be deleted once the product moved off it")
	}
}
