package builder

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityViewIncludesWorkingLogoPicker(t *testing.T) {
	s, _, ctx := builderTest(t)
	tpl, _ := kernel.Get[*httpx.Templates](s.d, kernel.ServiceAdminTemplates)
	h := identityHandler{d: s.d, tpl: tpl}
	rec := httptest.NewRecorder()
	h.view(rec, httptest.NewRequest("GET", "/admin/appearance/identity", nil).WithContext(ctx))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="gp-media-picker-body"`) {
		t.Fatalf("identity logo picker modal missing: HTTP %d", rec.Code)
	}
}

func TestIdentitySaveValidatesBeforeChangingGlobalSettings(t *testing.T) {
	s, _, ctx := builderTest(t)
	h := identityHandler{d: s.d}
	form := identityForm{Title: "Studio", Tagline: "Introduction", Identity: brand.Default()}
	if err := h.store(ctx, form); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(previewBrand(s.d, ctx, "unused"), "--gp-accent:#167252") {
		t.Fatal("saved design missing from preview")
	}
	form.Title = "Invalid edit"
	form.Identity.Text = form.Identity.Background
	if err := h.store(ctx, form); err == nil {
		t.Fatal("low contrast accepted")
	}
	if s.d.Settings.GetString(ctx, "site.title", "") != "Studio" {
		t.Fatal("invalid identity partly changed site")
	}
	contributor := auth.WithUser(context.Background(), &auth.User{ID: "contributor", Role: "contributor", Status: auth.StatusActive})
	form.Identity = brand.Default()
	if err := h.store(contributor, form); err == nil {
		t.Fatal("contributor modified global identity")
	}
}
