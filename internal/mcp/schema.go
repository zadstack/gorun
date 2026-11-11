package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
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
	Type       string            `json:"type" enum:"text,textarea,richtext,number,date,datetime,boolean,select,multiselect,repeater,relationship,image,file,json"`
	Required   bool              `json:"required,omitempty"`
	Indexed    bool              `json:"indexed,omitempty"`
	Multiple   bool              `json:"multiple,omitempty"`
	Default    any               `json:"default,omitempty"`
	Help       map[string]string `json:"help,omitempty"`
	Options    []optionShape     `json:"options,omitempty" jsonschema:"choices of select and multiselect fields"`
	Min        *float64          `json:"min,omitempty"`
	Max        *float64          `json:"max,omitempty"`
	Step       *float64          `json:"step,omitempty"`
	SubFields  []map[string]any  `json:"sub_fields,omitempty" jsonschema:"repeater sub-fields (same shape as a field)"`
	TargetType string            `json:"target_type,omitempty" jsonschema:"relationship fields: the content type slug they point to"`
	Mime       []string          `json:"mime,omitempty"`
}

// optionShape is content.Option with the label optional (as the service accepts it).
type optionShape struct {
	Value string            `json:"value"`
	Label map[string]string `json:"label,omitempty"`
}

var (
	schemaMu    sync.Mutex
	schemaCache = map[reflect.Type]json.RawMessage{}
	typeSchemas map[reflect.Type]*jsonschema.Schema
	fieldType   = reflect.TypeFor[content.Field]()
	shapeType   = reflect.TypeFor[fieldShape]()
)

// schemaFor returns the JSON Schema (2020-12) of a tool's Input value: the
// properties are its json (snake_case) names, and at every level the fields
// without omitempty/omitzero are required (the same rule as the REST
// OpenAPI document). Besides `jsonschema:"description"`, the REST tags
// `doc:"…"`, `enum:"a,b"` (comma separated; a leading comma allows ""),
// `pattern:"…"`, `minimum:"n"` and `maximum:"n"` are honoured. Results are
// cached per type.
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
		applyTags(shapeType, fs, 0)
		typeSchemas = map[reflect.Type]*jsonschema.Schema{fieldType: fs}
	}
	s, err := jsonschema.ForType(t, &jsonschema.ForOptions{IgnoreInvalidTypes: true, TypeSchemas: typeSchemas})
	if err != nil {
		return nil, fmt.Errorf("mcp: schema for %s: %w", t, err)
	}
	applyTags(t, s, 0)
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal schema for %s: %w", t, err)
	}
	schemaCache[t] = b
	return b, nil
}

// maxTagDepth bounds the walk over nested (possibly recursive) types.
const maxTagDepth = 16

// applyTags copies the REST schema tags of t's fields onto s, recursively.
func applyTags(t reflect.Type, s *jsonschema.Schema, depth int) {
	if s == nil || depth > maxTagDepth {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() != reflect.Uint8 {
			applyTags(t.Elem(), s.Items, depth+1)
		}
		return
	case reflect.Map:
		applyTags(t.Elem(), s.AdditionalProperties, depth+1)
		return
	case reflect.Struct:
	default:
		return
	}
	if t == fieldType {
		return // its shared schema is fieldShape's, tagged once when built
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			applyTags(f.Type, s, depth+1) // embedded fields are flattened into s
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		ps := s.Properties[name]
		if ps == nil {
			continue
		}
		tagField(f, ps)
		applyTags(f.Type, ps, depth+1)
	}
}

// tagField applies one field's doc/enum/pattern/minimum/maximum tags; on a
// slice field the value constraints go to its items.
func tagField(f reflect.StructField, ps *jsonschema.Schema) {
	if doc := f.Tag.Get("doc"); doc != "" && ps.Description == "" {
		ps.Description = doc
	}
	target := ps
	ft := f.Type
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if ft.Kind() == reflect.Slice && ps.Items != nil {
		target = ps.Items
	}
	if e := f.Tag.Get("enum"); e != "" {
		target.Enum = nil
		for _, v := range strings.Split(e, ",") {
			target.Enum = append(target.Enum, v)
		}
	}
	if p := f.Tag.Get("pattern"); p != "" {
		target.Pattern = p
	}
	if v, err := strconv.ParseFloat(f.Tag.Get("minimum"), 64); err == nil {
		target.Minimum = &v
	}
	if v, err := strconv.ParseFloat(f.Tag.Get("maximum"), 64); err == nil {
		target.Maximum = &v
	}
}
