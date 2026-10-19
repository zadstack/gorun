package builder

import (
	"context"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/brand"
	"github.com/zadstack/gorun/internal/kernel"
)

// Both existing forms retain their own routes, validation and write boundaries.
type siteDesignView struct {
	Build, CanBuild                        bool
	Title, Theme, Accent, Background, Text string
}

func canBuildSite(ctx context.Context) bool {
	for _, permission := range []string{"settings.manage", "content.create", "content.edit", "content.publish", "menus.manage"} {
		if !auth.Can(ctx, permission) {
			return false
		}
	}
	return true
}
func designView(d *kernel.Deps, ctx context.Context, build bool) siteDesignView {
	identity := brand.Default()
	if err := d.Settings.Get(ctx, "builder.identity", &identity); err != nil || brand.Validate(identity) != nil {
		identity = brand.Default()
	}
	return siteDesignView{Build: build, CanBuild: canBuildSite(ctx), Title: d.Settings.GetString(ctx, "site.title", "GoRun"), Theme: d.Settings.GetString(ctx, "site.theme", "default"), Accent: identity.Accent, Background: identity.Background, Text: identity.Text}
}
func siteDesignHome(w http.ResponseWriter, r *http.Request) {
	destination := "/admin/appearance/identity"
	if canBuildSite(r.Context()) {
		destination = "/admin/site-builder"
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
