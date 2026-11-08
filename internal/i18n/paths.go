package i18n

import (
	"slices"
	"strings"
)

// NativeNames are the supported locales in their own language (language switchers).
var NativeNames = map[string]string{"ar": "العربية", "en": "English"}

// LocalePrefix is the public path prefix of loc on a site whose locale is site: "/<loc>" for
// another supported locale, "" for the site locale, "" (unprefixed) or an unsupported one.
func LocalePrefix(loc, site string, configured ...[]string) string {
	locales := []string{"ar", "en"}
	if len(configured) > 0 {
		locales = configured[0]
	}
	if loc == "" || loc == site || !slices.Contains(locales, loc) {
		return ""
	}
	return "/" + loc
}

// LocalizePath puts p (a site path starting with "/") under the prefix of loc.
func LocalizePath(loc, site, p string, configured ...[]string) string {
	prefix := LocalePrefix(loc, site, configured...)
	if prefix == "" {
		return p
	}
	if p == "" || p == "/" {
		return prefix + "/"
	}
	return prefix + p
}

// SplitLocalePath splits a leading supported-locale segment off p: "/en/post/x" → "en",
// "/post/x"; "/en" → "en", "/". Any other path is returned whole with loc "".
func SplitLocalePath(p string, configured ...[]string) (loc, rest string) {
	locales := []string{"ar", "en"}
	if len(configured) > 0 {
		locales = configured[0]
	}
	segment, tail, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	// Route prefixes match configured spelling, ignoring case. Language aliases
	// such as eng -> en must not capture an existing ordinary page named eng.
	for _, locale := range locales {
		if strings.EqualFold(segment, locale) {
			return locale, "/" + tail
		}
	}
	return "", p
}
