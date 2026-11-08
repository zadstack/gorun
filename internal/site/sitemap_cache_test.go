package site_test

import (
	"context"
	"encoding/xml"
	"github.com/zadstack/gorun/internal/db"
	"github.com/zadstack/gorun/internal/taxonomy"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/auth"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/seo"
)

func TestSitemapCachedValidatorsInvalidateBeforeServingProtectedPage(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	page := publish(t, cs, content.Input{Type: "page", Slug: "cached-page", Title: "Cached page"})
	response, body := e.get("/sitemap.xml")
	etag := response.Header.Get("ETag")
	if etag == "" || !strings.Contains(body, "/cached-page") {
		t.Fatalf("missing document/validator: %s", body)
	}
	conditional := func() (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", e.srv.URL+"/sitemap.xml", nil)
		req.Header.Set("If-None-Match", etag)
		resp, err := e.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(b), resp.Header.Get("ETag")
	}
	if status, body, _ := conditional(); status != 304 || body != "" {
		t.Fatalf("cached response: %d %s", status, body)
	}
	ctx := auth.WithUser(context.Background(), &auth.User{ID: "owner", Role: "admin", Status: "active"})
	if _, err := cs.(content.PageAccessService).SavePageAccess(ctx, page.ID, content.PageAccessInput{Mode: "blocked"}, 0); err != nil {
		t.Fatal(err)
	}
	status, body, changed := conditional()
	if status != 200 || strings.Contains(body, "/cached-page") || changed == etag {
		t.Fatalf("stale protected URL: %d %s", status, body)
	}
}

func TestSitemapSnapshotSeparatesHostExclusions(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	page := publish(t, cs, content.Input{Type: "page", Slug: "host-restricted", Title: "Restricted"})
	svc, _ := kernel.Get[seo.Service](e.app.Deps, kernel.ServiceSEO)
	ctx := content.WithPublicExclusions(context.Background(), []string{page.ID})
	for _, scenario := range []struct {
		ctx     context.Context
		visible bool
	}{{context.Background(), true}, {ctx, false}, {context.Background(), true}} {
		body, err := svc.Sitemap(scenario.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "/host-restricted") != scenario.visible {
			t.Fatalf("wrong host projection: %s", body)
		}
	}
}

