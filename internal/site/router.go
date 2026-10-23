package site

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
	"github.com/zadstack/gorun/internal/theme"
)

// dispatch resolves every public GET not matched by a specific route, in
// this order:
//
//  1. /{archive_slug}/      archive of a public type with has_archive
//  2. /{type}/{slug}        single item of a public non-page type
//  3. /{taxonomy}/{term}    term archive of a public taxonomy
//  4. /a/b/c                page path; every segment must match the page's
//     ancestor chain exactly (slugs are unique per type+locale, so the last
//     segment identifies the page and the parents are verified)
//  5. otherwise the themed 404 (after a redirects lookup)
func (h *handlers) dispatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	segs := splitPath(r.URL.Path)
	if len(segs) == 0 || len(segs) > maxPathSegments {
		h.notFound(w, r)
		return
	}
	tc := h.newContext(w, r)
	types := h.publicTypes(ctx)
	if len(segs) == 1 {
		if t, ok := archiveType(types, segs[0]); ok {
			h.archive(w, r, tc, t)
			return
		}
	}
	if len(segs) == 2 {
		if t, ok := types[segs[0]]; ok && t.Slug != "page" {
			if it := h.bySlug(ctx, t.Slug, segs[1]); it != nil {
				h.renderItem(w, r, tc, it)
				return
			}
		}
		if tax := h.publicTaxonomy(ctx, segs[0]); tax != nil {
			h.term(w, r, tc, tax, segs[1])
			return
		}
	}
	if _, ok := types["page"]; ok {
		if it := h.pageByPath(ctx, segs); it != nil {
			h.renderItem(w, r, tc, it)
			return
		}
	}
	h.notFound(w, r)
}

// splitPath splits a decoded URL path into non-empty segments.
func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func archiveType(types map[string]content.Type, seg string) (content.Type, bool) {
	for _, t := range types {
		slug := t.ArchiveSlug
		if slug == "" {
			slug = t.Slug
		}
		if t.HasArchive && slug == seg {
			return t, true
		}
	}
	return content.Type{}, false
}

func (h *handlers) publicTaxonomy(ctx context.Context, slug string) *taxonomy.Taxonomy {
	svc, ok := h.taxonomy()
	if !ok {
		return nil
	}
	tax, err := svc.Get(ctx, slug)
	if err != nil || tax == nil || !tax.Public {
		return nil
	}
	return tax
}

// pageByPath resolves /a/b/c: the page with the last slug whose parents'
// slugs match the preceding segments, with no further ancestors.
func (h *handlers) pageByPath(ctx context.Context, segs []string) *content.Item {
	page := h.bySlug(ctx, "page", segs[len(segs)-1])
	if page == nil {
		return nil
	}
	svc, _ := h.content()
	cur := page
	for i := len(segs) - 2; i >= 0; i-- {
		if cur.ParentID == "" {
			return nil
		}
		parent, err := svc.Get(ctx, cur.ParentID)
		if err != nil || parent.Slug != segs[i] || !h.visible(ctx, parent) {
			return nil
		}
		cur = parent
	}
	if cur.ParentID != "" {
		return nil
	}
	return page
}

func localized(m map[string]string, locale, fallback string) string {
	for _, l := range []string{locale, "en"} {
		if v := m[l]; v != "" {
			return v
		}
	}
	for _, v := range m {
		if v != "" {
			return v
		}
	}
	return fallback
}

func (h *handlers) archive(w http.ResponseWriter, r *http.Request, tc *theme.Context, t content.Type) {
	lib := theme.ParseLibrary(theme.LibrarySpecFor(t), r.URL)
	q := content.Query{Type: t.Slug}
	lib.Apply(&q)
	tc.Extra["library"] = lib
	if err := h.list(r.Context(), r, tc, q); err != nil {
		h.serverError(w, r, tc, err)
		return
	}
	tc.Extra["type"] = t
	tc.Extra["title"] = localized(t.Plural, tc.Locale, localized(t.Name, tc.Locale, t.Slug))
	h.render(w, r, kernel.KindRequest{Kind: theme.KindArchive, Type: t.Slug}, tc)
}

func (h *handlers) term(w http.ResponseWriter, r *http.Request, tc *theme.Context, tax *taxonomy.Taxonomy, slug string) {
	svc, _ := h.taxonomy()
	t, err := svc.GetTermBySlug(r.Context(), tax.Slug, slug)
	if err != nil || t == nil {
		h.notFound(w, r)
		return
	}
	if err := h.termItems(r, tc, tax, t.Slug); err != nil {
		h.serverError(w, r, tc, err)
		return
	}
	tc.Term = t
	tc.Extra["taxonomy"] = *tax
	tc.Extra["title"] = t.Name
	h.render(w, r, kernel.KindRequest{Kind: theme.KindTaxonomy, Taxonomy: tax.Slug, Term: t.Slug}, tc)
}

// termItems lists the term's items, restricted to the public content types
// attached to the taxonomy. With exactly one such type the query is narrowed
// to it (totals stay exact); with several, non-public items are dropped
// after the query (content.Query has a single Type); with none, the archive
// is empty.
func (h *handlers) termItems(r *http.Request, tc *theme.Context, tax *taxonomy.Taxonomy, term string) error {
	ctx := r.Context()
	public := h.publicTypes(ctx)
	var types []string
	if len(tax.Types) == 0 {
		for slug := range public {
			types = append(types, slug)
		}
	}
	for _, slug := range tax.Types {
		if _, ok := public[slug]; ok {
			types = append(types, slug)
		}
	}
	q := content.Query{Taxonomy: map[string][]string{tax.Slug: {term}}}
	switch len(types) {
	case 0:
		tc.Pagination = theme.NewPagination(1, h.perPage(ctx), 0)
		return nil
	case 1:
		// One type: the term archive is a library of that type, filterable
		// by its other taxonomies and fields; the term itself stays fixed.
		if t, ok := public[types[0]]; ok {
			spec := theme.LibrarySpecFor(t)
			spec.Taxonomies = slices.DeleteFunc(spec.Taxonomies, func(s string) bool { return s == tax.Slug })
			spec.Fixed = map[string][]string{tax.Slug: {term}}
			lib := theme.ParseLibrary(spec, r.URL)
			lib.Apply(&q)
			tc.Extra["library"] = lib
		}
		q.Type = types[0]
		return h.list(ctx, r, tc, q)
	}
	if err := h.list(ctx, r, tc, q); err != nil {
		return err
	}
	tc.Items = h.publicOnly(ctx, tc.Items)
	return nil
}
