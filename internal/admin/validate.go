package admin

import (
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zadstack/gorun/internal/seo"
)

// SettingError is a rejected setting value; Code is an i18n key.
type SettingError struct{ Key, Code string }

func (e *SettingError) Error() string { return fmt.Sprintf("setting %s: %s", e.Key, e.Code) }

// ValidateGeneralSetting validates and normalises one Settings → General
// value exactly as the admin screen does, for the keys that need no other
// service: site.title, site.tagline (trimmed, max 500 runes), site.locale
// (a supported locale), site.timezone (an IANA name; "" and "Local" are
// rejected) and site.base_url (absolute http(s) origin, no trailing slash).
// It returns the value to store, or a *SettingError. Other keys (site.theme
// and site.front_page need the theme and content services, site.logo the
// media service) are rejected with code "admin.settings.error_key".
// contact.email must be empty or a plain address; site.posts_per_page an
// integer 1–100 (returned in canonical decimal form; store it as a number,
// see PostsPerPage).
func ValidateGeneralSetting(key, value string) (string, error) {
	fail := func(code string) (string, error) { return "", &SettingError{Key: key, Code: code} }
	switch key {
	case "site.title", "site.tagline":
		v := strings.TrimSpace(value)
		if utf8.RuneCountInString(v) > maxSettingLen {
			return fail("admin.settings.error_too_long")
		}
		return v, nil
	case "site.locale":
		if !validLocale(value) {
			return fail("admin.users.error_locale")
		}
		return value, nil
	case "site.timezone":
		tz := strings.TrimSpace(value)
		if !validTimezone(tz) {
			return fail("admin.settings.error_timezone")
		}
		return tz, nil
	case "site.base_url":
		u, ok := normalizeBaseURL(value)
		if !ok {
			return fail("admin.settings.error_base_url")
		}
		return u, nil
	case "contact.email":
		v := strings.TrimSpace(value)
		if v == "" {
			return "", nil
		}
		if a, err := mail.ParseAddress(v); err != nil || a.Address != v || len(v) > 254 {
			return fail("auth.error.email_invalid")
		}
		return v, nil
	case "site.posts_per_page":
		n, ok := PostsPerPage(value)
		if !ok {
			return fail("admin.settings.error_per_page")
		}
		return strconv.Itoa(n), nil
	}
	return fail("admin.settings.error_key")
}

// PostsPerPage parses a site.posts_per_page value (an integer 1–100).
func PostsPerPage(value string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	return n, err == nil && n >= 1 && n <= 100
}

// ValidateSEOSetting validates one Settings → SEO value. The rules belong to
// the seo module (seo.ValidateSetting); this alias keeps the admin settings
// validators in one place for callers such as mcp.
func ValidateSEOSetting(key, value string) (string, error) { return seo.ValidateSetting(key, value) }
