package admin_test

import (
	"context"
	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"testing"
)

func TestDashboardWorkspaceRespectsOwnership(t *testing.T) {
	for _, role := range []string{"author", "contributor"} {
		t.Run(role, func(t *testing.T) {
			s := boot(t, content.New())
			current := s.login("writer@example.com", role, "en")
			other := s.createUser("other@example.com", "editor", "en")
			cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
			own, err := cs.Create(auth.WithUser(context.Background(), current), content.Input{Type: "post", Title: "My workspace draft"})
			if err != nil {
				t.Fatal(err)
			}
			secret, err := cs.Create(auth.WithUser(context.Background(), other), content.Input{Type: "post", Title: "Other private draft"})
			if err != nil {
				t.Fatal(err)
			}
			_, page := s.do("GET", "/admin", nil)
			mustContain(t, page, `data-widget="workspace"`, own.Title, `href="/admin/canvases"`)
			mustNotContain(t, page, secret.Title, secret.ID)
			s.signIn(other.Email)
			_, page = s.do("GET", "/admin", nil)
			mustContain(t, page, own.Title, secret.Title)
		})
	}
}

func TestDashboardWorkspacePermissionAndEscaping(t *testing.T) {
	s := boot(t, content.New())
	owner := s.login("owner@example.com", "admin", "en")
	cs, _ := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	_, err := cs.Create(auth.WithUser(context.Background(), owner), content.Input{Type: "page", Title: `<script>alert(1)</script>`})
	if err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin", nil)
	mustContain(t, page, `&lt;script&gt;alert(1)&lt;/script&gt;`)
	mustNotContain(t, page, `<script>alert(1)</script>`)
	if _, err := s.app.Deps.DB.Exec(`INSERT INTO roles (slug, name_json, system) VALUES ('workspace_viewer', '{"en":"Workspace viewer"}', 0)`); err != nil {
		t.Fatal(err)
	}
	s.login("reader@example.com", "workspace_viewer", "en")
	_, page = s.do("GET", "/admin", nil)
	mustContain(t, page, `data-widget="canvas_gallery"`, `href="/admin/canvases"`)
	mustNotContain(t, page, `dashboard-content-table`, `&lt;script&gt;alert(1)&lt;/script&gt;`)
}
