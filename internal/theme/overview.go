package theme

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// The site overview (Appearance → Site overview): the site's identity and
// the active theme's design tokens, then every public page from the
// sitemap, grouped, each previewed in a scaled, lazily loaded iframe.
// Public previews stay read-only. Real content can open the separately
// reviewed assistant workflow; synthetic routes have no edit action.

const (
	adminOverviewPath = "/appearance/overview"
	overviewPerGroup  = 24 // previews per group; the rest are counted
)

// Label is shown as is; LabelKey (when set) is translated in the template.
type overviewPage struct {
	URL, Path, Label, LabelKey string
	ContentID                  string
}

type overviewGroup struct {
	ID, Label, LabelKey string
	Pages               []overviewPage
	More                int // pages not shown
	Count               int // all pages of the group
}

type designToken struct {
	Name, Value string
	// Style previews the token (a swatch background or a font-family);
	// set only for values made of safe characters.
	Style template.CSS
}

type overviewIdentity struct {
	Title, Tagline, BaseURL, Locale, LogoURL string
	Theme                                    Info
	HasScreenshot                            bool
	Colors, Fonts, Other                     []designToken
	Roles                                    []colorRole
	Contrast                                 []contrastRow
	Type                                     themeFonts
	Specimen                                 specimenStyle
}

func (a *admin) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	groups, err := a.overviewGroups(ctx)
	if err != nil {
		a.s.log.Error("theme: overview", "err", err)
	}
	total := 0
	for _, g := range groups {
		total += g.Count
	}
	a.tpl.Render(w, r, "pages/appearance/overview.html", map[string]any{
		"Identity": a.identity(ctx), "Groups": groups, "Total": total,
	})
}

func (a *admin) identity(ctx context.Context) overviewIdentity {
	st := a.s.d.Settings
	id := overviewIdentity{Theme: a.s.Active(ctx)}
	if st != nil {
		id.Title = st.GetString(ctx, "site.title", "")
		id.Tagline = st.GetString(ctx, "site.tagline", "")
		id.BaseURL = st.GetString(ctx, "site.base_url", a.s.d.Config.BaseURL)
		id.Locale = st.GetString(ctx, "site.locale", "")
		if logo := st.GetString(ctx, "site.logo", ""); logo != "" {
			if ms, ok := kernel.Get[media.Service](a.s.d, kernel.ServiceMedia); ok {
				if m, err := ms.Get(ctx, logo); err == nil {
					id.LogoURL = m.URL
				}
			}
		}
	}
	if id.Theme.Dir != "" {
		_, err := os.Stat(filepath.Join(id.Theme.Dir, "screenshot.png"))
		id.HasScreenshot = err == nil
	}
	chain := a.s.themeChain(id.Theme)
	id.Colors, id.Fonts, id.Other = a.s.designTokens(chain)
	id.Roles = roles(id.Colors)
	id.Contrast = contrastRows(id.Roles)
	id.Type = a.s.fonts(chain, id.Fonts)
	radius := ""
	for _, t := range id.Other {
		if strings.HasSuffix(t.Name, "radius") {
			radius = t.Value
			break
		}
	}
	id.Specimen = specimen(id.Roles, id.Type, radius)
	return id
}

