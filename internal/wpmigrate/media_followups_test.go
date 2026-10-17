package wpmigrate

import (
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

// Re-review follow-ups for replaced media: custom image fields count as
// references, the plain WordPress URL resolves to the current file, and
// products that exist locally count even when their last run failed.
func TestReplacedMediaFollowUps(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("first run %s", st.Status)
	}
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	cs := e.contentSvc()
	att := e.item(id, "media", "attachment", "862")
	oldID := att.LocalID
	oldMedia, _ := ms.Get(e.ctx(), oldID)
	plainURL := oldMedia.SourceURL

	// Local (not migrated) content whose image FIELD holds the media id.
	if err := cs.SaveType(e.ctx(), content.Type{Slug: "card", Name: map[string]string{"en": "Cards"}, Singular: map[string]string{"en": "Card"}, Plural: map[string]string{"en": "Cards"},
		Public: true, Supports: []string{"title"}, Fields: []content.Field{{Key: "img", Type: "image"}}}); err != nil {
		t.Fatal(err)
	}
	card, err := cs.Create(e.ctx(), content.Input{Type: "card", Title: "Local card", Status: "draft", Fields: map[string]any{"img": oldID}})
	if err != nil {
		t.Fatal(err)
	}

	e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", redPNG())
	bumpWXRAttachment(t, e, id)
	if st := e.run(id); st.Status != StatusValidated || st.Failed != 0 {
		t.Fatalf("re-run: %s %+v", st.Status, e.failedItems(id))
	}
	newID := e.item(id, "media", "attachment", "862").LocalID
	if newID == oldID {
		t.Fatal("media not replaced")
	}
	if _, err := ms.Get(e.ctx(), oldID); err != nil {
		t.Fatalf("media referenced by a local image field was deleted: %v", err)
	}
	if got, _ := cs.Get(e.ctx(), card.ID); got.Fields["img"] != oldID {
		t.Fatalf("local content must not be rewritten: %v", got.Fields["img"])
	}

	// The plain WordPress URL now resolves to the current file.
	if cur, err := ms.FindBySourceURL(e.ctx(), plainURL); err != nil || cur.ID != newID {
		t.Fatalf("FindBySourceURL(plain) = %v %v, want %s", cur, err, newID)
	}
	if cur, err := ms.FromURL(e.ctx(), plainURL, media.UploadMeta{}); err != nil || cur.ID != newID {
		t.Fatalf("FromURL(plain) = %v %v, want %s", cur, err, newID)
	}

	// A product item that exists locally counts even if its last run failed.
	m, _ := e.svc.getMigration(e.ctx(), id)
	if e.svc.hasDoneProducts(e.ctx(), m) {
		t.Fatal("no products were imported in this env")
	}
	e.app.Deps.DB.Exec(`UPDATE migration_items SET local_id = 'prod_1', status = 'failed' WHERE migration_id = ? AND kind = 'product' AND wp_id = '1068'`, id)
	if !e.svc.hasDoneProducts(e.ctx(), m) {
		t.Fatal("a product with a local id (last run failed) must count")
	}
}

// repointValue swaps exact media ids and old URLs inside nested fields.
func TestRepointValue(t *testing.T) {
	rep := strings.NewReplacer("/media/old.png", "/media/new.png")
	in := map[string]any{"img": "OLD", "gallery": []any{"OLD", "KEEP"}, "rows": []any{map[string]any{"pic": "OLD", "html": `<img src="/media/old.png">`}}, "n": 3.0}
	out := repointValue(in, "OLD", "NEW", rep).(map[string]any)
	rows := out["rows"].([]any)[0].(map[string]any)
	if out["img"] != "NEW" || out["gallery"].([]any)[0] != "NEW" || out["gallery"].([]any)[1] != "KEEP" || rows["pic"] != "NEW" ||
		rows["html"] != `<img src="/media/new.png">` || out["n"] != 3.0 {
		t.Fatalf("repointValue = %#v", out)
	}
	if in["img"] != "OLD" {
		t.Fatal("repointValue must not mutate its input")
	}
}
