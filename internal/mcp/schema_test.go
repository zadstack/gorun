package mcp

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"

	"github.com/zadstack/gorun/internal/builder"
)

// schemaOf decodes the generated schema of input into a generic map.
func schemaOf(t *testing.T, input any) map[string]any {
	t.Helper()
	raw, err := schemaFor(input)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// at walks object keys (properties are addressed by name).
func at(t *testing.T, m map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := m
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			t.Fatalf("schema path %v: no object at %q in %v", path, k, cur)
		}
		cur = next
	}
	return cur
}

func list(v any) []any { l, _ := v.([]any); return l }

func TestSchemaEnumsPatternsAndNestedRequired(t *testing.T) {
	lc := schemaOf(t, listContentInput{})
	order := at(t, lc, "properties", "order", "items")
	if got := list(order["required"]); !reflect.DeepEqual(got, []any{"by"}) {
		t.Errorf("order items required = %v, want [by]", got)
	}
	if got := list(at(t, order, "properties", "dir")["enum"]); !reflect.DeepEqual(got, []any{"asc", "desc"}) {
		t.Errorf("order dir enum = %v", got)
	}
	filter := at(t, lc, "properties", "fields", "additionalProperties")
	if got := list(at(t, filter, "properties", "op")["enum"]); len(got) != 8 || got[0] != "eq" || got[7] != "like" {
		t.Errorf("filter op enum = %v", got)
	}
	if got := list(filter["required"]); !reflect.DeepEqual(got, []any{"op", "value"}) {
		t.Errorf("filter required = %v", got)
	}
	pattern, _ := at(t, lc, "properties", "status")["pattern"].(string)
	re := regexp.MustCompile(pattern)
	for s, want := range map[string]bool{"draft": true, "draft, pending": true, "trash": true, "live": false, "draft,": false} {
		if re.MatchString(s) != want {
			t.Errorf("status pattern %q on %q: want %v", pattern, s, want)
		}
	}

	mc := schemaOf(t, moderateCommentInput{})
	if got := list(at(t, mc, "properties", "status")["enum"]); !reflect.DeepEqual(got, []any{"pending", "approved", "spam", "trash"}) {
		t.Errorf("moderate_comment status enum = %v", got)
	}
	if got := list(mc["required"]); !reflect.DeepEqual(got, []any{"id", "status"}) {
		t.Errorf("moderate_comment required = %v", got)
	}
	if got := list(at(t, schemaOf(t, createContentInput{}), "properties", "status")["enum"]); !reflect.DeepEqual(got, []any{"draft", "pending"}) {
		t.Errorf("create_content status enum = %v", got)
	}

	focal := at(t, schemaOf(t, updateMediaInput{}), "properties", "focal")
	if got := list(focal["required"]); !reflect.DeepEqual(got, []any{"x", "y"}) {
		t.Errorf("focal required = %v (nested required must be kept)", got)
	}
	if x := at(t, focal, "properties", "x"); x["minimum"] != 0.0 || x["maximum"] != 1.0 {
		t.Errorf("focal x bounds = %v", x)
	}

	field := at(t, schemaOf(t, createTypeInput{}), "properties", "fields", "items")
	if got := list(at(t, field, "properties", "type")["enum"]); len(got) != 14 || got[0] != "text" {
		t.Errorf("field type enum = %v", got)
	}
	if got := list(field["required"]); !reflect.DeepEqual(got, []any{"key", "type"}) {
		t.Errorf("field required = %v", got)
	}

	// Builder inputs use the REST enum/doc tags; they reach MCP too.
	purpose := at(t, schemaOf(t, builder.PurposePageInput{}), "properties", "purpose")
	if got := list(purpose["enum"]); !reflect.DeepEqual(got, []any{"about", "service", "methodology", "contact"}) {
		t.Errorf("purpose enum = %v", got)
	}
	if purpose["description"] == nil {
		t.Errorf("purpose doc tag missing: %v", purpose)
	}
}
