package content_test

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

// bootApp boots settings, auth and content (plus any extra modules, in
// boot order) on a temp database.
func bootApp(t *testing.T, extra ...kernel.Module) *kernel.App {
	t.Helper()
	cfg := config.Config{Env: "dev", DataDir: t.TempDir(), Addr: ":0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}
	mods := append([]kernel.Module{settingsmod.New(), auth.New(), content.New()}, extra...)
	app, err := kernel.Boot(context.Background(), cfg, mods)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

func mustUser(t *testing.T, app *kernel.App, email, role string) *auth.User {
	t.Helper()
	us, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	u, err := us.Create(context.Background(), email, "User "+role, "correct horse battery", role)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func contentService(t *testing.T, app *kernel.App) content.Service {
	t.Helper()
	cs, ok := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content service not registered")
	}
	return cs
}
