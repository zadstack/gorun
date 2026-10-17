package builder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/menus"
)

func starterTest(t *testing.T) (*starterHandler, content.Service, context.Context) {
	s, cs, ctx := builderTest(t)
	if err := db.Migrate(ctx, s.d.DB, "menus", menus.New().Migrations()); err != nil {
		t.Fatal(err)
	}
	if err := menus.New().Register(s.d); err != nil {
		t.Fatal(err)
	}
	return &starterHandler{d: s.d, svc: s}, cs, ctx
}
func starterInput(token, kind string) starterForm {
	return starterForm{Token: token, Starter: kind, Title: "Example Studio", Tagline: "Editable introduction", Locale: "ar", Accent: "#2563eb", Background: "#ffffff", Text: "#172033", Font: "system", Width: "standard"}
}
func reviewFor(t *testing.T, h *starterHandler, cs content.Service, ctx context.Context, run *starterRun) []starterExpectation {
	t.Helper()
	var out []starterExpectation
	for _, id := range run.Pages {
		it, err := cs.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		draft, err := h.svc.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, starterExpectation{ID: id, UpdatedAt: content.EditToken(it), DocumentUpdatedAt: draft.UpdatedAt})
	}
	return out
}
func TestStartersCreateCompleteDraftsWithoutChangingExistingSite(t *testing.T) {
	for _, kind := range []string{"company", "portfolio", "magazine"} {
		t.Run(kind, func(t *testing.T) {
			h, cs, ctx := starterTest(t)
			if err := h.d.Settings.Set(ctx, "site.title", "Old website"); err != nil {
				t.Fatal(err)
			}
			old, err := cs.Create(ctx, content.Input{Type: "page", Title: "Old home", Slug: "home", Status: "published", Body: "<p>Existing</p>"})
			if err != nil {
				t.Fatal(err)
			}
			token, err := h.issue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.create(ctx, starterInput(token.Token, kind))
			if err != nil {
				t.Fatal(err)
			}
			count := len(run.Pages)
			if count < 3 || run.MenuID == "" {
				t.Fatalf("incomplete: %+v", run)
			}
			for _, id := range run.Pages {
				it, err := cs.Get(ctx, id)
				if err != nil || it.Status != "draft" || it.Body == "" || it.Template != "builder" {
					t.Fatalf("invalid draft %+v %v", it, err)
				}
				draft, err := h.svc.Load(ctx, id)
				if err != nil || len(draft.Document.Sections) < 2 {
					t.Fatalf("empty document %+v %v", draft, err)
				}
			}
			if h.d.Settings.GetString(ctx, "site.title", "") != "Old website" {
				t.Fatal("draft changes active site")
			}
			original, err := cs.Get(ctx, old.ID)
			if err != nil || original.Body != old.Body || original.Status != "published" {
				t.Fatal("old content changed")
			}
			again, err := h.create(ctx, starterInput(token.Token, kind))
			if err != nil || again.Pages["home"] != run.Pages["home"] {
				t.Fatalf("retry duplicated %v", err)
			}
			page, err := cs.Query(ctx, content.Query{Type: "page", PerPage: 100})
			if err != nil || page.Total != count+1 {
				t.Fatalf("duplicate pages total=%d %v", page.Total, err)
			}
			if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err != nil {
				t.Fatal(err)
			}
			home, err := cs.Get(ctx, run.Pages["home"])
			// The magazine masthead carries the site title, so its cover invites reading instead of repeating it.
			heroes := []string{"Example Studio"}
			if kind == "magazine" {
				heroes = []string{"Stories worth your time", "قصص تستحق وقتك"}
			}
			hasHero := false
			for _, hero := range heroes {
				hasHero = hasHero || (home != nil && strings.Contains(home.Body, hero))
			}
			if err != nil || home.Status != "published" || !hasHero {
				t.Fatalf("missing public hero %+v %v", home, err)
			}
			if kind == "magazine" && strings.Contains(home.Body, "Example Studio") {
				t.Fatal("magazine cover repeats the masthead title")
			}
			if h.d.Settings.GetString(ctx, "site.front_page", "") != home.ID {
				t.Fatal("wrong front page")
			}
			draft, err := h.svc.Load(ctx, home.ID)
			if err != nil {
				t.Fatal(err)
			}
			draft.Document.Sections[0].Title = "Edited after activation"
			saved, err := h.svc.Save(ctx, home.ID, draft.Document, draft.UpdatedAt)
			if err != nil {
				t.Fatal(err)
			}
			if err := h.svc.Publish(ctx, home.ID, saved.UpdatedAt); err != nil {
				t.Fatalf("baseline not reset: %v", err)
			}
		})
	}
}
func TestStarterActivationRejectsChangedReviewAndKeepsAllDrafts(t *testing.T) {
	h, cs, ctx := starterTest(t)
	token, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(token.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	review := reviewFor(t, h, cs, ctx, run)
	draft, err := h.svc.Load(ctx, run.Pages["home"])
	if err != nil {
		t.Fatal(err)
	}
	draft.Document.Sections[0].Title = "Changed review"
	if _, err := h.svc.Save(ctx, run.Pages["home"], draft.Document, draft.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, review); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale review %v", err)
	}
	for _, id := range run.Pages {
		it, err := cs.Get(ctx, id)
		if err != nil || it.Status != "draft" {
			t.Fatalf("partial publish %+v %v", it, err)
		}
	}
}
func TestStarterTokenOwnershipAndBrandValidation(t *testing.T) {
	h, _, ctx := starterTest(t)
	token, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := auth.WithUser(context.Background(), &auth.User{ID: "other", Role: "admin", Status: auth.StatusActive})
	if _, err := h.create(other, starterInput(token.Token, "company")); !errors.Is(err, content.ErrForbidden) {
		t.Fatalf("foreign token %v", err)
	}
	f := starterInput(token.Token, "company")
	f.Accent = "red; background:url(https://evil.test)"
	if validateStarter(f) == nil {
		t.Fatal("unsafe color")
	}
	f = starterInput(token.Token, "company")
	f.Font = "evil-css"
	if validateStarter(f) == nil {
		t.Fatal("unsafe font")
	}
}

