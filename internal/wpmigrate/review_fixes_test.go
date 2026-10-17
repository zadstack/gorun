package wpmigrate

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/zadstack/gorun/internal/config"
	"github.com/zadstack/gorun/internal/content"
	"github.com/zadstack/gorun/internal/kernel"
	"github.com/zadstack/gorun/internal/media"
)

var attachmentModified = regexp.MustCompile(`(<wp:post_modified_gmt><!\[CDATA\[)[^\]]*(\]\]>)`)

// setAttachmentModified gives the WXR's cover attachment (the first item) a
// new modified date and a newer file mtime, so the next run re-imports it.
func setAttachmentModified(t *testing.T, e *env, id string, when string, bump int) {
	t.Helper()
	m, _ := e.svc.getMigration(e.ctx(), id)
	src, _ := e.svc.getSource(e.ctx(), m.SourceID)
	raw, err := os.ReadFile(src.filePath)
	if err != nil {
		t.Fatal(err)
	}
	done := false
	raw = attachmentModified.ReplaceAllFunc(raw, func(b []byte) []byte {
		if done {
			return b
		}
		done = true
		return attachmentModified.ReplaceAll(b, []byte("${1}"+when+"${2}"))
	})
	if err := os.WriteFile(src.filePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Duration(bump) * time.Hour)
	_ = os.Chtimes(src.filePath, future, future)
}

