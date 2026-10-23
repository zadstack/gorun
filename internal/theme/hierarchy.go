package theme

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/kernel"
)

// Built-in request kinds (spec §4.3). Modules add more with
// kernel.ThemeRegistry.AddKind (e.g. commerce: shop, product).
const (
	KindSingle   = "single"
	KindPage     = "page"
	KindHome     = "home"
	KindArchive  = "archive"
	KindTaxonomy = "taxonomy"
	KindSearch   = "search"
	Kind404      = "404"
	Kind500      = "500"
)

const indexTemplate = "index.html"

// ErrNoTemplate is returned when no candidate template exists in the theme.
var ErrNoTemplate = errors.New("theme: no template matches")

// candidates lists the template names for req, most specific first, exactly
// per spec §4.3. Candidates with an empty slot (e.g. "page-.html") are
// skipped. Kinds registered by modules get index.html appended; unknown
// kinds resolve to index.html only.
func candidates(req kernel.KindRequest, reg *kernel.ThemeRegistry) []string {
	var c []string
	add := func(parts ...string) {
		for _, p := range parts {
			if p == "" {
				return
			}
		}
		c = append(c, strings.Join(parts, "-")+".html")
	}
	switch req.Kind {
	case KindSingle:
		add("single", req.Type, req.Slug)
		add("single", req.Type)
		c = append(c, "single.html", indexTemplate)
	case KindPage:
		add("page", req.Slug)
		add("page", pageTemplateName(req.Template))
		if slices.Contains(PagePurposes, req.Purpose) {
			add("page", req.Purpose)
		}
		c = append(c, "page.html", "single.html", indexTemplate)
	case KindHome:
		c = append(c, "front-page.html", "home.html", indexTemplate)
	case KindArchive:
		add("archive", req.Type)
		c = append(c, "archive.html", indexTemplate)
	case KindTaxonomy:
		add("taxonomy", req.Taxonomy, req.Term)
		add("taxonomy", req.Taxonomy)
		c = append(c, "taxonomy.html", "archive.html", indexTemplate)
	case KindSearch:
		c = append(c, "search.html", "archive.html")
	case Kind404:
		c = append(c, "404.html", indexTemplate)
	case Kind500:
		c = append(c, "500.html")
	default:
		if reg != nil {
			c = append(c, reg.Candidates(req)...)
		}
		c = append(c, indexTemplate)
	}
	return slices.Compact(c)
}

// pageTemplateName normalises a content item's template ("wide",
// "page-wide" or "page-wide.html") to the slot used in page-{template}.html.
func pageTemplateName(t string) string {
	t = strings.TrimSuffix(strings.TrimSpace(t), ".html")
	return strings.TrimPrefix(t, "page-")
}

// resolve returns the first candidate for which exists reports true.
func resolve(req kernel.KindRequest, reg *kernel.ThemeRegistry, exists func(string) bool) (string, error) {
	cands := candidates(req, reg)
	for _, name := range cands {
		if exists(name) {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w: kind %q (tried %s)", ErrNoTemplate, req.Kind, strings.Join(cands, ", "))
}
