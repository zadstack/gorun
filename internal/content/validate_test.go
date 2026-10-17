package content

import (
	"context"
	"testing"
)

func validateType(fields ...Field) *Type {
	return &Type{Slug: "thing", Name: map[string]string{"en": "Thing"}, Fields: fields}
}

func TestValidateRequired(t *testing.T) {
	s := newTestService(t)
	typ := validateType(
		Field{Key: "title2", Type: FieldText, Required: true},
		Field{Key: "tags", Type: FieldMultiselect, Required: true, Options: []Option{{Value: "a"}}},
		Field{Key: "flag", Type: FieldBoolean, Required: true},
		Field{Key: "note", Type: FieldText, Default: "hello"},
	)
	_, errs := s.Validate(typ, map[string]any{"title2": "  ", "tags": []any{}})
	if errs["title2"] != "content.validation.required" || errs["tags"] != "content.validation.required" {
		t.Fatalf("errs: %v", errs)
	}
	if _, ok := errs["flag"]; !ok {
		t.Fatalf("missing boolean must fail required: %v", errs)
	}
	out, errs := s.Validate(typ, map[string]any{"title2": "x", "tags": []string{"a"}, "flag": false})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if out["note"] != "hello" || out["flag"] != false {
		t.Fatalf("defaults/values: %v", out)
	}
}

func TestValidateNumberRange(t *testing.T) {
	s := newTestService(t)
	typ := validateType(Field{Key: "price", Type: FieldNumber, Min: ptr(0.0), Max: ptr(100.0)})
	for _, c := range []struct {
		in   any
		want string
		out  float64
	}{
		{-1.0, "content.validation.min", 0}, {101, "content.validation.max", 0}, {"abc", "content.validation.number", 0},
		{"12.5", "", 12.5}, {float64(100), "", 100}, {int64(3), "", 3},
	} {
		out, errs := s.Validate(typ, map[string]any{"price": c.in})
		if errs["price"] != c.want {
			t.Errorf("%v: err %q want %q", c.in, errs["price"], c.want)
		}
		if c.want == "" && out["price"] != c.out {
			t.Errorf("%v: out %v (%T)", c.in, out["price"], out["price"])
		}
	}
	out, errs := s.Validate(typ, map[string]any{"price": ""})
	if len(errs) != 0 || out["price"] != nil {
		t.Fatalf("empty optional number: %v %v", out, errs)
	}
}

func TestValidateSelectOption(t *testing.T) {
	s := newTestService(t)
	opts := []Option{{Value: "red"}, {Value: "green"}}
	typ := validateType(Field{Key: "color", Type: FieldSelect, Options: opts}, Field{Key: "colors", Type: FieldMultiselect, Options: opts})
	_, errs := s.Validate(typ, map[string]any{"color": "blue", "colors": []any{"red", "pink"}})
	if errs["color"] != "content.validation.option" || errs["colors"] != "content.validation.option" {
		t.Fatalf("errs: %v", errs)
	}
	out, errs := s.Validate(typ, map[string]any{"color": "red", "colors": []any{"green", "red"}})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if out["color"] != "red" || len(out["colors"].([]string)) != 2 {
		t.Fatalf("out: %v", out)
	}
	out, _ = s.Validate(typ, map[string]any{"colors": "green"})
	if got, ok := out["colors"].([]string); !ok || len(got) != 1 || got[0] != "green" {
		t.Fatalf("a single form value becomes a list: %#v", out["colors"])
	}
}

func TestValidateRepeaterSubFields(t *testing.T) {
	s := newTestService(t)
	typ := validateType(Field{Key: "specs", Type: FieldRepeater, SubFields: []Field{
		{Key: "name", Type: FieldText, Required: true},
		{Key: "value", Type: FieldNumber},
	}})
	_, errs := s.Validate(typ, map[string]any{"specs": []any{
		map[string]any{"name": "Weight", "value": "2"},
		map[string]any{"name": "", "value": "x"},
	}})
	if errs["specs.1.name"] != "content.validation.required" || errs["specs.1.value"] != "content.validation.number" || len(errs) != 2 {
		t.Fatalf("errs: %v", errs)
	}
	out, errs := s.Validate(typ, map[string]any{"specs": []any{map[string]any{"name": "Weight", "value": "2", "junk": 1}}})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	rows := out["specs"].([]map[string]any)
	if len(rows) != 1 || rows[0]["value"] != 2.0 || rows[0]["junk"] != nil {
		t.Fatalf("rows: %#v", rows)
	}
	if _, errs := s.Validate(typ, map[string]any{"specs": "nope"}); errs["specs"] != "content.validation.repeater" {
		t.Fatalf("non-list repeater: %v", errs)
	}
}

func TestValidateRelationshipTargetType(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mustType(t, s, Type{Slug: "product", Name: map[string]string{"en": "Products"}})
	prod := mustCreate(t, s, Input{Type: "product", Title: "Plan"})
	post := mustCreate(t, s, Input{Type: "post", Title: "Hello"})
	_ = ctx
	typ := validateType(
		Field{Key: "product", Type: FieldRelationship, TargetType: "product"},
		Field{Key: "related", Type: FieldRelationship, TargetType: "product", Multiple: true},
	)
	_, errs := s.Validate(typ, map[string]any{"product": post.ID, "related": []any{prod.ID, "missing"}})
	if errs["product"] != "content.validation.target_type" || errs["related"] != "content.validation.target_type" {
		t.Fatalf("errs: %v", errs)
	}
	out, errs := s.Validate(typ, map[string]any{"product": prod.ID, "related": []any{prod.ID}})
	if len(errs) != 0 || out["product"] != prod.ID || len(out["related"].([]string)) != 1 {
		t.Fatalf("%v %v", out, errs)
	}
}

func TestValidateUnknownKeysDropped(t *testing.T) {
	s := newTestService(t)
	typ := validateType(Field{Key: "a", Type: FieldText}, Field{Key: "meta", Type: FieldJSON}, Field{Key: "on", Type: FieldBoolean},
		Field{Key: "day", Type: FieldDate}, Field{Key: "at", Type: FieldDatetime})
	out, errs := s.Validate(typ, map[string]any{"a": "x", "zzz": "dropped", "meta": `{"k":[1,2]}`, "on": "on",
		"day": "2026-10-10", "at": "2026-10-10T12:30"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, ok := out["zzz"]; ok {
		t.Fatalf("unknown key kept: %v", out)
	}
	if m, ok := out["meta"].(map[string]any); !ok || m["k"] == nil {
		t.Fatalf("json string should be parsed: %#v", out["meta"])
	}
	if out["on"] != true || out["day"] != "2026-10-10" || out["at"] != "2026-10-10T12:30:00Z" {
		t.Fatalf("coercion: %v", out)
	}
	if _, errs := s.Validate(typ, map[string]any{"meta": "{bad", "day": "10/10/2026"}); errs["meta"] == "" || errs["day"] == "" {
		t.Fatalf("bad json/date: %v", errs)
	}
}
