package api

import (
	"encoding"
	"encoding/json"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// schemaGen turns Go types into JSON Schema (OpenAPI 3.1 dialect) by
// reflection. Use it in two passes: collect every root type, assignNames
// (deterministic, order independent), then schema() each root. Named struct
// types become components referenced with $ref (which also handles
// recursive types); everything else is inlined.
//
// Tags: `json` (name, "-", omitempty/omitzero → not required, ",string"),
// `doc:"…"` → description, `enum:"a,b"` → enum. Pointers are nullable,
// time.Time and string fields named *_at are format date-time, generic
// instantiations like Page[content.Item] are named "PageItem", and names
// shared by types from different packages are package-qualified
// ("ContentItem", "MenusItem").
type schemaGen struct {
	seen  map[reflect.Type]bool
	order []reflect.Type
	names map[reflect.Type]string
	defs  map[string]any
}

var (
	timeType          = reflect.TypeOf(time.Time{})
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	nonAlnum          = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

func newSchemaGen() *schemaGen {
	return &schemaGen{seen: map[reflect.Type]bool{}, names: map[reflect.Type]string{}, defs: map[string]any{}}
}

// isComponent reports whether t is rendered as a named component.
func isComponent(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.Name() != "" && t != timeType && !customJSON(t)
}

// customJSON reports whether t (or *t) controls its own JSON encoding.
func customJSON(t reflect.Type) bool {
	if t == timeType {
		return false
	}
	p := reflect.PointerTo(t)
	return t.Implements(jsonMarshalerType) || p.Implements(jsonMarshalerType) ||
		t.Implements(textMarshalerType) || p.Implements(textMarshalerType)
}

// collect records every component type reachable from t.
func (g *schemaGen) collect(t reflect.Type) {
	if t == nil {
		return
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return
		}
		g.collect(t.Elem())
	case reflect.Map:
		g.collect(t.Elem())
	case reflect.Struct:
		if t == timeType || customJSON(t) {
			return
		}
		if t.Name() != "" {
			if g.seen[t] {
				return
			}
			g.seen[t] = true
			g.order = append(g.order, t)
		}
		for _, f := range jsonFields(t) {
			g.collect(f.typ)
		}
	}
}

// baseName is the component name of t before collision handling.
func baseName(t reflect.Type) string {
	name := t.Name()
	open := strings.IndexByte(name, '[')
	if open < 0 {
		return name
	}
	var b strings.Builder
	b.WriteString(name[:open])
	for _, arg := range splitTypeArgs(name[open+1 : len(name)-1]) {
		arg = strings.TrimLeft(arg, "*[]")
		if i := strings.LastIndexByte(arg, '/'); i >= 0 {
			arg = arg[i+1:]
		}
		if i := strings.IndexByte(arg, '['); i >= 0 { // nested generic: keep outer + inner names
			head, tail := arg[:i], arg[i:]
			if j := strings.LastIndexByte(head, '.'); j >= 0 {
				head = head[j+1:]
			}
			arg = head + tail
		} else if i := strings.LastIndexByte(arg, '.'); i >= 0 {
			arg = arg[i+1:]
		}
		b.WriteString(upperFirst(nonAlnum.ReplaceAllString(arg, "")))
	}
	return nonAlnum.ReplaceAllString(b.String(), "")
}

// splitTypeArgs splits "A,B[C,D]" at top-level commas.
func splitTypeArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// assignNames gives every collected type a unique component name. Types
// whose base name is shared get their package name prefixed; any remaining
// clash gets the sanitised package path. The result does not depend on the
// order types were collected in.
func (g *schemaGen) assignNames() {
	groups := map[string][]reflect.Type{}
	for _, t := range g.order {
		groups[baseName(t)] = append(groups[baseName(t)], t)
	}
	for base, ts := range groups {
		if len(ts) == 1 {
			g.names[ts[0]] = base
			continue
		}
		for _, t := range ts {
			g.names[t] = upperFirst(path.Base(t.PkgPath())) + base
		}
	}
	// Second-level clashes (same package name, different path): fall back to
	// the whole package path, sorted for determinism.
	byName := map[string][]reflect.Type{}
	for t, n := range g.names {
		byName[n] = append(byName[n], t)
	}
	for n, ts := range byName {
		if len(ts) < 2 {
			continue
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i].PkgPath() < ts[j].PkgPath() })
		for _, t := range ts {
			g.names[t] = nonAlnum.ReplaceAllString(upperFirst(t.PkgPath()), "") + n
		}
	}
}

// components returns the generated component schemas (call after schema).
func (g *schemaGen) components() map[string]any { return g.defs }

