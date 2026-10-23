package theme

import (
	"cmp"
	"slices"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// SeriesTaxonomy and SeriesOrderField hold a post's series membership and its
// place in it (taxonomy migration 0002, content migration 0008).
const (
	SeriesTaxonomy   = "series"
	SeriesOrderField = "series_order"
	// SeriesMaxParts caps a series: the helper and the series archive list
	// at most this many parts, on one page.
	SeriesMaxParts = 200
)

// Series is an item's guide series: its published public parts in the item's
// type and locale, in series order. Index is the item's 1-based position.
// Prev and Next are the neighbouring parts in the series, not by date (that
// is {{adjacent}}). Draft, private, deleted and other-locale parts are not
// listed or counted.
type Series struct {
	Term  taxonomy.Term
	Parts []content.Item
	Index int
	Count int
	Prev  *content.Item
	Next  *content.Item
}

// seriesOrder reads an item's series_order: (value, true) for a number.
func seriesOrder(it content.Item) (float64, bool) {
	switch v := it.Fields[SeriesOrderField].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

// series returns v's series, or nil when v is in no series, is not published,
// or is the only published part. With several series terms the first (by
// slug) is used.
func (f *funcs) series(v any) *Series {
	it := itemOf(v)
	if it == nil || len(it.Terms[SeriesTaxonomy]) == 0 || !f.published(it) {
		return nil
	}
	slugs := slices.Clone(it.Terms[SeriesTaxonomy])
	slices.Sort(slugs)
	tsvc, ok := kernel.Get[taxonomy.Service](f.s.d, kernel.ServiceTaxonomy)
	if !ok {
		return nil
	}
	term, err := tsvc.GetTermBySlug(f.st.ctx, SeriesTaxonomy, slugs[0])
	if err != nil || term == nil {
		return nil
	}
	parts := f.publicItems(content.Query{Type: it.Type, PublicLocale: f.itemLocale(it),
		Taxonomy: map[string][]string{SeriesTaxonomy: {term.Slug}}, Order: []content.Order{{By: "published_at", Dir: "ASC"}}, PerPage: SeriesMaxParts})
	parts = slices.DeleteFunc(parts, func(c content.Item) bool { return f.itemLocale(&c) != f.itemLocale(it) })
	SortSeries(parts)
	i := slices.IndexFunc(parts, func(c content.Item) bool { return c.ID == it.ID })
	if i < 0 || len(parts) < 2 {
		return nil
	}
	s := &Series{Term: *term, Parts: parts, Index: i + 1, Count: len(parts)}
	if i > 0 {
		s.Prev = &parts[i-1]
	}
	if i+1 < len(parts) {
		s.Next = &parts[i+1]
	}
	return s
}

// SortSeries puts series parts in series order: numbered parts first, by
// number; then the unnumbered, oldest first; id breaks every tie. The series
// helper and the series term archive use it.
func SortSeries(parts []content.Item) {
	slices.SortStableFunc(parts, func(a, b content.Item) int {
		oa, hasA := seriesOrder(a)
		ob, hasB := seriesOrder(b)
		switch {
		case hasA && !hasB:
			return -1
		case !hasA && hasB:
			return 1
		case hasA && hasB && oa != ob:
			return cmp.Compare(oa, ob)
		}
		if c := cmp.Compare(a.PublishedAt, b.PublishedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}
