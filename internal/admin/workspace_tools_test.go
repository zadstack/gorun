package admin_test

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

func TestWorkspaceToolsRespectPermissionsAndModuleAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, role        string
		media, management bool
	}{
		{"administrator", "admin", true, true},
		{"editor", "editor", true, false},
		{"without media module", "admin", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var modules []kernel.Module
			if tc.media {
				modules = append(modules, media.New())
			}
			s := boot(t, modules...)
			s.login("tools@example.test", tc.role, "en")
			response, page := s.do("GET", "/admin", nil)
			if response.StatusCode != 200 {
				t.Fatalf("dashboard: %d", response.StatusCode)
			}
			// The dashboard directory lists administration tools; content
			// destinations such as media belong to the Content tabs.
			for id, want := range map[string]bool{"media": false, "users": tc.management, "system.health": tc.management} {
				if got := strings.Contains(page, `data-workspace-tool="`+id+`"`); got != want {
					t.Errorf("direct tool %s present = %v, want %v", id, got, want)
				}
			}
			if tc.media {
				if response, page = s.do("GET", "/admin/media", nil); response.StatusCode != 200 || !strings.Contains(page, `data-nav-id="media"`) {
					t.Errorf("media library lost its Content tab: %d", response.StatusCode)
				}
			}
		})
	}
}

// Each destination has one visible owner: the rail holds agents only, the
// topbar links only the public site and the sidebar has no "Open directly"
// list repeating section tabs.
func TestWorkspaceShellRepeatsNoSectionDestinations(t *testing.T) {
	s := boot(t, media.New())
	s.login("shell@example.test", "admin", "en")
	for _, path := range []string{"/admin", "/admin/media", "/admin/settings/general", "/admin/agents/keys"} {
		response, page := s.do("GET", path, nil)
		if response.StatusCode != 200 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
		mustNotContain(t, page, `workspace-rail-tools`, `workspace-rail-footer`, `workspace-sidebar-tools`, `data-workspace-mobile-tools`)
		start := strings.Index(page, `class="workspace-top-tools"`)
		if start < 0 {
			t.Fatalf("%s: topbar site link missing", path)
		}
		top := page[start : start+strings.Index(page[start:], `</nav>`)]
		if n := strings.Count(top, "<a "); n != 1 || !strings.Contains(top, `href="/"`) {
			t.Errorf("%s: topbar should link only the public site, got %d links: %s", path, n, top)
		}
		rail := page[strings.Index(page, `class="workspace-tools-rail"`):]
		rail = rail[:strings.Index(rail, `</dialog>`)]
		if strings.Contains(rail, `data-workspace-tool=`) {
			t.Errorf("%s: rail carries page shortcuts", path)
		}
	}
}
