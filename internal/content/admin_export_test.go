package content_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/zadstack/gorun/internal/content"
)

// The content list offers JSON and CSV exports carrying its current filters; an author's export
// holds the author's own items only, like the list.
func TestAdminContentListExport(t *testing.T) {
	s := loggedIn(t, "editor")
	cs := contentService(t, s.app)
	ctx := context.Background()
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "Draft one", Status: "draft", Locale: "ar"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Create(ctx, content.Input{Type: "post", Title: "Live one", Status: "published", Locale: "en"}); err != nil {
		t.Fatal(err)
	}
	_, page := s.do("GET", "/admin/content/post?status=draft&search=one", nil, nil)
	for _, want := range []string{`data-content-export`, `/admin/content/post/export?format=json&amp;search=one&amp;status=draft`,
		`/admin/content/post/export?format=csv&amp;search=one&amp;status=draft`} {
		if !strings.Contains(page, want) {
			t.Fatalf("list misses %q", want)
		}
	}
	resp, body := s.do("GET", "/admin/content/post/export?format=csv&status=draft", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), `filename=content-post-`) ||
		!strings.Contains(body, "Draft one") || strings.Contains(body, "Live one") {
		t.Fatalf("csv export: %d %v\n%s", resp.StatusCode, resp.Header, body)
	}
	resp, body = s.do("GET", "/admin/content/post/export?format=json&locale=en", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"Live one"`) || strings.Contains(body, "Draft one") {
		t.Fatalf("json export: %d\n%s", resp.StatusCode, body)
	}
	// A bad value comes back to the list with an error, nothing downloads.
	resp, _ = s.do("GET", "/admin/content/post/export?format=xml&status=draft", nil, nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/admin/content/post?") {
		t.Fatalf("bad format: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	author := loggedIn(t, "author")
	acs := contentService(t, author.app)
	if _, err := acs.Create(ctx, content.Input{Type: "post", Title: "Someone else's live", Status: "published", Locale: "ar"}); err != nil {
		t.Fatal(err)
	}
	resp, body = author.do("GET", "/admin/content/post/export?format=csv", nil, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(body, "Someone else") {
		t.Fatalf("author export: %d\n%s", resp.StatusCode, body)
	}
}
