// Package starter holds the content a new site starts with (Arabic sample
// posts, pages and categories as WXR), so a fresh site is never empty.
package starter

import _ "embed"

// WXR is the starter content, importable with the WordPress importer.
//
//go:embed sample-content.xml
var WXR []byte
