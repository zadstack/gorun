package seo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/i18n"
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

// Entries lists the home page, published items of public content types
// (minus those whose SEO robots says noindex), public type archives and the
// terms of public taxonomies, each with its absolute Loc after the
// FilterSitemapEntry hook. The home page has no Item, Term or Type.
func (s *service) Entries(ctx context.Context) ([]SitemapEntry, error) {
	locales := i18n.ContentLocales(ctx, nil)
	if s.settings != nil {
		locales = i18n.ContentLocales(ctx, s.settings)
	}
	ctx = i18n.WithContentLocales(ctx, locales)
	base := s.base(ctx)
	out := []SitemapEntry{{Loc: base + "/"}}
	seen := map[string]bool{base + "/": true}
	if !s.publicHome(ctx) {
		out = nil
		seen = map[string]bool{}
	}
	add := func(e *SitemapEntry) { out = append(out, *e) }
	if svc, ok := s.content(); ok {
		if err := s.addContent(ctx, svc, add); err != nil {
			return nil, err
		}
	}
	if svc, ok := s.taxonomy(); ok {
		if err := s.addTerms(ctx, svc, add); err != nil {
			return nil, err
		}
	}
	if s.hooks != nil {
		batch := &SitemapBatch{Context: ctx, Entries: out}
		if _, err := s.hooks.Apply(ctx, FilterSitemapBatch, batch); err != nil {
			return nil, err
		}
		ctx = batch.Context
	}
	resolved := make([]SitemapEntry, 0, len(out))
	for i := range out {
		e := &out[i]
		// The home page historically bypasses the per-entry filter.
		if i == 0 && e.Item == nil && e.Term == nil && e.Type == nil {
			resolved = append(resolved, *e)
			continue
		}
		if s.hooks != nil {
			v, err := s.hooks.Apply(ctx, FilterSitemapEntry, e)
			if err != nil {
				return nil, fmt.Errorf("seo: sitemap entry filter: %w", err)
			} else if p, ok := v.(*SitemapEntry); ok && p != nil {
				e = p
			}
		}
		loc := absolute(base, e.Loc)
		if loc != "" && !seen[loc] {
			seen[loc] = true
			entry := *e
			entry.Loc = loc
			resolved = append(resolved, entry)
		}
	}
	return s.finalSitemapEntries(ctx, base, resolved)
}

// Sitemap returns the root document: a urlset for small sites, otherwise an index.
func (s *service) Sitemap(ctx context.Context) ([]byte, error) {
	doc, err := s.SitemapDocument(ctx, "")
	return doc.XML, err
}

func noindex(it *content.Item) bool {
	r, _ := it.SEO["robots"].(string)
	return strings.Contains(strings.ToLower(r), "noindex")
}

func (s *service) addContent(ctx context.Context, svc content.Service, add func(*SitemapEntry)) error {
	types, err := svc.Types(ctx)
	if err != nil {
		return fmt.Errorf("seo: sitemap types: %w", err)
	}
	for _, t := range types {
		if !t.Public {
			continue
		}
		if t.HasArchive {
			add(&SitemapEntry{Loc: menus.ArchivePath(&t), Type: &t})
		}
		if scanner, ok := svc.(content.PublishedScanner); ok {
			if err := scanSitemapContent(ctx, scanner, t.Slug, add); err != nil {
				return err
			}
			continue
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
				add(&SitemapEntry{Loc: menus.ContentPath(it), LastMod: first(it.UpdatedAt, it.PublishedAt), Item: it})
			}
			if len(res.Items) < sitemapPageSize || page*sitemapPageSize >= res.Total {
				break
			}
		}
	}
	return nil
}

func (s *service) addTerms(ctx context.Context, svc taxonomy.Service, add func(*SitemapEntry)) error {
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
				add(&SitemapEntry{Loc: menus.TermPath(&terms[i]), Term: &terms[i]})
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
	locale := i18n.ContentLocale(ctx, s.settings)
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

// Keyset reads avoid repeated database sorts. Preserve the existing Entries
// order for consumers such as the overview's bounded preview groups.
func scanSitemapContent(ctx context.Context, svc content.PublishedScanner, typeSlug string, add func(*SitemapEntry)) error {
	afterID := ""
	var published []*content.Item
	for {
		items, err := svc.PublishedAfter(ctx, typeSlug, afterID)
		if err != nil {
			return fmt.Errorf("seo: sitemap scan %s: %w", typeSlug, err)
		}
		for i := range items {
			it := &items[i]
			if !noindex(it) {
				published = append(published, it)
			}
		}
		if len(items) < sitemapPageSize {
			break
		}
		afterID = items[len(items)-1].ID
	}
	sort.Slice(published, func(i, j int) bool {
		if published[i].PublishedAt == published[j].PublishedAt {
			return published[i].ID < published[j].ID
		}
		return published[i].PublishedAt > published[j].PublishedAt
	})
	for _, it := range published {
		add(&SitemapEntry{Loc: menus.ContentPath(it), LastMod: first(it.UpdatedAt, it.PublishedAt), Item: it})
	}
	return nil
}
