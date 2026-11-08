package i18n

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/language"
)

const ContentLocalesSetting = "site.content_locales"
const MaxContentLocales = 32

// LocaleSettings is the read-only settings contract; i18n does not depend on
// the settings module or its storage implementation.
type LocaleSettings interface {
	Get(context.Context, string, any) error
}

// CanonicalLocale preserves the language, script and region of a BCP-47 tag.
// Admin catalog lookup may still fall back to a primary language separately.
func CanonicalLocale(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 63 || strings.Contains(raw, "_") {
		return "", fmt.Errorf("invalid content locale %q", raw)
	}
	tag, err := language.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid content locale: %w", err)
	}
	return tag.String(), nil
}

func ParseContentLocales(tags []string) ([]string, error) {
	if len(tags) == 0 || len(tags) > MaxContentLocales {
		return nil, fmt.Errorf("choose between 1 and %d content locales", MaxContentLocales)
	}
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		tag, err := CanonicalLocale(raw)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, tag) {
			return nil, fmt.Errorf("duplicate content locale %q", tag)
		}
		out = append(out, tag)
	}
	return out, nil
}

// ContentLocales deliberately defaults independently of Supported (admin UI).
func ContentLocales(ctx context.Context, settings LocaleSettings) []string {
	if cached, ok := ctx.Value(contentLocalesKey{}).([]string); ok && len(cached) > 0 {
		return slices.Clone(cached)
	}
	return ReadContentLocales(ctx, settings)
}

// ReadContentLocales bypasses a rendering context's pinned language list.
// Mutation code uses settings.ContentLocalesTx to retain storage errors.
func ReadContentLocales(ctx context.Context, settings LocaleSettings) []string {
	if settings != nil {
		var saved []string
		if settings.Get(ctx, ContentLocalesSetting, &saved) == nil {
			if tags, err := ParseContentLocales(saved); err == nil {
				return tags
			}
		}
	}
	return []string{"ar", "en"}
}

func ContentLocale(ctx context.Context, settings LocaleSettings) string {
	locales := ContentLocales(ctx, settings)
	if settings != nil {
		var saved string
		if settings.Get(ctx, "site.locale", &saved) == nil {
			if tag, err := CanonicalLocale(saved); err == nil && slices.Contains(locales, tag) {
				return tag
			}
		}
	}
	return locales[0]
}

type contentLocalesKey struct{}

// WithContentLocales pins one consistent language list for a rendering batch.
func WithContentLocales(ctx context.Context, locales []string) context.Context {
	return context.WithValue(ctx, contentLocalesKey{}, slices.Clone(locales))
}

func LocaleName(locale string) string {
	if name := NativeNames[locale]; name != "" {
		return name
	}
	return locale
}

// SplitLanguagePath recognizes a well-formed language tag without consulting
// site settings. Use it for credential redaction and reserved-path checks.
func SplitLanguagePath(p string) (locale, rest string) {
	segment, tail, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	tag, err := CanonicalLocale(segment)
	if err != nil {
		return "", p
	}
	return tag, "/" + tail
}
