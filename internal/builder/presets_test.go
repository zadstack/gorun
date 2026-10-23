package builder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
)

// Every purpose preset is a valid, publishable layout whose public output is
// only the author's title (and, for contact, the real contact block): the
// other sections start hidden and empty, so nothing invented is published.
func TestPurposePresetsPublishOnlyWhatTheAuthorWrote(t *testing.T) {
	for _, purpose := range []string{"about", "service", "methodology", "contact"} {
		for _, locale := range []string{"ar", "en"} {
			t.Run(purpose+"/"+locale, func(t *testing.T) {
				doc, err := PurposePreset(purpose, "  Atlas Studio  ", locale)
				if err != nil {
					t.Fatal(err)
				}
				if err := ValidatePublication(doc); err != nil {
					t.Fatalf("a fresh preset must be publishable: %v", err)
				}
				hero := doc.Sections[0]
				if hero.Kind != "hero" || hero.Library != SectionLibrary || hero.Title != "Atlas Studio" || hero.Hidden {
					t.Fatalf("hero = %+v", hero)
				}
				for _, s := range doc.Sections[1:] {
					if s.Text != "" || len(s.Items) != 0 || s.Link != "" || s.Image != "" {
						t.Errorf("%s section carries content the author did not write: %+v", s.Kind, s)
					}
					if s.Hidden != (s.Kind != "contact") {
						t.Errorf("%s hidden = %v", s.Kind, s.Hidden)
					}
				}
				html, err := Render(doc)
				if err != nil {
					t.Fatal(err)
				}
				for _, placeholder := range []string{"Add your section content here", "اكتب محتوى القسم هنا", "Item 1", "عنصر 1", "Learn more"} {
					if strings.Contains(html, placeholder) {
						t.Errorf("published HTML contains the placeholder %q", placeholder)
					}
				}
				if strings.Count(html, "<section") != map[bool]int{true: 2, false: 1}[purpose == "contact"] {
					t.Errorf("only the hero (and the contact block) may render:\n%s", html)
				}
			})
		}
	}
}

func TestPurposePresetRejectsBadInput(t *testing.T) {
	for _, tc := range []struct{ purpose, title string }{
		{"pricing", "Prices"}, {"../x", "X"}, {"about", "   "}, {"about", strings.Repeat("ع", 201)},
	} {
		if _, err := PurposePreset(tc.purpose, tc.title, "ar"); !errors.Is(err, ErrPreset) {
			t.Errorf("PurposePreset(%q, %q) err = %v, want ErrPreset", tc.purpose, tc.title, err)
		}
	}
}

// The service creates a private draft page with the purpose field and the
// preset as its builder draft, and refuses users who cannot create content
// (a customer).
// The REST operation answers 201 with the new page and edit URL.
func TestCreatePurposePageDraftAndAPI(t *testing.T) {
	f := newLayoutFixture(t)
	admin := f.user(t, "admin")
	ctx := auth.WithUser(context.Background(), admin)
	page, err := f.svc.CreatePurposePage(ctx, PurposePageInput{Purpose: "service", Title: "مراجعة المواقع", Locale: "ar"})
	if err != nil {
		t.Fatal(err)
	}
	it, err := f.cs.Get(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if it.Status != "draft" || it.Template != "builder" || it.Fields["purpose"] != "service" || it.Locale != "ar" || it.Title != "مراجعة المواقع" {
		t.Fatalf("created page = status %s template %s purpose %v locale %s", it.Status, it.Template, it.Fields["purpose"], it.Locale)
	}
	draft, err := f.svc.Load(ctx, page.ID)
	if err != nil || len(draft.Document.Sections) != 5 || page.EditURL != "/admin/builder/"+page.ID {
		t.Fatalf("draft = %+v, %v; edit url %q", draft, err, page.EditURL)
	}

	customer := &auth.User{ID: "customer", Role: auth.RoleCustomer, Status: auth.StatusActive}
	if _, err := f.svc.CreatePurposePage(auth.WithUser(context.Background(), customer), PurposePageInput{Purpose: "about", Title: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a customer created a page: %v", err)
	}

	status, body := f.request(t, http.DefaultClient, "POST", "/api/v1/builder/pages", f.token(t, admin, []string{"content.create", "content.edit"}), "",
		map[string]string{"purpose": "contact", "title": "Contact", "locale": "en"})
	if status != 201 {
		t.Fatalf("POST /api/v1/builder/pages = %d %s", status, body)
	}
	var out struct {
		Data PurposePage `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Data.ID == "" {
		t.Fatalf("response %s: %v", body, err)
	}
	status, body = f.request(t, http.DefaultClient, "POST", "/api/v1/builder/pages", f.token(t, admin, []string{"content.create", "content.edit"}), "",
		map[string]string{"purpose": "pricing", "title": "Prices"})
	if status != 422 {
		t.Fatalf("unknown purpose = %d %s, want 422", status, body)
	}
}
