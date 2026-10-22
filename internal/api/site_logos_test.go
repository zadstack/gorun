package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme"
)

func siteLogos(t *testing.T, app *kernel.App) (api.SiteLogos, string) {
	t.Helper()
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site/info", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var env struct {
		Data api.SiteInformation `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data.Logos, w.Body.String()
}

func TestSiteInformationLogoVariantsFollowSettingsThenThemeManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "default")
	for path, body := range map[string]string{
		"theme.json":           `{"name":"Brand","version":"1.0.0","logos":{"icon":"icon.svg","light":"marks/light.svg"}}`,
		"templates/index.html": "x",
		"assets/icon.svg":      "<svg/>",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := apitest.Boot(t, media.New(), theme.New(theme.WithThemesDir(root)))
	ctx := t.Context()
	logos, _ := siteLogos(t, app)
	if logos.Primary.Available || logos.Primary.Reason != "not_configured" || logos.Dark.Available || logos.Dark.Reason != "not_configured" {
		t.Fatalf("empty settings invented a logo: %+v %+v", logos.Primary, logos.Dark)
	}
	// Theme-shipped marks fill variants the site did not set.
	if !logos.Icon.Available || *logos.Icon.Value != "/themes/default/assets/icon.svg" || logos.Icon.Fallback != "theme_manifest" || logos.Icon.Source != "theme:default:manifest:logos.icon" {
		t.Fatalf("manifest icon: %+v", logos.Icon)
	}
	if !logos.Light.Available || *logos.Light.Value != "/themes/default/assets/marks/light.svg" {
		t.Fatalf("manifest light: %+v", logos.Light)
	}
	svc, _ := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12"><rect width="12" height="12"/></svg>`
	primary, err := svc.Upload(ctx, "logo.svg", strings.NewReader(svg), media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	dark, err := svc.Upload(ctx, "logo-dark.svg", strings.NewReader(svg), media.UploadMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(ctx, "site.logo", primary.ID); err != nil {
		t.Fatal(err)
	}
	logos, body := siteLogos(t, app)
	p := logos.Primary
	if !p.Available || *p.Value != primary.URL || p.Path == nil || *p.Path != media.PublicPath(primary.StorageKey) || p.MediaID == nil || *p.MediaID != primary.ID || p.Rendered == nil || p.Fallback != "" {
		t.Fatalf("primary: %+v", p)
	}
	// Dark and light fall back to the main logo and say so; the site setting
	// outranks the theme's light mark; the icon still comes from the theme.
	if !logos.Dark.Available || *logos.Dark.Value != primary.URL || logos.Dark.Fallback != "site.logo" || !strings.HasPrefix(logos.Dark.Source, "settings:site.logo ") {
		t.Fatalf("dark fallback: %+v", logos.Dark)
	}
	if *logos.Light.Value != primary.URL || logos.Light.Fallback != "site.logo" || logos.Icon.Fallback != "theme_manifest" {
		t.Fatalf("precedence: light=%+v icon=%+v", logos.Light, logos.Icon)
	}
	if err := app.Deps.Settings.Set(ctx, "site.logo_dark", dark.ID); err != nil {
		t.Fatal(err)
	}
	logos, _ = siteLogos(t, app)
	if *logos.Dark.Value != dark.URL || logos.Dark.Fallback != "" || !strings.HasPrefix(logos.Dark.Source, "settings:site.logo_dark ") || *logos.Dark.MediaID != dark.ID {
		t.Fatalf("explicit dark: %+v", logos.Dark)
	}
	if strings.Contains(body, root) {
		t.Fatal("theme directory leaked")
	}
	// A signed storage URL is withheld, but the file is still served by the
	// site, so the logo is reported through its /media/ path.
	if _, err := app.Deps.DB.ExecContext(ctx, `UPDATE media SET url = ? WHERE id = ?`, "https://cdn.example/logo.svg?X-Amz-Signature=PRIVATE_SIGNATURE", primary.ID); err != nil {
		t.Fatal(err)
	}
	logos, body = siteLogos(t, app)
	p = logos.Primary
	if strings.Contains(body, "PRIVATE_SIGNATURE") {
		t.Fatal("signed URL published")
	}
	if !p.Available || *p.Value != media.PublicPath(primary.StorageKey) || p.Source != "settings:site.logo -> media.path" || p.URL.Available || p.URL.Reason != "unsafe_public_url" {
		t.Fatalf("signed storage URL left the logo unavailable: %+v url=%+v", p, p.URL)
	}
	// Reads never changed data.
	if got := app.Deps.Settings.GetString(ctx, "site.logo", ""); got != primary.ID {
		t.Fatal("settings changed")
	}
	if err := app.Deps.Settings.Set(ctx, "site.icon", "missing-media"); err != nil {
		t.Fatal(err)
	}
	logos, _ = siteLogos(t, app)
	if logos.Icon.Available || logos.Icon.Reason != "media_not_found" || logos.Icon.Fallback != "" {
		t.Fatalf("missing icon media must not fall through to the theme silently: %+v", logos.Icon)
	}
}

// Every logo branch carries provenance on the url field too: a missing
// setting, a dangling media id, an absent media module and a manifest mark
// must never leave url as an empty value without source or reason.
func TestSiteInformationLogoURLAlwaysCarriesProvenance(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "default")
	for path, body := range map[string]string{"theme.json": `{"name":"Brand","version":"1.0.0","logos":{"icon":"icon.svg"}}`, "templates/index.html": "x", "assets/icon.svg": "<svg/>"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, name string, l api.SiteLogo, wantReason string) {
		t.Helper()
		if l.URL.Source == "" || l.URL.Available != (l.URL.Value != nil) {
			t.Fatalf("%s: url without provenance: %+v", name, l.URL)
		}
		if wantReason != "" && (l.URL.Available || l.URL.Reason != wantReason) {
			t.Fatalf("%s: url reason %+v, want %s", name, l.URL, wantReason)
		}
	}
	// No media module at all.
	bare := apitest.Boot(t, theme.New(theme.WithThemesDir(root)))
	if err := bare.Deps.Settings.Set(t.Context(), "site.logo", "some-id"); err != nil {
		t.Fatal(err)
	}
	logos, _ := siteLogos(t, bare)
	check(t, "primary without media module", logos.Primary, "media_module_unavailable")
	check(t, "dark fallback without media module", logos.Dark, "media_module_unavailable")
	check(t, "manifest icon", logos.Icon, "")
	if !logos.Icon.URL.Available || *logos.Icon.URL.Value != "/themes/default/assets/icon.svg" {
		t.Fatalf("manifest url: %+v", logos.Icon.URL)
	}
	// Media module present: not configured and dangling ids.
	app := apitest.Boot(t, media.New(), theme.New(theme.WithThemesDir(root)))
	logos, _ = siteLogos(t, app)
	check(t, "not configured", logos.Primary, "not_configured")
	check(t, "light not configured", logos.Light, "not_configured")
	if err := app.Deps.Settings.Set(t.Context(), "site.logo", "missing-media"); err != nil {
		t.Fatal(err)
	}
	logos, _ = siteLogos(t, app)
	check(t, "dangling media", logos.Primary, "media_not_found")
	check(t, "dark via dangling media", logos.Dark, "media_not_found")
}
