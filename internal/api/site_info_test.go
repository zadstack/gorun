package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/theme"
)

func TestPublicSiteInformationAndProtectedRuntime(t *testing.T) {
	previous := api.CoreVersion
	api.CoreVersion = "v0.12.0-test.7"
	t.Cleanup(func() { api.CoreVersion = previous })
	app := apitest.Boot(t)
	for key, value := range map[string]any{
		"site.title": "Existing site", "site.tagline": "Existing tagline", "site.locale": "en",
		"site.base_url": "https://example.com/", "site.logo": "missing-logo",
		"mail.smtp_password": "never-public", "system.update.version": "v99.0.0",
	} {
		if err := app.Deps.Settings.Set(t.Context(), key, value); err != nil {
			t.Fatal(err)
		}
	}
	// Changes to desired/accepted versions and subsequent boot configuration must
	// never change this instance's running build identity.
	api.CoreVersion = "v99.0.0"
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site/info", nil))
	if w.Code != 200 {
		t.Fatalf("public information: %d %s", w.Code, w.Body)
	}
	var response struct {
		Data struct {
			Version struct {
				Value  *string
				Source string
			}
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Version.Value == nil || *response.Data.Version.Value != "v0.12.0-test.7" || response.Data.Version.Source != "process.build" {
		t.Fatalf("running version: %s", w.Body)
	}
	for _, want := range []string{`"Existing site"`, `"https://example.com"`, `"settings:site.title"`, `"supported":["ar","en"]`, `"appearance":null`, `"media_module_unavailable"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing %s: %s", want, w.Body)
		}
	}
	for _, secret := range []string{"never-public", "v99.0.0", app.Deps.Config.DataDir, "go_version", "goroutine"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("public response leaks %q", secret)
		}
	}
	anon := apitest.NewClient(t, app).Anonymous()
	if status, _ := anon.Get("/site/runtime"); status != 401 {
		t.Fatalf("anonymous runtime: %d", status)
	}
	if status, _ := apitest.NewClient(t, app, "content.view").Get("/site/runtime"); status != 403 {
		t.Fatalf("limited runtime: %d", status)
	}
	status, body := apitest.NewClient(t, app, "settings.manage").Get("/site/runtime")
	if status != 200 || apitest.Data(body)["version"] != "v0.12.0-test.7" {
		t.Fatalf("runtime: %d %v", status, body)
	}
	if status, _ := anon.Get("/site"); status != 401 {
		t.Fatalf("legacy structure must remain protected: %d", status)
	}
	var title string
	if err := app.Deps.Settings.Get(t.Context(), "site.title", &title); err != nil || title != "Existing site" {
		t.Fatalf("GET changed settings: %q %v", title, err)
	}
}

func TestSiteInformationUnavailableAndReadFailure(t *testing.T) {
	app := apitest.Boot(t)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site/info", nil))
		return w
	}
	w := request()
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"value":null`) || !strings.Contains(w.Body.String(), `"not_configured"`) {
		t.Fatalf("missing values: %d %s", w.Code, w.Body)
	}
	if err := app.Deps.Settings.Set(t.Context(), "site.title", map[string]string{"unexpected": "value"}); err != nil {
		t.Fatal(err)
	}
	w = request()
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"internal_error"`) || strings.Contains(w.Body.String(), "unexpected") {
		t.Fatalf("read failure: %d %s", w.Code, w.Body)
	}
}

func TestSiteDiscoveryOpenAPI(t *testing.T) {
	app := apitest.Boot(t)
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/openapi.json", nil))
	var doc struct {
		Paths map[string]map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	public := doc.Paths["/v1/site/info"]["get"]
	if string(public["security"]) != "[]" {
		t.Fatalf("public security: %s", public["security"])
	}
	if string(doc.Paths["/v1/site/runtime"]["get"]["x-scopes"]) != `["settings.manage"]` {
		t.Fatal("runtime scope missing from OpenAPI")
	}
}

func TestSiteInformationResolvesExistingLogoWithoutChangingData(t *testing.T) {
	app := apitest.Boot(t, media.New())
	svc, ok := kernel.Get[media.Service](app.Deps, kernel.ServiceMedia)
	if !ok {
		t.Fatal("missing media service")
	}
	logo, err := svc.Upload(t.Context(), "logo.svg", strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12"><rect width="12" height="12" fill="red"/></svg>`), media.UploadMeta{Alt: "Existing logo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(t.Context(), "site.logo", logo.ID); err != nil {
		t.Fatal(err)
	}
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site/info", nil))
		return w
	}
	w := get()
	if w.Code != 200 || !strings.Contains(w.Body.String(), logo.URL) {
		t.Fatalf("logo: %d %s", w.Code, w.Body)
	}
	stored, err := svc.Get(t.Context(), logo.ID)
	if err != nil || stored.Alt != "Existing logo" || stored.URL != logo.URL {
		t.Fatalf("logo modified: %+v %v", stored, err)
	}
	if _, err := app.Deps.DB.ExecContext(t.Context(), `UPDATE media SET url = ? WHERE id = ?`, "https://example.com/logo.svg?token=PRIVATE_LOGO_TOKEN", logo.ID); err != nil {
		t.Fatal(err)
	}
	w = get()
	if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE_LOGO_TOKEN") || !strings.Contains(w.Body.String(), `"unsafe_public_url"`) {
		t.Fatalf("private logo URL: %d %s", w.Code, w.Body)
	}
	if err := app.Deps.Settings.Set(t.Context(), "site.logo", "missing-media"); err != nil {
		t.Fatal(err)
	}
	w = get()
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"media_not_found"`) {
		t.Fatalf("missing logo: %d %s", w.Code, w.Body)
	}
}

func TestSiteInformationUsesRealThemeProvider(t *testing.T) {
	app := apitest.Boot(t, theme.New(theme.WithThemesDir("../../themes")))
	if err := app.Deps.Settings.Set(t.Context(), "site.theme", "business"); err != nil {
		t.Fatal(err)
	}
	if err := app.Deps.Settings.Set(t.Context(), "theme.business.identity_accent", "#123456"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site/info", nil))
	if w.Code != 200 {
		t.Fatalf("appearance: %d %s", w.Code, w.Body)
	}
	var env struct {
		Data api.SiteInformation `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Appearance == nil || env.Data.Appearance.Theme.Slug.Value == nil || *env.Data.Appearance.Theme.Slug.Value != "business" {
		t.Fatalf("active theme missing: %s", w.Body)
	}
	accent := env.Data.Appearance.Colors["accent"]
	if accent.Value == nil || *accent.Value != "#123456" || accent.Source != "settings:theme.business.identity_accent" {
		t.Fatalf("active identity missing: %+v", accent)
	}
	if strings.Contains(w.Body.String(), `"dir":"../../themes`) || strings.Contains(w.Body.String(), `"settings":[`) {
		t.Fatal("public response exposed theme implementation details")
	}
}

func TestSiteInformationDoesNotPublishURLCredentials(t *testing.T) {
	app := apitest.Boot(t)
	for _, unsafe := range []string{
		"https://operator:PRIVATE_CREDENTIAL@example.com",
		"https://example.com?token=PRIVATE_CREDENTIAL",
		"https://example.com/#PRIVATE_CREDENTIAL",
		"javascript:PRIVATE_CREDENTIAL",
	} {
		app.Deps.Config.BaseURL = unsafe
		for _, fromSetting := range []bool{false, true} {
			if fromSetting {
				if err := app.Deps.Settings.Set(t.Context(), "site.base_url", unsafe); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := app.Deps.Settings.Set(t.Context(), "site.base_url", ""); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/site/info", nil))
			if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE_CREDENTIAL") || !strings.Contains(w.Body.String(), `"unsafe_public_url"`) {
				t.Fatalf("unsafe URL leaked (setting=%v): %d %s", fromSetting, w.Code, w.Body)
			}
		}
	}
}
