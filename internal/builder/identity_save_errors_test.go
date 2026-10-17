package builder

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
)

func identityValues(title string, id brand.Identity) url.Values {
	return url.Values{"title": {title}, "tagline": {"Tagline"}, "accent": {id.Accent}, "background": {id.Background}, "text": {id.Text}, "font": {id.Font}, "width": {id.Width}}
}

func TestIdentitySaveHandlerRedirectsOrRerendersWithError(t *testing.T) {
	s, _, ctx := builderTest(t)
	tpl, _ := kernel.Get[*httpx.Templates](s.d, kernel.ServiceAdminTemplates)
	h := identityHandler{d: s.d, tpl: tpl}
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/admin/appearance/identity", strings.NewReader(body)).WithContext(i18n.WithLocale(ctx, "en"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.save(rec, req)
		return rec
	}
	if rec := post("%zz"); rec.Code != 400 {
		t.Fatalf("malformed identity form HTTP %d", rec.Code)
	}
	rec := post(identityValues("", brand.Default()).Encode())
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Check the site name, logo and settings") {
		t.Fatalf("empty title HTTP %d", rec.Code)
	}
	if s.d.Settings.GetString(ctx, "site.title", "unset") != "unset" {
		t.Fatal("invalid identity saved title")
	}
	rec = post(identityValues("Saved Studio", brand.Default()).Encode())
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/appearance/identity?saved=1" {
		t.Fatalf("valid identity HTTP %d", rec.Code)
	}
	if s.d.Settings.GetString(ctx, "site.title", "") != "Saved Studio" {
		t.Fatal("valid identity not saved")
	}
}

func TestIdentityStoreRejectsLongFieldsAndUnknownLogo(t *testing.T) {
	h, _, ctx := starterTest(t)
	ih := identityHandler{d: h.d}
	for name, form := range map[string]identityForm{
		"long title":   {Title: strings.Repeat("a", 201), Identity: brand.Default()},
		"long tagline": {Title: "Studio", Tagline: strings.Repeat("a", 501), Identity: brand.Default()},
		"no media":     {Title: "Studio", Logo: "01HZZZZZZZZZZZZZZZZZZZZZZZ", Identity: brand.Default()},
	} {
		if err := ih.store(ctx, form); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	profileMedia(t, h, ctx)
	if err := ih.store(ctx, identityForm{Title: "Studio", Logo: "01HZZZZZZZZZZZZZZZZZZZZZZZ", Identity: brand.Default()}); err == nil || !strings.Contains(err.Error(), "existing logo") {
		t.Fatalf("unknown logo accepted: %v", err)
	}
	if h.d.Settings.GetString(ctx, "site.title", "unset") != "unset" {
		t.Fatal("rejected identity changed site title")
	}
}

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
