package api_test

import (
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func TestContentPatchOmittedNullAndEmptySemantics(t *testing.T) {
	app := apitest.Boot(t, content.New())
	c := apitest.NewClient(t, app)
	status, env := c.Post("/types", map[string]any{"slug": "contract", "name": map[string]string{"en": "Contract"},
		"fields": []map[string]any{{"key": "a", "type": "text"}, {"key": "b", "type": "text"}}})
	if status != 201 {
		t.Fatalf("create type: %d %v", status, env)
	}
	for _, tc := range []struct {
		name, patch, title, excerpt string
		menuOrder                   float64
		seoCount, fieldsCount       int
	}{
		{"omitted", `{}`, "Title", "Excerpt", 7, 2, 2},
		{"scalar null ignored", `{"title":null,"excerpt":null}`, "Title", "Excerpt", 7, 2, 2},
		{"empty scalar clears", `{"title":"","excerpt":""}`, "", "", 7, 2, 2},
		{"menu null resets", `{"menu_order":null}`, "Title", "Excerpt", 0, 2, 2},
		{"empty objects merge", `{"seo":{},"fields":{}}`, "Title", "Excerpt", 7, 2, 2},
		{"map null clears", `{"seo":null,"fields":null}`, "Title", "Excerpt", 7, 0, 0},
		{"one key merges", `{"seo":{"a":"changed"},"fields":{"a":"changed"}}`, "Title", "Excerpt", 7, 2, 2},
		{"one empty field removed", `{"fields":{"a":""}}`, "Title", "Excerpt", 7, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, env := c.Post("/content", map[string]any{"type": "contract", "title": "Title", "excerpt": "Excerpt", "menu_order": 7,
				"fields": map[string]string{"a": "one", "b": "two"}, "seo": map[string]string{"a": "one", "b": "two"}})
			if status != 201 {
				t.Fatalf("create: %d %v", status, env)
			}
			path := "/content/" + apitest.Data(env)["id"].(string)
			status, env = c.Patch(path, tc.patch)
			if status != 200 {
				t.Fatalf("PATCH: %d %v", status, env)
			}
			status, persisted := c.Get(path)
			it := apitest.Data(persisted)
			if status != 200 || it["title"] != tc.title || it["excerpt"] != tc.excerpt || it["menu_order"] != tc.menuOrder ||
				len(it["seo"].(map[string]any)) != tc.seoCount || len(it["fields"].(map[string]any)) != tc.fieldsCount {
				t.Fatalf("persisted PATCH semantics: %v", it)
			}
		})
	}
}

func TestContentPatchTaxonomyNullAndEmptySemantics(t *testing.T) {
	app := apitest.Boot(t, content.New(), taxonomy.New())
	c := apitest.NewClient(t, app)
	for _, slug := range []string{"a", "b"} {
		status, env := c.Post("/taxonomies/category/terms", map[string]any{"slug": slug, "name": slug})
		if status != 201 {
			t.Fatalf("term: %d %v", status, env)
		}
	}
	for _, tc := range []struct {
		name, patch string
		count       int
	}{
		{"omitted", `{}`, 2}, {"empty object", `{"terms":{}}`, 2},
		{"null map", `{"terms":null}`, 0}, {"null category", `{"terms":{"category":null}}`, 0},
		{"empty category", `{"terms":{"category":[]}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, env := c.Post("/content", map[string]any{"type": "post", "terms": map[string][]string{"category": {"a", "b"}}})
			if status != 201 {
				t.Fatalf("create: %d %v", status, env)
			}
			path := "/content/" + apitest.Data(env)["id"].(string)
			status, env = c.Patch(path, tc.patch)
			if status != 200 {
				t.Fatalf("patch: %d %v", status, env)
			}
			_, env = c.Get(path)
			terms := apitest.Data(env)["terms"].(map[string]any)
			values, _ := terms["category"].([]any)
			if len(values) != tc.count {
				t.Fatalf("terms persisted: %v", terms)
			}
		})
	}
}

func TestContentCreationUsesOptionalDefaultsAndEmptyStatusMeansDraft(t *testing.T) {
	app := apitest.Boot(t, content.New())
	c := apitest.NewClient(t, app)
	status, env := c.Post("/content", map[string]any{"type": "post"})
	if status != 201 || apitest.Data(env)["title"] != "" || apitest.Data(env)["status"] != "draft" {
		t.Fatalf("creation defaults: %d %v", status, env)
	}
	status, env = c.Post("/content", map[string]any{"type": "post", "title": "published", "status": "published"})
	if status != 201 {
		t.Fatalf("create published: %d %v", status, env)
	}
	path := "/content/" + apitest.Data(env)["id"].(string)
	status, env = c.Patch(path, `{"status":null}`)
	if status != 200 || apitest.Data(env)["status"] != "published" {
		t.Fatalf("null status: %d %v", status, env)
	}
	status, env = c.Patch(path, `{"status":""}`)
	if status != 200 || apitest.Data(env)["status"] != "draft" {
		t.Fatalf("empty status: %d %v", status, env)
	}
}
