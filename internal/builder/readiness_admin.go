package builder

import (
	"errors"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/httpx"
	"github.com/zadstack/gorun/internal/kernel"
)

func registerReadiness(d *kernel.Deps) error {
	tpl, ok := kernel.Get[*httpx.Templates](d, kernel.ServiceAdminTemplates)
	if !ok {
		return errors.New("builder: admin templates unavailable")
	}
	d.Admin.AddNav(kernel.NavItem{ID: "builder.readiness", LabelKey: "readiness.title", Icon: "list-checks", Href: "/admin/appearance/readiness", Permission: "settings.manage", Group: "appearance", Position: 20})
	d.Router.Admin.With(auth.RequirePerm("settings.manage")).Get("/appearance/readiness", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		tpl.RenderWith(w, r, http.StatusOK, "admin", "pages/builder/readiness.html", buildReadiness(r.Context(), d))
	})
	return nil
}
