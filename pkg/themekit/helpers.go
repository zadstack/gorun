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

// TypeScaleTokens are the steps of the shared type scale themes/_base/tokens.css
// declares (xs < sm < md < base < lg < xl < 2xl < 3xl < 4xl < 5xl < display)
// with the line heights that pair with them. Body copy is --body-size; the
// small steps are fixed and the steps from lg up are fluid (clamp between a
// 360px and a 1280px viewport). Theme CSS writes var(--fs-*) and a theme may
// retune a step under DataThemeSelector(slug), unlike RoleTokens; the admin
// declares a compact instance of the same names in admin.css, and Tailwind's
// text-* utilities read the steps through --text-* in both builds.
var TypeScaleTokens = []string{
	"--fs-xs", "--fs-sm", "--fs-md", "--fs-base", "--fs-lg", "--fs-xl",
	"--fs-2xl", "--fs-3xl", "--fs-4xl", "--fs-5xl", "--fs-display",
	"--leading-tight", "--leading-snug", "--leading-normal", "--leading-relaxed",
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
