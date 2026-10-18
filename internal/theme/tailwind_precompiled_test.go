package theme

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
)

// The default theme (and docs, which keeps its head) links the precompiled
// Tailwind stylesheet and no longer loads the in-browser compiler; a child
// theme opts back into the runtime by including parts/tailwind-runtime.html.
func TestDefaultThemeUsesPrecompiledTailwind(t *testing.T) {
	css, err := os.ReadFile("../../themes/default/assets/tailwind.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(css), "*,") || !strings.Contains(string(css), "tailwindcss v3.4.17") {
		t.Fatalf("themes/default/assets/tailwind.css does not look like a Tailwind build (%d bytes)", len(css))
	}

	extra := t.TempDir()
	writeTheme(t, extra, "runtime-child", `{"name":"Runtime child","version":"1.0.0","parent":"default"}`, map[string]string{
		"parts/head-extra.html": `{{part "tailwind-runtime" .}}`,
	})
	t.Setenv(EnvThemesExtra, extra)
	e := newEnv(t, "../../themes")
	ctx := context.Background()

	for _, slug := range []string{"default", "docs"} {
		if err := e.svc.Activate(ctx, slug); err != nil {
			t.Fatal(err)
		}
		rec, err := e.render(kernel.KindRequest{Kind: Kind404}, &Context{Status: 404})
		if err != nil {
			t.Fatal(err)
		}
		body := rec.Body.String()
		mustContain(t, body, `/themes/default/assets/tailwind.css?v=`)
		mustNotContain(t, body, "tailwindcss-3.4.17.js", "tailwind.config")
		if strings.Index(body, "tailwind.css") < strings.Index(body, "sections.css") {
			t.Errorf("%s: tailwind.css must come after the theme stylesheets (it replaces the runtime's late styles)", slug)
		}
	}

	if err := e.svc.Activate(ctx, "runtime-child"); err != nil {
		t.Fatal(err)
	}
	rec, err := e.render(kernel.KindRequest{Kind: Kind404}, &Context{Status: 404})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, rec.Body.String(), `/static/admin/vendor/tailwindcss-3.4.17.js`, "tailwind.config", `/themes/default/assets/tailwind.css?v=`)
}
