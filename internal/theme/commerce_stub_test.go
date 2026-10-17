package theme

import (
	"io/fs"
	"os"

	"github.com/zadstack/gorun/internal/kernel"
)

// Stand-ins for the commerce template contract (Task 12). Templates reach
// fields by name, so structurally identical local types are enough.
type stubProduct struct {
	Kind                                          string
	ID, Slug, Name, Description, ShortDescription string
	PriceMinor                                    int64
	Currency                                      string
	Active                                        bool
	GroupSlug, GroupName, PlanCode, ServerID      string
	AccessMonths                                  int
	Features                                      []string
	ImageMediaID                                  string
	Sort                                          int
}

type stubGroup struct {
	Slug, Name string
	Products   []stubProduct
}

// commerceStub registers the commerce funcs and kinds the theme needs; it
// must boot before the theme module (funcs are resolved at parse time).
type commerceStub struct{ groups []stubGroup }

func (*commerceStub) Catalogs() fs.FS { return os.DirFS("../commerce") }

func (*commerceStub) Name() string      { return "commercestub" }
func (*commerceStub) Migrations() fs.FS { return nil }
func (m *commerceStub) Register(d *kernel.Deps) error {
	d.Theme.AddFunc("products", func() []stubGroup { return m.groups })
	d.Theme.AddFunc("product", func(slug string) *stubProduct {
		for _, g := range m.groups {
			for i := range g.Products {
				if g.Products[i].Slug == slug {
					return &g.Products[i]
				}
			}
		}
		return nil
	})
	for _, kind := range []string{"shop", "product", "account", "checkout-result", "form-result"} { // form-result: the site module
		d.Theme.AddKind(kind, func(kernel.KindRequest) []string { return []string{kind + ".html"} })
	}
	return nil
}

func stubGroups() []stubGroup {
	feat := []string{"Unlimited tools", "Priority support"}
	return []stubGroup{
		{Slug: "wordpress", Name: "WordPress MCP", Products: []stubProduct{
			{ID: "p1", Slug: "wp-monthly", Name: "WordPress Starter", ShortDescription: "Connect one site", PriceMinor: 4900, Currency: "AED", Active: true, GroupSlug: "wordpress", GroupName: "WordPress MCP", PlanCode: "wp-1", AccessMonths: 1, Features: feat},
			{ID: "p2", Slug: "wp-pro", Name: "WordPress Pro", ShortDescription: "Up to five sites", PriceMinor: 9900, Currency: "AED", Active: true, GroupSlug: "wordpress", GroupName: "WordPress MCP", PlanCode: "wp-5", AccessMonths: 1, Features: feat},
			{ID: "p3", Slug: "wp-yearly", Name: "WordPress Yearly", PriceMinor: 49000, Currency: "AED", Active: true, GroupSlug: "wordpress", GroupName: "WordPress MCP", PlanCode: "wp-1y", AccessMonths: 12, Features: feat, ImageMediaID: "m-hero"},
		}},
		{Slug: "n8n", Name: "n8n MCP", Products: []stubProduct{
			{ID: "p4", Slug: "n8n-monthly", Name: "n8n Starter", PriceMinor: 3900, Currency: "AED", Active: true, GroupSlug: "n8n", GroupName: "n8n MCP", PlanCode: "n8n-1", AccessMonths: 1},
		}},
	}
}
