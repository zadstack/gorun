package content

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

// iframeSrc is the embed allowlist of the "cms" policy.
var iframeSrc = regexp.MustCompile(`^https://(www\.youtube\.com/embed/|www\.youtube-nocookie\.com/embed/|player\.vimeo\.com/video/|www\.google\.com/maps/embed|open\.spotify\.com/embed/|w\.soundcloud\.com/player/)`)

// editorClass matches Quill's formatting classes (ql-align-right,
// ql-direction-rtl, ql-indent-2, ...) and code-highlight language classes,
// so alignment and RTL direction survive sanitizing.
var editorClass = regexp.MustCompile(`^((ql-[a-z0-9-]+|gp-[a-z0-9_-]+|language-[a-z0-9+-]+)( |$))+$`)

var cmsPolicy = sync.OnceValue(func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy() // no data-*, no style, no scripts or handlers
	p.AllowElements("section", "figure", "figcaption", "details", "summary", "mark", "small", "u", "s", "sub", "sup", "hr", "br", "iframe", "picture", "source")
	p.AllowAttrs("open").OnElements("details")
	// A builder feature switcher groups its panels with name="gp-switch-<section id>",
	// which makes them exclusive (one open at a time) without JavaScript.
	p.AllowAttrs("name").Matching(regexp.MustCompile(`^gp-switch-[A-Za-z0-9_-]{1,80}$`)).OnElements("details")
	// Native comparison tables own horizontal scrolling. Keep a keyboard focus
	// target and a bounded accessible name, without permitting focus reordering.
	p.AllowAttrs("tabindex").Matching(regexp.MustCompile(`^0$`)).OnElements("div")
	p.AllowAttrs("role").Matching(regexp.MustCompile(`^region$`)).OnElements("div")
	p.AllowAttrs("aria-label").Matching(regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,300}$`)).OnElements("div")
	p.AllowAttrs("scope").Matching(regexp.MustCompile(`^(col|row)$`)).OnElements("th")
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^gp-[A-Za-z0-9_-]{1,80}$`)).OnElements("section")
	p.AllowAttrs("loading", "width", "height", "srcset", "sizes").OnElements("img")
	// bluemonday's UGC base matches img alt against a Latin-only pattern, so a
	// localized alt with an Arabic comma (،), guillemets («») or a diacritic was
	// dropped whole on save, silently losing it for assistive tech. Allow any
	// printable text (no control characters, no angle brackets); the value is
	// HTML-escaped on output, as with the div aria-label above. Empty alt (a
	// decorative image) stays valid.
	p.AllowAttrs("alt").Matching(regexp.MustCompile(`^[^\x00-\x1f\x7f<>]{0,1000}$`)).OnElements("img")
	p.AllowAttrs("srcset", "sizes").OnElements("source")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^image/(avif|webp|png|jpeg|gif)$`)).OnElements("source")
	p.AllowAttrs("dir").Matching(regexp.MustCompile(`^(rtl|ltr|auto)$`)).Globally()
	p.AllowAttrs("class").Matching(editorClass).Globally()
	p.AllowAttrs("src").Matching(iframeSrc).OnElements("iframe")
	// Embeds get no Permissions-Policy "allow" list and are always sandboxed
	// (scripts and their own origin only, so players still work).
	p.AllowAttrs("width", "height", "title", "allowfullscreen", "frameborder", "loading").OnElements("iframe")
	p.RequireSandboxOnIFrame(bluemonday.SandboxAllowScripts, bluemonday.SandboxAllowSameOrigin,
		bluemonday.SandboxAllowPresentation, bluemonday.SandboxAllowPopups)
	p.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	return p
})

// Policy returns the "cms" sanitizer policy (spec §4.2): bluemonday's UGC
// base (headings, lists, tables, links, images, code) plus figure/figcaption,
// details/summary, iframes from an allowlist, editor classes and dir.
// data-* attributes, inline styles, scripts and event handlers are stripped.
func Policy() *bluemonday.Policy { return cmsPolicy() }

// iframeSandbox is the fixed sandbox every embedded iframe gets.
const iframeSandbox = `sandbox="allow-scripts allow-same-origin allow-presentation allow-popups"`

var (
	iframeTag   = regexp.MustCompile(`<iframe\b[^>]*>`)
	sandboxAttr = regexp.MustCompile(`\s+sandbox="[^"]*"`)
)

// Sanitize cleans HTML with the "cms" policy, then pins every iframe's
// sandbox to iframeSandbox (bluemonday can filter sandbox values but not set
// them, and a bare sandbox="" would break video players).
func Sanitize(html string) string {
	if html == "" {
		return ""
	}
	out := cmsPolicy().Sanitize(html)
	out = mediaTag.ReplaceAllStringFunc(out, func(tag string) string {
		return srcsetAttr.ReplaceAllStringFunc(tag, sanitizeSrcset)
	})
	return iframeTag.ReplaceAllStringFunc(out, func(tag string) string {
		tag = sandboxAttr.ReplaceAllString(tag, "")
		return strings.TrimSuffix(tag, ">") + " " + iframeSandbox + ">"
	})
}

var mediaTag = regexp.MustCompile(`<(?i:img|source)\b[^>]*>`)

var srcsetAttr = regexp.MustCompile(`\s+srcset="([^"]*)"`)

func sanitizeSrcset(attr string) string {
	value := html.UnescapeString(srcsetAttr.FindStringSubmatch(attr)[1])
	for _, candidate := range strings.Split(value, ",") {
		fields := strings.Fields(candidate)
		if len(fields) == 0 {
			return ""
		}
		u, err := url.Parse(fields[0])
		if err != nil || (u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http") {
			return ""
		}
	}
	return attr
}
