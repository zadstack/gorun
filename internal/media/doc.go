// Package media manages the media library, storage drivers and image variants.
//
// Uploads are sniffed (content must match the extension) and checked
// against the media.allowed_mimes allowlist and media.max_upload_mb.
//
// SVG security: uploaded SVG is sanitized and is also served with a
// sandboxing Content-Security-Policy. The sanitizer keeps allowlisted
// elements in the SVG namespace only and drops event handlers; hrefs are
// checked per element (local fragments, embedded raster data:, http(s)/
// mailto links on <a>). An attribute value is dropped, and a whole <style>
// element is dropped, when it contains (after XML entity decoding, case-
// and whitespace-insensitively) a script URL, @import, expression(), a
// url() other than url(#local), one of the resource-loading functions
// image-set()/-webkit-image-set(), src(), image(), cross-fade(), or any
// backslash: CSS escapes ("\75rl(", "@\69mport") could otherwise spell
// those, and plain SVG never needs them. This blocks the known script and
// external-fetch vectors; it is defense in depth, not a guarantee: themes
// or editors that INLINE SVG markup into HTML must still treat it as
// untrusted (prefer <img src> for media SVG).
//
// contract.go holds the frozen wave-0 contract (types and interfaces only).
package media
