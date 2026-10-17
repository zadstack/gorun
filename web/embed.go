// Package web embeds the admin templates and static assets.
package web

import "embed"

// FS holds admin/templates/** and admin/static/**.
//
//go:embed admin/templates admin/static
var FS embed.FS
