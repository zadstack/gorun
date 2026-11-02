package theme

import (
	"html"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestNativeUIStylesReachPublicAndSandboxPreviewsOnlyWhenUsed(t *testing.T) {
	e := newEnv(t, "../../themes")
	e.setting("site.locale", "ar")
	for _, tc := range []struct {
		name, body string
		used       bool
	}{
		{"legacy", `<section class="gp-s-hero"><h1>Existing page</h1></section>`, false},
		{"task entry", taskSearchFixture("/search", "q"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := &content.Item{ID: "preview", Type: "page", Title: "Preview", Slug: "preview", Template: "builder", Locale: "ar", Status: "published", Body: tc.body}
			rec, err := e.render(kernel.KindRequest{Kind: KindPage, Template: "builder"}, &Context{Content: page})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(rec.Body.String(), NativeUICSSURL()); got != map[bool]int{false: 0, true: 1}[tc.used] {
				t.Fatalf("conditional public stylesheet count: %d", got)
			}
			preview, err := e.svc.RenderBuilderThemePreview(t.Context(), page, BuilderPreviewOptions{Theme: "default"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(preview, NativeUICSSPath) {
				t.Fatal("sandbox retained a blocked external stylesheet")
			}
			if strings.Contains(preview, ".gp-task-search form") != tc.used {
				t.Fatal("sandbox did not preserve the public native layout styling")
			}
			if tc.used && !strings.Contains(html.UnescapeString(preview), "form-action 'none'") {
				t.Fatal("preview must remain read-only")
			}
			fallback, err := e.svc.RenderBuilderBody(t.Context(), page)
			if err != nil {
				t.Fatal(err)
			}
			if tc.used && (!strings.Contains(fallback, ".gp-task-search form") || strings.Contains(fallback, NativeUICSSPath)) {
				t.Fatal("body-only sandbox lost its native stylesheet")
			}
		})
	}
}
