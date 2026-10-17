package theme

import (
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"html/template"
	"testing"
)

func TestOptionalAddonBodyRendersWithAndWithoutAddon(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeTheme(t, root, "default", "", baseTheme(map[string]string{"templates/page.html": `{{addonBody "fixtureRender" .Content.Body}}`}))
			e := newEnv(t, root, func(c *config.Config) { c.Theme = "default" })
			if enabled {
				e.svc.d.Theme.AddFunc("fixtureRender", func(body string) template.HTML { return template.HTML(body + "<p>addon widget</p>") })
			}
			rec, err := e.render(kernel.KindRequest{Kind: KindPage, Slug: "fixture"}, &Context{Content: &content.Item{Body: "<p>original body</p>"}})
			if err != nil || rec.Code != 200 {
				t.Fatalf("render %d %v", rec.Code, err)
			}
			mustContain(t, rec.Body.String(), "<p>original body</p>")
			if enabled {
				mustContain(t, rec.Body.String(), "addon widget")
			} else {
				mustNotContain(t, rec.Body.String(), "addon widget")
			}
		})
	}
}
