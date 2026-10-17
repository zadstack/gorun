package admin_test

import (
	"context"
	"net/http"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"testing"
)

func TestGlobalSearch(t *testing.T) {
	s := boot(t)
	s.login("admin@example.com", "admin", "en")
	s.createUser("findme@example.com", "editor", "")
	resp, frag := s.do("GET", "/admin/x/search?q=findme", nil, "HX-Request", "true")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	mustContain(t, frag, "findme@example.com", "no content module")
	mustNotContain(t, frag, "<html")

	_, frag = s.do("GET", "/admin/x/search?q=audit", nil, "HX-Request", "true")
	mustContain(t, frag, `href="/admin/agents/audit"`)
	_, frag = s.do("GET", "/admin/x/search?q=mail", nil, "HX-Request", "true")
	mustContain(t, frag, `href="/admin/settings/mail"`)
	_, frag = s.do("GET", "/admin/x/search?q=a", nil, "HX-Request", "true")
	mustContain(t, frag, "at least 2 characters")

	// Full-page fallback for the topbar form.
	resp, page := s.do("GET", "/admin/search?q=findme", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search page: %d", resp.StatusCode)
	}
	mustContain(t, page, "<html", "findme@example.com")

	// Editors do not see users or pages they cannot open.
	s.login("ed@example.com", "editor", "en")
	_, frag = s.do("GET", "/admin/x/search?q=findme", nil, "HX-Request", "true")
	mustNotContain(t, frag, "findme@example.com")
	_, frag = s.do("GET", "/admin/x/search?q=audit", nil, "HX-Request", "true")
	mustNotContain(t, frag, `href="/admin/agents/audit"`)
}

func TestGlobalSearchRespectsContentOwnership(t *testing.T) {
	for _, role := range []string{"author", "contributor"} {
		t.Run(role, func(t *testing.T) {
			s := boot(t, content.New())
			current := s.login("writer@example.com", role, "en")
			other := s.createUser("editor@example.com", "editor", "en")
			cs, ok := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
			if !ok {
				t.Fatal("missing content service")
			}
			own, err := cs.Create(auth.WithUser(context.Background(), current), content.Input{Type: "post", Title: "Permission needle own"})
			if err != nil {
				t.Fatal(err)
			}
			for _, status := range []content.Status{"draft", "private"} {
				otherItem, err := cs.Create(auth.WithUser(context.Background(), other), content.Input{Type: "post", Title: "Permission needle other " + string(status), Status: status})
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{"/admin/content/post", "/admin/search?q=Permission+needle", "/admin/x/search?q=Permission+needle"} {
					resp, body := s.do("GET", path, nil)
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("%s: status %d", path, resp.StatusCode)
					}
					mustContain(t, body, own.Title)
					mustNotContain(t, body, otherItem.Title, otherItem.ID)
				}
			}
			s.signIn(other.Email)
			_, body := s.do("GET", "/admin/search?q=Permission+needle", nil)
			mustContain(t, body, "Permission needle other draft", "Permission needle other private", own.Title)
		})
	}
}
