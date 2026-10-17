package seo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestSEOInventoryNativePublicContentAndPermissions(t *testing.T) {
	s := boot(t, "admin", content.New())
	svc, ok := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content unavailable")
	}
	ctx := context.Background()
	for _, row := range []content.Input{{Type: "page", Title: "Inventory public fixture", Status: "published"}, {Type: "page", Title: "Inventory secret draft", Status: "draft"}, {Type: "page", Title: "Deliberately unindexed", Status: "published", SEO: map[string]any{"robots": "noindex"}}} {
		if _, err := svc.Create(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	resp, body := s.do("GET", "/admin/settings/seo/audit", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "Inventory public fixture") || !strings.Contains(body, "Deliberately unindexed") || strings.Contains(body, "Inventory secret draft") {
		t.Fatalf("inventory status=%d published/draft mismatch", resp.StatusCode)
	}
	if !strings.Contains(body, "غير متصلة أو متحقق منها") || !strings.Contains(body, "قد يُستخدم النص أو الوصف الافتراضي") {
		t.Fatal("honest analytics/fallback boundary missing")
	}
	s = boot(t, "editor", content.New())
	resp, _ = s.do("GET", "/admin/settings/seo/audit", nil)
	if resp.StatusCode != 403 {
		t.Fatalf("permission %d", resp.StatusCode)
	}
}
