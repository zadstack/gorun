// Package web embeds the admin templates and static assets.
package web

import "embed"

// FS holds admin/templates/**, admin/static/**, and launch/**.
// launch/launch.css is served at /static/launch.css (replaces a Traefik file mount).
//
//go:embed admin/templates admin/static launch
var FS embed.FS
