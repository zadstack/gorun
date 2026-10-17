package kernel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

// M1: kernel.BaseURL prefers the site.base_url setting over the config;
// HSTS ignores it and follows the request (addendum A).
func TestBaseURLPrefersSettingButHSTSFollowsRequest(t *testing.T) {
	cfg := testConfig(t)
	cfg.TrustedProxies = []string{"10.0.0.9/32"}
	cfg.BaseURL = "http://internal.example:8080/"
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	ctx := context.Background()
	hsts := func(forwardedHTTPS bool) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		if forwardedHTTPS {
			req.RemoteAddr = "10.0.0.9:443"
			req.Header.Set("X-Forwarded-Proto", "https")
		}
		app.Handler.ServeHTTP(rec, req)
		return rec.Header().Get("Strict-Transport-Security")
	}
	if got := kernel.BaseURL(ctx, app.Deps); got != "http://internal.example:8080" || hsts(false) != "" {
		t.Fatalf("config fallback: %q hsts=%q", got, hsts(false))
	}
	if err := app.Deps.Settings.Set(ctx, kernel.SettingBaseURL, "https://example.org/"); err != nil {
		t.Fatal(err)
	}
	if got := kernel.BaseURL(ctx, app.Deps); got != "https://example.org" {
		t.Fatalf("setting: %q", got)
	}
	if hsts(false) != "" {
		t.Fatal("an https site.base_url must not add HSTS to a plain-http request")
	}
	if hsts(true) == "" {
		t.Fatal("HSTS expected for an https request via a trusted proxy")
	}
	if kernel.BaseURL(ctx, nil) != "" || kernel.BaseURLOf(ctx, nil, "https://x.example/") != "https://x.example" {
		t.Fatal("nil deps / store fallbacks")
	}
}
