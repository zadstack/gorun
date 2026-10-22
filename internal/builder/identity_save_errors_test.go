package builder

import (
	"errors"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestIdentityErrorMapsBrandErrorsAndLocalizesFallback(t *testing.T) {
	s, _, ctx := builderTest(t)
	en := i18n.WithLocale(ctx, "en")
	for _, err := range []error{brand.ErrColors, brand.ErrFont, brand.ErrWidth, brand.ErrTextContrast, brand.ErrButtonContrast} {
		msg := identityError(s.d, en, err)
		if msg == "" || strings.Contains(msg, "Check the site name") {
			t.Fatalf("%v mapped to generic message %q", err, msg)
		}
	}
	if got := identityError(s.d, en, errors.New("other")); !strings.Contains(got, "Check the site name") {
		t.Fatalf("english fallback %q", got)
	}
	if got := identityError(s.d, i18n.WithLocale(ctx, "ar"), errors.New("other")); !strings.Contains(got, "تحقق من اسم الموقع") {
		t.Fatalf("arabic fallback %q", got)
	}
}

func TestPreviewBrandUsesPendingStarterIdentityThenSavedIdentity(t *testing.T) {
	h, _, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f := starterInput(issued.Token, "company")
	run, err := h.create(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	if css := previewBrand(h.d, ctx, run.Pages["home"]); !strings.Contains(css, f.Accent) {
		t.Fatalf("pending starter identity not previewed: %s", css)
	}
	if _, err := h.d.DB.ExecContext(ctx, `ALTER TABLE settings RENAME TO settings_gone`); err != nil {
		t.Fatal(err)
	}
	if css := previewBrand(h.d, ctx, run.Pages["home"]); css != brand.CSS(brand.Default()) {
		t.Fatalf("lookup failure did not fall back to default identity")
	}
}

func TestRegistrationFailsWithoutAdminTemplates(t *testing.T) {
	d := &kernel.Deps{Services: kernel.NewServiceRegistry()}
	for name, register := range map[string]func() error{
		"admin":     func() error { return registerAdmin(d, &service{d: d}) },
		"identity":  func() error { return registerIdentity(d) },
		"readiness": func() error { return registerReadiness(d) },
		"starters":  func() error { return registerStarters(d, &service{d: d}) },
	} {
		if err := register(); err == nil || !strings.Contains(err.Error(), "admin templates unavailable") {
			t.Fatalf("%s registered without templates: %v", name, err)
		}
	}
}
