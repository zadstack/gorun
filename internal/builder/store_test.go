package builder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings/settingsmod"
)

func builderTest(t *testing.T) (*service, content.Service, context.Context) {
	t.Helper()
	app, err := kernel.Boot(context.Background(), config.Config{Env: "dev", DataDir: t.TempDir(), Addr: "127.0.0.1:0", SecretKey: make([]byte, 32), BaseURL: "http://localhost"}, []kernel.Module{settingsmod.New(), auth.New(), content.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	for _, name := range []string{"migrations/0001_documents.sql", "migrations/0002_revisions.sql"} {
		raw, err := files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = app.Deps.DB.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	cs, _ := kernel.Get[content.Service](app.Deps, kernel.ServiceContent)
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "admin", Role: "admin", Status: auth.StatusActive})
	return &service{d: app.Deps}, cs, ctx
}
func TestDraftIsSeparateAndPublishPreservesFields(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Existing", Slug: "existing", Body: "<p>Legacy body</p>", Excerpt: "Keep excerpt", Status: "published", SEO: map[string]any{"description": "Keep SEO"}, Locale: "ar"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.Load(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(draft.Document.Sections) != 0 {
		t.Fatal("legacy inferred destructively")
	}
	sec := DefaultSection("hero", "ar")
	sec.Title = "New hero"
	doc := Document{Version: 1, Sections: []Section{sec}}
	saved, err := s.Save(ctx, it.ID, doc, draft.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := cs.Get(ctx, it.ID)
	if fresh.Body != it.Body || fresh.Status != it.Status {
		t.Fatal("saving changed public page")
	}
	if _, err = s.Save(ctx, it.ID, doc, draft.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save %v", err)
	}
	if err = s.Publish(ctx, it.ID, saved.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	fresh, err = cs.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fresh.Body, "New hero") || fresh.Template != "builder" || fresh.Excerpt != it.Excerpt || fresh.Locale != "ar" || fresh.SEO["description"] != "Keep SEO" {
		t.Fatalf("wrong publish %#v", fresh)
	}
	revs, err := cs.Revisions(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	legacy := false
	for _, r := range revs {
		if r.Snapshot.Body == it.Body {
			legacy = true
		}
	}
	if !legacy {
		t.Fatal("conversion lost legacy revision")
	}
	if err = s.Publish(ctx, it.ID, saved.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish %v", err)
	}
}
func TestBuilderConflictsWithContentChangesAndPermissions(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Page", AuthorID: "admin", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{DefaultSection("text", "en")}}, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	in := content.InputFromItem(it)
	in.Title = "Edited outside builder"
	if _, err = cs.Update(ctx, it.ID, in); err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, it.ID, draft.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwrote newer content %v", err)
	}
	for _, role := range []string{"author", "contributor", "subscriber"} {
		other := auth.WithUser(context.Background(), &auth.User{ID: "other", Role: role, Status: auth.StatusActive})
		if _, err = s.Load(other, it.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s read other draft %v", role, err)
		}
	}
	if _, err = s.Load(context.Background(), it.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous %v", err)
	}
	own := auth.WithUser(context.Background(), &auth.User{ID: "admin", Role: "contributor", Status: auth.StatusActive})
	if err = s.Publish(own, it.ID, draft.UpdatedAt); !errors.Is(err, ErrForbidden) {
		t.Fatalf("contributor publish %v", err)
	}
}

func TestResetRecoversAfterExternalEditWithoutChangingPublicPage(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Page", Body: "<p>First</p>", Status: "published"})
	if err != nil {
		t.Fatal(err)
	}
	doc := Document{Version: 1, Sections: []Section{DefaultSection("hero", "en")}}
	saved, err := s.Save(ctx, it.ID, doc, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	in := content.InputFromItem(it)
	in.Body = "<p>Changed by HTML editor</p>"
	current, err := cs.Update(ctx, it.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, it.ID, saved.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	if err = s.Reset(ctx, it.ID, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reset %v", err)
	}
	if err = s.Reset(ctx, it.ID, saved.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	blank, err := s.Load(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(blank.Document.Sections) != 0 || blank.BaseUpdatedAt != content.EditToken(current) {
		t.Fatal("reset failed to adopt current baseline")
	}
	after, _ := cs.Get(ctx, it.ID)
	if after.Body != current.Body {
		t.Fatal("reset mutated public HTML")
	}
	fresh, err := s.Save(ctx, it.ID, doc, blank.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, it.ID, fresh.UpdatedAt); err != nil {
		t.Fatal(err)
	}
}

func TestResetRestoresPublishedSectionsAndKeepsLiveBody(t *testing.T) {
	s, cs, ctx := builderTest(t)
	it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Page", Status: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	a := DefaultSection("hero", "en")
	a.Title = "Published A"
	published, err := s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{a}}, content.EditToken(it))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, it.ID, published.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	baseline, err := s.Load(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := a
	b.Title = "Unpublished B"
	draft, err := s.Save(ctx, it.ID, Document{Version: 1, Sections: []Section{b}}, baseline.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Reset(ctx, it.ID, baseline.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reset %v", err)
	}
	if err = s.Reset(ctx, it.ID, draft.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	restored, err := s.Load(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Document.Sections) != 1 || restored.Document.Sections[0].Title != "Published A" || restored.UpdatedAt == draft.UpdatedAt {
		t.Fatalf("lost published document %#v", restored)
	}
	live, err := cs.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(live.Body, "Published A") || strings.Contains(live.Body, "Unpublished B") {
		t.Fatal("reset changed live page")
	}
	if restored.BaseUpdatedAt != content.EditToken(live) {
		t.Fatal("reset baseline differs")
	}
	// External HTML remains untouched even when the builder snapshot is restored.
	input := content.InputFromItem(live)
	input.Body = "<p>External HTML</p>"
	external, err := cs.Update(ctx, it.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Publish(ctx, it.ID, restored.UpdatedAt); !errors.Is(err, ErrConflict) {
		t.Fatalf("external edit did not conflict %v", err)
	}
	if err = s.Reset(ctx, it.ID, restored.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	final, err := s.Load(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.BaseUpdatedAt != content.EditToken(external) || final.Document.Sections[0].Title != "Published A" {
		t.Fatal("explicit reset failed")
	}
	untouched, _ := cs.Get(ctx, it.ID)
	if untouched.Body != external.Body {
		t.Fatal("reset overwrote external body")
	}
}

func TestPublishRejectsEmptyHiddenAndUntitledDocuments(t *testing.T) {
	for _, kind := range []string{"empty", "hidden", "untitled"} {
		t.Run(kind, func(t *testing.T) {
			s, cs, ctx := builderTest(t)
			it, err := cs.Create(ctx, content.Input{Type: "page", Title: "Live page", Body: "<p>Keep current live body</p>", Status: "published"})
			if err != nil {
				t.Fatal(err)
			}
			doc := Document{Version: 1, Sections: []Section{}}
			switch kind {
			case "hidden":
				a := DefaultSection("hero", "en")
				a.Hidden = true
				doc.Sections = []Section{a}
			case "untitled":
				a := DefaultSection("text", "en")
				a.Title = " \n "
				doc.Sections = []Section{a}
			}
			saved, err := s.Save(ctx, it.ID, doc, content.EditToken(it))
			if err != nil {
				t.Fatalf("empty drafts must be allowed: %v", err)
			}
			if err = s.Publish(ctx, it.ID, saved.UpdatedAt); err == nil {
				t.Fatal("published blank page")
			}
			live, err := cs.Get(ctx, it.ID)
			if err != nil {
				t.Fatal(err)
			}
			if live.Body != it.Body || live.Status != it.Status {
				t.Fatal("rejected publish changed live page")
			}
			draft, err := s.Load(ctx, it.ID)
			if err != nil {
				t.Fatal(err)
			}
			if draft.HasPublished {
				t.Fatal("rejected draft got published snapshot")
			}
		})
	}
}
