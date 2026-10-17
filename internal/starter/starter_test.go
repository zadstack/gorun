package starter

import (
	"encoding/xml"
	"regexp"
	"strings"
	"testing"
)

type wxrItem struct {
	Title    string `xml:"title"`
	Body     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Slug     string `xml:"http://wordpress.org/export/1.2/ post_name"`
	Type     string `xml:"http://wordpress.org/export/1.2/ post_type"`
	ParentID string `xml:"http://wordpress.org/export/1.2/ post_parent"`
}

var href = regexp.MustCompile(`href="([^"]*)"`)

// Every link in the starter pages points to a route the starter itself
// creates: the home page, the post archive (/blog) or a top-level page. A
// fresh site has no shop or contact page, so the About page cannot link there.
func TestStarterLinksOnlyToRoutesItCreates(t *testing.T) {
	var doc struct {
		Items []wxrItem `xml:"channel>item"`
	}
	if err := xml.Unmarshal(WXR, &doc); err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{"/": true, "/blog": true}
	for _, it := range doc.Items {
		if it.Type == "page" && it.ParentID == "0" {
			routes["/"+it.Slug] = true
		}
	}
	about := false
	for _, it := range doc.Items {
		if strings.Contains(it.Body, "gp-s-hero") {
			about = true
		}
		for _, m := range href.FindAllStringSubmatch(it.Body, -1) {
			if !routes[m[1]] {
				t.Errorf("%s links to %q, which the starter does not create", it.Title, m[1])
			}
		}
	}
	if !about {
		t.Fatal("the starter About page (section library) is missing")
	}
}
