package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/zadstack/gorun/internal/content"
)

// emptyObject is the input schema of a tool without an Input type.
var emptyObject = json.RawMessage(`{"type":"object"}`)

// fieldShape mirrors content.Field without the recursion (sub_fields are
// plain objects) so a JSON Schema can be inferred for it.
type fieldShape struct {
	Key        string            `json:"key"`
	Label      map[string]string `json:"label,omitempty"`
	Type       string            `json:"type" jsonschema:"text, textarea, richtext, number, date, datetime, boolean, select, multiselect, repeater, relationship, image, file or json"`
	Required   bool              `json:"required,omitempty"`
	Indexed    bool              `json:"indexed,omitempty"`
	Multiple   bool              `json:"multiple,omitempty"`
	Default    any               `json:"default,omitempty"`
	Help       map[string]string `json:"help,omitempty"`
	Options    []content.Option  `json:"options,omitempty"`
	Min        *float64          `json:"min,omitempty"`
	Max        *float64          `json:"max,omitempty"`
	Step       *float64          `json:"step,omitempty"`
	SubFields  []map[string]any  `json:"sub_fields,omitempty" jsonschema:"repeater sub-fields (same shape as a field)"`
	TargetType string            `json:"target_type,omitempty"`
	Mime       []string          `json:"mime,omitempty"`
}

var (
	schemaMu    sync.Mutex
	schemaCache = map[reflect.Type]json.RawMessage{}
	typeSchemas map[reflect.Type]*jsonschema.Schema
)

// schemaFor returns the JSON Schema (2020-12) of a tool's Input value: the
// properties are its json (snake_case) names; only fields without
// omitempty at the top level are required. Nested objects never declare
// required properties (service validation reports them instead). Results
// are cached per type.
func schemaFor(input any) (json.RawMessage, error) {
	if input == nil {
		return emptyObject, nil
	}
	t := reflect.TypeOf(input)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	schemaMu.Lock()
	defer schemaMu.Unlock()
	if s, ok := schemaCache[t]; ok {
		return s, nil
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("mcp: tool input must be a struct, got %s", t)
	}
	if typeSchemas == nil {
		fs, err := jsonschema.For[fieldShape](nil)
		if err != nil {
			return nil, fmt.Errorf("mcp: field schema: %w", err)
		}
		typeSchemas = map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[content.Field](): fs}
	}
	s, err := jsonschema.ForType(t, &jsonschema.ForOptions{IgnoreInvalidTypes: true, TypeSchemas: typeSchemas})
	if err != nil {
		return nil, fmt.Errorf("mcp: schema for %s: %w", t, err)
	}
	for _, p := range s.Properties {
		dropRequired(p)
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal schema for %s: %w", t, err)
	}
	schemaCache[t] = b
	return b, nil
}

// dropRequired clears Required on s and every schema nested in it.
func dropRequired(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	s.Required = nil
	for _, p := range s.Properties {
		dropRequired(p)
	}
	dropRequired(s.Items)
	dropRequired(s.AdditionalProperties)
	for _, x := range s.AnyOf {
		dropRequired(x)
	}
	for _, x := range s.OneOf {
		dropRequired(x)
	}
}