var (
	rootBlockRe = regexp.MustCompile(`(?s):root\s*\{(.*?)\}`)
	cssVarRe    = regexp.MustCompile(`--([a-zA-Z0-9-]+)\s*:\s*([^;]+)`)
	safeCSSRe   = regexp.MustCompile(`^[A-Za-z0-9#%(),.\s"'_-]{1,200}$`)
	colorRe     = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|(rgb|rgba|hsl|hsla|oklch|oklab|color-mix)\(.*\))$`)
)

// designTokens reads the custom properties in :root of the theme chain's
// stylesheets (parent first, so the child's values win) and sorts them
// into colors, fonts and the rest.
func (s *service) designTokens(chain []Info) (colors, fonts, other []designToken) {
	vals := map[string]string{}
	var order []string
	for _, info := range chain {
		files, _ := filepath.Glob(filepath.Join(info.Dir, "assets", "*.css"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil || len(b) > 2<<20 {
				continue
			}
			for _, block := range rootBlockRe.FindAllStringSubmatch(string(b), -1) {
				for _, m := range cssVarRe.FindAllStringSubmatch(block[1], -1) {
					name, val := m[1], strings.TrimSpace(m[2])
					if _, seen := vals[name]; !seen {
						order = append(order, name)
					}
					vals[name] = val
				}
			}
		}
	}
	for _, name := range order {
		tok := designToken{Name: "--" + name, Value: vals[name]}
		safe := safeCSSRe.MatchString(tok.Value)
		switch {
		case colorRe.MatchString(tok.Value):
			if safe {
				tok.Style = template.CSS("background: " + tok.Value)
			}
			colors = append(colors, tok)
		case strings.Contains(name, "font"):
			if safe {
				tok.Style = template.CSS("font-family: " + tok.Value)
			}
			fonts = append(fonts, tok)
		case len(other) < 24:
			other = append(other, tok)
		}
	}
	return colors, fonts, other
}

// overviewGroups turns the sitemap into groups: the home page, one group per
// content type, archives, one group per taxonomy, then search and a 404.
func (a *admin) overviewGroups(ctx context.Context) ([]overviewGroup, error) {
	svc, ok := kernel.Get[seo.Service](a.s.d, kernel.ServiceSEO)
	if !ok {
		return nil, nil
	}
	entries, err := svc.Entries(ctx)
	if err != nil {
		return nil, err
	}
	locale := i18n.Locale(ctx)
	types, taxes := a.labels(ctx, locale)
	byID := map[string]*overviewGroup{}
	var order []string
	put := func(g overviewGroup, p overviewPage) {
		cur, ok := byID[g.ID]
		if !ok {
			cur = &g
			byID[g.ID] = cur
			order = append(order, g.ID)
		}
		if len(cur.Pages) < overviewPerGroup {
			cur.Pages = append(cur.Pages, p)
		} else {
			cur.More++
		}
	}
	assistantTypes := map[string]bool{}
	user := auth.UserFromContext(ctx)
	if user != nil && auth.Can(ctx, "content.edit") {
		if cs, ok := kernel.Get[content.Service](a.s.d, kernel.ServiceContent); ok {
			if ts, err := cs.Types(ctx); err == nil {
				for _, typ := range ts {
					assistantTypes[typ.Slug] = slices.Contains(typ.Supports, "body")
				}
			}
		}
	}
	home := ""
	for _, e := range entries {
		p := overviewPage{URL: e.Loc, Path: pathOf(e.Loc)}
		switch {
		case e.Item != nil:
			p.Label = e.Item.Title
			if assistantTypes[e.Item.Type] && e.Item.Status != "trash" && ((user.Role != "author" && user.Role != "contributor") || e.Item.AuthorID == user.ID) {
				p.ContentID = e.Item.ID
			}
			put(overviewGroup{ID: "type-" + e.Item.Type, Label: or(types[e.Item.Type], e.Item.Type)}, p)
		case e.Term != nil:
			p.Label = e.Term.Name
			put(overviewGroup{ID: "tax-" + e.Term.Taxonomy, Label: or(taxes[e.Term.Taxonomy], e.Term.Taxonomy)}, p)
		case e.Type != nil:
			p.Label = or(types[e.Type.Slug], e.Type.Slug)
			put(overviewGroup{ID: "archives", LabelKey: "theme.overview.archives"}, p)
		default:
			home = e.Loc
			p.LabelKey = "theme.overview.home"
			put(overviewGroup{ID: "main", LabelKey: "theme.overview.main"}, p)
		}
	}
	if home != "" {
		base := strings.TrimSuffix(home, "/")
		checks := overviewGroup{ID: "checks", LabelKey: "theme.overview.checks"}
		put(checks, overviewPage{URL: base + "/search?q=a", Path: "/search?q=a", LabelKey: "theme.overview.search"})
		put(checks, overviewPage{URL: base + "/gp-404-reference", Path: "/gp-404-reference", LabelKey: "theme.overview.not_found"})
	}
	out := make([]overviewGroup, 0, len(order))
	for _, id := range order {
		g := *byID[id]
		g.Count = len(g.Pages) + g.More
		out = append(out, g)
	}
	return out, nil
}

// labels returns content type and taxonomy names (plural) in locale.
func (a *admin) labels(ctx context.Context, locale string) (types, taxes map[string]string) {
	types, taxes = map[string]string{}, map[string]string{}
	if cs, ok := kernel.Get[content.Service](a.s.d, kernel.ServiceContent); ok {
		if list, err := cs.Types(ctx); err == nil {
			for _, t := range list {
				types[t.Slug] = pick(t.Plural, locale, pick(t.Name, locale, t.Slug))
			}
		}
	}
	if ts, ok := kernel.Get[taxonomy.Service](a.s.d, kernel.ServiceTaxonomy); ok {
		if list, err := ts.Taxonomies(ctx); err == nil {
			for _, t := range list {
				taxes[t.Slug] = pick(t.Name, locale, t.Slug)
			}
		}
	}
	return types, taxes
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func pick(m map[string]string, locale, fallback string) string {
	if v := m[locale]; v != "" {
		return v
	}
	if v := m["en"]; v != "" {
		return v
	}
	return fallback
}

func pathOf(loc string) string {
	u, err := url.Parse(loc)
	if err != nil || u.Path == "" {
		return "/"
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery
	}
	return u.Path
}
