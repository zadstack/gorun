package ext_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
)

// Installed extensions appear in the module catalog with their manifest
// version, hooks, permissions, configuration need and activation state;
// compatibility is stated as not verified.
func TestInstalledExtensionsAppearInModuleCatalog(t *testing.T) {
	e := boot(t)
	endpoint := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(endpoint.Close)
	m := manifest("hello", endpoint.URL, hook("content.published", "event", 0))
	m["settings"] = []map[string]any{{"key": "greeting", "label": map[string]string{"en": "Greeting"}, "type": "text", "default": ""}}
	e.install(m)
	catalog := func() []admin.CatalogEntry {
		t.Helper()
		list := []admin.CatalogEntry{}
		v, err := e.app.Deps.Hooks.Apply(e.ctx, admin.ModuleCatalogFilter, &list)
		if err != nil {
			t.Fatal(err)
		}
		return *v.(*[]admin.CatalogEntry)
	}
	entries := catalog()
	i := slices.IndexFunc(entries, func(x admin.CatalogEntry) bool { return x.ID == "ext:"+m["slug"].(string) })
	if i < 0 {
		t.Fatalf("extension missing from catalog: %+v", entries)
	}
	x := entries[i]
	if x.Kind != admin.CatalogKindExtension || x.Source != admin.CatalogSourceExternal || x.Version != m["version"] || x.State.Status != "installed" || x.State.Enabled {
		t.Fatalf("installed entry: %+v", x)
	}
	if !slices.Contains(x.Registered.HookSubscriptions, "content.published") || !slices.Contains(x.Permissions, "content.view") {
		t.Fatalf("hooks/permissions: %+v", x.Registered)
	}
	if x.Configuration.Status != "incomplete" || x.Compatibility == "" || x.Links["extension"] == "" {
		t.Fatalf("configuration/compat/links: %+v", x)
	}
	if err := e.svc.Activate(e.ctx, m["slug"].(string)); err != nil {
		t.Fatal(err)
	}
	entries = catalog()
	x = entries[slices.IndexFunc(entries, func(x admin.CatalogEntry) bool { return x.ID == "ext:"+m["slug"].(string) })]
	if x.State.Status != "active" || !x.State.Enabled || !slices.Contains(x.Jobs.Kinds, "ext.deliver") {
		t.Fatalf("active entry: %+v", x.State)
	}
}
