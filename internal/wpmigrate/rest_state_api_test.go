package wpmigrate

import (
	"encoding/json"
	"testing"

	"github.com/zadstack/gorun/internal/api/apitest"
)

func TestOpenAPIStateCountsAreDocumentedOptionalIntegerMaps(t *testing.T) {
	e := newEnv(t)
	c := apitest.NewClient(t, e.app).Anonymous()
	w := c.Do("GET", "/api/v1/openapi.json", nil, nil)
	var document struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]map[string]any
				Required   []string
			}
		}
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &document) != nil {
		t.Fatalf("OpenAPI status%d or invalid JSON", w.Code)
	}
	contracts := 0
	for name, schema := range document.Components.Schemas {
		field, exists := schema.Properties["content_state_counts"]
		if !exists {
			continue
		}
		contracts++
		if field["type"] != "object" || field["description"] == nil {
			t.Fatalf("%s state count lacks type/description", name)
		}
		states, ok := field["additionalProperties"].(map[string]any)
		if !ok || states["type"] != "object" {
			t.Fatalf("%s lacks type-to-state map", name)
		}
		count, ok := states["additionalProperties"].(map[string]any)
		if !ok || count["type"] != "integer" {
			t.Fatalf("%s state counts are not integers", name)
		}
		for _, required := range schema.Required {
			if required == "content_state_counts" {
				t.Fatalf("%s additive count contract must remain optional", name)
			}
		}
	}
	if contracts != 2 {
		t.Fatalf("state count contracts%d, want discovery and preview", contracts)
	}
}
