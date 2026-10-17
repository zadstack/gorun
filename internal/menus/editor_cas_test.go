package menus_test

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestMenuEditorRejectsConcurrentTreeAndMetadata(t *testing.T) {
	s := boot(t, "editor")
	r, _ := s.form("/admin/menus", url.Values{"name": {"CAS menu"}, "slug": {"cas-menu"}})
	path := r.Header.Get("Location")
	token := func() string {
		_, page := s.do("GET", path, "", "")
		m := regexp.MustCompile(`name="edit_token" value="([^"]*)"`).FindStringSubmatch(page)
		if len(m) != 2 || len(m[1]) != 64 {
			t.Fatal("missing native token")
		}
		return m[1]
	}
	old := token()
	tree := `[{"kind":"custom","label":"New human link","url":"/human"}]`
	if r, _ := s.form(path+"/items", url.Values{"items": {tree}, "edit_token": {old}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	stale := `[{"kind":"custom","label":"Unsaved link","url":"/unsaved"}]`
	for _, tok := range []string{old, ""} {
		r, page := s.form(path+"/items", url.Values{"items": {stale}, "edit_token": {tok}})
		if r.StatusCode != 409 || !strings.Contains(page, `data-invalid="true"`) || (!strings.Contains(page, "This menu changed elsewhere") && !strings.Contains(page, "تغيّرت هذه القائمة")) || !strings.Contains(page, "Unsaved link") || !strings.Contains(page, `name="edit_token" value="`+tok+`"`) {
			t.Fatalf("stale items status=%d", r.StatusCode)
		}
	}
	r, page := s.form(path, url.Values{"name": {"Unsaved menu title"}, "slug": {"cas-menu"}, "edit_token": {old}})
	if r.StatusCode != 409 || !strings.Contains(page, "Unsaved menu title") {
		t.Fatalf("stale metadata=%d", r.StatusCode)
	}
	fresh := token()
	if r, _ := s.form(path, url.Values{"name": {"Current menu title"}, "slug": {"cas-menu"}, "edit_token": {fresh}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	if r, _ := s.form(path+"/items", url.Values{"items": {stale}, "edit_token": {fresh}}); r.StatusCode != 409 {
		t.Fatal("metadata change not guarded", r.StatusCode)
	}
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	m, err := svc.Get(context.Background(), "cas-menu")
	if err != nil || m.Name != "Current menu title" || len(m.Items) != 1 || m.Items[0].Label != "New human link" {
		t.Fatalf("concurrent state overwritten: %#v %v", m, err)
	}
}
