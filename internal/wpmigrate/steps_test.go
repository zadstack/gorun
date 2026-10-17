package wpmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
	"github.com/zadstack/gorun/internal/redirects"
	"github.com/zadstack/gorun/internal/seo"
	"github.com/zadstack/gorun/internal/theme"
)

// TestMediaDownloadRewritesSizeURLs: the attachment is downloaded once
// and every size variant URL (src and srcset) points at the local file.
func TestMediaDownloadRewritesSizeURLs(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	host := e.wp.URL()
	// A REST post whose body uses sizes listed in media_details and one that
	// is not listed (-1024x576) plus a -scaled original.
	e.wp.limitMedia(0)
	e.wp.override("/wp-json/wp/v2/media", func([]byte) []byte {
		b, _ := json.Marshal([]map[string]any{{
			"id": 862, "slug": "cover", "source_url": host + "/wp-content/uploads/2026/08/cover-scaled.png", "mime_type": "image/png",
			"alt_text": "غلاف", "caption": map[string]any{"rendered": "<p>تعليق</p>"}, "title": map[string]any{"rendered": "Cover"},
			"modified_gmt": "2026-08-31T06:00:00",
			"media_details": map[string]any{"filesize": 70, "sizes": map[string]any{
				"thumbnail": map[string]any{"source_url": host + "/wp-content/uploads/2026/08/cover-150x150.png"},
				"medium":    map[string]any{"source_url": host + "/wp-content/uploads/2026/08/cover-300x169.png"}}},
		}})
		return b
	})
	body := `<p><img src="` + host + `/wp-content/uploads/2026/08/cover-1024x576.png" srcset="` + host +
		`/wp-content/uploads/2026/08/cover-300x169.png 300w, ` + host + `/wp-content/uploads/2026/08/cover-scaled.png 2560w" alt=""></p>` +
		`<p><a href="` + host + `/wp-content/uploads/2026/08/cover-150x150.png">thumb</a> <img src="` + host + `/wp-content/uploads/2026/08/missing.png"></p>`
	e.wp.override("/wp-json/wp/v2/posts", func(b []byte) []byte {
		var list []map[string]any
		_ = json.Unmarshal(b, &list)
		list = list[:1]
		list[0]["content"] = map[string]any{"rendered": body}
		list[0]["featured_media"] = 862
		list[0]["yoast_head_json"] = map[string]any{"title": "T", "canonical": host + "/2026/09/23/developers-garden-kit/",
			"og_image": []any{map[string]any{"url": host + "/wp-content/uploads/2026/08/cover-300x169.png"}},
			"schema":   map[string]any{"@graph": []any{map[string]any{"@type": "ImageObject", "url": host + "/wp-content/uploads/2026/08/cover-scaled.png", "@id": host + "/#logo"}}}}
		out, _ := json.Marshal(list)
		return out
	})
	id := e.restMigration(false, Options{})
	st := e.run(id)
	if st.Status != StatusValidated {
		t.Fatalf("status %s failed=%+v", st.Status, e.failedItems(id))
	}
	if n := e.wp.hitCount("/wp-content/uploads/2026/08/cover-scaled.png"); n != 1 {
		t.Fatalf("original downloaded %d times", n)
	}
	mi := e.item(id, "media", "attachment", "862")
	mediaSvc, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	mm, err := mediaSvc.Get(e.ctx(), mi.LocalID)
	if err != nil || mm.Alt != "غلاف" || mm.Caption != "تعليق" || mm.Title != "Cover" || mm.Filename != "cover-scaled.png" {
		t.Fatalf("media = %+v %v", mm, err)
	}
	post := e.item(id, "content", "post", "1154")
	it := mustContent(t, e, post.LocalID)
	if strings.Contains(it.Body, "cover-1024x576") || strings.Contains(it.Body, "cover-300x169") || strings.Contains(it.Body, "cover-150x150") {
		t.Fatalf("size URLs not rewritten:\n%s", it.Body)
	}
	if strings.Count(it.Body, mm.URL) < 3 {
		t.Fatalf("local media URL %s not used for every variant:\n%s", mm.URL, it.Body)
	}
	if !strings.Contains(it.Body, "missing.png") {
		t.Fatal("unknown upload must stay (reported by validate)")
	}
	meta := seo.FromMap(it.SEO)
	if meta.Canonical != "" || !strings.HasPrefix(meta.OGImage, "/media/") || strings.Contains(string(meta.Schema), "/wp-content/uploads/") ||
		!strings.Contains(string(meta.Schema), mm.URL) || strings.Contains(string(meta.Schema), host) || !strings.Contains(string(meta.Schema), `"@id":"/#logo"`) {
		t.Fatalf("seo not rewritten: %+v schema=%s", meta, meta.Schema)
	}
	if it.FeaturedMediaID != mm.ID {
		t.Fatalf("featured media = %q, want %q", it.FeaturedMediaID, mm.ID)
	}
	rw := e.item(id, "rewrite", "post", "1154")
	if num(rw.Meta["media"]) < 4 || num(rw.Meta["unresolved_media"]) < 1 { // missing.png plus the real post's og_image/schema (not imported here)
		t.Fatalf("rewrite log = %v", rw.Meta)
	}
	rep, _ := e.svc.report(e.ctx(), id)
	if len(rep.MissingMedia) != 1 || rep.MissingMedia[0].URL != safeReference(host+"/wp-content/uploads/2026/08/missing.png") {
		t.Fatalf("missing media = %+v", rep.MissingMedia)
	}
}

