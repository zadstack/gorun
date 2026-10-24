package theme

import (
	"cmp"
	"slices"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// PagePurposes are the values of the page type's "purpose" field (content
// migration 0007). Only these select a page-<purpose>.html template or a
// {{destination}}: a hand-edited or custom value never becomes a file name.
var PagePurposes = []string{"about", "service", "methodology", "contact"}

// PagePurpose returns it's purpose when it is one of PagePurposes, else "".
func PagePurpose(it *content.Item) string {
	if it == nil {
		return ""
	}
	p, _ := it.Fields["purpose"].(string)
	if slices.Contains(PagePurposes, p) {
		return p
	}
	return ""
}

// Destination is where a theme links for a purpose. Available is false when
// no published public page serves it. Fallback is true when the page found is
// in another locale (Locale says which): templates must say so rather than
// present it as the current language.
type Destination struct {
	Available bool
	Fallback  bool
	Purpose   string
	URL       string
	Title     string
	Locale    string
	Item      *content.Item
}

// Adjacent is the previous (older) and next (newer) published item of the same
// type and locale, ordered by publication date then id. Either may be nil.
type Adjacent struct {
	Prev *content.Item
	Next *content.Item
}

// TermCount is a term with the number of published public items of one
// content type in the page's locale.
type TermCount struct {
	Term  taxonomy.Term
	Count int
}

const (
	relatedMax   = 12
	adjacentScan = 1000
	termsOfMax   = 50
)

func (f *funcs) contentService() (content.Service, bool) {
	return kernel.Get[content.Service](f.s.d, kernel.ServiceContent)
}

// pageLocale is the locale the page renders in.
func (f *funcs) pageLocale() string {
	if f.st.tc.Locale != "" {
		return f.st.tc.Locale
	}
	return f.st.tc.Site.Locale
}

// itemLocale is the locale an item belongs to: items stored without one
// belong to the site locale.
func (f *funcs) itemLocale(it *content.Item) string {
	if it.Locale != "" {
		return it.Locale
	}
	return f.st.tc.Site.Locale
}

// published reports whether it is published and publicly readable (staff
// previews of drafts never count as a destination, a related item or a
// neighbour).
func (f *funcs) published(it *content.Item) bool {
	return it != nil && it.Status == "published" && f.visible(it)
}

// publicItems runs q restricted to published public items.
func (f *funcs) publicItems(q content.Query) []content.Item {
	svc, ok := f.contentService()
	if !ok {
		return nil
	}
	q.Status, q.PublicOnly = "published", true
	page, err := svc.Query(f.st.ctx, q)
	if err != nil {
		return nil
	}
	out := page.Items[:0]
	for i := range page.Items {
		if f.published(&page.Items[i]) {
			out = append(out, page.Items[i])
		}
	}
	return out
}

// purposePages are the published public pages with purpose p, oldest first
// (then by id), in the page locale (locale "") or in any locale ("*").
func (f *funcs) purposePages(p, locale string) []content.Item {
	q := content.Query{Type: "page", Fields: map[string]content.Filter{"purpose": {Op: "eq", Value: p}},
		Order: []content.Order{{By: "published_at", Dir: "ASC"}}, PerPage: 50}
	if locale != "*" {
		q.PublicLocale = locale
	}
	return f.publicItems(q)
}

// slugPage is the published public page with slug p in locale.
func (f *funcs) slugPage(p, locale string) *content.Item {
	svc, ok := f.contentService()
	if !ok {
		return nil
	}
	for _, l := range []string{locale, ""} {
		if l == "" && locale != f.st.tc.Site.Locale {
			continue // locale "" belongs to the site locale only
		}
		if it, err := svc.GetBySlug(f.st.ctx, "page", p, l); err == nil && f.published(it) {
			return it
		}
	}
	return nil
}

// translationIn returns the published public member of it's translation
// group in locale, or nil.
func (f *funcs) translationIn(it *content.Item, locale string) *content.Item {
	svc, ok := f.contentService()
	if !ok || it == nil {
		return nil
	}
	members, err := svc.Translations(f.st.ctx, it.ID)
	if err != nil {
		return nil
	}
	for i := range members {
		if m := &members[i]; f.itemLocale(m) == locale && f.published(m) {
			return m
		}
	}
	return nil
}

// destination resolves the page that serves purpose p for the current locale:
// a published public page with that purpose in the current locale, else one
// with slug p; else the current-locale translation of such a page in another
// locale; else that other-locale page itself, marked Fallback. It never builds
// a URL by prefixing a guessed slug.
func (f *funcs) destination(p string) Destination {
	d := Destination{Purpose: p}
	if !slices.Contains(PagePurposes, p) {
		return d
	}
	cur := f.pageLocale()
	found := func(it *content.Item, fallback bool) Destination {
		d.Available, d.Fallback, d.Item = true, fallback, it
		d.URL, d.Title, d.Locale = f.url(it), it.Title, f.itemLocale(it)
		return d
	}
	if pages := f.purposePages(p, cur); len(pages) > 0 {
		return found(&pages[0], false)
	}
	if it := f.slugPage(p, cur); it != nil {
		return found(it, false)
	}
	others := f.purposePages(p, "*")
	for _, l := range i18n.Supported {
		if l == cur {
			continue
		}
		if it := f.slugPage(p, l); it != nil {
			others = append(others, *it)
		}
	}
	for i := range others {
		if t := f.translationIn(&others[i], cur); t != nil {
			return found(t, false)
		}
	}
	if len(others) > 0 {
		return found(&others[0], true)
	}
	return d
}

// related returns up to n published public items of v's type and locale that
// share at least one term with it (series membership excluded: {{series}}
// lists those), newest first (then newest id), excluding v.
// Empty when v has no terms or nothing shares one.
func (f *funcs) related(v any, n int) []content.Item {
	it := itemOf(v)
	if it == nil || n <= 0 {
		return nil
	}
	n = min(n, relatedMax)
	seen := map[string]bool{it.ID: true}
	var out []content.Item
	taxes := make([]string, 0, len(it.Terms))
	for tax := range it.Terms {
		if tax != SeriesTaxonomy { // a series has its own navigation ({{series}})
			taxes = append(taxes, tax)
		}
	}
	slices.Sort(taxes)
	for _, tax := range taxes {
		if len(it.Terms[tax]) == 0 {
			continue
		}
		for _, r := range f.publicItems(content.Query{Type: it.Type, PublicLocale: f.itemLocale(it),
			Taxonomy: map[string][]string{tax: it.Terms[tax]}, Order: []content.Order{{By: "published_at", Dir: "DESC"}}, PerPage: relatedMax + 1}) {
			if !seen[r.ID] && f.itemLocale(&r) == f.itemLocale(it) {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b content.Item) int {
		if c := cmp.Compare(b.PublishedAt, a.PublishedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID) // ids are time-ordered: newer first on a tie
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// adjacent returns v's previous (older) and next (newer) published public item
// of the same type and locale, by publication date then id. It looks at the
// newest adjacentScan items; an older item gets no neighbours.
func (f *funcs) adjacent(v any) Adjacent {
	it := itemOf(v)
	if it == nil || !f.published(it) {
		return Adjacent{}
	}
	list := f.publicItems(content.Query{Type: it.Type, PublicLocale: f.itemLocale(it),
		Order: []content.Order{{By: "published_at", Dir: "ASC"}}, PerPage: adjacentScan})
	list = slices.DeleteFunc(list, func(c content.Item) bool { return f.itemLocale(&c) != f.itemLocale(it) })
	i := slices.IndexFunc(list, func(c content.Item) bool { return c.ID == it.ID })
	if i < 0 {
		return Adjacent{}
	}
	var a Adjacent
	if i > 0 {
		a.Prev = &list[i-1]
	}
	if i+1 < len(list) {
		a.Next = &list[i+1]
	}
	return a
}

// termsOf returns the terms of taxonomy tax that have published public items
// of content type typ in the page's locale, with that count, in the
// taxonomy's own order. At most termsOfMax terms are considered.
func (f *funcs) termsOf(tax, typ string) []TermCount {
	svc, ok := kernel.Get[taxonomy.Service](f.s.d, kernel.ServiceTaxonomy)
	csvc, ok2 := f.contentService()
	if !ok || !ok2 || tax == "" || typ == "" {
		return nil
	}
	terms, _, err := svc.Terms(f.st.ctx, tax, taxonomy.TermQuery{PerPage: termsOfMax})
	if err != nil {
		return nil
	}
	var out []TermCount
	for _, t := range terms {
		page, err := csvc.Query(f.st.ctx, content.Query{Type: typ, Status: "published", PublicOnly: true, PublicLocale: f.pageLocale(),
			Taxonomy: map[string][]string{tax: {t.Slug}}, PerPage: 1})
		if err == nil && page.Total > 0 {
			out = append(out, TermCount{Term: t, Count: page.Total})
		}
	}
	return out
}
