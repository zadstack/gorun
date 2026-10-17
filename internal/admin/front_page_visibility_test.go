package admin_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
)

func TestFrontPageSettingRequiresPublicContentType(t *testing.T) {
	s := boot(t, content.New())
	s.login("admin@example.com", "admin", "en")
	cs, ok := kernel.Get[content.Service](s.app.Deps, kernel.ServiceContent)
	if !ok {
		t.Fatal("content service missing")
	}
	ctx := context.Background()
	if err := cs.SaveType(ctx, content.Type{Slug: "internal_note", Public: false, Name: map[string]string{"en": "Internal"}}); err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "internal_note", Title: "Internal", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	form := generalForm("http://example.test")
	form.Set("front_page", it.ID)
	if response, _ := s.post("/admin/settings/general", form); response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("non-public front page accepted: %d", response.StatusCode)
	}
	if id := s.app.Deps.Settings.GetString(ctx, "site.front_page", ""); id != "" {
		t.Fatal("rejected front page was stored")
	}
	page, err := cs.Create(ctx, content.Input{Type: "page", Title: "Public", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	form.Set("front_page", page.ID)
	if response, _ := s.post("/admin/settings/general", form); response.StatusCode != http.StatusSeeOther {
		t.Fatalf("public page refused: %d", response.StatusCode)
	}
}