// TestInternalLinkRewriteAndRedirectForChangedSlug: same-site links become
// local paths and changed permalinks get enabled 301s.
func TestInternalLinkRewriteAndRedirectForChangedSlug(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("status %s", st.Status)
	}
	home := e.item(id, "content", "page", "1105")
	if home.LocalURL != "/" {
		t.Fatalf("front page local url = %q", home.LocalURL)
	}
	body := mustContent(t, e, home.LocalID).Body
	if strings.Contains(body, e.wp.URL()) {
		t.Fatalf("home still links the WordPress host:\n%.2000s", body)
	}
	if !strings.Contains(body, `href="/pricing"`) || !strings.Contains(body, `href="/platforms/garden-kit`) {
		t.Fatalf("internal links not rewritten:\n%.3000s", body)
	}
	rd, _ := kernel.Get[redirects.Service](e.app.Deps, kernel.ServiceRedirects)
	// Posts move from /YYYY/MM/DD/slug/ to /post/slug.
	r, ok := rd.Lookup(e.ctx(), "/2026/09/23/developers-garden-kit")
	if !ok || r.ToURL != "/post/developers-garden-kit" || r.Status != 301 || !r.Enabled {
		t.Fatalf("post redirect = %+v %v", r, ok)
	}
	// Duplicate page slugs: the second garden-kit got a suffix and a redirect.
	a, b := e.item(id, "content", "page", "1148"), e.item(id, "content", "page", "1108")
	suffixed := a
	if !strings.HasSuffix(a.LocalURL, "-2") {
		suffixed = b
	}
	if !strings.HasSuffix(suffixed.LocalURL, "/garden-kit-2") {
		t.Fatalf("no suffixed page: %s %s", a.LocalURL, b.LocalURL)
	}
	if r, ok := rd.Lookup(e.ctx(), pathOf(suffixed.WPURL)); !ok || r.ToURL != suffixed.LocalURL {
		t.Fatalf("redirect for suffixed page = %+v %v", r, ok)
	}
	// Unchanged permalinks and the front page get none.
	if _, ok := rd.Lookup(e.ctx(), "/pricing"); ok {
		t.Fatal("unchanged /pricing must not redirect")
	}
	if n := e.count(`SELECT count(*) FROM redirects WHERE from_path = '/'`); n != 0 {
		t.Fatal("front page redirect created")
	}
	front := e.app.Deps.Settings.GetString(e.ctx(), theme.SettingFront, "")
	if front != home.LocalID {
		t.Fatalf("site.front_page = %q, want %q", front, home.LocalID)
	}
	// Redirects work through the public site.
	rec := httptest.NewRecorder()
	e.app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/2026/09/23/developers-garden-kit/", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/post/developers-garden-kit" {
		t.Fatalf("public redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// TestYoastHeadToSEO maps the real yoast_head_json of the Example Press home.
func TestYoastHeadToSEO(t *testing.T) {
	raw, err := os.ReadFile("testdata/example-press/wp-json-wp-v2-pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var pages []struct {
		Slug  string         `json:"slug"`
		Yoast map[string]any `json:"yoast_head_json"`
	}
	if err := json.Unmarshal(raw, &pages); err != nil {
		t.Fatal(err)
	}
	var y map[string]any
	for _, p := range pages {
		if p.Slug == "home" {
			y = p.Yoast
		}
	}
	m := yoastToSEO(y)
	if !strings.HasPrefix(m.Title, "EXAMPLE PRESS |") || !strings.HasPrefix(m.Description, "مسودة نافذة CRAFT") || m.Canonical != "https://example.org/" ||
		m.OGTitle == "" || m.OGDescription == "" || m.TwitterCard != "summary_large_image" || m.Robots != "" || len(m.Schema) == 0 {
		t.Fatalf("seo = %+v", m)
	}
	back := seo.FromMap(m.ToMap())
	if back.Title != m.Title || string(back.Schema) == "" {
		t.Fatalf("roundtrip = %+v", back)
	}
	noindex := yoastToSEO(map[string]any{"title": "x", "robots": map[string]any{"index": "noindex", "follow": "follow"},
		"og_image": []any{map[string]any{"url": "https://example.org/og.png"}}})
	if noindex.Robots != "noindex,follow" || noindex.OGImage != "https://example.org/og.png" {
		t.Fatalf("noindex = %+v", noindex)
	}
	if got := yoastToSEO(map[string]any{"robots": map[string]any{"index": "noindex"}}); got.Robots != "noindex,follow" {
		t.Fatalf("noindex default follow = %q", got.Robots)
	}
	if got := yoastToSEO(nil); len(got.ToMap()) != 0 {
		t.Fatalf("empty = %+v", got)
	}

	t.Run("imported page carries SEO", func(t *testing.T) {
		e := newEnv(t)
		id := e.fastExamplePress(false, Options{})
		e.run(id)
		it := mustContent(t, e, e.item(id, "content", "page", "1106").LocalID)
		meta := seo.FromMap(it.SEO)
		if meta.Title == "" || meta.Description == "" || meta.Canonical != "" { // the old permalink is dropped
			t.Fatalf("pricing SEO = %+v", meta)
		}
	})
}

// TestArabicSlugRoundtrip: percent-encoded WordPress slugs become UTF-8
// slugs, permalinks and redirects that work through the public site.
func TestArabicSlugRoundtrip(t *testing.T) {
	e := newEnv(t)
	id := e.fastExamplePress(false, Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("status %s", st.Status)
	}
	it := e.item(id, "content", "post", "70")
	wantSlug := "craft-نهار-حافلة-ذهب-محطة-نزهة-سوباباج"
	if it.WPSlug != wantSlug {
		t.Fatalf("decoded wp slug = %q", it.WPSlug)
	}
	c, err := e.contentSvc().GetBySlug(e.ctx(), "post", wantSlug, "ar")
	if err != nil || c.ID != it.LocalID {
		t.Fatalf("GetBySlug(arabic) = %v %v", c, err)
	}
	if it.LocalURL != "/post/"+wantSlug {
		t.Fatalf("local url = %q", it.LocalURL)
	}
	// Public render of the Arabic permalink (escaped on the wire).
	rec := httptest.NewRecorder()
	e.app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, (&url.URL{Path: it.LocalURL}).EscapedPath(), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "CRAFT") {
		t.Fatalf("public render = %d", rec.Code)
	}
	// The old encoded WordPress URL redirects to the new path.
	old, _ := url.Parse(it.WPURL)
	rec = httptest.NewRecorder()
	e.app.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, old.RequestURI(), nil))
	loc, _ := url.PathUnescape(rec.Header().Get("Location"))
	if rec.Code != http.StatusMovedPermanently || loc != it.LocalURL {
		t.Fatalf("old url %s → %d %q", old.RequestURI(), rec.Code, loc)
	}
	// Arabic term slugs too.
	term := e.item(id, "term", "category", "42")
	if term.LocalURL != "/category/شيبوباه" {
		t.Fatalf("arabic term url = %q", term.LocalURL)
	}
}

