package builder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
)

// The Studio editor (content.edit_text, content.publish_text) changes texts
// and images where they stand and nothing else: a save or publish that
// touches the layout is refused on the server, whatever the client sent.
func TestStudioEditorChangesTextsOnly(t *testing.T) {
	h, cs, admin := adminFixture(t)
	s := h.svc
	it, err := cs.Create(admin, content.Input{Type: "page", Title: "Studio page", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	hero := DefaultSection("hero", "en")
	feats := DefaultSection("features", "en")
	feats.Link, feats.LinkLabel = "/more", "More"
	doc := Document{Version: 1, Sections: []Section{hero, feats}}
	saved, err := s.Save(admin, it.ID, doc, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(admin, it.ID, saved.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	studio := auth.WithUser(context.Background(), &auth.User{ID: "studio-1", Role: "studio", Status: auth.StatusActive, Name: "Client"})
	if !auth.Can(studio, PermEditText) || !auth.Can(studio, PermPublishText) || auth.Can(studio, "content.edit") || auth.Can(studio, "content.publish") {
		t.Fatal("the seeded studio role does not hold the text permissions only")
	}
	draft, err := s.Load(studio, it.ID)
	if err != nil {
		t.Fatalf("studio load: %v", err)
	}
	if _, err := s.editable(studio, it.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("studio may edit the layout: %v", err)
	}

	// Texts and images, section and entry: accepted.
	text := draft.Document
	text.Sections[0].Title = "Welcome"
	text.Sections[0].Image, text.Sections[0].ImageAlt = "/hero.png", "Hero"
	text.Sections[1].Items[0].Text = "First"
	text.Sections[1].LinkLabel = "Read more"
	draft, err = s.Save(studio, it.ID, text, draft.UpdatedAt)
	if err != nil {
		t.Fatalf("text save: %v", err)
	}
	// Layout: refused, and the draft stays as it was.
	for name, mutate := range map[string]func(d *Document){
		"link":    func(d *Document) { d.Sections[1].Link = "/elsewhere" },
		"hidden":  func(d *Document) { d.Sections[1].Hidden = true },
		"variant": func(d *Document) { d.Sections[0].Variant = "split" },
		"remove":  func(d *Document) { d.Sections = d.Sections[:1] },
		"add":     func(d *Document) { d.Sections = append(d.Sections, DefaultSection("cta", "en")) },
		"reorder": func(d *Document) { d.Sections[0], d.Sections[1] = d.Sections[1], d.Sections[0] },
		"item":    func(d *Document) { d.Sections[1].Items = d.Sections[1].Items[:2] },
	} {
		layout := copyDocument(t, draft.Document)
		mutate(&layout)
		if _, err := s.Save(studio, it.ID, layout, draft.UpdatedAt); !errors.Is(err, ErrTextOnly) {
			t.Errorf("%s: studio save = %v, want ErrTextOnly", name, err)
		}
	}
	if again, _ := s.Load(studio, it.ID); again.UpdatedAt != draft.UpdatedAt || again.Document.Sections[0].Title != "Welcome" {
		t.Fatal("a refused save changed the draft")
	}
	// The handler answers 403 builder.text_only.
	layout := copyDocument(t, draft.Document)
	layout.Sections[1].Hidden = true
	body, _ := json.Marshal(saveRequest{Document: layout, BaseUpdatedAt: draft.UpdatedAt})
	if rec := adminCall(studio, h.save, it.ID, string(body)); rec.Code != 403 || rec.Body.String() != "{\"error\":\"builder.text_only\"}\n" {
		t.Fatalf("handler: %d %s", rec.Code, rec.Body.String())
	}
	rec := adminCall(studio, h.draftJSON, it.ID, "")
	var meta struct {
		CanPublish bool `json:"can_publish"`
		CanLayout  bool `json:"can_layout"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil || rec.Code != 200 || !meta.CanPublish || meta.CanLayout {
		t.Fatalf("draft.json for studio: %d %s", rec.Code, rec.Body.String())
	}

	// Publishing text changes: accepted; publishing someone else's layout change: refused.
	if err := s.Publish(studio, it.ID, draft.UpdatedAt); err != nil {
		t.Fatalf("studio publish of text changes: %v", err)
	}
	live, _ := cs.Get(admin, it.ID)
	if live.Status != "published" || !containsAll(live.Body, "Welcome", "First") {
		t.Fatalf("published body: %s", live.Body)
	}
	draft, _ = s.Load(admin, it.ID)
	layout = copyDocument(t, draft.Document)
	layout.Sections[1].Hidden = true
	draft, err = s.Save(admin, it.ID, layout, draft.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Publish(studio, it.ID, draft.UpdatedAt); !errors.Is(err, ErrTextOnly) {
		t.Fatalf("studio published a layout change: %v", err)
	}
	if rec := adminCall(studio, h.publish, it.ID, `{"updated_at":"`+draft.UpdatedAt+`"}`); rec.Code != 403 {
		t.Fatalf("publish handler: %d", rec.Code)
	}
	// The REST plan tool tells the account the same (the API keeps content.edit for layout tools).
	if status := http.StatusForbidden; layoutFailure(ErrTextOnly).code != "forbidden" || status != 403 {
		t.Fatal("ErrTextOnly must map to forbidden")
	}
}

func copyDocument(t *testing.T, doc Document) Document {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out Document
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func containsAll(body string, parts ...string) bool {
	for _, p := range parts {
		if !contains(body, p) {
			return false
		}
	}
	return true
}

func contains(body, part string) bool {
	return len(part) == 0 || (len(body) >= len(part) && indexOf(body, part) >= 0)
}

func indexOf(body, part string) int {
	for i := 0; i+len(part) <= len(body); i++ {
		if body[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
