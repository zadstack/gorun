package wpmigrate

import (
	"fmt"
	"github.com/zadstack/gorun/internal/media"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportedMediaExactMapBeforeHostRewrite(t *testing.T) {
	e := newEnv(t)
	a := &media.Media{ID: "a", URL: "/media/2026/10/a.png", Variants: map[string]media.Variant{}}
	b := &media.Media{ID: "b", URL: "/media/2026/10/b.png", Variants: map[string]media.Variant{}}
	rw := &rewriter{s: e.svc, hosts: map[string]bool{"source.test": true}, media: map[string]string{
		mediaKey("https://cdn.test/images/غلاف-scaled.png"): "a",
		mediaKey("https://source.test/assets/غلاف.png"):     "b",
	}, links: map[string]string{"/old-post": "/post/new"}, byID: map[string]*media.Media{"a": a, "b": b}}
	rw.compileHosts()
	in := `<picture><source srcset="https://cdn.test/images/%D8%BA%D9%84%D8%A7%D9%81-300x200.png?cache=1 300w"><img src="https://cdn.test/images/غلاف-scaled.png?token=private" srcset="https://cdn.test/images/غلاف-scaled.png 2x, /assets/غلاف.png 1x"></picture><a href="https://source.test/old-post">post</a><img src="//source.test/assets/غلاف.png">`
	out, st := rw.rewrite(e.ctx(), in)
	for _, want := range []string{`<source srcset="/media/2026/10/a.png 300w"`, `src="/media/2026/10/a.png"`, `srcset="/media/2026/10/a.png 2x, /media/2026/10/b.png 1x"`, `href="/post/new"`, `src="/media/2026/10/b.png"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s: %s", want, out)
		}
	}
	if st.media != 5 {
		t.Errorf("media stats=%d", st.media)
	}
}

func TestWXRInlineMediaHTTPIsolationAndRerun(t *testing.T) {
	e := newEnv(t)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG())
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(redPNG())
	}))
	defer second.Close()
	a, b := first.URL+"/assets/cover.png", second.URL+"/assets/cover.png"
	raw := `<?xml version="1.0"?><rss version="2.0" xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/"><channel><title>Isolated</title><link>https://source.test</link><wp:wxr_version>1.2</wp:wxr_version><wp:base_site_url>https://source.test</wp:base_site_url><wp:base_blog_url>https://source.test</wp:base_blog_url>`
	for i, u := range []string{a, b} {
		raw += fmt.Sprintf(`<item><title>cover%d</title><link>%s</link><wp:post_id>%d</wp:post_id><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status><wp:post_date_gmt>2020-01-01 12:00:00</wp:post_date_gmt><wp:attachment_url>%s</wp:attachment_url></item>`, i, u, i+1, u)
	}
	body := `<picture><source type="image/png" srcset="` + a + `?cache=1 1x, ` + b + ` 2x"><img src="` + a + `"></picture><img src="` + b + `">`
	raw += `<item><title>Arabic عنوان</title><link>https://source.test/original</link><wp:post_id>10</wp:post_id><wp:post_type>post</wp:post_type><wp:post_name>media-fixture</wp:post_name><wp:status>publish</wp:status><wp:post_date_gmt>2020-01-01 12:00:00</wp:post_date_gmt><content:encoded><![CDATA[` + body + `]]></content:encoded><wp:postmeta><wp:meta_key>_thumbnail_id</wp:meta_key><wp:meta_value>1</wp:meta_value></wp:postmeta></item></channel></rss>`
	path := filepath.Join(t.TempDir(), "fixture.xml")
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewWXR(e.ctx(), e.app.Deps, WXRFile{Path: path}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.MediaCount != 2 {
		t.Fatalf("preview: %+v", preview)
	}
	if e.count(`SELECT count(*) FROM media`) != 0 || e.count(`SELECT count(*) FROM content`) != 0 {
		t.Fatal("dry-run mutated content/media")
	}
	res, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	post := e.item(res.MigrationID, "content", "post", "10")
	item, err := e.contentSvc().Get(e.ctx(), post.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	aa, bb := e.item(res.MigrationID, "media", "attachment", "1"), e.item(res.MigrationID, "media", "attachment", "2")
	if aa.LocalID == bb.LocalID || item.FeaturedMediaID != aa.LocalID {
		t.Fatalf("mapping collision or missing featured: %+v %+v", aa, bb)
	}
	if !strings.Contains(item.Body, "<picture>") || !strings.Contains(item.Body, aa.LocalURL) || !strings.Contains(item.Body, bb.LocalURL) || strings.Contains(item.Body, first.URL) {
		t.Fatalf("unmapped body: %s", item.Body)
	}
	for _, u := range []string{aa.LocalURL, bb.LocalURL} {
		resp, err := http.Get(e.srv.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		bytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 200 || len(bytes) == 0 {
			t.Fatalf("media HTTP %s: %d %v", u, resp.StatusCode, err)
		}
	}
	if _, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{}); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT count(*) FROM media`) != 2 || e.count(`SELECT count(*) FROM content`) != 1 {
		t.Fatal("re-import duplicated content/media")
	}
	raw = strings.Replace(raw, "Arabic عنوان", "Updated عنوان", 1)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportWXRFile(e.ctx(), e.app.Deps, path, Options{}); err != nil {
		t.Fatal(err)
	}
	updated, _ := e.contentSvc().Get(e.ctx(), post.LocalID)
	if updated.Title != "Updated عنوان" || !strings.Contains(updated.Body, aa.LocalURL) {
		t.Fatalf("update lost mapping: %+v", updated)
	}
}

func TestRelativeMediaUsesSourceSubdirectory(t *testing.T) {
	e := newEnv(t)
	src, err := e.svc.CreateSource(e.ctx(), SourceInput{Name: "subdir", URL: "https://source.test/blog"})
	if err != nil {
		t.Fatal(err)
	}
	migration, err := e.svc.CreateMigration(e.ctx(), src.ID, Options{}, e.adm.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := e.svc.getMigration(e.ctx(), migration.ID)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := e.svc.newRewriter(e.ctx(), m)
	if err != nil {
		t.Fatal(err)
	}
	mm := &media.Media{ID: "m", URL: "/media/m.png", Variants: map[string]media.Variant{}}
	rw.media[mediaKey("https://source.test/blog/images/a.png")] = "m"
	rw.byID["m"] = mm
	if out, _ := rw.rewrite(e.ctx(), `<img src="images/a.png?cache=1">`); out != `<img src="/media/m.png">` {
		t.Fatalf("relative source path: %s", out)
	}
}