// fakeImporter records products (commerce stand-in).
type fakeImporter struct {
	mu   sync.Mutex
	got  []WPProduct
	fail bool
}

func (f *fakeImporter) UpsertFromWP(_ context.Context, p WPProduct) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, p)
	return "prod_" + strconv.Itoa(p.ID), nil
}

// TestWCProductsToCatalogWithMCBBMeta: WooCommerce products reach the
// commerce importer with prices in minor units and the MCBB plan meta.
func TestWCProductsToCatalogWithMCBBMeta(t *testing.T) {
	t.Run("wxr postmeta", func(t *testing.T) {
		e := newEnv(t)
		imp := &fakeImporter{}
		e.app.Deps.Services.Set(ProductImporterService, ProductImporter(imp))
		id := e.wxrMigration(Options{Currency: "AED"})
		if st := e.run(id); st.Status != StatusValidated || st.Counts["product"][ItemDone] != 2 {
			t.Fatalf("status %s %v %+v", st.Status, st.Counts["product"], e.failedItems(id))
		}
		if len(imp.got) != 2 {
			t.Fatalf("importer got %d products", len(imp.got))
		}
		p := imp.got[0]
		if p.ID != 1068 || p.PriceMinor != 9900 || p.Currency != "AED" || p.Slug != "سرير-حليب" ||
			p.Meta["_mcbb_server"] != "garden-kit" || p.Meta["_mcbb_plan"] != "basic" || p.Meta["_mcbb_months"] != "1" ||
			p.Meta["_sku"] != "ep-garden-kit-basic-monthly" || len(p.Categories) != 1 || p.Categories[0] != "ملاح حديقة ورق" ||
			len(p.ImageURLs) != 1 || !strings.HasSuffix(p.ImageURLs[0], "/cover.png") {
			t.Fatalf("product = %+v", p)
		}
		if _, internal := p.Meta["_price"]; internal {
			t.Fatal("WooCommerce internals must not reach the catalog")
		}
		if imp.got[1].PriceMinor != 1199050 {
			t.Fatalf("decimal price → minor = %d", imp.got[1].PriceMinor)
		}
		second := e.item(id, "product", "product", "1103")
		if second.Meta["needs_mapping"] != true || e.item(id, "product", "product", "1068").Meta["needs_mapping"] != false {
			t.Fatalf("needs_mapping flags wrong: %v", second.Meta)
		}
		logs, _ := e.svc.Log(e.ctx(), id, 500)
		found := false
		for _, l := range logs {
			found = found || l.Message == "product_needs_mapping"
		}
		if !found {
			t.Fatal("missing product_needs_mapping log")
		}
	})
	t.Run("rest store api with wc/v3 meta", func(t *testing.T) {
		e := newEnv(t)
		imp := &fakeImporter{}
		e.app.Deps.Services.Set(ProductImporterService, ProductImporter(imp))
		id := e.fastExamplePress(true, Options{})
		if st := e.run(id); st.Counts["product"][ItemDone] != 36 {
			t.Fatalf("products %v", st.Counts["product"])
		}
		byID := map[int]WPProduct{}
		for _, p := range imp.got {
			byID[p.ID] = p
		}
		p := byID[1103]
		if p.PriceMinor != 1199000 || p.Currency != "AED" || !strings.HasPrefix(p.Slug, "نهر-حديقة-طاولة") || p.Name == "" || p.Categories[0] != "ملاح حديقة ورق" {
			t.Fatalf("store product = %+v", p)
		}
		if byID[1068].Meta["_mcbb_plan"] != "basic" {
			t.Fatalf("wc/v3 meta not merged: %+v", byID[1068].Meta)
		}
	})
	t.Run("without commerce, then a re-run after it is installed", func(t *testing.T) {
		e := newEnv(t)
		id := e.wxrMigration(Options{})
		st := e.run(id)
		if st.Counts["product"][ItemSkipped] != 2 || e.item(id, "product", "product", "1068").Error != "needs_commerce_module" {
			t.Fatalf("products = %v", st.Counts["product"])
		}
		imp := &fakeImporter{}
		e.app.Deps.Services.Set(ProductImporterService, ProductImporter(imp))
		st = e.run(id)
		if st.Counts["product"][ItemDone] != 2 || len(imp.got) != 2 {
			t.Fatalf("after installing commerce: %v, importer got %d", st.Counts["product"], len(imp.got))
		}
	})
}

