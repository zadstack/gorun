package themekit

import (
	"fmt"
	"path"
	"strings"
)

// IdentityTokens are CSS custom properties site identity can drive.
// Catalog themes declare defaults under DataThemeSelector(slug).
var IdentityTokens = []string{
	"--accent",
	"--gp-accent",
	"--green",
	"--bg",
	"--ink",
	"--on-accent",
	"--gp-on-accent",
	"--font",
	"--font-ui",
	"--font-heading",
	"--radius",
}

// AssetPath builds the public URL path for a theme asset (no cache buster).
// Templates use {{asset "…"}} at render time; this helper is for tooling.
func AssetPath(slug, rel string) string {
	rel = path.Clean("/" + strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "/"))
	rel = strings.TrimPrefix(rel, "/")
	return "/themes/" + slug + "/assets/" + rel
}

// DataThemeSelector returns the zero-specificity selector catalog themes
// use for token defaults.
func DataThemeSelector(slug string) string {
	return fmt.Sprintf(`:where(html[data-theme=%q])`, slug)
}

// SettingKey returns the settings store key for a theme setting field.
func SettingKey(slug, field string) string {
	return "theme." + slug + "." + field
}
