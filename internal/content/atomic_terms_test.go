package content_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
	"testing"
)

func TestAtomicUpdatePreservesPrestateTermsAndRejectsConcurrentTermChanges(t *testing.T) {
	app := bootApp(t, taxonomy.New())
	ctx := context.Background()
	cs := contentService(t, app)
	tax, _ := kernel.Get[taxonomy.Service](app.Deps, kernel.ServiceTaxonomy)
	if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: "original", Name: "Original"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: "newer", Name: "Newer"}); err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Original title", Body: "<p>Original</p>", Status: "published", Terms: map[string][]string{"category": {"original"}}})
	if err != nil {
		t.Fatal(err)
	}
	updater := cs.(content.AtomicUpdater)
	input := content.InputFromItem(it)
	input.Body = "<p>New body</p>"
	if _, err := updater.UpdateAtomic(ctx, it.ID, input, content.EditToken(it), func(*sql.Tx, *content.Item) error { return nil }); err != nil {
		t.Fatal(err)
	}
	revisions, err := cs.Revisions(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, revision := range revisions {
		if revision.Snapshot.Body == it.Body {
			found = true
			if len(revision.Snapshot.Terms["category"]) != 1 || revision.Snapshot.Terms["category"][0] != "original" {
				t.Fatalf("prestate terms lost: %+v", revision.Snapshot.Terms)
			}
		}
	}
	if !found {
		t.Fatal("prestate revision missing")
	}
	current, _ := cs.Get(ctx, it.ID)
	input = content.InputFromItem(current)
	input.Title = "Conflicting save"
	changed := false
	app.Deps.Hooks.Filter("content.before_save", 1, func(hctx context.Context, v any) (any, error) {
		if !changed {
			changed = true
			if err := tax.SetContentTerms(hctx, it.ID, map[string][]string{"category": {"newer"}}); err != nil {
				return nil, err
			}
		}
		return v, nil
	})
	_, err = updater.UpdateAtomic(ctx, it.ID, input, content.EditToken(current), nil)
	if !errors.Is(err, content.ErrConflict) {
		t.Fatalf("term race not rejected: %v", err)
	}
	stored, _ := cs.Get(ctx, it.ID)
	if stored.Title != current.Title || stored.Terms["category"][0] != "newer" {
		t.Fatal("concurrent terms overwritten")
	}
}

func TestBatchPublicationSnapshotsExistingTaxonomyAssignments(t *testing.T) {
	app := bootApp(t, taxonomy.New())
	ctx := context.Background()
	cs := contentService(t, app)
	tax, _ := kernel.Get[taxonomy.Service](app.Deps, kernel.ServiceTaxonomy)
	if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: "batch", Name: "Batch"}); err != nil {
		t.Fatal(err)
	}
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Batch", Status: "draft", Terms: map[string][]string{"category": {"batch"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.(content.BatchPublisher).PublishBatch(ctx, []content.PublishExpectation{{ID: it.ID, UpdatedAt: content.EditToken(it)}}, nil); err != nil {
		t.Fatal(err)
	}
	revisions, err := cs.Revisions(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions[0].Snapshot.Terms["category"]) != 1 {
		t.Fatalf("batch publication lost terms %+v", revisions[0].Snapshot.Terms)
	}
}

func TestAtomicUpdateRejectsTermsChangedBeforeItsFreshRead(t *testing.T) {
	app := bootApp(t, taxonomy.New())
	ctx := context.Background()
	cs := contentService(t, app)
	tax, _ := kernel.Get[taxonomy.Service](app.Deps, kernel.ServiceTaxonomy)
	for _, slug := range []string{"original", "newer"} {
		if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: slug, Name: slug}); err != nil {
			t.Fatal(err)
		}
	}
	it, err := cs.Create(ctx, content.Input{Type: "post", Title: "Original title", Status: "published", Terms: map[string][]string{"category": {"original"}}})
	if err != nil {
		t.Fatal(err)
	}
	input := content.InputFromItem(it)
	input.Title = "Stale input"
	if err := tax.SetContentTerms(ctx, it.ID, map[string][]string{"category": {"newer"}}); err != nil {
		t.Fatal(err)
	}
	_, err = cs.(content.AtomicUpdater).UpdateAtomic(ctx, it.ID, input, content.EditToken(it), nil)
	if !errors.Is(err, content.ErrConflict) {
		t.Fatalf("stale terms accepted: %v", err)
	}
	stored, _ := cs.Get(ctx, it.ID)
	if stored.Title != it.Title || stored.Terms["category"][0] != "newer" {
		t.Fatal("new assignments overwritten")
	}
}
