// Package i18n loads translation catalogs and resolves the request locale.
//
// Catalog contract: every catalog FS (the embedded core one and any returned
// by a module's kernel.CatalogProvider) has files at
// "catalog/<module>.<locale>.json", each a flat {"key": "text"} object.
// Keys are stored with the module prefix: "app.name" in core.en.json is
// looked up as "core.app.name". Duplicate full keys across catalogs are an
// error, and so is a key that already starts with "<module>." (it would be
// stored as "core.core.x"). T falls back locale → en → the key itself.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed catalog/*.json
var coreCatalog embed.FS

// DefaultLocale is used when neither the user preference nor
// Accept-Language names a supported locale. GoRun is Arabic/RTL-first.
var DefaultLocale = "ar"

// Supported lists the locales LocaleFromRequest may return.
var Supported = []string{"ar", "en"}

const fallbackLocale = "en"

var rtlLocales = map[string]bool{"ar": true, "fa": true, "he": true, "ur": true}

// Bundle holds merged messages: locale → full key → text.
type Bundle struct {
	messages map[string]map[string]string
}

// Load merges the embedded core catalog with the given catalogs.
func Load(catalogs ...fs.FS) (*Bundle, error) {
	b := &Bundle{messages: map[string]map[string]string{}}
	for _, c := range append([]fs.FS{coreCatalog}, catalogs...) {
		if err := b.merge(c); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b *Bundle) merge(c fs.FS) error {
	files, err := fs.Glob(c, "catalog/*.json")
	if err != nil {
		return fmt.Errorf("i18n: list catalogs: %w", err)
	}
	for _, p := range files {
		module, locale, ok := splitCatalogName(path.Base(p))
		if !ok {
			return fmt.Errorf("i18n: catalog %s: want <module>.<locale>.json", p)
		}
		raw, err := fs.ReadFile(c, p)
		if err != nil {
			return fmt.Errorf("i18n: read %s: %w", p, err)
		}
		var msgs map[string]string
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return fmt.Errorf("i18n: parse %s: %w", p, err)
		}
		if b.messages[locale] == nil {
			b.messages[locale] = map[string]string{}
		}
		for k, v := range msgs {
			if strings.HasPrefix(k, module+".") {
				return fmt.Errorf("i18n: %s: key %q repeats the module prefix (write %q; it is stored as %q)",
					p, k, strings.TrimPrefix(k, module+"."), k)
			}
			full := module + "." + k
			if _, dup := b.messages[locale][full]; dup {
				return fmt.Errorf("i18n: duplicate key %s in %s", full, locale)
			}
			b.messages[locale][full] = v
		}
	}
	return nil
}

func splitCatalogName(name string) (module, locale string, ok bool) {
	base := strings.TrimSuffix(name, ".json")
	i := strings.LastIndex(base, ".")
	if i <= 0 || i == len(base)-1 {
		return "", "", false
	}
	return base[:i], strings.ToLower(base[i+1:]), true
}

func (b *Bundle) lookup(locale, key string) (string, bool) {
	s, ok := b.messages[locale][key]
	return s, ok
}

// T translates key ("module.key") for locale, formatting args with
// fmt.Sprintf when given. Falls back to en, then to the key itself.
func (b *Bundle) T(locale, key string, args ...any) string {
	msg, ok := b.lookup(normalize(locale), key)
	if !ok {
		msg, ok = b.lookup(fallbackLocale, key)
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// Dir returns "rtl" for right-to-left locales and "ltr" otherwise.
func (b *Bundle) Dir(locale string) string {
	if rtlLocales[normalize(locale)] {
		return "rtl"
	}
	return "ltr"
}

// normalize reduces a language tag to its lower-case primary subtag.
func normalize(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

func supported(locale string) bool {
	for _, s := range Supported {
		if s == locale {
			return true
		}
	}
	return false
}

// LocaleFromRequest picks the locale: a supported userPref wins, then the
// highest-weighted supported Accept-Language entry, then DefaultLocale.
func LocaleFromRequest(r *http.Request, userPref string) string {
	if p := normalize(userPref); supported(p) {
		return p
	}
	best, bestQ := "", 0.0
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, q := parseLangQ(part)
		if loc := normalize(tag); q > bestQ && supported(loc) {
			best, bestQ = loc, q
		}
	}
	if best == "" {
		return DefaultLocale
	}
	return best
}

func parseLangQ(part string) (string, float64) {
	tag, params, _ := strings.Cut(part, ";")
	q := 1.0
	if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return tag, 0
		}
		q = f
	}
	return tag, q
}

type localeKey struct{}

// WithLocale returns ctx carrying locale.
func WithLocale(ctx context.Context, locale string) context.Context {
	return context.WithValue(ctx, localeKey{}, locale)
}

// Locale returns the locale stored by WithLocale, or DefaultLocale.
func Locale(ctx context.Context) string {
	if l, ok := RequestLocale(ctx); ok {
		return l
	}
	return DefaultLocale
}

// RequestLocale returns the locale attached by WithLocale, if any.
// Unlike Locale it does not fall back to DefaultLocale, so callers can
// distinguish an explicit request locale from an unset context.
func RequestLocale(ctx context.Context) (string, bool) {
	if l, ok := ctx.Value(localeKey{}).(string); ok && l != "" {
		return l, true
	}
	return "", false
}
