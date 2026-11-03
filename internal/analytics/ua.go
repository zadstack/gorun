package analytics

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// maxPath is the longest stored path, in bytes.
const maxPath = 512

// maxUTM caps each stored utm_* value, in bytes.
const maxUTM = 128

// botMarkers are lower-case user-agent substrings of crawlers, headless
// browsers, uptime monitors, link previewers and HTTP libraries.
var botMarkers = []string{
	"bot", "crawl", "spider", "slurp", "headless", "phantomjs", "lighthouse", "pagespeed", "gtmetrix",
	"uptimerobot", "pingdom", "statuscake", "site24x7", "monitor", "newrelicpinger", "datadog", "checkly",
	"facebookexternalhit", "preview", "curl/", "wget/", "python-", "go-http-client", "okhttp", "java/",
	"httpclient", "libwww", "axios/", "node-fetch", "scrapy",
}

// parseUA reduces a user agent to coarse browser and OS families, and
// reports a bot (including an empty user agent). The raw string is never
// stored.
func parseUA(ua string) (browser, os string, bot bool) {
	s := strings.ToLower(ua)
	if strings.TrimSpace(s) == "" {
		return "other", "other", true
	}
	for _, m := range botMarkers {
		if strings.Contains(s, m) {
			return "other", "other", true
		}
	}
	switch {
	case strings.Contains(s, "edg/") || strings.Contains(s, "edga/") || strings.Contains(s, "edgios/") || strings.Contains(s, "edge/"):
		browser = "Edge"
	case strings.Contains(s, "samsungbrowser/"):
		browser = "Samsung"
	case strings.Contains(s, "opr/") || strings.Contains(s, "opera") || strings.Contains(s, "opios/"):
		browser = "Opera"
	case strings.Contains(s, "firefox/") || strings.Contains(s, "fxios/"):
		browser = "Firefox"
	case strings.Contains(s, "chrome/") || strings.Contains(s, "crios/") || strings.Contains(s, "chromium/"):
		browser = "Chrome"
	case strings.Contains(s, "safari/") && strings.Contains(s, "applewebkit"):
		browser = "Safari"
	default:
		browser = "other"
	}
	switch {
	case strings.Contains(s, "iphone") || strings.Contains(s, "ipad") || strings.Contains(s, "ipod"):
		os = "iOS"
	case strings.Contains(s, "android"):
		os = "Android"
	case strings.Contains(s, "windows"):
		os = "Windows"
	case strings.Contains(s, "macintosh") || strings.Contains(s, "mac os x"):
		os = "macOS"
	case strings.Contains(s, "linux") || strings.Contains(s, "x11"):
		os = "Linux"
	default:
		os = "other"
	}
	return browser, os, false
}

// deviceFor buckets a viewport width: below 768 is mobile, below 1024 is
// tablet, anything else desktop. A missing width (0 or less) is unknown.
func deviceFor(width int) string {
	switch {
	case width <= 0:
		return "unknown"
	case width < 768:
		return "mobile"
	case width < 1024:
		return "tablet"
	default:
		return "desktop"
	}
}

// refHost reduces a referrer to its lower-case host without port. An empty
// or unparsable referrer, or one from siteHost itself, is "direct".
func refHost(ref, siteHost string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "direct"
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "direct"
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if h == "" || len(h) > 253 || h == strings.ToLower(siteHost) {
		return "direct"
	}
	return h
}

// cleanPath keeps a site-relative path without its query and fragment,
// in canonical form (canonicalPath) and cut to maxPath bytes without
// splitting an escape, plus the utm_source, utm_medium and utm_campaign
// query values (every other query value is dropped). A value that is not
// a site path ("" or not starting with a single "/", before or after
// canonicalisation) returns "".
func cleanPath(raw string) (path string, utm [3]string) {
	raw, _, _ = strings.Cut(raw, "#")
	p, q, _ := strings.Cut(raw, "?")
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\x00\r\n\t\\") {
		return "", utm
	}
	if p = canonicalPath(p); strings.HasPrefix(p, "//") {
		return "", utm
	}
	if q != "" {
		vals, _ := url.ParseQuery(q)
		for i, k := range [3]string{"utm_source", "utm_medium", "utm_campaign"} {
			utm[i] = truncate(strings.TrimSpace(vals.Get(k)), maxUTM)
		}
	}
	return truncateEscaped(p, maxPath), utm
}

// canonicalPath is the one form every stored and compared path takes:
// decoded, then re-escaped as net/url escapes a path (upper-case hex,
// UTF-8 percent-encoded). location.pathname (views) is encoded, site's
// form source (submits) and an operator's typed exclusion are decoded,
// and a request's EscapedPath may use either; all meet here. A value that
// does not decode is kept as given.
func canonicalPath(p string) string {
	d, err := url.PathUnescape(p)
	if err != nil {
		return p
	}
	return (&url.URL{Path: d}).EscapedPath()
}

// truncateEscaped is truncate for a canonical path: a cut never splits a
// %XX escape, nor the escapes of one UTF-8 character (up to three trailing
// escapes are dropped until the path decodes to valid UTF-8).
func truncateEscaped(s string, n int) string {
	s = truncate(s, n)
	switch l := len(s); {
	case l >= 1 && s[l-1] == '%':
		s = s[:l-1]
	case l >= 2 && s[l-2] == '%':
		s = s[:l-2]
	}
	for range utf8.UTFMax - 1 {
		if d, err := url.PathUnescape(s); err != nil || utf8.ValidString(d) {
			break
		}
		if l := len(s); l >= 3 && s[l-3] == '%' {
			s = s[:l-3]
		}
	}
	return s
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n-- // s[n] continues the rune before it: cut before that rune
	}
	return s[:n]
}
