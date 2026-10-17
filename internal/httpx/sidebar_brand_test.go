package httpx

import (
	"context"
	"testing"
)

func TestSidebarBrandResolverIsInstanceScoped(t *testing.T) {
	a := newTestTemplates(t, false)
	b := newTestTemplates(t, false)
	if a.sidebarBrand(context.Background()) != (SidebarBrand{}) {
		t.Fatal("nonempty fallback")
	}
	a.SetSidebarBrandResolver(func(context.Context) SidebarBrand {
		return SidebarBrand{Title: "One", LogoPath: "/media/2026/10/logo.png"}
	})
	if a.sidebarBrand(context.Background()).Title != "One" || b.sidebarBrand(context.Background()).Title != "" {
		t.Fatal("shared identity across instances")
	}
}
