package content_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestAdminContentUXFiltersPreserveOwnershipAndState(t *testing.T) {
	for _, role := range []string{"admin", "author"} {
		t.Run(role, func(t *testing.T) {
			s := loggedIn(t, role)
			cs := contentService(t, s.app)
			us, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
			users, _, e := us.List(context.Background(), auth.ListQuery{PerPage: 100})
			if e != nil {
				t.Fatal(e)
			}
			owner := ""
			for _, u := range users {
				if u.Email == role+"@example.com" {
					owner = u.ID
				}
			}
			other, e := us.Create(context.Background(), "other-filter@example.com", "Other Writer", "correct horse battery", "author")
			if e != nil {
				t.Fatal(e)
			}
			for _, x := range []struct{ title, lang, author, status string }{{"Mine English", "en", owner, "draft"}, {"Mine Arabic", "ar", owner, "draft"}, {"Other English", "en", other.ID, "draft"}, {"Review English", "en", owner, "pending"}} {
				if _, e = cs.Create(context.Background(), content.Input{Type: "post", Title: x.title, Locale: x.lang, AuthorID: x.author, Status: content.Status(x.status)}); e != nil {
					t.Fatal(e)
				}
			}
			response, page := s.do("GET", "/admin/content/post?locale=en&view=mine", nil, nil)
			if response.StatusCode != 200 || !strings.Contains(page, "Mine English") || strings.Contains(page, "Mine Arabic") || strings.Contains(page, "Other English") || strings.Contains(page, "Review English") {
				t.Fatal("my drafts/language filter failed")
			}
			_, page = s.do("GET", "/admin/content/post?locale=en&view=review", nil, nil)
			if !strings.Contains(page, "Review English") || strings.Contains(page, "Mine English") {
				t.Fatal("review filter failed")
			}
			_, page = s.do("GET", "/admin/content/post?author="+url.QueryEscape(other.ID), nil, nil)
			if role == "author" && strings.Contains(page, "Other English") {
				t.Fatal("author filter escaped ownership")
			}
			if role == "admin" && !strings.Contains(page, "Other English") {
				t.Fatal("admin author filter failed")
			}
		})
	}
}
func TestPublishedEditorHasOneClearUpdateAction(t *testing.T) {
	s := loggedIn(t, "admin")
	cs := contentService(t, s.app)
	it, e := cs.Create(context.Background(), content.Input{Type: "page", Title: "Published UX", Status: "published"})
	if e != nil {
		t.Fatal(e)
	}
	_, page := s.do("GET", "/admin/content/page/"+it.ID, nil, nil)
	if !strings.Contains(page, `data-save-label`) || strings.Contains(page, `name="intent" value="publish"`) {
		t.Fatal("ambiguous duplicate publishing action")
	}
	response, _ := s.do("POST", "/admin/content/page/"+it.ID, url.Values{"_csrf": {s.csrf(page)}, "edit_token": {editField(t, page, "edit_token")}, "terms_token": {editField(t, page, "terms_token")}, "title": {"Published UX updated"}, "status": {"published"}, "intent": {"save"}}, nil)
	if response.StatusCode != 303 {
		t.Fatal(response.StatusCode)
	}
	saved, e := cs.Get(context.Background(), it.ID)
	if e != nil || saved.Status != "published" || saved.Title != "Published UX updated" {
		t.Fatal("update semantics changed", e)
	}
}

func TestAdminPageTreeIsFilteredAndKeepsSelectionPermission(t *testing.T) {
	s := loggedIn(t, "author")
	cs := contentService(t, s.app)
	us, _ := kernel.Get[auth.Service](s.app.Deps, kernel.ServiceAuth)
	users, _, _ := us.List(context.Background(), auth.ListQuery{PerPage: 100})
	owner := ""
	for _, u := range users {
		if u.Email == "author@example.com" {
			owner = u.ID
		}
	}
	parent, e := cs.Create(context.Background(), content.Input{Type: "page", Title: "Owned parent", AuthorID: owner, Locale: "en"})
	if e != nil {
		t.Fatal(e)
	}
	child, e := cs.Create(context.Background(), content.Input{Type: "page", Title: "Owned child", ParentID: parent.ID, AuthorID: owner, Locale: "en"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cs.Create(context.Background(), content.Input{Type: "page", Title: "Filtered Arabic page", ParentID: parent.ID, AuthorID: owner, Locale: "ar"}); e != nil {
		t.Fatal(e)
	}
	response, page := s.do("GET", "/admin/content/page?layout=tree&locale=en", nil, nil)
	if response.StatusCode != 200 || !strings.Contains(page, `data-tree-row="`+child.ID+`"`) || !strings.Contains(page, `<details open>`) || !strings.Contains(page, "Owned parent") || strings.Contains(page, "Filtered Arabic page") {
		t.Fatal("filtered page tree failed", response.StatusCode)
	}
}
