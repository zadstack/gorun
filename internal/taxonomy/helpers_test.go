package taxonomy

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func TestMain(m *testing.M) {
	kernel.LogOutput = io.Discard
	os.Exit(m.Run())
}

// bootApp boots settings, auth, content and taxonomy on a temp database.
func bootApp(t *testing.T) *kernel.App {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	app, err := kernel.Boot(context.Background(), cfg, []kernel.Module{settingsmod.New(), auth.New(), content.New(), New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

func services(t *testing.T, app *kernel.App) (content.Service, *service) {
	t.Helper()
	cs, ok := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content service missing")
	}
	ts, ok := kernel.Get[Service](app.Deps, kernel.ServiceTaxonomy)
	if !ok {
		t.Fatal("taxonomy service missing")
	}
	return cs, ts.(*service)
}

func mustTerm(t *testing.T, s *service, term Term) *Term {
	t.Helper()
	got, err := s.SaveTerm(context.Background(), term)
	if err != nil {
		t.Fatalf("save term %s: %v", term.Name, err)
	}
	return got
}

func servicesAuth(t *testing.T, app *kernel.App) (*auth.User, error) {
	return servicesAuthRole(t, app, "admin")
}

func servicesAuthRole(t *testing.T, app *kernel.App, role string) (*auth.User, error) {
	t.Helper()
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := us.Create(context.Background(), role+"@example.com", "U", "correct horse battery", role)
	if err != nil {
		t.Fatal(err)
	}
	return u, nil
}