// TestValidateReportCountsAndBrokenLinks runs the WXR sample and checks
// the step-7 report.
func TestValidateReportCountsAndBrokenLinks(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("status %s", st.Status)
	}
	rep, err := e.svc.report(e.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]CountRow{}
	for _, r := range rep.Counts {
		rows[r.Kind] = r
	}
	if r := rows["content"]; r.Source != 3 || r.Imported != 3 || r.Failed != 0 {
		t.Fatalf("content counts = %+v", r)
	}
	if r := rows["media"]; r.Source != 1 || r.Imported != 1 {
		t.Fatalf("media counts = %+v", r)
	}
	if r := rows["term"]; r.Source != 3 || r.Imported != 3 {
		t.Fatalf("term counts = %+v", r)
	}
	if r := rows["user"]; r.Imported != 2 {
		t.Fatalf("user counts = %+v", r)
	}
	if rep.RenderChecked != 3 || len(rep.RenderFailures) != 0 {
		t.Fatalf("render checks = %d %+v", rep.RenderChecked, rep.RenderFailures)
	}
	// The post links /pricing/, which the sample export does not contain.
	if len(rep.BrokenLinks) != 1 || rep.BrokenLinks[0].URL != safeSourceReference("/pricing/") {
		t.Fatalf("broken links = %+v", rep.BrokenLinks)
	}
	if rep.Shortcodes["ep_contact_form"] != 1 || rep.Shortcodes["ep_platform_cards"] != 1 {
		t.Fatalf("shortcodes = %v", rep.Shortcodes)
	}
	if len(rep.SlugChanges) != 0 || len(rep.MissingMedia) != 0 {
		t.Fatalf("slug changes / missing media = %+v %+v", rep.SlugChanges, rep.MissingMedia)
	}
	for _, want := range []string{"# Migration report", "| content | 3 | 3 | 0 | 0 |", safeSourceReference("/pricing/"), "`[ep_contact_form]` ×1"} {
		if !strings.Contains(rep.Markdown, want) {
			t.Fatalf("markdown lacks %q:\n%s", want, rep.Markdown)
		}
	}
	logs, _ := e.svc.Log(e.ctx(), id, 500)
	var currentReport string
	for _, l := range logs {
		if l.Message == "validate_report" {
			currentReport, _ = l.Meta["current_report_path"].(string)
		}
	}
	if currentReport != "/api/v1/migrations/"+id+"/report" {
		t.Fatal("validation log lacks current sanitized report reference")
	}
}

