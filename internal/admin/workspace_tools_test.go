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
			for id, want := range map[string]bool{"media": tc.media, "users": tc.management, "settings": tc.management, "system.health": tc.management} {
				if got := strings.Contains(page, `data-workspace-tool="`+id+`"`); got != want {
					t.Errorf("direct tool %s present = %v, want %v", id, got, want)
				}
			}
		})
	}
}
