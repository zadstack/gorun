package site_test

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/theme"
)

// The core site stylesheet is linked from the public page with a content
// hash, served from the binary as immutable CSS, and stays reachable in
// maintenance mode like the other public stylesheets.
func TestCoreStylesheetIsLinkedAndServed(t *testing.T) {
	e := boot(t)
	home, body := e.get("/")
	if home.StatusCode != 200 {
		t.Fatalf("home: %d", home.StatusCode)
	}
	mustContain(t, body, theme.CoreCSSURL())
	if strings.Index(body, theme.CoreCSSPath) > strings.Index(body, "</head>") {
		t.Fatal("the core stylesheet must be linked inside <head>")
	}

	resp, css := e.get(theme.CoreCSSURL())
	if resp.StatusCode != 200 {
		t.Fatalf("core.css: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("content-type %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache-control %q want immutable", cc)
	}
	if css != string(theme.CoreCSS()) {
		t.Fatal("served bytes differ from the embedded stylesheet")
	}

	e.setting("site.maintenance", true)
	if resp, _ := e.get("/"); resp.StatusCode != 503 {
		t.Fatalf("maintenance home: %d, want 503", resp.StatusCode)
	}
	if resp, _ := e.get(theme.CoreCSSURL()); resp.StatusCode != 200 {
		t.Fatalf("core.css in maintenance: %d", resp.StatusCode)
	}
}
