// Package web embeds the admin templates and static assets.
package web

import "embed"

// FS holds admin/templates/**, admin/static/**, launch/** and site/**.
// site/core.css is the core site stylesheet every theme links (see theme.CoreCSSURL).
// launch/launch.css is served at /static/launch.css (replaces a Traefik file mount).
//
//go:embed admin/templates admin/static launch site
var FS embed.FS
