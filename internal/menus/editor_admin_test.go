package menus_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/menus"
)

func TestMenuEditorLocaleAndHierarchyControls(t *testing.T) {
	s := boot(t, "editor")
	resp, _ := s.form("/admin/menus", url.Values{"name": {"Arabic menu"}, "slug": {"arabic-menu"}, "locale": {"ar"}, "location": {"primary"}})
	if resp.StatusCode != 303 {
		t.Fatal(resp.StatusCode)
	}
	svc, _ := kernel.Get[menus.Service](s.app.Deps, kernel.ServiceMenus)
	m, err := svc.Get(context.Background(), "arabic-menu")
	if err != nil || m.Locale != "ar" {
		t.Fatalf("locale lost: %v %#v", err, m)
	}
	path := resp.Header.Get("Location")
	_, page := s.do("GET", path, "", "")
	for _, want := range []string{"menu-editor.js", "menu-editor.css", "menuEditor()", "data-menu-key", "reparent(i", "dragOver($event"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing editor control %q", want)
		}
	}
	tree := `[{"label":"B","kind":"custom","url":"/b","children":[{"label":"A","kind":"custom","url":"/a","children":[{"label":"Child","kind":"custom","url":"/child"}]}]}]`
	if r, _ := s.form(path+"/items", url.Values{"items": {tree}}); r.StatusCode != 303 {
		t.Fatal(r.StatusCode)
	}
	m, err = svc.Get(context.Background(), "arabic-menu")
	if err != nil || m.Locale != "ar" || len(m.Items) != 1 || m.Items[0].Children[0].Children[0].Label != "Child" {
		t.Fatal("hierarchy or locale lost")
	}
	prior := s.csrf
	s.csrf = ""
	if r, _ := s.do("POST", path+"/items", url.Values{"items": {"[]"}}.Encode(), "application/x-www-form-urlencoded"); r.StatusCode != 403 {
		t.Fatal("missing CSRF accepted", r.StatusCode)
	}
	s.csrf = prior
}
