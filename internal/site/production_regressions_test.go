package site_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/mcp"
	"github.com/zadstack/gorun/internal/site"
)

func TestLocaleRedirectStaysLocal(t *testing.T) {
	e := boot(t)
	for _, path := range []string{"/ar//evil.example", "/ar///evil.example?x=1", "/ar/%2f%2fevil.example", "/ar/%5cevil.example"} {
		t.Run(path, func(t *testing.T) {
			resp, _ := e.get(path)
			location := resp.Header.Get("Location")
			u, err := url.Parse(location)
			if resp.StatusCode != http.StatusMovedPermanently || err != nil || u.Host != "" || u.Scheme != "" || !strings.HasPrefix(location, "/") || strings.HasPrefix(location, "//") || strings.Contains(location, "\\") {
				t.Fatalf("unsafe redirect: status=%d location=%q error=%v", resp.StatusCode, location, err)
			}
		})
	}
}

func TestMaintenanceCoversCommerce(t *testing.T) {
	e := boot(t, commerce.New())
	e.setting(site.SettingMaintenance, true)
	for _, path := range []string{"/shop", "/shop/", "/product/example", "/checkout/success", "/checkout/status/example", "/account"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			resp, body := e.do(method, path, nil)
			if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" {
				t.Errorf("%s %s: status=%d", method, path, resp.StatusCode)
			}
			if method == http.MethodHead && body != "" {
				t.Errorf("HEAD %s has a body", path)
			}
		}
	}
	for _, path := range []string{"/checkout/example", "/account/login", "/account/open/example"} {
		resp, _ := e.do(http.MethodPost, path, nil)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("POST %s: %d", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/healthz", "/admin/login", "/static/admin/admin.css", "/static/admin/admin.js", httpx.LaunchCSSPath, "/sitemap.xml", "/robots.txt", "/themes/default/assets/style.css", "/themes/default/assets/tailwind.css", "/media/example.jpg"} {
		resp, _ := e.get(path)
		if resp.StatusCode == http.StatusServiceUnavailable {
			t.Errorf("exempt route %s blocked", path)
		}
	}
	if resp, body := e.get("/static/admin/vendor/tailwindcss-3.4.17.js"); resp.StatusCode != http.StatusOK || len(body) != 407279 {
		t.Fatalf("vendored compiler during maintenance: status=%d bytes=%d", resp.StatusCode, len(body))
	}
	e.setting(site.SettingMaintenance, false)
	if resp, _ := e.get("/shop"); resp.StatusCode != http.StatusOK {
		t.Fatalf("reopened shop: %d", resp.StatusCode)
	}
}

func TestMaintenanceKeepsMCPAuthentication(t *testing.T) {
	e := boot(t, mcp.New())
	e.setting(site.SettingMaintenance, true)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		resp, _ := e.do(method, "/mcp", nil)
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("%s /mcp: %d", method, resp.StatusCode)
		}
	}
	if resp, _ := e.get("/mcp/page"); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("MCP exemption matched page: %d", resp.StatusCode)
	}
}
