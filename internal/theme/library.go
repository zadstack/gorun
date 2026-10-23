package theme

import (
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// A library is a filterable listing: a content type archive or a term archive.
// The site parses the filters it accepts from the query string (ParseLibrary),
// applies them to the listing query (Apply), and stores the Library in
// Context.Extra["library"]; {{libraryFilters .}} turns it into filter groups
// with facet counts. Only the type's own taxonomies and its indexed select
// fields are filters: an unknown key or value is ignored, never echoed.

const (
	libraryMaxGroups  = 6
	libraryMaxOptions = 50
)

var librarySlug = regexp.MustCompile(`^[\p{L}\p{N}_-]{1,120}$`)

// LibrarySpec is what a listing can be filtered by.
type LibrarySpec struct {
	// Type narrows every query and enables field filters; empty for a term
	// archive spanning several types (taxonomy filters only).
	Type       string
	Taxonomies []string
	Fields     []content.Field
	// Fixed are filters the page itself imposes (a term archive's term);
	// they are applied but not offered as options.
	Fixed map[string][]string
}

// Library is a listing's accepted filters and the request they came from.
type Library struct {
	Spec   LibrarySpec
	Values map[string]string // key → value, keys from Spec only
	path   string
	query  url.Values // the search query (q), kept in filter URLs
}

// LibrarySpecFor returns the filters content type t accepts: its taxonomies
// and its indexed select fields, in declaration order and within the caps.
func LibrarySpecFor(t content.Type) LibrarySpec {
	spec := LibrarySpec{Type: t.Slug}
	for _, tax := range t.Taxonomies {
		if len(spec.Taxonomies) < libraryMaxGroups && librarySlug.MatchString(tax) {
			spec.Taxonomies = append(spec.Taxonomies, tax)
		}
	}
	for _, f := range t.Fields {
		if f.Type == content.FieldSelect && f.Indexed && len(spec.Taxonomies)+len(spec.Fields) < libraryMaxGroups && !slices.Contains(spec.Taxonomies, f.Key) {
			spec.Fields = append(spec.Fields, f)
		}
	}
	return spec
}

// ParseLibrary reads spec's filters from u. A taxonomy value must look like a
// term slug (whether the term exists is left to the query: an unknown slug
// matches nothing); a field value must be one of the field's options.
func ParseLibrary(spec LibrarySpec, u *url.URL) *Library {
	l := &Library{Spec: spec, Values: map[string]string{}, path: u.Path, query: url.Values{}}
	q := u.Query()
	for _, tax := range spec.Taxonomies {
		if v := q.Get(tax); librarySlug.MatchString(v) {
			l.Values[tax] = v
		}
	}
	for _, f := range spec.Fields {
		v := q.Get(f.Key)
		if slices.ContainsFunc(f.Options, func(o content.Option) bool { return o.Value == v && v != "" }) {
			l.Values[f.Key] = v
		}
	}
	// Only the search query travels with filter links: any other parameter
	// is dropped, so nothing a visitor typed is echoed into the page.
	if v := q.Get("q"); v != "" {
		l.query.Set("q", v)
	}
	return l
}

// Apply adds the library's type, fixed filters and active filters to q.
func (l *Library) Apply(q *content.Query) {
	l.apply(q, l.Values)
}

func (l *Library) apply(q *content.Query, values map[string]string) {
	if l.Spec.Type != "" {
		q.Type = l.Spec.Type
	}
	for tax, slugs := range l.Spec.Fixed {
		if q.Taxonomy == nil {
			q.Taxonomy = map[string][]string{}
		}
		q.Taxonomy[tax] = slugs
	}
	for k, v := range values {
		if slices.Contains(l.Spec.Taxonomies, k) {
			if q.Taxonomy == nil {
				q.Taxonomy = map[string][]string{}
			}
			q.Taxonomy[k] = []string{v}
			continue
		}
		if q.Fields == nil {
			q.Fields = map[string]content.Filter{}
		}
		q.Fields[k] = content.Filter{Op: "eq", Value: v}
	}
}

// URL is the listing URL with values (a key set to "" is removed), the search
// query kept, and no page: a filter change starts at page 1.
func (l *Library) URL(values map[string]string) string {
	q := url.Values{}
	for k, vs := range l.query {
		q[k] = vs
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if values[k] != "" {
			q.Set(k, values[k])
		}
	}
	if len(q) == 0 {
		return l.path
	}
	return l.path + "?" + q.Encode()
}

// LibraryOption is one value of a filter group.
type LibraryOption struct {
	Value, Label, URL string
	Count             int
	Active            bool
}

// LibraryGroup is one filter: a taxonomy or an indexed select field.
type LibraryGroup struct {
	Key, Label string
	Options    []LibraryOption
}

// LibraryActive is an applied filter with the URL that removes it.
type LibraryActive struct {
	Key, Value, Label, RemoveURL string
}

// LibraryFilters is what {{libraryFilters .}} returns.
type LibraryFilters struct {
	Groups   []LibraryGroup
	Active   []LibraryActive
	ClearURL string
	Total    int
}

// libraryFilters builds the filter groups of the page's library: options
// with facet counts (published public items of the listing in the page
// locale, under the other active filters), zero-count options dropped, a
// group shown only with two or more options or an active value.
func (f *funcs) libraryFilters(tc *Context) LibraryFilters {
	var out LibraryFilters
	if tc == nil {
		return out
	}
	l, _ := tc.Extra["library"].(*Library)
	if l == nil {
		return out
	}
	out.Total = tc.Pagination.Total
	csvc, ok := f.contentService()
	if !ok {
		return out
	}
	count := func(values map[string]string) int {
		q := content.Query{Status: "published", PublicOnly: true, PublicLocale: f.pageLocale(), PerPage: 1}
		l.apply(&q, values)
		page, err := csvc.Query(f.st.ctx, q)
		if err != nil {
			return 0
		}
		return page.Total
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for kk, vv := range l.Values {
			m[kk] = vv
		}
		m[k] = v
		return m
	}
	locale := f.pageLocale()
	group := func(key, label string, opts []LibraryOption) {
		var kept []LibraryOption
		for _, o := range opts {
			if o.Active || o.Count > 0 {
				kept = append(kept, o)
			}
		}
		if len(kept) >= 2 || slices.ContainsFunc(kept, func(o LibraryOption) bool { return o.Active }) {
			out.Groups = append(out.Groups, LibraryGroup{Key: key, Label: label, Options: kept})
		}
	}
	tsvc, hasTax := kernel.Get[taxonomy.Service](f.s.d, kernel.ServiceTaxonomy)
	for _, tax := range l.Spec.Taxonomies {
		if !hasTax {
			break
		}
		if _, fixed := l.Spec.Fixed[tax]; fixed {
			continue
		}
		t, err := tsvc.Get(f.st.ctx, tax)
		if err != nil || t == nil || !t.Public {
			continue
		}
		terms, _, err := tsvc.Terms(f.st.ctx, tax, taxonomy.TermQuery{PerPage: libraryMaxOptions})
		if err != nil {
			continue
		}
		var opts []LibraryOption
		for _, term := range terms {
			active := l.Values[tax] == term.Slug
			if active {
				out.Active = append(out.Active, LibraryActive{Key: tax, Value: term.Slug, Label: term.Name, RemoveURL: l.URL(with(tax, ""))})
			}
			opts = append(opts, LibraryOption{Value: term.Slug, Label: term.Name, Active: active, URL: l.URL(with(tax, term.Slug)), Count: count(with(tax, term.Slug))})
		}
		group(tax, localeLabel(t.Name, locale, tax), opts)
	}
	for _, fd := range l.Spec.Fields {
		var opts []LibraryOption
		for _, o := range fd.Options {
			label := localeLabel(o.Label, locale, o.Value)
			active := l.Values[fd.Key] == o.Value
			if active {
				out.Active = append(out.Active, LibraryActive{Key: fd.Key, Value: o.Value, Label: label, RemoveURL: l.URL(with(fd.Key, ""))})
			}
			opts = append(opts, LibraryOption{Value: o.Value, Label: label, Active: active, URL: l.URL(with(fd.Key, o.Value)), Count: count(with(fd.Key, o.Value))})
		}
		group(fd.Key, localeLabel(fd.Label, locale, fd.Key), opts)
	}
	if len(out.Active) > 0 {
		out.ClearURL = l.URL(nil)
	}
	return out
}

func localeLabel(m map[string]string, locale, fallback string) string {
	for _, k := range []string{locale, "en", "ar"} {
		if v := m[k]; v != "" {
			return v
		}
	}
	return fallback
}

// PageURLs fills p's PrevURL and NextURL for the listing at u: only page
// changes, and only parameters the listing accepts travel (the library's
// filters when lib is not nil, and the search query), so a parameter a
// visitor invented is never echoed into a link.
func PageURLs(p Pagination, u *url.URL, lib *Library) Pagination {
	keep := url.Values{}
	if v := u.Query().Get("q"); v != "" {
		keep.Set("q", v)
	}
	if lib != nil {
		for k, v := range lib.Values {
			keep.Set(k, v)
		}
	}
	link := func(n int) string {
		q := url.Values{}
		for k, vs := range keep {
			q[k] = vs
		}
		if n > 1 {
			q.Set("page", strconv.Itoa(n))
		}
		if len(q) == 0 {
			return u.Path
		}
		return u.Path + "?" + q.Encode()
	}
	if p.HasPrev {
		p.PrevURL = link(p.Page - 1)
	}
	if p.HasNext {
		p.NextURL = link(p.Page + 1)
	}
	return p
}
