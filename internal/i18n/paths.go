package i18n

import (
	"slices"
	"strings"
)

// NativeNames are the supported locales in their own language (language switchers).
var NativeNames = map[string]string{"ar": "العربية", "en": "English"}

// LocalePrefix is the public path prefix of loc on a site whose locale is site: "/<loc>" for
// another supported locale, "" for the site locale, "" (unprefixed) or an unsupported one.
func LocalePrefix(loc, site string) string {
	if loc == "" || loc == site || !slices.Contains(Supported, loc) {
		return ""
	}
	return "/" + loc
}

// LocalizePath puts p (a site path starting with "/") under the prefix of loc.
func LocalizePath(loc, site, p string) string {
	prefix := LocalePrefix(loc, site)
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
func SplitLocalePath(p string) (loc, rest string) {
	seg, tail, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
	if !slices.Contains(Supported, seg) {
		return "", p
	}
	return seg, "/" + tail
}
