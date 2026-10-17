package wpmigrate

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The wizard's WXR upload stores every upload under a new file name; the
// same site uploaded again (or a newer export of it) must reuse the source,
// so its re-import updates what the first import created.
func TestWXRUploadReusesSourceOfSameSite(t *testing.T) {
	e := newEnv(t)
	raw, err := os.ReadFile("testdata/wxr/sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) string {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first, err := e.svc.createWXRSource(e.ctx(), "export.xml", write("a.xml", raw))
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.createWXRSource(e.ctx(), "export (1).xml", write("b.xml", raw))
	if err != nil || again.ID != first.ID {
		t.Fatalf("same site uploaded again: %v %s vs %s", err, again.ID, first.ID)
	}
	other := bytes.ReplaceAll(raw, []byte("https://example.org"), []byte("https://other.example"))
	third, err := e.svc.createWXRSource(e.ctx(), "export.xml", write("c.xml", other))
	if err != nil || third.ID == first.ID {
		t.Fatalf("another site must get its own source: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM wp_sources WHERE kind = 'wxr'`); n != 2 {
		t.Fatalf("wxr sources = %d, want 2", n)
	}
	src, _ := e.svc.getSource(e.ctx(), first.ID)
	if filepath.Base(src.filePath) != "b.xml" {
		t.Fatalf("a reused source must read the newest upload, got %s", src.filePath)
	}
}

// The site key is the BLOG url (multisite subsites share base_site_url, the
// network root), with the scheme and host case ignored.
func TestWXRSiteKeyMultisiteAndScheme(t *testing.T) {
	e := newEnv(t)
	raw, err := os.ReadFile("testdata/wxr/sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	export := func(name, siteURL, blogURL string) string {
		b := bytes.Replace(raw, []byte("<wp:base_site_url>https://example.org</wp:base_site_url>"), []byte("<wp:base_site_url>"+siteURL+"</wp:base_site_url>"), 1)
		b = bytes.Replace(b, []byte("<wp:base_blog_url>https://example.org</wp:base_blog_url>"), []byte("<wp:base_blog_url>"+blogURL+"</wp:base_blog_url>"), 1)
		b = bytes.Replace(b, []byte("<link>https://example.org</link>"), []byte("<link>"+blogURL+"</link>"), 1)
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, err := e.svc.createWXRSource(e.ctx(), "a.xml", export("a.xml", "https://net.example", "https://net.example/a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.createWXRSource(e.ctx(), "b.xml", export("b.xml", "https://net.example", "https://net.example/b/"))
	if err != nil || b.ID == a.ID {
		t.Fatalf("two subsites of one network must be two sources: %v", err)
	}
	// The same subsite exported over http with a different host case and a
	// trailing slash is the same site.
	a2, err := e.svc.createWXRSource(e.ctx(), "a-http.xml", export("a-http.xml", "http://NET.example", "http://Net.Example/a/"))
	if err != nil || a2.ID != a.ID {
		t.Fatalf("http vs https export of one site must share a source: %v %s vs %s", err, a2.ID, a.ID)
	}
	if n := e.count(`SELECT count(*) FROM wp_sources WHERE kind = 'wxr'`); n != 2 {
		t.Fatalf("wxr sources = %d, want 2", n)
	}
	for in, want := range map[string]string{"http://Net.Example/a/": "https://net.example/a", "https://example.org": "https://example.org", "nope": ""} {
		if got := wxrSiteKey(in); got != want {
			t.Errorf("wxrSiteKey(%q) = %q, want %q", in, got, want)
		}
	}
}