// A → B → A → B: when bytes come back that an earlier, still-referenced
// revision holds, that row is reused (no upload) and becomes current again.
func TestChangedMediaReusesKeptRevision(t *testing.T) {
	e := newEnv(t)
	const cover = "/wp-content/uploads/2026/08/cover.png"
	bytesA, bytesB := tinyPNG(), redPNG()
	e.wp = newFixtureServer(t)
	e.wp.uploadBytes(cover, bytesA)
	id := e.wxrMigration(Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("A: %s", st.Status)
	}
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	keepAlive := func(mediaID, title string) { // a local page keeps each version alive
		if _, err := e.contentSvc().Create(e.ctx(), content.Input{Type: "page", Title: title, FeaturedMediaID: mediaID, Status: "draft"}); err != nil {
			t.Fatal(err)
		}
	}
	step := func(name string, b []byte, when string, bump int) string {
		e.wp.uploadBytes(cover, b)
		setAttachmentModified(t, e, id, when, bump)
		if st := e.run(id); st.Status != StatusValidated || st.Failed != 0 {
			t.Fatalf("%s: %s %+v", name, st.Status, e.failedItems(id))
		}
		return e.item(id, "media", "attachment", "862").LocalID
	}
	idA := e.item(id, "media", "attachment", "862").LocalID
	plain := func() string { mm, _ := ms.Get(e.ctx(), idA); return mm.SourceURL }()
	keepAlive(idA, "keeps A")

	idB := step("B", bytesB, "2026-09-30 06:00:00", 1)
	keepAlive(idB, "keeps B")
	idA2 := step("A again", bytesA, "2026-10-01 06:00:00", 2)
	if idA2 == idB {
		t.Fatal("A again must not stay on B")
	}
	keepAlive(idA2, "keeps A again") // a newer row than B stays: B must still become current
	uploads := e.count(`SELECT count(*) FROM audit_log WHERE action = 'media.upload'`)

	idB2 := step("B again", bytesB, "2026-10-02 06:00:00", 3)
	if idB2 != idB {
		t.Fatalf("B again must reuse the kept B revision %s, got %s", idB, idB2)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'media.upload'`); n != uploads {
		t.Fatalf("reuse must not upload: media.upload audit rows %d → %d", uploads, n)
	}
	if _, err := ms.Get(e.ctx(), idA2); err != nil {
		t.Fatalf("the referenced A revision must be kept: %v", err)
	}
	if cur, err := ms.FindBySourceURL(e.ctx(), plain); err != nil || cur.ID != idB {
		t.Fatalf("the reused revision must be current for the plain URL: %v %v", cur, err)
	}
}

// rewriteSchemaIDs: every site URL string in the JSON-LD (at any depth,
// under any key) becomes an absolute local URL when the base URL is set;
// other values stay; the input is not modified.
func TestRewriteSchemaIDsAbsoluteBase(t *testing.T) {
	rw := &rewriter{s: &service{cfg: config.Config{BaseURL: "https://new.example/"}}, hosts: map[string]bool{"example.org": true},
		media: map[string]string{}, links: map[string]string{"/about": "/about-us", "/2026/09/23/hello": "/post/hello"}, byID: map[string]*media.Media{}}
	rw.compileHosts()
	in := map[string]any{"@graph": []any{
		map[string]any{"@type": "WebSite", "@id": "https://example.org/#website", "url": "https://example.org/about",
			"potentialAction": []any{map[string]any{"@type": "SearchAction",
				"target": map[string]any{"@type": "EntryPoint", "urlTemplate": "https://example.org/?s={search_term_string}"}}}},
		map[string]any{"@type": "Organization", "url": "https://elsewhere.example/x", "name": "https://example.org/not-an-id",
			"logo": map[string]any{"contentUrl": "https://www.example.org/logo-page/"}, "description": "see https://example.org/about"},
		map[string]any{"@type": "BreadcrumbList", "itemListElement": []any{
			map[string]any{"@type": "ListItem", "position": 1.0, "item": "https://example.org/2026/09/23/hello/"}}},
		map[string]any{"@type": "Article", "target": []any{"https://example.org/2026/09/23/hello/#respond"}},
	}}
	var st rewriteStats
	out := rw.rewriteSchemaIDs(context.Background(), in, &st).(map[string]any)
	g := out["@graph"].([]any)
	site, org, crumbs, art := g[0].(map[string]any), g[1].(map[string]any), g[2].(map[string]any), g[3].(map[string]any)
	if site["@id"] != "https://new.example/#website" || site["url"] != "https://new.example/about-us" {
		t.Fatalf("site ids = %v", site)
	}
	tpl := site["potentialAction"].([]any)[0].(map[string]any)["target"].(map[string]any)["urlTemplate"]
	if tpl != "https://new.example/search?q={search_term_string}" {
		t.Fatalf("search urlTemplate = %v", tpl)
	}
	if org["url"] != "https://elsewhere.example/x" || org["description"] != "see https://example.org/about" {
		t.Fatalf("non-site values / embedded text changed: %v", org)
	}
	if org["name"] != "https://new.example/not-an-id" || org["logo"].(map[string]any)["contentUrl"] != "https://new.example/logo-page/" {
		t.Fatalf("site strings under any key must be rewritten: %v", org)
	}
	if item := crumbs["itemListElement"].([]any)[0].(map[string]any)["item"]; item != "https://new.example/post/hello" {
		t.Fatalf("breadcrumb item = %v", item)
	}
	if tg := art["target"].([]any)[0]; tg != "https://new.example/post/hello#respond" {
		t.Fatalf("target[] = %v", tg)
	}
	b, _ := json.Marshal(out)
	if n := strings.Count(string(b), "example.org/"); n != 1 { // only the embedded description text
		t.Fatalf("source host left in schema: %s", b)
	}
	if in["@graph"].([]any)[0].(map[string]any)["@id"] != "https://example.org/#website" {
		t.Fatal("rewriteSchemaIDs must not modify its input")
	}
}

// rewriteSEO maps the Yoast canonical to the local permalink.
func TestRewriteSEOCanonical(t *testing.T) {
	rw := &rewriter{s: &service{cfg: config.Config{BaseURL: "https://new.example"}}, hosts: map[string]bool{"example.org": true},
		media: map[string]string{}, links: map[string]string{"/2026/09/23/hello": "/post/hello"}, byID: map[string]*media.Media{}}
	rw.compileHosts()
	var st rewriteStats
	out, changed := rw.rewriteSEO(context.Background(), map[string]any{"canonical": "https://example.org/2026/09/23/hello/", "title": "T"}, &st)
	if !changed || out["canonical"] != "https://new.example/post/hello" || out["title"] != "T" {
		t.Fatalf("canonical = %v changed=%v", out, changed)
	}
}

// An image field on migrated (owned) content that holds the replaced media
// id is re-pointed to the new media, and the old media can go.
func TestReplacedMediaRepointsImageFieldOnMigratedContent(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("first run %s", st.Status)
	}
	cs := e.contentSvc()
	oldID := e.item(id, "media", "attachment", "862").LocalID
	typ, err := cs.GetType(e.ctx(), "post")
	if err != nil {
		t.Fatal(err)
	}
	typ.Fields = append(typ.Fields, content.Field{Key: "hero", Type: "image"})
	if err := cs.SaveType(e.ctx(), *typ); err != nil {
		t.Fatal(err)
	}
	post := e.item(id, "content", "post", "70")
	it, _ := cs.Get(e.ctx(), post.LocalID)
	in := content.InputFromItem(it)
	if in.Fields == nil {
		in.Fields = map[string]any{}
	}
	in.Fields["hero"] = oldID
	if _, err := cs.Update(e.ctx(), post.LocalID, in); err != nil {
		t.Fatal(err)
	}

	e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", redPNG())
	bumpWXRAttachment(t, e, id)
	if st := e.run(id); st.Status != StatusValidated || st.Failed != 0 {
		t.Fatalf("re-run: %s %+v", st.Status, e.failedItems(id))
	}
	newID := e.item(id, "media", "attachment", "862").LocalID
	got, _ := cs.Get(e.ctx(), post.LocalID)
	if newID == oldID || got.Fields["hero"] != newID {
		t.Fatalf("hero field = %v, want the new media %s (old %s)", got.Fields["hero"], newID, oldID)
	}
	ms, _ := kernel.Get[media.Service](e.app.Deps, kernel.ServiceMedia)
	if _, err := ms.Get(e.ctx(), oldID); err == nil {
		t.Fatal("old media should be deleted once the field moved off it")
	}
}

// replacingImporter is a fake importer that also replaces product media.
type replacingImporter struct {
	fakeImporter
	replaced [][2]string
}

func (r *replacingImporter) ReplaceMediaRef(_ context.Context, oldID, newID string) error {
	r.replaced = append(r.replaced, [2]string{oldID, newID})
	return nil
}

// With a media replacer available, product images are re-pointed even when
// this source has not imported products itself (hand-made products may use
// the imported file).
func TestReplaceMediaRefCalledWithoutImportedProducts(t *testing.T) {
	e := newEnv(t)
	id := e.wxrMigration(Options{})
	if st := e.run(id); st.Status != StatusValidated { // no importer yet: products skipped
		t.Fatalf("first run %s", st.Status)
	}
	m, _ := e.svc.getMigration(e.ctx(), id)
	if e.svc.hasDoneProducts(e.ctx(), m) {
		t.Fatal("precondition: no products imported")
	}
	oldID := e.item(id, "media", "attachment", "862").LocalID
	imp := &replacingImporter{}
	e.app.Deps.Services.Set(ProductImporterService, ProductImporter(imp))
	e.wp.uploadBytes("/wp-content/uploads/2026/08/cover.png", redPNG())
	bumpWXRAttachment(t, e, id)
	if st := e.run(id); st.Status != StatusValidated || st.Failed != 0 {
		t.Fatalf("re-run: %s %+v", st.Status, e.failedItems(id))
	}
	newID := e.item(id, "media", "attachment", "862").LocalID
	if len(imp.replaced) != 1 || imp.replaced[0] != [2]string{oldID, newID} {
		t.Fatalf("ReplaceMediaRef calls = %v, want [%s → %s]", imp.replaced, oldID, newID)
	}
}

// BUG-04: after a full import no SEO map (canonical, schema JSON-LD) still
// points at the WordPress host.
func TestImportedSEOHasNoSourceHost(t *testing.T) {
	e := newEnv(t)
	e.wp = newFixtureServer(t)
	id := e.restMigration(false, Options{})
	if st := e.run(id); st.Status != StatusValidated {
		t.Fatalf("status = %s failed=%v", st.Status, e.failedItems(id))
	}
	host := strings.TrimPrefix(e.wp.URL(), "http://")
	rows, err := e.app.Deps.DB.Query(`SELECT slug, seo_json FROM content WHERE seo_json LIKE ?`, "%"+host+"%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var slug, js string
		_ = rows.Scan(&slug, &js)
		i := strings.Index(js, host)
		t.Errorf("%s keeps the source host: …%s…", slug, js[max(0, i-120):min(len(js), i+80)])
	}
	if n := e.count(`SELECT count(*) FROM content WHERE seo_json LIKE '%search?q={search_term_string}%'`); n == 0 {
		var js string
		_ = e.app.Deps.DB.QueryRow(`SELECT seo_json FROM content WHERE seo_json LIKE '%SearchAction%' LIMIT 1`).Scan(&js)
		t.Errorf("the Yoast SearchAction must target /search?q=: %s", js)
	}
}
