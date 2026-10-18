package api

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/menus"
)

type schemaBase struct {
	ID        string `json:"id" doc:"ULID"`
	CreatedAt string `json:"created_at"`
}

type schemaSample struct {
	schemaBase
	Title    string            `json:"title" doc:"Display title"`
	Status   string            `json:"status" enum:"draft,published"`
	Note     string            `json:"note,omitempty"`
	Parent   *schemaSample     `json:"parent"`
	Count    *int              `json:"count"`
	Tags     []string          `json:"tags"`
	Meta     map[string]any    `json:"meta"`
	Labels   map[string]string `json:"labels"`
	When     time.Time         `json:"when"`
	Raw      json.RawMessage   `json:"raw"`
	Blob     []byte            `json:"blob"`
	Score    float64           `json:"score"`
	N        int64             `json:"n"`
	Ok       bool              `json:"ok"`
	Skipped  string            `json:"-"`
	Children []schemaSample    `json:"children"`
	Inline   struct {
		A string `json:"a"`
	} `json:"inline"`

	//lint:ignore U1000 an unexported field the generator must skip
	hidden string
}

func gen(t *testing.T, vals ...any) (*schemaGen, []map[string]any) {
	t.Helper()
	g := newSchemaGen()
	for _, v := range vals {
		g.collect(reflect.TypeOf(v))
	}
	g.assignNames()
	out := make([]map[string]any, len(vals))
	for i, v := range vals {
		out[i] = g.schema(reflect.TypeOf(v))
	}
	return g, out
}

func TestSchemaStructTagsAndKinds(t *testing.T) {
	g, s := gen(t, schemaSample{})
	if s[0]["$ref"] != "#/components/schemas/schemaSample" {
		t.Fatalf("ref: %v", s[0])
	}
	def := g.components()["schemaSample"].(map[string]any)
	props := def["properties"].(map[string]any)
	req := def["required"].([]string)
	if _, ok := props["id"]; !ok {
		t.Fatal("embedded struct fields must be flattened")
	}
	for _, k := range []string{"-", "Skipped", "hidden"} {
		if _, ok := props[k]; ok {
			t.Fatalf("%s must be skipped", k)
		}
	}
	if slices.Contains(req, "note") || !slices.Contains(req, "title") || !slices.Contains(req, "id") {
		t.Fatalf("required: %v", req)
	}
	check := func(name string, want map[string]any) {
		t.Helper()
		got, _ := json.Marshal(props[name])
		w, _ := json.Marshal(want)
		if string(got) != string(w) {
			t.Errorf("%s: got %s want %s", name, got, w)
		}
	}
	check("id", map[string]any{"type": "string", "description": "ULID"})
	check("created_at", map[string]any{"type": "string", "format": "date-time"})
	check("title", map[string]any{"type": "string", "description": "Display title"})
	check("status", map[string]any{"type": "string", "enum": []string{"draft", "published"}})
	check("parent", map[string]any{"oneOf": []any{map[string]any{"$ref": "#/components/schemas/schemaSample"}, map[string]any{"type": "null"}}})
	check("count", map[string]any{"type": []string{"integer", "null"}, "format": "int64"})
	check("tags", map[string]any{"type": "array", "items": map[string]any{"type": "string"}})
	check("meta", map[string]any{"type": "object", "additionalProperties": true})
	check("labels", map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}})
	check("when", map[string]any{"type": "string", "format": "date-time"})
	check("raw", map[string]any{})
	check("blob", map[string]any{"type": "string", "format": "byte"})
	check("score", map[string]any{"type": "number", "format": "double"})
	check("n", map[string]any{"type": "integer", "format": "int64"})
	check("ok", map[string]any{"type": "boolean"})
	check("children", map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/schemaSample"}})
	check("inline", map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "required": []string{"a"}})
}

func TestSchemaGenericAndCollidingNames(t *testing.T) {
	g, s := gen(t, content.Page[content.Item]{}, []menus.Item{}, content.Item{})
	if s[0]["$ref"] != "#/components/schemas/PageItem" {
		t.Fatalf("generic name: %v", s[0])
	}
	comps := g.components()
	for _, name := range []string{"PageItem", "ContentItem", "MenusItem"} {
		if _, ok := comps[name]; !ok {
			t.Fatalf("missing component %s in %v", name, keys(comps))
		}
	}
	if _, ok := comps["Item"]; ok {
		t.Fatal("colliding names must both be package-qualified")
	}
	// order independence
	g2, _ := gen(t, content.Item{}, []menus.Item{}, content.Page[content.Item]{})
	if !reflect.DeepEqual(keys(g2.components()), keys(comps)) {
		t.Fatalf("names depend on order: %v vs %v", keys(g2.components()), keys(comps))
	}
}

func TestSchemaNamedScalarsInline(t *testing.T) {
	g, s := gen(t, content.Status(""))
	if s[0]["type"] != "string" || len(g.components()) != 0 {
		t.Fatalf("named scalar: %v %v", s[0], g.components())
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
