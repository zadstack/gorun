package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

// A child may inherit the public shell without supplying any CSS or templates.
// Default-only redesign rules must not leave that child with unstyled markup.
func TestDefaultChildInheritsCompatibleShell(t *testing.T) {
	for _, customHeader := range []bool{false, true} {
		name := "inherited-header"
		if customHeader {
			name = "custom-header"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.CopyFS(root, os.DirFS("../../themes")); err != nil {
				t.Fatal(err)
			}
			child := filepath.Join(root, "review-child")
			if err := os.MkdirAll(filepath.Join(child, "parts"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(child, "theme.json"), []byte(`{"name":"Child","parent":"default"}`), 0644); err != nil {
				t.Fatal(err)
			}
			if customHeader {
				if err := os.WriteFile(filepath.Join(child, "parts", "header.html"), []byte(`{{part "head" .}}<header class="custom-shell">Custom shell</header><main id="main">`), 0644); err != nil {
					t.Fatal(err)
				}
			}
			e := newEnv(t, root, func(c *config.Config) { c.Theme = "review-child" })
			post := e.fake.Content.Add(content.Item{Type: "post", Slug: "hello", Title: "Hello", Body: "<p>Read this story</p>"})
			for _, kind := range []string{KindHome, KindSingle} {
				rec, err := e.render(kernel.KindRequest{Kind: kind, Type: "post", Slug: "hello"}, &Context{Content: &post, Items: []content.Item{post}, Pagination: NewPagination(1, 1, 1)})
				if err != nil || rec.Code != 200 {
					t.Fatalf("%s: status=%d err=%v", kind, rec.Code, err)
				}
				body := rec.Body.String()
				mustContain(t, body, `data-theme="review-child"`, "</html>")
				if customHeader {
					mustContain(t, body, `class="custom-shell"`)
				} else {
					mustContain(t, body, `class="default-site-header border-b border-line bg-panel"`, `items-center justify-between gap-3`)
				}
				for _, forbidden := range []string{"default-layout.css", "dt-header", "dt-grid", "dt-hero"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("%s leaked default-only layout %q", kind, forbidden)
					}
				}
				if kind == KindHome {
					mustContain(t, body, "gr-home-intro", "Hello")
				}
			}
		})
	}
}
