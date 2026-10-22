package theme

import (
	"slices"
	"strconv"
	"strings"
)

// IdentityKeys are the theme.<slug>.* keys the identity page owns, in form
// order. Every value is a string; "" means "keep the theme's design".
var IdentityKeys = []string{idAccent, idBackground, idText, idHeading, idBody, idLatinFont, idScheme, idRadius, idSpacing, idCustomCSS}

// SiteIdentityKeys are the global settings edited alongside the theme
// identity: name, tagline and the logo variants (media ids).
var SiteIdentityKeys = []string{"site.title", "site.tagline", "site.logo", "site.logo_light", "site.logo_dark", "site.icon"}

// IdentitySnapshot is one complete identity state for a theme: its
// theme.<slug>.identity_* values plus the site keys. Drafts, revisions and
// previews all carry one.
type IdentitySnapshot struct {
	Theme  string            `json:"theme"`
	Values map[string]string `json:"values"`
	Site   map[string]string `json:"site"`
	// Links are the theme's header link settings (cta_label, cta_url) when
	// the theme declares them; they are written with the identity but are
	// ordinary theme settings, not design tokens.
	Links map[string]string `json:"links,omitempty"`
}

// lookup answers a settings key read by templates during a preview: the
// theme's identity keys and the logo settings. Site title and tagline go
// through the render context instead.
func (s IdentitySnapshot) lookup(key string) (string, bool) {
	if v, ok := logoKeys[key]; ok && v {
		return s.Site[key], true
	}
	prefix := "theme." + s.Theme + "."
	if strings.HasPrefix(key, prefix) {
		k := strings.TrimPrefix(key, prefix)
		if slices.Contains(IdentityKeys, k) {
			return s.Values[k], true
		}
		if v, ok := s.Links[k]; ok && slices.Contains(identityLinkKeys, k) {
			return v, true
		}
	}
	return "", false
}

var logoKeys = map[string]bool{"site.logo": true, "site.logo_light": true, "site.logo_dark": true, "site.icon": true}

// Get returns the theme identity value for key ("" when absent).
func (s IdentitySnapshot) Get(key string) string { return s.Values[key] }

// IdentityFontLabel returns the display name of an identity font value from
// either font list, or the value itself when unknown ("" for "").
func IdentityFontLabel(value string) string {
	if value == "" {
		return ""
	}
	if f, ok := findFontIn(identityFonts, value); ok {
		return f.Label
	}
	if f, ok := findFontIn(latinFonts, value); ok {
		return f.Label
	}
	return value
}

// ContrastRatio formats the WCAG contrast between two hex colours ("" when
// either is not a colour).
func ContrastRatio(a, b string) string {
	r := contrast(a, b)
	if r == 0 {
		return ""
	}
	return strconv.FormatFloat(r, 'f', 1, 64) + ":1"
}
