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

// RoleTokens are the shadcn role names themes/_base/tokens.css declares as
// aliases of the identity tokens (Source), the vocabulary the admin, Basecoat
// and the site builder share. They resolve on <html>, so a theme sets the
// source under DataThemeSelector(slug) and never the role name itself: a
// direct --primary would detach it from --accent and from the site identity.
var RoleTokens = []RoleToken{
	{"--background", "--bg"},
	{"--foreground", "--ink"},
	{"--card", "--panel"},
	{"--card-foreground", "--ink"},
	{"--popover", "--panel"},
	{"--popover-foreground", "--ink"},
	{"--primary", "--accent"},
	{"--primary-hover", "--accent-hover"},
	{"--primary-foreground", "--on-accent"},
	{"--secondary", "--panel-2"},
	{"--secondary-foreground", "--ink"},
	{"--muted-foreground", "--muted"},
	{"--destructive", "--red"},
	{"--destructive-foreground", "--on-accent"},
	{"--border", "--line"},
	{"--input", "--line"},
	{"--ring", "--accent"},
	{"--radius-sm", "--radius"},
	{"--radius-md", "--radius"},
	{"--radius-lg", "--radius-card"},
	{"--radius-xl", "--radius-card"},
	{"--font-sans", "--font"},
}

// RoleToken is one shadcn role name and the identity token it follows.
type RoleToken struct {
	Name, Source string
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
