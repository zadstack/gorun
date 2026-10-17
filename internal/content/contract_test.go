package content

import (
	"encoding/json"
	"reflect"
	"regexp"
	"testing"
)

var snakeKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestFieldJSONKeysAreSnakeCase(t *testing.T) {
	min := 1.0
	f := Field{Key: "price", Label: map[string]string{"en": "Price"}, Type: "number", Required: true, Indexed: true, Multiple: true,
		Default: 0, Help: map[string]string{"en": "h"}, Options: []Option{{Value: "a"}}, Min: &min, Max: &min, Step: &min,
		SubFields: []Field{{Key: "x"}}, TargetType: "post", Mime: []string{"image/png"}}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, want := range []string{"key", "label", "type", "required", "indexed", "multiple", "default", "help", "options", "min", "max", "step", "sub_fields", "target_type", "mime"} {
		if _, ok := m[want]; !ok {
			t.Errorf("missing key %q in %s", want, b)
		}
	}
	for k := range m {
		if !snakeKey.MatchString(k) {
			t.Errorf("key %q is not snake_case", k)
		}
	}
	for _, v := range []any{Type{}, Item{}, Input{}, Query{}, Revision{}, Page[Item]{}} {
		b, _ := json.Marshal(v)
		var mm map[string]any
		json.Unmarshal(b, &mm)
		for k := range mm {
			if !snakeKey.MatchString(k) {
				t.Errorf("%T: key %q is not snake_case", v, k)
			}
		}
	}
}

func TestInputFromItemCopiesEverything(t *testing.T) {
	it := &Item{ID: "01X", Type: "post", Slug: "s", Title: "T", Excerpt: "E", Body: "<p>b</p>", BodyFormat: "html",
		Fields: map[string]any{"a": 1}, AuthorID: "u", Status: "draft", ParentID: "p", MenuOrder: 3, Template: "tpl",
		FeaturedMediaID: "m", SEO: map[string]any{"title": "x"}, Locale: "ar", PublishAt: "2026-10-10T00:00:00Z",
		Terms: map[string][]string{"category": {"news"}}}
	in := InputFromItem(it)
	want := Input{Type: "post", Slug: "s", Title: "T", Excerpt: "E", Body: "<p>b</p>", Fields: it.Fields, AuthorID: "u",
		Status: "draft", ParentID: "p", MenuOrder: in.MenuOrder, Template: "tpl", FeaturedMediaID: "m", SEO: it.SEO,
		Locale: "ar", PublishAt: "2026-10-10T00:00:00Z", Terms: it.Terms}
	if in.MenuOrder == nil || *in.MenuOrder != 3 || !reflect.DeepEqual(in, want) {
		t.Fatalf("%+v", in)
	}
	if InputFromItem(&Item{}).MenuOrder == nil {
		t.Fatal("MenuOrder should always be set")
	}
}
