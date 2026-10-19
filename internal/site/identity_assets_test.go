package site_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/brand"
)

func TestIdentityStylesheetsAreCachedAssets(t *testing.T) {
	e := boot(t)
	e.setting("builder.identity", brand.Identity{
		Accent: "#167252", Background: "#101010", Text: "#eeeeee", Font: "mono", Width: "wide",
	})
	e.setting("theme.default.identity_accent", "#c2410c")

	home, body := e.get("/")
	if home.StatusCode != 200 {
		t.Fatalf("home: %d", home.StatusCode)
	}
	if strings.Contains(body, `data-gorun-identity><`) || strings.Contains(body, `<style nonce`) && strings.Contains(body, "data-gorun-identity") {
		t.Fatal("identity must not be an inline <style> on the public home")
	}
	mustContain(t, body, `/site/builder-identity.css?v=`, `/site/identity.css?v=`)

	for _, path := range []string{"/site/builder-identity.css?v=test", "/site/identity.css?v=test"} {
		resp, css := e.get(path)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/css") {
			t.Fatalf("%s content-type %q", path, ct)
		}
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Fatalf("%s cache-control %q want immutable", path, cc)
		}
		if path == "/site/builder-identity.css?v=test" {
			mustContain(t, css, "--gp-width:1280px", "--bg:#101010")
		} else {
			mustContain(t, css, "--green:#c2410c", "--gp-accent:#c2410c")
		}
	}
}

func TestIdentityStylesheetAbsentWithoutSettings(t *testing.T) {
	e := boot(t)
	resp, _ := e.get("/site/identity.css")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("empty identity: %d", resp.StatusCode)
	}
}