func ref(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

// schema returns the schema for t, defining components as needed.
func (g *schemaGen) schema(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{}
	}
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	if t.Kind() == reflect.Pointer {
		return nullable(g.schema(t.Elem()))
	}
	if customJSON(t) {
		if t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType) {
			return map[string]any{"type": "string"}
		}
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64, reflect.Uintptr:
		return withMin(map[string]any{"type": "integer", "format": "int64"}, t)
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return withMin(map[string]any{"type": "integer", "format": "int32"}, t)
	case reflect.Float32:
		return map[string]any{"type": "number", "format": "float"}
	case reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "format": "byte"}
		}
		return map[string]any{"type": "array", "items": g.schema(t.Elem())}
	case reflect.Map:
		if t.Elem().Kind() == reflect.Interface {
			return map[string]any{"type": "object", "additionalProperties": true}
		}
		return map[string]any{"type": "object", "additionalProperties": g.schema(t.Elem())}
	case reflect.Struct:
		if t.Name() == "" {
			return g.object(t)
		}
		name, ok := g.names[t]
		if !ok { // not collected (direct use without collect): name it now
			g.collect(t)
			g.assignNames()
			name = g.names[t]
		}
		if _, done := g.defs[name]; !done {
			g.defs[name] = map[string]any{} // placeholder: breaks recursion
			g.defs[name] = g.object(t)
		}
		return ref(name)
	default: // interface, func, chan, complex: any JSON value
		return map[string]any{}
	}
}

func withMin(s map[string]any, t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		s["minimum"] = 0
	}
	return s
}

// nullable allows null in addition to s.
func nullable(s map[string]any) map[string]any {
	if _, isRef := s["$ref"]; isRef {
		return map[string]any{"oneOf": []any{s, map[string]any{"type": "null"}}}
	}
	typ, ok := s["type"].(string)
	if !ok { // empty schema already allows null
		return s
	}
	out := make(map[string]any, len(s))
	for k, v := range s {
		out[k] = v
	}
	out["type"] = []string{typ, "null"}
	return out
}

// object renders a struct's JSON fields.
func (g *schemaGen) object(t reflect.Type) map[string]any {
	props := map[string]any{}
	var required []string
	for _, f := range jsonFields(t) {
		s := g.schema(f.typ)
		if f.asString {
			s = map[string]any{"type": "string"}
		}
		s = decorate(s, f)
		props[f.name] = s
		if !f.optional {
			required = append(required, f.name)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// decorate applies doc/enum tags and the *_at date-time convention.
func decorate(s map[string]any, f jsonField) map[string]any {
	if f.doc == "" && f.enum == nil && f.format == "" && !strings.HasSuffix(f.name, "_at") {
		return s
	}
	out := make(map[string]any, len(s)+2)
	for k, v := range s {
		out[k] = v
	}
	if f.doc != "" {
		out["description"] = f.doc
	}
	isString := out["type"] == "string" || reflect.DeepEqual(out["type"], []string{"string", "null"})
	if f.enum != nil && isString {
		enum := make([]any, 0, len(f.enum)+1)
		for _, e := range f.enum {
			enum = append(enum, e)
		}
		if _, isList := out["type"].([]string); isList {
			enum = append(enum, nil)
		}
		out["enum"] = enum
	}
	if f.format != "" && f.format != "-" {
		out["format"] = f.format
	}
	if strings.HasSuffix(f.name, "_at") && f.format != "-" && isString {
		if _, has := out["format"]; !has {
			out["format"] = "date-time"
		}
	}
	return out
}

type jsonField struct {
	name, doc, format  string
	enum               []string
	typ                reflect.Type
	optional, asString bool
}

// jsonFields lists t's fields as encoding/json would encode them
// (embedded structs without a json name are flattened).
func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	seen := map[string]bool{}
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			ft := f.Type
			if f.Anonymous && name == "" {
				et := ft
				if et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if et.Kind() == reflect.Struct && !customJSON(et) {
					walk(et)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			jf := jsonField{name: name, typ: ft, doc: f.Tag.Get("doc"), format: f.Tag.Get("format")}
			for _, o := range strings.Split(opts, ",") {
				switch o {
				case "omitempty", "omitzero":
					jf.optional = true
				case "string":
					jf.asString = isScalar(ft)
				}
			}
			if e := f.Tag.Get("enum"); e != "" {
				jf.enum = strings.Split(e, ",")
			}
			out = append(out, jf)
		}
	}
	walk(t)
	return out
}

func isScalar(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64, reflect.String:
		return true
	}
	return false
}
