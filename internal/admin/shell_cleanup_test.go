package admin_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Row actions sit behind one accessible menu button; the rotate choices are
// submit buttons of the unchanged rotate form, so the posted field is still
// "grace" and the route is unchanged.
func TestAPIKeyRowActionsUseOneMenu(t *testing.T) {
	s := boot(t)
	s.login("menu@example.com", "admin", "en")
	resp, page := s.post("/admin/agents/keys", url.Values{"name": {"menu agent"}, "scopes": {"content.view"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	plain := plaintextRe.FindString(page)
	var id string
	s.app.Deps.DB.QueryRow(`SELECT id FROM api_keys WHERE prefix = ?`, plain[3:11]).Scan(&id)
	_, page = s.do("GET", "/admin/agents/keys", nil)
	mustContain(t, page,
		`data-row-menu`, `id="key-actions-`+id+`"`, `aria-haspopup="menu"`, `aria-controls="key-menu-`+id+`"`,
		`<span class="sr-only">Actions for menu agent</span>`,
		`action="/admin/agents/keys/`+id+`/rotate"`, `name="grace" value="0"`, `name="grace" value="3600"`, `name="grace" value="86400"`,
		`action="/admin/agents/keys/`+id+`/revoke"`, `row-menu-item row-menu-item-danger`,
		`Rotate, revoke the old key now`, `Rotate, old key works 1 hour`)
	mustNotContain(t, page, `<select id="grace-`)
	if strings.Count(page, `aria-haspopup="menu"`) != 1 {
		t.Fatalf("one menu per key row expected")
	}

	// A menu choice posts the same field the select used to.
	if resp, _ := s.post("/admin/agents/keys/"+id+"/rotate", url.Values{"grace": {"3600"}}); resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate with grace: %d", resp.StatusCode)
	}
}

// Destructive actions live in one "Danger zone" section at the bottom of the
// page, after the form and its side panels.
func TestDetailPagesEndWithDangerZone(t *testing.T) {
	s := boot(t)
	me := s.login("danger@example.com", "admin", "ar")
	other := s.createUser("target@example.com", "editor", "en")
	for path, action := range map[string]string{
		"/admin/users/" + other.ID: `/admin/users/` + other.ID + `/delete`,
	} {
		resp, page := s.do("GET", path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		zone := strings.Index(page, `class="danger-zone"`)
		if zone < 0 || zone < strings.Index(page, `data-user-two-factor`) {
			t.Fatalf("%s: danger zone missing or not after the side panels", path)
		}
		mustContain(t, page[zone:], `منطقة الخطر`, `action="`+action+`"`, `btn btn-danger`)
		if strings.Count(page, `action="`+action+`"`) != 1 {
			t.Fatalf("%s: delete action repeated", path)
		}
	}
	// Your own account has nothing to delete here.
	resp, page := s.do("GET", "/admin/users/"+me.ID, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(page, `class="danger-zone"`) {
		t.Fatalf("own account shows a danger zone: %d", resp.StatusCode)
	}
}
