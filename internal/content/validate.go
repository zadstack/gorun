package content

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/zadstack/gorun/internal/db"
)

// Validate checks fields against t's schema and returns the cleaned values:
// unknown keys dropped, form strings coerced (numbers → float64, booleans →
// bool, single values → lists for multiselect/multiple), dates normalized
// (date "2006-01-02", datetime RFC3339 UTC), json strings parsed, richtext
// sanitized, defaults applied to absent keys and empty optional values
// omitted. Errors are keyed by field key (repeater rows "key.<i>.<sub>") and
// hold i18n keys. The same validator serves admin forms, REST, MCP and the
// migrator.
func (s *service) Validate(t *Type, fields map[string]any) (map[string]any, FieldErrors) {
	return s.validate(t, fields, nil)
}

// validate is Validate with the item's previously stored field maps (prev):
// relationship ids already stored that no longer resolve (target deleted
// or retyped) are dropped instead of failing, so an item never becomes
// unsaveable because of something that happened to another item. Only newly
// supplied unknown ids are rejected.
func (s *service) validate(t *Type, fields map[string]any, prev []map[string]any) (map[string]any, FieldErrors) {
	errs := FieldErrors{}
	out := s.validateLevel("", t.Fields, fields, errs, prev)
	if len(errs) == 0 {
		errs = nil
	}
	return out, errs
}

// valCtx is the previously stored values at one schema level (a repeater's
// sub-fields see the union of every stored row).
type valCtx struct{ prev []map[string]any }

// storedIDs returns the relationship ids stored under key at this level.
func (vc valCtx) storedIDs(key string) map[string]bool {
	out := map[string]bool{}
	for _, m := range vc.prev {
		for _, id := range stringList(m[key]) {
			out[id] = true
		}
	}
	return out
}

// rows returns the stored repeater rows under key at this level.
func (vc valCtx) rows(key string) []map[string]any {
	var out []map[string]any
	for _, m := range vc.prev {
		if rows, ok := asRows(m[key]); ok {
			out = append(out, rows...)
		}
	}
	return out
}

func (s *service) validateLevel(prefix string, schema []Field, in map[string]any, errs FieldErrors, prev []map[string]any) map[string]any {
	out := map[string]any{}
	vc := valCtx{prev: prev}
	for _, f := range schema {
		raw, present := in[f.Key]
		if !present && f.Default != nil {
			raw, present = f.Default, true
		}
		key := prefix + f.Key
		v, msg := s.coerce(key, f, raw, errs, vc)
		if msg != "" {
			errs[key] = msg
			continue
		}
		if isEmptyValue(v) {
			if f.Required {
				errs[key] = "content.validation.required"
			}
			continue
		}
		out[f.Key] = v
	}
	return out
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []string:
		return len(x) == 0
	case []map[string]any:
		return len(x) == 0
	}
	return false
}

// coerce converts raw to f's canonical representation. It returns the
// value or an i18n error key.
func (s *service) coerce(key string, f Field, raw any, errs FieldErrors, vc valCtx) (any, string) {
	if raw == nil {
		return nil, ""
	}
	switch f.Type {
	case FieldText, FieldTextarea:
		return scalarString(raw), ""
	case FieldRichtext:
		return Sanitize(scalarString(raw)), ""
	case FieldNumber:
		return coerceNumber(f, raw)
	case FieldBoolean:
		return coerceBool(raw)
	case FieldDate:
		return coerceDate(scalarString(raw))
	case FieldDatetime:
		return coerceDatetime(scalarString(raw))
	case FieldSelect:
		v := scalarString(raw)
		if v != "" && !hasOption(f, v) {
			return nil, "content.validation.option"
		}
		return v, ""
	case FieldMultiselect:
		list := stringList(raw)
		for _, v := range list {
			if !hasOption(f, v) {
				return nil, "content.validation.option"
			}
		}
		return list, ""
	case FieldRepeater:
		return s.coerceRepeater(key, f, raw, errs, vc.rows(f.Key))
	case FieldRelationship:
		return s.coerceRelationship(f, raw, vc.storedIDs(f.Key))
	case FieldImage, FieldFile:
		if f.Multiple {
			return stringList(raw), ""
		}
		return scalarString(raw), ""
	case FieldJSON:
		if str, ok := raw.(string); ok {
			if strings.TrimSpace(str) == "" {
				return nil, ""
			}
			var v any
			if err := json.Unmarshal([]byte(str), &v); err != nil {
				return nil, "content.validation.json"
			}
			return v, ""
		}
		return raw, ""
	}
	return nil, "content.validation.field_type"
}

