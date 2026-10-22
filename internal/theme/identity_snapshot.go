package theme

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
}

// Get returns the theme identity value for key ("" when absent).
func (s IdentitySnapshot) Get(key string) string { return s.Values[key] }
