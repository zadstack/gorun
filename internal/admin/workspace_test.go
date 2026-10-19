package admin_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/admin"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestFilterNavForWorkspace(t *testing.T) {
	items := []kernel.NavItem{
		{ID: "settings", Group: "settings"},
		{ID: "settings.workspace", Group: "settings"},
		{ID: "canvases"},
		{ID: "agents.keys", Group: "agents"},
		{ID: "commerce.products", Group: "commerce"},
		{ID: "agents.audit", Group: "system"},
		{ID: "settings.connections", Group: "integrations"},
		{ID: "content.type.post", Group: "content"},
	}
	got := admin.FilterNavForWorkspace(admin.WorkspaceSimple, items)
	ids := make([]string, len(got))
	for i, n := range got {
		ids[i] = n.ID
	}
	want := []string{"settings", "settings.workspace", "content.type.post"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("simple filter: got %v want %v", ids, want)
	}
	if n := admin.FilterNavForWorkspace(admin.WorkspaceFull, items); len(n) != len(items) {
		t.Fatalf("full must keep all: %d", len(n))
	}
	if n := admin.FilterNavForWorkspace("", items); len(n) != len(items) {
		t.Fatalf("unset must keep all: %d", len(n))
	}
}

func TestWorkspaceChooserAndSimpleHidesAdvancedNav(t *testing.T) {
	s := boot(t)
	s.login("workspace-owner@example.com", "admin", "en")

	resp, page := s.do("GET", "/admin/workspace", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `name="mode"`) || !strings.Contains(page, `value="simple"`) {
		t.Fatalf("chooser: %d\n%s", resp.StatusCode, page)
	}
	mustContain(t, page, "Simple CMS", `value="full"`, `value="simple"`)

	resp, _ = s.post("/admin/workspace", url.Values{"mode": {"simple"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "/admin/workspace") {
		t.Fatalf("save simple: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := admin.WorkspaceMode(context.Background(), s.app.Deps.Settings); got != admin.WorkspaceSimple {
		t.Fatalf("mode=%q", got)
	}

	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/settings/general"`, `href="/admin/workspace"`)
	mustNotContain(t, page,
		`href="/admin/agents/keys"`,
		`href="/admin/canvases"`,
		`href="/admin/settings/connections"`,
		`data-widget="canvas_gallery"`,
		`data-widget="workspace_choice"`,
		`admin.dashboard.new_key`,
	)

	_, page = s.do("GET", "/admin/settings/general", nil)
	mustNotContain(t, page, `href="/admin/settings/ai"`)
	mustContain(t, page, `href="/admin/settings/mail"`)

	resp, _ = s.do("GET", "/admin/settings/ai", nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/admin/workspace" {
		t.Fatalf("ai blocked in simple: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	if resp, _ = s.post("/admin/workspace", url.Values{"mode": {"full"}}); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save full: %d", resp.StatusCode)
	}
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `href="/admin/agents/keys"`, `href="/admin/canvases"`)
}

func TestWorkspaceChoiceWidgetWhenUnset(t *testing.T) {
	s := boot(t)
	s.login("chooser-widget@example.com", "admin", "en")
	_, page := s.do("GET", "/admin", nil)
	mustContain(t, page, `data-widget="workspace_choice"`, `href="/admin/workspace"`)
}

func TestWorkspaceInvalidMode(t *testing.T) {
	s := boot(t)
	s.login("bad-mode@example.com", "admin", "en")
	resp, page := s.post("/admin/workspace", url.Values{"mode": {"nope"}})
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(page, "Choose Simple CMS or Agents") {
		t.Fatalf("invalid mode: %d\n%s", resp.StatusCode, page)
	}
}
