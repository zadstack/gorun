package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/zadstack/gorun/internal/i18n"
)

func TestNavigationCacheSharesProjectionPerRequestAndPath(t *testing.T) {
	b, err := i18n.Load()
	if err != nil {
		t.Fatal(err)
	}
	navCalls, pinCalls := 0, 0
	tpl, err := NewTemplates(os.DirFS("testdata"), false, b, Funcs{
		Nav: func(context.Context) []NavItem {
			navCalls++
			return []NavItem{{ID: "dash", Href: "/admin"}, {ID: "posts", Href: "/admin/posts", Group: "content"}}
		},
		PinnedNav: func(context.Context) []NavItem {
			pinCalls++
			return []NavItem{{ID: "posts", Href: "/admin/posts"}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	render := func(r *http.Request) {
		fm := tpl.funcMap(r, &reqState{}, nil)
		fm["nav"].(func() []NavGroup)()
		fm["workspaceTools"].(func() WorkspaceTools)()
	}

	var shared, other *http.Request
	NavigationCache(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		shared = r
		other = r.Clone(r.Context())
		other.URL.Path = "/admin/posts"
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/admin", nil))

	render(shared)
	render(shared) // a second template execution (a widget) in the same request
	if navCalls != 1 || pinCalls != 1 {
		t.Fatalf("same request loaded nav %d and pins %d times, want 1 each", navCalls, pinCalls)
	}
	render(other) // active styling differs, so another path gets its own projection
	if navCalls != 2 || pinCalls != 2 {
		t.Fatalf("other path reused the cached projection: nav %d pins %d", navCalls, pinCalls)
	}
	render(httptest.NewRequest("GET", "/admin", nil)) // without the middleware
	if navCalls != 3 {
		t.Fatalf("uncached request did not load navigation: %d", navCalls)
	}
}

func TestWorkspaceToolsNavigationURLsCoverShortcutSlots(t *testing.T) {
	groups := []NavGroup{{Items: []NavItem{{ID: "site.analytics", Href: "/admin/analytics"}, {ID: "b", Href: "/admin/b"}}}, {Key: "system", Items: []NavItem{{ID: "system.jobs", Href: "/admin/system/jobs"}}}}
	tools := workspaceTools(groups, nil, []QuickAccessItem{{NavItem: NavItem{Href: "/admin/pinned"}}})
	// A widget link is hidden when any visible owner, pins or the dashboard
	// directory, already links to it, so each destination has one owner.
	for _, href := range []string{"/admin/pinned", "/admin/system/jobs"} {
		if !tools.NavigationURLs[href] {
			t.Errorf("%s has a visible owner but is missing from navigation URLs", href)
		}
	}
	for _, href := range []string{"/admin/b", "/admin/analytics"} {
		if tools.NavigationURLs[href] {
			t.Errorf("%s has no visible owner on this page but counted as navigation", href)
		}
	}
}
