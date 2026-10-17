package seo

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/hooks"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/settings"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// Settings keys read by the service (site.* are owned by the admin module).
const (
	SettingSiteTitle          = "site.title"
	SettingSiteTagline        = "site.tagline"
	SettingSiteBaseURL        = "site.base_url"
	SettingTitleTemplate      = "seo.title_template"
	SettingDefaultDescription = "seo.default_description"
	SettingDefaultOGImage     = "seo.default_og_image"
	SettingRobotsTxt          = "seo.robots_txt"

	// DefaultTitleTemplate formats page titles; {title} and {site} are
	// replaced.
	DefaultTitleTemplate = "{title} – {site}"
)

type service struct {
	settings *settings.Store
	hooks    *hooks.Registry
	log      *slog.Logger
	baseURL  string // config fallback for site.base_url
	content  func() (content.Service, bool)
	taxonomy func() (taxonomy.Service, bool)
}

func first(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func (s *service) setting(ctx context.Context, key string) string {
	return strings.TrimSpace(s.settings.GetString(ctx, key, ""))
}

// base returns the site's absolute base URL without a trailing slash
// (kernel.BaseURLOf: setting site.base_url, else the configured URL).
func (s *service) base(ctx context.Context) string {
	return kernel.BaseURLOf(ctx, s.settings, s.baseURL)
}

// absolute makes site paths absolute and drops anything that is neither a
// site path nor an http(s) URL (so javascript: can never reach an attribute).
func absolute(base, u string) string {
	u = strings.TrimSpace(u)
	l := strings.ToLower(u)
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(l, "http://"), strings.HasPrefix(l, "https://"):
		return u
	case strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//"):
		return base + u
	}
	return ""
}

// firstURL returns the first candidate that absolute accepts.
func firstURL(base string, candidates ...string) string {
	for _, c := range candidates {
		if u := absolute(base, c); u != "" {
			return u
		}
	}
	return ""
}

// formatTitle applies the title template to a page title.
func formatTitle(tmpl, title, site string) string {
	if site == "" || title == site {
		return title
	}
	if tmpl == "" {
		tmpl = DefaultTitleTemplate
	}
	return strings.NewReplacer("{title}", title, "{site}", site).Replace(tmpl)
}

// Render returns the head tags for a page. m (the item's own SEO values)
// wins over fallback (values derived from the page, e.g. its title and
// excerpt), which wins over site settings. An explicit m.Title is used
// verbatim; a fallback title goes through seo.title_template. Every value is
// HTML-escaped; Schema is re-encoded as JSON-LD with <, > and & escaped.
func (s *service) Render(ctx context.Context, m Meta, fallback Meta) template.HTML {
	site := s.setting(ctx, SettingSiteTitle)
	tagline := s.setting(ctx, SettingSiteTagline)
	base := s.base(ctx)

	title := strings.TrimSpace(m.Title)
	if title == "" {
		if fb := strings.TrimSpace(fallback.Title); fb != "" {
			title = formatTitle(first(s.setting(ctx, SettingTitleTemplate), DefaultTitleTemplate), fb, site)
		} else if tagline != "" && site != "" {
			title = site + " – " + tagline
		} else {
			title = site
		}
	}
	desc := first(m.Description, fallback.Description, s.setting(ctx, SettingDefaultDescription), tagline)
	canonical := firstURL(base, m.Canonical, fallback.Canonical)
	robots := first(m.Robots, fallback.Robots)
	ogTitle := first(m.OGTitle, fallback.OGTitle, m.Title, fallback.Title, site)
	ogDesc := first(m.OGDescription, fallback.OGDescription, desc)
	ogImage := firstURL(base, m.OGImage, fallback.OGImage, s.setting(ctx, SettingDefaultOGImage))
	card := first(m.TwitterCard, fallback.TwitterCard)
	if card == "" {
		card = "summary"
		if ogImage != "" {
			card = "summary_large_image"
		}
	}

	var b strings.Builder
	esc := template.HTMLEscapeString
	if title != "" {
		b.WriteString("<title>" + esc(title) + "</title>\n")
	}
	meta := func(attr, key, val string) {
		if val != "" {
			b.WriteString(`<meta ` + attr + `="` + key + `" content="` + esc(val) + `">` + "\n")
		}
	}
	meta("name", "description", desc)
	meta("name", "robots", robots)
	if canonical != "" {
		b.WriteString(`<link rel="canonical" href="` + esc(canonical) + `">` + "\n")
	}
	meta("property", "og:type", "website")
	meta("property", "og:site_name", site)
	meta("property", "og:title", ogTitle)
	meta("property", "og:description", ogDesc)
	meta("property", "og:url", canonical)
	meta("property", "og:image", ogImage)
	meta("name", "twitter:card", card)
	meta("name", "twitter:title", ogTitle)
	meta("name", "twitter:description", ogDesc)
	meta("name", "twitter:image", ogImage)
	if ld := jsonLD(first(string(m.Schema), string(fallback.Schema))); ld != "" {
		b.WriteString(`<script type="application/ld+json">` + ld + "</script>\n")
	}
	return template.HTML(b.String())
}

// jsonLD validates raw JSON (an object or array) and compacts it, then
// escapes <, >, &, U+2028 and U+2029 with json.HTMLEscape, so the result
// cannot close the script element. Bytes are kept as written (no
// round-trip through any, so key order and number precision survive).
func jsonLD(raw string) string {
	trimmed := bytes.TrimSpace([]byte(raw))
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return ""
	}
	var out bytes.Buffer
	json.HTMLEscape(&out, compact.Bytes())
	return out.String()
}

// Robots returns robots.txt: the seo.robots_txt setting or a default that
// allows everything and points at the sitemap.
func (s *service) Robots(ctx context.Context) string {
	if v := s.settings.GetString(ctx, SettingRobotsTxt, ""); strings.TrimSpace(v) != "" {
		if !strings.HasSuffix(v, "\n") {
			v += "\n"
		}
		return v
	}
	return DefaultRobots(s.base(ctx))
}

// DefaultRobots is the robots.txt used when seo.robots_txt is unset.
func DefaultRobots(base string) string {
	return "User-agent: *\nAllow: /\nSitemap: " + strings.TrimRight(base, "/") + "/sitemap.xml\n"
}
