package site_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/taxonomy"
)

func sitemapLocations(t *testing.T, e *env) []string {
	t.Helper()
	response, body := e.get("/sitemap.xml")
	if response.StatusCode != 200 {
		t.Fatalf("sitemap: %d %s", response.StatusCode, body)
	}
	var sitemap struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal([]byte(body), &sitemap); err != nil {
		t.Fatal(err)
	}
	locations := make([]string, 0, len(sitemap.URLs))
	for _, entry := range sitemap.URLs {
		decoded, err := url.PathUnescape(entry.Loc)
		if err != nil {
			t.Fatal(err)
		}
		locations = append(locations, decoded)
	}
	return locations
}

func saveSitemapRedirect(t *testing.T, service redirects.Service, from, target string) {
	t.Helper()
	if _, err := service.Save(context.Background(), redirects.Redirect{FromPath: from, ToURL: target, Status: 301, Enabled: true}); err != nil {
		t.Fatal(err)
	}
}

func TestSitemapUsesFinalLocalizedTermURLs(t *testing.T) {
	for _, scenario := range []struct{ siteLocale, locale, slug string }{{"en", "ar", "دليل-الفريق"}, {"ar", "en", "team-guide"}} {
		t.Run(scenario.locale, func(t *testing.T) {
			e := bootReal(t, redirects.New())
			ctx := context.Background()
			if err := e.app.Deps.Settings.Set(ctx, "site.locale", scenario.siteLocale); err != nil {
				t.Fatal(err)
			}
			terms, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
			if _, err := terms.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: scenario.slug, Name: "Team guide"}); err != nil {
				t.Fatal(err)
			}
			cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
			publish(t, cs, content.Input{Type: "post", Slug: "guide", Title: "Team guide", Locale: scenario.locale, Terms: map[string][]string{"category": {scenario.slug}}})
			redirectsService, _ := kernel.Get[redirects.Service](e.app.Deps, kernel.ServiceRedirects)
			source := "/category/" + scenario.slug
			target := "/" + scenario.locale + source
			saveSitemapRedirect(t, redirectsService, source, "/legacy-guide")
			saveSitemapRedirect(t, redirectsService, "/legacy-guide", "http://example.test"+target+"#sources")
			locations := sitemapLocations(t, e)
			joined := strings.Join(locations, "\n")
			if !strings.Contains(joined, "http://example.test"+target) {
				t.Fatalf("Missing final locale URL: %s", joined)
			}
			for _, location := range locations {
				if location == "http://example.test"+source || strings.Contains(location, "legacy-guide") || strings.Contains(location, "#") {
					t.Fatalf("Nonfinal URL: %s", location)
				}
			}
			if response, body := e.get(target); response.StatusCode != 200 || !strings.Contains(body, `lang="`+scenario.locale+`"`) {
				t.Fatalf("Final route: %d %s", response.StatusCode, body)
			}
		})
	}
}

func TestSitemapOmitsUnsafeRedirectDestinationsAndDeduplicates(t *testing.T) {
	e := bootReal(t, redirects.New())
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	redirectsService, _ := kernel.Get[redirects.Service](e.app.Deps, kernel.ServiceRedirects)
	canonical := publish(t, cs, content.Input{Type: "page", Slug: "canonical", Title: "Public destination"})
	publish(t, cs, content.Input{Type: "page", Slug: "noindex", Title: "Do not index", SEO: map[string]any{"robots": "noindex"}})
	private := publish(t, cs, content.Input{Type: "page", Slug: "protected", Title: "Protected destination"})
	admin := auth.WithUser(ctx, &auth.User{ID: "owner", Role: "admin", Status: "active"})
	if _, err := cs.(content.PageAccessService).SavePageAccess(admin, private.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "page", Slug: "draft-target", Title: "Draft target", Status: "draft"}); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ slug, target string }{
		{"alias-public", "/canonical"}, {"alias-query", "/query-bridge?lang=ar"}, {"alias-override", "/query-override?source=old"},
		{"alias-protected", "/protected"}, {"alias-noindex", "/noindex"}, {"alias-draft", "/draft-target"},
		{"alias-admin", "/admin/login"}, {"alias-external", "https://elsewhere.test/canonical"},
		{"alias-missing", "/missing-page"}, {"alias-loop", "/cycle-two"}, {"alias-long", "/hop-0"},
	} {
		publish(t, cs, content.Input{Type: "page", Slug: scenario.slug, Title: scenario.slug})
		saveSitemapRedirect(t, redirectsService, "/"+scenario.slug, scenario.target)
	}
	saveSitemapRedirect(t, redirectsService, "/query-bridge", "/canonical#top")
	saveSitemapRedirect(t, redirectsService, "/query-override", "/canonical?source=new#top")
	saveSitemapRedirect(t, redirectsService, "/cycle-two", "/alias-loop")
	for step := range 18 {
		target := fmt.Sprintf("/hop-%d", step+1)
		if step == 17 {
			target = "/canonical"
		}
		saveSitemapRedirect(t, redirectsService, fmt.Sprintf("/hop-%d", step), target)
	}
	locations := sitemapLocations(t, e)
	sitemapService, _ := kernel.Get[seo.Service](e.app.Deps, kernel.ServiceSEO)
	entries, err := sitemapService.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := redirectsService.List(ctx, redirects.ListQuery{PerPage: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Hits != 0 {
			t.Fatalf("Sitemap generation counted a redirect hit: %+v", row)
		}
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Loc, "http://example.test/canonical") && (entry.Item == nil || entry.Item.ID != canonical.ID) {
			t.Fatalf("Redirect retained alias metadata: %+v", entry)
		}
	}
	joined := strings.Join(locations, "\n")
	canonicalCount := 0
	for _, location := range locations {
		if location == "http://example.test/canonical" {
			canonicalCount++
		}
	}
	if canonicalCount != 1 || !strings.Contains(joined, "http://example.test/canonical?lang=ar") || !strings.Contains(joined, "http://example.test/canonical?source=new") || strings.Contains(joined, "source=old") {
		t.Fatalf("Final URLs not deduped/query preserved: %s", joined)
	}
	for _, bad := range []string{"alias-", "protected", "noindex", "draft-target", "admin/login", "elsewhere.test", "missing-page", "cycle-two", "/hop-", "#"} {
		if strings.Contains(joined, bad) {
			t.Errorf("Sitemap contains %q: %s", bad, joined)
		}
	}
}

func TestSitemapOmitsRedirectToEmptyLocalizedListing(t *testing.T) {
	e := bootReal(t, redirects.New())
	ctx := context.Background()
	if err := e.app.Deps.Settings.Set(ctx, "site.locale", "en"); err != nil {
		t.Fatal(err)
	}
	terms, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	if _, err := terms.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: "empty", Name: "Empty"}); err != nil {
		t.Fatal(err)
	}
	service, _ := kernel.Get[redirects.Service](e.app.Deps, kernel.ServiceRedirects)
	saveSitemapRedirect(t, service, "/category/empty", "/ar/category/empty")
	if joined := strings.Join(sitemapLocations(t, e), "\n"); strings.Contains(joined, "category/empty") {
		t.Fatalf("Empty localized listing indexed: %s", joined)
	}
}
