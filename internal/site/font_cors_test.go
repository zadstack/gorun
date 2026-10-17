package site_test

import (
	"testing"

	"github.com/zadstack/gorun/internal/theme"
)

func TestPublicFontAllowsOpaquePreviewWithoutCredentials(t *testing.T) {
	t.Setenv(theme.EnvThemesExtra, "../theme/testdata/extra") // a custom theme outside themes/
	e := boot(t)
	for _, path := range []string{"/static/admin/fonts/zad/Zad-Regular.woff2", "/themes/custom-fixture/assets/fonts/fixture.woff2"} {
		resp, _ := e.do("GET", path, nil, "Origin", "null")
		if resp.StatusCode != 200 || resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("font CORS %s: status=%d origin=%q credentials=%q", path, resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"), resp.Header.Get("Access-Control-Allow-Credentials"))
		}
	}
	for _, path := range []string{"/static/admin/fonts/zad/missing.woff2", "/static/admin/admin.css", "/themes/custom-fixture/assets/fonts/missing.woff2", "/themes/default/assets/style.css", "/themes/custom-fixture/assets/site.css", "/admin/login"} {
		resp, _ := e.do("GET", path, nil, "Origin", "null")
		if resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("nonfont %s allowed CORS", path)
		}
	}
}
