package admin

import (
	"net/http"

	"github.com/zadstack/gorun/internal/theme"
)

// designComponents renders /admin/design/components: every shared component
// of the admin in each of its states, and the site components the core
// stylesheet scopes under .gp-core, for review in both languages. The visual
// regression suite (e2e/tests/visual-regression.spec.ts) takes its baselines
// here. A reference page behind the settings permission, reached by URL and
// from docs/ADMIN_COMPONENTS.md, not a navigation destination.
func (h *handlers) designComponents(w http.ResponseWriter, r *http.Request) {
	h.tpl.Render(w, r, "pages/design/components.html", struct{ CoreCSS string }{theme.CoreCSSURL()})
}
