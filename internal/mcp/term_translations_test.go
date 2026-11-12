package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// list_terms and create_term take an optional locale; create_term links a translation with
// translation_of, and list_terms filters by translation_group. Tool names stay unchanged.
func TestTermToolsWithLocale(t *testing.T) {
	e := newEnv(t, &fakeModule{})
	a := e.agent("*")
	news := a.ok("create_term", map[string]any{"taxonomy": "category", "name": "News", "locale": "en"})
	if str(news, "locale") != "en" || str(news, "translation_group") != "" {
		t.Fatalf("create_term with locale: %v", news)
	}
	akhbar := a.ok("create_term", map[string]any{"taxonomy": "category", "name": "أخبار", "locale": "ar", "translation_of": str(news, "id")})
	group := str(akhbar, "translation_group")
	if str(akhbar, "locale") != "ar" || group == "" {
		t.Fatalf("create_term as translation: %v", akhbar)
	}
	plain := a.ok("create_term", map[string]any{"taxonomy": "category", "name": "Plain"})
	if str(plain, "locale") == "" {
		t.Fatalf("create_term without locale gets the site language: %v", plain)
	}
	if errObj := a.fails("create_term", map[string]any{"taxonomy": "tag", "name": "x", "locale": "ar", "translation_of": str(news, "id")}); str(errObj, "code") != "validation_failed" {
		t.Fatalf("translation_of another taxonomy: %v", errObj)
	}
	page := a.ok("list_terms", map[string]any{"taxonomy": "category", "locale": "ar"})
	if items, _ := page["items"].([]any); len(items) != 2 { // أخبار and Plain (site language ar)
		t.Fatalf("list_terms locale: %v", page)
	}
	page = a.ok("list_terms", map[string]any{"taxonomy": "category", "translation_group": group})
	if items, _ := page["items"].([]any); len(items) != 2 {
		t.Fatalf("list_terms translation_group: %v", page)
	}

	// The schemas advertise the new optional arguments.
	res, err := a.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range res.Tools {
		if tl.Name != "create_term" && tl.Name != "list_terms" {
			continue
		}
		raw, _ := json.Marshal(tl.InputSchema)
		var schema struct {
			Properties map[string]any `json:"properties"`
			Required   []string       `json:"required"`
		}
		_ = json.Unmarshal(raw, &schema)
		if schema.Properties["locale"] == nil {
			t.Errorf("%s has no locale argument: %s", tl.Name, raw)
		}
		for _, r := range schema.Required {
			if r == "locale" || r == "translation_of" || r == "translation_group" {
				t.Errorf("%s requires optional %s", tl.Name, r)
			}
		}
	}
}