func TestSitemapPartRouteDoesNotFallThroughToContent(t *testing.T) {
	e := bootReal(t)
	for _, path := range []string{"/sitemaps/1.xml", "/sitemaps/999.xml"} {
		response, _ := e.get(path)
		if response.StatusCode != 404 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	response, _ := e.do("HEAD", "/sitemap.xml", nil)
	if response.StatusCode != 200 || response.Header.Get("ETag") == "" {
		t.Fatalf("HEAD: %d %v", response.StatusCode, response.Header)
	}
}

func TestSitemapInvalidatesOnWritesFromAnotherConnection(t *testing.T) {
	e := bootReal(t)
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	page := publish(t, cs, content.Input{Type: "page", Slug: "external-write", Title: "External write"})
	response, body := e.get("/sitemap.xml")
	if response.StatusCode != 200 || !strings.Contains(body, "/external-write") {
		t.Fatalf("initial sitemap: %d %s", response.StatusCode, body)
	}
	database, err := db.Open(e.app.Deps.Config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE content SET status='draft' WHERE id=?`, page.ID); err != nil {
		t.Fatal(err)
	}
	// Rolled-back changes must not invalidate a committed generation.
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := e.get("/sitemap.xml")
	if unchanged.Header.Get("ETag") != response.Header.Get("ETag") {
		t.Fatal("rollback changed representation")
	}
	if _, err := database.Exec(`UPDATE content SET status='draft' WHERE id=?`, page.ID); err != nil {
		t.Fatal(err)
	}
	changed, body := e.get("/sitemap.xml")
	if changed.StatusCode != 200 || strings.Contains(body, "/external-write") {
		t.Fatalf("stale external write: %d %s", changed.StatusCode, body)
	}
}

func TestBatchedContentQueryKeepsTermsAndLocalizedPermalinks(t *testing.T) {
	e := bootReal(t)
	ctx := context.Background()
	tax, _ := kernel.Get[taxonomy.Service](e.app.Deps, kernel.ServiceTaxonomy)
	if _, err := tax.SaveTerm(ctx, taxonomy.Term{Taxonomy: "category", Slug: "batch-term", Name: "Batch term"}); err != nil {
		t.Fatal(err)
	}
	category, err := tax.Get(ctx, "category")
	if err != nil {
		t.Fatal(err)
	}
	category.Types = append(category.Types, "page")
	if err := tax.Save(ctx, *category); err != nil {
		t.Fatal(err)
	}
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	parent := publish(t, cs, content.Input{Type: "page", Slug: "parent", Title: "Parent", Locale: "en"})
	publish(t, cs, content.Input{Type: "page", Slug: "child", Title: "Child", Locale: "en", ParentID: parent.ID, Terms: map[string][]string{"category": {"batch-term"}}})
	page, err := cs.Query(ctx, content.Query{Type: "page", PublicOnly: true, PerPage: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range page.Items {
		if it.Slug == "child" {
			found = slices.Contains(it.Terms["category"], "batch-term")
		}
	}
	if !found {
		t.Fatalf("lost batched term assignment: %+v", page.Items)
	}
	response, body := e.get("/sitemap.xml")
	if response.StatusCode != 200 || !strings.Contains(body, "/en/parent/child") {
		t.Fatalf("batched permalink: %d %s", response.StatusCode, body)
	}
}

func TestSitemapReadsExternalRedirectChangesWithoutRequestCacheTTL(t *testing.T) {
	e := bootReal(t, redirects.New())
	ctx := context.Background()
	cs, _ := kernel.Get[content.Service](e.app.Deps, kernel.ServiceContent)
	publish(t, cs, content.Input{Type: "page", Slug: "redirect-source", Title: "Redirect source"})
	rs, _ := kernel.Get[redirects.Service](e.app.Deps, kernel.ServiceRedirects)
	rs.Lookup(ctx, "/redirect-source") // Warm the independent HTTP redirect cache.
	_, body := e.get("/sitemap.xml")
	if !strings.Contains(body, "/redirect-source") {
		t.Fatal("initial source missing")
	}
	database, err := db.Open(e.app.Deps.Config.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO redirects(id,from_path,to_url,status,enabled,created_at) VALUES('external','/redirect-source','https://external.example/',301,1,?)`, db.Now()); err != nil {
		t.Fatal(err)
	}
	response, body := e.get("/sitemap.xml")
	if response.StatusCode != 200 || strings.Contains(body, "/redirect-source") {
		t.Fatalf("stale redirect cache: %d %s", response.StatusCode, body)
	}
}

func TestSitemapIndexAndPartsServeEveryURLOnLargeSite(t *testing.T) {
	e := bootReal(t)
	// Seed the real SQLite schema in one transaction; this tests scale and HTTP
	// delivery rather than spending 50,001 editor saves on fixture setup.
	_, err := e.app.Deps.DB.Exec(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<50001)
 INSERT INTO content(id,type,slug,status,created_at,updated_at)
 SELECT 'bulk-'||i,'post','bulk-'||i,'published',?,? FROM n`, db.Now(), db.Now())
	if err != nil {
		t.Fatal(err)
	}
	response, body := e.get("/sitemap.xml")
	if response.StatusCode != 200 {
		t.Fatalf("index: %d %s", response.StatusCode, body)
	}
	var index struct {
		XMLName xml.Name `xml:"sitemapindex"`
		Parts   []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal([]byte(body), &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Parts) != 2 {
		t.Fatalf("parts: %d", len(index.Parts))
	}
	found := map[string]bool{}
	for _, part := range index.Parts {
		path := strings.TrimPrefix(part.Loc, "http://example.test")
		response, body := e.do("GET", path, nil, "Accept-Encoding", "identity")
		if response.StatusCode != 200 {
			t.Fatalf("part %s: %d %s", path, response.StatusCode, body)
		}
		var urls struct {
			XMLName xml.Name `xml:"urlset"`
			URLs    []struct {
				Loc string `xml:"loc"`
			} `xml:"url"`
		}
		if err := xml.Unmarshal([]byte(body), &urls); err != nil {
			t.Fatal(err)
		}
		if len(urls.URLs) > 50000 {
			t.Fatalf("oversized part: %d", len(urls.URLs))
		}
		for _, url := range urls.URLs {
			if strings.Contains(url.Loc, "/post/bulk-") {
				if found[url.Loc] {
					t.Fatalf("duplicate URL: %s", url.Loc)
				}
				found[url.Loc] = true
			}
		}
		head, headBody := e.do("HEAD", path, nil)
		if head.StatusCode != 200 || headBody != "" || head.Header.Get("ETag") != response.Header.Get("ETag") {
			t.Fatalf("part HEAD differs: %d", head.StatusCode)
		}
	}
	if len(found) != 50001 {
		t.Fatalf("lost entries: %d of 50001", len(found))
	}
}
