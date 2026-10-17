package theme

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestShellVariantsPreserveNavigationAndFallback(t *testing.T) {
	for _, slug := range []string{"default", "magazine", "business", "portfolio"} {
		t.Run(slug, func(t *testing.T) {
			e := newEnv(t, "../../themes", func(c *config.Config) { c.Theme = slug })
			_, err := e.fake.Menus.Save(t.Context(), menus.Menu{Slug: "main", Location: "primary", Items: []menus.Item{{Label: "Actual services", URL: "/real-services", OpenInNew: true}}})
			if err != nil {
				t.Fatal(err)
			}
			e.setting("theme."+slug+".header_style", "framed")
			e.setting("theme."+slug+".footer_style", "columns")
			rec, err := e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil {
				t.Fatal(err)
			}
			body := rec.Body.String()
			mustContain(t, body, `data-shell-header="framed"`, `data-shell-footer="columns"`, `tabindex="-1"`, `shell.css`, `shell.js`)
			footer := body[strings.Index(body, "<footer"):]
			mustContain(t, footer, "Actual services", "/real-services")
			e.setting("theme."+slug+".header_style", `<script>alert(1)</script>`)
			e.setting("theme."+slug+".footer_style", "unknown")
			rec, err = e.render(kernel.KindRequest{Kind: KindHome}, nil)
			if err != nil {
				t.Fatal(err)
			}
			mustContain(t, rec.Body.String(), `data-shell-header="native"`, `data-shell-footer="native"`)
		})
	}
}
