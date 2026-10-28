package seo

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/zadstack/gorun/internal/i18n"
	"github.com/zadstack/gorun/internal/redirects"
)

const sitemapRedirectLimit = 16

// finalSitemapEntries follows native redirects only to known public entries or
// populated localized listings. It never requests a URL or counts redirect hits.
func (s *service) finalSitemapEntries(ctx context.Context, base string, entries []SitemapEntry) ([]SitemapEntry, error) {
	if s.redirects == nil {
		return entries, nil
	}
	redirectsService, ok := s.redirects()
	if !ok {
		return entries, nil
	}
	origin, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("seo: sitemap origin: %w", err)
	}
	declared := make(map[string]SitemapEntry, len(entries))
	for _, entry := range entries {
		address, err := url.Parse(entry.Loc)
		if err == nil && sameSitemapOrigin(origin, address) {
			declared[sitemapPath(address.Path)] = entry
		}
	}
	listings := sitemapListingLocales(entries)
	final := make([]SitemapEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		address := finalSitemapURL(ctx, origin, entry.Loc, redirectsService)
		if address == nil {
			continue
		}
		if !publicSitemapDestination(address, declared, listings) {
			continue
		}
		address.Fragment, address.RawFragment = "", ""
		canonical := address.String()
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		if destination, exists := declared[sitemapPath(address.Path)]; exists {
			entry = destination
		}
		entry.Loc = canonical
		final = append(final, entry)
	}
	return final, nil
}

func sameSitemapOrigin(origin, address *url.URL) bool {
	return address.User == nil && strings.EqualFold(origin.Scheme, address.Scheme) && strings.EqualFold(origin.Host, address.Host)
}

func sitemapPath(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func finalSitemapURL(ctx context.Context, origin *url.URL, loc string, redirectsService redirects.Service) *url.URL {
	address, err := url.Parse(loc)
	if err != nil || !sameSitemapOrigin(origin, address) {
		return nil
	}
	seen := map[string]bool{}
	for range sitemapRedirectLimit {
		path := sitemapPath(address.Path)
		if seen[path] || redirects.Reserved(path) {
			return nil
		}
		seen[path] = true
		redirect, exists := redirectsService.Lookup(ctx, address.Path)
		if !exists {
			return address
		}
		target, err := url.Parse(redirect.ToURL)
		if err != nil {
			return nil
		}
		if address.RawQuery != "" && !strings.Contains(redirect.ToURL, "?") {
			target.RawQuery = address.RawQuery
		}
		address = address.ResolveReference(target)
		if !sameSitemapOrigin(origin, address) {
			return nil
		}
	}
	return nil
}

type sitemapListing struct{ taxonomy, slug, locale string }

// Published indexable items already loaded for the sitemap also identify the
// locales with public listing content; no per-term database queries are needed.
func sitemapListingLocales(entries []SitemapEntry) map[sitemapListing]bool {
	listings := map[sitemapListing]bool{}
	for _, entry := range entries {
		if entry.Item == nil {
			continue
		}
		locales := []string{entry.Item.Locale}
		if entry.Item.Locale == "" {
			locales = i18n.Supported
		}
		for _, locale := range locales {
			listings[sitemapListing{slug: entry.Item.Type, locale: locale}] = true
			for taxonomy, slugs := range entry.Item.Terms {
				for _, slug := range slugs {
					listings[sitemapListing{taxonomy: taxonomy, slug: slug, locale: locale}] = true
				}
			}
		}
	}
	return listings
}

func publicSitemapDestination(address *url.URL, declared map[string]SitemapEntry, listings map[sitemapListing]bool) bool {
	if _, known := declared[sitemapPath(address.Path)]; known {
		return true
	}
	locale, path := i18n.SplitLocalePath(address.Path)
	if locale == "" {
		return false
	}
	entry, known := declared[sitemapPath(path)]
	if !known {
		return false
	}
	if entry.Term != nil {
		return listings[sitemapListing{taxonomy: entry.Term.Taxonomy, slug: entry.Term.Slug, locale: locale}]
	}
	if entry.Type != nil {
		return listings[sitemapListing{slug: entry.Type.Slug, locale: locale}]
	}
	return false
}
