package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/i18n"
)

const (
	maxJSONBody      = 1 << 20
	maxMultipartBody = 32 << 20
)

// FieldErrors maps a field's form name to a translated message.
type FieldErrors map[string]string

// Bind fills the struct pointed to by dst from the request. JSON bodies
// (Content-Type application/json, max 1 MiB) use encoding/json; otherwise
// form values (URL-encoded or multipart) are matched by the `form` tag
// (default: lower-cased field name; "-" skips). Supported field kinds:
// string, bool ("on", "true", "1", "yes"), ints, uints, floats, []string
// and *string (nil when absent).
func Bind(r *http.Request, dst any) error {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "application/json" {
		dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody))
		if err := dec.Decode(dst); err != nil {
			return fmt.Errorf("httpx: decode json: %w", err)
		}
		return nil
	}
	var err error
	if ct == "multipart/form-data" {
		err = r.ParseMultipartForm(maxMultipartBody)
	} else {
		err = r.ParseForm()
	}
	if err != nil {
		return fmt.Errorf("httpx: parse form: %w", err)
	}
	return bindValues(r.Form, dst)
}

func bindValues(form map[string][]string, dst any) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("httpx: Bind needs a pointer to a struct, got %T", dst)
	}
	rv = rv.Elem()
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name := fieldName(f)
		if name == "" || !f.IsExported() {
			continue
		}
		vals, ok := form[name]
		if !ok {
			continue
		}
		if err := setField(rv.Field(i), vals); err != nil {
			return fmt.Errorf("httpx: field %s: %w", name, err)
		}
	}
	return nil
}

func fieldName(f reflect.StructField) string {
	for _, tag := range []string{"form", "json"} {
		if v, ok := f.Tag.Lookup(tag); ok {
			name, _, _ := strings.Cut(v, ",")
			if name == "-" {
				return ""
			}
			if name != "" {
				return name
			}
		}
	}
	return strings.ToLower(f.Name)
}

func setField(v reflect.Value, vals []string) error {
	first := ""
	if len(vals) > 0 {
		first = vals[0]
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(first)
	case reflect.Bool:
		switch strings.ToLower(first) {
		case "on", "true", "1", "yes":
			v.SetBool(true)
		case "", "off", "false", "0", "no":
			v.SetBool(false)
		default:
			return fmt.Errorf("invalid boolean %q", first)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if first == "" {
			return nil
		}
		n, err := strconv.ParseInt(strings.TrimSpace(first), 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if first == "" {
			return nil
		}
		n, err := strconv.ParseUint(strings.TrimSpace(first), 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		if first == "" {
			return nil
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(first), v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetFloat(n)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type %s", v.Type())
		}
		v.Set(reflect.ValueOf(append([]string(nil), vals...)))
	case reflect.Pointer:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported pointer type %s", v.Type())
		}
		s := first
		v.Set(reflect.ValueOf(&s))
	default:
		return fmt.Errorf("unsupported kind %s", v.Kind())
	}
	return nil
}

// Validate checks `validate` struct tags and returns messages translated for
// i18n.Locale(ctx), keyed like Bind's form names. Rules (comma-separated,
// first failure wins): required, email, min=N / max=N (string length in
// characters, or numeric value), oneof=a b c. Optional empty fields skip the
// other rules.
func Validate(ctx context.Context, b *i18n.Bundle, v any) FieldErrors {
	rv := reflect.Indirect(reflect.ValueOf(v))
	errs := FieldErrors{}
	if rv.Kind() != reflect.Struct {
		return errs
	}
	locale := i18n.Locale(ctx)
	tr := func(key string, args ...any) string {
		if b == nil {
			return key
		}
		return b.T(locale, key, args...)
	}
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		rules := f.Tag.Get("validate")
		if rules == "" || !f.IsExported() {
			continue
		}
		if msg := checkRules(rv.Field(i), rules, tr); msg != "" {
			errs[fieldName(f)] = msg
		}
	}
	return errs
}

func checkRules(v reflect.Value, rules string, tr func(string, ...any) string) string {
	empty := isEmpty(v)
	for _, rule := range strings.Split(rules, ",") {
		name, arg, _ := strings.Cut(strings.TrimSpace(rule), "=")
		if name == "required" {
			if empty {
				return tr("core.validation.required")
			}
			continue
		}
		if empty {
			return ""
		}
		switch name {
		case "email":
			s := strings.TrimSpace(v.String())
			if a, err := mail.ParseAddress(s); err != nil || a.Address != s || !strings.Contains(s[strings.LastIndex(s, "@")+1:], ".") {
				return tr("core.validation.email")
			}
		case "min", "max":
			n, _ := strconv.ParseFloat(arg, 64)
			size := measure(v)
			if name == "min" && size < n {
				return tr("core.validation.min", int(n))
			}
			if name == "max" && size > n {
				return tr("core.validation.max", int(n))
			}
		case "oneof":
			if !contains(strings.Fields(arg), fmt.Sprint(v.Interface())) {
				return tr("core.validation.oneof")
			}
		}
	}
	return ""
}

func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return strings.TrimSpace(v.String()) == ""
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return v.IsZero()
	}
}

func measure(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.String:
		return float64(utf8.RuneCountInString(v.String()))
	case reflect.Slice, reflect.Map:
		return float64(v.Len())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		return v.Float()
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
