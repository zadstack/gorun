package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/commerce"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/theme"
)

var logSink io.Writer = io.Discard

// bootFull boots every module of this build (Modules()) on a temp data dir.
func bootFull(t *testing.T) (*kernel.App, *httptest.Server) {
	t.Helper()
	kernel.LogOutput = logSink
	t.Setenv(theme.EnvThemesDir, "../../themes") // the repo's themes (the binary reads ./themes or /app/themes)
	cfg, err := config.Load([]string{"--data", t.TempDir(), "--base-url", "http://gorun.test"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := kernel.Boot(context.Background(), cfg, Modules())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(app.Handler)
	t.Cleanup(func() { srv.Close(); app.Close() })
	return app, srv
}

func adminKey(t *testing.T, app *kernel.App, kind string) string {
	t.Helper()
	ctx := context.Background()
	users, _ := kernel.Get[auth.Service](app.Deps, kernel.ServiceAuth)
	keys, _ := kernel.Get[auth.KeyService](app.Deps, kernel.ServiceAPIKeys)
	u, err := users.Create(ctx, kind+"-owner@example.com", "Owner", "correct horse battery staple", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := keys.CreateKind(ctx, kind, "it", u.ID, []string{"*"}, 10000, "")
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// A2: with the real commerce and wpmigrate services booted, mcp binds
// list_products, run_migration_preview and get_migration_status.
func TestFullModulesMCPCommerceTools(t *testing.T) {
	app, srv := bootFull(t)
	cs, ok := kernel.Get[commerce.Service](app.Deps, kernel.ServiceCommerce)
	if !ok {
		t.Fatal("commerce service missing from Modules()")
	}
	if _, err := cs.SaveProduct(context.Background(), commerce.Product{Name: "Plan", Slug: "plan", PriceMinor: 4900, Currency: "AED", AccessMonths: 1}); err != nil {
		t.Fatal(err)
	}
	tr := &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: bearerRT{adminKey(t, app, auth.KeyKindAgent)}}}
	cl, err := sdk.NewClient(&sdk.Implementation{Name: "it", Version: "v0"}, nil).Connect(context.Background(), tr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	res, err := cl.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	for _, want := range []string{"list_products", "run_migration_preview", "get_migration_status"} {
		if !slices.Contains(names, want) {
			t.Fatalf("%s not registered against the real services: %v", want, names)
		}
	}
	out, err := cl.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_products", Arguments: map[string]any{}})
	if err != nil || out.IsError {
		t.Fatalf("list_products call: %v %+v", err, out)
	}
	text := ""
	for _, c := range out.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, `"slug":"plan"`) {
		t.Fatalf("list_products result: %s", text)
	}
}

// A4: a catalog theme (business) renders the site and the shop pages with commerce.
func TestFullModulesBusinessThemeWithCommerce(t *testing.T) {
	app, srv := bootFull(t)
	ctx := context.Background()
	if err := app.Deps.Settings.Set(ctx, "site.theme", "business"); err != nil {
		t.Fatal(err)
	}
	cs, _ := kernel.Get[commerce.Service](app.Deps, kernel.ServiceCommerce)
	if err := cs.SaveServer(ctx, commerce.Server{Name: "Billing", BaseURL: "https://billing.example.com", ClientID: "gorun", Secret: "s3cret-s3cret-s3cret"}); err != nil {
		t.Fatal(err)
	}
	servers, _ := cs.Servers(ctx)
	if len(servers) != 1 {
		t.Fatalf("servers: %v", servers)
	}
	if _, err := cs.SaveProduct(ctx, commerce.Product{Name: "MCP Pro", Slug: "mcp-pro", PriceMinor: 4900, Currency: "AED", Active: true,
		PlanCode: "pro", ServerID: servers[0].ID, AccessMonths: 1, Features: []string{"Unlimited tools"}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, want string }{
		{"/", "<html"}, {"/shop", "MCP Pro"}, {"/product/mcp-pro", "MCP Pro"}, {"/account", "<form"},
	} {
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), c.want) {
			t.Errorf("%s: %d (want 200 containing %q)\n%.600s", c.path, resp.StatusCode, c.want, b)
		}
		if !strings.Contains(string(b), `/themes/business/assets/site.css`) {
			t.Errorf("%s: not rendered by the business theme", c.path)
		}
	}
}
