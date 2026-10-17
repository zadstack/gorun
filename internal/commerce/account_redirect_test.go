package commerce

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/api"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
	"github.com/zadstack/gorun/internal/site"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// bootReal boots the real stack with commerce on dataDir (reusable to
// simulate a restart).
func bootReal(t *testing.T, dataDir string) *kernel.App {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: dataDir, Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://shop.test", Theme: "default"}
	app, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), taxonomy.New(),
		media.New(), menus.New(), redirects.New(), seo.New(), theme.New(theme.WithThemesDir("../../themes")), site.New(), api.New(),
		admin.New(), New()}, kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func myAccountRedirects(t *testing.T, app *kernel.App) []redirects.Redirect {
	t.Helper()
	rs, _ := kernel.Get[redirects.Service](app.Deps, kernel.ServiceRedirects)
	list, _, err := rs.List(context.Background(), redirects.ListQuery{Search: "/my-account", PerPage: 100})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// M10: with commerce booted, /my-account and /my-account/ redirect (301) to
// /account; the seed is idempotent across restarts and a deliberate
// delete is not undone.
func TestDefaultMyAccountRedirect(t *testing.T) {
	dir := t.TempDir()
	app := bootReal(t, dir)
	list := myAccountRedirects(t, app)
	if len(list) != 1 || list[0].ToURL != "/account" || list[0].Status != 301 || !list[0].Enabled {
		t.Fatalf("seeded redirects: %+v", list)
	}
	for _, p := range []string{"/my-account", "/my-account/"} {
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/account" {
			t.Fatalf("GET %s: %d %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	id := list[0].ID
	app.Close()

	app = bootReal(t, dir) // restart: no duplicate
	if list := myAccountRedirects(t, app); len(list) != 1 || list[0].ID != id {
		t.Fatalf("after restart: %+v", list)
	}
	rs, _ := kernel.Get[redirects.Service](app.Deps, kernel.ServiceRedirects)
	if err := rs.Delete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	app.Close()
	app = bootReal(t, dir) // a deleted default stays deleted
	defer app.Close()
	if list := myAccountRedirects(t, app); len(list) != 0 {
		t.Fatalf("deleted default re-created: %+v", list)
	}
}

// A page that owns /my-account prevents the default redirect.
func TestMyAccountRedirectSkippedWhenAPageOwnsThePath(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Env: "dev", DataDir: dir, Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://shop.test"}
	pre, err := kernel.BootWithOptions(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New()},
		kernel.BootOptions{StartJobs: false})
	if err != nil {
		t.Fatal(err)
	}
	cs, _ := kernel.Get[content.Service](pre.Deps, kernel.ServiceContent)
	if _, err := cs.Create(context.Background(), content.Input{Type: "page", Slug: "my-account", Title: "My account", Status: "published"}); err != nil {
		t.Fatal(err)
	}
	pre.Close()
	app := bootReal(t, dir)
	defer app.Close()
	if list := myAccountRedirects(t, app); len(list) != 0 {
		t.Fatalf("redirect seeded over a page: %+v", list)
	}
}