// scalarString renders a scalar (or the first element of a one-element
// list, as form values arrive) as a string.
func scalarString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		if len(x) > 0 {
			return x[0]
		}
		return ""
	case []any:
		if len(x) > 0 {
			return scalarString(x[0])
		}
		return ""
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return fmt.Sprint(v)
}

// stringList accepts a list ([]string, []any) or a single scalar and drops
// empty entries.
func stringList(v any) []string {
	var list []string
	switch x := v.(type) {
	case []string:
		list = x
	case []any:
		for _, e := range x {
			list = append(list, scalarString(e))
		}
	default:
		list = []string{scalarString(v)}
	}
	out := []string{}
	for _, e := range list {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func hasOption(f Field, v string) bool {
	return slices.ContainsFunc(f.Options, func(o Option) bool { return o.Value == v })
}

func coerceNumber(f Field, raw any) (any, string) {
	var n float64
	switch x := raw.(type) {
	case float64:
		n = x
	case float32:
		n = float64(x)
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	case int32:
		n = float64(x)
	case json.Number:
		v, err := x.Float64()
		if err != nil {
			return nil, "content.validation.number"
		}
		n = v
	default:
		str := strings.TrimSpace(scalarString(raw))
		if str == "" {
			return nil, ""
		}
		v, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return nil, "content.validation.number"
		}
		n = v
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, "content.validation.number"
	}
	if f.Min != nil && n < *f.Min {
		return nil, "content.validation.min"
	}
	if f.Max != nil && n > *f.Max {
		return nil, "content.validation.max"
	}
	return n, ""
}

func coerceBool(raw any) (any, string) {
	if b, ok := raw.(bool); ok {
		return b, ""
	}
	if list, ok := raw.([]string); ok && len(list) > 1 {
		// checkbox after a hidden "0": the last value wins
		raw = list[len(list)-1]
	}
	switch strings.ToLower(strings.TrimSpace(scalarString(raw))) {
	case "on", "true", "1", "yes":
		return true, ""
	case "off", "false", "0", "no":
		return false, ""
	case "":
		return nil, ""
	}
	return nil, "content.validation.boolean"
}

const dateLayout = "2006-01-02"

func coerceDate(v string) (any, string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, ""
	}
	if ts, err := time.Parse(dateLayout, v); err == nil {
		return ts.Format(dateLayout), ""
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts.UTC().Format(dateLayout), ""
	}
	return nil, "content.validation.date"
}

func coerceDatetime(v string) (any, string) {
	ts, ok := ParseDatetime(v)
	if !ok {
		if strings.TrimSpace(v) == "" {
			return nil, ""
		}
		return nil, "content.validation.datetime"
	}
	return db.FormatTime(ts), ""
}

// ParseDatetime parses RFC3339 or the HTML datetime-local forms
// ("2006-01-02T15:04[:05]", "2006-01-02 15:04[:05]", taken as UTC).
func ParseDatetime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if ts, err := time.Parse(layout, v); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func (s *service) coerceRepeater(key string, f Field, raw any, errs FieldErrors, prevRows []map[string]any) (any, string) {
	var rows []map[string]any
	switch x := raw.(type) {
	case []map[string]any:
		rows = x
	case []any:
		for _, e := range x {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, "content.validation.repeater"
			}
			rows = append(rows, m)
		}
	default:
		return nil, "content.validation.repeater"
	}
	out := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		out = append(out, s.validateLevel(key+"."+strconv.Itoa(i)+".", f.SubFields, row, errs, prevRows))
	}
	return out, ""
}

func (s *service) coerceRelationship(f Field, raw any, stored map[string]bool) (any, string) {
	ids := []string{}
	for _, id := range stringList(raw) {
		var typ string
		err := s.db.QueryRowContext(context.Background(), `SELECT type FROM content WHERE id = ?`, id).Scan(&typ)
		if err == nil && typ == f.TargetType {
			ids = append(ids, id)
			continue
		}
		if stored[id] {
			s.log.Info("content: dropping stale relationship", "field", f.Key, "target", id)
			continue
		}
		return nil, "content.validation.target_type"
	}
	if f.Multiple {
		return ids, ""
	}
	if len(ids) == 0 {
		return nil, ""
	}
	if len(ids) > 1 {
		return nil, "content.validation.single"
	}
	return ids[0], ""
}