// TestRewriteHostBoundariesAndScope: look-alike hosts are untouched; only
// attribute values and whole text tokens are rewritten.
func TestRewriteHostBoundariesAndScope(t *testing.T) {
	e := newEnv(t)
	mm := &media.Media{ID: "m1", URL: "/media/2026/10/m1.png", Variants: map[string]media.Variant{
		"medium": {URL: "/media/2026/10/m1-medium.png"}}}
	rw := &rewriter{s: e.svc, hosts: map[string]bool{"example.org": true}, links: map[string]string{"/pricing": "/pricing", "/2026/08/31/x": "/post/x"},
		media: map[string]string{"example.org/wp-content/uploads/a.png": "m1"}, byID: map[string]*media.Media{"m1": mm}}
	rw.compileHosts()
	in := `<p><a href="https://example.org.evil.net/x">evil</a> <a href="https://example.organic/forum">community</a>` +
		` <a href="https://example.org/pricing/#plans">p</a> <a href='//example.org/2026/08/31/x/'>post</a>` +
		` <img src="https://www.example.org/wp-content/uploads/a-300x200.png" srcset="https://example.org/wp-content/uploads/a-600x400.png 600w, https://example.org/wp-content/uploads/a.png 1200w">` +
		` <span title="https://example.org/pricing/">t</span>` +
		` <a href="https://twitter.com/intent/tweet?url=https://example.org/post&text=x">tw</a>` +
		` <a href="https://web.archive.org/web/2024/https://example.org/pricing/">wa</a></p>` +
		`<p>Visit https://example.org/pricing/. Not https://example.org.evil.net/y or example.organic or xhttps://example.org/q</p>`
	out, st := rw.rewrite(e.ctx(), in)
	for _, want := range []string{
		`href="https://example.org.evil.net/x"`, `href="https://example.organic/forum"`,
		`href="/pricing#plans"`, `href='/post/x'`,
		`src="/media/2026/10/m1.png"`, `srcset="/media/2026/10/m1-medium.png 600w, /media/2026/10/m1.png 1200w"`,
		`title="https://example.org/pricing/"`,
		`Visit /pricing.`, `https://example.org.evil.net/y`, `example.organic`, `xhttps://example.org/q`,
		`href="https://twitter.com/intent/tweet?url=https://example.org/post&text=x"`,
		`href="https://web.archive.org/web/2024/https://example.org/pricing/"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if st.media != 3 || st.links != 3 {
		t.Errorf("stats = %+v", st)
	}
}

// bumpWXRAttachment rewrites the export so attachment 862 looks modified.
func bumpWXRAttachment(t *testing.T, e *env, id string) {
	t.Helper()
	m, _ := e.svc.getMigration(e.ctx(), id)
	src, _ := e.svc.getSource(e.ctx(), m.SourceID)
	raw, err := os.ReadFile(src.filePath)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("2026-08-31 06:00:00"), []byte("2026-09-30 06:00:00"), 1)
	if err := os.WriteFile(src.filePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(src.filePath, future, future)
}

func redPNG() []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for x := 0; x < 3; x++ {
		img.Set(x, 1, color.RGBA{B: 255, A: 255})
	}
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// TestChangedMediaKeepsReferences: a re-imported file replaces the media
// row only after every owned reference points at the new one; unchanged
// content's featured image keeps resolving; a failed upload changes
// nothing.
func TestChangedMediaKeepsReferences(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("first run %s", st.Status)
	}
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	oldID := e.item(id, "media", "attachment", "862").LocalID
	oldMedia, _ := ms.Get(e.ctx(), oldID)
	post := e.item(id, "content", "post", "70")

	t.Run("failed upload leaves the old media", func(t *testing.T) {
		e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", []byte("not an image at all"))
		bumpWXRAttachment(t, e, id)
		e.run(id)
		if it := e.item(id, "media", "attachment", "862"); it.Status != ItemFailed {
			t.Fatalf("media item = %s %s", it.Status, it.Error)
		}
		if _, err := ms.Get(e.ctx(), oldID); err != nil {
			t.Fatalf("old media gone after a failed upload: %v", err)
		}
		if c := mustContent(t, e, post.LocalID); c.FeaturedMediaID != oldID {
			t.Fatalf("featured image changed: %s", c.FeaturedMediaID)
		}
	})

	t.Run("new bytes replace the media and repoint references", func(t *testing.T) {
		e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", redPNG())
		if err := e.svc.RetryFailed(e.ctx(), id); err != nil {
			t.Fatal(err)
		}
		if st := e.drain(id); st.Status != StatusValidated || st.Failed != 0 {
			t.Fatalf("retry: %s %+v", st.Status, e.failedItems(id))
		}
		newID := e.item(id, "media", "attachment", "862").LocalID
		if newID == oldID {
			t.Fatal("media not replaced")
		}
		c := mustContent(t, e, post.LocalID)
		if c.FeaturedMediaID != newID {
			t.Fatalf("featured image = %s, want %s", c.FeaturedMediaID, newID)
		}
		if _, err := ms.Get(e.ctx(), c.FeaturedMediaID); err != nil {
			t.Fatalf("featured image does not resolve: %v", err)
		}
		if strings.Contains(c.Body, strings.TrimSuffix(oldMedia.URL, ".png")) {
			t.Fatalf("body still references the old media:\n%s", c.Body)
		}
		if _, err := ms.Get(e.ctx(), oldID); err == nil {
			t.Fatal("old media should be deleted once nothing references it")
		}
		if n := e.count(`SELECT count(*) FROM media`); n != 1 {
			t.Fatalf("media rows = %d", n)
		}
	})

	t.Run("foreign references keep the old media", func(t *testing.T) {
		cur := e.item(id, "media", "attachment", "862").LocalID
		if _, err := e.contentSvc().Create(e.ctx(), content.Input{Type: "page", Title: "Local", FeaturedMediaID: cur, Status: "draft"}); err != nil {
			t.Fatal(err)
		}
		e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", tinyPNG()) // different bytes again
		m, _ := e.svc.getMigration(e.ctx(), id)
		src, _ := e.svc.getSource(e.ctx(), m.SourceID)
		b, _ := os.ReadFile(src.filePath)
		b = bytes.Replace(b, []byte("2026-09-30 06:00:00"), []byte("2026-10-01 06:00:00"), 1)
		_ = os.WriteFile(src.filePath, b, 0o600)
		future := time.Now().Add(2 * time.Hour)
		_ = os.Chtimes(src.filePath, future, future)
		e.run(id)
		if _, err := ms.Get(e.ctx(), cur); err != nil {
			t.Fatalf("media referenced by local content was deleted: %v", err)
		}
		logs, _ := e.svc.Log(e.ctx(), id, 1000)
		kept := false
		for _, l := range logs {
			kept = kept || (l.Message == "media_old_kept" && l.Meta["reason"] == "referenced_elsewhere")
		}
		if !kept || e.item(id, "media", "attachment", "862").LocalID == cur {
			t.Fatal("expected a replacement that keeps the referenced old media")
		}
	})
}
