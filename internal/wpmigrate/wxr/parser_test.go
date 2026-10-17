package wxr

import (
	"os"
	"strings"
	"testing"
)

func TestWXRParsePostmetaAndAttachments(t *testing.T) {
	fh, err := os.Open("../testdata/wxr/sample.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	f, err := Parse(fh)
	if err != nil {
		t.Fatal(err)
	}
	if f.BaseSiteURL != "https://example.org" || f.Version != "1.2" || f.Language != "ar" || !strings.Contains(f.Generator, "v=7.1") {
		t.Fatalf("channel = %+v", f)
	}
	if len(f.Authors) != 2 || f.Authors[1].Email != "editor@example.org" || f.Authors[1].DisplayName != "كاتب تجريبي" {
		t.Fatalf("authors = %+v", f.Authors)
	}
	tax := map[string]int{}
	for _, tm := range f.Terms {
		tax[tm.Taxonomy]++
	}
	if tax["category"] != 2 || tax["post_tag"] != 1 || tax["product_cat"] != 1 || tax["nav_menu"] != 1 {
		t.Fatalf("terms = %v", tax)
	}
	if f.Terms[1].Parent != "from-examplepress" {
		t.Fatalf("category parent = %q", f.Terms[1].Parent)
	}
	att := f.ItemsOfType("attachment")
	if len(att) != 1 || att[0].AttachmentURL != "https://example.org/wp-content/uploads/2026/08/cover.png" || att[0].Meta["_wp_attachment_image_alt"] != "كوباباو" || att[0].Excerpt != "يتوباب راديباي" {
		t.Fatalf("attachment = %+v", att)
	}
	posts := f.ItemsOfType("post")
	if len(posts) != 1 {
		t.Fatalf("posts = %d", len(posts))
	}
	p := posts[0]
	if !strings.Contains(p.Content, "<!-- wp:paragraph -->") || p.Excerpt != "صسيباق نقطة CRAFT" || p.MetaInt("_thumbnail_id") != 862 {
		t.Fatalf("post = %+v", p)
	}
	if got := p.TermsIn("category"); len(got) != 1 || got[0] != "from-examplepress" {
		t.Fatalf("post categories = %v", got)
	}
	if p.Name != "craft-%d9%86%d9%87%d8%a7%d8%b1" || p.ModifiedGMT != "2026-09-01 08:00:00" {
		t.Fatalf("post name/modified = %q %q", p.Name, p.ModifiedGMT)
	}
	prods := f.ItemsOfType("product")
	if len(prods) != 2 || prods[0].Meta["_mcbb_server"] != "garden-kit" || prods[0].Meta["_mcbb_plan"] != "basic" || prods[0].Meta["_mcbb_months"] != "1" || prods[0].Meta["_price"] != "99" {
		t.Fatalf("product meta = %+v", prods)
	}
	menu := f.ItemsOfType("nav_menu_item")
	if len(menu) != 2 || menu[0].Meta["_menu_item_object_id"] != "1107" || menu[1].TermsIn("nav_menu")[0] != "primary" || menu[1].Meta["_menu_item_url"] != "https://example.net/examplepress" {
		t.Fatalf("menu items = %+v", menu)
	}
	pages := f.ItemsOfType("page")
	if len(pages) != 2 || pages[1].Parent != 1107 || pages[0].Meta["_wp_page_template"] != "template-platforms.php" {
		t.Fatalf("pages = %+v", pages)
	}
}

func TestWXRRejectsNonExport(t *testing.T) {
	if _, err := Parse(strings.NewReader(`<rss><channel><title>x</title></channel></rss>`)); err == nil {
		t.Fatal("expected error for plain RSS")
	}
	if _, err := Parse(strings.NewReader(`not xml`)); err == nil {
		t.Fatal("expected error for garbage")
	}
}