func TestStarterRetriesPartialPreparationWithoutDuplicates(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.d.DB.Exec(`CREATE TRIGGER starter_failure BEFORE INSERT ON content WHEN NEW.title='الخدمات' BEGIN SELECT RAISE(ABORT,'test interruption'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.create(ctx, starterInput(issued.Token, "company")); err == nil {
		t.Fatal("expected interrupted preparation")
	}
	partial, err := h.run(ctx, issued.Token)
	if err != nil || partial.Pages["home"] == "" {
		t.Fatalf("missing resumable state: %v", err)
	}
	_, err = h.d.DB.Exec(`DROP TRIGGER starter_failure`)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := h.create(ctx, starterInput(issued.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Pages["home"] != partial.Pages["home"] {
		t.Fatal("retry duplicated home")
	}
	pages, err := cs.Query(ctx, content.Query{Type: "page", PerPage: 100})
	if err != nil || pages.Total != 5 {
		t.Fatalf("duplicate partial pages: %d %v", pages.Total, err)
	}
}
func TestStarterActivationMissingMenuRollsBackPageBodiesAndSettings(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(issued.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	review := reviewFor(t, h, cs, ctx, run)
	_, err = h.d.DB.Exec(`DELETE FROM menus WHERE id=?`, run.MenuID)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.activateReviewed(ctx, run.Token, review); err == nil {
		t.Fatal("missing menu should abort activation")
	}
	for _, id := range run.Pages {
		it, err := cs.Get(ctx, id)
		if err != nil || it.Status != "draft" {
			t.Fatalf("partial publish %+v %v", it, err)
		}
	}
	if h.d.Settings.GetString(ctx, "site.front_page", "") != "" {
		t.Fatal("partial front-page setting")
	}
}

func TestStarterDoesNotReuseUnrelatedCollidingDraft(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	collision, err := cs.Create(ctx, content.Input{Type: "page", Slug: "home-" + issued.Token[len(issued.Token)-8:], Title: "Unrelated draft", Status: "draft", AuthorID: "admin", Locale: "ar", Body: "<p>Keep me</p>"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(issued.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	if run.Pages["home"] == collision.ID {
		t.Fatal("unrelated colliding draft reused")
	}
	original, err := cs.Get(ctx, collision.ID)
	if err != nil || original.Body != collision.Body {
		t.Fatal("colliding draft overwritten")
	}
}

func TestStarterActivationSnapshotSurvivesDiscardedEdits(t *testing.T) {
	h, cs, ctx := starterTest(t)
	issued, err := h.issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.create(ctx, starterInput(issued.Token, "company"))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.activateReviewed(ctx, run.Token, reviewFor(t, h, cs, ctx, run)); err != nil {
		t.Fatal(err)
	}
	id := run.Pages["home"]
	draft, err := h.svc.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	count := len(draft.Document.Sections)
	draft.Document.Sections[0].Title = "Unpublished change to discard"
	edited, err := h.svc.Save(ctx, id, draft.Document, draft.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Reset(ctx, id, edited.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	restored, err := h.svc.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Document.Sections) != count || restored.Document.Sections[0].Title != "Example Studio" {
		t.Fatalf("activation layout lost after discard: %+v", restored.Document)
	}
}
