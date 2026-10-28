package seo

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/menus"
	"github.com/zadstack/gorun/internal/taxonomy"
)

// FilterSitemapEntry runs for every sitemap entry with a *SitemapEntry whose
// Loc is the fallback path (menus.ContentPath / TermPath / ArchivePath).
// Filters may rewrite Loc (a site path or absolute URL) or clear it to drop
// the entry, and must return the *SitemapEntry. The theme module subscribes
// and sets Loc to the theme permalink.
const FilterSitemapEntry = "seo.sitemap_entry"

const (
	sitemapPageSize   = 100
	sitemapMaxEntries = 50000
	sitemapNS         = "http://www.sitemaps.org/schemas/sitemap/0.9"
)

// SitemapEntry is one <url>; Item, Term or Type says what it points to.
type SitemapEntry struct {
	Loc     string
	LastMod string
	Item    *content.Item
	Term    *taxonomy.Term
	Type    *content.Type
}

type xmlURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type xmlURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	NS      string   `xml:"xmlns,attr"`
	URLs    []xmlURL `xml:"url"`
}

// Entries lists the home page, published items of public content types
// (minus those whose SEO robots says noindex), public type archives and the
// terms of public taxonomies, each with its absolute Loc after the
// FilterSitemapEntry hook. The home page has no Item, Term or Type.
func (s *service) Entries(ctx context.Context) ([]SitemapEntry, error) {
	base := s.base(ctx)
	out := []SitemapEntry{{Loc: base + "/"}}
	seen := map[string]bool{base + "/": true}
	if !s.publicHome(ctx) {
		out = nil
		seen = map[string]bool{}
	}
	// add appends e and reports whether there is room for more entries.
	add := func(e *SitemapEntry) bool {
		if len(out) >= sitemapMaxEntries {
			return false
		}
		if s.hooks != nil {
			v, err := s.hooks.Apply(ctx, FilterSitemapEntry, e)
			if err != nil {
				s.log.Error("seo: sitemap filter", "err", err)
			} else if p, ok := v.(*SitemapEntry); ok && p != nil {
				e = p
			}
		}
		loc := absolute(base, e.Loc)
		if loc != "" && !seen[loc] {
			seen[loc] = true
			entry := *e
			entry.Loc = loc
			out = append(out, entry)
		}
		return len(out) < sitemapMaxEntries
	}
	if svc, ok := s.content(); ok {
		if err := s.addContent(ctx, svc, add); err != nil {
			return nil, err
		}
	}
	if svc, ok := s.taxonomy(); ok && len(out) < sitemapMaxEntries {
		if err := s.addTerms(ctx, svc, add); err != nil {
			return nil, err
		}
	}
	return s.finalSitemapEntries(ctx, base, out)
}

// Sitemap renders Entries as sitemap XML.
func (s *service) Sitemap(ctx context.Context) ([]byte, error) {
	entries, err := s.Entries(ctx)
	if err != nil {
		return nil, err
	}
	set := xmlURLSet{NS: sitemapNS, URLs: make([]xmlURL, len(entries))}
	for i, e := range entries {
		set.URLs[i] = xmlURL{Loc: e.Loc, LastMod: e.LastMod}
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(set); err != nil {
		return nil, fmt.Errorf("seo: encode sitemap: %w", err)
	}
	buf.WriteString("\n")
	return buf.Bytes(), nil
}

func noindex(it *content.Item) bool {
	r, _ := it.SEO["robots"].(string)
	return strings.Contains(strings.ToLower(r), "noindex")
}

func (s *service) addContent(ctx context.Context, svc content.Service, add func(*SitemapEntry) bool) error {
	types, err := svc.Types(ctx)
	if err != nil {
		return fmt.Errorf("seo: sitemap types: %w", err)
	}
	for _, t := range types {
		if !t.Public {
			continue
		}
		if t.HasArchive && !add(&SitemapEntry{Loc: menus.ArchivePath(&t), Type: &t}) {
			return nil
		}
		for page := 1; ; page++ {
			res, err := svc.Query(ctx, content.Query{PublicOnly: true, Type: t.Slug, Status: "published", Page: page, PerPage: sitemapPageSize,
				Order: []content.Order{{By: "published_at", Dir: "desc"}}})
			if err != nil {
				return fmt.Errorf("seo: sitemap query %s: %w", t.Slug, err)
			}
			for i := range res.Items {
				it := &res.Items[i]
				if it.Status != "published" || noindex(it) {
					continue
				}
				if !add(&SitemapEntry{Loc: menus.ContentPath(it), LastMod: first(it.UpdatedAt, it.PublishedAt), Item: it}) {
					return nil // cap reached: stop paging
				}
			}
			if len(res.Items) < sitemapPageSize || page*sitemapPageSize >= res.Total {
				break
			}
		}
	}
	return nil
}

func (s *service) addTerms(ctx context.Context, svc taxonomy.Service, add func(*SitemapEntry) bool) error {
	taxes, err := svc.Taxonomies(ctx)
	if err != nil {
		return fmt.Errorf("seo: sitemap taxonomies: %w", err)
	}
	for _, tx := range taxes {
		if !tx.Public {
			continue
		}
		for page := 1; ; page++ {
			terms, total, err := svc.Terms(ctx, tx.Slug, taxonomy.TermQuery{Page: page, PerPage: sitemapPageSize})
			if err != nil {
				return fmt.Errorf("seo: sitemap terms %s: %w", tx.Slug, err)
			}
			for i := range terms {
				if !add(&SitemapEntry{Loc: menus.TermPath(&terms[i]), Term: &terms[i]}) {
					return nil
				}
			}
			if len(terms) < sitemapPageSize || page*sitemapPageSize >= total {
				break
			}
		}
	}
	return nil
}

func (s *service) publicHome(ctx context.Context) bool {
	svc, ok := s.content()
	id := s.setting(ctx, "site.front_page")
	if !ok || id == "" {
		return true
	}
	it, err := svc.Get(ctx, id)
	if err != nil {
		return true
	}
	locale := s.setting(ctx, "site.locale")
	if locale == "" {
		locale = "ar"
	}
	if it.Locale != "" && it.Locale != locale {
		members, err := svc.Translations(ctx, id)
		if err != nil {
			return false
		}
		it = nil
		for i := range members {
			if members[i].Locale == locale {
				it = &members[i]
				break
			}
		}
	}
	return it == nil || it.Status != "published" || content.PubliclyReadable(ctx, svc, it.ID)
}
